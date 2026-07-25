<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import AgentPromptDiffViewer from "../components/AgentPromptDiffViewer.vue";
import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
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

type AgentStatusFilter = "ALL" | "ACTIVE" | "DISABLED";

const agents = useAgentStore();
const workspaces = useWorkspaceStore();
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
const acceptingPromptRevision = ref(false);
const capabilityAgent = ref<Agent | null>(null);
const capabilityLoading = ref(false);
const capabilitySavingId = ref("");
const capabilityDrafts = ref<Record<string, AgentCapabilityBinding>>({});

const statusSourceText = "状态由 Agent 配置维护；Tool/Workflow 数量从当前启用的 Capability Binding 只读派生。";

const selectedAgent = computed(() => agents.selectedAgent || agents.items[0] || null);
const hasAgentRecords = computed(() => agents.items.length > 0);
const agentSummaryItems = computed<ManagementSummaryItem[]>(() => {
  const total = agents.items.length;
  const active = agents.items.filter((agent) => agent.status === "ACTIVE").length;
  const paused = agents.items.filter((agent) => agent.status === "DISABLED").length;
  const modelCount = new Set(agents.items.map((agent) => agent.modelConfigId).filter(Boolean)).size;
  return [
    { label: "Agent 总数", value: total, icon: "fa-solid fa-user-gear" },
    { label: "运行中", value: active, note: total ? `${((active / total) * 100).toFixed(1)}%` : "0%", icon: "fa-solid fa-circle-check" },
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
const promptDetailHTML = computed(() => renderPromptMarkdown(promptDetailAgent.value?.systemPrompt || ""));
const agentManagementFilterOptions = computed<Array<{ label: string; value: AgentStatusFilter }>>(() => [
  { label: "全部", value: "ALL" },
  { label: "运行中", value: "ACTIVE" },
  { label: "暂停", value: "DISABLED" },
]);
const agentColumns = computed<ManagementListColumn<Agent>[]>(() => [
  { key: "identity", label: "Agent", width: 286, sortable: true, sortKey: "name", getValue: (agent) => `${agent.name} ${agent.roleDescription}` },
  { key: "workspace", label: "绑定空间", width: 190, hidable: true, sortable: true, sortKey: "workspace", getValue: workspaceLabel },
  { key: "model", label: "决策模型", width: 180, hidable: true, sortable: true, sortKey: "model", getValue: modelLabel },
  { key: "prompt", label: "Prompt Revision", width: 150, hidable: true, getValue: (agent) => agent.currentPromptRevisionId || "-" },
  { key: "status", label: "状态", width: 140, hidable: true, sortable: true, sortKey: "status", getValue: (agent) => statusLabel(agent.status) },
  { key: "updatedAt", label: "最近修改", width: 130, hidable: true, sortable: true, sortKey: "updatedAt", getValue: formatAgentUpdatedAt },
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
const agentPromptError = computed(() => (studioMode.value === "create" && !draftAgent.value.systemPrompt.trim() ? "请输入 System Prompt。" : ""));
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
  () => !agentNameError.value && !agentWorkspaceError.value && !agentModelError.value && !agentRoleError.value && !agentPromptError.value,
);
const canSaveAgent = computed(() => Boolean(studioMode.value && !savingAgent.value && isAgentStudioDirty.value && isAgentDraftValid.value));
const originalPrompt = computed(() => agentStudioOriginalAgent.value?.systemPrompt || "");
const promptSaveDiff = computed(() => buildPromptDiffSummary(originalPrompt.value, draftAgent.value.systemPrompt || ""));
const pendingPromptText = computed(() => pendingPromptSaveReview.value?.systemPrompt || draftAgent.value.systemPrompt || "");
const weavePreviewDiff = computed(() => buildPromptDiffSummary(draftAgent.value.systemPrompt || "", weavePreviewAgent.value?.output || ""));
const capabilityCatalog = computed(() => (capabilityAgent.value ? agents.capabilitiesByWorkspace[capabilityAgent.value.workspaceId] || [] : []));
const agentSaveButtonLabel = computed(() => {
  if (savingAgent.value) return studioMode.value === "create" ? "创建中..." : "保存中...";
  return studioMode.value === "create" ? "创建 Agent" : "保存 Agent";
});
const canEnhanceDraftPrompt = computed(() => Boolean(draftAgent.value.id && draftAgent.value.systemPrompt.trim() && !isEnhancing(draftAgent.value.id)));
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
  return renderMarkdown(source, "暂无 System Prompt 内容。");
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

function isProductionPromptChange(agent: Agent) {
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

function enterCreateMode() {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  draftAgent.value = newAgent();
  agentStudioOriginalAgent.value = null;
  agentStudioInitialSnapshot.value = serializeAgentDraft(draftAgent.value);
  agentStudioInlineWarning.value = "";
  pendingPromptSaveReview.value = null;
  weavePreviewAgent.value = null;
  studioMode.value = "create";
  clearAgentToast();
  void focusAgentStudio();
}

function enterEditMode(agent: Agent) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  selectAgent(agent);
  draftAgent.value = { ...agent, systemPrompt: "" };
  agentStudioOriginalAgent.value = { ...agent };
  agentStudioInitialSnapshot.value = serializeAgentDraft(draftAgent.value);
  agentStudioInlineWarning.value = "";
  pendingPromptSaveReview.value = null;
  weavePreviewAgent.value = null;
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

function requestCloseStudio(source: "backdrop" | "keyboard" | "back") {
  if (!studioMode.value || savingAgent.value) return;
  if (isAgentStudioDirty.value) {
    agentStudioInlineWarning.value = "已有未保存修改，请先创建/保存 Agent，或使用“放弃改动”明确退出。";
    showAgentToast("Agent 草稿已修改，请先创建/保存 Agent 或放弃改动，再离开当前编辑窗口。", "error");
    return;
  }
  closeStudio();
}

function openPromptDetail(agent: Agent) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  promptDetailAgent.value = agent;
  void nextTick(() => focusDialog(promptDetailDialogRef.value));
}

function closePromptDetail() {
  promptDetailAgent.value = null;
  restoreLastFocus();
}

async function focusAgentStudio() {
  await nextTick();
  const target = agentNameInputRef.value || agentStudioPanelRef.value?.querySelector<HTMLElement>('button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])');
  target?.focus();
}

async function focusDialog(dialog: HTMLElement | null, preferredTarget?: HTMLElement | null) {
  await nextTick();
  const target =
    preferredTarget ||
    dialog?.querySelector<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])');
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
    modal.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'),
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
  if (!draftAgent.value.id) {
    showAgentToast("请先创建 Agent，再使用模型整理补充 System Prompt。", "error");
    return;
  }
  enhancingAgentId.value = draftAgent.value.id;
  showAgentToast(`${draftAgent.value.name} 正在生成 System Prompt 整理预览，通常需要 1 到 2 分钟。`, "success", false);
  try {
    const preview = await agents.enhanceAgentPrompt(draftAgent.value, draftAgent.value.systemPrompt, { preview: true });
    weavePreviewAgent.value = preview;
    showAgentToast(`${draftAgent.value.name} 的 System Prompt 整理预览已生成，请审查后再采纳。`);
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
    showAgentToast(`已采纳 Prompt Revision ${accepted.revisionNo || ""}，原始输入和输出按后端保留策略永久存档。`);
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

  const created = await agents.createAgent(agent);
  await loadAgentRegistry({ page: 1 });
  agents.selectedAgentId = created.id;
  showAgentToast(`${created.name} 已创建。`);
  agentStudioInitialSnapshot.value = serializeAgentDraft(created);
  closeStudio();
}

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
    agentStudioInlineWarning.value = isAgentDraftValid.value ? "请先修改至少一项 Agent 配置后再提交。" : "请补全必填信息后再提交 Agent。";
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
    { key: "debug", label: `调试`, icon: "fa-solid fa-sliders", tone: "primary" },
    { key: "capabilities", label: "管理 Capability Binding", icon: "fa-solid fa-link" },
    {
      key: "delete",
      label: "删除 Agent",
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
    showAgentToast("默认 Agent 不能删除，可在编辑中调整职责和 Prompt。", "error");
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

function requestCloseAgentDeleteConfirm(source: "backdrop" | "keyboard") {
  if (!agentDeleteTarget.value || agentDeleting.value) return;
  if (isAgentDeleteConfirmDirty.value) {
    showAgentToast("为避免误关闭，已禁用当前删除确认弹框的遮罩和 Esc 关闭。请使用取消或右上角关闭明确放弃删除。", "error");
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
    const page = agents.pageItems.length === 0 && agents.pagination.page > 1 ? agents.pagination.page - 1 : agents.pagination.page;
    await loadAgentRegistry({ page });
    showAgentToast(`${agent.name} 已从 Agent Registry 移除。`);
    closeAgentDeleteConfirm();
  } catch {
    showAgentToast("默认 Agent 不能删除，可在编辑中调整职责和 Prompt。", "error");
  } finally {
    agentDeleting.value = false;
  }
}

async function openCapabilityBindings(agent: Agent) {
  capabilityAgent.value = agent;
  capabilityLoading.value = true;
  capabilityDrafts.value = {};
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
    showAgentToast("Capability Catalog 或 Binding 加载失败，请稍后重试。", "error");
    capabilityAgent.value = null;
  } finally {
    capabilityLoading.value = false;
  }
}

function closeCapabilityBindings() {
  if (capabilitySavingId.value) return;
  capabilityAgent.value = null;
  capabilityDrafts.value = {};
}

function currentCapabilityBinding(capabilityId: string) {
  const agent = capabilityAgent.value;
  return agent ? (agents.bindingsByAgent[agent.id] || []).find((binding) => binding.capabilityId === capabilityId) : undefined;
}

function setCapabilityVersionPolicy(capability: CapabilityCatalogItem, value: string) {
  const draft = capabilityDrafts.value[capability.id];
  if (!draft) return;
  draft.versionPolicy = value === "PINNED" ? "PINNED" : "FOLLOW_ACTIVE";
  draft.pinnedReleaseId = draft.versionPolicy === "PINNED" ? capability.activeReleaseId : undefined;
}

function capabilityVersionPolicyOptions(capability: CapabilityCatalogItem) {
  return [
    { label: "FOLLOW_ACTIVE · 跟随当前发布版", value: "FOLLOW_ACTIVE" },
    { label: "PINNED · 固定 Active Release", value: "PINNED", disabled: !capability.activeReleaseId },
  ];
}

async function saveCapabilityBinding(capability: CapabilityCatalogItem) {
  const agent = capabilityAgent.value;
  const draft = capabilityDrafts.value[capability.id];
  if (!agent || !draft || capabilitySavingId.value) return;
  if (draft.versionPolicy === "PINNED" && !capability.activeReleaseId) {
    showAgentToast("该 Capability 没有可固定的 Active Release。", "error");
    return;
  }
  // P3.3 FE guard: unpublished WORKFLOW (no active release) cannot form a binding.
  if (capability.kind === "WORKFLOW" && !capability.activeReleaseId) {
    showAgentToast("该 Workflow 尚未发布，无法绑定。请先完成 compile → trial → publish。", "error");
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
    showAgentToast(`${capability.name} Binding 已保存。`);
  } catch {
    showAgentToast(`${capability.name} Binding 保存失败，请检查 Release、Connection 和乐观锁。`, "error");
  } finally {
    capabilitySavingId.value = "";
  }
}

async function removeCapabilityBinding(capability: CapabilityCatalogItem) {
  const agent = capabilityAgent.value;
  const binding = currentCapabilityBinding(capability.id);
  if (!agent || !binding || capabilitySavingId.value) return;
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

</script>

<template>
  <div class="page-grid agent-grid management-page-grid" v-loading="pageInitialLoading" element-loading-text="正在加载 Agent Registry...">
    <ManagementPageHeader
      class="span-12"
      title="Agent 管理"
      description="维护职责、绑定空间、模型配置与 Prompt Revision。"
      icon="fa-solid fa-user-gear"
    >
      <template #actions>
        <button class="primary-button agent-create-button" type="button" @click="enterCreateMode">
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          <span>新建 Agent</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" :items="agentSummaryItems" />

    <section class="span-12 agent-registry-card management-list-card">
      <div v-if="hasAgentRecords" class="source-note">
        <i class="fa-solid fa-circle-info" aria-hidden="true" />
        <span>{{ statusSourceText }}</span>
      </div>
      <p v-if="hasAgentRecords" class="agent-narrow-notice" role="note">当前页面按桌面宽度设计；在窄视口下请左右滚动表格查看完整列。</p>

      <ManagementList
        class="agent-management-list"
        :rows="agents.pageItems"
        :columns="agentColumns"
        row-key="id"
        :sticky-left-keys="['identity']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:agents:columns"
        :selected-row-key="selectedAgent?.id"
        selection-tone="neutral"
        :loading="agents.pageLoading"
        :error="agents.pageError"
        :has-loaded="agents.pageHasLoaded"
        :search="query"
        search-placeholder="搜索 Agent / 角色职责..."
        search-aria-label="搜索 Agent 或角色职责"
        :reset-disabled="!query && agentStatusFilter === 'ALL'"
        :pagination="agents.pagination"
        :sort-by="agents.listQuery?.sortBy"
        :sort-order="agents.listQuery?.sortOrder"
        @select-row="selectAgent"
        @update:search="setAgentSearch"
        @reset="resetFilters"
        @page-change="changeAgentPage"
        @sort-change="changeAgentSort"
      >
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="agentStatusFilter"
            :options="agentManagementFilterOptions"
            ariaLabel="Agent 状态筛选"
            @update:model-value="setAgentStatusFilter($event as AgentStatusFilter)"
          />
        </template>
        <template #cell-identity="{ row: agent }">
          <button type="button" class="agent-identity-cell agent-select-button" :aria-pressed="selectedAgent?.id === agent.id" @click.stop="selectAgent(agent)">
            <span class="agent-avatar"><i class="fa-solid fa-robot" aria-hidden="true" /></span>
            <span class="agent-identity-copy">
              <strong class="aw-table-title" :title="agent.name">{{ agent.name }}</strong>
              <small class="aw-table-subtitle" :title="agent.roleDescription">{{ agent.roleDescription }}</small>
            </span>
          </button>
        </template>
        <template #cell-workspace="{ row: agent }"><span class="agent-workspace-pill aw-table-pill" :title="workspaceLabel(agent)">{{ workspaceLabel(agent) }}</span></template>
        <template #cell-model="{ row: agent }">
          <span class="agent-model-chip aw-table-meta" :title="modelLabel(agent)"><i class="fa-solid fa-microchip" aria-hidden="true" /><span>{{ modelLabel(agent) }}</span></span>
        </template>
        <template #cell-prompt="{ row: agent }">
          <span class="prompt-preview">
            <button type="button" class="prompt-preview-trigger" title="查看 Prompt Revision" :aria-label="`查看 ${agent.name} Prompt Revision`" @click.stop="openPromptDetail(agent)">
              <i class="fa-solid fa-file-lines" aria-hidden="true" /><span>{{ agent.currentPromptRevisionId ? "查看 Revision" : "暂无 Revision" }}</span>
            </button>
          </span>
        </template>
        <template #cell-status="{ row: agent }">
          <span :class="['agent-status-pill', 'aw-table-pill', statusTone(agent.status)]"><span aria-hidden="true" />{{ statusLabel(agent.status) }}</span>
        </template>
        <template #cell-updatedAt="{ row: agent }"><span class="agent-updated-at aw-table-meta">{{ formatAgentUpdatedAt(agent) }}</span></template>
        <template #cell-actions="{ row: agent }">
          <ManagementRowActions
            :menu-actions="agentMenuActions(agent)"
            @action="handleAgentRowAction($event, agent)"
          />
        </template>
        <template #empty>
          <div v-if="!hasAgentRecords" class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-robot" aria-hidden="true" /></div>
            <h2>暂无 Agent</h2>
            <p>创建 Agent 后再绑定业务空间、模型配置和 System Prompt。</p>
            <button class="primary-button" type="button" @click="enterCreateMode">创建 Agent</button>
          </div>
          <div v-else class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-magnifying-glass" aria-hidden="true" /></div>
            <h2>没有匹配的 Agent</h2>
            <p>调整搜索词或状态后再试，或切换顶部业务空间查看其他空间的 Agent。</p>
            <button class="ghost-button" type="button" @click="resetFilters">重置检索</button>
          </div>
        </template>
      </ManagementList>
    </section>

    <Transition name="modal-fade">
      <div
        v-if="studioMode"
        class="modal-backdrop agent-studio-backdrop"
        @click.self="requestCloseStudio('backdrop')"
        @keydown.esc="requestCloseStudio('keyboard')"
        @keydown="trapAgentModalFocus"
      >
      <section ref="agentStudioPanelRef" class="modal-card agent-studio-panel" role="dialog" aria-modal="true" :aria-label="studioTitle">
        <header class="agent-studio-shell">
          <div class="agent-studio-title">
            <button class="agent-back-button" type="button" @click="requestCloseStudio('back')">
              <i class="fa-solid fa-chevron-left" aria-hidden="true" />
              <span>返回注册中心</span>
            </button>
            <span class="agent-studio-divider" aria-hidden="true" />
            <div>
              <span>{{ draftAgent.id || "AUTO_ID" }}</span>
              <h3>{{ studioMode === "create" ? "新建 Agent" : "Agent 属性微调空间" }}</h3>
            </div>
          </div>
          <div class="agent-studio-actions">
            <button class="ghost-button" type="button" :disabled="savingAgent" @click="closeStudio">放弃改动</button>
            <button class="primary-button" type="button" :disabled="!canSaveAgent" @click="saveDraftAgent">
              <i :class="['fa-solid', savingAgent ? 'fa-spinner fa-spin' : 'fa-circle-check']" aria-hidden="true" />
              <span>{{ agentSaveButtonLabel }}</span>
            </button>
          </div>
        </header>

        <p v-if="agentStudioInlineWarning" class="agent-studio-inline-warning" role="alert">
          <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
          <span>{{ agentStudioInlineWarning }}</span>
        </p>

        <div class="agent-studio-body">
          <section class="agent-studio-section agent-parameters-panel">
            <header>
              <span><i class="fa-solid fa-sliders" aria-hidden="true" /> AGENT PARAMETERS ORCHESTRATION / 属性参数配置</span>
            </header>
            <div class="agent-studio-fields">
              <label class="modal-field">
                <span>Agent 运行名称 <b class="required-mark" aria-hidden="true">*</b></span>
                <input
                  ref="agentNameInputRef"
                  v-model="draftAgent.name"
                  type="text"
                  required
                  aria-required="true"
                  aria-label="Agent 运行名称"
                  :aria-invalid="Boolean(agentNameError)"
                  :aria-describedby="agentNameError ? 'agent-name-error' : undefined"
                  placeholder="例如: 智能风控审查官"
                />
                <small v-if="agentNameError" id="agent-name-error" class="field-error">{{ agentNameError }}</small>
              </label>
              <label class="modal-field">
                <span>绑定业务空间 <b class="required-mark" aria-hidden="true">*</b></span>
                <AppSelect
                  class="agent-studio-select"
                  v-model="draftAgent.workspaceId"
                  :options="workspaceOptions"
                  placeholder="选择业务空间"
                  aria-label="绑定业务空间"
                  :aria-required="true"
                  :aria-invalid="Boolean(agentWorkspaceError)"
                  :aria-describedby="agentWorkspaceError ? 'agent-workspace-error' : undefined"
                />
                <small v-if="agentWorkspaceError" id="agent-workspace-error" class="field-error">{{ agentWorkspaceError }}</small>
              </label>
              <label class="modal-field">
                <span>决策大模型 <b class="required-mark" aria-hidden="true">*</b></span>
                <AppSelect
                  class="agent-studio-select"
                  v-model="draftAgent.modelConfigId"
                  :options="modelConfigOptions"
                  placeholder="选择模型配置"
                  aria-label="决策大模型"
                  :aria-required="true"
                  :aria-invalid="Boolean(agentModelError)"
                  :aria-describedby="agentModelError ? 'agent-model-error' : undefined"
                />
                <small v-if="agentModelError" id="agent-model-error" class="field-error">{{ agentModelError }}</small>
              </label>
              <label class="modal-field">
                <span>场景决策职责 <b class="required-mark" aria-hidden="true">*</b></span>
                <input
                  v-model="draftAgent.roleDescription"
                  type="text"
                  required
                  aria-required="true"
                  aria-label="场景决策职责"
                  :aria-invalid="Boolean(agentRoleError)"
                  :aria-describedby="agentRoleError ? 'agent-role-error' : undefined"
                  placeholder="简述其在协同链路下的核心边界..."
                />
                <small v-if="agentRoleError" id="agent-role-error" class="field-error">{{ agentRoleError }}</small>
              </label>
              <div class="agent-status-toggle">
                <div>
                  <p>激活运行状态</p>
                  <small>允许平台对该 Agent 注入生产流量并开启心跳监测。</small>
                </div>
                <button
                  type="button"
                  role="switch"
                  aria-label="切换 Agent 激活运行状态"
                  :aria-checked="draftAgent.status === 'ACTIVE'"
                  :class="{ active: draftAgent.status === 'ACTIVE' }"
                  @click="toggleDraftStatus"
                >
                  <span />
                </button>
              </div>
            </div>
          </section>

          <section class="agent-studio-section studio-prompt-editor">
            <header>
              <span><i class="fa-solid fa-code" aria-hidden="true" /> {{ studioMode === "create" ? "SYSTEM PROMPT / 初始提示词" : "PROMPT ENHANCEMENT INPUT / 增强指令" }} <b v-if="studioMode === 'create'" class="required-mark" aria-hidden="true">*</b></span>
              <button
                class="agent-weave-button"
                type="button"
                :disabled="!canEnhanceDraftPrompt"
                :title="draftAgent.id ? 'AI 智能整理 System Prompt' : '保存 Agent 后可使用 AI 智能整理'"
                aria-describedby="agent-weave-helper"
                @click="enhancePrompt"
              >
                <i :class="['fa-solid', isEnhancing(draftAgent.id) ? 'fa-spinner fa-spin' : 'fa-wand-magic-sparkles']" aria-hidden="true" />
                <span>AI 智能整理 (Weaving)</span>
              </button>
            </header>
            <div class="agent-prompt-overview" :aria-label="studioMode === 'create' ? 'System Prompt 首段预览' : 'Prompt 增强输入首段预览'">
              <div>
                <strong>首段预览</strong>
                <p class="agent-prompt-preview-text">{{ promptPreviewText }}</p>
              </div>
              <dl>
                <div>
                  <dt>行数</dt>
                  <dd>{{ promptLineCount }}</dd>
                </div>
                <div>
                  <dt>字符</dt>
                  <dd>{{ draftAgent.systemPrompt?.length || 0 }}</dd>
                </div>
              </dl>
            </div>
            <div class="agent-prompt-editor-box" :class="{ 'is-weaving': isEnhancing(draftAgent.id) }">
              <textarea
                v-model="draftAgent.systemPrompt"
                rows="8"
                :required="studioMode === 'create'"
                :aria-required="studioMode === 'create'"
                :aria-label="studioMode === 'create' ? 'System Prompt' : 'Prompt 增强输入'"
                :aria-invalid="Boolean(agentPromptError)"
                :aria-describedby="agentPromptError ? 'agent-prompt-error agent-weave-helper' : 'agent-weave-helper'"
                :placeholder="studioMode === 'create' ? '作为智能决策主控：负责解析当前业务输入...' : '描述希望模型如何增强当前 Prompt；服务端不会回传现有 Prompt 原文。'"
              />
            </div>
            <div class="agent-prompt-meter">
              <span><i class="fa-solid fa-calculator" aria-hidden="true" /> 字符长度: <strong>{{ draftAgent.systemPrompt?.length || 0 }}</strong></span>
              <span id="agent-weave-helper">{{ draftAgent.id ? "输入增强要求后先预览，再显式采纳为不可变 Prompt Revision。" : "初始 Prompt 仅在创建请求中提交。" }}</span>
            </div>
            <small v-if="agentPromptError" id="agent-prompt-error" class="field-error agent-prompt-error">{{ agentPromptError }}</small>
          </section>
        </div>
      </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div
        v-if="pendingPromptSaveReview"
        class="modal-backdrop agent-prompt-save-review-modal"
        @click.self="cancelPromptSaveReview"
        @keydown.esc="cancelPromptSaveReview"
        @keydown="trapAgentModalFocus"
      >
      <section ref="promptDetailDialogRef" class="modal-card agent-prompt-save-review-dialog" role="dialog" aria-modal="true" aria-label="生产 Agent Prompt 变更审查">
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-shield-halved" aria-hidden="true" />
            <span>
              <strong>生产 Agent Prompt 变更审查</strong>
              <small>AGENT: {{ pendingPromptSaveReview.id }}</small>
            </span>
          </div>
          <button class="icon-action-button" type="button" title="关闭" aria-label="关闭 Prompt 变更审查" @click="cancelPromptSaveReview">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert">
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            当前 Agent 处于 Active，保存后会影响生产流量中的 System Prompt。请确认差异后再生效。
          </p>
          <div class="agent-prompt-diff-summary">
            <span><b>{{ promptSaveDiff.beforeChars }}</b> 原字符</span>
            <span><b>{{ promptSaveDiff.afterChars }}</b> 新字符</span>
            <span><b>{{ formatSignedDelta(promptSaveDiff.charDelta) }}</b> 字符变化</span>
            <span><b>{{ formatSignedDelta(promptSaveDiff.lineDelta) }}</b> 行变化</span>
          </div>
          <AgentPromptDiffViewer
            :before="originalPrompt"
            :after="pendingPromptText"
            before-label="历史版本"
            after-label="当前草稿"
            title="System Prompt Diff Viewer"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="savingAgent" @click="cancelPromptSaveReview">返回编辑</button>
          <button class="primary-button" type="button" :disabled="savingAgent" @click="confirmPromptSaveReview">
            <i :class="['fa-solid', savingAgent ? 'fa-spinner fa-spin' : 'fa-circle-check']" aria-hidden="true" />
            <span>{{ savingAgent ? "保存中..." : "确认保存并生效" }}</span>
          </button>
        </footer>
      </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div
        v-if="weavePreviewAgent"
        class="modal-backdrop agent-weave-preview-modal"
        @click.self="cancelWeavePreview"
        @keydown.esc="cancelWeavePreview"
        @keydown="trapAgentModalFocus"
      >
      <section ref="promptDetailDialogRef" class="modal-card agent-weave-preview-dialog" role="dialog" aria-modal="true" aria-label="AI 智能整理预览">
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />
            <span>
              <strong>AI 智能整理预览</strong>
              <small>AGENT: {{ draftAgent.id }}</small>
            </span>
          </div>
          <button class="icon-action-button" type="button" title="关闭" aria-label="关闭 AI 整理预览" @click="cancelWeavePreview">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert neutral">
            <i class="fa-solid fa-circle-info" aria-hidden="true" />
            预览不会修改 Agent；采纳会再次执行后端增强命令并创建不可变 Prompt Revision，原始输入与输出永久留存。
          </p>
          <div class="agent-prompt-diff-summary">
            <span><b>{{ weavePreviewDiff.beforeChars }}</b> 当前字符</span>
            <span><b>{{ weavePreviewDiff.afterChars }}</b> 预览字符</span>
            <span><b>{{ formatSignedDelta(weavePreviewDiff.charDelta) }}</b> 字符变化</span>
            <span><b>{{ formatSignedDelta(weavePreviewDiff.lineDelta) }}</b> 行变化</span>
          </div>
          <AgentPromptDiffViewer
            :before="draftAgent.systemPrompt || ''"
            :after="weavePreviewAgent.output || ''"
            before-label="增强输入"
            after-label="AI 预览"
            title="AI Prompt Preview Diff Viewer"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" @click="cancelWeavePreview">取消</button>
          <button class="primary-button" type="button" :disabled="acceptingPromptRevision" @click="applyWeavePreview">
            <i :class="acceptingPromptRevision ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-check'" aria-hidden="true" />
            <span>{{ acceptingPromptRevision ? "采纳中..." : "采纳为新 Revision" }}</span>
          </button>
        </footer>
      </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div v-if="promptDetailVisible" class="modal-backdrop agent-prompt-detail-modal" @click.self="closePromptDetail" @keydown.esc="closePromptDetail" @keydown="trapAgentModalFocus">
      <section ref="promptDetailDialogRef" class="modal-card agent-prompt-detail-dialog" role="dialog" aria-modal="true" :aria-label="promptDetailAgent ? `${promptDetailAgent.name} · Prompt Revision` : 'Prompt Revision 详情'">
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-rectangle-list" aria-hidden="true" />
            <span>
              <strong>Prompt Revision Audit</strong>
              <small>AGENT: {{ promptDetailAgent?.id }}</small>
            </span>
          </div>
          <button class="icon-action-button" type="button" title="关闭" aria-label="关闭 Prompt Revision 详情" @click="closePromptDetail">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-prompt-revision-readonly">
          <strong>{{ promptDetailAgent?.currentPromptRevisionId || "尚未创建 Prompt Revision" }}</strong>
          <p>Agent Read DTO 只返回当前 Revision ID，不返回 Prompt 明文。增强输入与输出由后端 StoredObject 永久保留。</p>
        </div>
        <footer class="agent-prompt-detail-footer">
          <span>LOCK VERSION: {{ promptDetailAgent?.lockVersion || 0 }}</span>
          <button class="primary-button" type="button" @click="closePromptDetail">关闭窗口</button>
        </footer>
      </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div v-if="capabilityAgent" class="modal-backdrop agent-capability-modal" @click.self="closeCapabilityBindings" @keydown.esc="closeCapabilityBindings" @keydown="trapAgentModalFocus">
        <section class="modal-card agent-capability-dialog" role="dialog" aria-modal="true" :aria-label="`${capabilityAgent.name} Capability Binding`">
          <header class="agent-prompt-detail-head">
            <div>
              <i class="fa-solid fa-link" aria-hidden="true" />
              <span><strong>Capability Binding</strong><small>AGENT: {{ capabilityAgent.id }}</small></span>
            </div>
            <button class="icon-action-button" type="button" aria-label="关闭 Capability Binding" :disabled="Boolean(capabilitySavingId)" @click="closeCapabilityBindings"><i class="fa-solid fa-xmark" /></button>
          </header>
          <div class="agent-capability-body">
            <p>Tool 与 Workflow 是 Workspace 级 Capability；此处只管理 Agent 的 follow/pin、Connection 选择和启用状态。</p>
            <div v-if="capabilityLoading" class="agent-capability-empty">正在加载统一 Capability Catalog...</div>
            <div v-else-if="!capabilityCatalog.length" class="agent-capability-empty">当前 Workspace 尚无已发布 Capability。</div>
            <article v-for="capability in capabilityCatalog" v-else :key="capability.id" class="agent-capability-item">
              <header>
                <div><span>{{ capability.kind }}</span><strong>{{ capability.name }}</strong><small>{{ capability.description }}</small></div>
                <em>{{ currentCapabilityBinding(capability.id) ? "已绑定" : "未绑定" }}</em>
              </header>
              <div v-if="capabilityDrafts[capability.id]" class="agent-capability-fields">
                <label class="modal-field select-field">
                  <span>版本策略</span>
                  <AppSelect
                    :model-value="capabilityDrafts[capability.id].versionPolicy"
                    :options="capabilityVersionPolicyOptions(capability)"
                    :aria-label="`${capability.name} 版本策略`"
                    @update:model-value="setCapabilityVersionPolicy(capability, String($event))"
                  />
                </label>
                <label class="modal-field">
                  <span>{{ capabilityDrafts[capability.id].versionPolicy === "PINNED" ? "Pinned Release ID" : "Resolved Release" }}</span>
                  <input :value="capabilityDrafts[capability.id].versionPolicy === 'PINNED' ? capability.activeReleaseId || '' : capability.activeRelease?.releaseId || ''" class="mono" disabled readonly />
                </label>
                <label class="modal-field">
                  <span>Connection ID（可选）</span>
                  <input v-model.trim="capabilityDrafts[capability.id].connectionId" class="mono" placeholder="同 Workspace 且与 Capability Provider 兼容" />
                </label>
                <label class="agent-capability-enabled"><input v-model="capabilityDrafts[capability.id].enabled" type="checkbox" /><span>启用该 Binding</span></label>
              </div>
              <footer>
                <button v-if="currentCapabilityBinding(capability.id)" class="ghost-button danger" type="button" :disabled="Boolean(capabilitySavingId)" @click="removeCapabilityBinding(capability)">解绑</button>
                <button class="primary-button" type="button" :disabled="Boolean(capabilitySavingId)" @click="saveCapabilityBinding(capability)">
                  <i v-if="capabilitySavingId === capability.id" class="fa-solid fa-spinner fa-spin" />{{ currentCapabilityBinding(capability.id) ? "更新 Binding" : "绑定 Capability" }}
                </button>
              </footer>
            </article>
          </div>
          <footer class="agent-prompt-detail-footer"><button class="ghost-button" type="button" :disabled="Boolean(capabilitySavingId)" @click="closeCapabilityBindings">关闭</button></footer>
        </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div
        v-if="agentDeleteTarget"
        class="modal-backdrop agent-delete-backdrop"
        @click.self="requestCloseAgentDeleteConfirm('backdrop')"
        @keydown="trapAgentModalFocus"
      >
      <section ref="agentDeleteDialogRef" class="modal-card agent-delete-dialog" role="dialog" aria-modal="true" aria-label="删除 Agent 确认">
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            <span>
              <strong>Delete Agent</strong>
              <small>AGENT: {{ agentDeleteTarget.id }}</small>
            </span>
          </div>
          <button class="icon-action-button" type="button" title="关闭" aria-label="关闭删除确认" @click="closeAgentDeleteConfirm">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-delete-body">
          <strong>{{ agentDeleteTarget.name }}</strong>
          <p>删除后会移除该 Agent，并影响其默认绑定、可用 Tool 和 Workflow 调度入口。此操作当前不可在页面内撤销。</p>
          <div class="agent-delete-impact">
            <span><b>{{ agentDeleteTarget.isDefault ? "是" : "否" }}</b> 默认 Agent</span>
            <span><b>{{ agentDeleteTarget.toolsCount }}</b> Tool</span>
            <span><b>{{ agentDeleteTarget.workflowsCount }}</b> Workflow</span>
          </div>
        </div>
        <label class="modal-field agent-delete-confirm-input">
          <span>请输入 Agent 名称 <em>{{ agentDeleteTarget.name }}</em> 以确认删除</span>
          <input
            ref="agentDeleteInputRef"
            v-model.trim="agentDeleteConfirmName"
            autocomplete="off"
            :aria-invalid="agentDeleteConfirmName.length > 0 && !canConfirmAgentDelete"
            aria-describedby="agent-delete-name-helper agent-delete-name-error"
          />
          <small id="agent-delete-name-helper">需精确匹配 Agent 名称；首尾空格会被自动忽略，大小写必须一致。</small>
          <small v-if="agentDeleteNameError" id="agent-delete-name-error" class="field-error">{{ agentDeleteNameError }}</small>
        </label>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="agentDeleting" @click="closeAgentDeleteConfirm">取消</button>
          <button class="primary-button danger" type="button" :disabled="agentDeleting || !canConfirmAgentDelete" @click="confirmDeleteAgent">
            <i :class="['fa-solid', agentDeleting ? 'fa-spinner fa-spin' : 'fa-trash']" aria-hidden="true" />
            <span>{{ agentDeleting ? "删除中..." : "删除 Agent" }}</span>
          </button>
        </footer>
      </section>
      </div>
    </Transition>

    <div v-if="agentActionNote" :class="['action-toast', agentActionTone === 'error' && 'error']" role="status" aria-live="polite">
      <i :class="agentActionTone === 'error' ? 'fa-solid fa-circle-exclamation' : 'fa-solid fa-circle-check'" aria-hidden="true" />
      <span>{{ agentActionNote }}</span>
      <button type="button" aria-label="关闭反馈提示" @click="clearAgentToast">
        <i class="fa-solid fa-xmark" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.agent-grid {
  align-items: start;
  row-gap: 24px;
}

.agent-grid .page-header {
  padding: 0;
  background: transparent;
  border: 0;
  box-shadow: none;
}

.agent-grid .page-header > div:first-child > span {
  color: #047857;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.agent-grid .page-header h2 {
  margin-top: 2px;
  color: #0f172a;
  font-size: 24px;
  font-weight: 700;
  line-height: 1.2;
}

.agent-grid .page-header p {
  margin-top: 4px;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
}

.agent-create-button {
  min-height: 44px;
  padding: 0 16px;
  gap: 8px;
  color: #fff;
  background: #020617;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 600;
  text-transform: none;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
}

.agent-create-button:hover {
  background: #1e293b;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.12);
}

.agent-create-button:active {
  transform: scale(0.98);
}

.agent-create-button i {
  color: #34d399;
}

.agent-create-button span {
  color: #fff;
  text-transform: none;
}

.agent-summary-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 24px;
}

.agent-summary-card {
  min-height: 148px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 32px;
  align-content: space-between;
  gap: 16px 12px;
  padding: 20px;
  color: #0f172a;
  text-align: left;
  background: #fff;
  border: 1px solid #f1f5f9;
  border-radius: 12px;
  box-shadow: 0 10px 30px -10px rgba(0, 0, 0, 0.04), 0 1px 3px rgba(0, 0, 0, 0.02);
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.agent-summary-card:hover {
  border-color: #e2e8f0;
  box-shadow: 0 12px 26px rgba(15, 23, 42, 0.08);
  transform: scale(1.01);
}

.agent-summary-card.active {
  border-color: rgba(16, 185, 129, 0.4);
  box-shadow: 0 10px 30px -10px rgba(0, 0, 0, 0.04), 0 0 0 1px rgba(16, 185, 129, 0.1);
}

.agent-summary-card span,
.agent-summary-card small,
.agent-summary-card strong {
  display: block;
  min-width: 0;
}

.agent-summary-card span {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.agent-summary-card.online span {
  color: #059669;
}

.agent-summary-card.warning span {
  color: #92400e;
}

.agent-summary-card > i {
  width: 32px;
  height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  justify-self: end;
  color: #475569;
  background: #f8fafc;
  border-radius: 8px;
  font-size: 12px;
}

.agent-summary-card.online > i::before {
  content: "";
  width: 10px;
  height: 10px;
  display: block;
  background: #10b981;
  border-radius: 999px;
  animation: agentPulse 1.8s infinite;
}

.agent-summary-card.warning > i {
  color: #92400e;
  background: #fffbeb;
}

.agent-summary-card strong {
  grid-column: 1 / -1;
  color: #0f172a;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 30px;
  font-weight: 700;
  line-height: 1;
}

.agent-summary-card.online strong {
  color: #059669;
}

.agent-summary-card.warning strong {
  color: #92400e;
}

.agent-summary-card small {
  grid-column: 1 / -1;
  padding-top: 8px;
  color: #64748b;
  border-top: 1px solid #f8fafc;
  font-size: 10px;
  font-weight: 300;
  line-height: 1.35;
}

/* Keep the registry shell transparent so ManagementList owns table/toolbar/footer chrome
   (avoids toolbar-in-table and double bottom-bar stacking). */
.agent-registry-card.management-list-card {
  min-width: 0;
  padding: 0;
  overflow: visible;
  background: transparent;
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

.agent-registry-toolbar {
  margin: 0;
  padding: 20px;
  align-items: center;
  background: #fff;
  border-bottom: 1px solid #f1f5f9;
}

.agent-search-box {
  width: 320px;
  flex: 0 0 320px;
  min-height: 44px;
  padding: 0 12px;
  background: #f8fafc;
  border-color: #e2e8f0;
  border-radius: 8px;
}

.agent-search-box input {
  height: 44px;
  color: #1e293b;
  font-size: 12px;
}

.agent-search-box input::placeholder {
  color: #64748b;
}

.agent-search-box:focus-within {
  border-color: #059669;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.16);
}

.agent-filter-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  gap: 12px;
}

.agent-grid .segmented-filter {
  gap: 2px;
  padding: 2px;
  background: #f8fafc;
  border: 1px solid rgba(226, 232, 240, 0.8);
  border-radius: 8px;
}

.agent-grid .segmented-filter button {
  min-height: 44px;
  gap: 6px;
  padding: 0 12px;
  color: #64748b;
  border-radius: 6px;
  font-size: 10px;
  font-weight: 600;
}

.agent-grid .segmented-filter button.active {
  color: #0f172a;
  background: #fff;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
}

.agent-grid .segmented-filter b {
  padding: 0;
  color: inherit;
  background: transparent;
  font-size: 10px;
}

.agent-reset-button {
  min-height: 44px;
  padding: 0 8px;
  color: #64748b;
  background: transparent;
  border: 0;
  font-size: 12px;
  font-weight: 700;
}

.agent-reset-button:hover {
  color: #334155;
  background: transparent;
}

.source-note {
  margin: 0;
  padding: 12px 24px;
  gap: 12px;
  color: #065f46;
  background: rgba(236, 253, 245, 0.6);
  border: 0;
  border-bottom: 1px solid rgba(167, 243, 208, 0.5);
  border-radius: 0;
  font-size: 12px;
  font-weight: 300;
  line-height: 1.35;
}

.source-note i {
  color: #10b981;
}

.agent-narrow-notice {
  display: none;
  margin: 0 20px 16px;
  padding: 10px 12px;
  color: #475569;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
  line-height: 1.5;
}

.agent-identity-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 12px;
  overflow: hidden;
}

.agent-select-button {
  display: flex;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  min-height: 44px;
  align-items: center;
  gap: 12px;
  padding: 0;
  overflow: hidden;
  color: inherit;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: 8px;
  cursor: pointer;
  font: inherit;
}

.agent-select-button:hover strong,
.agent-select-button:focus-visible strong {
  color: #0f172a;
}

.agent-select-button:focus-visible {
  outline: 2px solid rgba(100, 116, 139, 0.35);
  outline-offset: 3px;
}

.agent-avatar {
  width: 44px;
  height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  color: #059669;
  background: rgba(236, 253, 245, 0.65);
  border: 1px solid #d1fae5;
  border-radius: 8px;
  font-size: 12px;
}

.agent-identity-copy {
  min-width: 0;
  max-width: 100%;
  flex: 1 1 auto;
  overflow: hidden;
}

.agent-identity-cell strong,
.agent-identity-cell small {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-identity-cell strong,
.agent-identity-cell .aw-table-title {
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
}

.agent-identity-cell small,
.agent-identity-cell .aw-table-subtitle {
  max-width: 100%;
  margin-top: 2px;
  overflow: hidden;
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.75rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-workspace-pill {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  justify-content: center;
  padding: 4px 10px;
  overflow: hidden;
  color: #475569;
  background: #f8fafc;
  border: 1px solid #f1f5f9;
  border-radius: 999px;
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-model-chip {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--aw-table-meta-color, #6b7280);
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-weight: var(--aw-table-meta-weight, 400);
  line-height: 1.25;
}

.agent-model-chip i {
  flex: 0 0 auto;
  color: #64748b;
  font-size: 10px;
}

.agent-model-chip span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.prompt-preview {
  display: inline-flex;
}

.prompt-preview-trigger {
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 0 10px;
  color: #4f46e5;
  background: #eef2ff;
  border: 0;
  border-radius: 6px;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1;
  transition: color 0.15s ease, background-color 0.15s ease;
}

.prompt-preview-trigger:hover {
  color: #3730a3;
  background: rgba(224, 231, 255, 0.75);
}

.prompt-preview-trigger i {
  font-size: 10px;
}

.agent-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  color: #64748b;
  background: #f1f5f9;
  border-radius: 999px;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.2;
}

.agent-status-pill > span {
  width: 6px;
  height: 6px;
  display: inline-block;
  background: #64748b;
  border-radius: 999px;
}

.agent-status-pill.active {
  color: #047857;
  background: rgba(209, 250, 229, 0.65);
}

.agent-status-pill.active > span {
  background: #10b981;
  animation: agentPulse 1.8s infinite;
}

.table-pagination {
  margin-top: 0;
  padding: 16px 20px;
  background: #fff;
  border-top: 1px solid #f1f5f9;
}

.table-pagination > span {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 1;
}

.table-pagination button {
  width: 44px;
  height: 44px;
  border-color: #e2e8f0;
  border-radius: 8px;
  font-size: 12px;
}

.table-pagination .page-number-button.active {
  color: #fff;
  background: #020617;
  border-color: #020617;
  font-weight: 700;
}

.agent-studio-backdrop {
  align-items: center;
  justify-content: center;
  padding: 16px;
  background: rgba(15, 23, 42, 0.35);
  backdrop-filter: blur(2px);
}

.agent-studio-panel {
  width: min(1100px, calc(100vw - 32px));
  max-height: calc(100vh - 32px);
  overflow: hidden;
  padding: 16px 18px;
  background: #fafbfc;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.05);
}

.agent-studio-shell {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid #e2e8f0;
}

.agent-studio-title,
.agent-studio-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.agent-studio-actions {
  padding: 0;
  border: 0;
}

.agent-studio-inline-warning {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 12px 0 0;
  padding: 10px 12px;
  color: #991b1b;
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
  line-height: 1.45;
}

.agent-studio-inline-warning i {
  color: #dc2626;
}

.agent-studio-actions .ghost-button,
.agent-studio-actions .primary-button,
.agent-prompt-detail-footer .ghost-button,
.agent-prompt-detail-footer .primary-button {
  min-height: 44px;
}

.agent-back-button {
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  color: #64748b;
  background: transparent;
  border: 0;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
}

.agent-back-button:hover {
  color: #1e293b;
  background: #f1f5f9;
}

.agent-studio-divider {
  width: 1px;
  height: 16px;
  background: #cbd5e1;
}

.agent-studio-title div > span {
  display: inline-flex;
  padding: 2px 8px;
  color: #475569;
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
  border-radius: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
}

.agent-studio-title h3 {
  margin-top: 4px;
  color: #020617;
  font-size: 18px;
  font-weight: 700;
  line-height: 1.2;
}

.agent-studio-body {
  display: grid;
  grid-template-columns: 1fr;
  align-content: start;
  gap: 12px;
  min-height: auto;
  overflow: visible;
  padding: 12px 0 0;
}

.agent-studio-section {
  overflow: visible;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  box-shadow: 0 10px 30px -10px rgba(0, 0, 0, 0.04), 0 1px 3px rgba(0, 0, 0, 0.02);
}

.agent-parameters-panel {
  overflow: visible;
}

.agent-studio-section > header {
  min-height: 44px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  background: #f8fafc;
  border-bottom: 1px solid #e2e8f0;
}

.agent-studio-section > header span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.required-mark {
  color: #dc2626;
  font-family: inherit;
  font-size: inherit;
  font-weight: 800;
  line-height: 1;
}

.agent-studio-section > header i {
  color: #10b981;
}

.agent-studio-fields {
  --agent-studio-control-height: 44px;
  display: grid;
  grid-template-columns: repeat(2, minmax(260px, 1fr));
  gap: 10px 16px;
  padding: 14px 16px;
}

.agent-parameters-panel .agent-studio-fields {
  grid-template-columns: repeat(2, minmax(260px, 1fr));
}

.agent-studio-fields .modal-field > span {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.agent-studio-fields .modal-field input {
  height: var(--agent-studio-control-height);
  min-height: var(--agent-studio-control-height);
  padding: 0 12px;
  color: #1e293b;
  background: #f8fafc;
  border-color: #e2e8f0;
  border-radius: 8px;
  font-size: 12px;
}

:deep(.agent-studio-select .el-select__wrapper) {
  height: var(--agent-studio-control-height);
  min-height: var(--agent-studio-control-height);
  padding: 0 12px;
  border-radius: 8px;
}

.agent-studio-fields .modal-field input[aria-invalid="true"],
.agent-prompt-editor-box:has(textarea[aria-invalid="true"]) {
  border-color: #fca5a5;
  box-shadow: 0 0 0 3px rgba(220, 38, 38, 0.08);
}

.field-error {
  display: block;
  margin-top: 6px;
  color: #b91c1c;
  font-size: 11px;
  font-weight: 700;
  line-height: 1.4;
}

.agent-status-toggle {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 14px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
}

.agent-status-toggle p {
  margin: 0;
  color: #1e293b;
  font-size: 12px;
  font-weight: 700;
}

.agent-status-toggle small {
  display: block;
  margin-top: 2px;
  color: #64748b;
  font-size: 9px;
  font-weight: 300;
  line-height: 1.4;
}

.agent-status-toggle button {
  width: 48px;
  min-height: 32px;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  padding: 4px;
  background: #cbd5e1;
  border: 0;
  border-radius: 999px;
  transition: background-color 0.2s ease;
}

.agent-status-toggle button.active {
  justify-content: flex-end;
  background: #10b981;
}

.agent-status-toggle button span {
  width: 20px;
  height: 20px;
  display: block;
  background: #fff;
  border-radius: 999px;
  box-shadow: 0 2px 4px rgba(15, 23, 42, 0.18);
}

.agent-weave-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 44px;
  padding: 4px 10px;
  color: #059669;
  background: #ecfdf5;
  border: 1px solid #d1fae5;
  border-radius: 8px;
  font-size: 10px;
  font-weight: 700;
}

.agent-weave-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.agent-prompt-overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 16px;
  margin: 12px 16px 0;
  padding: 10px 12px;
  color: #334155;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
}

.agent-prompt-overview strong {
  display: block;
  color: #0f172a;
  font-size: 12px;
  font-weight: 800;
  line-height: 1.35;
}

.agent-prompt-preview-text {
  display: -webkit-box;
  margin: 4px 0 0;
  overflow: hidden;
  color: #475569;
  font-size: 12px;
  line-height: 1.5;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.agent-prompt-overview dl {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
}

.agent-prompt-overview dl > div {
  min-width: 64px;
  padding: 7px 10px;
  text-align: center;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
}

.agent-prompt-overview dt {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  line-height: 1.2;
}

.agent-prompt-overview dd {
  margin: 2px 0 0;
  color: #0f172a;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 13px;
  font-weight: 800;
  line-height: 1.2;
}

.agent-prompt-editor-box {
  margin: 12px 16px 0;
  overflow: hidden;
  background: rgba(248, 250, 252, 0.7);
  border: 1px solid #e2e8f0;
  border-radius: 12px;
}

.agent-prompt-editor-box.is-weaving {
  animation: weaveBorder 1.5s infinite ease-in-out;
}

.agent-prompt-editor-box textarea {
  width: 100%;
  height: 150px;
  min-height: 150px;
  max-height: 260px;
  padding: 14px;
  resize: vertical;
  color: #334155;
  background: transparent;
  border: 0;
  outline: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 1.65;
}

.agent-prompt-meter {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 12px 16px 14px;
  padding: 10px 12px;
  color: #64748b;
  background: #f8fafc;
  border: 1px solid #f1f5f9;
  border-radius: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
}

.agent-prompt-meter strong {
  color: #334155;
}

.agent-prompt-error {
  margin: -6px 16px 14px;
}

.agent-prompt-detail-dialog {
  width: min(672px, calc(100vw - 32px));
  overflow: hidden;
  border-radius: 16px;
}

.agent-prompt-save-review-dialog,
.agent-weave-preview-dialog {
  width: min(1180px, calc(100vw - 32px));
  max-height: calc(100vh - 32px);
  overflow: hidden;
  border-radius: 16px;
}

.agent-prompt-detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 20px;
  background: #f8fafc;
  border-bottom: 1px solid #e2e8f0;
}

.agent-prompt-detail-head > div {
  display: flex;
  align-items: center;
  gap: 8px;
}

.agent-prompt-detail-head > div > i {
  color: #10b981;
}

.agent-prompt-detail-head strong,
.agent-prompt-detail-head small {
  display: block;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.agent-prompt-detail-head strong {
  color: #334155;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.agent-prompt-detail-head small {
  margin-top: 2px;
  color: #64748b;
  font-size: 9px;
}

.agent-prompt-markdown {
  max-height: 50vh;
  margin: 0;
  overflow: auto;
  padding: 24px;
  color: #334155;
  background: #f8fafc;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 1.65;
  white-space: pre-wrap;
}

.agent-prompt-detail-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 14px 20px;
  background: #f8fafc;
  border-top: 1px solid #e2e8f0;
}

.agent-prompt-detail-footer span {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
}

.agent-risk-review-body {
  display: grid;
  gap: 14px;
  padding: 18px 20px;
  overflow: auto;
  background: #fff;
  border-bottom: 1px solid #e2e8f0;
}

.agent-risk-alert {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 0;
  padding: 10px 12px;
  color: #991b1b;
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 10px;
  font-size: 12px;
  font-weight: 700;
  line-height: 1.55;
}

.agent-risk-alert.neutral {
  color: #075985;
  background: #f0f9ff;
  border-color: #bae6fd;
}

.agent-risk-alert i {
  margin-top: 2px;
  flex: 0 0 auto;
}

.agent-prompt-diff-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.agent-prompt-diff-summary span {
  min-height: 32px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 0 10px;
  color: #475569;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
}

.agent-prompt-diff-summary b {
  color: #0f172a;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.agent-delete-dialog {
  width: min(560px, calc(100vw - 32px));
  overflow: hidden;
  border-radius: 16px;
}

.agent-delete-body {
  display: grid;
  gap: 10px;
  padding: 20px;
  color: #334155;
  background: #fff;
  border-bottom: 1px solid #e2e8f0;
}

.agent-delete-body strong {
  color: #0f172a;
  font-size: 14px;
  font-weight: 800;
}

.agent-delete-body p {
  margin: 0;
  color: #475569;
  font-size: 12px;
  line-height: 1.6;
}

.agent-delete-impact {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.agent-delete-impact span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 28px;
  padding: 0 10px;
  color: #475569;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
}

.agent-delete-impact b {
  color: #b91c1c;
}

.agent-delete-confirm-input {
  padding: 18px 20px 0;
}

.agent-delete-confirm-input span {
  color: #475569;
}

.agent-delete-confirm-input em {
  color: #b91c1c;
  font-style: normal;
}

.agent-delete-confirm-input input {
  min-height: 44px;
}

@keyframes agentPulse {
  0%,
  100% {
    opacity: 0.8;
  }
  50% {
    opacity: 1;
  }
}

@keyframes weaveBorder {
  0% {
    border-color: rgba(16, 185, 129, 0.2);
    box-shadow: 0 0 5px rgba(16, 185, 129, 0.05);
  }
  50% {
    border-color: rgba(16, 185, 129, 0.8);
    box-shadow: 0 0 15px rgba(16, 185, 129, 0.2);
  }
  100% {
    border-color: rgba(16, 185, 129, 0.2);
    box-shadow: 0 0 5px rgba(16, 185, 129, 0.05);
  }
}

.agent-prompt-revision-readonly {
  display: grid;
  gap: 10px;
  margin: 20px;
  padding: 18px;
  border: 1px solid #dbe3ec;
  border-radius: 10px;
  background: #f8fafc;
}

.agent-prompt-revision-readonly strong {
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.agent-prompt-revision-readonly p,
.agent-capability-body > p {
  margin: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 20px;
}

.agent-capability-dialog {
  width: min(920px, calc(100vw - 32px));
  max-height: calc(100vh - 32px);
}

.agent-capability-body {
  display: grid;
  gap: 14px;
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
  background: #f8fafc;
}

.agent-capability-item {
  display: grid;
  gap: 14px;
  padding: 16px;
  border: 1px solid #dbe3ec;
  border-radius: 12px;
  background: #fff;
}

.agent-capability-item > header,
.agent-capability-item > footer,
.agent-capability-enabled {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.agent-capability-item > header div {
  display: grid;
  gap: 3px;
}

.agent-capability-item > header span,
.agent-capability-item > header small,
.agent-capability-item > header em {
  color: #64748b;
  font-size: 11px;
  font-style: normal;
}

.agent-capability-fields {
  display: grid;
  grid-template-columns: 1fr 1fr 1.4fr auto;
  align-items: end;
  gap: 12px;
}

.agent-capability-enabled {
  justify-content: flex-start;
  min-height: 44px;
  color: #334155;
  font-size: 12px;
  font-weight: 700;
}

.agent-capability-item > footer {
  justify-content: flex-end;
}

.agent-capability-empty {
  padding: 28px;
  text-align: center;
  color: #64748b;
}

@media (max-width: 1180px) {
  .agent-summary-grid,
  .agent-studio-body {
    grid-template-columns: 1fr;
  }

  .agent-narrow-notice {
    display: block;
  }

  .agent-search-box {
    width: 100%;
    flex-basis: 100%;
  }

  .agent-capability-fields {
    grid-template-columns: 1fr;
  }
}
</style>
