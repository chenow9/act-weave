<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import DebugOutboundCredentialPanel from "../components/DebugOutboundCredentialPanel.vue";
import { toAPIError } from "../services/api";
import { useAgentStore } from "../stores/agents";
import { useAuthStore } from "../stores/auth";
import { useChatStore } from "../stores/chat";
import { useIntegrationStore } from "../stores/integration";
import { useWorkspaceStore } from "../stores/workspaces";
import type { Agent, AgentRunStep, OutboundCredentialsEnvelope, Workspace, WorkspaceChatSession } from "../types/domain";
import { renderMarkdown } from "../utils/markdown";

type ChatDropdownKey = "workspace" | "agent";
type ChatSidePanel = "sessions" | "runtime";

const workspaces = useWorkspaceStore();
const agents = useAgentStore();
const auth = useAuthStore();
const chat = useChatStore();
const integration = useIntegrationStore();
const composer = ref("");
const selectedWorkspaceId = ref("");
const selectedAgentId = ref("");
const sessionKeyword = ref("");
const sending = ref(false);
const confirming = ref(false);
const contextLoading = ref(false);
const actionError = ref("");
const activeSidePanel = ref<ChatSidePanel>();
const sessionPanelTrigger = ref<HTMLButtonElement>();
const runtimePanelTrigger = ref<HTMLButtonElement>();
const sessionPanelCloseButton = ref<HTMLButtonElement>();
const runtimePanelCloseButton = ref<HTMLButtonElement>();
const chatScrollArea = ref<HTMLElement>();
const workspaceDropdownTrigger = ref<HTMLButtonElement>();
const agentDropdownTrigger = ref<HTMLButtonElement>();
const hasUnreadMessages = ref(false);
const forceFollowNextMessage = ref(false);
const syncingSessionSelection = ref(false);
/** One-shot outbound credential attachment for the next message only (never Token plaintext). */
const outboundAttachmentId = ref<string | null>(null);
const debugOutboundPanel = ref<InstanceType<typeof DebugOutboundCredentialPanel> | null>(null);
const debugPassthroughConnectionId = ref("");
const chatDropdowns = ref<Record<ChatDropdownKey, boolean>>({
  workspace: false,
  agent: false,
});
const selectedWorkspace = computed(() => workspaces.items.find((workspace) => workspace.id === selectedWorkspaceId.value));
const filteredAgents = computed(() =>
  agents.items.filter((agent) => !selectedWorkspaceId.value || agent.workspaceId === selectedWorkspaceId.value),
);
const selectedAgent = computed(() => filteredAgents.value.find((agent) => agent.id === selectedAgentId.value));
const activeSessionAgent = computed(() => agents.items.find((agent) => agent.id === chat.activeSession?.agentId));
const activeSessionWorkspace = computed(() => workspaces.items.find((workspace) => workspace.id === chat.activeSession?.workspaceId));
const activeWorkspaceLabel = computed(() => selectedWorkspace.value?.displayName || selectedWorkspace.value?.name || "全部业务空间");
const activeAgentLabel = computed(() => selectedAgent.value?.name || (contextLoading.value ? "载入 Agent" : "请选择 Agent"));
const activeUserLabel = computed(() => auth.user?.username || auth.user?.displayName || "当前用户");
const activeUserInitials = computed(() => {
  const source = (auth.user?.displayName || auth.user?.username || "我").trim();
  const words = source.split(/[\s._-]+/).filter(Boolean);
  return (words.length > 1 ? words.map((word) => word[0]).join("") : source.slice(0, 2)).toUpperCase();
});
const contextSelectionDirty = computed(
  () =>
    Boolean(chat.activeSession) &&
    (chat.activeSession?.workspaceId !== selectedWorkspaceId.value || chat.activeSession?.agentId !== selectedAgentId.value),
);
const activeSessionReadOnly = computed(
  () => Boolean(chat.activeSession) && (chat.activeSession?.status === "ARCHIVED" || !activeSessionAgent.value),
);
const activeSessionAgentStatusLabel = computed(() => (chat.activeSession?.status === "ARCHIVED" ? "已归档" : "不可用"));
const composerPlaceholder = computed(() => (activeSessionReadOnly.value ? "关联 Agent 已不可用，请使用可用 Agent 新建会话" : "输入业务指令或目标任务..."));
const filteredSessions = computed(() => {
  const keyword = sessionKeyword.value.trim().toLowerCase();
  if (!keyword) return chat.sessions;
  return chat.sessions.filter((session) =>
    [session.title, sessionAgentName(session), sessionWorkspaceName(session), session.latestRunId]
      .filter(Boolean)
      .join(" ")
      .toLowerCase()
      .includes(keyword),
  );
});

const latestRunStepRows = computed(() =>
  chat.latestRunSteps.map((step) => ({
    id: step.id,
    name: runStepLabel(step.stepType),
    status: timelineStatus(step.status),
    statusLabel: runStepStatusLabel(step.status),
    summary: runStepSummary(step),
  })),
);

const runtimeSummary = computed(() => {
  if (chat.runStatus === "WAITING_CONFIRMATION") return "运行时已完成意图识别，当前停在确认门禁。";
  if (chat.runStatus === "FAILED") return chat.latestRun?.errorCode || "运行时执行失败。";
  if (chat.runStatus === "SUCCEEDED") return "运行时已完成，步骤时间线如下。";
  if (chat.runStatus === "RUNNING") return "运行时正在执行，步骤会持续刷新。";
  if (chat.runStatus === "PENDING") return "请求已进入队列，等待运行时处理。";
  return "提交消息后会展示 run 状态、步骤时间线和能力快照。";
});
const runStatusLabel = computed(() => statusLabel(chat.runStatus));
const runtimeIntentLabel = computed(() => {
  if (chat.runStatus === "PENDING" || chat.runStatus === "RUNNING") return "识别中";
  if (chat.runStatus === "WAITING_CONFIRMATION") return "待确认";
  if (chat.runStatus === "SUCCEEDED") return "已完成";
  if (chat.runStatus === "FAILED" || chat.runStatus === "CANCELLED") return "未完成";
  return "—";
});
const capabilityCount = computed(() => capabilitySnapshotCount(chat.latestRun?.capabilitySnapshot));
const capabilityCountLabel = computed(() => (chat.latestRun ? `${capabilityCount.value} 项` : "—"));
const runtimeTargetReleaseId = computed(
  () => [...chat.latestRunSteps].reverse().find((step) => step.capabilityReleaseId)?.capabilityReleaseId,
);
const conversationBusy = computed(() => sending.value || chat.runStatus === "PENDING" || chat.runStatus === "RUNNING");
const currentSubjectLabel = computed(() => {
  const user = auth.user;
  if (!user) return "未登录";
  return `${user.displayName || user.username} · USER`;
});
/** Connections fixed to REQUEST_PASSTHROUGH in this workspace (need one-shot Token attach). */
const passthroughConnections = computed(() =>
  integration.serviceConnections.filter((c) => c.outboundMode === "REQUEST_PASSTHROUGH" && c.status !== "DISABLED"),
);
const requiresPassthroughToken = computed(() => passthroughConnections.value.length > 0);
const effectiveDebugConnectionId = computed(
  () => debugPassthroughConnectionId.value || passthroughConnections.value[0]?.id || "",
);

onMounted(async () => {
  await runChatAction(async () => {
    await Promise.all([workspaces.load(), agents.loadAgents()]);
    await chat.loadSessions(workspaces.items.map((workspace) => workspace.id));
    // AppShell remounts this view when the global active workspace changes.
    // Never re-apply a cross-workspace active session here — that overwrites the
    // just-selected workspace and makes the topbar/chat selectors flash back.
    await bootstrapChatForActiveWorkspace();
    if (selectedWorkspaceId.value || workspaces.activeWorkspaceId) {
      try {
        await integration.loadServiceConnectionCatalog({ commit: true });
      } catch {
        /* non-blocking for debug panel connection list */
      }
    }
  }, "会话初始化失败，请稍后重试。");
  await scrollToLatestTurn();
});

onBeforeUnmount(() => {
  chat.closeRunStream();
  outboundAttachmentId.value = null;
  debugOutboundPanel.value?.clearSecrets();
  debugOutboundPanel.value?.clearAttachment();
});

watch(selectedWorkspaceId, async (workspaceId) => {
  if (syncingSessionSelection.value) return;
  if (!workspaceId) {
    selectedAgentId.value = "";
    return;
  }
  contextLoading.value = true;
  try {
    await runChatAction(async () => {
      // Local selector is draft "新会话配置" only. Do not write the global
      // workspace store here — AppShell keys router-view on activeWorkspaceId,
      // so a store write remounts this page and drops the draft selection.
      await agents.loadAgents({ workspaceId });
      syncSelectionFromWorkspace(workspaceId);
    }, "切换业务空间失败，请稍后重试。");
  } finally {
    contextLoading.value = false;
  }
});

watch(
  () => chat.messages.length,
  (messageCount, previousCount) => {
    if (!messageCount || messageCount <= previousCount) return;
    const shouldFollow = forceFollowNextMessage.value || !previousCount || isConversationNearLatest();
    forceFollowNextMessage.value = false;
    if (shouldFollow) {
      hasUnreadMessages.value = false;
      void scrollToLatestTurn(previousCount ? "smooth" : "auto");
      return;
    }
    hasUnreadMessages.value = true;
  },
);

