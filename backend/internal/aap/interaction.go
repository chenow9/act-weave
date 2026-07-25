package aap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

var (
	ErrInteractionDecisionInvalid = errors.New("AAP Interaction decision input is invalid")
	ErrInteractionNotFound        = errors.New("AAP Interaction was not found")
)

type InteractionItemStore interface {
	Get(context.Context, string, string, string, string) (protocolevent.RunItemProjection, error)
}

type ProtocolInteractionDecider interface {
	Decide(context.Context, execution.ProtocolInteractionDecisionInput) (execution.ProtocolInteractionDecisionResult, error)
}

type InteractionDecisionContinuation interface {
	ContinueApprovedInteraction(context.Context, execution.InteractionDecisionResult) error
}

type InteractionDecisionService struct {
	runs         RunStore
	items        InteractionItemStore
	decisions    ProtocolInteractionDecider
	receipts     CommandReceiptLedger
	continuation InteractionDecisionContinuation
}

func (service *InteractionDecisionService) ConfigureCommandReceipts(ledger CommandReceiptLedger) error {
	if service == nil || ledger == nil || service.receipts != nil {
		return ErrCommandReceiptInvalid
	}
	service.receipts = ledger
	return nil
}

func (service *InteractionDecisionService) ConfigureContinuation(
	continuation InteractionDecisionContinuation,
) error {
	if service == nil || continuation == nil || service.continuation != nil {
		return ErrInteractionDecisionInvalid
	}
	service.continuation = continuation
	return nil
}

func NewInteractionDecisionService(
	runs RunStore,
	items InteractionItemStore,
	decisions ProtocolInteractionDecider,
) (*InteractionDecisionService, error) {
	if runs == nil || items == nil || decisions == nil {
		return nil, ErrInteractionDecisionInvalid
	}
	return &InteractionDecisionService{runs: runs, items: items, decisions: decisions}, nil
}

type DecideInteractionInput struct {
	Scope           ConversationScope
	RunID           string
	InteractionID   string
	Decision        string
	ExpectedVersion int64
	IdempotencyKey  string
	Principal       agentaccessauth.AAPAccessTokenPrincipal
	Authorization   agentaccessauth.AAPAuthorizationDecision
}

type DecideInteractionResult struct {
	Interaction       protocolevent.Interaction
	Item              protocolevent.RunItemProjection
	Decision          execution.InteractionDecisionResult
	Events            []protocolevent.ProtocolEvent
	Idempotent        bool
	NotifyError       error
	ContinuationError error
}

