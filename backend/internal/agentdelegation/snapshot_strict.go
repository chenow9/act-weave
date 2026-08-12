package agentdelegation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/strictjson"
)

// Complete raw schema for freeze-only agent_graph_snapshot.v1 is documented in
// snapshot_schema.md. parseSnapshotStrict enforces it before json.Unmarshal.
//
// Nested node snapshots match chatruntimebridge.snapshotAgentNode /
// capabilitySnapshotJSON producers (not the root run.ModelSnapshot schema).

const (
	nodeAgentBindingSchemaV1       = "agent-binding.v1"
	nodeCapabilitySnapshotSchemaV1 = "capability-snapshot.v1"
)

var graphRootRequired = []string{
	"schemaVersion", "rootAgentId",
	"maxDepth", "maxTotalDelegations", "maxPerBinding",
	"nodes", "edges", "builtAt",
	"frozenRemotesByCaller", "remotesFrozen",
}

var graphRootAllowed = map[string]struct{}{
	"schemaVersion": {}, "rootAgentId": {},
	"maxDepth": {}, "maxTotalDelegations": {}, "maxPerBinding": {},
	"nodes": {}, "edges": {}, "builtAt": {},
	"frozenRemotesByCaller": {}, "remotesFrozen": {},
	// extra is never emitted by freezeGraphSnapshot / SnapshotJSON — rejected.
}

var graphNodeRequired = []string{
	"agentId", "depth", "modelConfigId", "modelConfigLockVersion",
	"modelSnapshot", "agentSnapshot", "capabilitySnapshot",
}

var graphNodeAllowed = map[string]struct{}{
	"agentId": {}, "name": {}, "depth": {},
	"promptRevisionId": {}, "promptRevisionHash": {},
	"modelConfigId": {}, "modelConfigLockVersion": {},
	"modelSnapshot": {}, "agentSnapshot": {}, "capabilitySnapshot": {},
}

// Node model snapshot (snapshotAgentNode) — distinct from root agentic-model.v1.
var nodeModelRequired = []string{
	"id", "provider", "apiBase", "modelName", "options", "credentialSecretId", "lockVersion",
}

var nodeModelAllowed = map[string]struct{}{
	"id": {}, "provider": {}, "apiBase": {}, "modelName": {},
	"options": {}, "credentialSecretId": {}, "lockVersion": {},
}

// Node agent-binding.v1 (snapshotAgentNode).
var nodeAgentRequired = []string{
	"schemaVersion", "agentId", "name", "roleDescription",
	"promptRevisionId", "promptRevisionHash",
	"modelConfigId", "modelConfigLockVer",
}

var nodeAgentAllowed = map[string]struct{}{
	"schemaVersion": {}, "agentId": {}, "name": {}, "roleDescription": {},
	"promptRevisionId": {}, "promptRevisionHash": {},
	"modelConfigId": {}, "modelConfigLockVer": {},
}

// Node capability-snapshot.v1.
var nodeCapRequired = []string{"schemaVersion", "releases"}

var nodeCapAllowed = map[string]struct{}{
	"schemaVersion": {}, "releases": {},
}

var nodeCapReleaseRequired = []string{
	"capabilityId", "releaseId", "kind", "callableName", "callableDescription",
	"inputSchema", "outputSchema", "riskLevel", "sideEffectLevel", "requiresConfirmation",
}

var nodeCapReleaseAllowed = map[string]struct{}{
	"capabilityId": {}, "releaseId": {}, "kind": {},
	"callableName": {}, "callableDescription": {},
	"inputSchema": {}, "outputSchema": {},
	"riskLevel": {}, "sideEffectLevel": {},
	"requiresConfirmation": {}, "connectionId": {},
}

var graphEdgeRequired = []string{
	"bindingId", "callerAgentId", "targetAgentId", "callableName",
	"mode", "contextPolicy", "version", "protocol",
}

var graphEdgeAllowed = map[string]struct{}{
	"bindingId": {}, "callerAgentId": {}, "targetAgentId": {},
	"callableName": {}, "description": {}, "mode": {},
	"contextPolicy": {}, "version": {}, "protocol": {}, "externalRef": {},
}

