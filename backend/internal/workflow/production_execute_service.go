package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/workflowruntime"

	"github.com/google/uuid"
)

// Production execute errors (Console additive API; D4/D11).
var (
	ErrRevisionNotActive     = errors.New("workflow revision is not the active published revision")
	ErrRevisionNotExecutable = errors.New("workflow revision is not executable")
	ErrIdempotencyConflict   = errors.New("workflow production execute idempotency conflict")
)

const (
	ProductionTriggerConsole = "console"
	ProductionTriggerAPI     = "api"

	ProductionStatusPending             = "PENDING"
	ProductionStatusRunning             = "RUNNING"
	ProductionStatusWaitingConfirmation = "WAITING_CONFIRMATION"
	ProductionStatusSucceeded           = "SUCCEEDED"
	ProductionStatusFailed              = "FAILED"
	ProductionStatusCancelled           = "CANCELLED"
)

// ProductionExecuteInput is the Console production :execute command (D4/D11).
type ProductionExecuteInput struct {
	WorkspaceID    string
	WorkflowID     string
	RevisionID     string
	ActorID        string
	TraceID        string
	Trigger        string
	Input          json.RawMessage
	IdempotencyKey string
}

// ProductionExecuteResult is the 202 Accepted body.
type ProductionExecuteResult struct {
	ExecutionID string
	WorkflowID  string
	RevisionID  string
	Status      string
	TraceID     string
	// ConfirmationID is set when the run paused on Approval and a durable
	// execution_confirmations + resume checkpoint was prepared.
	ConfirmationID string
	// ResumeToken is returned once at pause time so Console/API can confirm.
	// Never re-read from storage; empty on idempotent replay and non-waiting results.
	ResumeToken string `json:"-"`
	// ConfirmationLockVersion is the initial lock version of the pending confirmation.
	ConfirmationLockVersion int64
}

// ProductionExecutionRecord is the durable start + transition surface used by
// production execute (execution.RunService / RunRepository).
type ProductionExecutionRecord interface {
	StartWorkflowExecution(context.Context, execution.StartWorkflowExecutionRequest) (execution.WorkflowExecution, error)
	TransitionWorkflowExecution(context.Context, string, string, execution.RunTransition) (execution.WorkflowExecution, error)
	GetWorkflowExecution(context.Context, string, string) (execution.WorkflowExecution, error)
}

// ProductionExecutionPreparer authorizes and canonicalizes a workflow start
// without writing it. Production adapters use the prepared input in the same
// transaction as the durable idempotency claim.
type ProductionExecutionPreparer interface {
	PrepareWorkflowExecution(
		context.Context,
		execution.StartWorkflowExecutionRequest,
	) (execution.StartWorkflowExecutionInput, error)
}

// ProductionPlanRunner executes an immutable published revision plan.
type ProductionPlanRunner interface {
	Run(ctx context.Context, request ProductionPlanRunRequest) (ProductionPlanRunResult, error)
}

// ProductionPlanRunRequest is the runtime call for a production revision plan.
type ProductionPlanRunRequest struct {
	ExecutionID string
	WorkspaceID string
	WorkflowID  string
	RevisionID  string
	PlanHash    string
	Plan        domain.CompiledExecutionPlan
	Input       map[string]any
	StartedBy   string
	Trigger     string
	// PrincipalSnapshot is the immutable production actor/subject (USER for console).
	// Production never accepts independent outboundCredentials — inherit root only.
	PrincipalSnapshot *principal.ExecutionSnapshot
	ActorType         string
	// AgentRunID when nested under an AgentRun keeps the top-level Vault root.
	AgentRunID string
}

// ProductionPlanRunResult is the normalized terminal (or waiting) outcome.
type ProductionPlanRunResult struct {
	Status        string
	OutputSummary json.RawMessage
	ErrorCode     string
	// Approval is non-nil when the plan paused on an Approval node (HITL).
	Approval *workflowruntime.WorkflowApprovalCheckpoint
}

// ProductionConfirmationPreparer creates durable confirmation + resume checkpoint
// when production execute pauses on Approval.
type ProductionConfirmationPreparer interface {
	Prepare(context.Context, execution.PrepareConfirmationResumeInput) (execution.PreparedConfirmationResume, error)
}

