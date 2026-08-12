package chatruntimebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// CapabilityCatalog resolves agent TOOL/WORKFLOW descriptors for graph freeze.
type CapabilityCatalog interface {
	ListForAgent(ctx context.Context, workspaceID, agentID string) ([]capability.Descriptor, error)
}

// BindingLister loads enabled internal edges.
type BindingLister interface {
	ListEnabledEdges(ctx context.Context, workspaceID string) ([]agentdelegation.GraphEdgeSnapshot, error)
}

// DelegationDeps wires internal Agent→Agent + A2A remotes into the bridge.
type DelegationDeps struct {
	Bindings BindingLister
	Audit    agentdelegation.AuditWriter
	// Catalog freezes target agent capabilities into graph snapshot (required for tools).
	Catalog CapabilityCatalog
	// ChildRuns implements TASK-mode independent agent_run lifecycle.
	ChildRuns agentdelegation.ChildRunStore
	// A2A remotes optional.
	RemoteBindings a2agateway.RemoteLister
	// AuthHeaderResolver for outbound A2A secret refs.
	AuthHeaderResolver func(ctx context.Context, secretRef string) (string, error)
	// EnqueueFinalizeOutbox durable recovery for internal/outbound finalize failures.
	EnqueueFinalizeOutbox func(ctx context.Context, workspaceID, delegationID, stepID string, payload json.RawMessage) error
}

