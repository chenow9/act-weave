package einoruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseCheckpointIDValidAgentRun(t *testing.T) {
	t.Parallel()
	id := "ws/ws-abc/agent_run/run-1/nonce-xyz"
	parsed, err := ParseCheckpointID(id)
	if err != nil {
		t.Fatalf("ParseCheckpointID: %v", err)
	}
	if parsed.Raw != id {
		t.Fatalf("Raw = %q, want %q", parsed.Raw, id)
	}
	if parsed.WorkspaceID != "ws-abc" {
		t.Fatalf("WorkspaceID = %q", parsed.WorkspaceID)
	}
	if parsed.Kind != CheckpointKindAgentRun {
		t.Fatalf("Kind = %q, want %q", parsed.Kind, CheckpointKindAgentRun)
	}
	if parsed.OwnerID != "run-1" {
		t.Fatalf("OwnerID = %q", parsed.OwnerID)
	}
	if parsed.Nonce != "nonce-xyz" {
		t.Fatalf("Nonce = %q", parsed.Nonce)
	}
}

func TestParseCheckpointIDValidWorkflowExec(t *testing.T) {
	t.Parallel()
	id := "ws/ws-99/workflow_exec/exec-42/n1"
	parsed, err := ParseCheckpointID(id)
	if err != nil {
		t.Fatalf("ParseCheckpointID: %v", err)
	}
	if parsed.Kind != CheckpointKindWorkflowExecution {
		t.Fatalf("Kind = %q, want %q", parsed.Kind, CheckpointKindWorkflowExecution)
	}
	if parsed.OwnerID != "exec-42" {
		t.Fatalf("OwnerID = %q", parsed.OwnerID)
	}
	ws, err := ParseWorkspacePrefix(id)
	if err != nil || ws != "ws-99" {
		t.Fatalf("ParseWorkspacePrefix = %q, %v", ws, err)
	}
}

func TestParseCheckpointIDRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "whitespace", id: "   "},
		{name: "no_prefix", id: "agent_run/run-1/nonce"},
		{name: "wrong_prefix", id: "tenant/ws-1/agent_run/run-1/nonce"},
		{name: "missing_segments", id: "ws/ws-1/agent_run/run-1"},
		{name: "extra_segments", id: "ws/ws-1/agent_run/run-1/nonce/extra"},
		{name: "empty_workspace", id: "ws//agent_run/run-1/nonce"},
		{name: "empty_owner", id: "ws/ws-1/agent_run//nonce"},
		{name: "empty_nonce", id: "ws/ws-1/agent_run/run-1/"},
		{name: "unknown_kind", id: "ws/ws-1/workflow_execution/run-1/nonce"},
		{name: "padded_segment", id: "ws/ ws-1/agent_run/run-1/nonce"},
		{name: "leading_slash", id: "/ws/ws-1/agent_run/run-1/nonce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCheckpointID(tc.id)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrInvalidCheckpointID) {
				t.Fatalf("expected ErrInvalidCheckpointID, got %v", err)
			}
		})
	}
}

func TestFormatCheckpointIDRoundTrip(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{
		CheckpointKindAgentRun,
		CheckpointKindWorkflowExecution,
		"workflow_exec",
	} {
		id, err := FormatCheckpointID("ws-1", kind, "owner-1", "nonce-1")
		if err != nil {
			t.Fatalf("FormatCheckpointID(%q): %v", kind, err)
		}
		parsed, err := ParseCheckpointID(id)
		if err != nil {
			t.Fatalf("Parse after Format(%q): %v", kind, err)
		}
		if parsed.WorkspaceID != "ws-1" || parsed.OwnerID != "owner-1" || parsed.Nonce != "nonce-1" {
			t.Fatalf("unexpected parse %#v", parsed)
		}
		if !strings.HasPrefix(id, "ws/ws-1/") {
			t.Fatalf("id missing prefix: %q", id)
		}
	}
}

func TestFormatCheckpointIDRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ws, kind, owner, nonce string
	}{
		{ws: "", kind: CheckpointKindAgentRun, owner: "o", nonce: "n"},
		{ws: "ws", kind: "nope", owner: "o", nonce: "n"},
		{ws: "a/b", kind: CheckpointKindAgentRun, owner: "o", nonce: "n"},
		{ws: "ws", kind: CheckpointKindAgentRun, owner: "", nonce: "n"},
	}
	for _, tc := range cases {
		if _, err := FormatCheckpointID(tc.ws, tc.kind, tc.owner, tc.nonce); err == nil {
			t.Fatalf("expected error for %#v", tc)
		}
	}
}

func TestTrustedWorkspaceContext(t *testing.T) {
	t.Parallel()
	ctx := WithTrustedWorkspaceID(context.Background(), "ws-1")
	got, ok := TrustedWorkspaceID(ctx)
	if !ok || got != "ws-1" {
		t.Fatalf("TrustedWorkspaceID = %q, %v", got, ok)
	}
	if _, ok := TrustedWorkspaceID(context.Background()); ok {
		t.Fatal("expected no trusted workspace on bare context")
	}
	if err := matchTrustedWorkspace(ctx, "ws-1"); err != nil {
		t.Fatalf("match same: %v", err)
	}
	if err := matchTrustedWorkspace(ctx, "ws-2"); err == nil {
		t.Fatal("expected mismatch error")
	}
}
