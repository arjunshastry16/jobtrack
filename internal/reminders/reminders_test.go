package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/notify"
	"github.com/arjunshastry16/jobtrack/internal/notify/notifytest"
	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

// noon is a fixed "now" inside reminder hours, so tests don't depend on the real clock.
var noon = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

type fixture struct {
	pool *pgxpool.Pool
	st   *store.Store
	q    *jobs.Queue
	rec  *notifytest.Recorder
	svc  *Service
	n    atomic.Int32 // for unique company names
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	st := store.NewStore(pool)
	q := jobs.New(st)
	rec := &notifytest.Recorder{}
	svc := NewService(st, q, rec, slog.New(slog.DiscardHandler), Config{
		AfterDays: 7, StartHour: 9, EndHour: 21, Location: time.UTC,
	})
	svc.now = func() time.Time { return noon }
	return &fixture{pool: pool, st: st, q: q, rec: rec, svc: svc}
}

// app creates an application at a new company whose only (latest) stage change
// happened at lastChange.
func (f *fixture) app(t *testing.T, company, status string, lastChange time.Time) int64 {
	t.Helper()
	ctx := t.Context()
	co, err := f.st.CreateCompany(ctx, store.CreateCompanyParams{Name: fmt.Sprintf("%s %d", company, f.n.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.st.CreateApplication(ctx, store.CreateApplicationParams{CompanyID: co.ID, Role: "SWE", Status: status})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateApplicationEvent(ctx, store.CreateApplicationEventParams{
		ApplicationID: id, ToStatus: status, OccurredAt: &lastChange,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// queued returns the application ids of all follow-up jobs, sorted.
func (f *fixture) queued(t *testing.T) []int64 {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT payload FROM jobs WHERE kind = $1", FollowUpKind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p followUpPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ApplicationID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	return ids
}

func daysAgo(n int) time.Time { return noon.AddDate(0, 0, -n) }

func TestScanEnqueuesOnlyStaleOpenApplications(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	staleApplied := f.app(t, "Acme", "applied", daysAgo(10))
	staleInterviewing := f.app(t, "Globex", "interviewing", daysAgo(8))
	f.app(t, "Fresh", "applied", daysAgo(2))                     // too recent
	f.app(t, "Someday", "wishlist", daysAgo(30))                 // not applied yet
	f.app(t, "Closed", "rejected", daysAgo(30))                  // final status
	f.app(t, "Done", "accepted", daysAgo(30))                    // final status
	f.app(t, "Boundary", "applied", daysAgo(7).Add(time.Minute)) // just under 7 days

	if err := f.svc.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []int64{staleApplied, staleInterviewing}
	slices.Sort(want)
	if got := f.queued(t); !slices.Equal(got, want) {
		t.Errorf("queued follow-ups for %v, want %v", got, want)
	}
}

func TestScanIsIdempotentPerDay(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.app(t, "Acme", "applied", daysAgo(10))

	for range 3 { // hourly scans on the same day
		if err := f.svc.Scan(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.queued(t)); n != 1 {
		t.Fatalf("%d jobs after 3 scans on one day, want 1", n)
	}

	f.svc.now = func() time.Time { return noon.AddDate(0, 0, 1) } // next day: remind again
	if err := f.svc.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.queued(t)); n != 2 {
		t.Errorf("%d jobs after a scan the next day, want 2", n)
	}
}

func TestScanRespectsQuietHours(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.app(t, "Acme", "applied", daysAgo(10))

	for _, hour := range []int{0, 3, 8, 21, 23} {
		f.svc.now = func() time.Time { return time.Date(2026, 11, 17, hour, 30, 0, 0, time.UTC) }
		if err := f.svc.Scan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if n := len(f.queued(t)); n != 0 {
			t.Fatalf("scan at %02d:30 queued %d reminders, want none (quiet hours)", hour, n)
		}
	}
	f.svc.now = func() time.Time { return time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC) }
	if err := f.svc.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.queued(t)); n != 1 {
		t.Errorf("scan at 09:00 queued %d, want 1", n)
	}
}

// End to end: scan -> queue -> worker -> handler -> notifier.
func TestRemindersAreDelivered(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.app(t, "Acme", "applied", daysAgo(10))
	f.app(t, "Globex", "interviewing", daysAgo(8))

	w := jobs.NewWorker(f.q, slog.New(slog.DiscardHandler), jobs.Config{PollInterval: 10 * time.Millisecond})
	f.svc.Register(w)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	if err := f.svc.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(f.rec.Messages()) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	var got []string
	for _, m := range f.rec.Messages() {
		got = append(got, m.Title+" | "+m.Body)
	}
	slices.Sort(got)
	want := []string{
		"Follow up: Acme 1 | No update in 10 days (status: applied).",
		"Follow up: Globex 2 | No update in 8 days (status: interviewing).",
	}
	if !slices.Equal(got, want) {
		t.Errorf("notifications:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func followUpJob(appID int64) jobs.Job {
	return jobs.Job{Kind: FollowUpKind, Payload: json.RawMessage(fmt.Sprintf(`{"application_id":%d}`, appID)), Attempt: 1}
}

func TestHandlerSkipsApplicationsThatMovedOn(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	// A new stage change after the scan: no reminder.
	progressed := f.app(t, "Acme", "applied", daysAgo(10))
	recent := daysAgo(1)
	if _, err := f.st.CreateApplicationEvent(ctx, store.CreateApplicationEventParams{
		ApplicationID: progressed, FromStatus: ptr("applied"), ToStatus: "screening", OccurredAt: &recent,
	}); err != nil {
		t.Fatal(err)
	}
	// Rejected after the scan: no reminder.
	closed := f.app(t, "Globex", "applied", daysAgo(10))
	if err := f.st.SetApplicationStatus(ctx, store.SetApplicationStatusParams{ID: closed, Status: "rejected"}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{progressed, closed} {
		if err := f.svc.HandleFollowUp(ctx, followUpJob(id)); err != nil {
			t.Errorf("application %d: %v, want nil (nothing to do)", id, err)
		}
	}
	if n := len(f.rec.Messages()); n != 0 {
		t.Errorf("%d notifications sent, want 0", n)
	}
}

func TestHandlerErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	app := f.app(t, "Acme", "applied", daysAgo(10))

	// Permanent: the payload or the application is gone for good.
	for name, j := range map[string]jobs.Job{
		"deleted application": followUpJob(999999),
		"bad payload":         {Kind: FollowUpKind, Payload: json.RawMessage(`not json`)},
		"missing id":          {Kind: FollowUpKind, Payload: json.RawMessage(`{}`)},
	} {
		if err := f.svc.HandleFollowUp(ctx, j); !jobs.IsPermanent(err) {
			t.Errorf("%s: err = %v, want a permanent error", name, err)
		}
	}

	// ntfy down or rate limiting: retry. ntfy refusing us (bad token): give up.
	f.rec.Err = &notify.StatusError{Code: 503}
	if err := f.svc.HandleFollowUp(ctx, followUpJob(app)); err == nil || jobs.IsPermanent(err) {
		t.Errorf("503 from ntfy: err = %v, want a retryable error", err)
	}
	f.rec.Err = &notify.StatusError{Code: 403}
	if err := f.svc.HandleFollowUp(ctx, followUpJob(app)); !jobs.IsPermanent(err) {
		t.Errorf("403 from ntfy: err = %v, want a permanent error", err)
	}
	f.rec.Err = errors.New("dial tcp: connection refused")
	if err := f.svc.HandleFollowUp(ctx, followUpJob(app)); err == nil || jobs.IsPermanent(err) {
		t.Errorf("network error: err = %v, want a retryable error", err)
	}
}

func ptr(s string) *string { return &s }
