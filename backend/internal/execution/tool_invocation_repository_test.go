package execution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"
	"github.com/google/uuid"
)

const (
	invocationProviderID          = "708f1f2e-7b5a-7c3d-8e9f-123456789001"
	invocationOtherProviderID     = "708f1f2e-7b5a-7c3d-8e9f-123456789002"
	invocationConnectionID        = "708f1f2e-7b5a-7c3d-8e9f-123456789003"
	invocationOtherConnectionID   = "708f1f2e-7b5a-7c3d-8e9f-123456789004"
	invocationToolID              = "708f1f2e-7b5a-7c3d-8e9f-123456789005"
	invocationOtherToolID         = "708f1f2e-7b5a-7c3d-8e9f-123456789006"
	invocationVersionID           = "708f1f2e-7b5a-7c3d-8e9f-123456789007"
	invocationOtherVersionID      = "708f1f2e-7b5a-7c3d-8e9f-123456789008"
	invocationReleaseID           = "708f1f2e-7b5a-7c3d-8e9f-123456789009"
	invocationOtherReleaseID      = "708f1f2e-7b5a-7c3d-8e9f-12345678900a"
	invocationMismatchReleaseID   = "708f1f2e-7b5a-7c3d-8e9f-12345678900b"
	invocationWorkflowExecutionID = "708f1f2e-7b5a-7c3d-8e9f-12345678900c"
	invocationExecutionStepID     = "708f1f2e-7b5a-7c3d-8e9f-12345678900d"
	invocationID                  = "708f1f2e-7b5a-7c3d-8e9f-12345678900e"
	invocationRetryID             = "708f1f2e-7b5a-7c3d-8e9f-12345678900f"
	invocationFailID              = "708f1f2e-7b5a-7c3d-8e9f-123456789010"
	invocationRawObjectID         = "708f1f2e-7b5a-7c3d-8e9f-123456789011"
	invocationProbeID             = "708f1f2e-7b5a-7c3d-8e9f-123456789012"
	invocationFailRawObjectID     = "708f1f2e-7b5a-7c3d-8e9f-123456789013"
	invocationChecksum            = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func TestToolInvocationMigration(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateTo(t, 20)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected clean tool invocation migration version 20, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	assertToolInvocationSchema(t, db)
	assertToolInvocationConstraints(t, db)

	version = testDatabase.MigrateTo(t, 19)
	if !version.Applied || version.Number != 19 || version.Dirty {
		t.Fatalf("expected clean tool invocation rollback version 19, got %+v", version)
	}
	assertToolInvocationTableMissing(t, db)
	version = testDatabase.MigrateTo(t, 20)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("expected clean tool invocation migration reapply, got %+v", version)
	}
}

