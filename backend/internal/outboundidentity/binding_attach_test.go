package outboundidentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBindingAttachPassthroughHappyPath(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	attacher, err := NewBindingAttacher(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	connID := "018f70a0-0001-7000-8000-0000000000aa"
	req := Requirements{
		SchemaVersion: SchemaRequirements,
		Connections: []RequirementConnection{{
			ConnectionID: connID, ProviderID: "prov-1", Mode: ModeRequestPassthrough,
			ProviderContractVersion: 1, ConnectionPolicyVersion: 2,
			RequiredScopes: []string{"orders.read"}, CredentialRequired: true,
		}},
	}
	token := canaryToken("attach-happy")
	expires := clock.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
	raw := json.RawMessage(`{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"` + connID + `","credentialType":"ACCESS_TOKEN","value":"` + string(token) + `","expiresAt":"` + expires + `"}]}`)
	result, err := attacher.Attach(context.Background(), BindingAttachInput{
		RawEnvelope:  raw,
		Requirements: req,
		Connections: []ConnectionPolicyView{{
			ConnectionID: connID, ProviderID: "prov-1", Mode: ModeRequestPassthrough,
			ConnectionPolicyVersion: 2, ProviderContractVersion: 1,
			MaxResidenceSeconds: 600, Executable: true,
		}},
		Context: BindingAttachContext{
			BootID: testBoot, WorkspaceID: testWorkspace,
			SubjectType: SubjectTypeUser, SubjectID: testSubject,
			RootScopeType: RootScopeAgentRun, RootScopeID: testRootID,
			RootDeadline: clock.Now().Add(10 * time.Minute), Now: clock.Now(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Attached || result.CredentialDescriptorHash == "" {
		t.Fatalf("result: %+v", result)
	}
	if strings.Contains(result.CredentialDescriptorHash, "CANARY") {
		t.Fatal("hash leaked canary")
	}
	// Vault has entry.
	key := VaultKey{
		BootID: testBoot, WorkspaceID: testWorkspace,
		SubjectType: SubjectTypeUser, SubjectID: testSubject,
		RootScopeType: RootScopeAgentRun, RootScopeID: testRootID,
		ConnectionID: connID, ConnectionPolicyVersion: 2,
	}
	b, err := vault.Borrow(key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(b.Bytes, canaryToken("attach-happy")) {
		t.Fatal("token mismatch")
	}
	b.Release()
	// Descriptor hash must not include value/expiresAt.
	if _, err := CredentialDescriptorHash(req, []CredentialBinding{{
		ConnectionID: connID, CredentialType: CredentialTypeAccessToken,
		Value: token, ExpiresAt: clock.Now().Add(5 * time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingAttachIdempotentReplay(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	attacher, _ := NewBindingAttacher(vault, nil)
	raw := []byte(`{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"CANARY-REPLAY","expiresAt":"2099-01-01T00:00:00Z"}]}`)
	// Alive vault → discard plaintext, succeed as replay.
	res, err := attacher.Attach(context.Background(), BindingAttachInput{
		RawEnvelope: raw, ExistingRunID: "run-existing", ExistingVaultAlive: true,
		Requirements: Requirements{SchemaVersion: SchemaRequirements},
		Context: BindingAttachContext{
			BootID: testBoot, WorkspaceID: testWorkspace,
			SubjectType: SubjectTypeUser, SubjectID: testSubject,
			RootScopeType: RootScopeAgentRun, RootScopeID: testRootID, Now: clock.Now(),
		},
	})
	if err != nil || !res.IdempotentReplay {
		t.Fatalf("alive replay: %+v %v", res, err)
	}
	// Dead vault → OUTBOUND_CREDENTIAL_EXPIRED, no rebind.
	_, err = attacher.Attach(context.Background(), BindingAttachInput{
		RawEnvelope: append([]byte(nil), raw...), ExistingRunID: "run-existing", ExistingVaultAlive: false,
		Requirements: Requirements{SchemaVersion: SchemaRequirements},
		Context: BindingAttachContext{
			BootID: testBoot, WorkspaceID: testWorkspace,
			SubjectType: SubjectTypeUser, SubjectID: testSubject,
			RootScopeType: RootScopeAgentRun, RootScopeID: testRootID, Now: clock.Now(),
		},
	})
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("dead replay: %v", err)
	}
}

func TestBindingAttachRejectsBrokerBindingAndMissing(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC))
	vault, _ := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	attacher, _ := NewBindingAttacher(vault, nil)
	connPass := "018f70a0-0001-7000-8000-0000000000bb"
	connBroker := "018f70a0-0001-7000-8000-0000000000cc"
	req := Requirements{
		SchemaVersion: SchemaRequirements,
		Connections: []RequirementConnection{
			{
				ConnectionID: connPass, ProviderID: "p", Mode: ModeRequestPassthrough,
				ProviderContractVersion: 1, ConnectionPolicyVersion: 1, CredentialRequired: true,
			},
			{
				ConnectionID: connBroker, ProviderID: "p", Mode: ModeBrokerOBO,
				ProviderContractVersion: 1, ConnectionPolicyVersion: 1,
			},
		},
	}
	ctx := BindingAttachContext{
		BootID: testBoot, WorkspaceID: testWorkspace,
		SubjectType: SubjectTypeUser, SubjectID: testSubject,
		RootScopeType: RootScopeAgentRun, RootScopeID: testRootID, Now: clock.Now(),
	}
	// Missing envelope.
	if _, err := attacher.Attach(context.Background(), BindingAttachInput{
		Requirements: req, Context: ctx,
		Connections: []ConnectionPolicyView{{
			ConnectionID: connPass, ProviderID: "p", Mode: ModeRequestPassthrough,
			ConnectionPolicyVersion: 1, ProviderContractVersion: 1, Executable: true,
		}},
	}); !errors.Is(err, ErrCredentialRequired) {
		t.Fatalf("missing: %v", err)
	}
	// Broker connection in binding rejected.
	exp := clock.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	raw := json.RawMessage(`{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"` + connBroker + `","credentialType":"ACCESS_TOKEN","value":"CANARY-broker","expiresAt":"` + exp + `"}]}`)
	if _, err := attacher.Attach(context.Background(), BindingAttachInput{
		RawEnvelope: raw, Requirements: req, Context: ctx,
		Connections: []ConnectionPolicyView{
			{ConnectionID: connPass, ProviderID: "p", Mode: ModeRequestPassthrough, ConnectionPolicyVersion: 1, ProviderContractVersion: 1, Executable: true},
			{ConnectionID: connBroker, ProviderID: "p", Mode: ModeBrokerOBO, ConnectionPolicyVersion: 1, ProviderContractVersion: 1, Executable: true},
		},
	}); err == nil {
		t.Fatal("expected broker binding reject")
	}
	// Policy drift.
	raw2 := json.RawMessage(`{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"` + connPass + `","credentialType":"ACCESS_TOKEN","value":"CANARY-drift","expiresAt":"` + exp + `"}]}`)
	if _, err := attacher.Attach(context.Background(), BindingAttachInput{
		RawEnvelope: raw2, Requirements: req, Context: ctx,
		Connections: []ConnectionPolicyView{{
			ConnectionID: connPass, ProviderID: "p", Mode: ModeRequestPassthrough,
			ConnectionPolicyVersion: 99, ProviderContractVersion: 1, Executable: true,
		}},
	}); !errors.Is(err, ErrIdentityPolicyChanged) {
		t.Fatalf("drift: %v", err)
	}
}

func TestBindingAttachPureBrokerNoEnvelope(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC))
	vault, _ := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	attacher, _ := NewBindingAttacher(vault, nil)
	req := Requirements{
		SchemaVersion: SchemaRequirements,
		Connections: []RequirementConnection{{
			ConnectionID: "c-broker", ProviderID: "p", Mode: ModeBrokerOBO,
			ProviderContractVersion: 1, ConnectionPolicyVersion: 1,
		}},
	}
	res, err := attacher.Attach(context.Background(), BindingAttachInput{
		Requirements: req,
		Context: BindingAttachContext{
			BootID: testBoot, WorkspaceID: testWorkspace,
			SubjectType: SubjectTypeUser, SubjectID: testSubject,
			RootScopeType: RootScopeAgentRun, RootScopeID: testRootID, Now: clock.Now(),
		},
	})
	if err != nil || res.Attached || res.RequiresPassthrough {
		t.Fatalf("%+v %v", res, err)
	}
	// Envelope not allowed for pure broker.
	raw := []byte(`{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"c-broker","credentialType":"ACCESS_TOKEN","value":"x","expiresAt":"2099-01-01T00:00:00Z"}]}`)
	if _, err := attacher.Attach(context.Background(), BindingAttachInput{
		RawEnvelope: raw, Requirements: req,
		Context: BindingAttachContext{
			BootID: testBoot, WorkspaceID: testWorkspace,
			SubjectType: SubjectTypeUser, SubjectID: testSubject,
			RootScopeType: RootScopeAgentRun, RootScopeID: testRootID, Now: clock.Now(),
		},
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("broker with envelope: %v", err)
	}
}

func TestStripAndExtractOutboundCredentials(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}],"outboundCredentials":{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"CANARY-BODY","expiresAt":"2099-01-01T00:00:00Z"}]}}`)
	raw, err := ExtractOutboundCredentialsRaw(body)
	if err != nil || !strings.Contains(string(raw), "CANARY-BODY") {
		t.Fatalf("extract: %s %v", raw, err)
	}
	stripped, err := StripOutboundCredentialsFromBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stripped), "CANARY") || strings.Contains(string(stripped), "outboundCredentials") {
		t.Fatalf("stripped still has secrets: %s", stripped)
	}
	if !strings.Contains(string(stripped), `"input"`) {
		t.Fatalf("business fields lost: %s", stripped)
	}
}

func TestCredentialDescriptorHashExcludesSecrets(t *testing.T) {
	req := Requirements{SchemaVersion: SchemaRequirements, Connections: []RequirementConnection{{
		ConnectionID: "c1", ProviderID: "p", Mode: ModeRequestPassthrough,
		ProviderContractVersion: 1, ConnectionPolicyVersion: 1, CredentialRequired: true,
	}}}
	bindings := []CredentialBinding{{
		ConnectionID: "c1", CredentialType: CredentialTypeAccessToken,
		Value: []byte("CANARY-HASH-SECRET"), ExpiresAt: time.Now().Add(time.Minute),
	}}
	h, err := CredentialDescriptorHash(req, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "CANARY") || len(h) != 64 {
		t.Fatalf("hash: %s", h)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
