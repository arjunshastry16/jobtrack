package httpapi

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

func contactPath(id int64) string { return "/api/v1/contacts/" + strconv.FormatInt(id, 10) }

func linkPath(appID, contactID int64) string {
	return appPath(appID) + "/contacts/" + strconv.FormatInt(contactID, 10)
}

func mustCreateContact(t *testing.T, h http.Handler, companyID int64, name string) contactJSON {
	t.Helper()
	var c contactJSON
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/contacts", map[string]any{"company_id": companyID, "name": name}, &c), http.StatusCreated)
	return c
}

func contactNames(t *testing.T, h http.Handler, path string) []string {
	t.Helper()
	var resp struct {
		Contacts []contactJSON `json:"contacts"`
	}
	wantStatus(t, call(t, h, http.MethodGet, path, nil, &resp), http.StatusOK)
	names := []string{}
	for _, c := range resp.Contacts {
		names = append(names, c.Name)
	}
	return names
}

func TestContactsCRUDAndFilter(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	acme := mustCreateCompany(t, h, "Acme")
	globex := mustCreateCompany(t, h, "Globex")

	var ada contactJSON
	rec := call(t, h, http.MethodPost, "/api/v1/contacts", map[string]any{
		"company_id": acme, "name": "Ada Lovelace", "title": "Recruiter",
		"email": "ada@acme.example", "linkedin_url": "https://www.linkedin.com/in/ada",
	}, &ada)
	wantStatus(t, rec, http.StatusCreated)
	if rec.Header().Get("Location") != contactPath(ada.ID) || ada.Email == nil || *ada.Email != "ada@acme.example" {
		t.Fatalf("created %+v (Location %q)", ada, rec.Header().Get("Location"))
	}
	mustCreateContact(t, h, acme, "bob")
	mustCreateContact(t, h, globex, "Carol")

	if got := contactNames(t, h, "/api/v1/contacts"); !slices.Equal(got, []string{"Ada Lovelace", "bob", "Carol"}) {
		t.Errorf("all contacts = %v", got)
	}
	if got := contactNames(t, h, "/api/v1/contacts?company_id="+strconv.FormatInt(acme, 10)); !slices.Equal(got, []string{"Ada Lovelace", "bob"}) {
		t.Errorf("acme contacts = %v", got)
	}

	// Replace: move Ada to Globex and drop her email.
	var updated contactJSON
	wantStatus(t, call(t, h, http.MethodPut, contactPath(ada.ID), map[string]any{"company_id": globex, "name": "Ada Lovelace"}, &updated), http.StatusOK)
	if updated.CompanyID != globex || updated.Email != nil {
		t.Errorf("updated = %+v", updated)
	}

	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/contacts", map[string]any{"company_id": 999, "name": "X"}, nil), http.StatusUnprocessableEntity)
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/contacts?company_id=nope", nil, nil), http.StatusBadRequest)

	wantStatus(t, call(t, h, http.MethodDelete, contactPath(ada.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, contactPath(ada.ID), nil, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodPut, contactPath(ada.ID), map[string]any{"company_id": acme, "name": "Ghost"}, nil), http.StatusNotFound)
}

func TestLinkingContactsToApplications(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})
	recruiter := mustCreateContact(t, h, co, "Recruiter Rae")
	manager := mustCreateContact(t, h, co, "Manager Max")
	appContacts := appPath(app.ID) + "/contacts"

	if got := contactNames(t, h, appContacts); len(got) != 0 {
		t.Fatalf("new application has contacts %v", got)
	}

	// Linking is idempotent.
	for range 2 {
		wantStatus(t, call(t, h, http.MethodPut, linkPath(app.ID, recruiter.ID), nil, nil), http.StatusNoContent)
	}
	wantStatus(t, call(t, h, http.MethodPut, linkPath(app.ID, manager.ID), nil, nil), http.StatusNoContent)
	if got := contactNames(t, h, appContacts); !slices.Equal(got, []string{"Manager Max", "Recruiter Rae"}) {
		t.Errorf("linked contacts = %v", got)
	}

	// Missing application or contact -> 404 with the right message.
	var errResp errorResponse
	wantStatus(t, call(t, h, http.MethodPut, linkPath(app.ID, 9999), nil, &errResp), http.StatusNotFound)
	if errResp.Error.Message != msgContactNotFound {
		t.Errorf("missing contact message = %q", errResp.Error.Message)
	}
	wantStatus(t, call(t, h, http.MethodPut, linkPath(9999, recruiter.ID), nil, &errResp), http.StatusNotFound)
	if errResp.Error.Message != msgApplicationNotFound {
		t.Errorf("missing application message = %q", errResp.Error.Message)
	}
	wantStatus(t, call(t, h, http.MethodGet, appPath(9999)+"/contacts", nil, nil), http.StatusNotFound)

	// Unlink, then unlinking again is a 404 (nothing to remove).
	wantStatus(t, call(t, h, http.MethodDelete, linkPath(app.ID, manager.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodDelete, linkPath(app.ID, manager.ID), nil, nil), http.StatusNotFound)

	// Deleting a contact removes its links but not the application.
	wantStatus(t, call(t, h, http.MethodDelete, contactPath(recruiter.ID), nil, nil), http.StatusNoContent)
	if got := contactNames(t, h, appContacts); len(got) != 0 {
		t.Errorf("contacts after deleting the linked contact = %v", got)
	}
	wantStatus(t, call(t, h, http.MethodGet, appPath(app.ID), nil, nil), http.StatusOK)
}