// Closed edge enums for *internal* frozen edges (BindingsToEdges / freeze).
// A2A is remote-map only; SUMMARY/SELECTED_MESSAGES are not write-path legal.
var edgeProtocolAllowed = map[string]struct{}{
	ProtocolInternal: {},
}

var edgeModeAllowed = map[string]struct{}{
	ModeInline: {},
	ModeTask:   {},
}

var edgeContextPolicyAllowed = map[string]struct{}{
	ContextTaskOnly: {},
}

var graphRemoteRequired = []string{
	"id", "callerAgentId", "callableName", "endpointUrl",
	"allowedHosts", "timeoutMs", "version",
}

var graphRemoteAllowed = map[string]struct{}{
	"id": {}, "callerAgentId": {}, "callableName": {},
	"description": {}, "endpointUrl": {}, "agentCardUrl": {},
	"allowedHosts": {}, "authSecretRef": {}, "timeoutMs": {}, "version": {},
}

// parseSnapshotStrict validates raw agent_graph_snapshot.v1 for freeze-only use.
// Explicit empty arrays/objects are legal; missing/null required containers are not.
// Unknown fields, missing producer budgets, and foreign remotes map keys fail closed.
func parseSnapshotStrict(workspaceID string, raw json.RawMessage) (*GraphSnapshotV1, error) {
	if len(raw) == 0 || !bytes.Equal(raw, bytes.TrimSpace(raw)) {
		return nil, fmt.Errorf("agent_graph_snapshot raw invalid")
	}
	if string(raw) == "null" || string(raw) == "{}" {
		return nil, nil
	}

	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot structure: %w", err)
	}
	for k := range top {
		if _, ok := graphRootAllowed[k]; !ok {
			return nil, fmt.Errorf("agent_graph_snapshot unknown field %q", k)
		}
	}
	for _, req := range graphRootRequired {
		if _, err := strictjson.RequirePresentNonNull(top, req); err != nil {
			return nil, fmt.Errorf("agent_graph_snapshot: %w", err)
		}
	}

	schema, err := strictjson.DecodeStringExact(top["schemaVersion"])
	if err != nil || schema != GraphSnapshotSchemaV1 {
		return nil, fmt.Errorf("unsupported graph snapshot schema")
	}
	root, err := decodeNonemptyExactString(top["rootAgentId"])
	if err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot rootAgentId invalid")
	}

	maxDepth, err := strictjson.DecodeInt64Exact(top["maxDepth"])
	if err != nil || maxDepth < 1 {
		return nil, fmt.Errorf("agent_graph_snapshot maxDepth invalid")
	}
	maxTotal, err := strictjson.DecodeInt64Exact(top["maxTotalDelegations"])
	if err != nil || maxTotal < 1 {
		return nil, fmt.Errorf("agent_graph_snapshot maxTotalDelegations invalid")
	}
	maxPer, err := strictjson.DecodeInt64Exact(top["maxPerBinding"])
	if err != nil || maxPer < 1 {
		return nil, fmt.Errorf("agent_graph_snapshot maxPerBinding invalid")
	}
	if err := strictjson.RequireArray(top["nodes"]); err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot nodes: %w", err)
	}
	if err := strictjson.RequireArray(top["edges"]); err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot edges: %w", err)
	}
	if err := strictjson.RequireObject(top["frozenRemotesByCaller"]); err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller: %w", err)
	}
	remotesFrozen, err := strictjson.DecodeBoolExact(top["remotesFrozen"])
	if err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot remotesFrozen invalid")
	}
	if !remotesFrozen {
		// Freeze-only contract: remotes must be frozen for production freezes.
		return nil, fmt.Errorf("agent_graph_snapshot remotesFrozen must be true")
	}
	builtAtStr, err := strictjson.DecodeStringExact(top["builtAt"])
	if err != nil || builtAtStr == "" {
		return nil, fmt.Errorf("agent_graph_snapshot builtAt invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, builtAtStr); err != nil {
		if _, err2 := time.Parse(time.RFC3339, builtAtStr); err2 != nil {
			return nil, fmt.Errorf("agent_graph_snapshot builtAt invalid")
		}
	}

	nodeIDs, err := validateNodesRawComplete(top["nodes"])
	if err != nil {
		return nil, err
	}
	if _, ok := nodeIDs[root]; !ok {
		return nil, fmt.Errorf("agent_graph_snapshot rootAgentId not in nodes")
	}
	if err := validateEdgesRawComplete(top["edges"], nodeIDs); err != nil {
		return nil, err
	}
	if err := validateRemotesMapComplete(top["frozenRemotesByCaller"], nodeIDs); err != nil {
		return nil, err
	}

	var snap GraphSnapshotV1
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&snap); err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot decode: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("agent_graph_snapshot trailing data")
	}

	// Explicit empty slices from raw [] (Unmarshal may leave nil).
	if snap.Edges == nil {
		snap.Edges = []GraphEdgeSnapshot{}
	}
	if snap.Nodes == nil {
		snap.Nodes = []GraphNodeSnapshot{}
	}
	if snap.FrozenRemotesByCaller == nil {
		snap.FrozenRemotesByCaller = map[string][]FrozenRemoteBinding{}
	}
	for id := range nodeIDs {
		if snap.FrozenRemotesByCaller[id] == nil {
			snap.FrozenRemotesByCaller[id] = []FrozenRemoteBinding{}
		}
	}

	snap.SchemaVersion = GraphSnapshotSchemaV1
	snap.RootAgentID = root
	snap.MaxDepth = int(maxDepth)
	snap.MaxTotal = int(maxTotal)
	snap.MaxPerBinding = int(maxPer)
	snap.RemotesFrozen = true

	if err := ValidateGraphSnapshotIntegrity(&snap); err != nil {
		return nil, err
	}
	// Domain-semantic closure (topology, enums, remote SSRF, canonical IDs).
	// Must run before ParseSnapshot returns success so Agentic initial never
	// classifies a hostile graph as migration-pending.
	if err := validateGraphSnapshotSemantics(workspaceID, &snap); err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot semantic: %w", err)
	}
	return &snap, nil
}

