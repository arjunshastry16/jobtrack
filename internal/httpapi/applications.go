package httpapi

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

// Application statuses, in rough pipeline order. Must match the CHECK constraint in
// migrations/00002_create_applications.sql.
var applicationStatuses = []string{
	"wishlist", "applied", "screening", "interviewing",
	"offer", "accepted", "rejected", "withdrawn",
}

const (
	defaultApplicationStatus = "applied"
	maxRoleLen               = 200
	maxSourceLen             = 100
	maxLocationLen           = 200
	maxSalary                = 100_000_000

	msgApplicationNotFound = "application not found"
)

// applicationJSON is the API representation of an application.
type applicationJSON struct {
	ID             int64     `json:"id"`
	CompanyID      int64     `json:"company_id"`
	CompanyName    string    `json:"company_name"`
	Role           string    `json:"role"`
	JobURL         *string   `json:"job_url"`
	Source         *string   `json:"source"`
	Location       *string   `json:"location"`
	SalaryMin      *int32    `json:"salary_min"`
	SalaryMax      *int32    `json:"salary_max"`
	SalaryCurrency *string   `json:"salary_currency"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toApplicationJSON(a store.Application, companyName string) applicationJSON {
	return applicationJSON{
		ID:             a.ID,
		CompanyID:      a.CompanyID,
		CompanyName:    companyName,
		Role:           a.Role,
		JobURL:         a.JobUrl,
		Source:         a.Source,
		Location:       a.Location,
		SalaryMin:      a.SalaryMin,
		SalaryMax:      a.SalaryMax,
		SalaryCurrency: a.SalaryCurrency,
		Status:         a.Status,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
}

// applicationFields are the editable fields: the PUT body, and part of the POST body.
type applicationFields struct {
	CompanyID      int64   `json:"company_id"`
	Role           string  `json:"role"`
	JobURL         *string `json:"job_url"`
	Source         *string `json:"source"`
	Location       *string `json:"location"`
	SalaryMin      *int32  `json:"salary_min"`
	SalaryMax      *int32  `json:"salary_max"`
	SalaryCurrency *string `json:"salary_currency"`
}

// applicationCreateInput is the POST body: the editable fields plus an optional initial
// status. Embedding promotes the fields, so the JSON is flat.
type applicationCreateInput struct {
	applicationFields
	Status *string `json:"status"`
}

// normalize trims and checks the fields, adding any problems to errs.
func (f *applicationFields) normalize(errs map[string]string) {
	if f.CompanyID <= 0 {
		errs["company_id"] = "is required"
	}

	f.Role = strings.TrimSpace(f.Role)
	switch {
	case f.Role == "":
		errs["role"] = "is required"
	case utf8.RuneCountInString(f.Role) > maxRoleLen:
		errs["role"] = fmt.Sprintf("must be at most %d characters", maxRoleLen)
	}

	f.JobURL = trimOptional(f.JobURL)
	if f.JobURL != nil && !isWebURL(*f.JobURL) {
		errs["job_url"] = "must be an http:// or https:// URL"
	}
	f.Source = trimOptional(f.Source)
	checkMaxLen(errs, "source", f.Source, maxSourceLen)
	f.Location = trimOptional(f.Location)
	checkMaxLen(errs, "location", f.Location, maxLocationLen)

	for field, v := range map[string]*int32{"salary_min": f.SalaryMin, "salary_max": f.SalaryMax} {
		if v != nil && (*v < 0 || *v > maxSalary) {
			errs[field] = fmt.Sprintf("must be between 0 and %d", maxSalary)
		}
	}
	if f.SalaryMin != nil && f.SalaryMax != nil && *f.SalaryMin > *f.SalaryMax {
		errs["salary_max"] = "must not be less than salary_min"
	}

	f.SalaryCurrency = trimOptional(f.SalaryCurrency)
	if f.SalaryCurrency != nil {
		cur := strings.ToUpper(*f.SalaryCurrency)
		f.SalaryCurrency = &cur
		if !isCurrencyCode(cur) {
			errs["salary_currency"] = "must be a 3-letter ISO 4217 code, e.g. USD"
		}
	} else if f.SalaryMin != nil || f.SalaryMax != nil {
		errs["salary_currency"] = "is required when a salary is given"
	}
}

// trimOptional trims s; an empty result becomes nil (absent).
func trimOptional(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func checkMaxLen(errs map[string]string, field string, s *string, maxLen int) {
	if s != nil && utf8.RuneCountInString(*s) > maxLen {
		errs[field] = fmt.Sprintf("must be at most %d characters", maxLen)
	}
}

func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func (a *api) createApplication(w http.ResponseWriter, r *http.Request) {
	var in applicationCreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	errs := map[string]string{}
	in.normalize(errs)
	status := defaultApplicationStatus
	if in.Status != nil {
		status = *in.Status
		if !slices.Contains(applicationStatuses, status) {
			errs["status"] = "must be one of: " + strings.Join(applicationStatuses, ", ")
		}
	}
	if len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	f := in.applicationFields
	id, err := a.store.CreateApplication(r.Context(), store.CreateApplicationParams{
		CompanyID: f.CompanyID, Role: f.Role, JobUrl: f.JobURL, Source: f.Source, Location: f.Location,
		SalaryMin: f.SalaryMin, SalaryMax: f.SalaryMax, SalaryCurrency: f.SalaryCurrency, Status: status,
	})
	if pgCode(err) == pgForeignKeyViolation {
		writeValidationError(w, map[string]string{"company_id": "does not exist"})
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}

	// Re-read so the response includes the company name (and DB defaults like timestamps).
	row, err := a.store.GetApplication(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/applications/"+strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusCreated, toApplicationJSON(row.Application, row.CompanyName))
}

// listApplications returns one page, newest first.
//
//	GET /api/v1/applications?status=applied&company_id=3&limit=50&cursor=<next_cursor>
func (a *api) listApplications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	errs := map[string]string{}
	var params store.ListApplicationsParams

	pageSize, err := parsePageSize(q.Get("limit"))
	if err != nil {
		errs["limit"] = err.Error()
	}
	if s := q.Get("status"); s != "" {
		if !slices.Contains(applicationStatuses, s) {
			errs["status"] = "must be one of: " + strings.Join(applicationStatuses, ", ")
		}
		params.Status = &s
	}
	if s := q.Get("company_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			errs["company_id"] = "must be a positive integer"
		}
		params.CompanyID = &id
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
	rows, err := a.store.ListApplications(r.Context(), params)
	if err != nil {
		a.internalError(w, r, err)
		return
	}

	var next *string
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		c := encodeCursor(rows[len(rows)-1].Application.ID)
		next = &c
	}
	out := make([]applicationJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toApplicationJSON(row.Application, row.CompanyName))
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out, "next_cursor": next})
}

func (a *api) getApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	row, err := a.store.GetApplication(r.Context(), id)
	if err != nil {
		a.storeError(w, r, err, msgApplicationNotFound, "")
		return
	}
	writeJSON(w, http.StatusOK, toApplicationJSON(row.Application, row.CompanyName))
}

// updateApplication replaces the editable fields (PUT). Sending "status" is rejected as an
// unknown field: stage changes get their own endpoint, which also records history.
func (a *api) updateApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var f applicationFields
	if !decodeJSON(w, r, &f) {
		return
	}
	errs := map[string]string{}
	f.normalize(errs)
	if len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	n, err := a.store.UpdateApplication(r.Context(), store.UpdateApplicationParams{
		ID: id, CompanyID: f.CompanyID, Role: f.Role, JobUrl: f.JobURL, Source: f.Source, Location: f.Location,
		SalaryMin: f.SalaryMin, SalaryMax: f.SalaryMax, SalaryCurrency: f.SalaryCurrency,
	})
	if pgCode(err) == pgForeignKeyViolation {
		writeValidationError(w, map[string]string{"company_id": "does not exist"})
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, msgApplicationNotFound)
		return
	}

	row, err := a.store.GetApplication(r.Context(), id)
	if err != nil {
		a.storeError(w, r, err, msgApplicationNotFound, "")
		return
	}
	writeJSON(w, http.StatusOK, toApplicationJSON(row.Application, row.CompanyName))
}

func (a *api) deleteApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := a.store.DeleteApplication(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, msgApplicationNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
