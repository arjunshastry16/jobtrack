package boards_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

func TestParseWorkdayURL(t *testing.T) {
	const india = "2fcb99c455831013ea52b82135ba3266"
	tests := []struct {
		in, want string
	}{
		// As copied from the browser: locale, tracking parameters and job paths dropped.
		{"https://nvidia.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite?locationHierarchy1=" + india + "&utm_source=linkedin",
			"nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?locationHierarchy1=" + india},
		{"  NVIDIA.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA/Engineer_JR1  ",
			"nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite"},
		{"http://intel.wd1.myworkdayjobs.com/External/", "intel.wd1.myworkdayjobs.com/External"},
		{"https://adobe.wd5.myworkdayjobs.com/fr/external_experienced", "adobe.wd5.myworkdayjobs.com/external_experienced"},
		// Search text is tidied; several filters are kept and sorted.
		{"https://x.wd3.myworkdayjobs.com/Careers?q=++site+++reliability+&timeType=5509c0b5959810ac0029943377d47364&locations=91336993fab910af6d6fb9748af4c1df&locations=f28ae3c49d2601aff203e114d301695f",
			"x.wd3.myworkdayjobs.com/Careers?locations=91336993fab910af6d6fb9748af4c1df&locations=f28ae3c49d2601aff203e114d301695f&q=site+reliability&timeType=5509c0b5959810ac0029943377d47364"},
		// A company's own filter, with a long name full of hyphens (Salesforce's country filter).
		{"https://salesforce.wd12.myworkdayjobs.com/en-US/External_Career_Site?CF_-_REC_-_LRV_-_Job_Posting_Anchor_-_Country_from_Job_Posting_Location_Extended=c4f78be1a8f14da0ab49ce1162348a5e",
			"salesforce.wd12.myworkdayjobs.com/External_Career_Site?CF_-_REC_-_LRV_-_Job_Posting_Anchor_-_Country_from_Job_Posting_Location_Extended=c4f78be1a8f14da0ab49ce1162348a5e"},
		// The canonical form parses to itself.
		{"nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?locationHierarchy1=" + india,
			"nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?locationHierarchy1=" + india},
	}
	for _, tt := range tests {
		site, err := boards.ParseWorkdayURL(tt.in)
		if err != nil {
			t.Errorf("ParseWorkdayURL(%q): %v", tt.in, err)
			continue
		}
		if got := site.Token(); got != tt.want {
			t.Errorf("ParseWorkdayURL(%q).Token() =\n %q\nwant\n %q", tt.in, got, tt.want)
		}
	}

	site, _ := boards.ParseWorkdayURL(tests[0].in)
	if site.Tenant != "nvidia" || site.Host != "nvidia.wd5.myworkdayjobs.com" || site.Site != "NVIDIAExternalCareerSite" {
		t.Errorf("site = %+v", site)
	}

	for _, bad := range []string{
		"",
		"stripe",
		"https://example.com/External",
		"https://nvidia.wd5.myworkdayjobs.com.evil.example/External", // look-alike host
		"https://evil.example/nvidia.wd5.myworkdayjobs.com/External",
		"https://user@nvidia.wd5.myworkdayjobs.com/External",
		"https://nvidia.wd5.myworkdayjobs.com:8443/External",
		"ftp://nvidia.wd5.myworkdayjobs.com/External",
		"https://nvidia.wd5.myworkdayjobs.com/",      // no site
		"https://nvidia.wd5.myworkdayjobs.com/en-US", // only a locale
		"https://nvidia.wd5.myworkdayjobs.com/../x",  // odd site name
		"https://nvidia.wd5.myworkdayjobs.com/a b",   // space in site
		"https://nvidia.myworkdayjobs.com/External",  // no wdN
		"https://nvidia.wd5.myworkdayjobs.com/External?q=" + strings.Repeat("x", 101),
		"https://nvidia.wd5.myworkdayjobs.com/External?" + strings.Repeat("a=2fcb99c455831013ea52b82135ba3266&", 21),
		"https://nvidia.wd5.myworkdayjobs.com/" + strings.Repeat("x", 2000),
		// A filter that can't be kept is refused, not dropped: dropping it would widen the watch.
		"https://nvidia.wd5.myworkdayjobs.com/External?" + strings.Repeat("f", 101) + "=" + india,
		"https://nvidia.wd5.myworkdayjobs.com/External?loc.country=" + india,
	} {
		if site, err := boards.ParseWorkdayURL(bad); err == nil {
			t.Errorf("ParseWorkdayURL(%q) accepted: %+v", bad, site)
		}
	}
}

func TestWorkdayLive(t *testing.T) {
	if os.Getenv("JOBTRACK_LIVE_TESTS") != "1" {
		t.Skip("set JOBTRACK_LIVE_TESTS=1 to run against a real Workday careers site")
	}
	// NVIDIA's careers site filtered to India, as copied from the browser.
	const site = "https://nvidia.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite?locationHierarchy1=2fcb99c455831013ea52b82135ba3266"
	start := time.Now()
	got, err := boards.NewWorkday(nil).Fetch(t.Context(), site)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].URL == "" {
		t.Fatalf("live fetch: %d postings, first %+v", len(got), got)
	}
	notIndia := 0
	for _, p := range got {
		if !strings.Contains(strings.ToLower(p.Location), "india") {
			notIndia++
		}
	}
	t.Logf("live workday/NVIDIA (India filter): %d postings in %s (%d without \"India\" in the location text, e.g. \"2 Locations\"), e.g. %q (%s) %s",
		len(got), time.Since(start).Round(time.Second), notIndia, got[0].Title, got[0].Location, got[0].URL)
}
