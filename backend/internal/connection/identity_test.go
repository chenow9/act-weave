package connection

import (
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/outboundidentity"
)

func TestImpactProofIssueAndVerify(t *testing.T) {
	service, err := NewImpactProofService(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service.WithClock(func() time.Time { return now })

	payload := ImpactProofPayload{
		WorkspaceID:      connWorkspaceID,
		ConnectionID:     connID,
		ActorID:          connOwnerID,
		ChangeKind:       ImpactChangeMode,
		DescriptorHash:   "abc",
		LockVersion:      3,
		PolicyVersion:    2,
		ImpactSetVersion: 1001001,
	}
	proof, expiresAt, err := service.Issue(payload)
	if err != nil || proof == "" || !expiresAt.After(now) {
		t.Fatalf("issue: proof=%q exp=%v err=%v", proof, expiresAt, err)
	}
	if err := service.Verify(proof, payload); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Actor drift → stale.
	wrong := payload
	wrong.ActorID = connDeleteActorID
	if err := service.Verify(proof, wrong); !errors.Is(err, outboundidentity.ErrIdentityChangeConfirmationStale) {
		t.Fatalf("expected stale actor, got %v", err)
	}

	// Expiry.
	service.WithClock(func() time.Time { return now.Add(6 * time.Minute) })
	if err := service.Verify(proof, payload); !errors.Is(err, outboundidentity.ErrIdentityChangeConfirmationStale) {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestRejectLegacyWrite(t *testing.T) {
	if err := RejectLegacyWrite("API_KEY", false, false); !errors.Is(err, outboundidentity.ErrIdentityModeUnsupported) {
		t.Fatalf("API_KEY: %v", err)
	}
	if err := RejectLegacyWrite("BROKER_OBO", true, false); !errors.Is(err, outboundidentity.ErrIdentityModeUnsupported) {
		t.Fatalf("legacy secret: %v", err)
	}
	if err := RejectLegacyWrite(string(outboundidentity.ModeRequestPassthrough), false, false); err != nil {
		t.Fatalf("dual mode: %v", err)
	}
}

func TestBuildAndValidateIdentityWrite(t *testing.T) {
	provider, err := outboundidentity.ParseProviderIdentity(jsonRaw(validOutboundProviderJSON()))
	if err != nil {
		t.Fatal(err)
	}
	write := IdentityWrite{
		Mode: outboundidentity.ModeRequestPassthrough,
		RequestPassthrough: &outboundidentity.ConnectionPassthrough{
			MaxResidenceSeconds: 600,
		},
	}
	if err := ValidateIdentityWrite(write, provider); err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	broker := IdentityWrite{
		Mode: outboundidentity.ModeBrokerOBO,
		BrokerOBO: &outboundidentity.ConnectionBrokerOBO{
			ClientID: "client-1", Scopes: []string{"orders.read"}, MaxTokenTTLSeconds: 300,
		},
	}
	if err := ValidateIdentityWrite(broker, provider); !errors.Is(err, outboundidentity.ErrIdentityConnectionNotReady) {
		t.Fatalf("broker without machine secret: %v", err)
	}
	secretID := "028f1f2e-7b5a-7c3d-8e9f-1234567890af"
	broker.MachineCredentialSecretID = &secretID
	if err := ValidateIdentityWrite(broker, provider); err != nil {
		t.Fatalf("broker ready: %v", err)
	}
}

func validOutboundProviderJSON() string {
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

func jsonRaw(s string) []byte { return []byte(s) }
