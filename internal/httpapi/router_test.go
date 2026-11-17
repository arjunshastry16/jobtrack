package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeDB is a Pinger whose result the test controls.
type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newTestRouter(db Pinger) http.Handler {
	return NewRouter(Deps{
		Version: "test-version",
		DB:      db,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// do sends a request through the router and returns the recorded response.
func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	// Liveness must not depend on the database, so give it a broken one.
	rec := do(t, newTestRouter(fakeDB{err: errors.New("down")}), http.MethodGet, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" || body.Version != "test-version" {
		t.Errorf("body = %+v, want status=ok version=test-version", body)
	}
}

func TestHealthzRejectsWrongMethod(t *testing.T) {
	rec := do(t, newTestRouter(fakeDB{}), http.MethodPost, "/healthz")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name       string
		dbErr      error
		wantStatus int
		wantBody   string
	}{
		{name: "database reachable", dbErr: nil, wantStatus: http.StatusOK, wantBody: "ready"},
		{name: "database down", dbErr: errors.New("connection refused"), wantStatus: http.StatusServiceUnavailable, wantBody: "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newTestRouter(fakeDB{err: tt.dbErr}), http.MethodGet, "/readyz")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body struct {
				Status string `json:"status"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Status != tt.wantBody {
				t.Errorf("status field = %q, want %q", body.Status, tt.wantBody)
			}
		})
	}
}
