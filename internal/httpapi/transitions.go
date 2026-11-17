package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arjunshastry16/jobtrack/internal/stages"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

const (
	maxNoteLen = 2000
	// clockSkew tolerates a client clock slightly ahead of the server's when backdating.
	clockSkew = 5 * time.Minute
)

// eventJSON is one entry in an application's stage history.
type eventJSON struct {
	ID         int64     `json:"id"`
	FromStatus *string   `json:"from_status"` // null for the creation event
	ToStatus   string    `json:"to_status"`
	Note       *string   `json:"note"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
}

func toEventJSON(e store.ApplicationEvent) eventJSON {
	return eventJSON{
		ID: e.ID, FromStatus: e.FromStatus, ToStatus: e.ToStatus,
		Note: e.Note, OccurredAt: e.OccurredAt, CreatedAt: e.CreatedAt,
	}
}

// transitionInput is the body of POST /applications/{id}/transitions.
type transitionInput struct {
	To         string     `json:"to"`
	Note       *string    `json:"note"`
	OccurredAt *time.Time `json:"occurred_at"` // optional backdating, RFC 3339
}

// transitionError: the state machine does not allow from -> to.
type transitionError struct{ from, to string }

func (e *transitionError) Error() string {
	allowed := stages.Next(e.from)
	if len(allowed) == 0 {
		return fmt.Sprintf("cannot move from %s to %s: %s is a final status", e.from, e.to, e.from)
	}
	return fmt.Sprintf("cannot move from %s to %s; allowed: %s", e.from, e.to, strings.Join(allowed, ", "))
}

// tooEarlyError: a backdated occurred_at would put the event before the previous one.
type tooEarlyError struct{ previous time.Time }

func (e *tooEarlyError) Error() string {
	return "must not be before the previous stage change (" + e.previous.UTC().Format(time.RFC3339) + ")"
}

// transitionApplication moves an application to a new status and records the change,
// atomically: either both the status update and the history event are saved, or neither.
//
//	POST /api/v1/applications/{id}/transitions  {"to": "screening", "note": "...", "occurred_at": "..."}
func (a *api) transitionApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in transitionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	errs := map[string]string{}
	if !stages.Valid(in.To) {
		errs["to"] = "must be one of: " + strings.Join(stages.All, ", ")
	}
	in.Note = trimOptional(in.Note)
	checkMaxLen(errs, "note", in.Note, maxNoteLen)
	if in.OccurredAt != nil && in.OccurredAt.After(time.Now().Add(clockSkew)) {
		errs["occurred_at"] = "must not be in the future"
	}
	if len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	ctx := r.Context()
	var event store.ApplicationEvent
	err := a.store.InTx(ctx, func(q store.Querier) error {
		// Lock the row: a concurrent transition waits here, then sees our new status.
		from, err := q.LockApplicationStatus(ctx, id)
		if err != nil {
			return err
		}
		if !stages.CanTransition(from, in.To) {
			return &transitionError{from: from, to: in.To}
		}
		if err := checkNotBeforeLatest(ctx, q, id, in.OccurredAt); err != nil {
			return err
		}
		if err := q.SetApplicationStatus(ctx, store.SetApplicationStatusParams{ID: id, Status: in.To}); err != nil {
			return err
		}
		event, err = q.CreateApplicationEvent(ctx, store.CreateApplicationEventParams{
			ApplicationID: id, FromStatus: &from, ToStatus: in.To, Note: in.Note, OccurredAt: in.OccurredAt,
		})
		return err
	})

	var tErr *transitionError
	var early *tooEarlyError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, codeNotFound, msgApplicationNotFound)
		return
	case errors.As(err, &tErr):
		writeError(w, http.StatusConflict, codeInvalidTransition, tErr.Error())
		return
	case errors.As(err, &early):
		writeValidationError(w, map[string]string{"occurred_at": early.Error()})
		return
	case err != nil:
		a.internalError(w, r, err)
		return
	}

	row, err := a.store.GetApplication(ctx, id)
	if err != nil {
		a.storeError(w, r, err, msgApplicationNotFound, "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"application": toApplicationJSON(row.Application, row.CompanyName),
		"event":       toEventJSON(event),
	})
}

// checkNotBeforeLatest keeps the timeline in order: a backdated event may not precede
// the application's most recent one. A nil occurredAt means "now", which is always fine.
func checkNotBeforeLatest(ctx context.Context, q store.Querier, appID int64, occurredAt *time.Time) error {
	if occurredAt == nil {
		return nil
	}
	latest, err := q.LatestApplicationEventTime(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no history yet
	}
	if err != nil {
		return err
	}
	if occurredAt.Before(latest) {
		return &tooEarlyError{previous: latest}
	}
	return nil
}

// applicationTimeline returns the application's stage history, oldest first.
//
//	GET /api/v1/applications/{id}/timeline
func (a *api) applicationTimeline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	row, err := a.store.GetApplication(r.Context(), id)
	if err != nil {
		a.storeError(w, r, err, msgApplicationNotFound, "")
		return
	}
	events, err := a.store.ListApplicationEvents(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, toEventJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application_id": id,
		"status":         row.Application.Status,
		"events":         out,
	})
}
