package aap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/protocolevent"
)

var (
	ErrRunCancelInvalid  = errors.New("AAP Run cancellation input is invalid")
	ErrRunNotCancellable = errors.New("AAP Run is not cancellable")
)

type RunCancellationLifecycle interface {
	TransitionAgentRun(
		context.Context,
		execution.ProtocolRunTransitionInput,
	) (execution.ProtocolRunLifecycleResult, error)
}

type RuntimeRunCanceller interface {
	CancelRun(workspaceID, runID string) error
}

type RunCancellationService struct {
	runs      RunStore
	lifecycle RunCancellationLifecycle
	runtime   RuntimeRunCanceller
	receipts  CommandReceiptLedger
}

func (service *RunCancellationService) ConfigureCommandReceipts(ledger CommandReceiptLedger) error {
	if service == nil || ledger == nil || service.receipts != nil {
		return ErrCommandReceiptInvalid
	}
	service.receipts = ledger
	return nil
}

func NewRunCancellationService(
	runs RunStore,
	lifecycle RunCancellationLifecycle,
	runtime RuntimeRunCanceller,
) (*RunCancellationService, error) {
	if runs == nil || lifecycle == nil || runtime == nil {
		return nil, ErrRunCancelInvalid
	}
	return &RunCancellationService{runs: runs, lifecycle: lifecycle, runtime: runtime}, nil
}

type CancelRunInput struct {
	Scope          ConversationScope
	RunID          string
	IdempotencyKey string
	Principal      agentaccessauth.AAPAccessTokenPrincipal
	Authorization  agentaccessauth.AAPAuthorizationDecision
}

type CancelRunResult struct {
	Run                execution.AgentRun
	CancelledEvent     protocolevent.ProtocolEvent
	Idempotent         bool
	RuntimeCancelError error
}

