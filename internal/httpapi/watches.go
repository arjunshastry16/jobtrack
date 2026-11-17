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
	"github.com/arjunshastry16/jobtrack/internal/store"
)

const (
	maxKeywords   = 20
	maxKeywordLen = 50

	msgWatchNotFound = "watch not found"
	msgWatchConflict = "this board is already being watched"
)

type watchJSON struct {
	ID            int64      `json:"id"`
	CompanyID     int64      `json:"company_id"`
	Board         string     `json:"board"`
	BoardToken    string     `json:"board_token"`
	Keywords      []string   `json:"keywords"`
	Enabled       bool       `json:"enabled"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastError     *string    `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func toWatchJSON(w store.Watch) watchJSON {
	kw := w.Keywords
	if kw == nil {
		kw = []string{}
	}
	return watchJSON{
		ID: w.ID, CompanyID: w.CompanyID, Board: w.Board, BoardToken: w.BoardToken, Keywords: kw,
		Enabled: w.Enabled, LastCheckedAt: w.LastCheckedAt, LastError: w.LastError,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
}

// watchInput is the body for create (POST) and replace (PUT).
type watchInput struct {
	CompanyID  int64    `json:"company_id"`
	Board      string   `json:"board"`
	BoardToken string   `json:"board_token"`
	Keywords   []string `json:"keywords"`
	Enabled    *bool    `json:"enabled"` // default true
}

// normalize validates the input and returns the cleaned keyword list and enabled flag.
func (in *watchInput) normalize() (keywords []string, enabled bool, errs map[string]string) {
	errs = map[string]string{}
	if in.CompanyID <= 0 {
		errs["company_id"] = "is required"
	}

	in.Board = strings.ToLower(strings.TrimSpace(in.Board))
	if !slices.Contains(boards.Supported, in.Board) {
		errs["board"] = "must be one of: " + strings.Join(boards.Supported, ", ")
	}

	in.BoardToken = strings.TrimSpace(in.BoardToken)
	if !boards.ValidToken(in.BoardToken) {
		errs["board_token"] = "must be the company's id on the board, e.g. \"stripe\" (letters, digits, . _ -)"
	}

	// Keywords are matched case-insensitively against titles, so store them lower-case,
	// trimmed and without duplicates, keeping the order given.
	keywords = []string{}
	for _, k := range in.Keywords {
		k = strings.ToLower(strings.Join(strings.Fields(k), " "))
		if k == "" || slices.Contains(keywords, k) {
			continue
		}
		if utf8.RuneCountInString(k) > maxKeywordLen {
			errs["keywords"] = fmt.Sprintf("each keyword must be at most %d characters", maxKeywordLen)
		}
		keywords = append(keywords, k)
	}
	if len(keywords) > maxKeywords {
		errs["keywords"] = fmt.Sprintf("at most %d keywords", maxKeywords)
	}

	enabled = in.Enabled == nil || *in.Enabled
	if len(errs) == 0 {
		errs = nil
	}
	return keywords, enabled, errs
}

func (a *api) createWatch(w http.ResponseWriter, r *http.Request) {
	var in watchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	keywords, enabled, errs := in.normalize()
	if errs != nil {
		writeValidationError(w, errs)
		return
	}
	wt, err := a.store.CreateWatch(r.Context(), store.CreateWatchParams{
		CompanyID: in.CompanyID, Board: in.Board, BoardToken: in.BoardToken, Keywords: keywords, Enabled: enabled,
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
	keywords, enabled, errs := in.normalize()
	if errs != nil {
		writeValidationError(w, errs)
		return
	}
	wt, err := a.store.UpdateWatch(r.Context(), store.UpdateWatchParams{
		ID: id, CompanyID: in.CompanyID, Board: in.Board, BoardToken: in.BoardToken, Keywords: keywords, Enabled: enabled,
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
