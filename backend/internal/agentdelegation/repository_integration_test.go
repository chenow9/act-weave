package agentdelegation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// Integration: migration + authoritative audit prewrite/finalize + binding CRUD.

func TestDelegationMigrationAndAuditDAO(t *testing.T) {
	harness := dbtest.New(t)
	version := harness.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("migration version = %+v want >=13", version)
	}
	db := harness.Open(t)
	assertDelegationSchema(t, db)

	fx := seedDelegationFixture(t, db)
	repo, err := agentdelegation.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := agentdelegation.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// --- Binding create / cycle / self-loop ---
	b, err := svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: fx.bindingID, WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, TargetAgentID: fx.agentB,
		CallableName: "call_b", Description: "delegate to B",
		Mode: agentdelegation.ModeInline, ContextPolicy: agentdelegation.ContextTaskOnly,
		Enabled: true, ActorID: fx.ownerID,
	})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if b.Version != 1 || b.CallableName != "call_b" {
		t.Fatalf("binding = %+v", b)
	}

	// Self-loop rejected
	_, err = svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, TargetAgentID: fx.agentA,
		CallableName: "self", Mode: agentdelegation.ModeInline,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.ownerID,
	})
	if !errors.Is(err, agentdelegation.ErrSelfLoop) {
		t.Fatalf("self-loop err = %v", err)
	}

	// Cycle A→B→A rejected
	_, err = svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentB, TargetAgentID: fx.agentA,
		CallableName: "call_a", Mode: agentdelegation.ModeInline,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.ownerID,
	})
	if err == nil {
		t.Fatal("expected cycle reject")
	}

	// Duplicate alias
	_, err = svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, TargetAgentID: fx.agentC,
		CallableName: "call_b", Mode: agentdelegation.ModeInline,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.ownerID,
	})
	if err == nil {
		t.Fatal("expected duplicate alias")
	}

	// Nested A→C allowed (no cycle with A→B)
	_, err = svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, TargetAgentID: fx.agentC,
		CallableName: "call_c", Mode: agentdelegation.ModeTask,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.ownerID,
	})
	if err != nil {
		t.Fatalf("A→C: %v", err)
	}

	// --- Audit fail-closed prewrite + finalize + idempotency ---
	startParentRun(t, db, fx)
	delID := uuid.Must(uuid.NewV7()).String()
	stepID := uuid.Must(uuid.NewV7()).String()
	idem := agentdelegation.IdempotencyKey(fx.parentRunID, "tool-call-1", 1, fx.bindingID)
	target := fx.agentB
	del, replay, err := svc.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: delID, WorkspaceID: fx.workspaceID, ParentRunID: fx.parentRunID,
		CallerAgentID: fx.agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "tool-call-1", IdempotencyKey: idem,
		InputSummary: json.RawMessage(`{"callableName":"call_b","requestPreview":"do work"}`),
		InputPayload: json.RawMessage(`{"request":"do work"}`),
		StepID:       stepID, AgentID: fx.agentA,
	})
	if err != nil {
		t.Fatalf("prewrite: %v", err)
	}
	if replay {
		t.Fatal("first prewrite must not be replay")
	}
	if del.Status != agentdelegation.StatusRunning {
		t.Fatalf("status=%s", del.Status)
	}
	// Step must exist as AGENT_DELEGATION RUNNING
	var stepType, stepStatus, stepAgent, stepDel string
	if err := db.QueryRow(`
		SELECT step_type, status, COALESCE(agent_id::text,''), COALESCE(delegation_id::text,'')
		FROM agent_run_steps WHERE id=$1
	`, stepID).Scan(&stepType, &stepStatus, &stepAgent, &stepDel); err != nil {
		t.Fatal(err)
	}
	if stepType != agentdelegation.StepTypeAgentDelegation || stepStatus != "RUNNING" {
		t.Fatalf("step type/status = %s/%s", stepType, stepStatus)
	}
	if stepAgent != fx.agentA || stepDel != delID {
		t.Fatalf("step attribution agent=%s del=%s", stepAgent, stepDel)
	}

	// Idempotent replay
	del2, replay2, err := svc.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID, ParentRunID: fx.parentRunID,
		CallerAgentID: fx.agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "tool-call-1", IdempotencyKey: idem,
		StepID: uuid.Must(uuid.NewV7()).String(), AgentID: fx.agentA,
	})
	if err != nil || !replay2 {
		t.Fatalf("replay=%v err=%v", replay2, err)
	}
	if del2.ID != delID {
		t.Fatalf("idempotent id mismatch %s vs %s", del2.ID, delID)
	}

	// Finalize SUCCEEDED (idempotent twice)
	outSum := json.RawMessage(`{"ok":true,"status":"SUCCEEDED"}`)
	outPay := json.RawMessage(`{"result":"child done"}`)
	fin, err := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
	})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if fin.Status != agentdelegation.StatusSucceeded {
		t.Fatalf("fin status=%s", fin.Status)
	}
	_, err = svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status: agentdelegation.StatusFailed, ErrorCode: "X",
		OutputSummary: json.RawMessage(`{}`), OutputPayload: json.RawMessage(`{}`),
	})
	// Sticky terminal: different status is ErrConflict (row remains SUCCEEDED).
	if !errors.Is(err, agentdelegation.ErrConflict) {
		t.Fatalf("want ErrConflict on different terminal finalize, got %v", err)
	}
	fin2, err := svc.FinalizeDelegation(ctx, agentdelegation.FinalizeDelegationInput{
		WorkspaceID: fx.workspaceID, DelegationID: delID, StepID: stepID,
		Status: agentdelegation.StatusSucceeded, OutputSummary: outSum, OutputPayload: outPay,
	})
	if err != nil {
		t.Fatalf("same-status finalize: %v", err)
	}
	if fin2.Status != agentdelegation.StatusSucceeded {
		t.Fatalf("sticky terminal lost: %s", fin2.Status)
	}
	if err := db.QueryRow(`SELECT status FROM agent_run_steps WHERE id=$1`, stepID).Scan(&stepStatus); err != nil {
		t.Fatal(err)
	}
	if stepStatus != "SUCCEEDED" {
		t.Fatalf("step status=%s", stepStatus)
	}

	// Soft disable: enabled=false, still listable for edit/re-enable.
	if err := svc.SoftDisable(ctx, fx.workspaceID, fx.bindingID, b.Version, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListEnabledForCaller(ctx, fx.workspaceID, fx.agentA)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.ID == fx.bindingID {
			t.Fatal("disabled binding still listed as enabled")
		}
	}
	all, err := svc.ListBindings(ctx, fx.workspaceID, fx.agentA)
	if err != nil {
		t.Fatal(err)
	}
	var found *agentdelegation.Binding
	for i := range all {
		if all[i].ID == fx.bindingID {
			found = &all[i]
			break
		}
	}
	if found == nil || found.Enabled {
		t.Fatalf("soft-disabled binding must remain visible; found=%v", found)
	}
	// Re-enable via update
	re, err := svc.UpdateBinding(ctx, agentdelegation.UpdateBindingInput{
		WorkspaceID: fx.workspaceID, BindingID: fx.bindingID,
		ExpectedVersion: found.Version, Enabled: boolPtr(true), ActorID: fx.ownerID,
	})
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if !re.Enabled {
		t.Fatal("re-enable failed")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestAuditPrewriteRequiresRunningParent(t *testing.T) {
	harness := dbtest.New(t)
	harness.MigrateToLatest(t)
	db := harness.Open(t)
	fx := seedDelegationFixture(t, db)
	// Parent run in SUCCEEDED — prewrite must fail
	startParentRun(t, db, fx)
	if _, err := db.Exec(`
		UPDATE agent_runs SET status='SUCCEEDED', finished_at=NOW(),
		 output_summary='{}'::jsonb, lock_version=lock_version+1 WHERE id=$1
	`, fx.parentRunID); err != nil {
		t.Fatal(err)
	}
	repo, _ := agentdelegation.NewRepository(db)
	target := fx.agentB
	_, _, err := repo.CreateDelegationAndStep(context.Background(), agentdelegation.CreateDelegationInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID, ParentRunID: fx.parentRunID,
		CallerAgentID: fx.agentA, TargetAgentID: &target,
		Mode: agentdelegation.ModeInline, Protocol: agentdelegation.ProtocolInternal,
		Origin: agentdelegation.OriginInternal, Depth: 1, BindingVersion: 1,
		ToolCallID: "x", IdempotencyKey: "unique-key-" + uuid.Must(uuid.NewV7()).String(),
		StepID: uuid.Must(uuid.NewV7()).String(), AgentID: fx.agentA,
	})
	if err == nil {
		t.Fatal("expected fail on non-running parent")
	}
}