func TestCompanyWithContactsCannotBeDeleted(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	c := mustCreateContact(t, h, co, "Ada")

	wantStatus(t, call(t, h, http.MethodDelete, pathFor(co), nil, nil), http.StatusConflict)
	wantStatus(t, call(t, h, http.MethodDelete, contactPath(c.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodDelete, pathFor(co), nil, nil), http.StatusNoContent)
}

func TestNotes(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})
	notesPath := appPath(app.ID) + "/notes"

	var first, second noteJSON
	wantStatus(t, call(t, h, http.MethodPost, notesPath, map[string]any{"body": "  Read their eng blog  "}, &first), http.StatusCreated)
	wantStatus(t, call(t, h, http.MethodPost, notesPath, map[string]any{"body": "Ask about on-call"}, &second), http.StatusCreated)
	if first.Body != "Read their eng blog" || first.ApplicationID != app.ID {
		t.Errorf("first note = %+v", first)
	}

	listBodies := func() []string {
		var resp struct {
			Notes []noteJSON `json:"notes"`
		}
		wantStatus(t, call(t, h, http.MethodGet, notesPath, nil, &resp), http.StatusOK)
		out := []string{}
		for _, n := range resp.Notes {
			out = append(out, n.Body)
		}
		return out
	}
	if got := listBodies(); !slices.Equal(got, []string{"Read their eng blog", "Ask about on-call"}) {
		t.Errorf("notes = %v (want oldest first)", got)
	}

	notePath := "/api/v1/notes/" + strconv.FormatInt(first.ID, 10)
	var edited noteJSON
	wantStatus(t, call(t, h, http.MethodPut, notePath, map[string]any{"body": "Read their eng blog (done)"}, &edited), http.StatusOK)
	if edited.Body != "Read their eng blog (done)" || !edited.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("edited = %+v", edited)
	}
	wantStatus(t, call(t, h, http.MethodPut, notePath, map[string]any{"body": "   "}, nil), http.StatusUnprocessableEntity)

	wantStatus(t, call(t, h, http.MethodDelete, notePath, nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodDelete, notePath, nil, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodPut, notePath, map[string]any{"body": "x"}, nil), http.StatusNotFound)

	wantStatus(t, call(t, h, http.MethodPost, appPath(9999)+"/notes", map[string]any{"body": "x"}, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodGet, appPath(9999)+"/notes", nil, nil), http.StatusNotFound)

	// Notes go away with their application.
	wantStatus(t, call(t, h, http.MethodDelete, appPath(app.ID), nil, nil), http.StatusNoContent)
	secondPath := "/api/v1/notes/" + strconv.FormatInt(second.ID, 10)
	wantStatus(t, call(t, h, http.MethodDelete, secondPath, nil, nil), http.StatusNotFound)
}
