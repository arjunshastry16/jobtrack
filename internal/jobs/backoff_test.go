package jobs_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/jobs"
)

func TestBackoff(t *testing.T) {
	const base, maxDelay = 10 * time.Second, 5 * time.Minute
	low := func() float64 { return 0 }       // smallest jitter
	high := func() float64 { return 0.9999 } // (almost) largest jitter

	tests := []struct {
		attempt int
		full    time.Duration // un-jittered delay for this attempt
	}{
		{0, base}, // treated as attempt 1
		{1, base},
		{2, 2 * base},
		{3, 4 * base},
		{5, 16 * base},
		{6, maxDelay}, // 320s would exceed the cap
		{1000, maxDelay},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint("attempt ", tt.attempt), func(t *testing.T) {
			// Equal jitter: always between half and all of the full delay.
			if got := jobs.Backoff(tt.attempt, base, maxDelay, low); got != tt.full/2 {
				t.Errorf("min = %s, want %s", got, tt.full/2)
			}
			if got := jobs.Backoff(tt.attempt, base, maxDelay, high); got < tt.full*99/100 || got > tt.full {
				t.Errorf("max = %s, want just under %s", got, tt.full)
			}
		})
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("bad payload")
	p := jobs.Permanent(base)
	if !jobs.IsPermanent(p) || !errors.Is(p, base) {
		t.Errorf("Permanent(err) = %v: IsPermanent %v, Is(base) %v", p, jobs.IsPermanent(p), errors.Is(p, base))
	}
	if !jobs.IsPermanent(fmt.Errorf("handler: %w", p)) {
		t.Error("wrapping a permanent error lost the mark")
	}
	if jobs.IsPermanent(base) || jobs.Permanent(nil) != nil {
		t.Error("plain errors must not be permanent; Permanent(nil) must be nil")
	}
}
