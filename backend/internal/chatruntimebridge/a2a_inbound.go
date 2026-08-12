package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// FreezeInbound implements a2agateway.SnapshotFreezer: freezes target agent
// model/capability/agent/prompt and the full reachable internal topology at Prepare.
func (b *Bridge) FreezeInbound(ctx context.Context, workspaceID, agentID string) (a2agateway.InboundFreeze, error) {
	if b == nil || b.agents == nil || b.models == nil {
		return a2agateway.InboundFreeze{}, fmt.Errorf("bridge freeze: agents/models required")
	}
	workspaceID, agentID = strings.TrimSpace(workspaceID), strings.TrimSpace(agentID)
	if workspaceID == "" || agentID == "" {
		return a2agateway.InboundFreeze{}, fmt.Errorf("bridge freeze: workspace and agent required")
	}
	// Full reachable internal edges so external A2A → A can still call A→B tools.
	var edges []agentdelegation.GraphEdgeSnapshot
	if b.delegation != nil && b.delegation.Bindings != nil {
		live, err := b.delegation.Bindings.ListEnabledEdges(ctx, workspaceID)
		if err != nil {
			return a2agateway.InboundFreeze{}, fmt.Errorf("list edges for inbound freeze: %w", err)
		}
		if err := agentdelegation.DetectCycle(live); err != nil {
			return a2agateway.InboundFreeze{}, err
		}
		edges = agentdelegation.EdgesFromRoot(agentID, live, agentdelegation.DefaultMaxDepth)
	}
	job := agentrun.Job{WorkspaceID: workspaceID}
	run := execution.AgentRun{AgentID: agentID, WorkspaceID: workspaceID}
	graph, err := b.freezeGraphSnapshot(ctx, job, run, edges)
	if err != nil {
		return a2agateway.InboundFreeze{}, err
	}
	// Root node snapshots for agent_run model/agent/capability columns.
	var rootNode agentdelegation.GraphNodeSnapshot
	for _, n := range graph.Nodes {
		if n.AgentID == agentID {
			rootNode = n
			break
		}
	}
	if rootNode.AgentID == "" {
		rootNode, err = b.snapshotAgentNode(ctx, workspaceID, agentID, 0)
		if err != nil {
			return a2agateway.InboundFreeze{}, err
		}
	}
	// freezeGraphSnapshot already freezes remotes for every reachable caller
	// (FrozenRemotesByCaller + RemotesFrozen=true). No top-level map hacks.
	rawGraph, gerr := graphSnapshotBytes(graph)
	if gerr != nil {
		return a2agateway.InboundFreeze{}, gerr
	}
	// Agentic inbound needs a non-legacy context policy (same hard preflight as chat).
	frozenModel, _, ferr := parseModelSnapshot(rootNode.ModelSnapshot)
	if ferr != nil {
		return a2agateway.InboundFreeze{}, fmt.Errorf("inbound freeze model snapshot: %w", ferr)
	}
	liveCfg, lerr := b.models.Get(ctx, workspaceID, firstNonEmpty(frozenModel.ID, rootNode.ModelConfigID))
	if lerr != nil {
		return a2agateway.InboundFreeze{}, fmt.Errorf("inbound freeze model load: %w", lerr)
	}
	window, reserve := int64(128000), int64(4096)
	profile, tokVer := modelconfig.TokenizerProfileO200kBase, "2026-01"
	limitMode := modelconfig.OutputTokenLimitModeMaxTokens
	if caps, _, cerr := modelconfig.ParseRuntimeCapabilities(liveCfg.RuntimeCapabilities); cerr == nil {
		if caps.ContextWindowTokens > 0 {
			window = caps.ContextWindowTokens
		}
		if caps.DefaultOutputReserveTokens > 0 {
			reserve = caps.DefaultOutputReserveTokens
		}
		if strings.TrimSpace(caps.TokenizerProfile) != "" {
			profile = strings.TrimSpace(caps.TokenizerProfile)
		}
		if strings.TrimSpace(caps.TokenizerVersion) != "" {
			tokVer = strings.TrimSpace(caps.TokenizerVersion)
		}
		if strings.TrimSpace(caps.OutputTokenLimitMode) != "" {
			limitMode = strings.TrimSpace(caps.OutputTokenLimitMode)
		}
	}
	_, ctxPolicy, rerr := sessioncontext.Resolve(sessioncontext.ResolveInput{
		ContextWindowTokens:        window,
		DefaultOutputReserveTokens: reserve,
		OutputTokenLimitMode:       limitMode,
		TokenizerProfile:           profile,
		TokenizerVersion:           tokVer,
		GateEnabled:                true,
		RolloutVersion:             "a2a.inbound.prepare",
	})
	if rerr != nil {
		return a2agateway.InboundFreeze{}, fmt.Errorf("inbound freeze context policy: %w", rerr)
	}
	return a2agateway.InboundFreeze{
		ModelSnapshot:      rootNode.ModelSnapshot,
		CapabilitySnapshot: rootNode.CapabilitySnapshot,
		AgentSnapshot:      rootNode.AgentSnapshot,
		ContextPolicy:      ctxPolicy,
		GraphSnapshot:      rawGraph,
	}, nil
}

