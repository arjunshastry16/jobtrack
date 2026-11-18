package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/requestid"
)

// A middleware wraps a handler to add behaviour before and/or after it runs.
//
// The chain (outermost first) is built in NewRouter:
//
//	withRequestID -> withTimeout -> accessLog -> recoverPanics -> requireLogin -> ServeMux
//
// Order matters: the request ID must exist before anything logs; accessLog must sit
// outside recoverPanics so it logs the 500 a panic turns into, and outside requireLogin
// so refused requests are logged too; and nothing between
// accessLog and the ServeMux may copy the request, because ServeMux records the
// matched route pattern (r.Pattern) on the *http.Request it receives.

// withRequestID reuses a valid incoming X-Request-ID (e.g. from a proxy) or makes a new
// one, returns it in the response header and stores it in the request context.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestid.Header)
		if !requestid.Valid(id) {
			id = requestid.New()
		}
		w.Header().Set(requestid.Header, id)
		next.ServeHTTP(w, r.WithContext(requestid.NewContext(r.Context(), id)))
	})
}

// withTimeout gives every request a deadline. Handlers pass r.Context() to the database,
// so a slow query is cancelled instead of tying up a connection forever.
func withTimeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder wraps a ResponseWriter to remember the status code and body size.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK // Write without WriteHeader implies 200
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the real writer (e.g. to flush).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) wroteHeader() bool { return s.status != 0 }

// probePaths are hit every few seconds by health checks; logging them at Info would drown real traffic.
var probePaths = map[string]bool{"/healthz": true, "/readyz": true}

// accessLog writes one structured line per request after it completes.
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK // handler wrote nothing at all
		}
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case probePaths[r.URL.Path]:
			level = slog.LevelDebug
		}
		logger.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			// Who asked (the Tailscale login, "" without one); refused requests included.
			slog.String("user", tailscaleLogin(r)),
			slog.String("path", r.URL.Path), // no query string: it may hold personal data
			slog.String("route", r.Pattern), // e.g. "GET /api/v1/companies/{id}": low-cardinality, good for grouping
			slog.Int("status", status),
			slog.Int("bytes", rec.bytes),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		)
	})
}

// recoverPanics turns a panic in a handler into a logged 500 instead of a dropped
// connection, and keeps the server running.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			// http.ErrAbortHandler is net/http's deliberate "abort this response" signal; let it through.
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			logger.ErrorContext(r.Context(), "panic in handler",
				"panic", fmt.Sprint(v), "stack", string(debug.Stack()))

			// If the handler already started its response we can't change the status; just stop.
			if rec, ok := w.(*statusRecorder); ok && rec.wroteHeader() {
				return
			}
			writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}
