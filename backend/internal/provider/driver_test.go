package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/serviceendpoint"
)

type recordingHTTPDiscoverer struct {
	requests []DiscoveryRequest
	page     DiscoveryPage
	err      error
}

func (d *recordingHTTPDiscoverer) DiscoverHTTP(
	_ context.Context,
	request DiscoveryRequest,
) (DiscoveryPage, error) {
	d.requests = append(d.requests, request)
	return d.page, d.err
}

func TestRegistryRegistersOnlyPhaseOneHTTPDriver(t *testing.T) {
	discoverer := &recordingHTTPDiscoverer{page: DiscoveryPage{
		Assets: []Asset{{Kind: "TOOL", ExternalID: "orders.get", Name: "Get Order"}},
	}}
	registry, err := NewPhaseOneRegistry(discoverer)
	if err != nil {
		t.Fatalf("create phase one registry: %v", err)
	}
	driver, err := registry.Resolve(KindHTTPOpenAPI)
	if err != nil {
		t.Fatalf("resolve HTTP OpenAPI driver: %v", err)
	}
	request := DiscoveryRequest{Provider: validHTTPProvider(), Limit: 25}
	page, err := driver.Discover(context.Background(), request)
	if err != nil {
		t.Fatalf("discover HTTP assets: %v", err)
	}
	if len(page.Assets) != 1 || page.Assets[0].ExternalID != "orders.get" || len(discoverer.requests) != 1 {
		t.Fatalf("HTTP discovery was not delegated: page=%+v requests=%+v", page, discoverer.requests)
	}

	for _, kind := range []Kind{
		KindInternalRegistry,
		KindMCPServer,
		KindOpenConnector,
		KindShellRuntime,
	} {
		if _, err := registry.Resolve(kind); !errors.Is(err, ErrKindNotAvailable) {
			t.Fatalf("expected %s unavailable error, got %v", kind, err)
		}
	}
}

func TestRegistryRejectsDuplicateDriver(t *testing.T) {
	discoverer := &recordingHTTPDiscoverer{}
	first, _ := NewHTTPOpenAPIDriver(discoverer)
	second, _ := NewHTTPOpenAPIDriver(discoverer)
	if _, err := NewRegistry(first, second); !errors.Is(err, ErrDriverAlreadyExists) {
		t.Fatalf("expected duplicate driver error, got %v", err)
	}
	if _, err := NewHTTPOpenAPIDriver(nil); err == nil {
		t.Fatal("expected missing concrete HTTP discoverer rejection")
	}
}

func TestProviderKindValidation(t *testing.T) {
	discoverer := &recordingHTTPDiscoverer{}
	driver, err := NewHTTPOpenAPIDriver(discoverer)
	if err != nil {
		t.Fatalf("create HTTP driver: %v", err)
	}
	provider := validHTTPProvider()
	connection := &ConnectionContext{
		ID:          "connection-1",
		WorkspaceID: provider.WorkspaceID,
		Alias:       "work",
		Configured:  true,
	}
	if err := driver.Validate(context.Background(), provider, connection); err != nil {
		t.Fatalf("validate HTTP provider: %v", err)
	}

	invalidKind := provider
	invalidKind.Kind = KindMCPServer
	if err := driver.Validate(context.Background(), invalidKind, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected unsupported kind validation error, got %v", err)
	}
	invalidJSON := provider
	invalidJSON.EndpointConfig = json.RawMessage(`[]`)
	if err := driver.Validate(context.Background(), invalidJSON, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected endpoint config validation error, got %v", err)
	}
	crossWorkspace := *connection
	crossWorkspace.WorkspaceID = "workspace-2"
	if err := driver.Validate(context.Background(), provider, &crossWorkspace); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected cross-workspace connection rejection, got %v", err)
	}
	if _, err := driver.Discover(context.Background(), DiscoveryRequest{Provider: provider, Limit: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid discovery limit rejection, got %v", err)
	}
}

func TestHTTPProviderValidatesV2EndpointAndOutboundIdentityContract(t *testing.T) {
	driver, err := NewHTTPOpenAPIDriver(&recordingHTTPDiscoverer{})
	if err != nil {
		t.Fatal(err)
	}
	value := validContractHTTPProvider(t)
	if err := driver.Validate(context.Background(), value, nil); err != nil {
		t.Fatalf("expected valid v2 Provider contract, got %v", err)
	}

	endpointCases := []struct {
		name   string
		mutate func(*serviceendpoint.Config)
	}{
		{name: "missing service base URL", mutate: func(config *serviceendpoint.Config) { config.ServiceBaseURL = "" }},
		{name: "absolute verification path", mutate: func(config *serviceendpoint.Config) { config.Verification.Path = "https://evil.example/verify" }},
	}

	runtimeOnly := value
	var runtimeOnlyConfig serviceendpoint.Config
	if err := json.Unmarshal(runtimeOnly.EndpointConfig, &runtimeOnlyConfig); err != nil {
		t.Fatal(err)
	}
	runtimeOnlyConfig.Discovery = serviceendpoint.Discovery{}
	runtimeOnly.EndpointConfig = mustJSON(t, runtimeOnlyConfig)
	if err := driver.Validate(context.Background(), runtimeOnly, nil); err != nil {
		t.Fatalf("runtime-only Provider should not require an online OpenAPI document: %v", err)
	}
	for _, test := range endpointCases {
		t.Run("endpoint/"+test.name, func(t *testing.T) {
			candidate := value
			var config serviceendpoint.Config
			if err := json.Unmarshal(candidate.EndpointConfig, &config); err != nil {
				t.Fatal(err)
			}
			test.mutate(&config)
			candidate.EndpointConfig = mustJSON(t, config)
			if err := driver.Validate(context.Background(), candidate, nil); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid v2 endpoint rejection, got %v", err)
			}
		})
	}

	// Dual-mode only: legacy service-auth.v1 and invalid outbound modes are rejected.
	legacy := value
	legacy.DriverConfig = json.RawMessage(`{"authentication":{"version":"service-auth.v1","defaultSchemeKey":"x","schemes":[]}}`)
	if err := driver.Validate(context.Background(), legacy, nil); err == nil {
		t.Fatal("expected legacy service-auth rejection")
	}
	missingIdentity := value
	missingIdentity.DriverConfig = json.RawMessage(`{}`)
	if err := driver.Validate(context.Background(), missingIdentity, nil); err == nil {
		t.Fatal("expected missing outboundIdentity rejection")
	}
	thirdMode := value
	thirdMode.DriverConfig = json.RawMessage(`{
		"outboundIdentity":{
			"schemaVersion":"outbound-identity.v1",
			"supportedModes":["NONE"],
			"supportedSubjectTypes":["USER"],
			"requestPassthrough":{"credentialTypes":["ACCESS_TOKEN"],"businessInjection":{"headerName":"Authorization"}}
		}
	}`)
	if err := driver.Validate(context.Background(), thirdMode, nil); err == nil {
		t.Fatal("expected NONE mode rejection")
	}
}

