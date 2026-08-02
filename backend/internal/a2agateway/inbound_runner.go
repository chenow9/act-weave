package a2agateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// InboundFreeze holds immutable root agent snapshots written at Prepare.
// Execute must use these freezes only (fail closed on empty/missing).
type InboundFreeze struct {
	ModelSnapshot      json.RawMessage
	CapabilitySnapshot json.RawMessage
	AgentSnapshot      json.RawMessage
	ContextPolicy      json.RawMessage
	GraphSnapshot      json.RawMessage
}

// SnapshotFreezer freezes target agent identity at Prepare time.
type SnapshotFreezer interface {
	FreezeInbound(ctx context.Context, workspaceID, agentID string) (InboundFreeze, error)
}

// StaticReplyRunner is a test/dev AgentRunner that returns a fixed reply after
// creating a durable agent_run when Runs is set.
type StaticReplyRunner struct {
	Reply string
	// OnPrepare optional hook for tests.
	OnPrepare func(InboundRunRequest)
	OnExecute func(InboundRunRequest, string)
	// Runs when set, creates a real agent_run row so audit parent_run_id FK works.
	Runs *execution.RunRepository
	// Freezer optional; when set, Prepare writes real freezes.
	Freezer SnapshotFreezer
}

func (r *StaticReplyRunner) PrepareRun(ctx context.Context, req InboundRunRequest) (string, error) {
	if r.OnPrepare != nil {
		r.OnPrepare(req)
	}
	runID := uuid.Must(uuid.NewV7()).String()
	if r.Runs != nil {
		return startInboundAgentRun(ctx, r.Runs, nil, req, runID, r.Freezer, InboundFreeze{})
	}
	return runID, nil
}

// PrepareRunInTx inserts the authority agent_run on the claim transaction.
// Freezing that needs a separate pool connection must be done via MaterializeFreeze first.
func (r *StaticReplyRunner) PrepareRunInTx(ctx context.Context, tx *sql.Tx, req InboundRunRequest, freeze InboundFreeze) (string, error) {
	if r.OnPrepare != nil {
		r.OnPrepare(req)
	}
	runID := uuid.Must(uuid.NewV7()).String()
	if r.Runs != nil {
		return startInboundAgentRun(ctx, r.Runs, tx, req, runID, nil, freeze)
	}
	return runID, nil
}

// MaterializeFreeze builds freeze snapshots without writing agent_runs.
func (r *StaticReplyRunner) MaterializeFreeze(ctx context.Context, req InboundRunRequest) (InboundFreeze, error) {
	return materializeInboundFreeze(ctx, req, r.Freezer)
}

func (r *StaticReplyRunner) ExecuteRun(ctx context.Context, req InboundRunRequest, runID string) (InboundRunResult, error) {
	if r.OnExecute != nil {
		r.OnExecute(req, runID)
	}
	reply := strings.TrimSpace(r.Reply)
	if reply == "" {
		reply = "ok"
	}
	// Under inbound lease fence the gateway owns atomic terminal TX — never
	// TransitionAgentRun here (same contract as DurableInboundRunner).
	if _, fenced := ExecutionFenceFrom(ctx); fenced {
		return InboundRunResult{
			RunID: runID, AssistantText: reply, Status: "SUCCEEDED",
		}, nil
	}
	if r.Runs != nil {
		if run, gerr := r.Runs.GetAgentRun(ctx, req.WorkspaceID, runID); gerr == nil {
			if run.Status == "RUNNING" || run.Status == "PENDING" {
				_, _ = r.Runs.TransitionAgentRun(ctx, req.WorkspaceID, runID, execution.RunTransition{
					ExpectedStatus: run.Status, ExpectedLockVersion: run.LockVersion,
					NewStatus:     "SUCCEEDED",
					OutputSummary: json.RawMessage(`{"source":"a2a.static"}`),
				})
			}
		}
	}
	return InboundRunResult{
		RunID: runID, AssistantText: reply, Status: "SUCCEEDED",
	}, nil
}

func (r *StaticReplyRunner) InterruptRun(context.Context, string, string) error { return nil }

