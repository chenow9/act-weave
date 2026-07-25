package agentaccess

import (
	"context"
	"database/sql"
	"errors"
)

const (
	ActionClientCreated                 = "agentaccess.client.created"
	ActionClientStatusChanged           = "agentaccess.client.status.changed"
	ActionTrustedSubjectIssuerUpdated   = "agentaccess.client.trusted_subject_issuer.updated"
	ActionCredentialCreated             = "agentaccess.credential.created"
	ActionCredentialRotated             = "agentaccess.credential.rotated"
	ActionCredentialRevoked             = "agentaccess.credential.revoked"
	ActionGrantCreated                  = "agentaccess.grant.created"
	ActionGrantRevoked                  = "agentaccess.grant.revoked"
	ActionExternalSubjectStatusChanged  = "agentaccess.external_subject.status.changed"
	ActionExternalSubjectDisplayUpdated = "agentaccess.external_subject.display_ref.updated"
	ActionAuthenticationFailed          = "agentaccess.authentication.failed"
	ActionAuthorizationDenied           = "agentaccess.authorization.denied"
)

type ManagementAuditEvent struct {
	Action, WorkspaceID, ActorID string
	ResourceType, ResourceID     string
	Before, After, Metadata      map[string]any
	SecurityChange               *SecurityChangeEvent
}

type AuthenticationFailureAuditEvent struct {
	WorkspaceID, ClientID string
	AuthMethod            ClientAuthMethod
	ErrorCode             string
	SourceIP, UserAgent   string
}

type AuthorizationDenialAuditEvent struct {
	WorkspaceID, AgentID, ServicePrincipalID, PublicClientID string
	Action, RequiredScope, Reason                            string
	ResourceType, ResourceID                                 string
}

type ManagementAuditSink interface {
	RecordAgentAccessManagement(context.Context, *sql.Tx, ManagementAuditEvent) error
}

type AuthenticationAuditSink interface {
	RecordAgentAccessAuthenticationFailure(context.Context, AuthenticationFailureAuditEvent) error
}

type AuthorizationAuditSink interface {
	RecordAgentAccessAuthorizationDenied(context.Context, AuthorizationDenialAuditEvent) error
}

type SecurityChangeEvent struct {
	WorkspaceID, AgentID, ClientID, GrantID string
	SecurityVersion                         int64
}

type SecurityChangePublisher interface {
	PublishAgentAccessSecurityChange(context.Context, SecurityChangeEvent) error
}

type ManagementOption func(*ManagementService) error

func WithManagementAudit(sink ManagementAuditSink) ManagementOption {
	return func(service *ManagementService) error {
		if sink == nil {
			return errors.New("Agent Access management audit sink is required")
		}
		service.audit = sink
		return nil
	}
}

func WithSecurityChangePublisher(publisher SecurityChangePublisher) ManagementOption {
	return func(service *ManagementService) error {
		if publisher == nil {
			return errors.New("Agent Access security change publisher is required")
		}
		service.securityChanges = publisher
		return nil
	}
}
