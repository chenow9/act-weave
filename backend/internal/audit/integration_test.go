package audit_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"

	"github.com/google/uuid"
)

func TestPhaseOneAuditIntegrationsAreCorrelatedAndTransactional(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertAuditMigrationFixtures(t, db)
	repository, _ := audit.NewRepository(db)
	outbox, _ := audit.NewOutboxRepository(db)
	builder, _ := audit.NewBuilder(0, "must-never-appear")
	recorder, err := audit.NewRecorder(repository, outbox, builder)
	if err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequestContext(context.Background(), audit.RequestContext{
		RequestID: "request-phase-one-1", TraceID: "trace-phase-one-1",
		SourceIP: "2001:db8::21", UserAgent: "phase-one-audit-test",
	})

	for _, input := range []audit.ManagementEventInput{
		phaseOneManagementInput(audit.ActionWorkspaceMemberChanged, "WORKSPACE_MEMBER", "SUCCESS"),
		phaseOneManagementInput(audit.ActionAgentChanged, "AGENT", "SUCCESS"),
		phaseOneManagementInput(audit.ActionConfigurationChanged, "MODEL_CONFIG", "FAILURE"),
	} {
		if _, err := recorder.Record(ctx, input); err != nil {
			t.Fatalf("record management event %s: %v", input.Action, err)
		}
	}

	if err := recorder.RecordAuthentication(ctx, authn.AuthenticationAuditEvent{
		Action: audit.ActionAuthenticationLogin, SubjectHash: strings.Repeat("a", 64),
		Result: "FAILURE", ErrorCode: "INVALID_CREDENTIALS",
		SourceIP: "203.0.113.21", UserAgent: "ignored-because-context-wins",
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAuthentication(ctx, authn.AuthenticationAuditEvent{
		Action: audit.ActionAuthenticationLogin, UserID: auditMigrationUserID,
		SubjectHash: strings.Repeat("b", 64), Result: "SUCCESS",
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAuthorizationDenied(ctx, authz.AuthorizationDenialEvent{
		UserID: auditMigrationUserID, WorkspaceID: auditMigrationWorkspaceID,
		Action: authz.ActionDelete, Reason: authz.DenialRoleInsufficient,
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordIdentityManagement(ctx, authn.IdentityManagementAuditEvent{
		Action: authn.ActionUserPasswordReset, ActorUserID: auditMigrationUserID,
		TargetUserID: auditMigrationUserID,
		Metadata:     map[string]any{"temporaryPassword": "must-never-appear", "sessionsRevoked": true},
	}); err != nil {
		t.Fatal(err)
	}

	withTransaction(t, db, func(tx *sql.Tx) error {
		return recorder.AppendToolReleasePublished(ctx, tx, tool.ToolReleasePublishedEvent{
			ID: uuid.NewString(), Type: "tool.release.published",
			WorkspaceID: auditMigrationWorkspaceID, CapabilityID: uuid.NewString(),
			ToolVersionID: uuid.NewString(), ToolTestID: uuid.NewString(),
			ReleaseID: uuid.NewString(), ReleaseNo: 1, Checksum: strings.Repeat("c", 64),
			PublishedBy: auditMigrationUserID, SchemaVersion: 1,
		})
	})
	withTransaction(t, db, func(tx *sql.Tx) error {
		return recorder.AppendWorkflowReleasePublished(ctx, tx, workflow.WorkflowReleasePublishedEvent{
			ID: uuid.NewString(), Type: "workflow.release.published",
			WorkspaceID: auditMigrationWorkspaceID, CapabilityID: uuid.NewString(),
			CompilationID: uuid.NewString(), TrialID: uuid.NewString(), RevisionID: uuid.NewString(),
			RevisionNo: 1, ReleaseID: uuid.NewString(), ReleaseNo: 1,
			PlanHash: strings.Repeat("d", 64), PublishedBy: auditMigrationUserID, SchemaVersion: 1,
		})
	})
	withTransaction(t, db, func(tx *sql.Tx) error {
		return recorder.AppendWorkflowRevisionActivated(ctx, tx, workflow.WorkflowRevisionActivatedEvent{
			ID: uuid.NewString(), Type: "workflow.release.activated",
			WorkspaceID: auditMigrationWorkspaceID, CapabilityID: uuid.NewString(),
			TargetRevisionID: uuid.NewString(), TargetRevisionNo: 2,
			TargetReleaseID: uuid.NewString(), TargetReleaseNo: 2,
			ActivatedBy: auditMigrationUserID, SchemaVersion: 1,
		})
	})
	for _, event := range []chat.AuditEvent{
		{
			EventID: uuid.NewString(), WorkspaceID: auditMigrationWorkspaceID,
			ActorType: "USER", ActorID: auditMigrationUserID, Action: audit.ActionChatMessageSent,
			ResourceType: "CHAT_MESSAGE", ResourceID: uuid.NewString(), Result: "SUCCESS",
			TraceID:  "event-trace-does-not-overwrite-context",
			Metadata: map[string]any{"contentSha256": strings.Repeat("e", 64), "content": "must-never-appear"},
		},
		{
			EventID: uuid.NewString(), WorkspaceID: auditMigrationWorkspaceID,
			ActorType: "SYSTEM", ActorID: uuid.NewString(), Action: audit.ActionRunFailed,
			ResourceType: "AGENT_RUN", ResourceID: uuid.NewString(), Result: "FAILURE",
			Metadata: map[string]any{"errorCode": "UPSTREAM_UNAVAILABLE"},
		},
	} {
		withTransaction(t, db, func(tx *sql.Tx) error {
			return recorder.AppendChatAuditEvent(ctx, tx, event)
		})
	}

	const expected = 12
	var events, outboxEvents, correlated int
	if err := db.QueryRow(`SELECT count(*),count(*) FILTER (
		WHERE request_id='request-phase-one-1' AND trace_id='trace-phase-one-1'
	) FROM audit_events`).Scan(&events, &correlated); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&outboxEvents); err != nil {
		t.Fatal(err)
	}
	if events != expected || correlated != expected || outboxEvents != expected {
		t.Fatalf("audit/correlated/outbox counts=%d/%d/%d want=%d", events, correlated, outboxEvents, expected)
	}
	for result, minimum := range map[string]int{"SUCCESS": 1, "FAILURE": 1, "DENIED": 1} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE result=$1`, result).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count < minimum {
			t.Fatalf("result %s count=%d", result, count)
		}
	}
	var serialized, future string
	if err := db.QueryRow(`SELECT string_agg(changes::text || metadata::text,' ') FROM audit_events`).Scan(&serialized); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(serialized, "must-never-appear") {
		t.Fatalf("sensitive/raw content entered audit event: %s", serialized)
	}
	if err := db.QueryRow(`SELECT COALESCE(string_agg(action,','),'') FROM audit_events
		WHERE action LIKE 'memory.%' OR action LIKE 'a2a.%' OR action LIKE 'sandbox.%'`).Scan(&future); err != nil {
		t.Fatal(err)
	}
	if future != "" {
		t.Fatalf("future-domain audit producers present: %s", future)
	}
}

func phaseOneManagementInput(action, resourceType, result string) audit.ManagementEventInput {
	return audit.ManagementEventInput{
		EventID: uuid.NewString(), WorkspaceID: auditMigrationWorkspaceID,
		ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Workspace administrator",
		Action: action, ResourceType: resourceType, ResourceID: uuid.NewString(), Result: result,
		Before: map[string]any{"status": "OLD"}, After: map[string]any{"status": "NEW"},
		Metadata: map[string]any{"errorCode": map[bool]string{true: "CONFIGURATION_REJECTED"}[result == "FAILURE"]},
	}
}

func withTransaction(t *testing.T, db *sql.DB, callback func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := callback(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
