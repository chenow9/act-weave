package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"
)

// SnapshotRuntime holds immutable run-scoped prompt/model/tool inputs for v2 runs.
// Live Agent/model config must not silently recompute these after run create.
type SnapshotRuntime struct {
	// UseSnapshot is true for run.v2 with a recognized context snapshot (v1 or v2).
	UseSnapshot bool
	// IsContextV2 is true when session-context.v2 is frozen on the run.
	IsContextV2 bool
	// Model is the config used to build the chat model (from snapshot or live).
	Model modelconfig.Config
	// SystemPrompt is the instruction for this run (pinned revision when available).
	SystemPrompt string
	// ToolSchemas are the estimator inputs matching actual bound tools.
	ToolSchemas []contextwindow.ToolSchema
	// PromptRevisionID is non-empty when agent snapshot pins a revision.
	PromptRevisionID string
	// ModelConfigID from agent/model snapshot binding.
	ModelConfigID string
}

// SnapshotRuntimeResolver builds SnapshotRuntime from an AgentRun's frozen snapshots.
// Kill-switch (model DISABLED) still reads live model status only for the same secret/id.
type SnapshotRuntimeResolver struct {
	Agents  chatruntime.AgentReader
	Models  chatruntime.ModelReader
	Content chatruntime.ContentReader // unused; reserved for future revision body load
}