async function newSession() {
  if (contextLoading.value || !selectedWorkspaceId.value || !selectedAgentId.value) return;
  await runChatAction(createSessionWithSelection, "创建会话失败，请稍后重试。");
  await scrollToLatestTurn();
  closeSidePanel();
}

async function selectSession(sessionId: string) {
  await runChatAction(async () => {
    await chat.loadSession(sessionId);
    await syncSelectionFromSession(chat.activeSession);
  }, "加载会话失败，请稍后重试。");
  await scrollToLatestTurn();
  closeSidePanel();
}

async function toggleSidePanel(panel: ChatSidePanel) {
  if (activeSidePanel.value === panel) {
    closeSidePanel();
    return;
  }
  closeChatDropdowns();
  activeSidePanel.value = panel;
  await nextTick();
  if (panel === "sessions") {
    sessionPanelCloseButton.value?.focus();
  } else {
    runtimePanelCloseButton.value?.focus();
  }
}

function closeSidePanel() {
  const panel = activeSidePanel.value;
  if (!panel) return;
  activeSidePanel.value = undefined;
  void nextTick(() => {
    if (panel === "sessions") {
      sessionPanelTrigger.value?.focus();
    } else {
      runtimePanelTrigger.value?.focus();
    }
  });
}

function trapSidePanelFocus(event: KeyboardEvent) {
  const panel = event.currentTarget;
  if (!(panel instanceof HTMLElement)) return;

  const focusableElements = Array.from(
    panel.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, a[href], [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null);

  if (focusableElements.length === 0) {
    event.preventDefault();
    panel.focus();
    return;
  }

  const firstElement = focusableElements[0];
  const lastElement = focusableElements[focusableElements.length - 1];

  if (event.shiftKey && document.activeElement === firstElement) {
    event.preventDefault();
    lastElement.focus();
  } else if (!event.shiftKey && document.activeElement === lastElement) {
    event.preventDefault();
    firstElement.focus();
  }
}

async function scrollToLatestTurn(behavior: ScrollBehavior = "auto") {
  await nextTick();
  const scrollArea = chatScrollArea.value;
  if (!scrollArea) return;

  const userMessages = scrollArea.querySelectorAll<HTMLElement>(".message-row.user");
  const latestUserMessage = userMessages.item(userMessages.length - 1);
  if (!latestUserMessage) {
    scrollArea.scrollTo({ top: scrollArea.scrollHeight, behavior });
    return;
  }

  const scrollAreaBounds = scrollArea.getBoundingClientRect();
  const latestUserBounds = latestUserMessage.getBoundingClientRect();
  const latestTurnTop = scrollArea.scrollTop + latestUserBounds.top - scrollAreaBounds.top - 8;
  scrollArea.scrollTo({ top: Math.max(0, latestTurnTop), behavior });
}

function isConversationNearLatest() {
  const scrollArea = chatScrollArea.value;
  if (!scrollArea) return true;
  return scrollArea.scrollHeight - scrollArea.scrollTop - scrollArea.clientHeight <= 120;
}

function handleConversationScroll() {
  if (isConversationNearLatest()) {
    hasUnreadMessages.value = false;
  }
}

async function revealLatestMessages() {
  hasUnreadMessages.value = false;
  await scrollToLatestTurn("smooth");
}

async function attachDebugOutboundCredentials(body: OutboundCredentialsEnvelope) {
  const session = chat.activeSession;
  if (!session) throw new Error("请先选择会话");
  return integration.attachChatOutboundCredentials(session.id, body);
}

function onOutboundAttachment(id: string | null) {
  outboundAttachmentId.value = id;
}

async function send() {
  if (sending.value || activeSessionReadOnly.value || !composer.value.trim()) return;
  const value = composer.value;
  const attachmentId = outboundAttachmentId.value || undefined;
  composer.value = "";
  sending.value = true;
  forceFollowNextMessage.value = true;
  actionError.value = "";
  try {
    await chat.sendMessage(value, { outboundCredentialAttachmentId: attachmentId });
    // Attachment is single-use; clear local id after successful handoff (never stored in Pinia).
    outboundAttachmentId.value = null;
    debugOutboundPanel.value?.clearAttachment();
    debugOutboundPanel.value?.clearSecrets();
  } catch (error) {
    forceFollowNextMessage.value = false;
    actionError.value = readableChatError(error, "消息发送失败，请稍后重试。");
    if (!composer.value.trim()) {
      composer.value = value;
    }
    // Fail closed: do not retry with residual attachment id or Token.
    outboundAttachmentId.value = null;
    debugOutboundPanel.value?.clearAttachment();
    debugOutboundPanel.value?.clearSecrets();
  } finally {
    sending.value = false;
  }
}

async function confirm() {
  if (confirming.value || activeSessionReadOnly.value) return;
  confirming.value = true;
  actionError.value = "";
  try {
    await chat.confirmPending();
  } catch (error) {
    actionError.value = readableChatError(error, "授权确认失败，请稍后重试。");
  } finally {
    confirming.value = false;
  }
}

async function cancelConfirmation() {
  if (confirming.value || activeSessionReadOnly.value) return;
  confirming.value = true;
  actionError.value = "";
  try {
    await chat.cancelPending();
  } catch (error) {
    actionError.value = readableChatError(error, "取消确认失败，请稍后重试。");
  } finally {
    confirming.value = false;
  }
}

async function archiveCurrentSession() {
  if (!chat.activeSession || chat.activeSession.status === "ARCHIVED") return;
  await runChatAction(() => chat.archiveSession(), "归档会话失败，请稍后重试。");
}

function renderMessageMarkdown(content: string) {
  return renderMarkdown(content, "");
}

async function createSessionWithSelection() {
  if (!selectedWorkspaceId.value || !selectedAgentId.value) return;
  const session = await chat.createSession(selectedWorkspaceId.value, selectedAgentId.value, `${activeAgentLabel.value} 对话`);
  await syncSelectionFromSession(session, false);
}

async function bootstrapChatForActiveWorkspace() {
  const activeWorkspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
  const activeSessionInWorkspace =
    Boolean(chat.activeSessionId) &&
    chat.sessions.some(
      (session) => session.id === chat.activeSessionId && session.workspaceId === activeWorkspaceId,
    );

  if (activeSessionInWorkspace && chat.activeSessionId) {
    await chat.loadSession(chat.activeSessionId);
    await syncSelectionFromSession(chat.activeSession);
    return;
  }

  syncSelectionFromWorkspaceStore();
  const sessionInActiveWorkspace = activeWorkspaceId
    ? chat.sessions.find((session) => session.workspaceId === activeWorkspaceId)
    : undefined;
  if (sessionInActiveWorkspace) {
    await chat.loadSession(sessionInActiveWorkspace.id);
    await syncSelectionFromSession(chat.activeSession);
    return;
  }
  await createSessionWithSelection();
}

function syncSelectionFromWorkspaceStore() {
  const workspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
  selectedWorkspaceId.value = workspaceId;
  if (workspaceId) syncSelectionFromWorkspace(workspaceId);
}

function syncSelectionFromWorkspace(workspaceId: string) {
  const scopedAgents = agents.items.filter((agent) => agent.workspaceId === workspaceId);
  const defaultAgentId = workspaces.items.find((workspace) => workspace.id === workspaceId)?.defaultAgentId;
  if (scopedAgents.some((agent) => agent.id === selectedAgentId.value)) return;
  selectedAgentId.value = defaultAgentId && scopedAgents.some((agent) => agent.id === defaultAgentId) ? defaultAgentId : scopedAgents[0]?.id || "";
}

async function syncSelectionFromSession(session?: WorkspaceChatSession, reloadAgents = true) {
  if (!session) return;
  syncingSessionSelection.value = true;
  contextLoading.value = true;
  try {
    selectedWorkspaceId.value = session.workspaceId;
    // Opening / creating a real session may legitimately change the global
    // workspace (history session in another space, or apply-and-create).
    workspaces.selectWorkspace(session.workspaceId);
    if (reloadAgents) {
      await agents.loadAgents({ workspaceId: session.workspaceId });
    }
    if (agents.items.some((agent) => agent.id === session.agentId)) {
      selectedAgentId.value = session.agentId;
    } else {
      selectedAgentId.value = "";
      syncSelectionFromWorkspace(session.workspaceId);
    }
  } finally {
    contextLoading.value = false;
    syncingSessionSelection.value = false;
  }
}

function sessionAgentName(session: WorkspaceChatSession) {
  return agents.items.find((agent) => agent.id === session.agentId)?.name || session.agentId;
}

function sessionWorkspaceName(session: WorkspaceChatSession) {
  const workspace = workspaces.items.find((item) => item.id === session.workspaceId);
  return workspace?.displayName || workspace?.name || session.workspaceId;
}

async function toggleChatDropdown(key: ChatDropdownKey) {
  const opening = !chatDropdowns.value[key];
  chatDropdowns.value = {
    workspace: false,
    agent: false,
    [key]: opening,
  };
  if (!opening) return;
  await nextTick();
  focusDropdownOption(key, "first");
}

function closeChatDropdowns() {
  chatDropdowns.value = {
    workspace: false,
    agent: false,
  };
}

function selectWorkspaceOption(workspace: Workspace) {
  selectedAgentId.value = "";
  selectedWorkspaceId.value = workspace.id;
  closeChatDropdowns();
  workspaceDropdownTrigger.value?.focus();
}

function selectAgentOption(agent: Agent) {
  selectedAgentId.value = agent.id;
  closeChatDropdowns();
  agentDropdownTrigger.value?.focus();
}

function handleDropdownMenuKeydown(event: KeyboardEvent, key: ChatDropdownKey) {
  const menu = event.currentTarget;
  if (!(menu instanceof HTMLElement)) return;
  const options = Array.from(menu.querySelectorAll<HTMLButtonElement>('button[role="menuitemradio"]'));
  if (!options.length) return;

  const currentIndex = options.indexOf(document.activeElement as HTMLButtonElement);
  let nextIndex = currentIndex;
  if (event.key === "ArrowDown") nextIndex = currentIndex < 0 ? 0 : (currentIndex + 1) % options.length;
  else if (event.key === "ArrowUp") nextIndex = currentIndex <= 0 ? options.length - 1 : currentIndex - 1;
  else if (event.key === "Home") nextIndex = 0;
  else if (event.key === "End") nextIndex = options.length - 1;
  else if (event.key === "Escape") {
    event.preventDefault();
    closeChatDropdowns();
    (key === "workspace" ? workspaceDropdownTrigger.value : agentDropdownTrigger.value)?.focus();
    return;
  } else if (event.key === "Tab") {
    closeChatDropdowns();
    return;
  } else {
    return;
  }

  event.preventDefault();
  options[nextIndex]?.focus();
}

function focusDropdownOption(key: ChatDropdownKey, position: "first" | "last") {
  const menu = document.querySelector<HTMLElement>(`.chat-context-dropdown-menu[data-dropdown="${key}"]`);
  const options = menu?.querySelectorAll<HTMLButtonElement>('button[role="menuitemradio"]');
  if (!options?.length) return;
  options.item(position === "first" ? 0 : options.length - 1).focus();
}

async function runChatAction(action: () => Promise<unknown>, fallback: string) {
  actionError.value = "";
  try {
    return await action();
  } catch (error) {
    actionError.value = readableChatError(error, fallback);
    return undefined;
  }
}

function readableChatError(error: unknown, fallback: string) {
  // Prefer the API error body so VALIDATION_ERROR / FORBIDDEN / etc. are not
  // collapsed into a generic or stale auth message after interceptor rewrites.
  const apiError = toAPIError(error);
  if (apiError.message.trim()) {
    return apiError.message;
  }
  return fallback;
}

function statusLabel(status?: string) {
  switch (status) {
    case "WAITING_CONFIRMATION":
      return "等待授权确认";
    case "RUNNING":
      return "执行中";
    case "PENDING":
      return "队列中";
    case "SUCCEEDED":
      return "已完成";
    case "FAILED":
      return "失败";
    case "CANCELLED":
      return "已取消";
    default:
      return "待运行";
  }
}

function statusBadgeClass(status?: string) {
  switch (status) {
    case "WAITING_CONFIRMATION":
    case "PENDING":
      return "waiting";
    case "RUNNING":
      return "running";
    case "SUCCEEDED":
      return "completed";
    case "FAILED":
    case "CANCELLED":
      return "failed";
    default:
      return "";
  }
}

function timelineStatus(status: string) {
  switch (status.toLowerCase()) {
    case "completed":
    case "passed":
      return "completed";
    case "failed":
    case "blocked":
      return "failed";
    case "running":
      return "running";
    default:
      return "pending";
  }
}

function stepStatusIcon(status: string) {
  if (status === "completed") return "fa-solid fa-check";
  if (status === "running") return "fa-solid fa-spinner";
  if (status === "failed") return "fa-solid fa-xmark";
  return "fa-solid fa-lock";
}

function runStepLabel(type: string) {
  switch (type) {
    case "MODEL":
      return "意图分析";
    case "TOOL":
      return "工具调用";
    case "WORKFLOW":
      return "流程调用";
    case "CONFIRMATION":
      return "人工确认";
    case "ASSISTANT_MESSAGE":
      return "回复会话";
    case "CHECKPOINT":
      return "状态检查点";
    default:
      return type;
  }
}

function runStepSummary(step: AgentRunStep) {
  if (step.errorCode) return `错误代码：${step.errorCode}`;
  if (step.capabilityReleaseId) return `Capability Release：${step.capabilityReleaseId}`;
  return summarizeRuntimeValue(step.outputSummary) || summarizeRuntimeValue(step.inputSummary) || "运行步骤已持久化";
}

function summarizeRuntimeValue(value: unknown) {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value);
  } catch {
    return "";
  }
}

