package scheduler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/scheduler"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRunsImmediatelyThenOnInterval(t *testing.T) {
	var runs atomic.Int32
	s := scheduler.New(slog.New(slog.DiscardHandler), scheduler.Task{
		Name: "count", Every: 30 * time.Millisecond,
		Run: func(context.Context) error { runs.Add(1); return nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.Run(ctx) // returns when ctx expires

	// t=0 (immediate), ~30, ~60, ~90ms: allow for timer slop on a busy machine.
	if n := runs.Load(); n < 3 || n > 5 {
		t.Errorf("task ran %d times in 100ms at a 30ms interval, want about 4", n)
	}
}

func TestFailuresAndPanicsDontStopTasks(t *testing.T) {
	logs := &syncBuf{}
	var flaky, steady atomic.Int32
	s := scheduler.New(slog.New(slog.NewTextHandler(logs, nil)),
		scheduler.Task{Name: "flaky", Every: 20 * time.Millisecond, Run: func(context.Context) error {
			if flaky.Add(1)%2 == 1 {
				panic("boom")
			}
			return errors.New("db down")
		}},
		scheduler.Task{Name: "steady", Every: 20 * time.Millisecond, Run: func(context.Context) error {
			steady.Add(1)
			return nil
		}},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	s.Run(ctx)

	if flaky.Load() < 3 || steady.Load() < 3 {
		t.Errorf("runs: flaky %d, steady %d; want both to keep running", flaky.Load(), steady.Load())
	}
	for _, want := range []string{"scheduled task panicked", "scheduled task failed", "db down"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs missing %q", want)
		}
	}
}

func TestRunWaitsForTaskInProgress(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	s := scheduler.New(slog.New(slog.DiscardHandler), scheduler.Task{
		Name: "slow", Every: time.Hour,
		Run: func(context.Context) error {
			close(started)
			time.Sleep(100 * time.Millisecond)
			finished.Store(true)
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	<-started
	cancel()
	<-done
	if !finished.Load() {
		t.Error("Run returned before the in-progress task finished")
	}
}

func TestNewRejectsBadTasks(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a task with no interval was accepted")
		}
	}()
	scheduler.New(slog.New(slog.DiscardHandler), scheduler.Task{Name: "x", Run: func(context.Context) error { return nil }})
}