// ProductionIdempotencyStore prevents double-run for the same Idempotency-Key.
// Claim is atomic: first writer wins; same-hash retries return the existing executionID.
type ProductionIdempotencyStore interface {
	Claim(
		ctx context.Context,
		workspaceID, actorID, key, requestHash, newExecutionID string,
	) (executionID string, created bool, err error)
}

// ProductionIdempotencyExecutionStore atomically claims an idempotency key and
// creates its durable execution row. Implementations prevent another replica
// from observing a claim before the execution exists.
type ProductionIdempotencyExecutionStore interface {
	ClaimExecution(
		context.Context,
		string, string, string, string,
		execution.StartWorkflowExecutionInput,
	) (execution.WorkflowExecution, bool, error)
}

// MemoryProductionIdempotencyStore is process-local (sufficient for unit tests and
// single-instance Console MVP; multi-instance durable store can replace later).
type MemoryProductionIdempotencyStore struct {
	mu    sync.Mutex
	byKey map[string]memoryIdempotencyEntry
}

type memoryIdempotencyEntry struct {
	requestHash string
	executionID string
}

func NewMemoryProductionIdempotencyStore() *MemoryProductionIdempotencyStore {
	return &MemoryProductionIdempotencyStore{byKey: map[string]memoryIdempotencyEntry{}}
}