// Resolve returns snapshot-backed runtime inputs for initial (non-resume) runs.
// Legacy / run.v1 paths set UseSnapshot=false and leave callers on live resolution.
func (r *SnapshotRuntimeResolver) Resolve(
	ctx context.Context,
	workspaceID string,
	run execution.AgentRun,
	liveAgent agent.Agent,
	fallbackInstruction string,
) (SnapshotRuntime, error) {
	out := SnapshotRuntime{
		SystemPrompt:  fallbackInstruction,
		ModelConfigID: strings.TrimSpace(liveAgent.ModelConfigID),
	}
	if run.SnapshotSchemaVersion != execution.RunSnapshotSchemaV2 {
		return out, nil
	}
	if sessioncontext.IsLegacySnapshot(run.ContextPolicySnapshot) {
		return out, nil
	}
	resolved, err := sessioncontext.ParseResolvedSnapshot(run.ContextPolicySnapshot)
	if err != nil {
		if errors.Is(err, sessioncontext.ErrUnsupportedSnapshot) {
			return SnapshotRuntime{}, execution.NewContextError(execution.ErrCodeContextSnapshotUnsupported)
		}
		return SnapshotRuntime{}, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if resolved.Mode == sessioncontext.ModeLegacy {
		return out, nil
	}

	out.UseSnapshot = true
	out.IsContextV2 = resolved.SchemaVersion == sessioncontext.SnapshotSchemaV2

	// Model from immutable run.ModelSnapshot; only kill-switch via live status.
	// Malformed snapshot is never reconstructed from live config.
	model, modelID, err := parseModelSnapshot(run.ModelSnapshot)
	if err != nil {
		return SnapshotRuntime{}, fmt.Errorf("%w: %v", ErrAgenticModelSnapshotRequired, err)
	}
	if modelID != "" {
		out.ModelConfigID = modelID
	}
	// Live kill switch: DISABLED blocks the run; other live fields are ignored.
	if r != nil && r.Models != nil && out.ModelConfigID != "" {
		live, liveErr := r.Models.Get(ctx, workspaceID, out.ModelConfigID)
		if liveErr == nil && live.Status == modelconfig.StatusDisabled {
			return SnapshotRuntime{}, errors.New("model config is disabled")
		}
		// Same secret reference may rotate; snapshot keeps credential secret ID.
		if liveErr == nil && model.CredentialSecretID == nil && live.CredentialSecretID != nil {
			// Do not pull live secrets into snapshot model identity; BuildChatModel
			// may still resolve credential by ID from snapshot when present.
		}
	}
	out.Model = model

	// Prompt from agent snapshot revision id — not live CurrentPromptRevisionID.
	revID := agentSnapshotPromptRevisionID(run.AgentSnapshot)
	out.PromptRevisionID = revID
	if revID != "" && r != nil && r.Agents != nil {
		if prompt, ok := loadPromptRevision(ctx, r.Agents, workspaceID, run.AgentID, revID); ok {
			out.SystemPrompt = prompt
		}
	}

	// Tool schemas from capability snapshot (same set buildPipelineTools uses).
	out.ToolSchemas, err = toolSchemasFromCapabilitySnapshot(run.CapabilitySnapshot)
	if err != nil {
		return SnapshotRuntime{}, err
	}
	return out, nil
}

// SnapshotModelFactory builds a chat model from snapshot-backed Config.
// Callers pass the same BuildChatModel used for live path; factory only selects input.
type SnapshotModelFactory struct {
	Build ChatModelBuilder
}

// BuildFromSnapshot constructs the model for a SnapshotRuntime (or live cfg when not snapshot).
func (f SnapshotModelFactory) BuildFromSnapshot(
	ctx context.Context,
	rt SnapshotRuntime,
	live modelconfig.Config,
) (modelconfig.Config, error) {
	if f.Build == nil {
		return modelconfig.Config{}, errors.New("chat model builder is not configured")
	}
	cfg := live
	if rt.UseSnapshot && strings.TrimSpace(rt.Model.ID) != "" {
		cfg = rt.Model
		// Preserve workspace for secret resolution scope.
		if cfg.WorkspaceID == "" {
			cfg.WorkspaceID = live.WorkspaceID
		}
		// Status on snapshot may be empty; kill-switch already applied in Resolve.
		if cfg.Status == "" {
			cfg.Status = modelconfig.StatusVerified
		}
	}
	return cfg, nil
}

func parseModelSnapshot(raw json.RawMessage) (modelconfig.Config, string, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return modelconfig.Config{}, "", nil
	}
	var doc struct {
		ID                  string          `json:"id"`
		Provider            string          `json:"provider"`
		APIBase             string          `json:"apiBase"`
		ModelName           string          `json:"modelName"`
		Options             json.RawMessage `json:"options"`
		CredentialSecretID  *string         `json:"credentialSecretId"`
		LockVersion         int64           `json:"lockVersion"`
		AgenticCapabilities json.RawMessage `json:"agenticCapabilities"`
		RuntimeCapabilities json.RawMessage `json:"runtimeCapabilities"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return modelconfig.Config{}, "", err
	}
	id := strings.TrimSpace(doc.ID)
	if id == "" {
		return modelconfig.Config{}, "", errors.New("model snapshot missing id")
	}
	cfg := modelconfig.Config{
		ID:                  id,
		Provider:            strings.TrimSpace(doc.Provider),
		APIBase:             strings.TrimSpace(doc.APIBase),
		ModelName:           strings.TrimSpace(doc.ModelName),
		Options:             doc.Options,
		CredentialSecretID:  doc.CredentialSecretID,
		LockVersion:         doc.LockVersion,
		Status:              modelconfig.StatusVerified,
		AgenticCapabilities: doc.AgenticCapabilities,
		RuntimeCapabilities: doc.RuntimeCapabilities,
	}
	if len(cfg.Options) == 0 {
		cfg.Options = json.RawMessage(`{}`)
	}
	if len(cfg.AgenticCapabilities) == 0 {
		cfg.AgenticCapabilities = json.RawMessage(`{}`)
	}
	if len(cfg.RuntimeCapabilities) == 0 {
		cfg.RuntimeCapabilities = json.RawMessage(`{}`)
	}
	return cfg, id, nil
}

func loadPromptRevision(
	ctx context.Context,
	agents chatruntime.AgentReader,
	workspaceID, agentID, revisionID string,
) (string, bool) {
	revisions, err := agents.ListPromptRevisions(ctx, workspaceID, agentID)
	if err != nil {
		return "", false
	}
	for _, revision := range revisions {
		if revision.ID == revisionID && strings.TrimSpace(revision.SystemPrompt) != "" {
			return strings.TrimSpace(revision.SystemPrompt), true
		}
	}
	return "", false
}

func toolSchemasFromCapabilitySnapshot(raw json.RawMessage) ([]contextwindow.ToolSchema, error) {
	capabilities, err := chatruntime.ParseCapabilitySnapshot(raw)
	if err != nil {
		return nil, fmt.Errorf("parse capability snapshot: %w", err)
	}
	out := make([]contextwindow.ToolSchema, 0, len(capabilities))
	for _, cap := range capabilities {
		kind := strings.ToUpper(strings.TrimSpace(cap.Kind))
		if kind == "" {
			kind = "TOOL"
		}
		if kind != "TOOL" && kind != "WORKFLOW" {
			continue
		}
		name := strings.TrimSpace(cap.CallableName)
		if name == "" {
			continue
		}
		params := cap.InputSchema
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		out = append(out, contextwindow.ToolSchema{
			Name:        name,
			Description: strings.TrimSpace(cap.CallableDescription),
			Parameters:  params,
		})
	}
	return out, nil
}
