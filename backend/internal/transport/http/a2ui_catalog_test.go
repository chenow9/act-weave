package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
)

func a2uiCatalogRouter(t *testing.T) http.Handler {
	t.Helper()
	catalogRoutes, err := NewA2UICatalogRoutes()
	if err != nil {
		t.Fatal(err)
	}
	// An authenticator that would happily mint a principal, to show these routes
	// never consult it.
	router, err := NewRouter(Config{
		Authenticator: contractAuthenticator{},
		Registrars:    []V1RouteRegistrar{catalogRoutes},
	})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func a2uiSchemaPath(t *testing.T, documentID string) string {
	t.Helper()
	parsed, err := url.Parse(documentID)
	if err != nil {
		t.Fatal(err)
	}
	return "/api/v1" + a2uiCatalogRoot + parsed.Path
}

// The point of serving these documents is that a third party can build a
// renderer or validator from the same bytes we validate against.
func TestA2UICatalogServesTheEmbeddedDocuments(t *testing.T) {
	t.Parallel()

	router := a2uiCatalogRouter(t)
	catalog, err := a2ui.CatalogDocument()
	if err != nil {
		t.Fatal(err)
	}
	surfaceSchema, err := a2ui.SurfaceSchemaDocument()
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []struct {
		id   string
		want []byte
	}{
		{a2ui.CatalogID, catalog},
		{a2ui.SurfaceSchemaID, surfaceSchema},
	} {
		t.Run(document.id, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, a2uiSchemaPath(t, document.id), nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Body.String() != string(document.want) {
				t.Fatalf("served bytes differ from the embedded document")
			}
			if got := response.Header().Get("Content-Type"); got != "application/schema+json" {
				t.Fatalf("content-type=%q", got)
			}
			// The served document must declare the identifier it was served for,
			// or a client resolving $id would leave our host.
			var declared struct {
				ID string `json:"$id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &declared); err != nil {
				t.Fatal(err)
			}
			if declared.ID != document.id {
				t.Fatalf("$id=%q want %q", declared.ID, document.id)
			}
		})
	}
}

// The surface schema reaches the catalog through a relative $ref, so the two
// routes must be siblings. If they ever stop being siblings, a third-party
// validator breaks while ours keeps working off the embedded files.
func TestA2UISchemasAreServedAsSiblings(t *testing.T) {
	t.Parallel()

	catalogPath := a2uiSchemaPath(t, a2ui.CatalogID)
	surfacePath := a2uiSchemaPath(t, a2ui.SurfaceSchemaID)
	if path.Dir(catalogPath) != path.Dir(surfacePath) {
		t.Fatalf("not siblings: %s vs %s", catalogPath, surfacePath)
	}

	surfaceSchema, err := a2ui.SurfaceSchemaDocument()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Components struct {
				Items struct {
					Ref string `json:"$ref"`
				} `json:"items"`
			} `json:"components"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(surfaceSchema, &schema); err != nil {
		t.Fatal(err)
	}
	reference := schema.Properties.Components.Items.Ref
	if reference == "" {
		t.Fatal("surface schema stopped referencing the catalog through components.items")
	}
	// Resolving the relative reference against the served surface path must land
	// on the served catalog path.
	document, _, _ := strings.Cut(reference, "#")
	resolved := path.Join(path.Dir(surfacePath), document)
	if resolved != catalogPath {
		t.Fatalf("relative $ref %q resolves to %s, catalog is served at %s",
			reference, resolved, catalogPath)
	}
}

// Public means usable with no token: a client decides whether it can render our
// surfaces before it has credentials, and often before it has an account.
func TestA2UICatalogNeedsNoCredentials(t *testing.T) {
	t.Parallel()

	router := a2uiCatalogRouter(t)
	request := httptest.NewRequest(http.MethodGet, a2uiSchemaPath(t, a2ui.CatalogID), nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow-origin=%q: a browser renderer cannot read the catalog", got)
	}
	// A wildcard origin must never be paired with credentials.
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow-credentials=%q alongside a wildcard origin", got)
	}
}

func TestA2UICatalogRevalidatesWithETag(t *testing.T) {
	t.Parallel()

	router := a2uiCatalogRouter(t)
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, a2uiSchemaPath(t, a2ui.CatalogID), nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag: every fetch would transfer the whole schema")
	}
	if got := first.Header().Get("Cache-Control"); got == "" {
		t.Fatal("no Cache-Control")
	}

	request := httptest.NewRequest(http.MethodGet, a2uiSchemaPath(t, a2ui.CatalogID), nil)
	request.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	router.ServeHTTP(second, request)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status=%d want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 carried a body: %s", second.Body.String())
	}

	stale := httptest.NewRequest(http.MethodGet, a2uiSchemaPath(t, a2ui.CatalogID), nil)
	stale.Header.Set("If-None-Match", `"stale"`)
	third := httptest.NewRecorder()
	router.ServeHTTP(third, stale)
	if third.Code != http.StatusOK || third.Body.Len() == 0 {
		t.Fatalf("stale validator status=%d len=%d want a fresh copy", third.Code, third.Body.Len())
	}
}

// Only the registered catalog is served. An unknown version must not fall back
// to the one we happen to have.
func TestA2UICatalogServesNothingElse(t *testing.T) {
	t.Parallel()

	router := a2uiCatalogRouter(t)
	for _, path := range []string{
		"/api/v1" + a2uiCatalogRoot + "/standard/v2/catalog.json",
		"/api/v1" + a2uiCatalogRoot + "/standard/v1",
		"/api/v1" + a2uiCatalogRoot + "/standard/v1/catalog.json/",
		"/api/v1" + a2uiCatalogRoot,
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code == http.StatusOK {
				t.Fatalf("served %s: %s", path, response.Body.String())
			}
		})
	}
}

// The profile advertises catalogIds; those identifiers must be the ones we serve,
// otherwise a client follows the advertisement to a 404.
func TestA2UICatalogCoversEveryAdvertisedCatalogID(t *testing.T) {
	t.Parallel()

	router := a2uiCatalogRouter(t)
	advertised := a2ui.RegisteredCatalogIDs()
	if len(advertised) == 0 {
		t.Fatal("no catalog advertised")
	}
	for _, catalogID := range advertised {
		t.Run(catalogID, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, a2uiSchemaPath(t, catalogID), nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d for advertised catalog", response.Code)
			}
		})
	}
}

func TestA2UISchemaRoutePathRejectsAnIdentifierWithoutAPath(t *testing.T) {
	t.Parallel()

	for _, documentID := range []string{"https://catalog.actweave.dev", "https://catalog.actweave.dev/", ""} {
		if _, err := a2uiSchemaRoutePath(documentID); err == nil {
			t.Fatalf("accepted %q", documentID)
		}
	}
}
