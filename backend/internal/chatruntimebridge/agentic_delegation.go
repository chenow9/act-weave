package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
)

// attachAgenticDelegationTools builds Typed child agents from the frozen graph
// and injects AuditedAgentTool + A2A outbound tools into the root tool list.
//
// Unlike the classic seam, this never ListEnabledEdges / live-rebuilds topology:
// the freeze on the run is the sole authority (Task 5 / §7.4). Children use
// NewTypedAgentTool — never SetSubAgents — so adk's transfer system message is
// not injected (design §7.4.1).
func (b *Bridge) attachAgenticDelegationTools(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	rootTools []tool.BaseTool,
	pendingKey string,
	snap *agentdelegation.GraphSnapshotV1,
) ([]tool.BaseTool, *agentdelegation.Budget, error) {
	if snap == nil {
		return rootTools, nil, nil
	}
	edges := snap.Edges
	hasRemote := false
	for _, list := range snap.FrozenRemotesByCaller {
		if len(list) > 0 {
			hasRemote = true
			break
		}
	}
	if len(edges) == 0 && !hasRemote {
		return rootTools, nil, nil
	}
	if b == nil || b.delegation == nil || b.delegation.Audit == nil {
		return nil, nil, fmt.Errorf("agentic delegation: Audit required when frozen graph has edges or remotes")
	}
	if b.buildAgenticModel == nil {
		return nil, nil, fmt.Errorf("agentic delegation: agentic model builder is not configured")
	}

	budget := agentdelegation.NewBudget()
	if snap.MaxDepth > 0 {
		budget.MaxDepth = snap.MaxDepth
	}
	if snap.MaxTotal > 0 {
		budget.MaxTotal = snap.MaxTotal
	}
	if snap.MaxPerBinding > 0 {
		budget.MaxPerBinding = snap.MaxPerBinding
	}

	nodeByID := map[string]agentdelegation.GraphNodeSnapshot{}
	for _, n := range snap.Nodes {
		nodeByID[n.AgentID] = n
	}

	snapRaw, err := graphSnapshotBytes(snap)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal frozen graph: %w", err)
	}

	childAgents := map[string]adk.TypedAgent[*schema.AgenticMessage]{}
	var buildChild func(agentID string, depth int, stack map[string]bool) (adk.TypedAgent[*schema.AgenticMessage], error)
	buildChild = func(agentID string, depth int, stack map[string]bool) (adk.TypedAgent[*schema.AgenticMessage], error) {
		if existing, ok := childAgents[agentID]; ok {
			return existing, nil
		}
		if stack[agentID] {
			return nil, agentdelegation.ErrCycle
		}
		stack[agentID] = true
		defer delete(stack, agentID)

		parts, buildCfg, err := b.loadChildAgentPartsAgentic(ctx, job, run, agentID, pendingKey, nodeByID[agentID])
		if err != nil {
			return nil, err
		}
		tools := append([]tool.BaseTool{}, parts.Tools...)
		if depth < budget.MaxDepth {
			for _, edge := range edges {
				if edge.CallerAgentID != agentID || (edge.Protocol != "" && edge.Protocol != agentdelegation.ProtocolInternal) {
					continue
				}
				child, err := buildChild(edge.TargetAgentID, depth+1, stack)
				if err != nil {
					return nil, err
				}
				audited, err := wrapTypedAgentTool(ctx, child, edge, b, agentID, nodeByID[edge.TargetAgentID], snap)
				if err != nil {
					return nil, err
				}
				tools = append(tools, audited)
			}
		}
		remotes := frozenRemotesForAgent(job.WorkspaceID, snapRaw, agentID)
		if remotes == nil {
			if hasFrozenRemotesKey(job.WorkspaceID, snapRaw) {
				return nil, fmt.Errorf("frozen remotes required for agent %s (no live fallback)", agentID)
			}
			remotes = []a2agateway.RemoteBinding{}
		}
		for _, remote := range remotes {
			ot, oerr := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
				Binding: remote, Audit: b.delegation.Audit,
				AuthHeaderResolver:    b.delegation.AuthHeaderResolver,
				CallerAgentID:         agentID,
				EnqueueFinalizeOutbox: b.delegation.EnqueueFinalizeOutbox,
			})
			if oerr != nil {
				return nil, fmt.Errorf("a2a remote %s: %w", remote.CallableName, oerr)
			}
			tools = append(tools, ot)
		}

		child, err := b.buildAgenticChildAgent(ctx, parts, buildCfg, tools)
		if err != nil {
			return nil, err
		}
		childAgents[agentID] = child
		return child, nil
	}

	out := append([]tool.BaseTool{}, rootTools...)
	names := map[string]string{}
	for _, t := range rootTools {
		info, ierr := t.Info(ctx)
		if ierr != nil {
			return nil, nil, fmt.Errorf("root tool Info: %w", ierr)
		}
		if info != nil && info.Name != "" {
			names[info.Name] = "capability"
		}
	}
	if err := assertReachableCallableNamespaces(ctx, b, job.WorkspaceID, run.AgentID, edges, names, snapRaw, snap); err != nil {
		return nil, nil, err
	}
	for _, edge := range edges {
		if edge.CallerAgentID != run.AgentID {
			continue
		}
		if edge.Protocol != "" && edge.Protocol != agentdelegation.ProtocolInternal {
			continue
		}
		child, err := buildChild(edge.TargetAgentID, 1, map[string]bool{run.AgentID: true})
		if err != nil {
			return nil, nil, fmt.Errorf("build child agent %s: %w", edge.TargetAgentID, err)
		}
		audited, err := wrapTypedAgentTool(ctx, child, edge, b, run.AgentID, nodeByID[edge.TargetAgentID], snap)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, audited)
	}
	remotes := frozenRemotesForAgent(job.WorkspaceID, snapRaw, run.AgentID)
	if remotes == nil {
		if hasFrozenRemotesKey(job.WorkspaceID, snapRaw) {
			return nil, nil, fmt.Errorf("frozen remotes required for root agent %s (no live fallback)", run.AgentID)
		}
		remotes = []a2agateway.RemoteBinding{}
	}
	for _, remote := range remotes {
		ot, oerr := a2agateway.NewOutboundTool(a2agateway.OutboundConfig{
			Binding: remote, Audit: b.delegation.Audit,
			AuthHeaderResolver:    b.delegation.AuthHeaderResolver,
			CallerAgentID:         run.AgentID,
			EnqueueFinalizeOutbox: b.delegation.EnqueueFinalizeOutbox,
		})
		if oerr != nil {
			return nil, nil, fmt.Errorf("a2a remote %s: %w", remote.CallableName, oerr)
		}
		out = append(out, ot)
	}
	return out, budget, nil
}

