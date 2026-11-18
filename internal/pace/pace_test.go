package pace_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/pace"
)

func TestGateOneAtATimeWithGap(t *testing.T) {
	const gap = 20 * time.Millisecond
	g := pace.New(gap)
	var (
		inFlight, maxSeen atomic.Int32
		mu                sync.Mutex
		spans             [][2]time.Time // start and end of each call
		wg                sync.WaitGroup
	)
	for range 5 {
		wg.Go(func() {
			err := g.Do(t.Context(), func() error {
				n := inFlight.Add(1)
				defer inFlight.Add(-1)
				recordMax(&maxSeen, n)
				start := time.Now()
				time.Sleep(2 * time.Millisecond)
				mu.Lock()
				spans = append(spans, [2]time.Time{start, time.Now()})
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	if maxSeen.Load() != 1 {
		t.Errorf("%d calls ran at once, want 1", maxSeen.Load())
	}
	slices.SortFunc(spans, func(a, b [2]time.Time) int { return a[0].Compare(b[0]) })
	for i := 1; i < len(spans); i++ {
		if gapSeen := spans[i][0].Sub(spans[i-1][1]); gapSeen < gap {
			t.Errorf("call %d started %s after the previous one ended, want >= %s", i, gapSeen, gap)
		}
	}
}

// recordMax raises *m to n if n is larger, safely under concurrent calls.
func recordMax(m *atomic.Int32, n int32) {
	for {
		cur := m.Load()
		if n <= cur || m.CompareAndSwap(cur, n) {
			return
		}
	}
}

func TestGateReturnsFnError(t *testing.T) {
	boom := errors.New("boom")
	if err := pace.New(0).Do(t.Context(), func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestGateGivesUpWaitingWhenContextEnds(t *testing.T) {
	g := pace.New(time.Hour) // the second call would have to wait an hour
	_ = g.Do(t.Context(), func() error { return nil })

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	ran := false
	start := time.Now()
	err := g.Do(ctx, func() error { ran = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || ran || time.Since(start) > time.Second {
		t.Errorf("err %v, ran %v, after %s", err, ran, time.Since(start))
	}

	// Also while another call holds the gate.
	busy := pace.New(0)
	release := make(chan struct{})
	go func() { _ = busy.Do(context.Background(), func() error { <-release; return nil }) }()
	time.Sleep(5 * time.Millisecond)
	ctx2, cancel2 := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel2()
	if err := busy.Do(ctx2, func() error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waiting behind a busy call: err %v", err)
	}
	close(release)
}

func TestGatesArePerKey(t *testing.T) {
	gs := pace.NewGates(time.Hour)
	if a1, a2 := gs.For("a"), gs.For("a"); a1 != a2 {
		t.Error("same key gave different gates")
	}
	// A different key isn't held up by the first key's gap.
	_ = gs.For("a").Do(t.Context(), func() error { return nil })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := gs.For("b").Do(ctx, func() error { return nil }); err != nil {
		t.Errorf("key b waited on key a: %v", err)
	}
}
