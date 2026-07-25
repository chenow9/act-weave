package agentaccess

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
)

var ErrGrantConfigurationInvalid = errors.New("Agent Access Grant configuration is invalid")

//go:embed schemas/agent-grant.schema.json
var grantSchemaFiles embed.FS

func GrantConfigurationSchema() ([]byte, error) {
	raw, err := grantSchemaFiles.ReadFile("schemas/agent-grant.schema.json")
	return bytes.Clone(raw), err
}

type AgentScope string

const (
	ScopeAgentRead          AgentScope = "agent:read"
	ScopeConversationCreate AgentScope = "conversation:create"
	ScopeConversationRead   AgentScope = "conversation:read"
	ScopeRunCreate          AgentScope = "run:create"
	ScopeRunRead            AgentScope = "run:read"
	ScopeRunCancel          AgentScope = "run:cancel"
	ScopeEventRead          AgentScope = "event:read"
	ScopeInteractionDecide  AgentScope = "interaction:decide"
	ScopeArtifactRead       AgentScope = "artifact:read"
)

func KnownAgentScopes() []AgentScope {
	return []AgentScope{
		ScopeAgentRead,
		ScopeConversationCreate,
		ScopeConversationRead,
		ScopeRunCreate,
		ScopeRunRead,
		ScopeRunCancel,
		ScopeEventRead,
		ScopeInteractionDecide,
		ScopeArtifactRead,
	}
}

type GrantConfiguration struct {
	Scopes []AgentScope `json:"scopes"`
	Policy GrantPolicy  `json:"policy"`
}

type GrantPolicy struct {
	ServiceDecision *ServiceDecisionPolicy `json:"serviceDecision,omitempty"`
	SubjectSharing  *SubjectSharingPolicy  `json:"subjectSharing,omitempty"`
}

type ServiceDecisionPolicy struct {
	Enabled bool   `json:"enabled"`
	MaxRisk string `json:"maxRisk,omitempty"`
}

type SubjectSharingResource string

const (
	SubjectSharingConversation SubjectSharingResource = "conversation"
	SubjectSharingRun          SubjectSharingResource = "run"
	SubjectSharingEvent        SubjectSharingResource = "event"
	SubjectSharingInteraction  SubjectSharingResource = "interaction"
	SubjectSharingArtifact     SubjectSharingResource = "artifact"
)

type SubjectSharingPolicy struct {
	Enabled   bool                     `json:"enabled"`
	Resources []SubjectSharingResource `json:"resources,omitempty"`
}

func KnownSubjectSharingResources() []SubjectSharingResource {
	return []SubjectSharingResource{
		SubjectSharingConversation, SubjectSharingRun, SubjectSharingEvent,
		SubjectSharingInteraction, SubjectSharingArtifact,
	}
}

func ValidateGrantConfiguration(raw json.RawMessage) (GrantConfiguration, error) {
	var required map[string]json.RawMessage
	if err := json.Unmarshal(raw, &required); err != nil || len(required) != 2 ||
		required["scopes"] == nil || required["policy"] == nil {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var configuration GrantConfiguration
	if err := decoder.Decode(&configuration); err != nil {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	if len(configuration.Scopes) < 1 || len(configuration.Scopes) > len(KnownAgentScopes()) {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	known := make(map[AgentScope]struct{}, len(KnownAgentScopes()))
	for _, scope := range KnownAgentScopes() {
		known[scope] = struct{}{}
	}
	seen := make(map[AgentScope]struct{}, len(configuration.Scopes))
	for _, scope := range configuration.Scopes {
		if _, exists := known[scope]; !exists {
			return GrantConfiguration{}, ErrGrantConfigurationInvalid
		}
		if _, duplicate := seen[scope]; duplicate {
			return GrantConfiguration{}, ErrGrantConfigurationInvalid
		}
		seen[scope] = struct{}{}
	}
	var policyDocument map[string]json.RawMessage
	if json.Unmarshal(required["policy"], &policyDocument) != nil {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	if decision := configuration.Policy.ServiceDecision; decision != nil {
		var decisionDocument map[string]json.RawMessage
		if json.Unmarshal(policyDocument["serviceDecision"], &decisionDocument) != nil ||
			decisionDocument["enabled"] == nil {
			return GrantConfiguration{}, ErrGrantConfigurationInvalid
		}
		if decision.Enabled {
			if decision.MaxRisk != "low" && decision.MaxRisk != "medium" {
				return GrantConfiguration{}, ErrGrantConfigurationInvalid
			}
		} else if decision.MaxRisk != "" {
			return GrantConfiguration{}, ErrGrantConfigurationInvalid
		}
	} else if policyDocument["serviceDecision"] != nil {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	if sharing := configuration.Policy.SubjectSharing; sharing != nil {
		var sharingDocument map[string]json.RawMessage
		if json.Unmarshal(policyDocument["subjectSharing"], &sharingDocument) != nil ||
			sharingDocument["enabled"] == nil {
			return GrantConfiguration{}, ErrGrantConfigurationInvalid
		}
		if !sharing.Enabled {
			if len(sharing.Resources) != 0 || sharingDocument["resources"] != nil {
				return GrantConfiguration{}, ErrGrantConfigurationInvalid
			}
			return configuration, nil
		}
		knownResources := make(map[SubjectSharingResource]struct{}, len(KnownSubjectSharingResources()))
		for _, resource := range KnownSubjectSharingResources() {
			knownResources[resource] = struct{}{}
		}
		if len(sharing.Resources) < 1 || len(sharing.Resources) > len(knownResources) ||
			sharingDocument["resources"] == nil {
			return GrantConfiguration{}, ErrGrantConfigurationInvalid
		}
		seenResources := make(map[SubjectSharingResource]struct{}, len(sharing.Resources))
		for _, resource := range sharing.Resources {
			if _, exists := knownResources[resource]; !exists {
				return GrantConfiguration{}, ErrGrantConfigurationInvalid
			}
			if _, duplicate := seenResources[resource]; duplicate {
				return GrantConfiguration{}, ErrGrantConfigurationInvalid
			}
			seenResources[resource] = struct{}{}
		}
	} else if policyDocument["subjectSharing"] != nil {
		return GrantConfiguration{}, ErrGrantConfigurationInvalid
	}
	return configuration, nil
}
