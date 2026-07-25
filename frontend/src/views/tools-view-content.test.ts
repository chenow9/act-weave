import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const toolsView = readFileSync(resolve(currentDir, "ToolsView.vue"), "utf8");
const domainTypes = readFileSync(resolve(currentDir, "..", "types", "domain.ts"), "utf8");
const appStyles = readFileSync(resolve(currentDir, "..", "styles", "app.css"), "utf8");
const dataTable = readFileSync(resolve(currentDir, "..", "components", "DataTable.vue"), "utf8");
const managementRowActions = readFileSync(resolve(currentDir, "..", "components", "ManagementRowActions.vue"), "utf8");
const hybridContractEditor = readFileSync(resolve(currentDir, "..", "components", "ToolContractHybridEditor.vue"), "utf8");
const flatContractEditor = readFileSync(resolve(currentDir, "..", "components", "ToolFlatContractEditor.vue"), "utf8");

describe("tools view", () => {
  const registryTableBlock = toolsView.match(/<ManagementList\s[\s\S]*?<\/ManagementList>/)?.[0] || "";
  const toolTypeBlock = domainTypes.match(/export interface Tool \{[\s\S]*?\n\}/)?.[0] || "";
  const step2Block = toolsView.match(/<template v-else-if="draftStep === 2">[\s\S]*?<template v-else>/)?.[0] || "";

  it("keeps Tool filtering independent from Agent ownership", () => {
    expect(toolsView).not.toContain("useAgentStore");
    expect(toolsView).not.toContain("selectedAgentFilter");
    expect(toolsView).not.toContain("agentFilterOptions");
    expect(toolsView).not.toContain("按 Agent 筛选");
    expect(toolsView).toContain("statusTabs");
    expect(toolsView).toContain("selectedStatusFilter");
    expect(toolsView).toContain("tool-status-segmented");
  });

  it("uses editor modal wiring instead of reopening the default create draft", () => {
    expect(toolsView).toContain("toolEditorVisible");
    expect(toolsView).toContain("toolEditorMode");
    expect(toolsView).toContain("openEditTool");
    expect(toolsView).toContain("buildDraftFromTool");
    expect(toolsView).toContain("integration.updateTool");
    expect(toolsView).toContain('key: "edit"');
    expect(toolsView).toContain('actionKey === "edit"');
    expect(toolsView).toContain("tool-editor-modal-card");
    expect(toolsView).not.toContain("drawerMode");
    expect(toolsView).not.toContain("toolDrawerVisible");
    expect(toolsView).not.toContain("<el-drawer");
  });

  it("maps tool statuses to Chinese labels and keeps runtime statuses action-driven in the editor", () => {
    expect(toolsView).toContain("toolStatusOptions");
    expect(toolsView).toContain("toolStatusLabel");
    expect(toolsView).toContain("toolStatusHelperText");
    expect(toolsView).toContain("草稿");
    expect(toolsView).toContain("待评审");
    expect(toolsView).toContain("已测试");
    expect(toolsView).toContain("已发布");
    expect(toolsView).toContain("已停用");
    expect(toolsView).toContain("当前状态");
    expect(toolsView).toContain("状态由测试、发布与停用动作驱动");
    expect(toolsView).not.toContain("v-model=\"draftTool.status\"");
  });

  it("models Tool as a workspace capability with version and release metadata", () => {
    expect(toolTypeBlock).not.toContain("agentId");
    expect(toolTypeBlock).toContain("providerId: string;");
    expect(toolTypeBlock).toContain("versions: ToolVersion[];");
    expect(toolTypeBlock).toContain("activeReleaseId?: string;");
  });

  it("uses ManagementList with the Tool Runtime columns and scoped visual classes", () => {
    expect(toolsView).toContain("toolSummaryMeta");
    expect(toolsView).toContain("toolEndpointSummary");
    expect(toolsView).toContain("tool-summary-grid");
    expect(toolsView).toContain("tool-runtime-card");
    expect(toolsView).toContain("<ManagementList");
    expect(toolsView).toContain("ManagementListColumn<Tool>");
    expect(toolsView).toContain('storage-key="actweave:tools:columns"');
    expect(toolsView).toContain('{ key: "tool", label: "工具名称"');
    expect(toolsView).toContain('{ key: "type", label: "工具类型"');
    expect(toolsView).toContain('{ key: "protocol", label: "协议类型"');
    expect(toolsView).toContain('{ key: "method", label: "Method"');
    expect(toolsView).toContain('{ key: "path", label: "Path"');
    expect(toolsView).toContain('{ key: "connection", label: "Provider / 服务连接"');
    expect(toolsView).toContain('{ key: "status", label: "状态"');
    expect(toolsView).toContain('{ key: "version", label: "版本"');
    expect(toolsView).toContain('{ key: "updatedAt", label: "更新时间"');
    expect(toolsView).toContain('{ key: "actions", label: "操作"');
    expect(registryTableBlock).not.toContain("使用方 Agent");
    expect(registryTableBlock).not.toContain("生命周期");
    expect(registryTableBlock).not.toContain("测试状态");
    expect(registryTableBlock).not.toContain("运行状态");
    expect(registryTableBlock).not.toContain("最近测试");
    expect(registryTableBlock).not.toContain("调用量 / 失败率");
    expect(registryTableBlock).toContain(':sticky-left-keys="[\'tool\']"');
    expect(registryTableBlock).toContain(':sticky-right-keys="[\'actions\']"');
    expect(registryTableBlock).toContain("checkable");
    expect(registryTableBlock).toContain("selectedToolRowKeys");
    expect(toolsView).toContain("tool-method-badge");
    expect(toolsView).toContain("tool-provider-connection");
    expect(toolsView).toContain("tool-status-pill");
    expect(toolsView).toContain("ManagementRowActions");
    expect(toolsView).not.toContain("tool-more-menu");
    expect(toolsView).toContain("tool-entity-cell");
    expect(toolsView).toContain("tool-endpoint-summary");
    expect(toolsView).toContain(":title=\"tool.description\"");
    expect(toolsView).toContain(":title=\"tool.name\"");
    expect(toolsView).toContain(":title=\"toolEndpointSummary(tool)\"");
    expect(toolsView).toContain(':selectable="false"');
    expect(toolsView).toContain('{ key: "actions", label: "操作", width: 68');
    expect(toolsView).toContain("<style scoped>");
    expect(toolsView).not.toContain("tool-action-table-head");
    expect(toolsView).not.toContain("tool-action-list");
    expect(toolsView).not.toContain("tool-action-row");
  });

  it("merges lifecycle, test, and run status into one readable status column", () => {
    expect(registryTableBlock).toContain("服务连接");
    expect(registryTableBlock).toContain("状态");
    expect(toolsView).toContain('{ key: "updatedAt", label: "更新时间"');
    expect(toolsView).toContain("getToolLifecycleStatus");
    expect(toolsView).toContain("getToolTestStatus");
    expect(toolsView).toContain("getToolRunStatus");
    expect(toolsView).toContain("getToolUnifiedStatus");
    expect(toolsView).toContain("toolHasConnectionAttention");
    expect(toolsView).toContain("toolUnifiedStatus");
    expect(toolsView).toContain("connectionIssueTools");
    // Connection attention is surfaced via KPI + status pills, not a page banner.
    expect(toolsView).not.toContain("tool-connection-alert");
    expect(toolsView).toContain('label: "连接异常", value: "attention"');
    expect(toolsView).not.toContain("observabilityLabel");
    expect(toolsView).not.toContain("tool-observability-cell");
  });

  it("keeps the status filter taxonomy in the table toolbar with prototype summary metrics", () => {
    expect(toolsView).toContain("需处理");
    expect(toolsView).toContain('label: "全部状态", value: "all"');
    expect(toolsView).toContain('label: "连接异常", value: "attention"');
    expect(toolsView).toContain("toolHasConnectionAttention");
    expect(toolsView).toContain("publishedWithConnectionIssueCount");
    expect(toolsView).toContain('label: "全部类型", value: "all"');
    expect(toolsView).toContain('ariaLabel="工具类型筛选"');
    expect(toolsView).toContain("ManagementPageHeader");
    expect(toolsView).toContain("ManagementSummaryStrip");
    expect(toolsView).not.toContain("草稿 / 已停用");
    // KPI 需处理 must count connection attention, not lifecycle Review/Disabled only.
    expect(toolsView).not.toMatch(
      /label:\s*"需处理"[\s\S]{0,200}tool\.status === "Review" \|\| tool\.status === "Disabled"/,
    );
  });

  it("uses shared icon-and-label primary actions and keeps risky actions in a confirmed menu", () => {
    expect(registryTableBlock).toContain("ManagementRowActions");
    expect(toolsView).toContain('key: "detail"');
    expect(toolsView).toContain('key: "test"');
    expect(toolsView).toContain('key: "delete"');
    expect(managementRowActions).toContain("width: 44px;");
    expect(managementRowActions).toContain("gap: 4px;");
    expect(registryTableBlock).not.toContain("tool-row-text-button");
    expect(toolsView).toContain("pendingRiskAction");
    expect(toolsView).toContain("openRiskConfirmation");
    expect(toolsView).toContain("tool-risk-confirmation-modal");
    expect(toolsView).toContain("确认停用");
    expect(toolsView).toContain("确认删除");
  });

  it("opens a dedicated test dialog from list and detail actions", () => {
    expect(toolsView).toContain("ToolTestDialog");
    expect(toolsView).toContain("testDialogVisible");
    expect(toolsView).toContain("testDialogTool");
    expect(toolsView).toContain("openToolTestDialog");
    expect(toolsView).toContain('actionKey === "test"');
    expect(toolsView).toContain("@click=\"openToolTestDialog(detailTool)\"");
  });

  it("uses centered detail and editor modals from the new UI refactor", () => {
    expect(toolsView).toContain("toolDetailVisible");
    expect(toolsView).toContain("tool-detail-modal-card");
    expect(toolsView).toContain("toolEditorVisible");
    expect(toolsView).toContain("tool-editor-modal-card");
    expect(toolsView).toContain("tool-editor-progress");
    expect(toolsView).toContain("tool-editor-actions");
    expect(toolsView).toContain("toolEditorTitle");
    expect(toolsView).not.toContain("detailDrawerVisible");
    expect(toolsView).not.toContain("drawerSteps");
    expect(toolsView).not.toContain("tool-drawer-actions");
  });

  it("uses a full-screen registration workspace with the Hybrid+ three-step flow", () => {
    expect(toolsView).toContain("tool-registration-workspace");
    expect(toolsView).toContain("tool-hybrid-title-icon");
    expect(toolsView).toContain("基础与接口");
    expect(toolsView).toContain("契约配置");
    expect(toolsView).toContain("确认保存");
    expect(toolsView).toContain("请求、响应与错误");
    expect(toolsView).toContain("保存草稿");
    expect(toolsView).toContain("保存后状态：草稿");
    expect(toolsView).toContain("hasUnsavedToolChanges");
    expect(toolsView).toContain("saveStateLabel");
  });

  it("uses the existing project modal visual system around the three-step flow", () => {
    expect(toolsView).toContain("Project-aligned Tool registration workspace");
    expect(toolsView).toContain("background: rgb(15 23 42 / 0.6)");
    expect(toolsView).toContain("backdrop-filter: blur(12px)");
    expect(toolsView).toContain("background: var(--aw-bg)");
    expect(toolsView).toContain("background: #d1f0d0");
    expect(toolsView).toContain("background: #020617");
    expect(hybridContractEditor).toContain("var(--aw-border)");
    expect(flatContractEditor).toContain("var(--aw-border)");
  });

  it("uses explicit publish and availability actions instead of hand-editing terminal statuses", () => {
    expect(toolsView).toContain("publishTool");
    expect(toolsView).toContain("enableTool");
    expect(toolsView).toContain("disableTool");
    expect(toolsView).toContain("canPublishTool");
    expect(toolsView).toContain("toggleToolAvailability");
    expect(toolsView).toContain("toolAvailabilityActionLabel");
    expect(toolsView).toContain('actionKey === "publish"');
    expect(toolsView).toContain('openRiskConfirmation(tool.status === "Disabled" ? "enable" : "disable", tool)');
    expect(toolsView).toContain("@click=\"publishTool(detailTool)\"");
    expect(toolsView).toContain("@click=\"toggleToolAvailability(detailTool)\"");
  });

  it("keeps flat request parameters and nested body contracts in one step", () => {
    expect(step2Block).toContain("ToolFlatContractEditor");
    expect(step2Block).toContain("activeRequestFlatContract");
    expect(step2Block).toContain("contractEditorTabs");
    expect(toolsView).toContain('{ value: "Path"');
    expect(toolsView).toContain('{ value: "Query"');
    expect(toolsView).toContain('{ value: "Header"');
    expect(toolsView).toContain('{ value: "Body"');
    expect(toolsView).toContain('{ value: "Response"');
    expect(toolsView).toContain('{ value: "Errors"');
    expect(step2Block).toContain("Body");
    expect(step2Block).toContain("requestBodyContract");
    expect(step2Block).toContain("responseBodyContract");
  });

  it("starts a new Tool with empty request, response, and error contracts", () => {
    const defaultDraftBlock = toolsView.match(/function defaultToolDraft\(\): ToolDraft \{[\s\S]*?\n\}/)?.[0] || "";

    expect(defaultDraftBlock).toContain("requestContract: []");
    expect(defaultDraftBlock).toContain("responseContract: []");
    expect(defaultDraftBlock).toContain("errorMappings: []");
    expect(toolsView).toContain("draftCompletionPercent");
    expect(toolsView).toContain("completedBaseRequiredCount");
    expect(toolsView).toContain("建议检查 {{ draftSuggestionCount }}");
  });

  it("uses the Hybrid+ structured editor with a derived read-only JSON preview", () => {
    expect(toolsView).toContain("ToolContractHybridEditor");
    expect(toolsView).toContain("requestBodyContract");
    expect(toolsView).toContain("responseBodyContract");
    expect(toolsView).toContain("请求体 Body");
    expect(toolsView).toContain("成功响应");
    expect(hybridContractEditor).toContain("结构化");
    expect(hybridContractEditor).toContain(">JSON<");
    expect(hybridContractEditor).toContain("serializeContractNodesToJson");
    expect(hybridContractEditor).toContain("只读");
  });

  it("keeps the structured contract editor as a field tree with one inspector", () => {
    expect(hybridContractEditor).toContain("tool-hybrid-contract-tree");
    expect(hybridContractEditor).toContain("tool-hybrid-contract-inspector");
    expect(hybridContractEditor).toContain("addField");
    expect(hybridContractEditor).toContain("addChildNode");
    expect(hybridContractEditor).toContain("复制");
    expect(hybridContractEditor).toContain("deleteSelectedNode");
    expect(hybridContractEditor).toContain("selectedNode");
  });

  it("uses a compact contract layout on short desktop viewports", () => {
    expect(toolsView).toContain("@media (max-height: 760px)");
    expect(toolsView).toContain(".tool-hybrid-topbar");
    expect(toolsView).toContain(".tool-contract-body-wrap");
  });

  it("still splits transport params from body contracts while reusing the schema workspace implementation", () => {
    expect(toolsView).toContain("buildBodyContractFromRequestParams");
    expect(toolsView).toContain("buildRequestParamsFromContracts");
    expect(toolsView).toContain("buildResponseContractFromFields");
    expect(toolsView).toContain("requestTransportContract");
    expect(toolsView).toContain("requestBodyContract");
    expect(toolsView).toContain("responseBodyContract");
    expect(toolsView).not.toContain("ToolSchemaTreeEditor v-model=\"draftTool.requestContract\"");
  });

  it("uses accessible filter semantics and the shared viewport-aware action menu", () => {
    expect(toolsView).toContain("ManagementSegmentedFilter");
    expect(toolsView).toContain('ariaLabel="工具状态筛选"');
    expect(toolsView).not.toContain("toolMenuDirection");
    expect(toolsView).not.toContain("openMenuToolId");
    expect(managementRowActions).toContain("position: \"fixed\"");
    expect(managementRowActions).toContain("overflow-y: auto;");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
  });

  it("uses accessible tab semantics and keyboard navigation in the tool detail modal", () => {
    const detailTabsBlock = toolsView.match(/<div class="tool-detail-tabs"[\s\S]*?id="tool-detail-panel-base"/)?.[0] || "";

    expect(detailTabsBlock).toContain('role="tablist"');
    expect(detailTabsBlock).toContain('aria-label="工具详情分区"');
    expect(detailTabsBlock).toContain('role="tab"');
    expect(detailTabsBlock).toContain(":aria-selected");
    expect(detailTabsBlock).toContain(":aria-controls");
    expect(detailTabsBlock).toContain(":tabindex");
    expect(detailTabsBlock).toContain("@keydown=\"handleDetailTabKeydown($event, tab.id)\"");
    expect(toolsView).toContain("role=\"tabpanel\"");
    expect(toolsView).toContain('id="tool-detail-panel-base"');
    expect(toolsView).toContain('aria-labelledby="tool-detail-tab-base"');
    expect(toolsView).toContain('id="tool-detail-panel-connection"');
    expect(toolsView).toContain('aria-labelledby="tool-detail-tab-connection"');
    expect(toolsView).toContain('id="tool-detail-panel-test"');
    expect(toolsView).toContain('aria-labelledby="tool-detail-tab-test"');
    expect(toolsView).toContain("selectDetailTab");
    expect(toolsView).toContain("handleDetailTabKeydown");
  });

  it("preserves the selected detail tab when opening another tool for consecutive review", () => {
    const openToolDetailBlock = toolsView.match(/function openToolDetail\(tool: Tool\) \{[\s\S]*?\n\}/)?.[0] || "";

    expect(openToolDetailBlock).toContain("toolDetailVisible.value = true;");
    expect(openToolDetailBlock).not.toContain("detailTab.value = \"base\"");
  });

  it("shows connection health and a maintenance action in the detail connection tab", () => {
    expect(toolsView).toContain("serviceConnectionStatusLabel");
    expect(toolsView).toContain("const detailConnection = computed(() => (detailTool.value ? connectionForTool(detailTool.value) : undefined));");
    expect(toolsView).not.toContain('const detailConnection = computed(() => connectionById(detailTool.value?.connectionId || "") || integration.serviceConnections[0]);');
    expect(toolsView).toContain("连接状态");
    expect(toolsView).toContain("服务连接未找到");
    expect(toolsView).toContain("去服务连接维护");
    expect(toolsView).toContain("@click=\"router.push('/connections')\"");
  });

  it("explains failed test readiness with actionable details before publishing", () => {
    expect(toolsView).toContain("toolLastTestDetail");
    expect(toolsView).toContain("tool-publish-readiness");
    expect(toolsView).toContain(":aria-describedby=\"'tool-publish-readiness'\"");
    expect(toolsView).toContain("连通性未通过");
    expect(toolsView).toContain("响应 Schema 未通过");
    expect(toolsView).toContain("错误映射未通过");
    expect(toolsView).toContain("运行策略未通过");
  });

  it("keeps tool detail tabs and test actions at accessible touch target sizes", () => {
    const tabButtonBlock = appStyles.match(/\.tool-detail-tabs button\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const actionButtonBlock = appStyles.match(/\.tool-test-action-group button\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const mobileBlock = appStyles.match(/@media \(max-width: 480px\)\s*\{[\s\S]*?\.tool-test-action-group\s*\{[\s\S]*?\n\s*\}[\s\S]*?\n\}/)?.[0] || "";

    expect(tabButtonBlock).toContain("min-height: 44px;");
    expect(actionButtonBlock).toContain("min-height: 44px;");
    expect(mobileBlock).toContain(".tool-detail-tabs");
    expect(mobileBlock).toContain("overflow-x: auto;");
    expect(mobileBlock).toContain(".tool-test-action-group");
    expect(mobileBlock).toContain("grid-template-columns: 1fr;");
  });

  it("gives the tool detail modal enough reading rhythm for dense metadata", () => {
    const detailModalBlock = appStyles.match(/\.tool-detail-modal-card\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const detailBodyBlock = appStyles.match(/\.tool-detail-modal-body\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const configGridBlock = appStyles.match(/\.tool-config-grid\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const configItemBlock = appStyles.match(/\.config-summary-item\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const configIconBlock = appStyles.match(/\.config-summary-item i\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(toolsView).toContain("tool-detail-modal-body");
    expect(detailModalBlock).toContain("width: min(920px, calc(100vw - 56px));");
    expect(detailBodyBlock).toContain("padding: 18px 20px 20px;");
    expect(configGridBlock).toContain("gap: 16px;");
    expect(configItemBlock).toContain("grid-template-columns: 40px minmax(0, 1fr);");
    expect(configItemBlock).toContain("column-gap: 12px;");
    expect(configItemBlock).toContain("padding: 14px;");
    expect(configIconBlock).toContain("width: 36px;");
    expect(configIconBlock).toContain("height: 36px;");
  });

  it("renders the read-only tool status as a single-line field aligned with form controls", () => {
    const statusBlock = appStyles.match(/\.tool-status-readonly\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const helperBlock = appStyles.match(/\.tool-status-readonly small\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(statusBlock).toContain("min-height: 44px;");
    expect(statusBlock).toContain("flex-direction: row;");
    expect(statusBlock).toContain("align-items: center;");
    expect(statusBlock).toContain("padding: 0 12px;");
    expect(statusBlock).not.toContain("flex-direction: column;");
    expect(helperBlock).toContain("white-space: nowrap;");
    expect(helperBlock).toContain("text-overflow: ellipsis;");
  });

  it("keeps contract workspace launch actions at a reliable click target size", () => {
    const launchPrimaryBlock = appStyles.match(/\.tool-contract-launch-primary\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(launchPrimaryBlock).toContain("min-height: 44px;");
    expect(launchPrimaryBlock).toContain("padding: 0 18px;");
  });

  it("wires custom modals to keyboard focus management", () => {
    expect(toolsView).toContain("useModalFocus");
    expect(toolsView).toContain("toolDetailModalRef");
    expect(toolsView).toContain("toolEditorModalRef");
    expect(toolsView).toContain("data-modal-initial-focus");
  });
});