func (service *RunCancellationService) Cancel(
	ctx context.Context,
	input CancelRunInput,
) (CancelRunResult, error) {
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.RunID = strings.ToLower(strings.TrimSpace(input.RunID))
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	if service == nil || service.runs == nil || service.lifecycle == nil || service.runtime == nil ||
		ctx == nil || !validConversationScope(input.Scope) || !canonicalUUID(input.RunID) ||
		!canonicalUUID(input.IdempotencyKey) ||
		!validRunResourceAuthorization(input, agentaccessauth.ActionRunCancel, "run:cancel") {
		return CancelRunResult{}, ErrRunCancelInvalid
	}
	decisionPrincipal, err := cancelDecisionPrincipal(input)
	if err != nil {
		return CancelRunResult{}, ErrRunCancelInvalid
	}
	receiptKey := commandReceiptKey(
		input.Scope, input.Principal, input.Authorization, CommandRunCancel, input.IdempotencyKey,
	)
	requestHash, err := commandRequestHash(struct {
		RunID string `json:"runId"`
	}{RunID: input.RunID})
	if err != nil {
		return CancelRunResult{}, err
	}
	if err := observeCommand(ctx, service.receipts, receiptKey, requestHash); err != nil {
		return CancelRunResult{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		run, loadErr := service.runs.GetAgentRun(ctx, input.Scope.WorkspaceID, input.RunID)
		if loadErr != nil {
			return CancelRunResult{}, loadErr
		}
		if run.WorkspaceID != input.Scope.WorkspaceID || run.AgentID != input.Scope.AgentID ||
			!cancelPrincipalCanMutate(run.PrincipalSnapshot, decisionPrincipal,
				input.Authorization.Snapshot.OwnershipMode) {
			return CancelRunResult{}, ErrRunNotFound
		}
		switch strings.ToUpper(strings.TrimSpace(run.Status)) {
		case "CANCELLED":
			if err := completeCommand(ctx, service.receipts, receiptKey, requestHash,
				"RUN", run.ID, run.LockVersion); err != nil {
				return CancelRunResult{}, err
			}
			return CancelRunResult{Run: run, Idempotent: true}, nil
		case "SUCCEEDED", "FAILED", "PENDING", "WAITING_CONFIRMATION":
			return CancelRunResult{}, ErrRunNotCancellable
		case "RUNNING":
		default:
			return CancelRunResult{}, ErrRunCancelInvalid
		}
		transitioned, transitionErr := service.lifecycle.TransitionAgentRun(
			ctx,
			execution.ProtocolRunTransitionInput{
				WorkspaceID: input.Scope.WorkspaceID, RunID: input.RunID,
				Transition: execution.RunTransition{
					ExpectedStatus: run.Status, ExpectedLockVersion: run.LockVersion,
					NewStatus: "CANCELLED", OutputSummary: cancelRunSummary(input.IdempotencyKey),
				},
			},
		)
		if errors.Is(transitionErr, execution.ErrRunConflict) {
			continue
		}
		if transitionErr != nil {
			return CancelRunResult{}, transitionErr
		}
		if transitioned.Run.Status != "CANCELLED" || len(transitioned.Events) != 1 ||
			transitioned.Events[0].Type != protocolevent.EventRunCancelled {
			return CancelRunResult{}, ErrRunCancelInvalid
		}
		cancelErr := service.runtime.CancelRun(input.Scope.WorkspaceID, input.RunID)
		if err := completeCommand(ctx, service.receipts, receiptKey, requestHash,
			"RUN", transitioned.Run.ID, transitioned.Run.LockVersion); err != nil {
			return CancelRunResult{}, err
		}
		return CancelRunResult{
			Run: transitioned.Run, CancelledEvent: transitioned.Events[0],
			RuntimeCancelError: cancelErr,
		}, nil
	}
	latest, err := service.runs.GetAgentRun(ctx, input.Scope.WorkspaceID, input.RunID)
	if err == nil && latest.Status == "CANCELLED" {
		if receiptErr := completeCommand(ctx, service.receipts, receiptKey, requestHash,
			"RUN", latest.ID, latest.LockVersion); receiptErr != nil {
			return CancelRunResult{}, receiptErr
		}
		return CancelRunResult{Run: latest, Idempotent: true}, nil
	}
	if err == nil && (latest.Status == "SUCCEEDED" || latest.Status == "FAILED") {
		return CancelRunResult{}, ErrRunNotCancellable
	}
	return CancelRunResult{}, execution.ErrRunConflict
}

func validRunResourceAuthorization(
	input CancelRunInput,
	action agentaccessauth.AAPAction,
	requiredScope string,
) bool {
	snapshot := input.Authorization.Snapshot
	return snapshot.SpecVersion == "aap.authorization.v1" && snapshot.Action == action &&
		snapshot.RequiredScope == requiredScope && snapshot.WorkspaceID == input.Scope.WorkspaceID &&
		snapshot.AgentID == input.Scope.AgentID && snapshot.ServicePrincipalID == input.Principal.ServicePrincipalID &&
		snapshot.SubjectID == input.Principal.PrincipalID && snapshot.AuthorizedParty == input.Principal.AuthorizedParty &&
		snapshot.TokenID == input.Principal.TokenID && snapshot.ClientID != "" && snapshot.GrantID != "" &&
		snapshot.TokenSecurityVersion == input.Principal.SecurityVersion &&
		snapshot.GrantVersion > 0 && snapshot.AgentPolicyVersion > 0 &&
		snapshot.ResourceType == agentaccessauth.ResourceRun && snapshot.ResourceID == input.RunID &&
		(snapshot.OwnershipMode == "SUBJECT_OWNED" || snapshot.OwnershipMode == "POLICY_SHARED") &&
		containsScope(input.Authorization.EffectiveScopes, requiredScope) &&
		containsScope(snapshot.EffectiveScopes, requiredScope)
}

func cancelDecisionPrincipal(input CancelRunInput) (principal.ExecutionSnapshot, error) {
	identity, err := invocationIdentity(input.Scope.WorkspaceID, input.Principal)
	if err != nil {
		return principal.ExecutionSnapshot{}, err
	}
	return input.Authorization.Snapshot.ExecutionPrincipalSnapshot(identity)
}

func cancelPrincipalCanMutate(
	owner principal.ExecutionSnapshot,
	decision principal.ExecutionSnapshot,
	ownershipMode string,
) bool {
	if owner.Validate() != nil || decision.Validate() != nil {
		return false
	}
	if ownershipMode == "SUBJECT_OWNED" {
		return owner.SameDecisionPrincipal(decision)
	}
	return ownershipMode == "POLICY_SHARED" &&
		owner.Identity.Actor == decision.Identity.Actor && owner.ClientID == decision.ClientID &&
		owner.GrantID == decision.GrantID
}

func cancelRunSummary(idempotencyKey string) json.RawMessage {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(idempotencyKey))))
	value, _ := json.Marshal(map[string]string{
		"schemaVersion":        "aap.run-cancel.v1",
		"idempotencyKeySha256": hex.EncodeToString(digest[:]),
		"reason":               "client_cancelled",
	})
	return value
}
