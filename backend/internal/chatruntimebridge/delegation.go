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
	"actweave/backend/internal/execution"
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
