// Package httpapi wires HTTP routes to handlers.
package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewRouter returns the service's HTTP handler. version is reported by /healthz
// so you can tell which build is running.
func NewRouter(version string) http.Handler {
	mux := http.NewServeMux()
	// Go 1.22+ patterns: the method is part of the route, so POST /healthz gets a 405 for free.
	mux.HandleFunc("GET /healthz", handleHealthz(version))
	return mux
}

// handleHealthz reports that the process is up and serving HTTP (liveness).
// It deliberately checks nothing else; a readiness check that pings the database comes later.
func handleHealthz(version string) http.HandlerFunc {
	type response struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response{Status: "ok", Version: version})
	}
}

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encode error can't change the response; ignore it.
	_ = json.NewEncoder(w).Encode(v)
}