func TestToolInvocationRepository(t *testing.T) {
	repository, _ := newToolInvocationRepository(t)
	ctx := context.Background()
	input := validToolInvocationStart(invocationID, "order-lookup")
	started, err := repository.Start(ctx, input)
	if err != nil {
		t.Fatalf("start tool invocation: %v", err)
	}
	if !started.Created || started.Invocation.Status != "RUNNING" ||
		started.Invocation.ToolVersionID != invocationVersionID {
		t.Fatalf("unexpected started invocation: %+v", started)
	}
	if _, err := repository.Get(ctx, executionOtherWorkspaceID, invocationID); !errors.Is(err, execution.ErrToolInvocationNotFound) {
		t.Fatalf("expected cross-workspace get to be not found, got %v", err)
	}

	retry := input
	retry.ID = invocationRetryID
	retry.TraceID = "trace-http-retry"
	retried, err := repository.Start(ctx, retry)
	if err != nil {
		t.Fatalf("retry idempotent tool invocation: %v", err)
	}
	if retried.Created || retried.Invocation.ID != invocationID {
		t.Fatalf("expected idempotent retry to reuse %s, got %+v", invocationID, retried)
	}
	conflicting := retry
	conflicting.InputSummary = json.RawMessage(`{"order_id":"B-2"}`)
	if _, err := repository.Start(ctx, conflicting); !errors.Is(err, execution.ErrToolInvocationIdempotencyConflict) {
		t.Fatalf("expected changed idempotent input conflict, got %v", err)
	}

	completed, err := repository.Complete(ctx, executionWorkspaceID, invocationID,
		execution.CompleteToolInvocationInput{
			OutputSummary:     json.RawMessage(`{"status":"shipped"}`),
			RawObjectID:       invocationRawObjectID,
			ProviderRequestID: "provider-request-1",
		})
	if err != nil {
		t.Fatalf("complete tool invocation: %v", err)
	}
	if completed.Status != "SUCCEEDED" || completed.FinishedAt == nil ||
		completed.LatencyMS == nil || completed.RawObjectID != invocationRawObjectID {
		t.Fatalf("unexpected completed invocation: %+v", completed)
	}
	if _, err := repository.Complete(ctx, executionWorkspaceID, invocationID,
		execution.CompleteToolInvocationInput{}); !errors.Is(err, execution.ErrToolInvocationConflict) {
		t.Fatalf("expected duplicate completion conflict, got %v", err)
	}

	failedStart, err := repository.Start(ctx, validToolInvocationStart(invocationFailID, "order-fail"))
	if err != nil || !failedStart.Created {
		t.Fatalf("start invocation that will fail: created=%v err=%v", failedStart.Created, err)
	}
	if _, err := repository.Fail(ctx, executionWorkspaceID, invocationFailID,
		execution.FailToolInvocationInput{}); !errors.Is(err, execution.ErrToolInvocationInvalid) {
		t.Fatalf("expected blank failure code to be invalid, got %v", err)
	}
	failed, err := repository.Fail(ctx, executionWorkspaceID, invocationFailID,
		execution.FailToolInvocationInput{
			OutputSummary:     json.RawMessage(`{"upstream_status":503}`),
			RawObjectID:       invocationFailRawObjectID,
			ProviderRequestID: "provider-request-2",
			ErrorCode:         "UPSTREAM_UNAVAILABLE",
		})
	if err != nil {
		t.Fatalf("fail tool invocation: %v", err)
	}
	if failed.Status != "FAILED" || failed.ErrorCode != "UPSTREAM_UNAVAILABLE" ||
		failed.FinishedAt == nil || failed.LatencyMS == nil {
		t.Fatalf("unexpected failed invocation: %+v", failed)
	}
}

func TestToolInvocationIdempotencyConcurrent(t *testing.T) {
	repository, db := newToolInvocationRepository(t)
	db.SetMaxOpenConns(12)
	const workers = 8
	type result struct {
		value execution.StartToolInvocationResult
		err   error
	}
	results := make(chan result, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			input := validToolInvocationStart(uuid.NewString(), "concurrent-order")
			value, err := repository.Start(context.Background(), input)
			results <- result{value: value, err: err}
		}()
	}
	wait.Wait()
	close(results)
	created := 0
	invocationIDs := map[string]struct{}{}
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent idempotent start: %v", item.err)
		}
		if item.value.Created {
			created++
		}
		invocationIDs[item.value.Invocation.ID] = struct{}{}
	}
	if created != 1 || len(invocationIDs) != 1 {
		t.Fatalf("expected one created invocation and one shared ID, created=%d ids=%v", created, invocationIDs)
	}
}