// attachDelegationTools freezes graph snapshot (fail-closed), builds child agents with
// their own capabilities, and injects AuditedAgentTools.
func (b *Bridge) attachDelegationTools(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	rootTools []tool.BaseTool,
	pendingKey string,
) ([]tool.BaseTool, *agentdelegation.Budget, *agentdelegation.GraphSnapshotV1, error) {
	if b == nil || b.delegation == nil || b.delegation.Audit == nil || b.delegation.Bindings == nil {
		return rootTools, nil, nil, nil
	}

	var snap *agentdelegation.GraphSnapshotV1
	var edges []agentdelegation.GraphEdgeSnapshot
	rawSnap := run.AgentGraphSnapshot
	emptySnap := len(rawSnap) == 0 || string(rawSnap) == "{}" || string(rawSnap) == "null"
	if !emptySnap {
		parsed, perr := agentdelegation.ParseSnapshot(job.WorkspaceID, rawSnap)
		if perr != nil || parsed == nil {
			// Non-empty but malformed/unsupported: never live-rebuild.
			return nil, nil, nil, fmt.Errorf("agent_graph_snapshot parse failed (fail-closed): %v", perr)
		}
		snap = parsed
		edges = parsed.Edges
	} else {
		live, err := b.delegation.Bindings.ListEnabledEdges(ctx, job.WorkspaceID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("list delegation bindings: %w", err)
		}
		if err := agentdelegation.DetectCycle(live); err != nil {
			return nil, nil, nil, err
		}
		edges = agentdelegation.EdgesFromRoot(run.AgentID, live, agentdelegation.DefaultMaxDepth)
		// Fail-closed full freeze of topology + node capabilities before dispatch.
		built, berr := b.freezeGraphSnapshot(ctx, job, run, edges)
		if berr != nil {
			return nil, nil, nil, fmt.Errorf("freeze agent graph snapshot: %w", berr)
		}
		snap = built
		// Immediately use the just-built snap for remotes/namespace (not empty run field).
		if raw, rerr := graphSnapshotBytes(snap); rerr == nil {
			run.AgentGraphSnapshot = raw
		}
	}

	if len(edges) == 0 && snap != nil && snap.RemotesFrozen {
		// Authoritative empty remotes for root: still may have no tools.
		hasAnyRemote := false
		for _, list := range snap.FrozenRemotesByCaller {
			if len(list) > 0 {
				hasAnyRemote = true
				break
			}
		}
		if !hasAnyRemote {
			return rootTools, nil, snap, nil
		}
	}
	if len(edges) == 0 && (b.delegation.RemoteBindings == nil) && (snap == nil || !snap.RemotesFrozen) {
		return rootTools, nil, snap, nil
	}

	budget := agentdelegation.NewBudget()
	if snap != nil {
		if snap.MaxDepth > 0 {
			budget.MaxDepth = snap.MaxDepth
		}
		if snap.MaxTotal > 0 {
			budget.MaxTotal = snap.MaxTotal
		}
		if snap.MaxPerBinding > 0 {
			budget.MaxPerBinding = snap.MaxPerBinding
		}
	}

	nodeByID := map[string]agentdelegation.GraphNodeSnapshot{}
	if snap != nil {
		for _, n := range snap.Nodes {
			nodeByID[n.AgentID] = n
		}
	}

	childAgents := map[string]adk.Agent{}
	var buildChild func(agentID string, depth int, stack map[string]bool) (adk.Agent, error)
	buildChild = func(agentID string, depth int, stack map[string]bool) (adk.Agent, error) {
		if existing, ok := childAgents[agentID]; ok {
			return existing, nil
		}
		if stack[agentID] {
			return nil, agentdelegation.ErrCycle
		}
		stack[agentID] = true
		defer delete(stack, agentID)

		parts, err := b.loadChildAgentParts(ctx, job, run, agentID, pendingKey, nodeByID[agentID])
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
				agentTool := adk.NewAgentTool(ctx, child)
				inv, ok := agentTool.(tool.InvokableTool)
				if !ok {
					return nil, fmt.Errorf("agent tool not invokable: %s", edge.CallableName)
				}
				audited, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
					Inner: inv, Name: edge.CallableName, Description: edge.Description,
					Edge: edge, Audit: b.delegation.Audit, DefaultCallerAgentID: agentID,
					Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
					ChildRuns:             b.delegation.ChildRuns,
					TargetSnapshots:       targetSnapshotsFromNode(nodeByID[edge.TargetAgentID], snap),
					EnqueueFinalizeOutbox: b.delegation.EnqueueFinalizeOutbox,
				})
				if err != nil {
					return nil, err
				}
				tools = append(tools, audited)
			}
		}
		// Prefer frozen remotes from parent graph snapshot (execute freeze-only).
		// v1 with RemotesFrozen must never live-fallback; legacy may list live fail-closed.
		remotes := frozenRemotesForAgent(job.WorkspaceID, run.AgentGraphSnapshot, agentID)
		if remotes == nil && b.delegation != nil && b.delegation.RemoteBindings != nil {
			if hasFrozenRemotesKey(job.WorkspaceID, run.AgentGraphSnapshot) {
				return nil, fmt.Errorf("frozen remotes required for agent %s (no live fallback)", agentID)
			}
			live, rerr := b.delegation.RemoteBindings.ListEnabledRemotesForCaller(ctx, job.WorkspaceID, agentID)
			if rerr != nil {
				return nil, fmt.Errorf("list live remotes for agent %s: %w", agentID, rerr)
			}
			remotes = live
		}
		if remotes == nil {
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
		maxIter := b.maxIterations
		if maxIter <= 0 {
			maxIter = einoruntime.DefaultMaxIterations
		}
		name := strings.TrimSpace(parts.Name)
		if name == "" {
			name = "agent-" + agentID
		}
		desc := strings.TrimSpace(parts.Description)
		if desc == "" {
			desc = "workspace agent"
		}
		child, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name: name, Description: desc, Instruction: parts.Instruction, Model: parts.Model,
			MaxIterations: maxIter,
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{
					Tools: tools, ExecuteSequentially: true,
				},
				EmitInternalEvents: false,
			},
		})
		if err != nil {
			return nil, err
		}
		childAgents[agentID] = child
		return child, nil
	}

	out := append([]tool.BaseTool{}, rootTools...)
	// Cross-source callable_name uniqueness: pipeline tools + internal edges + remotes.
	names := map[string]string{}
	for _, t := range rootTools {
		info, ierr := t.Info(ctx)
		if ierr != nil {
			return nil, nil, nil, fmt.Errorf("root tool Info: %w", ierr)
		}
		if info != nil && info.Name != "" {
			names[info.Name] = "capability"
		}
	}
	// Use the in-memory snap for remotes/namespace — never re-read an empty run field
	// after a just-built freeze.
	snapRaw := run.AgentGraphSnapshot
	if snap != nil {
		if raw, rerr := graphSnapshotBytes(snap); rerr == nil {
			snapRaw = raw
			run.AgentGraphSnapshot = raw
		}
	}
	// Case-insensitive namespace for root + every reachable child agent (fail-closed).
	if err := assertReachableCallableNamespaces(ctx, b, job.WorkspaceID, run.AgentID, edges, names, snapRaw, snap); err != nil {
		return nil, nil, nil, err
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
			return nil, nil, nil, fmt.Errorf("build child agent %s: %w", edge.TargetAgentID, err)
		}
		agentTool := adk.NewAgentTool(ctx, child)
		inv, ok := agentTool.(tool.InvokableTool)
		if !ok {
			return nil, nil, nil, fmt.Errorf("agent tool not invokable: %s", edge.CallableName)
		}
		audited, err := agentdelegation.NewAuditedAgentTool(agentdelegation.AgentToolConfig{
			Inner: inv, Name: edge.CallableName, Description: edge.Description,
			Edge: edge, Audit: b.delegation.Audit, DefaultCallerAgentID: run.AgentID,
			Protocol: agentdelegation.ProtocolInternal, Origin: agentdelegation.OriginInternal,
			ChildRuns:             b.delegation.ChildRuns,
			TargetSnapshots:       targetSnapshotsFromNode(nodeByID[edge.TargetAgentID], snap),
			EnqueueFinalizeOutbox: b.delegation.EnqueueFinalizeOutbox,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		out = append(out, audited)
	}
	remotes := frozenRemotesForAgent(job.WorkspaceID, snapRaw, run.AgentID)
	if remotes == nil && b.delegation != nil && b.delegation.RemoteBindings != nil && !hasFrozenRemotesKey(job.WorkspaceID, snapRaw) {
		// Legacy only: live remotes when freeze key absent.
		live, rerr := b.delegation.RemoteBindings.ListEnabledRemotesForCaller(ctx, job.WorkspaceID, run.AgentID)
		if rerr != nil {
			return nil, nil, nil, fmt.Errorf("list live remotes: %w", rerr)
		}
		remotes = live
	}
	if remotes == nil {
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
			return nil, nil, nil, fmt.Errorf("a2a remote %s: %w", remote.CallableName, oerr)
		}
		out = append(out, ot)
	}
	return out, budget, snap, nil
}