func (r *StaticReplyRunner) CancelRun(ctx context.Context, workspaceID, runID string) error {
	// Candidate-only durable cancel (not used by CancelInbound).
	if r.Runs == nil {
		return nil
	}
	run, err := r.Runs.GetAgentRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	if run.Status != "RUNNING" && run.Status != "PENDING" {
		return nil
	}
	// CANCELLED must not set ErrorCode (validRunTransition rejects non-FAILED+code).
	_, err = r.Runs.TransitionAgentRun(ctx, workspaceID, runID, execution.RunTransition{
		ExpectedStatus: run.Status, ExpectedLockVersion: run.LockVersion,
		NewStatus:     "CANCELLED",
		OutputSummary: json.RawMessage(`{"cancelled":true,"source":"a2a.inbound.candidate","code":"A2A_INBOUND_CANCELLED"}`),
	})
	return err
}

// RejectingRunner always rejects (used when inbound is not fully wired).
type RejectingRunner struct {
	Reason string
}

func (r *RejectingRunner) PrepareRun(context.Context, InboundRunRequest) (string, error) {
	reason := r.Reason
	if reason == "" {
		reason = "inbound agent runner is not configured"
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupported, reason)
}

func (r *RejectingRunner) ExecuteRun(context.Context, InboundRunRequest, string) (InboundRunResult, error) {
	return InboundRunResult{}, fmt.Errorf("%w: runner not configured", ErrUnsupported)
}

func (r *RejectingRunner) InterruptRun(context.Context, string, string) error { return nil }
func (r *RejectingRunner) CancelRun(context.Context, string, string) error    { return nil }

// FunctionRunner adapts callbacks to AgentRunner (application wiring).
type FunctionRunner struct {
	Prepare   func(ctx context.Context, req InboundRunRequest) (runID string, err error)
	Execute   func(ctx context.Context, req InboundRunRequest, runID string) (InboundRunResult, error)
	Interrupt func(ctx context.Context, workspaceID, runID string) error
	Cancel    func(ctx context.Context, workspaceID, runID string) error
}

func (r *FunctionRunner) PrepareRun(ctx context.Context, req InboundRunRequest) (string, error) {
	if r == nil || r.Prepare == nil {
		return "", fmt.Errorf("%w: prepare not configured", ErrUnsupported)
	}
	return r.Prepare(ctx, req)
}

func (r *FunctionRunner) ExecuteRun(ctx context.Context, req InboundRunRequest, runID string) (InboundRunResult, error) {
	if r == nil || r.Execute == nil {
		return InboundRunResult{}, fmt.Errorf("%w: execute not configured", ErrUnsupported)
	}
	return r.Execute(ctx, req, runID)
}

func (r *FunctionRunner) InterruptRun(ctx context.Context, workspaceID, runID string) error {
	if r == nil || r.Interrupt == nil {
		return nil
	}
	return r.Interrupt(ctx, workspaceID, runID)
}

func (r *FunctionRunner) CancelRun(ctx context.Context, workspaceID, runID string) error {
	if r == nil || r.Cancel == nil {
		return nil
	}
	return r.Cancel(ctx, workspaceID, runID)
}

// DurableInboundRunner creates a durable agent_run in PrepareRun with frozen
// root snapshots, dispatches via Execute hook, and supports cancel propagation.
type DurableInboundRunner struct {
	Runs *execution.RunRepository
	// Freezer freezes target agent model/capability/agent/prompt/graph at Prepare.
	// Required for production immutability; when nil Prepare fails closed.
	Freezer SnapshotFreezer
	// Execute runs the agent after the run row exists and audit prewrite succeeded.
	// When nil, the run is marked SUCCEEDED with a placeholder assistant text.
	Execute func(ctx context.Context, req InboundRunRequest, runID string) (assistantText string, err error)
	// CancelHook interrupts in-process work for runID.
	CancelHook func(ctx context.Context, workspaceID, runID string) error
	// DefaultReply used when Execute is nil.
	DefaultReply string
}

func (r *DurableInboundRunner) PrepareRun(ctx context.Context, req InboundRunRequest) (string, error) {
	if r == nil || r.Runs == nil {
		return "", fmt.Errorf("%w: run repository required", ErrUnsupported)
	}
	if r.Freezer == nil {
		return "", fmt.Errorf("%w: inbound snapshot freezer required", ErrUnsupported)
	}
	runID := uuid.Must(uuid.NewV7()).String()
	return startInboundAgentRun(ctx, r.Runs, nil, req, runID, r.Freezer, InboundFreeze{})
}

