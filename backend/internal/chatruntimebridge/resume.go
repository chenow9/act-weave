package chatruntimebridge

import (
	"encoding/json"
	"errors"
	"strings"

	"actweave/backend/internal/execution"
)

// ErrAgenticResumeGenerationMismatch is returned when the recorded runtime
// generation is not one this build knows. Guessing a runtime for an
// unidentifiable checkpoint is what produces adk's opaque
// "no child agents leading to interrupted agent were found".
var ErrAgenticResumeGenerationMismatch = errors.New("AGENTIC_RESUME_GENERATION_MISMATCH")

// ErrAgenticResumeClassicOnFrozenRun is returned when the classic seam is asked
// to restore a run that already carries a frozen agent_graph_snapshot.
//
// Absent RuntimeGeneration still routes to classic (pre-Agentic confirmations
// carry no marker), but an Agentic-era pause written before generation stamping
// also carries no marker — and those runs are exactly the ones that freeze a
// graph. Refusing here turns that mis-route into a stable code instead of adk's
// opaque restore failure after the user has already approved.
var ErrAgenticResumeClassicOnFrozenRun = errors.New("AGENTIC_RESUME_CLASSIC_ON_FROZEN_RUN")

// EinoChatResumeSchemaVersion is the nested resume payload version (design §3.6.2).
const EinoChatResumeSchemaVersion = "eino-chat-resume.v1"

// InterruptKindToolConfirmation is the only v1 interrupt kind for agent tools.
const InterruptKindToolConfirmation = "tool_confirmation"

// Runtime generation that produced the checkpoint behind a confirmation.
//
// A checkpoint written by one runtime cannot be restored by the other: the
// classic runner persists schema.Message state while the Agentic runner persists
// *schema.AgenticMessage state, and adk resolves the interrupted agent out of
// whatever it restores. Handing an Agentic checkpoint to a classic agent fails
// deep inside adk with "no child agents leading to interrupted agent were
// found", which says nothing about the real cause. Both runtimes share one
// CheckPointStore (application.Open), so the ID alone cannot distinguish them —
// the runtime that paused stamps itself here and resume routes on that.
const (
	RuntimeGenerationClassic = "classic"
	RuntimeGenerationAgentic = "agentic"
)

// EinoChatResume is the nested recovery payload embedded inside
// tool-resume-request.v1 (NOT a replacement outer schema).
//
// Contract:
//   - IDs only (no secrets / JWT / principal tokens)
//   - Message history lives in the gob checkpoint, not here
//   - Mutually exclusive with legacy chatLoop on the same snapshot
type EinoChatResume struct {
	ResumeSchemaVersion string   `json:"resumeSchemaVersion"`
	SessionID           string   `json:"sessionId"`
	UserMessageID       string   `json:"userMessageId"`
	ActorID             string   `json:"actorId"`
	EinoCheckpointID    string   `json:"einoCheckpointId"`
	InterruptIDs        []string `json:"interruptIds"`
	RootInterruptID     string   `json:"rootInterruptId"`
	GatedToolCallID     string   `json:"gatedToolCallId"`
	GatedStepID         string   `json:"gatedStepId"`
	InterruptKind       string   `json:"interruptKind"`
	// RuntimeGeneration names the runtime that paused the run. Absent on
	// payloads written before generation stamping existed; see
	// EffectiveRuntimeGeneration. Absence alone is not proof of classic —
	// Agentic pauses before 4B-1 also omit the marker; driveClassicResume
	// refuses those via the frozen-graph discriminator.
	RuntimeGeneration string `json:"runtimeGeneration,omitempty"`
}

// EffectiveRuntimeGeneration reports which runtime must carry the resume.
//
// An absent marker still routes toward classic so pre-Agentic in-flight
// confirmations (and rollbacks to builds that never wrote a marker) keep
// working. That is a routing default, not a historical claim that classic was
// the only unmarked writer: Agentic pauses between Task 4A and generation
// stamping also omit the marker. Those runs freeze an agent_graph_snapshot,
// and driveClassicResume refuses them with ErrAgenticResumeClassicOnFrozenRun
// before any live config read. Any other value is returned verbatim so the
// resume path rejects an unrecognised generation instead of guessing.
func (m EinoChatResume) EffectiveRuntimeGeneration() string {
	if generation := strings.TrimSpace(m.RuntimeGeneration); generation != "" {
		return generation
	}
	return RuntimeGenerationClassic
}

