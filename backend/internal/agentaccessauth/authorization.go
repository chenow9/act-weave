package agentaccessauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"actweave/backend/internal/principal"
)

var (
	ErrAAPAuthorizationInvalid       = errors.New("AAP authorization request is invalid")
	ErrAAPAuthorizationDenied        = errors.New("AAP authorization is denied")
	ErrAAPAuthorizationNotVisible    = errors.New("AAP resource is not visible")
	ErrAAPAuthorizationUnavailable   = errors.New("AAP authorization service is unavailable")
	ErrAAPAuthorizationStateNotFound = errors.New("AAP authorization state was not found")
	ErrSubjectOwnershipNotFound      = errors.New("AAP subject ownership was not found")
)

type AAPAction string

const (
	ActionAgentProfileRead   AAPAction = "agent.profile.read"
	ActionConversationCreate AAPAction = "conversation.create"
	ActionConversationRead   AAPAction = "conversation.read"
	ActionRunCreate          AAPAction = "run.create"
	ActionRunRead            AAPAction = "run.read"
	ActionRunCancel          AAPAction = "run.cancel"
	ActionEventRead          AAPAction = "event.read"
	ActionInteractionDecide  AAPAction = "interaction.decide"
	ActionArtifactRead       AAPAction = "artifact.read"
	ActionFileCreate         AAPAction = "file.create"
	ActionFileComplete       AAPAction = "file.complete"
	ActionFileRead           AAPAction = "file.read"
	ActionFileContent        AAPAction = "file.content"
)

type AAPResourceType string

const (
	ResourceNone         AAPResourceType = ""
	ResourceConversation AAPResourceType = "CONVERSATION"
	ResourceRun          AAPResourceType = "RUN"
	ResourceInteraction  AAPResourceType = "INTERACTION"
	ResourceArtifact     AAPResourceType = "ARTIFACT"
	ResourceFile         AAPResourceType = "FILE"
)

type AAPActionRule struct {
	RequiredScope     string
	ResourceType      AAPResourceType
	OwnershipRequired bool
	ConcealDenial     bool
}

// aapActionRules is the AAP v1 action matrix. Multiple actions may share a
// scope (e.g. file.create/complete → file:write); len(matrix) is not required
// to equal len(canonicalAAPScopes).
var aapActionRules = map[AAPAction]AAPActionRule{
	ActionAgentProfileRead:   {RequiredScope: "agent:read", ConcealDenial: true},
	ActionConversationCreate: {RequiredScope: "conversation:create"},
	ActionConversationRead:   {RequiredScope: "conversation:read", ResourceType: ResourceConversation, OwnershipRequired: true, ConcealDenial: true},
	ActionRunCreate:          {RequiredScope: "run:create", ResourceType: ResourceConversation, OwnershipRequired: true, ConcealDenial: true},
	ActionRunRead:            {RequiredScope: "run:read", ResourceType: ResourceRun, OwnershipRequired: true, ConcealDenial: true},
	ActionRunCancel:          {RequiredScope: "run:cancel", ResourceType: ResourceRun, OwnershipRequired: true, ConcealDenial: true},
	ActionEventRead:          {RequiredScope: "event:read", ResourceType: ResourceRun, OwnershipRequired: true, ConcealDenial: true},
	ActionInteractionDecide:  {RequiredScope: "interaction:decide", ResourceType: ResourceInteraction, OwnershipRequired: true, ConcealDenial: true},
	ActionArtifactRead:       {RequiredScope: "artifact:read", ResourceType: ResourceArtifact, OwnershipRequired: true, ConcealDenial: true},
	// File actions (KD-5/15, §5.6.2): create has no ownership resource; complete/read/content conceal denial.
	ActionFileCreate:   {RequiredScope: "file:write"},
	ActionFileComplete: {RequiredScope: "file:write", ResourceType: ResourceFile, OwnershipRequired: true, ConcealDenial: true},
	ActionFileRead:     {RequiredScope: "file:read", ResourceType: ResourceFile, OwnershipRequired: true, ConcealDenial: true},
	ActionFileContent:  {RequiredScope: "file:read", ResourceType: ResourceFile, OwnershipRequired: true, ConcealDenial: true},
}

