package chatruntimebridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/strictjson"
)

// Run-level capability freeze (agent_runs.capability_snapshot).
//
// Producer: application.agentRunSnapshots.SnapshotAgentRun, which marshals
// {schemaVersion, releases:[...]} where each release is
// capability.Descriptor's ten always-present fields plus connectionId, emitted
// only when the agent binding pinned a connection.
//
// This validator is scoped to the Agentic initial path. chatruntime's lax
// ParseCapabilitySnapshot stays as-is for the classic/resume path; the point of
// this file is that the Agentic path never accepts an entry the producer could
// not have written, and never silently drops one either.
const runCapabilitySnapshotSchemaV1 = "capability-snapshot.v1"

var runCapEnvelopeRequired = []string{"schemaVersion", "releases"}

var runCapEnvelopeAllowed = map[string]struct{}{
	"schemaVersion": {}, "releases": {},
}

var runCapReleaseRequired = []string{
	"capabilityId", "releaseId", "kind", "callableName", "callableDescription",
	"inputSchema", "outputSchema", "riskLevel", "sideEffectLevel", "requiresConfirmation",
}

var runCapReleaseAllowed = map[string]struct{}{
	"capabilityId": {}, "releaseId": {}, "kind": {},
	"callableName": {}, "callableDescription": {},
	"inputSchema": {}, "outputSchema": {},
	"riskLevel": {}, "sideEffectLevel": {},
	"requiresConfirmation": {},
	// connectionId is written only when the binding pinned a connection.
	"connectionId": {},
}

// Closed enums, identical to capability/repository publish-time validation.
// Agentic initial only executes TOOL and WORKFLOW; AGENT / A2A are Task 5.
var (
	runCapKindAllowed       = map[string]struct{}{"TOOL": {}, "WORKFLOW": {}}
	runCapRiskAllowed       = map[string]struct{}{"LOW": {}, "MEDIUM": {}, "HIGH": {}, "CRITICAL": {}}
	runCapSideEffectAllowed = map[string]struct{}{"NONE": {}, "READ": {}, "WRITE": {}, "IRREVERSIBLE": {}}
)

// parseRunCapabilitySnapshotStrict validates run.CapabilitySnapshot and returns
// the releases in producer order.
//
// Fail closed on: missing / null / non-object envelope / duplicate JSON keys at
// any depth / unknown fields / wrong types / non-canonical UUIDs / values
// outside the closed enums / duplicate callableName. A malformed entry fails the
// whole run rather than being dropped, so a corrupted freeze can never assemble
// a different executable tool set than the one that was frozen.
func parseRunCapabilitySnapshotStrict(raw json.RawMessage) ([]chatruntime.SnapshotCapability, error) {
	if len(raw) == 0 || !bytes.Equal(raw, bytes.TrimSpace(raw)) {
		return nil, fmt.Errorf("%w: capability snapshot raw invalid", ErrAgenticCapabilitySnapshotRequired)
	}
	if string(raw) == "null" || string(raw) == "{}" {
		return nil, fmt.Errorf("%w: capability snapshot missing", ErrAgenticCapabilitySnapshotRequired)
	}

	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: capability snapshot structure", ErrAgenticCapabilitySnapshotRequired)
	}
	for k := range top {
		if _, ok := runCapEnvelopeAllowed[k]; !ok {
			return nil, fmt.Errorf("%w: capability snapshot structure", ErrAgenticCapabilitySnapshotRequired)
		}
	}
	for _, req := range runCapEnvelopeRequired {
		if _, err := strictjson.RequirePresentNonNull(top, req); err != nil {
			return nil, fmt.Errorf("%w: capability snapshot structure", ErrAgenticCapabilitySnapshotRequired)
		}
	}
	schema, err := strictjson.DecodeStringExact(top["schemaVersion"])
	if err != nil || schema != runCapabilitySnapshotSchemaV1 {
		return nil, fmt.Errorf("%w: capability snapshot schemaVersion", ErrAgenticCapabilitySnapshotRequired)
	}
	if err := strictjson.RequireArray(top["releases"]); err != nil {
		return nil, fmt.Errorf("%w: capability snapshot releases", ErrAgenticCapabilitySnapshotRequired)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(top["releases"], &elems); err != nil {
		return nil, fmt.Errorf("%w: capability snapshot releases", ErrAgenticCapabilitySnapshotRequired)
	}

	out := make([]chatruntime.SnapshotCapability, 0, len(elems))
	seenCallable := map[string]struct{}{}
	seenRelease := map[string]struct{}{}
	for i, el := range elems {
		item, err := parseRunCapabilityRelease(el, i)
		if err != nil {
			return nil, err
		}
		ck := strings.ToLower(item.CallableName)
		if _, dup := seenCallable[ck]; dup {
			return nil, fmt.Errorf(
				"%w: capability snapshot releases[%d] duplicate callableName",
				ErrAgenticCapabilitySnapshotRequired, i)
		}
		seenCallable[ck] = struct{}{}
		if _, dup := seenRelease[item.ReleaseID]; dup {
			return nil, fmt.Errorf(
				"%w: capability snapshot releases[%d] duplicate releaseId",
				ErrAgenticCapabilitySnapshotRequired, i)
		}
		seenRelease[item.ReleaseID] = struct{}{}
		out = append(out, item)
	}
	return out, nil
}

