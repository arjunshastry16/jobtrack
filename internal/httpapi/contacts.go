package httpapi

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

const (
	maxContactNameLen = 200
	maxTitleLen       = 200
	maxEmailLen       = 254 // the practical limit for an email address
	contactListLimit  = 500

	msgContactNotFound = "contact not found"
)

type contactJSON struct {
	ID          int64     `json:"id"`
	CompanyID   int64     `json:"company_id"`
	Name        string    `json:"name"`
	Title       *string   `json:"title"`
	Email       *string   `json:"email"`
	LinkedinURL *string   `json:"linkedin_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toContactJSON(c store.Contact) contactJSON {
	return contactJSON{
		ID: c.ID, CompanyID: c.CompanyID, Name: c.Name, Title: c.Title,
		Email: c.Email, LinkedinURL: c.LinkedinUrl, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func toContactsJSON(cs []store.Contact) []contactJSON {
	out := make([]contactJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, toContactJSON(c))
	}
	return out
}

// contactInput is the body for create (POST) and replace (PUT).
type contactInput struct {
	CompanyID   int64   `json:"company_id"`
	Name        string  `json:"name"`
	Title       *string `json:"title"`
	Email       *string `json:"email"`
	LinkedinURL *string `json:"linkedin_url"`
}

func (in *contactInput) normalize() map[string]string {
	errs := map[string]string{}
	if in.CompanyID <= 0 {
		errs["company_id"] = "is required"
	}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		errs["name"] = "is required"
	case utf8.RuneCountInString(in.Name) > maxContactNameLen:
		errs["name"] = fmt.Sprintf("must be at most %d characters", maxContactNameLen)
	}

	in.Title = trimOptional(in.Title)
	checkMaxLen(errs, "title", in.Title, maxTitleLen)

	in.Email = trimOptional(in.Email)
	if in.Email != nil && !isEmail(*in.Email) {
		errs["email"] = "must be a plain email address like name@example.com"
	}

	in.LinkedinURL = trimOptional(in.LinkedinURL)
	if in.LinkedinURL != nil && !isLinkedInURL(*in.LinkedinURL) {
		errs["linkedin_url"] = "must be an https://www.linkedin.com/... URL"
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

// isEmail accepts a bare address only ("a@b.com", not "Name <a@b.com>").
func isEmail(s string) bool {
	if len(s) > maxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && strings.Contains(s[strings.LastIndex(s, "@"):], ".")
}

func isLinkedInURL(s string) bool {
	if !isWebURL(s) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "linkedin.com" || strings.HasSuffix(host, ".linkedin.com")
}

func (a *api) createContact(w http.ResponseWriter, r *http.Request) {
	var in contactInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if errs := in.normalize(); errs != nil {
		writeValidationError(w, errs)
		return
	}
	c, err := a.store.CreateContact(r.Context(), store.CreateContactParams{
		CompanyID: in.CompanyID, Name: in.Name, Title: in.Title, Email: in.Email, LinkedinUrl: in.LinkedinURL,
	})
	if pgCode(err) == pgForeignKeyViolation {
		writeValidationError(w, map[string]string{"company_id": "does not exist"})
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/contacts/"+strconv.FormatInt(c.ID, 10))
	writeJSON(w, http.StatusCreated, toContactJSON(c))
}

// listContacts returns contacts sorted by name, optionally only one company's.
//
//	GET /api/v1/contacts?company_id=3
func (a *api) listContacts(w http.ResponseWriter, r *http.Request) {
	params := store.ListContactsParams{RowLimit: contactListLimit}
	if s := r.URL.Query().Get("company_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			writeQueryError(w, map[string]string{"company_id": "must be a positive integer"})
			return
		}
		params.CompanyID = &id
	}
	cs, err := a.store.ListContacts(r.Context(), params)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": toContactsJSON(cs)})
}

func (a *api) getContact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := a.store.GetContact(r.Context(), id)
	if err != nil {
		a.storeError(w, r, err, msgContactNotFound, "")
		return
	}
	writeJSON(w, http.StatusOK, toContactJSON(c))
}

func (a *api) updateContact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in contactInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if errs := in.normalize(); errs != nil {
		writeValidationError(w, errs)
		return
	}
	c, err := a.store.UpdateContact(r.Context(), store.UpdateContactParams{
		ID: id, CompanyID: in.CompanyID, Name: in.Name, Title: in.Title, Email: in.Email, LinkedinUrl: in.LinkedinURL,
	})
	if pgCode(err) == pgForeignKeyViolation {
		writeValidationError(w, map[string]string{"company_id": "does not exist"})
		return
	}
	if err != nil {
		a.storeError(w, r, err, msgContactNotFound, "")
		return
	}
	writeJSON(w, http.StatusOK, toContactJSON(c))
}

// deleteContact removes the contact and its links to applications (not the applications).
func (a *api) deleteContact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := a.store.DeleteContact(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, msgContactNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// linkContact attaches a contact to an application. It is idempotent (PUT): linking
// twice is the same as linking once.
//
//	PUT /api/v1/applications/{id}/contacts/{contact_id}
func (a *api) linkContact(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(w, r)
	if !ok {
		return
	}
	contactID, ok := pathInt(w, r, "contact_id")
	if !ok {
		return
	}
	err := a.store.LinkContact(r.Context(), store.LinkContactParams{ApplicationID: appID, ContactID: contactID})
	if pgCode(err) == pgForeignKeyViolation {
		// The constraint name says which side is missing.
		msg := msgApplicationNotFound
		if strings.Contains(pgConstraint(err), "contact_id") {
			msg = msgContactNotFound
		}
		writeError(w, http.StatusNotFound, codeNotFound, msg)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unlinkContact detaches a contact from an application (both remain).
func (a *api) unlinkContact(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(w, r)
	if !ok {
		return
	}
	contactID, ok := pathInt(w, r, "contact_id")
	if !ok {
		return
	}
	n, err := a.store.UnlinkContact(r.Context(), store.UnlinkContactParams{ApplicationID: appID, ContactID: contactID})
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, "contact is not linked to this application")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listApplicationContacts(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathID(w, r)
	if !ok || !a.requireApplication(w, r, appID) {
		return
	}
	cs, err := a.store.ListApplicationContacts(r.Context(), appID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": toContactsJSON(cs)})
}

// requireApplication writes a 404 and returns false if the application doesn't exist.
// List endpoints use it so an unknown application is a 404, not an empty list.
func (a *api) requireApplication(w http.ResponseWriter, r *http.Request, id int64) bool {
	exists, err := a.store.ApplicationExists(r.Context(), id)
	if err != nil {
		a.internalError(w, r, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, codeNotFound, msgApplicationNotFound)
		return false
	}
	return true
}
