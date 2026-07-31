/**
 * Agents page model (ZKL-64 item 16).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useAgentStore } from "../stores/agents";
import { useModelConfigStore } from "../stores/modelConfigs";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Agent,
  AgentCapabilityBinding,
  AgentListQuery,
  CapabilityCatalogItem,
  ModelApiConfig,
  PromptEnhancement,
  Workspace,
} from "../types/domain";
import { renderMarkdown } from "../utils/markdown";
import {
  buildContextPolicyPayload,
  DEFAULT_ROLLING_SUMMARY_MAX_RECENT_TURNS,
  defaultRollingSummary,
  normalizeContextPolicy,
} from "../utils/session-context-config";
import type { ManagementListColumn } from "../components/ManagementList.vue";
import type { ManagementRowAction } from "../components/ManagementRowActions.vue";
import type { ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import type { SessionContextPolicy } from "../types/domain";

export function createAgentsPageModel() {
  type AgentStatusFilter = "ALL" | "ACTIVE" | "DISABLED";

  const agents = useAgentStore();
  const workspaces = useWorkspaceStore();
  const canEditWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "EDIT"),
  );
  const canDeleteWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "DELETE"),
  );
  const modelConfigs = useModelConfigStore();

  const query = ref("");
  const agentStatusFilter = ref<AgentStatusFilter>("ALL");
  const studioMode = ref<"create" | "edit" | null>(null);
  const draftAgent = ref<Agent>(newAgent());
  const agentActionNote = ref("");
  const agentActionTone = ref<"success" | "error">("success");
  const enhancingAgentId = ref("");
  const promptDetailAgent = ref<Agent | null>(null);
  const pageInitialLoading = ref(true);
  const savingAgent = ref(false);
  const agentDeleting = ref(false);
  const agentDeleteTarget = ref<Agent | null>(null);
  const agentDeleteConfirmName = ref("");
  const agentToastTimer = ref<ReturnType<typeof window.setTimeout> | null>(null);
  const agentStudioPanelRef = ref<HTMLElement | null>(null);
  const agentNameInputRef = ref<HTMLInputElement | null>(null);
  const promptDetailDialogRef = ref<HTMLElement | null>(null);
  const agentDeleteDialogRef = ref<HTMLElement | null>(null);
  const agentDeleteInputRef = ref<HTMLInputElement | null>(null);
  const lastFocusBeforeModal = ref<HTMLElement | null>(null);
  const agentStudioInitialSnapshot = ref("");
  const agentStudioOriginalAgent = ref<Agent | null>(null);
  const agentStudioInlineWarning = ref("");
  const pendingPromptSaveReview = ref<Agent | null>(null);
  const weavePreviewAgent = ref<PromptEnhancement | null>(null);
  /** Create-mode only: run id applied to the draft for optional source linking. */
  const sourcePromptPreviewRunId = ref("");
  const createPreviewRequestSeq = ref(0);
  const createPreviewSnapshot = ref<{
    requestSeq: number;
    workspaceId: string;
    modelConfigId: string;
    input: string;
  } | null>(null);
  const currentPromptBody = ref("");
  const currentPromptMeta = ref<{
    revisionId: string;
    revisionNo: number;
    source: string;
    createdAt: string;
  } | null>(null);
  const currentPromptLoading = ref(false);
  const currentPromptError = ref("");
  const currentPromptAbort = ref<AbortController | null>(null);
  const acceptingPromptRevision = ref(false);
  const capabilityAgent = ref<Agent | null>(null);
  const capabilityLoading = ref(false);
  const capabilitySavingId = ref("");
  /** When non-empty, batch bind/unbind is in progress (disables per-row actions too). */
  const capabilityBatchBusy = ref(false);
  const capabilityDrafts = ref<Record<string, AgentCapabilityBinding>>({});
  /** Selected capability IDs for batch bind/unbind in the capability dialog. */
  const capabilitySelectedIds = ref<string[]>([]);

  const statusSourceText = "状态由 Agent 配置维护；Tool/Workflow 数量从当前启用的能力绑定只读派生。";

  const selectedAgent = computed(() => agents.selectedAgent || agents.items[0] || null);
  const hasAgentRecords = computed(() => agents.items.length > 0);
  const agentSummaryItems = computed<ManagementSummaryItem[]>(() => {
    const total = agents.items.length;
    const active = agents.items.filter((agent) => agent.status === "ACTIVE").length;
    const paused = agents.items.filter((agent) => agent.status === "DISABLED").length;
    const modelCount = new Set(agents.items.map((agent) => agent.modelConfigId).filter(Boolean)).size;
    return [
      { label: "Agent 总数", value: total, icon: "fa-solid fa-user-gear" },
      {
        label: "运行中",
        value: active,
        note: total ? `${((active / total) * 100).toFixed(1)}%` : "0%",
        icon: "fa-solid fa-circle-check",
      },
      { label: "已暂停", value: paused, icon: "fa-solid fa-circle-pause", tone: "warning" },
      { label: "模型配置", value: modelCount, icon: "fa-solid fa-brain" },
    ];
  });
  const workspaceOptions = computed(() =>
    workspaces.items.map((workspace) => ({
      label: `${workspace.name} (${workspace.displayName})`,
      value: workspace.id,
    })),
  );
  const modelConfigOptions = computed(() =>
    modelConfigs.items.map((config) => ({
      label: modelConfigOptionLabel(config),
      value: config.id,
    })),
  );
  const promptDetailVisible = computed({
    get: () => promptDetailAgent.value !== null,
    set: (visible: boolean) => {
      if (!visible) {
        closePromptDetail();
      }
    },
  });
  const studioTitle = computed(() => (studioMode.value === "create" ? "创建 Agent" : "编辑 Agent"));
  const promptDetailHTML = computed(() => renderPromptMarkdown(currentPromptBody.value || ""));
  const agentManagementFilterOptions = computed<Array<{ label: string; value: AgentStatusFilter }>>(() => [
    { label: "全部", value: "ALL" },
    { label: "运行中", value: "ACTIVE" },
    { label: "暂停", value: "DISABLED" },
  ]);
  const agentColumns = computed<ManagementListColumn<Agent>[]>(() => [
    {
      key: "identity",
      label: "Agent",
      width: 286,
      sortable: true,
      sortKey: "name",
      getValue: (agent) => `${agent.name} ${agent.roleDescription}`,
    },
    {
      key: "workspace",
      label: "绑定空间",
      width: 190,
      hidable: true,
      sortable: true,
      sortKey: "workspace",
      getValue: workspaceLabel,
    },
    {
      key: "model",
      label: "决策模型",
      width: 180,
      hidable: true,
      sortable: true,
      sortKey: "model",
      getValue: modelLabel,
    },
    {
      key: "prompt",
      label: "系统提示词",
      width: 150,
      hidable: true,
      getValue: (agent) => agent.currentPromptRevisionId || "-",
    },
    {
      key: "status",
      label: "状态",
      width: 140,
      hidable: true,
      sortable: true,
      sortKey: "status",
      getValue: (agent) => statusLabel(agent.status),
    },
    {
      key: "updatedAt",
      label: "最近修改",
      width: 130,
      hidable: true,
      sortable: true,
      sortKey: "updatedAt",
      getValue: formatAgentUpdatedAt,
    },
    { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
  ]);
  const isAgentDeleteConfirmDirty = computed(() => agentDeleteConfirmName.value.trim().length > 0);
  const isAgentStudioDirty = computed(() => {
    return Boolean(studioMode.value && serializeAgentDraft(draftAgent.value) !== agentStudioInitialSnapshot.value);
  });
  const agentNameError = computed(() => (draftAgent.value.name.trim() ? "" : "请输入 Agent 运行名称。"));
  const agentWorkspaceError = computed(() => (draftAgent.value.workspaceId ? "" : "请选择绑定业务空间。"));
  const agentModelError = computed(() => (draftAgent.value.modelConfigId ? "" : "请选择决策大模型。"));
  const agentRoleError = computed(() => (draftAgent.value.roleDescription.trim() ? "" : "请输入场景决策职责。"));
  const agentPromptError = computed(() =>
    studioMode.value === "create" && !draftAgent.value.systemPrompt.trim() ? "请输入系统提示词。" : "",
  );
  const promptLineCount = computed(() => {
    const value = draftAgent.value.systemPrompt.trim();
    return value ? value.split(/\r?\n/).length : 0;
  });
  const promptPreviewText = computed(() => {
    const value =
      draftAgent.value.systemPrompt
        .split(/\r?\n/)
        .map((line) => line.trim())
        .find(Boolean) || "";
    if (!value) return "暂无提示词内容。";
    return value.length > 140 ? `${value.slice(0, 140)}...` : value;
  });
  const isAgentDraftValid = computed(
    () =>
      !agentNameError.value &&
      !agentWorkspaceError.value &&
      !agentModelError.value &&
      !agentRoleError.value &&
      !agentPromptError.value,
  );
  const canSaveAgent = computed(() =>
    Boolean(studioMode.value && !savingAgent.value && isAgentStudioDirty.value && isAgentDraftValid.value),
  );
  const originalPrompt = computed(() => agentStudioOriginalAgent.value?.systemPrompt || "");
  const promptSaveDiff = computed(() =>
    buildPromptDiffSummary(originalPrompt.value, draftAgent.value.systemPrompt || ""),
  );
  const pendingPromptText = computed(
    () => pendingPromptSaveReview.value?.systemPrompt || draftAgent.value.systemPrompt || "",
  );
  const weavePreviewDiff = computed(() =>
    buildPromptDiffSummary(draftAgent.value.systemPrompt || "", weavePreviewAgent.value?.output || ""),
  );
  const capabilityCatalog = computed(() =>
    capabilityAgent.value ? agents.capabilitiesByWorkspace[capabilityAgent.value.workspaceId] || [] : [],
  );
  const agentSaveButtonLabel = computed(() => {
    if (savingAgent.value) return studioMode.value === "create" ? "创建中..." : "保存中...";
    return studioMode.value === "create" ? "创建 Agent" : "保存 Agent";
  });
  const canEnhanceDraftPrompt = computed(() => {
    if (!canEditWorkspace.value) return false;
    const prompt = draftAgent.value.systemPrompt.trim();
    const busy = isEnhancing(draftAgent.value.id || "create-draft");
    if (!prompt || busy) return false;
    // Edit mode still requires Agent ID; create mode only needs workspace + model + non-empty prompt.
    if (studioMode.value === "edit") return Boolean(draftAgent.value.id);
    return Boolean(
      draftAgent.value.workspaceId &&
      draftAgent.value.modelConfigId &&
      modelConfigs.items.some((item) => item.id === draftAgent.value.modelConfigId),
    );
  });
  const canConfirmAgentDelete = computed(() => {
    const agent = agentDeleteTarget.value;
    return Boolean(agent && agentDeleteConfirmName.value.trim() === agent.name);
  });
  const agentDeleteNameError = computed(() => {
    const agent = agentDeleteTarget.value;
    if (!agent || !agentDeleteConfirmName.value) return "";
    if (agentDeleteConfirmName.value.trim() === agent.name) return "";
    return `名称需与 ${agent.name} 完全一致，区分大小写并忽略首尾空格。`;
  });

  onMounted(async () => {
    try {
      await Promise.all([workspaces.load(), modelConfigs.loadModelConfigs(), loadAgentRegistry({ page: 1 })]);
      if (!agents.selectedAgentId) {
        agents.selectedAgentId = agents.items[0]?.id || "";
      }
    } finally {
      pageInitialLoading.value = false;
    }
  });

  onBeforeUnmount(() => {
    clearAgentToast();
    window.removeEventListener("keydown", handleAgentDeleteDialogKeydown);
  });

  watch(
    () => serializeAgentDraft(draftAgent.value),
    () => {
      agentStudioInlineWarning.value = "";
    },
  );

  function newAgent(): Agent {
    const workspaceId = workspaces.activeWorkspaceId || "default";
    const workspace = workspaceById(workspaceId);
    return {
      id: "",
      workspaceId,
      name: "售后处理 Agent",
      roleDescription: "处理售后通知、退款前置校验与异常升级",
      modelConfigId: workspace?.defaultModelConfigId || modelConfigs.items[0]?.id || "",
      systemPrompt: "负责处理当前业务空间内的售后问题，必要时调用已授权的 Tool 或 Workflow。",
      isDefault: false,
      status: "ACTIVE",
      contextPolicy: {
        schemaVersion: "session-context-policy.v1",
        mode: "token_window",
        maxInputTokens: 0,
        maxRecentTurns: 0,
      },
      toolsCount: 0,
      workflowsCount: 0,
      createdBy: "",
      updatedBy: "",
      createdAt: "",
      updatedAt: "",
      lockVersion: 0,
    };
  }

  function serializeAgentDraft(agent: Agent) {
    return JSON.stringify({
      workspaceId: agent.workspaceId,
      name: agent.name,
      roleDescription: agent.roleDescription,
      modelConfigId: agent.modelConfigId,
      ...(agent.id ? {} : { systemPrompt: agent.systemPrompt }),
      isDefault: agent.isDefault,
      status: agent.status,
      contextPolicy: agent.contextPolicy || {},
    });
  }

  function workspaceById(workspaceId: string): Workspace | undefined {
    return workspaces.items.find((workspace) => workspace.id === workspaceId);
  }

  function modelConfigById(configId: string): ModelApiConfig | undefined {
    return modelConfigs.items.find((config) => config.id === configId);
  }

  function workspaceLabel(agent: Agent) {
    return workspaceById(agent.workspaceId)?.name || agent.workspaceId;
  }

  function modelLabel(agent: Agent) {
    return modelConfigById(agent.modelConfigId)?.modelName || agent.modelConfigId;
  }

  function statusLabel(status: string) {
    return status === "ACTIVE" ? "运行中" : "已暂停";
  }

  function formatAgentUpdatedAt(agent: Agent) {
    if (!agent.updatedAt) return "-";
    const timestamp = Date.parse(agent.updatedAt);
    if (!Number.isFinite(timestamp)) return agent.updatedAt;
    const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
    if (elapsedMinutes < 1) return "刚刚";
    if (elapsedMinutes < 60) return `${elapsedMinutes} 分钟前`;
    const elapsedHours = Math.floor(elapsedMinutes / 60);
    if (elapsedHours < 24) return `${elapsedHours} 小时前`;
    return `${Math.floor(elapsedHours / 24)} 天前`;
  }

  function modelConfigOptionLabel(config: ModelApiConfig) {
    return `${config.modelName}${config.id === workspaceById(draftAgent.value.workspaceId)?.modelConfigId ? " · 当前空间默认" : ""}`;
  }

  function renderPromptMarkdown(source: string) {
    return renderMarkdown(source, "暂无系统提示词内容。");
  }

  function buildPromptDiffSummary(before: string, after: string) {
    const beforeLines = before ? before.split(/\r?\n/).length : 0;
    const afterLines = after ? after.split(/\r?\n/).length : 0;
    return {
      beforeChars: before.length,
      afterChars: after.length,
      charDelta: after.length - before.length,
      beforeLines,
      afterLines,
      lineDelta: afterLines - beforeLines,
    };
  }

  function formatSignedDelta(value: number) {
    if (value > 0) return `+${value}`;
    return String(value);
  }

  function isProductionPromptChange(_agent: Agent) {
    return false;
  }

  function agentActionErrorMessage(error: unknown) {
    const responseError = (error as { response?: { data?: { error?: string } } }).response?.data?.error || "";
    const timeoutCode = (error as { code?: string }).code || "";
    if (responseError) {
      return `整理失败：${responseError}`;
    }
    if (timeoutCode === "ECONNABORTED") {
      return "整理失败：模型响应超时，请稍后重试。";
    }
    return "整理失败：模型调用未成功完成，请检查模型配置和网络后重试。";
  }

  function statusTone(status: string) {
    return status.toLowerCase();
  }

  function isEnhancing(agentId: string) {
    return Boolean(agentId) && enhancingAgentId.value === agentId;
  }

  function canDeleteAgent(agent: Agent) {
    return !agent.isDefault;
  }

  function selectAgent(agent: Agent) {
    agents.selectedAgentId = agent.id;
  }

  function resetFilters() {
    query.value = "";
    agentStatusFilter.value = "ALL";
    void loadAgentRegistry({ query: "", status: undefined, page: 1 });
  }

  function setAgentStatusFilter(value: AgentStatusFilter) {
    agentStatusFilter.value = value;
    void loadAgentRegistry({ status: value === "ALL" ? undefined : value, page: 1 });
  }

  function activeWorkspaceFilterId() {
    return workspaces.activeWorkspaceId || workspaces.items[0]?.id || undefined;
  }

  function toggleDraftStatus() {
    draftAgent.value.status = draftAgent.value.status === "ACTIVE" ? "DISABLED" : "ACTIVE";
  }

  function draftContextPolicy(): SessionContextPolicy {
    return normalizeContextPolicy(draftAgent.value.contextPolicy);
  }

  const agentContextMode = computed(() => draftContextPolicy().mode || "");
  const agentContextMaxInputTokens = computed(() => draftContextPolicy().maxInputTokens ?? 0);
  const agentContextMaxRecentTurns = computed(() => draftContextPolicy().maxRecentTurns ?? 0);
  const agentContextSummaryMaxTokens = computed(
    () => draftContextPolicy().summary?.maxTokens ?? defaultRollingSummary().maxTokens ?? 2048,
  );
  const agentContextSummaryMinEvictedTurns = computed(
    () => draftContextPolicy().summary?.minEvictedTurns ?? defaultRollingSummary().minEvictedTurns ?? 4,
  );
  const agentContextSummaryMaxPasses = computed(
    () => draftContextPolicy().summary?.maxGenerationPasses ?? defaultRollingSummary().maxGenerationPasses ?? 2,
  );
  const agentContextIncludeCompactionSummary = computed(() =>
    Boolean(draftContextPolicy().aap?.includeCompactionSummary),
  );
  const agentContextAdvancedOpen = ref(false);

  function toggleAgentContextAdvanced() {
    agentContextAdvancedOpen.value = !agentContextAdvancedOpen.value;
  }

  function setAgentContextMode(mode: string) {
    const value = String(mode || "").trim();
    if (!value || value === "inherit") {
      const include = Boolean(draftContextPolicy().aap?.includeCompactionSummary);
      draftAgent.value.contextPolicy = include
        ? { schemaVersion: "session-context-policy.v2", mode: undefined, aap: { includeCompactionSummary: true } }
        : { mode: undefined };
      return;
    }
    if (value === "disabled") {
      const include = Boolean(draftContextPolicy().aap?.includeCompactionSummary);
      draftAgent.value.contextPolicy = include
        ? {
            schemaVersion: "session-context-policy.v2",
            mode: "disabled",
            aap: { includeCompactionSummary: true },
          }
        : {
            schemaVersion: "session-context-policy.v1",
            mode: "disabled",
          };
      return;
    }
    if (value !== "token_window" && value !== "rolling_summary") return;
    const current = draftContextPolicy();
    const include = Boolean(current.aap?.includeCompactionSummary);
    const schemaVersion = include ? "session-context-policy.v2" : "session-context-policy.v1";
    const aap = include ? { aap: { includeCompactionSummary: true as const } } : {};
    if (value === "token_window") {
      draftAgent.value.contextPolicy = {
        schemaVersion,
        mode: "token_window",
        maxInputTokens: current.maxInputTokens ?? 0,
        maxRecentTurns: current.maxRecentTurns ?? 0,
        ...(current.outputReserveTokens != null ? { outputReserveTokens: current.outputReserveTokens } : {}),
        ...(current.safetyMarginTokens != null ? { safetyMarginTokens: current.safetyMarginTokens } : {}),
        ...aap,
      };
      return;
    }
    // rolling_summary: fill product defaults so create/save always carries summary knobs.
    const maxRecent =
      current.maxRecentTurns && current.maxRecentTurns > 0
        ? current.maxRecentTurns
        : DEFAULT_ROLLING_SUMMARY_MAX_RECENT_TURNS;
    draftAgent.value.contextPolicy = {
      schemaVersion,
      mode: "rolling_summary",
      maxInputTokens: current.maxInputTokens ?? 0,
      maxRecentTurns: maxRecent,
      ...(current.outputReserveTokens != null ? { outputReserveTokens: current.outputReserveTokens } : {}),
      ...(current.safetyMarginTokens != null ? { safetyMarginTokens: current.safetyMarginTokens } : {}),
      summary: defaultRollingSummary(current.summary),
      ...aap,
    };
  }

  function setAgentContextMaxInput(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "token_window" && current.mode !== "rolling_summary") return;
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v1",
      maxInputTokens: Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0,
    };
  }

  function setAgentContextMaxTurns(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "token_window" && current.mode !== "rolling_summary") return;
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v1",
      maxRecentTurns: Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0,
    };
  }

  function setAgentContextSummaryMaxTokens(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "rolling_summary") return;
    const summary = defaultRollingSummary(current.summary);
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v1",
      summary: {
        ...summary,
        maxTokens: Number.isFinite(value) && value > 0 ? Math.floor(value) : summary.maxTokens,
      },
    };
  }

  function setAgentContextSummaryMinEvictedTurns(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "rolling_summary") return;
    const summary = defaultRollingSummary(current.summary);
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v1",
      summary: {
        ...summary,
        minEvictedTurns: Number.isFinite(value) && value >= 0 ? Math.floor(value) : summary.minEvictedTurns,
      },
    };
  }

  function setAgentContextSummaryMaxPasses(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "rolling_summary") return;
    const summary = defaultRollingSummary(current.summary);
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v1",
      summary: {
        ...summary,
        maxGenerationPasses: Number.isFinite(value) && value > 0 ? Math.floor(value) : summary.maxGenerationPasses,
      },
    };
  }

  /** T4-B Agent-only AAP disclosure; forces policy v2 when set. */
  function setAgentContextIncludeCompactionSummary(value: boolean) {
    const current = draftContextPolicy();
    const include = Boolean(value);
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v2",
      aap: { includeCompactionSummary: include },
    };
  }

  function enterCreateMode() {
    lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    draftAgent.value = newAgent();
    agentStudioOriginalAgent.value = null;
    agentStudioInitialSnapshot.value = serializeAgentDraft(draftAgent.value);
    agentStudioInlineWarning.value = "";
    agentContextAdvancedOpen.value = false;
    pendingPromptSaveReview.value = null;
    weavePreviewAgent.value = null;
    sourcePromptPreviewRunId.value = "";
    studioMode.value = "create";
    clearAgentToast();
    void focusAgentStudio();
  }

  function enterEditMode(agent: Agent) {
    lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    selectAgent(agent);
    const policy = normalizeContextPolicy(agent.contextPolicy);
    draftAgent.value = {
      ...agent,
      systemPrompt: "",
      contextPolicy: policy,
    };
    agentStudioOriginalAgent.value = { ...agent };
    agentStudioInitialSnapshot.value = serializeAgentDraft(draftAgent.value);
    agentStudioInlineWarning.value = "";
    // Only auto-expand advanced when values differ from product defaults.
    const isNonDefaultTokenWindow =
      (policy.mode === "token_window" || policy.mode === "rolling_summary") &&
      ((policy.maxInputTokens != null && policy.maxInputTokens > 0) ||
        (policy.mode === "token_window" && policy.maxRecentTurns != null && policy.maxRecentTurns > 0) ||
        (policy.mode === "rolling_summary" &&
          policy.maxRecentTurns != null &&
          policy.maxRecentTurns > 0 &&
          policy.maxRecentTurns !== DEFAULT_ROLLING_SUMMARY_MAX_RECENT_TURNS));
    const summary = policy.summary;
    const isNonDefaultSummary =
      policy.mode === "rolling_summary" &&
      summary != null &&
      ((summary.maxTokens != null && summary.maxTokens !== 2048) ||
        (summary.minEvictedTurns != null && summary.minEvictedTurns !== 4) ||
        (summary.maxGenerationPasses != null && summary.maxGenerationPasses !== 2));
    agentContextAdvancedOpen.value = Boolean(isNonDefaultTokenWindow || isNonDefaultSummary);
    pendingPromptSaveReview.value = null;
    weavePreviewAgent.value = null;
    sourcePromptPreviewRunId.value = "";
    studioMode.value = "edit";
    clearAgentToast();
    void focusAgentStudio();
  }

  function closeStudio() {
    studioMode.value = null;
    agentStudioInitialSnapshot.value = "";
    agentStudioOriginalAgent.value = null;
    agentStudioInlineWarning.value = "";
    pendingPromptSaveReview.value = null;
    weavePreviewAgent.value = null;
    restoreLastFocus();
  }

  function exitStudio() {
    closeStudio();
  }

  function requestCloseStudio(_source: "backdrop" | "keyboard" | "back") {
    if (!studioMode.value || savingAgent.value) return;
    if (isAgentStudioDirty.value) {
      agentStudioInlineWarning.value = "已有未保存修改，请先创建/保存 Agent，或使用“放弃改动”明确退出。";
      showAgentToast("Agent 草稿已修改，请先创建/保存 Agent 或放弃改动，再离开当前编辑窗口。", "error");
      return;
    }
    closeStudio();
  }

  async function focusAgentStudio() {
    await nextTick();
    const target =
      agentNameInputRef.value ||
      agentStudioPanelRef.value?.querySelector<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
      );
    target?.focus();
  }

  async function focusDialog(dialog: HTMLElement | null, preferredTarget?: HTMLElement | null) {
    await nextTick();
    const target =
      preferredTarget ||
      dialog?.querySelector<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      );
    target?.focus();
  }

  function restoreLastFocus() {
    void nextTick(() => {
      lastFocusBeforeModal.value?.focus();
      lastFocusBeforeModal.value = null;
    });
  }

  function activeAgentModal() {
    return agentDeleteDialogRef.value || promptDetailDialogRef.value || agentStudioPanelRef.value;
  }

  function trapAgentModalFocus(event: KeyboardEvent) {
    if (event.key !== "Tab") return;
    const modal = activeAgentModal();
    if (!modal) return;
    const focusable = Array.from(
      modal.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((element) => !element.hasAttribute("disabled") && element.offsetParent !== null);
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function showAgentToast(message: string, tone: "success" | "error" = "success", autoClose = true) {
    if (agentToastTimer.value) {
      window.clearTimeout(agentToastTimer.value);
      agentToastTimer.value = null;
    }
    agentActionTone.value = tone;
    agentActionNote.value = message;
    if (!autoClose) return;
    const duration = tone === "error" ? 8000 : Math.max(6000, Math.min(10000, message.length * 180));
    agentToastTimer.value = window.setTimeout(() => {
      clearAgentToast();
    }, duration);
  }

  function clearAgentToast() {
    if (agentToastTimer.value) {
      window.clearTimeout(agentToastTimer.value);
      agentToastTimer.value = null;
    }
    agentActionNote.value = "";
  }

  async function enhancePrompt() {
    if (!canEnhanceDraftPrompt.value) {
      showAgentToast("请完善业务空间、模型和系统提示词后再整理。", "error");
      return;
    }
    const isCreate = studioMode.value === "create" || !draftAgent.value.id;
    enhancingAgentId.value = draftAgent.value.id || "create-draft";
    showAgentToast(
      isCreate
        ? "正在生成系统提示词整理预览，通常需要 1 到 2 分钟。"
        : `${draftAgent.value.name} 正在生成系统提示词整理预览，通常需要 1 到 2 分钟。`,
      "success",
      false,
    );
    try {
      if (isCreate) {
        const requestSeq = ++createPreviewRequestSeq.value;
        const snapshot = {
          requestSeq,
          workspaceId: draftAgent.value.workspaceId,
          modelConfigId: draftAgent.value.modelConfigId,
          input: draftAgent.value.systemPrompt,
        };
        createPreviewSnapshot.value = snapshot;
        const preview = await agents.previewCreatePromptEnhancement(
          snapshot.workspaceId,
          snapshot.modelConfigId,
          snapshot.input,
        );
        const latest = createPreviewSnapshot.value;
        if (
          !latest ||
          latest.requestSeq !== requestSeq ||
          latest.workspaceId !== draftAgent.value.workspaceId ||
          latest.modelConfigId !== draftAgent.value.modelConfigId ||
          latest.input !== draftAgent.value.systemPrompt
        ) {
          showAgentToast("草稿已变化，已丢弃过期的整理结果，请重新整理。", "error");
          return;
        }
        weavePreviewAgent.value = {
          runId: preview.runId,
          status: preview.status,
          preview: true,
          output: preview.output,
          createdAt: preview.createdAt,
          expiresAt: preview.expiresAt,
        };
        showAgentToast("系统提示词整理预览已生成，可应用到草稿后创建 Agent。");
      } else {
        const preview = await agents.enhanceAgentPrompt(draftAgent.value, draftAgent.value.systemPrompt, {
          preview: true,
        });
        weavePreviewAgent.value = preview;
        showAgentToast(`${draftAgent.value.name} 的系统提示词整理预览已生成，请审查后再采纳为新版本。`);
      }
      void nextTick(() => focusDialog(promptDetailDialogRef.value));
    } catch (error) {
      showAgentToast(agentActionErrorMessage(error), "error");
    } finally {
      enhancingAgentId.value = "";
    }
  }

  async function markPromptEnhanced(agent: Agent) {
    if (isEnhancing(agent.id)) return;
    enterEditMode(agent);
    await nextTick();
    await enhancePrompt();
  }

  async function applyWeavePreview() {
    if (!weavePreviewAgent.value || acceptingPromptRevision.value) return;
    const isCreate = studioMode.value === "create" || !draftAgent.value.id;
    if (isCreate) {
      // Apply to draft only — do not persist Agent/Revision.
      draftAgent.value = {
        ...draftAgent.value,
        systemPrompt: weavePreviewAgent.value.output,
      };
      sourcePromptPreviewRunId.value = weavePreviewAgent.value.runId;
      weavePreviewAgent.value = null;
      showAgentToast("已应用到草稿。请检查后点击「创建 Agent」保存。");
      void focusAgentStudio();
      return;
    }
    acceptingPromptRevision.value = true;
    try {
      const accepted = await agents.enhanceAgentPrompt(draftAgent.value, draftAgent.value.systemPrompt, {
        preview: false,
        lockVersion: draftAgent.value.lockVersion,
      });
      await loadAgentRegistry();
      const refreshed = agents.items.find((agent) => agent.id === draftAgent.value.id);
      if (refreshed) {
        draftAgent.value = { ...refreshed, systemPrompt: "" };
        agentStudioOriginalAgent.value = { ...draftAgent.value };
        agentStudioInitialSnapshot.value = serializeAgentDraft(draftAgent.value);
      }
      weavePreviewAgent.value = null;
      showAgentToast(`已采纳为新版本（版本 ${accepted.revisionNo || ""}）。`);
      void focusAgentStudio();
    } catch (error) {
      showAgentToast(agentActionErrorMessage(error), "error");
    } finally {
      acceptingPromptRevision.value = false;
    }
  }

  function cancelWeavePreview() {
    weavePreviewAgent.value = null;
    void focusAgentStudio();
  }

  async function persistDraftAgent(agent: Agent) {
    if (studioMode.value === "edit") {
      const updated = await agents.updateAgent(agent.id, agent);
      await loadAgentRegistry();
      agents.selectedAgentId = updated.id;
      showAgentToast(`${updated.name} 已保存，状态将由运行时信号继续自动汇总。`);
      agentStudioInitialSnapshot.value = serializeAgentDraft(updated);
      agentStudioOriginalAgent.value = { ...updated };
      closeStudio();
      return;
    }

    const result = await agents.createAgent(agent, {
      sourcePromptPreviewRunId: sourcePromptPreviewRunId.value || undefined,
    });
    // Create API does not accept contextPolicy; apply via update when set.
    let saved = result.agent;
    const policyPayload = buildContextPolicyPayload(agent.contextPolicy);
    if (Object.keys(policyPayload).length > 0) {
      saved = await agents.updateAgent(result.agent.id, {
        ...result.agent,
        contextPolicy: agent.contextPolicy,
      });
    }
    sourcePromptPreviewRunId.value = "";
    await loadAgentRegistry({ page: 1 });
    agents.selectedAgentId = saved.id;
    showAgentToast(`${saved.name} 已创建。`);
    agentStudioInitialSnapshot.value = serializeAgentDraft(saved);
    closeStudio();
  }

  async function openPromptDetail(agent: Agent) {
    lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    clearCurrentPromptState();
    promptDetailAgent.value = agent;
    currentPromptLoading.value = true;
    currentPromptError.value = "";
    const controller = new AbortController();
    currentPromptAbort.value = controller;
    void nextTick(() => focusDialog(promptDetailDialogRef.value));
    try {
      const current = await agents.fetchCurrentPrompt(agent, controller.signal);
      if (promptDetailAgent.value?.id !== agent.id) return;
      currentPromptBody.value = current.systemPrompt;
      currentPromptMeta.value = {
        revisionId: current.revisionId,
        revisionNo: current.revisionNo,
        source: current.source,
        createdAt: current.createdAt,
      };
    } catch (error) {
      if (controller.signal.aborted) return;
      currentPromptError.value = agentActionErrorMessage(error);
      currentPromptBody.value = "";
      currentPromptMeta.value = null;
    } finally {
      if (currentPromptAbort.value === controller) {
        currentPromptLoading.value = false;
        currentPromptAbort.value = null;
      }
    }
  }

  function clearCurrentPromptState() {
    currentPromptAbort.value?.abort();
    currentPromptAbort.value = null;
    currentPromptBody.value = "";
    currentPromptMeta.value = null;
    currentPromptLoading.value = false;
    currentPromptError.value = "";
  }

  function closePromptDetail() {
    promptDetailAgent.value = null;
    clearCurrentPromptState();
    restoreLastFocus();
  }

  // Clear create-preview source when Prompt or Workspace changes; model-only change keeps source.
  watch(
    () => [draftAgent.value.systemPrompt, draftAgent.value.workspaceId] as const,
    () => {
      if (sourcePromptPreviewRunId.value) {
        sourcePromptPreviewRunId.value = "";
      }
    },
  );

  async function confirmPromptSaveReview() {
    const agent = pendingPromptSaveReview.value;
    if (!agent || savingAgent.value) return;
    pendingPromptSaveReview.value = null;
    savingAgent.value = true;
    try {
      await persistDraftAgent(agent);
    } catch {
      showAgentToast("Agent 保存失败，请检查业务空间、模型配置和网络后重试。", "error");
    } finally {
      savingAgent.value = false;
    }
  }

  function cancelPromptSaveReview() {
    pendingPromptSaveReview.value = null;
    void focusAgentStudio();
  }

  async function saveDraftAgent() {
    if (savingAgent.value) return;
    if (!canSaveAgent.value) {
      agentStudioInlineWarning.value = isAgentDraftValid.value
        ? "请先修改至少一项 Agent 配置后再提交。"
        : "请补全必填信息后再提交 Agent。";
      return;
    }
    if (isProductionPromptChange(draftAgent.value)) {
      pendingPromptSaveReview.value = { ...draftAgent.value };
      void nextTick(() => focusDialog(promptDetailDialogRef.value));
      return;
    }
    savingAgent.value = true;
    try {
      await persistDraftAgent(draftAgent.value);
    } catch {
      showAgentToast("Agent 保存失败，请检查业务空间、模型配置和网络后重试。", "error");
    } finally {
      savingAgent.value = false;
    }
  }

  function agentMenuActions(agent: Agent): ManagementRowAction[] {
    const deletable = canDeleteAgent(agent);
    return [
      { key: "debug", label: "编辑", icon: "fa-solid fa-pen", tone: "primary" },
      { key: "capabilities", label: "管理能力绑定", icon: "fa-solid fa-link" },
      {
        key: "delete",
        label: "删除",
        icon: "fa-solid fa-trash",
        tone: "danger",
        disabled: !deletable,
        disabledReason: deletable ? undefined : "默认 Agent 不能删除",
      },
    ];
  }

  function handleAgentRowAction(actionKey: string, agent: Agent) {
    if (actionKey === "debug") {
      enterEditMode(agent);
      return;
    }
    if (actionKey === "enhance") {
      void markPromptEnhanced(agent);
      return;
    }
    if (actionKey === "capabilities") {
      void openCapabilityBindings(agent);
      return;
    }
    if (actionKey === "delete") deleteAgent(agent);
  }

  function deleteAgent(agent: Agent) {
    if (!canDeleteAgent(agent)) {
      showAgentToast("默认 Agent 不能删除，可在编辑中调整职责和系统提示词。", "error");
      return;
    }
    lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    agentDeleteTarget.value = agent;
    agentDeleteConfirmName.value = "";
    clearAgentToast();
    window.removeEventListener("keydown", handleAgentDeleteDialogKeydown);
    window.addEventListener("keydown", handleAgentDeleteDialogKeydown);
    void nextTick(() => focusDialog(agentDeleteDialogRef.value, agentDeleteInputRef.value));
  }

  function closeAgentDeleteConfirm() {
    window.removeEventListener("keydown", handleAgentDeleteDialogKeydown);
    agentDeleteTarget.value = null;
    agentDeleteConfirmName.value = "";
    restoreLastFocus();
  }

  function requestCloseAgentDeleteConfirm(_source: "backdrop" | "keyboard") {
    if (!agentDeleteTarget.value || agentDeleting.value) return;
    if (isAgentDeleteConfirmDirty.value) {
      showAgentToast(
        "为避免误关闭，已禁用当前删除确认弹框的遮罩和 Esc 关闭。请使用取消或右上角关闭明确放弃删除。",
        "error",
      );
      return;
    }
    closeAgentDeleteConfirm();
  }

  function handleAgentDeleteDialogKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape" || !agentDeleteTarget.value) return;
    event.preventDefault();
    event.stopPropagation();
    requestCloseAgentDeleteConfirm("keyboard");
  }

  async function confirmDeleteAgent() {
    if (agentDeleting.value || !canConfirmAgentDelete.value) return;
    const agent = agentDeleteTarget.value;
    if (!agent) return;
    agentDeleting.value = true;
    try {
      await agents.deleteAgent(agent.id);
      const page =
        agents.pageItems.length === 0 && agents.pagination.page > 1
          ? agents.pagination.page - 1
          : agents.pagination.page;
      await loadAgentRegistry({ page });
      showAgentToast(`${agent.name} 已从 Agent 列表移除。`);
      closeAgentDeleteConfirm();
    } catch {
      showAgentToast("默认 Agent 不能删除，可在编辑中调整职责和系统提示词。", "error");
    } finally {
      agentDeleting.value = false;
    }
  }

  async function openCapabilityBindings(agent: Agent) {
    capabilityAgent.value = agent;
    capabilityLoading.value = true;
    capabilityDrafts.value = {};
    capabilitySelectedIds.value = [];
    capabilityBatchBusy.value = false;
    try {
      const [catalog, bindings] = await Promise.all([
        agents.loadCapabilities(agent.workspaceId),
        agents.loadAgentCapabilities(agent),
      ]);
      const existing = new Map(bindings.map((binding) => [binding.capabilityId, binding]));
      capabilityDrafts.value = Object.fromEntries(
        catalog.map((capability) => {
          const binding = existing.get(capability.id);
          return [
            capability.id,
            binding
              ? { ...binding, configOverrides: { ...binding.configOverrides } }
              : {
                  capabilityId: capability.id,
                  versionPolicy: "FOLLOW_ACTIVE",
                  pinnedReleaseId: undefined,
                  connectionId: undefined,
                  executionPolicyId: undefined,
                  enabled: true,
                  configOverrides: {},
                  lockVersion: 0,
                },
          ];
        }),
      );
    } catch {
      showAgentToast("能力目录或绑定加载失败，请稍后重试。", "error");
      capabilityAgent.value = null;
    } finally {
      capabilityLoading.value = false;
    }
  }

  function closeCapabilityBindings() {
    if (capabilitySavingId.value || capabilityBatchBusy.value) return;
    capabilityAgent.value = null;
    capabilityDrafts.value = {};
    capabilitySelectedIds.value = [];
  }

  function currentCapabilityBinding(capabilityId: string) {
    const agent = capabilityAgent.value;
    return agent
      ? (agents.bindingsByAgent[agent.id] || []).find((binding) => binding.capabilityId === capabilityId)
      : undefined;
  }

  function setCapabilityVersionPolicy(capability: CapabilityCatalogItem, value: string) {
    const draft = capabilityDrafts.value[capability.id];
    if (!draft) return;
    draft.versionPolicy = value === "PINNED" ? "PINNED" : "FOLLOW_ACTIVE";
    draft.pinnedReleaseId = draft.versionPolicy === "PINNED" ? capability.activeReleaseId : undefined;
  }

  function capabilityVersionPolicyOptions(capability: CapabilityCatalogItem) {
    return [
      { label: "跟随当前生效版本", value: "FOLLOW_ACTIVE" },
      { label: "固定指定版本", value: "PINNED", disabled: !capability.activeReleaseId },
    ];
  }

  function isCapabilitySelected(capabilityId: string) {
    return capabilitySelectedIds.value.includes(capabilityId);
  }

  function toggleCapabilitySelection(capabilityId: string, checked?: boolean) {
    const next = checked ?? !isCapabilitySelected(capabilityId);
    if (next) {
      if (!capabilitySelectedIds.value.includes(capabilityId)) {
        capabilitySelectedIds.value = [...capabilitySelectedIds.value, capabilityId];
      }
      return;
    }
    capabilitySelectedIds.value = capabilitySelectedIds.value.filter((id) => id !== capabilityId);
  }

  function clearCapabilitySelection() {
    capabilitySelectedIds.value = [];
  }

  function selectUnboundCapabilities() {
    capabilitySelectedIds.value = capabilityCatalog.value
      .filter((capability) => !currentCapabilityBinding(capability.id) && canBindCapability(capability).ok)
      .map((capability) => capability.id);
  }

  function selectAllCapabilities() {
    capabilitySelectedIds.value = capabilityCatalog.value.map((capability) => capability.id);
  }

  function canBindCapability(capability: CapabilityCatalogItem): { ok: true } | { ok: false; reason: string } {
    const draft = capabilityDrafts.value[capability.id];
    if (!draft) return { ok: false, reason: "无草稿" };
    if (draft.versionPolicy === "PINNED" && !capability.activeReleaseId) {
      return { ok: false, reason: "无固定版本" };
    }
    // P3.3 FE guard: unpublished WORKFLOW (no active release) cannot form a binding.
    if (capability.kind === "WORKFLOW" && !capability.activeReleaseId) {
      return { ok: false, reason: "Workflow 未发布" };
    }
    return { ok: true };
  }

  const capabilitySelectedCount = computed(() => capabilitySelectedIds.value.length);
  const capabilityUnboundCount = computed(
    () => capabilityCatalog.value.filter((capability) => !currentCapabilityBinding(capability.id)).length,
  );
  const capabilityBindableUnboundCount = computed(
    () =>
      capabilityCatalog.value.filter(
        (capability) => !currentCapabilityBinding(capability.id) && canBindCapability(capability).ok,
      ).length,
  );
  const capabilitySelectedBoundCount = computed(
    () => capabilitySelectedIds.value.filter((id) => Boolean(currentCapabilityBinding(id))).length,
  );
  const capabilitySelectedUnboundCount = computed(
    () => capabilitySelectedIds.value.filter((id) => !currentCapabilityBinding(id)).length,
  );
  const capabilityActionsBusy = computed(() => Boolean(capabilitySavingId.value) || capabilityBatchBusy.value);

  async function saveCapabilityBinding(capability: CapabilityCatalogItem) {
    const agent = capabilityAgent.value;
    const draft = capabilityDrafts.value[capability.id];
    if (!agent || !draft || capabilityActionsBusy.value) return;
    const gate = canBindCapability(capability);
    if (!gate.ok) {
      if (capability.kind === "WORKFLOW" && !capability.activeReleaseId) {
        showAgentToast("该 Workflow 尚未发布，无法绑定。请先完成编译 → 试运行 → 发布。", "error");
      } else {
        showAgentToast(`无法绑定：${gate.reason}。`, "error");
      }
      return;
    }
    capabilitySavingId.value = capability.id;
    try {
      const saved = await agents.bindCapability(agent, capability.id, {
        ...draft,
        pinnedReleaseId: draft.versionPolicy === "PINNED" ? capability.activeReleaseId : undefined,
      });
      capabilityDrafts.value = { ...capabilityDrafts.value, [capability.id]: saved };
      await Promise.all([agents.loadCapabilities(agent.workspaceId), loadAgentRegistry()]);
      showAgentToast(`${capability.name} 绑定已保存。`);
    } catch {
      showAgentToast(`${capability.name} 绑定保存失败，请检查版本、连接和数据版本。`, "error");
    } finally {
      capabilitySavingId.value = "";
    }
  }

  async function removeCapabilityBinding(capability: CapabilityCatalogItem) {
    const agent = capabilityAgent.value;
    const binding = currentCapabilityBinding(capability.id);
    if (!agent || !binding || capabilityActionsBusy.value) return;
    capabilitySavingId.value = capability.id;
    try {
      await agents.unbindCapability(agent, binding);
      capabilityDrafts.value = {
        ...capabilityDrafts.value,
        [capability.id]: {
          capabilityId: capability.id,
          versionPolicy: "FOLLOW_ACTIVE",
          enabled: true,
          configOverrides: {},
          lockVersion: 0,
        },
      };
      await Promise.all([agents.loadCapabilities(agent.workspaceId), loadAgentRegistry()]);
      showAgentToast(`${capability.name} 已解除绑定。`);
    } catch {
      showAgentToast(`${capability.name} 解绑失败，请刷新后重试。`, "error");
    } finally {
      capabilitySavingId.value = "";
    }
  }

  /**
   * Batch-bind selected (or all unbound) capabilities using each row's draft settings.
   * Sequential PUTs so lockVersion stays consistent; reports success/skip/fail counts.
   */
  async function batchBindCapabilities(options: { mode: "selected" | "all-unbound" }) {
    const agent = capabilityAgent.value;
    if (!agent || capabilityActionsBusy.value) return;

    const targets =
      options.mode === "all-unbound"
        ? capabilityCatalog.value.filter(
            (capability) => !currentCapabilityBinding(capability.id) && canBindCapability(capability).ok,
          )
        : capabilityCatalog.value.filter((capability) => {
            if (!capabilitySelectedIds.value.includes(capability.id)) return false;
            // Allow re-bind (update) for selected already-bound items too.
            return canBindCapability(capability).ok;
          });

    if (!targets.length) {
      showAgentToast(options.mode === "all-unbound" ? "没有可绑定的未绑定能力。" : "请先勾选可绑定的能力。", "error");
      return;
    }

    capabilityBatchBusy.value = true;
    let success = 0;
    let failed = 0;
    let lastError = "";
    try {
      for (const capability of targets) {
        const draft = capabilityDrafts.value[capability.id];
        if (!draft) {
          failed += 1;
          continue;
        }
        capabilitySavingId.value = capability.id;
        try {
          const saved = await agents.bindCapability(agent, capability.id, {
            ...draft,
            pinnedReleaseId: draft.versionPolicy === "PINNED" ? capability.activeReleaseId : undefined,
          });
          capabilityDrafts.value = { ...capabilityDrafts.value, [capability.id]: saved };
          success += 1;
        } catch (error) {
          failed += 1;
          lastError = error instanceof Error ? error.message : String(error);
        }
      }
      await Promise.all([agents.loadCapabilities(agent.workspaceId), loadAgentRegistry()]);
      if (failed === 0) {
        showAgentToast(`已批量绑定 ${success} 个能力。`);
        if (options.mode === "selected") {
          capabilitySelectedIds.value = [];
        }
      } else {
        showAgentToast(
          `批量绑定完成：成功 ${success}，失败 ${failed}${lastError ? `。${lastError}` : ""}`,
          success > 0 ? "success" : "error",
        );
      }
    } finally {
      capabilitySavingId.value = "";
      capabilityBatchBusy.value = false;
    }
  }

  async function batchUnbindCapabilities() {
    const agent = capabilityAgent.value;
    if (!agent || capabilityActionsBusy.value) return;

    const targets = capabilityCatalog.value.filter((capability) => {
      if (!capabilitySelectedIds.value.includes(capability.id)) return false;
      return Boolean(currentCapabilityBinding(capability.id));
    });
    if (!targets.length) {
      showAgentToast("请先勾选已绑定的能力。", "error");
      return;
    }

    capabilityBatchBusy.value = true;
    let success = 0;
    let failed = 0;
    try {
      for (const capability of targets) {
        const binding = currentCapabilityBinding(capability.id);
        if (!binding) continue;
        capabilitySavingId.value = capability.id;
        try {
          await agents.unbindCapability(agent, binding);
          capabilityDrafts.value = {
            ...capabilityDrafts.value,
            [capability.id]: {
              capabilityId: capability.id,
              versionPolicy: "FOLLOW_ACTIVE",
              enabled: true,
              configOverrides: {},
              lockVersion: 0,
            },
          };
          success += 1;
        } catch {
          failed += 1;
        }
      }
      await Promise.all([agents.loadCapabilities(agent.workspaceId), loadAgentRegistry()]);
      capabilitySelectedIds.value = capabilitySelectedIds.value.filter(
        (id) => !targets.some((capability) => capability.id === id) || Boolean(currentCapabilityBinding(id)),
      );
      if (failed === 0) {
        showAgentToast(`已批量解绑 ${success} 个能力。`);
        capabilitySelectedIds.value = [];
      } else {
        showAgentToast(`批量解绑完成：成功 ${success}，失败 ${failed}`, success > 0 ? "success" : "error");
      }
    } finally {
      capabilitySavingId.value = "";
      capabilityBatchBusy.value = false;
    }
  }

  function loadAgentRegistry(overrides: AgentListQuery = {}) {
    return agents.loadAgentPage({
      query: overrides.query ?? query.value,
      status: overrides.status ?? (agentStatusFilter.value === "ALL" ? undefined : agentStatusFilter.value),
      workspaceId: overrides.workspaceId ?? activeWorkspaceFilterId(),
      page: overrides.page ?? agents.pagination.page,
      pageSize: overrides.pageSize ?? agents.pagination.pageSize,
      ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
    });
  }

  function setAgentSearch(value: string) {
    query.value = value;
    void loadAgentRegistry({ query: value, page: 1 });
  }

  function changeAgentPage(pagination: { page: number; pageSize: number }) {
    void loadAgentRegistry(pagination);
  }

  function changeAgentSort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
    void loadAgentRegistry({
      page: 1,
      pageSize: agents.pagination.pageSize,
      sortBy: sort.sortBy ?? "",
      sortOrder: sort.sortOrder,
    });
  }

  return {
    agents,
    workspaces,
    canEditWorkspace,
    canDeleteWorkspace,
    modelConfigs,
    query,
    agentStatusFilter,
    studioMode,
    draftAgent,
    agentActionNote,
    agentActionTone,
    enhancingAgentId,
    promptDetailAgent,
    pageInitialLoading,
    savingAgent,
    agentDeleting,
    agentDeleteTarget,
    agentDeleteConfirmName,
    agentToastTimer,
    agentStudioPanelRef,
    agentNameInputRef,
    promptDetailDialogRef,
    agentDeleteDialogRef,
    agentDeleteInputRef,
    lastFocusBeforeModal,
    agentStudioInitialSnapshot,
    agentStudioOriginalAgent,
    agentStudioInlineWarning,
    pendingPromptSaveReview,
    weavePreviewAgent,
    sourcePromptPreviewRunId,
    currentPromptBody,
    currentPromptMeta,
    currentPromptLoading,
    currentPromptError,
    acceptingPromptRevision,
    capabilityAgent,
    capabilityLoading,
    capabilitySavingId,
    capabilityDrafts,
    statusSourceText,
    selectedAgent,
    hasAgentRecords,
    agentSummaryItems,
    workspaceOptions,
    modelConfigOptions,
    promptDetailVisible,
    studioTitle,
    promptDetailHTML,
    agentManagementFilterOptions,
    agentColumns,
    isAgentDeleteConfirmDirty,
    isAgentStudioDirty,
    agentNameError,
    agentWorkspaceError,
    agentModelError,
    agentRoleError,
    agentPromptError,
    promptLineCount,
    promptPreviewText,
    isAgentDraftValid,
    canSaveAgent,
    originalPrompt,
    promptSaveDiff,
    pendingPromptText,
    weavePreviewDiff,
    capabilityCatalog,
    capabilityBatchBusy,
    capabilitySelectedIds,
    capabilitySelectedCount,
    capabilityUnboundCount,
    capabilityBindableUnboundCount,
    capabilitySelectedBoundCount,
    capabilitySelectedUnboundCount,
    capabilityActionsBusy,
    agentSaveButtonLabel,
    canEnhanceDraftPrompt,
    canConfirmAgentDelete,
    agentDeleteNameError,
    newAgent,
    serializeAgentDraft,
    workspaceById,
    modelConfigById,
    workspaceLabel,
    modelLabel,
    statusLabel,
    formatAgentUpdatedAt,
    modelConfigOptionLabel,
    renderPromptMarkdown,
    buildPromptDiffSummary,
    formatSignedDelta,
    isProductionPromptChange,
    agentActionErrorMessage,
    statusTone,
    isEnhancing,
    canDeleteAgent,
    selectAgent,
    resetFilters,
    setAgentStatusFilter,
    activeWorkspaceFilterId,
    toggleDraftStatus,
    agentContextMode,
    agentContextMaxInputTokens,
    agentContextMaxRecentTurns,
    agentContextSummaryMaxTokens,
    agentContextSummaryMinEvictedTurns,
    agentContextSummaryMaxPasses,
    agentContextIncludeCompactionSummary,
    agentContextAdvancedOpen,
    toggleAgentContextAdvanced,
    setAgentContextMode,
    setAgentContextMaxInput,
    setAgentContextMaxTurns,
    setAgentContextSummaryMaxTokens,
    setAgentContextSummaryMinEvictedTurns,
    setAgentContextSummaryMaxPasses,
    setAgentContextIncludeCompactionSummary,
    enterCreateMode,
    enterEditMode,
    closeStudio,
    exitStudio,
    requestCloseStudio,
    openPromptDetail,
    closePromptDetail,
    focusAgentStudio,
    focusDialog,
    restoreLastFocus,
    activeAgentModal,
    trapAgentModalFocus,
    showAgentToast,
    clearAgentToast,
    enhancePrompt,
    markPromptEnhanced,
    applyWeavePreview,
    cancelWeavePreview,
    persistDraftAgent,
    confirmPromptSaveReview,
    cancelPromptSaveReview,
    saveDraftAgent,
    agentMenuActions,
    handleAgentRowAction,
    deleteAgent,
    closeAgentDeleteConfirm,
    requestCloseAgentDeleteConfirm,
    handleAgentDeleteDialogKeydown,
    confirmDeleteAgent,
    openCapabilityBindings,
    closeCapabilityBindings,
    currentCapabilityBinding,
    setCapabilityVersionPolicy,
    capabilityVersionPolicyOptions,
    isCapabilitySelected,
    toggleCapabilitySelection,
    clearCapabilitySelection,
    selectUnboundCapabilities,
    selectAllCapabilities,
    canBindCapability,
    saveCapabilityBinding,
    removeCapabilityBinding,
    batchBindCapabilities,
    batchUnbindCapabilities,
    loadAgentRegistry,
    setAgentSearch,
    changeAgentPage,
    changeAgentSort,
  };
}
