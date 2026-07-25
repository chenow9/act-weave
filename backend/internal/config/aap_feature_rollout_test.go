package config

import (
	"strings"
	"testing"
)

// TestAAPFeatureRollout is the M10-T8 config gate for AAP gray release flags.
func TestAAPFeatureRollout(t *testing.T) {
	t.Run("DefaultClosedPublicSurface", testAAPFeatureDefaultClosed)
	t.Run("AllowlistsAndAllowAll", testAAPFeatureAllowlists)
	t.Run("ValidationRejectsContradictions", testAAPFeatureValidation)
	t.Run("EnvironmentOverrides", testAAPFeatureEnvironmentOverrides)
	t.Run("LoadFromYAML", testAAPFeatureLoadYAML)
}

func testAAPFeatureDefaultClosed(t *testing.T) {
	var feature AAPFeatureRollout
	if feature.PublicSurfaceOpen() {
		t.Fatal("zero-value feature must keep AAP public surface closed")
	}
	if feature.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") ||
		feature.AllowsClient("b0000000-0000-4000-8000-000000000001") {
		t.Fatal("closed surface must deny workspace/client checks")
	}
}

func testAAPFeatureAllowlists(t *testing.T) {
	ws := "a0000000-0000-4000-8000-0000000000aa"
	cl := "b0000000-0000-4000-8000-0000000000bb"
	feature := AAPFeatureRollout{
		Enabled: true, WorkspaceIDs: []string{ws}, ClientIDs: []string{cl},
	}
	if !feature.PublicSurfaceOpen() {
		t.Fatal("enabled surface must be open")
	}
	if !feature.AllowsWorkspace(ws) || !feature.AllowsClient(cl) {
		t.Fatal("allowlisted ids must pass")
	}
	if feature.AllowsWorkspace("c0000000-0000-4000-8000-0000000000cc") ||
		feature.AllowsClient("d0000000-0000-4000-8000-0000000000dd") {
		t.Fatal("non-allowlisted ids must fail")
	}
	empty := AAPFeatureRollout{Enabled: true}
	if empty.AllowsWorkspace(ws) || empty.AllowsClient(cl) {
		t.Fatal("empty allowlist must deny")
	}
	open := AAPFeatureRollout{Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true}
	if !open.AllowsWorkspace(ws) || !open.AllowsClient(cl) {
		t.Fatal("allow-all must pass")
	}
}

func testAAPFeatureValidation(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded.AgentAccess.Feature = AAPFeatureRollout{
		Enabled: true, AllowAllWorkspaces: true,
		WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000001"},
	}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected validation error for allowAll + workspace list")
	}
	loaded.AgentAccess.Feature = AAPFeatureRollout{
		Enabled: true, AllowAllClients: true,
		ClientIDs: []string{"b0000000-0000-4000-8000-000000000001"},
	}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected validation error for allowAll + client list")
	}
	loaded.AgentAccess.Feature = AAPFeatureRollout{
		Enabled: true, WorkspaceIDs: []string{"ws-1", "ws-1"},
	}
	if err := loaded.ValidateServer(); err == nil {
		t.Fatal("expected duplicate workspace validation error")
	}
	// Valid gray config.
	loaded.AgentAccess.Feature = AAPFeatureRollout{
		Enabled: true,
		WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000001"},
		ClientIDs:    []string{"b0000000-0000-4000-8000-000000000002"},
	}
	if err := loaded.ValidateServer(); err != nil {
		t.Fatalf("valid gray config rejected: %v", err)
	}
}

func testAAPFeatureEnvironmentOverrides(t *testing.T) {
	path := writeConfig(t, validConfigYAML)
	values := map[string]string{
		"ACTWEAVE_AAP_FEATURE_ENABLED":              "true",
		"ACTWEAVE_AAP_FEATURE_ALLOW_ALL_WORKSPACES": "false",
		"ACTWEAVE_AAP_FEATURE_ALLOW_ALL_CLIENTS":    "false",
		"ACTWEAVE_AAP_FEATURE_WORKSPACE_IDS":        "a0000000-0000-4000-8000-0000000000a1, a0000000-0000-4000-8000-0000000000a2",
		"ACTWEAVE_AAP_FEATURE_CLIENT_IDS":           "b0000000-0000-4000-8000-0000000000b1",
	}
	loaded, err := Load(path, lookup(values))
	if err != nil {
		t.Fatal(err)
	}
	feature := loaded.AgentAccess.Feature
	if !feature.Enabled || feature.AllowAllWorkspaces || feature.AllowAllClients {
		t.Fatalf("feature env override failed: %+v", feature)
	}
	if len(feature.WorkspaceIDs) != 2 || feature.ClientIDs[0] != "b0000000-0000-4000-8000-0000000000b1" {
		t.Fatalf("allowlist env override failed: %+v", feature)
	}
}

func testAAPFeatureLoadYAML(t *testing.T) {
	yaml := strings.Replace(validConfigYAML, "agentAccess:\n  tokenEndpoint:",
		"agentAccess:\n  feature:\n    enabled: true\n    allowAllWorkspaces: false\n    workspaceIds: [\"a0000000-0000-4000-8000-0000000000f1\"]\n    allowAllClients: false\n    clientIds: [\"b0000000-0000-4000-8000-0000000000f2\"]\n  tokenEndpoint:", 1)
	path := writeConfig(t, yaml)
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.AgentAccess.Feature.Enabled ||
		!loaded.AgentAccess.Feature.AllowsWorkspace("a0000000-0000-4000-8000-0000000000f1") ||
		!loaded.AgentAccess.Feature.AllowsClient("b0000000-0000-4000-8000-0000000000f2") {
		t.Fatalf("yaml feature load failed: %+v", loaded.AgentAccess.Feature)
	}
}
