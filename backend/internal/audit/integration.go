package audit

import (
	"context"
	"database/sql"
	"strings"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"
)

// RecordAgentAccessManagement writes the external identity management fact in
// the caller's transaction. Security-version changes use a dedicated durable
// outbox payload that contains only scope identifiers and the new version.
func (recorder *Recorder) RecordAgentAccessManagement(
	ctx context.Context,
	tx *sql.Tx,
	input agentaccess.ManagementAuditEvent,
) error {
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	record := ManagementEventInput{
		EventID: eventID, WorkspaceID: input.WorkspaceID,
		ActorType: "USER", ActorID: input.ActorID, ActorDisplay: "Agent Access manager",
		Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Result: "SUCCESS", Before: input.Before, After: input.After, Metadata: input.Metadata,
	}
	if change := input.SecurityChange; change != nil {
		record.OutboxEventType = "agentaccess.security.changed"
		record.OutboxSchema = "agent_access.security_change.v1"
		record.OutboxIdempotency = "agent-access-security:" + eventID
		record.OutboxPayload = map[string]any{
			"eventId": eventID, "workspaceId": change.WorkspaceID,
			"agentId": change.AgentID, "clientId": change.ClientID,
			"grantId": change.GrantID, "securityVersion": change.SecurityVersion,
		}
	}
	_, err = recorder.RecordInTransaction(ctx, tx, record)
	return err
}

// RecordAgentAccessAuthenticationFailure is the M6 authentication audit
// boundary. It never accepts a presented Secret, assertion, JWT, JWK, or hash.
func (recorder *Recorder) RecordAgentAccessAuthenticationFailure(
	ctx context.Context,
	input agentaccess.AuthenticationFailureAuditEvent,
) error {
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	request := requestContextFrom(ctx)
	if request.SourceIP == "" {
		request.SourceIP = strings.TrimSpace(input.SourceIP)
	}
	if request.UserAgent == "" {
		request.UserAgent = strings.TrimSpace(input.UserAgent)
	}
	ctx = WithRequestContext(ctx, request)
	_, err = recorder.Record(ctx, ManagementEventInput{
		EventID: eventID, WorkspaceID: input.WorkspaceID,
		ActorType: "SYSTEM", ActorDisplay: "Agent Access authentication service",
		Action:       agentaccess.ActionAuthenticationFailed,
		ResourceType: "AGENT_ACCESS_CLIENT", ResourceID: input.ClientID,
		Result: "FAILURE", Metadata: map[string]any{
			"authMethod": string(input.AuthMethod), "errorCode": input.ErrorCode,
		},
	})
	return err
}

// RecordAgentAccessAuthorizationDenied records only verified Principal and
// scoped resource identifiers. It cannot receive a Bearer Token, Client
// Credential, JWT claim set, or request payload.
func (recorder *Recorder) RecordAgentAccessAuthorizationDenied(
	ctx context.Context,
	input agentaccess.AuthorizationDenialAuditEvent,
) error {
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	workspaceID := input.WorkspaceID
	if !validAuditUUID(workspaceID) {
		workspaceID = ""
	}
	resourceID := input.ResourceID
	if !validAuditUUID(resourceID) {
		resourceID = ""
	}
	resourceType := strings.TrimSpace(input.ResourceType)
	if resourceType == "" {
		resourceType = "AAP_AUTHORIZATION"
	}
	_, err = recorder.Record(ctx, ManagementEventInput{
		EventID: eventID, WorkspaceID: workspaceID,
		ActorType: "SERVICE_PRINCIPAL", ActorID: input.ServicePrincipalID,
		ActorDisplay: "Agent Access service principal",
		Action:       agentaccess.ActionAuthorizationDenied,
		ResourceType: resourceType, ResourceID: resourceID, Result: "DENIED",
		Metadata: map[string]any{
			"agentId": input.AgentID, "clientId": input.PublicClientID,
			"requestedAction": input.Action, "requiredScope": input.RequiredScope,
			"reason": input.Reason,
		},
	})
	return err
}