// ExecuteA2AInbound drives a durable agent_run created for inbound A2A without a
// chat session, on the Agentic frozen path (Task 5): planAgenticRun → Typed agent
// → runAgenticTurn. Public A2A shape is unchanged.
func (b *Bridge) ExecuteA2AInbound(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("bridge not configured")
	}
	if b.buildAgenticModel == nil || b.agenticEngine == nil {
		return "", fmt.Errorf("a2a inbound: agentic runtime is not configured")
	}
	run, err := b.runs.GetAgentRun(ctx, req.WorkspaceID, runID)
	if err != nil {
		return "", err
	}
	if run.Status != "RUNNING" && run.Status != "PENDING" {
		return "", fmt.Errorf("a2a inbound run %s not executable (status=%s)", runID, run.Status)
	}
	if run.AgentID != req.AgentID {
		return "", fmt.Errorf("a2a inbound agent mismatch")
	}
	if len(run.ModelSnapshot) == 0 || string(run.ModelSnapshot) == "{}" || string(run.ModelSnapshot) == "null" {
		return "", fmt.Errorf("a2a inbound: missing frozen model_snapshot (prepare freeze required)")
	}
	if len(run.AgentSnapshot) == 0 || string(run.AgentSnapshot) == "{}" || string(run.AgentSnapshot) == "null" {
		return "", fmt.Errorf("a2a inbound: missing frozen agent_snapshot (prepare freeze required)")
	}
	if b.modelTurns == nil || b.steps == nil {
		return "", fmt.Errorf("model turn recorder and step store required for a2a inbound audit")
	}

	runCtx, runCancel := context.WithCancelCause(ctx)
	active, registered := b.registerActiveRun(req.WorkspaceID, runID, "initial", runCancel)
	if !registered {
		runCancel(fmt.Errorf("a2a inbound run already active"))
		return "", fmt.Errorf("a2a inbound run %s already active", runID)
	}
	defer b.unregisterActiveRun(req.WorkspaceID, runID, "initial", active)
	ctx = runCtx

	job := agentrun.Job{
		WorkspaceID: req.WorkspaceID,
		RunID:       runID,
		ActorID:     firstNonEmpty(req.ActorID, "a2a"),
	}
	userText := strings.TrimSpace(req.UserText)
	if userText == "" {
		return "", fmt.Errorf("a2a inbound: empty user text")
	}

	plan, err := b.planAgenticRun(ctx, job, run)
	if err != nil {
		return "", err
	}
	if err := preflightAgenticInboundTurn(plan, userText); err != nil {
		return "", err
	}
	messages := []*schema.AgenticMessage{
		agenticmsg.System(plan.instruction),
		agenticmsg.UserText(userText),
	}
	if err := agenticmsg.ValidateConversation(messages); err != nil {
		return "", execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	built, err := b.buildAgenticAgentFromPlan(ctx, run, plan)
	if err != nil {
		return "", err
	}
	ctx = withDelegationRunContext(ctx, job, run, plan.delBudget)

	text, _, runErr := b.runAgenticTurn(ctx, job, run, built, func(
		turnCtx context.Context,
		agent adk.TypedAgent[*schema.AgenticMessage],
		projector einoruntime.ProtocolProjector,
	) (*einoruntime.AgenticRunResult, error) {
		return b.agenticEngine.Run(turnCtx, agent, einoruntime.AgenticRunInput{
			WorkspaceID: job.WorkspaceID,
			RunID:       job.RunID,
			Messages:    messages,
			Projector:   projector,
		})
	})
	if runErr != nil {
		_ = b.failRunA2A(ctx, job, run, runErr)
		return "", runErr
	}
	if err := b.completeRunA2A(ctx, job, run, text); err != nil {
		return text, err
	}
	return text, nil
}

// preflightAgenticInboundTurn bounds the synthetic system+user inbound request
// without requiring an assembly store (inbound often has none).
func preflightAgenticInboundTurn(plan *agenticFrozenPlan, userText string) error {
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
	exposure := toolExposureFromCatalog(plan.catalog)
	got, err := est.EstimateAgenticRequest(plan.instruction, exposure, []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: userText},
	})
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

