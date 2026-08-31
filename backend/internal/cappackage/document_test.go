package cappackage

import (
	"strings"
	"testing"
)

func TestParseRoundTripAndRejectsSecrets(t *testing.T) {
	source := strings.TrimSpace(`
apiVersion: actweave.package/v1
kind: Package
metadata:
  name: get-orders
spec:
  tools:
    - slug: get-orders
      name: Get orders
      provider: API
      connection: api-test
      riskLevel: LOW
      sideEffectLevel: READ
      requiresConfirmation: false
      action:
        schemaVersion: http.v1
        config:
          method: GET
          path: /orders
      inputSchema:
        type: object
      outputSchema:
        type: object
      errorMappings: {}
      runtimePolicy:
        timeoutMs: 1000
        maxResponseBytes: 1048576
        retryCount: 0
`) + "\n"
	document, err := Parse([]byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if document.Spec.Tools[0].Slug != "get-orders" || document.Spec.Tools[0].Action.Config["method"] != "GET" {
		t.Fatalf("parsed=%+v", document.Spec.Tools[0])
	}
	encoded, err := Marshal(document)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	again, err := Parse(encoded)
	if err != nil || again.Spec.Tools[0].Slug != "get-orders" {
		t.Fatalf("round trip: %+v err=%v", again, err)
	}

	secretDoc := strings.ReplaceAll(source, "retryCount: 0", "authorization: Bearer secret\n        retryCount: 0")
	if _, err := Parse([]byte(secretDoc)); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected secret rejection, got %v", err)
	}
}

func TestParseRejectsUnknownAPIVersionAndBadSlug(t *testing.T) {
	if _, err := Parse([]byte("apiVersion: v0\nkind: Package\nspec: {}\n")); err == nil {
		t.Fatal("expected invalid apiVersion")
	}
	source := `
apiVersion: actweave.package/v1
kind: Package
spec:
  tools:
    - slug: Not A Slug
      name: X
      provider: API
      riskLevel: LOW
      sideEffectLevel: READ
      action:
        schemaVersion: http.v1
        config: {method: GET, path: /x}
      inputSchema: {type: object}
      outputSchema: {type: object}
      runtimePolicy: {timeoutMs: 1}
`
	if _, err := Parse([]byte(source)); err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("expected slug rejection, got %v", err)
	}
}

func TestRewriteGraphSlugRoundTrip(t *testing.T) {
	graph := Graph{
		SchemaVersion: DefaultGraphSchema,
		Nodes: []GraphNode{
			{ID: "start", Type: "Start"},
			{ID: "call", Type: "Tool", Data: Object{"toolId": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "inputMapping": Object{}}},
		},
		Edges: []GraphEdge{{ID: "e1", SourceNodeID: "start", TargetNodeID: "call"}},
	}
	exported := rewriteGraphForExport(graph, map[string]string{
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa": "get-orders",
	}, nil)
	if exported.Nodes[1].Data["toolSlug"] != "get-orders" {
		t.Fatalf("export data=%v", exported.Nodes[1].Data)
	}
	if _, exists := exported.Nodes[1].Data["toolId"]; exists {
		t.Fatalf("toolId should be removed: %v", exported.Nodes[1].Data)
	}
	imported, missing := rewriteGraphForImport(exported, map[string]string{
		"get-orders": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
	}, nil)
	if len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}
	if imported.Nodes[1].Data["toolId"] != "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" {
		t.Fatalf("import data=%v", imported.Nodes[1].Data)
	}
}