func (s *MemoryProductionIdempotencyStore) Claim(
	_ context.Context,
	workspaceID, actorID, key, requestHash, newExecutionID string,
) (string, bool, error) {
	if s == nil {
		return newExecutionID, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mapKey := memoryIdempotencyKey(workspaceID, actorID, key)
	if existing, ok := s.byKey[mapKey]; ok {
		if existing.requestHash != requestHash {
			return "", false, ErrIdempotencyConflict
		}
		return existing.executionID, false, nil
	}
	s.byKey[mapKey] = memoryIdempotencyEntry{requestHash: requestHash, executionID: newExecutionID}
	return newExecutionID, true, nil
}

func memoryIdempotencyKey(workspaceID, actorID, key string) string {
	return strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(actorID) + "\x00" + strings.TrimSpace(key)
}

// ProductionExecuteService starts production runs against the active published revision.
type ProductionExecuteService struct {
	repository    *Repository
	executions    ProductionExecutionRecord
	runner        ProductionPlanRunner
	idem          ProductionIdempotencyStore
	confirmations ProductionConfirmationPreparer
	// requirements re-validates published plan descriptors (policy drift fail-closed).
	requirements *OutboundRequirementsLoader
	// cleaner clears process-local vault/cache/affinity on terminal outcomes.
	cleaner *execution.RootOutboundLifecycle
	bootID  string
}

func NewProductionExecuteService(
	repository *Repository,
	executions ProductionExecutionRecord,
	runner ProductionPlanRunner,
	idem ProductionIdempotencyStore,
) (*ProductionExecuteService, error) {
	if repository == nil || executions == nil || runner == nil {
		return nil, errors.New("workflow production execute dependencies are required")
	}
	if idem == nil {
		idem = NewMemoryProductionIdempotencyStore()
	}
	return &ProductionExecuteService{
		repository: repository, executions: executions, runner: runner, idem: idem,
	}, nil
}

// ConfigureConfirmationResume wires durable Approval HITL prepare (optional; tests may omit).
func (s *ProductionExecuteService) ConfigureConfirmationResume(preparer ProductionConfirmationPreparer) error {
	if s == nil {
		return errors.New("production execute service is required")
	}
	s.confirmations = preparer
	return nil
}

// ConfigureOutbound wires published requirements recheck + terminal cleanup (#10).
func (s *ProductionExecuteService) ConfigureOutbound(
	loader *OutboundRequirementsLoader,
	cleaner *execution.RootOutboundLifecycle,
	bootID string,
) error {
	if s == nil {
		return errors.New("production execute service is required")
	}
	s.requirements = loader
	s.cleaner = cleaner
	s.bootID = strings.TrimSpace(bootID)
	return nil
}

// Execute validates active revision, starts a WorkflowExecution, runs the compiled
// plan via workflowruntime/Eino, and returns 202-shaped result. Sync completion is
// used so list/detail/events can observe a terminal status without a worker.
func (s *ProductionExecuteService) Execute(
	ctx context.Context,
	input ProductionExecuteInput,
) (ProductionExecuteResult, error) {
	input, requestHash, err := normalizeProductionExecuteInput(input)
	if err != nil {
		return ProductionExecuteResult{}, err
	}

	workflowValue, err := s.repository.Get(ctx, input.WorkspaceID, input.WorkflowID)
	if err != nil {
		return ProductionExecuteResult{}, err
	}
	if workflowValue.ActiveRevisionID == nil ||
		strings.TrimSpace(*workflowValue.ActiveRevisionID) != input.RevisionID {
		return ProductionExecuteResult{}, ErrRevisionNotActive
	}
	revision, err := s.repository.GetRevision(ctx, input.WorkspaceID, input.WorkflowID, input.RevisionID)
	if err != nil {
		return ProductionExecuteResult{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(revision.Status), "PUBLISHED") ||
		revision.RetiredAt != nil || len(revision.PlanSnapshot) == 0 {
		return ProductionExecuteResult{}, ErrRevisionNotExecutable
	}
	var plan domain.CompiledExecutionPlan
	if err := json.Unmarshal(revision.PlanSnapshot, &plan); err != nil {
		return ProductionExecuteResult{}, fmt.Errorf("decode published revision plan: %w", err)
	}
	// Policy drift fail-closed: re-check published outbound requirements vs live Connection.
	if s.requirements != nil {
		if err := s.requirements.ValidatePublishedRequirements(
			ctx, input.WorkspaceID, revision.PlanSnapshot,
		); err != nil {
			return ProductionExecuteResult{}, err
		}
	}
	var inputObject map[string]any
	if err := json.Unmarshal(input.Input, &inputObject); err != nil {
		return ProductionExecuteResult{}, ErrInvalid
	}
	inputSummary, err := json.Marshal(map[string]any{
		"trigger": input.Trigger, "input": inputObject, "requestHash": requestHash,
	})
	if err != nil {
		return ProductionExecuteResult{}, ErrInvalid
	}

	executionID, err := uuid.NewV7()
	if err != nil {
		return ProductionExecuteResult{}, fmt.Errorf("create workflow production execution id: %w", err)
	}
	var started execution.WorkflowExecution
	startedByIdempotency := false
	if input.IdempotencyKey != "" {
		if atomicStore, ok := s.idem.(ProductionIdempotencyExecutionStore); ok {
			preparer, ok := s.executions.(ProductionExecutionPreparer)
			if !ok {
				return ProductionExecuteResult{}, errors.New("workflow production execution preparer is required")
			}
			prepared, prepareErr := preparer.PrepareWorkflowExecution(
				ctx, execution.StartWorkflowExecutionRequest{
					ID: executionID.String(), WorkspaceID: input.WorkspaceID,
					WorkflowID: input.WorkflowID, RevisionID: input.RevisionID,
					TriggerType: productionTriggerType(input.Trigger), TriggeredByType: "USER",
					TriggeredByID: input.ActorID, TraceID: input.TraceID, InputSummary: inputSummary,
				},
			)
			if prepareErr != nil {
				return ProductionExecuteResult{}, prepareErr
			}
			started, startedByIdempotency, err = atomicStore.ClaimExecution(
				ctx, input.WorkspaceID, input.ActorID, input.IdempotencyKey, requestHash,
				prepared,
			)
			if err != nil {
				return ProductionExecuteResult{}, err
			}
			if !startedByIdempotency {
				return productionResultFromExecution(started), nil
			}
			executionID, err = uuid.Parse(started.ID)
			if err != nil {
				return ProductionExecuteResult{}, ErrInvalid
			}
		} else {
			claimedID, created, claimErr := s.idem.Claim(
				ctx, input.WorkspaceID, input.ActorID, input.IdempotencyKey, requestHash, executionID.String(),
			)
			if claimErr != nil {
				return ProductionExecuteResult{}, claimErr
			}
			if !created {
				existing, getErr := s.executions.GetWorkflowExecution(ctx, input.WorkspaceID, claimedID)
				if getErr == nil {
					return productionResultFromExecution(existing), nil
				}
				if !errors.Is(getErr, execution.ErrRunNotFound) {
					return ProductionExecuteResult{}, getErr
				}
				// Memory/test stores can still expose a claim-before-start window.
				// Production uses the atomic PostgreSQL implementation above.
			}
			executionID, err = uuid.Parse(claimedID)
			if err != nil {
				return ProductionExecuteResult{}, ErrInvalid
			}
		}
	}

	if !startedByIdempotency {
		started, err = s.executions.StartWorkflowExecution(ctx, execution.StartWorkflowExecutionRequest{
			ID: executionID.String(), WorkspaceID: input.WorkspaceID, WorkflowID: input.WorkflowID,
			RevisionID: input.RevisionID, TriggerType: productionTriggerType(input.Trigger),
			TriggeredByType: "USER", TriggeredByID: input.ActorID, TraceID: input.TraceID,
			InputSummary: inputSummary,
		})
		if err != nil {
			if input.IdempotencyKey != "" && errors.Is(err, execution.ErrRunConflict) {
				if existing, getErr := s.executions.GetWorkflowExecution(ctx, input.WorkspaceID, executionID.String()); getErr == nil {
					return productionResultFromExecution(existing), nil
				}
			}
			return ProductionExecuteResult{}, err
		}
	}

	// Console production actor is USER subject (no independent Token envelope).
	snapshot, snapErr := principal.NewInternalExecutionSnapshot(
		input.WorkspaceID, principal.TypeUser, input.ActorID,
	)
	if snapErr != nil {
		return ProductionExecuteResult{}, ErrInvalid
	}

	// Terminal cleanup for production root (workflow execution scope).
	// HITL waiting is not terminal — defer only clears on real terminal statuses.
	defer func() {
		// Inspected after return via named result is awkward; cleanup below on terminal paths.
	}()

	runResult, runErr := s.runner.Run(ctx, ProductionPlanRunRequest{
		ExecutionID: started.ID, WorkspaceID: input.WorkspaceID, WorkflowID: input.WorkflowID,
		RevisionID: input.RevisionID, PlanHash: revision.PlanHash, Plan: plan,
		Input: inputObject, StartedBy: input.ActorID, Trigger: input.Trigger,
		PrincipalSnapshot: &snapshot, ActorType: "USER",
	})
	// Approval HITL: prepare durable confirmation + resume checkpoint. Prepare
	// itself transitions the workflow execution RUNNING → WAITING_CONFIRMATION.
	// Confirmation wait must NOT acquire credentials (pipeline already gates that).
	if runErr == nil &&
		strings.EqualFold(strings.TrimSpace(runResult.Status), ProductionStatusWaitingConfirmation) &&
		runResult.Approval != nil && s.confirmations != nil {
		prepared, prepareErr := s.prepareApprovalConfirmation(
			context.WithoutCancel(ctx), input, started, revision, inputObject, runResult.Approval,
		)
		if prepareErr != nil {
			// Fall back to status-only WAITING so callers still see the pause;
			// confirmation gap remains observable when prepare fails.
			_, _ = s.executions.TransitionWorkflowExecution(
				context.WithoutCancel(ctx), input.WorkspaceID, started.ID, execution.RunTransition{
					ExpectedStatus: "RUNNING", ExpectedLockVersion: started.LockVersion,
					NewStatus: ProductionStatusWaitingConfirmation,
					OutputSummary: json.RawMessage(
						`{"outcome":"waiting_confirmation","prepareError":true}`,
					),
				},
			)
			return ProductionExecuteResult{}, prepareErr
		}
		waiting, getErr := s.executions.GetWorkflowExecution(ctx, input.WorkspaceID, started.ID)
		if getErr != nil {
			return ProductionExecuteResult{}, getErr
		}
		result := productionResultFromExecution(waiting)
		result.ConfirmationID = prepared.Requested.Confirmation.ID
		result.ResumeToken = prepared.Requested.ResumeToken
		result.ConfirmationLockVersion = prepared.Requested.Confirmation.LockVersion
		// Do NOT cleanup vault/cache while WAITING_CONFIRMATION — resume may continue.
		return result, nil
	}
	status, errorCode, output := normalizeProductionRunResult(ctx, runResult, runErr)
	transitioned, transitionErr := s.executions.TransitionWorkflowExecution(
		context.WithoutCancel(ctx), input.WorkspaceID, started.ID, execution.RunTransition{
			ExpectedStatus: "RUNNING", ExpectedLockVersion: started.LockVersion,
			NewStatus: status, OutputSummary: output, ErrorCode: errorCode,
		},
	)
	if transitionErr != nil {
		return ProductionExecuteResult{}, transitionErr
	}
	// Terminal: clear process-local outbound state for this workflow execution root.
	if s.cleaner != nil && isProductionTerminal(status) {
		s.cleaner.CleanupRoot(context.WithoutCancel(ctx), execution.CleanupRootInput{
			BootID: s.bootID, WorkspaceID: input.WorkspaceID,
			SubjectType: outboundidentity.SubjectTypeUser, SubjectID: input.ActorID,
			RootScopeType: outboundidentity.RootScopeWorkflowExecution,
			RootScopeID:   started.ID,
			ClearAffinity: true,
		})
	}
	// Always return the durable execution identity; run failures are reflected in status.
	_ = runErr
	return productionResultFromExecution(transitioned), nil
}

func isProductionTerminal(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case ProductionStatusSucceeded, ProductionStatusFailed, ProductionStatusCancelled:
		return true
	default:
		return false
	}
}

func normalizeProductionExecuteInput(input ProductionExecuteInput) (ProductionExecuteInput, string, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.WorkflowID = strings.TrimSpace(input.WorkflowID)
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.TraceID = strings.TrimSpace(input.TraceID)
	input.Trigger = strings.ToLower(strings.TrimSpace(input.Trigger))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.Trigger == "" {
		input.Trigger = ProductionTriggerConsole
	}
	if input.Trigger != ProductionTriggerConsole && input.Trigger != ProductionTriggerAPI {
		return ProductionExecuteInput{}, "", ErrInvalid
	}
	if !validUUID(input.WorkspaceID) || !validUUID(input.WorkflowID) ||
		!validUUID(input.RevisionID) || !validUUID(input.ActorID) {
		return ProductionExecuteInput{}, "", ErrInvalid
	}
	if input.TraceID == "" {
		input.TraceID = "workflow-production/" + input.RevisionID
	}
	if len(input.TraceID) > 512 {
		return ProductionExecuteInput{}, "", ErrInvalid
	}
	if input.IdempotencyKey != "" && len(input.IdempotencyKey) > 255 {
		return ProductionExecuteInput{}, "", ErrInvalid
	}
	canonicalInput, _, err := canonicalJSON(input.Input, "object")
	if err != nil {
		// empty body → empty object
		if len(strings.TrimSpace(string(input.Input))) == 0 {
			canonicalInput = json.RawMessage(`{}`)
		} else {
			return ProductionExecuteInput{}, "", ErrInvalid
		}
	}
	input.Input = canonicalInput
	sum := sha256.Sum256([]byte(
		input.WorkspaceID + "\x00" + input.WorkflowID + "\x00" + input.RevisionID +
			"\x00" + input.Trigger + "\x00" + string(canonicalInput),
	))
	return input, hex.EncodeToString(sum[:]), nil
}

func productionTriggerType(trigger string) string {
	switch trigger {
	case ProductionTriggerAPI:
		return "API"
	default:
		return "CONSOLE"
	}
}

func productionResultFromExecution(value execution.WorkflowExecution) ProductionExecuteResult {
	return ProductionExecuteResult{
		ExecutionID: value.ID, WorkflowID: value.WorkflowID, RevisionID: value.RevisionID,
		Status: value.Status, TraceID: value.TraceID,
	}
}

func (s *ProductionExecuteService) prepareApprovalConfirmation(
	ctx context.Context,
	input ProductionExecuteInput,
	started execution.WorkflowExecution,
	revision Revision,
	workflowInput map[string]any,
	approval *workflowruntime.WorkflowApprovalCheckpoint,
) (execution.PreparedConfirmationResume, error) {
	if s.confirmations == nil || approval == nil {
		return execution.PreparedConfirmationResume{}, errors.New("approval confirmation preparer is required")
	}
	releaseID, err := s.repository.GetReleaseIDForRevision(
		ctx, input.WorkspaceID, input.WorkflowID, input.RevisionID,
	)
	if err != nil {
		return execution.PreparedConfirmationResume{}, fmt.Errorf("resolve capability release for approval: %w", err)
	}
	inputJSON, err := json.Marshal(workflowInput)
	if err != nil {
		return execution.PreparedConfirmationResume{}, err
	}
	decision, err := execution.EvaluateConfirmationPolicy(execution.ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: execution.ConfirmationReleaseRisk{
			ReleaseID: releaseID, RiskLevel: "HIGH", SideEffectLevel: "WRITE",
			RequiresConfirmation: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Connection: execution.ConfirmationConnectionRisk{},
		Input:      inputJSON,
	})
	if err != nil {
		return execution.PreparedConfirmationResume{}, err
	}
	if !decision.RequiresConfirmation {
		decision.RequiresConfirmation = true
		decision.RiskReasons = []string{execution.ConfirmationReasonReleaseRequired}
	}
	snapshot, err := principal.NewInternalExecutionSnapshot(
		input.WorkspaceID, principal.TypeUser, input.ActorID,
	)
	if err != nil {
		return execution.PreparedConfirmationResume{}, err
	}
	publishedRequest := workflowruntime.PublishedRunRequest{
		WorkspaceID: input.WorkspaceID, CapabilityID: input.WorkflowID, ReleaseID: releaseID,
		ActorID: input.ActorID, ActorType: "USER", PrincipalSnapshot: &snapshot,
		WorkflowExecutionID: started.ID, Input: workflowInput,
	}
	revisionSnapshot := workflowruntime.RevisionSnapshot{
		WorkspaceID: input.WorkspaceID, CapabilityID: input.WorkflowID, ReleaseID: releaseID,
		RevisionID: input.RevisionID, PlanHash: revision.PlanHash, Plan: approval.Plan,
	}
	// Ensure checkpoint carries durable execution identity for resume.
	approvalCopy := *approval
	if strings.TrimSpace(approvalCopy.ExecutionID) == "" {
		approvalCopy.ExecutionID = started.ID
	}
	if strings.TrimSpace(approvalCopy.WorkflowID) == "" {
		approvalCopy.WorkflowID = input.WorkflowID
	}
	if strings.TrimSpace(approvalCopy.RequestedBy) == "" {
		approvalCopy.RequestedBy = input.ActorID
	}
	requestSnapshot, resolvedSnapshot, err := workflowruntime.BuildWorkflowConfirmationResumeSnapshotsWithApproval(
		publishedRequest, revisionSnapshot, "", &approvalCopy,
	)
	if err != nil {
		return execution.PreparedConfirmationResume{}, err
	}
	confirmationID, err := uuid.NewV7()
	if err != nil {
		return execution.PreparedConfirmationResume{}, err
	}
	targetItemID, err := uuid.NewV7()
	if err != nil {
		return execution.PreparedConfirmationResume{}, err
	}
	nodeID := strings.TrimSpace(approvalCopy.NodeID)
	if nodeID == "" {
		nodeID = "approval"
	}
	return s.confirmations.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: confirmationID.String(), WorkspaceID: input.WorkspaceID,
			ExecutionID: started.ID, TargetItemID: targetItemID.String(),
			NodeID: nodeID, ReleaseID: releaseID, PlanHash: revision.PlanHash,
			RequestedBy: input.ActorID, PrincipalSnapshot: &snapshot, Decision: decision,
		},
		Kind:                         execution.ResumeKindWorkflow,
		SnapshotSchemaVersion:        execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot:              requestSnapshot,
		ResolvedSnapshot:             resolvedSnapshot,
		Input:                        inputJSON,
		ExpectedExecutionLockVersion: started.LockVersion,
		TerminalOnSuccess:            true,
	})
}

