// Package ingest fetches watched job boards and stores the postings that match.
//
// A scheduled Sync enqueues one "watch.fetch" job per enabled watch per day, once the
// configured hour has passed (a dedupe key makes the hourly re-runs harmless). Each job
// fetches the watch's board, keeps the postings that pass its keyword and location
// filters, and in one transaction upserts them and marks the watch's postings that
// weren't returned this time as removed.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/arjunshastry16/jobtrack/internal/boards"
	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/match"
	"github.com/arjunshastry16/jobtrack/internal/pace"
	"github.com/arjunshastry16/jobtrack/internal/scheduler"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

// FetchKind is the job kind for fetching one watch's board.
const FetchKind = "watch.fetch"

const (
	// A polite Workday fetch can take several minutes (pages 5s apart, waits after a 429),
	// and may queue behind another fetch from the same board, so these jobs get a long lease.
	fetchLease = 20 * time.Minute
	// Fewer tries than the default: tomorrow's sync tries again anyway.
	fetchMaxAttempts = 3
	// boardGap spaces out fetches from the same board; together with the gate allowing
	// one fetch per board at a time, that's the per-host rate limit.
	boardGap = 2 * time.Second

	// Limits on what's stored, matching the postings table's CHECK constraints.
	maxExternalIDLen = 500
	maxTextLen       = 500 // title and location
	maxURLLen        = 2000
	maxErrorLen      = 1000 // watches.last_error
)

// Config controls when boards are fetched.
type Config struct {
	SyncHour int            // fetch once a day, from this local hour (0-23)
	Location *time.Location // nil means time.Local (set TZ in the container)
}

// Service syncs watches.
type Service struct {
	store  store.DB
	queue  *jobs.Queue
	boards map[string]boards.Board
	gates  *pace.Gates // one per board name
	cfg    Config
	logger *slog.Logger
	now    func() time.Time // replaced in tests
}

// NewService returns an ingest Service using the given board clients (see boards.All).
func NewService(st store.DB, q *jobs.Queue, bs map[string]boards.Board, logger *slog.Logger, cfg Config) *Service {
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	return &Service{
		store: st, queue: q, boards: bs, gates: pace.NewGates(boardGap),
		cfg: cfg, logger: logger, now: time.Now,
	}
}

// Register adds the fetch handler to a worker.
func (s *Service) Register(w *jobs.Worker) {
	w.Handle(FetchKind, s.HandleFetch)
}

// SyncTask is the scheduled task that runs Sync.
func (s *Service) SyncTask(every time.Duration) scheduler.Task {
	return scheduler.Task{Name: "ingest.sync", Every: every, Run: s.Sync}
}

type fetchPayload struct {
	WatchID int64 `json:"watch_id"`
}

// Sync enqueues a fetch job for every enabled watch, at most one per watch per local
// day, and only from the configured hour on. A watch created later in the day is
// picked up by the next run.
func (s *Service) Sync(ctx context.Context) error {
	now := s.now().In(s.cfg.Location)
	if now.Hour() < s.cfg.SyncHour {
		return nil
	}
	ids, err := s.store.ListEnabledWatchIDs(ctx)
	if err != nil {
		return fmt.Errorf("ingest: list watches: %w", err)
	}
	day := now.Format(time.DateOnly)
	enqueued := 0
	for _, id := range ids {
		_, created, err := s.queue.Enqueue(ctx, FetchKind, fetchPayload{WatchID: id},
			jobs.DedupeKey(fmt.Sprintf("watch:%d:%s", id, day)),
			jobs.Lease(fetchLease), jobs.MaxAttempts(fetchMaxAttempts))
		if err != nil {
			return fmt.Errorf("ingest: enqueue fetch for watch %d: %w", id, err)
		}
		if created {
			enqueued++
		}
	}
	if enqueued > 0 {
		s.logger.InfoContext(ctx, "watch fetches enqueued", "count", enqueued)
	}
	return nil
}

