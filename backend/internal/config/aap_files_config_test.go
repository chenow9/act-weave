package config

import (
	"strings"
	"testing"
)

func TestAgentAccessFilesConfig(t *testing.T) {
	t.Run("DefaultDisabled", func(t *testing.T) {
		var files AgentAccessFilesConfig
		if files.Enabled || files.RuntimeMultimodal || files.RuntimeOutboundAttachments ||
			files.RuntimeInboundRead ||
			files.AllowsWorkspace("a0000000-0000-4000-8000-000000000001") ||
			files.AllowsClient("b0000000-0000-4000-8000-000000000001") {
			t.Fatalf("zero files config must be closed: %+v", files)
		}
	})

	t.Run("Allowlist", func(t *testing.T) {
		ws := "a0000000-0000-4000-8000-0000000000f1"
		cl := "b0000000-0000-4000-8000-0000000000f2"
		files := AgentAccessFilesConfig{
			Enabled: true, AllowAllWorkspaces: false, WorkspaceIDs: []string{ws},
			AllowAllClients: false, ClientIDs: []string{cl},
		}
		if !files.AllowsWorkspace(ws) || !files.AllowsClient(cl) {
			t.Fatal("allowlist must admit listed ids")
		}
		if files.AllowsWorkspace("c0000000-0000-4000-8000-0000000000cc") ||
			files.AllowsClient("c0000000-0000-4000-8000-0000000000cc") {
			t.Fatal("allowlist must deny unlisted ids")
		}
		open := AgentAccessFilesConfig{Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true}
		if !open.AllowsWorkspace(ws) || !open.AllowsClient(cl) {
			t.Fatal("allow-all must admit")
		}
	})

	t.Run("ValidateMutualExclusion", func(t *testing.T) {
		if err := validateAgentAccessFilesConfig(AgentAccessFilesConfig{
			AllowAllWorkspaces: true, WorkspaceIDs: []string{"a0000000-0000-4000-8000-000000000001"},
		}); err == nil {
			t.Fatal("allowAll + workspaceIds must fail")
		}
		if err := validateAgentAccessFilesConfig(AgentAccessFilesConfig{
			AllowAllClients: true, ClientIDs: []string{"b0000000-0000-4000-8000-000000000001"},
		}); err == nil {
			t.Fatal("allowAll + clientIds must fail")
		}
	})

	t.Run("EnvironmentOverrides", func(t *testing.T) {
		path := writeConfig(t, validConfigYAML)
		values := map[string]string{
			"ACTWEAVE_AAP_FILES_ENABLED":                      "true",
			"ACTWEAVE_AAP_FILES_ALLOW_ALL_WORKSPACES":         "false",
			"ACTWEAVE_AAP_FILES_ALLOW_ALL_CLIENTS":            "false",
			"ACTWEAVE_AAP_FILES_WORKSPACE_IDS":                "a0000000-0000-4000-8000-0000000000a1",
			"ACTWEAVE_AAP_FILES_CLIENT_IDS":                   "b0000000-0000-4000-8000-0000000000b1",
			"ACTWEAVE_AAP_FILES_MAX_BYTES":                    "1048576",
			"ACTWEAVE_AAP_FILES_RUNTIME_MULTIMODAL":           "true",
			"ACTWEAVE_AAP_FILES_RUNTIME_OUTBOUND_ATTACHMENTS": "true",
			"ACTWEAVE_AAP_FILES_RUNTIME_INBOUND_READ":         "true",
		}
		loaded, err := Load(path, lookup(values))
		if err != nil {
			t.Fatal(err)
		}
		files := loaded.AgentAccess.Files
		if !files.Enabled || files.AllowAllWorkspaces || files.AllowAllClients {
			t.Fatalf("files env override failed: %+v", files)
		}
		if files.MaxBytes != 1048576 || !files.RuntimeMultimodal || !files.RuntimeOutboundAttachments ||
			!files.RuntimeInboundRead {
			t.Fatalf("files numeric/bool env failed: %+v", files)
		}
		if !files.AllowsWorkspace("a0000000-0000-4000-8000-0000000000a1") ||
			!files.AllowsClient("b0000000-0000-4000-8000-0000000000b1") {
			t.Fatalf("files allowlist env failed: %+v", files)
		}
	})

	t.Run("YAMLDefaultDisabledInSampleShape", func(t *testing.T) {
		yaml := strings.Replace(validConfigYAML, "agentAccess:\n  tokenEndpoint:",
			"agentAccess:\n  files:\n    enabled: false\n    allowAllWorkspaces: false\n    workspaceIds: []\n    allowAllClients: false\n    clientIds: []\n  tokenEndpoint:", 1)
		path := writeConfig(t, yaml)
		loaded, err := Load(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.AgentAccess.Files.Enabled || loaded.AgentAccess.Files.RuntimeOutboundAttachments {
			t.Fatal("sample files.enabled / runtimeOutboundAttachments must default false")
		}
	})
}