// instructionFromAgentSnapshot loads the frozen prompt revision and verifies hash.
func (b *Bridge) instructionFromAgentSnapshot(
	ctx context.Context, workspaceID, agentID string, agentSnap json.RawMessage,
) (string, error) {
	const defaultSystemPrompt = "You are a helpful workspace agent. Answer clearly and concisely."
	revID := agentSnapshotPromptRevisionID(agentSnap)
	wantHash := agentSnapshotPromptRevisionHash(agentSnap)
	if revID == "" {
		// Freeze without prompt revision: use default (explicit, not live current).
		return defaultSystemPrompt, nil
	}
	revs, err := b.agents.ListPromptRevisions(ctx, workspaceID, agentID)
	if err != nil {
		return "", fmt.Errorf("list frozen prompt revisions: %w", err)
	}
	for _, rev := range revs {
		if rev.ID != revID {
			continue
		}
		if strings.TrimSpace(rev.SystemPrompt) == "" {
			return "", fmt.Errorf("frozen prompt revision %s empty", revID)
		}
		liveHash := strings.TrimSpace(rev.ContentSHA256)
		if liveHash == "" {
			liveHash = execution.HashJSONObject(json.RawMessage(strconvQuote(rev.SystemPrompt)))
		}
		if wantHash != "" && !strings.EqualFold(liveHash, wantHash) {
			return "", fmt.Errorf("a2a inbound prompt hash drift rev=%s", revID)
		}
		return strings.TrimSpace(rev.SystemPrompt), nil
	}
	return "", fmt.Errorf("frozen prompt revision %s not found for agent %s", revID, agentID)
}

func agentSnapshotPromptRevisionHash(raw json.RawMessage) string {
	var doc struct {
		PromptRevisionHash string `json:"promptRevisionHash"`
	}
	_ = json.Unmarshal(raw, &doc)
	return strings.TrimSpace(doc.PromptRevisionHash)
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (b *Bridge) failRunA2A(ctx context.Context, job agentrun.Job, run execution.AgentRun, runErr error) error {
	// Under inbound lease fence the gateway FencedInboundTerminal owns all terminals
	// (run+task+delegation) in one atomic TX — do not race a separate run transition.
	if _, ok := a2agateway.ExecutionFenceFrom(ctx); ok {
		return runErr
	}
	if b.steps == nil {
		return runErr
	}
	current, err := b.runs.GetAgentRun(context.WithoutCancel(ctx), job.WorkspaceID, job.RunID)
	if err != nil {
		return err
	}
	if current.Status != "RUNNING" && current.Status != "PENDING" {
		return runErr
	}
	out, _ := json.Marshal(map[string]any{
		"source": "a2a.inbound", "error": truncateStr(runErr.Error(), 500),
	})
	_, err = b.steps.TransitionAgentRun(context.WithoutCancel(ctx), job.WorkspaceID, job.RunID, execution.RunTransition{
		ExpectedStatus: current.Status, ExpectedLockVersion: current.LockVersion,
		NewStatus: "FAILED", OutputSummary: out, ErrorCode: "A2A_INBOUND_EXECUTE_FAILED",
	})
	if err != nil {
		return err
	}
	return runErr
}

func (b *Bridge) completeRunA2A(ctx context.Context, job agentrun.Job, run execution.AgentRun, text string) error {
	// Under inbound lease fence: leave agent_run RUNNING; gateway applies
	// FencedInboundTerminal (atomic run+task+delegation+step).
	if fence, ok := a2agateway.ExecutionFenceFrom(ctx); ok {
		if fence.Repo != nil {
			// Optional early atomic probe: if lease already lost, fail closed now.
			if err := fence.Repo.AssertInboundExecutionHeld(ctx, fence.WorkspaceID, fence.TaskID, fence.Owner, fence.Token, fence.Generation); err != nil {
				return fmt.Errorf("a2a inbound lease lost before complete: %w", err)
			}
		}
		return nil
	}
	if b.steps == nil {
		return fmt.Errorf("step store required to complete a2a inbound run")
	}
	current, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return err
	}
	if current.Status != "RUNNING" && current.Status != "PENDING" {
		return nil
	}
	out, _ := json.Marshal(map[string]any{
		"source": "a2a.inbound", "assistantPreview": truncateStr(text, 500),
	})
	_, err = b.steps.TransitionAgentRun(context.WithoutCancel(ctx), job.WorkspaceID, job.RunID, execution.RunTransition{
		ExpectedStatus: current.Status, ExpectedLockVersion: current.LockVersion,
		NewStatus: "SUCCEEDED", OutputSummary: out,
	})
	return err
}

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
