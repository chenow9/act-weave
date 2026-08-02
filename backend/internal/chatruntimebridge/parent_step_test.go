package chatruntimebridge

import (
	"testing"

	"actweave/backend/internal/agentdelegation"
)

func TestSameRunParentStep_TASKSkipsCrossRun(t *testing.T) {
	t.Parallel()
	parentStep := "step-parent"
	// TASK: child run != parent run
	rc := &agentdelegation.RunContext{
		RunID: "child-run", ParentRunID: "parent-run", ParentStepID: &parentStep,
	}
	if sameRunParentStep(rc) {
		t.Fatal("TASK child must not set parent_step_id across runs")
	}
	// INLINE: same run
	rc.RunID = "parent-run"
	if !sameRunParentStep(rc) {
		t.Fatal("INLINE nested steps should keep parent_step_id")
	}
	rc.ParentStepID = nil
	if sameRunParentStep(rc) {
		t.Fatal("nil parent step")
	}
}
