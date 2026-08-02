package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// ChildRunStoreAdapter implements agentdelegation.ChildRunStore via RunRepository.
type ChildRunStoreAdapter struct {
	Runs *execution.RunRepository
	// GetParent is required. Parent agent_run is the sole freeze authority for TASK children.
	GetParent func(ctx context.Context, workspaceID, parentRunID string) (execution.AgentRun, error)
}

func (a *ChildRunStoreAdapter) StartChild(ctx context.Context, in agentdelegation.ChildRunStartInput) (string, error) {
	if a == nil || a.Runs == nil {
		return "", fmt.Errorf("child run store not configured")
	}
	if a.GetParent == nil {
		return "", fmt.Errorf("TASK child: GetParent required (parent agent_run is freeze authority)")
	}
	parent, err := a.GetParent(ctx, in.WorkspaceID, in.ParentRunID)
	if err != nil {
		return "", fmt.Errorf("load parent run for TASK freeze: %w", err)
	}

	// Parent graph is the only freeze authority.
	parentGraph := parent.AgentGraphSnapshot
	if len(parentGraph) == 0 || string(parentGraph) == "{}" || string(parentGraph) == "null" {
		return "", fmt.Errorf("TASK child: parent agent_graph_snapshot required (empty)")
	}
	// Caller-provided GraphSnapshot, if any, must match parent graph (semantic).
	if len(in.GraphSnapshot) > 0 && string(in.GraphSnapshot) != "{}" && string(in.GraphSnapshot) != "null" {
		if !jsonEqualRaw(in.GraphSnapshot, parentGraph) {
			return "", fmt.Errorf("TASK child: caller GraphSnapshot mismatches parent freeze")
		}
	}
	snap, perr := agentdelegation.ParseSnapshot(parentGraph)
	if perr != nil {
		return "", fmt.Errorf("parse parent agent_graph_snapshot for TASK child: %w", perr)
	}
	if snap == nil {
		return "", fmt.Errorf("TASK child: parent agent_graph_snapshot parse returned nil")
	}

	target := strings.TrimSpace(in.TargetAgentID)
	if target == "" {
		return "", fmt.Errorf("TASK child: TargetAgentID required")
	}
	var node *agentdelegation.GraphNodeSnapshot
	for i := range snap.Nodes {
		if snap.Nodes[i].AgentID == target {
			node = &snap.Nodes[i]
			break
		}
	}
	if node == nil {
		return "", fmt.Errorf("TASK child: target %s not in parent graph nodes (no caller snapshot fallback)", target)
	}
	// Always use original frozen node bytes (never rebuild from id/lockVersion).
	if len(node.ModelSnapshot) == 0 || string(node.ModelSnapshot) == "{}" {
		return "", fmt.Errorf("TASK child: frozen modelSnapshot missing for target %s", target)
	}
	if len(node.AgentSnapshot) == 0 || string(node.AgentSnapshot) == "{}" {
		return "", fmt.Errorf("TASK child: frozen agentSnapshot missing for target %s", target)
	}
	if len(node.CapabilitySnapshot) == 0 {
		return "", fmt.Errorf("TASK child: frozen capabilitySnapshot missing for target %s", target)
	}
	// If caller provided per-node snapshots, they must match graph authority.
	if len(in.ModelSnapshot) > 0 && !jsonEqualRaw(in.ModelSnapshot, node.ModelSnapshot) {
		return "", fmt.Errorf("TASK child: modelSnapshot mismatch vs graph node %s", target)
	}
	if len(in.AgentSnapshot) > 0 && !jsonEqualRaw(in.AgentSnapshot, node.AgentSnapshot) {
		return "", fmt.Errorf("TASK child: agentSnapshot mismatch vs graph node %s", target)
	}
	if len(in.CapabilitySnapshot) > 0 && !jsonEqualRaw(in.CapabilitySnapshot, node.CapabilitySnapshot) {
		return "", fmt.Errorf("TASK child: capabilitySnapshot mismatch vs graph node %s", target)
	}

	modelSnap := append(json.RawMessage(nil), node.ModelSnapshot...)
	agentSnap := append(json.RawMessage(nil), node.AgentSnapshot...)
	capSnap := append(json.RawMessage(nil), node.CapabilitySnapshot...)
	graphSnap := append(json.RawMessage(nil), parentGraph...)

	triggeredByType := in.TriggeredByType
	triggeredByID := in.TriggeredByID
	if triggeredByType == "" || triggeredByType == "SYSTEM" || triggeredByID == "" {
		triggeredByType = firstNonEmpty(parent.TriggeredByType, "USER")
		triggeredByID = firstNonEmpty(parent.TriggeredByID, in.ParentRunID)
	}

	childID := uuid.Must(uuid.NewV7()).String()
	input := in.InputSummary
	if len(input) == 0 {
		input = json.RawMessage(`{"source":"agentdelegation.task"}`)
	}
	_, err = a.Runs.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: childID, WorkspaceID: in.WorkspaceID, AgentID: target,
		TriggerType: "DELEGATION_TASK", TriggeredByType: firstNonEmpty(triggeredByType, "USER"),
		TriggeredByID: firstNonEmpty(triggeredByID, in.ParentRunID),
		TraceID:       firstNonEmpty(in.TraceID, in.ParentRunID),
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         modelSnap,
			Capabilities:  capSnap,
			ContextPolicy: json.RawMessage(`{}`),
			Agent:         agentSnap,
		},
		AuthorizationSnapshot: json.RawMessage(`{"source":"agentdelegation.task"}`),
		InputSummary:          input,
		ParentRunID:           in.ParentRunID,
		ParentDelegationID:    in.ParentDelegationID,
		AgentGraphSnapshot:    graphSnap,
	})
	if err != nil {
		return "", err
	}
	return childID, nil
}

