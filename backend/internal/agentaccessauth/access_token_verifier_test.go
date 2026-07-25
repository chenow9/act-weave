package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAAPAuthenticationMiddlewareVerifierAcceptsOnlyAAPAccessTokenProfile(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x71}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("aap-verifier-key-1", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewAAPAccessTokenVerifier(keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	claims := aapVerifierTestClaims(now)
	value := signAAPVerifierClaims(t, keys, claims, now)
	principal, err := verifier.VerifyAccessToken(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	if principal.PrincipalID != claims.Subject || principal.ServicePrincipalID != claims.Subject ||
		principal.AuthorizedParty != claims.AuthorizedParty || principal.WorkspaceID != claims.WorkspaceID ||
		principal.AgentID != claims.AgentID || principal.SecurityVersion != claims.SecurityVersion ||
		principal.TokenID != claims.ID || !principal.IssuedAt.Equal(now) ||
		!principal.NotBefore.Equal(now.Add(-DefaultTokenClockSkew)) ||
		!principal.ExpiresAt.Equal(now.Add(10*time.Minute)) ||
		!principal.HasScope("run:create") || !principal.HasScope("event:read") ||
		principal.HasScope("interaction:decide") {
		t.Fatalf("unexpected AAP Principal: %+v", principal)
	}
	principal.Scopes[0] = "mutated"
	again, err := verifier.VerifyAccessToken(context.Background(), value)
	if err != nil || again.Scopes[0] != "run:create" {
		t.Fatalf("Principal scope must be an isolated copy: %+v err=%v", again, err)
	}
}

func TestAAPDataPlaneAcceptanceTokenAudienceIsolation(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x79}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("aap-data-plane-acceptance", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewAAPAccessTokenVerifier(
		keys, "https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	claims := aapVerifierTestClaims(now)
	if _, err := verifier.VerifyAccessToken(
		context.Background(), signAAPVerifierClaims(t, keys, claims, now),
	); err != nil {
		t.Fatalf("AAP profile token rejected: %v", err)
	}
	claims.Audience = jwt.ClaimStrings{"actweave-user-api"}
	if _, err := verifier.VerifyAccessToken(
		context.Background(), signAAPVerifierClaims(t, keys, claims, now),
	); !errors.Is(err, ErrInvalidAAPAccessToken) {
		t.Fatalf("old user audience accepted: %v", err)
	}
}

func TestAAPAuthenticationMiddlewareVerifierRejectsJWTBCPAttackMatrix(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x72}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("aap-verifier-key-2", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewAAPAccessTokenVerifier(keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	base := aapVerifierTestClaims(now)
	tests := map[string]func(*AAPAccessTokenClaims){
		"wrong issuer":     func(value *AAPAccessTokenClaims) { value.Issuer = "https://evil.example.test/oauth" },
		"missing audience": func(value *AAPAccessTokenClaims) { value.Audience = nil },
		"wrong audience":   func(value *AAPAccessTokenClaims) { value.Audience = jwt.ClaimStrings{"actweave-user-api"} },
		"multiple audiences": func(value *AAPAccessTokenClaims) {
			value.Audience = jwt.ClaimStrings{AAPAccessTokenAudience, "actweave-user-api"}
		},
		"missing subject":          func(value *AAPAccessTokenClaims) { value.Subject = "" },
		"invalid authorized party": func(value *AAPAccessTokenClaims) { value.AuthorizedParty = "client-admin" },
		"invalid Workspace":        func(value *AAPAccessTokenClaims) { value.WorkspaceID = "not-a-uuid" },
		"invalid Agent":            func(value *AAPAccessTokenClaims) { value.AgentID = "not-a-uuid" },
		"missing JTI":              func(value *AAPAccessTokenClaims) { value.ID = "" },
		"zero security version":    func(value *AAPAccessTokenClaims) { value.SecurityVersion = 0 },
		"unknown scope":            func(value *AAPAccessTokenClaims) { value.Scope = "workspace:manage" },
		"noncanonical scope order": func(value *AAPAccessTokenClaims) { value.Scope = "event:read run:create" },
		"duplicate scope":          func(value *AAPAccessTokenClaims) { value.Scope = "run:create run:create" },
		"missing issued at":        func(value *AAPAccessTokenClaims) { value.IssuedAt = nil },
		"missing not before":       func(value *AAPAccessTokenClaims) { value.NotBefore = nil },
		"missing expiration":       func(value *AAPAccessTokenClaims) { value.ExpiresAt = nil },
		"future issued at": func(value *AAPAccessTokenClaims) {
			issued := now.Add(DefaultTokenClockSkew + time.Second)
			value.IssuedAt = jwt.NewNumericDate(issued)
			value.NotBefore = jwt.NewNumericDate(issued.Add(-DefaultTokenClockSkew))
			value.ExpiresAt = jwt.NewNumericDate(issued.Add(10 * time.Minute))
		},
		"future not before": func(value *AAPAccessTokenClaims) {
			value.NotBefore = jwt.NewNumericDate(now.Add(DefaultTokenClockSkew + time.Second))
		},
		"not before precedes allowed skew": func(value *AAPAccessTokenClaims) {
			value.NotBefore = jwt.NewNumericDate(now.Add(-DefaultTokenClockSkew - time.Second))
		},
		"short lifetime": func(value *AAPAccessTokenClaims) {
			value.ExpiresAt = jwt.NewNumericDate(now.Add(MinimumAccessTokenTTL - time.Second))
		},
		"excessive lifetime": func(value *AAPAccessTokenClaims) {
			value.ExpiresAt = jwt.NewNumericDate(now.Add(DefaultMaxAccessTokenTTL + time.Second))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			claims := base
			mutate(&claims)
			value := signAAPVerifierClaims(t, keys, claims, now)
			if _, err := verifier.VerifyAccessToken(context.Background(), value); !errors.Is(err, ErrInvalidAAPAccessToken) {
				t.Fatalf("error=%v want invalid AAP access token", err)
			}
		})
	}

	expired := base
	expired.IssuedAt = jwt.NewNumericDate(now.Add(-10 * time.Minute))
	expired.NotBefore = jwt.NewNumericDate(now.Add(-10*time.Minute - DefaultTokenClockSkew))
	expired.ExpiresAt = jwt.NewNumericDate(now)
	if _, err := verifier.VerifyAccessToken(context.Background(),
		signAAPVerifierClaims(t, keys, expired, now)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token error=%v", err)
	}

	userJWT := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "actweave", Subject: base.Subject,
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
	})
	userJWT.Header["typ"], userJWT.Header["kid"] = AAPAccessTokenType, "aap-verifier-key-2"
	userValue, err := userJWT.SignedString(bytes.Repeat([]byte{0x73}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyAccessToken(context.Background(), userValue); !errors.Is(err, ErrInvalidAAPAccessToken) {
		t.Fatalf("HS256 user/algorithm-confusion token error=%v", err)
	}

	for name, mutateHeader := range map[string]func(map[string]any){
		"missing typ":         func(header map[string]any) { delete(header, "typ") },
		"wrong typ":           func(header map[string]any) { header["typ"] = "JWT" },
		"unknown kid":         func(header map[string]any) { header["kid"] = "unknown-aap-key" },
		"embedded JWK header": func(header map[string]any) { header["jwk"] = map[string]any{"kty": "OKP"} },
	} {
		t.Run(name, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, base)
			token.Header["typ"], token.Header["kid"] = AAPAccessTokenType, "aap-verifier-key-2"
			mutateHeader(token.Header)
			value, err := token.SignedString(privateKey)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.VerifyAccessToken(context.Background(), value); !errors.Is(err, ErrInvalidAAPAccessToken) {
				t.Fatalf("header attack error=%v", err)
			}
		})
	}

	valid := signAAPVerifierClaims(t, keys, base, now)
	for name, value := range map[string]string{
		"leading whitespace": " " + valid,
		"malformed compact":  "not.a.jwt.extra",
		"oversized":          strings.Repeat("a", maximumAAPAccessTokenBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.VerifyAccessToken(context.Background(), value); !errors.Is(err, ErrInvalidAAPAccessToken) {
				t.Fatalf("malformed token error=%v", err)
			}
		})
	}
}

