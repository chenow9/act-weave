package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/contextwindow"
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
// not in a manifest, so nothing is assembled or persisted before the engine call.
// The tool results being injected are still preflighted — see
// preflightAgenticResume — because they are new material on the wire.
func (b *Bridge) driveAgenticResume(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	checkpointID string,
	targets map[string]any,
) (text string, streamMessageID string, err error) {
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
	// The engine checks this too, but only after runAgenticTurn has opened the
	// assistant item: a checkpoint belonging to another run would be refused with
	// an item.started already on the client's stream and nothing ever finishing
	// it. Ownership is decidable from the ID alone, so decide it first.
	if _, err := einoruntime.EnsureAgentRunCheckpointID(
		job.WorkspaceID, job.RunID, checkpointID,
	); err != nil {
		return "", "", err
	}

	plan, err := b.planAgenticRun(ctx, job, run)
	if err != nil {
		return "", "", err
	}

	if err := preflightAgenticResume(plan, targets); err != nil {
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

// preflightAgenticResume bounds what a resume adds to the restored conversation.
//
// A resume assembles nothing, but it still sends a whole request: the checkpoint's
// conversation plus the tool results just approved. A tool that answers with tens
// of thousands of lines therefore overflows the window on a turn the user has
// already approved — and without this check that overflow is discovered by the
// provider, as a raw upstream error, instead of by the same
// CONTEXT_REQUIRED_INPUT_TOO_LARGE the initial turn would have returned.
//
// The bound is deliberately a lower bound: the restored history is inside the
// checkpoint and cannot be counted here, so this weighs the frozen system prompt,
// the tool exposure and the approved results. That is enough to catch the case
// this protects against, because an oversized result fails the bound on its own;
// what it cannot do is prove a request that passes will fit.
func preflightAgenticResume(plan *agenticFrozenPlan, targets map[string]any) error {
	if plan == nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	profile := strings.TrimSpace(plan.policy.TokenizerProfile)
	if profile == "" || plan.policy.ModelContextWindowTokens <= 0 {
		return execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	est, err := contextwindow.NewEstimator(profile)
	if err != nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	// Sorted so the estimate for one payload is the same on every attempt.
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	messages := make([]contextwindow.Message, 0, len(names))
	for _, name := range names {
		encoded, err := json.Marshal(targets[name])
		if err != nil {
			return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
		}
		messages = append(messages, contextwindow.Message{
			Role: contextwindow.RoleTool, Name: name, Content: string(encoded),
		})
	}

	got, err := est.EstimateAgenticRequest(
		plan.instruction, toolExposureFromCatalog(plan.catalog), messages)
	if err != nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	_, preErr := contextwindow.PreflightAgenticMandatory(contextwindow.AgenticPreflightInput{
		ModelContextWindowTokens: plan.policy.ModelContextWindowTokens,
		OutputReserveTokens:      plan.policy.OutputReserveTokens,
		SafetyMarginTokens:       plan.policy.SafetyMarginTokens,
		DynamicReserveTokens:     got.DynamicToolLoadReserveTokens,
		MandatoryTokens:          got.InitialVisibleTokens,
		MaxLoadedToolCount:       got.MaxLoadedToolCount,
		ActualLoadedToolCount:    0,
	})
	if preErr != nil {
		if errors.Is(preErr, contextwindow.ErrMandatoryInputTooLarge) ||
			errors.Is(preErr, contextwindow.ErrDynamicToolReserveExceeded) {
			return execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
		}
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	return nil
}
