package capability

import (
	"context"
	"errors"
	"fmt"

	"actweave/backend/internal/modelconfig"
)

// ConnectionCompatibilityChecker is implemented by the HTTP Tool domain once
// Tool(provider_id) exists. Requiring it here prevents a Binding from silently
// treating same-workspace as sufficient provider compatibility.
type ConnectionCompatibilityChecker interface {
	ValidateBindingConnection(context.Context, string, string, string) error
}

type ConnectionCompatibilityFunc func(context.Context, string, string, string) error

func (f ConnectionCompatibilityFunc) ValidateBindingConnection(ctx context.Context, workspaceID, capabilityID, connectionID string) error {
	return f(ctx, workspaceID, capabilityID, connectionID)
}

type ModelConfigReader interface {
	Get(ctx context.Context, workspaceID, configID string) (modelconfig.Config, error)
}

type AgentCatalogLister interface {
	ListForAgent(ctx context.Context, workspaceID, agentID string) ([]Descriptor, error)
}

type DelegationEdgeReader interface {
	HasEnabledDelegationEdges(ctx context.Context, workspaceID, agentID string) (bool, error)
}

type BindingService struct {
	repository    *Repository
	compatibility ConnectionCompatibilityChecker
	models        ModelConfigReader
	catalog       AgentCatalogLister
	edges         DelegationEdgeReader
}

func NewBindingService(repository *Repository, compatibility ConnectionCompatibilityChecker) (*BindingService, error) {
	if repository == nil || compatibility == nil {
		return nil, errors.New("binding repository and connection compatibility checker are required")
	}
	return &BindingService{repository: repository, compatibility: compatibility}, nil
}

// WithToolCompatibility installs the Agent-model tool gate used on Bind.
func (s *BindingService) WithToolCompatibility(
	models ModelConfigReader, catalog AgentCatalogLister, edges DelegationEdgeReader,
) *BindingService {
	if s != nil {
		s.models = models
		s.catalog = catalog
		s.edges = edges
	}
	return s
}

func (s *BindingService) Bind(ctx context.Context, input BindInput) (Binding, error) {
	if input.ConnectionID != nil {
		if err := s.compatibility.ValidateBindingConnection(ctx, input.WorkspaceID, input.CapabilityID, *input.ConnectionID); err != nil {
			return Binding{}, fmt.Errorf("validate binding connection compatibility: %w", err)
		}
	}
	if err := s.assertModelToolCompatibility(ctx, input); err != nil {
		return Binding{}, err
	}
	return s.repository.Bind(ctx, input)
}

func (s *BindingService) assertModelToolCompatibility(ctx context.Context, input BindInput) error {
	if s == nil || s.models == nil || s.catalog == nil {
		return nil
	}
	modelID, err := s.repository.AgentModelConfigID(ctx, input.WorkspaceID, input.AgentID)
	if err != nil {
		return err
	}
	cfg, err := s.models.Get(ctx, input.WorkspaceID, modelID)
	if err != nil {
		return err
	}
	descriptors, err := s.catalog.ListForAgent(ctx, input.WorkspaceID, input.AgentID)
	if err != nil {
		return err
	}
	count := len(descriptors)
	already := false
	for _, descriptor := range descriptors {
		if descriptor.CapabilityID == input.CapabilityID {
			already = true
			break
		}
	}
	switch {
	case input.Enabled && !already:
		count++
	case !input.Enabled && already:
		count--
	}
	hasEdges := false
	if s.edges != nil {
		hasEdges, err = s.edges.HasEnabledDelegationEdges(ctx, input.WorkspaceID, input.AgentID)
		if err != nil {
			return err
		}
	}
	return modelconfig.AssertAgentModelToolCompatibility(cfg, modelconfig.AgentModelToolCheck{
		AgentID:            input.AgentID,
		CatalogCount:       count,
		HasDelegationEdges: hasEdges,
		RequireVerified:    false,
	})
}

func (s *BindingService) Unbind(ctx context.Context, workspaceID, agentID, capabilityID string, expectedLockVersion int64) error {
	return s.repository.Unbind(ctx, workspaceID, agentID, capabilityID, expectedLockVersion)
}
