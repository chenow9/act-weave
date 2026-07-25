package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const privateKeyJWTTestEndpoint = "https://actweave.example.test/api/agent-access/v1/oauth/token"

func TestPrivateKeyJWTAuthenticationAcceptsEdDSAAndRejectsReplay(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	publicClientID := clientSecretTestPublicID(21)
	privateKey := signingTestPrivateKey(22)
	jwks, verificationKey := privateKeyJWTTestEdJWKS(t, "client-ed-1", privateKey.Public().(ed25519.PublicKey))
	fetcher := &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{{Body: jwks, CacheTTL: time.Minute}}}
	cache := privateKeyJWTTestCache(t, fetcher, now)
	store := &privateKeyJWTTestClientStore{client: privateKeyJWTTestClient(now, publicClientID, verificationKey)}
	replays, _ := NewInMemoryClientAssertionJTIStore(100)
	audit := &privateKeyJWTTestAudit{}
	authenticator, err := NewPrivateKeyJWTAuthenticator(store, cache, replays, privateKeyJWTTestEndpoint,
		WithPrivateKeyJWTAuthenticationAudit(audit))
	if err != nil {
		t.Fatal(err)
	}
	authenticator.now = func() time.Time { return now }
	assertion := privateKeyJWTTestAssertion(t, privateKey, "client-ed-1", publicClientID,
		jwt.ClaimStrings{privateKeyJWTTestEndpoint}, "assertion-jti-0001", now, now.Add(2*time.Minute))
	request := PrivateKeyJWTAuthenticationRequest{
		ClientAssertion: assertion, SourceIP: "203.0.113.31", UserAgent: "partner-key-client/1.0",
	}
	result, err := authenticator.Authenticate(context.Background(), request)
	if err != nil || result.ClientID != store.client.ClientID || result.CredentialID != "credential-client-ed-1" ||
		result.ServicePrincipalVersion != 9 || store.markCalls != 1 {
		t.Fatalf("authenticated Client=%+v marks=%d err=%v", result, store.markCalls, err)
	}
	if _, err := authenticator.Authenticate(context.Background(), request); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("replayed assertion err=%v", err)
	}
	if len(audit.failures) != 1 || audit.failures[0].ErrorCode != FailureClientAssertionReplay {
		t.Fatalf("replay audit=%+v", audit.failures)
	}
	encoded, _ := json.Marshal(audit.failures)
	if bytes.Contains(encoded, []byte(assertion)) {
		t.Fatalf("authentication audit leaked Client Assertion: %s", encoded)
	}
}

func TestPrivateKeyJWTAuthenticationAcceptsPS256WithRegisteredThumbprint(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicClientID := clientSecretTestPublicID(23)
	jwks, verificationKey := privateKeyJWTTestRSAJWKS(t, "client-rsa-1", &privateKey.PublicKey)
	cache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{
		results: []RemoteJWKSFetchResult{{Body: jwks}},
	}, now)
	store := &privateKeyJWTTestClientStore{client: privateKeyJWTTestClient(now, publicClientID, verificationKey)}
	replays, _ := NewInMemoryClientAssertionJTIStore(100)
	authenticator, err := NewPrivateKeyJWTAuthenticator(store, cache, replays, privateKeyJWTTestEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	authenticator.now = func() time.Time { return now }
	assertion := privateKeyJWTTestAssertion(t, privateKey, "client-rsa-1", publicClientID,
		jwt.ClaimStrings{privateKeyJWTTestEndpoint}, "assertion-jti-rsa-1", now, now.Add(2*time.Minute))
	if result, err := authenticator.Authenticate(context.Background(), PrivateKeyJWTAuthenticationRequest{
		ClientAssertion: assertion,
	}); err != nil || result.CredentialID != "credential-client-rsa-1" {
		t.Fatalf("PS256 authenticated Client=%+v err=%v", result, err)
	}
}

