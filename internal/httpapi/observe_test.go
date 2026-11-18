package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type observed struct {
	pattern string
	status  int
}

// fakeObserver records what the router reports, as metrics.Metrics would.
type fakeObserver struct {
	mu  sync.Mutex
	got []observed
}

func (f *fakeObserver) ObserveRequest(pattern string, status int, took time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if took < 0 {
		status = -1 // makes the comparison fail
	}
	f.got = append(f.got, observed{pattern, status})
}

func (f *fakeObserver) last() observed {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		return observed{}
	}
	return f.got[len(f.got)-1]
}

// TestRouterReportsEveryRequest checks each request reaches the observer with the route
// pattern that matched (the metrics' route label), including requests refused before
// routing, which have none.
func TestRouterReportsEveryRequest(t *testing.T) {
	obs := &fakeObserver{}
	h := NewRouter(Deps{
		Version: "test", DB: fakeDB{}, Logger: slog.New(slog.DiscardHandler),
		UI:   fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}},
		Auth: testAuth, Observer: obs,
	})
	tests := []struct {
		method, path, login string
		want                observed
	}{
		{"GET", "/healthz", "", observed{"GET /healthz", 200}},
		{"GET", "/api/v1/openapi.yaml", testLogin, observed{"GET /api/v1/openapi.yaml", 200}},
		{"GET", "/applications", testLogin, observed{"GET /", 200}}, // a web app page
		{"GET", "/api/v1/stats", "", observed{"", 401}},
		{"GET", "/api/v1/stats", "stranger@example.com", observed{"", 403}},
		{"POST", "/healthz", "", observed{"", 405}},
	}
	for _, tt := range tests {
		req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
		if tt.login != "" {
			req.Header.Set(headerLogin, tt.login)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want.status {
			t.Errorf("%s %s: status %d, want %d", tt.method, tt.path, rec.Code, tt.want.status)
		}
		if got := obs.last(); got != tt.want {
			t.Errorf("%s %s: observed %+v, want %+v", tt.method, tt.path, got, tt.want)
		}
	}
	if len(obs.got) != len(tests) {
		t.Errorf("observed %d requests, want one per request (%d)", len(obs.got), len(tests))
	}
}

func TestPanicsAreObservedAs500(t *testing.T) {
	obs := &fakeObserver{}
	logger := slog.New(slog.DiscardHandler)
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := accessLog(logger, obs, recoverPanics(logger, boom))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))
	if got := obs.last(); got != (observed{"", http.StatusInternalServerError}) {
		t.Errorf("observed %+v, want a 500", got)
	}
}

// A handler that writes nothing has answered 200 (net/http sends it), and must be
// counted as that, not as status 0.
func TestSilentHandlerObservedAs200(t *testing.T) {
	obs := &fakeObserver{}
	silent := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := accessLog(slog.New(slog.DiscardHandler), obs, silent)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))
	if got := obs.last(); got != (observed{"", http.StatusOK}) {
		t.Errorf("observed %+v, want a 200", got)
	}
}
