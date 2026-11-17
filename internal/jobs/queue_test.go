package jobs_test

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

func newQueue(t *testing.T) (*jobs.Queue, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return jobs.New(store.New(pool)), pool
}

func jobState(t *testing.T, pool *pgxpool.Pool, id int64) store.Job {
	t.Helper()
	j, err := store.New(pool).GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestEnqueueClaimComplete(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	ctx := t.Context()

	type payload struct {
		ApplicationID int64 `json:"application_id"`
	}
	id, created, err := q.Enqueue(ctx, "test.kind", payload{ApplicationID: 42})
	if err != nil || !created || id <= 0 {
		t.Fatalf("Enqueue = %d, %v, %v", id, created, err)
	}

	claimed, err := q.Claim(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(claimed))
	}
	j := claimed[0]
	var p payload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.ApplicationID != 42 {
		t.Errorf("payload = %s (%v), want application_id 42", j.Payload, err)
	}
	if j.Kind != "test.kind" || j.Attempt != 1 || j.MaxAttempts != jobs.DefaultMaxAttempts {
		t.Errorf("job = %+v", j)
	}
	if lease := time.Until(j.LeaseEnds); lease < 50*time.Second || lease > 70*time.Second {
		t.Errorf("lease ends in %s, want about 1m", lease)
	}

	// A running job is not handed out again.
	again, err := q.Claim(ctx, 10, time.Minute)
	if err != nil || len(again) != 0 {
		t.Fatalf("second claim = %d jobs, %v; want none", len(again), err)
	}

	if err := q.Complete(ctx, j); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if s := jobState(t, pool, id); s.State != "done" || s.FinishedAt == nil || s.LockedUntil != nil {
		t.Errorf("after complete: state %q finished_at %v locked_until %v", s.State, s.FinishedAt, s.LockedUntil)
	}
	if err := q.Complete(ctx, j); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("completing twice = %v, want ErrLeaseLost", err)
	}
}

func TestClaimOrderAndDelay(t *testing.T) {
	t.Parallel()
	q, _ := newQueue(t)
	ctx := t.Context()

	later, _, _ := q.Enqueue(ctx, "k", nil, jobs.RunAt(time.Now().Add(time.Hour)))
	first, _, _ := q.Enqueue(ctx, "k", nil, jobs.RunAt(time.Now().Add(-2*time.Minute)))
	second, _, _ := q.Enqueue(ctx, "k", nil, jobs.RunAt(time.Now().Add(-time.Minute)))

	claimed, err := q.Claim(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, j := range claimed {
		ids = append(ids, j.ID)
	}
	if len(ids) != 2 || ids[0] != first || ids[1] != second {
		t.Errorf("claimed %v, want [%d %d] (oldest due first; %d is not due yet)", ids, first, second, later)
	}
}

func TestEnqueueDedupe(t *testing.T) {
	t.Parallel()
	q, _ := newQueue(t)
	ctx := t.Context()

	id1, created1, err1 := q.Enqueue(ctx, "reminder", map[string]int{"n": 1}, jobs.DedupeKey("app:7:2026-11-17"))
	id2, created2, err2 := q.Enqueue(ctx, "reminder", map[string]int{"n": 2}, jobs.DedupeKey("app:7:2026-11-17"))
	if err1 != nil || err2 != nil || !created1 || created2 || id2 != 0 {
		t.Fatalf("enqueue twice = (%d %v %v), (%d %v %v); want second to be a no-op", id1, created1, err1, id2, created2, err2)
	}
	// Same key under a different kind, or a different key, is a different job.
	if _, created, _ := q.Enqueue(ctx, "other", nil, jobs.DedupeKey("app:7:2026-11-17")); !created {
		t.Error("same key with another kind was deduplicated")
	}
	if _, created, _ := q.Enqueue(ctx, "reminder", nil, jobs.DedupeKey("app:7:2026-11-18")); !created {
		t.Error("different key was deduplicated")
	}
	// Jobs without a key are never deduplicated.
	for range 2 {
		if _, created, _ := q.Enqueue(ctx, "plain", nil); !created {
			t.Error("job without a dedupe key was deduplicated")
		}
	}
}

func TestStaleWorkerCannotComplete(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	ctx := t.Context()

	id, _, _ := q.Enqueue(ctx, "k", nil)
	claimed, err := q.Claim(ctx, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	// Simulate the lease expiring and another worker re-claiming the job (attempt 2).
	if _, err := pool.Exec(ctx, "UPDATE jobs SET attempts = attempts + 1 WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(ctx, claimed[0]); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("stale Complete = %v, want ErrLeaseLost", err)
	}
	if s := jobState(t, pool, id); s.State != "running" {
		t.Errorf("state = %q, want still running (owned by the newer attempt)", s.State)
	}
}

func TestInvalidArguments(t *testing.T) {
	t.Parallel()
	q, _ := newQueue(t)
	ctx := t.Context()
	if _, _, err := q.Enqueue(ctx, "", nil); err == nil {
		t.Error("empty kind accepted")
	}
	if _, _, err := q.Enqueue(ctx, "k", nil, jobs.MaxAttempts(0)); err == nil {
		t.Error("max attempts 0 accepted")
	}
	if _, _, err := q.Enqueue(ctx, "k", func() {}); err == nil {
		t.Error("unencodable payload accepted")
	}
	if _, err := q.Claim(ctx, 0, time.Minute); err == nil {
		t.Error("batch size 0 accepted")
	}
	if _, err := q.Claim(ctx, 1, 0); err == nil {
		t.Error("zero lease accepted")
	}
}

// TestConcurrentWorkersNeverDoubleClaim runs several workers against one queue at the
// same time. Every job must be processed exactly once.
func TestConcurrentWorkersNeverDoubleClaim(t *testing.T) {
	t.Parallel()
	q, pool := newQueue(t)
	ctx := t.Context()

	const total, workers, batch = 300, 8, 5
	for i := range total {
		if _, _, err := q.Enqueue(ctx, "k", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu       sync.Mutex
		seen     = map[int64]int{} // job id -> times claimed
		done     atomic.Int64
		start    = make(chan struct{})
		wg       sync.WaitGroup
		deadline = time.Now().Add(30 * time.Second)
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for done.Load() < total && time.Now().Before(deadline) {
				claimed, err := q.Claim(ctx, batch, time.Minute)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				for _, j := range claimed {
					mu.Lock()
					seen[j.ID]++
					mu.Unlock()
					if err := q.Complete(ctx, j); err != nil {
						t.Errorf("complete %d: %v", j.ID, err)
					}
					done.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	var dupes []int64
	for id, n := range seen {
		if n > 1 {
			dupes = append(dupes, id)
		}
	}
	if len(dupes) > 0 {
		t.Errorf("%d jobs were claimed more than once, e.g. %v", len(dupes), dupes[:min(5, len(dupes))])
	}
	if len(seen) != total {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), total)
	}
	var notDone int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state <> 'done'").Scan(&notDone); err != nil {
		t.Fatal(err)
	}
	if notDone != 0 {
		t.Errorf("%d jobs not done", notDone)
	}
}
