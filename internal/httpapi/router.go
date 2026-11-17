// Package httpapi wires HTTP routes to handlers.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

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
	Store   store.Querier
	Logger  *slog.Logger
	// RequestTimeout bounds each request; zero means defaultRequestTimeout.
	RequestTimeout time.Duration
}

// defaultRequestTimeout is below the server's 15s WriteTimeout, so a slow handler gets
// a clean cancellation (and a logged error) rather than a silently cut connection.
const defaultRequestTimeout = 10 * time.Second

// api holds what the resource handlers share.
type api struct {
	store  store.Querier
	logger *slog.Logger
}

// NewRouter returns the service's HTTP handler.
func NewRouter(d Deps) http.Handler {
	a := &api{store: d.Store, logger: d.Logger}

	mux := http.NewServeMux()
	// Go 1.22+ patterns: the method is part of the route, so e.g. POST /healthz gets a 405 for free.
	mux.HandleFunc("GET /healthz", handleHealthz(d.Version))
	mux.HandleFunc("GET /readyz", handleReadyz(d.DB, d.Logger))

	mux.HandleFunc("POST /api/v1/companies", a.createCompany)
	mux.HandleFunc("GET /api/v1/companies", a.listCompanies)
	mux.HandleFunc("GET /api/v1/companies/{id}", a.getCompany)
	mux.HandleFunc("PUT /api/v1/companies/{id}", a.updateCompany)
	mux.HandleFunc("DELETE /api/v1/companies/{id}", a.deleteCompany)

	mux.HandleFunc("POST /api/v1/applications", a.createApplication)
	mux.HandleFunc("GET /api/v1/applications", a.listApplications)
	mux.HandleFunc("GET /api/v1/applications/{id}", a.getApplication)
	mux.HandleFunc("PUT /api/v1/applications/{id}", a.updateApplication)
	mux.HandleFunc("DELETE /api/v1/applications/{id}", a.deleteApplication)

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
