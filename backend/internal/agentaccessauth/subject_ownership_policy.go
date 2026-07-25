package agentaccessauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	OwnershipModeSubjectOwned = "SUBJECT_OWNED"
	OwnershipModePolicyShared = "POLICY_SHARED"

	OwnershipReasonResourceNotFound = "OWNERSHIP_RESOURCE_NOT_FOUND"
	OwnershipReasonScopeMismatch    = "OWNERSHIP_SCOPE_MISMATCH"
	OwnershipReasonActorMismatch    = "OWNERSHIP_ACTOR_MISMATCH"
	OwnershipReasonClientMismatch   = "OWNERSHIP_CLIENT_MISMATCH"
	OwnershipReasonSubjectMismatch  = "OWNERSHIP_SUBJECT_MISMATCH"
	OwnershipReasonSharingDenied    = "OWNERSHIP_SHARING_POLICY_DENIED"
	OwnershipReasonArtifactUnbound  = "OWNERSHIP_ARTIFACT_UNBOUND"
)

type SubjectOwnershipError struct {
	Reason string
}

func (err *SubjectOwnershipError) Error() string {
	return "AAP Subject Ownership denied: " + err.Reason
}

func (err *SubjectOwnershipError) Unwrap() error { return ErrSubjectOwnershipNotFound }

func subjectOwnershipDenied(reason string) error {
	return &SubjectOwnershipError{Reason: reason}
}

// SubjectOwnershipRecord is the normalized immutable ownership fact consumed
// by the centralized policy. Domain repositories remain responsible for their
// own tables; authorization does not copy those rules into HTTP handlers.
type SubjectOwnershipRecord struct {
	WorkspaceID   string
	AgentID       string
	ResourceType  AAPResourceType
	ResourceID    string
	ActorType     string
	ActorID       string
	SubjectType   string
	SubjectID     string
	ClientID      string
	Mode          string
	PolicyVersion int64
	ArtifactBound bool
}

type SubjectOwnershipStore interface {
	ResolveSubjectOwnershipRecord(
		context.Context,
		AAPAction,
		AAPAuthorizationResource,
	) (SubjectOwnershipRecord, error)
}

type SubjectOwnershipPolicy struct {
	store SubjectOwnershipStore
}

func NewSubjectOwnershipPolicy(store SubjectOwnershipStore) (*SubjectOwnershipPolicy, error) {
	if store == nil {
		return nil, errors.New("Subject Ownership store is required")
	}
	return &SubjectOwnershipPolicy{store: store}, nil
}

