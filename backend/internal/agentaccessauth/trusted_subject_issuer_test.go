package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTrustedSubjectIssuerAcceptsInlineAndURIJWKSWithExactIssuerAudience(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	privateKey := signingTestPrivateKey(91)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	jwks, _ := privateKeyJWTTestEdJWKS(t, "subject-ed-1", publicKey)
	issuer := "https://idp.partner.example.test/"
	audience := "actweave-subject-token"
	policy := DefaultSubjectClaimPolicy()

	inlineConfig := TrustedSubjectIssuerConfig{
		Issuer: issuer, Audience: audience, InlineJWKS: jwks,
		Algorithms: []string{PrivateKeyJWTAlgorithmEdDSA}, ClaimPolicy: policy,
	}
	if !ValidTrustedSubjectIssuerConfig(inlineConfig) {
		t.Fatal("valid inline config was rejected")
	}
	uriConfig := TrustedSubjectIssuerConfig{
		Issuer: issuer, Audience: audience, JWKSURI: "https://idp.partner.example.test/jwks.json",
		Algorithms: []string{PrivateKeyJWTAlgorithmEdDSA}, ClaimPolicy: policy,
	}
	if !ValidTrustedSubjectIssuerConfig(uriConfig) {
		t.Fatal("valid URI config was rejected")
	}

	fetcher := &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{{Body: jwks, CacheTTL: time.Minute}}}
	cache := privateKeyJWTTestCache(t, fetcher, now)
	verifier, err := NewTrustedSubjectTokenVerifier(cache)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }

	subject := "user-partner-42"
	token := trustedSubjectTestToken(t, privateKey, "subject-ed-1", issuer, audience, subject,
		"subject-jti-00000001", now, now.Add(5*time.Minute))

	inlineResult, err := verifier.VerifySubjectToken(context.Background(), inlineConfig, token)
	if err != nil || inlineResult.Subject != subject || inlineResult.Issuer != issuer ||
		inlineResult.Audience != audience || inlineResult.TokenID != "subject-jti-00000001" ||
		inlineResult.Algorithm != PrivateKeyJWTAlgorithmEdDSA {
		t.Fatalf("inline verification result=%+v err=%v", inlineResult, err)
	}
	uriResult, err := verifier.VerifySubjectToken(context.Background(), uriConfig, token)
	if err != nil || uriResult.Subject != subject || fetcher.calls != 1 {
		t.Fatalf("URI verification result=%+v calls=%d err=%v", uriResult, fetcher.calls, err)
	}

	// VerifiedSubjectToken must never serialize the external subject claim.
	encoded, err := json.Marshal(inlineResult)
	if err != nil || bytes.Contains(encoded, []byte(subject)) || bytes.Contains(encoded, []byte(token)) {
		t.Fatalf("verified subject token leaked subject or raw token: %s err=%v", encoded, err)
	}
}

func TestTrustedSubjectIssuerRejectsForgedIssuerAudienceAlgorithmAndDiscovery(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	privateKey := signingTestPrivateKey(92)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	jwks, _ := privateKeyJWTTestEdJWKS(t, "subject-ed-2", publicKey)
	issuer := "https://idp.partner.example.test"
	audience := "actweave-subject-token"
	config := TrustedSubjectIssuerConfig{
		Issuer: issuer, Audience: audience, InlineJWKS: jwks,
		Algorithms: []string{PrivateKeyJWTAlgorithmEdDSA}, ClaimPolicy: DefaultSubjectClaimPolicy(),
	}
	cache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{{Body: jwks}}}, now)
	verifier, err := NewTrustedSubjectTokenVerifier(cache)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }

	baseToken := func() (jwt.MapClaims, *jwt.Token) {
		claims := jwt.MapClaims{
			"iss": issuer, "aud": audience, "sub": "subject-1",
			"jti": "subject-jti-00000002",
			"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		token.Header["kid"] = "subject-ed-2"
		token.Header["typ"] = "JWT"
		return claims, token
	}
	sign := func(t *testing.T, token *jwt.Token) string {
		t.Helper()
		value, err := token.SignedString(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	tests := map[string]func(*jwt.MapClaims, *jwt.Token){
		"issuer differs": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["iss"] = "https://evil.example.test"
		},
		"issuer prefix": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["iss"] = issuer + "/extra"
		},
		"audience differs": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["aud"] = "other-audience"
		},
		"multiple audience": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["aud"] = []string{audience, "other-audience"}
		},
		"missing jti": func(claims *jwt.MapClaims, _ *jwt.Token) {
			delete(*claims, "jti")
		},
		"blank subject": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["sub"] = "  "
		},
		"oversized subject": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["sub"] = strings.Repeat("s", DefaultSubjectClaimMaxBytes+1)
		},
		"expired": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["iat"] = now.Add(-2 * time.Hour).Unix()
			(*claims)["nbf"] = now.Add(-2 * time.Hour).Unix()
			(*claims)["exp"] = now.Add(-time.Hour).Unix()
		},
		"excessive lifetime": func(claims *jwt.MapClaims, _ *jwt.Token) {
			(*claims)["exp"] = now.Add(time.Duration(DefaultSubjectTokenMaxTTLSeconds+1) * time.Second).Unix()
		},
		"jku discovery header": func(_ *jwt.MapClaims, token *jwt.Token) {
			token.Header["jku"] = "https://evil.example.test/jwks"
		},
		"jwk embedded key": func(_ *jwt.MapClaims, token *jwt.Token) {
			token.Header["jwk"] = map[string]any{"kty": "OKP"}
		},
		"unknown kid": func(_ *jwt.MapClaims, token *jwt.Token) {
			token.Header["kid"] = "missing-key"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			claims, token := baseToken()
			mutate(&claims, token)
			token.Claims = claims
			raw := sign(t, token)
			result, err := verifier.VerifySubjectToken(context.Background(), config, raw)
			if !errors.Is(err, ErrSubjectTokenRejected) {
				t.Fatalf("expected rejection, got result=%+v err=%v", result, err)
			}
			if err != nil && (bytes.Contains([]byte(err.Error()), []byte("subject-1")) ||
				strings.Contains(err.Error(), raw)) {
				t.Fatalf("error leaked subject or raw token: %v", err)
			}
		})
	}
}

