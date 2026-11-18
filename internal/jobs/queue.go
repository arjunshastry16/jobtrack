// Package jobs is a durable background-job queue stored in Postgres.
//
// Producers call Enqueue. Workers call Claim to take a batch of due jobs (each gets a
// time-limited lease), do the work, then call Complete. Claiming uses
// SELECT ... FOR UPDATE SKIP LOCKED, so any number of workers can share the queue
// without double-processing a job or blocking each other.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

// DefaultMaxAttempts is how many times a job is tried before it is given up on.
const DefaultMaxAttempts = 5

// ErrLeaseLost means the job is no longer this worker's to finish: its lease expired
// and another worker claimed it (or it was already finished).
var ErrLeaseLost = errors.New("jobs: lease lost")

// Job is a claimed job. Attempt is this claim's attempt number (1 for the first try);
// it also acts as the fencing token Complete uses.
type Job struct {
	ID          int64
	Kind        string
	Payload     json.RawMessage
	Attempt     int32
	MaxAttempts int32
	Lease       time.Duration // how long this claim is leased for
	LeaseEnds   time.Time     // by the database clock
}

// MaxLease is the longest lease a job may ask for.
const MaxLease = 24 * time.Hour

// Queue enqueues and claims jobs.
type Queue struct {
	q store.Querier
}

// New returns a Queue that runs its queries through q (a pool, or a transaction to
// enqueue atomically with other changes).
func New(q store.Querier) *Queue {
	return &Queue{q: q}
}

// EnqueueOption customises Enqueue.
type EnqueueOption func(*store.EnqueueJobParams)

// RunAt delays the job until t.
func RunAt(t time.Time) EnqueueOption {
	return func(p *store.EnqueueJobParams) { p.RunAt = &t }
}

// DedupeKey makes Enqueue idempotent: a second job with the same kind and key is not created.
func DedupeKey(key string) EnqueueOption {
	return func(p *store.EnqueueJobParams) { p.DedupeKey = &key }
}

// MaxAttempts overrides DefaultMaxAttempts.
func MaxAttempts(n int32) EnqueueOption {
	return func(p *store.EnqueueJobParams) { p.MaxAttempts = n }
}

// Lease sets how long each claim of the job lasts, instead of the worker's default.
// Use it for jobs that can legitimately run longer than the default lease; the worker
// stops the handler at 90% of it. Rounded down to whole seconds; 1s to MaxLease.
func Lease(d time.Duration) EnqueueOption {
	return func(p *store.EnqueueJobParams) {
		secs := int32(-1) // invalid: rejected by Enqueue
		if d >= time.Second && d <= MaxLease {
			secs = int32(d / time.Second)
		}
		p.LeaseSeconds = &secs
	}
}

// Enqueue adds a job of the given kind with payload encoded as JSON. created is false
// (and id 0) when DedupeKey matched an existing job.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any, opts ...EnqueueOption) (id int64, created bool, err error) {
	if kind == "" {
		return 0, false, errors.New("jobs: empty kind")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, false, fmt.Errorf("jobs: encode %s payload: %w", kind, err)
	}
	p := store.EnqueueJobParams{Kind: kind, Payload: body, MaxAttempts: DefaultMaxAttempts}
	for _, opt := range opts {
		opt(&p)
	}
	if p.MaxAttempts < 1 {
		return 0, false, fmt.Errorf("jobs: max attempts must be at least 1, got %d", p.MaxAttempts)
	}
	if p.LeaseSeconds != nil && *p.LeaseSeconds < 1 {
		return 0, false, fmt.Errorf("jobs: lease must be between 1s and %s", MaxLease)
	}

	id, err = q.q.EnqueueJob(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // duplicate dedupe key: the job already exists
	}
	if err != nil {
		return 0, false, fmt.Errorf("jobs: enqueue %s: %w", kind, err)
	}
	return id, true, nil
}

// Claim takes up to n due jobs, oldest first, leasing each for its own lease (see Lease)
// or else defaultLease. The caller must Complete each job before its lease ends.
func (q *Queue) Claim(ctx context.Context, n int, defaultLease time.Duration) ([]Job, error) {
	if n < 1 || n > 1000 {
		return nil, fmt.Errorf("jobs: claim batch size must be 1..1000, got %d", n)
	}
	if defaultLease <= 0 {
		return nil, fmt.Errorf("jobs: lease must be positive, got %s", defaultLease)
	}
	rows, err := q.q.ClaimJobs(ctx, store.ClaimJobsParams{
		DefaultLeaseSeconds: defaultLease.Seconds(),
		MaxJobs:             int32(n), //nolint:gosec // bounded to 1..1000 above
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: claim: %w", err)
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		j := Job{ID: r.ID, Kind: r.Kind, Payload: r.Payload, Attempt: r.Attempts, MaxAttempts: r.MaxAttempts, Lease: defaultLease}
		if r.LeaseSeconds != nil {
			j.Lease = time.Duration(*r.LeaseSeconds) * time.Second
		}
		if r.LockedUntil != nil {
			j.LeaseEnds = *r.LockedUntil
		}
		out = append(out, j)
	}
	return out, nil
}

// maxErrorLen caps the stored error message so one huge error can't bloat the table.
const maxErrorLen = 2000

// Fail records a failed attempt. The job is retried after retryIn, unless it has used
// all its attempts or giveUp is set, in which case it becomes dead. dead reports which
// happened. Returns ErrLeaseLost if the job is no longer this worker's.
func (q *Queue) Fail(ctx context.Context, j Job, cause error, retryIn time.Duration, giveUp bool) (dead bool, err error) {
	msg := "unknown error"
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > maxErrorLen {
		// Cutting at a byte offset can split a multi-byte character, and Postgres rejects
		// invalid UTF-8; ToValidUTF8 drops any partial character left at the end.
		msg = strings.ToValidUTF8(msg[:maxErrorLen], "") + "…"
	}
	row, err := q.q.FailJob(ctx, store.FailJobParams{
		ID: j.ID, Attempts: j.Attempt, LastError: msg, GiveUp: giveUp, RetryInSeconds: retryIn.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("jobs: fail %d: %w", j.ID, err)
	}
	return row.State == "dead", nil
}

// Requeued describes a job the reaper took back from an expired lease.
type Requeued struct {
	ID       int64
	Kind     string
	Attempts int32
	Dead     bool // true if that was its last attempt
}

// RequeueExpired returns running jobs whose lease has passed to the queue (or to dead,
// if they have no attempts left). Workers call it periodically.
func (q *Queue) RequeueExpired(ctx context.Context) ([]Requeued, error) {
	rows, err := q.q.RequeueExpiredJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: requeue expired: %w", err)
	}
	out := make([]Requeued, 0, len(rows))
	for _, r := range rows {
		out = append(out, Requeued{ID: r.ID, Kind: r.Kind, Attempts: r.Attempts, Dead: r.State == "dead"})
	}
	return out, nil
}

// Complete marks a claimed job done. It returns ErrLeaseLost if the job was meanwhile
// re-claimed by another worker (or already finished), in which case this worker's
// result must be treated as discarded.
func (q *Queue) Complete(ctx context.Context, j Job) error {
	n, err := q.q.CompleteJob(ctx, store.CompleteJobParams{ID: j.ID, Attempts: j.Attempt})
	if err != nil {
		return fmt.Errorf("jobs: complete %d: %w", j.ID, err)
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}
