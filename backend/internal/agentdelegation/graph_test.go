package agentdelegation

import "testing"

func TestDetectCycle_RejectsLoop(t *testing.T) {
	t.Parallel()
	err := DetectCycle([]GraphEdgeSnapshot{
		{CallerAgentID: "A", TargetAgentID: "B", CallableName: "b"},
		{CallerAgentID: "B", TargetAgentID: "A", CallableName: "a"},
	})
	if err == nil {
		t.Fatal("expected cycle")
	}
}

func TestDetectCycle_AcceptsDAG(t *testing.T) {
	t.Parallel()
	err := DetectCycle([]GraphEdgeSnapshot{
		{CallerAgentID: "A", TargetAgentID: "B", CallableName: "b"},
		{CallerAgentID: "A", TargetAgentID: "C", CallableName: "c"},
		{CallerAgentID: "B", TargetAgentID: "C", CallableName: "c2"},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestDetectCycle_SelfLoop(t *testing.T) {
	t.Parallel()
	err := DetectCycle([]GraphEdgeSnapshot{
		{CallerAgentID: "A", TargetAgentID: "A", CallableName: "self"},
	})
	if err == nil {
		t.Fatal("expected self-loop")
	}
}

func TestMaxReachableDepth(t *testing.T) {
	t.Parallel()
	d := MaxReachableDepth("A", []GraphEdgeSnapshot{
		{CallerAgentID: "A", TargetAgentID: "B"},
		{CallerAgentID: "B", TargetAgentID: "C"},
	})
	if d != 2 {
		t.Fatalf("depth=%d want 2", d)
	}
}

func TestBudget_Limits(t *testing.T) {
	t.Parallel()
	b := NewBudget()
	b.MaxDepth = 2
	b.MaxTotal = 2
	b.MaxPerBinding = 1
	if err := b.CheckAndReserve(1, "x"); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckAndReserve(1, "x"); err == nil {
		t.Fatal("per-binding")
	}
	if err := b.CheckAndReserve(1, "y"); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckAndReserve(1, "z"); err == nil {
		t.Fatal("total")
	}
	if err := b.CheckAndReserve(3, "z"); err == nil {
		t.Fatal("depth")
	}
	// Release one slot and re-reserve.
	b.Release("y")
	if err := b.CheckAndReserve(1, "z"); err != nil {
		t.Fatal(err)
	}
}
