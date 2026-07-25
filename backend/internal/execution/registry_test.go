package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestExecutorRegistryRegisterResolveAndRejectDuplicate(t *testing.T) {
	executor := staticExecutor{kind: ExecutorTypeHTTP}
	registry, err := NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(" http ")
	if err != nil || resolved.Kind() != ExecutorTypeHTTP {
		t.Fatalf("resolve HTTP executor: %v %v", resolved, err)
	}
	if !reflect.DeepEqual(registry.Kinds(), []string{ExecutorTypeHTTP}) {
		t.Fatalf("unexpected registered executor kinds: %v", registry.Kinds())
	}
	if err := registry.Register(executor); !errors.Is(err, ErrExecutorExists) {
		t.Fatalf("expected duplicate executor rejection, got %v", err)
	}
	if _, err := registry.Resolve("MCP"); !errors.Is(err, ErrExecutorNotFound) {
		t.Fatalf("expected unavailable executor error, got %v", err)
	}
}

func TestExecutorRegistryRejectsInvalidExecutor(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(nil); !errors.Is(err, ErrInvalidExecutor) {
		t.Fatalf("expected nil executor rejection, got %v", err)
	}
	if err := registry.Register(staticExecutor{}); !errors.Is(err, ErrInvalidExecutor) {
		t.Fatalf("expected empty kind rejection, got %v", err)
	}
}

type staticExecutor struct{ kind string }

func (executor staticExecutor) Kind() string { return executor.kind }
func (staticExecutor) Capabilities() ExecutorFeatures {
	return ExecutorFeatures{}
}
func (staticExecutor) Invoke(context.Context, InvocationRequest, InvocationEventSink) (InvocationResult, error) {
	return InvocationResult{}, nil
}
func (staticExecutor) Cancel(context.Context, InvocationRef) error { return nil }
