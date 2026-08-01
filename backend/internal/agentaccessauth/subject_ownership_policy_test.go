package agentaccessauth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const (
	ownershipSubjectID      = "a78f1f2e-7b5a-7c3d-8e9f-123456789001"
	ownershipOtherSubjectID = "a78f1f2e-7b5a-7c3d-8e9f-123456789002"
)

func TestSubjectOwnershipPolicy(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	caller := authorizationTestPrincipal(now)
	caller.PrincipalID = ownershipSubjectID
	caller.Scopes = append([]string(nil), canonicalAAPScopes...)
	state := authorizationTestState(caller)
	state.GrantScopes = append([]string(nil), canonicalAAPScopes...)
	state.AgentPolicyScopes = append([]string(nil), canonicalAAPScopes...)
	state.SubjectSharingResources = []string{"conversation", "run", "event", "interaction", "artifact", "file"}

	tests := []struct {
		name       string
		action     AAPAction
		resource   AAPAuthorizationResource
		sharingKey string
	}{
		{"conversation read", ActionConversationRead, ownershipResource(ResourceConversation, 10), "conversation"},
		{"run create", ActionRunCreate, ownershipResource(ResourceConversation, 11), "conversation"},
		{"run read", ActionRunRead, ownershipResource(ResourceRun, 12), "run"},
		{"run cancel", ActionRunCancel, ownershipResource(ResourceRun, 13), "run"},
		{"event read", ActionEventRead, ownershipResource(ResourceRun, 14), "event"},
		{"interaction decide", ActionInteractionDecide, ownershipResource(ResourceInteraction, 15), "interaction"},
		{"artifact read", ActionArtifactRead, ownershipResource(ResourceArtifact, 16), "artifact"},
		{"file complete", ActionFileComplete, ownershipResource(ResourceFile, 17), "file"},
		{"file read", ActionFileRead, ownershipResource(ResourceFile, 18), "file"},
		{"file content", ActionFileContent, ownershipResource(ResourceFile, 19), "file"},
	}
	for _, test := range tests {
		t.Run(test.name+" subject owner", func(t *testing.T) {
			store := &subjectOwnershipRecordStub{record: ownedSubjectRecord(caller, state, test.resource)}
			service, audit := newSubjectOwnershipAuthorization(t, now, state, store)
			decision, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: caller, Action: test.action, Resource: test.resource,
			})
			if err != nil || decision.Snapshot.OwnershipMode != OwnershipModeSubjectOwned ||
				decision.Snapshot.OwnershipPolicyVersion != 7 || len(audit.denials) != 0 ||
				store.action != test.action {
				t.Fatalf("decision=%+v audit=%+v action=%s err=%v",
					decision, audit.denials, store.action, err)
			}
		})

		t.Run(test.name+" cross subject concealed", func(t *testing.T) {
			store := &subjectOwnershipRecordStub{record: ownedSubjectRecord(caller, state, test.resource)}
			service, audit := newSubjectOwnershipAuthorization(t, now, state, store)
			other := caller
			other.PrincipalID = ownershipOtherSubjectID
			_, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: other, Action: test.action, Resource: test.resource,
			})
			assertOwnershipDenial(t, err, audit, OwnershipReasonSubjectMismatch)
		})

		t.Run(test.name+" sharing requires Grant policy", func(t *testing.T) {
			record := ownedSubjectRecord(caller, state, test.resource)
			record.Mode, record.SubjectType, record.SubjectID = OwnershipModePolicyShared, "", ""
			store := &subjectOwnershipRecordStub{record: record}
			withoutSharing := state
			withoutSharing.SubjectSharingResources = nil
			service, audit := newSubjectOwnershipAuthorization(t, now, withoutSharing, store)
			_, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: caller, Action: test.action, Resource: test.resource,
			})
			assertOwnershipDenial(t, err, audit, OwnershipReasonSharingDenied)

			allowed := state
			allowed.SubjectSharingResources = []string{test.sharingKey}
			service, audit = newSubjectOwnershipAuthorization(t, now, allowed, store)
			decision, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: caller, Action: test.action, Resource: test.resource,
			})
			if err != nil || decision.Snapshot.OwnershipMode != OwnershipModePolicyShared ||
				decision.Snapshot.OwnershipPolicyVersion != state.GrantVersion || len(audit.denials) != 0 {
				t.Fatalf("shared decision=%+v audit=%+v err=%v", decision, audit.denials, err)
			}
		})
	}

	t.Run("pure Service Principal keeps its private and shared resources", func(t *testing.T) {
		pure := caller
		pure.PrincipalID = pure.ServicePrincipalID
		pureState := state
		pureState.SubjectSharingResources = nil
		resource := ownershipResource(ResourceRun, 20)
		for _, mode := range []string{OwnershipModeSubjectOwned, OwnershipModePolicyShared} {
			record := ownedSubjectRecord(pure, pureState, resource)
			record.Mode, record.SubjectType, record.SubjectID = mode, "", ""
			service, _ := newSubjectOwnershipAuthorization(
				t, now, pureState, &subjectOwnershipRecordStub{record: record},
			)
			if _, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
				Principal: pure, Action: ActionRunRead, Resource: resource,
			}); err != nil {
				t.Fatalf("pure Service Principal mode=%s err=%v", mode, err)
			}
		}
	})

	t.Run("binding and Artifact reasons remain distinguishable only in audit", func(t *testing.T) {
		resource := ownershipResource(ResourceArtifact, 21)
		base := ownedSubjectRecord(caller, state, resource)
		cases := []struct {
			name   string
			reason string
			mutate func(*SubjectOwnershipRecord)
		}{
			{"scope", OwnershipReasonScopeMismatch, func(value *SubjectOwnershipRecord) { value.AgentID = ownershipOtherSubjectID }},
			{"actor", OwnershipReasonActorMismatch, func(value *SubjectOwnershipRecord) { value.ActorID = ownershipOtherSubjectID }},
			{"client", OwnershipReasonClientMismatch, func(value *SubjectOwnershipRecord) { value.ClientID = ownershipOtherSubjectID }},
			{"artifact", OwnershipReasonArtifactUnbound, func(value *SubjectOwnershipRecord) { value.ArtifactBound = false }},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				record := base
				test.mutate(&record)
				service, audit := newSubjectOwnershipAuthorization(
					t, now, state, &subjectOwnershipRecordStub{record: record},
				)
				_, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
					Principal: caller, Action: ActionArtifactRead, Resource: resource,
				})
				assertOwnershipDenial(t, err, audit, test.reason)
			})
		}
	})
}