func (policy *SubjectOwnershipPolicy) ResolveSubjectOwnership(
	ctx context.Context,
	caller AAPAccessTokenPrincipal,
	state AAPAuthorizationState,
	action AAPAction,
	resource AAPAuthorizationResource,
) (SubjectOwnershipDecision, error) {
	rule, knownAction := aapActionRules[action]
	if policy == nil || policy.store == nil || ctx == nil || !knownAction ||
		!rule.OwnershipRequired || !validAuthorizationPrincipal(caller) ||
		!validAuthorizationResource(rule, resource) || !validAuthorizationState(state, caller) {
		return SubjectOwnershipDecision{}, ErrAAPAuthorizationInvalid
	}
	record, err := policy.store.ResolveSubjectOwnershipRecord(ctx, action, resource)
	if err != nil {
		if errors.Is(err, ErrSubjectOwnershipNotFound) {
			var ownershipError *SubjectOwnershipError
			if errors.As(err, &ownershipError) {
				return SubjectOwnershipDecision{}, ownershipError
			}
			return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonResourceNotFound)
		}
		return SubjectOwnershipDecision{}, err
	}
	if record.WorkspaceID != caller.WorkspaceID || record.WorkspaceID != state.WorkspaceID ||
		record.AgentID != caller.AgentID || record.AgentID != state.AgentID ||
		record.ResourceType != resource.Type || record.ResourceID != resource.ID ||
		record.PolicyVersion < 1 {
		return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonScopeMismatch)
	}
	if record.ActorType != "SERVICE_PRINCIPAL" ||
		record.ActorID != caller.ServicePrincipalID || record.ActorID != state.ServicePrincipalID {
		return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonActorMismatch)
	}
	if record.ClientID == "" || record.ClientID != state.ClientID {
		return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonClientMismatch)
	}
	if resource.Type == ResourceArtifact && !record.ArtifactBound {
		return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonArtifactUnbound)
	}

	ownerID := record.ActorID
	switch record.Mode {
	case OwnershipModeSubjectOwned:
		if record.SubjectID != "" {
			if record.SubjectType != "EXTERNAL_SUBJECT" {
				return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonSubjectMismatch)
			}
			ownerID = record.SubjectID
		} else if record.SubjectType != "" {
			return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonSubjectMismatch)
		}
		if caller.PrincipalID != ownerID {
			return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonSubjectMismatch)
		}
	case OwnershipModePolicyShared:
		if record.SubjectID != "" || record.SubjectType != "" {
			return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonSubjectMismatch)
		}
		if caller.PrincipalID != caller.ServicePrincipalID {
			shareResource := subjectSharingResourceForAction(action)
			if shareResource == "" || !containsSubjectSharingResource(
				state.SubjectSharingResources, shareResource,
			) {
				return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonSharingDenied)
			}
			return SubjectOwnershipDecision{
				Mode: record.Mode, OwnerID: ownerID, PolicyVersion: state.GrantVersion,
			}, nil
		}
	default:
		return SubjectOwnershipDecision{}, subjectOwnershipDenied(OwnershipReasonScopeMismatch)
	}
	return SubjectOwnershipDecision{
		Mode: record.Mode, OwnerID: ownerID, PolicyVersion: record.PolicyVersion,
	}, nil
}

func subjectSharingResourceForAction(action AAPAction) string {
	switch action {
	case ActionConversationRead, ActionRunCreate:
		return "conversation"
	case ActionRunRead, ActionRunCancel:
		return "run"
	case ActionEventRead:
		return "event"
	case ActionInteractionDecide:
		return "interaction"
	case ActionArtifactRead:
		return "artifact"
	default:
		return ""
	}
}

func containsSubjectSharingResource(resources []string, target string) bool {
	found := false
	seen := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		resource = strings.TrimSpace(resource)
		if _, duplicate := seen[resource]; duplicate {
			return false
		}
		seen[resource] = struct{}{}
		switch resource {
		case "conversation", "run", "event", "interaction", "artifact":
		default:
			return false
		}
		if resource == target {
			found = true
		}
	}
	return found
}

// SubjectOwnershipRepository normalizes Conversation, Run/Event,
// Interaction and Artifact ownership from their authoritative domain facts.
// Artifact lookup deliberately requires one unique Run Item binding.
type SubjectOwnershipRepository struct {
	db *sql.DB
}

func NewSubjectOwnershipRepository(db *sql.DB) (*SubjectOwnershipRepository, error) {
	if db == nil {
		return nil, errors.New("Subject Ownership database is required")
	}
	return &SubjectOwnershipRepository{db: db}, nil
}

func (repository *SubjectOwnershipRepository) ResolveSubjectOwnershipRecord(
	ctx context.Context,
	action AAPAction,
	resource AAPAuthorizationResource,
) (SubjectOwnershipRecord, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return SubjectOwnershipRecord{}, ErrAAPAuthorizationInvalid
	}
	switch resource.Type {
	case ResourceConversation:
		return repository.resolveConversation(ctx, resource)
	case ResourceRun:
		return repository.resolveRun(ctx, resource)
	case ResourceInteraction:
		return repository.resolveInteraction(ctx, resource)
	case ResourceArtifact:
		return repository.resolveArtifact(ctx, action, resource)
	default:
		return SubjectOwnershipRecord{}, ErrAAPAuthorizationInvalid
	}
}

