package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"
)

// HandlerFunc does the work for one job. Delivery is at-least-once (a job can run again
// if a worker dies after doing the work but before recording it), so handlers must be
// idempotent. Return an error to retry with backoff, or Permanent(err) to give up now.
type HandlerFunc func(ctx context.Context, j Job) error

// Config tunes a Worker. Zero fields get the defaults noted.
type Config struct {
	Concurrency  int           // jobs processed in parallel (default 2)
	PollInterval time.Duration // wait between claims when the queue is empty (default 1s)
	Lease        time.Duration // how long a claimed job is reserved, unless it set its own (default 5m)
	ReapInterval time.Duration // how often expired leases are reclaimed (default 30s)
	BackoffBase  time.Duration // delay before the first retry (default 10s)
	BackoffMax   time.Duration // longest retry delay (default 1h)
	// DrainTimeout is how long jobs still running at shutdown get to finish before their
	// context is cancelled (default 30s). A cancelled job is recorded as failed and
	// retried later, so a long job can't hold up a restart.
	DrainTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Concurrency <= 0 {
		c.Concurrency = 2
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.Lease <= 0 {
		c.Lease = 5 * time.Minute
	}
	if c.ReapInterval <= 0 {
		c.ReapInterval = 30 * time.Second
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 10 * time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = time.Hour
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = 30 * time.Second
	}
	return c
}

// recordTimeout bounds the query that records a job's outcome, which runs even during shutdown.
const recordTimeout = 10 * time.Second

// Worker runs registered handlers for jobs from a Queue.
type Worker struct {
	queue    *Queue
	logger   *slog.Logger
	cfg      Config
	handlers map[string]HandlerFunc
	rnd      func() float64
}

// NewWorker returns a Worker. Register handlers with Handle, then call Run.
func NewWorker(q *Queue, logger *slog.Logger, cfg Config) *Worker {
	return &Worker{
		queue:    q,
		logger:   logger,
		cfg:      cfg.withDefaults(),
		handlers: map[string]HandlerFunc{},
		rnd:      rand.Float64, //nolint:gosec // jitter doesn't need a cryptographic RNG
	}
}

// Handle registers the handler for a job kind. Call it before Run.
func (w *Worker) Handle(kind string, h HandlerFunc) {
	if _, dup := w.handlers[kind]; dup {
		panic("jobs: duplicate handler for kind " + kind)
	}
	w.handlers[kind] = h
}

// Run processes jobs until ctx is cancelled. It then stops claiming new jobs, waits for
// the jobs already in progress to finish (drain), and returns.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("job worker started", "concurrency", w.cfg.Concurrency, "lease", w.cfg.Lease.String())
	var wg sync.WaitGroup
	for range w.cfg.Concurrency {
		wg.Go(func() { w.claimLoop(ctx) })
	}
	wg.Go(func() { w.reapLoop(ctx) })
	wg.Wait()
	w.logger.Info("job worker stopped")
}

// claimLoop is one worker slot: claim a job, run it, repeat.
func (w *Worker) claimLoop(ctx context.Context) {
	for ctx.Err() == nil {
		// Claim with a context that shutdown can't interrupt halfway: a claim that commits
		// but whose result is lost would leave the job locked until its lease expires.
		claimCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
		claimed, err := w.queue.Claim(claimCtx, 1, w.cfg.Lease)
		cancel()
		if err != nil {
			w.logger.Warn("claiming jobs failed", "err", err)
			sleep(ctx, w.cfg.PollInterval)
			continue
		}
		if len(claimed) == 0 {
			sleep(ctx, w.cfg.PollInterval)
			continue
		}
		w.process(ctx, claimed[0])
	}
}

// process runs one job and records the outcome.
func (w *Worker) process(ctx context.Context, j Job) {
	log := w.logger.With("job_id", j.ID, "kind", j.Kind, "attempt", j.Attempt)

	// The handler's context is not cancelled by shutdown straight away, so in-flight work
	// can finish (that's the drain), but only for DrainTimeout. It also ends before the
	// lease does (90% of it, by this process's clock), so a hung handler gives up before
	// the job could be handed to another worker.
	handlerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), j.Lease*9/10)
	stopDrainTimer := context.AfterFunc(ctx, func() {
		time.AfterFunc(w.cfg.DrainTimeout, cancel) // firing after the job ends is harmless
	})
	start := time.Now()
	err := w.runHandler(handlerCtx, j)
	stopDrainTimer()
	cancel()
	took := time.Since(start)

	recordCtx, cancelRecord := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancelRecord()

	if err == nil {
		switch cerr := w.queue.Complete(recordCtx, j); {
		case errors.Is(cerr, ErrLeaseLost):
			log.Warn("job finished after its lease was lost; result discarded", "took", took.String())
		case cerr != nil:
			log.Error("could not record job completion", "err", cerr)
		default:
			log.Info("job done", "took", took.String())
		}
		return
	}

	retryIn := Backoff(int(j.Attempt), w.cfg.BackoffBase, w.cfg.BackoffMax, w.rnd)
	dead, ferr := w.queue.Fail(recordCtx, j, err, retryIn, IsPermanent(err))
	switch {
	case errors.Is(ferr, ErrLeaseLost):
		log.Warn("job failed after its lease was lost", "err", err)
	case ferr != nil:
		log.Error("could not record job failure", "err", ferr, "job_err", err)
	case dead:
		log.Error("job failed for good; moved to dead", "err", err, "max_attempts", j.MaxAttempts)
	default:
		log.Warn("job failed; will retry", "err", err, "retry_in", retryIn.String())
	}
}

// runHandler calls the job's handler, turning a panic into an error so one bad job
// can't take down the worker.
func (w *Worker) runHandler(ctx context.Context, j Job) (err error) {
	h, ok := w.handlers[j.Kind]
	if !ok {
		return Permanent(fmt.Errorf("no handler registered for kind %q", j.Kind))
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v\n%s", v, debug.Stack())
		}
	}()
	return h(ctx, j)
}

// reapLoop periodically returns jobs with expired leases to the queue.
func (w *Worker) reapLoop(ctx context.Context) {
	for {
		requeued, err := w.queue.RequeueExpired(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.Warn("requeueing expired jobs failed", "err", err)
		}
		for _, r := range requeued {
			w.logger.Warn("job lease expired; requeued",
				"job_id", r.ID, "kind", r.Kind, "attempts", r.Attempts, "dead", r.Dead)
		}
		if !sleep(ctx, w.cfg.ReapInterval) {
			return
		}
	}
}

// sleep waits for d or until ctx is cancelled. It reports whether the full wait elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