// assertCombinedCallableNamespace fail-closes when capability / internal binding /
// A2A remote share a callable_name for the same caller agent (start/attach path).
// Names are matched case-insensitively (trim + lower).
func assertCombinedCallableNamespace(
	names map[string]string,
	agentID string,
	edges []agentdelegation.GraphEdgeSnapshot,
	remotes []a2agateway.RemoteBinding,
) error {
	if names == nil {
		names = map[string]string{}
	}
	// Normalize existing keys to lower.
	norm := map[string]string{}
	for k, v := range names {
		norm[strings.ToLower(strings.TrimSpace(k))] = v
	}
	for _, edge := range edges {
		if edge.CallerAgentID != agentID {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(edge.CallableName))
		if name == "" {
			continue
		}
		if src, ok := norm[name]; ok {
			return fmt.Errorf("callable_name conflict %q already used by %s (edge %s)",
				edge.CallableName, src, edge.BindingID)
		}
		norm[name] = "internal_binding"
		names[name] = "internal_binding"
	}
	for _, remote := range remotes {
		name := strings.ToLower(strings.TrimSpace(remote.CallableName))
		if name == "" {
			continue
		}
		if src, ok := norm[name]; ok {
			return fmt.Errorf("callable_name conflict %q already used by %s (remote %s)",
				remote.CallableName, src, remote.ID)
		}
		norm[name] = "a2a_remote"
		names[name] = "a2a_remote"
	}
	return nil
}

