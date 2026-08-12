package chatruntimebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
)

// driveAgenticResume is the Task 4B production resume path for a confirmation
// that the Agentic runtime paused.
//
// It is not a variant of the classic seam. The classic seam reads live agent and
// model config to rebuild its agent, which for a resume is worse than for an
// initial turn: the conversation is already half-executed against frozen
// identity, so a live read that has since changed would resume it against a
// different model or a different tool set than the one that paused. Everything
// here comes from the same frozen documents through planAgenticRun, in the same
// order, and the agent is built by the same function the initial turn uses.
//
// There is no assembly phase: the message history lives in the adk checkpoint,
// not in a manifest, so nothing is estimated or persisted before the engine call.
func (b *Bridge) driveAgenticResume(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	checkpointID string,
	targets map[string]any,
) (text string, streamMessageID string, err error) {
	// Same trusted-workspace binding drive() establishes: tools resolved from the
	// frozen catalog authorize against it, so a resume must not run without it.
	ctx = einoruntime.WithTrustedWorkspaceID(ctx, job.WorkspaceID)

	if b.buildAgenticModel == nil {
		return "", "", errors.New("chatruntimebridge: agentic model builder is not configured")
	}
	if b.agenticEngine == nil {
		return "", "", errors.New("chatruntimebridge: agentic engine is not configured")
	}
	if strings.TrimSpace(checkpointID) == "" {
		return "", "", errors.New("chatruntimebridge: checkpoint id is required for agentic resume")
	}
	if len(targets) == 0 {
		// The engine also refuses this. Refusing here keeps a resume without tool
		// results from reaching a model at all.
		return "", "", errors.New("chatruntimebridge: resume targets are required for agentic resume")
	}

	plan, err := b.planAgenticRun(ctx, job, run)
	if err != nil {
		return "", "", err
	}

	built, err := b.buildAgenticAgentFromPlan(ctx, run, plan)
	if err != nil {
		return "", "", err
	}

	return b.runAgenticTurn(ctx, job, run, built, func(
		turnCtx context.Context,
		agent adk.TypedAgent[*schema.AgenticMessage],
		projector einoruntime.ProtocolProjector,
	) (*einoruntime.AgenticRunResult, error) {
		return b.agenticEngine.Resume(turnCtx, agent, einoruntime.AgenticResumeInput{
			WorkspaceID:  job.WorkspaceID,
			RunID:        job.RunID,
			CheckpointID: checkpointID,
			Targets:      targets,
			Projector:    projector,
		})
	})
}
