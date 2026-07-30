package config

import (
	"testing"
)

func TestCompactionGateDefaultClosed(t *testing.T) {
	var zero SessionContextRollout
	n := zero.Normalized()
	if n.Compaction.Enabled || n.Compaction.Mode != "disabled" {
		t.Fatalf("default compaction=%+v", n.Compaction)
	}
	ws := "a0000000-0000-4000-8000-000000000001"
	if n.AllowsCompaction(ws) {
		t.Fatal("default must deny compaction")
	}
	if n.Compaction.IsShadow() || n.Compaction.IsEnforced() {
		t.Fatal("default mode flags")
	}
}

func TestCompactionGateIndependentAllowlist(t *testing.T) {
	ws := "a0000000-0000-4000-8000-000000000001"
	other := "b0000000-0000-4000-8000-000000000002"
	// Parent session context open for all; compaction only allowlisted ws.
	gate := SessionContextRollout{
		Enabled: true, AllowAllWorkspaces: true, Mode: "enforced",
		Compaction: CompactionRollout{
			Enabled: true, Mode: "enforced",
			AllowAllWorkspaces: false,
			WorkspaceIDs:       []string{ws},
			RolloutVersion:     "c1",
		},
	}.Normalized()
	if !gate.AllowsWorkspace(ws) || !gate.AllowsWorkspace(other) {
		t.Fatal("parent should allow all")
	}
	if !gate.AllowsCompaction(ws) {
		t.Fatal("compaction allowlist hit")
	}
	if gate.AllowsCompaction(other) {
		t.Fatal("compaction must not inherit parent allow-all")
	}
}

func TestCompactionGateShadowAndEnforced(t *testing.T) {
	ws := "a0000000-0000-4000-8000-000000000001"
	shadow := CompactionRollout{Enabled: true, Mode: "shadow", AllowAllWorkspaces: true}.Normalized()
	if !shadow.AllowsWorkspace(ws) || !shadow.IsShadow() || shadow.IsEnforced() {
		t.Fatalf("shadow=%+v", shadow)
	}
	enforced := CompactionRollout{Enabled: true, Mode: "enforced", AllowAllWorkspaces: true}.Normalized()
	if !enforced.AllowsWorkspace(ws) || !enforced.IsEnforced() {
		t.Fatalf("enforced=%+v", enforced)
	}
	// enabled but mode disabled
	off := CompactionRollout{Enabled: true, Mode: "disabled", AllowAllWorkspaces: true}.Normalized()
	if off.AllowsWorkspace(ws) {
		t.Fatal("mode disabled must deny")
	}
	// unknown mode
	bad := CompactionRollout{Enabled: true, Mode: "maybe", AllowAllWorkspaces: true}.Normalized()
	if bad.AllowsWorkspace(ws) {
		t.Fatal("unknown mode deny")
	}
}

func TestCompactionRolloutVersionDefault(t *testing.T) {
	n := CompactionRollout{}.Normalized()
	if n.RolloutVersion != "context-compaction-default" {
		t.Fatalf("rollout=%s", n.RolloutVersion)
	}
}
