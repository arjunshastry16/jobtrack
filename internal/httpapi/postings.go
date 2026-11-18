package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/stages"
	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/jackc/pgx/v5"
)

const msgPostingNotFound = "posting not found"

// postingJSON is a job posting found by a watch.
type postingJSON struct {
	ID          int64      `json:"id"`
	WatchID     int64      `json:"watch_id"`
	CompanyID   int64      `json:"company_id"`
	CompanyName string     `json:"company_name"`
	Board       string     `json:"board"`
	Title       string     `json:"title"`
	Location    string     `json:"location"`
	URL         string     `json:"url"`
	PublishedAt *time.Time `json:"published_at"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	RemovedAt   *time.Time `json:"removed_at"`
	// ApplicationID is the application saved from this posting, if any.
	ApplicationID *int64 `json:"application_id"`
}

func toPostingJSON(p store.ListPostingsRow) postingJSON {
	return postingJSON{
		ID: p.ID, WatchID: p.WatchID, CompanyID: p.CompanyID, CompanyName: p.CompanyName, Board: p.Board,
		Title: p.Title, Location: p.Location, URL: p.Url, PublishedAt: p.PublishedAt,
		FirstSeenAt: p.FirstSeenAt, LastSeenAt: p.LastSeenAt, RemovedAt: p.RemovedAt,
		ApplicationID: p.ApplicationID,
	}
}

// listPostings is the inbox: postings found by watches, newest first.
func (a *api) listPostings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	errs := map[string]string{}
	params := store.ListPostingsParams{
		WatchID:   queryID(q, "watch_id", errs),
		CompanyID: queryID(q, "company_id", errs),
	}

	pageSize, err := parsePageSize(q.Get("limit"))
	if err != nil {
		errs["limit"] = err.Error()
	}
	if s := q.Get("include_removed"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			errs["include_removed"] = "must be true or false"
		}
		params.IncludeRemoved = b
	}
	if s := q.Get("cursor"); s != "" {
		id, err := decodeCursor(s)
		if err != nil {
			errs["cursor"] = err.Error()
		}
		params.BeforeID = &id
	}
	if len(errs) > 0 {
		writeQueryError(w, errs)
		return
	}

	// Ask for one extra row: if it comes back, there is another page.
	params.RowLimit = int32(pageSize) + 1 //nolint:gosec // pageSize is validated to 1..maxPageSize
	rows, err := a.store.ListPostings(r.Context(), params)
	if err != nil {
		a.internalError(w, r, err)
		return
	}

	var next *string
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		c := encodeCursor(rows[len(rows)-1].ID)
		next = &c
	}
	out := make([]postingJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPostingJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"postings": out, "next_cursor": next})
}

// savePosting turns a posting into a wishlist application, filled in from the posting.
// Saving is idempotent: a posting already saved answers 200 with its application instead
// of creating a second one (201), so a double click or a retried request is harmless.
//
//	POST /api/v1/postings/{id}/application
func (a *api) savePosting(w http.ResponseWriter, r *http.Request) {
	postingID, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var appID int64
	created := false
	// The application and its first history event are saved together or not at all.
	err := a.store.InTx(ctx, func(q store.Querier) error {
		id, err := q.CreateApplicationFromPosting(ctx, postingID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Already saved, or no such posting (then this is ErrNoRows too: a 404).
			appID, err = q.GetApplicationIDByPosting(ctx, postingID)
			return err
		}
		if err != nil {
			return err
		}
		appID, created = id, true
		_, err = q.CreateApplicationEvent(ctx, store.CreateApplicationEventParams{
			ApplicationID: id, ToStatus: stages.Wishlist,
		})
		return err
	})
	if err != nil {
		a.storeError(w, r, err, msgPostingNotFound, "")
		return
	}

	row, err := a.store.GetApplication(ctx, appID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/applications/"+strconv.FormatInt(appID, 10))
	}
	writeJSON(w, status, toApplicationJSON(row.Application, row.CompanyName))
}

// queryID reads an optional positive id from the query string. A bad value is noted in
// errs under name.
func queryID(q url.Values, name string, errs map[string]string) *int64 {
	s := q.Get(name)
	if s == "" {
		return nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		errs[name] = "must be a positive integer"
		return nil
	}
	return &id
}