func decodeNonemptyExactString(raw json.RawMessage) (string, error) {
	s, err := strictjson.DecodeStringExact(raw)
	if err != nil {
		return "", err
	}
	if s == "" || s != strings.TrimSpace(s) {
		return "", fmt.Errorf("non-canonical string")
	}
	return s, nil
}

func decodeExactStringAllowEmpty(raw json.RawMessage) (string, error) {
	s, err := strictjson.DecodeStringExact(raw)
	if err != nil {
		return "", err
	}
	if s != strings.TrimSpace(s) {
		return "", fmt.Errorf("non-canonical string")
	}
	return s, nil
}

func validateClosedObject(top map[string]json.RawMessage, allowed map[string]struct{}, required []string, path string) error {
	for k := range top {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("%s unknown field %q", path, k)
		}
	}
	for _, req := range required {
		if _, err := strictjson.RequirePresentNonNull(top, req); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

func validateNodesRawComplete(raw json.RawMessage) (map[string]struct{}, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("agent_graph_snapshot nodes: %w", err)
	}
	ids := make(map[string]struct{}, len(elems))
	for i, el := range elems {
		path := fmt.Sprintf("nodes[%d]", i)
		if strictjson.IsNull(el) {
			return nil, fmt.Errorf("agent_graph_snapshot %s must not be null", path)
		}
		node, err := strictjson.DecodeObjectMap(el)
		if err != nil {
			return nil, fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
		}
		if err := validateClosedObject(node, graphNodeAllowed, graphNodeRequired, "agent_graph_snapshot "+path); err != nil {
			return nil, err
		}
		agentID, err := decodeNonemptyExactString(node["agentId"])
		if err != nil {
			return nil, fmt.Errorf("agent_graph_snapshot %s.agentId invalid", path)
		}
		if _, dup := ids[agentID]; dup {
			return nil, fmt.Errorf("agent_graph_snapshot duplicate node agentId")
		}
		ids[agentID] = struct{}{}

		modelConfigID, err := decodeNonemptyExactString(node["modelConfigId"])
		if err != nil {
			return nil, fmt.Errorf("agent_graph_snapshot %s.modelConfigId invalid", path)
		}
		depth, err := strictjson.DecodeInt64Exact(node["depth"])
		if err != nil || depth < 0 {
			return nil, fmt.Errorf("agent_graph_snapshot %s.depth invalid", path)
		}
		// modelConfigLockVersion is producer-required (snapshotAgentNode always sets it).
		// No sentinel/default: missing/null/wrong type already fail via graphNodeRequired
		// + DecodeInt64Exact; domain requires lockVersion >= 1 (same as nested model lock).
		lockVer, err := strictjson.DecodeInt64Exact(node["modelConfigLockVersion"])
		if err != nil || lockVer < 1 {
			return nil, fmt.Errorf("agent_graph_snapshot %s.modelConfigLockVersion invalid", path)
		}
		// Optional exact kinds
		if v, ok := node["name"]; ok {
			if _, err := decodeExactStringAllowEmpty(v); err != nil {
				return nil, fmt.Errorf("agent_graph_snapshot %s.name invalid", path)
			}
		}
		if v, ok := node["promptRevisionId"]; ok {
			if _, err := decodeExactStringAllowEmpty(v); err != nil {
				return nil, fmt.Errorf("agent_graph_snapshot %s.promptRevisionId invalid", path)
			}
		}
		if v, ok := node["promptRevisionHash"]; ok {
			if _, err := decodeExactStringAllowEmpty(v); err != nil {
				return nil, fmt.Errorf("agent_graph_snapshot %s.promptRevisionHash invalid", path)
			}
		}

		if err := validateNodeModelSnapshot(node["modelSnapshot"], path+".modelSnapshot", modelConfigID, lockVer); err != nil {
			return nil, err
		}
		if err := validateNodeAgentSnapshot(node["agentSnapshot"], path+".agentSnapshot", agentID, modelConfigID, lockVer); err != nil {
			return nil, err
		}
		if err := validateNodeCapabilitySnapshot(node["capabilitySnapshot"], path+".capabilitySnapshot"); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// validateNodeModelSnapshot enforces snapshotAgentNode model shape.
// credentialSecretId is required and deliberately nullable (null when unset).
func validateNodeModelSnapshot(raw json.RawMessage, path, modelConfigID string, nodeLockVer int64) error {
	if err := strictjson.RequireObject(raw); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
	}
	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
	}
	for k := range top {
		if _, ok := nodeModelAllowed[k]; !ok {
			return fmt.Errorf("agent_graph_snapshot %s unknown field %q", path, k)
		}
	}
	// credentialSecretId is required as a key but may be JSON null.
	for _, req := range nodeModelRequired {
		if req == "credentialSecretId" {
			if _, ok := top[req]; !ok {
				return fmt.Errorf("agent_graph_snapshot %s: missing field %q", path, req)
			}
			continue
		}
		if _, err := strictjson.RequirePresentNonNull(top, req); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
		}
	}

	id, err := decodeNonemptyExactString(top["id"])
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.id invalid", path)
	}
	if id != modelConfigID {
		return fmt.Errorf("agent_graph_snapshot %s.id must equal node modelConfigId", path)
	}
	if _, err := decodeNonemptyExactString(top["provider"]); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.provider invalid", path)
	}
	// apiBase: exact non-empty string at structural layer; format via modelapi in semantic.
	if _, err := decodeNonemptyExactString(top["apiBase"]); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.apiBase invalid", path)
	}
	if _, err := decodeNonemptyExactString(top["modelName"]); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.modelName invalid", path)
	}
	if err := strictjson.RequireObject(top["options"]); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.options: %w", path, err)
	}
	if _, err := strictjson.DecodeObjectMap(top["options"]); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.options: %w", path, err)
	}
	// credentialSecretId: null OR non-empty exact string (producer always emits key).
	credRaw := top["credentialSecretId"]
	if !strictjson.IsNull(credRaw) {
		if _, err := decodeNonemptyExactString(credRaw); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.credentialSecretId invalid", path)
		}
	}
	lock, err := strictjson.DecodeInt64Exact(top["lockVersion"])
	if err != nil || lock < 1 {
		return fmt.Errorf("agent_graph_snapshot %s.lockVersion invalid", path)
	}
	// Unconditional identity: nested model lock must equal required node lock.
	if lock != nodeLockVer {
		return fmt.Errorf("agent_graph_snapshot %s.lockVersion must equal node modelConfigLockVersion", path)
	}
	return nil
}

