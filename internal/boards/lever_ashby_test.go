package boards_test

import (
	_ "embed" // for //go:embed
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

//go:embed testdata/lever_postings.json
var leverPostings []byte

//go:embed testdata/ashby_jobs.json
var ashbyJobs []byte

// serveAt answers path with body (200) and everything else with 404.
func serveAt(t *testing.T, path string, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		// The Lever client pages; serve everything on the first page and nothing after.
		if r.URL.Query().Get("skip") != "" && r.URL.Query().Get("skip") != "0" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLeverFetch(t *testing.T) {
	srv := serveAt(t, "/v0/postings/examplecorp", leverPostings)
	got, err := boards.NewLeverAt(srv.URL, nil).Fetch(t.Context(), "examplecorp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d postings, want 4 (empty title skipped): %+v", len(got), got)
	}
	tests := []struct {
		i                        int
		title, location, url, id string
		published                time.Time
	}{
		{0, "Backend Engineer - Payments", "Bangalore, Karnataka; Hyderabad, Telangana; India",
			"https://jobs.lever.co/examplecorp/11111111-aaaa-4bbb-8ccc-000000000001", "11111111-aaaa-4bbb-8ccc-000000000001",
			time.UnixMilli(1757916149833).UTC()},
		{1, "Site Reliability Engineer", "Remote; United States", "", "11111111-aaaa-4bbb-8ccc-000000000002",
			time.UnixMilli(1760000000000).UTC()},
		{2, "Data Engineer", "Pune", "", "11111111-aaaa-4bbb-8ccc-000000000003", time.Time{}},
	}
	for _, tt := range tests {
		p := got[tt.i]
		if p.Title != tt.title || p.Location != tt.location || p.ExternalID != tt.id || !p.PublishedAt.Equal(tt.published) ||
			(tt.url != "" && p.URL != tt.url) {
			t.Errorf("posting %d = %+v\nwant title %q location %q id %q published %v", tt.i, p, tt.title, tt.location, tt.id, tt.published)
		}
	}
}

func TestAshbyFetch(t *testing.T) {
	srv := serveAt(t, "/posting-api/job-board/examplecorp", ashbyJobs)
	got, err := boards.NewAshbyAt(srv.URL, nil).Fetch(t.Context(), "examplecorp")
	if err != nil {
		t.Fatal(err)
	}
	// Unlisted and blank-title jobs are skipped.
	if len(got) != 2 {
		t.Fatalf("got %d postings, want 2: %+v", len(got), got)
	}
	first := got[0]
	if first.Title != "Platform Engineer" || first.ExternalID != "22222222-aaaa-4bbb-8ccc-000000000001" ||
		first.Location != "Bengaluru; Hyderabad; India" || // duplicate "bengaluru" dropped, country added
		!first.PublishedAt.Equal(time.Date(2026, 4, 17, 11, 46, 39, 214_000_000, time.UTC)) {
		t.Errorf("first = %+v", first)
	}
	if got[1].Location != "Remote - US; United States; Remote" {
		t.Errorf("remote posting location = %q", got[1].Location)
	}
}

func TestLeverAndAshbyLive(t *testing.T) {
	if os.Getenv("JOBTRACK_LIVE_TESTS") != "1" {
		t.Skip("set JOBTRACK_LIVE_TESTS=1 to run against the real Lever and Ashby APIs")
	}
	for _, c := range []struct {
		b     boards.Board
		token string
	}{{boards.NewLever(nil), "meesho"}, {boards.NewAshby(nil), "sarvam"}} {
		got, err := c.b.Fetch(t.Context(), c.token)
		if err != nil {
			t.Errorf("%s/%s: %v", c.b.Name(), c.token, err)
			continue
		}
		if len(got) == 0 || got[0].ExternalID == "" || got[0].URL == "" {
			t.Errorf("%s/%s: %d postings, first %+v", c.b.Name(), c.token, len(got), got)
			continue
		}
		t.Logf("live %s/%s: %d postings, e.g. %q (%s)", c.b.Name(), c.token, len(got), got[0].Title, got[0].Location)
	}
}
