package agentdelegation_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// Residual #4/#12: combined callable namespace unique on create/update paths,
// concurrent-safe via advisory lock.

func TestCallableNamespace_InternalVsRemote_CreateUpdate(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedNSFixture(t, db)
	delRepo, _ := agentdelegation.NewRepository(db)
	delSvc, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)
	ctx := context.Background()

	// Create internal binding.
	b, err := delSvc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws,
		CallerAgentID: fx.agentA, TargetAgentID: fx.agentB,
		CallableName: "shared_tool", Mode: agentdelegation.ModeInline,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Remote with same name must conflict.
	_, err = a2aRepo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		CallableName: "shared_tool", EndpointURL: "https://1.1.1.1/a2a",
		AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 5000, Enabled: true, ActorID: fx.owner,
	})
	if !errors.Is(err, a2agateway.ErrNamespaceConflict) && !errors.Is(err, a2agateway.ErrConflict) {
		t.Fatalf("create remote conflict: %v", err)
	}

	// Create remote under different name, then rename into conflict.
	remote, err := a2aRepo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		CallableName: "other_remote", EndpointURL: "https://1.1.1.1/a2a",
		AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 5000, Enabled: true, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	cn := "shared_tool"
	_, err = a2aRepo.UpdateRemote(ctx, a2agateway.UpdateRemoteInput{
		WorkspaceID: fx.ws, BindingID: remote.ID, ExpectedVersion: remote.Version,
		CallableName: &cn, ActorID: fx.owner,
	})
	if !errors.Is(err, a2agateway.ErrNamespaceConflict) && !errors.Is(err, a2agateway.ErrConflict) {
		t.Fatalf("update remote conflict: %v", err)
	}

	// Soft-disable internal → remote may take the name.
	if err := delSvc.SoftDisable(ctx, fx.ws, b.ID, b.Version, fx.owner); err != nil {
		t.Fatal(err)
	}
	// Refresh remote version after failed update (unchanged).
	remote, _ = a2aRepo.GetRemote(ctx, fx.ws, remote.ID)
	_, err = a2aRepo.UpdateRemote(ctx, a2agateway.UpdateRemoteInput{
		WorkspaceID: fx.ws, BindingID: remote.ID, ExpectedVersion: remote.Version,
		CallableName: &cn, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatalf("after soft-disable should allow: %v", err)
	}

	// Re-enable internal with same name must fail (remote holds it).
	b, _ = delSvc.GetBinding(ctx, fx.ws, b.ID)
	en := true
	_, err = delSvc.UpdateBinding(ctx, agentdelegation.UpdateBindingInput{
		WorkspaceID: fx.ws, BindingID: b.ID, ExpectedVersion: b.Version,
		Enabled: &en, ActorID: fx.owner,
	})
	if !errors.Is(err, agentdelegation.ErrNamespaceConflict) && !errors.Is(err, agentdelegation.ErrDuplicateAlias) {
		// Update without renaming may only check if re-enabling conflicts.
		if err == nil {
			t.Fatal("re-enable internal with live remote same name must fail")
		}
	}
}

func TestCallableNamespace_ConcurrentCreateOnlyOneWins(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedNSFixture(t, db)
	delRepo, _ := agentdelegation.NewRepository(db)
	delSvc, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)
	ctx := context.Background()

	const n = 8
	var okCount atomic.Int64
	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := delSvc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
				ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws,
				CallerAgentID: fx.agentA, TargetAgentID: fx.agentB,
				CallableName: "race_name", Mode: agentdelegation.ModeInline,
				ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.owner,
			})
			if err == nil {
				okCount.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			_, err := a2aRepo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
				ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
				CallableName: "race_name", EndpointURL: "https://1.1.1.1/a2a",
				AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 5000, Enabled: true, ActorID: fx.owner,
			})
			if err == nil {
				okCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("concurrent namespace winners=%d want 1", okCount.Load())
	}
}

type nsFx struct{ owner, ws, model, agentA, agentB string }

func seedNSFixture(t *testing.T, db *sql.DB) nsFx {
	t.Helper()
	fx := nsFx{
		owner: uuid.Must(uuid.NewV7()).String(), ws: uuid.Must(uuid.NewV7()).String(),
		model: uuid.Must(uuid.NewV7()).String(), agentA: uuid.Must(uuid.NewV7()).String(),
		agentB: uuid.Must(uuid.NewV7()).String(),
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'ns.o','N')`, fx.owner)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'N','SANDBOX',$3,$3,$3)`, fx.ws, "ns-"+fx.ws[:8], fx.owner)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, fx.model, fx.ws, fx.owner)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'A',$3,$4,$4),($5,$2,'B',$3,$4,$4)`, fx.agentA, fx.ws, fx.model, fx.owner, fx.agentB)
	return fx
}
