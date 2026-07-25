package authz

import (
	"errors"
	"testing"

	"actweave/backend/internal/workspace"
)

func TestWorkspaceRoleMatrix(t *testing.T) {
	actions := []Action{
		ActionView,
		ActionEdit,
		ActionTest,
		ActionPublish,
		ActionExecute,
		ActionManage,
		ActionDelete,
	}
	tests := []struct {
		role    workspace.Role
		allowed map[Action]bool
	}{
		{
			role: workspace.RoleOwner,
			allowed: allowedActions(
				ActionView,
				ActionEdit,
				ActionTest,
				ActionPublish,
				ActionExecute,
				ActionManage,
				ActionDelete,
			),
		},
		{
			role: workspace.RoleAdmin,
			allowed: allowedActions(
				ActionView,
				ActionEdit,
				ActionTest,
				ActionPublish,
				ActionExecute,
				ActionManage,
			),
		},
		{
			role: workspace.RoleEditor,
			allowed: allowedActions(
				ActionView,
				ActionEdit,
				ActionTest,
				ActionPublish,
				ActionExecute,
			),
		},
		{
			role: workspace.RoleOperator,
			allowed: allowedActions(
				ActionView,
				ActionTest,
				ActionExecute,
			),
		},
		{
			role:    workspace.RoleViewer,
			allowed: allowedActions(ActionView),
		},
	}

	for _, test := range tests {
		t.Run(string(test.role), func(t *testing.T) {
			for _, action := range actions {
				want := test.allowed[action]
				if got := CanWorkspace(test.role, action); got != want {
					t.Fatalf("CanWorkspace(%q, %q)=%t, want %t", test.role, action, got, want)
				}
				err := RequireWorkspace(test.role, action)
				if want && err != nil {
					t.Fatalf("RequireWorkspace(%q, %q) denied allowed action: %v", test.role, action, err)
				}
				if !want && !errors.Is(err, ErrDenied) {
					t.Fatalf("RequireWorkspace(%q, %q) error=%v, want ErrDenied", test.role, action, err)
				}
			}
		})
	}
}

func TestWorkspaceRoleMatrixDeniesUnknownValues(t *testing.T) {
	for _, test := range []struct {
		role   workspace.Role
		action Action
	}{
		{role: workspace.Role("PRIVATE"), action: ActionView},
		{role: workspace.RoleEditor, action: Action("RESOURCE_GRANT")},
		{role: "", action: ActionView},
		{role: workspace.RoleOwner, action: ""},
	} {
		if CanWorkspace(test.role, test.action) {
			t.Fatalf("unknown role/action was allowed: role=%q action=%q", test.role, test.action)
		}
	}
}

func allowedActions(actions ...Action) map[Action]bool {
	allowed := make(map[Action]bool, len(actions))
	for _, action := range actions {
		allowed[action] = true
	}
	return allowed
}
