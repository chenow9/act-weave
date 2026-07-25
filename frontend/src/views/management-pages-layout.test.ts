import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));

function readView(fileName: string) {
  return readFileSync(resolve(currentDir, fileName), "utf8");
}

function readStyle(fileName: string) {
  return readFileSync(resolve(currentDir, "..", "styles", fileName), "utf8");
}

function readComponent(fileName: string) {
  return readFileSync(resolve(currentDir, "..", "components", fileName), "utf8");
}

describe("management pages prototype alignment", () => {
  it("uses the Hybrid+ three-step Tool creation flow", () => {
    const view = readView("ToolsView.vue");
    const stepDefinition = view.match(/const toolEditorSteps = \[[\s\S]*?\n\];/)?.[0] || "";
    const editorBlock = view.match(/<div v-if="toolEditorVisible"[\s\S]*?<div v-if="riskConfirmationVisible"/)?.[0] || "";

    expect(stepDefinition).toContain('["基础与接口", "归属、连接与 Endpoint"]');
    expect(stepDefinition).toContain('["契约配置", "请求、响应与错误"]');
    expect(stepDefinition).toContain('["确认保存", "检查后保存草稿"]');
    expect(stepDefinition.match(/^\s*\[/gm)).toHaveLength(3);
    expect(stepDefinition).not.toContain('["测试调用"');
    expect(stepDefinition).not.toContain('["发布设置"');

    expect(editorBlock).toContain('class="tool-contract-side-tabs"');
    expect(editorBlock).toContain('class="tool-contract-body-wrap"');
    expect(editorBlock).toContain('draftTool.errorMappings');
    expect(editorBlock).toContain('tool-review-summary-grid');
    expect(editorBlock).toContain(':aria-current="draftStep === index + 1 ? \'step\' : undefined"');
    expect(editorBlock).toContain('保存草稿');
    expect(editorBlock).not.toContain('>发布</button>');
  });

  it("uses the prototype Tool Runtime registry layout", () => {
    const view = readView("ToolsView.vue");
    const registryTableBlock = view.match(/<ManagementList\s[\s\S]*?<\/ManagementList>/)?.[0] || "";

    expect(view).toContain("tool-grid");
    expect(view).toContain("tool-section-bar");
    expect(view).toContain("tool-summary-grid");
    expect(view).toContain("tool-runtime-card");
    expect(view).toContain("<ManagementList");
    expect(view).toContain("ManagementListColumn<Tool>");
    expect(view).toContain('storage-key="actweave:tools:columns"');
    expect(view).toContain("ManagementSegmentedFilter");
    expect(view).toContain('import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue"');
    expect(view).toContain("<ManagementRowActions");
    expect(view).toContain('menu-label="更多工具操作"');
    expect(view).toContain("checkable");
    expect(view).toContain(':selectable="false"');
    expect(view).toContain('{ key: "tool", label: "工具名称"');
    expect(view).toContain('{ key: "type", label: "工具类型"');
    expect(view).toContain('{ key: "protocol", label: "协议类型"');
    expect(view).toContain('{ key: "method", label: "Method"');
    expect(view).toContain('{ key: "path", label: "Path"');
    expect(view).toContain('{ key: "connection", label: "Provider / 服务连接"');
    expect(view).toContain('{ key: "status", label: "状态"');
    expect(view).toContain('{ key: "updatedAt", label: "更新时间"');
    expect(view).toContain('{ key: "actions", label: "操作"');
    expect(registryTableBlock).toContain(':sticky-left-keys="[\'tool\']"');
    expect(registryTableBlock).toContain(':sticky-right-keys="[\'actions\']"');
    expect(registryTableBlock).toContain("selectedToolRowKeys");
    expect(registryTableBlock).not.toContain("<span>使用方 Agent</span>");
    expect(registryTableBlock).not.toContain("<span>生命周期</span>");
    expect(registryTableBlock).not.toContain("<span>测试状态</span>");
    expect(registryTableBlock).not.toContain("<span>运行状态</span>");
    expect(registryTableBlock).not.toContain("<span>最近测试</span>");
    expect(registryTableBlock).not.toContain("<span>调用量 / 失败率</span>");
    expect(view).toContain("tool-summary-meta");
    expect(view).toContain("tool-endpoint-summary");
    expect(view).toContain("tool-connection-alert");
    expect(registryTableBlock).not.toContain("业务空间");
    expect(registryTableBlock).toContain("tool-protocol-cell");
    expect(registryTableBlock).not.toContain("参数摘要");
    expect(view).toContain("tool-editor-progress");
    expect(view).toContain("fa-solid fa-vial");
    expect(view).toContain("<style scoped>");
    expect(view).not.toContain("<el-drawer");
    expect(view).not.toContain("<el-steps");
    expect(view).not.toContain("<el-table");
    expect(view).not.toContain("tool-action-row");
  });

  it("keeps registry create and edit surfaces aligned to the prototype structure", () => {
    const toolsView = readView("ToolsView.vue");
    const serviceConnectionsView = readView("ServiceConnectionsView.vue");
    const openAPIImportsView = readView("OpenAPIImportsView.vue");
    const modelAPIConfigsView = readView("ModelAPIConfigsView.vue");
    const styles = readStyle("app.css");

    expect(toolsView).toContain("tool-editor-progress");
    expect(toolsView).toContain("connection-reference-head");
    expect(toolsView).toContain("tool-endpoint-preview");
    expect(toolsView).toContain("管理服务连接");
    expect(toolsView).toContain("选择业务空间");
    expect(toolsView).toContain("退避策略");
    expect(toolsView).toContain("限流策略");
    expect(toolsView).toContain("import ToolFlatContractEditor");
    expect(toolsView).toContain("import ToolContractHybridEditor");
    expect(toolsView).toContain("import ToolSchemaTreeView");
    expect(toolsView).toContain("<ToolFlatContractEditor");
    expect(toolsView).toContain("<ToolContractHybridEditor");
    expect(toolsView).toContain("<ToolSchemaTreeView");
    expect(toolsView).toContain("requestParamToSchemaNode");
    expect(toolsView).toContain("responseFieldToSchemaNode");
    expect(toolsView).not.toContain("yesNoOptions");
    expect(toolsView).not.toContain("requestLocationOptions");
    expect(toolsView).not.toContain("fieldTypeOptions");
    expect(toolsView).toContain("tool-editor-modal-card");
    expect(toolsView).toContain("tool-detail-modal-card");
    expect(toolsView).toContain("tool-editor-progress");
    expect(toolsView).toContain("tool-connection-summary-card");
    expect(toolsView).toContain("tool-connection-summary-head");
    expect(toolsView).toContain("tool-connection-summary-grid");
    expect(toolsView).toContain("tool-connection-summary-meta");
    expect(toolsView).toContain("tool-connection-summary-value mono");
    expect(toolsView).toContain("tool-editor-actions");
    expect(toolsView).toContain("connectionDomainLabel(draftConnection)");
    expect(toolsView).toContain("authModeLabel(draftConnection)");
    expect(toolsView).toContain("Content-Type");
    expect(toolsView).toContain("<AppSelect v-model=\"draftTool.workspaceId\"");
    expect(toolsView).not.toContain("按 Agent 筛选");
    expect(toolsView).toContain("Capability Binding");
    expect(toolsView).toContain("openEditTool");
    expect(toolsView).toContain("toolStatusLabel");
    expect(toolsView).toContain("tool-status-readonly");
    expect(toolsView).toContain("当前状态");
    expect(toolsView).toContain("publishTool");
    expect(toolsView).toContain("toggleToolAvailability");
    expect(toolsView).not.toContain("v-model=\"draftTool.status\"");

    expect(serviceConnectionsView).toContain("connection-reference-table-card");
    expect(serviceConnectionsView).toContain("<ManagementList");
    expect(serviceConnectionsView).toContain("ManagementListColumn<ServiceConnection>");
    expect(serviceConnectionsView).toContain("connection-detail-hero");
    expect(serviceConnectionsView).toContain("connection-detail-grid");
    expect(serviceConnectionsView).toContain("connection-form-modal");
    expect(serviceConnectionsView).toContain("connection-form-single-column");
    expect(serviceConnectionsView).toContain("connection-disclosure-trigger");
    expect(serviceConnectionsView).not.toContain("connection-step-card");
    expect(serviceConnectionsView).toContain("connection-address-preview");
    expect(serviceConnectionsView).toContain("connection-verification-plan");
    expect(serviceConnectionsView).toContain("connectionCurrentView");
    expect(serviceConnectionsView).toContain("connection-form-workspace");
    expect(serviceConnectionsView).toContain("connection-reference-select");
    expect(serviceConnectionsView).toContain("openCreateConnection");
    expect(serviceConnectionsView).toContain("integration.createServiceConnection");
    expect(serviceConnectionsView).toContain("AppSelect");
    expect(serviceConnectionsView).not.toContain("<select");

    expect(openAPIImportsView).toContain("openapi-import-table-card");
    expect(openAPIImportsView).toContain("<ManagementList");
    expect(openAPIImportsView).toContain("ManagementListColumn<OpenAPIImport>");
    expect(openAPIImportsView).toContain("import-drawer-preview");
    expect(openAPIImportsView).toContain("drawer-footer-actions");
    expect(openAPIImportsView).toContain("importModalVisible");
    expect(openAPIImportsView).toContain("openapi-modal-card");
    expect(openAPIImportsView).toContain("openapi-reference-select");
    expect(openAPIImportsView).toContain("openapi-modal-actions");
    expect(openAPIImportsView).toContain("当前业务空间");
    expect(openAPIImportsView).toContain("选择 Provider");
    expect(openAPIImportsView).toContain("归属空间");
    expect(openAPIImportsView).toContain("来源 Provider");

    expect(modelAPIConfigsView).toContain("modelModalVisible");
    expect(modelAPIConfigsView).toContain("model-modal-card");
    expect(modelAPIConfigsView).toContain("model-modal-head");
    expect(modelAPIConfigsView).toContain("model-modal-actions");

    expect(styles).toContain(".editable-schema-card");
    expect(styles).toContain(".connection-reference-head");
    expect(styles).toContain(".drawer-schema-preview");
    expect(styles).toContain(".tool-editor-modal-card");
    expect(styles).toContain(".tool-detail-modal-card");
    expect(styles).toContain(".tool-editor-progress");
    expect(styles).toContain(".tool-connection-summary-grid");
    expect(styles).toContain(".tool-connection-summary-meta");
    expect(styles).toContain(".tool-connection-summary-value.mono");
    expect(styles).toContain(".tool-editor-actions");
    expect(styles).toContain(".model-modal-actions");
    expect(styles).toContain(".openapi-modal-actions");
    expect(styles).toContain(".tool-status-readonly");
    expect(styles).toContain(".tool-test-action-group");
  });

  it("keeps tool connection summary values on a single line", () => {
    const styles = readStyle("app.css");
    const summaryValueBlock = styles.match(/\.tool-connection-summary-value\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const summaryMonoBlock = styles.match(/\.tool-connection-summary-value\.mono\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const legacySummaryBlock = styles.match(/\.connection-reference-card \.config-summary-item strong\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(summaryValueBlock).toContain("white-space: nowrap;");
    expect(summaryValueBlock).toContain("overflow: hidden;");
    expect(summaryValueBlock).toContain("text-overflow: ellipsis;");
    expect(summaryMonoBlock).not.toContain("overflow-wrap: anywhere;");
    expect(legacySummaryBlock).toContain("white-space: nowrap;");
    expect(legacySummaryBlock).toContain("overflow: hidden;");
    expect(legacySummaryBlock).toContain("text-overflow: ellipsis;");
  });

  it("wires destructive registry actions to backend store deletion instead of local-only filtering", () => {
    const toolsView = readView("ToolsView.vue");
    const serviceConnectionsView = readView("ServiceConnectionsView.vue");
    const openAPIImportsView = readView("OpenAPIImportsView.vue");
    const modelAPIConfigsView = readView("ModelAPIConfigsView.vue");

    expect(toolsView).toContain('label: "删除工具"');
    expect(toolsView).toContain("integration.deleteTool");

    expect(serviceConnectionsView).toContain("integration.deleteServiceConnection");
    expect(serviceConnectionsView).not.toContain("removedConnectionIds");
    expect(serviceConnectionsView).not.toContain("原型列表");

    expect(openAPIImportsView).toContain("integration.deleteOpenAPIImport");
    expect(openAPIImportsView).not.toContain("removedImportIds");

    expect(modelAPIConfigsView).toContain("modelConfigs.deleteModelConfig");
    expect(modelAPIConfigsView).not.toContain("removedModelIds");
    expect(modelAPIConfigsView).not.toContain("当前原型列表");
  });

  it("separates empty registries from filtered no-match states", () => {
    const agentsView = readView("AgentsView.vue");
    const toolsView = readView("ToolsView.vue");
    const serviceConnectionsView = readView("ServiceConnectionsView.vue");
    const openAPIImportsView = readView("OpenAPIImportsView.vue");
    const modelAPIConfigsView = readView("ModelAPIConfigsView.vue");
    const styles = readStyle("app.css");

    expect(agentsView).toContain("暂无 Agent");
    expect(agentsView).toContain("没有匹配的 Agent");
    expect(agentsView).toContain('v-if="hasAgentRecords" class="source-note"');
    expect(agentsView).toContain("management-registry-empty-state");
    expect(agentsView).not.toContain("agent-empty-state");

    expect(toolsView).toContain("hasToolRecords");
    expect(toolsView).toContain("暂无工具");
    expect(toolsView).toContain("可以注册 Tool，或者从 OpenAPI 导入生成草稿。");
    expect(toolsView).toContain("没有匹配的工具");
    expect(toolsView).toContain('v-if="hasToolRecords" class="tool-section-bar"');
    expect(toolsView).toContain("management-registry-empty-state");
    expect(toolsView).not.toContain("tool-empty-state");

    expect(serviceConnectionsView).toContain("hasConnectionRecords");
    expect(serviceConnectionsView).toContain("serviceConnectionRegistryTotal");
    expect(serviceConnectionsView).toContain("暂无服务连接");
    expect(serviceConnectionsView).toContain("没有匹配连接");

    expect(openAPIImportsView).toContain("hasImportRecords");
    expect(openAPIImportsView).toContain("openAPIImportRegistryTotal");
    expect(openAPIImportsView).toContain("暂无导入记录");
    expect(openAPIImportsView).toContain("没有匹配导入记录");

    expect(modelAPIConfigsView).toContain("hasActiveModelFilters");
    expect(modelAPIConfigsView).toContain("ManagementList");
    expect(modelAPIConfigsView).toContain("暂无模型配置");
    expect(modelAPIConfigsView).toContain("没有匹配的模型配置");
    expect(modelAPIConfigsView).toContain("management-registry-empty-state");

    expect(styles).toContain(".registry-empty-state");
    expect(styles).toContain(".registry-empty-state.management-registry-empty-state");
    expect(styles).toContain("border: 0;");
    expect(styles).toContain(".registry-empty-actions");
  });

  it("keeps create surfaces free of manual backend ids", () => {
    const toolsView = readView("ToolsView.vue");
    const serviceConnectionsView = readView("ServiceConnectionsView.vue");
    const workflowView = readView("WorkflowView.vue");
    const workspacesView = readView("WorkspacesView.vue");
    const agentsView = readView("AgentsView.vue");

    expect(toolsView).toContain("draftTool");
    expect(toolsView).toContain("saveDraftTool");
    expect(toolsView).toContain("integration.createTool");
    expect(toolsView).toContain("id: \"\"");
    expect(toolsView).not.toContain("Tool ID");
    expect(toolsView).not.toContain("v-model=\"draftTool.id\"");
    expect(toolsView).not.toContain("toolIdConflict");

    expect(serviceConnectionsView).toContain("id: \"\"");
    expect(serviceConnectionsView).not.toContain("服务连接 ID");
    expect(serviceConnectionsView).not.toContain("v-model=\"draftConnection.id\"");

    expect(workflowView).toContain("id: \"\"");
    expect(workflowView).not.toContain("Workflow ID");
    expect(workflowView).not.toContain("v-model=\"workflowDraft.id\"");
    expect(workflowView).not.toContain("uniqueWorkflowId");

    expect(workspacesView).not.toContain("Workspace ID");
    expect(agentsView).not.toContain("Agent ID");
  });

  it("uses clearer workflow editor action labels and a neutral workbench background", () => {
    const workflowView = readView("WorkflowView.vue");
    const styles = readStyle("app.css");
    const workbenchBlock = styles.match(/\.workflow-workbench\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const gridBlock = styles.match(/\.workflow-graph-canvas-grid,\s*\.workflow-graph-canvas-grid \.vue-flow__pane\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(workflowView).toContain("流程画布编辑");
    expect(workflowView).toContain("基础信息");
    expect(workflowView).toContain("保存画布");
    expect(workflowView).toContain("检查问题");
    expect(workflowView).toContain("模拟运行");
    expect(workflowView).toContain("发布上线");
    expect(workflowView).toContain("退出编辑");
    expect(workbenchBlock).toContain("background: #fff;");
    expect(gridBlock).toContain("#fff");
  });

  it("uses the shared management list for the service connection registry", () => {
    const view = readView("ServiceConnectionsView.vue");
    const scopedStyles = view.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";
    const columnsBlock = view.match(/const connectionColumns[\s\S]*?\n\]\);/)?.[0] || "";

    expect(view).toContain("service-connections-page");
    expect(view).toContain("connection-page-header");
    expect(view).toContain("primary-button");
    expect(view).toContain("ghost-button");
    expect(view).toContain("connection-primary-button");
    expect(view).toContain("connection-reference-banner");
    expect(view).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(view).toContain("<ManagementSegmentedFilter");
    expect(view).not.toContain("connection-abnormal-toggle");
    expect(view).not.toContain("showAbnormalOnly");
    expect(view).toContain("connection-reference-table-card");
    expect(view).toContain("<ManagementList");
    expect(view).toContain("ManagementListColumn<ServiceConnection>");
    expect(view).toContain('storage-key="actweave:service-connections:columns"');
    expect(view).toContain('<template #card="{ row: connection }">');
    expect(columnsBlock).toContain('label: "连接名称"');
    expect(columnsBlock).toContain('label: "地址 & 验证接口"');
    expect(columnsBlock).toContain('label: "认证方式"');
    expect(columnsBlock).toContain('label: "状态"');
    expect(columnsBlock).toContain('label: "操作"');
    expect(view).toContain("connection-name-cell");
    expect(view).toContain("connection-address-cell");
    expect(view).toContain("router.push('/providers')");
    expect(view).toContain("connection-status-pill");
    expect(view).toContain("connection-status-dot");
    expect(view).toContain("ManagementRowActions");
    expect(view).toContain("connectionMenuActions");
    expect(view).toContain("connection-empty-state");
    expect(view).toContain("connection-detail-page");
    expect(view).toContain("connection-detail-topbar");
    expect(view).toContain("connection-detail-back");
    expect(view).toContain("返回连接列表");
    expect(view).toContain("connection-detail-hero");
    expect(view).toContain("connection-verdict-banner");
    expect(view).toContain("connection-detail-grid");
    expect(view).toContain("connection-detail-card");
    expect(view).toContain("connection-detail-card-head");
    expect(view).toContain("connection-detail-facts");
    expect(view).toContain("credentialSecretId");
    expect(view).toContain("connection-form-modal");
    expect(view).toContain("connection-form-single-column");
    expect(view).toContain("connection-disclosure-trigger");
    expect(view).toContain("connection-form-actions");
    expect(view).not.toContain("connection-form-summary");
    expect(view).not.toContain("connection-form-intro");
    expect(view).not.toContain("connection-step-card");
    expect(view).not.toContain("connection-validation-panel");
    expect(view).toContain("connection-address-preview");
    expect(view).toContain("connection-verification-plan");
    expect(view).toContain("connection-verification-item");
    expect(view).toContain("connection-reference-select");
    expect(view).toContain("connection-select-menu");
    expect(view).toContain("saveAndVerifyConnection");
    expect(view).toContain("normalizeServiceAddress");
    expect(view).toContain("endpointUrlParts");
    expect(view).toContain("defaultPortForScheme");
    expect(view).toContain("verificationMethodOptions");
    expect(view).toContain("connectionVerificationTarget");
    expect(view).toContain("verificationPathLabel");
    expect(view).toContain("openConnectionPreview");
    expect(view).toContain("closeConnectionPreview");
    expect(view).toContain("openConnectionEditor");
    expect(view).toContain("detailConnection");
    expect(view).toContain("selectedAuthScheme");
    expect(view).toContain("selectedPublicAuthFields");
    expect(view).toContain("selectedCredentialField");
    expect(view).toContain("environmentLabel");
    expect(view).toContain("保存草稿");
    expect(view).toContain("保存并验证");
    expect(view).toContain("验证接口");
    expect(view).toContain("验证方法");
    expect(view).toContain("验证路径");
    expect(view).toContain("期望状态码");
    expect(view).toContain("响应包含");
    expect(view).toContain("使用环境");
    expect(view).toContain("Token Endpoint（Provider）");
    expect(view).toContain("Client Authentication");
    expect(view).toContain("资源请求注入");
    expect(view).toContain("selectedPublicAuthFields");
    expect(view).toContain("selectedCredentialField");
    expect(view).not.toContain("API Key + API Secret");
    expect(view).not.toContain("API Key 名称");
    expect(view).not.toContain("API Secret 名称");
    expect(view).not.toContain("Token 值");
    expect(view).not.toContain("Credential Secret ID");
    expect(view).not.toContain("Header 名称");
    expect(view).toContain("凭据与 Connection 由后端统一编排");
    expect(view).toContain("后端返回的稳定诊断码");
    expect(view).toContain("Provider 端点（只读）");
    expect(view).toContain("验证接口通过");
    expect(view).toContain("凭证已配置/已获取");
    expect(view).toContain("connection-status-pill");
    expect(view).toContain("{ label: \"生产\", value: \"生产\" }");
    expect(view).toContain("{ label: \"测试\", value: \"测试\" }");
    expect(view).not.toContain("Production\", \"Sandbox\", \"Staging");
    expect(view).not.toContain("environment: \"Production\"");
    expect(view).not.toContain("name: \"消息服务 HTTP\"");
    expect(view).not.toContain("domain: \"message-api.actweave.local\"");
    expect(view).not.toContain("host: \"10.24.19.8\"");
    expect(view).not.toContain("port: \"9443\"");
    expect(view).not.toContain("tokenUrl: \"https://auth.actweave.local/oauth/token\"");
    expect(view).not.toContain("v-model=\"draftConnection.authConfig.label\"");
    expect(view).not.toContain("Token 可获取");
    expect(view).not.toContain("Refresh Token 策略");
    expect(view).not.toContain("验证连接与 Token 策略");
    expect(view).not.toContain("class=\"primary-button full\"");
    expect(view).not.toContain("connection-card-grid");
    expect(view).not.toContain("service-connection-card");
    expect(view).not.toContain("connection-card-verify");
    expect(view).not.toContain("connection-card-tool-count");
    expect(view).not.toContain("segmented-filter");
    expect(view).not.toContain("search-box");
    expect(view).not.toContain("token-status-pill");
    expect(view).toContain("AppSelect");
    expect(view).not.toContain("<select");
    expect(view).not.toContain("connection-wizard");
    expect(view).not.toContain("service-connection-shell");
    expect(view).not.toContain("service-connection-card-list");
    expect(view).not.toContain("connection-card-meta");
    expect(view).not.toContain("connection-detail-panel");
    expect(view).not.toContain("connection-detail-empty");
    expect(view).not.toContain("service-connection-head");
    expect(view).not.toContain("service-connection-row");
    expect(view).not.toContain("span-12 connection-detail-card");
    expect(view).not.toContain("当前后端为模拟验证");
    expect(view).not.toContain("@click.stop=\"openConnectionDetail(connection)\"");
    expect(scopedStyles).toContain(".service-connections-page");
    expect(scopedStyles).toContain(".connection-page-header");
    expect(scopedStyles).toContain(".connection-reference-table-card.management-list-card");
    expect(scopedStyles.match(/\.connection-reference-table-card\.management-list-card\s*\{[\s\S]*?\n\}/)?.[0] || "").toContain("background: transparent");
    expect(scopedStyles).toContain(".connection-management-list");
    expect(scopedStyles).toContain(".connection-mobile-card");
    expect(scopedStyles).toContain(".connection-status-pill");
    expect(scopedStyles).toContain(".connection-tool-pill");
    expect(scopedStyles).not.toContain(".connection-icon-action");
    expect(scopedStyles).toContain(".connection-detail-card");
    expect(scopedStyles).toContain(".connection-form-modal");
    expect(scopedStyles).toContain(".connection-reference-select");
    expect(scopedStyles).toContain(".connection-select-menu");
    expect(scopedStyles).not.toContain(".service-connection-shell");
    expect(scopedStyles).not.toContain(".service-connection-card-list");
    expect(scopedStyles).not.toContain(".connection-card-meta");
    expect(scopedStyles).not.toContain(".connection-detail-panel");
    expect(scopedStyles).not.toContain(".connection-detail-empty");
    expect(scopedStyles).not.toContain(".service-connection-head");
    expect(scopedStyles).not.toContain(".service-connection-row");
    expect(view).not.toContain('<table class="service-connection-table">');
    expect(view).not.toContain("<el-table");
    expect(view).not.toContain("m2-editor");
  });

  it("uses the shared management list for the OpenAPI import registry", () => {
    const view = readView("OpenAPIImportsView.vue");
    const scopedStyles = view.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";
    const columnsBlock = view.match(/const openAPIImportColumns[\s\S]*?\n\]\);/)?.[0] || "";

    expect(view).toContain("openapi-import-page");
    expect(view).toContain("ManagementPageHeader");
    expect(view).toContain("openapi-page-header");
    expect(view).toContain("primary-button");
    expect(view).toContain("ghost-button");
    expect(view).toContain("openapi-import-table-card");
    expect(view).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(view).toContain("<ManagementSegmentedFilter");
    expect(view).not.toContain("openapi-filter-tabs");
    expect(view).toContain("<ManagementList");
    expect(view).toContain("ManagementListColumn<OpenAPIImport>");
    expect(view).toContain('storage-key="actweave:openapi-imports:columns"');
    expect(view).toContain('<template #card="{ row: record }">');
    expect(columnsBlock).toContain('label: "导入文件"');
    expect(columnsBlock).toContain('label: "服务连接"');
    expect(columnsBlock).toContain('label: "接口数"');
    expect(columnsBlock).toContain('label: "可生成"');
    expect(columnsBlock).toContain('label: "待处理"');
    expect(columnsBlock).toContain('label: "导入时间"');
    expect(columnsBlock).toContain('label: "状态"');
    expect(columnsBlock).toContain('label: "操作"');
    expect(view).toContain("openapi-file-cell");
    expect(view).toContain("openapi-count-cell");
    expect(view).toContain("openapi-status-pill");
    expect(view).toContain("ManagementRowActions");
    expect(view).toContain("openAPIImportMenuActions");
    expect(view).toContain("openapi-empty-state");
    expect(view).toContain("import-drawer-preview");
    expect(view).toContain("openapi-modal-head");
    expect(view).toContain("openapi-reference-select");
    expect(view).toContain("openapi-select-menu");
    expect(view).toContain('class="openapi-import-mode-tabs"');
    expect(view).toContain("workspaceLabel");
    expect(view).toContain("providerLabel");
    expect(view).toContain("fa-solid fa-wand-magic-sparkles");
    expect(view).toContain("<style scoped>");
    expect(view).not.toContain("openapi-import-row");
    expect(view).not.toContain("tool-main-panel");
    expect(view).not.toContain("segmented-filter");
    expect(view).not.toContain("search-box");
    expect(view).not.toContain("select-field");
    expect(view).not.toContain('<table class="openapi-import-table"');
    expect(view).not.toContain("<el-table");
    expect(view).not.toContain("导入示例 OpenAPI");
    expect(scopedStyles).toContain(".openapi-import-page");
    expect(scopedStyles.match(/\.openapi-import-table-card\.management-list-card\s*\{[\s\S]*?\n\}/)?.[0] || "").toContain("background: transparent");
    expect(scopedStyles).toContain(".openapi-import-management-list");
    expect(scopedStyles).toContain(".openapi-import-mobile-card");
    expect(scopedStyles).toContain(".openapi-status-pill");
    expect(scopedStyles).toContain(".openapi-reference-select");
    expect(scopedStyles).not.toContain(".openapi-filter-tabs");
  });

  it("uses the prototype model API registry layout", () => {
    const view = readView("ModelAPIConfigsView.vue");
    const scopedStyles = view.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(view).toContain("ManagementList");
    expect(view).toContain("modelConfigColumns");
    expect(view).toContain('storage-key="actweave:model-api-configs:columns"');
    expect(view).toContain(':sticky-left-keys="[\'config\']"');
    expect(view).toContain(':sticky-right-keys="[\'actions\']"');
    expect(view).toContain("model-config-page");
    expect(view).toContain("ManagementPageHeader");
    expect(view).toContain("model-config-header");
    expect(view).toContain("model-config-card");
    expect(view).toContain("model-config-management-list");
    expect(view).toContain("@page-change=\"changeModelConfigPage\"");
    expect(view).toContain("model-config-name-cell");
    expect(view).toContain("model-provider-pill");
    expect(view).toContain("model-latency-value");
    expect(view).toContain('import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue"');
    expect(view).toContain("<ManagementRowActions");
    expect(view).toContain("model-modal-backdrop");
    expect(view).toContain("model-modal-head");
    expect(view).toContain("model-modal-field");
    expect(view).toContain("<style scoped>");
    expect(view).toContain('label: "配置名称"');
    expect(view).toContain('label: "Provider"');
    expect(view).toContain('label: "凭据"');
    expect(view).not.toContain('label: "API Key"');
    expect(view).toContain('label: "API 请求地址"');
    expect(view).toContain('label: "模型名称"');
    expect(view).toContain('label: "延迟"');
    expect(view).toContain('label: "操作"');
    expect(view).not.toContain("model-api-table-head");
    expect(view).not.toContain("model-api-list");
    expect(view).not.toContain("model-api-row");
    expect(view).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(view).toContain("<ManagementSegmentedFilter");
    expect(view).not.toContain("search-box");
    expect(view).not.toContain("@element-plus/icons-vue");
    expect(view).not.toContain("<el-table");
    expect(view).not.toContain("m1-editor");
    expect(scopedStyles).toContain(".model-config-page");
    expect(scopedStyles).not.toContain(".model-config-management-list :deep(.data-table .is-sticky-boundary-right::after)");
    expect(scopedStyles).toContain(".model-provider-pill");
    expect(scopedStyles).toContain(".model-modal-field");
  });

  it("shares compact row actions and sticky-column treatment across management tables", () => {
    const managementRowActions = readComponent("ManagementRowActions.vue");
    const dataTable = readComponent("DataTable.vue");
    const managementViews = [
      { fileName: "AgentsView.vue", columnName: "agentColumns", neutralSelection: true },
      { fileName: "ToolsView.vue", columnName: "toolColumns", neutralSelection: false },
      { fileName: "WorkflowView.vue", columnName: "workflowColumns", neutralSelection: true },
      { fileName: "ModelAPIConfigsView.vue", columnName: "modelConfigColumns", neutralSelection: false },
      { fileName: "ProvidersView.vue", columnName: "providerColumns", neutralSelection: false },
    ];

    for (const { fileName, columnName, neutralSelection } of managementViews) {
      const view = readView(fileName);
      const columnsBlock = view.match(new RegExp(`const ${columnName}[\\s\\S]*?\\n\\]\\);`))?.[0] || "";

      expect(view, `${fileName} should use the shared row actions`).toContain("<ManagementRowActions");
      expect(columnsBlock, `${fileName} should reserve the shared action width`).toContain('{ key: "actions"');
      expect(columnsBlock, `${fileName} should use the shared action header`).toContain('label: "操作"');
      expect(columnsBlock, `${fileName} should use menu-only action column width`).toContain("width: 68");
      expect(view, `${fileName} should keep list rows menu-only`).toContain(":menu-actions=");
      expect(view, `${fileName} should not expose primary row actions`).not.toContain(":primary-actions=");
      if (neutralSelection) {
        expect(view, `${fileName} should keep row selection neutral`).toContain('selection-tone="neutral"');
      }
    }

    const rowActionsLayout = managementRowActions.match(/\.management-row-actions\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const actionButtonLayout = managementRowActions.match(/\.management-row-action-button\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(rowActionsLayout).toContain("gap: 4px;");
    expect(actionButtonLayout).toContain("width: 44px;");
    expect(actionButtonLayout).toContain("height: 44px;");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain(".data-table-scroll.has-scroll-left .data-table .is-sticky-boundary-right");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
  });

  it("uses a workflow center entry page with a separate editor mode", () => {
    const view = readView("WorkflowView.vue");

    expect(view).toContain("workflow-center-page");
    expect(view).toContain("workflow-orchestration-page");
    expect(view).toContain("ManagementPageHeader");
    expect(view).toContain('title="编排"');
    expect(view).not.toContain("workflow-view-toggle");
    expect(view).toContain("ManagementSummaryStrip");
    expect(view).not.toContain("canvasTargetWorkflowId");
    expect(view).toContain("workflow-orchestration-table-card");
    expect(view).toContain("<ManagementList");
    expect(view).toContain("ManagementListColumn<WorkflowSummary>");
    expect(view).toContain('storage-key="actweave:workflows:columns"');
    expect(view).not.toContain("workflow-agent-chip");
    expect(view).not.toContain("agentId");
    expect(view).toContain("workflow-status-badge");
    expect(view).toContain("<ManagementRowActions");
    expect(view).toContain('menu-label="更多编排操作"');
    expect(view).toContain('selection-tone="neutral"');
    expect(view).toContain("workflowDetailVisible");
    expect(view).toContain("workflowEditorVisible");
    expect(view).toContain("workflowMetadataVisible");
    expect(view).toContain("openWorkflowDetail");
    expect(view).toContain("openWorkflowEditor");
    expect(view).toContain("workflow-detail-modal-card");
    expect(view).toContain("workflow-metadata-modal-card");
    expect(view).toContain("workflowStore.createWorkflow");
    expect(view).toContain("workflowStore.updateWorkflow");
    expect(view).toContain("workflowStore.deleteWorkflow");
    expect(view).toContain("workflowStore.validateWorkflow");
    expect(view).toContain("workflowStore.trialRunWorkflow");
    expect(view).toContain("workflowStore.publishWorkflow");
    expect(view).toContain("流程详情");
    expect(view).toContain("这个流程做什么");
    expect(view).toContain("什么时候触发");
    expect(view).toContain("包含哪些步骤");
    expect(view).toContain("编辑流程图");
    expect(view).toContain("workflow-editor-overlay");
    expect(view).toContain('v-if="workflowEditorVisible"');
    expect(view).toContain("workflow-workbench");
    expect(view).toContain("WorkflowNodePalette");
    expect(view).toContain("WorkflowGraphCanvas");
    expect(view).toContain("WorkflowInspector");
    expect(view).toContain("WorkflowIssuesPanel");
    expect(view).toContain("activeDraft.graph");
    expect(view).toContain("AppSelect");
    expect(view).toContain("<style scoped>");
    expect(view).not.toContain("<el-drawer");
    expect(view).not.toContain("workflowDrawerVisible");
    expect(view).not.toContain("canvas-page");
    expect(view).not.toContain("workflow-registry");
    expect(view).not.toContain("workflow-registry-row");
    expect(view).not.toContain("workflow-table-head");
    expect(view).not.toContain("Workflow DAG Editor");
    expect(view).not.toContain("<select");
    expect(view).not.toContain("canvas-toolbar");
    expect(view).not.toContain("canvas-board");

    const styles = readStyle("app.css");
    expect(styles).toContain(".workflow-center-page > *");
    expect(styles).toContain(".workflow-detail-modal-card");
    expect(styles).toContain(".workflow-metadata-modal-card");
    expect(styles).toContain("grid-column: 1 / -1");
    expect(styles).toContain("grid-template-columns: minmax(0, 1fr)");
    expect(styles).toContain("height: auto");
    expect(styles).toContain("align-self: start");
    expect(styles).toContain(".workflow-workbench");
    expect(styles).toContain(".workflow-node-palette");
    expect(styles).toContain(".workflow-graph-canvas");
    expect(styles).toContain(".workflow-inspector");
  });

  it("uses a conversation-first orchestrator console with on-demand side panels", () => {
    const view = readView("ChatExecutionView.vue");
    const scopedStyles = view.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(view).toContain("orchestrator-console-page");
    expect(view).toContain("orchestrator-console-topbar");
    expect(view).toContain("chat-context-dropdown");
    expect(view).toContain("chat-context-dropdown-menu");
    expect(view).toContain("chat-workbench");
    expect(view).toContain("activeSidePanel");
    expect(view).toContain("chat-side-panel-backdrop");
    expect(view).toContain("chat-session-rail");
    expect(view).toContain("chat-session-search");
    expect(view).toContain("chat-session-card");
    expect(view).toContain("chat-conversation-panel");
    expect(view).toContain("runtime-console-header");
    expect(view).toContain("runtime-console-header-content");
    expect(view).toContain("runtime-summary-list");
    expect(view).toContain('ref="chatScrollArea"');
    expect(view).toContain("scrollToLatestTurn");
    expect(view).toContain("message-row");
    expect(view).toContain("assistant-bubble");
    expect(view).toContain("user-bubble");
    expect(view).toContain("risk-gate-card");
    expect(view).toContain("prompt-suggestion-strip");
    expect(view).toContain("chat-composer-dock");
    expect(view).toContain("runtime-monitor-panel");
    expect(view).toContain("runtime-decision-card");
    expect(view).toContain("runtime-step-list");
    expect(view).toContain("runtime-policy-card");
    expect(view).toContain("runtime-trace-block");
    expect(view).toContain('<details class="runtime-policy-card"');
    expect(view).not.toContain("1,284 req/min");
    expect(view).not.toContain("148ms");
    expect(view).toContain("toggleChatDropdown('workspace')");
    expect(view).toContain("toggleChatDropdown('agent')");
    expect(view).not.toContain("AppSelect");
    expect(view).not.toContain("page-grid chat-page");
    expect(view).not.toContain("chat-stream-panel");
    expect(view).not.toContain('class="panel span-3 runtime-panel"');
    expect(view).not.toContain("composer-input");
    expect(view).not.toContain("<el-button");
    expect(view).not.toContain("<el-input");
    expect(scopedStyles).toContain(".orchestrator-console-page");
    expect(scopedStyles).toContain("--chat-content-width: 800px");
    expect(scopedStyles).toContain(".chat-composer-dock > *");
    expect(scopedStyles).toContain(".chat-session-rail");
    expect(scopedStyles).toContain(".assistant-bubble");
    expect(scopedStyles).toContain(".risk-gate-card");
    expect(scopedStyles).toContain(".runtime-monitor-panel");
  });

  it("keeps hover previews and click feedback in stable shared layers", () => {
    const agentsView = readView("AgentsView.vue");
    const serviceConnectionsView = readView("ServiceConnectionsView.vue");
    const toolsView = readView("ToolsView.vue");
    const workflowView = readView("WorkflowView.vue");
    const modelAPIConfigsView = readView("ModelAPIConfigsView.vue");
    const styles = readStyle("app.css");

    for (const view of [agentsView, serviceConnectionsView, toolsView, workflowView, modelAPIConfigsView]) {
      expect(view).toContain("action-toast");
    }
    expect(agentsView).toContain("agent-prompt-detail-dialog");
    expect(agentsView).toContain("agent-prompt-detail-modal");
    expect(agentsView).toContain("agent-prompt-markdown");
    expect(styles).toContain(".agent-list");
    expect(styles).toContain("overflow: visible");
    expect(styles).toContain(".agent-prompt-detail-dialog");
    expect(styles).toContain(".agent-prompt-markdown");
    expect(styles).toContain(".action-toast");
    expect(styles).toContain("bottom: 72px");
  });

  it("routes every management-page select through the shared dropdown treatment", () => {
    const sharedSelectViews = [
      "AgentsView.vue",
      "OpenAPIImportsView.vue",
      "ProvidersView.vue",
      "ServiceConnectionsView.vue",
      "ToolsView.vue",
      "WorkflowView.vue",
      "WorkspacesView.vue",
    ];
    const styles = readStyle("app.css");

    for (const fileName of sharedSelectViews) {
      const view = readView(fileName);
      expect(view, `${fileName} has an unwrapped native select`).not.toContain("<select");
    }

    expect(styles).toContain(".drawer-field:has(select)::after");
    expect(styles).toContain(".el-select .el-select__wrapper");
    expect(styles).toContain(".el-select.is-focused .el-select__wrapper");
    expect(styles).toContain(".el-select__popper");
    expect(styles).toContain(".el-select-dropdown__item.is-selected");
  });

  it("uses one shared styled select component in Agent and Tool create drawers", () => {
    const agentsView = readView("AgentsView.vue");
    const toolsView = readView("ToolsView.vue");
    const appSelect = readComponent("AppSelect.vue");
    const styles = readStyle("app.css");

    expect(toolsView).toContain("AppSelect");
    expect(toolsView).not.toContain("<select");
    expect(toolsView).not.toContain("select-field");
    expect(agentsView).toContain("AppSelect");
    expect(agentsView).toContain("agent-capability-dialog");
    expect(agentsView).toContain('class="modal-field select-field"');
    expect(appSelect).toContain('popper-class="app-select-popper"');
    expect(appSelect).toContain('class="app-select"');
    expect(appSelect).toContain("ariaInvalid?: boolean");
    expect(appSelect).toContain("ariaDescribedby?: string");
    expect(appSelect).toContain(":aria-invalid=\"ariaInvalid\"");
    expect(appSelect).toContain(":aria-describedby=\"ariaDescribedby\"");
    expect(styles).toContain(".app-select .el-select__wrapper");
    expect(styles).toContain(".app-select-popper.el-select__popper");
    const appSelectPopperBlock = styles.match(/\.app-select-popper\.el-select__popper\s*\{[\s\S]*?\}/)?.[0] || "";
    expect(appSelectPopperBlock).toContain("z-index:");
  });

  it("keeps modal and full-screen overlays above sticky topbar and fluid island chrome", () => {
    const styles = readStyle("app.css");
    const models = readView("ModelAPIConfigsView.vue");
    const providers = readView("ProvidersView.vue");
    const openapi = readView("OpenAPIImportsView.vue");
    const connections = readView("ServiceConnectionsView.vue");
    const smartDag = readView("SmartDagView.vue");
    const agentAccess = readView("AgentAccessView.vue");
    const rowActions = readComponent("ManagementRowActions.vue");
    const segmentedFilter = readComponent("ManagementSegmentedFilter.vue");
    const managementList = readComponent("ManagementList.vue");

    const readZ = (source: string, selector: string) => {
      const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      const block =
        source.match(new RegExp(`${escaped}\\s*\\{[^}]*\\}`))?.[0]
        || source.match(new RegExp(`${escaped}\\s*\\{[\\s\\S]*?\\n\\}`))?.[0]
        || "";
      const match = block.match(/z-index:\s*(\d+)/);
      return match ? Number(match[1]) : NaN;
    };

    const topbarZ = readZ(styles, ".app-shell > .app-topbar");
    const islandZ = readZ(styles, ".fluid-island");
    const modalZ = readZ(styles, ".modal-backdrop");
    const workflowEditorZ = readZ(styles, ".workflow-editor-overlay");
    const toastZ = readZ(styles, ".action-toast");
    const selectZ = readZ(styles, ".app-select-popper.el-select__popper");
    const workbenchZ = readZ(styles, ".tool-contract-workbench-modal");
    const contextMenuZ = readZ(styles, ".workflow-context-menu");

    expect(topbarZ).toBe(2500);
    expect(islandZ).toBe(2800);
    expect(modalZ).toBeGreaterThan(islandZ);
    expect(workflowEditorZ).toBeGreaterThan(islandZ);
    expect(workbenchZ).toBeGreaterThan(islandZ);
    expect(contextMenuZ).toBeGreaterThan(workflowEditorZ);
    expect(toastZ).toBeGreaterThan(modalZ);
    expect(selectZ).toBeGreaterThan(modalZ);

    const connectionDeleteZ = Number(
      connections.match(/\.connection-delete-modal[\s\S]*?z-index:\s*(\d+)/)?.[1] || NaN,
    );
    const pageLocalOverlays = [
      readZ(models, ".model-modal-backdrop"),
      readZ(models, ".model-confirmation-backdrop"),
      readZ(providers, ".provider-modal-backdrop"),
      readZ(openapi, ".openapi-modal-backdrop"),
      readZ(connections, ".connection-form-modal"),
      connectionDeleteZ,
      readZ(smartDag, ".smart-modal-backdrop"),
      readZ(agentAccess, ".modal-backdrop"),
    ];

    for (const z of pageLocalOverlays) {
      expect(z).toBeGreaterThan(islandZ);
    }

    expect(connectionDeleteZ).toBeGreaterThan(readZ(connections, ".connection-form-modal"));
    expect(readZ(rowActions, ".management-row-actions-menu")).toBeGreaterThan(topbarZ);
    expect(readZ(segmentedFilter, ".management-filter-menu")).toBeGreaterThan(topbarZ);
    expect(readZ(managementList, ".data-table-column-menu")).toBeGreaterThan(topbarZ);
    expect(readZ(providers, ".provider-action-toast")).toBeGreaterThan(readZ(providers, ".provider-modal-backdrop"));
  });

  it("wires the seven management registries to approved server sort keys without changing fixed action widths", () => {
    const workspaces = readView("WorkspacesView.vue");
    const agents = readView("AgentsView.vue");
    const models = readView("ModelAPIConfigsView.vue");
    const connections = readView("ServiceConnectionsView.vue");
    const tools = readView("ToolsView.vue");
    const imports = readView("OpenAPIImportsView.vue");
    const workflows = readView("WorkflowView.vue");

    for (const [view, keys] of [
      [workspaces, ["name", "status", "mode", "updatedBy", "createdBy"]],
      [agents, ["name", "workspace", "model", "status"]],
      [models, ["name", "provider", "apiBase", "modelName", "latency"]],
      [connections, ["name", "protocol", "environment", "address", "authMode", "status"]],
      [tools, ["name", "protocol", "status", "updatedAt"]],
      [imports, ["fileName", "connection", "totalEndpoints", "readyEndpoints", "issueCount", "createdAt", "status"]],
      [workflows, ["name", "workspace", "nodeCount", "status"]],
    ] as const) {
      for (const key of keys) expect(view).toContain(`sortable: true, sortKey: "${key}"`);
      expect(view).toContain(":sort-by=");
      expect(view).toContain(":sort-order=");
      expect(view).toContain('@sort-change=');
      expect(view).toContain("management-page-grid");
      expect(view).toContain("management-list-card");
    }

    expect(agents.match(/key: "prompt"[^\n]+/)?.[0]).not.toContain("sortable: true");
    expect(models.match(/key: "credential"[^\n]+/)?.[0]).not.toContain("sortable: true");
    expect(workflows.match(/key: "successRate"[^\n]+/)?.[0]).not.toContain("sortable: true");
    for (const view of [workspaces, agents, models, connections, tools, imports, workflows]) {
      expect(view.match(/key: "actions"[^\n]+/)?.[0]).not.toContain("sortable: true");
    }
    for (const view of [workspaces, agents, models, connections, tools, imports, workflows]) {
      expect(view).toContain('{ key: "actions", label: "操作", width: 68');
    }
  });

  it("opts management pages into viewport height while retaining table and sticky-action geometry", () => {
    const appCss = readStyle("app.css");
    const managementList = readComponent("ManagementList.vue");
    const dataTable = readComponent("DataTable.vue");

    expect(appCss).toContain(".management-page-grid");
    expect(appCss).toContain("grid-template-rows:");
    expect(appCss).toContain(".content-area:has(> .management-page-grid)");
    expect(managementList).toContain("overflow: auto;");
    expect(dataTable).toContain('tbody td[data-column-key="actions"]');
    expect(dataTable).toContain("padding-right: 12px;");
    expect(dataTable).toContain("padding-left: 8px;");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain("has-scroll-left");
    expect(dataTable).toContain("has-scroll-right");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
  });

  it("keeps the Model API wrapper in the remaining-height flex chain", () => {
    const modelView = readView("ModelAPIConfigsView.vue");
    const wrapperBlock = modelView.match(/\.model-config-table-wrap\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const listBlock = modelView.match(/\.model-config-table-wrap\s*>\s*\.model-config-management-list\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(wrapperBlock).toContain("display: flex;");
    expect(wrapperBlock).toContain("min-height: 0;");
    expect(wrapperBlock).toContain("flex: 1 1 auto;");
    expect(listBlock).toContain("height: 100%;");
    expect(listBlock).toContain("min-height: 0;");
    expect(listBlock).toContain("flex: 1 1 auto;");
  });

  it("keeps specialized connection dropdowns and shared provider selects styled", () => {
    const serviceConnectionsView = readView("ServiceConnectionsView.vue");
    const scopedStyles = serviceConnectionsView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(serviceConnectionsView).toContain("AppSelect");
    expect((serviceConnectionsView.match(/class="connection-reference-select/g) || []).length).toBeGreaterThanOrEqual(3);
    expect(serviceConnectionsView).toContain("toggleConnectionDropdown('environment')");
    expect(serviceConnectionsView).toContain("toggleConnectionDropdown('verificationMethod')");
    expect(serviceConnectionsView).toContain("toggleConnectionDropdown('refreshMode')");
    expect(serviceConnectionsView).not.toContain("toggleConnectionDropdown('authMode')");
    expect(serviceConnectionsView).not.toContain("toggleConnectionDropdown('credentialPlacement')");
    expect((serviceConnectionsView.match(/<select\b/g) || []).length).toBe(0);
    expect(serviceConnectionsView).toContain('class="connection-field select-field"');
    expect(serviceConnectionsView).toContain("providerOptions");
    expect(scopedStyles).toContain(".connection-reference-select");
    expect(scopedStyles).toContain(".connection-select-menu");
    expect(scopedStyles).toContain(".connection-select-option");
  });

});