func (service *InteractionDecisionService) Decide(
	ctx context.Context,
	input DecideInteractionInput,
) (DecideInteractionResult, error) {
	input.Scope.WorkspaceID = strings.TrimSpace(input.Scope.WorkspaceID)
	input.Scope.AgentID = strings.TrimSpace(input.Scope.AgentID)
	input.RunID = strings.ToLower(strings.TrimSpace(input.RunID))
	input.InteractionID = strings.ToLower(strings.TrimSpace(input.InteractionID))
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	if service == nil || service.runs == nil || service.items == nil || service.decisions == nil ||
		ctx == nil || !validConversationScope(input.Scope) || !canonicalUUID(input.RunID) ||
		!canonicalUUID(input.InteractionID) || !canonicalUUID(input.IdempotencyKey) ||
		input.ExpectedVersion < 1 || !validInteractionDecisionAuthorization(input) {
		return DecideInteractionResult{}, ErrInteractionDecisionInvalid
	}
	if input.Decision != execution.InteractionDecisionApprove &&
		input.Decision != execution.InteractionDecisionDecline &&
		input.Decision != execution.InteractionDecisionCancel {
		return DecideInteractionResult{}, ErrInteractionDecisionInvalid
	}
	receiptKey := commandReceiptKey(
		input.Scope, input.Principal, input.Authorization,
		CommandInteractionDecide, input.IdempotencyKey,
	)
	requestHash, err := commandRequestHash(struct {
		RunID           string `json:"runId"`
		InteractionID   string `json:"interactionId"`
		Decision        string `json:"decision"`
		ExpectedVersion int64  `json:"expectedVersion"`
	}{input.RunID, input.InteractionID, input.Decision, input.ExpectedVersion})
	if err != nil {
		return DecideInteractionResult{}, err
	}
	if err := observeCommand(ctx, service.receipts, receiptKey, requestHash); err != nil {
		return DecideInteractionResult{}, err
	}
	run, err := service.runs.GetAgentRun(ctx, input.Scope.WorkspaceID, input.RunID)
	if err != nil {
		return DecideInteractionResult{}, err
	}
	if run.WorkspaceID != input.Scope.WorkspaceID || run.AgentID != input.Scope.AgentID {
		return DecideInteractionResult{}, ErrRunNotFound
	}
	projection, interaction, err := service.loadInteraction(ctx, input, run)
	if err != nil {
		return DecideInteractionResult{}, err
	}
	if !interactionAllowsDecision(interaction, input.Decision) ||
		(interaction.Status == protocolevent.InteractionStatusPending &&
			interaction.Version != input.ExpectedVersion) {
		return DecideInteractionResult{}, execution.ErrInteractionDecisionBindingChanged
	}
	identity, err := invocationIdentity(input.Scope.WorkspaceID, input.Principal)
	if err != nil {
		return DecideInteractionResult{}, ErrInteractionDecisionInvalid
	}
	decisionPrincipal, err := input.Authorization.Snapshot.ExecutionPrincipalSnapshot(identity)
	if err != nil || !interactionDeciderMatches(interaction.RequiredDecider, decisionPrincipal.Identity.Subject != nil) {
		return DecideInteractionResult{}, agentaccessauth.ErrAAPAuthorizationDenied
	}
	binding, err := execution.NewInteractionDecisionBinding(
		input.Scope.WorkspaceID, interaction, run.PrincipalSnapshot, input.ExpectedVersion,
	)
	if err != nil {
		return DecideInteractionResult{}, err
	}
	policy, err := servicePrincipalDecisionPolicy(interaction, decisionPrincipal.Identity.Subject == nil)
	if err != nil {
		return DecideInteractionResult{}, err
	}
	result, err := service.decisions.Decide(ctx, execution.ProtocolInteractionDecisionInput{
		Context: execution.ProtocolInteractionContext{
			Scope: protocolevent.RunScope{
				WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
				ConversationID: run.SessionID, RunID: run.ID,
			},
			EventStreamID: run.ID, TraceID: run.TraceID,
		},
		Decision: execution.DecideInteractionInput{
			WorkspaceID: input.Scope.WorkspaceID, ConfirmationID: input.InteractionID,
			ActorID:           input.Principal.ServicePrincipalID,
			PrincipalSnapshot: &decisionPrincipal, ServiceDecisionPolicy: policy,
			Decision: input.Decision, IdempotencyKey: input.IdempotencyKey, Binding: binding,
		},
		Presentation: interactionPresentation(interaction),
	})
	if err != nil {
		return DecideInteractionResult{}, err
	}
	if !result.Decision.Cached {
		projection = result.Projection
	} else {
		projection, _, err = service.loadInteraction(ctx, input, run)
		if err != nil {
			return DecideInteractionResult{}, err
		}
	}
	item, ok := projection.Item.(protocolevent.InteractionItem)
	if !ok {
		decoded, decodeErr := protocolevent.DecodeItem(projection.Snapshot)
		if decodeErr != nil {
			return DecideInteractionResult{}, ErrInteractionDecisionInvalid
		}
		item, ok = decoded.(protocolevent.InteractionItem)
	}
	if !ok || item.ID != input.InteractionID || item.Interaction.ID != input.InteractionID {
		return DecideInteractionResult{}, ErrInteractionDecisionInvalid
	}
	response := DecideInteractionResult{
		Interaction: item.Interaction, Item: projection, Decision: result.Decision,
		Events: result.Events, Idempotent: result.Decision.Cached,
		NotifyError: result.NotifyError,
	}
	if err := completeCommand(ctx, service.receipts, receiptKey, requestHash,
		"INTERACTION", item.Interaction.ID, item.Interaction.Version); err != nil {
		return DecideInteractionResult{}, err
	}
	if input.Decision == execution.InteractionDecisionApprove && !result.Decision.Cached &&
		service.continuation != nil {
		response.ContinuationError = service.continuation.ContinueApprovedInteraction(
			context.WithoutCancel(ctx), result.Decision,
		)
	}
	return response, nil
}

