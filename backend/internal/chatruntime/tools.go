package chatruntime

import (
	"encoding/json"
	"fmt"
	"strings"
)

const capabilitySnapshotSchema = "capability-snapshot.v1"

// SnapshotCapability is one immutable capability release pinned on an AgentRun.
type SnapshotCapability struct {
	CapabilityID         string          `json:"capabilityId"`
	ReleaseID            string          `json:"releaseId"`
	Kind                 string          `json:"kind"`
	CallableName         string          `json:"callableName"`
	CallableDescription  string          `json:"callableDescription"`
	InputSchema          json.RawMessage `json:"inputSchema"`
	OutputSchema         json.RawMessage `json:"outputSchema"`
	RiskLevel            string          `json:"riskLevel"`
	SideEffectLevel      string          `json:"sideEffectLevel"`
	RequiresConfirmation bool            `json:"requiresConfirmation"`
	ConnectionID         string          `json:"connectionId,omitempty"`
}

type capabilitySnapshotEnvelope struct {
	SchemaVersion string               `json:"schemaVersion"`
	Releases      []SnapshotCapability `json:"releases"`
}

// ParseCapabilitySnapshot decodes the run-pinned capability snapshot.
// Missing optional fields are tolerated; runtime never re-resolves FOLLOW_ACTIVE.
func ParseCapabilitySnapshot(raw json.RawMessage) ([]SnapshotCapability, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var envelope capabilitySnapshotEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode capability snapshot: %w", err)
	}
	// SchemaVersion may be empty or newer; accept any envelope that still carries
	// releases (forward-compatible). capabilitySnapshotSchema documents the current name.
	_ = capabilitySnapshotSchema
	result := make([]SnapshotCapability, 0, len(envelope.Releases))
	seenNames := map[string]struct{}{}
	for _, item := range envelope.Releases {
		item.CapabilityID = strings.TrimSpace(item.CapabilityID)
		item.ReleaseID = strings.TrimSpace(item.ReleaseID)
		item.Kind = strings.ToUpper(strings.TrimSpace(item.Kind))
		item.CallableName = strings.TrimSpace(item.CallableName)
		item.CallableDescription = strings.TrimSpace(item.CallableDescription)
		item.RiskLevel = strings.ToUpper(strings.TrimSpace(item.RiskLevel))
		item.SideEffectLevel = strings.ToUpper(strings.TrimSpace(item.SideEffectLevel))
		item.ConnectionID = strings.TrimSpace(item.ConnectionID)
		if item.CapabilityID == "" || item.ReleaseID == "" || item.CallableName == "" {
			continue
		}
		if item.Kind == "" {
			item.Kind = "TOOL"
		}
		if item.SideEffectLevel == "" {
			item.SideEffectLevel = "NONE"
		}
		if len(item.InputSchema) == 0 {
			item.InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		key := strings.ToLower(item.CallableName)
		if _, exists := seenNames[key]; exists {
			continue
		}
		seenNames[key] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}