func TestPrivateKeyJWTAuthenticationRejectsClaimAlgorithmAndKeyConfusion(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	publicClientID := clientSecretTestPublicID(24)
	privateKey := signingTestPrivateKey(25)
	jwks, verificationKey := privateKeyJWTTestEdJWKS(t, "client-ed-2", privateKey.Public().(ed25519.PublicKey))
	baseClient := privateKeyJWTTestClient(now, publicClientID, verificationKey)
	revokedAt := now.Add(-time.Minute)
	credentialExpiredAt := now
	tests := map[string]func(*jwt.RegisteredClaims, *jwt.Token, *PrivateKeyJWTClient){
		"subject differs": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) { claims.Subject = "different" },
		"audience differs": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) {
			claims.Audience = jwt.ClaimStrings{"https://other.example.test/token"}
		},
		"multiple audience": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) {
			claims.Audience = jwt.ClaimStrings{privateKeyJWTTestEndpoint, "https://other.example.test/token"}
		},
		"missing JTI": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) { claims.ID = "" },
		"excessive lifetime": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) {
			claims.ExpiresAt = jwt.NewNumericDate(now.Add(DefaultClientAssertionMaxTTL + time.Second))
		},
		"expired assertion": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) {
			claims.IssuedAt = jwt.NewNumericDate(now.Add(-2 * time.Minute))
			claims.NotBefore = jwt.NewNumericDate(now.Add(-2 * time.Minute))
			claims.ExpiresAt = jwt.NewNumericDate(now.Add(-DefaultTokenClockSkew - time.Second))
		},
		"future not before": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) {
			claims.NotBefore = jwt.NewNumericDate(now.Add(DefaultTokenClockSkew + time.Second))
		},
		"future issued at": func(claims *jwt.RegisteredClaims, _ *jwt.Token, _ *PrivateKeyJWTClient) {
			claims.IssuedAt = jwt.NewNumericDate(now.Add(DefaultTokenClockSkew + time.Second))
			claims.ExpiresAt = jwt.NewNumericDate(now.Add(time.Minute))
		},
		"forbidden JWK header": func(_ *jwt.RegisteredClaims, token *jwt.Token, _ *PrivateKeyJWTClient) {
			token.Header["jwk"] = map[string]any{"kty": "OKP"}
		},
		"disabled Client": func(_ *jwt.RegisteredClaims, _ *jwt.Token, client *PrivateKeyJWTClient) { client.ClientActive = false },
		"revoked credential": func(_ *jwt.RegisteredClaims, _ *jwt.Token, client *PrivateKeyJWTClient) {
			client.Credentials[0].RevokedAt = &revokedAt
		},
		"expired credential": func(_ *jwt.RegisteredClaims, _ *jwt.Token, client *PrivateKeyJWTClient) {
			client.Credentials[0].ExpiresAt = &credentialExpiredAt
		},
		"thumbprint mismatch": func(_ *jwt.RegisteredClaims, _ *jwt.Token, client *PrivateKeyJWTClient) {
			client.Credentials[0].JWKThumbprint = bytes.Repeat([]byte{0xff}, sha256.Size)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			client := baseClient
			client.Credentials = append([]PrivateKeyJWTCredential(nil), baseClient.Credentials...)
			client.Credentials[0].JWKThumbprint = append([]byte(nil), baseClient.Credentials[0].JWKThumbprint...)
			claims := privateKeyJWTTestClaims(publicClientID, jwt.ClaimStrings{privateKeyJWTTestEndpoint},
				"assertion-jti-matrix-1", now, now.Add(2*time.Minute))
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, &claims)
			token.Header["kid"], token.Header["typ"] = "client-ed-2", "JWT"
			mutate(&claims, token, &client)
			assertion, err := token.SignedString(privateKey)
			if err != nil {
				t.Fatal(err)
			}
			cache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{
				results: []RemoteJWKSFetchResult{{Body: jwks}},
			}, now)
			replays, _ := NewInMemoryClientAssertionJTIStore(100)
			authenticator, err := NewPrivateKeyJWTAuthenticator(
				&privateKeyJWTTestClientStore{client: client}, cache, replays, privateKeyJWTTestEndpoint,
			)
			if err != nil {
				t.Fatal(err)
			}
			authenticator.now = func() time.Time { return now }
			if _, err := authenticator.Authenticate(context.Background(), PrivateKeyJWTAuthenticationRequest{
				ClientAssertion: assertion,
			}); !errors.Is(err, ErrInvalidClient) {
				t.Fatalf("rejected assertion err=%v", err)
			}
		})
	}

	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256,
		privateKeyJWTTestClaims(publicClientID, jwt.ClaimStrings{privateKeyJWTTestEndpoint},
			"assertion-jti-hmac-1", now, now.Add(time.Minute)))
	hmacToken.Header["kid"] = "client-ed-2"
	hmacAssertion, _ := hmacToken.SignedString([]byte("attacker-controlled-shared-secret"))
	cache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{{Body: jwks}}}, now)
	replays, _ := NewInMemoryClientAssertionJTIStore(100)
	authenticator, _ := NewPrivateKeyJWTAuthenticator(
		&privateKeyJWTTestClientStore{client: baseClient}, cache, replays, privateKeyJWTTestEndpoint,
	)
	if _, err := authenticator.Authenticate(context.Background(), PrivateKeyJWTAuthenticationRequest{
		ClientAssertion: hmacAssertion,
	}); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("HMAC algorithm confusion err=%v", err)
	}
}