type subjectOwnershipRecordStub struct {
	record SubjectOwnershipRecord
	err    error
	action AAPAction
}

func (stub *subjectOwnershipRecordStub) ResolveSubjectOwnershipRecord(
	_ context.Context,
	action AAPAction,
	_ AAPAuthorizationResource,
) (SubjectOwnershipRecord, error) {
	stub.action = action
	return stub.record, stub.err
}

func newSubjectOwnershipAuthorization(
	t *testing.T,
	now time.Time,
	state AAPAuthorizationState,
	store SubjectOwnershipStore,
) (*AAPAuthorizationService, *authorizationAuditStub) {
	t.Helper()
	policy, err := NewSubjectOwnershipPolicy(store)
	if err != nil {
		t.Fatal(err)
	}
	audit := &authorizationAuditStub{}
	service, err := NewAAPAuthorizationService(
		&authorizationStateStoreStub{state: state}, policy, WithAAPAuthorizationAudit(audit),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service, audit
}

func ownedSubjectRecord(
	caller AAPAccessTokenPrincipal,
	state AAPAuthorizationState,
	resource AAPAuthorizationResource,
) SubjectOwnershipRecord {
	return SubjectOwnershipRecord{
		WorkspaceID: caller.WorkspaceID, AgentID: caller.AgentID,
		ResourceType: resource.Type, ResourceID: resource.ID,
		ActorType: "SERVICE_PRINCIPAL", ActorID: caller.ServicePrincipalID,
		SubjectType: "EXTERNAL_SUBJECT", SubjectID: ownershipSubjectID,
		ClientID: state.ClientID, Mode: OwnershipModeSubjectOwned,
		PolicyVersion: 7, ArtifactBound: resource.Type == ResourceArtifact,
	}
}

func ownershipResource(resourceType AAPResourceType, suffix int) AAPAuthorizationResource {
	return AAPAuthorizationResource{
		Type: resourceType,
		ID:   fmt.Sprintf("a78f1f2e-7b5a-7c3d-8e9f-%012x", suffix),
	}
}

func assertOwnershipDenial(
	t *testing.T,
	err error,
	audit *authorizationAuditStub,
	reason string,
) {
	t.Helper()
	if !errors.Is(err, ErrAAPAuthorizationNotVisible) || len(audit.denials) != 1 ||
		audit.denials[0].Reason != reason {
		t.Fatalf("denial err=%v audit=%+v want reason=%s", err, audit.denials, reason)
	}
}
