package agentaccessauth

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/storedobject"
)

type stubAAPFileObjectBindingStore struct {
	actorType string
	actorID   string
	err       error
	calls     int
	lastKind  string
}

func (stub *stubAAPFileObjectBindingStore) LookupAAPFileObjectActor(
	_ context.Context,
	_, _, kind string,
) (string, string, error) {
	stub.calls++
	stub.lastKind = kind
	return stub.actorType, stub.actorID, stub.err
}

func TestAAPFileObjectAuthorizerSystemTrustedPath(t *testing.T) {
	stub := &stubAAPFileObjectBindingStore{err: errors.New("must not query for SYSTEM")}
	authorizer := &AAPFileObjectAuthorizer{bindings: stub}
	for _, kind := range []string{storedobject.KindAAPFile, storedobject.KindAAPFileDerived} {
		err := authorizer.AuthorizeStoredObjectRead(context.Background(), storedobject.ReadAuthorization{
			WorkspaceID: "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
			ObjectID:    "a68f1f2e-7b5a-7c3d-8e9f-1234567890a1",
			ActorType:   storedobject.CreatorSystem,
			ActorID:     "a68f1f2e-7b5a-7c3d-8e9f-123456789099",
			Kind:        kind,
		})
		if err != nil {
			t.Fatalf("SYSTEM open kind=%s: %v", kind, err)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("SYSTEM path must not look up bindings: calls=%d", stub.calls)
	}
}

func TestAAPFileObjectAuthorizerServicePrincipalBinding(t *testing.T) {
	spID := "a68f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	stub := &stubAAPFileObjectBindingStore{
		actorType: "SERVICE_PRINCIPAL", actorID: spID,
	}
	authorizer := &AAPFileObjectAuthorizer{bindings: stub}
	err := authorizer.AuthorizeStoredObjectRead(context.Background(), storedobject.ReadAuthorization{
		WorkspaceID: "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
		ObjectID:    "a68f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		ActorType:   storedobject.CreatorServicePrincipal,
		ActorID:     spID,
		Kind:        storedobject.KindAAPFile,
	})
	if err != nil || stub.calls != 1 {
		t.Fatalf("SP matching binding err=%v calls=%d", err, stub.calls)
	}

	// Mismatched SP actor is denied.
	err = authorizer.AuthorizeStoredObjectRead(context.Background(), storedobject.ReadAuthorization{
		WorkspaceID: "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
		ObjectID:    "a68f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		ActorType:   storedobject.CreatorServicePrincipal,
		ActorID:     "a68f1f2e-7b5a-7c3d-8e9f-1234567890b2",
		Kind:        storedobject.KindAAPFile,
	})
	if !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("mismatched SP want denied, got %v", err)
	}

	// Missing binding is denied.
	stub.err = sql.ErrNoRows
	err = authorizer.AuthorizeStoredObjectRead(context.Background(), storedobject.ReadAuthorization{
		WorkspaceID: "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
		ObjectID:    "a68f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		ActorType:   storedobject.CreatorServicePrincipal,
		ActorID:     spID,
		Kind:        storedobject.KindAAPFileDerived,
	})
	if !errors.Is(err, authz.ErrDenied) || stub.lastKind != storedobject.KindAAPFileDerived {
		t.Fatalf("missing binding kind=%s err=%v", stub.lastKind, err)
	}
}

func TestAAPFileObjectAuthorizerRejectsUserAndUnknownKinds(t *testing.T) {
	authorizer := &AAPFileObjectAuthorizer{bindings: &stubAAPFileObjectBindingStore{}}
	base := storedobject.ReadAuthorization{
		WorkspaceID: "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
		ObjectID:    "a68f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		ActorType:   storedobject.CreatorUser,
		ActorID:     "a68f1f2e-7b5a-7c3d-8e9f-123456789001",
		Kind:        storedobject.KindAAPFile,
	}
	if err := authorizer.AuthorizeStoredObjectRead(context.Background(), base); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("USER must be denied without loosening WorkspaceReadAuthorizer: %v", err)
	}
	base.ActorType = storedobject.CreatorSystem
	base.Kind = storedobject.KindChatMessage
	if err := authorizer.AuthorizeStoredObjectRead(context.Background(), base); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("non-AAP kind must be denied: %v", err)
	}
}

func TestNewAAPFileObjectAuthorizerRequiresDB(t *testing.T) {
	if _, err := NewAAPFileObjectAuthorizer(nil); err == nil {
		t.Fatal("expected error for nil db")
	}
}
