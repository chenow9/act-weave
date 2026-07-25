package serviceendpoint

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestV2SeparatesDiscoveryRuntimeAndVerification(t *testing.T) {
	config, err := Parse(json.RawMessage(`{
		"schemaVersion":2,
		"serviceBaseUrl":"https://api.example/v1/",
		"discovery":{"documentUrl":"https://docs.example/openapi.json"},
		"verification":{"method":"HEAD","path":"health","expectedStatuses":[200,204]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.ServiceBaseURL != "https://api.example/v1" || config.Discovery.DocumentURL != "https://docs.example/openapi.json" ||
		config.VerificationURL() != "https://api.example/v1/health" || !config.Verification.Accepts(204) || config.Verification.Accepts(201) {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestV2AllowsRuntimeProviderWithoutOnlineDiscoveryDocument(t *testing.T) {
	config, err := Parse(json.RawMessage(`{
		"schemaVersion":2,
		"serviceBaseUrl":"https://api.example/v1",
		"verification":{"method":"GET","path":"/health","expectedStatuses":[200]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.HasDiscovery() || config.ServiceBaseURL != "https://api.example/v1" ||
		config.VerificationURL() != "https://api.example/v1/health" {
		t.Fatalf("unexpected runtime-only config: %+v", config)
	}
}

func TestV2ValidatesExplicitPrivateEgressPolicy(t *testing.T) {
	config, err := Parse(json.RawMessage(`{
		"schemaVersion":2,
		"serviceBaseUrl":"http://192.168.10.62:8000",
		"egress":{"allowedCIDRs":["192.168.10.0/24"],"allowedPorts":[8000],"maxRedirects":1},
		"verification":{"method":"GET","expectedStatuses":[200]}
	}`))
	if err != nil || len(config.Egress.AllowedCIDRs) != 1 {
		t.Fatalf("valid private egress config: %+v err=%v", config, err)
	}
	if _, err := Parse(json.RawMessage(`{
		"schemaVersion":2,
		"serviceBaseUrl":"http://192.168.10.62:8000",
		"egress":{"allowedCIDRs":["192.168.10.0/not-a-prefix"]}
	}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid egress CIDR, got %v", err)
	}
}

func TestRejectsRelativeServiceURLAndAbsoluteVerificationPath(t *testing.T) {
	for _, raw := range []string{
		`{"schemaVersion":2,"serviceBaseUrl":"/api","discovery":{"documentUrl":"https://docs.example/openapi.json"}}`,
		`{"schemaVersion":2,"serviceBaseUrl":"https://api.example/v1?tenant=a"}`,
		`{"schemaVersion":2,"serviceBaseUrl":"https://api.example","discovery":{"documentUrl":"/openapi.json"}}`,
		`{"schemaVersion":2,"serviceBaseUrl":"https://api.example","discovery":{"sourceRevision":"v1"}}`,
		`{"schemaVersion":2,"serviceBaseUrl":"https://api.example","discovery":{"documentUrl":"https://docs.example/openapi.json"},"verification":{"path":"https://evil.example"}}`,
		`{"schemaVersion":2,"serviceBaseUrl":"https://api.example","headers":{"Bad Header":"value"}}`,
		`{"schemaVersion":2,"serviceBaseUrl":"https://api.example","headers":{"X-Static":"line1\nline2"}}`,
	} {
		if _, err := Parse(json.RawMessage(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid endpoint for %s, got %v", raw, err)
		}
	}
}

func TestLegacyProviderRemainsReadable(t *testing.T) {
	config, err := Parse(json.RawMessage(`{"sourceUri":"https://api.example/openapi.json","baseUrl":"https://api.example"}`))
	if err != nil || config.Discovery.DocumentURL != "https://api.example/openapi.json" || config.ServiceBaseURL != "https://api.example" {
		t.Fatalf("legacy config: %+v err=%v", config, err)
	}
}