// --- fixtures ---

type delFixture struct {
	ownerID, workspaceID   string
	modelID                string
	agentA, agentB, agentC string
	bindingID, parentRunID string
	sessionID              string
}

func seedDelegationFixture(t *testing.T, db *sql.DB) delFixture {
	t.Helper()
	fx := delFixture{
		ownerID:     uuid.Must(uuid.NewV7()).String(),
		workspaceID: uuid.Must(uuid.NewV7()).String(),
		modelID:     uuid.Must(uuid.NewV7()).String(),
		agentA:      uuid.Must(uuid.NewV7()).String(),
		agentB:      uuid.Must(uuid.NewV7()).String(),
		agentC:      uuid.Must(uuid.NewV7()).String(),
		bindingID:   uuid.Must(uuid.NewV7()).String(),
		parentRunID: uuid.Must(uuid.NewV7()).String(),
		sessionID:   uuid.Must(uuid.NewV7()).String(),
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\nSQL: %s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'del.owner','Del Owner')`, fx.ownerID)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'Del Space','SANDBOX',$3,$3,$3)`,
		fx.workspaceID, "del-"+fx.workspaceID[:8], fx.ownerID)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://example.test','m',$3,$3)`,
		fx.modelID, fx.workspaceID, fx.ownerID)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES
		($1,$4,'Agent A',$5,$6,$6),
		($2,$4,'Agent B',$5,$6,$6),
		($3,$4,'Agent C',$5,$6,$6)`,
		fx.agentA, fx.agentB, fx.agentC, fx.workspaceID, fx.modelID, fx.ownerID)
	exec(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'s',$4)`, fx.sessionID, fx.workspaceID, fx.agentA, fx.ownerID)
	return fx
}

