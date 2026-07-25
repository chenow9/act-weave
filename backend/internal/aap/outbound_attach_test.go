package aap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/outboundidentity"
)

const (
	testOutboundConnID = "e51f1f2e-7b5a-7c3d-8e9f-1234567890aa"
	testOutboundBoot   = "boot-aap-run-test"
	testCanaryToken    = "CANARY-AAP-CREATE-RUN-TOKEN"
)

type stubAgentOutboundLoader struct {
	requirements outboundidentity.Requirements
	views        []outboundidentity.ConnectionPolicyView
	err          error
	calls        int
}

func (s *stubAgentOutboundLoader) LoadAgentOutbound(
	_ context.Context, _, _ string,
) (outboundidentity.Requirements, []outboundidentity.ConnectionPolicyView, error) {
	s.calls++
	if s.err != nil {
		return outboundidentity.Requirements{}, nil, s.err
	}
	return s.requirements, s.views, nil
}

func TestRunServiceAttachesPassthroughEnvelope(t *testing.T) {
	store := &runServiceStore{}
	events := &runServiceEvents{}
	lifecycle := &runServiceLifecycle{events: events}
	dispatcher := &runServiceDispatcher{lifecycle: lifecycle}
	service, err := NewRunService(store, store, lifecycle, events, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	// Use wall clock so RootDeadline (time.Now) and vault residence align.
	vault, err := outboundidentity.NewRuntimeCredentialVault(testOutboundBoot, nil, outboundidentity.VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	attacher, err := outboundidentity.NewBindingAttacher(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	loader := &stubAgentOutboundLoader{
		requirements: outboundidentity.Requirements{
			SchemaVersion: outboundidentity.SchemaRequirements,
			Connections: []outboundidentity.RequirementConnection{{
				ConnectionID: testOutboundConnID, ProviderID: "prov-1",
				Mode:                    outboundidentity.ModeRequestPassthrough,
				ProviderContractVersion: 1, ConnectionPolicyVersion: 1,
				CredentialRequired: true,
			}},
		},
		views: []outboundidentity.ConnectionPolicyView{{
			ConnectionID: testOutboundConnID, ProviderID: "prov-1",
			Mode:                    outboundidentity.ModeRequestPassthrough,
			ConnectionPolicyVersion: 1, ProviderContractVersion: 1, Executable: true,
		}},
	}
	if err := service.ConfigureOutbound(attacher, loader, testOutboundBoot); err != nil {
		t.Fatal(err)
	}

	input := validRunServiceInput()
	raw, _ := json.Marshal(map[string]any{
		"schemaVersion": "outbound-credentials.v1",
		"bindings": []map[string]any{{
			"connectionId": testOutboundConnID, "credentialType": "ACCESS_TOKEN",
			"value": testCanaryToken, "expiresAt": "2099-01-01T00:00:00Z",
		}},
	})
	input.OutboundCredentialsRaw = raw

	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Run.ID == "" || loader.calls != 1 {
		t.Fatalf("created=%+v loaderCalls=%d", created, loader.calls)
	}
	// Token must never enter durable chat/run snapshots.
	if strings.Contains(string(store.sent.RunInputSummary), testCanaryToken) ||
		strings.Contains(string(store.sent.AuthorizationSnapshot), testCanaryToken) ||
		strings.Contains(string(store.run.InputSummary), testCanaryToken) {
		t.Fatal("canary token leaked into durable run storage")
	}
	// Vault must hold binding under AgentRun root for tool inject.
	handle, borrowErr := vault.Borrow(outboundidentity.VaultKey{
		BootID: testOutboundBoot, WorkspaceID: testRunWorkspaceID,
		SubjectType: outboundidentity.SubjectTypeExternalSubject, SubjectID: testRunSubjectID,
		RootScopeType: outboundidentity.RootScopeAgentRun, RootScopeID: created.Run.ID,
		ConnectionID: testOutboundConnID, ConnectionPolicyVersion: 1,
	})
	if borrowErr != nil {
		t.Fatalf("vault borrow: %v", borrowErr)
	}
	if string(handle.Bytes) != testCanaryToken {
		t.Fatalf("token=%q", handle.Bytes)
	}
	handle.Release()
}

func TestRunServiceFailsClosedWithoutEnvelopeWhenPassthroughRequired(t *testing.T) {
	store := &runServiceStore{}
	events := &runServiceEvents{}
	lifecycle := &runServiceLifecycle{events: events}
	dispatcher := &runServiceDispatcher{lifecycle: lifecycle}
	service, err := NewRunService(store, store, lifecycle, events, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := outboundidentity.NewRuntimeCredentialVault(testOutboundBoot, nil, outboundidentity.VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	attacher, err := outboundidentity.NewBindingAttacher(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	loader := &stubAgentOutboundLoader{
		requirements: outboundidentity.Requirements{
			SchemaVersion: outboundidentity.SchemaRequirements,
			Connections: []outboundidentity.RequirementConnection{{
				ConnectionID: testOutboundConnID, ProviderID: "prov-1",
				Mode:                    outboundidentity.ModeRequestPassthrough,
				ProviderContractVersion: 1, ConnectionPolicyVersion: 1,
				CredentialRequired: true,
			}},
		},
		views: []outboundidentity.ConnectionPolicyView{{
			ConnectionID: testOutboundConnID, ProviderID: "prov-1",
			Mode:                    outboundidentity.ModeRequestPassthrough,
			ConnectionPolicyVersion: 1, ProviderContractVersion: 1, Executable: true,
		}},
	}
	if err := service.ConfigureOutbound(attacher, loader, testOutboundBoot); err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), validRunServiceInput())
	if !errors.Is(err, outboundidentity.ErrCredentialRequired) {
		t.Fatalf("error=%v", err)
	}
	if store.sendCalls != 0 {
		t.Fatal("create proceeded without credentials")
	}
}

func TestRunServiceRejectsEnvelopeWhenAttacherNotWired(t *testing.T) {
	store := &runServiceStore{}
	events := &runServiceEvents{}
	lifecycle := &runServiceLifecycle{events: events}
	dispatcher := &runServiceDispatcher{lifecycle: lifecycle}
	service, err := NewRunService(store, store, lifecycle, events, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	input := validRunServiceInput()
	input.OutboundCredentialsRaw = json.RawMessage(`{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"` + testOutboundConnID + `","credentialType":"ACCESS_TOKEN","value":"` + testCanaryToken + `","expiresAt":"2099-01-01T00:00:00Z"}]}`)
	_, err = service.Create(context.Background(), input)
	if !errors.Is(err, outboundidentity.ErrCredentialInvalid) {
		t.Fatalf("error=%v", err)
	}
	if store.sendCalls != 0 {
		t.Fatal("create proceeded with unwired attacher")
	}
}
