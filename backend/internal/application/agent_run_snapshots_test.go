package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/config"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/workspace"
)

const (
	snapshotOwnerID     = "a08f1f2e-7b5a-7c3d-8e9f-123456789001"
	snapshotWorkspaceID = "a08f1f2e-7b5a-7c3d-8e9f-123456789002"
	snapshotModelID     = "a08f1f2e-7b5a-7c3d-8e9f-123456789003"
	snapshotAgentID     = "a08f1f2e-7b5a-7c3d-8e9f-123456789004"
	snapshotRunID       = "a08f1f2e-7b5a-7c3d-8e9f-123456789005"
)

// Regression for chat SendMessage 422 "invalid run record": production SnapshotAgentRun
// previously marshaled capability descriptors as a JSON array, which fails
// canonicalRunObject / agent_runs.capability_snapshot object checks even after HTTP auth.
func TestAgentRunSnapshotsCapabilitySnapshotIsObject(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertAgentRunSnapshotFixtures(t, db)

	agentRepository, err := agent.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	modelRepository, err := modelconfig.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	capabilityRepository, err := capability.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := capability.NewCatalog(capabilityRepository, capabilityRepository)
	if err != nil {
		t.Fatal(err)
	}
	source := &agentRunSnapshots{
		agents: agentRepository, models: modelRepository, catalog: catalog,
	}

	snapshots, err := source.SnapshotAgentRun(context.Background(), snapshotWorkspaceID, snapshotAgentID)
	if err != nil {
		t.Fatalf("SnapshotAgentRun: %v", err)
	}
	assertJSONObject(t, "model", snapshots.Model)
	assertJSONObject(t, "capabilities", snapshots.Capabilities)
	assertJSONObject(t, "contextPolicy", snapshots.ContextPolicy)

	var capabilityEnvelope map[string]any
	if err := json.Unmarshal(snapshots.Capabilities, &capabilityEnvelope); err != nil {
		t.Fatalf("capabilities envelope: %v", err)
	}
	if _, ok := capabilityEnvelope["releases"]; !ok {
		t.Fatalf("capabilities must wrap releases, got %s", snapshots.Capabilities)
	}

	runRepository, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	// Document the failure mode that previously blocked POST .../messages after auth.
	if _, err := runRepository.StartAgentRun(context.Background(), execution.StartAgentRunInput{
		ID: snapshotRunID, WorkspaceID: snapshotWorkspaceID, SessionID: "",
		AgentID: snapshotAgentID, TriggerType: "CHAT", TriggeredByType: "USER",
		TriggeredByID: snapshotOwnerID, TraceID: "trace-snapshot-regression",
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: "run.v1",
			Model:         snapshots.Model,
			Capabilities:  json.RawMessage(`[]`),
			ContextPolicy: snapshots.ContextPolicy,
		},
		AuthorizationSnapshot: json.RawMessage(`{"decision":"ALLOW"}`),
		InputSummary:          json.RawMessage(`{"messageId":"` + snapshotRunID + `"}`),
	}); !errors.Is(err, execution.ErrRunInvalid) {
		t.Fatalf("array capability snapshot must be rejected as invalid run record, got %v", err)
	}

	run, err := runRepository.StartAgentRun(context.Background(), execution.StartAgentRunInput{
		ID: snapshotRunID, WorkspaceID: snapshotWorkspaceID, SessionID: "",
		AgentID: snapshotAgentID, TriggerType: "CHAT", TriggeredByType: "USER",
		TriggeredByID: snapshotOwnerID, TraceID: "trace-snapshot-regression",
		Snapshots:             snapshots,
		AuthorizationSnapshot: json.RawMessage(`{"decision":"ALLOW"}`),
		InputSummary:          json.RawMessage(`{"messageId":"` + snapshotRunID + `"}`),
	})
	if err != nil {
		t.Fatalf("object capability snapshot must start agent run: %v", err)
	}
	if run.Status != "RUNNING" {
		t.Fatalf("unexpected run status: %+v", run)
	}
	assertJSONObject(t, "stored capabilities", run.CapabilitySnapshot)
}

