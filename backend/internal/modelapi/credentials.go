package modelapi

import (
	"context"
	"encoding/json"
	"strings"

	"actweave/backend/internal/modelconfig"
)

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
