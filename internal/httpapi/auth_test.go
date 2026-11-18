package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// testLogin is the Tailscale login the test routers allow.
const testLogin = "me@github"

var testAuth = Auth{Logins: []string{testLogin}}

// viaServe plays tailscale serve in tests: it sets the caller's login on every request
// that doesn't already carry one, so tests exercise the real login check.
func viaServe(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headerLogin) == "" {
			r.Header.Set(headerLogin, testLogin)
		}
		h.ServeHTTP(w, r)
	})
}

func authRouter(auth Auth) http.Handler {
	return NewRouter(Deps{
		Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		UI: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}, Auth: auth,
	})
}

// errorCode is the code of an error response, or "" for any other body.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorResponse
	if json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		return ""
	}
	return body.Error.Code
}

func TestRequireLogin(t *testing.T) {
	friends := Auth{Logins: []string{testLogin, "Friend@Example.com"}}
	tests := []struct {
		name     string
		auth     Auth
		path     string
		login    string // "" sends no header
		want     int
		wantCode string
	}{
		{"allowed login", friends, "/api/v1/openapi.yaml", testLogin, http.StatusOK, ""},
		{"login case doesn't matter", friends, "/api/v1/openapi.yaml", "ME@GitHub", http.StatusOK, ""},
		{"list case doesn't matter", friends, "/api/v1/openapi.yaml", "friend@example.com", http.StatusOK, ""},
		{"RFC 2047 encoded login", friends, "/api/v1/openapi.yaml", "=?utf-8?q?me=40github?=", http.StatusOK, ""},
		{"no login", friends, "/api/v1/openapi.yaml", "", http.StatusUnauthorized, codeUnauthenticated},
		{"blank login", friends, "/api/v1/openapi.yaml", "  ", http.StatusUnauthorized, codeUnauthenticated},
		{"login not on the list", friends, "/api/v1/openapi.yaml", "stranger@github", http.StatusForbidden, codeForbidden},
		{"a prefix isn't a match", friends, "/api/v1/openapi.yaml", "me@githubx", http.StatusForbidden, codeForbidden},
		{"web app needs a login too", friends, "/postings", "", http.StatusUnauthorized, codeUnauthenticated},
		{"unknown API path needs a login first", friends, "/api/v1/nope", "", http.StatusUnauthorized, codeUnauthenticated},
		{"liveness probe stays open", friends, "/healthz", "", http.StatusOK, ""},
		{"readiness probe stays open", friends, "/readyz", "", http.StatusOK, ""},
		{"probe-like path isn't open", friends, "/healthz/", "", http.StatusUnauthorized, codeUnauthenticated},
		{"zero Auth lets nobody in", Auth{}, "/api/v1/openapi.yaml", testLogin, http.StatusForbidden, codeForbidden},
		{"zero Auth still answers probes", Auth{}, "/healthz", "", http.StatusOK, ""},
		{"off lets everyone in", Auth{Off: true}, "/api/v1/openapi.yaml", "", http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			if tt.login != "" {
				req.Header.Set(headerLogin, tt.login)
			}
			rec := httptest.NewRecorder()
			authRouter(tt.auth).ServeHTTP(rec, req)
			if rec.Code != tt.want || errorCode(t, rec) != tt.wantCode {
				t.Errorf("GET %s as %q: %d %s, want %d %q", tt.path, tt.login, rec.Code, rec.Body.String(), tt.want, tt.wantCode)
			}
		})
	}
}

// The refusals are documented in the spec, so contract-checked clients can rely on them.
func TestRequireLoginMatchesSpec(t *testing.T) {
	h := contractChecked(t, authRouter(testAuth))
	for _, login := range []string{"", "stranger@github"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/stats", nil)
		if login != "" {
			req.Header.Set(headerLogin, login)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

// TestRefusalsAreLogged: a refused request still gets an access log line, with the login
// that was refused.
func TestRefusalsAreLogged(t *testing.T) {
	logger, logs := newCapturingLogger()
	h := NewRouter(Deps{Version: "v", DB: fakeDB{}, Logger: logger, Auth: testAuth})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set(headerLogin, "stranger@github")
	h.ServeHTTP(httptest.NewRecorder(), req)

	line := logs.find(t, "http request")
	if line == nil || line["status"] != float64(http.StatusForbidden) || line["user"] != "stranger@github" {
		t.Errorf("access log = %v, want status 403 and user stranger@github", line)
	}
}

// TestSpecDocumentsLogin: every operation but the health probes needs a login, and
// documents the 401 and 403 it can return; the probes are marked as open.
func TestSpecDocumentsLogin(t *testing.T) {
	doc, _ := loadSpec(t)
	if len(doc.Security) != 1 || doc.Security[0]["tailscale"] == nil {
		t.Errorf("spec security = %v, want the tailscale scheme for every operation", doc.Security)
	}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			name := method + " " + path
			if probePaths[path] {
				if op.Security == nil || len(*op.Security) != 0 {
					t.Errorf("%s: want security: [] (probes need no login)", name)
				}
				continue
			}
			if op.Security != nil {
				t.Errorf("%s: overrides security; every operation but the probes needs a login", name)
			}
			for _, code := range []string{"401", "403"} {
				if op.Responses.Value(code) == nil {
					t.Errorf("%s: %s response not documented", name, code)
				}
			}
		}
	}
}

func TestTailscaleLogin(t *testing.T) {
	for header, want := range map[string]string{
		"":                                    "",
		"me@github":                           "me@github",
		" me@github ":                         "me@github",
		"=?utf-8?q?J=C3=B6rg=40example.com?=": "Jörg@example.com",
		"=?utf-8?q?broken":                    "=?utf-8?q?broken", // not a valid encoded word: used as is
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set(headerLogin, header)
		if got := tailscaleLogin(req); got != want {
			t.Errorf("tailscaleLogin(%q) = %q, want %q", header, got, want)
		}
	}
}
