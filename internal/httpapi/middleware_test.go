package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/requestid"
)

// logCapture collects JSON log lines (safe for concurrent writes).
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// lines decodes every captured log line.
func (l *logCapture) lines(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// find returns the first line whose msg equals msg, or nil.
func (l *logCapture) find(t *testing.T, msg string) map[string]any {
	t.Helper()
	for _, m := range l.lines(t) {
		if m["msg"] == msg {
			return m
		}
	}
	return nil
}

// newCapturingLogger logs everything (Debug and up) as JSON, with request IDs, into a logCapture.
func newCapturingLogger() (*slog.Logger, *logCapture) {
	c := &logCapture{}
	h := slog.NewJSONHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(requestid.LogHandler{Handler: h}), c
}

func TestRequestIDHeader(t *testing.T) {
	h := newTestRouter(fakeDB{})
	tests := []struct {
		name     string
		incoming string
		wantSame bool
	}{
		{name: "generated when absent", incoming: ""},
		{name: "valid incoming is reused", incoming: "abc-123", wantSame: true},
		{name: "invalid incoming is replaced", incoming: "bad id\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
			if tt.incoming != "" {
				req.Header.Set(requestid.Header, tt.incoming)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(requestid.Header)
			if !requestid.Valid(got) {
				t.Fatalf("response %s = %q, want a valid ID", requestid.Header, got)
			}
			if tt.wantSame && got != tt.incoming {
				t.Errorf("ID = %q, want incoming %q", got, tt.incoming)
			}
			if !tt.wantSame && got == tt.incoming {
				t.Errorf("ID = %q, want a newly generated one", got)
			}
		})
	}
}

func TestAccessLog(t *testing.T) {
	logger, logs := newCapturingLogger()
	h := viaServe(NewRouter(Deps{Version: "v", DB: fakeDB{}, Logger: logger, Auth: testAuth}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/companies/abc?q=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	line := logs.find(t, "http request")
	if line == nil {
		t.Fatalf("no access log line; logs: %v", logs.lines(t))
	}
	want := map[string]any{
		"level":      "INFO",
		"method":     "GET",
		"path":       "/api/v1/companies/abc", // query string deliberately dropped
		"route":      "GET /api/v1/companies/{id}",
		"status":     float64(http.StatusBadRequest), // JSON numbers decode as float64
		"request_id": rec.Header().Get(requestid.Header),
		"user":       testLogin,
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("log[%q] = %v, want %v", k, line[k], v)
		}
	}
	if b, _ := line["bytes"].(float64); b <= 0 {
		t.Errorf("log[bytes] = %v, want > 0", line["bytes"])
	}
	if _, ok := line["duration_ms"].(float64); !ok {
		t.Errorf("log[duration_ms] missing: %v", line)
	}
}

func TestAccessLogProbesAtDebug(t *testing.T) {
	logger, logs := newCapturingLogger()
	h := NewRouter(Deps{Version: "v", DB: fakeDB{}, Logger: logger, Auth: testAuth})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	line := logs.find(t, "http request")
	if line == nil || line["level"] != "DEBUG" {
		t.Errorf("probe access log = %v, want level DEBUG", line)
	}
}

func TestRecoverPanics(t *testing.T) {
	logger, logs := newCapturingLogger()
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := withRequestID(accessLog(logger, recoverPanics(logger, boom)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Error.Code != codeInternal {
		t.Errorf("body = %+v (err %v), want code %q", body, err, codeInternal)
	}

	p := logs.find(t, "panic in handler")
	if p == nil || p["panic"] != "boom" || !strings.Contains(p["stack"].(string), "middleware_test.go") {
		t.Errorf("panic log = %v, want panic=boom with a stack trace", p)
	}
	if p["request_id"] != rec.Header().Get(requestid.Header) {
		t.Errorf("panic log request_id = %v, want %q", p["request_id"], rec.Header().Get(requestid.Header))
	}
	if a := logs.find(t, "http request"); a == nil || a["status"] != float64(500) || a["level"] != "ERROR" {
		t.Errorf("access log = %v, want status 500 at ERROR", a)
	}
}

func TestRecoverPanicsAfterResponseStarted(t *testing.T) {
	logger, logs := newCapturingLogger()
	h := accessLog(logger, recoverPanics(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("late boom")
	})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))

	// The status line was already sent, so it can't become 500, and no error JSON is appended.
	if rec.Code != http.StatusOK || rec.Body.String() != "partial" {
		t.Errorf("got %d %q, want 200 \"partial\"", rec.Code, rec.Body.String())
	}
	if logs.find(t, "panic in handler") == nil {
		t.Error("late panic was not logged")
	}
}

func TestRecoverPanicsLetsAbortHandlerThrough(t *testing.T) {
	logger, _ := newCapturingLogger()
	h := recoverPanics(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		v := recover()
		if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler to be re-panicked", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))
	t.Error("ServeHTTP returned normally; want re-panic")
}

func TestWithTimeout(t *testing.T) {
	const d = 50 * time.Millisecond
	var gotErr error
	var elapsed time.Duration
	// The clock starts before the request: the deadline is set by the middleware, before
	// the handler runs, so timing from inside the handler would read slightly short.
	start := time.Now()
	h := withTimeout(d, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // stands in for a slow DB query that honours the context
		elapsed = time.Since(start)
		gotErr = r.Context().Err()
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil))

	if !errors.Is(gotErr, context.DeadlineExceeded) {
		t.Errorf("ctx.Err() = %v, want DeadlineExceeded", gotErr)
	}
	if elapsed < d || elapsed > 2*time.Second {
		t.Errorf("handler was cancelled after %s, want about %s", elapsed, d)
	}
}
