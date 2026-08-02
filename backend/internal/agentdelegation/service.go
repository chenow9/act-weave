package agentdelegation

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Service is the product-facing API for bindings + audit writes.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) (*Service, error) {
	if repo == nil {
		return nil, ErrInvalid
	}
	return &Service{repo: repo}, nil
}

func (s *Service) CreateBinding(ctx context.Context, input CreateBindingInput) (Binding, error) {
	if strings.TrimSpace(input.ID) == "" {
		input.ID = uuid.Must(uuid.NewV7()).String()
	}
	return s.repo.CreateBinding(ctx, input)
}

func (s *Service) UpdateBinding(ctx context.Context, input UpdateBindingInput) (Binding, error) {
	return s.repo.UpdateBinding(ctx, input)
}

func (s *Service) SoftDisable(ctx context.Context, workspaceID, bindingID string, expectedVersion int64, actorID string) error {
	return s.repo.SoftDisable(ctx, workspaceID, bindingID, expectedVersion, actorID)
}

func (s *Service) GetBinding(ctx context.Context, workspaceID, bindingID string) (Binding, error) {
	return s.repo.GetBinding(ctx, workspaceID, bindingID)
}

func (s *Service) ListBindings(ctx context.Context, workspaceID, callerAgentID string) ([]Binding, error) {
	return s.repo.ListBindings(ctx, workspaceID, callerAgentID)
}

func (s *Service) ListEnabledEdges(ctx context.Context, workspaceID string) ([]GraphEdgeSnapshot, error) {
	bindings, err := s.repo.ListAllEnabled(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return BindingsToEdges(bindings), nil
}

func (s *Service) ListEnabledForCaller(ctx context.Context, workspaceID, callerAgentID string) ([]Binding, error) {
	return s.repo.ListEnabledForCaller(ctx, workspaceID, callerAgentID)
}

// AuditWriter implementation.
func (s *Service) CreateDelegationAndStep(ctx context.Context, input CreateDelegationInput) (Delegation, bool, error) {
	return s.repo.CreateDelegationAndStep(ctx, input)
}

func (s *Service) FinalizeDelegation(ctx context.Context, input FinalizeDelegationInput) (Delegation, error) {
	return s.repo.FinalizeDelegation(ctx, input)
}

func (s *Service) SetChildRunID(ctx context.Context, workspaceID, delegationID, childRunID string) error {
	return s.repo.SetChildRunID(ctx, workspaceID, delegationID, childRunID)
}

func (s *Service) RecordDispatchAttempt(ctx context.Context, workspaceID, delegationID string) error {
	return s.repo.RecordDispatchAttempt(ctx, workspaceID, delegationID)
}

func (s *Service) AccumulateModelTokens(ctx context.Context, workspaceID, delegationID string, usage TokenUsage) error {
	return s.repo.AccumulateModelTokens(ctx, workspaceID, delegationID, usage)
}

func (s *Service) GetByIdempotency(ctx context.Context, workspaceID, key string) (Delegation, error) {
	return s.repo.GetByIdempotency(ctx, workspaceID, key)
}

func (s *Service) ListByParentRun(ctx context.Context, workspaceID, parentRunID string) ([]Delegation, error) {
	return s.repo.ListByParentRun(ctx, workspaceID, parentRunID)
}

// Repository exposes the underlying repo for advanced wiring/tests.
func (s *Service) Repository() *Repository { return s.repo }

var _ AuditWriter = (*Service)(nil)