func startParentRun(t *testing.T, db *sql.DB, fx delFixture) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
			id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
			triggered_by_id,trace_id,model_snapshot,capability_snapshot,context_policy_snapshot,
			authorization_snapshot,input_summary,agent_graph_snapshot
		) VALUES (
			$1,$2,$3,$4,'RUNNING','CHAT','USER',$5,$6,
			'{"modelName":"m"}'::jsonb,'{"releases":[]}'::jsonb,'{}'::jsonb,
			'{}'::jsonb,'{}'::jsonb,'{}'::jsonb
		)
	`, fx.parentRunID, fx.workspaceID, fx.sessionID, fx.agentA, fx.ownerID, "trace-"+fx.parentRunID[:8]); err != nil {
		t.Fatal(err)
	}
}

func assertDelegationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"agent_delegation_bindings", "agent_run_delegations",
		"agent_a2a_exposures", "agent_a2a_remote_bindings",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing table %s: exists=%v err=%v", table, exists, err)
		}
	}
	for _, col := range []string{"agent_id", "delegation_id", "parent_step_id"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='agent_run_steps' AND column_name=$1)
		`, col).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing agent_run_steps.%s", col)
		}
	}
	for _, col := range []string{"parent_run_id", "parent_delegation_id", "agent_graph_snapshot"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name='agent_runs' AND column_name=$1)
		`, col).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing agent_runs.%s", col)
		}
	}
}