func AAPActionMatrix() map[AAPAction]AAPActionRule {
	result := make(map[AAPAction]AAPActionRule, len(aapActionRules))
	for action, rule := range aapActionRules {
		result[action] = rule
	}
	return result
}

type AAPAuthorizationResource struct {
	Type AAPResourceType
	ID   string
}

type AAPAuthorizationState struct {
	WorkspaceID             string
	AgentID                 string
	ClientID                string
	PublicClientID          string
	ServicePrincipalID      string
	CurrentSecurityVersion  int64
	GrantID                 string
	GrantScopes             []string
	AgentPolicyScopes       []string
	SubjectSharingResources []string
	WorkspaceVersion        int64
	ClientVersion           int64
	GrantVersion            int64
	AgentPolicyVersion      int64
}

type AAPAuthorizationStateStore interface {
	ResolveAAPAuthorizationState(
		context.Context,
		AAPAccessTokenPrincipal,
		time.Time,
	) (AAPAuthorizationState, error)
}

type SubjectOwnershipDecision struct {
	Mode          string
	OwnerID       string
	PolicyVersion int64
}

type SubjectOwnershipResolver interface {
	ResolveSubjectOwnership(
		context.Context,
		AAPAccessTokenPrincipal,
		AAPAuthorizationState,
		AAPAction,
		AAPAuthorizationResource,
	) (SubjectOwnershipDecision, error)
}

type AAPAuthorizationDenial struct {
	WorkspaceID        string
	AgentID            string
	ServicePrincipalID string
	AuthorizedParty    string
	Action             AAPAction
	RequiredScope      string
	Reason             string
	ResourceType       AAPResourceType
	ResourceID         string
}

type AAPAuthorizationAudit interface {
	RecordAAPAuthorizationDenied(context.Context, AAPAuthorizationDenial) error
}

type AAPAuthorizationRequest struct {
	Principal AAPAccessTokenPrincipal
	Action    AAPAction
	Resource  AAPAuthorizationResource
}

type AAPAuthorizationSnapshot struct {
	SpecVersion             string          `json:"specVersion"`
	WorkspaceID             string          `json:"workspaceId"`
	AgentID                 string          `json:"agentId"`
	ClientID                string          `json:"clientId"`
	AuthorizedParty         string          `json:"authorizedParty"`
	ServicePrincipalID      string          `json:"servicePrincipalId"`
	SubjectID               string          `json:"subjectId"`
	GrantID                 string          `json:"grantId"`
	Action                  AAPAction       `json:"action"`
	RequiredScope           string          `json:"requiredScope"`
	TokenScopes             []string        `json:"tokenScopes"`
	GrantScopes             []string        `json:"grantScopes"`
	AgentPolicyScopes       []string        `json:"agentPolicyScopes"`
	EffectiveScopes         []string        `json:"effectiveScopes"`
	TokenSecurityVersion    int64           `json:"tokenSecurityVersion"`
	ResolvedSecurityVersion int64           `json:"resolvedSecurityVersion"`
	WorkspaceVersion        int64           `json:"workspaceVersion"`
	ClientVersion           int64           `json:"clientVersion"`
	GrantVersion            int64           `json:"grantVersion"`
	AgentPolicyVersion      int64           `json:"agentPolicyVersion"`
	TokenID                 string          `json:"tokenId"`
	ResourceType            AAPResourceType `json:"resourceType,omitempty"`
	ResourceID              string          `json:"resourceId,omitempty"`
	OwnershipMode           string          `json:"ownershipMode,omitempty"`
	OwnershipPolicyVersion  int64           `json:"ownershipPolicyVersion,omitempty"`
	AuthorizedAt            time.Time       `json:"authorizedAt"`
}

func (snapshot AAPAuthorizationSnapshot) JSON() (json.RawMessage, error) {
	if snapshot.SpecVersion != "aap.authorization.v1" || snapshot.WorkspaceID == "" ||
		snapshot.AgentID == "" || snapshot.ClientID == "" || snapshot.GrantID == "" ||
		snapshot.RequiredScope == "" || snapshot.TokenID == "" || snapshot.AuthorizedAt.IsZero() {
		return nil, ErrAAPAuthorizationInvalid
	}
	value, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal AAP authorization snapshot: %w", err)
	}
	return value, nil
}

