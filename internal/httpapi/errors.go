package httpapi

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Every error response has the same shape, so clients handle errors in one place:
//
//	{"error": {"code": "validation_failed", "message": "...", "fields": {"name": "is required"}}}
//
// code is stable and meant for programs; message is for humans and may change.
type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Error codes returned by the API.
const (
	codeInvalidJSON       = "invalid_json"
	codeBodyTooLarge      = "body_too_large"
	codeValidationFailed  = "validation_failed"
	codeInvalidID         = "invalid_id"
	codeInvalidQuery      = "invalid_query"
	codeNotFound          = "not_found"
	codeConflict          = "conflict"
	codeInvalidTransition = "invalid_transition"
	codeInternal          = "internal"
)

// Postgres SQLSTATE codes the API maps to client errors.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503" // insert/update references a missing row (or NO ACTION delete)
	pgRestrictViolation   = "23001" // delete blocked by an ON DELETE RESTRICT foreign key
)

// pgCode returns the Postgres SQLSTATE of err, or "" if it isn't a Postgres error.
func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// pgConstraint returns the name of the constraint a Postgres error is about, or "".
func pgConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// writeQueryError reports invalid query-string parameters (400).
func writeQueryError(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: apiError{
		Code:    codeInvalidQuery,
		Message: "one or more query parameters are invalid",
		Fields:  fields,
	}})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message}})
}

// writeValidationError reports which fields are wrong and why (422 Unprocessable Entity:
// the JSON was well-formed but its values are not acceptable).
func writeValidationError(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: apiError{
		Code:    codeValidationFailed,
		Message: "one or more fields are invalid",
		Fields:  fields,
	}})
}

// storeError maps a database error to a response: no rows -> 404, unique violation -> 409,
// anything else -> 500 (logged, with no details sent to the client).
func (a *api) storeError(w http.ResponseWriter, r *http.Request, err error, notFoundMsg, conflictMsg string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, codeNotFound, notFoundMsg)
	case pgCode(err) == pgUniqueViolation:
		writeError(w, http.StatusConflict, codeConflict, conflictMsg)
	default:
		a.internalError(w, r, err)
	}
}

func (a *api) internalError(w http.ResponseWriter, r *http.Request, err error) {
	a.logger.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
}
