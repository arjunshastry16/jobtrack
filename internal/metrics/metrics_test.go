package metrics_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/metrics"
	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

var quietLogger = slog.New(slog.DiscardHandler)

// fakeJobs is a JobCounter that returns fixed rows, or an error.
type fakeJobs struct {
	rows []store.CountJobsRow
	err  error
}

func (f fakeJobs) CountJobs(context.Context) ([]store.CountJobsRow, error) { return f.rows, f.err }

// scrape fetches /metrics the way Prometheus does and returns the text.
func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// hasLine fails the test unless the scrape has exactly this sample line.
func hasLine(t *testing.T, text, line string) {
	t.Helper()
	for l := range strings.Lines(text) {
		if strings.TrimSuffix(l, "\n") == line {
			return
		}
	}
	t.Errorf("metrics missing line %q", line)
}

func TestHTTPMetrics(t *testing.T) {
	m := metrics.New("abc1234", fakeJobs{}, quietLogger)
	m.ObserveRequest("GET /api/v1/companies/{id}", 200, 120*time.Millisecond)
	m.ObserveRequest("GET /api/v1/companies/{id}", 200, 400*time.Millisecond)
	m.ObserveRequest("GET /api/v1/companies/{id}", 404, 2*time.Millisecond)
	m.ObserveRequest("", 401, time.Millisecond) // refused before routing

	text := scrape(t, m)
	hasLine(t, text, `jobtrack_http_requests_total{code="200",route="GET /api/v1/companies/{id}"} 2`)
	hasLine(t, text, `jobtrack_http_requests_total{code="404",route="GET /api/v1/companies/{id}"} 1`)
	hasLine(t, text, `jobtrack_http_requests_total{code="401",route="unmatched"} 1`)
	// The 0.3s bucket (the latency target) holds the 2ms and 120ms requests, not the 400ms one.
	hasLine(t, text, `jobtrack_http_request_duration_seconds_bucket{route="GET /api/v1/companies/{id}",le="0.3"} 2`)
	hasLine(t, text, `jobtrack_http_request_duration_seconds_count{route="GET /api/v1/companies/{id}"} 3`)
}

func TestWorkMetrics(t *testing.T) {
	m := metrics.New("abc1234", fakeJobs{}, quietLogger)
	m.JobFinished("watch.fetch", jobs.OutcomeDone, 90*time.Second)
	m.JobFinished("watch.fetch", jobs.OutcomeRetry, time.Second)
	m.LeaseExpired("reminder.follow_up")
	m.FetchFinished("workday", "error", 3*time.Second)
	m.FetchFinished("lever", "ok", 2*time.Second)
	m.PostingsAdded("lever", 7)
	m.PostingsAdded("lever", 0)

	text := scrape(t, m)
	hasLine(t, text, `jobtrack_jobs_processed_total{kind="watch.fetch",outcome="done"} 1`)
	hasLine(t, text, `jobtrack_jobs_processed_total{kind="watch.fetch",outcome="retry"} 1`)
	hasLine(t, text, `jobtrack_job_duration_seconds_bucket{kind="watch.fetch",le="120"} 2`)
	hasLine(t, text, `jobtrack_job_duration_seconds_bucket{kind="watch.fetch",le="60"} 1`)
	hasLine(t, text, `jobtrack_job_leases_expired_total{kind="reminder.follow_up"} 1`)
	hasLine(t, text, `jobtrack_board_fetches_total{board="workday",outcome="error"} 1`)
	hasLine(t, text, `jobtrack_board_fetches_total{board="lever",outcome="ok"} 1`)
	hasLine(t, text, `jobtrack_board_fetch_duration_seconds_count{board="lever"} 1`)
	hasLine(t, text, `jobtrack_postings_new_total{board="lever"} 7`)
}

func TestBuildAndRuntimeMetrics(t *testing.T) {
	text := scrape(t, metrics.New("abc1234", fakeJobs{}, quietLogger))
	if !regexp.MustCompile(`(?m)^jobtrack_build_info\{goversion="go[^"]+",version="abc1234"\} 1$`).MatchString(text) {
		t.Error("no jobtrack_build_info for version abc1234")
	}
	for _, name := range []string{"go_goroutines", "process_resident_memory_bytes"} {
		if !regexp.MustCompile(`(?m)^` + name + ` `).MatchString(text) {
			t.Errorf("no %s: the Go runtime and process collectors aren't registered", name)
		}
	}
}

