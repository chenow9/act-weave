package execution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

const (
	externalExecutionPrincipalID = "b28f1f2e-7b5a-7c3d-8e9f-123456789001"
	externalExecutionClientID    = "b28f1f2e-7b5a-7c3d-8e9f-123456789002"
	externalExecutionSubjectID   = "b28f1f2e-7b5a-7c3d-8e9f-123456789003"
	externalExecutionGrantID     = "b28f1f2e-7b5a-7c3d-8e9f-123456789004"
	externalExecutionRunID       = "b28f1f2e-7b5a-7c3d-8e9f-123456789005"
	externalExecutionWorkflowID  = "b28f1f2e-7b5a-7c3d-8e9f-123456789006"
	externalExecutionToolID      = "b28f1f2e-7b5a-7c3d-8e9f-123456789007"
	externalExecutionStaleRunID  = "b28f1f2e-7b5a-7c3d-8e9f-123456789008"
	externalExecutionFreshRunID  = "b28f1f2e-7b5a-7c3d-8e9f-123456789009"
	externalExecutionUserRunID   = "b28f1f2e-7b5a-7c3d-8e9f-12345678900a"
	externalExecutionSystemRunID = "b28f1f2e-7b5a-7c3d-8e9f-12345678900b"
	executionRuntimeSystemID     = "00000000-0000-0000-0000-000000000001"
)

