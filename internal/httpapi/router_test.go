package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeDB is a Pinger whose result the test controls.
type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newTestRouter(db Pinger) http.Handler {
	return viaServe(NewRouter(Deps{
		Version: "test-version",
		DB:      db,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:    testAuth,
	}))
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

// The web app and the API share one server: app pages get index.html, API paths never do.
func TestRouterServesTheWebApp(t *testing.T) {
	const index = "<!doctype html><title>jobtrack</title>"
	h := viaServe(NewRouter(Deps{
		Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		UI: fstest.MapFS{"index.html": {Data: []byte(index)}}, Auth: testAuth,
	}))

	for _, path := range []string{"/", "/applications/12", "/postings"} {
		if rec := do(t, h, http.MethodGet, path); rec.Code != http.StatusOK || rec.Body.String() != index {
			t.Errorf("GET %s: %d %q, want the app", path, rec.Code, rec.Body.String())
		}
	}

	// Unknown API paths are the API's JSON 404, not the app.
	for _, path := range []string{"/api", "/api/", "/api/v1/nope", "/api/v2/companies"} {
		rec := do(t, h, http.MethodGet, path)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		if rec.Code != http.StatusNotFound || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error.Code != "not_found" {
			t.Errorf("GET %s: %d %q, want a JSON not_found", path, rec.Code, rec.Body.String())
		}
	}

	// Real endpoints still win over the catch-all, and wrong methods still get 405.
	if rec := do(t, h, http.MethodGet, "/healthz"); !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("/healthz = %q", rec.Body.String())
	}
	if rec := do(t, h, http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/postings"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/v1/postings = %d, want 405", rec.Code)
	}
}
