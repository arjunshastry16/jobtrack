package boards_test

import (
	"context"
	_ "embed" // for //go:embed
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

// greenhouseJobs is a synthetic response with the same shape as the real API (checked
// against boards-api.greenhouse.io), including fields we ignore and some bad entries.
//
//go:embed testdata/greenhouse_jobs.json
var greenhouseJobs []byte

// fixtureToken is the only board the fake server knows; anything else is a 404.
const fixtureToken = "examplecorp"

// greenhouseServer serves a canned response for fixtureToken, 404 for other tokens.
func greenhouseServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/boards/"+fixtureToken+"/jobs" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing request headers: %v", r.Header)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGreenhouseFetch(t *testing.T) {
	srv := greenhouseServer(t, http.StatusOK, greenhouseJobs)
	g := boards.NewGreenhouseAt(srv.URL, nil)

	got, err := g.Fetch(t.Context(), "examplecorp")
	if err != nil {
		t.Fatal(err)
	}
	// The posting with an empty title is skipped; the rest are normalised.
	if len(got) != 3 {
		t.Fatalf("got %d postings, want 3: %+v", len(got), got)
	}

	first := got[0]
	want := boards.Posting{
		ExternalID:  "8638232002", // > 32 bits: must not overflow
		Title:       "Senior Backend Engineer, Payments",
		Location:    "Remote, United States",
		URL:         "https://job-boards.greenhouse.io/examplecorp/jobs/8638232002",
		PublishedAt: time.Date(2026, 7, 22, 17, 38, 40, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 11, 6, 20, 25, 7, 0, time.UTC),
	}
	if first.ExternalID != want.ExternalID || first.Title != want.Title || first.Location != want.Location ||
		first.URL != want.URL || !first.PublishedAt.Equal(want.PublishedAt) || !first.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("first posting =\n  %+v\nwant\n  %+v", first, want)
	}
	if got[1].Title != "Site Reliability Engineer" {
		t.Errorf("title not trimmed: %q", got[1].Title)
	}
	if !got[2].UpdatedAt.IsZero() || !got[2].PublishedAt.IsZero() || got[2].Location != "" {
		t.Errorf("bad/missing fields should be zero values: %+v", got[2])
	}
}

func TestGreenhouseErrors(t *testing.T) {
	ctx := t.Context()

	// Unknown company -> ErrBoardNotFound (the watch is misconfigured; don't retry).
	srv := greenhouseServer(t, http.StatusOK, []byte(`{"jobs":[]}`))
	if _, err := boards.NewGreenhouseAt(srv.URL, nil).Fetch(ctx, "nobody"); !errors.Is(err, boards.ErrBoardNotFound) {
		t.Errorf("unknown board: err = %v, want ErrBoardNotFound", err)
	}

	// Server error -> HTTPError with the code.
	srv = greenhouseServer(t, http.StatusBadGateway, []byte("upstream down"))
	var he *boards.HTTPError
	if _, err := boards.NewGreenhouseAt(srv.URL, nil).Fetch(ctx, "examplecorp"); !errors.As(err, &he) || he.Code != 502 {
		t.Errorf("502: err = %v, want HTTPError 502", err)
	}

	// Not JSON -> decode error.
	srv = greenhouseServer(t, http.StatusOK, []byte("<html>maintenance</html>"))
	if _, err := boards.NewGreenhouseAt(srv.URL, nil).Fetch(ctx, "examplecorp"); err == nil {
		t.Error("HTML body accepted")
	}

	// Empty board is fine.
	srv = greenhouseServer(t, http.StatusOK, []byte(`{"jobs":[],"meta":{"total":0}}`))
	if got, err := boards.NewGreenhouseAt(srv.URL, nil).Fetch(ctx, "examplecorp"); err != nil || len(got) != 0 {
		t.Errorf("empty board: %v, %v", got, err)
	}
}

func TestGreenhouseRejectsBadTokensWithoutARequest(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	g := boards.NewGreenhouseAt(srv.URL, nil)

	for _, tok := range []string{"", "..", "a/b", "../../admin", "has space", "x?y=1"} {
		if _, err := g.Fetch(t.Context(), tok); err == nil {
			t.Errorf("token %q accepted", tok)
		}
	}
	if hits != 0 {
		t.Errorf("%d requests were sent for invalid tokens, want 0", hits)
	}
}

func TestGreenhouseHonoursContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := boards.NewGreenhouseAt(srv.URL, nil).Fetch(ctx, "examplecorp"); err == nil {
		t.Fatal("hung server: no error")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %s to give up, want ~100ms", time.Since(start))
	}
}

func TestValidToken(t *testing.T) {
	for tok, want := range map[string]bool{
		"stripe": true, "gitlab": true, "acme-corp": true, "acme_corp.io": true,
		"": false, ".": false, "..": false, "a/b": false, "a b": false, "ü": false,
	} {
		if got := boards.ValidToken(tok); got != want {
			t.Errorf("ValidToken(%q) = %v, want %v", tok, got, want)
		}
	}
}

// TestGreenhouseLive talks to the real API. It's skipped unless JOBTRACK_LIVE_TESTS=1,
// so CI and normal runs never depend on the network or a third party's data.
func TestGreenhouseLive(t *testing.T) {
	if os.Getenv("JOBTRACK_LIVE_TESTS") != "1" {
		t.Skip("set JOBTRACK_LIVE_TESTS=1 to run against the real Greenhouse API")
	}
	got, err := boards.NewGreenhouse(nil).Fetch(t.Context(), "gitlab")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ExternalID == "" || got[0].URL == "" {
		t.Errorf("live fetch returned %d postings; first: %+v", len(got), got)
	}
	t.Logf("live: %d postings, e.g. %q (%s)", len(got), got[0].Title, got[0].Location)
}