// MaterializeFreeze freezes root agent identity without writing agent_runs.
// Call before ClaimInboundTaskWithPrepare so catalog reads do not share the claim TX
// (avoids MaxOpenConns=1 deadlock under the claim advisory lock).
func (r *DurableInboundRunner) MaterializeFreeze(ctx context.Context, req InboundRunRequest) (InboundFreeze, error) {
	if r == nil || r.Freezer == nil {
		return InboundFreeze{}, fmt.Errorf("%w: inbound snapshot freezer required", ErrUnsupported)
	}
	return materializeInboundFreeze(ctx, req, r.Freezer)
}

// PrepareRunInTx inserts the authority agent_run on the claim transaction using a
// pre-materialized freeze. Same-TX with inbound task + alias → no orphan on rollback.
func (r *DurableInboundRunner) PrepareRunInTx(ctx context.Context, tx *sql.Tx, req InboundRunRequest, freeze InboundFreeze) (string, error) {
	if r == nil || r.Runs == nil {
		return "", fmt.Errorf("%w: run repository required", ErrUnsupported)
	}
	if tx == nil {
		return "", fmt.Errorf("%w: claim transaction required", ErrInvalid)
	}
	runID := uuid.Must(uuid.NewV7()).String()
	return startInboundAgentRun(ctx, r.Runs, tx, req, runID, nil, freeze)
}

func (r *DurableInboundRunner) ExecuteRun(ctx context.Context, req InboundRunRequest, runID string) (InboundRunResult, error) {
	if r == nil || r.Runs == nil {
		return InboundRunResult{}, fmt.Errorf("%w: run repository required", ErrUnsupported)
	}
	assistant := strings.TrimSpace(r.DefaultReply)
	var execErr error
	if r.Execute != nil {
		assistant, execErr = r.Execute(ctx, req, runID)
	}
	if assistant == "" && execErr == nil {
		assistant = "ok"
	}

	// Under lease fence the gateway applies FencedInboundTerminal (atomic
	// run+task+delegation). Do not race a separate agent_run transition here.
	if _, fenced := ExecutionFenceFrom(ctx); fenced {
		status := "RUNNING"
		if execErr != nil {
			status = "FAILED"
			if ctx.Err() == context.DeadlineExceeded {
				status = "TIMED_OUT"
			} else if ctx.Err() != nil {
				status = "CANCELLED"
			}
		}
		result := InboundRunResult{
			RunID: runID, AssistantText: assistant, Status: status,
		}
		if execErr != nil {
			result.ErrorCode = "A2A_INBOUND_EXECUTE_FAILED"
			result.ErrorMessage = truncate(execErr.Error(), 500)
			return result, execErr
		}
		// Report intended success; agent_run remains RUNNING until gateway TX.
		result.Status = "SUCCEEDED"
		return result, nil
	}

	run, gerr := r.Runs.GetAgentRun(ctx, req.WorkspaceID, runID)
	if gerr != nil {
		return InboundRunResult{RunID: runID, Status: "FAILED", ErrorCode: "A2A_INBOUND_LOAD_FAILED", ErrorMessage: gerr.Error()}, gerr
	}
	if run.Status == "RUNNING" || run.Status == "PENDING" {
		status := "SUCCEEDED"
		errCode, statusCode := "", ""
		if execErr != nil {
			status = "FAILED"
			errCode = "A2A_INBOUND_EXECUTE_FAILED"
			if ctx.Err() == context.DeadlineExceeded {
				status, errCode, statusCode = "TIMED_OUT", "", "A2A_INBOUND_TIMED_OUT"
			} else if ctx.Err() != nil {
				status, errCode, statusCode = "CANCELLED", "", "A2A_INBOUND_CANCELLED"
			} else {
				statusCode = errCode
			}
		}
		out, _ := json.Marshal(map[string]any{
			"source": "a2a.inbound", "assistantPreview": truncate(assistant, 500),
			"errorCode": statusCode,
		})
		if _, terr := r.Runs.TransitionAgentRun(context.WithoutCancel(ctx), req.WorkspaceID, runID, execution.RunTransition{
			ExpectedStatus: run.Status, ExpectedLockVersion: run.LockVersion,
			NewStatus: status, OutputSummary: out, ErrorCode: errCode,
		}); terr != nil {
			return InboundRunResult{
				RunID: runID, Status: "FAILED",
				ErrorCode: "A2A_INBOUND_TRANSITION_FAILED", ErrorMessage: terr.Error(),
			}, terr
		}
		var gerr2 error
		run, gerr2 = r.Runs.GetAgentRun(context.WithoutCancel(ctx), req.WorkspaceID, runID)
		if gerr2 != nil {
			return InboundRunResult{
				RunID: runID, Status: status,
				ErrorCode: "A2A_INBOUND_RELOAD_FAILED", ErrorMessage: gerr2.Error(),
			}, gerr2
		}
	}

	result := InboundRunResult{
		RunID: runID, AssistantText: assistant, Status: run.Status,
		ErrorCode: run.ErrorCode,
	}
	if execErr != nil && result.ErrorMessage == "" {
		result.ErrorMessage = truncate(execErr.Error(), 500)
	}
	if execErr != nil {
		return result, execErr
	}
	return result, nil
}