// RecordAuthentication implements authn.AuthenticationAuditSink without making
// the authentication package depend on the audit implementation.
func (recorder *Recorder) RecordAuthentication(
	ctx context.Context,
	input authn.AuthenticationAuditEvent,
) error {
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	request := requestContextFrom(ctx)
	if request.SourceIP == "" {
		request.SourceIP = strings.TrimSpace(input.SourceIP)
	}
	if request.UserAgent == "" {
		request.UserAgent = strings.TrimSpace(input.UserAgent)
	}
	ctx = WithRequestContext(ctx, request)
	actorType, actorID, actorDisplay := "SYSTEM", "", "Authentication service"
	resourceType, resourceID := "AUTHENTICATION", ""
	if validAuditUUID(input.UserID) {
		actorType, actorID, actorDisplay = "USER", input.UserID, "Authenticated user"
		resourceType, resourceID = "USER", input.UserID
	}
	_, err = recorder.Record(ctx, ManagementEventInput{
		EventID: eventID, ActorType: actorType, ActorID: actorID, ActorDisplay: actorDisplay,
		Action: ActionAuthenticationLogin, ResourceType: resourceType, ResourceID: resourceID,
		Result: input.Result, Metadata: map[string]any{
			"subjectSha256": input.SubjectHash, "errorCode": input.ErrorCode,
		},
	})
	return err
}

// RecordIdentityManagement implements authn.IdentityManagementAuditSink. The
// event intentionally carries only allowlisted user/profile state; passwords,
// credential hashes, tokens, and session identifiers never cross this boundary.
func (recorder *Recorder) RecordIdentityManagement(
	ctx context.Context,
	input authn.IdentityManagementAuditEvent,
) error {
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	_, err = recorder.Record(ctx, ManagementEventInput{
		EventID: eventID, ActorType: "USER", ActorID: input.ActorUserID,
		ActorDisplay: "Platform administrator", Action: input.Action,
		ResourceType: "USER", ResourceID: input.TargetUserID, Result: "SUCCESS",
		Before: input.Before, After: input.After, Metadata: input.Metadata,
	})
	return err
}

// RecordAuthorizationDenied implements authz.DenialAuditSink. Unknown scope
// denials deliberately omit workspace_id so auditing cannot create an
// existence oracle or violate the workspace foreign key.
func (recorder *Recorder) RecordAuthorizationDenied(
	ctx context.Context,
	input authz.AuthorizationDenialEvent,
) error {
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	workspaceID := input.WorkspaceID
	if input.Reason == authz.DenialScopeNotVisible || !validAuditUUID(workspaceID) {
		workspaceID = ""
	}
	actorType, actorID, actorDisplay := "SYSTEM", "", "Authorization service"
	if validAuditUUID(input.UserID) {
		actorType, actorID, actorDisplay = "USER", input.UserID, "Workspace user"
	}
	resourceID := input.WorkspaceID
	if !validAuditUUID(resourceID) {
		resourceID = ""
	}
	_, err = recorder.Record(ctx, ManagementEventInput{
		EventID: eventID, WorkspaceID: workspaceID,
		ActorType: actorType, ActorID: actorID, ActorDisplay: actorDisplay,
		Action: ActionAuthorizationDenied, ResourceType: "WORKSPACE", ResourceID: resourceID,
		Result: "DENIED", Metadata: map[string]any{
			"reason": string(input.Reason), "requestedAction": string(input.Action),
			"role": string(input.Role),
		},
	})
	return err
}

// AppendToolReleasePublished implements tool.PublishEventWriter in the same
// transaction that makes the immutable release visible.
func (recorder *Recorder) AppendToolReleasePublished(
	ctx context.Context,
	tx *sql.Tx,
	event tool.ToolReleasePublishedEvent,
) error {
	action := ActionToolReleasePublished
	actorDisplay := "Tool publisher"
	metadata := map[string]any{
		"capabilityId": event.CapabilityID, "toolVersionId": event.ToolVersionID,
		"toolTestId": event.ToolTestID, "releaseNo": event.ReleaseNo, "checksum": event.Checksum,
	}
	outboxPayload := map[string]any{
		"eventId": event.ID, "workspaceId": event.WorkspaceID,
		"capabilityId": event.CapabilityID, "toolVersionId": event.ToolVersionID,
		"toolTestId": event.ToolTestID, "releaseId": event.ReleaseID,
		"releaseNo": event.ReleaseNo, "checksum": event.Checksum,
	}
	if event.Force {
		action = ActionToolReleaseForcePublished
		actorDisplay = "Tool force publisher"
		metadata["force"] = true
		metadata["forceReason"] = event.ForceReason
		outboxPayload["force"] = true
		outboxPayload["forceReason"] = event.ForceReason
	}
	_, err := recorder.RecordInTransaction(ctx, tx, ManagementEventInput{
		EventID: event.ID, OccurredAt: event.OccurredAt, WorkspaceID: event.WorkspaceID,
		ActorType: "USER", ActorID: event.PublishedBy, ActorDisplay: actorDisplay,
		Action: action, ResourceType: "CAPABILITY_RELEASE", ResourceID: event.ReleaseID,
		Result: "SUCCESS", Metadata: metadata,
		OutboxEventType: event.Type, OutboxSchema: "tool.release.v1",
		OutboxIdempotency: "tool-release:" + event.ReleaseID,
		OutboxPayload: outboxPayload,
	})
	return err
}

