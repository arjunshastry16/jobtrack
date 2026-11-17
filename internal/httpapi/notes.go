package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

const (
	maxNoteBodyLen  = 10_000
	msgNoteNotFound = "note not found"
)

type noteJSON struct {
	ID            int64     `json:"id"`
	ApplicationID int64     `json:"application_id"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toNoteJSON(n store.Note) noteJSON {
	return noteJSON(n) // identical fields; the conversion keeps API and DB types independent
}

type noteInput struct {
	Body string `json:"body"`
}

func (in *noteInput) normalize() map[string]string {
	in.Body = strings.TrimSpace(in.Body)
	switch {
	case in.Body == "":
		return map[string]string{"body": "is required"}
	case utf8.RuneCountInString(in.Body) > maxNoteBodyLen:
		return map[string]string{"body": fmt.Sprintf("must be at most %d characters", maxNoteBodyLen)}
	}
	return nil
}

// createNote adds a note to an application.
//
//	POST /api/v1/applications/{id}/notes  {"body": "..."}
func (a *api) createNote(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in noteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if errs := in.normalize(); errs != nil {
		writeValidationError(w, errs)
		return
	}
	n, err := a.store.CreateNote(r.Context(), store.CreateNoteParams{ApplicationID: appID, Body: in.Body})
	if pgCode(err) == pgForeignKeyViolation {
		writeError(w, http.StatusNotFound, codeNotFound, msgApplicationNotFound)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toNoteJSON(n))
}

// listNotes returns an application's notes, oldest first.
func (a *api) listNotes(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(w, r)
	if !ok || !a.requireApplication(w, r, appID) {
		return
	}
	notes, err := a.store.ListNotes(r.Context(), appID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	out := make([]noteJSON, 0, len(notes))
	for _, n := range notes {
		out = append(out, toNoteJSON(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": out})
}

func (a *api) updateNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in noteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if errs := in.normalize(); errs != nil {
		writeValidationError(w, errs)
		return
	}
	n, err := a.store.UpdateNote(r.Context(), store.UpdateNoteParams{ID: id, Body: in.Body})
	if err != nil {
		a.storeError(w, r, err, msgNoteNotFound, "")
		return
	}
	writeJSON(w, http.StatusOK, toNoteJSON(n))
}

func (a *api) deleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := a.store.DeleteNote(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, msgNoteNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
