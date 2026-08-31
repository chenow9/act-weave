package cappackage

import (
	"testing"

	"actweave/backend/internal/connection"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/tool"
)

func TestPreviewToolAutoBindsUniqueWorkspaceConnection(t *testing.T) {
	catalog := uniqueLocalCatalog()
	spec := ToolSpec{Slug: "get-orders", Name: "Get orders", Provider: "Foreign API", Connection: "prod"}
	item := (&Service{}).previewTool(spec, catalog, nil)
	if item.Action != ActionCreate {
		t.Fatalf("expected auto-bind create, got %#v", item)
	}
	if item.ResolvedProviderID != "prov-local" || item.ResolvedConnectionID != "conn-local" {
		t.Fatalf("resolved ids=%s/%s", item.ResolvedProviderID, item.ResolvedConnectionID)
	}
}

func TestPreviewToolRemapsForeignProviderWhenWorkspaceHasMultipleConnections(t *testing.T) {
	catalog := uniqueLocalCatalog()
	catalog.providersByName["other api"] = provider.Provider{ID: "prov-other", Name: "Other API"}
	catalog.providersByID["prov-other"] = provider.Provider{ID: "prov-other", Name: "Other API"}
	catalog.connections = append(catalog.connections, connection.Connection{
		ID: "conn-other", ProviderID: "prov-other", Alias: "other", Name: "Other",
	})
	spec := ToolSpec{Slug: "get-orders", Name: "Get orders", Provider: "Foreign API", Connection: "prod"}
	svc := &Service{}

	blocked := svc.previewTool(spec, catalog, nil)
	if blocked.Action != ActionBlocked || blocked.Reason != reasonMultipleConnections {
		t.Fatalf("expected blocked until remap, got %#v", blocked)
	}

	remapped := svc.previewTool(spec, catalog, []ConnectionBinding{{
		Provider: "Foreign API", Connection: "prod", TargetConnectionID: "conn-local",
	}})
	if remapped.Action != ActionCreate {
		t.Fatalf("expected create after remap, got %#v", remapped)
	}
	if remapped.ResolvedProviderID != "prov-local" || remapped.ResolvedConnectionID != "conn-local" {
		t.Fatalf("resolved ids=%s/%s", remapped.ResolvedProviderID, remapped.ResolvedConnectionID)
	}
}

func uniqueLocalCatalog() *workspaceCatalog {
	return &workspaceCatalog{
		providersByName: map[string]provider.Provider{
			"local api": {ID: "prov-local", Name: "Local API"},
		},
		providersByID: map[string]provider.Provider{
			"prov-local": {ID: "prov-local", Name: "Local API"},
		},
		connections: []connection.Connection{
			{ID: "conn-local", ProviderID: "prov-local", Alias: "staging", Name: "Staging"},
		},
		toolBySlug: map[string]tool.Tool{},
	}
}
