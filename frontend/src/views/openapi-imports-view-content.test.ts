import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const openAPIImportsView = readFileSync(resolve(currentDir, "OpenAPIImportsView.vue"), "utf8");

describe("openapi imports view", () => {
  it("loads workspace and Provider context without coupling imports to Agent ownership", () => {
    expect(openAPIImportsView).toContain("useWorkspaceStore");
    expect(openAPIImportsView).not.toContain("useAgentStore");
    expect(openAPIImportsView).toContain("integration.loadOpenAPIImportPage");
    expect(openAPIImportsView).toContain("integration.loadServiceConnectionCatalog");
    expect(openAPIImportsView).toContain("integration.loadProviders");
    expect(openAPIImportsView).toContain("syncSelectedProvider");
    expect(openAPIImportsView).toContain("watch(");
    expect(openAPIImportsView).toContain("importForm.value.workspaceId");
    expect(openAPIImportsView).toContain("importForm.value.providerId");
    expect(openAPIImportsView).not.toContain("importForm.value.agentId");
    expect(openAPIImportsView).not.toContain("workspaces.selectWorkspace(");
  });

  it("requires workspace and an online-import-capable Provider and submits only the v1 import fields", () => {
    expect(openAPIImportsView).toContain("if (!workspaceId || !providerId) return false");
    expect(openAPIImportsView).toContain("selectedProviderCanImportOnline.value");
    expect(openAPIImportsView).toContain("const importProviders = computed(() => integration.providers || [])");
    expect(openAPIImportsView).toContain("provider.discoveryMode?.trim().toUpperCase() !== \"MANUAL\"");
    expect(openAPIImportsView).toContain("endpointConfig.sourceUri");
    expect(openAPIImportsView).toContain("documentUrl");
    expect(openAPIImportsView).toContain("workspaceId: importForm.value.workspaceId.trim()");
    expect(openAPIImportsView).toContain("providerId: importForm.value.providerId.trim()");
    expect(openAPIImportsView).not.toContain("agentId:");
    expect(openAPIImportsView).toContain("当前业务空间");
    expect(openAPIImportsView).toContain("在页面顶部切换");
    expect(openAPIImportsView).toContain("选择 Provider");
    expect(openAPIImportsView).toContain("选择服务连接");
  });

  it("delegates OpenAPI source retrieval to the selected Provider", () => {
    expect(openAPIImportsView).toContain("Provider OpenAPI 来源");
    expect(openAPIImportsView).toContain("受管来源拉取并解析 OpenAPI 文档");
    expect(openAPIImportsView).toContain("parseOpenAPIPreview");
    expect(openAPIImportsView).not.toContain("rawContent");
  });

  it("supports managed file upload without exposing arbitrary URL or raw-content controls", () => {
    const template = openAPIImportsView.match(/<template>[\s\S]*<\/template>/)?.[0] || "";
    expect(template).toContain("openapi-file-picker");
    expect(template).toContain("openapi-file-input");
    expect(openAPIImportsView).toContain("parseOpenAPIPreview");
    expect(openAPIImportsView).toContain("integration.createOpenAPIFileImport");
    expect(template).not.toContain("OpenAPI URL");
    expect(template).not.toContain("粘贴原文");
  });

  it("shows workspace and Provider provenance in the import registry and detail modal", () => {
    expect(openAPIImportsView).toContain("workspaceLabel");
    expect(openAPIImportsView).toContain("providerLabel");
    expect(openAPIImportsView).toContain("selectedWorkspace");
    expect(openAPIImportsView).toContain("归属空间");
    expect(openAPIImportsView).toContain("来源 Provider");
    expect(openAPIImportsView).not.toContain("归属 Agent");
  });

  it("renders recursive schema detail trees and paginates the import registry", () => {
    expect(openAPIImportsView).toContain("ToolSchemaTreeView");
    expect(openAPIImportsView).toContain("selectedImportDetail");
    expect(openAPIImportsView).toContain("integration.openAPIImportPageItems");
    expect(openAPIImportsView).toContain("integration.openAPIImportPagination");
    expect(openAPIImportsView).toContain('@page-change="changeOpenAPIPage"');
    expect(openAPIImportsView).not.toContain("openapiCurrentPage");
    expect(openAPIImportsView).not.toContain("openapiPageSize");
    expect(openAPIImportsView).not.toContain("visibleImportsPage");
    expect(openAPIImportsView).not.toContain("const visibleImports = computed");
  });

  it("renders imported body and response contracts with the dual-pane-aligned nested contract view", () => {
    expect(openAPIImportsView).toContain("ToolSchemaTreeView");
    expect(openAPIImportsView).toContain("请求体 Body");
    expect(openAPIImportsView).toContain("响应结果");
    expect(openAPIImportsView).toContain("selectedImportDetail");
  });

  it("uses the shared management list with typed columns, responsive cards, filters, and scoped modal controls", () => {
    const columnsBlock = openAPIImportsView.match(/const openAPIImportColumns[\s\S]*?\n\]\);/)?.[0] || "";
    const scopedStyle = openAPIImportsView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(openAPIImportsView).toContain("openAPIStatusFilter");
    expect(openAPIImportsView).toContain("ManagementList");
    expect(openAPIImportsView).toContain("ManagementListColumn<OpenAPIImport>");
    expect(openAPIImportsView).toContain('storage-key="actweave:openapi-imports:columns"');
    expect(openAPIImportsView).toContain(':sticky-left-keys="[\'file\']"');
    expect(openAPIImportsView).toContain(':sticky-right-keys="[\'actions\']"');
    expect(openAPIImportsView).toContain('@select-row="openImportDetail"');
    expect(openAPIImportsView).toContain('<template #card="{ row: record }">');
    expect(openAPIImportsView).toContain("openapi-import-page");
    expect(openAPIImportsView).toContain("ManagementPageHeader");
    expect(openAPIImportsView).toContain("openapi-page-header");
    expect(openAPIImportsView).toContain("openapi-import-table-card");
    expect(openAPIImportsView).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(openAPIImportsView).toContain("<ManagementSegmentedFilter");
    expect(openAPIImportsView).not.toContain("openapi-filter-tabs");
    expect(openAPIImportsView).toContain("openapi-import-management-list");
    expect(openAPIImportsView).toContain("openapi-file-cell");
    expect(openAPIImportsView).toContain("openapi-count-cell");
    expect(openAPIImportsView).toContain("openapi-status-pill");
    expect(openAPIImportsView).toContain("ManagementRowActions");
    expect(openAPIImportsView).toContain("openAPIImportMenuActions");
    expect(openAPIImportsView).toContain("importModalVisible");
    expect(openAPIImportsView).toContain("openapi-modal-card");
    expect(openAPIImportsView).toContain("openapi-modal-head");
    expect(openAPIImportsView).toContain("openapi-reference-select");
    expect(openAPIImportsView).toContain("openapi-select-menu");
    expect(openAPIImportsView).toContain('class="openapi-import-mode-tabs"');
    expect(openAPIImportsView).toContain("toggleOpenAPIDropdown");
    expect(openAPIImportsView).toContain("openapi-modal-actions");
    expect(openAPIImportsView).toContain("<style scoped>");
    expect(openAPIImportsView).not.toContain("<el-drawer");
    expect(openAPIImportsView).not.toContain("openapi-import-row");
    expect(openAPIImportsView).not.toContain("tool-main-panel");
    expect(openAPIImportsView).not.toContain("segmented-filter");
    expect(openAPIImportsView).not.toContain("search-box");
    expect(openAPIImportsView).not.toContain("select-field");
    expect(columnsBlock).toContain('label: "导入文件"');
    expect(columnsBlock).toContain('label: "服务连接"');
    expect(columnsBlock).toContain('label: "接口数"');
    expect(columnsBlock).toContain('label: "可生成"');
    expect(columnsBlock).toContain('label: "待处理"');
    expect(columnsBlock).toContain('label: "导入时间"');
    expect(columnsBlock).toContain('label: "状态"');
    expect(columnsBlock).toContain('label: "操作"');
    expect(scopedStyle).toContain(".openapi-import-page");
    expect(scopedStyle).toContain(".openapi-import-management-list");
    expect(scopedStyle).toContain(".openapi-import-mobile-card");
    expect(scopedStyle).toContain(".openapi-status-pill");
    expect(scopedStyle).toContain(".openapi-reference-select");
    expect(scopedStyle).not.toContain(".openapi-filter-tabs");
  });

  it("guards submit and draft generation actions against duplicate clicks", () => {
    expect(openAPIImportsView).toContain("const importingOpenAPI = ref(false)");
    expect(openAPIImportsView).toContain("if (importingOpenAPI.value || !canImportOpenAPI.value) return;");
    expect(openAPIImportsView).toContain("importingOpenAPI.value = true;");
    expect(openAPIImportsView).toContain("importingOpenAPI.value = false;");
    expect(openAPIImportsView).toContain(":disabled=\"!canImportOpenAPI || importingOpenAPI\"");
    expect(openAPIImportsView).toContain("generatingDraftsByImportId");
    expect(openAPIImportsView).toContain("if (generatingDraftsByImportId.value[record.id]) return;");
  });

  it("keeps modal focus and keyboard behavior explicit for import, detail, and destructive flows", () => {
    expect(openAPIImportsView).toContain("const importDialogRef = ref<HTMLElement | null>(null)");
    expect(openAPIImportsView).toContain("const detailDialogRef = ref<HTMLElement | null>(null)");
    expect(openAPIImportsView).toContain("const deleteDialogRef = ref<HTMLElement | null>(null)");
    expect(openAPIImportsView).toContain("lastModalTrigger");
    expect(openAPIImportsView).toContain("openImportModal");
    expect(openAPIImportsView).toContain("closeImportModal");
    expect(openAPIImportsView).toContain("handleModalTab");
    expect(openAPIImportsView).toContain("@keydown.esc.stop.prevent");
    expect(openAPIImportsView).toContain("@keydown.tab=\"handleModalTab");
    expect(openAPIImportsView).toContain("tabindex=\"-1\"");
  });

  it("confirms destructive OpenAPI import deletion before calling the backend", () => {
    expect(openAPIImportsView).toContain("const pendingDeleteImport = ref<OpenAPIImport | null>(null)");
    expect(openAPIImportsView).toContain("requestRemoveImport");
    expect(openAPIImportsView).toContain("confirmRemoveImport");
    expect(openAPIImportsView).toContain("删除导入记录");
    expect(openAPIImportsView).toContain("确认删除");
    expect(openAPIImportsView).not.toContain("title=\"删除记录\" @click=\"removeImport(record)\"");
  });

  it("adds accessible labels and live feedback controls to the import registry", () => {
    expect(openAPIImportsView).toContain("aria-label=\"搜索 OpenAPI 导入记录\"");
    expect(openAPIImportsView).toContain(":aria-selected");
    expect(openAPIImportsView).toContain('label: "查看详情"');
    expect(openAPIImportsView).toContain('label: "生成 Tool 草稿"');
    expect(openAPIImportsView).toContain('label: "删除记录"');
    expect(openAPIImportsView).toContain('menu-label="更多操作"');
    expect(openAPIImportsView).toContain("role=\"status\"");
    expect(openAPIImportsView).toContain("aria-live=\"polite\"");
    expect(openAPIImportsView).toContain("aria-label=\"关闭提示\"");
  });

  it("keeps touch targets and modal scrolling within audited UX thresholds", () => {
    const scopedStyle = openAPIImportsView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(scopedStyle).toContain("min-height: 44px;");
    expect(scopedStyle).toContain("overflow: hidden;");
    expect(scopedStyle).toContain("overflow-y: auto;");
    expect(scopedStyle).toContain(".openapi-modal-head > button");
    expect(scopedStyle).toContain("width: 44px;");
    expect(scopedStyle).toContain("height: 44px;");
  });

  it("removes scoped selectors that only served the legacy native import table", () => {
    const scopedStyle = openAPIImportsView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(scopedStyle).not.toMatch(/\.openapi-import-table(?=[\s.:>#,\[{])/);
    expect(scopedStyle).not.toMatch(/\.openapi-table-scroll(?=[\s.:>#,\[{])/);
    expect(scopedStyle).not.toMatch(/\.openapi-action-column(?=[\s.:>#,\[{])/);
    expect(scopedStyle).not.toMatch(/\.openapi-pagination(?=[\s.:>#,\[{])/);
  });
});