// P3.4: published + bound WORKFLOW appears in AgentRun capability snapshot so
// Console Chat / agentrun can resolve it (kind WORKFLOW, release pinned).
func TestAgentRunSnapshotsIncludesBoundWorkflow(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertAgentRunSnapshotFixtures(t, db)

	const (
		workflowCapID     = "a08f1f2e-7b5a-7c3d-8e9f-123456789011"
		workflowReleaseID = "a08f1f2e-7b5a-7c3d-8e9f-123456789012"
		workflowSourceID  = "a08f1f2e-7b5a-7c3d-8e9f-123456789013"
		checksum          = "5994471abb01112afcc18159f6cc74b4f511b99806da59b3caf5a9c173cacfc5"
	)
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'WORKFLOW','Snapshot Workflow','snapshot-workflow',$3,$3)
	`, workflowCapID, snapshotWorkspaceID, snapshotOwnerID); err != nil {
		t.Fatal(err)
	}
	capabilityRepository, err := capability.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := capabilityRepository.Publish(context.Background(), capability.PublishRelease{
		ID: workflowReleaseID, WorkspaceID: snapshotWorkspaceID, CapabilityID: workflowCapID,
		SourceType: "WORKFLOW_REVISION", SourceID: workflowSourceID,
		CallableName: "snapshot_workflow", CallableDescription: "Snapshot Workflow",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel: "LOW", SideEffectLevel: "READ", Checksum: checksum, PublishedBy: snapshotOwnerID,
	}); err != nil {
		t.Fatalf("publish workflow: %v", err)
	}
	if _, err := capabilityRepository.Bind(context.Background(), capability.BindInput{
		WorkspaceID: snapshotWorkspaceID, AgentID: snapshotAgentID, CapabilityID: workflowCapID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: snapshotOwnerID,
	}); err != nil {
		t.Fatalf("bind published workflow: %v", err)
	}

	agentRepository, err := agent.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	modelRepository, err := modelconfig.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := capability.NewCatalog(capabilityRepository, capabilityRepository)
	if err != nil {
		t.Fatal(err)
	}
	source := &agentRunSnapshots{
		agents: agentRepository, models: modelRepository, catalog: catalog,
	}
	snapshots, err := source.SnapshotAgentRun(context.Background(), snapshotWorkspaceID, snapshotAgentID)
	if err != nil {
		t.Fatalf("SnapshotAgentRun: %v", err)
	}
	var envelope struct {
		Releases []struct {
			CapabilityID string `json:"capabilityId"`
			ReleaseID    string `json:"releaseId"`
			Kind         string `json:"kind"`
			CallableName string `json:"callableName"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(snapshots.Capabilities, &envelope); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	var found bool
	for _, release := range envelope.Releases {
		if release.CapabilityID == workflowCapID && release.Kind == "WORKFLOW" &&
			release.ReleaseID == workflowReleaseID && release.CallableName == "snapshot_workflow" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected bound WORKFLOW in capability snapshot, got %s", snapshots.Capabilities)
	}
}

func assertJSONObject(t *testing.T, label string, value json.RawMessage) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		t.Fatalf("%s must be a JSON object, got %s err=%v", label, value, err)
	}
}

func insertAgentRunSnapshotFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id,username,display_name) VALUES($1,'snapshot.owner','Snapshot Owner')`,
		snapshotOwnerID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'snapshot-space','Snapshot Space','SANDBOX',$2,$2,$2)
	`, snapshotWorkspaceID, snapshotOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,options,created_by,updated_by
		) VALUES($1,$2,'Snapshot Model','openai','https://models.example.test','snapshot-model','{}',$3,$3)
	`, snapshotModelID, snapshotWorkspaceID, snapshotOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Snapshot Agent',$3,$4,$4)
	`, snapshotAgentID, snapshotWorkspaceID, snapshotModelID, snapshotOwnerID); err != nil {
		t.Fatal(err)
	}
}

const snapshotRevisionID = "a08f1f2e-7b5a-7c3d-8e9f-123456789006"

