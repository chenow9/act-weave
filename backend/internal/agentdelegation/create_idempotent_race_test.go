package agentdelegation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestCreateDelegationAndStep_ConcurrentSameKey_OneNewOneReplay drives two
// concurrent CreateDelegationAndStep calls with the same workspace+idempotency key
// but different candidate IDs. Exactly one durable delegation + paired step must
// exist; one call is new and one is replay, both returning the same durable IDs.
func TestCreateDelegationAndStep_ConcurrentSameKey_OneNewOneReplay(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()

	// Minimal fixtures via agentdelegation integration helper pattern.
	owner := uuid.Must(uuid.NewV7()).String()
	ws := uuid.Must(uuid.NewV7()).String()
	model := uuid.Must(uuid.NewV7()).String()
	agent := uuid.Must(uuid.NewV7()).String()
	session := uuid.Must(uuid.NewV7()).String()
	runID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'race','Race')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'race-ws','Race','PRODUCTION',$2,$2,$2)
	`, ws, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://x','m',$3,$3)
	`, model, ws, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'a',$3,$4,$4)
	`, agent, ws, model, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'s',$4)
	`, session, ws, agent, owner); err != nil {
		t.Fatal(err)
	}
	runRepo, _ := execution.NewRunRepository(db)
	// Start a RUNNING parent run the way production does.
	if _, err := db.Exec(`
		INSERT INTO agent_runs(
			id,workspace_id,session_id,agent_id,status,trigger_type,
			triggered_by_type,triggered_by_id,trace_id,
			model_snapshot,capability_snapshot,context_policy_snapshot
		) VALUES ($1,$2,$3,$4,'RUNNING','CHAT','USER',$5,'tr',
			'{}'::jsonb,'{}'::jsonb,'{}'::jsonb)
	`, runID, ws, session, agent, owner); err != nil {
		t.Fatal(err)
	}
	_ = runRepo

	repo, err := agentdelegation.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := agentdelegation.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}

	idem := "race-key-" + uuid.Must(uuid.NewV7()).String()
	var barrier sync.WaitGroup
	barrier.Add(2)
	start := make(chan struct{})

	type outcome struct {
		del    agentdelegation.Delegation
		replay bool
		err    error
	}
	out := make([]outcome, 2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer barrier.Done()
			<-start
			delID := uuid.Must(uuid.NewV7()).String()
			stepID := uuid.Must(uuid.NewV7()).String()
			target := agent
			d, replay, err := svc.CreateDelegationAndStep(ctx, agentdelegation.CreateDelegationInput{
				ID: delID, WorkspaceID: ws, ParentRunID: runID,
				CallerAgentID: agent, TargetAgentID: &target, Mode: agentdelegation.ModeInline,
				Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
				Depth: 1, BindingVersion: 1, ToolCallID: "tool-1",
				IdempotencyKey: idem,
				InputSummary:   []byte(`{"k":1}`), InputPayload: []byte(`{"p":1}`),
				StepID: stepID, AgentID: agent,
			})
			out[i] = outcome{del: d, replay: replay, err: err}
		}()
	}
	// Release both near-simultaneously.
	close(start)
	done := make(chan struct{})
	go func() {
		barrier.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent create timed out")
	}

	if out[0].err != nil || out[1].err != nil {
		t.Fatalf("errors: %v / %v", out[0].err, out[1].err)
	}
	newCount, replayCount := 0, 0
	for _, o := range out {
		if o.replay {
			replayCount++
		} else {
			newCount++
		}
	}
	if newCount != 1 || replayCount != 1 {
		t.Fatalf("want 1 new + 1 replay, got new=%d replay=%d ids=%s/%s",
			newCount, replayCount, out[0].del.ID, out[1].del.ID)
	}
	if out[0].del.ID != out[1].del.ID {
		t.Fatalf("both must return same durable delegation id: %s vs %s", out[0].del.ID, out[1].del.ID)
	}
	if out[0].del.StepID == "" || out[0].del.StepID != out[1].del.StepID {
		t.Fatalf("paired step must match: %q vs %q", out[0].del.StepID, out[1].del.StepID)
	}
	var delN, stepN int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_delegations WHERE workspace_id=$1 AND idempotency_key=$2`, ws, idem).Scan(&delN)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_run_steps WHERE workspace_id=$1 AND delegation_id=$2 AND step_type='AGENT_DELEGATION'`,
		ws, out[0].del.ID).Scan(&stepN)
	if delN != 1 || stepN != 1 {
		t.Fatalf("want exactly 1 del + 1 step, got del=%d step=%d", delN, stepN)
	}
}
