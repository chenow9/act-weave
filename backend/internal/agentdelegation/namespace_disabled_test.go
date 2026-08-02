package agentdelegation_test

import (
	"context"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// TestDisabledDoesNotOccupyNamespace_EnableChecksAtomically unifies semantics:
// disabled create/update free; enable/re-enable takes advisory lock and rejects
// collisions across internal/remote.
func TestDisabledDoesNotOccupyNamespace_EnableChecksAtomically(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedNSFixture(t, db)

	delRepo, _ := agentdelegation.NewRepository(db)
	a2aRepo, _ := a2agateway.NewRepository(db)

	name := "shared_callable"
	// 1) Create disabled internal binding with name — OK.
	b1, err := delRepo.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		TargetAgentID: fx.agentB, CallableName: name, Mode: agentdelegation.ModeTask,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: false, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatalf("disabled internal create: %v", err)
	}
	// 2) Create disabled remote with same name — OK (disabled free).
	r1, err := a2aRepo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		CallableName: name, EndpointURL: "https://1.1.1.1/a2a",
		AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 5000, Enabled: false, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatalf("disabled remote create: %v", err)
	}
	// 3) Enable internal — succeeds (remote still disabled).
	en := true
	b1, err = delRepo.UpdateBinding(ctx, agentdelegation.UpdateBindingInput{
		WorkspaceID: fx.ws, BindingID: b1.ID, ExpectedVersion: b1.Version,
		Enabled: &en, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatalf("enable internal: %v", err)
	}
	// 4) Enable remote — must conflict with enabled internal.
	_, err = a2aRepo.UpdateRemote(ctx, a2agateway.UpdateRemoteInput{
		WorkspaceID: fx.ws, BindingID: r1.ID, ExpectedVersion: r1.Version,
		Enabled: &en, ActorID: fx.owner,
	})
	if err == nil {
		t.Fatal("enable remote against enabled internal must conflict")
	}
	// 5) Create enabled internal with same name — conflict.
	_, err = delRepo.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		TargetAgentID: fx.agentB, CallableName: name, Mode: agentdelegation.ModeTask,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.owner,
	})
	if err == nil {
		t.Fatal("enabled create colliding name must fail")
	}
	// 6) Soft-disable internal, then enable remote — OK.
	if err := delRepo.SoftDisable(ctx, fx.ws, b1.ID, b1.Version, fx.owner); err != nil {
		t.Fatal(err)
	}
	r1, _ = a2aRepo.GetRemote(ctx, fx.ws, r1.ID)
	r1, err = a2aRepo.UpdateRemote(ctx, a2agateway.UpdateRemoteInput{
		WorkspaceID: fx.ws, BindingID: r1.ID, ExpectedVersion: r1.Version,
		Enabled: &en, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatalf("enable remote after internal disable: %v", err)
	}
	// 7) Re-enable internal — conflict with remote.
	b1, _ = delRepo.GetBinding(ctx, fx.ws, b1.ID)
	_, err = delRepo.UpdateBinding(ctx, agentdelegation.UpdateBindingInput{
		WorkspaceID: fx.ws, BindingID: b1.ID, ExpectedVersion: b1.Version,
		Enabled: &en, ActorID: fx.owner,
	})
	if err == nil {
		t.Fatal("re-enable internal against enabled remote must conflict")
	}
}

// TestThreeSourceNamespaceRace_InternalRemote: concurrent enable of same
// callable name from internal vs remote → exactly one wins.
func TestThreeSourceNamespaceRace_InternalRemote(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()
	fx := seedNSFixture(t, db)
	delRepo, _ := agentdelegation.NewRepository(db)
	a2aRepo, _ := a2agateway.NewRepository(db)
	name := "race_name_en"

	b, err := delRepo.CreateBinding(ctx, agentdelegation.CreateBindingInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		TargetAgentID: fx.agentB, CallableName: name, Mode: agentdelegation.ModeTask,
		ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: false, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := a2aRepo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agentA,
		CallableName: name, EndpointURL: "https://1.1.1.1/a2a",
		AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 5000, Enabled: false, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type res struct {
		who string
		err error
	}
	ch := make(chan res, 2)
	en := true
	go func() {
		<-start
		_, err := delRepo.UpdateBinding(ctx, agentdelegation.UpdateBindingInput{
			WorkspaceID: fx.ws, BindingID: b.ID, ExpectedVersion: b.Version,
			Enabled: &en, ActorID: fx.owner,
		})
		ch <- res{who: "internal", err: err}
	}()
	go func() {
		<-start
		_, err := a2aRepo.UpdateRemote(ctx, a2agateway.UpdateRemoteInput{
			WorkspaceID: fx.ws, BindingID: r.ID, ExpectedVersion: r.Version,
			Enabled: &en, ActorID: fx.owner,
		})
		ch <- res{who: "remote", err: err}
	}()
	close(start)
	r1, r2 := <-ch, <-ch
	ok, fail := 0, 0
	for _, x := range []res{r1, r2} {
		if x.err == nil {
			ok++
		} else {
			fail++
		}
	}
	if ok != 1 || fail != 1 {
		t.Fatalf("race: want 1 success 1 fail; got ok=%d fail=%d r1=%v r2=%v", ok, fail, r1, r2)
	}
}