func (service *InteractionDecisionService) loadInteraction(
	ctx context.Context,
	input DecideInteractionInput,
	run execution.AgentRun,
) (protocolevent.RunItemProjection, protocolevent.Interaction, error) {
	projection, err := service.items.Get(
		ctx, input.Scope.WorkspaceID, input.Scope.AgentID, input.RunID, input.InteractionID,
	)
	if errors.Is(err, protocolevent.ErrRunItemNotFound) {
		return protocolevent.RunItemProjection{}, protocolevent.Interaction{}, ErrInteractionNotFound
	}
	if err != nil {
		return protocolevent.RunItemProjection{}, protocolevent.Interaction{}, err
	}
	if projection.WorkspaceID != run.WorkspaceID || projection.AgentID != run.AgentID ||
		projection.RunID != run.ID || projection.ID != input.InteractionID ||
		projection.ItemType != string(protocolevent.ItemTypeInteraction) {
		return protocolevent.RunItemProjection{}, protocolevent.Interaction{}, ErrInteractionNotFound
	}
	item, ok := projection.Item.(protocolevent.InteractionItem)
	if !ok {
		decoded, decodeErr := protocolevent.DecodeItem(projection.Snapshot)
		if decodeErr != nil {
			return protocolevent.RunItemProjection{}, protocolevent.Interaction{}, ErrInteractionDecisionInvalid
		}
		item, ok = decoded.(protocolevent.InteractionItem)
	}
	if !ok || item.ID != input.InteractionID || item.Interaction.ID != input.InteractionID ||
		item.Interaction.RunID != input.RunID || item.Interaction.TargetItemID == "" {
		return protocolevent.RunItemProjection{}, protocolevent.Interaction{}, ErrInteractionNotFound
	}
	return projection, item.Interaction, nil
}

func interactionPresentation(value protocolevent.Interaction) execution.InteractionPresentation {
	return execution.InteractionPresentation{
		TargetItemID: value.TargetItemID, Title: value.Title,
		RiskLevel: string(value.Risk.Level), RiskReasons: append([]string(nil), value.Risk.Reasons...),
		InputSummary:     append(json.RawMessage(nil), value.InputSummary...),
		AllowedDecisions: append([]protocolevent.InteractionDecision(nil), value.AllowedDecisions...),
		RequiredDecider:  value.RequiredDecider,
	}
}

func interactionAllowsDecision(interaction protocolevent.Interaction, decision string) bool {
	for _, allowed := range interaction.AllowedDecisions {
		if string(allowed) == decision {
			return true
		}
	}
	return false
}

func interactionDeciderMatches(required protocolevent.RequiredDecider, hasExternalSubject bool) bool {
	if hasExternalSubject {
		return required == protocolevent.RequiredDeciderSameExternalSubject
	}
	return required == protocolevent.RequiredDeciderServicePrincipal
}

func servicePrincipalDecisionPolicy(
	interaction protocolevent.Interaction,
	pureServicePrincipal bool,
) (*execution.ServicePrincipalDecisionPolicy, error) {
	if !pureServicePrincipal {
		return nil, nil
	}
	risk := strings.ToLower(strings.TrimSpace(string(interaction.Risk.Level)))
	if risk != "low" && risk != "medium" {
		return nil, agentaccessauth.ErrAAPAuthorizationDenied
	}
	return &execution.ServicePrincipalDecisionPolicy{Enabled: true, MaxRisk: risk}, nil
}

func validInteractionDecisionAuthorization(input DecideInteractionInput) bool {
	snapshot := input.Authorization.Snapshot
	return snapshot.SpecVersion == "aap.authorization.v1" &&
		snapshot.Action == agentaccessauth.ActionInteractionDecide &&
		snapshot.RequiredScope == "interaction:decide" &&
		snapshot.WorkspaceID == input.Scope.WorkspaceID && snapshot.AgentID == input.Scope.AgentID &&
		snapshot.ServicePrincipalID == input.Principal.ServicePrincipalID &&
		snapshot.SubjectID == input.Principal.PrincipalID &&
		snapshot.AuthorizedParty == input.Principal.AuthorizedParty &&
		snapshot.TokenID == input.Principal.TokenID &&
		snapshot.TokenSecurityVersion == input.Principal.SecurityVersion &&
		snapshot.ClientID != "" && snapshot.GrantID != "" &&
		snapshot.GrantVersion > 0 && snapshot.AgentPolicyVersion > 0 &&
		snapshot.ResourceType == agentaccessauth.ResourceInteraction &&
		snapshot.ResourceID == input.InteractionID &&
		(snapshot.OwnershipMode == "SUBJECT_OWNED" || snapshot.OwnershipMode == "POLICY_SHARED") &&
		containsScope(input.Authorization.EffectiveScopes, "interaction:decide") &&
		containsScope(snapshot.EffectiveScopes, "interaction:decide")
}