func TestPrivateKeyJWTAuthenticationJWKSCacheRefreshAndSafetyLimits(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	firstKey := signingTestPrivateKey(26)
	secondKey := signingTestPrivateKey(27)
	firstJWKS, _ := privateKeyJWTTestEdJWKS(t, "key-1", firstKey.Public().(ed25519.PublicKey))
	secondJWKS, _ := privateKeyJWTTestEdJWKS(t, "key-2", secondKey.Public().(ed25519.PublicKey))
	fetcher := &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{
		{Body: firstJWKS, CacheTTL: time.Hour, CacheTTLSet: true},
		{Body: secondJWKS, CacheTTL: time.Hour, CacheTTLSet: true},
	}}
	cache := privateKeyJWTTestCache(t, fetcher, now)
	if _, err := cache.ResolveVerificationKey(context.Background(), "https://keys.example.test/jwks", "key-1", "EdDSA"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ResolveVerificationKey(context.Background(), "https://keys.example.test/jwks", "key-1", "EdDSA"); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fresh cached key fetched %d times", fetcher.calls)
	}
	if _, err := cache.ResolveVerificationKey(context.Background(), "https://keys.example.test/jwks", "key-2", "EdDSA"); err != nil {
		t.Fatalf("unknown kid did not force refresh: %v", err)
	}
	if fetcher.calls != 2 || fetcher.maximumBytes != DefaultRemoteJWKSMaxBytes {
		t.Fatalf("JWKS refresh calls=%d maxBytes=%d", fetcher.calls, fetcher.maximumBytes)
	}
	noCacheFetcher := &privateKeyJWTTestFetcher{results: []RemoteJWKSFetchResult{
		{Body: firstJWKS, CacheTTLSet: true}, {Body: firstJWKS, CacheTTLSet: true},
	}}
	noCache := privateKeyJWTTestCache(t, noCacheFetcher, now)
	for index := 0; index < 2; index++ {
		if _, err := noCache.ResolveVerificationKey(context.Background(),
			"https://no-cache.example.test/jwks", "key-1", "EdDSA"); err != nil {
			t.Fatal(err)
		}
	}
	if noCacheFetcher.calls != 2 {
		t.Fatalf("max-age=0 JWKS fetch calls=%d want=2", noCacheFetcher.calls)
	}

	privateJWK := `{"keys":[{"kty":"OKP","crv":"Ed25519","use":"sig","alg":"EdDSA","kid":"bad","x":"` +
		base64.RawURLEncoding.EncodeToString(firstKey.Public().(ed25519.PublicKey)) + `","d":"private"}]}`
	var firstDocument struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(firstJWKS, &firstDocument); err != nil {
		t.Fatal(err)
	}
	tooMany := make([]json.RawMessage, DefaultRemoteJWKSMaxKeys+1)
	for index := range tooMany {
		var value map[string]any
		_ = json.Unmarshal(firstDocument.Keys[0], &value)
		value["kid"] = "many-key-" + fmt.Sprint(index)
		tooMany[index], _ = json.Marshal(value)
	}
	tooManyJWKS, _ := json.Marshal(map[string]any{"keys": tooMany})
	duplicateJWKS, _ := json.Marshal(map[string]any{"keys": []json.RawMessage{
		firstDocument.Keys[0], firstDocument.Keys[0],
	}})
	for name, body := range map[string][]byte{
		"private material": []byte(privateJWK),
		"empty":            []byte(`{"keys":[]}`),
		"trailing JSON":    append(append([]byte(nil), firstJWKS...), []byte(` {}`)...),
		"oversized":        bytes.Repeat([]byte{'x'}, DefaultRemoteJWKSMaxBytes+1),
		"too many keys":    tooManyJWKS,
		"duplicate kid":    duplicateJWKS,
	} {
		t.Run(name, func(t *testing.T) {
			unsafeCache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{
				results: []RemoteJWKSFetchResult{{Body: body}},
			}, now)
			if _, err := unsafeCache.ResolveVerificationKey(context.Background(),
				"https://unsafe.example.test/jwks", "bad", "EdDSA"); !errors.Is(err, ErrRemoteJWKSRejected) {
				t.Fatalf("unsafe JWKS err=%v", err)
			}
		})
	}
	if _, err := cache.ResolveVerificationKey(context.Background(), "http://127.0.0.1/jwks", "key-1", "EdDSA"); !errors.Is(err, ErrRemoteJWKSRejected) {
		t.Fatalf("unsafe JWKS URI err=%v", err)
	}
}

