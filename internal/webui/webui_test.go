package webui_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/arjunshastry16/jobtrack/internal/webui"
)

const indexHTML = `<!doctype html><script type="module" src="/assets/index-abc123.js"></script>`

var built = fstest.MapFS{
	"index.html":              {Data: []byte(indexHTML)},
	"assets/index-abc123.js":  {Data: []byte("console.log('app')")},
	"assets/index-abc123.css": {Data: []byte("body{}")},
	"favicon.svg":             {Data: []byte("<svg/>")},
}

// response is what a test needs from a response; get reads and closes the body.
type response struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, h http.Handler, method, target string) (response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return response{StatusCode: res.StatusCode, Header: res.Header}, string(body)
}

func TestServesTheApp(t *testing.T) {
	h := webui.Handler(built)
	tests := []struct {
		name, path  string
		wantStatus  int
		wantBody    string // exact, when set
		wantType    string // prefix of Content-Type
		wantCaching string
	}{
		{"root is the app", "/", 200, indexHTML, "text/html", "no-cache"},
		{"index.html itself", "/index.html", 200, indexHTML, "text/html", "no-cache"},
		{"client-side route", "/applications/12", 200, indexHTML, "text/html", "no-cache"},
		{"route with a query", "/postings?watch=3", 200, indexHTML, "text/html", "no-cache"},
		{"hashed asset", "/assets/index-abc123.js", 200, "console.log('app')", "text/javascript", "public, max-age=31536000, immutable"},
		{"hashed stylesheet", "/assets/index-abc123.css", 200, "body{}", "text/css", "public, max-age=31536000, immutable"},
		{"other file", "/favicon.svg", 200, "<svg/>", "image/svg+xml", ""},
		{"missing asset is a 404, not the app", "/assets/gone-999.js", 404, "", "text/plain", ""},
		{"directory is the app, not a listing", "/assets/", 200, indexHTML, "text/html", "no-cache"},
		{"dot-dot can't escape", "/../../etc/passwd", 200, indexHTML, "text/html", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := get(t, h, http.MethodGet, tt.path)
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", res.StatusCode, tt.wantStatus, body)
			}
			if tt.wantBody != "" && body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, tt.wantType) {
				t.Errorf("Content-Type = %q, want %s…", ct, tt.wantType)
			}
			if cc := res.Header.Get("Cache-Control"); cc != tt.wantCaching {
				t.Errorf("Cache-Control = %q, want %q", cc, tt.wantCaching)
			}
			if !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") ||
				res.Header.Get("X-Content-Type-Options") != "nosniff" ||
				res.Header.Get("Referrer-Policy") != "no-referrer" {
				t.Errorf("security headers missing: %v", res.Header)
			}
		})
	}
}

func TestHeadRequest(t *testing.T) {
	res, body := get(t, webui.Handler(built), http.MethodHead, "/applications/1")
	if res.StatusCode != 200 || body != "" || res.Header.Get("Content-Length") != strconv.Itoa(len(indexHTML)) {
		t.Errorf("HEAD: status %d, body %q, length %q", res.StatusCode, body, res.Header.Get("Content-Length"))
	}
}

func TestWithoutTheUI(t *testing.T) {
	for name, files := range map[string]fs.FS{
		"not built in":  nil,
		"no index.html": fstest.MapFS{"assets/x.js": {Data: []byte("x")}},
	} {
		t.Run(name, func(t *testing.T) {
			res, body := get(t, webui.Handler(files), http.MethodGet, "/")
			if res.StatusCode != 404 || !strings.Contains(body, "make build") {
				t.Errorf("status %d, body %q", res.StatusCode, body)
			}
		})
	}
}