// ExecutionPrincipalSnapshot converts a successful AAP authorization result
// into the domain-neutral immutable binding persisted by Run/Workflow/Tool.
// The caller supplies the already resolved typed identity so a UUID cannot be
// reinterpreted between Service Principal and External Subject namespaces.
func (snapshot AAPAuthorizationSnapshot) ExecutionPrincipalSnapshot(
	identity principal.InvocationIdentity,
) (principal.ExecutionSnapshot, error) {
	if snapshot.SpecVersion != "aap.authorization.v1" || identity.Validate() != nil ||
		identity.Actor.Type != principal.TypeServicePrincipal ||
		identity.Actor.WorkspaceID != snapshot.WorkspaceID ||
		identity.Actor.ID != snapshot.ServicePrincipalID || snapshot.ClientID == "" ||
		snapshot.GrantID == "" || snapshot.GrantVersion < 1 || snapshot.AgentPolicyVersion < 1 {
		return principal.ExecutionSnapshot{}, ErrAAPAuthorizationInvalid
	}
	wantSubject := identity.Actor.ID
	if identity.Subject != nil {
		wantSubject = identity.Subject.ID
	}
	if snapshot.SubjectID != wantSubject {
		return principal.ExecutionSnapshot{}, ErrAAPAuthorizationInvalid
	}
	value, err := principal.NewExecutionSnapshot(
		identity, snapshot.ClientID, snapshot.GrantID,
		snapshot.GrantVersion, snapshot.AgentPolicyVersion,
	)
	if err != nil {
		return principal.ExecutionSnapshot{}, ErrAAPAuthorizationInvalid
	}
	return value, nil
}

type AAPAuthorizationDecision struct {
	EffectiveScopes []string
	Snapshot        AAPAuthorizationSnapshot
}

type AAPAuthorizationService struct {
	states    AAPAuthorizationStateStore
	ownership SubjectOwnershipResolver
	audit     AAPAuthorizationAudit
	now       func() time.Time
}

type AAPAuthorizationOption func(*AAPAuthorizationService) error

