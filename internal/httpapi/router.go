// Package httpapi wires HTTP routes to handlers.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
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
	Logger  *slog.Logger
}

// NewRouter returns the service's HTTP handler.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	// Go 1.22+ patterns: the method is part of the route, so POST /healthz gets a 405 for free.
	mux.HandleFunc("GET /healthz", handleHealthz(d.Version))
	mux.HandleFunc("GET /readyz", handleReadyz(d.DB, d.Logger))
	return mux
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

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encode error can't change the response; ignore it.
	_ = json.NewEncoder(w).Encode(v)
}
