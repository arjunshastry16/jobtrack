package boards

// Internal test (package boards) so it can shrink the page size.

import (
	_ "embed" // for //go:embed
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

//go:embed testdata/lever_postings.json
var leverPostingsJSON []byte

// leverServer serves the fixture with real skip/limit semantics, or ignores skip if
// brokenSkip is set (to test the page cap). It counts requests.
func leverServer(t *testing.T, brokenSkip bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var all []json.RawMessage
	if err := json.Unmarshal(leverPostingsJSON, &all); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v0/postings/examplecorp" || r.URL.Query().Get("mode") != "json" {
			http.NotFound(w, r)
			return
		}
		skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if brokenSkip {
			skip = 0
		}
		end := min(skip+limit, len(all))
		page := []json.RawMessage{}
		if skip < len(all) {
			page = all[skip:end]
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func TestLeverPaginates(t *testing.T) {
	srv, requests := leverServer(t, false)
	l := NewLeverAt(srv.URL, nil)
	l.pageSize = 2 // 5 fixture postings -> pages of 2, 2, 1

	got, err := l.Fetch(t.Context(), "examplecorp")
	if err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 3 {
		t.Errorf("%d requests, want 3 pages", n)
	}
	if len(got) != 4 { // the posting with an empty title is skipped
		t.Fatalf("got %d postings, want 4: %+v", len(got), got)
	}
}

func TestLeverStopsIfPagingIsBroken(t *testing.T) {
	srv, requests := leverServer(t, true) // every page is the same full page
	l := NewLeverAt(srv.URL, nil)
	l.pageSize = 2

	if _, err := l.Fetch(t.Context(), "examplecorp"); err == nil {
		t.Error("endless paging returned no error")
	}
	if n := requests.Load(); n != leverMaxPages {
		t.Errorf("%d requests, want the cap of %d", n, leverMaxPages)
	}
}
