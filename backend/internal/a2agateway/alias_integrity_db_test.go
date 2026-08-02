package a2agateway_test

import (
	"context"
	"strings"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestInboundTaskAlias_DBEnforcesExposureMatchAndImmutability proves database-layer
// permanent evidence: composite FK (workspace+exposure+task), RESTRICT delete,
// and UPDATE/DELETE anti-tamper triggers — not merely Go checks.
func TestInboundTaskAlias_DBEnforcesExposureMatchAndImmutability(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	runRepo, _ := execution.NewRunRepository(db)

	// Two exposures (different agents — unique per agent); cross-exposure attach must fail.
	exp1 := uuid.Must(uuid.NewV7()).String()
	exp2 := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub1','d',true,'AGENT_ACCESS',1,$4,$4)
	`, exp1, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub2','d',true,'AGENT_ACCESS',1,$4,$4)
	`, exp2, fx.workspaceID, fx.agentB, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	// Authoritative inbound task under exp1.
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			return "ok", nil
		},
	}
	// Prepare + claim via repository (same production path).
	runID, err := runner.PrepareRun(ctx, a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "alias integrity",
		TraceID: "tr-alias-integrity", ActorType: "USER", ActorID: fx.ownerID,
		IdempotencyKey: "ctx-ai|msg-ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: exp1, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "ctx-ai|msg-ai", ExternalTaskID: "task-primary",
		ExternalContextID: "ctx-ai", ExternalMessageID: "msg-ai",
		RequestHash: a2agateway.RequestBodyHash("alias integrity"),
		RunID:       runID, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Claim also registers primary alias; ensure present.
	if err := repo.RegisterInboundTaskAlias(ctx, fx.workspaceID, exp1, "USER", fx.ownerID, "task-primary", task.ID); err != nil {
		t.Fatal(err)
	}
	// Second alias (as if replica B minted another TaskID).
	if err := repo.RegisterInboundTaskAlias(ctx, fx.workspaceID, exp1, "USER", fx.ownerID, "task-replica-b", task.ID); err != nil {
		t.Fatal(err)
	}

	// --- Cross-exposure INSERT must fail (composite FK) ---
	_, err = db.Exec(`
		INSERT INTO agent_a2a_inbound_task_aliases(
			workspace_id, exposure_id, actor_type, actor_id, external_task_id, inbound_task_id
		) VALUES ($1,$2,'USER',$4,'evil-cross-exp',$3)
	`, fx.workspaceID, exp2, task.ID, fx.ownerID)
	if err == nil {
		t.Fatal("cross-exposure alias insert must fail at DB FK")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") &&
		!strings.Contains(strings.ToLower(err.Error()), "violates") {
		t.Fatalf("want FK violation, got %v", err)
	}

	// --- Rebind UPDATE inbound_task_id must fail (immutability trigger) ---
	// Need a second task to attempt rebind toward.
	run2, err := runner.PrepareRun(ctx, a2agateway.InboundRunRequest{
		WorkspaceID: fx.workspaceID, AgentID: fx.agentA, UserText: "other",
		TraceID: "tr-other", ActorType: "USER", ActorID: fx.ownerID,
		IdempotencyKey: "other-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	task2, _, err := repo.ClaimInboundTask(ctx, a2agateway.InboundTask{
		WorkspaceID: fx.workspaceID, ExposureID: exp1, AgentID: fx.agentA,
		ActorType: "USER", ActorID: fx.ownerID,
		ExternalKey: "other-key", ExternalTaskID: "task-other",
		RequestHash: a2agateway.RequestBodyHash("other"),
		RunID:       run2, Status: "RUNNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		UPDATE agent_a2a_inbound_task_aliases
		SET inbound_task_id=$3
		WHERE workspace_id=$1 AND exposure_id=$2 AND external_task_id='task-replica-b'
	`, fx.workspaceID, exp1, task2.ID)
	if err == nil {
		t.Fatal("rebind UPDATE must fail at immutability trigger")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permanent") &&
		!strings.Contains(strings.ToLower(err.Error()), "immutable") &&
		!strings.Contains(strings.ToLower(err.Error()), "update denied") {
		t.Fatalf("want immutability error on rebind, got %v", err)
	}

	// --- DELETE alias must fail ---
	_, err = db.Exec(`
		DELETE FROM agent_a2a_inbound_task_aliases
		WHERE workspace_id=$1 AND exposure_id=$2 AND external_task_id='task-replica-b'
	`, fx.workspaceID, exp1)
	if err == nil {
		t.Fatal("DELETE alias must fail at immutability trigger")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permanent") &&
		!strings.Contains(strings.ToLower(err.Error()), "delete denied") {
		t.Fatalf("want immutability error on delete, got %v", err)
	}

	// --- DELETE authority inbound task must RESTRICT while aliases exist ---
	_, err = db.Exec(`DELETE FROM agent_a2a_inbound_tasks WHERE id=$1`, task.ID)
	if err == nil {
		t.Fatal("DELETE inbound task with aliases must RESTRICT")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") &&
		!strings.Contains(strings.ToLower(err.Error()), "restrict") &&
		!strings.Contains(strings.ToLower(err.Error()), "violates") {
		t.Fatalf("want FK RESTRICT on task delete, got %v", err)
	}

	// Alias still present and unchanged.
	var n int
	var bound string
	_ = db.QueryRow(`
		SELECT COUNT(*), MAX(inbound_task_id::text)
		FROM agent_a2a_inbound_task_aliases
		WHERE workspace_id=$1 AND exposure_id=$2 AND external_task_id='task-replica-b'
	`, fx.workspaceID, exp1).Scan(&n, &bound)
	if n != 1 || bound != task.ID {
		t.Fatalf("alias corrupted n=%d bound=%s want task=%s", n, bound, task.ID)
	}

	// Same-id idempotent register still OK (Go path).
	if err := repo.RegisterInboundTaskAlias(ctx, fx.workspaceID, exp1, "USER", fx.ownerID, "task-replica-b", task.ID); err != nil {
		t.Fatalf("idempotent same-id register: %v", err)
	}
	// Different inbound id for same external task must fail at Go layer (and never UPDATE).
	if err := repo.RegisterInboundTaskAlias(ctx, fx.workspaceID, exp1, "USER", fx.ownerID, "task-replica-b", task2.ID); err == nil {
		t.Fatal("rebind via Register must fail")
	}
}
