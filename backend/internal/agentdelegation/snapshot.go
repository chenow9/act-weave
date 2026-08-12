package agentdelegation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

// AgentParts is everything needed to materialize one ChatModelAgent node under Bridge.
type AgentParts struct {
	AgentID            string
	Name               string
	Description        string
	Instruction        string
	Model              model.BaseChatModel
	Tools              []tool.BaseTool // existing TOOL/WORKFLOW tools
	PromptRevisionID   string
	PromptRevisionHash string
	ModelConfigID      string
	ModelConfigLockVer int64
	CapabilitySnapshot json.RawMessage
}

// SnapshotJSON marshals a graph snapshot for agent_runs.agent_graph_snapshot.
// Always emits the complete producer field set (including budget ints and builtAt)
// with explicit empty containers (never JSON null for nodes/edges/remotes map).
// Does not emit `extra` (freeze producers never set it).
func SnapshotJSON(snap GraphSnapshotV1) (json.RawMessage, error) {
	if snap.SchemaVersion == "" {
		snap.SchemaVersion = GraphSnapshotSchemaV1
	}
	if snap.MaxDepth <= 0 {
		snap.MaxDepth = DefaultMaxDepth
	}
	if snap.MaxTotal <= 0 {
		snap.MaxTotal = DefaultMaxTotalDelegations
	}
	if snap.MaxPerBinding <= 0 {
		snap.MaxPerBinding = DefaultMaxPerBinding
	}
	if snap.Nodes == nil {
		snap.Nodes = []GraphNodeSnapshot{}
	}
	if snap.Edges == nil {
		snap.Edges = []GraphEdgeSnapshot{}
	}
	if snap.FrozenRemotesByCaller == nil {
		snap.FrozenRemotesByCaller = map[string][]FrozenRemoteBinding{}
	}
	for k, v := range snap.FrozenRemotesByCaller {
		if v == nil {
			snap.FrozenRemotesByCaller[k] = []FrozenRemoteBinding{}
		}
	}
	// Structured marshal so omitempty cannot drop producer-required zeros.
	doc := map[string]any{
		"schemaVersion":         snap.SchemaVersion,
		"rootAgentId":           snap.RootAgentID,
		"maxDepth":              snap.MaxDepth,
		"maxTotalDelegations":   snap.MaxTotal,
		"maxPerBinding":         snap.MaxPerBinding,
		"nodes":                 snap.Nodes,
		"edges":                 snap.Edges,
		"builtAt":               snap.BuiltAt.UTC(),
		"frozenRemotesByCaller": snap.FrozenRemotesByCaller,
		"remotesFrozen":         snap.RemotesFrozen,
	}
	return json.Marshal(doc)
}

// ParseSnapshot unmarshals agent_graph_snapshot JSON and validates v1 integrity
// (root/reachable nodes, frozen model/agent/capability, RemotesFrozen + per-caller keys).
// Empty/null/{} remain "no snapshot" for legacy callers. Any non-empty document
// is strict: duplicate keys, null containers, and missing required fields fail closed.
//
// workspaceID is the tenant that owns the run carrying this freeze. It is required
// so frozen remote authSecretRef values are checked against the owning workspace
// exactly like a2agateway.CreateRemote does; a missing or malformed workspaceID
// fails the whole parse rather than silently disabling the cross-tenant check.
//
// Parsing performs no network I/O: remote policy uses the egressguard syntax layer
// only. DNS/IP SSRF resolution happens at dial time with the caller's context.
func ParseSnapshot(workspaceID string, raw json.RawMessage) (*GraphSnapshotV1, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil, nil
	}
	return parseSnapshotStrict(workspaceID, raw)
}

