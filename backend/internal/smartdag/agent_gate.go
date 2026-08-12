package smartdag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/modelconfig"
)

// AgentLookup loads an Agent scoped by workspace (same-workspace check).
type AgentLookup interface {
	Get(ctx context.Context, workspaceID, agentID string) (agent.Agent, error)
}

// ModelConfigLookup loads a model config scoped by workspace.
type ModelConfigLookup interface {
	Get(ctx context.Context, workspaceID, configID string) (modelconfig.Config, error)
}

// AgentModelGate enforces D2: generate requires same-workspace Agent with usable LLM.
// Model resolution is Agent → modelConfig only; never from request body.
type AgentModelGate struct {
	agents AgentLookup
	models ModelConfigLookup
}

// ResolvedAgentModel is the agent + usable modelConfig pair for generation.
type ResolvedAgentModel struct {
	Agent       agent.Agent
	ModelConfig modelconfig.Config
}

// NewAgentModelGate constructs a gate. Both lookups are required.
func NewAgentModelGate(agents AgentLookup, models ModelConfigLookup) (*AgentModelGate, error) {
	if agents == nil || models == nil {
		return nil, errors.New("agent and model lookups are required")
	}
	return &AgentModelGate{agents: agents, models: models}, nil
}

// Resolve loads the agent in the workspace and requires a usable modelConfig.
//
// requestModelConfigID, if non-empty, is always rejected (no body bypass — D2 / P1.1.3).
// Returns ErrAgentModelRequired when model is missing or unusable (no Draft side effects here).
// Returns ErrAgentNotInWorkspace when the agent is not found in the workspace.
func (g *AgentModelGate) Resolve(
	ctx context.Context,
	workspaceID, agentID, requestModelConfigID string,
) (ResolvedAgentModel, error) {
	if g == nil || g.agents == nil || g.models == nil {
		return ResolvedAgentModel{}, errors.New("agent model gate is not configured")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	requestModelConfigID = strings.TrimSpace(requestModelConfigID)

	if requestModelConfigID != "" {
		return ResolvedAgentModel{}, ErrModelConfigBypassRejected
	}
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return ResolvedAgentModel{}, ErrInvalid
	}

	value, err := g.agents.Get(ctx, workspaceID, agentID)
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			return ResolvedAgentModel{}, fmt.Errorf("%w: %v", ErrAgentNotInWorkspace, err)
		}
		return ResolvedAgentModel{}, fmt.Errorf("load agent for smart orchestration: %w", err)
	}
	// Defense in depth: repository is workspace-scoped; still assert equality.
	if strings.TrimSpace(value.WorkspaceID) != workspaceID {
		return ResolvedAgentModel{}, ErrAgentNotInWorkspace
	}
	if value.Status == agent.StatusDisabled {
		return ResolvedAgentModel{}, fmt.Errorf("%w: agent is disabled", ErrAgentModelRequired)
	}

	modelConfigID := strings.TrimSpace(value.ModelConfigID)
	if modelConfigID == "" || !validUUID(modelConfigID) {
		return ResolvedAgentModel{}, ErrAgentModelRequired
	}

	cfg, err := g.models.Get(ctx, workspaceID, modelConfigID)
	if err != nil {
		if errors.Is(err, modelconfig.ErrNotFound) {
			return ResolvedAgentModel{}, ErrAgentModelRequired
		}
		return ResolvedAgentModel{}, fmt.Errorf("load agent model config: %w", err)
	}
	if !modelConfigUsable(cfg) {
		return ResolvedAgentModel{}, ErrAgentModelRequired
	}

	return ResolvedAgentModel{Agent: value, ModelConfig: cfg}, nil
}

// modelConfigUsable is true when an AgenticModel can be constructed from the config.
// DISABLED / missing apiBase or modelName / soft-deleted are not usable.
func modelConfigUsable(cfg modelconfig.Config) bool {
	if strings.TrimSpace(cfg.ID) == "" {
		return false
	}
	if cfg.DeletedAt != nil {
		return false
	}
	if cfg.Status == modelconfig.StatusDisabled {
		return false
	}
	if strings.TrimSpace(cfg.APIBase) == "" || strings.TrimSpace(cfg.ModelName) == "" {
		return false
	}
	return true
}
