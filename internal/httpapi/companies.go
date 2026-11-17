package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

const (
	maxCompanyNameLen = 200
	maxURLLen         = 2048
	// companyListLimit caps GET /companies. Companies are few per user; cursor
	// pagination arrives with applications, which can grow much larger.
	companyListLimit = 500

	msgCompanyNotFound = "company not found"
	msgCompanyConflict = "a company with this name already exists"
)

// companyJSON is the API representation of a company. It is separate from the
// database model (store.Company) so the schema can change without breaking clients.
type companyJSON struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Website   *string   `json:"website"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toCompanyJSON(c store.Company) companyJSON {
	return companyJSON(c) // same fields today; the conversion keeps the types independent
}

// companyInput is the request body for create (POST) and replace (PUT).
type companyInput struct {
	Name    string  `json:"name"`
	Website *string `json:"website"`
}

// normalize trims whitespace and checks the input. It returns field errors, or nil if valid.
func (in *companyInput) normalize() map[string]string {
	errs := map[string]string{}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		errs["name"] = "is required"
	case utf8.RuneCountInString(in.Name) > maxCompanyNameLen:
		errs["name"] = fmt.Sprintf("must be at most %d characters", maxCompanyNameLen)
	}

	if in.Website != nil {
		site := strings.TrimSpace(*in.Website)
		switch {
		case site == "":
			in.Website = nil // treat "" as "no website"
		case !isWebURL(site):
			errs["website"] = "must be an http:// or https:// URL"
		default:
			in.Website = &site
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

func isWebURL(s string) bool {
	if len(s) > maxURLLen {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// pathID parses the {id} path segment. It writes a 400 and returns false if it isn't a positive integer.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, codeInvalidID, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func (a *api) createCompany(w http.ResponseWriter, r *http.Request) {
	var in companyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if errs := in.normalize(); errs != nil {
		writeValidationError(w, errs)
		return
	}

	c, err := a.store.CreateCompany(r.Context(), store.CreateCompanyParams{Name: in.Name, Website: in.Website})
	if err != nil {
		a.storeError(w, r, err, msgCompanyNotFound, msgCompanyConflict)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/companies/%d", c.ID))
	writeJSON(w, http.StatusCreated, toCompanyJSON(c))
}

func (a *api) listCompanies(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListCompanies(r.Context(), companyListLimit)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	// make, not a nil slice, so an empty list encodes as [] rather than null.
	out := make([]companyJSON, 0, len(rows))
	for _, c := range rows {
		out = append(out, toCompanyJSON(c))
	}
	// Wrapped in an object so fields like pagination cursors can be added without breaking clients.
	writeJSON(w, http.StatusOK, map[string]any{"companies": out})
}

func (a *api) getCompany(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := a.store.GetCompany(r.Context(), id)
	if err != nil {
		a.storeError(w, r, err, msgCompanyNotFound, msgCompanyConflict)
		return
	}
	writeJSON(w, http.StatusOK, toCompanyJSON(c))
}

// updateCompany replaces the company's editable fields (PUT semantics: omitted website clears it).
func (a *api) updateCompany(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in companyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if errs := in.normalize(); errs != nil {
		writeValidationError(w, errs)
		return
	}

	c, err := a.store.UpdateCompany(r.Context(), store.UpdateCompanyParams{ID: id, Name: in.Name, Website: in.Website})
	if err != nil {
		a.storeError(w, r, err, msgCompanyNotFound, msgCompanyConflict)
		return
	}
	writeJSON(w, http.StatusOK, toCompanyJSON(c))
}

func (a *api) deleteCompany(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := a.store.DeleteCompany(r.Context(), id)
	if code := pgCode(err); code == pgRestrictViolation || code == pgForeignKeyViolation {
		// applications.company_id is ON DELETE RESTRICT (SQLSTATE 23001): never silently
		// delete someone's applications. 23503 is also accepted in case the FK is ever NO ACTION.
		writeError(w, http.StatusConflict, codeConflict, "company still has applications; delete or move them first")
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, msgCompanyNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