// assertReachableCallableNamespaces checks capability+internal+remote for root
// and every reachable child agent in the graph edges. Prefer frozen snap data;
// catalog/remote query errors fail-closed.
func assertReachableCallableNamespaces(
	ctx context.Context,
	b *Bridge,
	workspaceID, rootAgentID string,
	edges []agentdelegation.GraphEdgeSnapshot,
	rootCapNames map[string]string,
	graphSnap json.RawMessage,
	snap *agentdelegation.GraphSnapshotV1,
) error {
	// Collect agents.
	agents := map[string]bool{rootAgentID: true}
	for _, e := range edges {
		if e.CallerAgentID != "" {
			agents[e.CallerAgentID] = true
		}
		if e.TargetAgentID != "" && (e.Protocol == "" || e.Protocol == agentdelegation.ProtocolInternal) {
			agents[e.TargetAgentID] = true
		}
	}
	nodeByID := map[string]agentdelegation.GraphNodeSnapshot{}
	if snap != nil {
		for _, n := range snap.Nodes {
			nodeByID[n.AgentID] = n
		}
	}
	for agentID := range agents {
		names := map[string]string{}
		if agentID == rootAgentID {
			for k, v := range rootCapNames {
				names[strings.ToLower(strings.TrimSpace(k))] = v
			}
		} else if node, ok := nodeByID[agentID]; ok && len(node.CapabilitySnapshot) > 0 {
			// Prefer frozen capability snapshot callable names.
			caps, cerr := chatruntime.ParseCapabilitySnapshot(node.CapabilitySnapshot)
			if cerr != nil {
				return fmt.Errorf("agent %s frozen capability parse: %w", agentID, cerr)
			}
			for _, c := range caps {
				n := strings.ToLower(strings.TrimSpace(c.CallableName))
				if n != "" {
					names[n] = "capability"
				}
			}
		} else if b != nil && b.delegation != nil && b.delegation.Catalog != nil {
			descs, err := b.delegation.Catalog.ListForAgent(ctx, workspaceID, agentID)
			if err != nil {
				return fmt.Errorf("agent %s list capabilities: %w", agentID, err)
			}
			for _, d := range descs {
				n := strings.ToLower(strings.TrimSpace(d.CallableName))
				if n != "" {
					names[n] = "capability"
				}
			}
		}
		remotes := frozenRemotesForAgent(workspaceID, graphSnap, agentID)
		if remotes == nil && b != nil && b.delegation != nil && b.delegation.RemoteBindings != nil && !hasFrozenRemotesKey(workspaceID, graphSnap) {
			live, err := b.delegation.RemoteBindings.ListEnabledRemotesForCaller(ctx, workspaceID, agentID)
			if err != nil {
				return fmt.Errorf("agent %s list remotes: %w", agentID, err)
			}
			remotes = live
		}
		if remotes == nil {
			remotes = []a2agateway.RemoteBinding{}
		}
		if err := assertCombinedCallableNamespace(names, agentID, edges, remotes); err != nil {
			return fmt.Errorf("agent %s: %w", agentID, err)
		}
	}
	return nil
}

func hasFrozenRemotesKey(workspaceID string, raw json.RawMessage) bool {
	snap, err := agentdelegation.ParseSnapshot(workspaceID, raw)
	if err == nil && snap != nil && snap.RemotesFrozen {
		return true
	}
	// Legacy extension key (pre-structured freeze).
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	if _, ok := doc["frozenRemotesByCaller"]; ok {
		return true
	}
	_, ok := doc["frozenRemotes"]
	return ok
}

