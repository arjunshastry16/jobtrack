// Package reminders nudges you to follow up on applications that have gone quiet.
//
// A scheduled Scan finds open applications with no stage change for AfterDays days and
// enqueues one "reminder.follow_up" job per application per day (the dedupe key makes
// repeated scans harmless). The job handler re-checks the application, because things
// may have moved on since the scan, and then sends a push notification.
package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/notify"
	"github.com/arjunshastry16/jobtrack/internal/scheduler"
	"github.com/arjunshastry16/jobtrack/internal/stages"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

// FollowUpKind is the job kind for follow-up reminders.
const FollowUpKind = "reminder.follow_up"

// scanLimit caps how many reminders one scan enqueues (the rest come next hour).
const scanLimit = 200

// waiting are the statuses where you are waiting on the company, so a long silence
// means it's time to follow up. Must match the list in queries/reminders.sql.
var waiting = map[string]bool{
	stages.Applied: true, stages.Screening: true, stages.Interviewing: true, stages.Offer: true,
}

// Config controls when reminders fire.
type Config struct {
	AfterDays int // remind after this many days without a stage change
	// Reminders are only enqueued between StartHour and EndHour local time (EndHour
	// exclusive), so nothing buzzes your phone at 3 a.m. 0 and 24 mean any time.
	StartHour, EndHour int
	Location           *time.Location // nil means time.Local (set TZ in the container)
}

// Service scans for stale applications and sends follow-up reminders.
type Service struct {
	store    store.Querier
	queue    *jobs.Queue
	notifier notify.Notifier
	cfg      Config
	logger   *slog.Logger
	now      func() time.Time // replaced in tests
}

// NewService returns a reminders Service.
func NewService(st store.Querier, q *jobs.Queue, n notify.Notifier, logger *slog.Logger, cfg Config) *Service {
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	return &Service{store: st, queue: q, notifier: n, cfg: cfg, logger: logger, now: time.Now}
}

// Register adds the follow-up handler to a worker.
func (s *Service) Register(w *jobs.Worker) {
	w.Handle(FollowUpKind, s.HandleFollowUp)
}

// ScanTask is the scheduled task that runs Scan.
func (s *Service) ScanTask(every time.Duration) scheduler.Task {
	return scheduler.Task{Name: "reminders.scan", Every: every, Run: s.Scan}
}

type followUpPayload struct {
	ApplicationID int64 `json:"application_id"`
}

// Scan enqueues a follow-up job for every stale application, at most one per
// application per local day, and only inside the configured hours.
func (s *Service) Scan(ctx context.Context) error {
	now := s.now().In(s.cfg.Location)
	if h := now.Hour(); h < s.cfg.StartHour || h >= s.cfg.EndHour {
		return nil // quiet hours
	}

	stale, err := s.store.ListStaleApplications(ctx, store.ListStaleApplicationsParams{
		Cutoff:   now.AddDate(0, 0, -s.cfg.AfterDays),
		RowLimit: scanLimit,
	})
	if err != nil {
		return fmt.Errorf("reminders: list stale applications: %w", err)
	}

	day := now.Format(time.DateOnly)
	enqueued := 0
	for _, a := range stale {
		_, created, err := s.queue.Enqueue(ctx, FollowUpKind, followUpPayload{ApplicationID: a.ID},
			jobs.DedupeKey(fmt.Sprintf("application:%d:%s", a.ID, day)))
		if err != nil {
			return fmt.Errorf("reminders: enqueue for application %d: %w", a.ID, err)
		}
		if created {
			enqueued++
		}
	}
	if enqueued > 0 {
		s.logger.InfoContext(ctx, "follow-up reminders enqueued", "count", enqueued, "stale", len(stale))
	}
	return nil
}

// HandleFollowUp sends the reminder for one application, unless it has moved on since
// the scan (a new stage change, or a final status) or been deleted.
func (s *Service) HandleFollowUp(ctx context.Context, j jobs.Job) error {
	var p followUpPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return jobs.Permanent(fmt.Errorf("bad follow-up payload %s: %w", j.Payload, err))
	}
	if p.ApplicationID <= 0 {
		return jobs.Permanent(fmt.Errorf("follow-up payload %s has no application_id", j.Payload))
	}

	row, err := s.store.GetApplication(ctx, p.ApplicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Permanent(fmt.Errorf("application %d no longer exists", p.ApplicationID))
	}
	if err != nil {
		return err
	}
	app := row.Application
	if !waiting[app.Status] {
		return nil // moved to a final status since the scan: nothing to follow up
	}

	last, err := s.store.LatestApplicationEventTime(ctx, app.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		last = app.CreatedAt
	} else if err != nil {
		return err
	}
	days := int(s.now().Sub(last).Hours() / 24)
	if days < s.cfg.AfterDays {
		return nil // there was a stage change since the scan
	}

	// Kept short and free of detail: on a public ntfy server the topic name is the only secret.
	msg := notify.Message{
		Title: "Follow up: " + row.CompanyName,
		Body:  fmt.Sprintf("No update in %d days (status: %s).", days, app.Status),
		Tags:  []string{"bell"},
	}
	if err := s.notifier.Notify(ctx, msg); err != nil {
		if notify.IsRetryable(err) {
			return err // the worker retries with backoff
		}
		return jobs.Permanent(err) // e.g. bad token: retrying won't help
	}
	s.logger.InfoContext(ctx, "follow-up reminder sent", "application_id", app.ID, "days_quiet", days)
	return nil
}
