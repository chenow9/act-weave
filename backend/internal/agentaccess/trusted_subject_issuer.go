package agentaccess

import (
	"encoding/json"
	"strings"

	"actweave/backend/internal/agentaccessauth"
)

// TrustedSubjectIssuerConfig is the Client-scoped trust configuration stored on
// agent_access_clients. It is deliberately free of raw Subject Tokens.
type TrustedSubjectIssuerConfig struct {
	Issuer      string
	Audience    string
	JWKSURI     string
	InlineJWKS  json.RawMessage
	Algorithms  []string
	ClaimPolicy agentaccessauth.SubjectClaimPolicy
}

func (config TrustedSubjectIssuerConfig) Enabled() bool {
	return strings.TrimSpace(config.Issuer) != "" ||
		strings.TrimSpace(config.Audience) != "" ||
		strings.TrimSpace(config.JWKSURI) != "" ||
		len(config.InlineJWKS) > 0 ||
		len(config.Algorithms) > 0 ||
		config.ClaimPolicy != (agentaccessauth.SubjectClaimPolicy{})
}

func (config TrustedSubjectIssuerConfig) ToAuthConfig() agentaccessauth.TrustedSubjectIssuerConfig {
	return agentaccessauth.TrustedSubjectIssuerConfig{
		Issuer: config.Issuer, Audience: config.Audience, JWKSURI: config.JWKSURI,
		InlineJWKS: append(json.RawMessage(nil), config.InlineJWKS...),
		Algorithms: append([]string(nil), config.Algorithms...), ClaimPolicy: config.ClaimPolicy,
	}
}

func (config TrustedSubjectIssuerConfig) PublicAuditState() map[string]any {
	if !config.Enabled() {
		return map[string]any{"configured": false}
	}
	state := map[string]any{
		"configured": true,
		"issuer":     config.Issuer,
		"audience":   config.Audience,
		"algorithms": append([]string(nil), config.Algorithms...),
		"claimPolicy": map[string]any{
			"subjectClaim":       config.ClaimPolicy.SubjectClaim,
			"requireJti":         config.ClaimPolicy.RequireJTI,
			"maxSubjectBytes":    config.ClaimPolicy.MaxSubjectBytes,
			"maxTokenTTLSeconds": config.ClaimPolicy.MaxTokenTTLSeconds,
		},
	}
	if config.JWKSURI != "" {
		state["jwksSource"] = "uri"
		state["jwksUri"] = config.JWKSURI
	} else if len(config.InlineJWKS) > 0 {
		state["jwksSource"] = "inline"
		// Never include the full JWKS document in audit payloads; only note presence.
		state["inlineJwksPresent"] = true
	}
	return state
}

func normalizeTrustedSubjectIssuerConfig(config TrustedSubjectIssuerConfig) (TrustedSubjectIssuerConfig, error) {
	if !config.Enabled() {
		return TrustedSubjectIssuerConfig{}, nil
	}
	algorithms, err := agentaccessauth.CanonicalSubjectAlgorithms(config.Algorithms)
	if err != nil {
		return TrustedSubjectIssuerConfig{}, err
	}
	claimPolicy := config.ClaimPolicy
	if claimPolicy == (agentaccessauth.SubjectClaimPolicy{}) {
		claimPolicy = agentaccessauth.DefaultSubjectClaimPolicy()
	}
	normalized := TrustedSubjectIssuerConfig{
		Issuer: strings.TrimSpace(config.Issuer), Audience: strings.TrimSpace(config.Audience),
		JWKSURI: strings.TrimSpace(config.JWKSURI),
		InlineJWKS: append(json.RawMessage(nil), config.InlineJWKS...),
		Algorithms: algorithms, ClaimPolicy: claimPolicy,
	}
	if len(normalized.InlineJWKS) > 0 {
		// Canonicalize JSON for stable storage while preserving object content.
		var document map[string]json.RawMessage
		if json.Unmarshal(normalized.InlineJWKS, &document) != nil {
			return TrustedSubjectIssuerConfig{}, agentaccessauth.ErrTrustedSubjectIssuerInvalid
		}
		compact, err := json.Marshal(document)
		if err != nil {
			return TrustedSubjectIssuerConfig{}, agentaccessauth.ErrTrustedSubjectIssuerInvalid
		}
		normalized.InlineJWKS = compact
	}
	if !agentaccessauth.ValidTrustedSubjectIssuerConfig(normalized.ToAuthConfig()) {
		return TrustedSubjectIssuerConfig{}, agentaccessauth.ErrTrustedSubjectIssuerInvalid
	}
	return normalized, nil
}

func clientTrustedSubjectConfig(client AgentAccessClient) TrustedSubjectIssuerConfig {
	return TrustedSubjectIssuerConfig{
		Issuer: client.TrustedSubjectIssuer, Audience: client.TrustedSubjectAudience,
		JWKSURI: client.TrustedSubjectJWKSURI, InlineJWKS: append(json.RawMessage(nil), client.TrustedSubjectInlineJWKS...),
		Algorithms: append([]string(nil), client.TrustedSubjectAlgorithms...),
		ClaimPolicy: client.TrustedSubjectClaimPolicy,
	}
}
