/**
 * Chat execution page model (ZKL-64 item 15).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { toAPIError } from "../services/api";
import { useAgentStore } from "../stores/agents";
import { useAuthStore } from "../stores/auth";
import { useChatStore } from "../stores/chat";
import { useConnectionsStore } from "../stores/connections";
import { useToolsStore } from "../stores/tools";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Agent,
  AgentRunStep,
  OutboundCredentialsEnvelope,
  Workspace,
  WorkspaceChatSession,
} from "../types/domain";
import { renderMarkdown } from "../utils/markdown";

export function createChatExecutionPageModel() {
  type ChatDropdownKey = "workspace" | "agent";
  type ChatSidePanel = "sessions" | "runtime";

  const workspaces = useWorkspaceStore();
  const agents = useAgentStore();
  const auth = useAuthStore();
  const chat = useChatStore();
  const connectionsStore = useConnectionsStore();
  const toolsStore = useToolsStore();
  const composer = ref("");
  const selectedWorkspaceId = ref("");
  const selectedAgentId = ref("");
  const sessionKeyword = ref("");
  const sending = ref(false);
  const confirming = ref(false);
  const archivingSession = ref(false);
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
  const debugOutboundPanel = ref<{
    clearSecrets?: () => void;
    clearAttachment?: () => void;
  } | null>(null);
  const debugPassthroughConnectionId = ref("");
  const chatDropdowns = ref<Record<ChatDropdownKey, boolean>>({
    workspace: false,
    agent: false,
  });
  const selectedWorkspace = computed(() =>
    workspaces.items.find((workspace) => workspace.id === selectedWorkspaceId.value),
  );
  const filteredAgents = computed(() =>
    agents.items.filter((agent) => !selectedWorkspaceId.value || agent.workspaceId === selectedWorkspaceId.value),
  );
  const selectedAgent = computed(() => filteredAgents.value.find((agent) => agent.id === selectedAgentId.value));
  const activeSessionAgent = computed(() => agents.items.find((agent) => agent.id === chat.activeSession?.agentId));
  const activeSessionWorkspace = computed(() =>
    workspaces.items.find((workspace) => workspace.id === chat.activeSession?.workspaceId),
  );
  const activeWorkspaceLabel = computed(
    () => selectedWorkspace.value?.displayName || selectedWorkspace.value?.name || "全部业务空间",
  );
  const activeAgentLabel = computed(
    () => selectedAgent.value?.name || (contextLoading.value ? "载入 Agent" : "请选择 Agent"),
  );
  const activeUserLabel = computed(() => auth.user?.username || auth.user?.displayName || "当前用户");
  const activeUserInitials = computed(() => {
    const source = (auth.user?.displayName || auth.user?.username || "我").trim();
    const words = source.split(/[\s._-]+/).filter(Boolean);
    return (words.length > 1 ? words.map((word) => word[0]).join("") : source.slice(0, 2)).toUpperCase();
  });
  const contextSelectionDirty = computed(
    () =>
      Boolean(chat.activeSession) &&
      (chat.activeSession?.workspaceId !== selectedWorkspaceId.value ||
        chat.activeSession?.agentId !== selectedAgentId.value),
  );
  const activeSessionReadOnly = computed(
    () => Boolean(chat.activeSession) && (chat.activeSession?.status === "ARCHIVED" || !activeSessionAgent.value),
  );
  const activeSessionAgentStatusLabel = computed(() =>
    chat.activeSession?.status === "ARCHIVED" ? "已归档" : "不可用",
  );
  const composerPlaceholder = computed(() =>
    activeSessionReadOnly.value ? "关联 Agent 已不可用，请使用可用 Agent 新建会话" : "输入业务指令或目标任务...",
  );
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
    if (chat.runStatus === "FAILED") {
      const code = chat.latestRun?.errorCode || "";
      // ZKL-74: stable context-window error projections (no raw provider body).
      switch (code) {
        case "CONTEXT_SNAPSHOT_UNSUPPORTED":
          return "运行上下文版本不受支持，请联系管理员。";
        case "CONTEXT_MODEL_LIMIT_UNKNOWN":
          return "模型未配置上下文容量，请联系管理员。";
        case "CONTEXT_REQUIRED_INPUT_TOO_LARGE":
          return "当前输入过长；请缩短输入、减少附件/工具或新建会话。";
        case "CONTEXT_ASSEMBLY_FAILED":
          return "无法准备本次上下文，请稍后重试。";
        case "CONTEXT_WINDOW_EXCEEDED_UPSTREAM":
          return "模型上下文容量校验失败，请联系管理员。";
        default:
          return code || "运行时执行失败。";
      }
    }
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
  const conversationBusy = computed(
    () => sending.value || chat.runStatus === "PENDING" || chat.runStatus === "RUNNING",
  );
  const currentSubjectLabel = computed(() => {
    const user = auth.user;
    if (!user) return "未登录";
    return `${user.displayName || user.username} · USER`;
  });
  /** Connections fixed to REQUEST_PASSTHROUGH in this workspace (need one-shot Token attach). */
  const passthroughConnections = computed(() =>
    connectionsStore.serviceConnections.filter(
      (c) => c.outboundMode === "REQUEST_PASSTHROUGH" && c.status !== "DISABLED",
    ),
  );
  const requiresPassthroughToken = computed(() => passthroughConnections.value.length > 0);
  const effectiveDebugConnectionId = computed(
    () => debugPassthroughConnectionId.value || passthroughConnections.value[0]?.id || "",
  );

  onMounted(async () => {
    await runChatAction(async () => {
      await Promise.all([workspaces.load(), agents.loadAgents()]);
      const activeWorkspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id;
      await chat.loadSessions(activeWorkspaceId ? [activeWorkspaceId] : []);
      // AppShell remounts this view when the global active workspace changes.
      // Never re-apply a cross-workspace active session here — that overwrites the
      // just-selected workspace and makes the topbar/chat selectors flash back.
      await bootstrapChatForActiveWorkspace();
      if (selectedWorkspaceId.value || workspaces.activeWorkspaceId) {
        try {
          await connectionsStore.loadServiceConnectionCatalog({ commit: true });
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
    debugOutboundPanel.value?.clearSecrets?.();
    debugOutboundPanel.value?.clearAttachment?.();
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
    return toolsStore.attachChatOutboundCredentials(session.id, body);
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
      debugOutboundPanel.value?.clearAttachment?.();
      debugOutboundPanel.value?.clearSecrets?.();
    } catch (error) {
      forceFollowNextMessage.value = false;
      actionError.value = readableChatError(error, "消息发送失败，请稍后重试。");
      if (!composer.value.trim()) {
        composer.value = value;
      }
      // Fail closed: do not retry with residual attachment id or Token.
      outboundAttachmentId.value = null;
      debugOutboundPanel.value?.clearAttachment?.();
      debugOutboundPanel.value?.clearSecrets?.();
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
    if (archivingSession.value) return;
    archivingSession.value = true;
    try {
      await runChatAction(() => chat.archiveSession(), "归档会话失败，请稍后重试。");
    } finally {
      archivingSession.value = false;
    }
  }

  function renderMessageMarkdown(content: string) {
    return renderMarkdown(content, "");
  }

  async function createSessionWithSelection() {
    if (!selectedWorkspaceId.value || !selectedAgentId.value) return;
    const session = await chat.createSession(
      selectedWorkspaceId.value,
      selectedAgentId.value,
      `${activeAgentLabel.value} 对话`,
    );
    await syncSelectionFromSession(session, false);
  }

  async function bootstrapChatForActiveWorkspace() {
    const activeWorkspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
    const activeSessionInWorkspace =
      Boolean(chat.activeSessionId) &&
      chat.sessions.some((session) => session.id === chat.activeSessionId && session.workspaceId === activeWorkspaceId);

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
    selectedAgentId.value =
      defaultAgentId && scopedAgents.some((agent) => agent.id === defaultAgentId)
        ? defaultAgentId
        : scopedAgents[0]?.id || "";
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
    return date.toLocaleDateString("zh-CN", {
      year: date.getFullYear() === today.getFullYear() ? undefined : "numeric",
      month: "long",
      day: "numeric",
    });
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

  return {
    workspaces,
    agents,
    auth,
    chat,
    connectionsStore,
    toolsStore,
    composer,
    selectedWorkspaceId,
    selectedAgentId,
    sessionKeyword,
    sending,
    confirming,
    archivingSession,
    contextLoading,
    actionError,
    activeSidePanel,
    sessionPanelTrigger,
    runtimePanelTrigger,
    sessionPanelCloseButton,
    runtimePanelCloseButton,
    chatScrollArea,
    workspaceDropdownTrigger,
    agentDropdownTrigger,
    hasUnreadMessages,
    forceFollowNextMessage,
    syncingSessionSelection,
    outboundAttachmentId,
    debugOutboundPanel,
    debugPassthroughConnectionId,
    chatDropdowns,
    selectedWorkspace,
    filteredAgents,
    selectedAgent,
    activeSessionAgent,
    activeSessionWorkspace,
    activeWorkspaceLabel,
    activeAgentLabel,
    activeUserLabel,
    activeUserInitials,
    contextSelectionDirty,
    activeSessionReadOnly,
    activeSessionAgentStatusLabel,
    composerPlaceholder,
    filteredSessions,
    latestRunStepRows,
    runtimeSummary,
    runStatusLabel,
    runtimeIntentLabel,
    capabilityCount,
    capabilityCountLabel,
    runtimeTargetReleaseId,
    conversationBusy,
    currentSubjectLabel,
    passthroughConnections,
    requiresPassthroughToken,
    effectiveDebugConnectionId,
    newSession,
    selectSession,
    toggleSidePanel,
    closeSidePanel,
    trapSidePanelFocus,
    scrollToLatestTurn,
    isConversationNearLatest,
    handleConversationScroll,
    revealLatestMessages,
    attachDebugOutboundCredentials,
    onOutboundAttachment,
    send,
    confirm,
    cancelConfirmation,
    archiveCurrentSession,
    renderMessageMarkdown,
    createSessionWithSelection,
    bootstrapChatForActiveWorkspace,
    syncSelectionFromWorkspaceStore,
    syncSelectionFromWorkspace,
    syncSelectionFromSession,
    sessionAgentName,
    sessionWorkspaceName,
    toggleChatDropdown,
    closeChatDropdowns,
    selectWorkspaceOption,
    selectAgentOption,
    handleDropdownMenuKeydown,
    focusDropdownOption,
    runChatAction,
    readableChatError,
    statusLabel,
    statusBadgeClass,
    timelineStatus,
    stepStatusIcon,
    runStepLabel,
    runStepSummary,
    summarizeRuntimeValue,
    capabilitySnapshotCount,
    runStepStatusLabel,
    messageTime,
    messageDateTimeTitle,
    messageDateLabel,
    shouldShowMessageDate,
    parseMessageDate,
    localDateKey,
    sessionTime,
  };
}
