package chatruntimebridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/strictjson"

	"github.com/google/uuid"
)

// Run-level agent binding freeze (agent_runs.agent_snapshot).
//
// Producer: application.agentRunSnapshots.SnapshotAgentRun (run.v2 branch) emits
// exactly six keys. This is a different document from the graph node
// agentSnapshot in agentdelegation (which carries name/roleDescription and spells
// the lock field modelConfigLockVer); the two must not be validated with each
// other's schema.
var runAgentSnapshotRequired = []string{
	"schemaVersion", "agentId", "promptRevisionId", "promptRevisionHash",
	"modelConfigId", "modelConfigLockVersion",
}

var runAgentSnapshotAllowed = map[string]struct{}{
	"schemaVersion": {}, "agentId": {}, "promptRevisionId": {},
	"promptRevisionHash": {}, "modelConfigId": {}, "modelConfigLockVersion": {},
}

// runAgentBinding is the validated frozen identity carried on run.AgentSnapshot.
type runAgentBinding struct {
	AgentID            string
	PromptRevisionID   string
	PromptRevisionHash string
	ModelConfigID      string
	ModelConfigLockVer int64
}

// parseRunAgentSnapshotStrict validates raw run.AgentSnapshot against the complete
// producer schema before any identity derived from it is used.
//
// Fail closed on: missing / null / non-object / duplicate JSON keys / unknown
// fields / wrong types / non-canonical UUIDs / non-hex prompt hash / lock < 1.
// There is no TrimSpace success path and no "unparseable means absent" path.
func parseRunAgentSnapshotStrict(raw json.RawMessage) (runAgentBinding, error) {
	if len(raw) == 0 || !bytes.Equal(raw, bytes.TrimSpace(raw)) {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot raw invalid", ErrAgenticAgentSnapshotRequired)
	}
	if string(raw) == "null" || string(raw) == "{}" {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot missing", ErrAgenticAgentSnapshotRequired)
	}

	top, err := strictjson.DecodeObjectMap(raw)
	if err != nil {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot structure", ErrAgenticAgentSnapshotRequired)
	}
	for k := range top {
		if _, ok := runAgentSnapshotAllowed[k]; !ok {
			return runAgentBinding{}, fmt.Errorf("%w: agent snapshot structure", ErrAgenticAgentSnapshotRequired)
		}
	}
	for _, req := range runAgentSnapshotRequired {
		if _, err := strictjson.RequirePresentNonNull(top, req); err != nil {
			return runAgentBinding{}, fmt.Errorf("%w: agent snapshot structure", ErrAgenticAgentSnapshotRequired)
		}
	}

	schema, err := strictjson.DecodeStringExact(top["schemaVersion"])
	if err != nil || schema != execution.AgentSnapshotSchemaV1 {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot schemaVersion", ErrAgenticAgentSnapshotRequired)
	}
	agentID, err := decodeCanonicalUUIDField(top["agentId"])
	if err != nil {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot agentId", ErrAgenticAgentSnapshotRequired)
	}
	modelConfigID, err := decodeCanonicalUUIDField(top["modelConfigId"])
	if err != nil {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot modelConfigId", ErrAgenticAgentSnapshotRequired)
	}
	revID, err := decodeCanonicalUUIDField(top["promptRevisionId"])
	if err != nil {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot promptRevisionId", ErrAgenticAgentSnapshotRequired)
	}
	revHash, err := strictjson.DecodeStringExact(top["promptRevisionHash"])
	if err != nil || !isLowerHex64(revHash) {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot promptRevisionHash", ErrAgenticAgentSnapshotRequired)
	}
	lock, err := strictjson.DecodeInt64Exact(top["modelConfigLockVersion"])
	if err != nil || lock < 1 {
		return runAgentBinding{}, fmt.Errorf("%w: agent snapshot modelConfigLockVersion", ErrAgenticAgentSnapshotRequired)
	}

	return runAgentBinding{
		AgentID:            agentID,
		PromptRevisionID:   revID,
		PromptRevisionHash: revHash,
		ModelConfigID:      modelConfigID,
		ModelConfigLockVer: lock,
	}, nil
}

// requireFrozenAgentBinding parses run.AgentSnapshot and cross-binds it to the
// run's agent and to the frozen model snapshot that actually builds the provider.
// Divergence between the two frozen documents fails closed: a run must never
// execute a model config that its own agent binding freeze does not describe.
func requireFrozenAgentBinding(run execution.AgentRun, cfg modelconfig.Config) (runAgentBinding, error) {
	binding, err := parseRunAgentSnapshotStrict(run.AgentSnapshot)
	if err != nil {
		return runAgentBinding{}, err
	}
	runAgentID := strings.TrimSpace(run.AgentID)
	if runAgentID == "" || binding.AgentID != runAgentID {
		return runAgentBinding{}, fmt.Errorf(
			"%w: agent snapshot agentId does not match run agent", ErrAgenticAgentSnapshotRequired)
	}
	if binding.ModelConfigID != cfg.ID {
		return runAgentBinding{}, fmt.Errorf(
			"%w: agent snapshot modelConfigId does not match frozen model snapshot", ErrAgenticAgentSnapshotRequired)
	}
	if binding.ModelConfigLockVer != cfg.LockVersion {
		return runAgentBinding{}, fmt.Errorf(
			"%w: agent snapshot modelConfigLockVersion does not match frozen model snapshot lockVersion",
			ErrAgenticAgentSnapshotRequired)
	}
	return binding, nil
}

func decodeCanonicalUUIDField(raw json.RawMessage) (string, error) {
	s, err := strictjson.DecodeStringExact(raw)
	if err != nil {
		return "", err
	}
	if s == "" || s != strings.TrimSpace(s) {
		return "", fmt.Errorf("non-canonical string")
	}
	id, err := uuid.Parse(s)
	if err != nil || id.String() != s {
		return "", fmt.Errorf("not a canonical UUID")
	}
	return s, nil
}

// isLowerHex64 matches the agent_prompt_revisions.content_sha256 column contract
// (CHAR(64) CHECK ~ '^[0-9a-f]{64}$').
func isLowerHex64(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
