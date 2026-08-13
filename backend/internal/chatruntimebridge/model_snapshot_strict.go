package chatruntimebridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/strictjson"

	"github.com/google/uuid"
)

// Model snapshot complete raw schema: model_snapshot_schema.md
// Producer: application.marshalModelSnapshot.

var modelSnapshotRequired = []string{
	"id", "provider", "apiBase", "modelName",
	"options", "status", "lockVersion",
	"agenticCapabilities", "runtimeCapabilities",
}

var modelSnapshotAllowed = map[string]struct{}{
	"id": {}, "provider": {}, "apiBase": {}, "modelName": {},
	"options": {}, "credentialSecretId": {}, "lockVersion": {},
	"agenticCapabilities": {}, "runtimeCapabilities": {}, "status": {},
	"toolDisclosurePolicy": {},
}

// parseModelSnapshotStrict validates raw run.ModelSnapshot for Agentic initial
// construction against the complete producer schema. No TrimSpace success path
// on identity fields; no defaulting missing options/runtimeCapabilities to {}.
func parseModelSnapshotStrict(raw json.RawMessage, workspaceID string) (modelconfig.Config, error) {
	if len(raw) == 0 || !bytes.Equal(raw, bytes.TrimSpace(raw)) {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot raw invalid", ErrAgenticModelSnapshotRequired)
	}
	if string(raw) == "null" || string(raw) == "{}" {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot missing", ErrAgenticModelSnapshotRequired)
	}

	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot structure", ErrAgenticModelSnapshotRequired)
	}
	for k := range top {
		if _, ok := modelSnapshotAllowed[k]; !ok {
			return modelconfig.Config{}, fmt.Errorf("%w: model snapshot structure", ErrAgenticModelSnapshotRequired)
		}
	}
	for _, req := range modelSnapshotRequired {
		if _, err := strictjson.RequirePresentNonNull(top, req); err != nil {
			return modelconfig.Config{}, fmt.Errorf("%w: model snapshot structure", ErrAgenticModelSnapshotRequired)
		}
	}

	id, err := strictjson.DecodeStringExact(top["id"])
	if err != nil || id == "" || id != strings.TrimSpace(id) {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot id", ErrAgenticModelSnapshotRequired)
	}
	parsedUUID, err := uuid.Parse(id)
	if err != nil || parsedUUID.String() != id {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot id", ErrAgenticModelSnapshotRequired)
	}

	provider, err := strictjson.DecodeStringExact(top["provider"])
	if err != nil || provider == "" || provider != strings.TrimSpace(provider) {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot provider", ErrAgenticModelSnapshotRequired)
	}
	apiBase, err := strictjson.DecodeStringExact(top["apiBase"])
	if err != nil || apiBase == "" || apiBase != strings.TrimSpace(apiBase) {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot apiBase", ErrAgenticModelSnapshotRequired)
	}
	// Same construction-time policy the graph nodes already get: absolute http/https,
	// non-empty host, no userinfo/query/fragment/opaque form. Applied here so a
	// hostile base cannot reach assembly/manifest, model, agent, or sink.
	// Never echo the raw base (it may carry credentials in userinfo).
	if _, err := modelapi.ValidateAgenticAPIBase(apiBase); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot apiBase policy", ErrAgenticModelSnapshotRequired)
	}
	modelName, err := strictjson.DecodeStringExact(top["modelName"])
	if err != nil || modelName == "" || modelName != strings.TrimSpace(modelName) {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot modelName", ErrAgenticModelSnapshotRequired)
	}
	lock, err := strictjson.DecodeInt64Exact(top["lockVersion"])
	if err != nil || lock < 1 {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot lockVersion", ErrAgenticModelSnapshotRequired)
	}
	statusStr, err := strictjson.DecodeStringExact(top["status"])
	if err != nil || statusStr != strings.TrimSpace(statusStr) {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot status", ErrAgenticModelSnapshotRequired)
	}

	if err := strictjson.RequireObject(top["options"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot options", ErrAgenticModelSnapshotRequired)
	}
	// options is an open map of model knobs; recursive dups already rejected.
	// Re-parse as object map to ensure valid object shape only.
	if _, err := strictjson.DecodeObjectMap(top["options"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot options", ErrAgenticModelSnapshotRequired)
	}
	options := append(json.RawMessage(nil), bytes.TrimSpace(top["options"])...)

	if err := strictjson.RequireObject(top["runtimeCapabilities"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot runtimeCapabilities", ErrAgenticModelSnapshotRequired)
	}
	runtimeCaps := append(json.RawMessage(nil), bytes.TrimSpace(top["runtimeCapabilities"])...)
	// Empty object is legal "unset". Non-empty must pass ParseRuntimeCapabilities.
	if !strictjson.IsEmptyObject(runtimeCaps) {
		if _, _, err := modelconfig.ParseRuntimeCapabilities(runtimeCaps); err != nil {
			return modelconfig.Config{}, fmt.Errorf("%w: model snapshot runtimeCapabilities", ErrAgenticModelSnapshotRequired)
		}
	}

	agenticRaw := top["agenticCapabilities"]
	if err := strictjson.RequireObject(agenticRaw); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot agenticCapabilities", ErrAgenticModelSnapshotRequired)
	}
	_, agenticNormalized, err := modelconfig.ParseAgenticCapabilities(agenticRaw)
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot agenticCapabilities", ErrAgenticModelSnapshotRequired)
	}

	// credentialSecretId: optional absence; if present must be non-null non-empty exact string.
	var cred *string
	if v, ok := top["credentialSecretId"]; ok {
		if strictjson.IsNull(v) {
			// Producer omits key when unset; explicit null is not used for run.ModelSnapshot.
			return modelconfig.Config{}, fmt.Errorf("%w: model snapshot credentialSecretId", ErrAgenticModelSnapshotRequired)
		}
		s, err := strictjson.DecodeStringExact(v)
		if err != nil || s == "" || s != strings.TrimSpace(s) {
			return modelconfig.Config{}, fmt.Errorf("%w: model snapshot credentialSecretId", ErrAgenticModelSnapshotRequired)
		}
		cred = &s
	}

	policyRaw := json.RawMessage(`{}`)
	if v, ok := top["toolDisclosurePolicy"]; ok {
		if _, normalized, err := modelconfig.ParseToolDisclosurePolicy(v); err != nil {
			return modelconfig.Config{}, fmt.Errorf("%w: model snapshot toolDisclosurePolicy", ErrAgenticModelSnapshotRequired)
		} else {
			policyRaw = normalized
		}
	}

	cfg := modelconfig.Config{
		ID:                   id,
		WorkspaceID:          strings.TrimSpace(workspaceID),
		Provider:             provider,
		APIBase:              apiBase,
		ModelName:            modelName,
		Options:              options,
		CredentialSecretID:   cred,
		LockVersion:          lock,
		Status:               modelconfig.Status(statusStr),
		AgenticCapabilities:  agenticNormalized,
		RuntimeCapabilities:  runtimeCaps,
		ToolDisclosurePolicy: policyRaw,
	}
	if cfg.Status == "" {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot status", ErrAgenticModelSnapshotRequired)
	}
	return cfg, nil
}

