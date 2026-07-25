package execution

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
)

func TestOutboundInjectorPassthroughBorrowsAfterConfirmationContext(t *testing.T) {
	clock := outboundidentity.NewFakeClock(time.Now().UTC())
	vault, err := outboundidentity.NewRuntimeCredentialVault("boot-test", clock, outboundidentity.VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()

	ws := "11111111-1111-4111-8111-111111111111"
	user := "22222222-2222-4222-8222-222222222222"
	conn := "33333333-3333-4333-8333-333333333333"
	run := "44444444-4444-4444-8444-444444444444"
	key := outboundidentity.VaultKey{
		BootID: "boot-test", WorkspaceID: ws,
		SubjectType: outboundidentity.SubjectTypeUser, SubjectID: user,
		RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: run,
		ConnectionID: conn, ConnectionPolicyVersion: 1,
	}
	if err := vault.Attach([]outboundidentity.AttachBinding{{
		Key: key, CredentialType: outboundidentity.CredentialTypeAccessToken,
		Value: []byte("passthrough-token"), ExpiresAt: clock.Now().Add(10 * time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}

	injector, err := NewOutboundIdentityInjector(OutboundIdentityInjectorConfig{
		Vault: vault, BootID: "boot-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := principal.NewInternalExecutionSnapshot(ws, principal.TypeUser, user)
	if err != nil {
		t.Fatal(err)
	}
	reqJSON := mustRequirementsJSON(t, conn, "provider-1", outboundidentity.ModeRequestPassthrough, 1, 1, nil)
	driver := mustProviderDriver(t, outboundidentity.ModeRequestPassthrough)

	var sawHeader string
	ctx := WithOutboundInvokeContext(context.Background(), OutboundInvokeContext{
		BootID:        "boot-test",
		RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: run,
		Principal: &snapshot,
	})
	err = injector.WithInjectedConnection(ctx, ConnectionSnapshot{
		ID: conn, WorkspaceID: ws, Headers: map[string]string{},
	}, CredentialReference{
		WorkspaceID: ws, OutboundMode: string(outboundidentity.ModeRequestPassthrough),
		AuthMode:             string(outboundidentity.ModeRequestPassthrough),
		OutboundRequirements: reqJSON, ProviderDriverConfig: driver,
	}, func(c ConnectionSnapshot) error {
		sawHeader = c.Headers["Authorization"]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawHeader != "Bearer passthrough-token" {
		t.Fatalf("header %q", sawHeader)
	}
}

func TestOutboundInjectorBrokerExchangeInjectsHeader(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := outboundidentity.NewRotatingSigningKeyProvider("out-test", priv, outboundidentity.DefaultMaxAssertionTTL)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := outboundidentity.NewAssertionIssuer(keys, "https://actweave.example/outbound", nil)
	if err != nil {
		t.Fatal(err)
	}
	var exchangeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchangeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "broker-biz-token",
			"token_type":   "Bearer",
			"expires_in":   120,
		})
	}))
	defer server.Close()

	broker, err := outboundidentity.NewBrokerClient(issuer,
		outboundidentity.WithBrokerHTTPClient(server.Client()),
		outboundidentity.WithBrokerAllowLoopbackHTTP(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	cache := outboundidentity.NewBrokerTokenCache(nil)
	_, machine, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := encodeEd25519PEM(machine)
	if err != nil {
		t.Fatal(err)
	}
	source := &staticActiveSecretSource{value: pemBytes}
	resolver := machineResolverStub{ref: MachineCredentialRef{SecretID: "ms-1", Version: 2}}

	injector, err := NewOutboundIdentityInjector(OutboundIdentityInjectorConfig{
		Broker: broker, Cache: cache, MachineSecrets: source,
		MachineCredentialLookup: resolver, BootID: "boot-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	ws := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	user := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	connID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	snapshot, err := principal.NewInternalExecutionSnapshot(ws, principal.TypeUser, user)
	if err != nil {
		t.Fatal(err)
	}
	driver := mustProviderDriverBroker(t, server.URL)
	reqJSON := mustRequirementsJSON(t, connID, "provider-1", outboundidentity.ModeBrokerOBO, 1, 1, []string{"orders.read"})
	authConfig, _ := json.Marshal(map[string]any{
		"clientId": "broker-client", "scopes": []string{"orders.read"}, "maxTokenTtlSeconds": 300,
	})

	var header string
	ctx := WithOutboundInvokeContext(context.Background(), OutboundInvokeContext{
		BootID: "boot-b", RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: "run-1",
		Principal: &snapshot,
	})
	err = injector.WithInjectedConnection(ctx, ConnectionSnapshot{
		ID: connID, WorkspaceID: ws, Headers: map[string]string{},
	}, CredentialReference{
		WorkspaceID: ws, OutboundMode: string(outboundidentity.ModeBrokerOBO),
		AuthMode:             string(outboundidentity.ModeBrokerOBO),
		OutboundRequirements: reqJSON, ProviderDriverConfig: driver, AuthConfig: authConfig,
	}, func(c ConnectionSnapshot) error {
		header = c.Headers["Authorization"]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if header != "Bearer broker-biz-token" {
		t.Fatalf("header %q", header)
	}
	if exchangeCalls.Load() != 1 {
		t.Fatalf("exchange calls %d", exchangeCalls.Load())
	}

	// SYSTEM / no subject fails closed
	sysSnap, err := principal.NewInternalExecutionSnapshot(ws, principal.TypeSystem, "00000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	ctxSys := WithOutboundInvokeContext(context.Background(), OutboundInvokeContext{
		BootID: "boot-b", RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: "run-2",
		Principal: &sysSnap,
	})
	err = injector.WithInjectedConnection(ctxSys, ConnectionSnapshot{
		ID: connID, WorkspaceID: ws, Headers: map[string]string{},
	}, CredentialReference{
		WorkspaceID: ws, OutboundMode: string(outboundidentity.ModeBrokerOBO),
		AuthMode:             string(outboundidentity.ModeBrokerOBO),
		OutboundRequirements: reqJSON, ProviderDriverConfig: driver, AuthConfig: authConfig,
	}, func(ConnectionSnapshot) error { return nil })
	if err == nil {
		t.Fatal("SYSTEM must not exchange")
	}
}

func TestLegacyInjectorRejectsDualMode(t *testing.T) {
	injector, err := NewHTTPSecretInjector(&staticActiveSecretSource{value: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	err = injector.WithInjectedConnection(context.Background(), ConnectionSnapshot{
		ID: "c", WorkspaceID: "w", Headers: map[string]string{},
	}, CredentialReference{
		WorkspaceID: "w", AuthMode: "BROKER_OBO", OutboundMode: "BROKER_OBO",
	}, func(ConnectionSnapshot) error { return nil })
	if err == nil {
		t.Fatal("legacy injector must fail closed for dual-mode")
	}
}

func TestEnsureSameOriginTarget(t *testing.T) {
	if err := EnsureSameOriginTarget("https://api.example.com/v1", mustParseURL(t, "https://evil.example.com/x")); err == nil {
		t.Fatal("cross-origin must reject")
	}
	if err := EnsureSameOriginTarget("https://api.example.com/v1", mustParseURL(t, "https://api.example.com/v2/items")); err != nil {
		t.Fatalf("same origin: %v", err)
	}
}

type machineResolverStub struct{ ref MachineCredentialRef }

func (m machineResolverStub) ResolveMachineCredential(context.Context, string, string) (MachineCredentialRef, error) {
	return m.ref, nil
}

func encodeEd25519PEM(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func mustRequirementsJSON(t *testing.T, connID, providerID string, mode outboundidentity.Mode, pVer, cVer int64, scopes []string) json.RawMessage {
	t.Helper()
	if scopes == nil {
		scopes = []string{}
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": "outbound-requirements.v1",
		"connections": []map[string]any{{
			"connectionId": connID, "providerId": providerID, "mode": string(mode),
			"providerContractVersion": pVer, "connectionPolicyVersion": cVer,
			"requiredScopes": scopes, "credentialRequired": mode == outboundidentity.ModeRequestPassthrough,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Normalize via package to ensure schema validity.
	if _, err := outboundidentity.ParseRequirements(raw); err != nil {
		t.Fatalf("requirements: %v raw=%s", err, raw)
	}
	return raw
}

func mustProviderDriver(t *testing.T, mode outboundidentity.Mode) json.RawMessage {
	t.Helper()
	identity := map[string]any{
		"schemaVersion":         "outbound-identity.v1",
		"supportedModes":        []string{string(mode)},
		"supportedSubjectTypes": []string{"USER"},
		"requestPassthrough": map[string]any{
			"credentialTypes":   []string{"ACCESS_TOKEN"},
			"businessInjection": map[string]string{"headerName": "Authorization", "prefix": "Bearer"},
		},
	}
	raw, err := json.Marshal(map[string]any{"outboundIdentity": identity})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustProviderDriverBroker(t *testing.T, tokenURL string) json.RawMessage {
	t.Helper()
	identity := map[string]any{
		"schemaVersion":         "outbound-identity.v1",
		"supportedModes":        []string{"BROKER_OBO"},
		"supportedSubjectTypes": []string{"USER", "EXTERNAL_SUBJECT"},
		"brokerObo": map[string]any{
			"tokenEndpoint": tokenURL, "audience": "urn:broker:tenant",
			"machineAuthMethod": "PRIVATE_KEY_JWT",
			"allowedScopes":     []string{"orders.read"},
			"response": map[string]string{
				"accessTokenPath": "access_token", "tokenTypePath": "token_type",
				"expiresInPath": "expires_in", "expectedTokenType": "Bearer",
			},
			"businessInjection": map[string]string{"headerName": "Authorization", "prefix": "Bearer"},
		},
	}
	raw, err := json.Marshal(map[string]any{"outboundIdentity": identity})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
