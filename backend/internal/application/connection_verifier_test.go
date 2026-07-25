package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"actweave/backend/internal/connection"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/provider"
)

type stubConnectionProviderReader struct {
	value provider.Provider
	err   error
}

func (s stubConnectionProviderReader) Get(context.Context, string, string) (provider.Provider, error) {
	return s.value, s.err
}

type failClosedSecretSource struct {
	called bool
}

func (s *failClosedSecretSource) WithActiveSecret(
	context.Context, string, string, func([]byte) error,
) error {
	s.called = true
	return errors.New("secret source must not be used for dual-mode connection verification")
}

func TestServiceConnectionVerifierRequestPassthroughSucceedsWithoutCredentials(t *testing.T) {
	t.Parallel()

	var sawAuth string
	var sawProviderHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		sawAuth = r.Header.Get("Authorization")
		sawProviderHeader = r.Header.Get("X-Provider-Static")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(target.Port())
	if err != nil {
		t.Fatal(err)
	}
	endpointConfig, err := json.Marshal(map[string]any{
		"schemaVersion":  2,
		"serviceBaseUrl": server.URL,
		"verification": map[string]any{
			"method":           "GET",
			"path":             "/health",
			"expectedStatuses": []int{204},
		},
		"headers": map[string]string{"X-Provider-Static": "provider-only"},
		"egress": map[string]any{
			"allowedHosts": []string{target.Hostname()},
			"allowedPorts": []int{port},
			"allowedCidrs": []string{"127.0.0.0/8"},
			"maxRedirects": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	secretSource := &failClosedSecretSource{}
	injector, err := execution.NewHTTPSecretInjector(secretSource)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &serviceConnectionVerifier{
		client: server.Client(),
		providers: stubConnectionProviderReader{value: provider.Provider{
			ID: "0194f0a0-0000-7000-8000-000000000001", WorkspaceID: "0194f0a0-0000-7000-8000-000000000002",
			Status: "ACTIVE", EndpointConfig: endpointConfig, DriverConfig: json.RawMessage(`{}`),
		}},
		injector: injector,
	}

	value := connection.Connection{
		ID: "0194f0a0-0000-7000-8000-000000000003", WorkspaceID: "0194f0a0-0000-7000-8000-000000000002",
		ProviderID: "0194f0a0-0000-7000-8000-000000000001",
		// Dual-mode placeholder; no business SecretID.
		AuthMode:   "OUTBOUND_IDENTITY",
		AuthConfig: json.RawMessage(`{}`),
		OutboundIdentity: json.RawMessage(`{
			"schemaVersion":"outbound-connection.v1",
			"mode":"REQUEST_PASSTHROUGH",
			"requestPassthrough":{"maxResidenceSeconds":600}
		}`),
		Policy: json.RawMessage(`{}`),
	}
	if err := verifier.Verify(context.Background(), value); err != nil {
		t.Fatalf("REQUEST_PASSTHROUGH verify: %v", err)
	}
	if secretSource.called {
		t.Fatal("dual-mode verification must not open business secrets")
	}
	if sawAuth != "" {
		t.Fatalf("must not inject user credentials, got Authorization=%q", sawAuth)
	}
	if sawProviderHeader != "provider-only" {
		t.Fatalf("provider static headers: got %q", sawProviderHeader)
	}
}

func TestServiceConnectionVerifierBrokerOBOSkipsUserExchange(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("BROKER_OBO verify must not inject credentials, got %q", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(target.Port())
	if err != nil {
		t.Fatal(err)
	}
	endpointConfig, err := json.Marshal(map[string]any{
		"schemaVersion":  2,
		"serviceBaseUrl": server.URL,
		"verification":   map[string]any{"method": "GET", "expectedStatuses": []int{200}},
		"egress": map[string]any{
			"allowedHosts": []string{target.Hostname()},
			"allowedPorts": []int{port},
			"allowedCidrs": []string{"127.0.0.0/8"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	secretSource := &failClosedSecretSource{}
	injector, err := execution.NewHTTPSecretInjector(secretSource)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &serviceConnectionVerifier{
		client: server.Client(),
		providers: stubConnectionProviderReader{value: provider.Provider{
			Status: "ACTIVE", EndpointConfig: endpointConfig, DriverConfig: json.RawMessage(`{}`),
		}},
		injector: injector,
	}
	value := connection.Connection{
		WorkspaceID: "ws", ProviderID: "p", ID: "c",
		AuthMode: "OUTBOUND_IDENTITY", AuthConfig: json.RawMessage(`{}`),
		OutboundIdentity: json.RawMessage(`{
			"schemaVersion":"outbound-connection.v1",
			"mode":"BROKER_OBO",
			"brokerObo":{"clientId":"broker-client","scopes":["orders:read"],"maxTokenTtlSeconds":300}
		}`),
		Policy: json.RawMessage(`{}`),
	}
	if err := verifier.Verify(context.Background(), value); err != nil {
		t.Fatalf("BROKER_OBO verify: %v", err)
	}
	if secretSource.called {
		t.Fatal("BROKER_OBO verification must not open secrets or exchange user tokens")
	}
}

func TestServiceConnectionVerifierLegacyStillUsesInjector(t *testing.T) {
	t.Parallel()

	// Prove the dual-mode skip does not break legacy: empty SecretID on a legacy
	// BEARER connection still fails closed inside HTTPSecretInjector.
	injector, err := execution.NewHTTPSecretInjector(&failClosedSecretSource{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(target.Port())
	endpointConfig, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "serviceBaseUrl": server.URL,
		"verification": map[string]any{"method": "GET", "expectedStatuses": []int{200}},
		"egress": map[string]any{
			"allowedHosts": []string{target.Hostname()},
			"allowedPorts": []int{port},
			"allowedCidrs": []string{"127.0.0.0/8"},
		},
	})
	verifier := &serviceConnectionVerifier{
		client: server.Client(),
		providers: stubConnectionProviderReader{value: provider.Provider{
			Status: "ACTIVE", EndpointConfig: endpointConfig, DriverConfig: json.RawMessage(`{}`),
		}},
		injector: injector,
	}
	err = verifier.Verify(context.Background(), connection.Connection{
		WorkspaceID: "ws", ProviderID: "p", ID: "c",
		AuthMode: "BEARER", AuthConfig: json.RawMessage(`{}`), Policy: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("expected legacy empty-secret verification to fail")
	}
	// Must be the credential fail-closed path, not a network/probe success.
	if execution.ErrorCode(err) != execution.ErrorCodeCredential {
		t.Fatalf("expected credential error for legacy path, got %v", err)
	}
}

func TestDualModeConnectionVerificationDetection(t *testing.T) {
	t.Parallel()
	ok, err := dualModeConnectionVerification(connection.Connection{
		OutboundIdentity: json.RawMessage(`{
			"schemaVersion":"outbound-connection.v1",
			"mode":"REQUEST_PASSTHROUGH",
			"requestPassthrough":{"maxResidenceSeconds":60}
		}`),
	})
	if err != nil || !ok {
		t.Fatalf("identity path: ok=%v err=%v", ok, err)
	}
	ok, err = dualModeConnectionVerification(connection.Connection{AuthMode: "BROKER_OBO"})
	if err != nil || !ok {
		t.Fatalf("empty dual-mode auth marker: ok=%v err=%v", ok, err)
	}
	ok, err = dualModeConnectionVerification(connection.Connection{AuthMode: "BEARER"})
	if err != nil || ok {
		t.Fatalf("legacy: ok=%v err=%v", ok, err)
	}
	_, err = dualModeConnectionVerification(connection.Connection{
		OutboundIdentity: json.RawMessage(`{"schemaVersion":"outbound-connection.v1","mode":"NOPE"}`),
	})
	if err == nil {
		t.Fatal("expected invalid outbound identity error")
	}
}