func newToolInvocationRepository(t *testing.T) (*execution.ToolInvocationRepository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertToolInvocationFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES
		 ($1,$3,'actweave-executions',$4,'TOOL_INVOCATION_PAYLOAD',
		  'application/json',2,$5,'tool-invocation-test-key-v1','SENSITIVE','PERMANENT','USER',$6),
		 ($2,$3,'actweave-executions',$7,'TOOL_INVOCATION_PAYLOAD',
		  'application/json',2,$5,'tool-invocation-test-key-v1','SENSITIVE','PERMANENT','USER',$6)
	`, invocationRawObjectID, invocationFailRawObjectID, executionWorkspaceID,
		executionWorkspaceID+"/tool-invocation/"+invocationRawObjectID,
		strings.Repeat("e", 64), executionOwnerID,
		executionWorkspaceID+"/tool-invocation/"+invocationFailRawObjectID); err != nil {
		t.Fatal(err)
	}
	repository, err := execution.NewToolInvocationRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func insertToolInvocationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	insertWorkflowExecutionMigrationFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO capability_providers(
		 id,workspace_id,name,provider_kind,driver_key,transport,created_by,updated_by
		) VALUES
		($1,$3,'Invocation Provider','HTTP_OPENAPI','http.openapi','HTTP',$5,$5),
		($2,$4,'Other Invocation Provider','HTTP_OPENAPI','http.openapi','HTTP',$5,$5)
	`, invocationProviderID, invocationOtherProviderID, executionWorkspaceID,
		executionOtherWorkspaceID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO service_connections(
		 id,workspace_id,provider_id,name,alias,environment,auth_mode,created_by,updated_by
		) VALUES
		($1,$3,$5,'Invocation Connection','primary','PRODUCTION','NONE',$7,$7),
		($2,$4,$6,'Other Invocation Connection','other','TEST','NONE',$7,$7)
	`, invocationConnectionID, invocationOtherConnectionID, executionWorkspaceID,
		executionOtherWorkspaceID, invocationProviderID, invocationOtherProviderID,
		executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by) VALUES
		($1,$3,'TOOL','Invocation Tool','invocation-tool',$5,$5),
		($2,$4,'TOOL','Other Invocation Tool','other-invocation-tool',$5,$5)
	`, invocationToolID, invocationOtherToolID, executionWorkspaceID,
		executionOtherWorkspaceID, executionOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO tools(capability_id,workspace_id,provider_id,default_connection_id) VALUES
		($1,$3,$5,$7),($2,$4,$6,$8)
	`, invocationToolID, invocationOtherToolID, executionWorkspaceID,
		executionOtherWorkspaceID, invocationProviderID, invocationOtherProviderID,
		invocationConnectionID, invocationOtherConnectionID); err != nil {
		t.Fatal(err)
	}
	insertFixtureToolVersion(t, db, invocationVersionID, executionWorkspaceID,
		invocationToolID, invocationProviderID, invocationConnectionID)
	insertFixtureToolVersion(t, db, invocationOtherVersionID, executionOtherWorkspaceID,
		invocationOtherToolID, invocationOtherProviderID, invocationOtherConnectionID)
	insertFixtureToolRelease(t, db, invocationReleaseID, executionWorkspaceID,
		invocationToolID, invocationVersionID, "invoke_order")
	insertFixtureToolRelease(t, db, invocationOtherReleaseID, executionOtherWorkspaceID,
		invocationOtherToolID, invocationOtherVersionID, "invoke_other_order")
	insertFixtureToolRelease(t, db, invocationMismatchReleaseID, executionWorkspaceID,
		invocationToolID, invocationProbeID, "invoke_mismatch")

	insertFixtureWorkflowExecution(t, db, invocationWorkflowExecutionID,
		executionWorkspaceID, executionWorkflowID, executionRevisionID, executionAgentRunID)
	if _, err := db.Exec(`
		UPDATE workflow_executions SET status='RUNNING',lock_version=lock_version+1 WHERE id=$1
	`, invocationWorkflowExecutionID); err != nil {
		t.Fatalf("start invocation parent workflow execution: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO execution_steps(
		 id,workspace_id,execution_id,node_id,node_type,sequence_no,status,input_summary
		) VALUES($1,$2,$3,'tool-1','TOOL',1,'RUNNING','{"order_id":"A-1"}')
	`, invocationExecutionStepID, executionWorkspaceID, invocationWorkflowExecutionID); err != nil {
		t.Fatalf("insert invocation parent execution step: %v", err)
	}
}

