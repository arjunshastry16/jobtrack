package httpapi

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/boards"
	"github.com/arjunshastry16/jobtrack/internal/match"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

const (
	// Limits for each of a watch's term lists (keywords, locations).
	maxTerms   = 20
	maxTermLen = 50

	msgWatchNotFound = "watch not found"
	msgWatchConflict = "this board is already being watched"
)

type watchJSON struct {
	ID            int64      `json:"id"`
	CompanyID     int64      `json:"company_id"`
	Board         string     `json:"board"`
	BoardToken    string     `json:"board_token"`
	Keywords      []string   `json:"keywords"`
	Locations     []string   `json:"locations"`
	Enabled       bool       `json:"enabled"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastError     *string    `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func toWatchJSON(w store.Watch) watchJSON {
	return watchJSON{
		ID: w.ID, CompanyID: w.CompanyID, Board: w.Board, BoardToken: w.BoardToken,
		Keywords: nonNil(w.Keywords), Locations: nonNil(w.Locations), Enabled: w.Enabled, LastCheckedAt: w.LastCheckedAt, LastError: w.LastError,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
}

// watchInput is the body for create (POST) and replace (PUT).
type watchInput struct {
	CompanyID  int64    `json:"company_id"`
	Board      string   `json:"board"`
	BoardToken string   `json:"board_token"`
	Keywords   []string `json:"keywords"`
	Locations  []string `json:"locations"`
	Enabled    *bool    `json:"enabled"` // default true
}

// watchFields is a validated watchInput's derived values.
type watchFields struct {
	keywords, locations []string
	enabled             bool
}

// normalize validates the input, cleaning Board and BoardToken in place, and returns the
// cleaned term lists and enabled flag.
func (in *watchInput) normalize() (f watchFields, errs map[string]string) {
	errs = map[string]string{}
	if in.CompanyID <= 0 {
		errs["company_id"] = "is required"
	}

	in.Board = strings.ToLower(strings.TrimSpace(in.Board))
	if !slices.Contains(boards.Supported, in.Board) {
		errs["board"] = "must be one of: " + strings.Join(boards.Supported, ", ")
	}

	// Usually the company's id on the board; for Workday, the careers-site URL, stored in
	// canonical form.
	tok, err := boards.NormalizeToken(in.Board, in.BoardToken)
	if err != nil {
		if in.Board == "workday" {
			errs["board_token"] = "must be the careers-site URL, e.g. https://<company>.wd5.myworkdayjobs.com/<site>: " + err.Error()
		} else {
			errs["board_token"] = err.Error()
		}
	}
	in.BoardToken = tok

	f.keywords = normalizeTerms(in.Keywords, "keywords", errs)
	f.locations = normalizeTerms(in.Locations, "locations", errs)
	f.enabled = in.Enabled == nil || *in.Enabled
	if len(errs) == 0 {
		errs = nil
	}
	return f, errs
}

// normalizeTerms cleans a list of filter terms: stored in match.Normalize form (the form
// they're matched in), without empty terms or duplicates, in the order given.
func normalizeTerms(raw []string, field string, errs map[string]string) []string {
	terms := []string{}
	for _, t := range raw {
		t = match.Normalize(t)
		if t == "" || slices.Contains(terms, t) {
			continue
		}
		if utf8.RuneCountInString(t) > maxTermLen {
			errs[field] = fmt.Sprintf("each entry must be at most %d characters", maxTermLen)
		}
		terms = append(terms, t)
	}
	if len(terms) > maxTerms {
		errs[field] = fmt.Sprintf("at most %d entries", maxTerms)
	}
	return terms
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *api) createWatch(w http.ResponseWriter, r *http.Request) {
	var in watchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	f, errs := in.normalize()
	if errs != nil {
		writeValidationError(w, errs)
		return
	}
	wt, err := a.store.CreateWatch(r.Context(), store.CreateWatchParams{
		CompanyID: in.CompanyID, Board: in.Board, BoardToken: in.BoardToken,
		Keywords: f.keywords, Locations: f.locations, Enabled: f.enabled,
	})
	if pgCode(err) == pgForeignKeyViolation {
		writeValidationError(w, map[string]string{"company_id": "does not exist"})
		return
	}
	if err != nil {
		a.storeError(w, r, err, msgWatchNotFound, msgWatchConflict)
		return
	}
	w.Header().Set("Location", "/api/v1/watches/"+strconv.FormatInt(wt.ID, 10))
	writeJSON(w, http.StatusCreated, toWatchJSON(wt))
}

func (a *api) listWatches(w http.ResponseWriter, r *http.Request) {
	ws, err := a.store.ListWatches(r.Context())
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	out := make([]watchJSON, 0, len(ws))
	for _, wt := range ws {
		out = append(out, toWatchJSON(wt))
	}
	writeJSON(w, http.StatusOK, map[string]any{"watches": out})
}

func (a *api) getWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	wt, err := a.store.GetWatch(r.Context(), id)
	if err != nil {
		a.storeError(w, r, err, msgWatchNotFound, msgWatchConflict)
		return
	}
	writeJSON(w, http.StatusOK, toWatchJSON(wt))
}

func (a *api) updateWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in watchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	f, errs := in.normalize()
	if errs != nil {
		writeValidationError(w, errs)
		return
	}
	wt, err := a.store.UpdateWatch(r.Context(), store.UpdateWatchParams{
		ID: id, CompanyID: in.CompanyID, Board: in.Board, BoardToken: in.BoardToken,
		Keywords: f.keywords, Locations: f.locations, Enabled: f.enabled,
	})
	if pgCode(err) == pgForeignKeyViolation {
		writeValidationError(w, map[string]string{"company_id": "does not exist"})
		return
	}
	if err != nil {
		a.storeError(w, r, err, msgWatchNotFound, msgWatchConflict)
		return
	}
	writeJSON(w, http.StatusOK, toWatchJSON(wt))
}

func (a *api) deleteWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := a.store.DeleteWatch(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, msgWatchNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