// InterruptRun cancels in-process work only (CancelHook). Never writes agent_runs.
// Used by CancelInbound so durable four-object terminal stays in AtomicInboundCancel.
func (r *DurableInboundRunner) InterruptRun(ctx context.Context, workspaceID, runID string) error {
	if r == nil || r.CancelHook == nil {
		return nil
	}
	return r.CancelHook(ctx, workspaceID, runID)
}

// CancelRun durable-cancels a standalone candidate agent_run (unused claim path).
// MUST NOT be used by CancelInbound — that path uses InterruptRun + AtomicInboundCancel.
func (r *DurableInboundRunner) CancelRun(ctx context.Context, workspaceID, runID string) error {
	var errs []error
	if r.CancelHook != nil {
		if herr := r.CancelHook(ctx, workspaceID, runID); herr != nil {
			errs = append(errs, fmt.Errorf("cancel hook: %w", herr))
		}
	}
	if r.Runs == nil {
		return errors.Join(errs...)
	}
	run, err := r.Runs.GetAgentRun(ctx, workspaceID, runID)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	if run.Status != "RUNNING" && run.Status != "PENDING" && run.Status != "WAITING_CONFIRMATION" {
		return errors.Join(errs...)
	}
	// CANCELLED must not set ErrorCode (validRunTransition rejects non-FAILED+code).
	if _, err := r.Runs.TransitionAgentRun(ctx, workspaceID, runID, execution.RunTransition{
		ExpectedStatus: run.Status, ExpectedLockVersion: run.LockVersion,
		NewStatus:     "CANCELLED",
		OutputSummary: json.RawMessage(`{"cancelled":true,"source":"a2a.inbound.candidate","code":"A2A_INBOUND_CANCELLED"}`),
	}); err != nil {
		errs = append(errs, fmt.Errorf("run transition: %w", err))
	}
	return errors.Join(errs...)
}