func insertFixtureToolVersion(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, toolID, providerID, connectionID string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO tool_versions(
		 id,workspace_id,capability_id,version_no,lifecycle_status,executor_type,
		 provider_id,default_connection_id,action_schema_version,action_config,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,
		 created_by,updated_by,published_at
		) VALUES($1,$2,$3,1,'PUBLISHED','HTTP',$4,$5,'http.v1',
		 '{"method":"GET","path":"/orders/{id}"}','{}','{}','LOW','READ',$6,$7,$7,
		 clock_timestamp())
	`, id, workspaceID, toolID, providerID, connectionID, invocationChecksum,
		executionOwnerID); err != nil {
		t.Fatalf("insert fixture tool version: %v", err)
	}
}

func insertFixtureToolRelease(
	t *testing.T,
	db *sql.DB,
	id, workspaceID, toolID, sourceID, callableName string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO capability_releases(
		 id,workspace_id,capability_id,release_no,source_type,source_id,callable_name,
		 input_schema,output_schema,risk_level,side_effect_level,checksum,published_by
		) VALUES($1,$2,$3,(SELECT COALESCE(MAX(release_no),0)+1 FROM capability_releases
		 WHERE capability_id=$3),'TOOL_VERSION',$4,$5,'{}','{}','LOW','READ',$6,$7)
	`, id, workspaceID, toolID, sourceID, callableName, invocationChecksum,
		executionOwnerID); err != nil {
		t.Fatalf("insert fixture tool release: %v", err)
	}
}

func assertToolInvocationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var tableExists bool
	if err := db.QueryRow(`SELECT to_regclass('public.tool_invocations') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if !tableExists {
		t.Fatal("expected tool_invocations table")
	}
	for _, index := range []string{
		"tool_invocations_idempotency_key",
		"tool_invocations_workspace_started_idx",
		"tool_invocations_workspace_status_started_idx",
		"tool_invocations_workspace_agent_run_started_idx",
		"tool_invocations_workspace_workflow_started_idx",
		"tool_invocations_trace_idx",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected tool invocation index %s", index)
		}
	}
}

func assertToolInvocationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	insertToolInvocationDirect(t, db, invocationID, invocationReleaseID,
		executionAgentRunID, invocationWorkflowExecutionID, invocationExecutionStepID,
		"migration-idempotency")
	assertToolInvocationStatementFails(t, db, `
		INSERT INTO tool_invocations(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 connection_id,actor_type,actor_id,trace_id,status,input_summary
		) VALUES($1,$2,$3,$4,$5,$6,$7,'USER',$8,'trace-cross-provider','RUNNING','{}')
	`, invocationProbeID, executionWorkspaceID, invocationToolID, invocationVersionID,
		invocationReleaseID, invocationOtherProviderID, invocationConnectionID,
		executionOwnerID)
	assertToolInvocationStatementFails(t, db, `
		INSERT INTO tool_invocations(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 connection_id,actor_type,actor_id,trace_id,status,input_summary
		) VALUES($1,$2,$3,$4,$5,$6,$7,'USER',$8,'trace-bad-release','RUNNING','{}')
	`, invocationProbeID, executionWorkspaceID, invocationToolID, invocationVersionID,
		invocationMismatchReleaseID, invocationProviderID, invocationConnectionID,
		executionOwnerID)
	assertToolInvocationStatementFails(t, db, `
		INSERT INTO tool_invocations(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 connection_id,agent_run_id,workflow_execution_id,actor_type,actor_id,trace_id,status
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'USER',$10,'trace-bad-parent','RUNNING')
	`, invocationProbeID, executionWorkspaceID, invocationToolID, invocationVersionID,
		invocationReleaseID, invocationProviderID, invocationConnectionID,
		executionOtherAgentRunID, invocationWorkflowExecutionID, executionOwnerID)
	assertToolInvocationStatementFails(t, db, `
		INSERT INTO tool_invocations(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 actor_type,actor_id,trace_id,status,input_summary
		) VALUES($1,$2,$3,$4,$5,$6,'USER',$7,'trace-bad-summary','RUNNING','[]')
	`, invocationProbeID, executionWorkspaceID, invocationToolID, invocationVersionID,
		invocationReleaseID, invocationProviderID, executionOwnerID)
	assertToolInvocationStatementFails(t, db, `
		INSERT INTO tool_invocations(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 actor_type,actor_id,trace_id,idempotency_key,status
		) VALUES($1,$2,$3,$4,$5,$6,'USER',$7,'trace-duplicate','migration-idempotency','RUNNING')
	`, invocationProbeID, executionWorkspaceID, invocationToolID, invocationVersionID,
		invocationReleaseID, invocationProviderID, executionOwnerID)
	assertToolInvocationStatementFails(t, db, `UPDATE tool_invocations SET input_summary='{}' WHERE id=$1`, invocationID)
	if _, err := db.Exec(`
		UPDATE tool_invocations SET status='SUCCEEDED',output_summary='{"ok":true}',
		 latency_ms=1,finished_at=clock_timestamp() WHERE id=$1
	`, invocationID); err != nil {
		t.Fatalf("complete direct tool invocation: %v", err)
	}
	assertToolInvocationStatementFails(t, db, `UPDATE tool_invocations SET output_summary='{}' WHERE id=$1`, invocationID)
	assertToolInvocationStatementFails(t, db, `DELETE FROM tool_invocations WHERE id=$1`, invocationID)
}

func insertToolInvocationDirect(
	t *testing.T,
	db *sql.DB,
	id, releaseID, agentRunID, workflowExecutionID, executionStepID, idempotencyKey string,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO tool_invocations(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 connection_id,agent_run_id,workflow_execution_id,execution_step_id,
		 actor_type,actor_id,trace_id,idempotency_key,status,input_summary
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'USER',$11,'trace-direct',$12,
		 'RUNNING','{"order_id":"A-1"}')
	`, id, executionWorkspaceID, invocationToolID, invocationVersionID, releaseID,
		invocationProviderID, invocationConnectionID, agentRunID, workflowExecutionID,
		executionStepID, executionOwnerID, idempotencyKey); err != nil {
		t.Fatalf("insert direct tool invocation: %v", err)
	}
}

func validToolInvocationStart(id, idempotencyKey string) execution.StartToolInvocationInput {
	return execution.StartToolInvocationInput{
		ID:                  id,
		WorkspaceID:         executionWorkspaceID,
		ToolID:              invocationToolID,
		ToolVersionID:       invocationVersionID,
		CapabilityReleaseID: invocationReleaseID,
		ProviderID:          invocationProviderID,
		ConnectionID:        invocationConnectionID,
		AgentRunID:          executionAgentRunID,
		WorkflowExecutionID: invocationWorkflowExecutionID,
		ExecutionStepID:     invocationExecutionStepID,
		ActorType:           "USER",
		ActorID:             executionOwnerID,
		TraceID:             "trace-http-invocation",
		IdempotencyKey:      idempotencyKey,
		InputSummary:        json.RawMessage(`{"order_id":"A-1"}`),
	}
}

func assertToolInvocationStatementFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected tool invocation statement to fail: %s", strings.TrimSpace(query))
	}
}

func assertToolInvocationTableMissing(t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.tool_invocations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("tool_invocations remained after rollback")
	}
	var providerKeyExists bool
	if err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM pg_constraint
		 WHERE conname='tool_versions_workspace_capability_version_provider_key')
	`).Scan(&providerKeyExists); err != nil {
		t.Fatal(err)
	}
	if providerKeyExists {
		t.Fatal("tool version provider key remained after rollback")
	}
}
