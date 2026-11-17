// Package notifytest provides a fake Notifier for tests.
package notifytest

import (
	"context"
	"slices"
	"sync"

	"github.com/arjunshastry16/jobtrack/internal/notify"
)

// Recorder is a notify.Notifier that stores messages instead of sending them. It is
// safe for concurrent use. Set Err to make every Notify call fail with it.
type Recorder struct {
	mu   sync.Mutex
	msgs []notify.Message
	Err  error
}

var _ notify.Notifier = (*Recorder)(nil)

// Notify records m (unless Err is set, in which case it returns Err and records nothing).
func (r *Recorder) Notify(_ context.Context, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.msgs = append(r.msgs, m)
	return nil
}

// Messages returns a copy of everything recorded so far.
func (r *Recorder) Messages() []notify.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs)
}