func materializeInboundFreeze(ctx context.Context, req InboundRunRequest, freezer SnapshotFreezer) (InboundFreeze, error) {
	var freeze InboundFreeze
	if freezer != nil {
		f, ferr := freezer.FreezeInbound(ctx, req.WorkspaceID, req.AgentID)
		if ferr != nil {
			return InboundFreeze{}, fmt.Errorf("freeze inbound agent snapshots: %w", ferr)
		}
		freeze = f
	} else {
		// Test/dev only: synthetic freeze without a full agent catalog.
		modelID := uuid.Must(uuid.NewV7()).String()
		freeze.ModelSnapshot, _ = json.Marshal(map[string]any{
			"id": modelID, "provider": "test", "apiBase": "https://example.test",
			"modelName": "static", "lockVersion": 1, "source": "a2a.static.prepare",
		})
		freeze.AgentSnapshot, _ = json.Marshal(map[string]any{
			"schemaVersion": "agent-binding.v1", "agentId": req.AgentID,
			"modelConfigId": modelID, "modelConfigLockVer": 1, "source": "a2a.static.prepare",
		})
		freeze.CapabilitySnapshot = json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
		freeze.ContextPolicy = json.RawMessage(`{"schemaVersion":"session-context.v1","mode":"LEGACY","source":"a2a.static"}`)
	}
	if len(freeze.ModelSnapshot) == 0 || string(freeze.ModelSnapshot) == "{}" || string(freeze.ModelSnapshot) == "null" {
		return InboundFreeze{}, fmt.Errorf("%w: inbound model snapshot freeze required", ErrUnsupported)
	}
	if len(freeze.AgentSnapshot) == 0 || string(freeze.AgentSnapshot) == "{}" {
		return InboundFreeze{}, fmt.Errorf("%w: inbound agent snapshot freeze required", ErrUnsupported)
	}
	if len(freeze.CapabilitySnapshot) == 0 {
		freeze.CapabilitySnapshot = json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
	}
	if len(freeze.ContextPolicy) == 0 {
		freeze.ContextPolicy = json.RawMessage(`{"schemaVersion":"session-context.v1","mode":"LEGACY","source":"a2a.inbound"}`)
	}
	return freeze, nil
}

// startInboundAgentRun writes the authority agent_run.
// When tx is non-nil, the insert uses that transaction (atomic claim path).
// When freezer is non-nil, freeze is materialized here; otherwise preFreeze is used.
func startInboundAgentRun(
	ctx context.Context,
	runs *execution.RunRepository,
	tx *sql.Tx,
	req InboundRunRequest,
	runID string,
	freezer SnapshotFreezer,
	preFreeze InboundFreeze,
) (string, error) {
	if runs == nil {
		return "", fmt.Errorf("runs required")
	}
	traceID := firstNonEmpty(req.TraceID, runID)
	input, _ := json.Marshal(map[string]any{
		"source": "a2a.inbound", "externalTaskId": req.ExternalTaskID,
		"externalContextId": req.ExternalContext, "externalMessageId": req.ExternalMessage,
		"requestPreview": truncate(req.UserText, 500),
	})
	authz, _ := json.Marshal(map[string]any{
		"source": "a2a.inbound", "actorType": req.ActorType, "actorId": req.ActorID,
	})

	var freeze InboundFreeze
	var err error
	if freezer != nil {
		freeze, err = materializeInboundFreeze(ctx, req, freezer)
		if err != nil {
			return "", err
		}
	} else {
		freeze = preFreeze
		if len(freeze.ModelSnapshot) == 0 || string(freeze.ModelSnapshot) == "{}" || string(freeze.ModelSnapshot) == "null" {
			// Last-resort synthetic freeze for callers that pass neither freezer nor material.
			freeze, err = materializeInboundFreeze(ctx, req, nil)
			if err != nil {
				return "", err
			}
		} else {
			if len(freeze.CapabilitySnapshot) == 0 {
				freeze.CapabilitySnapshot = json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
			}
			if len(freeze.ContextPolicy) == 0 {
				freeze.ContextPolicy = json.RawMessage(`{"schemaVersion":"session-context.v1","mode":"LEGACY","source":"a2a.inbound"}`)
			}
		}
	}

	startIn := execution.StartAgentRunInput{
		ID: runID, WorkspaceID: req.WorkspaceID, AgentID: req.AgentID,
		TriggerType:     "A2A_INBOUND",
		TriggeredByType: firstNonEmpty(req.ActorType, "SERVICE_PRINCIPAL"),
		TriggeredByID:   firstNonEmpty(req.ActorID, "a2a"),
		TraceID:         traceID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model:         freeze.ModelSnapshot,
			Capabilities:  freeze.CapabilitySnapshot,
			ContextPolicy: freeze.ContextPolicy,
			Agent:         freeze.AgentSnapshot,
		},
		AuthorizationSnapshot: authz,
		InputSummary:          input,
		AgentGraphSnapshot:    freeze.GraphSnapshot,
	}
	if tx != nil {
		_, err = runs.StartAgentRunInTransaction(ctx, tx, startIn)
	} else {
		_, err = runs.StartAgentRun(ctx, startIn)
	}
	if err != nil {
		return "", err
	}
	return runID, nil
}
