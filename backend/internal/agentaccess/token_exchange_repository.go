package agentaccess

import (
	"context"
	"encoding/json"

	"actweave/backend/internal/agentaccessauth"
)

// LoadTrustedSubjectIssuerConfig returns the Client trust configuration used by
// Token Exchange. Empty configuration means Token Exchange is not enabled.
func (repository *Repository) LoadTrustedSubjectIssuerConfig(
	ctx context.Context,
	workspaceID, clientID string,
) (TrustedSubjectIssuerConfig, error) {
	client, err := repository.GetClient(ctx, workspaceID, clientID)
	if err != nil {
		return TrustedSubjectIssuerConfig{}, err
	}
	config := clientTrustedSubjectConfig(client)
	if !config.Enabled() {
		return TrustedSubjectIssuerConfig{}, nil
	}
	normalized, err := normalizeTrustedSubjectIssuerConfig(config)
	if err != nil {
		return TrustedSubjectIssuerConfig{}, ErrRepositoryInvalid
	}
	return normalized, nil
}

func (config TrustedSubjectIssuerConfig) AuthConfig() agentaccessauth.TrustedSubjectIssuerConfig {
	return config.ToAuthConfig()
}

// MarshalTrustedSubjectConfigJSON is a helper for tests and diagnostics that
// never includes raw subject token material.
func MarshalTrustedSubjectConfigJSON(config TrustedSubjectIssuerConfig) ([]byte, error) {
	return json.Marshal(config.PublicAuditState())
}
