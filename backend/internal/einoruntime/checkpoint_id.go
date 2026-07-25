package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Storage kind values written to eino_checkpoints.kind.
const (
	CheckpointKindAgentRun          = "agent_run"
	CheckpointKindWorkflowExecution = "workflow_execution"
)

// Path segment after workspace for each kind. workflow_exec is shorter than
// the storage kind workflow_execution; both are fixed by design Rev 4.
const (
	checkpointPathKindAgentRun     = "agent_run"
	checkpointPathKindWorkflowExec = "workflow_exec"
	checkpointIDPrefix             = "ws"
	checkpointIDSegmentCount       = 5
)

// ErrInvalidCheckpointID is returned when a checkpoint ID fails prefix /
// shape validation. Eino must never receive IDs without a workspace prefix.
var ErrInvalidCheckpointID = errors.New("invalid eino checkpoint id")

// CheckpointID is a fully parsed multi-tenant checkpoint key.
type CheckpointID struct {
	// Raw is the original full key.
	Raw string
	// WorkspaceID is the tenant segment from the ws/ prefix.
	WorkspaceID string
	// Kind is the storage kind: agent_run or workflow_execution.
	Kind string
	// OwnerID is run_id (agent_run) or execution_id (workflow_execution).
	OwnerID string
	// Nonce is the per-run stable suffix (generated once per AgentRun /
	// WorkflowExecution lifecycle).
	Nonce string
}

type trustedWorkspaceKey struct{}

// WithTrustedWorkspaceID injects a trusted workspace for optional store-side
// defence-in-depth: when present, Get/Set/Delete require the parsed ID
// workspace to match. Bridge code should set this from the authenticated
// request scope.
func WithTrustedWorkspaceID(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, trustedWorkspaceKey{}, strings.TrimSpace(workspaceID))
}

// TrustedWorkspaceID returns the workspace previously injected via
// WithTrustedWorkspaceID, if any.
func TrustedWorkspaceID(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(trustedWorkspaceKey{}).(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// ParseCheckpointID validates and parses a full checkpoint key.
//
// Allowed shapes:
//
//	ws/{workspaceID}/agent_run/{runID}/{nonce}
//	ws/{workspaceID}/workflow_exec/{executionID}/{nonce}
func ParseCheckpointID(id string) (CheckpointID, error) {
	raw := strings.TrimSpace(id)
	if raw == "" {
		return CheckpointID{}, fmt.Errorf("%w: empty id", ErrInvalidCheckpointID)
	}
	// Reject leading/trailing slashes and empty segments by exact split count.
	parts := strings.Split(raw, "/")
	if len(parts) != checkpointIDSegmentCount {
		return CheckpointID{}, fmt.Errorf(
			"%w: expected %d slash-separated segments, got %d",
			ErrInvalidCheckpointID, checkpointIDSegmentCount, len(parts),
		)
	}
	for i, part := range parts {
		if strings.TrimSpace(part) == "" || part != strings.TrimSpace(part) {
			return CheckpointID{}, fmt.Errorf(
				"%w: empty or whitespace-padded segment at index %d",
				ErrInvalidCheckpointID, i,
			)
		}
	}
	if parts[0] != checkpointIDPrefix {
		return CheckpointID{}, fmt.Errorf(
			"%w: missing %q prefix (got %q)",
			ErrInvalidCheckpointID, checkpointIDPrefix, parts[0],
		)
	}
	kind, err := pathKindToStorageKind(parts[2])
	if err != nil {
		return CheckpointID{}, err
	}
	return CheckpointID{
		Raw:         raw,
		WorkspaceID: parts[1],
		Kind:        kind,
		OwnerID:     parts[3],
		Nonce:       parts[4],
	}, nil
}

// ParseWorkspacePrefix is the thin helper used by the store: it returns only
// the workspace segment after full ID validation.
func ParseWorkspacePrefix(id string) (string, error) {
	parsed, err := ParseCheckpointID(id)
	if err != nil {
		return "", err
	}
	return parsed.WorkspaceID, nil
}

// FormatCheckpointID builds a canonical checkpoint key.
// kind may be either the storage kind (workflow_execution) or the path
// segment (workflow_exec).
func FormatCheckpointID(workspaceID, kind, ownerID, nonce string) (string, error) {
	ws := strings.TrimSpace(workspaceID)
	owner := strings.TrimSpace(ownerID)
	n := strings.TrimSpace(nonce)
	if ws == "" || owner == "" || n == "" {
		return "", fmt.Errorf("%w: workspaceID, ownerID, and nonce are required", ErrInvalidCheckpointID)
	}
	if strings.ContainsAny(ws, "/") || strings.ContainsAny(owner, "/") || strings.ContainsAny(n, "/") {
		return "", fmt.Errorf("%w: segments must not contain '/'", ErrInvalidCheckpointID)
	}
	pathKind, err := storageKindToPathKind(kind)
	if err != nil {
		return "", err
	}
	id := strings.Join([]string{checkpointIDPrefix, ws, pathKind, owner, n}, "/")
	// Round-trip validates shape.
	if _, err := ParseCheckpointID(id); err != nil {
		return "", err
	}
	return id, nil
}

func pathKindToStorageKind(pathKind string) (string, error) {
	switch pathKind {
	case checkpointPathKindAgentRun:
		return CheckpointKindAgentRun, nil
	case checkpointPathKindWorkflowExec:
		return CheckpointKindWorkflowExecution, nil
	default:
		return "", fmt.Errorf(
			"%w: unknown kind segment %q (want %q or %q)",
			ErrInvalidCheckpointID, pathKind,
			checkpointPathKindAgentRun, checkpointPathKindWorkflowExec,
		)
	}
}

func storageKindToPathKind(kind string) (string, error) {
	switch strings.TrimSpace(kind) {
	case CheckpointKindAgentRun: // same string as path segment "agent_run"
		return checkpointPathKindAgentRun, nil
	case CheckpointKindWorkflowExecution, checkpointPathKindWorkflowExec:
		return checkpointPathKindWorkflowExec, nil
	default:
		return "", fmt.Errorf(
			"%w: unknown kind %q (want %q or %q)",
			ErrInvalidCheckpointID, kind,
			CheckpointKindAgentRun, CheckpointKindWorkflowExecution,
		)
	}
}

func matchTrustedWorkspace(ctx context.Context, workspaceID string) error {
	trusted, ok := TrustedWorkspaceID(ctx)
	if !ok {
		return nil
	}
	if trusted != workspaceID {
		return fmt.Errorf(
			"%w: trusted workspace %q does not match id workspace %q",
			ErrInvalidCheckpointID, trusted, workspaceID,
		)
	}
	return nil
}
