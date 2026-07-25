package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestClientCredentialsTokenEndpointClaimsAreAgentScopedAndAsymmetricallySigned(t *testing.T) {
	now := time.Date(2026, 7, 21, 4, 5, 6, 0, time.UTC)
	client := clientCredentialsTestClient()
	store := &clientCredentialsGrantStoreStub{grant: clientCredentialsTestGrant(client, now)}
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x52}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("aap-token-key-1", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewClientCredentialsTokenService(
		store, keys, "https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.newJTI = func() (string, error) { return "c48f1f2e-7b5a-7c3d-8e9f-123456789008", nil }
	issued, err := service.IssueClientCredentialsToken(context.Background(), ClientCredentialsTokenRequest{
		Client: client, AgentID: store.grant.AgentID,
		RequestedScopes: []string{"event:read", "run:create"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.TokenType != "Bearer" || issued.ExpiresIn != 600 ||
		issued.Scope != "run:create event:read" || issued.AccessToken == "" {
		t.Fatalf("unexpected token response: %+v", issued)
	}
	parsedClaims := AAPAccessTokenClaims{}
	token, err := jwt.ParseWithClaims(issued.AccessToken, &parsedClaims, func(token *jwt.Token) (any, error) {
		if token.Header["typ"] != AAPAccessTokenType || token.Header["alg"] != AAPSigningAlgorithm ||
			token.Header["kid"] != "aap-token-key-1" {
			t.Fatalf("unexpected protected header: %+v", token.Header)
		}
		return privateKey.Public(), nil
	}, jwt.WithValidMethods([]string{AAPSigningAlgorithm}), jwt.WithAudience(AAPAccessTokenAudience),
		jwt.WithIssuer("https://api.example.test/api/agent-access/v1/oauth"),
		jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || token == nil || !token.Valid {
		t.Fatalf("parse issued token valid=%v err=%v", token != nil && token.Valid, err)
	}
	if parsedClaims.Subject != client.ServicePrincipalID ||
		parsedClaims.AuthorizedParty != client.PublicClientID ||
		parsedClaims.WorkspaceID != client.WorkspaceID || parsedClaims.AgentID != store.grant.AgentID ||
		parsedClaims.SecurityVersion != client.ServicePrincipalVersion ||
		parsedClaims.ID != "c48f1f2e-7b5a-7c3d-8e9f-123456789008" ||
		parsedClaims.IssuedAt == nil || !parsedClaims.IssuedAt.Time.Equal(now) ||
		parsedClaims.NotBefore == nil || !parsedClaims.NotBefore.Time.Equal(now.Add(-DefaultTokenClockSkew)) ||
		parsedClaims.ExpiresAt == nil || !parsedClaims.ExpiresAt.Time.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("unexpected AAP claims: %+v", parsedClaims)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(jwtTokenPayload(t, issued.AccessToken)), &raw); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"iss", "aud", "sub", "azp", "wid", "aid", "scope", "ver", "jti", "iat", "nbf", "exp"} {
		if _, exists := raw[required]; !exists {
			t.Fatalf("issued token is missing %q: %+v", required, raw)
		}
	}
	if _, exists := raw["act"]; exists {
		t.Fatalf("pure Client Credentials token must omit delegated actor claim: %+v", raw)
	}
}

func TestClientCredentialsTokenEndpointRejectsScopeTargetTTLAndStoreFailures(t *testing.T) {
	now := time.Date(2026, 7, 21, 4, 5, 6, 0, time.UTC)
	client := clientCredentialsTestClient()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x53}, ed25519.SeedSize))
	keys, err := NewRotatingSigningKeyProvider("aap-token-key-2", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		mutate func(*clientCredentialsGrantStoreStub, *ClientCredentialsTokenRequest)
		want   error
	}{
		"empty scope": {mutate: func(_ *clientCredentialsGrantStoreStub, value *ClientCredentialsTokenRequest) {
			value.RequestedScopes = nil
		}, want: ErrClientCredentialsScopeInvalid},
		"unknown scope": {mutate: func(_ *clientCredentialsGrantStoreStub, value *ClientCredentialsTokenRequest) {
			value.RequestedScopes = []string{"workspace:manage"}
		}, want: ErrClientCredentialsScopeInvalid},
		"duplicate scope": {mutate: func(_ *clientCredentialsGrantStoreStub, value *ClientCredentialsTokenRequest) {
			value.RequestedScopes = []string{"run:create", "run:create"}
		}, want: ErrClientCredentialsScopeInvalid},
		"scope outside Grant": {mutate: func(_ *clientCredentialsGrantStoreStub, value *ClientCredentialsTokenRequest) {
			value.RequestedScopes = []string{"interaction:decide"}
		}, want: ErrClientCredentialsScopeInvalid},
		"unknown target": {mutate: func(store *clientCredentialsGrantStoreStub, _ *ClientCredentialsTokenRequest) {
			store.err = ErrClientCredentialsGrantNotFound
		}, want: ErrClientCredentialsTargetInvalid},
		"Grant expires before minimum TTL": {mutate: func(store *clientCredentialsGrantStoreStub, _ *ClientCredentialsTokenRequest) {
			expires := now.Add(MinimumAccessTokenTTL - time.Second)
			store.grant.GrantExpiresAt = &expires
		}, want: ErrClientCredentialsTargetInvalid},
		"changed security version": {mutate: func(store *clientCredentialsGrantStoreStub, _ *ClientCredentialsTokenRequest) {
			store.grant.ServicePrincipalVersion++
		}, want: ErrClientCredentialsTargetInvalid},
		"store failure": {mutate: func(store *clientCredentialsGrantStoreStub, _ *ClientCredentialsTokenRequest) {
			store.err = errors.New("database unavailable")
		}, want: ErrTokenServiceUnavailable},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := &clientCredentialsGrantStoreStub{grant: clientCredentialsTestGrant(client, now)}
			service, err := NewClientCredentialsTokenService(store, keys,
				"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return now }
			request := ClientCredentialsTokenRequest{
				Client: client, AgentID: store.grant.AgentID, RequestedScopes: []string{"run:create"},
			}
			test.mutate(store, &request)
			_, err = service.IssueClientCredentialsToken(context.Background(), request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestClientCredentialsTokenEndpointCapsTTLByServerAndGrant(t *testing.T) {
	now := time.Date(2026, 7, 21, 4, 5, 6, 0, time.UTC)
	client := clientCredentialsTestClient()
	client.TokenTTLSeconds = 900
	grant := clientCredentialsTestGrant(client, now)
	expires := now.Add(7 * time.Minute)
	grant.GrantExpiresAt = &expires
	store := &clientCredentialsGrantStoreStub{grant: grant}
	keys, err := NewRotatingSigningKeyProvider("aap-token-key-3",
		ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x54}, ed25519.SeedSize)), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewClientCredentialsTokenService(store, keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 8*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.newJTI = func() (string, error) { return "c58f1f2e-7b5a-7c3d-8e9f-123456789008", nil }
	issued, err := service.IssueClientCredentialsToken(context.Background(), ClientCredentialsTokenRequest{
		Client: client, AgentID: grant.AgentID, RequestedScopes: []string{"run:create"},
	})
	if err != nil || issued.ExpiresIn != 420 || !issued.Claims.ExpiresAt.Time.Equal(expires) {
		t.Fatalf("Grant-capped token=%+v err=%v", issued, err)
	}
}

func TestClientCredentialsTokenEndpointRequiresFiveToFifteenMinuteServerWindow(t *testing.T) {
	keys, err := NewRotatingSigningKeyProvider("aap-token-key-4",
		ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x55}, ed25519.SeedSize)), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, ttl := range []time.Duration{MinimumAccessTokenTTL - time.Second, DefaultMaxAccessTokenTTL + time.Second} {
		if _, err := NewClientCredentialsTokenService(&clientCredentialsGrantStoreStub{}, keys,
			"https://api.example.test/api/agent-access/v1/oauth/token", ttl); err == nil {
			t.Fatalf("unsafe server TTL %s must fail", ttl)
		}
	}
	shortRetention, err := NewRotatingSigningKeyProvider("aap-token-key-short",
		ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x56}, ed25519.SeedSize)), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClientCredentialsTokenService(&clientCredentialsGrantStoreStub{}, shortRetention,
		"https://api.example.test/api/agent-access/v1/oauth/token", 10*time.Minute); err == nil {
		t.Fatal("Token Service TTL must not exceed signing-key publication retention")
	}
}

