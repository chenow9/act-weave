package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/config"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
)

func TestParseNodeVsRootCredentialRules(t *testing.T) {
	cfg := modelconfig.Config{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1", Provider: "openai",
		APIBase: "https://api.example.com/v1", ModelName: "gpt-test",
		Options: json.RawMessage(`{}`), Status: modelconfig.StatusVerified, LockVersion: 2,
		AgenticCapabilities:  json.RawMessage(`{}`),
		RuntimeCapabilities:  json.RawMessage(`{}`),
		ToolDisclosurePolicy: json.RawMessage(`{}`),
	}
	node, err := MarshalNodeModelSnapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(node), `"credentialSecretId":null`) {
		t.Fatalf("node must emit credentialSecretId null: %s", node)
	}
	if !strings.Contains(string(node), `"toolDisclosurePolicy"`) {
		t.Fatalf("node must emit toolDisclosurePolicy: %s", node)
	}
	got, err := parseNodeModelSnapshotStrict(node, "ws", cfg.ID, cfg.LockVersion)
	if err != nil {
		t.Fatal(err)
	}
	if got.CredentialSecretID != nil {
		t.Fatalf("null credential must parse as nil, got %v", got.CredentialSecretID)
	}

	rootOmit := `{` +
		`"id":"` + cfg.ID + `","provider":"openai","apiBase":"https://api.example.com/v1",` +
		`"modelName":"gpt-test","options":{},"status":"VERIFIED","lockVersion":2,` +
		`"agenticCapabilities":{},"runtimeCapabilities":{}` +
		`}`
	root, err := parseModelSnapshotStrict(json.RawMessage(rootOmit), "ws")
	if err != nil {
		t.Fatalf("root omit credential: %v", err)
	}
	if root.CredentialSecretID != nil {
		t.Fatal("root omitted credential must be nil")
	}
	if string(root.ToolDisclosurePolicy) != "{}" {
		t.Fatalf("root omitted policy must be {}, got %s", root.ToolDisclosurePolicy)
	}

	rootNull := `{` +
		`"id":"` + cfg.ID + `","provider":"openai","apiBase":"https://api.example.com/v1",` +
		`"modelName":"gpt-test","options":{},"status":"VERIFIED","lockVersion":2,` +
		`"agenticCapabilities":{},"runtimeCapabilities":{},"credentialSecretId":null` +
		`}`
	if _, err := parseModelSnapshotStrict(json.RawMessage(rootNull), "ws"); err == nil {
		t.Fatal("root explicit null credential must fail")
	}
}

func TestRecordToolStep_RedactsPlatformSearch(t *testing.T) {
	store := &captureStepStoreWithTransitions{}
	b := &Bridge{steps: store}
	err := b.recordToolStep(context.Background(), einoruntime.ToolCompleteEvent{
		WorkspaceID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		AgentRunID:  "d08f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		ToolName:    einoruntime.PlatformCatalogSearchToolName,
		ArgsJSON:    `{"query":"secret-catalog","max_results":5}`,
		ResultJSON:  `{"ok":true,"count":1,"loadedNames":["alpha_tool"]}`,
		OK:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.appended) != 0 {
		t.Fatalf("search tool must not persist a TOOL step: %+v", store.appended)
	}
}

func TestBuildModelTurnAuditPayload_ExpandOnlyDisclosure(t *testing.T) {
	payload := buildModelTurnAuditPayload(einoruntime.ModelTurn{
		Content: "ok", HasToolCalls: true, ToolCallIDs: []string{"call_1"},
		ToolSearchMode: string(einoruntime.ToolSearchModePlatformBounded),
		ToolCalling:    modelconfig.ToolCallingFunctionCalling,
	}, true, false)
	if payload["toolSearchMode"] != string(einoruntime.ToolSearchModePlatformBounded) {
		t.Fatalf("mode=%v", payload["toolSearchMode"])
	}
	if payload["toolCalling"] != modelconfig.ToolCallingFunctionCalling {
		t.Fatalf("calling=%v", payload["toolCalling"])
	}
	raw, _ := json.Marshal(payload)
	for _, leak := range []string{"query", "loadedNames", "alpha_tool", "schema"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("payload leaked %q: %s", leak, raw)
		}
	}
	zero := buildModelTurnAuditPayload(einoruntime.ModelTurn{Content: "ok"}, true, false)
	if _, ok := zero["toolSearchMode"]; ok {
		t.Fatal("zero turn must omit toolSearchMode")
	}
}

