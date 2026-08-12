package agentdelegation

import (
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/egressguard"
	"actweave/backend/internal/modelapi"

	"github.com/google/uuid"
)

// Capability release enum domains — exact parity with capability/repository
// validate create (TOOL|WORKFLOW, risk, side-effect levels).
var (
	capKindAllowed = map[string]struct{}{
		"TOOL": {}, "WORKFLOW": {},
	}
	capRiskAllowed = map[string]struct{}{
		"LOW": {}, "MEDIUM": {}, "HIGH": {}, "CRITICAL": {},
	}
	capSideEffectAllowed = map[string]struct{}{
		"NONE": {}, "READ": {}, "WRITE": {}, "IRREVERSIBLE": {},
	}
)

// validateGraphSnapshotSemantics enforces the domain contract in
// snapshot_semantic.md after structural raw closure. Failures prevent
// ParseSnapshot success (Agentic initial maps them to AGENTIC_GRAPH_SNAPSHOT_REQUIRED).
//
// workspaceID is the tenant owning the freeze; it is mandatory so remote
// authSecretRef cross-tenant binding is enforced at the same strength as the
// a2agateway write path.
func validateGraphSnapshotSemantics(workspaceID string, snap *GraphSnapshotV1) error {
	if snap == nil {
		return fmt.Errorf("graph snapshot is nil")
	}
	if !canonicalUUID(strings.TrimSpace(workspaceID)) {
		return fmt.Errorf("graph snapshot workspace binding required")
	}
	if !canonicalUUID(snap.RootAgentID) {
		return fmt.Errorf("rootAgentId must be canonical UUID")
	}
	if snap.MaxDepth < 1 || snap.MaxTotal < 1 || snap.MaxPerBinding < 1 {
		return fmt.Errorf("budget fields must be >= 1")
	}

	nodeByID := make(map[string]GraphNodeSnapshot, len(snap.Nodes))
	for i, n := range snap.Nodes {
		if err := validateNodeSemantics(n, i); err != nil {
			return err
		}
		if _, dup := nodeByID[n.AgentID]; dup {
			return fmt.Errorf("duplicate node agentId %s", n.AgentID)
		}
		nodeByID[n.AgentID] = n
	}
	root, ok := nodeByID[snap.RootAgentID]
	if !ok {
		return fmt.Errorf("rootAgentId not in nodes")
	}
	if root.Depth != 0 {
		return fmt.Errorf("root node depth must be 0")
	}

	// Edges: internal-only binding domain (not A2A/remote).
	adj := make(map[string][]string, len(nodeByID))
	bindingSeen := map[string]struct{}{}
	callableSeen := map[string]struct{}{}
	for i, e := range snap.Edges {
		if err := validateInternalEdgeSemantics(e, i, nodeByID, bindingSeen, callableSeen); err != nil {
			return err
		}
		adj[e.CallerAgentID] = append(adj[e.CallerAgentID], e.TargetAgentID)
	}

	// Topology: BFS shortest-path depths from root; all nodes reachable; DAG.
	if err := validateTopologySemantics(snap.RootAgentID, nodeByID, adj, snap.MaxDepth); err != nil {
		return err
	}

	// Remotes: CreateRemote-equivalent policy via egressguard.
	if !snap.RemotesFrozen {
		return fmt.Errorf("remotesFrozen must be true")
	}
	if snap.FrozenRemotesByCaller == nil {
		return fmt.Errorf("frozenRemotesByCaller required")
	}
	if len(snap.FrozenRemotesByCaller) != len(nodeByID) {
		return fmt.Errorf("frozenRemotesByCaller key set mismatch")
	}
	for caller, list := range snap.FrozenRemotesByCaller {
		if !canonicalUUID(caller) {
			return fmt.Errorf("frozenRemotesByCaller key must be canonical UUID")
		}
		if _, ok := nodeByID[caller]; !ok {
			return fmt.Errorf("frozenRemotesByCaller foreign caller")
		}
		idSeen := map[string]struct{}{}
		callSeen := map[string]struct{}{}
		for i, rem := range list {
			if err := validateRemoteSemantics(workspaceID, caller, rem, i, idSeen, callSeen); err != nil {
				return err
			}
		}
	}
	for id := range nodeByID {
		if _, ok := snap.FrozenRemotesByCaller[id]; !ok {
			return fmt.Errorf("frozenRemotesByCaller missing node key")
		}
	}
	return nil
}

