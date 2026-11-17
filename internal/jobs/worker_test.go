package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arjunshastry16/jobtrack/internal/jobs"
)

// fastConfig makes the worker react in milliseconds so tests run quickly.
var fastConfig = jobs.Config{
	Concurrency:  2,
	PollInterval: 10 * time.Millisecond,
	Lease:        5 * time.Second,
	ReapInterval: 50 * time.Millisecond,
	BackoffBase:  10 * time.Millisecond,
	BackoffMax:   40 * time.Millisecond,
}

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// startWorker runs w until the test ends (or until the returned stop is called) and
// returns a channel closed when Run has returned.
func startWorker(t *testing.T, w *jobs.Worker) (stop func(), stopped <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel, done
}

// waitFor polls cond until it is true or the timeout passes.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func stateIs(t *testing.T, pool *pgxpool.Pool, id int64, want string) func() bool {
	return func() bool { return jobState(t, pool, id).State == want }
}

func TestWorkerRunsHandler(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	var got atomic.Value
	w := jobs.NewWorker(q, quietLogger, fastConfig)
	w.Handle("greet", func(_ context.Context, j jobs.Job) error {
		var p struct{ Name string }
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return jobs.Permanent(err)
		}
		got.Store(p.Name)
		return nil
	})
	id, _, _ := q.Enqueue(t.Context(), "greet", map[string]string{"name": "Ada"})
	startWorker(t, w)

	waitFor(t, 5*time.Second, "job done", stateIs(t, pool, id, "done"))
	// Compare decoded values: JSONB normalises formatting, so the raw bytes differ.
	if got.Load() != "Ada" {
		t.Errorf("handler saw name %v, want Ada", got.Load())
	}
}

func TestWorkerRetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	var calls atomic.Int32
	w := jobs.NewWorker(q, quietLogger, fastConfig)
	w.Handle("flaky", func(_ context.Context, j jobs.Job) error {
		calls.Add(1)
		if j.Attempt < 3 {
			return errors.New("upstream timeout")
		}
		return nil
	})
	id, _, _ := q.Enqueue(t.Context(), "flaky", nil)
	startWorker(t, w)

	waitFor(t, 5*time.Second, "job done", stateIs(t, pool, id, "done"))
	j := jobState(t, pool, id)
	if calls.Load() != 3 || j.Attempts != 3 {
		t.Errorf("handler calls %d, attempts %d; want 3 and 3", calls.Load(), j.Attempts)
	}
	if j.LastError == nil || *j.LastError != "upstream timeout" {
		t.Errorf("last_error = %v, want the last failure kept for debugging", j.LastError)
	}
}

func TestWorkerDeadLettersAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	var calls atomic.Int32
	w := jobs.NewWorker(q, quietLogger, fastConfig)
	w.Handle("broken", func(context.Context, jobs.Job) error {
		calls.Add(1)
		return errors.New("always fails")
	})
	id, _, _ := q.Enqueue(t.Context(), "broken", nil, jobs.MaxAttempts(3))
	startWorker(t, w)

	waitFor(t, 5*time.Second, "job dead", stateIs(t, pool, id, "dead"))
	time.Sleep(100 * time.Millisecond) // a dead job must never run again
	j := jobState(t, pool, id)
	if calls.Load() != 3 || j.Attempts != 3 || j.FinishedAt == nil || *j.LastError != "always fails" {
		t.Errorf("calls %d attempts %d finished %v last_error %v", calls.Load(), j.Attempts, j.FinishedAt, j.LastError)
	}
}

func TestWorkerPermanentErrorAndUnknownKind(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	var calls atomic.Int32
	w := jobs.NewWorker(q, quietLogger, fastConfig)
	w.Handle("strict", func(context.Context, jobs.Job) error {
		calls.Add(1)
		return jobs.Permanent(errors.New("application 7 no longer exists"))
	})
	strict, _, _ := q.Enqueue(t.Context(), "strict", nil)
	unknown, _, _ := q.Enqueue(t.Context(), "nobody.handles.this", nil)
	startWorker(t, w)

	waitFor(t, 5*time.Second, "both jobs dead", func() bool {
		return stateIs(t, pool, strict, "dead")() && stateIs(t, pool, unknown, "dead")()
	})
	if calls.Load() != 1 || jobState(t, pool, strict).Attempts != 1 {
		t.Errorf("permanent error retried: %d calls", calls.Load())
	}
	if msg := *jobState(t, pool, unknown).LastError; !strings.Contains(msg, "no handler registered") {
		t.Errorf("unknown kind last_error = %q", msg)
	}
}