func normalizeProductionRunResult(
	ctx context.Context,
	result ProductionPlanRunResult,
	runErr error,
) (status, errorCode string, output json.RawMessage) {
	if ctx.Err() != nil {
		return ProductionStatusCancelled, "", json.RawMessage(`{"outcome":"cancelled"}`)
	}
	status = strings.ToUpper(strings.TrimSpace(result.Status))
	switch status {
	case ProductionStatusSucceeded:
		output = result.OutputSummary
		if len(output) == 0 {
			output = json.RawMessage(`{"outcome":"succeeded"}`)
		}
		return ProductionStatusSucceeded, "", output
	case ProductionStatusWaitingConfirmation:
		output = result.OutputSummary
		if len(output) == 0 {
			output = json.RawMessage(`{"outcome":"waiting_confirmation"}`)
		}
		return ProductionStatusWaitingConfirmation, "", output
	case ProductionStatusCancelled:
		return ProductionStatusCancelled, "", json.RawMessage(`{"outcome":"cancelled"}`)
	default:
		errorCode = strings.TrimSpace(result.ErrorCode)
		if errorCode == "" {
			errorCode = "WORKFLOW_PRODUCTION_FAILED"
		}
		output = result.OutputSummary
		if len(output) == 0 {
			if runErr != nil {
				output = json.RawMessage(`{"outcome":"failed"}`)
			} else {
				output = json.RawMessage(`{"outcome":"failed"}`)
			}
		}
		return ProductionStatusFailed, errorCode, output
	}
}

