package authz

import (
	"errors"

	"actweave/backend/internal/workspace"
)

var ErrDenied = errors.New("workspace action denied")

type Action string

const (
	ActionView    Action = "VIEW"
	ActionEdit    Action = "EDIT"
	ActionTest    Action = "TEST"
	ActionPublish Action = "PUBLISH"
	ActionExecute Action = "EXECUTE"
	ActionManage  Action = "MANAGE"
	ActionDelete  Action = "DELETE"
)

var workspaceRoleActions = map[workspace.Role]map[Action]struct{}{
	workspace.RoleOwner: actionSet(
		ActionView,
		ActionEdit,
		ActionTest,
		ActionPublish,
		ActionExecute,
		ActionManage,
		ActionDelete,
	),
	workspace.RoleAdmin: actionSet(
		ActionView,
		ActionEdit,
		ActionTest,
		ActionPublish,
		ActionExecute,
		ActionManage,
	),
	workspace.RoleEditor: actionSet(
		ActionView,
		ActionEdit,
		ActionTest,
		ActionPublish,
		ActionExecute,
	),
	workspace.RoleOperator: actionSet(
		ActionView,
		ActionTest,
		ActionExecute,
	),
	workspace.RoleViewer: actionSet(
		ActionView,
	),
}

// CanWorkspace evaluates only the current Workspace role and requested action.
// Business resources are uniformly Workspace-visible; there is no resource ID,
// visibility flag, or grant lookup in this policy boundary.
func CanWorkspace(role workspace.Role, action Action) bool {
	actions, knownRole := workspaceRoleActions[role]
	if !knownRole {
		return false
	}
	_, allowed := actions[action]
	return allowed
}

func RequireWorkspace(role workspace.Role, action Action) error {
	if !CanWorkspace(role, action) {
		return ErrDenied
	}
	return nil
}

func actionSet(actions ...Action) map[Action]struct{} {
	set := make(map[Action]struct{}, len(actions))
	for _, action := range actions {
		set[action] = struct{}{}
	}
	return set
}
