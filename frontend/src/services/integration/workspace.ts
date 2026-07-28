/**
 * Shared workspace id resolution for integration domain stores (ZKL-64 item 10).
 */
import { useWorkspaceStore } from "../../stores/workspaces";

export function requireActiveWorkspaceId(): string {
  const store = useWorkspaceStore();
  const workspaceID = store.activeWorkspaceId || store.items[0]?.id;
  if (!workspaceID) {
    throw new Error("当前没有可用的业务空间。请先创建业务空间，或联系管理员加入已有空间。");
  }
  return workspaceID;
}

/** Only active workspace context — never treat a loaded page as full catalog. */
export async function accessibleWorkspaceIDs(): Promise<string[]> {
  const store = useWorkspaceStore();
  if (!store.items.length) await store.load();
  return [requireActiveWorkspaceId()];
}