func TestPrivateKeyJWTAuthenticationJTIClaimAllowsOnlyOneConcurrentWinner(t *testing.T) {
	store, _ := NewInMemoryClientAssertionJTIStore(100)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	hash := sha256.Sum256([]byte("same-jti"))
	start := make(chan struct{})
	results := make(chan bool, 16)
	var wait sync.WaitGroup
	for index := 0; index < cap(results); index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claimed, err := store.ClaimClientAssertionJTI(context.Background(), "client-1", hash, now.Add(time.Minute), now)
			if err != nil {
				t.Errorf("claim JTI: %v", err)
			}
			results <- claimed
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent JTI winners=%d", winners)
	}
}

type privateKeyJWTTestFetcher struct {
	results      []RemoteJWKSFetchResult
	err          error
	calls        int
	maximumBytes int64
}

func (fetcher *privateKeyJWTTestFetcher) FetchRemoteJWKS(
	_ context.Context,
	_ string,
	maximumBytes int64,
) (RemoteJWKSFetchResult, error) {
	fetcher.calls++
	fetcher.maximumBytes = maximumBytes
	if fetcher.err != nil {
		return RemoteJWKSFetchResult{}, fetcher.err
	}
	if len(fetcher.results) == 0 {
		return RemoteJWKSFetchResult{}, errors.New("no JWKS fixture")
	}
	index := fetcher.calls - 1
	if index >= len(fetcher.results) {
		index = len(fetcher.results) - 1
	}
	return fetcher.results[index], nil
}

type privateKeyJWTTestClientStore struct {
	client    PrivateKeyJWTClient
	lookupErr error
	markErr   error
	markCalls int
}

func (store *privateKeyJWTTestClientStore) LookupPrivateKeyJWTClient(
	context.Context,
	string,
) (PrivateKeyJWTClient, error) {
	return store.client, store.lookupErr
}

func (store *privateKeyJWTTestClientStore) MarkPrivateKeyJWTAuthenticated(
	context.Context,
	string,
	string,
	time.Time,
) error {
	store.markCalls++
	return store.markErr
}

type privateKeyJWTTestAudit struct {
	failures []PrivateKeyJWTAuthenticationFailure
}

