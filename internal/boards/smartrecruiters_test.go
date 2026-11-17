package boards_test

import (
	_ "embed" // for //go:embed
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

//go:embed testdata/smartrecruiters_postings.json
var srPostings []byte

func TestSmartRecruitersFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/companies/ExampleCorp/postings" || r.URL.Query().Get("limit") != "100" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(srPostings)
	}))
	defer srv.Close()

	got, err := boards.NewSmartRecruitersAt(srv.URL, nil).Fetch(t.Context(), "ExampleCorp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d postings, want 3 (internal + untitled skipped): %+v", len(got), got)
	}
	first := got[0]
	if first.ExternalID != "744000000000001" || first.Title != "Senior Backend Engineer" ||
		first.Location != "Bengaluru, Karnataka, India" ||
		first.URL != "https://jobs.smartrecruiters.com/ExampleCorp/744000000000001" ||
		!first.PublishedAt.Equal(time.Date(2026, 11, 17, 9, 2, 50, 621_000_000, time.UTC)) {
		t.Errorf("first = %+v", first)
	}
	if got[2].Location != "India; Remote" {
		t.Errorf("remote posting location = %q", got[2].Location)
	}
}

func TestSmartRecruitersLive(t *testing.T) {
	if os.Getenv("JOBTRACK_LIVE_TESTS") != "1" {
		t.Skip("set JOBTRACK_LIVE_TESTS=1 to run against the real SmartRecruiters API")
	}
	got, err := boards.NewSmartRecruiters(nil).Fetch(t.Context(), "Freshworks")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].URL == "" {
		t.Fatalf("live fetch: %d postings, first %+v", len(got), got)
	}
	india := 0
	for _, p := range got {
		if strings.Contains(strings.ToLower(p.Location), "india") {
			india++
		}
	}
	t.Logf("live smartrecruiters/Freshworks: %d postings (%d in India), e.g. %q (%s)", len(got), india, got[0].Title, got[0].Location)
}
