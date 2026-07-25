package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Engine drives a ChatModelAgent via adk.Runner with true streaming (D14)
// and once-per-run checkpoint IDs. It does not wire chatruntimebridge continue
// (PR7) or application production switch.
type Engine struct {
	store adk.CheckPointStore
}

// EngineConfig constructs an Engine.
type EngineConfig struct {
	// Store is the ADK checkpoint store. Optional for text-only runs that
	// never interrupt; required for HITL interrupt persistence.
	Store adk.CheckPointStore
}

// NewEngine builds an Engine.
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{store: cfg.Store}
}

// RunInput is one agent execution request.
type RunInput struct {
	// WorkspaceID is required for checkpoint ID allocation.
	WorkspaceID string
	// RunID is the agent_run owner segment (OwnerID).
	RunID string
	// CheckpointID, when non-empty and valid for this workspace/run, is reused
	// (stable once-per-run). Empty → allocate a new nonce once.
	CheckpointID string
	// Messages is the conversation input (typically user message(s)).
	Messages []*schema.Message
	// Projector receives item.delta-style text hooks. Optional (NopProjector).
	Projector ProtocolProjector
}

// RunResult is the outcome of Engine.Run. Interrupt is not a hard failure:
// Interrupted=true with InterruptContextIDs for later ResumeWithParams (PR7).
type RunResult struct {
	// CheckpointID is the stable ID used for this run (allocated or ensured).
	CheckpointID string
	// Interrupted is true when the agent paused for HITL (tool confirmation).
	Interrupted bool
	// InterruptContextIDs are InterruptCtx.ID values for ResumeWithParams Targets.
	InterruptContextIDs []string
	// RootCauseInterruptIDs are IsRootCause interrupt IDs (preferred resume targets).
	RootCauseInterruptIDs []string
	// FinalAssistantText is concatenated assistant content deltas for this run.
	FinalAssistantText string
	// Err is a hard failure (budget / model / internal). Nil on success or clean interrupt.
	Err error
}

// EnsureAgentRunCheckpointID returns a stable checkpoint ID for one agent run.
//
// If existing is a valid agent_run ID for workspaceID/runID, it is returned as-is
// (once-per-run reuse). Otherwise a new ID with a fresh nonce is allocated.
func EnsureAgentRunCheckpointID(workspaceID, runID, existing string) (string, error) {
	ws := strings.TrimSpace(workspaceID)
	owner := strings.TrimSpace(runID)
	if ws == "" || owner == "" {
		return "", fmt.Errorf("%w: workspaceID and runID are required", ErrInvalidCheckpointID)
	}

	if trimmed := strings.TrimSpace(existing); trimmed != "" {
		parsed, err := ParseCheckpointID(trimmed)
		if err != nil {
			return "", err
		}
		if parsed.WorkspaceID != ws {
			return "", fmt.Errorf(
				"%w: existing checkpoint workspace %q does not match %q",
				ErrInvalidCheckpointID, parsed.WorkspaceID, ws,
			)
		}
		if parsed.Kind != CheckpointKindAgentRun {
			return "", fmt.Errorf(
				"%w: existing checkpoint kind %q is not %q",
				ErrInvalidCheckpointID, parsed.Kind, CheckpointKindAgentRun,
			)
		}
		if parsed.OwnerID != owner {
			return "", fmt.Errorf(
				"%w: existing checkpoint owner %q does not match run %q",
				ErrInvalidCheckpointID, parsed.OwnerID, owner,
			)
		}
		return parsed.Raw, nil
	}

	nonce, err := uuid.NewV7()
	if err != nil {
		// Fallback: random UUID is still a stable once-allocated suffix.
		nonce = uuid.New()
	}
	return FormatCheckpointID(ws, CheckpointKindAgentRun, owner, nonce.String())
}

// Run executes agent with EnableStreaming and a once-per-run checkpoint ID.
//
// On tool HITL interrupt, captures InterruptContext IDs into the result and
// returns (result, nil) — callers must not treat interrupt as a hard error.
// Hard failures (budget exceeded family, model errors) set result.Err and
// return it as the error value.
func (e *Engine) Run(ctx context.Context, agent adk.Agent, in RunInput) (*RunResult, error) {
	if agent == nil {
		return nil, errors.New("einoruntime engine: agent is required")
	}
	if len(in.Messages) == 0 {
		return nil, errors.New("einoruntime engine: messages are required")
	}

	cpID, err := EnsureAgentRunCheckpointID(in.WorkspaceID, in.RunID, in.CheckpointID)
	if err != nil {
		return nil, err
	}

	runner := e.newRunner(ctx, agent)
	iter := runner.Run(ctx, in.Messages, adk.WithCheckPointID(cpID))
	return e.consumeIterator(ctx, cpID, in.Projector, iter)
}