func TestAAPAuthenticationMiddlewareVerifierSupportsPublishedRotationKeys(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	oldKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x74}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("aap-old-key", oldKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims := aapVerifierTestClaims(now)
	oldToken := signAAPVerifierClaims(t, keys, claims, now)
	if err := keys.Rotate("aap-new-key",
		ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x75}, ed25519.SeedSize)), now); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewAAPAccessTokenVerifier(keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now.Add(9 * time.Minute) }
	if _, err := verifier.VerifyAccessToken(context.Background(), oldToken); err != nil {
		t.Fatalf("old token must verify during published-key retention: %v", err)
	}
}

func aapVerifierTestClaims(now time.Time) AAPAccessTokenClaims {
	return AAPAccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   "https://api.example.test/api/agent-access/v1/oauth",
			Subject:  "f48f1f2e-7b5a-7c3d-8e9f-123456789001",
			Audience: jwt.ClaimStrings{AAPAccessTokenAudience},
			ID:       "f48f1f2e-7b5a-7c3d-8e9f-123456789002",
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-DefaultTokenClockSkew)),
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		},
		AuthorizedParty: clientSecretTestPublicID(91),
		WorkspaceID:     "f48f1f2e-7b5a-7c3d-8e9f-123456789003",
		AgentID:         "f48f1f2e-7b5a-7c3d-8e9f-123456789004",
		Scope:           "run:create event:read", SecurityVersion: 11,
	}
}

func signAAPVerifierClaims(
	t *testing.T,
	keys SigningKeyProvider,
	claims AAPAccessTokenClaims,
	now time.Time,
) string {
	t.Helper()
	key, err := keys.ActiveSigningKey(now)
	if err != nil {
		t.Fatal(err)
	}
	value, err := key.SignAccessToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
