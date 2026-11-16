package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

// TestRunServesAndShutsDown starts the real server on a random port, calls /healthz,
// then cancels the context (like SIGTERM) and expects a clean exit.
func TestRunServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := map[string]string{"JOBTRACK_ADDR": "127.0.0.1:0"} // port 0: the OS picks a free port
	logs := &syncBuffer{}

	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, logs) }()

	addr := waitForListenAddr(t, logs)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned %v, want nil after shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5s of cancellation")
	}
	if !strings.Contains(logs.String(), "stopped cleanly") {
		t.Errorf("logs missing shutdown message:\n%s", logs.String())
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	env := map[string]string{"JOBTRACK_LOG_LEVEL": "loud"}
	err := run(context.Background(), func(k string) string { return env[k] }, &syncBuffer{})
	if err == nil {
		t.Fatal("run() = nil, want a config error")
	}
}

// waitForListenAddr polls the JSON logs until the "listening" line shows up and returns its addr.
func waitForListenAddr(t *testing.T, logs *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sc := bufio.NewScanner(strings.NewReader(logs.String()))
		for sc.Scan() {
			var line struct {
				Msg  string `json:"msg"`
				Addr string `json:"addr"`
			}
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "http server listening" {
				return line.Addr
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never logged its address; logs:\n%s", logs.String())
	return ""
}
