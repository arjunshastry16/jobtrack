package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arjunshastry16/jobtrack/internal/boards"
	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/pace"
	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

// fakeBoard returns whatever the test sets.
type fakeBoard struct {
	mu       sync.Mutex
	postings []boards.Posting
	err      error
	onFetch  func() // runs during Fetch, if set
	calls    atomic.Int32
}

func (f *fakeBoard) Name() string { return "greenhouse" }

func (f *fakeBoard) Fetch(context.Context, string) ([]boards.Posting, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onFetch != nil {
		f.onFetch()
	}
	return f.postings, f.err
}

func (f *fakeBoard) set(err error, ps ...boards.Posting) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postings, f.err = ps, err
}

type fixture struct {
	pool  *pgxpool.Pool
	st    *store.Store
	board *fakeBoard
	svc   *Service
	n     atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	st := store.NewStore(pool)
	board := &fakeBoard{}
	svc := NewService(st, jobs.New(st), map[string]boards.Board{"greenhouse": board},
		slog.New(slog.DiscardHandler), Config{SyncHour: 6, Location: time.UTC})
	svc.gates = pace.NewGates(0)
	return &fixture{pool: pool, st: st, board: board, svc: svc}
}

func (f *fixture) watch(t *testing.T, keywords, locations []string, enabled bool) int64 {
	t.Helper()
	ctx := t.Context()
	co, err := f.st.CreateCompany(ctx, store.CreateCompanyParams{Name: fmt.Sprintf("Acme %d", f.n.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.st.CreateWatch(ctx, store.CreateWatchParams{
		CompanyID: co.ID, Board: "greenhouse", BoardToken: fmt.Sprintf("acme%d", f.n.Load()),
		Keywords: append([]string{}, keywords...), Locations: append([]string{}, locations...), // never NULL
		Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func (f *fixture) fetch(t *testing.T, watchID int64) error {
	t.Helper()
	payload, _ := json.Marshal(fetchPayload{WatchID: watchID})
	return f.svc.HandleFetch(t.Context(), jobs.Job{Kind: FetchKind, Payload: payload})
}

type storedPosting struct {
	id             int64
	title          string
	removed        bool
	firstSeenAt    time.Time
	publishedIsSet bool
}

// stored returns the watch's postings by external id.
func (f *fixture) stored(t *testing.T, watchID int64) map[string]storedPosting {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		`SELECT external_id, id, title, removed_at IS NOT NULL, first_seen_at, published_at IS NOT NULL
		 FROM postings WHERE watch_id = $1`, watchID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]storedPosting{}
	for rows.Next() {
		var ext string
		var p storedPosting
		if err := rows.Scan(&ext, &p.id, &p.title, &p.removed, &p.firstSeenAt, &p.publishedIsSet); err != nil {
			t.Fatal(err)
		}
		out[ext] = p
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func posting(id, title, location string) boards.Posting {
	return boards.Posting{ExternalID: id, Title: title, Location: location, URL: "https://jobs.example/" + id}
}

func TestFetchStoresOnlyMatchingPostings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	w := f.watch(t, []string{"backend"}, []string{"india"}, true)

	unsafe := posting("g5", "Backend Developer", "India")
	unsafe.URL = "javascript:alert(1)"
	shouty := posting("g6", "Backend Developer", "India")
	shouty.URL = "HTTPS://jobs.example/g6"
	dated := posting("g1", "Backend Engineer", "Bengaluru, India")
	dated.PublishedAt = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f.board.set(nil,
		dated,
		posting("g2", "Backend Engineer", "Austin, TX"), // wrong place
		posting("g3", "Sales Lead", "Pune, India"),      // wrong role
		posting("g4", "Backend Lead", "2 Locations"),    // place unknown: kept
		unsafe, // not an http(s) link
		shouty, // not exactly http(s)://
		posting("g7", strings.Repeat("é", 600)+" backend", "India"), // too long: cut
	)
	if err := f.fetch(t, w); err != nil {
		t.Fatal(err)
	}

	got := f.stored(t, w)
	if len(got) != 3 {
		t.Fatalf("stored %v, want g1, g4 and g7", got)
	}
	for _, id := range []string{"g1", "g4", "g7"} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s not stored", id)
		}
	}
	if n := utf8.RuneCountInString(got["g7"].title); n != maxTextLen {
		t.Errorf("long title stored with %d characters, want %d", n, maxTextLen)
	}
	if !got["g1"].publishedIsSet || got["g4"].publishedIsSet {
		t.Errorf("published_at: g1 set %v (want true), g4 set %v (want false)", got["g1"].publishedIsSet, got["g4"].publishedIsSet)
	}

	wt, err := f.st.GetWatch(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	if wt.LastCheckedAt == nil || wt.LastError != nil {
		t.Errorf("watch after a good fetch: checked %v, error %v", wt.LastCheckedAt, wt.LastError)
	}
}

func TestRefetchUpdatesAndMarksRemoved(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	w := f.watch(t, nil, nil, true)

	f.board.set(nil, posting("a", "Engineer", ""), posting("b", "Designer", ""))
	if err := f.fetch(t, w); err != nil {
		t.Fatal(err)
	}
	first := f.stored(t, w)

	// a is retitled, b is gone, c is new.
	f.board.set(nil, posting("a", "Senior Engineer", ""), posting("c", "Manager", ""))
	if err := f.fetch(t, w); err != nil {
		t.Fatal(err)
	}
	second := f.stored(t, w)
	if len(second) != 3 {
		t.Fatalf("stored %v, want a, b and c (no duplicates)", second)
	}
	a := second["a"]
	if a.id != first["a"].id || !a.firstSeenAt.Equal(first["a"].firstSeenAt) || a.title != "Senior Engineer" || a.removed {
		t.Errorf("a = %+v; want the same row, retitled (first was %+v)", a, first["a"])
	}
	if !second["b"].removed || second["c"].removed {
		t.Errorf("removed: b %v (want true), c %v (want false)", second["b"].removed, second["c"].removed)
	}

	// b comes back.
	f.board.set(nil, posting("a", "Senior Engineer", ""), posting("b", "Designer", ""), posting("c", "Manager", ""))
	if err := f.fetch(t, w); err != nil {
		t.Fatal(err)
	}
	if third := f.stored(t, w); third["b"].removed || third["b"].id != first["b"].id {
		t.Errorf("b after coming back = %+v", third["b"])
	}

	// Postings belong to their watch: another watch's fetch doesn't mark them removed.
	other := f.watch(t, nil, nil, true)
	f.board.set(nil)
	if err := f.fetch(t, other); err != nil {
		t.Fatal(err)
	}
	for id, p := range f.stored(t, w) {
		if p.removed {
			t.Errorf("%s marked removed by another watch's fetch", id)
		}
	}
}

func TestFetchErrorsAreRecordedOnTheWatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	w := f.watch(t, nil, nil, true)
	f.board.set(nil, posting("a", "Engineer", ""))
	if err := f.fetch(t, w); err != nil {
		t.Fatal(err)
	}

	lastError := func() *string {
		wt, err := f.st.GetWatch(t.Context(), w)
		if err != nil {
			t.Fatal(err)
		}
		return wt.LastError
	}

	// A board outage: retried, error shown on the watch, postings untouched.
	f.board.set(&boards.HTTPError{Board: "greenhouse", Code: 503})
	err := f.fetch(t, w)
	if err == nil || jobs.IsPermanent(err) {
		t.Fatalf("503: err = %v, want a retryable error", err)
	}
	if e := lastError(); e == nil || !strings.Contains(*e, "503") {
		t.Errorf("last_error = %v, want the 503", e)
	}
	if p := f.stored(t, w)["a"]; p.removed {
		t.Error("a failed fetch marked postings removed")
	}

	// A misconfigured watch: no point retrying.
	f.board.set(boards.ErrBoardNotFound)
	if err := f.fetch(t, w); !jobs.IsPermanent(err) {
		t.Errorf("not found: err = %v, want permanent", err)
	}

	// Fixed: the error is cleared.
	f.board.set(nil, posting("a", "Engineer", ""))
	if err := f.fetch(t, w); err != nil {
		t.Fatal(err)
	}
	if e := lastError(); e != nil {
		t.Errorf("last_error after a good fetch = %q", *e)
	}
}

func TestFetchSkipsPausedAndDeletedWatches(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	paused := f.watch(t, nil, nil, false)
	if err := f.fetch(t, paused); err != nil {
		t.Errorf("paused watch: %v", err)
	}
	if err := f.fetch(t, 999999); err != nil {
		t.Errorf("deleted watch: %v", err)
	}
	if n := f.board.calls.Load(); n != 0 {
		t.Errorf("board fetched %d times, want 0", n)
	}

	// Deleted while its board was being fetched: nothing to store, and not an error.
	w := f.watch(t, nil, nil, true)
	f.board.set(nil, posting("a", "Engineer", ""))
	f.board.onFetch = func() {
		if _, err := f.st.DeleteWatch(context.Background(), w); err != nil {
			t.Error(err)
		}
	}
	if err := f.fetch(t, w); err != nil {
		t.Errorf("watch deleted during fetch: %v", err)
	}
}

func TestFetchBadPayloadIsPermanent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	err := f.svc.HandleFetch(t.Context(), jobs.Job{Kind: FetchKind, Payload: json.RawMessage(`"nope"`)})
	if !jobs.IsPermanent(err) {
		t.Errorf("err = %v, want permanent", err)
	}
}

func TestSyncEnqueuesOneFetchPerWatchPerDay(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.watch(t, nil, nil, true)
	b := f.watch(t, nil, nil, true)
	f.watch(t, nil, nil, false) // paused: not fetched

	queued := func() map[int64]int {
		rows, err := f.pool.Query(t.Context(),
			"SELECT payload, lease_seconds, max_attempts FROM jobs WHERE kind = $1", FetchKind)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[int64]int{}
		for rows.Next() {
			var raw []byte
			var lease *int32
			var maxAttempts int32
			if err := rows.Scan(&raw, &lease, &maxAttempts); err != nil {
				t.Fatal(err)
			}
			var p fetchPayload
			_ = json.Unmarshal(raw, &p)
			out[p.WatchID]++
			if lease == nil || time.Duration(*lease)*time.Second != fetchLease || maxAttempts != fetchMaxAttempts {
				t.Errorf("job for watch %d: lease %v s, max attempts %d", p.WatchID, lease, maxAttempts)
			}
		}
		return out
	}
	at := func(day, hour int) {
		f.svc.now = func() time.Time { return time.Date(2026, 11, day, hour, 30, 0, 0, time.UTC) }
		if err := f.svc.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	at(18, 5) // before the sync hour
	if q := queued(); len(q) != 0 {
		t.Errorf("before 06:00: queued %v", q)
	}
	at(18, 6)
	at(18, 7) // the hourly re-run the same day adds nothing
	if q := queued(); len(q) != 2 || q[a] != 1 || q[b] != 1 {
		t.Errorf("on the 18th: queued %v, want one each for %d and %d", q, a, b)
	}
	at(19, 6)
	if q := queued(); q[a] != 2 || q[b] != 2 {
		t.Errorf("on the 19th: queued %v, want two each", q)
	}
}

// The whole path: the scheduler's Sync enqueues, a real worker runs the fetch job.
func TestSyncedFetchRunsOnAWorker(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	w := f.watch(t, []string{"engineer"}, nil, true)
	f.board.set(nil, posting("a", "Engineer", "Remote"), posting("b", "Recruiter", "Remote"))
	f.svc.now = func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }
	if err := f.svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}

	worker := jobs.NewWorker(jobs.New(f.st), slog.New(slog.DiscardHandler), jobs.Config{PollInterval: 10 * time.Millisecond})
	f.svc.Register(worker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(10 * time.Second)
	for len(f.stored(t, w)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no postings stored by the worker")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := f.stored(t, w); len(got) != 1 || got["a"].title != "Engineer" {
		t.Errorf("stored %v, want only a", got)
	}
}
