package authn

import (
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/identity"
)

func TestAccessTokenUsesShortTTLAndSessionClaims(t *testing.T) {
	manager, err := NewAccessTokenManager(strings.Repeat("s", 32), "actweave-test", 0)
	if err != nil {
		t.Fatalf("create access token manager: %v", err)
	}
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	token, err := manager.Generate(identity.User{
		ID:           "018f1f2e-7b5a-7c3d-8e9f-5234567890ab",
		Username:     "access.user",
		PlatformRole: identity.PlatformRoleAdmin,
	}, "018f1f2e-7b5a-7c3d-8e9f-5234567890ac", now)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	if !token.ExpiresAt.Equal(now.Add(DefaultAccessTokenTTL)) {
		t.Fatalf("unexpected access token expiry %s", token.ExpiresAt)
	}
	claims, err := manager.Parse(token.Value, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.Subject == "" || claims.SessionID == "" || claims.Username != "access.user" ||
		claims.PlatformRole != string(identity.PlatformRoleAdmin) {
		t.Fatalf("unexpected access token claims: %+v", claims)
	}
	if _, err := manager.Parse(token.Value, token.ExpiresAt.Add(time.Second)); err == nil {
		t.Fatal("expected expired access token to be rejected")
	}
}

func TestAccessTokenRejectsDevelopmentDefaultStrengthSecret(t *testing.T) {
	if _, err := NewAccessTokenManager("actweave-dev-secret", "actweave", 0); err == nil {
		t.Fatal("expected short development secret to be rejected")
	}
}