// ResumeInput continues an interrupted agent run via ResumeWithParams (design §3.6.3).
//
// Targets maps interrupt IDs → platform tool-result strings (already produced by
// Dispatch InvokeResolved). PipelineTool must return GetResumeContext data and
// must NOT call InvokeResolved again.
type ResumeInput struct {
	// WorkspaceID is required for checkpoint ID validation.
	WorkspaceID string
	// RunID is the agent_run owner segment.
	RunID string
	// CheckpointID is the stable once-per-run ID from the prior pause (required).
	CheckpointID string
	// Targets is adk.ResumeParams.Targets (interruptId → tool result string).
	Targets map[string]any
	// Projector receives item.delta-style text hooks. Optional (NopProjector).
	Projector ProtocolProjector
}

// Resume continues from a checkpoint with explicit Targets (design §3.6.3).
//
// Empty Targets is rejected so v1 never silent-resume-all without tool results.
// A further interrupt is returned the same way as Run (Interrupted=true, nil err).
func (e *Engine) Resume(ctx context.Context, agent adk.Agent, in ResumeInput) (*RunResult, error) {
	if agent == nil {
		return nil, errors.New("einoruntime engine: agent is required")
	}
	cpID := strings.TrimSpace(in.CheckpointID)
	if cpID == "" {
		return nil, errors.New("einoruntime engine: checkpoint ID is required for resume")
	}
	// Validate workspace/run ownership when provided.
	if ws, run := strings.TrimSpace(in.WorkspaceID), strings.TrimSpace(in.RunID); ws != "" && run != "" {
		ensured, err := EnsureAgentRunCheckpointID(ws, run, cpID)
		if err != nil {
			return nil, err
		}
		cpID = ensured
	}
	if len(in.Targets) == 0 {
		return nil, errors.New("einoruntime engine: resume Targets are required (EINO_INTERRUPT_ID_MISSING)")
	}

	runner := e.newRunner(ctx, agent)
	iter, err := runner.ResumeWithParams(ctx, cpID, &adk.ResumeParams{Targets: in.Targets})
	if err != nil {
		return nil, err
	}
	return e.consumeIterator(ctx, cpID, in.Projector, iter)
}

func (e *Engine) newRunner(ctx context.Context, agent adk.Agent) *adk.Runner {
	var store adk.CheckPointStore
	if e != nil {
		store = e.store
	}
	return adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true, // D14 — true Stream path
		CheckPointStore: store,
	})
}

// consumeIterator drains an ADK event iterator into a RunResult + projector hooks.
func (e *Engine) consumeIterator(
	ctx context.Context,
	cpID string,
	projector ProtocolProjector,
	iter *adk.AsyncIterator[*adk.AgentEvent],
) (*RunResult, error) {
	var sink ProtocolProjector = NopProjector{}
	if projector != nil {
		sink = projector
	}
	capturing := &capturingProjector{inner: sink}
	result := &RunResult{CheckpointID: cpID}

	if iter == nil {
		result.FinalAssistantText = capturing.Joined()
		return result, nil
	}

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}

		if event.Err != nil {
			result.Err = mapEngineError(event.Err)
			result.FinalAssistantText = capturing.Joined()
			return result, result.Err
		}

		if event.Action != nil && event.Action.Interrupted != nil {
			result.Interrupted = true
			for _, ic := range event.Action.Interrupted.InterruptContexts {
				if ic == nil || ic.ID == "" {
					continue
				}
				result.InterruptContextIDs = append(result.InterruptContextIDs, ic.ID)
				if ic.IsRootCause {
					result.RootCauseInterruptIDs = append(result.RootCauseInterruptIDs, ic.ID)
				}
			}
			// Drain remaining events; keep Interrupted=true.
			continue
		}

		if err := ProjectAgentEvent(ctx, event, capturing); err != nil {
			result.Err = err
			result.FinalAssistantText = capturing.Joined()
			return result, err
		}
	}

	result.FinalAssistantText = capturing.Joined()
	return result, nil
}

// capturingProjector records deltas for FinalAssistantText and forwards hooks.
// Implements ModelTurnObserver so optional audit hooks on the inner projector
// still receive per-turn content + reasoning.
type capturingProjector struct {
	inner  ProtocolProjector
	deltas []string
}

func (c *capturingProjector) OnTextDelta(ctx context.Context, delta string) error {
	c.deltas = append(c.deltas, delta)
	if c.inner != nil {
		return c.inner.OnTextDelta(ctx, delta)
	}
	return nil
}

func (c *capturingProjector) OnTextComplete(ctx context.Context, full string) error {
	if c.inner != nil {
		return c.inner.OnTextComplete(ctx, full)
	}
	return nil
}

func (c *capturingProjector) OnModelTurn(ctx context.Context, turn ModelTurn) error {
	if c == nil || c.inner == nil {
		return nil
	}
	if observer, ok := c.inner.(ModelTurnObserver); ok {
		return observer.OnModelTurn(ctx, turn)
	}
	return nil
}

func (c *capturingProjector) Joined() string {
	return strings.Join(c.deltas, "")
}

// mapEngineError maps vendor budget errors into the platform ErrToolBudgetExceeded family.
func mapEngineError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrToolBudgetExceeded) {
		return err
	}
	if errors.Is(err, adk.ErrExceedMaxIterations) {
		return fmt.Errorf("%w: %v", ErrToolBudgetExceeded, err)
	}
	return err
}
