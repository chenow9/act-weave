package modelconfig_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/modelconfig"
)

func TestAssertAgentModelToolCompatibilityEmptyCatalogAllowsNone(t *testing.T) {
	none := verifiedConfig(t, modelconfig.ToolCallingNone, json.RawMessage(`{}`))
	if err := modelconfig.AssertAgentModelToolCompatibility(none, modelconfig.AgentModelToolCheck{
		RequireVerified: true,
	}); err != nil {
		t.Fatalf("tool-less none: %v", err)
	}
	unverified := modelconfig.Config{Status: modelconfig.StatusUnverified, AgenticCapabilities: json.RawMessage(`{}`)}
	if err := modelconfig.AssertAgentModelToolCompatibility(unverified, modelconfig.AgentModelToolCheck{
		RequireVerified: true,
	}); err != nil {
		t.Fatalf("tool-less unverified: %v", err)
	}
}

func TestAssertAgentModelToolCompatibilityRejectsNoneBeforeCarryAll(t *testing.T) {
	policy, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	none := verifiedConfig(t, modelconfig.ToolCallingNone, policy)
	err = modelconfig.AssertAgentModelToolCompatibility(none, modelconfig.AgentModelToolCheck{
		AgentID:         "agent-none",
		CatalogCount:    modelconfig.CarryAllHardLimit + 1,
		RequireVerified: true,
	})
	if !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("none+tools want UNSUPPORTED, got %v", err)
	}
	if _, tooLarge := modelconfig.AsCarryAllTooLarge(err); tooLarge {
		t.Fatal("none must not surface carry-all overflow")
	}

	err = modelconfig.AssertAgentModelToolCompatibility(none, modelconfig.AgentModelToolCheck{
		CatalogCount:    1,
		RequireVerified: false,
	})
	if !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("bind none want UNSUPPORTED, got %v", err)
	}
}

func TestAssertAgentModelToolCompatibilityEmptyCapsDoNotFailOpen(t *testing.T) {
	cfg := modelconfig.Config{
		Status:              modelconfig.StatusVerified,
		AgenticCapabilities: json.RawMessage(`{}`),
	}
	err := modelconfig.AssertAgentModelToolCompatibility(cfg, modelconfig.AgentModelToolCheck{
		CatalogCount:    1,
		RequireVerified: true,
	})
	if !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("empty caps on verified row must not count as native, got %v", err)
	}

	unverified := modelconfig.Config{
		Status:              modelconfig.StatusUnverified,
		AgenticCapabilities: json.RawMessage(`{}`),
	}
	if err := modelconfig.AssertAgentModelToolCompatibility(unverified, modelconfig.AgentModelToolCheck{
		CatalogCount:    1,
		RequireVerified: false,
	}); err != nil {
		t.Fatalf("bind treats unverified as not none: %v", err)
	}
	err = modelconfig.AssertAgentModelToolCompatibility(unverified, modelconfig.AgentModelToolCheck{
		CatalogCount:    1,
		RequireVerified: true,
	})
	if !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("update unverified+tools: %v", err)
	}
}

func TestAssertAgentModelToolCompatibilityNativeAndFunctionCalling(t *testing.T) {
	native := verifiedConfig(t, modelconfig.ToolCallingNativeClientSearch, json.RawMessage(`{}`))
	if err := modelconfig.AssertAgentModelToolCompatibility(native, modelconfig.AgentModelToolCheck{
		CatalogCount:    9,
		RequireVerified: true,
	}); err != nil {
		t.Fatalf("native large catalog: %v", err)
	}

	demand, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModePlatformOnDemand)
	if err != nil {
		t.Fatal(err)
	}
	fc := verifiedConfig(t, modelconfig.ToolCallingFunctionCalling, demand)
	if err := modelconfig.AssertAgentModelToolCompatibility(fc, modelconfig.AgentModelToolCheck{
		CatalogCount:    9,
		RequireVerified: true,
	}); err != nil {
		t.Fatalf("platform_on_demand large catalog: %v", err)
	}

	carry, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	fcCarry := verifiedConfig(t, modelconfig.ToolCallingFunctionCalling, carry)
	if err := modelconfig.AssertAgentModelToolCompatibility(fcCarry, modelconfig.AgentModelToolCheck{
		AgentID:         "agent-8",
		CatalogCount:    modelconfig.CarryAllHardLimit,
		RequireVerified: true,
	}); err != nil {
		t.Fatalf("carry-all at hard limit: %v", err)
	}
	err = modelconfig.AssertAgentModelToolCompatibility(fcCarry, modelconfig.AgentModelToolCheck{
		AgentID:         "agent-9",
		CatalogCount:    modelconfig.CarryAllHardLimit + 1,
		RequireVerified: false,
	})
	tooLarge, ok := modelconfig.AsCarryAllTooLarge(err)
	if !ok || tooLarge.Count != 9 || tooLarge.Limit != 8 || tooLarge.AgentID != "agent-9" {
		t.Fatalf("carry-all overflow: %+v err=%v", tooLarge, err)
	}
}

func TestAssertAgentModelToolCompatibilityDelegationEdges(t *testing.T) {
	none := verifiedConfig(t, modelconfig.ToolCallingNone, json.RawMessage(`{}`))
	err := modelconfig.AssertAgentModelToolCompatibility(none, modelconfig.AgentModelToolCheck{
		HasDelegationEdges: true,
		RequireVerified:    true,
	})
	if !errors.Is(err, modelconfig.ErrAgentModelToolsUnsupported) {
		t.Fatalf("none+edges: %v", err)
	}

	native := verifiedConfig(t, modelconfig.ToolCallingNativeClientSearch, json.RawMessage(`{}`))
	if err := modelconfig.AssertAgentModelToolCompatibility(native, modelconfig.AgentModelToolCheck{
		HasDelegationEdges: true,
		RequireVerified:    true,
	}); err != nil {
		t.Fatalf("native+edges: %v", err)
	}
}

func verifiedConfig(t *testing.T, calling string, policy json.RawMessage) modelconfig.Config {
	t.Helper()
	digest := strings.Repeat("ab", 32)
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	var doc modelconfig.AgenticCapabilities
	var err error
	if calling == modelconfig.ToolCallingNativeClientSearch {
		doc, err = modelconfig.CanonicalAgenticCapabilities(at, 1, digest)
	} else {
		doc, err = modelconfig.CanonicalAgenticCapabilitiesV2(calling, at, 1, digest)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy) == 0 {
		policy = json.RawMessage(`{}`)
	}
	return modelconfig.Config{
		Status:               modelconfig.StatusVerified,
		AgenticCapabilities:  raw,
		ToolDisclosurePolicy: policy,
		LockVersion:          2,
	}
}
