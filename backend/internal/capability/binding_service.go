package capability

import (
	"context"
	"errors"
	"fmt"
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

type BindingService struct {
	repository    *Repository
	compatibility ConnectionCompatibilityChecker
}

func NewBindingService(repository *Repository, compatibility ConnectionCompatibilityChecker) (*BindingService, error) {
	if repository == nil || compatibility == nil {
		return nil, errors.New("binding repository and connection compatibility checker are required")
	}
	return &BindingService{repository: repository, compatibility: compatibility}, nil
}

func (s *BindingService) Bind(ctx context.Context, input BindInput) (Binding, error) {
	if input.ConnectionID != nil {
		if err := s.compatibility.ValidateBindingConnection(ctx, input.WorkspaceID, input.CapabilityID, *input.ConnectionID); err != nil {
			return Binding{}, fmt.Errorf("validate binding connection compatibility: %w", err)
		}
	}
	return s.repository.Bind(ctx, input)
}

func (s *BindingService) Unbind(ctx context.Context, workspaceID, agentID, capabilityID string, expectedLockVersion int64) error {
	return s.repository.Unbind(ctx, workspaceID, agentID, capabilityID, expectedLockVersion)
}