func (repository *SubjectOwnershipRepository) resolveConversation(
	ctx context.Context,
	resource AAPAuthorizationResource,
) (SubjectOwnershipRecord, error) {
	record := SubjectOwnershipRecord{ResourceType: resource.Type, ResourceID: resource.ID}
	var subjectType, subjectID, clientID sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT workspace_id,agent_id,actor_type,actor_id,subject_type,subject_id,
		       client_id,ownership_mode,ownership_policy_version
		FROM chat_sessions WHERE id=$1
	`, resource.ID).Scan(
		&record.WorkspaceID, &record.AgentID, &record.ActorType, &record.ActorID,
		&subjectType, &subjectID, &clientID, &record.Mode, &record.PolicyVersion,
	)
	if err != nil {
		return SubjectOwnershipRecord{}, mapSubjectOwnershipRead("resolve Conversation ownership", err)
	}
	record.SubjectType, record.SubjectID, record.ClientID = subjectType.String, subjectID.String, clientID.String
	return record, nil
}

func (repository *SubjectOwnershipRepository) resolveRun(
	ctx context.Context,
	resource AAPAuthorizationResource,
) (SubjectOwnershipRecord, error) {
	record := SubjectOwnershipRecord{ResourceType: resource.Type, ResourceID: resource.ID}
	var subjectType, subjectID, clientID sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT ar.workspace_id,ar.agent_id,ar.triggered_by_type,ar.triggered_by_id,
		       CASE WHEN cs.ownership_mode='POLICY_SHARED' THEN cs.subject_type ELSE ar.subject_type END,
		       CASE WHEN cs.ownership_mode='POLICY_SHARED' THEN cs.subject_id ELSE ar.subject_id END,
		       ar.client_id,
		       CASE WHEN cs.id IS NOT NULL THEN cs.ownership_mode ELSE 'SUBJECT_OWNED' END,
		       CASE WHEN cs.id IS NOT NULL THEN cs.ownership_policy_version
		            ELSE COALESCE(ar.agent_policy_version,1) END
		FROM agent_runs ar
		LEFT JOIN chat_sessions cs
		  ON cs.workspace_id=ar.workspace_id AND cs.id=ar.session_id
		 AND cs.agent_id=ar.agent_id AND cs.actor_type=ar.triggered_by_type
		 AND cs.actor_id=ar.triggered_by_id
		 AND cs.client_id IS NOT DISTINCT FROM ar.client_id
		 AND (cs.ownership_mode='POLICY_SHARED' OR (
		      cs.subject_type IS NOT DISTINCT FROM ar.subject_type
		  AND cs.subject_id IS NOT DISTINCT FROM ar.subject_id
		 ))
		WHERE ar.id=$1
	`, resource.ID).Scan(
		&record.WorkspaceID, &record.AgentID, &record.ActorType, &record.ActorID,
		&subjectType, &subjectID, &clientID, &record.Mode, &record.PolicyVersion,
	)
	if err != nil {
		return SubjectOwnershipRecord{}, mapSubjectOwnershipRead("resolve Run ownership", err)
	}
	record.SubjectType, record.SubjectID, record.ClientID = subjectType.String, subjectID.String, clientID.String
	return record, nil
}