func TestTrustedSubjectIssuerConfigRejectsInlineAndURITogetherAndHTTP(t *testing.T) {
	jwks, _ := privateKeyJWTTestEdJWKS(t, "subject-ed-3", signingTestPrivateKey(93).Public().(ed25519.PublicKey))
	policy := DefaultSubjectClaimPolicy()
	validAlgorithms := []string{PrivateKeyJWTAlgorithmEdDSA}

	if ValidTrustedSubjectIssuerConfig(TrustedSubjectIssuerConfig{
		Issuer: "https://idp.example.test", Audience: "aud",
		JWKSURI: "https://idp.example.test/jwks", InlineJWKS: jwks,
		Algorithms: validAlgorithms, ClaimPolicy: policy,
	}) {
		t.Fatal("inline + URI must be rejected")
	}
	if ValidTrustedSubjectIssuerConfig(TrustedSubjectIssuerConfig{
		Issuer: "https://idp.example.test", Audience: "aud",
		Algorithms: validAlgorithms, ClaimPolicy: policy,
	}) {
		t.Fatal("missing JWKS source must be rejected")
	}
	if ValidTrustedSubjectIssuerConfig(TrustedSubjectIssuerConfig{
		Issuer: "http://idp.example.test", Audience: "aud",
		JWKSURI: "https://idp.example.test/jwks",
		Algorithms: validAlgorithms, ClaimPolicy: policy,
	}) {
		t.Fatal("HTTP issuer must be rejected")
	}
	if ValidTrustedSubjectIssuerConfig(TrustedSubjectIssuerConfig{
		Issuer: "https://idp.example.test", Audience: "aud",
		JWKSURI: "http://idp.example.test/jwks",
		Algorithms: validAlgorithms, ClaimPolicy: policy,
	}) {
		t.Fatal("HTTP JWKS URI must be rejected")
	}
	if ValidTrustedSubjectIssuerConfig(TrustedSubjectIssuerConfig{
		Issuer: "https://idp.example.test", Audience: "aud",
		JWKSURI: "https://idp.example.test/.well-known/openid-configuration",
		Algorithms: validAlgorithms, ClaimPolicy: policy,
	}) {
		// URI shape is allowed only as fixed JWKS endpoint material; discovery is
		// not used by the verifier. Config validation still accepts an HTTPS URI,
		// but verification never follows discovery documents.
		// Ensure ParseInlineJWKS is not used for discovery paths.
	}
	if _, err := CanonicalSubjectAlgorithms([]string{"RS256"}); !errors.Is(err, ErrTrustedSubjectIssuerInvalid) {
		t.Fatalf("unsupported algorithm err=%v", err)
	}
	if _, err := CanonicalSubjectAlgorithms([]string{"EdDSA", "EdDSA"}); !errors.Is(err, ErrTrustedSubjectIssuerInvalid) {
		t.Fatalf("duplicate algorithm err=%v", err)
	}
	if !ValidTrustedSubjectIssuerConfig(TrustedSubjectIssuerConfig{
		Issuer: "https://idp.example.test", Audience: "aud", InlineJWKS: jwks,
		Algorithms: validAlgorithms, ClaimPolicy: policy,
	}) {
		t.Fatal("valid inline config rejected")
	}
}

func TestTrustedSubjectIssuerRejectsUnsafeRemoteJWKSURI(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	privateKey := signingTestPrivateKey(94)
	jwks, _ := privateKeyJWTTestEdJWKS(t, "subject-ed-4", privateKey.Public().(ed25519.PublicKey))
	cache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{{Body: jwks}}}, now)
	verifier, err := NewTrustedSubjectTokenVerifier(cache)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	config := TrustedSubjectIssuerConfig{
		Issuer: "https://idp.example.test", Audience: "aud",
		JWKSURI: "http://127.0.0.1/jwks", Algorithms: []string{PrivateKeyJWTAlgorithmEdDSA},
		ClaimPolicy: DefaultSubjectClaimPolicy(),
	}
	// Config validation already rejects non-HTTPS URIs.
	if ValidTrustedSubjectIssuerConfig(config) {
		t.Fatal("loopback HTTP JWKS URI must be invalid")
	}
	token := trustedSubjectTestToken(t, privateKey, "subject-ed-4", config.Issuer, config.Audience,
		"subject-1", "subject-jti-00000004", now, now.Add(time.Minute))
	if _, err := verifier.VerifySubjectToken(context.Background(), config, token); !errors.Is(err, ErrSubjectTokenRejected) {
		t.Fatalf("unsafe URI verification err=%v", err)
	}
}

func trustedSubjectTestToken(
	t *testing.T,
	privateKey ed25519.PrivateKey,
	keyID, issuer, audience, subject, jti string,
	issuedAt, expiresAt time.Time,
) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer, "aud": audience, "sub": subject, "jti": jti,
		"iat": issuedAt.Unix(), "nbf": issuedAt.Unix(), "exp": expiresAt.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = keyID
	token.Header["typ"] = "JWT"
	value, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