func TestApplyDisclosureRollout_NoNativeFallback(t *testing.T) {
	b := &Bridge{}
	ws := "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	if err := b.applyDisclosureRollout(ws, einoruntime.ToolSearchModeClientBounded); err != nil {
		t.Fatalf("native: %v", err)
	}
	if err := b.applyDisclosureRollout(ws, einoruntime.ToolSearchModeNone); err != nil {
		t.Fatalf("none: %v", err)
	}
	if err := b.applyDisclosureRollout(ws, einoruntime.ToolSearchModePlatformBounded); !errors.Is(err, modelconfig.ErrToolDisclosureNotRolledOut) {
		t.Fatalf("unrolled platform: %v", err)
	}
	if err := b.applyDisclosureRollout(ws, einoruntime.ToolSearchModeCarryAll); !errors.Is(err, modelconfig.ErrToolDisclosureNotRolledOut) {
		t.Fatalf("unrolled carry: %v", err)
	}
	b.toolDisclosure = config.RuntimeFeatureRollout{Enabled: true, AllowAllWorkspaces: true}
	if err := b.applyDisclosureRollout(ws, einoruntime.ToolSearchModePlatformBounded); err != nil {
		t.Fatalf("rolled platform: %v", err)
	}
}

func TestRootAndChildDispatchSameResolve(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	base := modelconfig.Config{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1", LockVersion: 2,
	}
	digest := modelconfig.WireConfigDigest(base)
	doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(modelconfig.ToolCallingFunctionCalling, at, 1, digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	cfg := base
	cfg.AgenticCapabilities = raw
	tools := []einoruntime.ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "alpha", desc: "tool a"}, Exposure: einoruntime.ToolExposureDeferred},
	}
	cat, err := einoruntime.BuildToolCatalog(context.Background(), tools)
	if err != nil {
		t.Fatal(err)
	}
	rootMode, _, err := resolveDisclosureModeFromCfg(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	childMode, _, err := resolveDisclosureModeFromCfg(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if rootMode != einoruntime.ToolSearchModePlatformBounded || rootMode != childMode {
		t.Fatalf("root=%q child=%q", rootMode, childMode)
	}
}

func resolveDisclosureModeFromCfg(cfg modelconfig.Config, cat *einoruntime.ToolCatalogSnapshot) (einoruntime.ToolSearchMode, string, error) {
	caps, policy, err := parseFrozenDisclosureInputs(cfg)
	if err != nil {
		return "", "", err
	}
	mode, err := resolveDisclosureMode(caps, policy, cat)
	return mode, caps.ToolCalling, err
}

func TestInFlightIgnoresLiveDisclosureChange(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cfg := modelconfig.Config{
		ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1", LockVersion: 2,
	}
	digest := modelconfig.WireConfigDigest(cfg)
	doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(modelconfig.ToolCallingFunctionCalling, at, 1, digest)
	if err != nil {
		t.Fatal(err)
	}
	capsRaw, _ := json.Marshal(doc)
	carry, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	frozen := cfg
	frozen.AgenticCapabilities = capsRaw
	frozen.ToolDisclosurePolicy = carry

	live := frozen
	onDemand, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModePlatformOnDemand)
	if err != nil {
		t.Fatal(err)
	}
	live.ToolDisclosurePolicy = onDemand

	tools := []einoruntime.ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "alpha", desc: "tool a"}, Exposure: einoruntime.ToolExposureDeferred},
	}
	cat, err := einoruntime.BuildToolCatalog(context.Background(), tools)
	if err != nil {
		t.Fatal(err)
	}
	frozenMode, _, err := resolveDisclosureModeFromCfg(frozen, cat)
	if err != nil {
		t.Fatal(err)
	}
	liveMode, _, err := resolveDisclosureModeFromCfg(live, cat)
	if err != nil {
		t.Fatal(err)
	}
	if frozenMode != einoruntime.ToolSearchModeCarryAll {
		t.Fatalf("frozen=%q", frozenMode)
	}
	if liveMode != einoruntime.ToolSearchModePlatformBounded {
		t.Fatalf("live=%q", liveMode)
	}
}

func TestAssemblyFieldsForDisclosure(t *testing.T) {
	mode, ver := assemblyFieldsForDisclosure(einoruntime.ToolSearchModePlatformBounded)
	if mode != execution.AssemblyToolSearchModePlatformBounded || !strings.HasSuffix(ver, ".v2") {
		t.Fatalf("platform: %s %s", mode, ver)
	}
	mode, ver = assemblyFieldsForDisclosure(einoruntime.ToolSearchModeClientBounded)
	if mode != execution.AssemblyToolSearchModeClientBounded || !strings.HasSuffix(ver, ".v1") {
		t.Fatalf("native: %s %s", mode, ver)
	}
}
