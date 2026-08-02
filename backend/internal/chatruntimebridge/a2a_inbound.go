package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"

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
	ctxPolicy, _ := json.Marshal(map[string]any{
		"schemaVersion": sessioncontext.SnapshotSchemaV1,
		"mode":          sessioncontext.ModeLegacy,
		"source":        "a2a.inbound.prepare",
	})
	return a2agateway.InboundFreeze{
		ModelSnapshot:      rootNode.ModelSnapshot,
		CapabilitySnapshot: rootNode.CapabilitySnapshot,
		AgentSnapshot:      rootNode.AgentSnapshot,
		ContextPolicy:      ctxPolicy,
		GraphSnapshot:      rawGraph,
	}, nil
}

// ExecuteA2AInbound drives a durable agent_run created for inbound A2A without a chat session.
// It uses ONLY frozen snapshots from Prepare (fail closed on empty freeze or drift),
// attaches tools, runs the agent, and transitions the run.
// Registers the run in activeRuns so CancelRun can interrupt engine.Run.
// Root MODEL turns go through ModelTurnHook → permanent MODEL_TURN evidence.
func (b *Bridge) ExecuteA2AInbound(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("bridge not configured")
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

	// Fail closed: Prepare must have frozen non-empty model/agent snapshots.
	if len(run.ModelSnapshot) == 0 || string(run.ModelSnapshot) == "{}" || string(run.ModelSnapshot) == "null" {
		return "", fmt.Errorf("a2a inbound: missing frozen model_snapshot (prepare freeze required)")
	}
	if len(run.AgentSnapshot) == 0 || string(run.AgentSnapshot) == "{}" || string(run.AgentSnapshot) == "null" {
		return "", fmt.Errorf("a2a inbound: missing frozen agent_snapshot (prepare freeze required)")
	}

	// Register for cancel propagation (same map CancelRun inspects).
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
		// Session/message optional for direct inbound; assembly falls back to synthetic user turn.
		SessionID:     "",
		UserMessageID: "",
	}

	configuredAgent, err := b.agents.Get(ctx, job.WorkspaceID, run.AgentID)
	if err != nil {
		return "", fmt.Errorf("load agent: %w", err)
	}
	if configuredAgent.Status != agent.StatusActive {
		return "", fmt.Errorf("agent is not active")
	}

	// Resolve model + prompt strictly from freeze (kill-switch only via live DISABLED).
	frozenModel, modelID, err := parseModelSnapshot(run.ModelSnapshot)
	if err != nil {
		return "", fmt.Errorf("parse frozen model_snapshot: %w", err)
	}
	if modelID == "" {
		return "", fmt.Errorf("frozen model_snapshot missing id")
	}
	liveCfg, err := b.models.Get(ctx, job.WorkspaceID, modelID)
	if err != nil {
		return "", fmt.Errorf("load model config for kill-switch: %w", err)
	}
	// Live kill-switch only: DISABLED blocks; ordinary lock_version/name/options
	// edits must not change or block a frozen run.
	if liveCfg.Status == modelconfig.StatusDisabled {
		return "", fmt.Errorf("model config is disabled")
	}
	// Build chat model from freeze fields (not live name/options).
	cfg := frozenModel
	cfg.WorkspaceID = liveCfg.WorkspaceID
	if cfg.Status == "" {
		cfg.Status = modelconfig.StatusVerified
	}
	// Credential secret id may only exist live for secret resolution.
	if cfg.CredentialSecretID == nil {
		cfg.CredentialSecretID = liveCfg.CredentialSecretID
	}
	if b.buildModel == nil {
		return "", fmt.Errorf("chat model builder is not configured")
	}
	chatModel, err := b.buildModel(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("build chat model from freeze: %w", err)
	}

	// Prompt from frozen agent snapshot revision (not live CurrentPromptRevisionID).
	instruction, err := b.instructionFromAgentSnapshot(ctx, job.WorkspaceID, run.AgentID, run.AgentSnapshot)
	if err != nil {
		return "", err
	}

	pendingKey := pendingConfirmKey(job.WorkspaceID, job.RunID)
	b.clearPending(pendingKey)

	// Capability tools from frozen capability snapshot on the run (not live re-bind).
	tools, err := b.buildPipelineTools(ctx, job, run, pendingKey)
	if err != nil {
		return "", err
	}
	var delBudget *agentdelegation.Budget
	var graphSnap *agentdelegation.GraphSnapshotV1
	tools, delBudget, graphSnap, err = b.attachDelegationTools(ctx, job, run, tools, pendingKey)
	if err != nil {
		return "", fmt.Errorf("attach agent delegation tools: %w", err)
	}
	// Graph must already be frozen at Prepare; re-freeze only if empty (legacy rows).
	if graphSnap != nil {
		raw, rawErr := graphSnapshotBytes(graphSnap)
		if rawErr != nil {
			return "", fmt.Errorf("marshal agent graph snapshot: %w", rawErr)
		}
		if setter, ok := b.runs.(interface {
			SetAgentGraphSnapshotIfEmpty(context.Context, string, string, json.RawMessage) error
		}); ok {
			if err := setter.SetAgentGraphSnapshotIfEmpty(ctx, job.WorkspaceID, job.RunID, raw); err != nil {
				return "", fmt.Errorf("persist agent graph snapshot: %w", err)
			}
			run.AgentGraphSnapshot = raw
			if current, gerr := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID); gerr == nil {
				run.LockVersion = current.LockVersion
				if len(current.AgentGraphSnapshot) > 0 {
					run.AgentGraphSnapshot = current.AgentGraphSnapshot
				}
			}
		} else if len(run.AgentGraphSnapshot) == 0 || string(run.AgentGraphSnapshot) == "{}" {
			return "", fmt.Errorf("persist agent graph snapshot: SetAgentGraphSnapshotIfEmpty not supported")
		}
	}
	ctx = withDelegationRunContext(ctx, job, run, delBudget)

	agentName := "agent-" + strings.TrimSpace(run.AgentID)
	built, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name: agentName, Instruction: instruction, Model: chatModel, Tools: tools,
		MaxIterations: b.maxIterations, MaxToolInvocations: b.maxTools,
	})
	if err != nil {
		return "", fmt.Errorf("build chat model agent: %w", err)
	}

	// Attribute model/tool steps to this agent for audit (root MODEL via projector).
	ctx = einoruntime.WithTrustedWorkspaceID(ctx, job.WorkspaceID)
	if b.engine == nil {
		return "", fmt.Errorf("eino engine not configured")
	}
	// Fail closed: root inbound MODEL must leave permanent audit evidence.
	if b.modelTurns == nil || b.steps == nil {
		return "", fmt.Errorf("model turn recorder and step store required for a2a inbound audit")
	}
	projector := &StreamDeltaRecorder{
		Now: b.now,
		ModelTurnHook: func(hookCtx context.Context, turn einoruntime.ModelTurn) error {
			return b.recordModelTurn(hookCtx, job, run, turn)
		},
	}
	result, err := b.engine.Run(ctx, built, einoruntime.RunInput{
		WorkspaceID: job.WorkspaceID,
		RunID:       job.RunID,
		Messages:    []*schema.Message{schema.UserMessage(req.UserText)},
		Projector:   projector,
	})
	if err != nil {
		_ = b.failRunA2A(ctx, job, run, err)
		return "", err
	}
	if result == nil {
		err = fmt.Errorf("nil eino result")
		_ = b.failRunA2A(ctx, job, run, err)
		return "", err
	}
	if result.Err != nil {
		_ = b.failRunA2A(ctx, job, run, result.Err)
		return "", result.Err
	}
	text := strings.TrimSpace(result.FinalAssistantText)
	if err := b.completeRunA2A(ctx, job, run, text); err != nil {
		return text, err
	}
	return text, nil
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