// AppendWorkflowReleasePublished implements workflow.PublishEventWriter.
func (recorder *Recorder) AppendWorkflowReleasePublished(
	ctx context.Context,
	tx *sql.Tx,
	event workflow.WorkflowReleasePublishedEvent,
) error {
	_, err := recorder.RecordInTransaction(ctx, tx, ManagementEventInput{
		EventID: event.ID, OccurredAt: event.OccurredAt, WorkspaceID: event.WorkspaceID,
		ActorType: "USER", ActorID: event.PublishedBy, ActorDisplay: "Workflow publisher",
		Action: ActionWorkflowReleasePublished, ResourceType: "CAPABILITY_RELEASE", ResourceID: event.ReleaseID,
		Result: "SUCCESS", Metadata: map[string]any{
			"capabilityId": event.CapabilityID, "compilationId": event.CompilationID,
			"trialId": event.TrialID, "revisionId": event.RevisionID,
			"revisionNo": event.RevisionNo, "releaseNo": event.ReleaseNo, "planHash": event.PlanHash,
		},
		OutboxEventType: event.Type, OutboxSchema: "workflow.release.v1",
		OutboxIdempotency: "workflow-release:" + event.ReleaseID,
		OutboxPayload: map[string]any{
			"eventId": event.ID, "workspaceId": event.WorkspaceID,
			"capabilityId": event.CapabilityID, "compilationId": event.CompilationID,
			"trialId": event.TrialID, "revisionId": event.RevisionID,
			"revisionNo": event.RevisionNo, "releaseId": event.ReleaseID,
			"releaseNo": event.ReleaseNo, "planHash": event.PlanHash,
		},
	})
	return err
}

// AppendWorkflowRevisionActivated implements workflow.ActivationEventWriter and
// preserves the domain event type for activation versus rollback.
func (recorder *Recorder) AppendWorkflowRevisionActivated(
	ctx context.Context,
	tx *sql.Tx,
	event workflow.WorkflowRevisionActivatedEvent,
) error {
	_, err := recorder.RecordInTransaction(ctx, tx, ManagementEventInput{
		EventID: event.ID, OccurredAt: event.OccurredAt, WorkspaceID: event.WorkspaceID,
		ActorType: "USER", ActorID: event.ActivatedBy, ActorDisplay: "Workflow publisher",
		Action: event.Type, ResourceType: "WORKFLOW_REVISION", ResourceID: event.TargetRevisionID,
		Result: "SUCCESS", Before: optionalIDs(event.PreviousRevisionID, event.PreviousReleaseID),
		After: map[string]any{
			"revisionId": event.TargetRevisionID, "releaseId": event.TargetReleaseID,
		},
		Metadata: map[string]any{
			"capabilityId": event.CapabilityID, "revisionNo": event.TargetRevisionNo,
			"releaseNo": event.TargetReleaseNo,
		},
		OutboxEventType: event.Type, OutboxSchema: "workflow.activation.v1",
		OutboxIdempotency: "workflow-activation:" + event.ID,
		OutboxPayload: map[string]any{
			"eventId": event.ID, "workspaceId": event.WorkspaceID,
			"capabilityId": event.CapabilityID, "targetRevisionId": event.TargetRevisionID,
			"targetReleaseId": event.TargetReleaseID,
		},
	})
	return err
}

func optionalIDs(revisionID, releaseID *string) map[string]any {
	values := make(map[string]any)
	if revisionID != nil {
		values["revisionId"] = *revisionID
	}
	if releaseID != nil {
		values["releaseId"] = *releaseID
	}
	return values
}

// AppendChatAuditEvent implements chat.AuditSink in the caller's chat or
// confirmation transaction. No message, prompt, input, output, or resume token
// crosses this boundary.
func (recorder *Recorder) AppendChatAuditEvent(
	ctx context.Context,
	tx *sql.Tx,
	event chat.AuditEvent,
) error {
	request := requestContextFrom(ctx)
	if request.TraceID == "" {
		request.TraceID = strings.TrimSpace(event.TraceID)
	}
	ctx = WithRequestContext(ctx, request)
	display := "Runtime service"
	if event.ActorType == "USER" {
		display = "Workspace user"
	}
	_, err := recorder.RecordInTransaction(ctx, tx, ManagementEventInput{
		EventID: event.EventID, WorkspaceID: event.WorkspaceID,
		ActorType: event.ActorType, ActorID: event.ActorID, ActorDisplay: display,
		Action: event.Action, ResourceType: event.ResourceType, ResourceID: event.ResourceID,
		Result: event.Result, Metadata: event.Metadata,
	})
	return err
}
