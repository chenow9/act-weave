package agentaccessauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/storedobject"
)

// aapFileObjectBindingStore resolves the owning Service Principal for a
// permanent AAP file (or derived) stored_object_id.
type aapFileObjectBindingStore interface {
	LookupAAPFileObjectActor(
		ctx context.Context,
		workspaceID, objectID, kind string,
	) (actorType, actorID string, err error)
}

// AAPFileObjectAuthorizer authorizes SecureStore Open for AAP permanent file
// objects (design §5.6.5). It is deliberately separate from
// storedobject.WorkspaceReadAuthorizer so Console USER semantics are not
// loosened to "also allow Service Principals".
//
// Rules:
//   - ActorType=SYSTEM + kind ∈ {AAP_FILE, AAP_FILE_DERIVED}: trusted worker /
//     download-token proxy path (caller has already validated the token or is
//     an in-process promote/pipeline worker).
//   - ActorType=SERVICE_PRINCIPAL: defense-in-depth — object must bind to an
//     aap_files (or derived artifact) row in the same workspace with matching
//     SP actor. Content routes still require AAP Authorize(file.content) first.
//   - All other principals / kinds: denied.
type AAPFileObjectAuthorizer struct {
	bindings aapFileObjectBindingStore
}

// NewAAPFileObjectAuthorizer constructs a storedobject.ReadAuthorizer for AAP files.
func NewAAPFileObjectAuthorizer(db *sql.DB) (*AAPFileObjectAuthorizer, error) {
	if db == nil {
		return nil, errors.New("AAP file object authorizer database is required")
	}
	return &AAPFileObjectAuthorizer{bindings: &sqlAAPFileObjectBindingStore{db: db}}, nil
}

// AuthorizeStoredObjectRead implements storedobject.ReadAuthorizer.
func (authorizer *AAPFileObjectAuthorizer) AuthorizeStoredObjectRead(
	ctx context.Context,
	request storedobject.ReadAuthorization,
) error {
	if authorizer == nil || authorizer.bindings == nil || ctx == nil {
		return authz.ErrDenied
	}
	request.ActorType = strings.ToUpper(strings.TrimSpace(request.ActorType))
	request.ActorID = strings.ToLower(strings.TrimSpace(request.ActorID))
	request.WorkspaceID = strings.ToLower(strings.TrimSpace(request.WorkspaceID))
	request.ObjectID = strings.ToLower(strings.TrimSpace(request.ObjectID))
	request.Kind = strings.ToUpper(strings.TrimSpace(request.Kind))
	if !validCanonicalUUID(request.WorkspaceID) || !validCanonicalUUID(request.ObjectID) ||
		!validCanonicalUUID(request.ActorID) {
		return authz.ErrDenied
	}
	if request.Kind != storedobject.KindAAPFile && request.Kind != storedobject.KindAAPFileDerived {
		return authz.ErrDenied
	}
	switch request.ActorType {
	case storedobject.CreatorSystem:
		// Promote/pipeline workers and download-token proxy after token validation.
		return nil
	case storedobject.CreatorServicePrincipal:
		return authorizer.authorizeServicePrincipalFileObject(ctx, request)
	default:
		return authz.ErrDenied
	}
}

func (authorizer *AAPFileObjectAuthorizer) authorizeServicePrincipalFileObject(
	ctx context.Context,
	request storedobject.ReadAuthorization,
) error {
	actorType, actorID, err := authorizer.bindings.LookupAAPFileObjectActor(
		ctx, request.WorkspaceID, request.ObjectID, request.Kind,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.ErrDenied
	}
	if err != nil {
		return fmt.Errorf("resolve AAP file object binding: %w", err)
	}
	actorType = strings.ToUpper(strings.TrimSpace(actorType))
	actorID = strings.ToLower(strings.TrimSpace(actorID))
	if actorType != "SERVICE_PRINCIPAL" || actorID != request.ActorID {
		return authz.ErrDenied
	}
	return nil
}

type sqlAAPFileObjectBindingStore struct {
	db *sql.DB
}

func (store *sqlAAPFileObjectBindingStore) LookupAAPFileObjectActor(
	ctx context.Context,
	workspaceID, objectID, kind string,
) (actorType, actorID string, err error) {
	switch kind {
	case storedobject.KindAAPFile:
		err = store.db.QueryRowContext(ctx, `
			SELECT actor_type, actor_id::text
			FROM aap_files
			WHERE workspace_id=$1 AND stored_object_id=$2
		`, workspaceID, objectID).Scan(&actorType, &actorID)
	case storedobject.KindAAPFileDerived:
		err = store.db.QueryRowContext(ctx, `
			SELECT f.actor_type, f.actor_id::text
			FROM aap_file_artifacts a
			JOIN aap_files f
			  ON f.workspace_id=a.workspace_id AND f.id=a.file_id
			WHERE a.workspace_id=$1 AND a.stored_object_id=$2
		`, workspaceID, objectID).Scan(&actorType, &actorID)
	default:
		return "", "", sql.ErrNoRows
	}
	return actorType, actorID, err
}