type clientCredentialsGrantStoreStub struct {
	grant ClientCredentialsGrant
	err   error
}

func (store *clientCredentialsGrantStoreStub) ResolveClientCredentialsGrant(
	_ context.Context,
	_ AuthenticatedClient,
	_ string,
	_ time.Time,
) (ClientCredentialsGrant, error) {
	return store.grant, store.err
}

func clientCredentialsTestClient() AuthenticatedClient {
	return AuthenticatedClient{
		WorkspaceID: "c48f1f2e-7b5a-7c3d-8e9f-123456789001",
		ClientID:    "c48f1f2e-7b5a-7c3d-8e9f-123456789002", PublicClientID: clientSecretTestPublicID(77),
		ServicePrincipalID:      "c48f1f2e-7b5a-7c3d-8e9f-123456789003",
		ServicePrincipalVersion: 7,
		CredentialID:            "c48f1f2e-7b5a-7c3d-8e9f-123456789004", TokenTTLSeconds: 600,
	}
}

func clientCredentialsTestGrant(client AuthenticatedClient, now time.Time) ClientCredentialsGrant {
	expires := now.Add(time.Hour)
	return ClientCredentialsGrant{
		GrantID:     "c48f1f2e-7b5a-7c3d-8e9f-123456789005",
		WorkspaceID: client.WorkspaceID, ClientID: client.ClientID,
		PublicClientID: client.PublicClientID, ServicePrincipalID: client.ServicePrincipalID,
		ServicePrincipalVersion: client.ServicePrincipalVersion,
		AgentID:                 "c48f1f2e-7b5a-7c3d-8e9f-123456789006",
		GrantedScopes:           []string{"agent:read", "run:create", "event:read"},
		ClientTokenTTL:          time.Duration(client.TokenTTLSeconds) * time.Second,
		GrantExpiresAt:          &expires,
	}
}

func jwtTokenPayload(t *testing.T, value string) string {
	t.Helper()
	parts := bytes.Split([]byte(value), []byte("."))
	if len(parts) != 3 {
		t.Fatalf("invalid compact JWT: %q", value)
	}
	payload, err := jwt.NewParser().DecodeSegment(string(parts[1]))
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