func (audit *privateKeyJWTTestAudit) RecordPrivateKeyJWTAuthenticationFailure(
	_ context.Context,
	failure PrivateKeyJWTAuthenticationFailure,
) error {
	audit.failures = append(audit.failures, failure)
	return nil
}

func privateKeyJWTTestCache(t *testing.T, fetcher RemoteJWKSFetcher, now time.Time) *RemoteJWKSCache {
	t.Helper()
	cache, err := NewRemoteJWKSCache(fetcher, DefaultRemoteJWKSMaxBytes,
		DefaultRemoteJWKSMaxKeys, DefaultRemoteJWKSCacheEntries)
	if err != nil {
		t.Fatal(err)
	}
	cache.now = func() time.Time { return now }
	return cache
}

func privateKeyJWTTestClient(now time.Time, publicClientID string, key VerificationJWK) PrivateKeyJWTClient {
	thumbprint := key.Thumbprint()
	return PrivateKeyJWTClient{
		WorkspaceID: "b18f1f2e-7b5a-7c3d-8e9f-123456789001",
		ClientID:    "b18f1f2e-7b5a-7c3d-8e9f-123456789002", PublicClientID: publicClientID,
		ServicePrincipalID:      "b18f1f2e-7b5a-7c3d-8e9f-123456789003",
		ServicePrincipalVersion: 9, ClientActive: true, ServicePrincipalActive: true,
		PrivateKeyAuthentication: true, JWKSURI: "https://keys.example.test/client.jwks",
		TokenTTLSeconds: 600, Credentials: []PrivateKeyJWTCredential{{
			CredentialID: "credential-" + key.KeyID(), JWKThumbprint: thumbprint[:],
			ValidFrom: now.Add(-time.Minute),
		}},
	}
}

func privateKeyJWTTestEdJWKS(
	t *testing.T,
	keyID string,
	publicKey ed25519.PublicKey,
) ([]byte, VerificationJWK) {
	t.Helper()
	jwk, _ := json.Marshal(map[string]any{
		"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA", "kid": keyID,
		"x": base64.RawURLEncoding.EncodeToString(publicKey),
	})
	verificationKey, err := parseRemoteJWK(jwk, allowedAlgorithmSet(PrivateKeyJWTAlgorithms()))
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"keys": []json.RawMessage{jwk}})
	return set, verificationKey
}

func privateKeyJWTTestRSAJWKS(
	t *testing.T,
	keyID string,
	publicKey *rsa.PublicKey,
) ([]byte, VerificationJWK) {
	t.Helper()
	exponent := big.NewInt(int64(publicKey.E)).Bytes()
	jwk, _ := json.Marshal(map[string]any{
		"kty": "RSA", "use": "sig", "alg": "PS256", "kid": keyID,
		"n": base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	})
	verificationKey, err := parseRemoteJWK(jwk, allowedAlgorithmSet(PrivateKeyJWTAlgorithms()))
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"keys": []json.RawMessage{jwk}})
	return set, verificationKey
}

func privateKeyJWTTestAssertion(
	t *testing.T,
	privateKey any,
	keyID, publicClientID string,
	audience jwt.ClaimStrings,
	jti string,
	issuedAt, expiresAt time.Time,
) string {
	t.Helper()
	claims := privateKeyJWTTestClaims(publicClientID, audience, jti, issuedAt, expiresAt)
	method := jwt.SigningMethodEdDSA
	if _, ok := privateKey.(*rsa.PrivateKey); ok {
		method = nil
	}
	var token *jwt.Token
	if method != nil {
		token = jwt.NewWithClaims(method, claims)
	} else {
		token = jwt.NewWithClaims(jwt.SigningMethodPS256, claims)
	}
	token.Header["kid"], token.Header["typ"] = keyID, "JWT"
	value, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func privateKeyJWTTestClaims(
	publicClientID string,
	audience jwt.ClaimStrings,
	jti string,
	issuedAt, expiresAt time.Time,
) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer: publicClientID, Subject: publicClientID, Audience: audience, ID: jti,
		IssuedAt: jwt.NewNumericDate(issuedAt), NotBefore: jwt.NewNumericDate(issuedAt),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}
}
