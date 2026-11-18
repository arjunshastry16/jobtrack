// Package webui serves the built frontend (web/) from inside the Go binary, so one
// binary is the whole app.
//
// The files are embedded only when building with -tags webui (`make build` builds the
// frontend first, then the binary with the tag). Without the tag, the Go code still
// builds, tests and lints without Node or a frontend build; the server then answers
// non-API paths with a note that the UI isn't included.
package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// contentSecurityPolicy only allows the app's own scripts, styles and API calls. Vite's
// build has no inline scripts or styles, so nothing needs 'unsafe-inline'. Postings are
// plain links to job boards, which CSP doesn't restrict.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; " +
	"form-action 'self'; frame-ancestors 'none'"

// Handler serves the frontend from files:
//   - a file that exists is served; Vite's content-hashed files under assets/ are cached
//     for a year, since a new build gives them new names;
//   - a path with a file extension that doesn't exist is a 404 (a missing script must
//     not come back as HTML);
//   - anything else is a page of the app (e.g. /applications/12, handled by the
//     frontend's router), so it gets index.html, which is never cached, so a deploy
//     takes effect on the next page load.
//
// files == nil (or without index.html) means the UI isn't built into this binary.
func Handler(files fs.FS) http.Handler {
	if files == nil {
		return notBuilt
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return notBuilt
	}
	fileServer := http.FileServerFS(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

		if name != "" && name != "index.html" {
			if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// ServeContent handles HEAD, conditional requests and ranges.
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}

func setSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	// Clicking through to a job posting shouldn't tell the job board this server's address.
	h.Set("Referrer-Policy", "no-referrer")
}

var notBuilt = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("This build of jobtrack doesn't include the web UI.\n" +
		"Build it with `make build`, or use `make web-dev` while developing.\n"))
})
