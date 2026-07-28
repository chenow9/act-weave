import { computed, type ComputedRef } from "vue";

import { WORKSPACE_ROLE_ACTIONS, useWorkspaceStore, type WorkspaceAction } from "../stores/workspaces";
import type { WorkspaceRole } from "../types/domain";

/**
 * Pure projection helpers aligned with backend workspace_policy.go.
 * Does not fetch; only reads Workspace.currentUserRole from the store.
 */
export function actionsForRole(role: WorkspaceRole | "" | undefined): readonly WorkspaceAction[] {
  if (!role) return [];
  return WORKSPACE_ROLE_ACTIONS[role] || [];
}

export function roleCan(role: WorkspaceRole | "" | undefined, action: WorkspaceAction): boolean {
  return actionsForRole(role).includes(action);
}

export function useWorkspacePermission(workspaceId: ComputedRef<string> | (() => string)) {
  const workspaces = useWorkspaceStore();
  const resolveId = () => (typeof workspaceId === "function" ? workspaceId() : workspaceId.value);

  const role = computed(() => workspaces.roleFor(resolveId()) || "");
  const can = (action: WorkspaceAction) => workspaces.can(resolveId(), action);
  const canView = computed(() => can("VIEW"));
  const canEdit = computed(() => can("EDIT"));
  const canTest = computed(() => can("TEST"));
  const canPublish = computed(() => can("PUBLISH"));
  const canExecute = computed(() => can("EXECUTE"));
  const canManage = computed(() => can("MANAGE"));
  const canDelete = computed(() => can("DELETE"));
  /** Permanent no-permission → hide writes; unknown role → read-only (D2-A). */
  const writesHidden = computed(() => !role.value || !can("EDIT"));

  return {
    role,
    can,
    canView,
    canEdit,
    canTest,
    canPublish,
    canExecute,
    canManage,
    canDelete,
    writesHidden,
  };
}
