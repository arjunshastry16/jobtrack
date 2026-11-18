package boards_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

// Every Board implementation must handle failures the same way, so the ingestion job can
// treat them uniformly. This runs each one through the same cases.
type implementation struct {
	name  string
	make  func(baseURL string) boards.Board
	token string // a well-formed token for this board
}

var implementations = []implementation{
	{"greenhouse", func(u string) boards.Board { return boards.NewGreenhouseAt(u, nil) }, "examplecorp"},
	{"lever", func(u string) boards.Board { return boards.NewLeverAt(u, nil) }, "examplecorp"},
	{"ashby", func(u string) boards.Board { return boards.NewAshbyAt(u, nil) }, "examplecorp"},
	{"smartrecruiters", func(u string) boards.Board { return boards.NewSmartRecruitersAt(u, nil) }, "examplecorp"},
	{"workday", func(u string) boards.Board { return boards.NewWorkdayAt(u, nil) }, "examplecorp.wd1.myworkdayjobs.com/External"},
}

// All has a client for exactly the supported boards, each under its own name.
func TestAllMatchesSupported(t *testing.T) {
	all := boards.All(nil)
	if len(all) != len(boards.Supported) {
		t.Errorf("All has %d boards, Supported lists %d", len(all), len(boards.Supported))
	}
	for _, name := range boards.Supported {
		if b, ok := all[name]; !ok || b.Name() != name {
			t.Errorf("All()[%q] = %v", name, b)
		}
	}
}

// Every supported board has an implementation here, so none skips these checks.
func TestEverySupportedBoardIsConformanceTested(t *testing.T) {
	for _, name := range boards.Supported {
		if !slices.ContainsFunc(implementations, func(i implementation) bool { return i.name == name }) {
			t.Errorf("board %q has no conformance entry", name)
		}
	}
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
			if _, err := impl.make(statusServer(t, 404, "").URL).Fetch(ctx, impl.token); !errors.Is(err, boards.ErrBoardNotFound) {
				t.Errorf("404: err = %v, want ErrBoardNotFound", err)
			}

			// Other bad statuses: HTTPError with the code.
			var he *boards.HTTPError
			if _, err := impl.make(statusServer(t, 503, "busy").URL).Fetch(ctx, impl.token); !errors.As(err, &he) || he.Code != 503 {
				t.Errorf("503: err = %v, want HTTPError 503", err)
			}

			// Not JSON.
			if _, err := impl.make(statusServer(t, 200, "<html>maintenance</html>").URL).Fetch(ctx, impl.token); err == nil {
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
			if _, err := impl.make(hung.URL).Fetch(tctx, impl.token); err == nil || time.Since(start) > 2*time.Second {
				t.Errorf("hung server: err %v after %s", err, time.Since(start))
			}
		})
	}
}