// RuntimeProductionPlanRunner runs compiled plans via workflowruntime / Eino.
type RuntimeProductionPlanRunner struct{ runner CompiledPlanRunner }

func NewRuntimeProductionPlanRunner(runner CompiledPlanRunner) (*RuntimeProductionPlanRunner, error) {
	if runner == nil {
		return nil, errors.New("compiled workflow plan runner is required")
	}
	return &RuntimeProductionPlanRunner{runner: runner}, nil
}

// checkpointCapableRunner is optional; when present, production execute keeps
// WorkflowApprovalCheckpoint for durable HITL prepare.
type checkpointCapableRunner interface {
	RunWithCheckpoint(
		plan domain.CompiledExecutionPlan,
		ctx workflowruntime.ExecutionContext,
	) (domain.Execution, *workflowruntime.WorkflowApprovalCheckpoint, error)
}

func (r *RuntimeProductionPlanRunner) Run(
	ctx context.Context,
	request ProductionPlanRunRequest,
) (ProductionPlanRunResult, error) {
	if err := ctx.Err(); err != nil {
		return ProductionPlanRunResult{Status: ProductionStatusCancelled}, err
	}
	trigger := "Workflow Production (" + request.Trigger + ")"
	execCtx := workflowruntime.ExecutionContext{
		UserID: request.StartedBy, Input: request.Input,
		WorkflowVersion: request.RevisionID, WorkspaceID: request.WorkspaceID,
		Trigger: trigger, WorkflowExecutionID: request.ExecutionID,
	}
	var (
		executionValue domain.Execution
		approval       *workflowruntime.WorkflowApprovalCheckpoint
		err            error
	)
	if checkpointRunner, ok := r.runner.(checkpointCapableRunner); ok {
		executionValue, approval, err = checkpointRunner.RunWithCheckpoint(request.Plan, execCtx)
	} else {
		executionValue, err = r.runner.Run(request.Plan, execCtx)
	}
	if ctx.Err() != nil {
		return ProductionPlanRunResult{Status: ProductionStatusCancelled}, ctx.Err()
	}
	switch executionValue.Status {
	case domain.ExecutionSuccess:
		summary, _ := json.Marshal(map[string]any{
			"outcome": "succeeded", "outputSummary": executionValue.OutputSummary,
		})
		return ProductionPlanRunResult{Status: ProductionStatusSucceeded, OutputSummary: summary}, err
	case domain.ExecutionApproval:
		return ProductionPlanRunResult{
			Status:        ProductionStatusWaitingConfirmation,
			OutputSummary: json.RawMessage(`{"outcome":"waiting_confirmation"}`),
			Approval:      approval,
		}, err
	case domain.ExecutionRunning:
		// Unexpected sticky running from plan runner → treat as failure for sync API.
		return ProductionPlanRunResult{
			Status: ProductionStatusFailed, ErrorCode: "WORKFLOW_PRODUCTION_STUCK",
			OutputSummary: json.RawMessage(`{"outcome":"failed","reason":"stuck_running"}`),
		}, err
	default:
		return ProductionPlanRunResult{
			Status: ProductionStatusFailed, ErrorCode: "WORKFLOW_PRODUCTION_FAILED",
			OutputSummary: json.RawMessage(`{"outcome":"failed"}`),
		}, err
	}
}
