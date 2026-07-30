package chatruntimebridge

import (
	"encoding/json"
	"testing"

	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/execution"
)

func TestParseModelSnapshot(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"c08f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		"provider":"openai",
		"apiBase":"https://api.example",
		"modelName":"gpt-test",
		"options":{"temperature":0},
		"lockVersion":3
	}`)
	cfg, id, err := parseModelSnapshot(raw)
	if err != nil || id != "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1" {
		t.Fatalf("parse: id=%s err=%v", id, err)
	}
	if cfg.Provider != "openai" || cfg.ModelName != "gpt-test" {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestToolSchemasFromCapabilitySnapshot(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[
			{
				"capabilityId":"c1","releaseId":"r1","kind":"TOOL",
				"callableName":"lookup_order",
				"callableDescription":"find order",
				"inputSchema":{"type":"object","properties":{"id":{"type":"string"}}},
				"outputSchema":{},"riskLevel":"LOW","sideEffectLevel":"NONE",
				"requiresConfirmation":false
			},
			{
				"capabilityId":"c2","releaseId":"r2","kind":"OTHER",
				"callableName":"skip_me","callableDescription":"x",
				"inputSchema":{},"outputSchema":{}
			}
		]
	}`)
	tools, err := toolSchemasFromCapabilitySnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "lookup_order" {
		t.Fatalf("tools=%+v", tools)
	}
	if len(tools[0].Parameters) == 0 {
		t.Fatal("expected parameters for estimator overhead")
	}
}

func TestToolSchemasContributeToAssemblerOverhead(t *testing.T) {
	tools := []contextwindow.ToolSchema{{
		Name: "lookup_order", Description: "find order by id",
		Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
	}}
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		PolicyMode:               "token_window",
		ModelContextWindowTokens: 128000,
		OutputReserveTokens:      4096,
		SafetyMarginTokens:       2048,
		MaxInputTokens:           100000,
		TokenizerProfile:         "o200k_base",
		SystemPrompt:             "you are helpful",
		Tools:                    tools,
		CurrentUser: contextwindow.HistoryMessage{
			ID: "u1", Role: "USER", Content: "hello", ContentHash: "a",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ToolsOverheadTokens <= 0 {
		t.Fatalf("tools overhead must be > 0, got %d", plan.ToolsOverheadTokens)
	}
}

func TestAgentSnapshotPromptRevisionID(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":"agent-binding.v1","promptRevisionId":"rev-1"}`)
	if agentSnapshotPromptRevisionID(raw) != "rev-1" {
		t.Fatal("revision id")
	}
	_ = execution.RunSnapshotSchemaV2
}
