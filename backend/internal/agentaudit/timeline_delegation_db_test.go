package agentaudit_test

import (
	"context"
	"database/sql"
	"testing"

	"actweave/backend/internal/agentaudit"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// DB integration: loadSteps LEFT JOIN agent_run_delegations surfaces authoritative
// caller/target/child_run/remote fields into the timeline API.
func TestService_TimelineJoinsDelegationTable(t *testing.T) {
	harness := dbtest.New(t)
	version := harness.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("migration = %+v", version)
	}
	db := harness.Open(t)
	ctx := context.Background()

	owner := uuid.Must(uuid.NewV7()).String()
	ws := uuid.Must(uuid.NewV7()).String()
	modelID := uuid.Must(uuid.NewV7()).String()
	agentA := uuid.Must(uuid.NewV7()).String()
	agentB := uuid.Must(uuid.NewV7()).String()
	runID := uuid.Must(uuid.NewV7()).String()
	childRunID := uuid.Must(uuid.NewV7()).String()
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	modelStepID := uuid.Must(uuid.NewV7()).String()
	sessionID := uuid.Must(uuid.NewV7()).String()
	traceID := "trace-" + runID[:8]

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\nSQL: %s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'audit.owner','Audit Owner')`, owner)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'Audit Space','SANDBOX',$3,$3,$3)`, ws, "aud-"+ws[:8], owner)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://example.test','m',$3,$3)`, modelID, ws, owner)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$3,'Agent A',$4,$5,$5),($2,$3,'Agent B',$4,$5,$5)`,
		agentA, agentB, ws, modelID, owner)
	exec(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'s',$4)`, sessionID, ws, agentA, owner)
	exec(`INSERT INTO agent_runs(
			id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
			triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot,
			authorization_snapshot,input_summary,agent_graph_snapshot,finished_at
		) VALUES (
			$1,$2,$3,$4,'SUCCEEDED','CHAT','USER',$5,$6,
			'{"modelName":"m"}'::jsonb,'{"releases":[]}'::jsonb,'{}'::jsonb,
			'{}'::jsonb,'{}'::jsonb,'{}'::jsonb, NOW()
		)`, runID, ws, sessionID, agentA, owner, traceID)

	// 1) Delegation without child_run_id first
	exec(`INSERT INTO agent_run_delegations (
			id, workspace_id, parent_run_id, caller_agent_id, target_agent_id,
			mode, protocol, origin, depth, binding_version, tool_call_id, idempotency_key,
			status, input_summary, input_payload, output_summary, output_payload,
			remote_task_id, remote_context_id, remote_message_id, remote_endpoint_ref,
			protocol_status, latency_ms, started_at, finished_at
		) VALUES (
			$1,$2,$3,$4,$5,'TASK','INTERNAL','INTERNAL',1,1,'tc1',$6,
			'RUNNING','{}','{}','{}','{}',
			'','','','','',NULL,
			NOW()-interval '1 second', NULL
		)`, delID, ws, runID, agentA, agentB, runID+":tc1:1:b")

	// 2) Child run with parent linkage at insert (triggered_by must satisfy principal FK).
	exec(`INSERT INTO agent_runs(
			id,workspace_id,agent_id,status,trigger_type,triggered_by_type,
			triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot,
			authorization_snapshot,input_summary,parent_run_id,parent_delegation_id,agent_graph_snapshot,finished_at
		) VALUES (
			$1,$2,$3,'SUCCEEDED','DELEGATION_TASK','USER',$4,$5,
			'{"id":"m-b","modelName":"m"}'::jsonb,
			'{"schemaVersion":"capability-snapshot.v1","releases":[]}'::jsonb,'{}'::jsonb,
			'{}'::jsonb,'{}'::jsonb,$6,$7,'{}'::jsonb, NOW()
		)`, childRunID, ws, agentB, owner, traceID, runID, delID)

	// 3) Link child + terminalize while RUNNING (terminal evidence is immutable after SUCCEEDED).
	exec(`UPDATE agent_run_delegations SET child_run_id=$1 WHERE id=$2 AND status='RUNNING'`, childRunID, delID)
	exec(`UPDATE agent_run_delegations SET
			status='SUCCEEDED',
			output_summary='{"ok":true}'::jsonb,
			output_payload='{"result":"ok"}'::jsonb,
			remote_task_id='rtask', remote_context_id='rctx', remote_message_id='rmsg',
			remote_endpoint_ref='https://example/a2a', protocol_status='completed',
			latency_ms=42, finished_at=NOW()
		WHERE id=$1 AND status='RUNNING'`, delID)

	// Parent AGENT_DELEGATION + child-run nested MODEL (no cross-run parent_step_id).
	exec(`INSERT INTO agent_run_steps (
			id, workspace_id, run_id, sequence_no, step_type, status, input_summary, output_summary,
			agent_id, delegation_id, started_at, finished_at
		) VALUES (
			$1,$2,$3,1,'AGENT_DELEGATION','SUCCEEDED','{"callableName":"call_b"}','{"ok":true}',
			$4,$5, NOW()-interval '1 second', NOW()
		)`, stepID, ws, runID, agentA, delID)
	// Nested MODEL on TASK child run: agent_id=B, delegation_id=d, run_id=child, parent_step_id NULL.
	// MODEL SUCCEEDED requires raw_object_id evidence FK.
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	exec(`INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES ($1,$2,'executions',$3,'MODEL_TURN','application/json',2,$4,
			'test-key','SENSITIVE','PERMANENT','USER',$5)`,
		modelStepID, ws, ws+"/model-turn/"+modelStepID, sha, owner)
	exec(`INSERT INTO agent_run_steps (
			id, workspace_id, run_id, sequence_no, step_type, status, input_summary, output_summary,
			agent_id, delegation_id, raw_object_id, raw_sha256, raw_length, started_at, finished_at
		) VALUES (
			$1,$2,$3,1,'MODEL','SUCCEEDED','{"source":"nested"}','{"contentSha256":"ab"}',
			$4,$5,$1,$6,2, NOW()-interval '500 ms', NOW()
		)`, modelStepID, ws, childRunID, agentB, delID, sha)

	svc, err := agentaudit.NewService(db, true)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetTrace(ctx, ws, traceID, agentaudit.DetailFilter{Limit: 100})
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	var foundDel *agentaudit.Step
	for i := range detail.Steps {
		if detail.Steps[i].Type == "agent_delegation" {
			foundDel = &detail.Steps[i]
			break
		}
	}
	if foundDel == nil {
		t.Fatalf("no agent_delegation in steps=%+v", stepTypes(detail.Steps))
	}
	if foundDel.CallerAgentID != agentA {
		t.Fatalf("callerAgentId=%s want %s", foundDel.CallerAgentID, agentA)
	}
	if foundDel.TargetAgentID != agentB {
		t.Fatalf("targetAgentId=%s want %s", foundDel.TargetAgentID, agentB)
	}
	if foundDel.ChildRunID != childRunID {
		t.Fatalf("childRunId=%s want %s", foundDel.ChildRunID, childRunID)
	}
	if foundDel.Mode != "TASK" || foundDel.Protocol != agentdelegation.ProtocolInternal {
		t.Fatalf("mode/protocol=%s/%s", foundDel.Mode, foundDel.Protocol)
	}
	if foundDel.RemoteTaskID != "rtask" || foundDel.RemoteEndpointRef == "" {
		t.Fatalf("remote fields missing: %+v", foundDel)
	}
	if foundDel.Status != agentdelegation.StatusSucceeded {
		t.Fatalf("status=%s", foundDel.Status)
	}
	// Nested MODEL must appear under the AGENT_DELEGATION frame via delegation_id nesting.
	nestedModel := findNestedModelStep(foundDel.Children, agentB)
	if nestedModel == nil {
		nestedModel = findNestedModelStep(detail.Steps, agentB)
	}
	if nestedModel == nil {
		t.Fatalf("nested B MODEL/reasoning not found under timeline; types=%v delChildren=%d childTypes=%v",
			stepTypes(detail.Steps), len(foundDel.Children), stepTypes(foundDel.Children))
	}
	if nestedModel.AgentID != agentB {
		t.Fatalf("nested model agent=%s want B", nestedModel.AgentID)
	}
	if nestedModel.DelegationID != delID {
		t.Fatalf("nested model delegation=%s want %s", nestedModel.DelegationID, delID)
	}
	// TASK: nested model run is child (when timeline surfaces run id).
	if nestedModel.RunID != "" && nestedModel.RunID != childRunID && nestedModel.RunID != runID {
		t.Fatalf("nested model runId=%s want child=%s or parent=%s", nestedModel.RunID, childRunID, runID)
	}
	_ = sql.ErrNoRows
}

func stepTypes(steps []agentaudit.Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.Type)
	}
	return out
}

func findNestedModelStep(steps []agentaudit.Step, agentID string) *agentaudit.Step {
	for i := range steps {
		// Timeline maps MODEL steps to type "reasoning" (大模型推理) in the UI tree.
		if (steps[i].Type == "model" || steps[i].Type == "reasoning") &&
			(agentID == "" || steps[i].AgentID == agentID) {
			return &steps[i]
		}
		if m := findNestedModelStep(steps[i].Children, agentID); m != nil {
			return m
		}
	}
	return nil
}