func validateNodeSemantics(n GraphNodeSnapshot, idx int) error {
	path := fmt.Sprintf("nodes[%d]", idx)
	if !canonicalUUID(n.AgentID) {
		return fmt.Errorf("%s.agentId must be canonical UUID", path)
	}
	if !canonicalUUID(n.ModelConfigID) {
		return fmt.Errorf("%s.modelConfigId must be canonical UUID", path)
	}
	if n.Depth < 0 {
		return fmt.Errorf("%s.depth invalid", path)
	}
	if err := validateNodeModelSemantics(n.ModelSnapshot, path+".modelSnapshot", n.ModelConfigID); err != nil {
		return err
	}
	if err := validateNodeAgentSemantics(n.AgentSnapshot, path+".agentSnapshot", n.AgentID, n.ModelConfigID); err != nil {
		return err
	}
	if err := validateNodeCapSemantics(n.CapabilitySnapshot, path+".capabilitySnapshot"); err != nil {
		return err
	}
	return nil
}

func validateNodeModelSemantics(raw json.RawMessage, path, modelConfigID string) error {
	var doc struct {
		ID                 string          `json:"id"`
		Provider           string          `json:"provider"`
		APIBase            string          `json:"apiBase"`
		ModelName          string          `json:"modelName"`
		Options            json.RawMessage `json:"options"`
		CredentialSecretID *string         `json:"credentialSecretId"`
		LockVersion        int64           `json:"lockVersion"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s malformed: %w", path, err)
	}
	if !canonicalUUID(doc.ID) || doc.ID != modelConfigID {
		return fmt.Errorf("%s.id must be canonical UUID equal to modelConfigId", path)
	}
	if doc.Provider == "" || doc.Provider != strings.TrimSpace(doc.Provider) {
		return fmt.Errorf("%s.provider invalid", path)
	}
	if doc.ModelName == "" || doc.ModelName != strings.TrimSpace(doc.ModelName) {
		return fmt.Errorf("%s.modelName invalid", path)
	}
	// Reuse modelapi construction-time API base policy (rejects empty).
	if _, err := modelapi.ValidateAgenticAPIBase(doc.APIBase); err != nil {
		return fmt.Errorf("%s.apiBase invalid", path)
	}
	if doc.LockVersion < 1 {
		return fmt.Errorf("%s.lockVersion invalid", path)
	}
	if doc.CredentialSecretID != nil {
		if !canonicalUUID(*doc.CredentialSecretID) {
			return fmt.Errorf("%s.credentialSecretId must be canonical UUID when set", path)
		}
	}
	return nil
}

func validateNodeAgentSemantics(raw json.RawMessage, path, agentID, modelConfigID string) error {
	var doc struct {
		SchemaVersion      string `json:"schemaVersion"`
		AgentID            string `json:"agentId"`
		ModelConfigID      string `json:"modelConfigId"`
		ModelConfigLockVer int64  `json:"modelConfigLockVer"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s malformed: %w", path, err)
	}
	if doc.SchemaVersion != nodeAgentBindingSchemaV1 {
		return fmt.Errorf("%s.schemaVersion invalid", path)
	}
	if !canonicalUUID(doc.AgentID) || doc.AgentID != agentID {
		return fmt.Errorf("%s.agentId cross-bind invalid", path)
	}
	if !canonicalUUID(doc.ModelConfigID) || doc.ModelConfigID != modelConfigID {
		return fmt.Errorf("%s.modelConfigId cross-bind invalid", path)
	}
	if doc.ModelConfigLockVer < 1 {
		return fmt.Errorf("%s.modelConfigLockVer invalid", path)
	}
	return nil
}

