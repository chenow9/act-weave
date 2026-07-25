package execution

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mutex     sync.RWMutex
	executors map[string]CapabilityExecutor
}

func NewRegistry(executors ...CapabilityExecutor) (*Registry, error) {
	registry := &Registry{executors: make(map[string]CapabilityExecutor, len(executors))}
	for _, executor := range executors {
		if err := registry.Register(executor); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (registry *Registry) Register(executor CapabilityExecutor) error {
	if registry == nil || executor == nil {
		return ErrInvalidExecutor
	}
	kind := normalizeKind(executor.Kind())
	if kind == "" {
		return ErrInvalidExecutor
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if _, exists := registry.executors[kind]; exists {
		return fmt.Errorf("%s: %w", kind, ErrExecutorExists)
	}
	registry.executors[kind] = executor
	return nil
}

func (registry *Registry) Resolve(executorType string) (CapabilityExecutor, error) {
	if registry == nil {
		return nil, ErrExecutorNotFound
	}
	kind := normalizeKind(executorType)
	registry.mutex.RLock()
	executor, exists := registry.executors[kind]
	registry.mutex.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%s: %w", kind, ErrExecutorNotFound)
	}
	return executor, nil
}

func (registry *Registry) Kinds() []string {
	if registry == nil {
		return nil
	}
	registry.mutex.RLock()
	kinds := make([]string, 0, len(registry.executors))
	for kind := range registry.executors {
		kinds = append(kinds, kind)
	}
	registry.mutex.RUnlock()
	sort.Strings(kinds)
	return kinds
}

func normalizeKind(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
