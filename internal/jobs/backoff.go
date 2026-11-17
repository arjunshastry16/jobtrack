package jobs

import (
	"errors"
	"time"
)

// Backoff returns how long to wait before retrying after the given failed attempt
// (1-based): exponential growth from base, capped at maxDelay, with "equal jitter":
// half the delay is fixed and half is random. The fixed half guarantees the delay still
// grows; the random half spreads retries out so many jobs that failed together (say,
// during an outage) don't all retry at the same instant and cause another spike.
//
// rnd must return a value in [0, 1); pass rand.Float64 (tests pass a fixed function).
func Backoff(attempt int, base, maxDelay time.Duration, rnd func() float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2 // doubling in a loop (not base << attempt) can't overflow past maxDelay
	}
	d = min(d, maxDelay)
	half := d / 2
	return half + time.Duration(rnd()*float64(d-half))
}

// permanentError marks an error that retrying cannot fix.
type permanentError struct{ err error }

func (p *permanentError) Error() string { return "permanent: " + p.err.Error() }
func (p *permanentError) Unwrap() error { return p.err }

// Permanent wraps err so the worker sends the job straight to the dead state instead of
// retrying it (for example: the payload is malformed, or the record it refers to was deleted).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err (or anything it wraps) was marked with Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}
