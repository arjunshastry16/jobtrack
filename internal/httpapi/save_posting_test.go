package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

func savePath(postingID int64) string {
	return "/api/v1/postings/" + strconv.FormatInt(postingID, 10) + "/application"
}

// inboxEntry returns the posting with id from the inbox (removed ones included).
func inboxEntry(t *testing.T, h http.Handler, id int64) postingJSON {
	t.Helper()
	var page postingPage
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/postings?include_removed=true&limit=200", nil, &page), http.StatusOK)
	for _, p := range page.Postings {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("posting %d not in the inbox", id)
	return postingJSON{}
}

func TestSavePosting(t *testing.T) {
	t.Parallel()
	h, st := newDBRouterAndStore(t)
	acme := mustCreateCompany(t, h, "Acme")
	w := mustCreateWatch(t, h, acme, "lever", "acme")
	ids := addPostings(t, st, w, "a1", "a2")

	// Saving creates a wishlist application filled in from the posting.
	var app applicationJSON
	rec := call(t, h, http.MethodPost, savePath(ids["a1"]), nil, &app)
	wantStatus(t, rec, http.StatusCreated)
	if got, want := rec.Header().Get("Location"), appPath(app.ID); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if app.CompanyID != acme || app.Role != "Engineer a1" || app.Status != "wishlist" ||
		deref(app.JobURL) != "https://jobs.example/a1" || deref(app.Location) != "Pune, India" || deref(app.Source) != "lever" {
		t.Errorf("saved application = %+v", app)
	}
	// ... with its first history event, as for any new application.
	if tl := timeline(t, h, app.ID); len(tl.Events) != 1 || tl.Events[0].ToStatus != "wishlist" {
		t.Errorf("timeline = %+v, want one event to wishlist", tl)
	}

	// Saving again finds the same application rather than adding a second.
	var again applicationJSON
	rec = call(t, h, http.MethodPost, savePath(ids["a1"]), nil, &again)
	wantStatus(t, rec, http.StatusOK)
	if again.ID != app.ID || rec.Header().Get("Location") != "" {
		t.Errorf("second save = %+v (Location %q), want application %d and no Location", again, rec.Header().Get("Location"), app.ID)
	}

	// The inbox shows which postings are saved.
	if got := inboxEntry(t, h, ids["a1"]).ApplicationID; got == nil || *got != app.ID {
		t.Errorf("a1 application_id = %v, want %d", got, app.ID)
	}
	if got := inboxEntry(t, h, ids["a2"]).ApplicationID; got != nil {
		t.Errorf("a2 application_id = %d, want null", *got)
	}

	// Deleting the application frees the posting to be saved afresh.
	wantStatus(t, call(t, h, http.MethodDelete, appPath(app.ID), nil, nil), http.StatusNoContent)
	if got := inboxEntry(t, h, ids["a1"]).ApplicationID; got != nil {
		t.Errorf("a1 application_id after delete = %d, want null", *got)
	}
	var fresh applicationJSON
	wantStatus(t, call(t, h, http.MethodPost, savePath(ids["a1"]), nil, &fresh), http.StatusCreated)

	// Deleting the watch deletes its postings but keeps the saved application.
	wantStatus(t, call(t, h, http.MethodDelete, "/api/v1/watches/"+strconv.FormatInt(w, 10), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, appPath(fresh.ID), nil, nil), http.StatusOK)
	wantStatus(t, call(t, h, http.MethodPost, savePath(ids["a1"]), nil, nil), http.StatusNotFound)
}

func TestSavePostingFitsApplicationLimits(t *testing.T) {
	t.Parallel()
	h, st := newDBRouterAndStore(t)
	w := mustCreateWatch(t, h, mustCreateCompany(t, h, "Acme"), "greenhouse", "acme")
	// Postings allow longer titles and locations than applications do; an empty
	// location is stored as no location.
	long, err := st.UpsertPosting(t.Context(), store.UpsertPostingParams{
		WatchID: w, ExternalID: "long", Title: strings.Repeat("é", 300), Url: "https://jobs.example/long",
		Location: strings.Repeat("x", 199) + " " + strings.Repeat("y", 50),
	})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.UpsertPosting(t.Context(), store.UpsertPostingParams{
		WatchID: w, ExternalID: "empty", Title: "Engineer", Url: "https://jobs.example/empty",
	})
	if err != nil {
		t.Fatal(err)
	}

	var app applicationJSON
	wantStatus(t, call(t, h, http.MethodPost, savePath(long.ID), nil, &app), http.StatusCreated)
	if n := utf8.RuneCountInString(app.Role); n != maxRoleLen {
		t.Errorf("role is %d characters, want cut to %d", n, maxRoleLen)
	}
	if got, want := deref(app.Location), strings.Repeat("x", 199); got != want { // cut at 200, then trimmed
		t.Errorf("location = %q, want %q", got, want)
	}
	wantStatus(t, call(t, h, http.MethodPost, savePath(empty.ID), nil, &app), http.StatusCreated)
	if app.Location != nil {
		t.Errorf("location = %q, want null", *app.Location)
	}
}

func TestSavePostingNotFound(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	wantStatus(t, call(t, h, http.MethodPost, savePath(999999), nil, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/postings/x/application", nil, nil), http.StatusBadRequest)
}

// Two tabs (or a double click) saving the same posting at once still get one application.
func TestSavePostingConcurrently(t *testing.T) {
	t.Parallel()
	h, st := newDBRouterAndStore(t)
	w := mustCreateWatch(t, h, mustCreateCompany(t, h, "Acme"), "lever", "acme")
	id := addPostings(t, st, w, "p")["p"]

	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, savePath(id), nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes[i] = rec.Code
		})
	}
	wg.Wait()

	created, found := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			found++
		}
	}
	if created != 1 || found != n-1 {
		t.Errorf("status codes = %v, want one 201 and the rest 200", codes)
	}
	var page struct {
		Applications []applicationJSON `json:"applications"`
	}
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/applications", nil, &page), http.StatusOK)
	if len(page.Applications) != 1 {
		t.Errorf("%d applications, want 1", len(page.Applications))
	}
}