func validateNodeAgentSnapshot(raw json.RawMessage, path, agentID, modelConfigID string, nodeLockVer int64) error {
	if err := strictjson.RequireObject(raw); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
	}
	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
	}
	if err := validateClosedObject(top, nodeAgentAllowed, nodeAgentRequired, "agent_graph_snapshot "+path); err != nil {
		return err
	}
	sv, err := strictjson.DecodeStringExact(top["schemaVersion"])
	if err != nil || sv != nodeAgentBindingSchemaV1 {
		return fmt.Errorf("agent_graph_snapshot %s.schemaVersion must be %s", path, nodeAgentBindingSchemaV1)
	}
	aid, err := decodeNonemptyExactString(top["agentId"])
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.agentId invalid", path)
	}
	if aid != agentID {
		return fmt.Errorf("agent_graph_snapshot %s.agentId must equal node agentId", path)
	}
	// name / roleDescription / prompt* may be empty strings (producer always emits).
	for _, f := range []string{"name", "roleDescription", "promptRevisionId", "promptRevisionHash"} {
		if _, err := decodeExactStringAllowEmpty(top[f]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.%s invalid", path, f)
		}
	}
	mid, err := decodeNonemptyExactString(top["modelConfigId"])
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.modelConfigId invalid", path)
	}
	if mid != modelConfigID {
		return fmt.Errorf("agent_graph_snapshot %s.modelConfigId must equal node modelConfigId", path)
	}
	lock, err := strictjson.DecodeInt64Exact(top["modelConfigLockVer"])
	if err != nil || lock < 1 {
		return fmt.Errorf("agent_graph_snapshot %s.modelConfigLockVer invalid", path)
	}
	// Unconditional identity: agent binding lock must equal required node lock.
	if lock != nodeLockVer {
		return fmt.Errorf("agent_graph_snapshot %s.modelConfigLockVer must equal node modelConfigLockVersion", path)
	}
	return nil
}

