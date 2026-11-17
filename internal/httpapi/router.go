// Package httpapi wires HTTP routes to handlers.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/arjunshastry16/jobtrack/docs"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

// Pinger is anything that can check it can reach the database. *pgxpool.Pool satisfies it;
// tests pass a fake. Depending on a small interface instead of the concrete pool keeps
// handlers easy to test.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the dependencies the handlers need.
type Deps struct {
	Version string
	DB      Pinger
	Store   store.DB
	Logger  *slog.Logger
	// RequestTimeout bounds each request; zero means defaultRequestTimeout.
	RequestTimeout time.Duration
}

// defaultRequestTimeout is below the server's 15s WriteTimeout, so a slow handler gets
// a clean cancellation (and a logged error) rather than a silently cut connection.
const defaultRequestTimeout = 10 * time.Second

// api holds what the resource handlers share.
type api struct {
	store  store.DB
	logger *slog.Logger
}

// route is one endpoint. Patterns use Go 1.22+ ServeMux syntax, which for our paths is
// the same as OpenAPI's ({id}); a test checks this table against docs/openapi.yaml.
type route struct {
	method, pattern string
	handler         http.HandlerFunc
}

func routes(a *api, d Deps) []route {
	return []route{
		{"GET", "/healthz", handleHealthz(d.Version)},
		{"GET", "/readyz", handleReadyz(d.DB, d.Logger)},
		{"GET", "/api/v1/openapi.yaml", handleOpenAPISpec},

		{"POST", "/api/v1/companies", a.createCompany},
		{"GET", "/api/v1/companies", a.listCompanies},
		{"GET", "/api/v1/companies/{id}", a.getCompany},
		{"PUT", "/api/v1/companies/{id}", a.updateCompany},
		{"DELETE", "/api/v1/companies/{id}", a.deleteCompany},

		{"POST", "/api/v1/applications", a.createApplication},
		{"GET", "/api/v1/applications", a.listApplications},
		{"GET", "/api/v1/applications/{id}", a.getApplication},
		{"PUT", "/api/v1/applications/{id}", a.updateApplication},
		{"DELETE", "/api/v1/applications/{id}", a.deleteApplication},
		{"POST", "/api/v1/applications/{id}/transitions", a.transitionApplication},
		{"GET", "/api/v1/applications/{id}/timeline", a.applicationTimeline},

		{"POST", "/api/v1/contacts", a.createContact},
		{"GET", "/api/v1/contacts", a.listContacts},
		{"GET", "/api/v1/contacts/{id}", a.getContact},
		{"PUT", "/api/v1/contacts/{id}", a.updateContact},
		{"DELETE", "/api/v1/contacts/{id}", a.deleteContact},
		{"GET", "/api/v1/applications/{id}/contacts", a.listApplicationContacts},
		{"PUT", "/api/v1/applications/{id}/contacts/{contact_id}", a.linkContact},
		{"DELETE", "/api/v1/applications/{id}/contacts/{contact_id}", a.unlinkContact},

		{"POST", "/api/v1/applications/{id}/notes", a.createNote},
		{"GET", "/api/v1/applications/{id}/notes", a.listNotes},
		{"PUT", "/api/v1/notes/{id}", a.updateNote},
		{"DELETE", "/api/v1/notes/{id}", a.deleteNote},

		{"POST", "/api/v1/watches", a.createWatch},
		{"GET", "/api/v1/watches", a.listWatches},
		{"GET", "/api/v1/watches/{id}", a.getWatch},
		{"PUT", "/api/v1/watches/{id}", a.updateWatch},
		{"DELETE", "/api/v1/watches/{id}", a.deleteWatch},
	}
}

// NewRouter returns the service's HTTP handler.
func NewRouter(d Deps) http.Handler {
	a := &api{store: d.Store, logger: d.Logger}

	mux := http.NewServeMux()
	// Go 1.22+ patterns: the method is part of the route, so e.g. POST /healthz gets a 405 for free.
	for _, rt := range routes(a, d) {
		mux.HandleFunc(rt.method+" "+rt.pattern, rt.handler)
	}

	timeout := d.RequestTimeout
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	// See middleware.go for why the order matters.
	return withRequestID(
		withTimeout(timeout,
			accessLog(d.Logger,
				recoverPanics(d.Logger, mux))))
}

// handleOpenAPISpec serves the API description (also used to generate the frontend client).
func handleOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(docs.OpenAPI)
}

// handleHealthz reports that the process is up and serving HTTP (liveness).
// It deliberately checks nothing else: a database outage should not make the
// orchestrator restart a healthy process.
func handleHealthz(version string) http.HandlerFunc {
	type response struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response{Status: "ok", Version: version})
	}
}

// readyzTimeout bounds the database check so /readyz answers quickly even when Postgres hangs.
const readyzTimeout = 2 * time.Second

// handleReadyz reports whether the service can do useful work right now (readiness):
// it returns 503 when the database is unreachable. Deploys wait for this to pass.
func handleReadyz(db Pinger, logger *slog.Logger) http.HandlerFunc {
	type response struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			// Log the detail for operators; don't leak connection errors to callers.
			logger.WarnContext(ctx, "readiness check failed", "check", "database", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, response{Status: "unavailable", Database: "unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, response{Status: "ready", Database: "ok"})
	}
}