func TestExternalPrincipalSnapshots(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("expected Interaction decision binding migration 61, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	insertExternalExecutionFixtures(t, db)

	actor := principal.Ref{WorkspaceID: executionWorkspaceID, Type: principal.TypeServicePrincipal, ID: externalExecutionPrincipalID}
	subject := principal.Ref{WorkspaceID: executionWorkspaceID, Type: principal.TypeExternalSubject, ID: externalExecutionSubjectID}
	identity, err := principal.NewInvocationIdentity(actor, &subject)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: executionWorkspaceID,
		AgentID: executionAgentID, ClientID: externalExecutionClientID,
		ServicePrincipalID: externalExecutionPrincipalID, SubjectID: externalExecutionSubjectID,
		GrantID: externalExecutionGrantID, GrantVersion: 1, AgentPolicyVersion: 1,
	}).ExecutionPrincipalSnapshot(identity)
	if err != nil {
		t.Fatal(err)
	}

	runs, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run, err := runs.StartAgentRun(ctx, externalAgentRunInput(externalExecutionRunID, &snapshot))
	if err != nil {
		t.Fatalf("start external AgentRun: %v", err)
	}
	assertExecutionSnapshot(t, run.PrincipalSnapshotVersion, run.PrincipalSnapshot, snapshot)
	assertAuthorizationEnvelope(t, run.AuthorizationSnapshot, snapshot, "run:create")

	// Current Grant/Agent versions can move while a committed Run continues to
	// spawn children from its fixed authorization snapshot.
	if _, err := db.Exec(`
		UPDATE agent_access_grants SET lock_version=2,updated_at=clock_timestamp() WHERE id=$1
	`, externalExecutionGrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE agents SET lock_version=2,updated_at=clock_timestamp() WHERE id=$1
	`, executionAgentID); err != nil {
		t.Fatal(err)
	}
	workflow, err := runs.StartWorkflowExecution(ctx, execution.StartWorkflowExecutionInput{
		ID: externalExecutionWorkflowID, WorkspaceID: executionWorkspaceID,
		WorkflowID: executionWorkflowID, RevisionID: executionRevisionID,
		AgentRunID: externalExecutionRunID, TriggerType: "AGENT",
		TriggeredByType: "SERVICE_PRINCIPAL", TriggeredByID: externalExecutionPrincipalID,
		TraceID: "trace-external-workflow", SnapshotSchemaVersion: "run.v1",
		AuthorizationSnapshot: json.RawMessage(`{"action":"workflow.execute"}`),
		InputSummary:          json.RawMessage(`{"source":"agent"}`), PrincipalSnapshot: &snapshot,
	})
	if err != nil {
		t.Fatalf("start child Workflow from fixed snapshot: %v", err)
	}
	assertExecutionSnapshot(t, workflow.PrincipalSnapshotVersion, workflow.PrincipalSnapshot, snapshot)

	invocations, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := invocations.Start(ctx, execution.StartToolInvocationInput{
		ID: externalExecutionToolID, WorkspaceID: executionWorkspaceID,
		ToolID: invocationToolID, ToolVersionID: invocationVersionID,
		CapabilityReleaseID: invocationReleaseID, ProviderID: invocationProviderID,
		ConnectionID: invocationConnectionID, AgentRunID: externalExecutionRunID,
		WorkflowExecutionID: externalExecutionWorkflowID,
		ActorType:           "SERVICE_PRINCIPAL", ActorID: externalExecutionPrincipalID,
		TraceID: "trace-external-tool", IdempotencyKey: "external-snapshot-tool",
		InputSummary:          json.RawMessage(`{"orderId":"A-1"}`),
		PrincipalSnapshot:     &snapshot,
		AuthorizationSnapshot: json.RawMessage(`{"action":"tool.execute"}`),
	})
	if err != nil || !tool.Created {
		t.Fatalf("start child Tool from fixed snapshot: %+v %v", tool, err)
	}
	assertExecutionSnapshot(t, tool.Invocation.PrincipalSnapshotVersion, tool.Invocation.PrincipalSnapshot, snapshot)
	assertAuthorizationEnvelope(t, tool.Invocation.AuthorizationSnapshot, snapshot, "tool.execute")

	if _, err := runs.StartAgentRun(ctx, externalAgentRunInput(externalExecutionStaleRunID, &snapshot)); !errors.Is(err, execution.ErrRunInvalid) {
		t.Fatalf("stale top-level Grant/Policy snapshot was accepted: %v", err)
	}
	freshSnapshot, err := principal.NewExecutionSnapshot(
		identity, externalExecutionClientID, externalExecutionGrantID, 2, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := runs.StartAgentRun(ctx, externalAgentRunInput(externalExecutionFreshRunID, &freshSnapshot)); err != nil ||
		fresh.PrincipalSnapshot.GrantVersion != 2 || fresh.PrincipalSnapshot.AgentPolicyVersion != 2 {
		t.Fatalf("fresh top-level authorization snapshot=%+v err=%v", fresh, err)
	}

	userRun, err := runs.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: externalExecutionUserRunID, WorkspaceID: executionWorkspaceID,
		AgentID: executionAgentID, TriggerType: "API", TriggeredByType: "USER",
		TriggeredByID: executionOwnerID, TraceID: "trace-user-principal",
		Snapshots: testAgentRunSnapshots(), AuthorizationSnapshot: json.RawMessage(`{"role":"OWNER"}`),
		InputSummary: json.RawMessage(`{}`),
	})
	if err != nil || userRun.PrincipalSnapshot.Identity.Subject == nil ||
		userRun.PrincipalSnapshot.Identity.Subject.ID != executionOwnerID || userRun.PrincipalSnapshot.ClientID != "" {
		t.Fatalf("internal User compatibility snapshot=%+v err=%v", userRun, err)
	}
	systemRun, err := runs.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: externalExecutionSystemRunID, WorkspaceID: executionWorkspaceID,
		AgentID: executionAgentID, TriggerType: "SCHEDULE", TriggeredByType: "SYSTEM",
		TriggeredByID: executionRuntimeSystemID, TraceID: "trace-system-principal",
		Snapshots: testAgentRunSnapshots(), AuthorizationSnapshot: json.RawMessage(`{"source":"scheduler"}`),
		InputSummary: json.RawMessage(`{}`),
	})
	if err != nil || systemRun.PrincipalSnapshot.Identity.Actor.Type != principal.TypeSystem ||
		systemRun.PrincipalSnapshot.Identity.Subject != nil || systemRun.TriggeredByID == externalExecutionPrincipalID {
		t.Fatalf("SYSTEM and Service Principal were confused: %+v err=%v", systemRun, err)
	}

	for _, mutation := range []struct {
		statement string
		id        string
	}{
		{`UPDATE agent_runs SET subject_id=NULL,lock_version=lock_version+1 WHERE id=$1`, externalExecutionRunID},
		{`UPDATE workflow_executions SET grant_version=99,lock_version=lock_version+1 WHERE id=$1`, externalExecutionWorkflowID},
		{`UPDATE tool_invocations SET authorization_snapshot='{}' WHERE id=$1`, externalExecutionToolID},
	} {
		if _, err := db.Exec(mutation.statement, mutation.id); err == nil {
			t.Fatalf("immutable execution Principal snapshot changed: %s", mutation.statement)
		}
	}
	var fakeUsers int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE id=$1`, externalExecutionPrincipalID).Scan(&fakeUsers); err != nil || fakeUsers != 0 {
		t.Fatalf("execution snapshot manufactured User count=%d err=%v", fakeUsers, err)
	}
}

func TestExternalPrincipalSnapshotsMigration(t *testing.T) {
	t.Skip("historical step migration retired after baseline squash; see migrations_archive")
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("expected migration 49, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	insertToolInvocationDirect(t, db, invocationID, invocationReleaseID,
		executionAgentRunID, invocationWorkflowExecutionID, invocationExecutionStepID,
		"principal-migration")

	version = testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 1 || version.Dirty {
		t.Fatalf("execution Principal migration=%+v", version)
	}
	for table, id := range map[string]string{
		"agent_runs":          executionAgentRunID,
		"workflow_executions": invocationWorkflowExecutionID,
		"tool_invocations":    invocationID,
	} {
		var snapshotVersion, subjectType, subjectID string
		if err := db.QueryRow(`SELECT principal_snapshot_version,subject_type,subject_id::TEXT FROM `+table+` WHERE id=$1`, id).
			Scan(&snapshotVersion, &subjectType, &subjectID); err != nil {
			t.Fatal(err)
		}
		if snapshotVersion != "legacy.v1" || subjectType != "USER" || subjectID != executionOwnerID {
			t.Fatalf("legacy %s snapshot=%s %s/%s", table, snapshotVersion, subjectType, subjectID)
		}
	}
}

func insertExternalExecutionFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO service_principals(id,workspace_id,name,created_by,updated_by)
		VALUES($1,$2,'Execution Snapshot Principal',$3,$3)
	`, externalExecutionPrincipalID, executionWorkspaceID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_clients(
		 id,workspace_id,service_principal_id,client_id,name,auth_method,
		 token_ttl_seconds,created_by,updated_by
		) VALUES($1,$2,$3,'awcl_abcdefghijklmnopqrstuvxyz1234567',
		 'Execution Snapshot Client','client_secret_basic',600,$4,$4)
	`, externalExecutionClientID, executionWorkspaceID, externalExecutionPrincipalID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO external_subjects(id,workspace_id,client_id,issuer,subject_hash,display_ref)
		VALUES($1,$2,$3,'https://execution-identity.example.test',
		 decode(repeat('33',32),'hex'),'ref_execution_subject')
	`, externalExecutionSubjectID, executionWorkspaceID, externalExecutionClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_access_grants(
		 id,workspace_id,client_id,agent_id,scopes,policy,created_by,updated_by
		) VALUES($1,$2,$3,$4,'["run:create"]','{}',$5,$5)
	`, externalExecutionGrantID, executionWorkspaceID, externalExecutionClientID,
		executionAgentID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
}

func externalAgentRunInput(id string, snapshot *principal.ExecutionSnapshot) execution.StartAgentRunInput {
	return execution.StartAgentRunInput{
		ID: id, WorkspaceID: executionWorkspaceID, AgentID: executionAgentID,
		TriggerType: "AAP", TriggeredByType: "SERVICE_PRINCIPAL",
		TriggeredByID: externalExecutionPrincipalID, TraceID: "trace-" + id,
		Snapshots: testAgentRunSnapshots(), PrincipalSnapshot: snapshot,
		AuthorizationSnapshot: json.RawMessage(`{"action":"run:create"}`),
		InputSummary:          json.RawMessage(`{"contentType":"text"}`),
	}
}

func testAgentRunSnapshots() execution.AgentRunSnapshots {
	return execution.AgentRunSnapshots{
		SchemaVersion: "run.v1", Model: json.RawMessage(`{"model":"snapshot-test"}`),
		Capabilities:  json.RawMessage(`{"releases":[]}`),
		ContextPolicy: json.RawMessage(`{"memory":false}`),
	}
}

func assertExecutionSnapshot(
	t *testing.T,
	version string,
	got, want principal.ExecutionSnapshot,
) {
	t.Helper()
	if version != principal.ExecutionAuthorizationSpecV1 || !got.SameBinding(want) {
		t.Fatalf("execution Principal snapshot version=%s got=%+v want=%+v", version, got, want)
	}
}

func assertAuthorizationEnvelope(
	t *testing.T,
	value json.RawMessage,
	want principal.ExecutionSnapshot,
	evidenceAction string,
) {
	t.Helper()
	var envelope struct {
		SpecVersion string `json:"specVersion"`
		WorkspaceID string `json:"workspaceId"`
		Actor       struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"actor"`
		Subject *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"subject"`
		ClientID           string          `json:"clientId"`
		GrantID            string          `json:"grantId"`
		GrantVersion       int64           `json:"grantVersion"`
		AgentPolicyVersion int64           `json:"agentPolicyVersion"`
		Evidence           json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(value, &envelope); err != nil {
		t.Fatal(err)
	}
	var evidence map[string]any
	_ = json.Unmarshal(envelope.Evidence, &evidence)
	if envelope.SpecVersion != principal.ExecutionAuthorizationSpecV1 ||
		envelope.WorkspaceID != want.Identity.Actor.WorkspaceID ||
		envelope.Actor.Type != string(want.Identity.Actor.Type) || envelope.Actor.ID != want.Identity.Actor.ID ||
		envelope.Subject == nil || envelope.Subject.ID != want.Identity.Subject.ID ||
		envelope.ClientID != want.ClientID || envelope.GrantID != want.GrantID ||
		envelope.GrantVersion != want.GrantVersion || envelope.AgentPolicyVersion != want.AgentPolicyVersion ||
		evidence["action"] != evidenceAction {
		t.Fatalf("authorization envelope=%s", value)
	}
}
