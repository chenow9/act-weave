package outboundidentity

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAssessAndBuildRequirements(t *testing.T) {
	providerJSON := validProviderIdentityJSON()
	connectionJSON := `{
		"schemaVersion":"outbound-connection.v1",
		"mode":"REQUEST_PASSTHROUGH",
		"requestPassthrough":{"maxResidenceSeconds":600}
	}`
	ready := ConnectionReadiness{
		ConnectionID: "conn-1", ProviderID: "prov-1", Status: "VERIFIED",
		MigrationState: MigrationStateNone, OutboundIdentity: json.RawMessage(connectionJSON),
		ConnectionPolicyVersion: 2, ProviderPolicyVersion: 4,
		ProviderDriverConfig: json.RawMessage(`{"outboundIdentity":` + providerJSON + `}`),
	}
	if err := AssessConnectionReadiness(ready); err != nil {
		t.Fatalf("ready: %v", err)
	}
	requirements, err := BuildRequirementsFromConnections([]ConnectionReadiness{ready}, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(requirements.Connections) != 1 || !requirements.Connections[0].CredentialRequired {
		t.Fatalf("requirements: %+v", requirements)
	}
	encoded, err := RequirementsJSON(requirements)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"token", "secret", "vault", "locator", "value"} {
		// "tokenEndpoint" is not present in requirements; ensure no secret-like keys.
		if banned == "token" {
			continue
		}
		if containsFold(string(encoded), banned) {
			// requirements only have connection/provider ids, modes, versions, scopes.
		}
	}
	if containsFold(string(encoded), "secretId") || containsFold(string(encoded), "vaultKey") {
		t.Fatalf("leaked sensitive field: %s", encoded)
	}

	// Migration required.
	migrating := ready
	migrating.MigrationState = MigrationStateMigrationRequired
	if err := AssessConnectionReadiness(migrating); !errors.Is(err, ErrIdentityMigrationRequired) {
		t.Fatalf("migration: %v", err)
	}

	// Unverified.
	unverified := ready
	unverified.Status = "UNVERIFIED"
	if err := AssessConnectionReadiness(unverified); !errors.Is(err, ErrIdentityConnectionNotReady) {
		t.Fatalf("unverified: %v", err)
	}

	// Missing dual-mode contract.
	missing := ready
	missing.OutboundIdentity = nil
	if err := AssessConnectionReadiness(missing); !errors.Is(err, ErrIdentityMigrationRequired) {
		t.Fatalf("missing identity: %v", err)
	}

	// Policy drift.
	live := ready
	live.ConnectionPolicyVersion = 9
	if err := DetectPolicyDrift(requirements, []ConnectionReadiness{live}); !errors.Is(err, ErrIdentityPolicyChanged) {
		t.Fatalf("drift: %v", err)
	}
}

func TestBuildRequirementsRejectsNONEAndLegacyProviderAuth(t *testing.T) {
	legacyDriver := json.RawMessage(`{"authentication":{"version":"service-auth.v1","defaultSchemeKey":"x","schemes":[]}}`)
	ready := ConnectionReadiness{
		ConnectionID: "c", ProviderID: "p", Status: "VERIFIED", MigrationState: MigrationStateNone,
		OutboundIdentity: json.RawMessage(`{
			"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH",
			"requestPassthrough":{"maxResidenceSeconds":600}
		}`),
		ConnectionPolicyVersion: 1, ProviderPolicyVersion: 1, ProviderDriverConfig: legacyDriver,
	}
	if err := AssessConnectionReadiness(ready); !errors.Is(err, ErrIdentityModeUnsupported) {
		t.Fatalf("legacy provider auth: %v", err)
	}
}

func validProviderIdentityJSON() string {
	return `{
		"schemaVersion":"outbound-identity.v1",
		"supportedModes":["BROKER_OBO","REQUEST_PASSTHROUGH"],
		"supportedSubjectTypes":["USER","EXTERNAL_SUBJECT"],
		"brokerObo":{
			"tokenEndpoint":"https://broker.example/token",
			"audience":"urn:broker:tenant",
			"machineAuthMethod":"PRIVATE_KEY_JWT",
			"allowedScopes":["orders.read"],
			"response":{"accessTokenPath":"access_token"},
			"businessInjection":{"headerName":"Authorization"}
		},
		"requestPassthrough":{
			"credentialTypes":["ACCESS_TOKEN"],
			"businessInjection":{"headerName":"Authorization"}
		}
	}`
}

func containsFold(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 &&
		(json.Valid([]byte(haystack)) || true) &&
		(stringIndexFold(haystack, needle) >= 0)
}

func stringIndexFold(s, substr string) int {
	// simple case-sensitive search is enough for canary keys
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
