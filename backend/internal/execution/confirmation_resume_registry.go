package execution

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var (
	ErrResumeExecutorNotFound = errors.New("CONFIRMATION_RESUME_EXECUTOR_NOT_AVAILABLE")
	ErrResumeExecutorExists   = errors.New("confirmation resume executor already registered")
)

type ConfirmationResumeExecutor interface {
	Kind() string
	Execute(context.Context, ResumeExecutionInput) (ResumeExecutionOutput, error)
}

type ConfirmationResumeRegistry struct {
	mutex     sync.RWMutex
	executors map[string]ConfirmationResumeExecutor
}

func NewConfirmationResumeRegistry(
	executors ...ConfirmationResumeExecutor,
) (*ConfirmationResumeRegistry, error) {
	registry := &ConfirmationResumeRegistry{executors: make(map[string]ConfirmationResumeExecutor)}
	for _, executor := range executors {
		if err := registry.Register(executor); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (registry *ConfirmationResumeRegistry) Register(executor ConfirmationResumeExecutor) error {
	if registry == nil || executor == nil {
		return ErrResumeExecutorNotFound
	}
	kind := strings.ToUpper(strings.TrimSpace(executor.Kind()))
	if kind != ResumeKindTool && kind != ResumeKindWorkflow {
		return ErrResumeExecutorNotFound
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if _, exists := registry.executors[kind]; exists {
		return ErrResumeExecutorExists
	}
	registry.executors[kind] = executor
	return nil
}

func (registry *ConfirmationResumeRegistry) Resolve(kind string) (ConfirmationResumeExecutor, error) {
	if registry == nil {
		return nil, ErrResumeExecutorNotFound
	}
	kind = strings.ToUpper(strings.TrimSpace(kind))
	registry.mutex.RLock()
	executor, exists := registry.executors[kind]
	registry.mutex.RUnlock()
	if !exists {
		return nil, ErrResumeExecutorNotFound
	}
	return executor, nil
}
