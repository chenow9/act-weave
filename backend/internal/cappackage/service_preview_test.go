package cappackage

import (
	"testing"

	"actweave/backend/internal/connection"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/tool"
)

func TestPreviewToolRemapsForeignProviderToLocalConnection(t *testing.T) {
	catalog := &workspaceCatalog{
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
	spec := ToolSpec{Slug: "get-orders", Name: "Get orders", Provider: "Foreign API", Connection: "prod"}
	svc := &Service{}

	blocked := svc.previewTool(spec, catalog, nil)
	if blocked.Action != ActionBlocked {
		t.Fatalf("expected blocked without remap, got %#v", blocked)
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