func TestAgentRunSnapshotsV2WhenGateAndCapabilitiesReady(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertAgentRunSnapshotFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO agent_prompt_revisions(
			id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by
		) VALUES($1,$2,$3,1,'You are a test agent.','MANUAL',$4,$5)
	`, snapshotRevisionID, snapshotWorkspaceID, snapshotAgentID,
		strings.Repeat("a", 64), snapshotOwnerID); err != nil {
		t.Fatalf("insert prompt revision: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE agents SET current_prompt_revision_id=$2 WHERE id=$1
	`, snapshotAgentID, snapshotRevisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE model_configs SET runtime_capabilities=$2::jsonb WHERE id=$1
	`, snapshotModelID, `{
		"schemaVersion":"model-runtime.v1",
		"contextWindowTokens":128000,
		"defaultOutputReserveTokens":4096,
		"outputTokenLimitMode":"max_tokens",
		"tokenizerProfile":"o200k_base",
		"tokenizerVersion":"2026-01"
	}`); err != nil {
		t.Fatal(err)
	}

	agentRepository, err := agent.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	modelRepository, err := modelconfig.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	capabilityRepository, err := capability.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := capability.NewCatalog(capabilityRepository, capabilityRepository)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	// Gate off → legacy.
	legacySource := &agentRunSnapshots{
		agents: agentRepository, models: modelRepository, catalog: catalog,
		workspaces:     workspaceRepository,
		sessionContext: config.SessionContextRollout{Enabled: false, Mode: "disabled"},
	}
	legacy, err := legacySource.SnapshotAgentRun(context.Background(), snapshotWorkspaceID, snapshotAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != execution.RunSnapshotSchemaV1 {
		t.Fatalf("gate off expected run.v1, got %s", legacy.SchemaVersion)
	}

	// Gate on + complete capabilities → run.v2 + session-context.v1.
	v2Source := &agentRunSnapshots{
		agents: agentRepository, models: modelRepository, catalog: catalog,
		workspaces: workspaceRepository,
		sessionContext: config.SessionContextRollout{
			Enabled: true, AllowAllWorkspaces: true, Mode: "enforced",
			RolloutVersion: "test-rollout",
		},
	}
	v2, err := v2Source.SnapshotAgentRun(context.Background(), snapshotWorkspaceID, snapshotAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if v2.SchemaVersion != execution.RunSnapshotSchemaV2 {
		t.Fatalf("expected run.v2, got %s", v2.SchemaVersion)
	}
	if !strings.Contains(string(v2.ContextPolicy), `"schemaVersion":"session-context.v1"`) {
		t.Fatalf("context snapshot: %s", v2.ContextPolicy)
	}
	if !strings.Contains(string(v2.Agent), snapshotRevisionID) {
		t.Fatalf("agent snapshot missing revision: %s", v2.Agent)
	}
	if !strings.Contains(string(v2.Model), "runtimeCapabilities") {
		t.Fatalf("model snapshot missing runtimeCapabilities: %s", v2.Model)
	}

	// Persist and re-read: snapshots immutable; agent_snapshot stored.
	runRepository, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runRepository.StartAgentRun(context.Background(), execution.StartAgentRunInput{
		ID: snapshotRunID, WorkspaceID: snapshotWorkspaceID, AgentID: snapshotAgentID,
		TriggerType: "CHAT", TriggeredByType: "USER", TriggeredByID: snapshotOwnerID,
		TraceID: "trace-v2-snapshot", Snapshots: v2,
		AuthorizationSnapshot: json.RawMessage(`{}`), InputSummary: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("start v2 run: %v", err)
	}
	if run.SnapshotSchemaVersion != execution.RunSnapshotSchemaV2 {
		t.Fatalf("stored schema: %s", run.SnapshotSchemaVersion)
	}
	if string(run.AgentSnapshot) == "" || string(run.AgentSnapshot) == "{}" {
		t.Fatalf("expected agent_snapshot stored, got %s", run.AgentSnapshot)
	}
	// Mutating agent_snapshot must fail (immutable).
	if _, err := db.Exec(`
		UPDATE agent_runs SET agent_snapshot='{}'::jsonb, lock_version=lock_version+1 WHERE id=$1
	`, snapshotRunID); err == nil {
		t.Fatal("expected agent_snapshot immutability")
	}
}
