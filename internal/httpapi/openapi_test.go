package httpapi

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/arjunshastry16/jobtrack/docs"
)

// The spec is parsed once and shared by all tests in the package.
var (
	specOnce   sync.Once
	specDoc    *openapi3.T
	specRouter routers.Router
	specErr    error
)

func loadSpec(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	specOnce.Do(func() {
		specDoc, specErr = openapi3.NewLoader().LoadFromData(docs.OpenAPI)
		if specErr != nil {
			return
		}
		if specErr = specDoc.Validate(context.Background()); specErr != nil {
			return
		}
		specRouter, specErr = legacy.NewRouter(specDoc)
	})
	if specErr != nil {
		t.Fatalf("docs/openapi.yaml: %v", specErr)
	}
	return specDoc, specRouter
}

// TestOpenAPISpecIsValid fails CI if the spec isn't valid OpenAPI 3.
func TestOpenAPISpecIsValid(t *testing.T) {
	loadSpec(t)
}

// TestOpenAPIMatchesRoutes fails if an endpoint is added to the server without being
// documented, or documented without existing.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	doc, _ := loadSpec(t)

	inSpec := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			inSpec[method+" "+path] = true
		}
	}
	inServer := map[string]bool{}
	for _, rt := range routes(&api{}, Deps{}) {
		inServer[rt.method+" "+rt.pattern] = true
	}

	for _, r := range slices.Sorted(maps.Keys(inServer)) {
		if !inSpec[r] {
			t.Errorf("route %q is served but missing from docs/openapi.yaml", r)
		}
	}
	for _, r := range slices.Sorted(maps.Keys(inSpec)) {
		if !inServer[r] {
			t.Errorf("route %q is in docs/openapi.yaml but not served", r)
		}
	}
}

func TestServesOpenAPISpec(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(fakeDB{}).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/openapi.yaml", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/yaml" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), docs.OpenAPI) {
		t.Error("served spec differs from docs/openapi.yaml")
	}
}

// contractChecked wraps h so that every response it produces is checked against the
// OpenAPI spec: the status code must be documented for that operation and the body must
// match its schema (response schemas forbid undocumented fields). Wrapping the router
// used by the integration tests turns all of them into contract tests for free.
func contractChecked(t *testing.T, h http.Handler) http.Handler {
	t.Helper()
	_, router := loadSpec(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		route, pathParams, err := router.FindRoute(r)
		if err != nil {
			t.Errorf("%s %s: no matching operation in docs/openapi.yaml: %v", r.Method, r.URL.Path, err)
		} else {
			in := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{Request: r, PathParams: pathParams, Route: route},
				Status:                 rec.Code,
				Header:                 rec.Header(),
				Options:                &openapi3filter.Options{IncludeResponseStatus: true, MultiError: true},
			}
			in.SetBodyBytes(rec.Body.Bytes())
			if err := openapi3filter.ValidateResponse(r.Context(), in); err != nil {
				t.Errorf("%s %s -> %d does not match docs/openapi.yaml:\n%v\nbody: %s",
					r.Method, r.URL.Path, rec.Code, err, rec.Body.String())
			}
		}

		// Pass the recorded response through unchanged.
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = io.Copy(w, rec.Body)
	})
}
