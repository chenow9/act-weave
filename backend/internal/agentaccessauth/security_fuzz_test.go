package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func FuzzClientSecretAuthenticationParsers(f *testing.F) {
	publicClientID := "awcl_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 32))
	credentialID := "f98f1f2e-7b5a-7c3d-8e9f-123456789001"
	secret := "awsk_live_" + credentialID + "_" +
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x32}, 32))
	validBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte(publicClientID+":"+secret))
	for _, seed := range []string{
		validBasic, "", "Bearer token", "Basic !!!", "Basic " +
			base64.StdEncoding.EncodeToString([]byte("missing-colon")),
		"Basic " + base64.StdEncoding.EncodeToString([]byte("%zz:"+secret)),
		strings.Repeat("A", maximumBasicAuthorizationSize+1), secret,
		"awsk_live_NOT-A-UUID_" + strings.Repeat("A", 43),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		clientID, presented, err := parseClientSecretBasic(value)
		if err == nil {
			if !validPublicClientID(clientID) || presented == "" {
				t.Fatalf("parser accepted invalid Basic result client=%q secretLen=%d", clientID, len(presented))
			}
		}
		parsedCredentialID, secretErr := parsePresentedClientSecret(value)
		if secretErr == nil {
			if parsedCredentialID == "" || !strings.Contains(value, parsedCredentialID) {
				t.Fatalf("parser accepted inconsistent Client Secret: credential=%q", parsedCredentialID)
			}
		}
	})
}

func FuzzUnverifiedPrivateKeyJWTParser(f *testing.F) {
	publicClientID := "awcl_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 32))
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	claims := jwt.RegisteredClaims{
		Issuer: publicClientID, Subject: publicClientID,
		Audience: jwt.ClaimStrings{"https://api.example.test/api/agent-access/v1/oauth/token"},
		ID:       "private-key-jwt-seed-0001", IssuedAt: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"], token.Header["typ"] = "fuzz-key-1", "JWT"
	signingInput, err := token.SigningString()
	if err != nil {
		f.Fatal(err)
	}
	validShape := signingInput + ".c2lnbmF0dXJl"
	for _, seed := range []string{
		validShape, "", "not.a.jwt.extra", "e30.e30.",
		"eyJhbGciOiJub25lIiwia2lkIjoiZnV6eiJ9.e30.",
		"eyJhbGciOiJSUzI1NiIsImtpZCI6ImZ1enoiLCJqa3UiOiJodHRwczovL2V2aWwudGVzdC9rZXlzIn0.e30.c2ln",
		strings.Repeat("a", maximumClientAssertionBytes+1),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, assertion string) {
		claims, keyID, algorithm, err := parseUnverifiedClientAssertion(assertion)
		if err == nil {
			if claims.Issuer != claims.Subject || !validPublicClientID(claims.Issuer) ||
				!validRemoteJWKKeyID(keyID) || !allowedPrivateKeyJWTAlgorithm(algorithm) {
				t.Fatalf("accepted inconsistent assertion iss=%q sub=%q kid=%q alg=%q",
					claims.Issuer, claims.Subject, keyID, algorithm)
			}
		}
	})
}

func FuzzAAPAccessTokenVerifier(f *testing.F) {
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x51}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("fuzz-aap-key-1", privateKey, 15*time.Minute)
	if err != nil {
		f.Fatal(err)
	}
	verifier, err := NewAAPAccessTokenVerifier(
		keys, "https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute,
	)
	if err != nil {
		f.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	claims := aapVerifierTestClaims(now)
	validToken := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	validToken.Header["typ"], validToken.Header["kid"] = AAPAccessTokenType, "fuzz-aap-key-1"
	valid, err := validToken.SignedString(privateKey)
	if err != nil {
		f.Fatal(err)
	}
	userToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	userToken.Header["typ"], userToken.Header["kid"] = AAPAccessTokenType, "fuzz-aap-key-1"
	hsValue, err := userToken.SignedString(bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{
		valid, hsValue, "", " not.a.token", "not.a.token", "not.a.token.extra",
		strings.Repeat("a", maximumAAPAccessTokenBytes+1),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		principal, err := verifier.VerifyAccessToken(context.Background(), value)
		if err == nil {
			if !validAuthorizationPrincipal(principal) || principal.AuthorizedParty == "" ||
				principal.WorkspaceID == "" || principal.AgentID == "" || len(principal.Scopes) == 0 {
				t.Fatalf("Verifier returned invalid Principal: %+v", principal)
			}
			return
		}
		if !errors.Is(err, ErrInvalidAAPAccessToken) && !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("Verifier exposed unstable error: %v", err)
		}
	})
}

func FuzzAuthorizationScopeCanonicalization(f *testing.F) {
	for _, seed := range []string{
		"run:create event:read", "event:read run:create", "run:create run:create",
		"workspace:manage", "", "run:create\x00event:read", strings.Repeat("run:create ", 32),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		scopes, canonical, err := canonicalizeRequestedScopes(strings.Split(value, " "))
		if err != nil {
			return
		}
		again, againCanonical, err := canonicalizeRequestedScopes(scopes)
		if err != nil || canonical != againCanonical || len(scopes) != len(again) {
			t.Fatalf("Scope canonicalization is not idempotent: %q -> %q -> %q err=%v",
				value, canonical, againCanonical, err)
		}
	})
}
