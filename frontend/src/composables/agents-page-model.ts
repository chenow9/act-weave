import { tt } from "../i18n/tt";
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
  aapFlagBag,
  anyAapFlagTrue,
  buildContextPolicyPayload,
  DEFAULT_ROLLING_SUMMARY_MAX_RECENT_TURNS,
  defaultRollingSummary,
  mergeAapFlags,
  needsSessionContextV2,
  normalizeContextPolicy,
  readAapFlags,
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

  const statusSourceText = computed(() => tt("agents.statusSource"));

  const selectedAgent = computed(() => agents.selectedAgent || agents.items[0] || null);
  const hasAgentRecords = computed(() => agents.items.length > 0);
  const agentSummaryItems = computed<ManagementSummaryItem[]>(() => {
    const total = agents.items.length;
    const active = agents.items.filter((agent) => agent.status === "ACTIVE").length;
    const paused = agents.items.filter((agent) => agent.status === "DISABLED").length;
    const modelCount = new Set(agents.items.map((agent) => agent.modelConfigId).filter(Boolean)).size;
    return [
      { label: tt("agents.summaryTotal"), value: total, icon: "fa-solid fa-user-gear" },
      {
        label: tt("agents.summaryRunning"),
        value: active,
        note: total ? `${((active / total) * 100).toFixed(1)}%` : "0%",
        icon: "fa-solid fa-circle-check",
      },
      { label: tt("agents.summaryPaused"), value: paused, icon: "fa-solid fa-circle-pause", tone: "warning" },
      { label: tt("agents.summaryModels"), value: modelCount, icon: "fa-solid fa-brain" },
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
  const studioTitle = computed(() =>
    studioMode.value === "create" ? tt("agents.createTitle") : tt("agents.editTitle"),
  );
  const promptDetailHTML = computed(() => renderPromptMarkdown(currentPromptBody.value || ""));
  const agentManagementFilterOptions = computed<Array<{ label: string; value: AgentStatusFilter }>>(() => [
    { label: tt("agents.filterAll"), value: "ALL" },
    { label: tt("agents.summaryRunning"), value: "ACTIVE" },
    { label: tt("agents.filterPaused"), value: "DISABLED" },
  ]);
  const agentColumns = computed<ManagementListColumn<Agent>[]>(() => [
    {
      key: "identity",
      label: tt("agents.colIdentity"),
      width: 286,
      sortable: true,
      sortKey: "name",
      getValue: (agent) => `${agent.name} ${agent.roleDescription}`,
    },
    {
      key: "workspace",
      label: tt("agents.colWorkspace"),
      width: 190,
      hidable: true,
      sortable: true,
      sortKey: "workspace",
      getValue: workspaceLabel,
    },
    {
      key: "model",
      label: tt("agents.colModel"),
      width: 180,
      hidable: true,
      sortable: true,
      sortKey: "model",
      getValue: modelLabel,
    },
    {
      key: "prompt",
      label: tt("agents.colPrompt"),
      width: 150,
      hidable: true,
      getValue: (agent) => agent.currentPromptRevisionId || "-",
    },
    {
      key: "status",
      label: tt("agents.colStatus"),
      width: 140,
      hidable: true,
      sortable: true,
      sortKey: "status",
      getValue: (agent) => statusLabel(agent.status),
    },
    {
      key: "updatedAt",
      label: tt("agents.colUpdated"),
      width: 130,
      hidable: true,
      sortable: true,
      sortKey: "updatedAt",
      getValue: formatAgentUpdatedAt,
    },
    { key: "actions", label: tt("agents.colActions"), width: 68, align: "right", headerAlign: "center" },
  ]);
  const isAgentDeleteConfirmDirty = computed(() => agentDeleteConfirmName.value.trim().length > 0);
  const isAgentStudioDirty = computed(() => {
    return Boolean(studioMode.value && serializeAgentDraft(draftAgent.value) !== agentStudioInitialSnapshot.value);
  });
  const agentNameError = computed(() => (draftAgent.value.name.trim() ? "" : tt("agents.nameRequired")));
  const agentWorkspaceError = computed(() => (draftAgent.value.workspaceId ? "" : tt("agents.workspaceRequired")));
  const agentModelError = computed(() => (draftAgent.value.modelConfigId ? "" : tt("agents.modelRequired")));
  const agentRoleError = computed(() => (draftAgent.value.roleDescription.trim() ? "" : tt("agents.roleRequired")));
  const agentPromptError = computed(() =>
    studioMode.value === "create" && !draftAgent.value.systemPrompt.trim() ? tt("agents.promptRequired") : "",
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
    if (!value) return tt("agents.noPromptContent");
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
    if (savingAgent.value) return studioMode.value === "create" ? tt("agents.creating") : tt("agents.saving");
    return studioMode.value === "create" ? tt("agents.createAgentBtn") : tt("agents.saveAgentBtn");
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
    return tt("agents.confirmNameHint", { name: agent.name });
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
      name: tt("agents.defaultRunName"),
      roleDescription: tt("agents.defaultRoleDescription"),
      modelConfigId: workspace?.defaultModelConfigId || modelConfigs.items[0]?.id || "",
      systemPrompt: tt("agents.defaultSystemPrompt"),
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

  function probeModelToolCalling(
    config: ModelApiConfig | undefined,
  ): "native_client_search" | "function_calling" | "none" | "unverified" {
    const caps = config?.agenticCapabilities;
    if (!caps || typeof caps !== "object") return "unverified";
    const rec = caps as Record<string, unknown>;
    if (Object.keys(rec).length === 0) return "unverified";
    if (rec.schemaVersion === "agentic-model.v1") return "native_client_search";
    const calling = rec.toolCalling;
    if (calling === "none" || calling === "function_calling" || calling === "native_client_search") {
      return calling;
    }
    return "unverified";
  }

  function modelCannotCallTools(config: ModelApiConfig | undefined): boolean {
    return probeModelToolCalling(config) === "none";
  }

  const selectedDraftModelCannotCallTools = computed(() =>
    modelCannotCallTools(modelConfigById(draftAgent.value.modelConfigId)),
  );
  const capabilityModelCannotCallTools = computed(() =>
    modelCannotCallTools(modelConfigById(capabilityAgent.value?.modelConfigId || "")),
  );

  function workspaceLabel(agent: Agent) {
    return workspaceById(agent.workspaceId)?.name || agent.workspaceId;
  }

  function modelLabel(agent: Agent) {
    return modelConfigById(agent.modelConfigId)?.modelName || agent.modelConfigId;
  }

  function statusLabel(status: string) {
    return status === "ACTIVE" ? tt("agents.running") : tt("agents.paused");
  }

  function formatAgentUpdatedAt(agent: Agent) {
    if (!agent.updatedAt) return "-";
    const timestamp = Date.parse(agent.updatedAt);
    if (!Number.isFinite(timestamp)) return agent.updatedAt;
    const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
    if (elapsedMinutes < 1) return tt("agents.justNow");
    if (elapsedMinutes < 60) return tt("agents.minutesAgo", { n: elapsedMinutes });
    const elapsedHours = Math.floor(elapsedMinutes / 60);
    if (elapsedHours < 24) return tt("agents.hoursAgo", { n: elapsedHours });
    return tt("agents.daysAgo", { n: Math.floor(elapsedHours / 24) });
  }

  function modelConfigOptionLabel(config: ModelApiConfig) {
    const suffix =
      config.id === workspaceById(draftAgent.value.workspaceId)?.modelConfigId
        ? tt("agents.workspaceDefaultSuffix")
        : "";
    const noneHint = modelCannotCallTools(config) ? ` · ${tt("agents.modelCannotCallToolsHint")}` : "";
    return `${config.modelName}${suffix}${noneHint}`;
  }

  function renderPromptMarkdown(source: string) {
    return renderMarkdown(source, tt("agents.emptyPromptMarkdown"));
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
      return tt("agents.enhanceFailedWithError", { error: responseError });
    }
    if (timeoutCode === "ECONNABORTED") {
      return tt("agents.enhanceFailedTimeout");
    }
    return tt("agents.enhanceFailedGeneric");
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
  const agentContextEnableA2UI = computed(() => Boolean(draftContextPolicy().aap?.enableA2UI));
  const agentContextAdvancedOpen = ref(false);

  function toggleAgentContextAdvanced() {
    agentContextAdvancedOpen.value = !agentContextAdvancedOpen.value;
  }

  /** Preserve aap flag bag + force v2 when any flag/aap requires it (never v1+aap). */
  function withPreservedAap(current: SessionContextPolicy, patch: Partial<SessionContextPolicy>): SessionContextPolicy {
    const flags = readAapFlags(current.aap);
    const next: SessionContextPolicy = { ...current, ...patch };
    if (needsSessionContextV2(current, flags) || needsSessionContextV2(next, flags)) {
      next.schemaVersion = "session-context-policy.v2";
      next.aap = aapFlagBag(flags);
    } else {
      next.schemaVersion = next.schemaVersion || "session-context-policy.v1";
      if (next.aap) delete next.aap;
    }
    return next;
  }

  function setAgentContextMode(mode: string) {
    const value = String(mode || "").trim();
    const flags = readAapFlags(draftContextPolicy().aap);
    const aapSlice = anyAapFlagTrue(flags)
      ? { schemaVersion: "session-context-policy.v2" as const, aap: aapFlagBag(flags) }
      : {};
    if (!value || value === "inherit") {
      draftAgent.value.contextPolicy = anyAapFlagTrue(flags)
        ? { schemaVersion: "session-context-policy.v2", mode: undefined, aap: aapFlagBag(flags) }
        : { mode: undefined };
      return;
    }
    if (value === "disabled") {
      draftAgent.value.contextPolicy = anyAapFlagTrue(flags)
        ? {
            schemaVersion: "session-context-policy.v2",
            mode: "disabled",
            aap: aapFlagBag(flags),
          }
        : {
            schemaVersion: "session-context-policy.v1",
            mode: "disabled",
          };
      return;
    }
    if (value !== "token_window" && value !== "rolling_summary") return;
    const current = draftContextPolicy();
    const schemaVersion = anyAapFlagTrue(flags) ? "session-context-policy.v2" : "session-context-policy.v1";
    if (value === "token_window") {
      draftAgent.value.contextPolicy = {
        schemaVersion,
        mode: "token_window",
        maxInputTokens: current.maxInputTokens ?? 0,
        maxRecentTurns: current.maxRecentTurns ?? 0,
        ...(current.outputReserveTokens != null ? { outputReserveTokens: current.outputReserveTokens } : {}),
        ...(current.safetyMarginTokens != null ? { safetyMarginTokens: current.safetyMarginTokens } : {}),
        ...aapSlice,
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
      ...aapSlice,
    };
  }

  function setAgentContextMaxInput(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "token_window" && current.mode !== "rolling_summary") return;
    draftAgent.value.contextPolicy = withPreservedAap(current, {
      maxInputTokens: Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0,
    });
  }

  function setAgentContextMaxTurns(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "token_window" && current.mode !== "rolling_summary") return;
    draftAgent.value.contextPolicy = withPreservedAap(current, {
      maxRecentTurns: Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0,
    });
  }

  function setAgentContextSummaryMaxTokens(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "rolling_summary") return;
    const summary = defaultRollingSummary(current.summary);
    draftAgent.value.contextPolicy = withPreservedAap(current, {
      summary: {
        ...summary,
        maxTokens: Number.isFinite(value) && value > 0 ? Math.floor(value) : summary.maxTokens,
      },
    });
  }

  function setAgentContextSummaryMinEvictedTurns(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "rolling_summary") return;
    const summary = defaultRollingSummary(current.summary);
    draftAgent.value.contextPolicy = withPreservedAap(current, {
      summary: {
        ...summary,
        minEvictedTurns: Number.isFinite(value) && value >= 0 ? Math.floor(value) : summary.minEvictedTurns,
      },
    });
  }

  function setAgentContextSummaryMaxPasses(value: number) {
    const current = draftContextPolicy();
    if (current.mode !== "rolling_summary") return;
    const summary = defaultRollingSummary(current.summary);
    draftAgent.value.contextPolicy = withPreservedAap(current, {
      summary: {
        ...summary,
        maxGenerationPasses: Number.isFinite(value) && value > 0 ? Math.floor(value) : summary.maxGenerationPasses,
      },
    });
  }

  /** T4-B Agent-only AAP disclosure; forces policy v2; preserves enableA2UI (KD-14). */
  function setAgentContextIncludeCompactionSummary(value: boolean) {
    const current = draftContextPolicy();
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v2",
      aap: aapFlagBag(mergeAapFlags(current.aap, { includeCompactionSummary: Boolean(value) })),
    };
  }

  /** Agent-only A2UI capability; forces policy v2; preserves includeCompactionSummary (KD-14). */
  function setAgentContextEnableA2UI(value: boolean) {
    const current = draftContextPolicy();
    draftAgent.value.contextPolicy = {
      ...current,
      schemaVersion: "session-context-policy.v2",
      aap: aapFlagBag(mergeAapFlags(current.aap, { enableA2UI: Boolean(value) })),
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
      agentStudioInlineWarning.value = tt("agents.unsavedChangesWarning");
      showAgentToast(tt("agents.unsavedChangesToast"), "error");
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
      showAgentToast(tt("agents.enhanceNeedFields"), "error");
      return;
    }
    const isCreate = studioMode.value === "create" || !draftAgent.value.id;
    enhancingAgentId.value = draftAgent.value.id || "create-draft";
    showAgentToast(
      isCreate
        ? tt("agents.enhancePreviewGenerating")
        : tt("agents.enhancePreviewGeneratingNamed", { name: draftAgent.value.name }),
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
          showAgentToast(tt("agents.enhancePreviewStale"), "error");
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
        showAgentToast(tt("agents.enhancePreviewReadyCreate"));
      } else {
        const preview = await agents.enhanceAgentPrompt(draftAgent.value, draftAgent.value.systemPrompt, {
          preview: true,
        });
        weavePreviewAgent.value = preview;
        showAgentToast(tt("agents.enhancePreviewReadyEdit", { name: draftAgent.value.name }));
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
      showAgentToast(tt("agents.enhanceAppliedToDraft"));
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
      showAgentToast(tt("agents.enhanceAcceptedRevision", { n: accepted.revisionNo || "" }));
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
      showAgentToast(tt("agents.agentSavedNamed", { name: updated.name }));
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
    showAgentToast(tt("agents.agentCreatedNamed", { name: saved.name }));
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
      showAgentToast(tt("agents.agentSaveFailed"), "error");
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
        ? tt("agents.noChangesToSubmit")
        : tt("agents.fillRequiredFields");
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
      showAgentToast(tt("agents.agentSaveFailed"), "error");
    } finally {
      savingAgent.value = false;
    }
  }

  function agentMenuActions(agent: Agent): ManagementRowAction[] {
    const deletable = canDeleteAgent(agent);
    return [
      { key: "debug", label: tt("agents.edit"), icon: "fa-solid fa-pen", tone: "primary" },
      { key: "capabilities", label: tt("agents.manageCapabilities"), icon: "fa-solid fa-link" },
      {
        key: "delete",
        label: tt("agents.delete"),
        icon: "fa-solid fa-trash",
        tone: "danger",
        disabled: !deletable,
        disabledReason: deletable ? undefined : tt("agents.defaultNotDeletable"),
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
      showAgentToast(tt("agents.defaultAgentDeleteBlocked"), "error");
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
      showAgentToast(tt("agents.deleteConfirmEscBlocked"), "error");
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
      showAgentToast(tt("agents.agentRemovedNamed", { name: agent.name }));
      closeAgentDeleteConfirm();
    } catch {
      showAgentToast(tt("agents.defaultAgentDeleteBlocked"), "error");
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
      showAgentToast(tt("agents.capabilityLoadFailed"), "error");
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
      { label: tt("agents.versionPolicyFollowActive"), value: "FOLLOW_ACTIVE" },
      { label: tt("agents.versionPolicyPinned"), value: "PINNED", disabled: !capability.activeReleaseId },
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
    if (capabilityModelCannotCallTools.value) {
      return { ok: false, reason: tt("agents.capabilityBindDisabledNoTools") };
    }
    const draft = capabilityDrafts.value[capability.id];
    if (!draft) return { ok: false, reason: tt("agents.bindGateNoDraft") };
    if (draft.versionPolicy === "PINNED" && !capability.activeReleaseId) {
      return { ok: false, reason: tt("agents.bindGateNoPinnedVersion") };
    }
    // P3.3 FE guard: unpublished WORKFLOW (no active release) cannot form a binding.
    if (capability.kind === "WORKFLOW" && !capability.activeReleaseId) {
      return { ok: false, reason: tt("agents.bindGateWorkflowUnpublished") };
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
        showAgentToast(tt("agents.workflowNotPublishedToast"), "error");
      } else {
        showAgentToast(tt("agents.bindFailedReason", { reason: gate.reason }), "error");
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
      showAgentToast(tt("agents.bindingSavedNamed", { name: capability.name }));
    } catch {
      showAgentToast(tt("agents.bindingSaveFailedNamed", { name: capability.name }), "error");
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
      showAgentToast(tt("agents.unbindSuccessNamed", { name: capability.name }));
    } catch {
      showAgentToast(tt("agents.unbindFailedNamed", { name: capability.name }), "error");
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
      showAgentToast(
        options.mode === "all-unbound" ? tt("agents.batchNoUnbound") : tt("agents.batchSelectToBind"),
        "error",
      );
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
        showAgentToast(tt("agents.batchBoundSuccess", { n: success }));
        if (options.mode === "selected") {
          capabilitySelectedIds.value = [];
        }
      } else {
        showAgentToast(
          tt("agents.batchBoundPartial", {
            success,
            failed,
            detail: lastError ? tt("agents.batchBoundPartialDetail", { error: lastError }) : "",
          }),
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
      showAgentToast(tt("agents.batchSelectToUnbind"), "error");
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
        showAgentToast(tt("agents.batchUnboundSuccess", { n: success }));
        capabilitySelectedIds.value = [];
      } else {
        showAgentToast(tt("agents.batchUnboundPartial", { success, failed }), success > 0 ? "success" : "error");
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
    selectedDraftModelCannotCallTools,
    capabilityModelCannotCallTools,
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
    agentContextEnableA2UI,
    agentContextAdvancedOpen,
    toggleAgentContextAdvanced,
    setAgentContextMode,
    setAgentContextMaxInput,
    setAgentContextMaxTurns,
    setAgentContextSummaryMaxTokens,
    setAgentContextSummaryMinEvictedTurns,
    setAgentContextSummaryMaxPasses,
    setAgentContextIncludeCompactionSummary,
    setAgentContextEnableA2UI,
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
