import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const workspacesView = readFileSync(resolve(currentDir, "WorkspacesView.vue"), "utf8");
const appStyles = readFileSync(resolve(currentDir, "../styles/app.css"), "utf8");

describe("workspaces management list", () => {
  it("always uses the shared management list and row actions", () => {
    expect(workspacesView).toContain("ManagementListColumn<Workspace>");
    expect(workspacesView).toContain("<ManagementList");
    expect(workspacesView).toContain("<ManagementRowActions");
    expect(workspacesView).not.toContain('expanded-row-key="detailWorkspaceId"');
    expect(workspacesView).not.toContain("#row-detail");
    expect(workspacesView).toContain("workspace-detail-page");
    expect(workspacesView).toContain('role="tablist" aria-label="业务空间详情分区"');
    expect(workspacesView).toContain("返回业务空间列表");
    expect(workspacesView).not.toContain("workspace-compact-grid");
    expect(workspacesView).not.toContain("workspace-compact-card");
    expect(workspacesView).not.toContain("useCompactWorkspaceCards");
    expect(workspacesView).not.toContain("compactWorkspaces");
    expect(workspacesView).not.toContain("showWorkspaceAdvancedList");
    expect(workspacesView).not.toContain("workspace-resource-table");
    expect(workspacesView).not.toContain("<table");
  });

  it("exposes only v1 Workspace sort keys and preserves fixed columns", () => {
    for (const key of ["name", "status", "mode", "createdBy", "updatedBy"]) {
      expect(workspacesView).toContain(`sortable: true, sortKey: "${key}"`);
    }
    for (const legacy of ["healthScore", "agentCount", "toolCount", "workflowCount", "owner"]) {
      expect(workspacesView).not.toContain(`sortKey: "${legacy}"`);
    }
    expect(workspacesView).toContain('{ key: "actions", label: "操作", width: 68');
    expect(workspacesView).toContain(':sticky-left-keys="[\'selection\', \'identity\']"');
    expect(workspacesView).toContain(':sticky-right-keys="[\'actions\']"');
    expect(workspacesView).toContain('selection-tone="neutral"');
  });

  it("offers column settings for every non-fixed business column", () => {
    for (const key of ["status", "mode", "createdBy", "updatedBy"]) {
      expect(workspacesView).toMatch(new RegExp(`key: "${key}"[^\\n]+hidable: true`));
    }
    for (const key of ["selection", "identity", "actions"]) {
      expect(workspacesView).not.toMatch(new RegExp(`key: "${key}"[^\\n]+hidable: true`));
    }
    expect(workspacesView).toContain('storage-key="actweave:workspaces:columns"');
  });

  it("keeps status, toolbar controls, and long model content readable without overlap", () => {
    expect(appStyles).toContain(".workspace-management-list .status-pill");
    expect(appStyles).toContain(".workspace-management-list .workspace-model-name");
    expect(appStyles).toContain("max-width: 100%;");
    expect(appStyles).toContain(".workspace-page-selection-toggle");
    expect(appStyles).toContain("white-space: nowrap;");
  });

  it("uses store-backed local pagination and the common 10/20/50 contract", () => {
    expect(workspacesView).toContain("workspaces.pageItems");
    expect(workspacesView).toContain(":pagination=\"workspaces.pagination\"");
    expect(workspacesView).toContain("loadWorkspacePage");
    expect(workspacesView).toContain("mode:");
    expect(workspacesView).toContain("sortBy:");
    expect(workspacesView).toContain("sortOrder:");
    expect(workspacesView).not.toContain("filteredWorkspaces");
    expect(workspacesView).not.toContain("sortedWorkspaces");
    expect(workspacesView).not.toContain("paginatedWorkspaces");
    expect(workspacesView).not.toContain("workspacePageSizeOptions = [20, 50, 100]");
  });

  it("keeps selection current-page-only and clears selection plus stale details on context changes", () => {
    expect(workspacesView).toContain("selectedWorkspaceIds");
    expect(workspacesView).toContain("visibleWorkspaceIds");
    expect(workspacesView).toContain("clearWorkspaceListContext");
    expect(workspacesView).toContain("clearWorkspaceSelection");
    expect(workspacesView).toContain("reconcileWorkspaceListContext");
    expect(workspacesView).toContain("detailWorkspaceId.value, visibleWorkspaceIds.value.join");
    expect(workspacesView).toContain("bulkSetSelectedWorkspaceStatus");
    expect(workspacesView).toContain("workspaces.pageItems.filter");
  });

  it("opts into the viewport-constrained management page without clipping modals", () => {
    expect(workspacesView).toContain("management-page-grid");
    expect(workspacesView).toContain("ManagementPageHeader");
    expect(workspacesView).toContain("ManagementSummaryStrip");
    expect(workspacesView).toContain("management-list-card");
    expect(workspacesView).not.toContain("workspace-inline-detail-panel");
    expect(workspacesView).toContain("workspace-detail-tabs");
    expect(appStyles).toContain(".workspace-grid.management-page-grid");
    expect(appStyles).toContain(".workspace-list-card.management-list-card");
    expect(appStyles).toContain(".modal-backdrop");
  });

  it("preserves create, edit, lifecycle confirmation, and accessible model details", () => {
    expect(workspacesView).toContain("showWorkspaceModal");
    expect(workspacesView).toContain("workspaceStatusTarget");
    expect(workspacesView).toContain("workspaceDeleteTarget");
    expect(workspacesView).toContain("workspaceDeleteConfirmName");
    expect(workspacesView).toContain("workspace-model-readonly-card");
    expect(workspacesView).toContain("前往模型 API 配置");
    expect(workspacesView).not.toContain('aria-haspopup="listbox"');
    expect(workspacesView).toContain("action-toast");
  });

  it("keeps the mutable Workspace create choices and member/RBAC controls explicit", () => {
    expect(workspacesView).toContain('role="radiogroup" aria-label="环境模式"');
    expect(workspacesView).not.toContain('role="radiogroup" aria-label="状态"');
    expect(workspacesView).not.toContain('placeholder="例如: Platform Team"');
    expect(workspacesView).toContain("membersByWorkspace");
    expect(workspacesView).toContain("workspaces.can");
    expect(workspacesView).toContain("changeWorkspaceMemberRole");
    expect(appStyles).toContain("button.tone-production.selected");
    expect(appStyles).toContain("button.tone-sandbox.selected");
  });
});
