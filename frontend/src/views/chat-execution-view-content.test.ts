import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const chatExecutionView = readFileSync(resolve(currentDir, "ChatExecutionView.vue"), "utf8");

describe("chat execution view content", () => {
  it("offers workspace and agent selectors before starting a new chat session", () => {
    expect(chatExecutionView).toContain("新会话配置");
    expect(chatExecutionView).toContain("选择新会话的业务空间");
    expect(chatExecutionView).toContain("选择新会话的 Agent");
    expect(chatExecutionView).toContain("应用并新建");
    expect(chatExecutionView).not.toContain("显示全部空间");
    expect(chatExecutionView).toContain("toggleChatDropdown('workspace')");
    expect(chatExecutionView).toContain("toggleChatDropdown('agent')");
    expect(chatExecutionView).toContain("chat-context-dropdown-menu");
    expect(chatExecutionView).not.toContain("AppSelect");
  });

  it("keeps global workspace selection stable across remounts and draft config changes", () => {
    // Remount (AppShell keys router-view on activeWorkspaceId) must not restore a
    // cross-workspace active session over the selected global workspace.
    expect(chatExecutionView).toContain("bootstrapChatForActiveWorkspace");
    expect(chatExecutionView).toContain("activeSessionInWorkspace");
    expect(chatExecutionView).toContain("sessionInActiveWorkspace");
    expect(chatExecutionView).toMatch(
      /session\.id === chat\.activeSessionId && session\.workspaceId === activeWorkspaceId/,
    );

    // Draft "新会话配置" workspace changes must load agents only — writing the
    // global store remounts the page and flashes the selection back.
    const workspaceWatch = chatExecutionView.match(
      /watch\(selectedWorkspaceId, async \(workspaceId\) => \{[\s\S]*?\n\}\);/,
    )?.[0];
    expect(workspaceWatch).toBeTruthy();
    expect(workspaceWatch).toContain("agents.loadAgents({ workspaceId })");
    expect(workspaceWatch).not.toContain("workspaces.selectWorkspace(workspaceId)");

    // Opening / creating a real session may still sync the global workspace.
    expect(chatExecutionView).toContain("workspaces.selectWorkspace(session.workspaceId)");
  });

  it("does not ship order-specific sample prompts or placeholders", () => {
    for (const forbidden of ["取消已支付订单", "查询订单", "A10293", "帮我取消这个订单", "订单"]) {
      expect(chatExecutionView).not.toContain(forbidden);
    }
  });

  it("delegates run updates to the chat store stream instead of page-level polling", () => {
    expect(chatExecutionView).not.toContain("setInterval");
    expect(chatExecutionView).not.toContain("startPolling");
    expect(chatExecutionView).not.toContain("loadRunSteps(chat.latestRunId)");
  });

  it("renders assistant messages through markdown html", () => {
    expect(chatExecutionView).toContain("renderMessageMarkdown");
    expect(chatExecutionView).toContain('v-html="renderMessageMarkdown(message.content)"');
    expect(chatExecutionView).not.toContain("<p>{{ message.content }}</p>");
  });

  it("keeps conversation primary and moves history and runtime detail into on-demand panels", () => {
    const scopedStyle = chatExecutionView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(chatExecutionView).toContain("orchestrator-console-page");
    expect(chatExecutionView).toContain("orchestrator-console-topbar");
    expect(chatExecutionView).toContain("chat-context-dropdown");
    expect(chatExecutionView).toContain("chat-workbench");
    expect(chatExecutionView).toContain("chat-session-rail");
    expect(chatExecutionView).toContain("chat-session-search");
    expect(chatExecutionView).toContain("chat-session-card");
    expect(chatExecutionView).toContain("chat-conversation-panel");
    expect(chatExecutionView).toContain("runtime-console-header");
    expect(chatExecutionView).toContain("runtime-console-header-content");
    expect(chatExecutionView).toContain("runtime-summary-list");
    expect(chatExecutionView).toContain('ref="chatScrollArea"');
    expect(chatExecutionView).toContain("scrollToLatestTurn");
    expect(chatExecutionView).toContain("getBoundingClientRect");
    expect(chatExecutionView).toContain("message-row");
    expect(chatExecutionView).toContain("assistant-bubble");
    expect(chatExecutionView).toContain("user-bubble");
    expect(chatExecutionView).toContain("risk-gate-card");
    expect(chatExecutionView).toContain("prompt-suggestion-strip");
    expect(chatExecutionView).toContain("chat-composer-dock");
    expect(chatExecutionView).toContain("runtime-monitor-panel");
    expect(chatExecutionView).toContain("runtime-decision-card");
    expect(chatExecutionView).toContain("runtime-step-list");
    expect(chatExecutionView).toContain("runtime-policy-card");
    expect(chatExecutionView).toContain("runtime-trace-block");
    expect(chatExecutionView).toContain("activeSidePanel");
    expect(chatExecutionView).toContain("chat-side-panel-backdrop");
    expect(chatExecutionView).not.toContain("新会话上下文");
    expect(chatExecutionView).toContain('aria-controls="chat-session-panel"');
    expect(chatExecutionView).toContain('aria-controls="chat-runtime-panel"');
    expect(chatExecutionView).toContain('role="dialog"');
    expect(chatExecutionView).toContain('aria-modal="true"');
    expect(chatExecutionView).toContain('<details class="runtime-policy-card"');
    expect(chatExecutionView).toContain('<details class="runtime-trace-section">');
    expect(chatExecutionView).toContain("技术信息与 Trace");
    expect(chatExecutionView).not.toContain("1,284 req/min");
    expect(chatExecutionView).not.toContain("99.0%");
    expect(chatExecutionView).not.toContain("148ms");
    expect(chatExecutionView).not.toContain("96.4%");
    expect(chatExecutionView).not.toContain("Execution ID:");
    expect(chatExecutionView).not.toContain("step.modelTurn?.assistantContent");
    expect(chatExecutionView).not.toContain("chat-session-card-head");
    expect(chatExecutionView).not.toContain("已启用");
    expect(chatExecutionView).toContain("<style scoped>");
    expect(chatExecutionView).not.toContain("page-grid chat-page");
    expect(chatExecutionView).not.toContain("chat-stream-panel");
    expect(chatExecutionView).not.toContain('class="panel span-3 runtime-panel"');
    expect(chatExecutionView).not.toContain("composer-input");
    expect(chatExecutionView).not.toContain("<el-button");
    expect(chatExecutionView).not.toContain("<el-input");
    expect(chatExecutionView).not.toContain("<el-tag");
    expect(chatExecutionView).not.toContain("<el-icon");
    expect(scopedStyle).toContain(".orchestrator-console-page");
    expect(scopedStyle).toContain("--chat-content-width: 800px");
    expect(scopedStyle).toContain("border-radius: 0");
    expect(scopedStyle).toContain(".chat-composer-dock > *");
    expect(scopedStyle).toContain(".chat-session-rail");
    expect(scopedStyle).toContain(".assistant-bubble");
    expect(scopedStyle).toContain(".risk-gate-card");
    expect(scopedStyle).toContain(".runtime-monitor-panel");
  });

  it("keeps chat async failures visible and disables repeat send while pending", () => {
    expect(chatExecutionView).toContain("const sending");
    expect(chatExecutionView).toContain("const actionError");
    expect(chatExecutionView).toContain('role="alert"');
    expect(chatExecutionView).toContain('aria-live="assertive"');
    expect(chatExecutionView).toContain(':disabled="sending || activeSessionReadOnly || !composer.trim()"');
    expect(chatExecutionView).toContain('aria-busy');
    expect(chatExecutionView).toContain('发送中');
    expect(chatExecutionView).toContain("try {");
    expect(chatExecutionView).toContain("catch (error)");
  });

  it("exposes custom dropdowns with menu semantics and expanded state", () => {
    expect(chatExecutionView).toContain('aria-haspopup="menu"');
    expect(chatExecutionView).toContain(':aria-expanded="chatDropdowns.workspace"');
    expect(chatExecutionView).toContain(':aria-expanded="chatDropdowns.agent"');
    expect(chatExecutionView).toContain('role="menu"');
    expect(chatExecutionView).toContain('role="menuitemradio"');
    expect(chatExecutionView).toContain(":aria-checked=");
    expect(chatExecutionView).toContain("handleDropdownMenuKeydown");
    expect(chatExecutionView).toContain('tabindex="-1"');
  });

  it("keeps message chronology and live updates understandable", () => {
    expect(chatExecutionView).toContain("message-date-separator");
    expect(chatExecutionView).toContain("shouldShowMessageDate");
    expect(chatExecutionView).toContain("messageDateTimeTitle");
    expect(chatExecutionView).toContain('role="log"');
    expect(chatExecutionView).toContain('aria-live="polite"');
    expect(chatExecutionView).toContain("hasUnreadMessages");
    expect(chatExecutionView).toContain("isConversationNearLatest");
    expect(chatExecutionView).toContain("有新消息");
  });

  it("uses authenticated identity and labels the composer for assistive technology", () => {
    expect(chatExecutionView).toContain("useAuthStore");
    expect(chatExecutionView).toContain("activeUserLabel");
    expect(chatExecutionView).not.toContain("<strong>chen.ops</strong>");
    expect(chatExecutionView).toContain('aria-label="输入业务指令或目标任务"');
    expect(chatExecutionView).toContain("chat-composer-shortcut");
    expect(chatExecutionView).toContain('aria-hidden="true"');
  });

  it("keeps archived or unavailable Agent sessions as read-only history and offers a replacement session", () => {
    expect(chatExecutionView).toContain("activeSessionAgent");
    expect(chatExecutionView).toContain("activeSessionReadOnly");
    expect(chatExecutionView).toContain("关联 Agent");
    expect(chatExecutionView).toContain("仅可查看，不能继续执行");
    expect(chatExecutionView).toContain("chat-session-agent-alert");
    expect(chatExecutionView).toContain(":disabled=\"activeSessionReadOnly\"");
    expect(chatExecutionView).toContain('chat.activeSession?.status === "ARCHIVED"');
    expect(chatExecutionView).not.toContain("agentAvailability");
  });

  it("exposes requester-only confirmation, cancellation, and non-destructive archive actions", () => {
    expect(chatExecutionView).toContain("只有本次执行的原发起人可以确认或取消");
    expect(chatExecutionView).toContain("chat.confirmPending()");
    expect(chatExecutionView).toContain("chat.cancelPending()");
    expect(chatExecutionView).toContain("chat.archiveSession()");
    expect(chatExecutionView).toContain("归档不会删除消息");
    expect(chatExecutionView).not.toContain("confirmationText");
  });

  it("keeps focus, responsive, and touch target rules for audited controls", () => {
    const scopedStyle = chatExecutionView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(scopedStyle).toContain(".chat-session-search:focus-within");
    expect(scopedStyle).not.toContain("@media (max-width: 1280px)");
    expect(scopedStyle).not.toContain("@media (max-width: 1440px)");
    expect(scopedStyle).toContain("@media (max-width: 900px)");
    expect(scopedStyle).toMatch(/\.orchestrator-console-page\s*\{[\s\S]*?height: 100%;[\s\S]*?min-height: 0;/);
    expect(scopedStyle).toMatch(/\.orchestrator-console-topbar\s*\{[\s\S]*?min-height: 52px;/);
    expect(scopedStyle).toMatch(/\.orchestrator-panel-trigger\s*\{[\s\S]*?min-height: 36px;/);
    expect(scopedStyle).toMatch(/\.chat-context-dropdown > button\s*\{[\s\S]*?min-height: 36px;/);
    expect(scopedStyle).toMatch(/\.runtime-console-header\s*\{[\s\S]*?min-height: 56px;/);
    expect(scopedStyle).toContain("padding-bottom: 48px");
    expect(scopedStyle).toMatch(/\.chat-side-panel-backdrop\s*\{[\s\S]*?position: absolute;[\s\S]*?inset: 0;/);
    expect(scopedStyle).toMatch(/\.prompt-suggestion-strip\s*\{[\s\S]*?flex-wrap: nowrap;[\s\S]*?overflow-x: auto;/);
    expect(scopedStyle).toContain("min-height: 44px");
    expect(scopedStyle).toContain("flex-wrap: wrap");
  });

  it("keeps runtime identifiers and summaries discoverable when truncated", () => {
    expect(chatExecutionView).toContain(':title="step.name"');
    expect(chatExecutionView).toContain(':title="step.summary"');
    expect(chatExecutionView).toContain("Run ID:");
    expect(chatExecutionView).toContain("Target ID:");
    expect(chatExecutionView).toContain("runStepSummary");
    expect(chatExecutionView).toContain("Capability Release：");
  });
});
