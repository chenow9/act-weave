package agentaccessauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSigningKeysUseFixedEdDSAKeyIDAndAccessTokenType(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	privateKey := signingTestPrivateKey(1)
	provider, err := NewRotatingSigningKeyProvider("aap-2026-07-a", privateKey, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	key, err := provider.ActiveSigningKey(now)
	if err != nil {
		t.Fatal(err)
	}
	value, err := key.SignAccessToken(jwt.MapClaims{
		"iss": "https://actweave.example/api/agent-access/v1/oauth",
		"sub": "service-principal", "exp": now.Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(value, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA {
			t.Fatalf("unexpected signing method %s", token.Method.Alg())
		}
		if token.Header["alg"] != AAPSigningAlgorithm || token.Header["typ"] != AAPAccessTokenType ||
			token.Header["kid"] != "aap-2026-07-a" {
			t.Fatalf("unexpected protected header: %+v", token.Header)
		}
		return key.PublicKey(), nil
	}, jwt.WithValidMethods([]string{AAPSigningAlgorithm}), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || !parsed.Valid {
		t.Fatalf("verify signed access token: valid=%t err=%v", parsed != nil && parsed.Valid, err)
	}
}

func TestSigningKeysRotationRetainsOldPublicKeyForMaximumTTL(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	provider, err := NewRotatingSigningKeyProvider(
		"aap-old", signingTestPrivateKey(2), DefaultMaxAccessTokenTTL,
		PublishedVerificationKey{KeyID: "aap-next", PublicKey: signingTestPrivateKey(3).Public().(ed25519.PublicKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Rotate("aap-next", signingTestPrivateKey(3), now); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(DefaultMaxAccessTokenTTL), now.Add(DefaultMaxAccessTokenTTL + 4*time.Second)} {
		if _, err := provider.VerificationKey("aap-old", at); err != nil {
			t.Fatalf("old key disappeared before retention window at %s: %v", at, err)
		}
	}
	if _, err := provider.VerificationKey("aap-old", now.Add(DefaultMaxAccessTokenTTL+DefaultTokenClockSkew)); !errors.Is(err, ErrSigningKeyNotFound) {
		t.Fatalf("old key must expire after maximum TTL and skew, got %v", err)
	}
	active, err := provider.ActiveSigningKey(now)
	if err != nil || active.KeyID() != "aap-next" {
		t.Fatalf("unexpected active key: id=%q err=%v", active.KeyID(), err)
	}
}

func TestSigningKeysLoadFromProtectedFileAndGenerateOnlyWhenExplicit(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	privatePath := filepath.Join(t.TempDir(), "keys", "aap-private.pem")
	provider, err := LoadFileSigningKeyProvider(FileSigningKeyConfig{
		Algorithm: AAPSigningAlgorithm, ActiveKeyID: "generated-local-key",
		PrivateKeyFile: privatePath, GenerateIfMissing: true, MaxTokenTTL: 10 * time.Minute,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("generated private key mode=%#o want 0600", info.Mode().Perm())
	}
	first, _ := provider.ActiveSigningKey(now)
	reloaded, err := LoadFileSigningKeyProvider(FileSigningKeyConfig{
		Algorithm: AAPSigningAlgorithm, ActiveKeyID: "generated-local-key",
		PrivateKeyFile: privatePath, MaxTokenTTL: 10 * time.Minute,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := reloaded.ActiveSigningKey(now)
	if !first.PublicKey().Equal(second.PublicKey()) {
		t.Fatal("reloading the private key file changed the public key")
	}
	if _, err := LoadFileSigningKeyProvider(FileSigningKeyConfig{
		Algorithm: AAPSigningAlgorithm, ActiveKeyID: "missing-key",
		PrivateKeyFile: filepath.Join(t.TempDir(), "missing.pem"), MaxTokenTTL: 10 * time.Minute,
	}, now); err == nil {
		t.Fatal("missing production key must not be generated implicitly")
	}
}

func TestJWKSContainsOnlySortedPublicEd25519Keys(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	privateKey := signingTestPrivateKey(4)
	provider, err := NewRotatingSigningKeyProvider(
		"key-z", privateKey, 10*time.Minute,
		PublishedVerificationKey{KeyID: "key-a", PublicKey: signingTestPrivateKey(5).Public().(ed25519.PublicKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	set, err := provider.PublicJWKS(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 2 || set.Keys[0].KeyID != "key-a" || set.Keys[1].KeyID != "key-z" {
		t.Fatalf("JWKS keys are not stable and sorted: %+v", set.Keys)
	}
	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	for _, required := range []string{`"kty":"OKP"`, `"crv":"Ed25519"`, `"use":"sig"`, `"alg":"EdDSA"`, `"kid":"key-z"`} {
		if !strings.Contains(value, required) {
			t.Errorf("JWKS missing %s: %s", required, value)
		}
	}
	if strings.Contains(value, `"d"`) || bytes.Contains(encoded, privateKey.Seed()) {
		t.Fatalf("JWKS exposed private key material: %s", value)
	}
}

func TestJWKSRetiredFileMustCoverMaximumTokenLifetime(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	activePath := filepath.Join(directory, "active.pem")
	retiredPath := filepath.Join(directory, "retired.pub.pem")
	writeSigningTestPrivatePEM(t, activePath, signingTestPrivateKey(6))
	writeSigningTestPublicPEM(t, retiredPath, signingTestPrivateKey(7).Public().(ed25519.PublicKey))
	_, err := LoadFileSigningKeyProvider(FileSigningKeyConfig{
		Algorithm: AAPSigningAlgorithm, ActiveKeyID: "active", PrivateKeyFile: activePath,
		MaxTokenTTL: DefaultMaxAccessTokenTTL,
		RetiredPublicKeys: []RetiredPublicKeyFile{{
			KeyID: "retired", PublicKeyFile: retiredPath, RetiredAt: now,
			PublishUntil: now.Add(DefaultMaxAccessTokenTTL),
		}},
	}, now)
	if err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("short retired key retention must fail, got %v", err)
	}
}

func signingTestPrivateKey(fill byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{fill}, ed25519.SeedSize))
}

func writeSigningTestPrivatePEM(t *testing.T, path string, key ed25519.PrivateKey) {
	t.Helper()
	value, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: value}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSigningTestPublicPEM(t *testing.T, path string, key ed25519.PublicKey) {
	t.Helper()
	value, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: value}), 0o600); err != nil {
		t.Fatal(err)
	}
}