func wrapTypedAgentTool(
	ctx context.Context,
	child adk.TypedAgent[*schema.AgenticMessage],
	edge agentdelegation.GraphEdgeSnapshot,
	b *Bridge,
	callerAgentID string,
	targetNode agentdelegation.GraphNodeSnapshot,
	snap *agentdelegation.GraphSnapshotV1,
) (*agentdelegation.AuditedAgentTool, error) {
	agentTool := adk.NewTypedAgentTool(ctx, child)
	inv, ok := agentTool.(tool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("typed agent tool not invokable: %s", edge.CallableName)
	}
	return agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
		Inner: inv, Name: edge.CallableName, Description: edge.Description,
		Edge: edge, Audit: b.delegation.Audit, DefaultCallerAgentID: callerAgentID,
		Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
		ChildRuns:             b.delegation.ChildRuns,
		TargetSnapshots:       targetSnapshotsFromNode(targetNode, snap),
		EnqueueFinalizeOutbox: b.delegation.EnqueueFinalizeOutbox,
	})
}

// loadChildAgentPartsAgentic materializes one frozen child for the Agentic path.
// Live agent/model reads are kill-switch + credential material only.
func (b *Bridge) loadChildAgentPartsAgentic(
	ctx context.Context,
	job agentrun.Job,
	parentRun execution.AgentRun,
	agentID string,
	pendingKey string,
	node agentdelegation.GraphNodeSnapshot,
) (agentdelegation.AgenticAgentParts, modelconfig.Config, error) {
	configured, err := b.agents.Get(ctx, job.WorkspaceID, agentID)
	if err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, err
	}
	if configured.Status != agent.StatusActive {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, agentdelegation.ErrAgentUnavailable
	}
	if node.AgentID == "" || strings.TrimSpace(node.ModelConfigID) == "" ||
		len(node.ModelSnapshot) == 0 || string(node.ModelSnapshot) == "{}" ||
		len(node.AgentSnapshot) == 0 || string(node.AgentSnapshot) == "{}" ||
		len(node.CapabilitySnapshot) == 0 {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf(
			"frozen node incomplete for agent %s (require model/agent/capability snapshots; no live fallback)", agentID)
	}
	buildCfg, err := requireFrozenModelConfig(node.ModelSnapshot, job.WorkspaceID)
	if err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf("child frozen model: %w", err)
	}
	if err := requireVerifiedAgenticSnapshot(node.ModelSnapshot, buildCfg); err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf("child agentic capability: %w", err)
	}
	modelID := firstNonEmpty(buildCfg.ID, node.ModelConfigID, configured.ModelConfigID)
	liveCfg, err := b.models.Get(ctx, job.WorkspaceID, modelID)
	if err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, err
	}
	if liveCfg.Status == modelconfig.StatusDisabled {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, agentdelegation.ErrAgentUnavailable
	}
	buildCfg.WorkspaceID = liveCfg.WorkspaceID
	if buildCfg.CredentialSecretID == nil {
		buildCfg.CredentialSecretID = liveCfg.CredentialSecretID
	}
	if buildCfg.Status == "" {
		buildCfg.Status = modelconfig.StatusVerified
	}

	const defaultChildPrompt = "You are a helpful workspace agent. Answer clearly and concisely."
	instruction := defaultChildPrompt
	promptHash := ""
	if node.PromptRevisionID != "" {
		revs, rerr := b.agents.ListPromptRevisions(ctx, job.WorkspaceID, agentID)
		if rerr != nil {
			return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf("list prompt revisions for frozen node: %w", rerr)
		}
		found := false
		for _, rev := range revs {
			if rev.ID != node.PromptRevisionID {
				continue
			}
			found = true
			if strings.TrimSpace(rev.SystemPrompt) == "" {
				return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf("frozen prompt revision %s empty", node.PromptRevisionID)
			}
			liveHash := strings.TrimSpace(rev.ContentSHA256)
			if liveHash == "" {
				liveHash = execution.HashJSONObject(json.RawMessage(strconv.Quote(rev.SystemPrompt)))
			}
			if node.PromptRevisionHash != "" && !strings.EqualFold(liveHash, node.PromptRevisionHash) {
				return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf(
					"%w: prompt hash drift agent=%s rev=%s",
					agentdelegation.ErrAgentUnavailable, agentID, node.PromptRevisionID)
			}
			instruction = strings.TrimSpace(rev.SystemPrompt)
			promptHash = liveHash
			break
		}
		if !found {
			return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf(
				"frozen prompt revision %s not found for agent %s", node.PromptRevisionID, agentID)
		}
	}

	childRun := parentRun
	childRun.AgentID = agentID
	childRun.CapabilitySnapshot = node.CapabilitySnapshot
	frozenCaps, err := parseRunCapabilitySnapshotStrict(node.CapabilitySnapshot)
	if err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf("child capability snapshot: %w", err)
	}
	tools, err := b.buildPipelineToolsFrom(ctx, job, childRun, pendingKey, frozenCaps)
	if err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, fmt.Errorf("build child tools for %s: %w", agentID, err)
	}

	agenticModel, err := b.buildAgenticModel(ctx, buildCfg)
	if err != nil {
		return agentdelegation.AgenticAgentParts{}, modelconfig.Config{}, err
	}
	agenticModel = wrapNestedAuditAgenticModel(agenticModel, b)

	name := strings.TrimSpace(node.Name)
	desc := ""
	if len(node.AgentSnapshot) > 0 {
		var as map[string]any
		if json.Unmarshal(node.AgentSnapshot, &as) == nil {
			if n, ok := as["name"].(string); ok && strings.TrimSpace(n) != "" {
				name = strings.TrimSpace(n)
			}
			if d, ok := as["roleDescription"].(string); ok {
				desc = strings.TrimSpace(d)
			}
		}
	}
	if name == "" {
		name = "agent-" + agentID
	}
	if desc == "" {
		desc = "workspace agent"
	}
	return agentdelegation.AgenticAgentParts{
		AgentID: agentID, Name: name, Description: desc,
		Instruction: instruction, Model: agenticModel, Tools: tools,
		PromptRevisionID: node.PromptRevisionID, PromptRevisionHash: promptHash,
		ModelConfigID:      firstNonEmpty(buildCfg.ID, liveCfg.ID),
		ModelConfigLockVer: firstNonEmptyInt64(node.ModelConfigLockVer, liveCfg.LockVersion),
		CapabilitySnapshot: childRun.CapabilitySnapshot,
	}, buildCfg, nil
}

