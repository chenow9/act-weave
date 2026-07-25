package modelapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"actweave/backend/internal/modelconfig"
)

// NewEinoOpenAIChatModel builds a production ToolCallingChatModel using the
// official eino-ext OpenAI adapter (not a hand-rolled Completions client).
//
// Secret handling: the active credential is resolved once at construction and
// passed as APIKey (same pattern as eino-ext examples). BaseURL / Model come
// from modelconfig; HTTPClient should be stream-safe (NewStreamingHTTPClient).
//
// Reasoning: maps modelconfig.Options.reasoningEffort (low|medium|high) onto
// the OpenAI-compatible request. When the option is omitted, defaults to
// high — probed gpt-5.x OpenAI-compatible gateways only emit
// delta/message.reasoning_content at high effort (low/medium often return
// empty reasoning text even when reasoning_tokens may appear). Set
// reasoningEffort to "none"/"off" to skip the field.
//
// Audit "大模型推理" still requires the upstream stream/response to include
// reasoning_content text (effort alone does not always expose the body).
func NewEinoOpenAIChatModel(
	ctx context.Context,
	client *http.Client,
	secrets SecretOpener,
	config modelconfig.Config,
) (model.ToolCallingChatModel, error) {
	if secrets == nil {
		return nil, errors.New("modelapi secrets are required")
	}
	apiBase := strings.TrimSpace(config.APIBase)
	modelName := strings.TrimSpace(config.ModelName)
	if apiBase == "" {
		return nil, errors.New("modelapi API base is required")
	}
	if modelName == "" {
		return nil, errors.New("modelapi model name is required")
	}
	if client == nil {
		client = NewStreamingHTTPClient()
	}

	apiKey, err := resolveAPIKey(ctx, secrets, config)
	if err != nil {
		return nil, err
	}

	cfg := &openai.ChatModelConfig{
		APIKey:     apiKey,
		BaseURL:    apiBase,
		Model:      modelName,
		HTTPClient: client,
	}
	if effort, ok := reasoningEffortFromOptions(config.Options); ok {
		cfg.ReasoningEffort = effort
	}
	// Azure OpenAI when provider is explicitly azure (optional).
	if isAzureProvider(config.Provider) {
		cfg.ByAzure = true
		if ver := azureAPIVersionFromOptions(config.Options); ver != "" {
			cfg.APIVersion = ver
		}
	}

	return openai.NewChatModel(ctx, cfg)
}

func resolveAPIKey(
	ctx context.Context,
	secrets SecretOpener,
	config modelconfig.Config,
) (string, error) {
	if config.CredentialSecretID == nil || strings.TrimSpace(*config.CredentialSecretID) == "" {
		// Some local OpenAI-compatible gateways accept empty keys.
		return "", nil
	}
	var apiKey string
	err := secrets.WithActiveSecret(
		ctx,
		strings.TrimSpace(config.WorkspaceID),
		strings.TrimSpace(*config.CredentialSecretID),
		func(plain []byte) error {
			apiKey = strings.TrimSpace(string(plain))
			return nil
		},
	)
	if err != nil {
		return "", err
	}
	return apiKey, nil
}

// defaultReasoningEffort is applied when modelconfig.Options omits
// reasoningEffort. High is required on some gpt-5-class OpenAI-compatible
// gateways that only surface reasoning_content text at high effort.
const defaultReasoningEffort = openai.ReasoningEffortLevelHigh

func reasoningEffortFromOptions(raw json.RawMessage) (openai.ReasoningEffortLevel, bool) {
	// Missing / null options → default on (see const comment).
	if len(raw) == 0 || string(raw) == "null" {
		return defaultReasoningEffort, true
	}
	var opts struct {
		ReasoningEffort *string `json:"reasoningEffort"`
	}
	if json.Unmarshal(raw, &opts) != nil {
		return defaultReasoningEffort, true
	}
	// Key omitted → default high.
	if opts.ReasoningEffort == nil {
		return defaultReasoningEffort, true
	}
	switch strings.ToLower(strings.TrimSpace(*opts.ReasoningEffort)) {
	case "low":
		return openai.ReasoningEffortLevelLow, true
	case "medium":
		return openai.ReasoningEffortLevelMedium, true
	case "high":
		return openai.ReasoningEffortLevelHigh, true
	case "none", "off", "disabled", "false":
		// Explicit opt-out: do not send reasoning_effort.
		return "", false
	default:
		// Unknown value: keep default so misconfig still surfaces reasoning when possible.
		return defaultReasoningEffort, true
	}
}

func isAzureProvider(provider string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	return p == "azure" || p == "azure_openai" || p == "azure-openai"
}

func azureAPIVersionFromOptions(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var opts struct {
		APIVersion string `json:"apiVersion"`
	}
	if json.Unmarshal(raw, &opts) != nil {
		return ""
	}
	return strings.TrimSpace(opts.APIVersion)
}
