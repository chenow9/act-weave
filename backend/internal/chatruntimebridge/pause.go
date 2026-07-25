package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
)

// pauseForInterrupt prepares a platform confirmation with outer
// tool-resume-request.v1 + nested einoChatResume, then aligns checkpoint TTL
// to confirmation.ExpiresAt (D15).
func (b *Bridge) pauseForInterrupt(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	result *einoruntime.RunResult,
) error {
	if b.confirmations == nil {
		return fmt.Errorf("confirmation is required but not configured")
	}
	if result == nil || !result.Interrupted {
		return fmt.Errorf("pauseForInterrupt requires Interrupted result")
	}
	if strings.TrimSpace(result.CheckpointID) == "" {
		return fmt.Errorf("interrupted run missing checkpoint id")
	}

	pendingKey := pendingConfirmKey(job.WorkspaceID, job.RunID)
	pending := b.takePending(pendingKey)
	if len(pending) == 0 {
		return fmt.Errorf("interrupted run missing pending confirm metadata")
	}
	// v1: single root-cause tool confirmation at a time (serial tools).
	confirm := pending[len(pending)-1]

	rootID := ""
	if len(result.RootCauseInterruptIDs) > 0 {
		rootID = result.RootCauseInterruptIDs[0]
	} else if len(result.InterruptContextIDs) > 0 {
		rootID = result.InterruptContextIDs[0]
	}
	if rootID == "" {
		return errInterruptIDMissing
	}
	interruptIDs := append([]string(nil), result.InterruptContextIDs...)
	if len(interruptIDs) == 0 {
		interruptIDs = []string{rootID}
	}

	gatedToolCallID := ToolCallIDFromInterruptID(rootID)
	if gatedToolCallID == "" {
		gatedToolCallID = confirm.InvocationID
	}

	capabilities, err := chatruntime.ParseCapabilitySnapshot(run.CapabilitySnapshot)
	if err != nil {
		return err
	}
	capability, ok := lookupCapability(capabilities, confirm.ToolName, confirm.CapabilityID)
	if !ok {
		return fmt.Errorf("gated capability %q not found in run snapshot", confirm.ToolName)
	}

	// Prefer the non-sensitive resolved snapshot captured on first actual tool
	// call (lazy resolve). Re-resolve only if the pending hook lacked it
	// (legacy/in-flight interrupt safety for older process state).
	resolved := confirm.Resolved
	if strings.TrimSpace(resolved.Snapshot.CapabilityID) == "" ||
		strings.TrimSpace(resolved.Snapshot.ReleaseID) == "" {
		if b.toolInvoker == nil {
			return fmt.Errorf("tool invoker is not configured")
		}
		var resolveErr error
		resolved, resolveErr = b.toolInvoker.ResolveInvocation(ctx, execution.ResolveRequest{
			WorkspaceID: job.WorkspaceID, CapabilityID: capability.CapabilityID,
			ReleaseID: capability.ReleaseID, BindingConnectionID: capability.ConnectionID,
		})
		if resolveErr != nil {
			return fmt.Errorf("resolve gated capability: %w", resolveErr)
		}
	}

	input, err := normalizeToolArgs(confirm.ArgsJSON)
	if err != nil {
		return err
	}

	invokeRequest := execution.InvokeRequest{
		InvocationID: confirm.InvocationID, WorkspaceID: job.WorkspaceID,
		CapabilityID: capability.CapabilityID, ReleaseID: capability.ReleaseID,
		ActorType: run.TriggeredByType, ActorID: firstNonEmpty(job.ActorID, run.TriggeredByID),
		TraceID: run.TraceID, Input: input, BindingConnectionID: capability.ConnectionID,
		AgentRunID: job.RunID, PrincipalSnapshot: &run.PrincipalSnapshot,
		AuthorizationSnapshot: json.RawMessage(`{"source":"chatruntimebridge","parent":"agent_run"}`),
	}

	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(invokeRequest, resolved)
	if err != nil {
		return err
	}

	meta := EinoChatResume{
		ResumeSchemaVersion: EinoChatResumeSchemaVersion,
		SessionID:           job.SessionID,
		UserMessageID:       job.UserMessageID,
		ActorID:             job.ActorID,
		EinoCheckpointID:    result.CheckpointID,
		InterruptIDs:        interruptIDs,
		RootInterruptID:     rootID,
		GatedToolCallID:     gatedToolCallID,
		GatedStepID:         confirm.StepID,
		InterruptKind:       InterruptKindToolConfirmation,
	}
	requestSnapshot, err = EmbedEinoChatResume(requestSnapshot, meta)
	if err != nil {
		return err
	}

	session, err := b.sessions.GetSession(ctx, job.WorkspaceID, job.SessionID)
	if err != nil {
		return err
	}
	currentRun, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return err
	}

	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID:            capability.ReleaseID,
			RiskLevel:            firstNonEmpty(capability.RiskLevel, resolved.RiskLevel, "HIGH"),
			SideEffectLevel:      firstNonEmpty(capability.SideEffectLevel, resolved.SideEffectLevel, "WRITE"),
			RequiresConfirmation: true,
			InputSchema:          capability.InputSchema,
		},
		Connection: execution.ConfirmationConnectionRisk{
			ConnectionID: resolved.Connection.ID, Environment: resolved.Connection.Environment,
		},
		Input: input,
	})
	if err != nil {
		return err
	}
	if !decision.RequiresConfirmation {
		decision.RequiresConfirmation = true
		decision.RiskReasons = []string{execution.ConfirmationReasonReleaseRequired}
	}

	confirmationID, err := newRuntimeID()
	if err != nil {
		return err
	}
	riskLevel := firstNonEmpty(capability.RiskLevel, resolved.RiskLevel, "HIGH")
	if riskLevel != "LOW" && riskLevel != "MEDIUM" && riskLevel != "HIGH" && riskLevel != "CRITICAL" {
		riskLevel = "HIGH"
	}

	prepared, err := b.confirmations.Prepare(ctx, chat.PrepareChatConfirmationInput{
		ID: confirmationID, WorkspaceID: job.WorkspaceID, SessionID: job.SessionID,
		MessageID: job.UserMessageID, TargetType: execution.ResumeKindTool,
		RiskLevel: riskLevel, RiskReasons: append([]string(nil), decision.RiskReasons...),
		InputSummary: json.RawMessage(
			`{"source":"chatruntimebridge","toolCallId":"` + jsonEscape(gatedToolCallID) + `"}`,
		),
		ExpectedSessionLockVersion: session.LockVersion,
		Resume: execution.PrepareConfirmationResumeInput{
			Confirmation: execution.RequestExecutionConfirmationInput{
				ID: mustNewID(), WorkspaceID: job.WorkspaceID, RunID: job.RunID,
				TargetItemID: invokeRequest.InvocationID,
				ReleaseID:    capability.ReleaseID, ConnectionID: resolved.Connection.ID,
				RequestedBy: run.TriggeredByID, PrincipalSnapshot: &run.PrincipalSnapshot,
				Decision: decision,
			},
			Kind:                   execution.ResumeKindTool,
			SnapshotSchemaVersion:  execution.ConfirmationResumeSnapshotVersion,
			RequestSnapshot:        requestSnapshot,
			ResolvedSnapshot:       resolvedSnapshot,
			Input:                  input,
			AgentRunStepID:         confirm.StepID,
			ExpectedRunLockVersion: currentRun.LockVersion,
			TerminalOnSuccess:      false,
		},
	})
	if err != nil {
		return err
	}

	// D15: checkpoint expires_at = confirmation.ExpiresAt.
	if b.checkpointTTL != nil && !prepared.Confirmation.ExpiresAt.IsZero() {
		if touchErr := b.checkpointTTL.TouchExpiresAt(
			ctx, result.CheckpointID, prepared.Confirmation.ExpiresAt,
		); touchErr != nil {
			b.logger.Error("eino checkpoint TTL align failed",
				"event", "chatruntimebridge.checkpoint.ttl_align_failed",
				"workspace_id", job.WorkspaceID,
				"run_id", job.RunID,
				"checkpoint_id", result.CheckpointID,
				"error", touchErr.Error(),
			)
			// Non-fatal: confirmation is durable; default TTL still bounds resume.
		}
	}

	waitingRun, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return err
	}
	return b.recordProtocol(ctx, chatruntime.ProtocolRecord{
		Kind: chatruntime.ProtocolRecordInteractionRequested, Job: job, Run: waitingRun,
		Confirmation: &prepared, TargetName: capability.CallableName,
		TargetArguments: append(json.RawMessage(nil), input...), OccurredAt: b.now().UTC(),
	})
}

func lookupCapability(
	capabilities []chatruntime.SnapshotCapability,
	toolName, capabilityID string,
) (chatruntime.SnapshotCapability, bool) {
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	capabilityID = strings.TrimSpace(capabilityID)
	for _, cap := range capabilities {
		if capabilityID != "" && cap.CapabilityID == capabilityID {
			return cap, true
		}
		if toolName != "" && strings.ToLower(cap.CallableName) == toolName {
			return cap, true
		}
	}
	return chatruntime.SnapshotCapability{}, false
}

func normalizeToolArgs(args string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("tool arguments are not valid JSON")
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, fmt.Errorf("tool arguments are not valid JSON: %w", err)
	}
	return json.Marshal(value)
}

func jsonEscape(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	// json.Marshal adds quotes; strip them for embedding inside a JSON string.
	if len(encoded) >= 2 && encoded[0] == '"' {
		return string(encoded[1 : len(encoded)-1])
	}
	return string(encoded)
}

func mustNewID() string {
	id, err := newRuntimeID()
	if err != nil {
		return "00000000-0000-7000-8000-000000000001"
	}
	return id
}
