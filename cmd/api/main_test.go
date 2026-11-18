package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer that is safe to write from the server goroutine
// while the test goroutine reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// unreachableDB returns a database URL for a port that was just freed, so connections
// are refused immediately: the database is "down". (A fixed port like 1 isn't safe: on
// some machines connections to it hang instead of being refused.)
func unreachableDB(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return "postgres://u@" + addr + "/db?connect_timeout=1"
}

// TestRunServesAndShutsDown starts the real server on a random port with the database
// down, checks /healthz is 200 (alive) and /readyz is 503 (not ready), then cancels the
// context (like SIGTERM) and expects a clean exit.
func TestRunServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := map[string]string{
		"JOBTRACK_ADDR":           "127.0.0.1:0", // port 0: the OS picks a free port
		"JOBTRACK_DATABASE_URL":   unreachableDB(t),
		"JOBTRACK_ALLOWED_LOGINS": "me@github",
	}
	logs := &syncBuffer{}

	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, logs) }()

	addr := waitForListenAddr(t, logs)

	if got := getStatus(t, "http://"+addr+"/healthz"); got != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", got)
	}
	if got := getStatus(t, "http://"+addr+"/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz status = %d, want 503 with the database down", got)
	}
	// Everything else needs the Tailscale login that tailscale serve would add.
	if got := getStatus(t, "http://"+addr+"/api/v1/openapi.yaml"); got != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/openapi.yaml without a login = %d, want 401", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned %v, want nil after shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("run() did not return within 5s of cancellation; logs:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "stopped cleanly") {
		t.Errorf("logs missing shutdown message:\n%s", logs.String())
	}
}

// testClient opens a fresh connection per request and closes it afterwards. The default
// client keeps connections alive and may dial a spare one it never uses; http.Server.Shutdown
// waits up to 5s for such never-used connections, which made the shutdown test flaky in CI.
var testClient = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true},
	Timeout:   5 * time.Second,
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // read to the end so the connection is finished cleanly
	return resp.StatusCode
}

func TestRunRejectsBadConfig(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"bad log level": {"JOBTRACK_DATABASE_URL": unreachableDB(t), "JOBTRACK_LOG_LEVEL": "loud", "JOBTRACK_ALLOWED_LOGINS": "me@github"},
		// No allowlist must stop the server, not open it to everyone.
		"no allowed logins": {"JOBTRACK_DATABASE_URL": unreachableDB(t)},
	} {
		err := run(context.Background(), func(k string) string { return env[k] }, &syncBuffer{})
		if err == nil {
			t.Errorf("%s: run() = nil, want a config error", name)
		}
	}
}

// waitForListenAddr polls the JSON logs until the "listening" line shows up and returns its addr.
func waitForListenAddr(t *testing.T, logs *syncBuffer) string {
	t.Helper()
	return waitForLoggedAddr(t, logs, "http server listening")
}

// waitForLoggedAddr polls the JSON logs until a line with message msg shows up and
// returns its addr.
func waitForLoggedAddr(t *testing.T, logs *syncBuffer, msg string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sc := bufio.NewScanner(strings.NewReader(logs.String()))
		for sc.Scan() {
			var line struct {
				Msg  string `json:"msg"`
				Addr string `json:"addr"`
			}
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == msg {
				return line.Addr
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never logged %q; logs:\n%s", msg, logs.String())
	return ""
}

// TestRunServesMetrics starts the server with a metrics address: the metrics are on that
// address only, count the app's requests, and stop with the server.
func TestRunServesMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := map[string]string{
		"JOBTRACK_ADDR":           "127.0.0.1:0",
		"JOBTRACK_METRICS_ADDR":   "127.0.0.1:0",
		"JOBTRACK_DATABASE_URL":   unreachableDB(t),
		"JOBTRACK_ALLOWED_LOGINS": "me@github",
	}
	logs := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, logs) }()

	app := waitForListenAddr(t, logs)
	metricsAddr := waitForLoggedAddr(t, logs, "metrics server listening")

	getStatus(t, "http://"+app+"/healthz")
	status, body := get(t, "http://"+metricsAddr+"/metrics")
	if status != http.StatusOK {
		t.Fatalf("GET /metrics on the metrics address = %d, want 200", status)
	}
	for _, want := range []string{
		`jobtrack_build_info{goversion="`,
		`jobtrack_http_requests_total{code="200",route="GET /healthz"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	// Not on the app's address, which tailscale serve exposes: there it's just a path
	// that needs a login.
	if got := getStatus(t, "http://"+app+"/metrics"); got != http.StatusUnauthorized {
		t.Errorf("GET /metrics on the app's address = %d, want 401", got)
	}
	// Only /metrics is served on the metrics address.
	if got := getStatus(t, "http://"+metricsAddr+"/api/v1/companies"); got != http.StatusNotFound {
		t.Errorf("GET /api/v1/companies on the metrics address = %d, want 404", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned %v, want nil after shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("run() did not return within 5s of cancellation; logs:\n%s", logs.String())
	}
	if conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", metricsAddr); err == nil {
		_ = conn.Close()
		t.Error("the metrics address still accepts connections after shutdown")
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}