// HandleFetch fetches one watch's board and stores the matching postings. It is
// idempotent: running it twice leaves the same postings.
func (s *Service) HandleFetch(ctx context.Context, j jobs.Job) error {
	var p fetchPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return jobs.Permanent(fmt.Errorf("bad fetch payload %s: %w", j.Payload, err))
	}
	w, err := s.store.GetWatch(ctx, p.WatchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted since the sync: nothing to do
	}
	if err != nil {
		return err
	}
	if !w.Enabled {
		return nil // paused since the sync
	}
	board, ok := s.boards[w.Board]
	if !ok {
		return jobs.Permanent(fmt.Errorf("watch %d: no client for board %q", w.ID, w.Board))
	}

	var found []boards.Posting
	start := time.Now()
	err = s.gates.For(w.Board).Do(ctx, func() error {
		var ferr error
		found, ferr = board.Fetch(ctx, w.BoardToken)
		return ferr
	})
	if err != nil {
		// Keep the error on the watch where the user can see it, then let the job retry
		// (or give up now, if retrying can't help). A fetch cut short by shutdown isn't
		// the board's fault, so it isn't recorded.
		if ctx.Err() == nil {
			s.recordFailure(ctx, w.ID, err)
		}
		if errors.Is(err, boards.ErrBoardNotFound) {
			return jobs.Permanent(fmt.Errorf("watch %d: %w", w.ID, err))
		}
		return fmt.Errorf("watch %d: %w", w.ID, err)
	}

	filter := match.New(w.Keywords, w.Locations)
	var rows []store.UpsertPostingParams
	skipped := 0
	for _, fp := range found {
		if !filter.Matches(fp.Title, fp.Location) {
			continue
		}
		row, ok := toRow(w.ID, fp)
		if !ok {
			skipped++
			continue
		}
		rows = append(rows, row)
	}

	var added, removed int64
	err = s.store.InTx(ctx, func(q store.Querier) error {
		for _, r := range rows {
			res, err := q.UpsertPosting(ctx, r)
			if err != nil {
				return err
			}
			if res.Inserted {
				added++
			}
		}
		var err error
		if removed, err = q.MarkPostingsRemoved(ctx, w.ID); err != nil {
			return err
		}
		return q.RecordWatchCheck(ctx, store.RecordWatchCheckParams{ID: w.ID})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return nil // the watch was deleted while we were fetching
	}
	if err != nil {
		return fmt.Errorf("watch %d: store postings: %w", w.ID, err)
	}
	s.logger.InfoContext(ctx, "watch fetched", "watch_id", w.ID, "board", w.Board,
		"found", len(found), "matched", len(rows), "new", added, "removed", removed,
		"skipped", skipped, "took", time.Since(start).Round(time.Millisecond).String())
	return nil
}

// recordFailure stores a fetch error on the watch. Failing to do so is only logged: the
// fetch error itself is what the job reports.
func (s *Service) recordFailure(ctx context.Context, watchID int64, cause error) {
	msg := cause.Error()
	if len(msg) > maxErrorLen {
		msg = strings.ToValidUTF8(msg[:maxErrorLen], "") + "…"
	}
	if err := s.store.RecordWatchCheck(ctx, store.RecordWatchCheckParams{ID: watchID, LastError: &msg}); err != nil {
		s.logger.WarnContext(ctx, "could not record watch error", "watch_id", watchID, "err", err)
	}
}

// toRow turns a fetched posting into a row, or reports false if it can't be stored
// safely. Over-long text is cut rather than dropping the posting; a link that isn't a
// plain http(s) URL drops it, since the inbox will render it as a link.
func toRow(watchID int64, p boards.Posting) (store.UpsertPostingParams, bool) {
	id := strings.TrimSpace(p.ExternalID)
	title := truncate(strings.TrimSpace(p.Title), maxTextLen)
	if id == "" || utf8.RuneCountInString(id) > maxExternalIDLen || title == "" || !safeURL(p.URL) {
		return store.UpsertPostingParams{}, false
	}
	row := store.UpsertPostingParams{
		WatchID: watchID, ExternalID: id, Title: title,
		Location: truncate(strings.TrimSpace(p.Location), maxTextLen), Url: p.URL,
	}
	if !p.PublishedAt.IsZero() {
		t := p.PublishedAt
		row.PublishedAt = &t
	}
	return row, true
}

// safeURL accepts plain http(s) links only. The prefix check matches the database's
// CHECK constraint exactly (url.Parse alone would also accept "HTTPS://").
func safeURL(raw string) bool {
	if len(raw) > maxURLLen || (!strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://")) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil
}

// truncate cuts s to at most n characters (runes, as Postgres's length() counts them).
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
