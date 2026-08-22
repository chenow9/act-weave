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
	// enableA2UI / enableOutboundAttachments missing when aap present → normalize false
	if doc.EnableA2UI() {
		t.Fatal("expected enableA2UI default false when omitted")
	}
	if doc.EnableOutboundAttachments() {
		t.Fatal("expected enableOutboundAttachments default false when omitted")
	}
	if doc.EnableInboundRead() {
		t.Fatal("expected enableInboundRead default false when omitted")
	}
	if doc.AAP == nil || doc.AAP.EnableA2UI == nil || *doc.AAP.EnableA2UI {
		t.Fatalf("expected normalized enableA2UI=false pointer: %+v", doc.AAP)
	}
	if doc.AAP.EnableOutboundAttachments == nil || *doc.AAP.EnableOutboundAttachments {
		t.Fatalf("expected normalized enableOutboundAttachments=false pointer: %+v", doc.AAP)
	}
	if !json.Valid(normalized) {
		t.Fatal("normalized invalid")
	}
	// Missing aap → false
	raw2 := json.RawMessage(`{"schemaVersion":"session-context-policy.v2","mode":"token_window"}`)
	doc2, _, err := sessioncontext.ParsePolicyScoped(raw2, sessioncontext.PolicyScopeAgent)
	if err != nil || doc2.IncludeCompactionSummary() || doc2.EnableA2UI() ||
		doc2.EnableOutboundAttachments() || doc2.EnableInboundRead() {
		t.Fatalf("default false: %+v err=%v", doc2, err)
	}
	// aap without bools → normalize all false
	raw3 := json.RawMessage(`{"schemaVersion":"session-context-policy.v2","aap":{}}`)
	doc3, _, err := sessioncontext.ParsePolicyScoped(raw3, sessioncontext.PolicyScopeAgent)
	if err != nil || doc3.IncludeCompactionSummary() || doc3.EnableA2UI() ||
		doc3.EnableOutboundAttachments() || doc3.EnableInboundRead() {
		t.Fatalf("empty aap false: %+v err=%v", doc3, err)
	}
	if doc3.AAP == nil || doc3.AAP.IncludeCompactionSummary == nil || doc3.AAP.EnableA2UI == nil ||
		doc3.AAP.EnableOutboundAttachments == nil || doc3.AAP.EnableInboundRead == nil {
		t.Fatalf("expected all flags materialised: %+v", doc3.AAP)
	}
}

func TestParsePolicyV2EnableA2UITrue(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableA2UI":true}
	}`)
	doc, normalized, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.EnableA2UI() {
		t.Fatal("expected enableA2UI true")
	}
	// Sibling flag still normalizes to false
	if doc.IncludeCompactionSummary() {
		t.Fatal("expected includeCompactionSummary false when omitted")
	}
	var top map[string]any
	if err := json.Unmarshal(normalized, &top); err != nil {
		t.Fatal(err)
	}
	aap, ok := top["aap"].(map[string]any)
	if !ok {
		t.Fatalf("aap missing in normalized: %s", normalized)
	}
	if aap["enableA2UI"] != true {
		t.Fatalf("normalized enableA2UI: %v", aap["enableA2UI"])
	}
	if aap["includeCompactionSummary"] != false {
		t.Fatalf("normalized include: %v", aap["includeCompactionSummary"])
	}
	if aap["enableOutboundAttachments"] != false {
		t.Fatalf("normalized enableOutboundAttachments: %v", aap["enableOutboundAttachments"])
	}
	if aap["enableInboundRead"] != false {
		t.Fatalf("normalized enableInboundRead: %v", aap["enableInboundRead"])
	}
}

func TestParsePolicyV2EnableA2UINullNormalizesFalse(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"aap":{"enableA2UI":null,"includeCompactionSummary":null,"enableOutboundAttachments":null,"enableInboundRead":null}
	}`)
	doc, _, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if doc.EnableA2UI() || doc.IncludeCompactionSummary() || doc.EnableOutboundAttachments() ||
		doc.EnableInboundRead() {
		t.Fatalf("null flags should normalize false: %+v", doc.AAP)
	}
}

func TestParsePolicyV2EnableOutboundAttachmentsTrue(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableOutboundAttachments":true}
	}`)
	doc, normalized, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.EnableOutboundAttachments() {
		t.Fatal("expected enableOutboundAttachments true")
	}
	if doc.EnableA2UI() || doc.IncludeCompactionSummary() {
		t.Fatal("sibling flags must stay false when omitted")
	}
	var top map[string]any
	if err := json.Unmarshal(normalized, &top); err != nil {
		t.Fatal(err)
	}
	aap, ok := top["aap"].(map[string]any)
	if !ok {
		t.Fatalf("aap missing in normalized: %s", normalized)
	}
	if aap["enableOutboundAttachments"] != true {
		t.Fatalf("normalized enableOutboundAttachments: %v", aap["enableOutboundAttachments"])
	}
	if aap["enableA2UI"] != false || aap["includeCompactionSummary"] != false {
		t.Fatalf("normalized siblings: %v", aap)
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
	// enableA2UI-only aap is still agent-only
	rawA2UI := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableA2UI":true}
	}`)
	_, _, err = sessioncontext.ParsePolicyScoped(rawA2UI, sessioncontext.PolicyScopeWorkspace)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("workspace enableA2UI aap err=%v", err)
	}
	rawOutbound := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableOutboundAttachments":true}
	}`)
	_, _, err = sessioncontext.ParsePolicyScoped(rawOutbound, sessioncontext.PolicyScopeWorkspace)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("workspace enableOutboundAttachments aap err=%v", err)
	}
	rawInbound := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableInboundRead":true}
	}`)
	_, _, err = sessioncontext.ParsePolicyScoped(rawInbound, sessioncontext.PolicyScopeWorkspace)
	if err == nil || !errors.Is(err, sessioncontext.ErrInvalidPolicy) {
		t.Fatalf("workspace enableInboundRead aap err=%v", err)
	}
}

func TestParsePolicyV2EnableInboundReadTrue(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion":"session-context-policy.v2",
		"mode":"token_window",
		"aap":{"enableInboundRead":true}
	}`)
	doc, normalized, err := sessioncontext.ParsePolicyScoped(raw, sessioncontext.PolicyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.EnableInboundRead() {
		t.Fatal("expected enableInboundRead true")
	}
	if doc.EnableA2UI() || doc.IncludeCompactionSummary() || doc.EnableOutboundAttachments() {
		t.Fatal("sibling flags must stay false when omitted")
	}
	var top map[string]any
	if err := json.Unmarshal(normalized, &top); err != nil {
		t.Fatal(err)
	}
	aap, ok := top["aap"].(map[string]any)
	if !ok {
		t.Fatalf("aap missing in normalized: %s", normalized)
	}
	if aap["enableInboundRead"] != true {
		t.Fatalf("normalized enableInboundRead: %v", aap["enableInboundRead"])
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
