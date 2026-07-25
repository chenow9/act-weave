package agentaccess_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/audit"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

type securityChangeCapture struct {
	events []agentaccess.SecurityChangeEvent
}

func (capture *securityChangeCapture) PublishAgentAccessSecurityChange(
	_ context.Context,
	event agentaccess.SecurityChangeEvent,
) error {
	capture.events = append(capture.events, event)
	return nil
}

func TestAgentAccessAudit(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, err := agentaccess.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := audit.NewRepository(db)
	outbox, _ := audit.NewOutboxRepository(db)
	builder, _ := audit.NewBuilder(audit.DefaultInlineDetailBytes)
	recorder, err := audit.NewRecorder(events, outbox, builder)
	if err != nil {
		t.Fatal(err)
	}
	changes := &securityChangeCapture{}
	service, err := agentaccess.NewManagementService(
		repository, bytes.Repeat([]byte{0x58}, 32),
		agentaccess.WithManagementAudit(recorder),
		agentaccess.WithSecurityChangePublisher(changes),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequestContext(context.Background(), audit.RequestContext{
		RequestID: "request-agent-access-audit", TraceID: "trace-agent-access-audit",
		SourceIP: "192.0.2.44", UserAgent: "ActWeave audit acceptance",
	})

	registration, err := service.RegisterClient(ctx, agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, ActorID: repositoryOwnerID,
		Name: "Audited business platform", AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
		AllowedCORSOrigins: []string{"https://audit.example.test"}, TokenTTLSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.AddCredential(ctx, agentaccess.AddCredentialInput{
		WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
		ActorID: repositoryOwnerID, Type: agentaccess.CredentialTypeClientSecret,
		ReplacesCredentialID:        registration.Credential.ID,
		ReplacesExpectedLockVersion: registration.Credential.LockVersion,
		Overlap:                     time.Hour,
	})
	if err != nil || issued.OneTimeSecret == "" {
		t.Fatalf("rotate credential=%+v err=%v", issued, err)
	}
	grant, err := service.GrantAgent(ctx, agentaccess.CreateGrantInput{
		ID: uuid.NewString(), WorkspaceID: repositoryWorkspaceID,
		ClientID: registration.Client.ID, AgentID: repositoryAgentID,
		Scopes: []agentaccess.AgentScope{
			agentaccess.ScopeAgentRead, agentaccess.ScopeRunCreate, agentaccess.ScopeEventRead,
		},
		Policy: agentaccess.GrantPolicy{}, ValidFrom: time.Now().UTC().Add(-time.Minute),
		ActorID: repositoryOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.RevokeGrant(
		ctx, repositoryWorkspaceID, registration.Client.ID, repositoryAgentID,
		grant.ID, repositoryOwnerID, grant.LockVersion,
	); err != nil {
		t.Fatal(err)
	}
	disabled, _, err := service.SetClientStatus(
		ctx, repositoryWorkspaceID, registration.Client.ID, repositoryOwnerID,
		agentaccess.StatusDisabled, registration.Client.LockVersion,
	)
	if err != nil || disabled.Status != agentaccess.StatusDisabled {
		t.Fatalf("disable=%+v err=%v", disabled, err)
	}
	replaced, err := repository.GetCredential(
		ctx, repositoryWorkspaceID, registration.Client.ID, registration.Credential.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.RevokeCredential(
		ctx, repositoryWorkspaceID, registration.Client.ID, replaced.ID,
		repositoryOwnerID, replaced.LockVersion,
	); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentAccessAuthenticationFailure(ctx,
		agentaccess.AuthenticationFailureAuditEvent{
			WorkspaceID: repositoryWorkspaceID, ClientID: registration.Client.ID,
			AuthMethod: agentaccess.ClientAuthMethodSecretBasic, ErrorCode: "CREDENTIAL_REJECTED",
			SourceIP: "198.51.100.9", UserAgent: "untrusted-client",
		}); err != nil {
		t.Fatal(err)
	}
	deniedResourceID := uuid.NewString()
	if err := recorder.RecordAgentAccessAuthorizationDenied(ctx,
		agentaccess.AuthorizationDenialAuditEvent{
			WorkspaceID: repositoryWorkspaceID, AgentID: repositoryAgentID,
			ServicePrincipalID: repositoryPrincipalID, PublicClientID: repositoryPublicClient,
			Action: "run.read", RequiredScope: "run:read", Reason: "SUBJECT_OWNERSHIP_DENIED",
			ResourceType: "RUN", ResourceID: deniedResourceID,
		}); err != nil {
		t.Fatal(err)
	}

	wantActions := map[string]int{
		agentaccess.ActionClientCreated:        1,
		agentaccess.ActionCredentialCreated:    1,
		agentaccess.ActionCredentialRotated:    1,
		agentaccess.ActionGrantCreated:         1,
		agentaccess.ActionGrantRevoked:         1,
		agentaccess.ActionClientStatusChanged:  1,
		agentaccess.ActionCredentialRevoked:    1,
		agentaccess.ActionAuthenticationFailed: 1,
		agentaccess.ActionAuthorizationDenied:  1,
	}
	rows, err := db.Query(`
		SELECT action,count(*) FROM audit_events
		WHERE workspace_id=$1 GROUP BY action
	`, repositoryWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotActions := make(map[string]int)
	for rows.Next() {
		var action string
		var count int
		if err := rows.Scan(&action, &count); err != nil {
			t.Fatal(err)
		}
		gotActions[action] = count
	}
	for action, count := range wantActions {
		if gotActions[action] != count {
			t.Fatalf("audit action %s count=%d want=%d all=%v", action, gotActions[action], count, gotActions)
		}
	}
	var contextualEvents int
	if err := db.QueryRow(`
		SELECT count(*) FROM audit_events
		WHERE workspace_id=$1 AND request_id='request-agent-access-audit'
		 AND trace_id='trace-agent-access-audit' AND source_ip='192.0.2.44'
		 AND actor_id=$2
	`, repositoryWorkspaceID, repositoryOwnerID).Scan(&contextualEvents); err != nil {
		t.Fatal(err)
	}
	if contextualEvents != 7 {
		t.Fatalf("management audit context rows=%d want=7", contextualEvents)
	}
	var denialActorType, denialActorID, denialResult, denialResourceID, denialMetadata string
	if err := db.QueryRow(`
		SELECT actor_type,actor_id::text,result,resource_id::text,metadata::text
		FROM audit_events WHERE workspace_id=$1 AND action=$2
	`, repositoryWorkspaceID, agentaccess.ActionAuthorizationDenied).Scan(
		&denialActorType, &denialActorID, &denialResult, &denialResourceID, &denialMetadata,
	); err != nil {
		t.Fatal(err)
	}
	if denialActorType != "SERVICE_PRINCIPAL" || denialActorID != repositoryPrincipalID ||
		denialResult != "DENIED" || denialResourceID != deniedResourceID ||
		!strings.Contains(denialMetadata, "SUBJECT_OWNERSHIP_DENIED") ||
		!strings.Contains(denialMetadata, "run:read") {
		t.Fatalf("unexpected authorization denial audit: actor=%s/%s result=%s resource=%s metadata=%s",
			denialActorType, denialActorID, denialResult, denialResourceID, denialMetadata)
	}
	var securityOutbox int
	if err := db.QueryRow(`
		SELECT count(*) FROM outbox_events
		WHERE workspace_id=$1 AND event_type='agentaccess.security.changed'
		 AND schema_version='agent_access.security_change.v1'
	`, repositoryWorkspaceID).Scan(&securityOutbox); err != nil {
		t.Fatal(err)
	}
	if securityOutbox != 3 || len(changes.events) != 3 {
		t.Fatalf("security notifications durable=%d immediate=%+v", securityOutbox, changes.events)
	}
	for index, version := range []int64{2, 3, 4} {
		if changes.events[index].WorkspaceID != repositoryWorkspaceID ||
			changes.events[index].ClientID != registration.Client.ID ||
			changes.events[index].SecurityVersion != version {
			t.Fatalf("security change[%d]=%+v want version=%d", index, changes.events[index], version)
		}
	}

	var persisted string
	if err := db.QueryRow(`
		SELECT coalesce(string_agg(changes::text || metadata::text,''),'')
		FROM audit_events WHERE workspace_id=$1
	`, repositoryWorkspaceID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	var outboxPayload string
	if err := db.QueryRow(`
		SELECT coalesce(string_agg(payload::text,''),'')
		FROM outbox_events WHERE workspace_id=$1
	`, repositoryWorkspaceID).Scan(&outboxPayload); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		registration.OneTimeSecret, issued.OneTimeSecret, "privateKey", "secretHash", "jwkThumbprint",
	} {
		if forbidden != "" && (strings.Contains(persisted, forbidden) || strings.Contains(outboxPayload, forbidden)) {
			t.Fatalf("audit/outbox leaked forbidden material %q", forbidden)
		}
	}
	if !strings.Contains(persisted, repositoryAgentID) || !strings.Contains(persisted, "run:create") {
		t.Fatalf("grant audit cannot answer Agent/Scope: %s", persisted)
	}
}

type failingManagementAudit struct{}

func (failingManagementAudit) RecordAgentAccessManagement(
	context.Context, *sql.Tx, agentaccess.ManagementAuditEvent,
) error {
	return errors.New("audit unavailable")
}

func TestAgentAccessAuditFailureRollsBackManagement(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertRepositoryFixtures(t, db)
	repository, _ := agentaccess.NewRepository(db)
	service, err := agentaccess.NewManagementService(
		repository, bytes.Repeat([]byte{0x59}, 32),
		agentaccess.WithManagementAudit(failingManagementAudit{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.RegisterClient(context.Background(), agentaccess.RegisterClientInput{
		WorkspaceID: repositoryWorkspaceID, ActorID: repositoryOwnerID,
		Name: "Must roll back", AuthMethod: agentaccess.ClientAuthMethodSecretBasic,
	})
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("registration error=%v", err)
	}
	var count int
	if err := db.QueryRow(`
		SELECT count(*) FROM agent_access_clients
		WHERE workspace_id=$1 AND name='Must roll back'
	`, repositoryWorkspaceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("audit failure left %d Client rows", count)
	}
}
