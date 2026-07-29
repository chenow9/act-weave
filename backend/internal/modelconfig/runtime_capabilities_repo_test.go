package modelconfig

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRuntimeCapabilitiesCASAndNotMergedIntoOptions(t *testing.T) {
	repository, _ := newModelConfigRepositoryTest(t, nil)
	ctx := context.Background()
	caps := json.RawMessage(`{
		"schemaVersion":"model-runtime.v1",
		"contextWindowTokens":128000,
		"defaultOutputReserveTokens":4096,
		"outputTokenLimitMode":"max_tokens",
		"tokenizerProfile":"o200k_base",
		"tokenizerVersion":"2026-01"
	}`)
	input := validNewConfig(repositoryConfigID, "Caps Model")
	input.RuntimeCapabilities = caps
	created, err := repository.Create(ctx, input)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !json.Valid(created.RuntimeCapabilities) || string(created.RuntimeCapabilities) == "{}" {
		t.Fatalf("expected persisted runtime capabilities, got %s", created.RuntimeCapabilities)
	}
	var options map[string]any
	if err := json.Unmarshal(created.Options, &options); err != nil {
		t.Fatal(err)
	}
	if _, ok := options["contextWindowTokens"]; ok {
		t.Fatal("runtime capabilities must not leak into options")
	}

	leak := validNewConfig("018f1f2e-7b5a-7c3d-8e9f-e234567890b2", "Leak Model")
	leak.Options = json.RawMessage(`{"contextWindowTokens":999999}`)
	if _, err := repository.Create(ctx, leak); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected options leak reject, got %v", err)
	}

	if _, err := repository.Update(ctx, repositoryWorkspaceID, created.ID, UpdateConfig{
		Name: created.Name, Provider: created.Provider, APIBase: created.APIBase,
		ModelName: created.ModelName, Options: created.Options,
		RuntimeCapabilities: created.RuntimeCapabilities, Status: StatusUnverified,
		UpdatedBy: repositoryOwnerID, ExpectedLockVersion: created.LockVersion + 99,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected CAS conflict, got %v", err)
	}

	if _, err := repository.Update(ctx, repositoryWorkspaceID, created.ID, UpdateConfig{
		Name: created.Name, Provider: created.Provider, APIBase: created.APIBase,
		ModelName: created.ModelName, Options: created.Options,
		RuntimeCapabilities: json.RawMessage(`{
			"schemaVersion":"model-runtime.v1",
			"contextWindowTokens":10,
			"defaultOutputReserveTokens":1,
			"outputTokenLimitMode":"max_tokens",
			"tokenizerProfile":"not-a-profile",
			"tokenizerVersion":"v"
		}`),
		Status: StatusUnverified, UpdatedBy: repositoryOwnerID,
		ExpectedLockVersion: created.LockVersion,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid tokenizer, got %v", err)
	}
}
