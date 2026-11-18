package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDispatchRejectsBadCommands(t *testing.T) {
	// A database URL is set, so the errors can only come from checking the arguments.
	env := map[string]string{"JOBTRACK_DATABASE_URL": "postgres://u@127.0.0.1:1/db"}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"serve"}, `unknown command "serve"`},
		{[]string{"migrate"}, "usage: jobtrack migrate"},
		{[]string{"migrate", "sideways"}, `unknown migrate command "sideways"`},
		{[]string{"migrate", "up", "now"}, "usage: jobtrack migrate"},
		{[]string{"healthcheck", "extra"}, "usage: jobtrack healthcheck"},
	} {
		err := dispatch(t.Context(), tt.args, func(k string) string { return env[k] }, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("dispatch(%q) = %v, want an error containing %q", tt.args, err, tt.want)
		}
	}
}

// migrate needs only the database URL: not the allowlist or anything else the service needs.
func TestMigrateNeedsOnlyTheDatabase(t *testing.T) {
	env := map[string]string{"JOBTRACK_DATABASE_URL": unreachableDB(t)}
	err := dispatch(t.Context(), []string{"migrate", "status"}, func(k string) string { return env[k] }, &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "JOBTRACK_ALLOWED_LOGINS") || !strings.Contains(err.Error(), "status") {
		t.Errorf("migrate status with the database down = %v, want a database error from status", err)
	}
	if err := dispatch(t.Context(), []string{"migrate", "up"}, func(string) string { return "" }, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "JOBTRACK_DATABASE_URL") {
		t.Errorf("migrate up without a database URL = %v, want it to ask for JOBTRACK_DATABASE_URL", err)
	}
}

func TestReadyzURL(t *testing.T) {
	for addr, want := range map[string]string{
		"":               "http://127.0.0.1:8091/readyz",
		"127.0.0.1:9000": "http://127.0.0.1:9000/readyz",
		"0.0.0.0:8090":   "http://127.0.0.1:8090/readyz",
		":8090":          "http://127.0.0.1:8090/readyz",
		"[::]:8090":      "http://[::1]:8090/readyz",
		"[::1]:8090":     "http://[::1]:8090/readyz",
		"localhost:8090": "http://localhost:8090/readyz",
	} {
		got, err := readyzURL(addr)
		if err != nil || got != want {
			t.Errorf("readyzURL(%q) = %q, %v; want %q", addr, got, err, want)
		}
	}
	if _, err := readyzURL("8090"); err == nil {
		t.Error(`readyzURL("8090") = nil error, want one (no host:port)`)
	}
}

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	env := func(k string) string {
		if k == "JOBTRACK_ADDR" {
			return addr
		}
		return ""
	}

	if err := dispatch(t.Context(), []string{"healthcheck"}, env, &bytes.Buffer{}); err != nil || path != "/readyz" {
		t.Errorf("healthcheck of a ready server = %v (asked %q), want nil from /readyz", err, path)
	}
	status = http.StatusServiceUnavailable
	if err := dispatch(t.Context(), []string{"healthcheck"}, env, &bytes.Buffer{}); err == nil {
		t.Error("healthcheck of an unready server = nil, want an error")
	}
	srv.Close()
	if err := dispatch(t.Context(), []string{"healthcheck"}, env, &bytes.Buffer{}); err == nil {
		t.Error("healthcheck with nothing listening = nil, want an error")
	}
}
