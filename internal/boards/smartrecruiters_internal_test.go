package boards

// Internal test (package boards) so it can shrink the page size and test srLocation.

import (
	_ "embed" // for //go:embed
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

//go:embed testdata/smartrecruiters_postings.json
var srPostingsJSON []byte

func TestSmartRecruitersPaginates(t *testing.T) {
	var fixture struct {
		TotalFound int               `json:"totalFound"`
		Content    []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(srPostingsJSON, &fixture); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page := []json.RawMessage{}
		if offset < len(fixture.Content) {
			page = fixture.Content[offset:min(offset+limit, len(fixture.Content))]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"offset": offset, "limit": limit, "totalFound": fixture.TotalFound, "content": page,
		})
	}))
	defer srv.Close()

	s := NewSmartRecruitersAt(srv.URL, nil)
	s.pageSize = 2 // 5 postings -> pages of 2, 2, 1 (stops at totalFound)
	got, err := s.Fetch(t.Context(), "ExampleCorp")
	if err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	if len(got) != 3 { // internal and untitled postings skipped
		t.Errorf("got %d postings, want 3: %+v", len(got), got)
	}
}

func TestSRLocation(t *testing.T) {
	tests := []struct {
		full, city, country string
		remote              bool
		want                string
	}{
		{"Bengaluru, Karnataka, India", "Bengaluru", "in", false, "Bengaluru, Karnataka, India"}, // country already present
		{"London, , United Kingdom", "London", "gb", false, "London, United Kingdom"},            // empty part dropped
		{"", "Pune", "in", false, "Pune; India"},                                                 // fall back to city
		{"", "", "in", true, "India; Remote"},
		{"Austin, TX", "Austin", "us", true, "Austin, TX; United States; Remote"},
		{"Somewhere", "", "zz", false, "Somewhere; ZZ"}, // unknown code kept as-is
		{"", "", "", false, ""},
	}
	for _, tt := range tests {
		if got := srLocation(tt.full, tt.city, tt.country, tt.remote); got != tt.want {
			t.Errorf("srLocation(%q, %q, %q, %v) = %q, want %q", tt.full, tt.city, tt.country, tt.remote, got, tt.want)
		}
	}
}