// ValidateGraphSnapshotIntegrity fail-closes incomplete v1 freezes.
// Live config fallback is never allowed when a non-empty v1 snapshot is present.
func ValidateGraphSnapshotIntegrity(snap *GraphSnapshotV1) error {
	if snap == nil {
		return fmt.Errorf("graph snapshot is nil")
	}
	if snap.SchemaVersion != GraphSnapshotSchemaV1 {
		return fmt.Errorf("unsupported graph snapshot schema %q", snap.SchemaVersion)
	}
	root := strings.TrimSpace(snap.RootAgentID)
	if root == "" {
		return fmt.Errorf("graph snapshot rootAgentId required")
	}
	nodeByID := make(map[string]GraphNodeSnapshot, len(snap.Nodes))
	for _, n := range snap.Nodes {
		id := strings.TrimSpace(n.AgentID)
		if id == "" {
			return fmt.Errorf("graph snapshot node missing agentId")
		}
		if _, dup := nodeByID[id]; dup {
			return fmt.Errorf("graph snapshot duplicate node agentId %s", id)
		}
		if err := validateFrozenNode(n); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
		nodeByID[id] = n
	}
	if _, ok := nodeByID[root]; !ok {
		return fmt.Errorf("graph snapshot missing root node %s", root)
	}
	// Validate all INTERNAL edges: non-empty identity, both ends in node set.
	// Reject duplicate bindingId / caller+callable collisions (no silent last-wins).
	bindingSeen := map[string]struct{}{}
	callableSeen := map[string]struct{}{} // key: lower(caller)|lower(callable)
	for i, e := range snap.Edges {
		caller := strings.TrimSpace(e.CallerAgentID)
		target := strings.TrimSpace(e.TargetAgentID)
		name := strings.TrimSpace(e.CallableName)
		bid := strings.TrimSpace(e.BindingID)
		proto := strings.TrimSpace(e.Protocol)
		if proto == "" {
			proto = ProtocolInternal
		}
		if proto != ProtocolInternal {
			continue // remotes not in edges as INTERNAL
		}
		if caller == "" || target == "" {
			return fmt.Errorf("edge[%d] missing caller/target", i)
		}
		if name == "" {
			return fmt.Errorf("edge[%d] callableName required", i)
		}
		if _, ok := nodeByID[caller]; !ok {
			return fmt.Errorf("edge[%d] caller %s not in frozen nodes", i, caller)
		}
		if _, ok := nodeByID[target]; !ok {
			return fmt.Errorf("edge[%d] target %s not in frozen nodes", i, target)
		}
		if bid != "" {
			if _, ok := bindingSeen[bid]; ok {
				return fmt.Errorf("graph snapshot duplicate bindingId %s", bid)
			}
			bindingSeen[bid] = struct{}{}
		}
		ck := strings.ToLower(caller) + "|" + strings.ToLower(name)
		if _, ok := callableSeen[ck]; ok {
			return fmt.Errorf("graph snapshot duplicate callable %s for caller %s", name, caller)
		}
		callableSeen[ck] = struct{}{}
	}
	// Reachable nodes from root via INTERNAL edges must all be present.
	maxDepth := snap.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	seen := map[string]int{root: 0}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		depth := seen[cur]
		if depth >= maxDepth {
			continue
		}
		for _, e := range snap.Edges {
			if strings.TrimSpace(e.CallerAgentID) != cur {
				continue
			}
			if e.Protocol != "" && e.Protocol != ProtocolInternal {
				continue
			}
			tid := strings.TrimSpace(e.TargetAgentID)
			if tid == "" {
				return fmt.Errorf("edge %s missing targetAgentId", e.CallableName)
			}
			if _, ok := nodeByID[tid]; !ok {
				return fmt.Errorf("graph snapshot missing reachable node %s (from %s)", tid, cur)
			}
			if _, ok := seen[tid]; !ok {
				seen[tid] = depth + 1
				queue = append(queue, tid)
			}
		}
	}
	// Remotes must be frozen for every reachable caller (explicit empty OK).
	if !snap.RemotesFrozen {
		return fmt.Errorf("graph snapshot remotesFrozen required (freeze-only)")
	}
	if snap.FrozenRemotesByCaller == nil {
		return fmt.Errorf("graph snapshot frozenRemotesByCaller required when remotesFrozen")
	}
	for agentID := range seen {
		if _, ok := snap.FrozenRemotesByCaller[agentID]; !ok {
			return fmt.Errorf("graph snapshot missing frozen remotes key for caller %s", agentID)
		}
	}
	return nil
}

func validateFrozenNode(n GraphNodeSnapshot) error {
	if strings.TrimSpace(n.ModelConfigID) == "" {
		return fmt.Errorf("modelConfigId required")
	}
	if len(n.ModelSnapshot) == 0 || string(n.ModelSnapshot) == "null" || string(n.ModelSnapshot) == "{}" {
		return fmt.Errorf("modelSnapshot required (freeze-only)")
	}
	if len(n.AgentSnapshot) == 0 || string(n.AgentSnapshot) == "null" || string(n.AgentSnapshot) == "{}" {
		return fmt.Errorf("agentSnapshot required (freeze-only)")
	}
	if len(n.CapabilitySnapshot) == 0 || string(n.CapabilitySnapshot) == "null" {
		return fmt.Errorf("capabilitySnapshot required (freeze-only)")
	}
	// capability may be empty releases list but must be a valid object with schema.
	var capDoc map[string]any
	if err := json.Unmarshal(n.CapabilitySnapshot, &capDoc); err != nil {
		return fmt.Errorf("capabilitySnapshot malformed: %w", err)
	}
	if sv, _ := capDoc["schemaVersion"].(string); strings.TrimSpace(sv) == "" {
		return fmt.Errorf("capabilitySnapshot schemaVersion required")
	}
	return nil
}

// BindingsToEdges converts repository bindings to graph edges.
func BindingsToEdges(bindings []Binding) []GraphEdgeSnapshot {
	out := make([]GraphEdgeSnapshot, 0, len(bindings))
	for _, b := range bindings {
		if !b.Enabled || b.DeletedAt != nil {
			continue
		}
		out = append(out, GraphEdgeSnapshot{
			BindingID: b.ID, CallerAgentID: b.CallerAgentID, TargetAgentID: b.TargetAgentID,
			CallableName: b.CallableName, Description: b.Description,
			Mode: b.Mode, ContextPolicy: b.ContextPolicy, Version: b.Version,
			Protocol: ProtocolInternal,
		})
	}
	return out
}