func validateNodeCapSemantics(raw json.RawMessage, path string) error {
	var doc struct {
		SchemaVersion string `json:"schemaVersion"`
		Releases      []struct {
			CapabilityID         string `json:"capabilityId"`
			ReleaseID            string `json:"releaseId"`
			Kind                 string `json:"kind"`
			CallableName         string `json:"callableName"`
			RiskLevel            string `json:"riskLevel"`
			SideEffectLevel      string `json:"sideEffectLevel"`
			ConnectionID         string `json:"connectionId"`
			RequiresConfirmation bool   `json:"requiresConfirmation"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s malformed: %w", path, err)
	}
	if doc.SchemaVersion != nodeCapabilitySnapshotSchemaV1 {
		return fmt.Errorf("%s.schemaVersion invalid", path)
	}
	for i, rel := range doc.Releases {
		rpath := fmt.Sprintf("%s.releases[%d]", path, i)
		if !canonicalUUID(rel.CapabilityID) {
			return fmt.Errorf("%s.capabilityId must be canonical UUID", rpath)
		}
		if !canonicalUUID(rel.ReleaseID) {
			return fmt.Errorf("%s.releaseId must be canonical UUID", rpath)
		}
		if _, ok := capKindAllowed[rel.Kind]; !ok {
			return fmt.Errorf("%s.kind %q not in closed domain", rpath, rel.Kind)
		}
		if _, ok := capRiskAllowed[rel.RiskLevel]; !ok {
			return fmt.Errorf("%s.riskLevel %q not in closed domain", rpath, rel.RiskLevel)
		}
		if _, ok := capSideEffectAllowed[rel.SideEffectLevel]; !ok {
			return fmt.Errorf("%s.sideEffectLevel %q not in closed domain", rpath, rel.SideEffectLevel)
		}
		if rel.CallableName == "" || rel.CallableName != strings.TrimSpace(rel.CallableName) {
			return fmt.Errorf("%s.callableName invalid", rpath)
		}
		if rel.ConnectionID != "" && !canonicalUUID(rel.ConnectionID) {
			return fmt.Errorf("%s.connectionId must be canonical UUID when set", rpath)
		}
	}
	return nil
}

func validateInternalEdgeSemantics(
	e GraphEdgeSnapshot,
	idx int,
	nodeByID map[string]GraphNodeSnapshot,
	bindingSeen, callableSeen map[string]struct{},
) error {
	path := fmt.Sprintf("edges[%d]", idx)
	// Producer freeze edges are INTERNAL bindings only — A2A lives in remotes map.
	if e.Protocol != ProtocolInternal {
		return fmt.Errorf("%s.protocol must be %s (A2A/remotes are not edges)", path, ProtocolInternal)
	}
	if !validMode(e.Mode) {
		return fmt.Errorf("%s.mode %q not in closed domain", path, e.Mode)
	}
	// Write-path validContextPolicy: TASK_ONLY only (SUMMARY/SELECTED not implemented).
	if !validContextPolicy(e.ContextPolicy) {
		return fmt.Errorf("%s.contextPolicy %q not in closed domain", path, e.ContextPolicy)
	}
	if !canonicalUUID(e.BindingID) {
		return fmt.Errorf("%s.bindingId must be canonical UUID", path)
	}
	if !canonicalUUID(e.CallerAgentID) || !canonicalUUID(e.TargetAgentID) {
		return fmt.Errorf("%s caller/target must be canonical UUIDs", path)
	}
	if e.CallerAgentID == e.TargetAgentID {
		return fmt.Errorf("%s self-edge forbidden", path)
	}
	if _, ok := nodeByID[e.CallerAgentID]; !ok {
		return fmt.Errorf("%s.callerAgentId not in nodes", path)
	}
	if _, ok := nodeByID[e.TargetAgentID]; !ok {
		return fmt.Errorf("%s.targetAgentId not in nodes", path)
	}
	if e.CallableName == "" || e.CallableName != strings.TrimSpace(e.CallableName) {
		return fmt.Errorf("%s.callableName invalid", path)
	}
	if e.Version < 1 {
		return fmt.Errorf("%s.version invalid", path)
	}
	if _, dup := bindingSeen[e.BindingID]; dup {
		return fmt.Errorf("duplicate edge bindingId %s", e.BindingID)
	}
	bindingSeen[e.BindingID] = struct{}{}
	ck := strings.ToLower(e.CallerAgentID) + "|" + strings.ToLower(e.CallableName)
	if _, dup := callableSeen[ck]; dup {
		return fmt.Errorf("duplicate edge callable %s for caller %s", e.CallableName, e.CallerAgentID)
	}
	callableSeen[ck] = struct{}{}
	return nil
}

func validateTopologySemantics(
	root string,
	nodeByID map[string]GraphNodeSnapshot,
	adj map[string][]string,
	maxDepth int,
) error {
	// Directed cycle detection (gray = on stack).
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodeByID))
	var dfs func(string) error
	dfs = func(u string) error {
		color[u] = gray
		for _, v := range adj[u] {
			switch color[v] {
			case gray:
				return fmt.Errorf("graph cycle detected at %s -> %s", u, v)
			case white:
				if err := dfs(v); err != nil {
					return err
				}
			}
		}
		color[u] = black
		return nil
	}
	for id := range nodeByID {
		if color[id] == white {
			if err := dfs(id); err != nil {
				return err
			}
		}
	}

	// BFS shortest-path distances from root (producer semantics).
	dist := map[string]int{root: 0}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		d := dist[cur]
		if d >= maxDepth {
			continue
		}
		for _, tid := range adj[cur] {
			if _, seen := dist[tid]; seen {
				continue
			}
			dist[tid] = d + 1
			queue = append(queue, tid)
		}
	}
	for id, n := range nodeByID {
		want, ok := dist[id]
		if !ok {
			return fmt.Errorf("node %s not reachable from root", id)
		}
		if n.Depth != want {
			return fmt.Errorf("node %s depth %d != shortest-path depth %d", id, n.Depth, want)
		}
		if n.Depth > maxDepth {
			return fmt.Errorf("node %s depth %d exceeds maxDepth %d", id, n.Depth, maxDepth)
		}
	}
	return nil
}

func validateRemoteSemantics(
	workspaceID string,
	caller string,
	rem FrozenRemoteBinding,
	idx int,
	idSeen, callSeen map[string]struct{},
) error {
	path := fmt.Sprintf("frozenRemotesByCaller[%s][%d]", caller, idx)
	if !canonicalUUID(rem.ID) {
		return fmt.Errorf("%s.id must be canonical UUID", path)
	}
	if _, dup := idSeen[rem.ID]; dup {
		return fmt.Errorf("%s duplicate remote id", path)
	}
	idSeen[rem.ID] = struct{}{}
	if rem.CallerAgentID != caller {
		return fmt.Errorf("%s.callerAgentId must equal map key", path)
	}
	if !canonicalUUID(rem.CallerAgentID) {
		return fmt.Errorf("%s.callerAgentId must be canonical UUID", path)
	}
	if rem.CallableName == "" || rem.CallableName != strings.TrimSpace(rem.CallableName) {
		return fmt.Errorf("%s.callableName invalid", path)
	}
	ck := strings.ToLower(rem.CallableName)
	if _, dup := callSeen[ck]; dup {
		return fmt.Errorf("%s duplicate remote callable", path)
	}
	callSeen[ck] = struct{}{}
	if rem.Version < 1 {
		return fmt.Errorf("%s.version invalid", path)
	}
	if rem.TimeoutMs < 0 {
		return fmt.Errorf("%s.timeoutMs invalid", path)
	}
	// Same allowlist/scheme/userinfo/literal-IP policy as a2agateway.CreateRemote,
	// minus DNS: snapshot parsing must not emit network traffic for attacker-supplied
	// host names. Resolution-time SSRF runs again at dial with the caller's context.
	if err := egressguard.ValidateRemoteAllowlistSyntax(rem.EndpointURL, rem.AgentCardURL, rem.AllowedHosts); err != nil {
		return fmt.Errorf("%s remote policy: %w", path, err)
	}
	// Cross-tenant secret binding uses the run's owning workspace (same strength
	// as a2agateway.CreateRemote); an unknown workspace fails closed upstream.
	if err := egressguard.ValidateAuthSecretRef(workspaceID, rem.AuthSecretRef); err != nil {
		return fmt.Errorf("%s.authSecretRef: %w", path, err)
	}
	return nil
}

func canonicalUUID(v string) bool {
	if v == "" || v != strings.TrimSpace(v) {
		return false
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return false
	}
	return id.String() == v
}