func (repository *SubjectOwnershipRepository) resolveInteraction(
	ctx context.Context,
	resource AAPAuthorizationResource,
) (SubjectOwnershipRecord, error) {
	record := SubjectOwnershipRecord{ResourceType: resource.Type, ResourceID: resource.ID}
	var subjectType, subjectID, clientID sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT ec.workspace_id,ar.agent_id,ec.request_actor_type,ec.request_actor_id,
		       ec.request_subject_type,ec.request_subject_id,ec.request_client_id,
		       CASE WHEN ec.request_subject_id IS NOT NULL THEN 'SUBJECT_OWNED'
		            WHEN cs.id IS NOT NULL THEN cs.ownership_mode ELSE 'SUBJECT_OWNED' END,
		       CASE WHEN cs.id IS NOT NULL THEN cs.ownership_policy_version
		            ELSE COALESCE(ec.request_agent_policy_version,1) END
		FROM execution_confirmations ec
		JOIN agent_runs ar
		  ON ar.workspace_id=ec.workspace_id AND ar.id=ec.run_id
		LEFT JOIN chat_sessions cs
		  ON cs.workspace_id=ar.workspace_id AND cs.id=ar.session_id
		 AND cs.agent_id=ar.agent_id AND cs.actor_type=ec.request_actor_type
		 AND cs.actor_id=ec.request_actor_id
		 AND cs.client_id IS NOT DISTINCT FROM ec.request_client_id
		WHERE ec.id=$1
	`, resource.ID).Scan(
		&record.WorkspaceID, &record.AgentID, &record.ActorType, &record.ActorID,
		&subjectType, &subjectID, &clientID, &record.Mode, &record.PolicyVersion,
	)
	if err != nil {
		return SubjectOwnershipRecord{}, mapSubjectOwnershipRead("resolve Interaction ownership", err)
	}
	record.SubjectType, record.SubjectID, record.ClientID = subjectType.String, subjectID.String, clientID.String
	return record, nil
}

func (repository *SubjectOwnershipRepository) resolveArtifact(
	ctx context.Context,
	action AAPAction,
	resource AAPAuthorizationResource,
) (SubjectOwnershipRecord, error) {
	if action != ActionArtifactRead {
		return SubjectOwnershipRecord{}, ErrAAPAuthorizationInvalid
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT so.workspace_id,ri.agent_id,ar.triggered_by_type,ar.triggered_by_id,
		       CASE WHEN cs.ownership_mode='POLICY_SHARED' THEN cs.subject_type ELSE ar.subject_type END,
		       CASE WHEN cs.ownership_mode='POLICY_SHARED' THEN cs.subject_id ELSE ar.subject_id END,
		       ar.client_id,
		       CASE WHEN cs.id IS NOT NULL THEN cs.ownership_mode ELSE 'SUBJECT_OWNED' END,
		       CASE WHEN cs.id IS NOT NULL THEN cs.ownership_policy_version
		            ELSE COALESCE(ar.agent_policy_version,1) END
		FROM stored_objects so
		JOIN run_items ri
		  ON ri.workspace_id=so.workspace_id AND ri.source_type='STORED_OBJECT'
		 AND ri.source_id=so.id AND ri.item_type='artifact'
		JOIN agent_runs ar
		  ON ar.workspace_id=ri.workspace_id AND ar.agent_id=ri.agent_id AND ar.id=ri.run_id
		LEFT JOIN chat_sessions cs
		  ON cs.workspace_id=ar.workspace_id AND cs.id=ar.session_id
		 AND cs.agent_id=ar.agent_id AND cs.actor_type=ar.triggered_by_type
		 AND cs.actor_id=ar.triggered_by_id
		 AND cs.client_id IS NOT DISTINCT FROM ar.client_id
		 AND (cs.ownership_mode='POLICY_SHARED' OR (
		      cs.subject_type IS NOT DISTINCT FROM ar.subject_type
		  AND cs.subject_id IS NOT DISTINCT FROM ar.subject_id
		 ))
		WHERE so.id=$1
		ORDER BY ri.id
		LIMIT 2
	`, resource.ID)
	if err != nil {
		return SubjectOwnershipRecord{}, fmt.Errorf("resolve Artifact ownership: %w", err)
	}
	defer rows.Close()
	var records []SubjectOwnershipRecord
	for rows.Next() {
		record := SubjectOwnershipRecord{
			ResourceType: resource.Type, ResourceID: resource.ID, ArtifactBound: true,
		}
		var subjectType, subjectID, clientID sql.NullString
		if err := rows.Scan(
			&record.WorkspaceID, &record.AgentID, &record.ActorType, &record.ActorID,
			&subjectType, &subjectID, &clientID, &record.Mode, &record.PolicyVersion,
		); err != nil {
			return SubjectOwnershipRecord{}, fmt.Errorf("scan Artifact ownership: %w", err)
		}
		record.SubjectType, record.SubjectID, record.ClientID = subjectType.String, subjectID.String, clientID.String
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return SubjectOwnershipRecord{}, fmt.Errorf("iterate Artifact ownership: %w", err)
	}
	if len(records) != 1 {
		return SubjectOwnershipRecord{}, subjectOwnershipDenied(OwnershipReasonArtifactUnbound)
	}
	return records[0], nil
}

func mapSubjectOwnershipRead(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return subjectOwnershipDenied(OwnershipReasonResourceNotFound)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}