func (b *Bridge) buildAgenticChildAgent(
	ctx context.Context,
	parts agentdelegation.AgenticAgentParts,
	cfg modelconfig.Config,
	tools []tool.BaseTool,
) (adk.TypedAgent[*schema.AgenticMessage], error) {
	caps, err := parseRunCapabilitySnapshotStrict(parts.CapabilitySnapshot)
	if err != nil {
		return nil, err
	}
	catalog, err := buildFrozenToolCatalogStrict(ctx, tools, caps)
	if err != nil {
		return nil, fmt.Errorf("child catalog: %w", err)
	}
	promptCacheKey, err := buildRunPromptCacheKey(cfg, parts.Instruction, catalog)
	if err != nil {
		return nil, err
	}
	hasToolsOrCatalog := len(tools) > 0 || (catalog != nil && catalog.Len() > 0)
	built, err := einoruntime.BuildAgenticAgent(ctx, einoruntime.AgenticAgentBuildConfig{
		Name:                     parts.Name,
		Description:              parts.Description,
		Instruction:              parts.Instruction,
		Model:                    parts.Model,
		Tools:                    tools,
		Catalog:                  catalog,
		MaxIterations:            einoruntime.DefaultMaxIterations,
		MaxToolInvocations:       b.maxTools,
		ToolSearchMode:           einoruntime.ToolSearchModeClientBounded,
		ClientToolSearchVerified: hasToolsOrCatalog,
		PromptCacheKey:           promptCacheKey,
	})
	if err != nil {
		return nil, fmt.Errorf("build agentic child %s: %w", parts.AgentID, err)
	}
	return built, nil
}

// catalogKindForTool classifies AgentTool / A2A outbound executables that are
// not present in the capability snapshot (graph-edge / remote sourced).
func catalogKindForTool(t tool.BaseTool) string {
	switch t.(type) {
	case *agentdelegation.AuditedAgentTool:
		return einoruntime.ToolKindAgent
	case *a2agateway.OutboundTool:
		return einoruntime.ToolKindA2A
	default:
		return ""
	}
}
