package httptransport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"actweave/backend/internal/a2ui"

	"github.com/gin-gonic/gin"
)

// a2uiCatalogRoot prefixes the served A2UI schema documents. What follows is the
// path of each document's own identifier, so a document is always reachable at
// the path its $id declares and the relative $ref between them resolves for a
// third-party validator exactly as it does for ours.
const a2uiCatalogRoot = "/a2ui/catalogs"

// A2UICatalogRoutes serves the A2UI component catalog and surface schema.
//
// Public and unauthenticated: these are the contract a client needs *before* it
// has any token, in order to decide whether it can render our surfaces at all.
// They carry no workspace data — the same bytes ship in every build.
type A2UICatalogRoutes struct {
	documents []a2uiSchemaDocument
}

// a2uiSchemaDocument is one served schema, with its route and validators
// precomputed: the bytes are embedded, so they cannot change within a build.
type a2uiSchemaDocument struct {
	path    string
	payload []byte
	etag    string
}

// NewA2UICatalogRoutes builds the registrar, failing fast when an embedded
// document is unreadable or its identifier is not a path we can serve.
func NewA2UICatalogRoutes() (*A2UICatalogRoutes, error) {
	documents := make([]a2uiSchemaDocument, 0, 2)
	for _, source := range []struct {
		id   string
		read func() ([]byte, error)
	}{
		{a2ui.CatalogID, a2ui.CatalogDocument},
		{a2ui.SurfaceSchemaID, a2ui.SurfaceSchemaDocument},
	} {
		payload, err := source.read()
		if err != nil {
			return nil, fmt.Errorf("read a2ui schema %s: %w", source.id, err)
		}
		path, err := a2uiSchemaRoutePath(source.id)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(payload)
		documents = append(documents, a2uiSchemaDocument{
			path: path, payload: payload, etag: `"` + hex.EncodeToString(digest[:]) + `"`,
		})
	}
	return &A2UICatalogRoutes{documents: documents}, nil
}

func (routes *A2UICatalogRoutes) RegisterV1(groups V1Routes) {
	for _, document := range routes.documents {
		groups.Public.GET(document.path, document.handle)
	}
}

// a2uiSchemaRoutePath maps a document identifier to the route serving it.
func a2uiSchemaRoutePath(documentID string) (string, error) {
	parsed, err := url.Parse(documentID)
	if err != nil {
		return "", fmt.Errorf("parse a2ui schema id %q: %w", documentID, err)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return "", errors.New("a2ui schema id carries no path: " + documentID)
	}
	return a2uiCatalogRoot + parsed.Path, nil
}

func (document a2uiSchemaDocument) handle(c *gin.Context) {
	// Versioned by path and additive within a version, so a cached copy stays
	// usable; the ETag makes the revalidation that follows nearly free.
	c.Header("Cache-Control", "public, max-age=3600, must-revalidate")
	c.Header("ETag", document.etag)
	// A browser renderer fetches this cross-origin. The response is static,
	// public and credential-free, and a wildcard origin forbids credentials, so
	// this exposes nothing that the document itself does not already state.
	c.Header("Access-Control-Allow-Origin", "*")
	if c.GetHeader("If-None-Match") == document.etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/schema+json", document.payload)
}