func parseRunCapabilityRelease(el json.RawMessage, idx int) (chatruntime.SnapshotCapability, error) {
	fail := func(field string) (chatruntime.SnapshotCapability, error) {
		return chatruntime.SnapshotCapability{}, fmt.Errorf(
			"%w: capability snapshot releases[%d].%s", ErrAgenticCapabilitySnapshotRequired, idx, field)
	}
	if strictjson.IsNull(el) {
		return fail("<null>")
	}
	obj, err := strictjson.DecodeObjectMap(el)
	if err != nil {
		return fail("<structure>")
	}
	for k := range obj {
		if _, ok := runCapReleaseAllowed[k]; !ok {
			return fail("<unknown field>")
		}
	}
	for _, req := range runCapReleaseRequired {
		if _, err := strictjson.RequirePresentNonNull(obj, req); err != nil {
			return fail(req)
		}
	}

	capabilityID, err := decodeCanonicalUUIDField(obj["capabilityId"])
	if err != nil {
		return fail("capabilityId")
	}
	releaseID, err := decodeCanonicalUUIDField(obj["releaseId"])
	if err != nil {
		return fail("releaseId")
	}
	kind, err := strictjson.DecodeStringExact(obj["kind"])
	if err != nil {
		return fail("kind")
	}
	if _, ok := runCapKindAllowed[kind]; !ok {
		return fail("kind")
	}
	callableName, err := strictjson.DecodeStringExact(obj["callableName"])
	if err != nil || callableName == "" || callableName != strings.TrimSpace(callableName) {
		return fail("callableName")
	}
	callableDescription, err := strictjson.DecodeStringExact(obj["callableDescription"])
	if err != nil {
		return fail("callableDescription")
	}
	riskLevel, err := strictjson.DecodeStringExact(obj["riskLevel"])
	if err != nil {
		return fail("riskLevel")
	}
	if _, ok := runCapRiskAllowed[riskLevel]; !ok {
		return fail("riskLevel")
	}
	sideEffect, err := strictjson.DecodeStringExact(obj["sideEffectLevel"])
	if err != nil {
		return fail("sideEffectLevel")
	}
	if _, ok := runCapSideEffectAllowed[sideEffect]; !ok {
		return fail("sideEffectLevel")
	}
	requiresConfirmation, err := strictjson.DecodeBoolExact(obj["requiresConfirmation"])
	if err != nil {
		return fail("requiresConfirmation")
	}
	// capability_releases.input_schema / output_schema are JSONB NOT NULL but the
	// column legitimately holds boolean JSON Schema (`true`/`false`) as well as
	// objects, so only null / absent / malformed are rejected here.
	inputSchema, err := decodeFrozenSchema(obj["inputSchema"])
	if err != nil {
		return fail("inputSchema")
	}
	outputSchema, err := decodeFrozenSchema(obj["outputSchema"])
	if err != nil {
		return fail("outputSchema")
	}

	connectionID := ""
	if v, ok := obj["connectionId"]; ok {
		connectionID, err = decodeCanonicalUUIDField(v)
		if err != nil {
			return fail("connectionId")
		}
	}

	return chatruntime.SnapshotCapability{
		CapabilityID:         capabilityID,
		ReleaseID:            releaseID,
		Kind:                 kind,
		CallableName:         callableName,
		CallableDescription:  callableDescription,
		InputSchema:          inputSchema,
		OutputSchema:         outputSchema,
		RiskLevel:            riskLevel,
		SideEffectLevel:      sideEffect,
		RequiresConfirmation: requiresConfirmation,
		ConnectionID:         connectionID,
	}, nil
}

func decodeFrozenSchema(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || strictjson.IsNull(trimmed) {
		return nil, fmt.Errorf("schema must be present and non-null")
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("schema is not valid JSON")
	}
	return append(json.RawMessage(nil), trimmed...), nil
}