// runCarriesFrozenAgentGraph reports whether the run already has the freeze
// document that only Agentic-era root chat writes. Missing / null / {} match
// requireFrozenAgentGraph's "no freeze" cases — those are the
// classic in-flight population this seam still serves until Task 9.
func runCarriesFrozenAgentGraph(run execution.AgentRun) bool {
	raw := run.AgentGraphSnapshot
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return false
	}
	return true
}

// Valid reports whether the payload is a usable v1 einoChatResume.
func (m EinoChatResume) Valid() bool {
	if strings.TrimSpace(m.ResumeSchemaVersion) != EinoChatResumeSchemaVersion {
		return false
	}
	if strings.TrimSpace(m.EinoCheckpointID) == "" {
		return false
	}
	if strings.TrimSpace(m.SessionID) == "" ||
		strings.TrimSpace(m.UserMessageID) == "" ||
		strings.TrimSpace(m.ActorID) == "" {
		return false
	}
	// Prefer root; otherwise require at least one interrupt id.
	if strings.TrimSpace(m.RootInterruptID) == "" && len(m.InterruptIDs) == 0 {
		return false
	}
	return true
}

// EffectiveInterruptIDs returns interrupt IDs for ResumeWithParams Targets.
// Prefer RootInterruptID when InterruptIDs is empty.
func (m EinoChatResume) EffectiveInterruptIDs() []string {
	if len(m.InterruptIDs) > 0 {
		out := make([]string, 0, len(m.InterruptIDs))
		for _, id := range m.InterruptIDs {
			id = strings.TrimSpace(id)
			if id != "" {
				out = append(out, id)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if root := strings.TrimSpace(m.RootInterruptID); root != "" {
		return []string{root}
	}
	return nil
}

// ExtractEinoChatResume reads nested einoChatResume from a confirmation
// request snapshot. ok=false when missing, wrong version, or incomplete.
//
// Production ContinueDispatcher (PR16 / design phase D):
//  1. ExtractEinoChatResume → eino continue
//  2. else ErrInteractionDecisionInvalid (chatLoop-only is not resumed)
//
// Dual presence still succeeds here; EmbedEinoChatResume strips chatLoop.
func ExtractEinoChatResume(requestSnapshot json.RawMessage) (EinoChatResume, bool) {
	if len(requestSnapshot) == 0 || string(requestSnapshot) == "null" {
		return EinoChatResume{}, false
	}
	var object map[string]any
	if err := json.Unmarshal(requestSnapshot, &object); err != nil {
		return EinoChatResume{}, false
	}
	raw, ok := object["einoChatResume"]
	if !ok || raw == nil {
		return EinoChatResume{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return EinoChatResume{}, false
	}
	var meta EinoChatResume
	if err := json.Unmarshal(encoded, &meta); err != nil {
		return EinoChatResume{}, false
	}
	if !meta.Valid() {
		return EinoChatResume{}, false
	}
	return meta, true
}

// EmbedEinoChatResume nests einoChatResume into an outer tool-resume-request.v1
// snapshot. chatLoop is removed if present so the snapshot never carries dual
// authority recovery state (design §3.6.2).
func EmbedEinoChatResume(requestSnapshot json.RawMessage, meta EinoChatResume) (json.RawMessage, error) {
	var object map[string]any
	if len(requestSnapshot) == 0 || string(requestSnapshot) == "null" {
		object = map[string]any{}
	} else if err := json.Unmarshal(requestSnapshot, &object); err != nil {
		return nil, err
	}
	// Mutual exclusion: never leave chatLoop alongside einoChatResume.
	delete(object, "chatLoop")
	object["einoChatResume"] = meta
	return json.Marshal(object)
}

// HasChatLoop reports whether a request snapshot embeds legacy chatLoop
// (regardless of schema validity). Used by tests and dual-authority guards.
func HasChatLoop(requestSnapshot json.RawMessage) bool {
	if len(requestSnapshot) == 0 {
		return false
	}
	var object map[string]any
	if err := json.Unmarshal(requestSnapshot, &object); err != nil {
		return false
	}
	raw, ok := object["chatLoop"]
	return ok && raw != nil
}

// ToolCallIDFromInterruptID extracts the tool address segment ID from a vendor
// fully-qualified interrupt address (e.g. "agent:A;tool:tool_call_abc").
func ToolCallIDFromInterruptID(interruptID string) string {
	for _, part := range strings.Split(interruptID, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "tool:") {
			return strings.TrimPrefix(part, "tool:")
		}
	}
	return ""
}
