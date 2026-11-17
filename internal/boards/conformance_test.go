package boards_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

// Every Board implementation must handle failures the same way, so the ingestion job can
// treat them uniformly. This runs each one through the same cases.
var implementations = []struct {
	name string
	make func(baseURL string) boards.Board
}{
	{"greenhouse", func(u string) boards.Board { return boards.NewGreenhouseAt(u, nil) }},
	{"lever", func(u string) boards.Board { return boards.NewLeverAt(u, nil) }},
	{"ashby", func(u string) boards.Board { return boards.NewAshbyAt(u, nil) }},
}

func statusServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBoardConformance(t *testing.T) {
	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			ctx := t.Context()

			if b := impl.make("http://unused"); b.Name() != impl.name {
				t.Errorf("Name() = %q, want %q", b.Name(), impl.name)
			}

			// 404: the company isn't on this board.
			if _, err := impl.make(statusServer(t, 404, "").URL).Fetch(ctx, "nobody"); !errors.Is(err, boards.ErrBoardNotFound) {
				t.Errorf("404: err = %v, want ErrBoardNotFound", err)
			}

			// Other bad statuses: HTTPError with the code.
			var he *boards.HTTPError
			if _, err := impl.make(statusServer(t, 503, "busy").URL).Fetch(ctx, "examplecorp"); !errors.As(err, &he) || he.Code != 503 {
				t.Errorf("503: err = %v, want HTTPError 503", err)
			}

			// Not JSON.
			if _, err := impl.make(statusServer(t, 200, "<html>maintenance</html>").URL).Fetch(ctx, "examplecorp"); err == nil {
				t.Error("HTML body accepted")
			}

			// Invalid tokens are rejected before any request.
			var hits atomic.Int32
			counting := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			defer counting.Close()
			for _, tok := range []string{"", "..", "a/b", "../admin", "x y"} {
				if _, err := impl.make(counting.URL).Fetch(ctx, tok); err == nil {
					t.Errorf("token %q accepted", tok)
				}
			}
			if hits.Load() != 0 {
				t.Errorf("%d requests sent for invalid tokens", hits.Load())
			}

			// A hung server is abandoned when the context ends.
			release := make(chan struct{})
			hung := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
			defer hung.Close()
			defer close(release)
			tctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			if _, err := impl.make(hung.URL).Fetch(tctx, "examplecorp"); err == nil || time.Since(start) > 2*time.Second {
				t.Errorf("hung server: err %v after %s", err, time.Since(start))
			}
		})
	}
}
