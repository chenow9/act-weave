package modelapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"

	"actweave/backend/internal/modelconfig"
)

func TestReasoningEffortFromOptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want openai.ReasoningEffortLevel
		ok   bool
	}{
		// Omitted key / empty options → default high (gateway needs high for CoT text).
		{`{}`, openai.ReasoningEffortLevelHigh, true},
		{``, openai.ReasoningEffortLevelHigh, true},
		{`{"temperature":0.2}`, openai.ReasoningEffortLevelHigh, true},
		{`{"reasoningEffort":"high"}`, openai.ReasoningEffortLevelHigh, true},
		{`{"reasoningEffort":"MEDIUM"}`, openai.ReasoningEffortLevelMedium, true},
		{`{"reasoningEffort":"low"}`, openai.ReasoningEffortLevelLow, true},
		{`{"reasoningEffort":"none"}`, "", false},
		{`{"reasoningEffort":"off"}`, "", false},
		// Unknown values fall back to default high (not silent disable).
		{`{"reasoningEffort":"extreme"}`, openai.ReasoningEffortLevelHigh, true},
	}
	for _, tc := range cases {
		got, ok := reasoningEffortFromOptions(json.RawMessage(tc.raw))
		if ok != tc.ok || got != tc.want {
			t.Fatalf("raw=%s got=(%q,%v) want=(%q,%v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNewEinoOpenAIChatModelValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secrets := secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})
	if _, err := NewEinoOpenAIChatModel(ctx, nil, nil, testConfig("http://example.com/v1")); err == nil {
		t.Fatal("expected secrets required")
	}
	if _, err := NewEinoOpenAIChatModel(ctx, nil, secrets, modelconfig.Config{ModelName: "m"}); err == nil ||
		!strings.Contains(err.Error(), "API base") {
		t.Fatalf("expected API base error, got %v", err)
	}
	if _, err := NewEinoOpenAIChatModel(ctx, nil, secrets, modelconfig.Config{APIBase: "http://example.com/v1"}); err == nil ||
		!strings.Contains(err.Error(), "model name") {
		t.Fatalf("expected model name error, got %v", err)
	}
}

func TestNewEinoOpenAIChatModelWithSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secretID := "019f8f43-5b4d-7ac5-acb2-c74434338e99"
	cfg := testConfig("http://example.com/v1")
	cfg.WorkspaceID = "019f8f43-5b4d-7ac5-acb2-c74434338e97"
	cfg.CredentialSecretID = &secretID
	cfg.Options = json.RawMessage(`{"reasoningEffort":"high"}`)

	var opened bool
	secrets := secretOpenerFunc(func(_ context.Context, workspaceID, id string, use func([]byte) error) error {
		opened = true
		if workspaceID != cfg.WorkspaceID || id != secretID {
			t.Fatalf("secret open ids: workspace=%s id=%s", workspaceID, id)
		}
		return use([]byte("sk-test-key"))
	})
	cm, err := NewEinoOpenAIChatModel(ctx, NewStreamingHTTPClient(), secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cm == nil || !opened {
		t.Fatalf("cm=%v opened=%v", cm, opened)
	}
}

func TestIsAzureProvider(t *testing.T) {
	t.Parallel()
	if !isAzureProvider("Azure_OpenAI") || isAzureProvider("openai") {
		t.Fatal("azure provider detection")
	}
}