func validateNodeCapabilitySnapshot(raw json.RawMessage, path string) error {
	if err := strictjson.RequireObject(raw); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
	}
	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
	}
	if err := validateClosedObject(top, nodeCapAllowed, nodeCapRequired, "agent_graph_snapshot "+path); err != nil {
		return err
	}
	sv, err := strictjson.DecodeStringExact(top["schemaVersion"])
	if err != nil || sv != nodeCapabilitySnapshotSchemaV1 {
		return fmt.Errorf("agent_graph_snapshot %s.schemaVersion must be %s", path, nodeCapabilitySnapshotSchemaV1)
	}
	if err := strictjson.RequireArray(top["releases"]); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.releases: %w", path, err)
	}
	var releases []json.RawMessage
	if err := json.Unmarshal(top["releases"], &releases); err != nil {
		return fmt.Errorf("agent_graph_snapshot %s.releases: %w", path, err)
	}
	seenCallable := map[string]struct{}{}
	for i, rel := range releases {
		rpath := fmt.Sprintf("%s.releases[%d]", path, i)
		if strictjson.IsNull(rel) {
			return fmt.Errorf("agent_graph_snapshot %s must not be null", rpath)
		}
		obj, err := strictjson.DecodeObjectMap(rel)
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s: %w", rpath, err)
		}
		if err := validateClosedObject(obj, nodeCapReleaseAllowed, nodeCapReleaseRequired, "agent_graph_snapshot "+rpath); err != nil {
			return err
		}
		for _, f := range []string{"capabilityId", "releaseId", "kind", "callableName", "riskLevel", "sideEffectLevel"} {
			if _, err := decodeNonemptyExactString(obj[f]); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.%s invalid", rpath, f)
			}
		}
		if _, err := decodeExactStringAllowEmpty(obj["callableDescription"]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.callableDescription invalid", rpath)
		}
		// Schemas must be objects (producer embeds json.RawMessage schemas).
		if err := strictjson.RequireObject(obj["inputSchema"]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.inputSchema: %w", rpath, err)
		}
		if _, err := strictjson.DecodeObjectMap(obj["inputSchema"]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.inputSchema: %w", rpath, err)
		}
		if err := strictjson.RequireObject(obj["outputSchema"]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.outputSchema: %w", rpath, err)
		}
		if _, err := strictjson.DecodeObjectMap(obj["outputSchema"]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.outputSchema: %w", rpath, err)
		}
		if _, err := strictjson.DecodeBoolExact(obj["requiresConfirmation"]); err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.requiresConfirmation invalid", rpath)
		}
		if v, ok := obj["connectionId"]; ok {
			if _, err := decodeNonemptyExactString(v); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.connectionId invalid", rpath)
			}
		}
		cn, _ := strictjson.DecodeStringExact(obj["callableName"])
		ck := strings.ToLower(cn)
		if _, dup := seenCallable[ck]; dup {
			return fmt.Errorf("agent_graph_snapshot %s duplicate callableName", path)
		}
		seenCallable[ck] = struct{}{}
	}
	return nil
}

