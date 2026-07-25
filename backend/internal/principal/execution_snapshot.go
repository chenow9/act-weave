package principal

import (
	"bytes"
	"encoding/json"
	"strings"
)

const ExecutionAuthorizationSpecV1 = "execution.principal.v1"

// ExecutionSnapshot is the immutable identity and authorization-version
// binding copied to every Run, WorkflowExecution, and ToolInvocation. The
// evidence itself remains domain-specific and is wrapped by Envelope.
type ExecutionSnapshot struct {
	Identity           InvocationIdentity
	ClientID           string
	GrantID            string
	GrantVersion       int64
	AgentPolicyVersion int64
}

func NewExecutionSnapshot(
	identity InvocationIdentity,
	clientID, grantID string,
	grantVersion, agentPolicyVersion int64,
) (ExecutionSnapshot, error) {
	value := ExecutionSnapshot{
		Identity: identity, ClientID: strings.TrimSpace(clientID),
		GrantID: strings.TrimSpace(grantID), GrantVersion: grantVersion,
		AgentPolicyVersion: agentPolicyVersion,
	}
	if value.Validate() != nil {
		return ExecutionSnapshot{}, ErrInvalid
	}
	return value, nil
}

func NewInternalExecutionSnapshot(
	workspaceID string,
	actorType Type,
	actorID string,
) (ExecutionSnapshot, error) {
	actor := Ref{WorkspaceID: strings.TrimSpace(workspaceID), Type: actorType, ID: strings.TrimSpace(actorID)}
	var subject *Ref
	if actorType == TypeUser {
		subject = &actor
	}
	identity, err := NewInvocationIdentity(actor, subject)
	if err != nil || (actorType != TypeUser && actorType != TypeSystem) {
		return ExecutionSnapshot{}, ErrInvalid
	}
	return NewExecutionSnapshot(identity, "", "", 0, 0)
}

func (value ExecutionSnapshot) Validate() error {
	if value.Identity.Validate() != nil {
		return ErrInvalid
	}
	switch value.Identity.Actor.Type {
	case TypeUser:
		if value.Identity.Subject == nil || value.Identity.Subject.Type != TypeUser ||
			value.Identity.Subject.ID != value.Identity.Actor.ID || value.hasAAPBinding() {
			return ErrInvalid
		}
	case TypeSystem:
		if value.Identity.Subject != nil || value.hasAAPBinding() {
			return ErrInvalid
		}
	case TypeServicePrincipal:
		if !canonicalUUID(value.ClientID) || !canonicalUUID(value.GrantID) ||
			value.GrantVersion < 1 || value.AgentPolicyVersion < 1 ||
			(value.Identity.Subject != nil && value.Identity.Subject.Type != TypeExternalSubject) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (value ExecutionSnapshot) SameBinding(other ExecutionSnapshot) bool {
	if value.Validate() != nil || other.Validate() != nil ||
		value.Identity.Actor != other.Identity.Actor || value.ClientID != other.ClientID ||
		value.GrantID != other.GrantID || value.GrantVersion != other.GrantVersion ||
		value.AgentPolicyVersion != other.AgentPolicyVersion {
		return false
	}
	if value.Identity.Subject == nil || other.Identity.Subject == nil {
		return value.Identity.Subject == nil && other.Identity.Subject == nil
	}
	return *value.Identity.Subject == *other.Identity.Subject
}

// SameDecisionPrincipal compares the stable initiator identity used by an
// interaction decision. Authorization versions may advance while a Run is
// paused, but the Actor, represented Subject, Client, and Grant cannot change.
func (value ExecutionSnapshot) SameDecisionPrincipal(other ExecutionSnapshot) bool {
	if value.Validate() != nil || other.Validate() != nil ||
		value.Identity.Actor != other.Identity.Actor || value.ClientID != other.ClientID ||
		value.GrantID != other.GrantID {
		return false
	}
	if value.Identity.Subject == nil || other.Identity.Subject == nil {
		return value.Identity.Subject == nil && other.Identity.Subject == nil
	}
	return *value.Identity.Subject == *other.Identity.Subject
}

func (value ExecutionSnapshot) Envelope(evidence json.RawMessage) (json.RawMessage, error) {
	if value.Validate() != nil {
		return nil, ErrInvalid
	}
	canonicalEvidence, err := canonicalEvidenceObject(evidence)
	if err != nil {
		return nil, err
	}
	type refEnvelope struct {
		Type Type   `json:"type"`
		ID   string `json:"id"`
	}
	type authorizationEnvelope struct {
		SpecVersion        string          `json:"specVersion"`
		WorkspaceID        string          `json:"workspaceId"`
		Actor              refEnvelope     `json:"actor"`
		Subject            *refEnvelope    `json:"subject,omitempty"`
		ClientID           string          `json:"clientId,omitempty"`
		GrantID            string          `json:"grantId,omitempty"`
		GrantVersion       int64           `json:"grantVersion,omitempty"`
		AgentPolicyVersion int64           `json:"agentPolicyVersion,omitempty"`
		Evidence           json.RawMessage `json:"evidence"`
	}
	actor := refEnvelope{Type: value.Identity.Actor.Type, ID: value.Identity.Actor.ID}
	var subject *refEnvelope
	if value.Identity.Subject != nil {
		entry := refEnvelope{Type: value.Identity.Subject.Type, ID: value.Identity.Subject.ID}
		subject = &entry
	}
	encoded, err := json.Marshal(authorizationEnvelope{
		SpecVersion: ExecutionAuthorizationSpecV1,
		WorkspaceID: value.Identity.Actor.WorkspaceID, Actor: actor, Subject: subject,
		ClientID: value.ClientID, GrantID: value.GrantID,
		GrantVersion: value.GrantVersion, AgentPolicyVersion: value.AgentPolicyVersion,
		Evidence: canonicalEvidence,
	})
	if err != nil {
		return nil, ErrInvalid
	}
	return encoded, nil
}

func (value ExecutionSnapshot) hasAAPBinding() bool {
	return value.ClientID != "" || value.GrantID != "" ||
		value.GrantVersion != 0 || value.AgentPolicyVersion != 0
}

func canonicalEvidenceObject(value json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		value = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, ErrInvalid
	}
	return encoded, nil
}
