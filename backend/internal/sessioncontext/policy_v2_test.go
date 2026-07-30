package sessioncontext_test

import (
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/sessioncontext"
)

func TestParsePolicyV2AgentAAP(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"rolling_summary",
		"maxRecentTurns":20,
		"aap":{"includeCompactionSummary":true}
	}`)
	doc, normalized, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.IncludeCompactionSummary() {
		t.Fatal("expected include true")
	}
	if !json.Valid(normalized) {
		t.Fatal("normalized invalid")
	}
	// Missing aap → false
	raw2 := json.RawMessage(`{"schemaVersion":"session-context-policy.v2","mode":"token_window"}`)
	doc2, _, err := sessioncontext.ParsePolicyScoped(raw2, sessioncontext.PolicyScopeAgent)
	if err != nil || doc2.IncludeCompactionSummary() {
		t.Fatalf("default false: %+v err=%v", doc2, err)
	}
	// aap without bool → normalize false
	raw3 := json.RawMessage(`{"schemaVersion":"session-context-policy.v2","aap":{}}`)
	doc3, _, err := sessioncontext.ParsePolicyScoped(raw3, sessioncontext.PolicyScopeAgent)
	if err != nil || doc3.IncludeCompactionSummary() {
		t.Fatalf("empty aap false: %+v err=%v", doc3, err)
	}
}

func TestParsePolicyWorkspaceRejectsAAP(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"includeCompactionSummary":true}
	}`)
	_, _, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeWorkspace)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("workspace aap err=%v", err)
	}
	// NormalizeWorkspace entry
	if _, err := sessioncontext.NormalizeWorkspacePolicyRaw(raw); err == nil {
		t.Fatal("expected workspace normalize reject")
	}
}

func TestParsePolicyV1RejectsAAP(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v1",
		"mode":"token_window",
		"aap":{"includeCompactionSummary":true}
	}`)
	_, _, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("v1 aap err=%v", err)
	}
}

func TestParsePolicyV2UnknownAAPField(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"aap":{"includeCompactionSummary":false,"override":true}
	}`)
	_, _, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("unknown aap field err=%v", err)
	}
}

func TestParsePolicyV1StillWorks(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v1",
		"mode":"token_window",
		"maxInputTokens":0,
		"outputReserveTokens":4096,
		"safetyMarginTokens":2048,
		"maxRecentTurns":0
	}`)
	doc, _, err := sessioncontext.ParsePolicy(raw)
	if err != nil || doc.SchemaVersion != sessioncontext.PolicySchemaV1 {
		t.Fatalf("v1: %+v err=%v", doc, err)
	}
}