// parseNodeModelSnapshotStrict validates a graph node model blob. The key set
// is closed and credentialSecretId is always present (JSON null when unbound).
func parseNodeModelSnapshotStrict(
	raw json.RawMessage,
	workspaceID, modelConfigID string,
	nodeLockVer int64,
) (modelconfig.Config, error) {
	if err := agentdelegation.ValidateNodeModelSnapshot(raw, "modelSnapshot", modelConfigID, nodeLockVer); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: %v", ErrAgenticModelSnapshotRequired, err)
	}
	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot structure", ErrAgenticModelSnapshotRequired)
	}
	id, err := strictjson.DecodeStringExact(top["id"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot id", ErrAgenticModelSnapshotRequired)
	}
	provider, err := strictjson.DecodeStringExact(top["provider"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot provider", ErrAgenticModelSnapshotRequired)
	}
	apiBase, err := strictjson.DecodeStringExact(top["apiBase"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot apiBase", ErrAgenticModelSnapshotRequired)
	}
	if _, err := modelapi.ValidateAgenticAPIBase(apiBase); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot apiBase policy", ErrAgenticModelSnapshotRequired)
	}
	modelName, err := strictjson.DecodeStringExact(top["modelName"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot modelName", ErrAgenticModelSnapshotRequired)
	}
	lock, err := strictjson.DecodeInt64Exact(top["lockVersion"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot lockVersion", ErrAgenticModelSnapshotRequired)
	}
	statusStr, err := strictjson.DecodeStringExact(top["status"])
	if err != nil || statusStr != strings.TrimSpace(statusStr) || statusStr == "" {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot status", ErrAgenticModelSnapshotRequired)
	}
	if err := strictjson.RequireObject(top["options"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot options", ErrAgenticModelSnapshotRequired)
	}
	options := append(json.RawMessage(nil), bytes.TrimSpace(top["options"])...)
	if err := strictjson.RequireObject(top["runtimeCapabilities"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot runtimeCapabilities", ErrAgenticModelSnapshotRequired)
	}
	runtimeCaps := append(json.RawMessage(nil), bytes.TrimSpace(top["runtimeCapabilities"])...)
	if !strictjson.IsEmptyObject(runtimeCaps) {
		if _, _, err := modelconfig.ParseRuntimeCapabilities(runtimeCaps); err != nil {
			return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot runtimeCapabilities", ErrAgenticModelSnapshotRequired)
		}
	}
	if err := strictjson.RequireObject(top["agenticCapabilities"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot agenticCapabilities", ErrAgenticModelSnapshotRequired)
	}
	_, agenticNormalized, err := modelconfig.ParseAgenticCapabilities(top["agenticCapabilities"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot agenticCapabilities", ErrAgenticModelSnapshotRequired)
	}
	if err := strictjson.RequireObject(top["toolDisclosurePolicy"]); err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot toolDisclosurePolicy", ErrAgenticModelSnapshotRequired)
	}
	_, policyRaw, err := modelconfig.ParseToolDisclosurePolicy(top["toolDisclosurePolicy"])
	if err != nil {
		return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot toolDisclosurePolicy", ErrAgenticModelSnapshotRequired)
	}

	var cred *string
	credRaw := top["credentialSecretId"]
	if !strictjson.IsNull(credRaw) {
		s, err := strictjson.DecodeStringExact(credRaw)
		if err != nil || s == "" || s != strings.TrimSpace(s) {
			return modelconfig.Config{}, fmt.Errorf("%w: node model snapshot credentialSecretId", ErrAgenticModelSnapshotRequired)
		}
		cred = &s
	}

	return modelconfig.Config{
		ID:                   id,
		WorkspaceID:          strings.TrimSpace(workspaceID),
		Provider:             provider,
		APIBase:              apiBase,
		ModelName:            modelName,
		Options:              options,
		CredentialSecretID:   cred,
		LockVersion:          lock,
		Status:               modelconfig.Status(statusStr),
		AgenticCapabilities:  agenticNormalized,
		RuntimeCapabilities:  runtimeCaps,
		ToolDisclosurePolicy: policyRaw,
	}, nil
}
