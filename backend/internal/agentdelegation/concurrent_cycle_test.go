package agentdelegation_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

// TestConcurrentCreateBindings_AB_vs_BA_OnlyOneSucceeds proves item 18:
// workspace graph advisory lock serializes cycle checks so concurrent A→B and
// B→A enabled creates cannot both succeed.
func TestConcurrentCreateBindings_AB_vs_BA_OnlyOneSucceeds(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	ctx := context.Background()

	fx := seedDelegationFixture(t, db)
	repo, err := agentdelegation.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := agentdelegation.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, e := svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
			ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
			CallerAgentID: fx.agentA, TargetAgentID: fx.agentB,
			CallableName: "to_b", Mode: agentdelegation.ModeInline,
			ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.ownerID,
		})
		errs <- e
	}()
	go func() {
		defer wg.Done()
		<-start
		_, e := svc.CreateBinding(ctx, agentdelegation.CreateBindingInput{
			ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
			CallerAgentID: fx.agentB, TargetAgentID: fx.agentA,
			CallableName: "to_a", Mode: agentdelegation.ModeInline,
			ContextPolicy: agentdelegation.ContextTaskOnly, Enabled: true, ActorID: fx.ownerID,
		})
		errs <- e
	}()
	close(start)
	wg.Wait()
	close(errs)

	var okN, cycleN, other []error
	for e := range errs {
		switch {
		case e == nil:
			okN = append(okN, nil)
		case errors.Is(e, agentdelegation.ErrCycle):
			cycleN = append(cycleN, e)
		default:
			other = append(other, e)
		}
	}
	if len(okN) != 1 {
		t.Fatalf("want exactly 1 success, got %d successes other=%v cycle=%d", len(okN), other, len(cycleN))
	}
	if len(cycleN)+len(other) != 1 {
		t.Fatalf("want exactly 1 failure (cycle/conflict), cycle=%d other=%v", len(cycleN), other)
	}
	if len(cycleN) == 0 && len(other) == 1 {
		// ErrConflict also acceptable if version/namespace race surfaces that way.
		t.Logf("non-cycle failure (acceptable if conflict): %v", other[0])
	}

	// Graph must remain acyclic.
	edges, err := repo.ListAllEnabled(ctx, fx.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	snaps := make([]agentdelegation.GraphEdgeSnapshot, 0, len(edges))
	for _, b := range edges {
		snaps = append(snaps, agentdelegation.GraphEdgeSnapshot{
			BindingID: b.ID, CallerAgentID: b.CallerAgentID, TargetAgentID: b.TargetAgentID,
			CallableName: b.CallableName, Mode: b.Mode, Protocol: agentdelegation.ProtocolInternal,
		})
	}
	if err := agentdelegation.DetectCycle(snaps); err != nil {
		t.Fatalf("enabled graph has cycle after concurrent creates: %v edges=%+v", err, snaps)
	}
}
