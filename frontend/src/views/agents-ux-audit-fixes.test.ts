import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const agentsView = readFileSync(resolve(currentDir, "AgentsView.vue"), "utf8");
const dataTable = readFileSync(resolve(currentDir, "../components/DataTable.vue"), "utf8");
const managementRowActions = readFileSync(resolve(currentDir, "../components/ManagementRowActions.vue"), "utf8");

describe("agents view UX audit fixes", () => {
  it("guards Agent save and delete actions against accidental destructive or duplicate requests", () => {
    expect(agentsView).toContain("const savingAgent = ref(false)");
    expect(agentsView).toContain("if (savingAgent.value) return;");
    expect(agentsView).toContain("const canSaveAgent = computed");
    expect(agentsView).toContain(":disabled=\"!canSaveAgent\"");
    expect(agentsView).toContain("agentSaveButtonLabel");
    expect(agentsView).toContain("const agentDeleteTarget = ref<Agent | null>(null)");
    expect(agentsView).toContain("const canConfirmAgentDelete = computed");
    expect(agentsView).toContain("function confirmDeleteAgent");
    expect(agentsView).toContain(":disabled=\"agentDeleting || !canConfirmAgentDelete\"");
  });

  it("makes Agent dialogs keyboard-operable with transitions, initial focus, focus trapping, and Esc close", () => {
    expect(agentsView).toContain("<Transition name=\"modal-fade\">");
    expect(agentsView).toContain("@keydown.esc=\"requestCloseStudio('keyboard')\"");
    expect(agentsView).toContain("@keydown.esc=\"closePromptDetail\"");
    expect(agentsView).toContain("@keydown=\"trapAgentModalFocus\"");
    expect(agentsView).toContain("ref=\"agentStudioPanelRef\"");
    expect(agentsView).toContain("ref=\"promptDetailDialogRef\"");
    expect(agentsView).toContain("function restoreLastFocus");
    expect(agentsView).toContain("function trapAgentModalFocus(event: KeyboardEvent)");
  });

  it("adds explicit accessible semantics to labeled actions, segmented filters, selects, and search controls", () => {
    expect(agentsView).toContain("ManagementSegmentedFilter");
    expect(agentsView).toContain("ariaLabel=\"Agent 状态筛选\"");
    expect(agentsView).not.toContain("ManagementSummaryCards");
    expect(agentsView).not.toContain('aria-label="按业务空间筛选 Agent"');
    expect(agentsView).not.toContain("agentWorkspaceFilter");
    expect(agentsView).toContain("activeWorkspaceFilterId");
    expect(agentsView).toContain("aria-label=\"搜索 Agent 或角色职责\"");
    expect(agentsView).toContain('label: "管理 Capability Binding"');
    expect(agentsView).toContain('label: "删除 Agent"');
    expect(managementRowActions).toContain(':aria-label="actionItem.label"');
    expect(agentsView).toContain("role=\"switch\"");
    expect(agentsView).toContain(":aria-checked=\"draftAgent.status === 'ACTIVE'\"");
    expect(agentsView).toContain("aria-required=\"true\"");
    expect(agentsView).toContain(":aria-required=\"true\"");
    expect(agentsView).toContain(":aria-invalid=\"Boolean(agentWorkspaceError)\"");
    expect(agentsView).toContain(":aria-describedby=\"agentWorkspaceError ? 'agent-workspace-error' : undefined\"");
    expect(agentsView).toContain(":aria-invalid=\"Boolean(agentModelError)\"");
    expect(agentsView).toContain(":aria-describedby=\"agentModelError ? 'agent-model-error' : undefined\"");
  });

  it("raises Agent-specific click targets and low-contrast text to the documented management-page baseline", () => {
    expect(agentsView).toContain("min-height: 44px;");
    expect(agentsView).toContain(".agent-avatar");
    expect(dataTable).toContain("height: 56px;");
    expect(dataTable).toContain("padding: 0 16px;");
    expect(agentsView).toContain("ManagementRowActions");
    expect(managementRowActions).toContain("width: 44px;");
    expect(managementRowActions).toContain("height: 44px;");
    expect(agentsView).toContain(".prompt-preview-trigger");
    expect(agentsView).toContain(".agent-summary-card small");
    expect(agentsView).toContain("color: #64748b;");
    expect(dataTable).toContain(".data-table th");
    expect(dataTable).toContain("color: var(--aw-table-header-color, #6b7280);");
    expect(agentsView).toContain(".agent-studio-actions .primary-button");
    expect(agentsView).toContain(".agent-prompt-detail-footer .primary-button");
    expect(agentsView).toContain("color: #047857;");
  });

  it("keeps row selection, table overflow, and clipped text accessible", () => {
    expect(agentsView).toContain("agent-select-button");
    expect(agentsView).toContain(":aria-pressed=\"selectedAgent?.id === agent.id\"");
    expect(agentsView).not.toContain("role=\"button\"\n              :aria-selected");
    expect(agentsView).not.toContain("@keydown.enter.prevent=\"selectAgent(agent)\"");
    expect(agentsView).not.toContain("@keydown.space.prevent=\"selectAgent(agent)\"");
    expect(agentsView).toContain(':title="agent.name"');
    expect(agentsView).toContain(":title=\"agent.roleDescription\"");
    expect(agentsView).toContain(':title="workspaceLabel(agent)"');
    expect(agentsView).toContain(':title="modelLabel(agent)"');
    expect(agentsView).toContain('selection-tone="neutral"');
    expect(dataTable).toContain("table-layout: fixed;");
    expect(dataTable).toContain("position: sticky;");
  });

  it("uses the shared compact action column and short sticky separator", () => {
    expect(agentsView).toContain('{ key: "actions", label: "操作", width: 68');
    expect(agentsView).not.toContain(".table-actions");
    expect(agentsView).not.toContain(".agent-more-menu");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
    expect(managementRowActions).toContain("gap: 4px;");
  });

  it("uses a vertical Agent studio layout that keeps all parameters visible above the prompt editor", () => {
    expect(agentsView).toContain("grid-template-columns: 1fr;");
    expect(agentsView).toContain("align-content: start;");
    expect(agentsView).toContain(".agent-parameters-panel");
    expect(agentsView).toContain("overflow: visible;");
    expect(agentsView).toContain(".agent-parameters-panel .agent-studio-fields");
    expect(agentsView).toContain("grid-template-columns: repeat(2, minmax(260px, 1fr));");
    expect(agentsView).toContain(".agent-status-toggle {");
    expect(agentsView).toContain("grid-column: 1 / -1;");
  });

  it("marks required Agent studio fields and avoids an internal scrollbar in the create dialog", () => {
    expect(agentsView).toContain("class=\"required-mark\"");
    expect(agentsView).toContain("Agent 运行名称 <b class=\"required-mark\"");
    expect(agentsView).toContain("绑定业务空间 <b class=\"required-mark\"");
    expect(agentsView).toContain("决策大模型 <b class=\"required-mark\"");
    expect(agentsView).toContain("场景决策职责 <b class=\"required-mark\"");
    expect(agentsView).toContain('studioMode === "create" ? "SYSTEM PROMPT / 初始提示词" : "PROMPT ENHANCEMENT INPUT / 增强指令"');
    expect(agentsView).toContain(".agent-studio-panel {");
    expect(agentsView).toContain("overflow: hidden;");
    expect(agentsView).toContain(".agent-studio-body {");
    expect(agentsView).toContain("overflow: visible;");
    expect(agentsView).toContain("padding: 12px 0 0;");
    expect(agentsView).toContain("min-height: 150px;");
  });

  it("keeps Agent studio field controls visually aligned and uses a compact status switch", () => {
    expect(agentsView).toContain("--agent-studio-control-height");
    expect(agentsView).toContain("height: 44px;");
    expect(agentsView).toContain(":deep(.agent-studio-select .el-select__wrapper)");
    expect(agentsView).toContain("min-height: var(--agent-studio-control-height);");
    expect(agentsView).toContain(".agent-status-toggle button {");
    expect(agentsView).toContain("width: 48px;");
    expect(agentsView).toContain("min-height: 32px;");
    expect(agentsView).toContain(".agent-status-toggle button span");
    expect(agentsView).toContain("width: 20px;");
    expect(agentsView).toContain("height: 20px;");
  });

  it("adds a scannable prompt workspace summary before the long prompt editor", () => {
    expect(agentsView).toContain("promptLineCount");
    expect(agentsView).toContain("promptPreviewText");
    expect(agentsView).toContain("agent-prompt-overview");
    expect(agentsView).toContain("首段预览");
    expect(agentsView).toContain("行数");
    expect(agentsView).toContain("字符");
    expect(agentsView).toContain(".agent-prompt-preview-text");
    expect(agentsView).toContain("resize: vertical;");
    expect(agentsView).toContain("max-height: 260px;");
  });

  it("uses dismissible timed Agent feedback and does not show an empty state below the first-screen loading mask", () => {
    expect(agentsView).toContain("const pageInitialLoading = ref(true)");
    expect(agentsView).toContain("const agentToastTimer = ref<ReturnType<typeof window.setTimeout> | null>(null)");
    expect(agentsView).toContain("function showAgentToast");
    expect(agentsView).toContain("function clearAgentToast");
    expect(agentsView).toContain("aria-label=\"关闭反馈提示\"");
    expect(agentsView).toContain("v-loading=\"pageInitialLoading\"");
    expect(agentsView).toContain("element-loading-text=\"正在加载 Agent Registry...\"");
    expect(agentsView).toContain("<template #empty>");
  });

  it("prevents accidental implicit dismissal of a dirty delete dialog and only paginates multi-page registries", () => {
    expect(agentsView).toContain("const agentStudioInitialSnapshot = ref(\"\")");
    expect(agentsView).toContain("const isAgentStudioDirty = computed");
    expect(agentsView).toContain("function requestCloseStudio(source: \"backdrop\" | \"keyboard\" | \"back\")");
    expect(agentsView).toContain("@click.self=\"requestCloseStudio('backdrop')\"");
    expect(agentsView).toContain("agentStudioInlineWarning");
    expect(agentsView).toContain("已有未保存修改");
    expect(agentsView).toContain("const isAgentDeleteConfirmDirty = computed");
    expect(agentsView).toContain("function requestCloseAgentDeleteConfirm(source: \"backdrop\" | \"keyboard\")");
    expect(agentsView).toContain("window.addEventListener(\"keydown\", handleAgentDeleteDialogKeydown)");
    expect(agentsView).toContain("showAgentToast(\"为避免误关闭，已禁用当前删除确认弹框的遮罩和 Esc 关闭。请使用取消或右上角关闭明确放弃删除。\", \"error\")");
    expect(agentsView).toContain("@click.self=\"requestCloseAgentDeleteConfirm('backdrop')\"");
    expect(agentsView).toContain(":pagination=\"agents.pagination\"");
  });

  it("adds a narrow viewport affordance for the desktop-first registry", () => {
    expect(agentsView).toContain("agent-narrow-notice");
    expect(agentsView).toContain("当前页面按桌面宽度设计；在窄视口下请左右滚动表格查看完整列。");
  });
});