func TestQueueMetrics(t *testing.T) {
	m := metrics.New("abc1234", fakeJobs{rows: []store.CountJobsRow{
		{Kind: "watch.fetch", State: "done", Jobs: 12},
		{Kind: "watch.fetch", State: "pending", Jobs: 2, OldestDueSeconds: 42.5},
		{Kind: "watch.fetch", State: "running", Jobs: 1}, // after pending, as the query sorts them
		{Kind: "reminder.follow_up", State: "dead", Jobs: 1},
	}}, quietLogger)

	text := scrape(t, m)
	hasLine(t, text, `jobtrack_jobs{kind="watch.fetch",state="done"} 12`)
	hasLine(t, text, `jobtrack_jobs{kind="watch.fetch",state="pending"} 2`)
	hasLine(t, text, `jobtrack_jobs_oldest_due_seconds{kind="watch.fetch"} 42.5`)
	// Missing states read 0, so an alert on dead jobs sees them clear.
	hasLine(t, text, `jobtrack_jobs{kind="watch.fetch",state="dead"} 0`)
	hasLine(t, text, `jobtrack_jobs{kind="watch.fetch",state="running"} 1`)
	hasLine(t, text, `jobtrack_jobs{kind="reminder.follow_up",state="dead"} 1`)
	hasLine(t, text, `jobtrack_jobs{kind="reminder.follow_up",state="pending"} 0`)
	hasLine(t, text, `jobtrack_jobs_oldest_due_seconds{kind="reminder.follow_up"} 0`)
}

func TestQueueQueryFailureKeepsTheRestOfTheScrape(t *testing.T) {
	m := metrics.New("abc1234", fakeJobs{err: errors.New("database down")}, quietLogger)
	m.ObserveRequest("GET /healthz", 200, time.Millisecond)

	text := scrape(t, m)
	if strings.Contains(text, "jobtrack_jobs{") {
		t.Error("queue metrics reported although the query failed")
	}
	hasLine(t, text, `jobtrack_http_requests_total{code="200",route="GET /healthz"} 1`)
}

// TestQueueMetricsFromPostgres runs the real CountJobs query: counts by kind and state,
// and how long the oldest due job has waited (jobs not yet due don't count).
func TestQueueMetricsFromPostgres(t *testing.T) {
	t.Parallel()
	st := store.NewStore(testdb.New(t))
	q := jobs.New(st)
	ctx := t.Context()
	enqueue := func(kind string, opts ...jobs.EnqueueOption) {
		t.Helper()
		if _, _, err := q.Enqueue(ctx, kind, nil, opts...); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("a", jobs.RunAt(time.Now().Add(-time.Hour))) // due an hour ago
	enqueue("a", jobs.RunAt(time.Now().Add(-time.Minute)))
	enqueue("a", jobs.RunAt(time.Now().Add(2*time.Hour))) // not due: doesn't count as waiting
	enqueue("b", jobs.RunAt(time.Now().Add(time.Hour)))   // nothing due at all
	claimed, err := q.Claim(ctx, 1, time.Minute)          // takes "a" (the oldest)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v, %d jobs", err, len(claimed))
	}
	if err := q.Complete(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}

	text := scrape(t, metrics.New("abc1234", st, quietLogger))
	hasLine(t, text, `jobtrack_jobs{kind="a",state="done"} 1`)
	hasLine(t, text, `jobtrack_jobs{kind="a",state="pending"} 2`)
	hasLine(t, text, `jobtrack_jobs{kind="b",state="pending"} 1`)
	hasLine(t, text, `jobtrack_jobs_oldest_due_seconds{kind="b"} 0`)
	// The hour-old job was done; the oldest still due was due about a minute ago.
	waited := sample(t, text, `jobtrack_jobs_oldest_due_seconds{kind="a"}`)
	if waited < 55 || waited > 120 {
		t.Errorf("oldest due job of kind a waited %.1fs, want about 60s", waited)
	}
}

// sample returns the value of the sample whose name and labels are series.
func sample(t *testing.T, text, series string) float64 {
	t.Helper()
	for l := range strings.Lines(text) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatal(err)
			}
			return f
		}
	}
	t.Fatalf("no sample %s", series)
	return 0
}