func validateEdgesRawComplete(raw json.RawMessage, nodeIDs map[string]struct{}) error {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return fmt.Errorf("agent_graph_snapshot edges: %w", err)
	}
	bindingSeen := map[string]struct{}{}
	callableSeen := map[string]struct{}{} // lower(caller)|lower(callable)
	for i, el := range elems {
		path := fmt.Sprintf("edges[%d]", i)
		if strictjson.IsNull(el) {
			return fmt.Errorf("agent_graph_snapshot %s must not be null", path)
		}
		edge, err := strictjson.DecodeObjectMap(el)
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
		}
		if len(edge) == 0 {
			return fmt.Errorf("agent_graph_snapshot %s must not be empty object", path)
		}
		if err := validateClosedObject(edge, graphEdgeAllowed, graphEdgeRequired, "agent_graph_snapshot "+path); err != nil {
			return err
		}
		caller, err := decodeNonemptyExactString(edge["callerAgentId"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.callerAgentId invalid", path)
		}
		target, err := decodeNonemptyExactString(edge["targetAgentId"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.targetAgentId invalid", path)
		}
		if _, ok := nodeIDs[caller]; !ok {
			return fmt.Errorf("agent_graph_snapshot %s.callerAgentId not in nodes", path)
		}
		if _, ok := nodeIDs[target]; !ok {
			return fmt.Errorf("agent_graph_snapshot %s.targetAgentId not in nodes", path)
		}
		bid, err := decodeNonemptyExactString(edge["bindingId"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.bindingId invalid", path)
		}
		if _, dup := bindingSeen[bid]; dup {
			return fmt.Errorf("agent_graph_snapshot duplicate edge bindingId %s", bid)
		}
		bindingSeen[bid] = struct{}{}

		cname, err := decodeNonemptyExactString(edge["callableName"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.callableName invalid", path)
		}
		ck := strings.ToLower(caller) + "|" + strings.ToLower(cname)
		if _, dup := callableSeen[ck]; dup {
			return fmt.Errorf("agent_graph_snapshot duplicate edge callable %s for caller %s", cname, caller)
		}
		callableSeen[ck] = struct{}{}

		mode, err := decodeNonemptyExactString(edge["mode"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.mode invalid", path)
		}
		if _, ok := edgeModeAllowed[mode]; !ok {
			return fmt.Errorf("agent_graph_snapshot %s.mode %q not in closed enum", path, mode)
		}
		policy, err := decodeNonemptyExactString(edge["contextPolicy"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.contextPolicy invalid", path)
		}
		if _, ok := edgeContextPolicyAllowed[policy]; !ok {
			return fmt.Errorf("agent_graph_snapshot %s.contextPolicy %q not in closed enum", path, policy)
		}
		proto, err := decodeNonemptyExactString(edge["protocol"])
		if err != nil {
			return fmt.Errorf("agent_graph_snapshot %s.protocol invalid", path)
		}
		if _, ok := edgeProtocolAllowed[proto]; !ok {
			return fmt.Errorf("agent_graph_snapshot %s.protocol %q not in closed enum", path, proto)
		}
		if ver, err := strictjson.DecodeInt64Exact(edge["version"]); err != nil || ver < 1 {
			return fmt.Errorf("agent_graph_snapshot %s.version invalid", path)
		}
		if v, ok := edge["description"]; ok {
			if _, err := decodeExactStringAllowEmpty(v); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.description invalid", path)
			}
		}
		if v, ok := edge["externalRef"]; ok {
			if _, err := decodeExactStringAllowEmpty(v); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.externalRef invalid", path)
			}
		}
	}
	return nil
}

func validateRemotesMapComplete(raw json.RawMessage, nodeIDs map[string]struct{}) error {
	remoteMap, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller: %w", err)
	}
	// Keys must be exactly the node set (no foreign, no missing).
	if len(remoteMap) != len(nodeIDs) {
		return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller key set mismatch")
	}
	for caller, listRaw := range remoteMap {
		if caller == "" || caller != strings.TrimSpace(caller) {
			return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller key invalid")
		}
		if _, ok := nodeIDs[caller]; !ok {
			return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller foreign caller")
		}
		if err := strictjson.RequireArray(listRaw); err != nil {
			return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller[%s]: %w", caller, err)
		}
		var elems []json.RawMessage
		if err := json.Unmarshal(listRaw, &elems); err != nil {
			return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller[%s]: %w", caller, err)
		}
		idSeen := map[string]struct{}{}
		callableSeen := map[string]struct{}{}
		for i, el := range elems {
			path := fmt.Sprintf("frozenRemotesByCaller[%s][%d]", caller, i)
			if strictjson.IsNull(el) {
				return fmt.Errorf("agent_graph_snapshot %s must not be null", path)
			}
			rem, err := strictjson.DecodeObjectMap(el)
			if err != nil {
				return fmt.Errorf("agent_graph_snapshot %s: %w", path, err)
			}
			if len(rem) == 0 {
				return fmt.Errorf("agent_graph_snapshot %s must not be empty object", path)
			}
			if err := validateClosedObject(rem, graphRemoteAllowed, graphRemoteRequired, "agent_graph_snapshot "+path); err != nil {
				return err
			}
			callerField, err := decodeNonemptyExactString(rem["callerAgentId"])
			if err != nil || callerField != caller {
				return fmt.Errorf("agent_graph_snapshot %s.callerAgentId must equal map key", path)
			}
			rid, err := decodeNonemptyExactString(rem["id"])
			if err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.id invalid", path)
			}
			if _, dup := idSeen[rid]; dup {
				return fmt.Errorf("agent_graph_snapshot %s duplicate remote binding id %s", path, rid)
			}
			idSeen[rid] = struct{}{}

			cname, err := decodeNonemptyExactString(rem["callableName"])
			if err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.callableName invalid", path)
			}
			ck := strings.ToLower(cname)
			if _, dup := callableSeen[ck]; dup {
				return fmt.Errorf("agent_graph_snapshot %s duplicate remote callable %s for caller %s", path, cname, caller)
			}
			callableSeen[ck] = struct{}{}

			if _, err := decodeNonemptyExactString(rem["endpointUrl"]); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.endpointUrl invalid", path)
			}
			if err := strictjson.RequireArray(rem["allowedHosts"]); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.allowedHosts: %w", path, err)
			}
			var hosts []json.RawMessage
			if err := json.Unmarshal(rem["allowedHosts"], &hosts); err != nil {
				return fmt.Errorf("agent_graph_snapshot %s.allowedHosts: %w", path, err)
			}
			for j, h := range hosts {
				// Host entries must be non-empty exact strings (policy identity).
				if _, err := decodeNonemptyExactString(h); err != nil {
					return fmt.Errorf("agent_graph_snapshot %s.allowedHosts[%d] invalid", path, j)
				}
			}
			if n, err := strictjson.DecodeInt64Exact(rem["timeoutMs"]); err != nil || n < 0 {
				return fmt.Errorf("agent_graph_snapshot %s.timeoutMs invalid", path)
			}
			if n, err := strictjson.DecodeInt64Exact(rem["version"]); err != nil || n < 1 {
				return fmt.Errorf("agent_graph_snapshot %s.version invalid", path)
			}
			for _, opt := range []string{"description", "agentCardUrl", "authSecretRef"} {
				if v, ok := rem[opt]; ok {
					if _, err := decodeExactStringAllowEmpty(v); err != nil {
						return fmt.Errorf("agent_graph_snapshot %s.%s invalid", path, opt)
					}
				}
			}
		}
	}
	for id := range nodeIDs {
		if _, ok := remoteMap[id]; !ok {
			return fmt.Errorf("agent_graph_snapshot frozenRemotesByCaller missing node key")
		}
	}
	return nil
}