func TestConfigurationSecurityAcceptanceRejectsProviderCredentialValues(t *testing.T) {
	discoverer := &recordingHTTPDiscoverer{}
	driver, err := NewHTTPOpenAPIDriver(discoverer)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Provider){
		func(value *Provider) { value.EndpointConfig = json.RawMessage(`{"apiKey":"raw-provider-secret"}`) },
		func(value *Provider) {
			value.DriverConfig = json.RawMessage(`{"nested":{"token_value":"raw-provider-secret"}}`)
		},
		func(value *Provider) {
			value.DriverConfig = json.RawMessage(`{"headers":{"Authorization":"Bearer raw-provider-secret"}}`)
		},
	} {
		value := validHTTPProvider()
		mutate(&value)
		if err := driver.Validate(context.Background(), value, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected provider credential-bearing JSON rejection, got %v", err)
		}
	}
}

func TestProviderExecutionContractExcludesOnlyV2DiscoveryMetadata(t *testing.T) {
	driver := json.RawMessage(validOutboundIdentityDriverConfig())
	current := json.RawMessage(`{"schemaVersion":2,"serviceBaseUrl":"https://api.example/v1","discovery":{"documentUrl":"https://docs.example/v1.json"},"verification":{"method":"GET"}}`)
	discoveryChanged := json.RawMessage(`{"schemaVersion":2,"serviceBaseUrl":"https://api.example/v1","discovery":{"documentUrl":"https://docs.example/v2.json","sourceRevision":"v2"},"verification":{"method":"GET"}}`)
	if !providerExecutionContractEqual(current, driver, discoveryChanged, driver) {
		t.Fatal("v2 discovery-only changes must not invalidate runtime Connections")
	}
	runtimeChanged := json.RawMessage(`{"schemaVersion":2,"serviceBaseUrl":"https://api.example/v2","discovery":{"documentUrl":"https://docs.example/v2.json"},"verification":{"method":"GET"}}`)
	if providerExecutionContractEqual(current, driver, runtimeChanged, driver) {
		t.Fatal("runtime endpoint changes must invalidate Connections")
	}
	legacyCurrent := json.RawMessage(`{"sourceUri":"https://docs.example/v1.json"}`)
	legacyChanged := json.RawMessage(`{"sourceUri":"https://docs.example/v2.json"}`)
	if providerExecutionContractEqual(legacyCurrent, driver, legacyChanged, driver) {
		t.Fatal("legacy sourceUri may also be the runtime base and must remain execution-relevant")
	}
}

func validOutboundIdentityDriverConfig() string {
	return `{
		"outboundIdentity":{
			"schemaVersion":"outbound-identity.v1",
			"supportedModes":["BROKER_OBO","REQUEST_PASSTHROUGH"],
			"supportedSubjectTypes":["USER","EXTERNAL_SUBJECT"],
			"brokerObo":{
				"tokenEndpoint":"https://broker.example/token",
				"audience":"urn:broker:tenant",
				"machineAuthMethod":"PRIVATE_KEY_JWT",
				"allowedScopes":["orders.read"],
				"response":{"accessTokenPath":"access_token","expiresInPath":"expires_in"},
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			},
			"requestPassthrough":{
				"credentialTypes":["ACCESS_TOKEN"],
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			}
		}
	}`
}

func validHTTPProvider() Provider {
	return Provider{
		ID:                            "provider-1",
		WorkspaceID:                   "workspace-1",
		Kind:                          KindHTTPOpenAPI,
		DriverKey:                     "http_openapi",
		Transport:                     "HTTP",
		EndpointConfig:                json.RawMessage(`{"sourceUri":"https://orders.example/openapi.json"}`),
		DriverConfig:                  json.RawMessage(validOutboundIdentityDriverConfig()),
		OutboundIdentityPolicyVersion: 1,
	}
}

func validContractHTTPProvider(t *testing.T) Provider {
	t.Helper()
	value := validHTTPProvider()
	value.EndpointConfig = mustJSON(t, serviceendpoint.Config{
		SchemaVersion:  serviceendpoint.SchemaVersion,
		ServiceBaseURL: "https://orders.example/api/v2",
		Discovery: serviceendpoint.Discovery{
			DocumentURL: "https://orders.example/openapi.json",
		},
		Verification: serviceendpoint.Verification{
			Method: "HEAD", Path: "/health", ExpectedStatuses: []int{200, 204},
		},
	})
	value.DriverConfig = json.RawMessage(validOutboundIdentityDriverConfig())
	return value
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
