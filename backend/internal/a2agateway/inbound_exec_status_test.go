package a2agateway_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

// TestProductionJSONRPC_ExecuteRunError_IsFAILEDNotCancelled proves that after
// ExecuteRun returns a normal error, post-execute execCancel must not reclassify
// the terminal as CANCELLED (task/run/delegation/step all FAILED).
func TestProductionJSONRPC_ExecuteRunError_IsFAILEDNotCancelled(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)

	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			// Ordinary model/runner failure — parent context still live.
			return "", errors.New("model provider 500: upstream boom")
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test", auth, a2agateway.WithLeaseTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st, body := postMessageSendCtx(t, srv.URL, fx.workspaceID, fx.agentA, "fail please", "", "msg-fail-status", "ctx-fail-status")
	if st != http.StatusOK {
		t.Fatalf("status=%d body=%s", st, body)
	}

	var taskSt, runSt, delSt, stepSt string
	var runID, delID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = db.QueryRow(`
			SELECT t.status, t.run_id, COALESCE(t.delegation_id::text,''),
			       COALESCE(r.status,''), COALESCE(d.status,'')
			FROM agent_a2a_inbound_tasks t
			LEFT JOIN agent_runs r ON r.id = t.run_id
			LEFT JOIN agent_run_delegations d ON d.id = t.delegation_id
			WHERE t.workspace_id=$1 AND t.exposure_id=$2
		`, fx.workspaceID, expID).Scan(&taskSt, &runID, &delID, &runSt, &delSt)
		if taskSt != "" && taskSt != "RUNNING" && delID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if taskSt == "" || runID == "" || delID == "" {
		t.Fatalf("incomplete durable rows task=%s run=%s del=%s", taskSt, runID, delID)
	}
	_ = db.QueryRow(`
		SELECT status FROM agent_run_steps
		WHERE workspace_id=$1 AND delegation_id=$2 AND step_type='AGENT_DELEGATION'
		LIMIT 1
	`, fx.workspaceID, delID).Scan(&stepSt)

	if taskSt != "FAILED" || runSt != "FAILED" || delSt != "FAILED" || stepSt != "FAILED" {
		t.Fatalf("want all FAILED; task=%s run=%s del=%s step=%s body=%s", taskSt, runSt, delSt, stepSt, body)
	}
	if taskSt == "CANCELLED" || runSt == "CANCELLED" || delSt == "CANCELLED" {
		t.Fatal("ordinary execute error must not be CANCELLED")
	}
	_ = ctx
}
