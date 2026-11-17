package notify_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/notify"
	"github.com/arjunshastry16/jobtrack/internal/notify/notifytest"
)

// captured is what the fake ntfy server received.
type captured struct {
	method, path string
	header       http.Header
	body         string
}

// fakeNtfy starts a local server standing in for ntfy. It answers with status and
// records each request.
func fakeNtfy(t *testing.T, status int, respBody string) (*httptest.Server, func() []captured) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, captured{r.Method, r.URL.Path, r.Header.Clone(), string(b)})
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []captured {
		mu.Lock()
		defer mu.Unlock()
		return append([]captured(nil), reqs...)
	}
}

func newNtfy(t *testing.T, url, token string) *notify.Ntfy {
	t.Helper()
	n, err := notify.NewNtfy(url, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNtfySendsMessage(t *testing.T) {
	srv, reqs := fakeNtfy(t, http.StatusOK, `{"id":"abc"}`)
	n := newNtfy(t, srv.URL+"/jobtrack-test", "tk_secret")

	err := n.Notify(t.Context(), notify.Message{
		Title:    "Follow up: Acme",
		Body:     "No reply in 7 days to your Backend Engineer application.",
		Priority: notify.PriorityHigh,
		Tags:     []string{"bell", "briefcase"},
		ClickURL: "https://example.test/applications/7",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	got := reqs()
	if len(got) != 1 {
		t.Fatalf("server got %d requests, want 1", len(got))
	}
	r := got[0]
	checks := map[string][2]string{
		"method":        {r.method, http.MethodPost},
		"path":          {r.path, "/jobtrack-test"},
		"body":          {r.body, "No reply in 7 days to your Backend Engineer application."},
		"Title":         {r.header.Get("Title"), "Follow up: Acme"},
		"Priority":      {r.header.Get("Priority"), "high"},
		"Tags":          {r.header.Get("Tags"), "bell,briefcase"},
		"Click":         {r.header.Get("Click"), "https://example.test/applications/7"},
		"Authorization": {r.header.Get("Authorization"), "Bearer tk_secret"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
}

func TestNtfyOmitsEmptyHeaders(t *testing.T) {
	srv, reqs := fakeNtfy(t, http.StatusOK, "")
	if err := newNtfy(t, srv.URL+"/jobtrack-test", "").Notify(t.Context(), notify.Message{Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Title", "Priority", "Tags", "Click", "Authorization"} {
		if v := reqs()[0].header.Get(h); v != "" {
			t.Errorf("header %s = %q, want it absent", h, v)
		}
	}
}

func TestNtfyHeaderSafety(t *testing.T) {
	srv, reqs := fakeNtfy(t, http.StatusOK, "")
	n := newNtfy(t, srv.URL+"/jobtrack-test", "")

	// Non-ASCII titles must survive (RFC 2047 encoded); line breaks must not be able to
	// inject extra headers.
	titles := map[string]string{
		"Follow up: Société Générale":             "Follow up: Société Générale",
		"Evil\r\nPriority: urgent\r\nX-Hack: yes": "Evil Priority: urgent X-Hack: yes",
	}
	for in, want := range titles {
		if err := n.Notify(t.Context(), notify.Message{Title: in, Body: "x"}); err != nil {
			t.Fatalf("Notify(%q): %v", in, err)
		}
		r := reqs()[len(reqs())-1]
		decoded, err := new(mime.WordDecoder).DecodeHeader(r.header.Get("Title"))
		if err != nil || decoded != want {
			t.Errorf("title %q arrived as %q (decoded %q, %v), want %q", in, r.header.Get("Title"), decoded, err, want)
		}
		if r.header.Get("X-Hack") != "" || r.header.Get("Priority") != "" {
			t.Errorf("title %q injected headers: %v", in, r.header)
		}
	}
}

func TestNtfyTruncatesLongBodies(t *testing.T) {
	srv, reqs := fakeNtfy(t, http.StatusOK, "")
	// 3-byte characters, so a 4096-byte cut would land in the middle of one.
	body := strings.Repeat("€", 2000)
	if err := newNtfy(t, srv.URL+"/jobtrack-test", "").Notify(t.Context(), notify.Message{Body: body}); err != nil {
		t.Fatal(err)
	}
	got := reqs()[0].body
	if len(got) > 4096 || len(got) < 4090 || !utf8.ValidString(got) {
		t.Errorf("body: %d bytes, valid UTF-8 %v; want just under 4096 and valid", len(got), utf8.ValidString(got))
	}
}

func TestNtfyErrors(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
	}{
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
	}
	for _, tt := range tests {
		srv, _ := fakeNtfy(t, tt.status, `{"error":"nope"}`)
		err := newNtfy(t, srv.URL+"/jobtrack-test", "").Notify(t.Context(), notify.Message{Body: "x"})

		var se *notify.StatusError
		if !errors.As(err, &se) || se.Code != tt.status || !strings.Contains(se.Error(), "nope") {
			t.Errorf("status %d: err = %v, want StatusError with the server's message", tt.status, err)
			continue
		}
		if notify.IsRetryable(err) != tt.retryable {
			t.Errorf("status %d: retryable = %v, want %v", tt.status, notify.IsRetryable(err), tt.retryable)
		}
	}
}

func TestNtfyNetworkErrorsAndTimeouts(t *testing.T) {
	// A server that never answers in time. release lets its handler return at the end;
	// otherwise slow.Close() would wait for the handler.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer slow.Close()
	defer close(release) // deferred calls run last-in first-out: release, then Close
	n, err := notify.NewNtfy(slow.URL+"/jobtrack-test", "", &http.Client{Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = n.Notify(context.Background(), notify.Message{Body: "x"})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Errorf("slow server: err %v after %s, want a timeout after ~100ms", err, time.Since(start))
	}
	if !notify.IsRetryable(err) {
		t.Errorf("timeout should be retryable: %v", err)
	}

	// Nothing listening at all.
	dead := newNtfy(t, "http://127.0.0.1:1/jobtrack-test", "")
	if err := dead.Notify(t.Context(), notify.Message{Body: "x"}); err == nil || !notify.IsRetryable(err) {
		t.Errorf("unreachable server: err %v, want a retryable error", err)
	}
}

func TestNewNtfyValidation(t *testing.T) {
	bad := []string{
		"",
		"ntfy.sh/topic",                    // no scheme
		"ftp://ntfy.sh/aaaaaaaaaaaaaaaaaa", // wrong scheme
		"https://ntfy.sh",                  // no topic
		"https://ntfy.sh/a/b",              // not a single topic
		"https://ntfy.sh/jobtrack",         // guessable topic on the public server
	}
	for _, u := range bad {
		if _, err := notify.NewNtfy(u, "", nil); err == nil {
			t.Errorf("NewNtfy(%q) accepted, want error", u)
		}
	}
	good := []string{
		"https://ntfy.sh/jt-8f3a9c2e71d04b56", // long random topic on the public server
		"http://ntfy.home.lan/jobtrack",       // short topics are fine on a private server
	}
	for _, u := range good {
		if _, err := notify.NewNtfy(u, "", nil); err != nil {
			t.Errorf("NewNtfy(%q) = %v, want ok", u, err)
		}
	}
}

func TestLogNotifier(t *testing.T) {
	var buf bytes.Buffer
	l := notify.Log{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	if err := l.Notify(t.Context(), notify.Message{Title: "Follow up: Acme", Body: "7 days"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Follow up: Acme") {
		t.Errorf("log = %q", buf.String())
	}
}

func TestRecorder(t *testing.T) {
	var r notifytest.Recorder
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { _ = r.Notify(t.Context(), notify.Message{Title: "t"}) })
	}
	wg.Wait()
	if n := len(r.Messages()); n != 20 {
		t.Errorf("recorded %d messages, want 20", n)
	}

	r.Err = errors.New("down")
	if err := r.Notify(t.Context(), notify.Message{}); err == nil || len(r.Messages()) != 20 {
		t.Errorf("with Err set: err %v, %d messages; want the error and nothing recorded", err, len(r.Messages()))
	}
}
