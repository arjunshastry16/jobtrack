// Package scheduler runs tasks on a fixed interval, like a tiny in-process cron.
//
// Tasks should be cheap and idempotent: typically they look for work and enqueue jobs
// (with dedupe keys), and the job queue does the heavy lifting with retries. That also
// means running two copies of the service can't cause duplicate work.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Task is something to run every Every. It runs once immediately at startup.
type Task struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context) error
}

// Scheduler runs a set of tasks.
type Scheduler struct {
	logger *slog.Logger
	tasks  []Task
}

// New returns a Scheduler for tasks.
func New(logger *slog.Logger, tasks ...Task) *Scheduler {
	for _, t := range tasks {
		if t.Every <= 0 || t.Run == nil || t.Name == "" {
			panic("scheduler: task needs a name, a positive interval and a Run func")
		}
	}
	return &Scheduler{logger: logger, tasks: tasks}
}

// Run runs every task on its interval until ctx is cancelled, then waits for any task
// that is mid-run to return. A failing (or panicking) task is logged and tried again
// at its next tick; it never stops the others.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range s.tasks {
		wg.Go(func() { s.loop(ctx, t) })
	}
	wg.Wait()
}

func (s *Scheduler) loop(ctx context.Context, t Task) {
	ticker := time.NewTicker(t.Every)
	defer ticker.Stop()
	for {
		s.runOnce(ctx, t)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context, t Task) {
	if ctx.Err() != nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			s.logger.Error("scheduled task panicked", "task", t.Name, "panic", v)
		}
	}()
	start := time.Now()
	if err := t.Run(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("scheduled task failed", "task", t.Name, "err", err)
		return
	}
	s.logger.Debug("scheduled task ran", "task", t.Name, "took", time.Since(start).String())
}