func jsonEqualRaw(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return string(a) == string(b)
	}
	ba, _ := json.Marshal(va)
	bb, _ := json.Marshal(vb)
	return string(ba) == string(bb)
}

func (a *ChildRunStoreAdapter) FinishChild(
	ctx context.Context, workspaceID, childRunID, status, errorCode string, output json.RawMessage,
) error {
	if a == nil || a.Runs == nil {
		return fmt.Errorf("child run store not configured")
	}
	run, err := a.Runs.GetAgentRun(ctx, workspaceID, childRunID)
	if err != nil {
		return err
	}
	if run.Status != "RUNNING" && run.Status != "PENDING" {
		return nil // already terminal
	}
	newStatus := strings.ToUpper(strings.TrimSpace(status))
	errorCode = strings.TrimSpace(errorCode)
	switch newStatus {
	case agentdelegation.StatusSucceeded:
		newStatus = "SUCCEEDED"
	case agentdelegation.StatusFailed:
		newStatus = "FAILED"
	case agentdelegation.StatusCancelled:
		newStatus = "CANCELLED"
	case agentdelegation.StatusTimedOut:
		newStatus = "TIMED_OUT"
	default:
		newStatus = "FAILED"
		if errorCode == "" {
			errorCode = "DELEGATION_CHILD_FAILED"
		}
	}
	// validRunTransition: only FAILED may carry ErrorCode. Preserve audit codes
	// for CANCELLED/TIMED_OUT inside output summary so delegation evidence keeps them.
	if newStatus != "FAILED" {
		if errorCode != "" {
			if len(output) == 0 {
				output = json.RawMessage(`{}`)
			}
			var m map[string]any
			if json.Unmarshal(output, &m) != nil || m == nil {
				m = map[string]any{}
			}
			if _, ok := m["errorCode"]; !ok {
				m["errorCode"] = errorCode
			}
			if b, mErr := json.Marshal(m); mErr == nil {
				output = b
			}
		}
		errorCode = ""
	} else if errorCode == "" {
		errorCode = "DELEGATION_CHILD_FAILED"
	}
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	_, err = a.Runs.TransitionAgentRun(ctx, workspaceID, childRunID, execution.RunTransition{
		ExpectedStatus: run.Status, ExpectedLockVersion: run.LockVersion,
		NewStatus: newStatus, OutputSummary: output, ErrorCode: errorCode,
	})
	return err
}

func (a *ChildRunStoreAdapter) CancelChild(ctx context.Context, workspaceID, childRunID string) error {
	return a.FinishChild(ctx, workspaceID, childRunID, agentdelegation.StatusCancelled, "", json.RawMessage(`{"cancelled":true}`))
}