function capabilitySnapshotCount(value: unknown) {
  if (!value || typeof value !== "object") return 0;
  const snapshot = value as { capabilities?: unknown[]; releases?: unknown[] };
  return snapshot.capabilities?.length || snapshot.releases?.length || 0;
}

function runStepStatusLabel(status: string) {
  switch (timelineStatus(status)) {
    case "completed":
      return "完成";
    case "running":
      return "进行中";
    case "failed":
      return "失败";
    default:
      return "等待";
  }
}

function messageTime(createdAt?: string) {
  if (!createdAt) return "刚刚";
  const date = new Date(createdAt);
  if (Number.isNaN(date.getTime())) return createdAt;
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

function messageDateTimeTitle(createdAt?: string) {
  const date = parseMessageDate(createdAt);
  if (!date) return createdAt || "刚刚";
  return date.toLocaleString("zh-CN", {
    year: "numeric",
    month: "long",
    day: "numeric",
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function messageDateLabel(createdAt?: string) {
  const date = parseMessageDate(createdAt);
  if (!date) return "";
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (localDateKey(date) === localDateKey(today)) return "今天";
  if (localDateKey(date) === localDateKey(yesterday)) return "昨天";
  return date.toLocaleDateString("zh-CN", { year: date.getFullYear() === today.getFullYear() ? undefined : "numeric", month: "long", day: "numeric" });
}

function shouldShowMessageDate(index: number) {
  const currentKey = localDateKey(parseMessageDate(chat.messages[index]?.createdAt));
  if (!currentKey) return false;
  const previousKey = localDateKey(parseMessageDate(chat.messages[index - 1]?.createdAt));
  return currentKey !== previousKey;
}

function parseMessageDate(createdAt?: string) {
  if (!createdAt) return undefined;
  const date = new Date(createdAt);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function localDateKey(date?: Date) {
  if (!date) return "";
  return `${date.getFullYear()}-${date.getMonth() + 1}-${date.getDate()}`;
}

function sessionTime(updatedAt?: string) {
  return messageTime(updatedAt);
}
</script>

<template>
  <div class="orchestrator-console-page" v-loading="chat.loading" @click="closeChatDropdowns" @keydown.esc="closeSidePanel">
    <header class="orchestrator-console-topbar">
      <div class="orchestrator-topbar-leading">
        <button
          ref="sessionPanelTrigger"
          class="orchestrator-panel-trigger"
          type="button"
          aria-controls="chat-session-panel"
          :aria-expanded="activeSidePanel === 'sessions'"
          @click.stop="toggleSidePanel('sessions')"
        >
          <i class="fa-regular fa-clock" aria-hidden="true" />
          <span>历史会话</span>
        </button>

        <div class="orchestrator-context-shell">
          <div class="orchestrator-context-group">
            <span class="orchestrator-context-label">新会话配置</span>
            <div class="chat-context-dropdown" @click.stop>
              <button
                ref="workspaceDropdownTrigger"
                type="button"
                aria-label="选择新会话的业务空间"
                title="选择新会话的业务空间"
                aria-haspopup="menu"
                :aria-expanded="chatDropdowns.workspace"
                @click="toggleChatDropdown('workspace')"
                @keydown.esc.stop="closeChatDropdowns"
              >
                <i class="fa-solid fa-cubes" aria-hidden="true" />
                <span>{{ activeWorkspaceLabel }}</span>
                <i class="fa-solid fa-chevron-down" :class="{ open: chatDropdowns.workspace }" aria-hidden="true" />
              </button>
              <div
                v-if="chatDropdowns.workspace"
                class="chat-context-dropdown-menu"
                data-dropdown="workspace"
                role="menu"
                aria-label="选择新会话的业务空间"
                @keydown="handleDropdownMenuKeydown($event, 'workspace')"
              >
                <button
                  v-for="workspace in workspaces.items"
                  :key="workspace.id"
                  type="button"
                  role="menuitemradio"
                  tabindex="-1"
                  :class="{ selected: workspace.id === selectedWorkspaceId }"
                  :aria-checked="workspace.id === selectedWorkspaceId"
                  @click="selectWorkspaceOption(workspace)"
                >
                  <span>{{ workspace.displayName }}</span>
                  <small>{{ workspace.name }}</small>
                </button>
              </div>
            </div>

            <div class="chat-context-dropdown agent" @click.stop>
              <button
                ref="agentDropdownTrigger"
                type="button"
                aria-label="选择新会话的 Agent"
                title="选择新会话的 Agent"
                aria-haspopup="menu"
                :aria-expanded="chatDropdowns.agent"
                @click="toggleChatDropdown('agent')"
                @keydown.esc.stop="closeChatDropdowns"
              >
                <i class="fa-solid fa-robot" aria-hidden="true" />
                <span>{{ activeAgentLabel }}</span>
                <i class="fa-solid fa-chevron-down" :class="{ open: chatDropdowns.agent }" aria-hidden="true" />
              </button>
              <div
                v-if="chatDropdowns.agent"
                class="chat-context-dropdown-menu agent-menu"
                data-dropdown="agent"
                role="menu"
                aria-label="选择新会话的 Agent"
                @keydown="handleDropdownMenuKeydown($event, 'agent')"
              >
                <p>选择新会话的 Agent</p>
                <button
                  v-for="agent in filteredAgents"
                  :key="agent.id"
                  type="button"
                  role="menuitemradio"
                  tabindex="-1"
                  :class="{ selected: agent.id === selectedAgentId }"
                  :aria-checked="agent.id === selectedAgentId"
                  @click="selectAgentOption(agent)"
                >
                  <span>
                    <strong>{{ agent.name }}</strong>
                    <small>{{ agent.modelConfigId || "Eino Runtime" }}</small>
                  </span>
                  <i class="agent-online-dot" aria-hidden="true" />
                </button>
              </div>
            </div>
            <button
              class="orchestrator-panel-trigger context-new-session-button"
              :class="{ 'has-pending-context': contextSelectionDirty }"
              type="button"
              :disabled="contextLoading || !selectedWorkspaceId || !selectedAgentId"
              :aria-busy="contextLoading"
              :title="contextSelectionDirty ? '使用所选配置创建新会话' : '创建新会话'"
              @click="newSession"
            >
              <i class="fa-solid fa-plus" aria-hidden="true" />
              <span>{{ contextLoading ? "载入配置" : contextSelectionDirty ? "应用并新建" : "新建会话" }}</span>
            </button>
          </div>
        </div>
      </div>
      <button
        ref="runtimePanelTrigger"
        class="orchestrator-panel-trigger"
        type="button"
        aria-controls="chat-runtime-panel"
        :aria-expanded="activeSidePanel === 'runtime'"
        @click.stop="toggleSidePanel('runtime')"
      >
        <i class="fa-solid fa-list-check" aria-hidden="true" />
        <span>运行详情</span>
      </button>
    </header>

    <main class="chat-workbench">
      <section class="chat-conversation-panel">
        <div class="runtime-console-header">
          <div class="runtime-console-header-content">
            <div class="runtime-console-title">
              <div class="runtime-title-row">
                <h1>运行调试台</h1>
                <b :class="statusBadgeClass(chat.runStatus)">{{ runStatusLabel }}</b>
                <button
                  v-if="chat.activeSession?.status === 'ACTIVE'"
                  class="chat-inline-action"
                  type="button"
                  title="归档当前会话（消息会永久保留）"
                  @click="archiveCurrentSession"
                >
                  归档
                </button>
              </div>
              <p>
                <span>{{ chat.activeSession?.title || "新会话" }}</span>
                <i aria-hidden="true">/</i>
                <span>{{ activeSessionWorkspace?.displayName || activeSessionWorkspace?.name || "未选择空间" }}</span>
                <i aria-hidden="true">/</i>
                <span>{{ activeSessionAgent?.name || chat.activeSession?.agentId || activeAgentLabel }}</span>
                <b
                  v-if="activeSessionReadOnly"
                  class="runtime-agent-state"
                  :title="`会话创建时使用的 Agent 当前${activeSessionAgentStatusLabel}，仅保留历史记录`"
                >
                  {{ activeSessionAgentStatusLabel }}
                </b>
              </p>
            </div>
            <div class="runtime-summary-list" aria-label="当前运行摘要">
              <span><small>意图</small><strong>{{ runtimeIntentLabel }}</strong></span>
              <span><small>本轮能力</small><strong>{{ capabilityCountLabel }}</strong></span>
            </div>
          </div>
        </div>

        <section class="debug-console-banner" role="status" data-testid="debug-console-nonprod-banner">
          <i class="fa-solid fa-flask" aria-hidden="true" />
          <div>
            <strong>内部运行调试台（非生产）</strong>
            <p>用于配置验证与出站身份调试。业务 Token 不会写入会话正文、历史或本地存储；透传凭据通过下方独立绑定面板一次性提交。</p>
          </div>
        </section>
        <div class="debug-subject-bar" data-testid="debug-console-subject" aria-label="当前 Subject">
          <span><small>当前 Subject</small><strong>{{ currentSubjectLabel }}</strong></span>
          <span v-if="outboundAttachmentId"><small>出站绑定</small><strong>已绑定（下一条消息消费）</strong></span>
          <span v-else><small>出站绑定</small><strong>未绑定</strong></span>
        </div>

        <section v-if="activeSessionReadOnly" class="chat-session-agent-alert" role="status">
          <div class="chat-session-agent-alert-copy">
            <i class="fa-solid fa-user-slash" aria-hidden="true" />
            <div>
              <strong>关联 Agent {{ activeSessionAgentStatusLabel }}</strong>
              <p>“{{ activeSessionAgent?.name || chat.activeSession?.agentId }}”关联的会话仅可查看，不能继续执行；归档不会删除消息。</p>
            </div>
          </div>
          <button
            type="button"
            :disabled="contextLoading || !selectedWorkspaceId || !selectedAgentId"
            :aria-busy="contextLoading"
            @click="newSession"
          >
            <i class="fa-solid fa-plus" aria-hidden="true" />
            <span>{{ contextLoading ? "载入可用 Agent" : `使用 ${activeAgentLabel} 新建会话` }}</span>
          </button>
        </section>

        <div
          ref="chatScrollArea"
          class="chat-scroll-area"
          role="log"
          aria-label="对话消息"
          aria-live="polite"
          aria-relevant="additions text"
          :aria-busy="conversationBusy"
          @scroll.passive="handleConversationScroll"
        >
          <div v-if="!chat.messages.length" class="chat-empty-state">
            <span><i class="fa-solid fa-robot" aria-hidden="true" /></span>
            <strong>开始一次可审计的执行对话</strong>
            <p>描述目标即可。涉及敏感能力时，系统会在执行前请求你的确认。</p>
          </div>

          <template v-for="(message, messageIndex) in chat.messages" :key="message.id">
            <div v-if="shouldShowMessageDate(messageIndex)" class="message-date-separator" role="separator">
              <span>{{ messageDateLabel(message.createdAt) }}</span>
            </div>
            <article class="message-row" :class="message.role.toLowerCase()">
              <div v-if="message.role === 'ASSISTANT'" class="assistant-message-shell">
                <div class="message-avatar assistant" aria-hidden="true"><i class="fa-solid fa-robot" /></div>
                <div>
                  <div class="message-meta">
                    <strong>ActWeave Agent</strong>
                    <time :datetime="message.createdAt" :title="messageDateTimeTitle(message.createdAt)">{{ messageTime(message.createdAt) }}</time>
                  </div>
                  <div class="assistant-bubble" v-html="renderMessageMarkdown(message.content)" />
                </div>
              </div>

              <div v-else-if="message.role === 'USER'" class="user-message-shell">
                <div class="message-avatar user" aria-hidden="true">{{ activeUserInitials }}</div>
                <div>
                  <div class="message-meta user">
                    <time :datetime="message.createdAt" :title="messageDateTimeTitle(message.createdAt)">{{ messageTime(message.createdAt) }}</time>
                    <strong>{{ activeUserLabel }}</strong>
                  </div>
                  <div class="user-bubble">{{ message.content }}</div>
                </div>
              </div>
            </article>
          </template>

          <section v-if="chat.pendingConfirmation?.status === 'PENDING'" class="risk-gate-card">
            <div class="risk-gate-head">
              <div>
                <i class="fa-solid fa-shield-halved" aria-hidden="true" />
                <strong>高风险拦截：执行前安全确认</strong>
              </div>
              <span>MANUAL GATE</span>
            </div>
            <p>该请求将访问敏感能力或触发高风险动作。只有本次执行的原发起人可以确认或取消。</p>
            <ul>
              <li v-for="reason in chat.pendingConfirmation.riskReasons" :key="reason">
                <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
                <span>{{ reason }}</span>
              </li>
            </ul>
            <div class="risk-confirm-row">
              <span v-if="!chat.pendingResumeToken" role="status">确认凭据未在当前浏览器恢复，请重新发起该操作。</span>
              <button type="button" :disabled="confirming || activeSessionReadOnly" :aria-busy="confirming" @click="cancelConfirmation">
                <i class="fa-solid fa-ban" aria-hidden="true" />
                取消操作
              </button>
              <button
                type="button"
                :disabled="confirming || activeSessionReadOnly || !chat.pendingResumeToken"
                :aria-busy="confirming"
                @click="confirm"
              >
                <i class="fa-solid fa-unlock-keyhole" aria-hidden="true" />
                {{ confirming ? "授权中" : "授权执行" }}
              </button>
            </div>
          </section>
        </div>

        <button v-if="hasUnreadMessages" class="chat-new-message-button" type="button" @click="revealLatestMessages">
          <i class="fa-solid fa-arrow-down" aria-hidden="true" />
          <span>有新消息</span>
        </button>

        <div class="chat-composer-dock">
          <p v-if="actionError" id="chat-action-error" class="chat-action-error" role="alert" aria-live="assertive">{{ actionError }}</p>
          <div v-if="!chat.messages.length && !activeSessionReadOnly" class="prompt-suggestion-strip">
            <span>暂无快捷指令</span>
          </div>
          <div
            v-if="chat.activeSession && !activeSessionReadOnly"
            class="debug-outbound-dock"
            data-testid="debug-outbound-credential-panel"
          >
            <label v-if="requiresPassthroughToken && passthroughConnections.length > 1" class="debug-connection-picker">
              透传 Connection
              <select v-model="debugPassthroughConnectionId" aria-label="选择透传 Connection">
                <option v-for="c in passthroughConnections" :key="c.id" :value="c.id">{{ c.name }}</option>
              </select>
            </label>
            <DebugOutboundCredentialPanel
              ref="debugOutboundPanel"
              :workspace-id="chat.activeSession.workspaceId"
              :session-id="chat.activeSession.id"
              :requires-passthrough="requiresPassthroughToken"
              :connection-id="effectiveDebugConnectionId"
              :attach="attachDebugOutboundCredentials"
              @attachment="onOutboundAttachment"
            />
          </div>
          <div class="chat-input-shell" :class="{ 'is-read-only': activeSessionReadOnly }">
            <textarea
              v-model="composer"
              rows="1"
              aria-label="输入业务指令或目标任务"
              :placeholder="composerPlaceholder"
              :disabled="activeSessionReadOnly"
              :aria-describedby="actionError ? 'chat-composer-shortcut chat-action-error' : 'chat-composer-shortcut'"
              @keydown.enter.exact.prevent="send"
            />
            <span id="chat-composer-shortcut" class="chat-sr-only">按 Enter 发送，按 Shift 加 Enter 换行。</span>
            <button type="button" :disabled="sending || activeSessionReadOnly || !composer.trim()" :aria-busy="sending" @click="send">
              <i class="fa-solid" :class="sending ? 'fa-spinner' : 'fa-paper-plane'" aria-hidden="true" />
              <span>{{ sending ? "发送中" : "发送" }}</span>
            </button>
          </div>
        </div>
      </section>

      <Transition name="chat-panel-fade">
        <div v-if="activeSidePanel === 'sessions'" class="chat-side-panel-backdrop session" @click.self="closeSidePanel">
          <aside
            id="chat-session-panel"
            class="chat-session-rail chat-side-panel"
            role="dialog"
            aria-modal="true"
            aria-label="历史会话"
            tabindex="-1"
            @keydown.tab="trapSidePanelFocus"
          >
            <div class="chat-session-head">
              <div>
                <span>历史会话</span>
                <h2>执行记录</h2>
              </div>
              <div class="chat-panel-head-actions">
                <button class="chat-panel-icon-button primary" type="button" title="创建新会话" aria-label="创建新会话" @click="newSession">
                  <i class="fa-solid fa-plus" aria-hidden="true" />
                </button>
                <button
                  ref="sessionPanelCloseButton"
                  class="chat-panel-icon-button"
                  type="button"
                  title="关闭历史会话"
                  aria-label="关闭历史会话"
                  @click="closeSidePanel"
                >
                  <i class="fa-solid fa-xmark" aria-hidden="true" />
                </button>
              </div>
            </div>
            <label class="chat-session-search">
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <input v-model="sessionKeyword" type="text" placeholder="筛选会话..." />
            </label>

            <div class="chat-session-list">
              <button
                v-for="session in filteredSessions"
                :key="session.id"
                class="chat-session-card"
                :class="{ active: session.id === chat.activeSessionId }"
                type="button"
                @click="selectSession(session.id)"
              >
                <strong :title="session.title">{{ session.title }}</strong>
                <span class="chat-session-meta">
                  <small class="chat-session-agent-name">
                    <span>{{ sessionAgentName(session) }}</span>
                    <b v-if="session.status === 'ARCHIVED' || !agents.items.some((agent) => agent.id === session.agentId)">
                      {{ session.status === "ARCHIVED" ? "已归档" : "不可用" }}
                    </b>
                  </small>
                  <small>{{ sessionTime(session.updatedAt) }}</small>
                </span>
              </button>

              <div v-if="!filteredSessions.length" class="chat-session-empty">
                <i class="fa-regular fa-message" aria-hidden="true" />
                <strong>没有匹配的会话</strong>
                <small>调整关键词，或新建一个执行会话。</small>
              </div>
            </div>
          </aside>
        </div>
      </Transition>

      <Transition name="chat-panel-fade">
        <div v-if="activeSidePanel === 'runtime'" class="chat-side-panel-backdrop runtime" @click.self="closeSidePanel">
          <aside
            id="chat-runtime-panel"
            class="runtime-monitor-panel chat-side-panel"
            role="dialog"
            aria-modal="true"
            aria-label="运行详情"
            tabindex="-1"
            @keydown.tab="trapSidePanelFocus"
          >
            <div class="runtime-monitor-head">
              <div>
                <span>运行详情</span>
                <h2>{{ chat.latestRun ? "Run 与执行链路" : "等待会话运行" }}</h2>
              </div>
              <button
                ref="runtimePanelCloseButton"
                class="chat-panel-icon-button"
                type="button"
                title="关闭运行详情"
                aria-label="关闭运行详情"
                @click="closeSidePanel"
              >
                <i class="fa-solid fa-xmark" aria-hidden="true" />
              </button>
            </div>

            <div class="runtime-monitor-body">
              <section class="runtime-decision-card">
                <div>
                  <span>决策目标</span>
                  <small>{{ chat.latestRun?.triggerType || "待识别" }}</small>
                </div>
                <strong>{{ runtimeTargetReleaseId || "尚未匹配 Capability Release" }}</strong>
                <p>{{ runtimeSummary }}</p>
                <div class="runtime-decision-meta">
                  <span>风险等级</span>
                  <b>{{ chat.pendingConfirmation?.riskLevel || "标准" }}</b>
                </div>
              </section>

              <section class="runtime-step-section">
                <div class="runtime-section-title">
                  <span>执行链路</span>
                  <small>{{ latestRunStepRows.length }} 个事件</small>
                </div>
                <div v-if="latestRunStepRows.length" class="runtime-step-list">
                  <div v-for="step in latestRunStepRows" :key="step.id" class="runtime-step-row">
                    <i :class="[stepStatusIcon(step.status), step.status]" aria-hidden="true" />
                    <span>
                      <strong :title="step.name">{{ step.name }}</strong>
                      <small :title="step.summary">{{ step.summary }}</small>
                    </span>
                    <b>{{ step.statusLabel }}</b>
                  </div>
                </div>
                <div v-else class="runtime-empty-note">
                  <strong>暂无执行事件</strong>
                  <small>发送指令后，这里会按顺序展示意图分析、能力调用和确认节点。</small>
                </div>
              </section>

              <details class="runtime-policy-card" :open="chat.pendingConfirmation?.status === 'PENDING'">
                <summary>
                  <span><i class="fa-solid fa-shield-halved" aria-hidden="true" />安全策略</span>
                  <span class="runtime-disclosure-state">
                    <small v-if="chat.pendingConfirmation?.status === 'PENDING'">需要确认</small>
                    <i class="fa-solid fa-angle-down" aria-hidden="true" />
                  </span>
                </summary>
                <div class="runtime-policy-detail">
                  <i class="fa-solid fa-shield-halved" aria-hidden="true" />
                  <span>
                    <strong>敏感能力访问</strong>
                    <small>涉及高风险动作时需要人工授权并写入审计链路。</small>
                  </span>
                </div>
              </details>

              <details class="runtime-trace-section">
                <summary>
                  <span><i class="fa-solid fa-bug" aria-hidden="true" />技术信息与 Trace</span>
                  <i class="fa-solid fa-angle-down" aria-hidden="true" />
                </summary>
                <div class="runtime-trace-block">
                  <p><span>Trace ID:</span><b>{{ chat.latestRun?.traceId || chat.latestExecution?.traceId || "-" }}</b></p>
                  <p><span>Run ID:</span><b>{{ chat.latestRunId || "-" }}</b></p>
                  <p><span>Target ID:</span><b>{{ runtimeTargetReleaseId || "-" }}</b></p>
                  <p><span>Runtime:</span><b>Eino Runtime Engine</b></p>
                  <p><span>Workspace:</span><b>{{ activeWorkspaceLabel }}</b></p>
                  <p><span>Capabilities:</span><b>{{ capabilityCount }}</b></p>
                </div>
              </details>
            </div>
          </aside>
        </div>
      </Transition>
    </main>
  </div>
</template>

<style scoped>
.orchestrator-console-page {
  --chat-content-width: 800px;
  display: flex;
  height: 100%;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--aw-border);
  border-radius: 0;
  background: #f6f8fb;
  color: var(--aw-text);
  font-family: Inter, "Noto Sans SC", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}

.orchestrator-console-topbar {
  position: relative;
  z-index: 20;
  display: flex;
  min-height: 52px;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 20px;
  border-bottom: 1px solid var(--aw-border-soft);
  background: #fff;
}

.orchestrator-topbar-leading {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
}

.orchestrator-panel-trigger {
  display: inline-flex;
  min-height: 36px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 6px 10px;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #fff;
  color: #475569;
  font-size: 11px;
  font-weight: 700;
  line-height: 14px;
  transition: border-color 0.16s ease, background-color 0.16s ease, color 0.16s ease;
}

.orchestrator-panel-trigger:hover,
.orchestrator-panel-trigger[aria-expanded="true"] {
  border-color: rgba(13, 148, 136, 0.32);
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
}

.orchestrator-panel-trigger i {
  font-size: 10px;
}

.orchestrator-context-group,
.prompt-suggestion-strip,
.chat-input-shell,
.risk-confirm-row {
  display: flex;
  align-items: center;
}

.orchestrator-context-group {
  min-width: 0;
  gap: 6px;
}

.orchestrator-context-label {
  flex-shrink: 0;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  line-height: 14px;
}

.orchestrator-context-shell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
}

.chat-context-dropdown {
  position: relative;
  flex-shrink: 0;
}

.chat-context-dropdown > button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  max-width: 208px;
  padding: 6px 10px;
  border: 1px solid rgba(226, 232, 240, 0.8);
  border-radius: 8px;
  background: #fff;
  color: #334155;
  font-size: 11px;
  font-weight: 500;
  line-height: 14px;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.chat-context-dropdown.agent > button {
  background: #f8fafc;
}

.chat-context-dropdown > button:hover {
  background: #f1f5f9;
  color: #0f172a;
}

.context-new-session-button.has-pending-context {
  border-color: rgba(13, 148, 136, 0.32);
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
}

.context-new-session-button:disabled {
  cursor: wait;
  opacity: 0.62;
}

.chat-context-dropdown > button span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-context-dropdown > button i:first-child {
  color: #10b981;
}

.chat-context-dropdown > button i:last-child {
  color: #64748b;
  font-size: 9px;
  transition: transform 0.2s ease;
}

.chat-context-dropdown > button i.open {
  transform: rotate(180deg);
}

.chat-context-dropdown-menu {
  position: absolute;
  top: calc(100% + 8px);
  left: 0;
  z-index: 60;
  width: 256px;
  padding: 4px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.05);
}

.chat-context-dropdown-menu.agent-menu {
  width: 288px;
}

.chat-context-dropdown-menu p {
  margin: 0;
  padding: 8px 12px;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.chat-context-dropdown-menu button {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 44px;
  padding: 8px 12px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #475569;
  font-size: 12px;
  line-height: 16px;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.chat-context-dropdown-menu button:hover,
.chat-context-dropdown-menu button.selected {
  background: rgba(236, 253, 245, 0.6);
  color: #047857;
}

.chat-context-dropdown-menu button.selected {
  border-left: 2px solid #10b981;
  font-weight: 600;
}

.chat-context-dropdown-menu button small {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 9px;
  line-height: 12px;
}

.agent-online-dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: #10b981;
}

.chat-workbench {
  position: relative;
  display: flex;
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.chat-side-panel-backdrop {
  position: absolute;
  inset: 0;
  z-index: 30;
  display: flex;
  background: rgba(15, 23, 42, 0.14);
  backdrop-filter: blur(1px);
}

.chat-side-panel-backdrop.runtime {
  justify-content: flex-end;
}

.chat-side-panel {
  height: 100%;
  box-shadow: 0 18px 48px rgba(15, 23, 42, 0.18);
}

.chat-panel-fade-enter-active,
.chat-panel-fade-leave-active {
  transition: opacity 0.18s ease;
}

.chat-panel-fade-enter-active .chat-side-panel,
.chat-panel-fade-leave-active .chat-side-panel {
  transition: transform 0.18s ease;
}

.chat-panel-fade-enter-from,
.chat-panel-fade-leave-to {
  opacity: 0;
}

.chat-panel-fade-enter-from.session .chat-side-panel,
.chat-panel-fade-leave-to.session .chat-side-panel {
  transform: translateX(-16px);
}

.chat-panel-fade-enter-from.runtime .chat-side-panel,
.chat-panel-fade-leave-to.runtime .chat-side-panel {
  transform: translateX(16px);
}

.chat-session-rail {
  display: flex;
  width: min(340px, 88%);
  flex-shrink: 0;
  flex-direction: column;
  border-right: 1px solid var(--aw-border);
  background: #fff;
}

.chat-session-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 68px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--aw-border-soft);
}

.chat-session-head > div:first-child > span,
.runtime-monitor-head > div > span {
  display: block;
  color: #047857;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.chat-session-head h2,
.runtime-monitor-head h2 {
  margin: 2px 0 0;
  color: #0f172a;
  font-size: 14px;
  font-weight: 700;
  line-height: 20px;
}

.chat-panel-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.chat-panel-icon-button {
  display: flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #fff;
  color: #64748b;
  cursor: pointer;
  transition: background-color 0.16s ease, border-color 0.16s ease, color 0.16s ease;
}

.chat-panel-icon-button:hover {
  border-color: rgba(13, 148, 136, 0.3);
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
}

.chat-panel-icon-button.primary {
  border-color: #0f172a;
  background: #0f172a;
  color: #fff;
}

.chat-panel-icon-button.primary:hover {
  background: #1e293b;
}

.chat-session-search {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  margin: 12px 14px;
  padding: 8px 12px;
  border: 1px solid rgba(226, 232, 240, 0.8);
  border-radius: 8px;
  background: #f8fafc;
  color: #64748b;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;
}

.chat-session-search:focus-within {
  border-color: #10b981;
  background: #fff;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.1);
}

.chat-session-search input {
  width: 100%;
  border: 0;
  outline: none;
  background: transparent;
  color: #334155;
  font-size: 12px;
  line-height: 16px;
}

.chat-session-search input::placeholder {
  color: #64748b;
}

.chat-session-list {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 6px;
  overflow-y: auto;
  padding: 0 10px 12px;
}

.chat-session-card {
  width: 100%;
  min-height: 68px;
  padding: 11px 12px;
  border: 1px solid transparent;
  border-radius: 10px;
  background: #fff;
  color: #334155;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease;
}

.chat-session-card:hover {
  border-color: var(--aw-border);
  background: #f8fafc;
}

.chat-session-card.active {
  border-color: rgba(13, 148, 136, 0.26);
  background: var(--aw-cyan-soft);
  box-shadow: inset 3px 0 0 var(--aw-cyan);
}

.chat-session-card strong {
  display: block;
  overflow: hidden;
  min-width: 0;
  color: var(--aw-text);
  font-size: 13px;
  font-weight: 700;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-session-card small {
  display: block;
  color: #64748b;
  font-size: 10px;
  line-height: 14px;
}

.chat-session-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 5px;
  color: #64748b;
}

.chat-session-meta small:last-child {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.chat-session-agent-name {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
}

.chat-session-agent-name > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-session-agent-name > b,
.runtime-agent-state {
  flex-shrink: 0;
  padding: 1px 5px;
  border: 1px solid #fed7aa;
  border-radius: 999px;
  background: #fff7ed;
  color: #c2410c;
  font-size: 9px;
  font-weight: 700;
  line-height: 12px;
}

.chat-session-empty {
  display: flex;
  min-height: 180px;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 6px;
  padding: 24px 16px;
  color: #94a3b8;
  text-align: center;
}

.chat-session-empty > i {
  margin-bottom: 4px;
  font-size: 22px;
}

.chat-session-empty strong {
  color: #475569;
  font-size: 12px;
  line-height: 18px;
}

.chat-session-empty small {
  max-width: 164px;
  font-size: 10px;
  line-height: 16px;
}

.chat-conversation-panel {
  position: relative;
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  background: #f8fafc;
}

.runtime-console-header {
  flex-shrink: 0;
  min-height: 56px;
  padding: 6px clamp(20px, 4vw, 48px);
  border-bottom: 1px solid var(--aw-border);
  background: #fff;
}

.runtime-console-header-content {
  display: flex;
  width: min(100%, var(--chat-content-width));
  min-height: 44px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 auto;
}

.runtime-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.runtime-title-row > b,
.runtime-monitor-head b {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 6px;
  border: 1px solid #d1fae5;
  background: #ecfdf5;
  color: #047857;
  font-size: 10px;
  font-weight: 700;
  line-height: 13px;
}

.runtime-title-row > b,
.runtime-monitor-head b {
  border-color: #e2e8f0;
  background: #f8fafc;
  color: #475569;
}

.runtime-title-row > b.waiting,
.runtime-monitor-head b.waiting {
  border-color: #fde68a;
  background: #fffbeb;
  color: #b45309;
}

.runtime-title-row > b.running,
.runtime-monitor-head b.running {
  border-color: #bfdbfe;
  background: #eff6ff;
  color: #1d4ed8;
}

.runtime-title-row > b.completed,
.runtime-monitor-head b.completed {
  border-color: #a7f3d0;
  background: #ecfdf5;
  color: #047857;
}

.runtime-title-row > b.failed,
.runtime-monitor-head b.failed {
  border-color: #fecdd3;
  background: #fff1f2;
  color: #be123c;
}

.runtime-console-header h1 {
  margin: 0;
  color: #0f172a;
  font-size: 16px;
  font-weight: 700;
  line-height: 22px;
}

.runtime-console-header p {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 1px 0 0;
  max-width: 620px;
  overflow: hidden;
  color: #64748b;
  font-size: 11px;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-console-header p i {
  color: #cbd5e1;
  font-style: normal;
}

.chat-session-agent-alert {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px max(clamp(20px, 4vw, 48px), calc((100% - var(--chat-content-width)) / 2));
  border-bottom: 1px solid #fed7aa;
  background: #fff7ed;
}

.chat-session-agent-alert-copy {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.chat-session-agent-alert-copy > i {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 9px;
  background: #ffedd5;
  color: #c2410c;
}

.chat-session-agent-alert-copy strong {
  display: block;
  color: #7c2d12;
  font-size: 12px;
  line-height: 16px;
}

.chat-session-agent-alert-copy p {
  margin: 2px 0 0;
  color: #9a3412;
  font-size: 11px;
  line-height: 16px;
}

.chat-session-agent-alert > button {
  display: inline-flex;
  min-height: 36px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 6px 10px;
  border: 1px solid rgba(13, 148, 136, 0.28);
  border-radius: 8px;
  background: #fff;
  color: #047857;
  font-size: 11px;
  font-weight: 700;
  line-height: 16px;
  cursor: pointer;
}

.chat-session-agent-alert > button:hover:not(:disabled) {
  background: var(--aw-cyan-soft);
}

.chat-session-agent-alert > button:disabled {
  cursor: wait;
  opacity: 0.62;
}

.runtime-summary-list {
  display: flex;
  flex-shrink: 0;
  align-items: stretch;
  gap: 6px;
}

.runtime-summary-list span {
  min-width: 78px;
  padding: 3px 8px;
  border-left: 1px solid var(--aw-border);
}

.runtime-summary-list small {
  display: block;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 12px;
  text-transform: uppercase;
}

.runtime-summary-list strong {
  display: block;
  overflow: hidden;
  margin-top: 3px;
  color: #1e293b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  font-weight: 700;
  line-height: 15px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-scroll-area {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 24px clamp(20px, 4vw, 48px);
  padding-bottom: 48px;
  scrollbar-gutter: stable;
}

.chat-scroll-area > * {
  width: min(100%, var(--chat-content-width));
  margin-right: auto;
  margin-left: auto;
}

.message-date-separator {
  display: flex;
  align-items: center;
  gap: 12px;
  color: #64748b;
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
}

.message-date-separator::before,
.message-date-separator::after {
  height: 1px;
  flex: 1;
  background: #e2e8f0;
  content: "";
}

.message-date-separator span {
  flex-shrink: 0;
  padding: 0 4px;
}

.chat-empty-state {
  display: flex;
  min-height: 100%;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  color: var(--aw-muted);
  text-align: center;
}

.chat-empty-state > span {
  display: inline-flex;
  width: 48px;
  height: 48px;
  align-items: center;
  justify-content: center;
  margin-bottom: 14px;
  border: 1px solid rgba(13, 148, 136, 0.18);
  border-radius: 14px;
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
}

.chat-empty-state strong {
  color: #334155;
  font-size: 14px;
  line-height: 20px;
}

.chat-empty-state p {
  max-width: 420px;
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 20px;
}

.message-row {
  animation: messageIn 0.2s ease-out;
}

.message-row.user + .message-row.assistant {
  margin-top: -8px;
}

.message-row.assistant + .message-row.user {
  margin-top: 12px;
}

@keyframes messageIn {
  from {
    opacity: 0;
    transform: translateY(8px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.assistant-message-shell,
.user-message-shell {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.assistant-message-shell {
  max-width: 740px;
}

.user-message-shell {
  max-width: 560px;
  margin-left: auto;
  flex-direction: row-reverse;
}

.message-avatar {
  display: flex;
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
}

.message-avatar.assistant {
  background: #047857;
  box-shadow: 0 1px 2px rgba(5, 150, 105, 0.1);
}

.message-avatar.user {
  background: #020617;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.message-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.message-meta.user {
  justify-content: flex-end;
}

.message-meta strong {
  color: #334155;
  font-size: 12px;
  font-weight: 700;
  line-height: 16px;
}

.message-meta time {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 16px;
}

.assistant-bubble,
.user-bubble {
  padding: 14px 16px;
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  font-size: 14px;
  line-height: 22px;
}

.assistant-bubble {
  border: 1px solid #e2e8f0;
  border-top-left-radius: 6px;
  background: #fff;
  color: #334155;
}

.user-bubble {
  border-top-right-radius: 6px;
  background: #0f172a;
  color: #f1f5f9;
}

.assistant-message-shell small {
  display: block;
  margin-top: 6px;
  color: #64748b;
  font-size: 10px;
  line-height: 14px;
}

.risk-gate-card {
  max-width: 760px;
  padding: 16px;
  border: 1px solid #fde68a;
  border-radius: 12px;
  background: #fffbeb;
  box-shadow: 0 1px 2px rgba(146, 64, 14, 0.05);
}

.risk-gate-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.risk-gate-head > div {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #92400e;
}

.risk-gate-head strong {
  font-size: 12px;
  line-height: 16px;
}

.risk-gate-head span {
  padding: 2px 8px;
  border: 1px solid #fde68a;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.8);
  color: #b45309;
  font-size: 9px;
  font-weight: 700;
  line-height: 12px;
}

.risk-gate-card p,
.risk-gate-card li {
  color: rgba(146, 64, 14, 0.82);
  font-size: 11px;
  line-height: 18px;
}

.risk-gate-card ul {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 12px 0 0;
  padding: 0;
  list-style: none;
}

.risk-gate-card li {
  display: flex;
  gap: 8px;
}

.risk-confirm-row {
  gap: 8px;
  margin-top: 16px;
}

.risk-confirm-row input {
  flex: 1;
  min-height: 44px;
  padding: 8px 12px;
  border: 1px solid #fcd34d;
  border-radius: 12px;
  outline: none;
  background: #fff;
  color: #334155;
  font-size: 12px;
  line-height: 16px;
}

.risk-confirm-row input:focus {
  border-color: #f59e0b;
  box-shadow: 0 0 0 2px rgba(245, 158, 11, 0.2);
}

.risk-confirm-row input:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

.risk-confirm-row button,
.chat-input-shell button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 0;
  border-radius: 12px;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  line-height: 16px;
  cursor: pointer;
  transition: background-color 0.2s ease;
}

.risk-confirm-row button {
  min-height: 44px;
  padding: 8px 16px;
  background: #d97706;
}

.risk-confirm-row button:hover:not(:disabled) {
  background: #b45309;
}

.risk-confirm-row button:disabled,
.chat-input-shell button:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

.chat-composer-dock {
  flex-shrink: 0;
  padding: 10px clamp(20px, 4vw, 48px) 14px;
  border-top: 1px solid #e2e8f0;
  background: #fff;
}

.chat-new-message-button {
  position: absolute;
  bottom: 86px;
  left: 50%;
  z-index: 8;
  display: inline-flex;
  min-height: 36px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 6px 12px;
  transform: translateX(-50%);
  border: 1px solid rgba(13, 148, 136, 0.28);
  border-radius: 999px;
  background: #fff;
  color: #047857;
  box-shadow: 0 8px 20px rgba(15, 23, 42, 0.12);
  font-size: 11px;
  font-weight: 700;
  line-height: 16px;
  cursor: pointer;
}

.chat-new-message-button:hover {
  background: var(--aw-cyan-soft);
}

.chat-composer-dock > * {
  width: min(100%, var(--chat-content-width));
  margin-right: auto;
  margin-left: auto;
}

.chat-action-error {
  margin: 0 auto 10px;
  padding: 10px 12px;
  border: 1px solid #fecaca;
  border-radius: 12px;
  background: #fef2f2;
  color: #991b1b;
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
}

.prompt-suggestion-strip {
  gap: 8px;
  flex-wrap: nowrap;
  overflow-x: auto;
  padding-bottom: 8px;
  scrollbar-width: none;
}

.prompt-suggestion-strip::-webkit-scrollbar {
  display: none;
}

.prompt-suggestion-strip > span {
  flex-shrink: 0;
  margin-right: 4px;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.prompt-suggestion-strip button {
  flex-shrink: 0;
  min-height: 40px;
  max-width: 260px;
  overflow: hidden;
  padding: 6px 12px;
  border: 1px solid rgba(226, 232, 240, 0.7);
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
  font-size: 10px;
  font-weight: 500;
  line-height: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.prompt-suggestion-strip button:hover {
  background: #f1f5f9;
  color: #1e293b;
}

.chat-input-shell {
  gap: 8px;
  padding: 6px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #f8fafc;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.chat-input-shell:focus-within {
  border-color: #10b981;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.1);
}

.chat-input-shell.is-read-only {
  border-color: #e2e8f0;
  background: #f1f5f9;
  box-shadow: none;
}

.chat-input-shell textarea {
  flex: 1;
  max-height: 112px;
  min-height: 44px;
  resize: none;
  border: 0;
  outline: none;
  background: transparent;
  color: #1e293b;
  font-family: inherit;
  font-size: 13px;
  line-height: 20px;
  padding: 10px;
}

.chat-input-shell textarea::placeholder {
  color: #64748b;
}

.chat-input-shell textarea:disabled {
  cursor: not-allowed;
  color: #64748b;
}

.chat-input-shell button {
  min-height: 44px;
  padding: 10px 16px;
  border-radius: 8px;
  background: var(--aw-cyan);
  box-shadow: 0 1px 2px rgba(5, 150, 105, 0.1);
}

.chat-input-shell button:hover:not(:disabled) {
  background: #0f766e;
}

.chat-sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  clip-path: inset(50%);
  white-space: nowrap;
}

.runtime-monitor-panel {
  display: flex;
  width: min(400px, 92%);
  flex-shrink: 0;
  flex-direction: column;
  border-left: 1px solid var(--aw-border);
  background: #fff;
}

.runtime-monitor-head {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 68px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--aw-border-soft);
}

.runtime-monitor-head > div {
  min-width: 0;
}

.runtime-monitor-body {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 14px;
  scrollbar-gutter: stable;
}

.runtime-decision-card {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
  padding: 14px;
}

.runtime-decision-card > div:first-child,
.runtime-decision-card > div:last-child,
.runtime-section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.runtime-decision-card > div:first-child span {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.runtime-decision-card > div:first-child small,
.runtime-section-title small {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 9px;
  line-height: 12px;
}

.runtime-decision-card > strong {
  display: block;
  margin-top: 12px;
  color: #0f172a;
  font-size: 12px;
  font-weight: 700;
  line-height: 16px;
}

.runtime-decision-card p {
  margin: 8px 0 0;
  color: #64748b;
  font-size: 10px;
  line-height: 16px;
}

.runtime-decision-card > .runtime-decision-meta {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid var(--aw-border-soft);
}

.runtime-decision-card > .runtime-decision-meta span {
  color: #64748b;
  font-size: 10px;
  font-weight: 600;
  line-height: 14px;
}

.runtime-decision-card small {
  display: block;
  color: #64748b;
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 12px;
  text-transform: uppercase;
}

.runtime-decision-card b {
  display: inline;
  margin: 0;
  color: #334155;
  font-size: 10px;
  font-weight: 700;
  line-height: 14px;
}

.runtime-step-section,
.runtime-policy-card,
.runtime-trace-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.runtime-section-title span {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.runtime-step-list {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-left: 4px;
}

.runtime-step-list::before {
  position: absolute;
  top: 8px;
  bottom: 8px;
  left: 13px;
  width: 1px;
  background: #f1f5f9;
  content: "";
}

.runtime-step-row {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  font-size: 11px;
}

.runtime-step-row > i {
  display: flex;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: #94a3b8;
  color: #fff;
  font-size: 8px;
}

.runtime-step-row > i.completed {
  background: #10b981;
}

.runtime-step-row > i.running {
  background: #3b82f6;
}

.runtime-step-row > i.failed {
  background: #e11d48;
}

.runtime-step-row span {
  min-width: 0;
  flex: 1;
}

.runtime-step-row strong {
  display: block;
  overflow: hidden;
  color: #1e293b;
  font-size: 10px;
  font-weight: 600;
  line-height: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-step-row span small,
.runtime-empty-note small {
  display: block;
  margin-top: 2px;
  color: #64748b;
  font-size: 10px;
  line-height: 14px;
}

.runtime-step-row > b {
  flex-shrink: 0;
  color: #64748b;
  font-size: 9px;
  font-weight: 600;
  line-height: 14px;
}

.runtime-empty-note {
  padding: 12px;
  border: 1px solid #f1f5f9;
  border-radius: 8px;
  background: #f8fafc;
}

.runtime-empty-note strong {
  display: block;
  color: #334155;
  font-size: 12px;
  line-height: 16px;
}

.runtime-policy-card,
.runtime-trace-section {
  border-top: 1px solid var(--aw-border-soft);
  padding-top: 12px;
}

.runtime-policy-card summary,
.runtime-trace-section summary {
  display: flex;
  min-height: 40px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px;
  border-radius: 8px;
  color: var(--aw-muted);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  list-style: none;
  text-transform: uppercase;
  cursor: pointer;
}

.runtime-policy-card summary::-webkit-details-marker,
.runtime-trace-section summary::-webkit-details-marker {
  display: none;
}

.runtime-policy-card summary:hover,
.runtime-trace-section summary:hover {
  background: #f8fafc;
  color: #334155;
}

.runtime-policy-card summary:focus-visible,
.runtime-trace-section summary:focus-visible {
  outline: 2px solid rgba(13, 148, 136, 0.46);
  outline-offset: 2px;
}

.runtime-policy-card summary span,
.runtime-trace-section summary span {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}

.runtime-policy-card summary small {
  padding: 2px 7px;
  border: 1px solid #fde68a;
  border-radius: 999px;
  background: #fffbeb;
  color: #b45309;
  font-size: 9px;
  letter-spacing: 0;
  line-height: 12px;
}

.runtime-disclosure-state {
  margin-left: auto;
}

.runtime-policy-card summary .runtime-disclosure-state > i,
.runtime-trace-section summary > i {
  transition: transform 0.16s ease;
}

.runtime-policy-card[open] summary .runtime-disclosure-state > i,
.runtime-trace-section[open] summary > i {
  transform: rotate(180deg);
}

.runtime-policy-detail {
  display: flex;
  gap: 12px;
  padding: 12px;
  border: 1px solid #fde68a;
  border-radius: 8px;
  border-color: #fde68a;
  background: rgba(255, 251, 235, 0.6);
}

.runtime-policy-detail > i {
  display: flex;
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: #f59e0b;
  color: #fff;
  font-size: 12px;
}

.runtime-policy-detail strong {
  display: block;
  color: #451a03;
  font-size: 12px;
  line-height: 16px;
}

.runtime-policy-detail small {
  display: block;
  margin-top: 4px;
  color: rgba(146, 64, 14, 0.82);
  font-size: 10px;
  line-height: 16px;
}

.runtime-trace-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px;
  border-radius: 8px;
  background: #020617;
  color: #cbd5e1;
  box-shadow: inset 0 2px 4px rgba(0, 0, 0, 0.12);
}

.runtime-trace-block p {
  margin: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 9px;
  line-height: 14px;
}

.runtime-trace-block span {
  color: #64748b;
}

.runtime-trace-block b {
  color: #34d399;
  font-weight: 500;
}

@media (max-width: 900px) {
  .orchestrator-console-topbar {
    height: auto;
    min-height: 64px;
    flex-wrap: wrap;
    align-items: flex-start;
    padding: 12px 16px;
  }

  .orchestrator-topbar-leading,
  .orchestrator-context-shell,
  .orchestrator-context-group {
    flex-wrap: wrap;
  }

  .orchestrator-context-label {
    width: 100%;
  }

  .runtime-console-header-content {
    flex-direction: column;
    align-items: stretch;
    gap: 16px;
  }

  .runtime-console-header p {
    max-width: none;
    white-space: normal;
  }

  .runtime-summary-list {
    width: 100%;
  }

  .runtime-summary-list span {
    flex: 1;
  }

  .chat-session-agent-alert {
    align-items: stretch;
    flex-direction: column;
    padding-right: 16px;
    padding-left: 16px;
  }

  .chat-session-agent-alert > button {
    align-self: flex-start;
  }

  .chat-scroll-area {
    padding: 20px 16px 44px;
  }

  .runtime-console-header,
  .chat-composer-dock {
    padding-right: 16px;
    padding-left: 16px;
  }
}

@media (max-width: 480px) {
  .chat-composer-dock {
    padding: 10px 12px 12px;
  }

  .prompt-suggestion-strip button {
    max-width: 240px;
  }

  .chat-input-shell {
    align-items: stretch;
  }
}

.debug-console-banner {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  margin: 0 clamp(20px, 4vw, 48px);
  padding: 10px 12px;
  border: 1px solid #fde68a;
  border-radius: 10px;
  background: #fffbeb;
  color: #92400e;
}

.debug-console-banner > i {
  margin-top: 2px;
  color: #d97706;
}

.debug-console-banner strong {
  display: block;
  font-size: 12px;
}

.debug-console-banner p {
  margin: 2px 0 0;
  font-size: 11px;
  line-height: 16px;
  color: #a16207;
}

.debug-subject-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 20px;
  margin: 8px clamp(20px, 4vw, 48px) 0;
  padding: 8px 12px;
  border: 1px solid var(--aw-border, #e2e8f0);
  border-radius: 8px;
  background: #fff;
}

.debug-subject-bar span {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.debug-subject-bar small {
  color: #94a3b8;
  font-size: 10px;
}

.debug-subject-bar strong {
  font-size: 12px;
  color: #0f172a;
}

.debug-outbound-dock {
  margin-bottom: 8px;
}

.debug-connection-picker {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 8px;
  font-size: 12px;
  color: #475569;
}

.debug-connection-picker select {
  max-width: 320px;
  padding: 6px 8px;
}
</style>
