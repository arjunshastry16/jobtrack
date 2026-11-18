// Package pace spaces out calls to someone else's server: one at a time, at least a set
// gap apart, so jobtrack never sends a burst of requests.
package pace

import (
	"context"
	"sync"
	"time"
)

// Gate lets one call through at a time, each starting at least gap after the previous
// one ended. The zero value is not usable; call New.
type Gate struct {
	gap time.Duration
	// slot holds the one call in progress. Unlike a sync.Mutex, waiting for it can be
	// abandoned when the context ends.
	slot chan struct{}
	last time.Time // when the previous call ended; only touched while holding slot
}

// New returns a Gate with the given gap between calls.
func New(gap time.Duration) *Gate {
	return &Gate{gap: gap, slot: make(chan struct{}, 1)}
}

// Do waits for its turn, then runs fn and returns its error. If ctx ends while waiting,
// fn isn't run and ctx's error is returned.
func (g *Gate) Do(ctx context.Context, fn func() error) error {
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		g.last = time.Now()
		<-g.slot
	}()
	if !g.last.IsZero() {
		if err := Sleep(ctx, g.gap-time.Since(g.last)); err != nil {
			return err
		}
	}
	return fn()
}

// Gates hands out one Gate per key (e.g. per host), created on first use.
type Gates struct {
	gap   time.Duration
	mu    sync.Mutex
	gates map[string]*Gate
}

// NewGates returns a Gates whose gates all use gap.
func NewGates(gap time.Duration) *Gates {
	return &Gates{gap: gap, gates: map[string]*Gate{}}
}

// For returns the Gate for key.
func (g *Gates) For(key string) *Gate {
	g.mu.Lock()
	defer g.mu.Unlock()
	gate, ok := g.gates[key]
	if !ok {
		gate = New(g.gap)
		g.gates[key] = gate
	}
	return gate
}

// Sleep waits for d, or until ctx ends (returning its error). d <= 0 doesn't wait.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
