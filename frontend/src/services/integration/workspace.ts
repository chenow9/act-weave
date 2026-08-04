/**
 * Shared workspace id resolution for integration domain stores (ZKL-64 item 10).
 */
import { tt } from "../../i18n/tt";
import { useWorkspaceStore } from "../../stores/workspaces";

export function requireActiveWorkspaceId(): string {
  const store = useWorkspaceStore();
  const workspaceID = store.activeWorkspaceId || store.items[0]?.id;
  if (!workspaceID) {
    throw new Error(tt("common.noActiveWorkspace"));
  }
  return workspaceID;
}

/** Only active workspace context — never treat a loaded page as full catalog. */
export async function accessibleWorkspaceIDs(): Promise<string[]> {
  const store = useWorkspaceStore();
  if (!store.items.length) await store.load();
  return [requireActiveWorkspaceId()];
}
