package storedobject

import (
	"context"
	"errors"
	"strings"

	"actweave/backend/internal/authz"
)

type WorkspaceActionAuthorizer interface {
	AuthorizeWorkspace(context.Context, string, string, authz.Action) (authz.WorkspaceContext, error)
}

// WorkspaceReadAuthorizer maps StoredObject classification to the current
// Workspace policy. Public through sensitive business content follows normal
// Workspace visibility; restricted objects require current manage permission.
// Non-user principals are rejected until their real authorization source is
// implemented rather than being treated as implicitly trusted.
type WorkspaceReadAuthorizer struct{ workspaces WorkspaceActionAuthorizer }

func NewWorkspaceReadAuthorizer(workspaces WorkspaceActionAuthorizer) (*WorkspaceReadAuthorizer, error) {
	if workspaces == nil {
		return nil, errors.New("stored object workspace authorizer is required")
	}
	return &WorkspaceReadAuthorizer{workspaces: workspaces}, nil
}

func (authorizer *WorkspaceReadAuthorizer) AuthorizeStoredObjectRead(
	ctx context.Context,
	request ReadAuthorization,
) error {
	request.ActorType = strings.ToUpper(strings.TrimSpace(request.ActorType))
	request.ActorID = strings.TrimSpace(request.ActorID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	if request.ActorType != CreatorUser || !validUUID(request.ActorID) ||
		!validUUID(request.WorkspaceID) || !validClassification(request.Classification) {
		return authz.ErrDenied
	}
	action := authz.ActionView
	if request.Classification == ClassificationRestricted {
		action = authz.ActionManage
	}
	_, err := authorizer.workspaces.AuthorizeWorkspace(ctx, request.ActorID, request.WorkspaceID, action)
	return err
}