func NewAAPAuthorizationService(
	states AAPAuthorizationStateStore,
	ownership SubjectOwnershipResolver,
	options ...AAPAuthorizationOption,
) (*AAPAuthorizationService, error) {
	if states == nil || ownership == nil {
		return nil, errors.New("AAP authorization state and Subject Ownership resolvers are required")
	}
	service := &AAPAuthorizationService{
		states: states, ownership: ownership, now: func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("AAP authorization option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func WithAAPAuthorizationAudit(audit AAPAuthorizationAudit) AAPAuthorizationOption {
	return func(service *AAPAuthorizationService) error {
		if audit == nil {
			return errors.New("AAP authorization audit is required")
		}
		service.audit = audit
		return nil
	}
}

func (service *AAPAuthorizationService) Authorize(
	ctx context.Context,
	request AAPAuthorizationRequest,
) (AAPAuthorizationDecision, error) {
	rule, ok := aapActionRules[request.Action]
	if service == nil || service.states == nil || service.ownership == nil || ctx == nil || !ok ||
		!validAuthorizationPrincipal(request.Principal) || !validAuthorizationResource(rule, request.Resource) {
		return AAPAuthorizationDecision{}, ErrAAPAuthorizationInvalid
	}
	now := service.now().UTC()
	if !now.Before(request.Principal.ExpiresAt) {
		return AAPAuthorizationDecision{}, ErrTokenExpired
	}
	state, err := service.states.ResolveAAPAuthorizationState(ctx, request.Principal, now)
	if err != nil {
		if errors.Is(err, ErrAAPAuthorizationStateNotFound) {
			return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "BINDING_NOT_VISIBLE", true)
		}
		return AAPAuthorizationDecision{}, ErrAAPAuthorizationUnavailable
	}
	if !validAuthorizationState(state, request.Principal) {
		return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "BINDING_NOT_VISIBLE", true)
	}
	if state.CurrentSecurityVersion != request.Principal.SecurityVersion {
		return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "SECURITY_VERSION_CHANGED", true)
	}
	tokenScopes := append([]string(nil), request.Principal.Scopes...)
	grantScopes, valid := authorizationScopeSet(state.GrantScopes)
	if !valid {
		return AAPAuthorizationDecision{}, ErrAAPAuthorizationUnavailable
	}
	policyScopes, valid := authorizationScopeSet(state.AgentPolicyScopes)
	if !valid {
		return AAPAuthorizationDecision{}, ErrAAPAuthorizationUnavailable
	}
	effective := intersectAuthorizationScopes(tokenScopes, state.GrantScopes, state.AgentPolicyScopes)
	if !request.Principal.HasScope(rule.RequiredScope) {
		return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "TOKEN_SCOPE_MISSING", rule.ConcealDenial)
	}
	if _, exists := grantScopes[rule.RequiredScope]; !exists {
		return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "GRANT_SCOPE_MISSING", rule.ConcealDenial)
	}
	if _, exists := policyScopes[rule.RequiredScope]; !exists {
		return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "AGENT_POLICY_DENIED", rule.ConcealDenial)
	}
	ownership := SubjectOwnershipDecision{}
	if rule.OwnershipRequired {
		ownership, err = service.ownership.ResolveSubjectOwnership(
			ctx, request.Principal, state, request.Action, request.Resource,
		)
		if err != nil {
			if errors.Is(err, ErrSubjectOwnershipNotFound) {
				reason := "SUBJECT_OWNERSHIP_DENIED"
				var ownershipError *SubjectOwnershipError
				if errors.As(err, &ownershipError) && ownershipError.Reason != "" {
					reason = ownershipError.Reason
				}
				return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, reason, true)
			}
			return AAPAuthorizationDecision{}, ErrAAPAuthorizationUnavailable
		}
		if !validOwnershipDecision(ownership, request.Principal) {
			return AAPAuthorizationDecision{}, service.denial(ctx, request, rule, "SUBJECT_OWNERSHIP_DENIED", true)
		}
	}
	snapshot := AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1",
		WorkspaceID: state.WorkspaceID, AgentID: state.AgentID, ClientID: state.ClientID,
		AuthorizedParty: state.PublicClientID, ServicePrincipalID: state.ServicePrincipalID,
		SubjectID: request.Principal.PrincipalID, GrantID: state.GrantID,
		Action: request.Action, RequiredScope: rule.RequiredScope,
		TokenScopes: tokenScopes, GrantScopes: append([]string(nil), state.GrantScopes...),
		AgentPolicyScopes:       append([]string(nil), state.AgentPolicyScopes...),
		EffectiveScopes:         append([]string(nil), effective...),
		TokenSecurityVersion:    request.Principal.SecurityVersion,
		ResolvedSecurityVersion: state.CurrentSecurityVersion,
		WorkspaceVersion:        state.WorkspaceVersion, ClientVersion: state.ClientVersion,
		GrantVersion: state.GrantVersion, AgentPolicyVersion: state.AgentPolicyVersion,
		TokenID: request.Principal.TokenID, ResourceType: request.Resource.Type,
		ResourceID: request.Resource.ID, OwnershipMode: ownership.Mode,
		OwnershipPolicyVersion: ownership.PolicyVersion, AuthorizedAt: now,
	}
	return AAPAuthorizationDecision{EffectiveScopes: effective, Snapshot: snapshot}, nil
}

func (service *AAPAuthorizationService) denial(
	ctx context.Context,
	request AAPAuthorizationRequest,
	rule AAPActionRule,
	reason string,
	conceal bool,
) error {
	cause := ErrAAPAuthorizationDenied
	if conceal {
		cause = ErrAAPAuthorizationNotVisible
	}
	denial := &AAPAuthorizationError{Reason: reason, Action: request.Action, cause: cause}
	if service.audit == nil {
		return denial
	}
	if err := service.audit.RecordAAPAuthorizationDenied(ctx, AAPAuthorizationDenial{
		WorkspaceID: request.Principal.WorkspaceID, AgentID: request.Principal.AgentID,
		ServicePrincipalID: request.Principal.ServicePrincipalID,
		AuthorizedParty:    request.Principal.AuthorizedParty, Action: request.Action,
		RequiredScope: rule.RequiredScope, Reason: reason,
		ResourceType: request.Resource.Type, ResourceID: request.Resource.ID,
	}); err != nil {
		return errors.Join(denial, fmt.Errorf("record AAP authorization denial audit: %w", err))
	}
	return denial
}