// frozenRemotesForAgent returns frozen remotes for caller. When RemotesFrozen,
// empty slice means "no remotes" (do not live-fallback). Returns nil only for
// legacy snapshots without freeze so callers may fall back carefully.
func frozenRemotesForAgent(workspaceID string, raw json.RawMessage, agentID string) []a2agateway.RemoteBinding {
	snap, err := agentdelegation.ParseSnapshot(workspaceID, raw)
	if err == nil && snap != nil && snap.RemotesFrozen {
		list := snap.FrozenRemotesByCaller[agentID]
		out := make([]a2agateway.RemoteBinding, 0, len(list))
		for _, r := range list {
			out = append(out, a2agateway.RemoteBinding{
				ID: r.ID, CallerAgentID: firstNonEmpty(r.CallerAgentID, agentID),
				CallableName: r.CallableName, Description: r.Description,
				EndpointURL: r.EndpointURL, AgentCardURL: r.AgentCardURL,
				AllowedHosts:  append([]string(nil), r.AllowedHosts...),
				AuthSecretRef: r.AuthSecretRef, TimeoutMs: r.TimeoutMs,
				Version: r.Version, Enabled: true,
			})
		}
		return out // may be empty slice — authoritative
	}
	// Legacy: top-level frozenRemotes array (root-only).
	var doc struct {
		FrozenRemotes []a2agateway.RemoteBinding `json:"frozenRemotes"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.FrozenRemotes) == 0 {
		return nil // signal: no freeze → controlled live fallback for legacy only
	}
	out := make([]a2agateway.RemoteBinding, 0, len(doc.FrozenRemotes))
	for _, r := range doc.FrozenRemotes {
		if r.CallerAgentID == "" || r.CallerAgentID == agentID {
			r.Enabled = true
			out = append(out, r)
		}
	}
	return out
}

// freezeGraphSnapshot builds agent_graph_snapshot.v1 with full node metadata (fail-closed).
func (b *Bridge) freezeGraphSnapshot(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	edges []agentdelegation.GraphEdgeSnapshot,
) (*agentdelegation.GraphSnapshotV1, error) {
	// Collect agents in the reachable tree.
	type agentDepth struct {
		id    string
		depth int
	}
	seen := map[string]int{run.AgentID: 0}
	queue := []agentDepth{{run.AgentID, 0}}
	maxDepth := agentdelegation.DefaultMaxDepth
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, e := range edges {
			if e.CallerAgentID != cur.id {
				continue
			}
			if e.Protocol != "" && e.Protocol != agentdelegation.ProtocolInternal {
				continue
			}
			nd := cur.depth + 1
			if prev, ok := seen[e.TargetAgentID]; !ok || nd < prev {
				seen[e.TargetAgentID] = nd
				queue = append(queue, agentDepth{e.TargetAgentID, nd})
			}
		}
	}
	nodes := make([]agentdelegation.GraphNodeSnapshot, 0, len(seen))
	for agentID, depth := range seen {
		node, err := b.snapshotAgentNode(ctx, job.WorkspaceID, agentID, depth)
		if err != nil {
			return nil, fmt.Errorf("snapshot agent %s: %w", agentID, err)
		}
		nodes = append(nodes, node)
	}
	// Enforce TASK_ONLY on edges.
	for i := range edges {
		if edges[i].ContextPolicy == "" {
			edges[i].ContextPolicy = agentdelegation.ContextTaskOnly
		}
		if edges[i].ContextPolicy != agentdelegation.ContextTaskOnly {
			return nil, fmt.Errorf("%w: edge %s context_policy %q unsupported",
				agentdelegation.ErrInvalid, edges[i].CallableName, edges[i].ContextPolicy)
		}
	}
	builtAt := time.Now().UTC()
	if b.now != nil {
		builtAt = b.now().UTC()
	}
	// Freeze remotes for EVERY reachable caller (including explicit empty lists).
	// List errors fail-closed; order is deterministic for reproducibility.
	remotesByCaller := map[string][]agentdelegation.FrozenRemoteBinding{}
	agentIDs := make([]string, 0, len(seen))
	for agentID := range seen {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		remotesByCaller[agentID] = []agentdelegation.FrozenRemoteBinding{}
		if b.delegation != nil && b.delegation.RemoteBindings != nil {
			live, rerr := b.delegation.RemoteBindings.ListEnabledRemotesForCaller(ctx, job.WorkspaceID, agentID)
			if rerr != nil {
				return nil, fmt.Errorf("list remotes for freeze agent %s: %w", agentID, rerr)
			}
			sort.Slice(live, func(i, j int) bool {
				if live[i].CallableName == live[j].CallableName {
					return live[i].ID < live[j].ID
				}
				return live[i].CallableName < live[j].CallableName
			})
			for _, rem := range live {
				remotesByCaller[agentID] = append(remotesByCaller[agentID], agentdelegation.FrozenRemoteBinding{
					ID: rem.ID, CallerAgentID: agentID, CallableName: rem.CallableName,
					Description: rem.Description, EndpointURL: rem.EndpointURL,
					AgentCardURL: rem.AgentCardURL, AllowedHosts: append([]string(nil), rem.AllowedHosts...),
					AuthSecretRef: rem.AuthSecretRef, TimeoutMs: rem.TimeoutMs, Version: rem.Version,
				})
			}
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Depth == nodes[j].Depth {
			return nodes[i].AgentID < nodes[j].AgentID
		}
		return nodes[i].Depth < nodes[j].Depth
	})
	snap := &agentdelegation.GraphSnapshotV1{
		SchemaVersion:         agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:           run.AgentID,
		MaxDepth:              agentdelegation.DefaultMaxDepth,
		MaxTotal:              agentdelegation.DefaultMaxTotalDelegations,
		MaxPerBinding:         agentdelegation.DefaultMaxPerBinding,
		Nodes:                 nodes,
		Edges:                 edges,
		BuiltAt:               builtAt,
		FrozenRemotesByCaller: remotesByCaller,
		RemotesFrozen:         true,
	}
	// Producer self-check: freeze output must satisfy the same strict+semantic
	// contract that ParseSnapshot enforces (fail closed, do not persist invalid).
	raw, err := agentdelegation.SnapshotJSON(*snap)
	if err != nil {
		return nil, fmt.Errorf("marshal frozen graph: %w", err)
	}
	if _, err := agentdelegation.ParseSnapshot(job.WorkspaceID, raw); err != nil {
		return nil, fmt.Errorf("producer freeze failed semantic contract: %w", err)
	}
	return snap, nil
}

func (b *Bridge) snapshotAgentNode(ctx context.Context, workspaceID, agentID string, depth int) (agentdelegation.GraphNodeSnapshot, error) {
	configured, err := b.agents.Get(ctx, workspaceID, agentID)
	if err != nil {
		return agentdelegation.GraphNodeSnapshot{}, err
	}
	if configured.Status != agent.StatusActive {
		return agentdelegation.GraphNodeSnapshot{}, agentdelegation.ErrAgentUnavailable
	}
	liveCfg, err := b.models.Get(ctx, workspaceID, configured.ModelConfigID)
	if err != nil {
		return agentdelegation.GraphNodeSnapshot{}, err
	}
	capSnap, err := b.capabilitySnapshotJSON(ctx, workspaceID, agentID)
	if err != nil {
		return agentdelegation.GraphNodeSnapshot{}, err
	}
	promptRev := ""
	promptHash := ""
	if configured.CurrentPromptRevisionID != nil {
		promptRev = *configured.CurrentPromptRevisionID
	}
	// Resolve prompt content hash from revision list (fail closed if revision id set but missing).
	if promptRev != "" {
		revs, rerr := b.agents.ListPromptRevisions(ctx, workspaceID, agentID)
		if rerr != nil {
			return agentdelegation.GraphNodeSnapshot{}, fmt.Errorf("list prompt revisions: %w", rerr)
		}
		found := false
		for _, rev := range revs {
			if rev.ID == promptRev {
				promptHash = strings.TrimSpace(rev.ContentSHA256)
				if promptHash == "" && strings.TrimSpace(rev.SystemPrompt) != "" {
					// Deterministic fallback when SHA not stored on revision row.
					promptHash = execution.HashJSONObject(json.RawMessage(strconv.Quote(rev.SystemPrompt)))
				}
				found = true
				break
			}
		}
		if !found {
			return agentdelegation.GraphNodeSnapshot{}, fmt.Errorf("prompt revision %s not found for agent %s", promptRev, agentID)
		}
	}
	// Node model shape is intentionally narrower than root run.ModelSnapshot
	// (no status/agenticCapabilities/runtimeCapabilities). options is always an
	// object; credentialSecretId is always present and may be JSON null.
	options := liveCfg.Options
	if len(options) == 0 || string(options) == "null" {
		options = json.RawMessage(`{}`)
	}
	modelSnap, merr := json.Marshal(map[string]any{
		"id": liveCfg.ID, "provider": liveCfg.Provider, "apiBase": liveCfg.APIBase,
		"modelName": liveCfg.ModelName, "options": options,
		"credentialSecretId": liveCfg.CredentialSecretID, "lockVersion": liveCfg.LockVersion,
	})
	if merr != nil {
		return agentdelegation.GraphNodeSnapshot{}, merr
	}
	roleDesc := strings.TrimSpace(configured.RoleDescription)
	// Agent-binding.v1 closed producer keys (always emitted, empty strings legal).
	agentSnap, aerr := json.Marshal(map[string]any{
		"schemaVersion": "agent-binding.v1", "agentId": agentID, "name": configured.Name,
		"roleDescription":  roleDesc,
		"promptRevisionId": promptRev, "promptRevisionHash": promptHash,
		"modelConfigId": liveCfg.ID, "modelConfigLockVer": liveCfg.LockVersion,
	})
	if aerr != nil {
		return agentdelegation.GraphNodeSnapshot{}, aerr
	}
	return agentdelegation.GraphNodeSnapshot{
		AgentID: agentID, Name: configured.Name, Depth: depth,
		PromptRevisionID: promptRev, PromptRevisionHash: promptHash,
		ModelConfigID: liveCfg.ID, ModelConfigLockVer: liveCfg.LockVersion,
		ModelSnapshot: modelSnap, AgentSnapshot: agentSnap,
		CapabilitySnapshot: capSnap,
	}, nil
}

func (b *Bridge) capabilitySnapshotJSON(ctx context.Context, workspaceID, agentID string) (json.RawMessage, error) {
	// Empty catalog is allowed for pure-text agents (inbound freeze / no tools).
	if b.delegation == nil || b.delegation.Catalog == nil {
		return json.Marshal(map[string]any{
			"schemaVersion": "capability-snapshot.v1", "releases": []any{},
		})
	}
	descriptors, err := b.delegation.Catalog.ListForAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	releases := make([]map[string]any, 0, len(descriptors))
	for _, item := range descriptors {
		inSchema := json.RawMessage(item.InputSchema)
		if len(inSchema) == 0 || string(inSchema) == "null" {
			inSchema = json.RawMessage(`{}`)
		}
		outSchema := json.RawMessage(item.OutputSchema)
		if len(outSchema) == 0 || string(outSchema) == "null" {
			outSchema = json.RawMessage(`{}`)
		}
		entry := map[string]any{
			"capabilityId": item.CapabilityID, "releaseId": item.ReleaseID, "kind": item.Kind,
			"callableName": item.CallableName, "callableDescription": item.CallableDescription,
			"inputSchema": inSchema, "outputSchema": outSchema,
			"riskLevel": item.RiskLevel, "sideEffectLevel": item.SideEffectLevel,
			"requiresConfirmation": item.RequiresConfirmation,
		}
		if item.ConnectionID != "" {
			entry["connectionId"] = item.ConnectionID
		}
		releases = append(releases, entry)
	}
	return json.Marshal(map[string]any{
		"schemaVersion": "capability-snapshot.v1", "releases": releases,
	})
}

func (b *Bridge) loadChildAgentParts(
	ctx context.Context,
	job agentrun.Job,
	parentRun execution.AgentRun,
	agentID string,
	pendingKey string,
	node agentdelegation.GraphNodeSnapshot,
) (agentdelegation.AgentParts, error) {
	configured, err := b.agents.Get(ctx, job.WorkspaceID, agentID)
	if err != nil {
		return agentdelegation.AgentParts{}, err
	}
	if configured.Status != agent.StatusActive {
		return agentdelegation.AgentParts{}, agentdelegation.ErrAgentUnavailable
	}
	modelID := configured.ModelConfigID
	if node.ModelConfigID != "" {
		modelID = node.ModelConfigID
	}
	// Freeze-only: missing/malformed node snapshots fail closed (no live config fallback).
	// Live load is kill-switch + credential material only.
	if node.AgentID == "" || strings.TrimSpace(node.ModelConfigID) == "" ||
		len(node.ModelSnapshot) == 0 || string(node.ModelSnapshot) == "{}" ||
		len(node.AgentSnapshot) == 0 || string(node.AgentSnapshot) == "{}" ||
		len(node.CapabilitySnapshot) == 0 {
		return agentdelegation.AgentParts{}, fmt.Errorf(
			"frozen node incomplete for agent %s (require model/agent/capability snapshots; no live fallback)", agentID)
	}
	frozen, _, ferr := parseModelSnapshot(node.ModelSnapshot)
	if ferr != nil {
		return agentdelegation.AgentParts{}, fmt.Errorf("parse frozen model snapshot: %w", ferr)
	}
	buildCfg := frozen
	modelID = firstNonEmpty(frozen.ID, node.ModelConfigID, modelID)
	liveCfg, err := b.models.Get(ctx, job.WorkspaceID, modelID)
	if err != nil {
		return agentdelegation.AgentParts{}, err
	}
	// Live kill-switch only: DISABLED / inactive blocks; ordinary lock drift
	// or name edits must not change frozen run execution.
	if liveCfg.Status == modelconfig.StatusDisabled {
		return agentdelegation.AgentParts{}, agentdelegation.ErrAgentUnavailable
	}
	buildCfg.WorkspaceID = liveCfg.WorkspaceID
	if buildCfg.CredentialSecretID == nil {
		buildCfg.CredentialSecretID = liveCfg.CredentialSecretID
	}
	if buildCfg.Status == "" {
		buildCfg.Status = modelconfig.StatusVerified
	}
	// Default instruction only when freeze has no prompt revision (explicit freeze decision).
	const defaultChildPrompt = "You are a helpful workspace agent. Answer clearly and concisely."
	instruction := defaultChildPrompt
	// Fail closed: when graph freezes a prompt revision, load and verify hash.
	if node.PromptRevisionID != "" {
		revs, rerr := b.agents.ListPromptRevisions(ctx, job.WorkspaceID, agentID)
		if rerr != nil {
			return agentdelegation.AgentParts{}, fmt.Errorf("list prompt revisions for frozen node: %w", rerr)
		}
		found := false
		for _, rev := range revs {
			if rev.ID != node.PromptRevisionID {
				continue
			}
			found = true
			if strings.TrimSpace(rev.SystemPrompt) == "" {
				return agentdelegation.AgentParts{}, fmt.Errorf("frozen prompt revision %s empty", node.PromptRevisionID)
			}
			liveHash := strings.TrimSpace(rev.ContentSHA256)
			if liveHash == "" {
				liveHash = execution.HashJSONObject(json.RawMessage(strconv.Quote(rev.SystemPrompt)))
			}
			if node.PromptRevisionHash != "" && !strings.EqualFold(liveHash, node.PromptRevisionHash) {
				return agentdelegation.AgentParts{}, fmt.Errorf(
					"%w: prompt hash drift agent=%s rev=%s",
					agentdelegation.ErrAgentUnavailable, agentID, node.PromptRevisionID)
			}
			instruction = strings.TrimSpace(rev.SystemPrompt)
			break
		}
		if !found {
			return agentdelegation.AgentParts{}, fmt.Errorf("frozen prompt revision %s not found for agent %s",
				node.PromptRevisionID, agentID)
		}
	}
	// When freeze has no revision id, do NOT pull live current prompt (immutability).

	childRun := parentRun
	childRun.AgentID = agentID
	// Freeze-only: never rebuild capability catalog from live for child execute.
	childRun.CapabilitySnapshot = node.CapabilitySnapshot
	tools, err := b.buildPipelineTools(ctx, job, childRun, pendingKey)
	if err != nil {
		return agentdelegation.AgentParts{}, fmt.Errorf("build child tools for %s: %w", agentID, err)
	}
	chatModel, err := b.buildModel(ctx, buildCfg)
	if err != nil {
		return agentdelegation.AgentParts{}, err
	}
	// Wrap model so nested AgentTool MODEL turns are audited without emitting
	// into the parent text stream (EmitInternalEvents stays false).
	chatModel = wrapNestedAuditModel(chatModel, b)
	// Prefer frozen identity (name/roleDescription); live agent is kill-switch only.
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
	return agentdelegation.AgentParts{
		AgentID: agentID, Name: name, Description: desc,
		Instruction: instruction, Model: chatModel, Tools: tools,
		PromptRevisionID: node.PromptRevisionID, ModelConfigID: firstNonEmpty(buildCfg.ID, liveCfg.ID),
		ModelConfigLockVer: firstNonEmptyInt64(node.ModelConfigLockVer, liveCfg.LockVersion),
		CapabilitySnapshot: childRun.CapabilitySnapshot,
	}, nil
}

func firstNonEmptyInt64(vals ...int64) int64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func targetSnapshotsFromNode(node agentdelegation.GraphNodeSnapshot, snap *agentdelegation.GraphSnapshotV1) agentdelegation.ChildRunStartInput {
	out := agentdelegation.ChildRunStartInput{
		TargetAgentID:      node.AgentID,
		ModelSnapshot:      node.ModelSnapshot,
		CapabilitySnapshot: node.CapabilitySnapshot,
		AgentSnapshot:      node.AgentSnapshot,
	}
	if snap != nil {
		if raw, err := agentdelegation.SnapshotJSON(*snap); err == nil {
			out.GraphSnapshot = raw
		}
	}
	return out
}

func withDelegationRunContext(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	budget *agentdelegation.Budget,
) context.Context {
	return agentdelegation.WithRunContext(ctx, &agentdelegation.RunContext{
		WorkspaceID: job.WorkspaceID, ParentRunID: job.RunID, RootRunID: job.RunID,
		RunID: job.RunID, CallerAgentID: run.AgentID, Depth: 0, Budget: budget,
		TraceID: run.TraceID,
	})
}

func graphSnapshotBytes(snap *agentdelegation.GraphSnapshotV1) (json.RawMessage, error) {
	if snap == nil {
		return nil, fmt.Errorf("nil graph snapshot")
	}
	return agentdelegation.SnapshotJSON(*snap)
}
