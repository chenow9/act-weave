package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/identity"
)

func TestIdentitySecurityAcceptanceRejectsCredentialSerialization(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	login := fixture.login(t, authServicePassword)
	credential, err := fixture.repository.GetPasswordCredential(context.Background(), authServiceUserID)
	if err != nil {
		t.Fatalf("get credential for serialization test: %v", err)
	}
	issued, err := NewRefreshTokenManager().Issue()
	if err != nil {
		t.Fatalf("issue serialization-test refresh token: %v", err)
	}

	values := []any{
		login,
		credential,
		identity.NewLocalUser{PasswordHash: credential.PasswordHash},
		identity.CredentialReplacement{PasswordHash: credential.PasswordHash},
		identity.NewAuthSession{RefreshTokenHash: issued.Hash},
		issued,
		AccessToken{Value: login.AccessToken, ExpiresAt: login.AccessTokenExpires},
	}
	for index, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal security value %d: %v", index, err)
		}
		for _, secret := range []string{
			authServicePassword,
			credential.PasswordHash,
			login.AccessToken,
			login.RefreshToken,
			issued.Plaintext,
			issued.Hash,
		} {
			if secret != "" && strings.Contains(string(encoded), secret) {
				t.Fatalf("serialized value %T leaked credential material: %s", value, encoded)
			}
		}
	}
}

func TestIdentitySecurityAcceptanceErrorsDoNotEchoSecrets(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	const wrongPassword = "wrong-password-that-must-not-be-logged"
	_, loginErr := fixture.service.Login(context.Background(), LoginRequest{
		Username: authServiceUsername,
		Password: wrongPassword,
	})
	if !errors.Is(loginErr, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", loginErr)
	}
	const malformedRefresh = "rt1.attacker-controlled-secret"
	_, refreshErr := fixture.service.Refresh(context.Background(), malformedRefresh)
	if !errors.Is(refreshErr, ErrRefreshRejected) {
		t.Fatalf("expected rejected refresh, got %v", refreshErr)
	}
	combined := fmt.Sprint(loginErr, " ", refreshErr)
	for _, secret := range []string{wrongPassword, malformedRefresh} {
		if strings.Contains(combined, secret) {
			t.Fatalf("authentication error echoed secret %q: %s", secret, combined)
		}
	}
}

func TestIdentitySecurityAcceptanceAccessAndRefreshTTLs(t *testing.T) {
	fixture := newAuthServiceFixture(t)
	login := fixture.login(t, authServicePassword)
	if got := login.AccessTokenExpires.Sub(fixture.now); got != DefaultAccessTokenTTL {
		t.Fatalf("expected access TTL %s, got %s", DefaultAccessTokenTTL, got)
	}
	if got := login.RefreshExpiresAt.Sub(fixture.now); got != 7*24*time.Hour {
		t.Fatalf("expected configured refresh TTL, got %s", got)
	}
}