type AAPAuthorizationError struct {
	Reason string
	Action AAPAction
	cause  error
}

func (err *AAPAuthorizationError) Error() string {
	return "AAP authorization denied: " + err.Reason
}

func (err *AAPAuthorizationError) Unwrap() error { return err.cause }

func validAuthorizationPrincipal(principal AAPAccessTokenPrincipal) bool {
	if principal.PrincipalID == "" || principal.ServicePrincipalID == "" ||
		!validCanonicalUUID(principal.PrincipalID) || !validCanonicalUUID(principal.ServicePrincipalID) ||
		!validPublicClientID(principal.AuthorizedParty) ||
		!validCanonicalUUID(principal.WorkspaceID) || !validCanonicalUUID(principal.AgentID) ||
		principal.SecurityVersion < 1 || !validCanonicalUUID(principal.TokenID) ||
		principal.ExpiresAt.IsZero() {
		return false
	}
	_, _, err := canonicalizeRequestedScopes(principal.Scopes)
	return err == nil
}

func validAuthorizationResource(rule AAPActionRule, resource AAPAuthorizationResource) bool {
	if !rule.OwnershipRequired {
		return resource.Type == ResourceNone && resource.ID == ""
	}
	return resource.Type == rule.ResourceType && validCanonicalUUID(resource.ID)
}

func validAuthorizationState(state AAPAuthorizationState, principal AAPAccessTokenPrincipal) bool {
	return state.WorkspaceID == principal.WorkspaceID && state.AgentID == principal.AgentID &&
		validCanonicalUUID(state.ClientID) && state.PublicClientID == principal.AuthorizedParty &&
		state.ServicePrincipalID == principal.ServicePrincipalID &&
		state.CurrentSecurityVersion > 0 && validCanonicalUUID(state.GrantID) &&
		state.WorkspaceVersion > 0 && state.ClientVersion > 0 && state.GrantVersion > 0 &&
		state.AgentPolicyVersion > 0 && len(state.GrantScopes) > 0 && len(state.AgentPolicyScopes) > 0
}

func authorizationScopeSet(scopes []string) (map[string]struct{}, bool) {
	ordered, _, err := canonicalizeRequestedScopes(scopes)
	if err != nil {
		return nil, false
	}
	result := make(map[string]struct{}, len(ordered))
	for _, scope := range ordered {
		result[scope] = struct{}{}
	}
	return result, true
}

func intersectAuthorizationScopes(groups ...[]string) []string {
	counts := make(map[string]int, len(canonicalAAPScopes))
	for _, group := range groups {
		seen := make(map[string]struct{}, len(group))
		for _, scope := range group {
			seen[scope] = struct{}{}
		}
		for scope := range seen {
			counts[scope]++
		}
	}
	result := make([]string, 0, len(canonicalAAPScopes))
	for _, scope := range canonicalAAPScopes {
		if counts[scope] == len(groups) {
			result = append(result, scope)
		}
	}
	return result
}

func validOwnershipDecision(
	decision SubjectOwnershipDecision,
	principal AAPAccessTokenPrincipal,
) bool {
	if decision.PolicyVersion < 1 || !validCanonicalUUID(decision.OwnerID) {
		return false
	}
	switch decision.Mode {
	case "SUBJECT_OWNED":
		return decision.OwnerID == principal.PrincipalID
	case "POLICY_SHARED":
		return true
	default:
		return false
	}
}

// FailClosedSubjectOwnershipResolver remains available for isolated security
// tests and deliberately disabled deployments. Production uses the centralized
// SubjectOwnershipPolicy backed by authoritative domain facts.
type FailClosedSubjectOwnershipResolver struct{}

func (FailClosedSubjectOwnershipResolver) ResolveSubjectOwnership(
	context.Context,
	AAPAccessTokenPrincipal,
	AAPAuthorizationState,
	AAPAction,
	AAPAuthorizationResource,
) (SubjectOwnershipDecision, error) {
	return SubjectOwnershipDecision{}, ErrSubjectOwnershipNotFound
}