func TestWorkerSurvivesPanics(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	var calls atomic.Int32
	w := jobs.NewWorker(q, quietLogger, fastConfig)
	w.Handle("panicky", func(context.Context, jobs.Job) error {
		if calls.Add(1) == 1 {
			panic("nil map write")
		}
		return nil
	})
	id, _, _ := q.Enqueue(t.Context(), "panicky", nil)
	startWorker(t, w)

	waitFor(t, 5*time.Second, "job done after a panic", stateIs(t, pool, id, "done"))
	if msg := *jobState(t, pool, id).LastError; !strings.Contains(msg, "panic: nil map write") {
		t.Errorf("last_error = %q, want the panic recorded", msg)
	}
}

// A worker that claims a job and then dies leaves it "running" with a lease. Once the
// lease expires, the reaper requeues it and a healthy worker finishes it.
func TestCrashedWorkersJobIsRecovered(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	id, _, _ := q.Enqueue(t.Context(), "work", nil)

	// The "crashed" worker: claims with a short lease and never reports back.
	if claimed, err := q.Claim(t.Context(), 1, 200*time.Millisecond); err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %v", claimed, err)
	}

	var attemptSeen atomic.Int32
	w := jobs.NewWorker(q, quietLogger, fastConfig)
	w.Handle("work", func(_ context.Context, j jobs.Job) error {
		attemptSeen.Store(j.Attempt)
		return nil
	})
	startWorker(t, w)

	waitFor(t, 5*time.Second, "recovered job done", stateIs(t, pool, id, "done"))
	if attemptSeen.Load() != 2 {
		t.Errorf("recovered job ran as attempt %d, want 2", attemptSeen.Load())
	}
}

func TestExpiredLeaseOnLastAttemptGoesDead(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	id, _, _ := q.Enqueue(t.Context(), "work", nil, jobs.MaxAttempts(1))
	if _, err := q.Claim(t.Context(), 1, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	requeued, err := q.RequeueExpired(t.Context())
	if err != nil || len(requeued) != 1 || !requeued[0].Dead {
		t.Fatalf("RequeueExpired = %+v, %v; want one job, dead", requeued, err)
	}
	if j := jobState(t, pool, id); j.State != "dead" || !strings.Contains(*j.LastError, "lease expired") {
		t.Errorf("state %q, last_error %v", j.State, j.LastError)
	}
}

// On shutdown the worker stops claiming but lets the job in progress finish.
func TestWorkerDrainsOnShutdown(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	cfg := fastConfig
	cfg.Concurrency = 1

	started := make(chan struct{})
	release := make(chan struct{})
	var handlerCtxErr atomic.Value
	w := jobs.NewWorker(q, quietLogger, cfg)
	w.Handle("slow", func(ctx context.Context, _ jobs.Job) error {
		close(started)
		<-release
		handlerCtxErr.Store(errOrNil(ctx.Err()))
		return nil
	})
	inFlight, _, _ := q.Enqueue(t.Context(), "slow", nil)
	stop, stopped := startWorker(t, w)

	<-started
	stop() // SIGTERM arrives while the job is running
	later, _, _ := q.Enqueue(t.Context(), "slow", nil)

	select {
	case <-stopped:
		t.Fatal("Run returned while a job was still in progress")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the in-flight job finished")
	}

	if s := jobState(t, pool, inFlight).State; s != "done" {
		t.Errorf("in-flight job state = %q, want done (drained)", s)
	}
	if s := jobState(t, pool, later).State; s != "pending" {
		t.Errorf("job enqueued after shutdown = %q, want pending (not claimed)", s)
	}
	if e := handlerCtxErr.Load(); e != "" {
		t.Errorf("handler context was cancelled by shutdown: %v", e)
	}
}

func errOrNil(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestWorkerConcurrencyLimit(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	cfg := fastConfig
	cfg.Concurrency = 3

	var (
		mu             sync.Mutex
		inFlight, peak int
	)
	w := jobs.NewWorker(q, quietLogger, cfg)
	w.Handle("slow", func(context.Context, jobs.Job) error {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	})
	var ids []int64
	for range 9 {
		id, _, _ := q.Enqueue(t.Context(), "slow", nil)
		ids = append(ids, id)
	}
	startWorker(t, w)

	waitFor(t, 10*time.Second, "all jobs done", func() bool {
		for _, id := range ids {
			if jobState(t, pool, id).State != "done" {
				return false
			}
		}
		return true
	})
	mu.Lock()
	defer mu.Unlock()
	if peak != 3 {
		t.Errorf("peak jobs in flight = %d, want exactly the concurrency limit 3", peak)
	}
}
