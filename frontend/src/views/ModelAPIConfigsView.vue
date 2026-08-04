<script setup lang="ts">
import "./model-api-page.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from "vue";
import { useI18n } from "vue-i18n";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useModelConfigStore } from "../stores/modelConfigs";
import { useWorkspaceStore } from "../stores/workspaces";
import type { ModelApiConfig, ModelApiConfigListQuery, ModelRuntimeCapabilities } from "../types/domain";
import { normalizeRuntimeCapabilities } from "../utils/session-context-config";

const OPENAI_COMPATIBLE_PROVIDER = "OpenAI Compatible";
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const { t } = useI18n();

const tokenizerProfileOptions = computed(() => [
  { label: t("modelApis.tokenizerO200k"), value: "o200k_base" },
  { label: t("modelApis.tokenizerCl100k"), value: "cl100k_base" },
  { label: t("modelApis.tokenizerByteUpper"), value: "byte_upper_bound" },
]);

const outputTokenLimitModeOptions = computed(() => [
  { label: t("modelApis.outputLimitMaxTokens"), value: "max_tokens" },
  { label: "max_completion_tokens", value: "max_completion_tokens" },
]);

/** One-click window presets so users need not know exact vendor limits. */
const contextWindowPresets = computed(() => [
  { label: "32K", tokens: 32000, hint: t("modelApis.presetCostSaving") },
  { label: "64K", tokens: 64000, hint: "" },
  { label: "128K", tokens: 128000, hint: t("modelApis.presetRecommended") },
  { label: "200K", tokens: 200000, hint: t("modelApis.presetLongContext") },
]);
type ModelStatusFilter = "ALL" | ModelApiConfig["status"];
type ModelModalFeedback = { tone: "success" | "error"; message: string } | null;
type ModelDraftField = "name" | "credentialSecretId" | "apiBase" | "modelName";

const modelConfigs = useModelConfigStore();
const workspaces = useWorkspaceStore();
const canEditWorkspace = computed(() =>
  workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "EDIT"),
);
const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));
const query = ref("");
const modelStatusFilter = ref<ModelStatusFilter>("ALL");
const modelModalVisible = ref(false);
const modelModalMode = ref<"create" | "edit">("create");
const editingModelDraft = ref<ModelApiConfig | null>(null);
const latencyById = ref<Record<string, number>>({});
const actionNote = ref("");
const actionNoteTimer = ref<number | null>(null);
const draftModelConfig = ref<ModelApiConfig>(newModelConfig());
const verifyingModelId = ref<string | null>(null);
const savingModelConfig = ref(false);
const modelModalFeedback = ref<ModelModalFeedback>(null);
const modelDraftInitialFingerprint = ref("");
const modelDraftValidationAttempted = ref(false);
const modelDraftTouchedFields = ref<ModelDraftField[]>([]);
const discardModelDraftVisible = ref(false);
const pendingModelDeletion = ref<ModelApiConfig | null>(null);
const deletingModelConfig = ref(false);
const modelDeleteError = ref("");
const mobileModelActionMenuId = ref<string | null>(null);
const modelRuntimeSectionOpen = ref(false);
const modelRuntimeAdvancedOpen = ref(false);
const modelModalRef = useTemplateRef<HTMLElement>("modelModalRef");
const credentialPlaintextInput = useTemplateRef<HTMLInputElement>("credentialPlaintextInput");
const discardModelDraftRef = useTemplateRef<HTMLElement>("discardModelDraftRef");
const modelDeletionConfirmRef = useTemplateRef<HTMLElement>("modelDeletionConfirmRef");
let modelModalRestoreTarget: HTMLElement | null = null;
let modelConfirmationRestoreTarget: HTMLElement | null = null;

const modelModalFocusableSelector = [
  "button:not(:disabled)",
  "[href]",
  "input:not(:disabled)",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

const modelStatusOptions = computed<Array<{ label: string; value: ModelStatusFilter }>>(() => [
  { label: t("modelApis.statusAll"), value: "ALL" },
  { label: t("modelApis.statusVerified"), value: "VERIFIED" },
  { label: t("modelApis.statusUnverified"), value: "UNVERIFIED" },
]);

const modelModalTitle = computed(() =>
  modelModalMode.value === "create" ? t("modelApis.create") : t("modelApis.edit"),
);
const activeModelDraft = computed(() =>
  modelModalMode.value === "create" ? draftModelConfig.value : editingModelDraft.value,
);
const isModelDraftDirty = computed(() => {
  const draft = activeModelDraft.value;
  return Boolean(
    draft && modelDraftInitialFingerprint.value && modelDraftFingerprint(draft) !== modelDraftInitialFingerprint.value,
  );
});
const modelKeyboardNavigationActive = computed(
  () => modelModalVisible.value || discardModelDraftVisible.value || Boolean(pendingModelDeletion.value),
);
const activeStatusFilterLabel = computed(
  () => modelStatusOptions.value.find((option) => option.value === modelStatusFilter.value)?.label || "",
);
const hasSearchQuery = computed(() => query.value.trim().length > 0);
const hasActiveModelFilters = computed(() => hasSearchQuery.value || modelStatusFilter.value !== "ALL");
const modelConfigColumns = computed<ManagementListColumn<ModelApiConfig>[]>(() => [
  {
    key: "config",
    label: t("modelApis.colConfig"),
    width: 244,
    sortable: true,
    sortKey: "name",
    getValue: (item) => item.name,
  },
  {
    key: "provider",
    label: t("modelApis.colProvider"),
    width: 164,
    hidable: true,
    sortable: true,
    sortKey: "provider",
    getValue: () => OPENAI_COMPATIBLE_PROVIDER,
  },
  {
    key: "credential",
    label: t("modelApis.colCredential"),
    width: 140,
    hidable: true,
    getValue: (item) =>
      item.credentialConfigured ? t("modelApis.credentialConfigured") : t("modelApis.credentialNotConfigured"),
  },
  {
    key: "apiBase",
    label: t("modelApis.colApiBase"),
    width: 244,
    hidable: true,
    sortable: true,
    sortKey: "apiBase",
    getValue: (item) => item.apiBase,
  },
  {
    key: "modelName",
    label: t("modelApis.colModelName"),
    width: 190,
    hidable: true,
    sortable: true,
    sortKey: "modelName",
    getValue: (item) => item.modelName,
  },
  {
    key: "latency",
    label: t("modelApis.colLatency"),
    width: 132,
    align: "right",
    headerAlign: "center",
    hidable: true,
    sortable: true,
    sortKey: "latency",
    defaultHiddenWhenEmpty: true,
    placeholderValues: ["-", t("modelApis.notTested")],
    getValue: (item) => (displayedLatency(item) ? `${displayedLatency(item)}ms` : "-"),
  },
  { key: "actions", label: t("modelApis.colActions"), width: 68, align: "right", headerAlign: "center" },
]);

onMounted(async () => {
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (hasWorkspaceContext.value) await modelConfigs.loadModelConfigs();
  } catch {
    // The shared Workspace state provides recovery actions when bootstrap fails.
  }
});

watch(modelModalVisible, async (visible) => {
  if (!visible) {
    modelModalRestoreTarget?.focus();
    modelModalRestoreTarget = null;
    return;
  }

  modelModalRestoreTarget = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  await nextTick();
  focusInitialModelModalElement();
});

watch(modelKeyboardNavigationActive, (active) => {
  window.removeEventListener("keydown", handleModelModalKeydown);
  if (active) {
    window.addEventListener("keydown", handleModelModalKeydown);
  }
});

watch(discardModelDraftVisible, async (visible) => {
  if (!visible) return;
  await nextTick();
  focusInitialDialogElement(discardModelDraftRef.value);
});

watch(pendingModelDeletion, async (pendingDeletion) => {
  if (!pendingDeletion) return;
  await nextTick();
  focusInitialDialogElement(modelDeletionConfirmRef.value);
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleModelModalKeydown);
});

function newModelConfig(): ModelApiConfig {
  return {
    id: "",
    name: t("modelApis.defaultDraftName"),
    provider: OPENAI_COMPATIBLE_PROVIDER,
    apiBase: "https://llm-gateway.actweave.local/v1",
    modelName: "claude-sonnet-4",
    credentialConfigured: false,
    credentialSecretId: "",
    options: {},
    runtimeCapabilities: normalizeRuntimeCapabilities({
      contextWindowTokens: 128000,
      defaultOutputReserveTokens: 4096,
      outputTokenLimitMode: "max_tokens",
      tokenizerProfile: "o200k_base",
      tokenizerVersion: "2026-01",
    }),
    status: "UNVERIFIED",
    createdBy: "",
    updatedBy: "",
    createdAt: "",
    updatedAt: "",
    lockVersion: 0,
  };
}

function modelDraftFingerprint(config: ModelApiConfig) {
  return JSON.stringify({
    name: config.name,
    provider: config.provider,
    credentialSecretId: config.credentialSecretId || "",
    apiBase: config.apiBase,
    modelName: config.modelName,
    runtimeCapabilities: config.runtimeCapabilities || {},
  });
}

function draftRuntimeCaps(): ModelRuntimeCapabilities {
  const draft = activeModelDraft.value;
  if (!draft) return normalizeRuntimeCapabilities(undefined);
  if (!draft.runtimeCapabilities || typeof draft.runtimeCapabilities !== "object") {
    draft.runtimeCapabilities = normalizeRuntimeCapabilities(undefined);
  }
  return draft.runtimeCapabilities as ModelRuntimeCapabilities;
}

function setDraftRuntimeCap<K extends keyof ModelRuntimeCapabilities>(key: K, value: ModelRuntimeCapabilities[K]) {
  const draft = activeModelDraft.value;
  if (!draft) return;
  draft.runtimeCapabilities = {
    ...normalizeRuntimeCapabilities(draft.runtimeCapabilities),
    [key]: value,
  };
}

function applyContextWindowPreset(tokens: number) {
  setDraftRuntimeCap("contextWindowTokens", tokens);
  // Keep safe product defaults for reserve/tokenizer when applying a preset.
  const caps = draftRuntimeCaps();
  if (!caps.defaultOutputReserveTokens || caps.defaultOutputReserveTokens <= 0) {
    setDraftRuntimeCap("defaultOutputReserveTokens", 4096);
  }
  if (!caps.tokenizerProfile) {
    setDraftRuntimeCap("tokenizerProfile", "o200k_base");
  }
  if (!caps.outputTokenLimitMode) {
    setDraftRuntimeCap("outputTokenLimitMode", "max_tokens");
  }
}

function isContextWindowPresetActive(tokens: number) {
  return Number(draftRuntimeCaps().contextWindowTokens) === tokens;
}

function formatContextWindowLabel(tokens: number | undefined) {
  if (!tokens || !Number.isFinite(tokens) || tokens <= 0) return t("modelApis.contextNotSet");
  if (tokens % 1000 === 0) {
    const k = tokens / 1000;
    if (k >= 1) return `${k}K`;
  }
  return String(tokens);
}

const modelRuntimeSectionSummary = computed(() => {
  const caps = draftRuntimeCaps();
  const windowLabel = formatContextWindowLabel(
    caps.contextWindowTokens != null ? Number(caps.contextWindowTokens) : undefined,
  );
  const reserve = caps.defaultOutputReserveTokens || 4096;
  return t("modelApis.contextSummary", { window: windowLabel, reserve });
});

async function toggleModelRuntimeSection() {
  modelRuntimeSectionOpen.value = !modelRuntimeSectionOpen.value;
  if (!modelRuntimeSectionOpen.value) return;
  await nextTick();
  // Keep expanded fields in the scrollable form viewport (card clips overflow).
  document.getElementById("model-runtime-section-body")?.scrollIntoView({
    block: "nearest",
    behavior: "smooth",
  });
}

function openCreateModel() {
  draftModelConfig.value = newModelConfig();
  clearActionNote();
  modelModalFeedback.value = null;
  modelModalMode.value = "create";
  modelRuntimeSectionOpen.value = false;
  modelRuntimeAdvancedOpen.value = false;
  modelDraftInitialFingerprint.value = modelDraftFingerprint(draftModelConfig.value);
  resetModelDraftValidation();
  discardModelDraftVisible.value = false;
  modelModalVisible.value = true;
}

function selectModel(item: ModelApiConfig) {
  modelConfigs.selectedConfigId = item.id;
}

async function testConnection(item: ModelApiConfig) {
  if (verifyingModelId.value) return;
  selectModel(item);
  verifyingModelId.value = item.id;
  try {
    const verified = await modelConfigs.verifyModelConfig(item.id);
    if (verified.lastLatencyMs !== undefined) {
      latencyById.value = { ...latencyById.value, [item.id]: verified.lastLatencyMs };
    }
    await loadModelConfigPage();
    const note =
      verified.status === "VERIFIED"
        ? t("modelApis.verifyPassed", { name: item.name })
        : t("modelApis.verifyFailed", { name: item.name });
    showActionNote(note);
  } finally {
    verifyingModelId.value = null;
  }
}

function modelMenuActions(item: ModelApiConfig): ManagementRowAction[] {
  const isVerifying = verifyingModelId.value === item.id;
  const verificationLocked = Boolean(verifyingModelId.value);
  return [
    {
      key: "verify",
      label: t("modelApis.actionTest"),
      icon: "fa-solid fa-plug-circle-bolt",
      tone: "primary",
      disabled: verificationLocked,
      loading: isVerifying,
      disabledReason: verificationLocked && !isVerifying ? t("modelApis.verifyBusy") : undefined,
    },
    { key: "edit", label: t("modelApis.actionEdit"), icon: "fa-solid fa-pen-to-square" },
    { key: "delete", label: t("modelApis.actionDelete"), icon: "fa-solid fa-trash-can", tone: "danger" },
  ];
}

function handleModelRowAction(actionKey: string, item: ModelApiConfig) {
  if (actionKey === "verify") {
    void testConnection(item);
    return;
  }
  if (actionKey === "edit") {
    openModelEditor(item);
    return;
  }
  if (actionKey === "delete") requestModelDeletion(item);
}

function openModelEditor(item: ModelApiConfig) {
  selectModel(item);
  editingModelDraft.value = {
    ...item,
    provider: OPENAI_COMPATIBLE_PROVIDER,
    credentialSecretId: "",
    runtimeCapabilities: normalizeRuntimeCapabilities(item.runtimeCapabilities),
  };
  clearActionNote();
  modelModalFeedback.value = null;
  modelModalMode.value = "edit";
  const caps = normalizeRuntimeCapabilities(item.runtimeCapabilities);
  modelRuntimeSectionOpen.value = false;
  modelRuntimeAdvancedOpen.value =
    (Boolean(caps.tokenizerProfile) && caps.tokenizerProfile !== "o200k_base") ||
    caps.outputTokenLimitMode === "max_completion_tokens";
  modelDraftInitialFingerprint.value = modelDraftFingerprint(editingModelDraft.value);
  resetModelDraftValidation();
  discardModelDraftVisible.value = false;
  modelModalVisible.value = true;
}

function requestModelDeletion(item: ModelApiConfig) {
  if (deletingModelConfig.value || verifyingModelId.value === item.id) return;
  modelConfirmationRestoreTarget = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  pendingModelDeletion.value = item;
  modelDeleteError.value = "";
}

function closeModelDeletionConfirm() {
  if (deletingModelConfig.value) return;
  pendingModelDeletion.value = null;
  modelDeleteError.value = "";
  restoreModelConfirmationFocus();
}

async function deleteModel(item: ModelApiConfig) {
  if (verifyingModelId.value === item.id) return;
  await modelConfigs.deleteModelConfig(item.id);
  const { [item.id]: _deletedLatency, ...remainingLatency } = latencyById.value;
  latencyById.value = remainingLatency;
  if (editingModelDraft.value?.id === item.id) {
    closeModelModal();
  }
  const pageCount = Math.max(1, Math.ceil(modelConfigs.pagination.total / modelConfigs.pagination.pageSize));
  await loadModelConfigPage({ page: Math.min(modelConfigs.pagination.page, pageCount) });
  showActionNote(t("modelApis.deleted", { name: item.name }));
}

async function confirmModelDeletion() {
  const item = pendingModelDeletion.value;
  if (!item || deletingModelConfig.value) return;

  deletingModelConfig.value = true;
  modelDeleteError.value = "";
  try {
    await deleteModel(item);
    pendingModelDeletion.value = null;
    modelConfirmationRestoreTarget = null;
  } catch {
    modelDeleteError.value = t("modelApis.deleteFailed");
  } finally {
    deletingModelConfig.value = false;
  }
}

async function saveDraftModelConfig() {
  if (savingModelConfig.value) return;
  if (!(await validateModelDraft())) return;
  savingModelConfig.value = true;
  try {
    const credential = await modelConfigs.createCredentialSecret(
      draftModelConfig.value.name,
      credentialPlaintextInput.value?.value || "",
    );
    const created = await modelConfigs.createModelConfig({
      ...draftModelConfig.value,
      provider: OPENAI_COMPATIBLE_PROVIDER,
      credentialSecretId: credential.id,
    });
    await loadModelConfigPage({ page: 1 });
    showActionNote(t("modelApis.created", { name: created.name }));
    closeModelModal();
  } catch {
    modelModalFeedback.value = {
      tone: "error",
      message: t("modelApis.saveFailed"),
    };
  } finally {
    if (credentialPlaintextInput.value) credentialPlaintextInput.value.value = "";
    savingModelConfig.value = false;
  }
}

async function saveEditedModelConfig() {
  if (!editingModelDraft.value) return;
  if (savingModelConfig.value) return;
  if (!(await validateModelDraft())) return;
  savingModelConfig.value = true;
  try {
    const updated = await modelConfigs.updateModelConfig(editingModelDraft.value.id, {
      ...editingModelDraft.value,
      provider: OPENAI_COMPATIBLE_PROVIDER,
    });
    await loadModelConfigPage();
    showActionNote(t("modelApis.saved", { name: updated.name }));
    closeModelModal();
  } catch {
    modelModalFeedback.value = {
      tone: "error",
      message: t("modelApis.saveFailed"),
    };
  } finally {
    savingModelConfig.value = false;
  }
}

function closeModelModal() {
  modelModalVisible.value = false;
  modelModalFeedback.value = null;
  modelDraftInitialFingerprint.value = "";
  resetModelDraftValidation();
  discardModelDraftVisible.value = false;
  draftModelConfig.value = newModelConfig();
  editingModelDraft.value = null;
  modelConfirmationRestoreTarget = null;
}

function requestModelModalClose() {
  if (savingModelConfig.value) return;
  if (isModelDraftDirty.value) {
    modelConfirmationRestoreTarget = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    discardModelDraftVisible.value = true;
    return;
  }
  closeModelModal();
}

function keepEditingModelDraft() {
  discardModelDraftVisible.value = false;
  restoreModelConfirmationFocus();
}

function confirmDiscardModelDraft() {
  if (savingModelConfig.value) return;
  closeModelModal();
}

function displayedLatency(item: ModelApiConfig) {
  return latencyById.value[item.id] ?? item.lastLatencyMs ?? 0;
}

function latencyTone(latencyMs: number) {
  if (!latencyMs) return "untested";
  if (latencyMs < 500) return "healthy";
  if (latencyMs <= 2000) return "warning";
  return "danger";
}

function latencyLabel(latencyMs: number) {
  if (!latencyMs) return t("modelApis.notTested");
  if (latencyMs < 500) return t("modelApis.latencyHealthy");
  if (latencyMs <= 2000) return t("modelApis.latencySlow");
  return t("modelApis.latencyVerySlow");
}

function resetModelFilters() {
  query.value = "";
  modelStatusFilter.value = "ALL";
  void loadModelConfigPage({ page: 1 });
}

function viewAllModelConfigs() {
  modelStatusFilter.value = "ALL";
  void loadModelConfigPage({ page: 1 });
}

async function retryLoadModelConfigs() {
  await loadModelConfigPage();
}

function setModelStatusFilter(value: ModelStatusFilter) {
  if (modelStatusFilter.value === value) return;
  modelStatusFilter.value = value;
  void loadModelConfigPage({ page: 1 });
}

function updateModelStatusFilter(value: string) {
  setModelStatusFilter(value as ModelStatusFilter);
}

function setModelSearch(value: string) {
  query.value = value;
  void loadModelConfigPage({ page: 1 });
}

async function loadModelConfigPage(overrides: Partial<ModelApiConfigListQuery> = {}) {
  await modelConfigs.loadModelConfigs({
    query: query.value.trim(),
    status: modelStatusFilter.value === "ALL" ? undefined : modelStatusFilter.value,
    page: overrides.page ?? modelConfigs.pagination.page,
    pageSize: overrides.pageSize ?? modelConfigs.pagination.pageSize,
    ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
  });
}

function changeModelConfigPage(pagination: { page: number; pageSize: number }) {
  void loadModelConfigPage(pagination);
}

function changeModelConfigSort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
  void loadModelConfigPage({
    page: 1,
    pageSize: modelConfigs.pagination.pageSize,
    sortBy: sort.sortBy ?? "",
    sortOrder: sort.sortOrder,
  });
}

function toggleMobileModelActions(item: ModelApiConfig) {
  mobileModelActionMenuId.value = mobileModelActionMenuId.value === item.id ? null : item.id;
}

function openMobileModelEditor(item: ModelApiConfig) {
  mobileModelActionMenuId.value = null;
  openModelEditor(item);
}

function requestMobileModelDeletion(item: ModelApiConfig) {
  mobileModelActionMenuId.value = null;
  requestModelDeletion(item);
}

function resetModelDraftValidation() {
  modelDraftValidationAttempted.value = false;
  modelDraftTouchedFields.value = [];
}

function touchModelDraftField(field: ModelDraftField) {
  if (!modelDraftTouchedFields.value.includes(field)) {
    modelDraftTouchedFields.value = [...modelDraftTouchedFields.value, field];
  }
}

function modelDraftValidationError(field: ModelDraftField) {
  const draft = activeModelDraft.value;
  if (!draft) return "";

  if (field === "name" && !draft.name.trim()) return t("modelApis.validationName");
  if (field === "credentialSecretId") {
    const secretID = draft.credentialSecretId?.trim() || "";
    if (modelModalMode.value === "create" && !credentialPlaintextInput.value?.value.trim())
      return t("modelApis.validationApiKey");
    if (secretID && !UUID_PATTERN.test(secretID)) return t("modelApis.validationSecretUuid");
  }
  if (field === "modelName" && !draft.modelName.trim()) return t("modelApis.validationModelName");
  if (field === "apiBase") {
    if (!draft.apiBase.trim()) return t("modelApis.validationApiBase");
    try {
      const url = new URL(draft.apiBase);
      if (url.protocol === "http:" || url.protocol === "https:") return "";
    } catch {
      // The field-level error below explains the expected format.
    }
    return t("modelApis.validationApiBaseUrl");
  }
  return "";
}

function shouldShowModelDraftError(field: ModelDraftField) {
  return modelDraftValidationAttempted.value || modelDraftTouchedFields.value.includes(field);
}

function visibleModelDraftValidationError(field: ModelDraftField) {
  return shouldShowModelDraftError(field) ? modelDraftValidationError(field) : "";
}

async function validateModelDraft() {
  const fields: ModelDraftField[] = ["name", "credentialSecretId", "apiBase", "modelName"];
  modelDraftValidationAttempted.value = true;
  modelDraftTouchedFields.value = fields;
  const invalid = fields.some((field) => Boolean(modelDraftValidationError(field)));
  if (invalid) {
    await nextTick();
    modelModalRef.value?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }
  return !invalid;
}

function clearActionNote() {
  actionNote.value = "";
  if (actionNoteTimer.value) {
    window.clearTimeout(actionNoteTimer.value);
    actionNoteTimer.value = null;
  }
}

function showActionNote(note: string) {
  clearActionNote();
  actionNote.value = note;
  actionNoteTimer.value = window.setTimeout(() => {
    actionNote.value = "";
    actionNoteTimer.value = null;
  }, 6000);
}

async function copyApiBase(item: ModelApiConfig) {
  await navigator.clipboard?.writeText(item.apiBase);
  showActionNote(t("modelApis.apiBaseCopied", { name: item.name }));
}

function dialogFocusableElements(dialog: HTMLElement | null) {
  if (!dialog) return [];
  return Array.from(dialog.querySelectorAll<HTMLElement>(modelModalFocusableSelector));
}

function focusInitialDialogElement(dialog: HTMLElement | null) {
  const initialTarget =
    dialog?.querySelector<HTMLElement>("[data-modal-initial-focus]") || dialogFocusableElements(dialog)[0];
  initialTarget?.focus();
}

function focusInitialModelModalElement() {
  focusInitialDialogElement(modelModalRef.value);
}

function activeModelConfirmationDialog() {
  if (discardModelDraftVisible.value) return discardModelDraftRef.value;
  if (pendingModelDeletion.value) return modelDeletionConfirmRef.value;
  return null;
}

function restoreModelConfirmationFocus() {
  void nextTick(() => {
    modelConfirmationRestoreTarget?.focus();
    modelConfirmationRestoreTarget = null;
  });
}

function trapModelDialogFocus(event: KeyboardEvent, dialog: HTMLElement) {
  const focusable = dialogFocusableElements(dialog);
  if (!focusable.length) {
    event.preventDefault();
    dialog.focus();
    return;
  }

  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
    return;
  }
  if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function handleModelModalKeydown(event: KeyboardEvent) {
  const confirmationDialog = activeModelConfirmationDialog();
  if (confirmationDialog) {
    if (event.key === "Escape") {
      event.preventDefault();
      if (discardModelDraftVisible.value) {
        keepEditingModelDraft();
      } else {
        closeModelDeletionConfirm();
      }
      return;
    }
    if (event.key === "Tab") {
      trapModelDialogFocus(event, confirmationDialog);
    }
    return;
  }

  if (!modelModalVisible.value || !modelModalRef.value) return;

  if (event.key === "Escape") {
    event.preventDefault();
    requestModelModalClose();
    return;
  }

  if (event.key !== "Tab") return;
  trapModelDialogFocus(event, modelModalRef.value);
}
</script>

<template>
  <div class="model-config-page management-page-grid management-page-grid--two-rows">
    <ManagementPageHeader
      class="model-config-header"
      :title="t('modelApis.title')"
      :description="t('modelApis.description')"
      icon="fa-solid fa-microchip"
      :eyebrow="t('modelApis.eyebrow')"
    >
      <template #actions>
        <button
          class="primary-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? t('modelApis.create') : t('modelApis.createNeedWorkspace')"
          @click="openCreateModel"
        >
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          {{ t("modelApis.create") }}
        </button>
      </template>
    </ManagementPageHeader>

    <section class="model-config-card management-list-card">
      <div class="model-config-table-wrap">
        <ManagementList
          class="model-config-management-list"
          :rows="hasWorkspaceContext ? modelConfigs.items : []"
          :columns="modelConfigColumns"
          row-key="id"
          :sticky-left-keys="['config']"
          :sticky-right-keys="['actions']"
          storage-key="actweave:model-api-configs:columns"
          :selectable="false"
          :loading="hasWorkspaceContext && modelConfigs.loading"
          :error="hasWorkspaceContext ? modelConfigs.error : undefined"
          :has-loaded="hasWorkspaceContext ? modelConfigs.hasLoaded : true"
          :search="query"
          :search-placeholder="t('modelApis.searchPlaceholder')"
          :search-aria-label="t('modelApis.searchAria')"
          :reset-disabled="!query && modelStatusFilter === 'ALL'"
          :pagination="modelConfigs.pagination"
          :sort-by="modelConfigs.listQuery?.sortBy"
          :sort-order="modelConfigs.listQuery?.sortOrder"
          @update:search="setModelSearch"
          @reset="resetModelFilters"
          @page-change="changeModelConfigPage"
          @sort-change="changeModelConfigSort"
        >
          <template #filters>
            <ManagementSegmentedFilter
              class="model-status-filter"
              :model-value="modelStatusFilter"
              :options="modelStatusOptions"
              :ariaLabel="t('modelApis.statusFilterAria')"
              @update:model-value="updateModelStatusFilter"
            />
          </template>
          <template #cell-config="{ row: item }">
            <div class="model-config-name-cell">
              <div class="model-config-icon">
                <i class="fa-solid fa-microchip" />
              </div>
              <div>
                <strong class="aw-table-title">{{ item.name }}</strong>
                <span class="aw-table-subtitle">{{ item.createdBy || t("modelApis.createdByWorkspace") }}</span>
              </div>
            </div>
          </template>
          <template #cell-provider>
            <span class="model-provider-pill aw-table-pill">{{ OPENAI_COMPATIBLE_PROVIDER }}</span>
          </template>
          <template #cell-credential="{ row: item }">
            <span class="model-credential-state aw-table-pill" :class="{ configured: item.credentialConfigured }">
              <i
                :class="item.credentialConfigured ? 'fa-solid fa-shield-halved' : 'fa-solid fa-triangle-exclamation'"
              />
              {{
                item.credentialConfigured
                  ? t("modelApis.credentialConfigured")
                  : t("modelApis.credentialNotConfigured")
              }}
            </span>
          </template>
          <template #cell-apiBase="{ row: item }">
            <div class="model-api-base-cell">
              <span class="model-api-base aw-table-mono" :title="item.apiBase">{{ item.apiBase }}</span>
              <button
                type="button"
                class="model-copy-button"
                :aria-label="t('modelApis.copyApiBase')"
                :title="t('modelApis.copyApiBase')"
                @click.stop="copyApiBase(item)"
              >
                <i class="fa-regular fa-copy" />
              </button>
            </div>
          </template>
          <template #cell-modelName="{ row: item }">
            <span class="model-mono-text model-name-text aw-table-mono" :title="item.modelName">{{
              item.modelName
            }}</span>
          </template>
          <template #cell-latency="{ row: item }">
            <span class="model-latency-badge aw-table-pill" :class="latencyTone(displayedLatency(item))">
              <span class="model-latency-value">{{
                displayedLatency(item) ? `${displayedLatency(item)}ms` : "-"
              }}</span>
              <span class="model-latency-label aw-table-meta">{{ latencyLabel(displayedLatency(item)) }}</span>
            </span>
          </template>
          <template #cell-actions="{ row: item }">
            <ManagementRowActions :menu-actions="modelMenuActions(item)" @action="handleModelRowAction($event, item)" />
          </template>
          <template #card="{ row: item }">
            <article class="model-config-mobile-card">
              <div class="model-config-mobile-card-head">
                <div class="model-config-name-cell">
                  <div class="model-config-icon"><i class="fa-solid fa-microchip" /></div>
                  <div>
                    <strong>{{ item.name }}</strong>
                    <span>{{ item.createdBy || t("modelApis.createdByWorkspace") }}</span>
                  </div>
                </div>
                <button
                  type="button"
                  class="model-mobile-actions-toggle"
                  :aria-label="t('modelApis.moreActions')"
                  :aria-expanded="mobileModelActionMenuId === item.id"
                  @click="toggleMobileModelActions(item)"
                >
                  <i class="fa-solid fa-ellipsis" />
                </button>
              </div>
              <dl>
                <div>
                  <dt>Provider</dt>
                  <dd>{{ OPENAI_COMPATIBLE_PROVIDER }}</dd>
                </div>
                <div>
                  <dt>{{ t("modelApis.mobileModel") }}</dt>
                  <dd class="model-mono-text">{{ item.modelName }}</dd>
                </div>
                <div>
                  <dt>{{ t("modelApis.mobileLatency") }}</dt>
                  <dd>
                    {{ displayedLatency(item) ? `${displayedLatency(item)}ms` : t("modelApis.notTested") }}
                  </dd>
                </div>
              </dl>
              <div
                v-if="mobileModelActionMenuId === item.id"
                class="model-mobile-actions-menu"
                role="menu"
                :aria-label="t('modelApis.mobileActions')"
              >
                <button
                  type="button"
                  role="menuitem"
                  :disabled="Boolean(verifyingModelId)"
                  @click="testConnection(item)"
                >
                  <i class="fa-solid fa-plug-circle-bolt" /> {{ t("modelApis.testConnection") }}
                </button>
                <button type="button" role="menuitem" @click="openMobileModelEditor(item)">
                  <i class="fa-solid fa-pen-to-square" /> {{ t("modelApis.editConfig") }}
                </button>
                <button type="button" role="menuitem" class="danger" @click="requestMobileModelDeletion(item)">
                  <i class="fa-solid fa-trash-can" /> {{ t("modelApis.deleteConfig") }}
                </button>
              </div>
            </article>
          </template>
          <template #error="{ error }">
            <div v-if="modelConfigs.items.length" class="model-load-error-banner" role="alert">
              <i class="fa-solid fa-triangle-exclamation" />
              <span>{{ t("modelApis.loadFailed", { error }) }}</span>
              <button type="button" @click="retryLoadModelConfigs">{{ t("modelApis.retry") }}</button>
            </div>
            <div
              v-else
              class="empty-state registry-empty-state management-registry-empty-state model-load-error-state"
              role="alert"
            >
              <div class="model-empty-state-icon management-empty-state-icon error">
                <i class="fa-solid fa-triangle-exclamation" />
              </div>
              <h2>{{ t("modelApis.loadFailedTitle") }}</h2>
              <p>{{ error }}</p>
              <button class="primary-button" type="button" @click="retryLoadModelConfigs">
                {{ t("modelApis.retry") }}
              </button>
            </div>
          </template>
          <template #empty>
            <WorkspaceContextState
              v-if="!hasWorkspaceContext"
              embedded-in-list
              :feature="t('modelApis.featureName')"
              icon="fa-solid fa-microchip"
              @retry="retryLoadModelConfigs"
            />
            <div
              v-else-if="!hasActiveModelFilters"
              class="empty-state registry-empty-state management-registry-empty-state"
            >
              <div class="model-empty-state-icon management-empty-state-icon"><i class="fa-solid fa-microchip" /></div>
              <h2>{{ t("modelApis.emptyTitle") }}</h2>
              <p>{{ t("modelApis.emptyBody") }}</p>
              <button v-if="canEditWorkspace" class="primary-button" type="button" @click="openCreateModel">
                {{ t("modelApis.create") }}
              </button>
            </div>
            <div v-else class="empty-state registry-empty-state management-registry-empty-state">
              <div class="model-empty-state-icon management-empty-state-icon">
                <i :class="hasSearchQuery ? 'fa-solid fa-magnifying-glass' : 'fa-solid fa-filter-circle-xmark'" />
              </div>
              <template v-if="hasSearchQuery">
                <h2>{{ t("modelApis.noMatchTitle") }}</h2>
                <p>{{ t("modelApis.noMatchBody") }}</p>
                <button class="ghost-button" type="button" @click="resetModelFilters">
                  {{ t("modelApis.reset") }}
                </button>
              </template>
              <template v-else>
                <h2>{{ t("modelApis.emptyFilterTitle", { status: activeStatusFilterLabel }) }}</h2>
                <p>{{ t("modelApis.emptyFilterBody") }}</p>
                <button class="ghost-button" type="button" @click="viewAllModelConfigs">
                  {{ t("modelApis.viewAll") }}
                </button>
              </template>
            </div>
          </template>
        </ManagementList>
      </div>
    </section>

    <Transition name="modal-fade">
      <div
        v-if="modelModalVisible && activeModelDraft"
        class="model-modal-backdrop"
        @click.self="requestModelModalClose"
      >
        <section
          ref="modelModalRef"
          class="modal-card model-modal-card"
          role="dialog"
          aria-modal="true"
          :aria-label="modelModalTitle"
          :aria-busy="savingModelConfig"
        >
          <header class="model-modal-head">
            <div class="model-modal-icon">
              <i class="fa-solid fa-microchip" />
            </div>
            <div>
              <h3>{{ modelModalTitle }}</h3>
              <p>{{ t("modelApis.modalSubtitle") }}</p>
            </div>
            <button
              class="model-modal-close"
              type="button"
              :aria-label="t('modelApis.closeModal')"
              :disabled="savingModelConfig"
              @click="requestModelModalClose"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </header>

          <div class="model-modal-form">
            <label class="model-modal-field">
              <span class="model-modal-field-label"
                >{{ t("modelApis.fieldName") }}
                <span class="model-field-required" aria-hidden="true">*</span></span
              >
              <input
                v-model="activeModelDraft.name"
                data-modal-initial-focus
                required
                aria-required="true"
                :aria-invalid="Boolean(visibleModelDraftValidationError('name'))"
                aria-describedby="model-field-error-name"
                :placeholder="t('modelApis.fieldNamePlaceholder')"
                @blur="touchModelDraftField('name')"
              />
              <span
                id="model-field-error-name"
                class="model-field-error"
                :class="{ visible: visibleModelDraftValidationError('name') }"
                aria-live="polite"
              >
                {{ visibleModelDraftValidationError("name") }}
              </span>
            </label>
            <label class="model-modal-field locked">
              <span class="model-modal-field-label">
                Provider
                <i
                  class="fa-solid fa-lock model-field-lock"
                  :title="t('modelApis.providerLockedTitle')"
                  :aria-label="t('modelApis.providerLockedTitle')"
                />
              </span>
              <input :value="OPENAI_COMPATIBLE_PROVIDER" disabled readonly />
            </label>
            <label class="model-modal-field">
              <span class="model-modal-field-label"
                >{{ modelModalMode === "create" ? "API Key" : "Credential Secret ID" }}
                <span v-if="modelModalMode === 'create'" class="model-field-required" aria-hidden="true">*</span></span
              >
              <input
                v-if="modelModalMode === 'create'"
                ref="credentialPlaintextInput"
                class="mono"
                type="password"
                autocomplete="new-password"
                required
                aria-required="true"
                :aria-invalid="Boolean(visibleModelDraftValidationError('credentialSecretId'))"
                aria-describedby="model-field-error-credentialSecretId model-credential-help"
                :placeholder="t('modelApis.pasteApiKey')"
                @blur="touchModelDraftField('credentialSecretId')"
              />
              <input
                v-else
                v-model="activeModelDraft.credentialSecretId"
                class="mono"
                autocomplete="off"
                :aria-invalid="Boolean(visibleModelDraftValidationError('credentialSecretId'))"
                aria-describedby="model-field-error-credentialSecretId model-credential-help"
                :placeholder="
                  activeModelDraft.credentialConfigured
                    ? t('modelApis.keepExistingCredential')
                    : t('modelApis.secretUuid')
                "
                @blur="touchModelDraftField('credentialSecretId')"
              />
              <small id="model-credential-help">{{
                modelModalMode === "create"
                  ? t("modelApis.credentialHelpCreate")
                  : t("modelApis.credentialHelpEdit")
              }}</small>
              <span
                id="model-field-error-credentialSecretId"
                class="model-field-error"
                :class="{ visible: visibleModelDraftValidationError('credentialSecretId') }"
                aria-live="polite"
              >
                {{ visibleModelDraftValidationError("credentialSecretId") }}
              </span>
            </label>
            <label class="model-modal-field">
              <span class="model-modal-field-label"
                >{{ t("modelApis.fieldApiBase") }}
                <span class="model-field-required" aria-hidden="true">*</span></span
              >
              <input
                v-model="activeModelDraft.apiBase"
                class="mono"
                type="url"
                autocomplete="url"
                required
                aria-required="true"
                :aria-invalid="Boolean(visibleModelDraftValidationError('apiBase'))"
                aria-describedby="model-field-error-apiBase"
                placeholder="https://llm-gateway.actweave.local/v1"
                @blur="touchModelDraftField('apiBase')"
              />
              <span
                id="model-field-error-apiBase"
                class="model-field-error"
                :class="{ visible: visibleModelDraftValidationError('apiBase') }"
                aria-live="polite"
              >
                {{ visibleModelDraftValidationError("apiBase") }}
              </span>
            </label>
            <label class="model-modal-field">
              <span class="model-modal-field-label"
                >{{ t("modelApis.fieldModelName") }}
                <span class="model-field-required" aria-hidden="true">*</span></span
              >
              <input
                v-model="activeModelDraft.modelName"
                class="mono"
                required
                aria-required="true"
                :aria-invalid="Boolean(visibleModelDraftValidationError('modelName'))"
                aria-describedby="model-field-error-modelName"
                placeholder="claude-sonnet-4"
                @blur="touchModelDraftField('modelName')"
              />
              <span
                id="model-field-error-modelName"
                class="model-field-error"
                :class="{ visible: visibleModelDraftValidationError('modelName') }"
                aria-live="polite"
              >
                {{ visibleModelDraftValidationError("modelName") }}
              </span>
            </label>
            <label class="model-modal-field locked">
              <span class="model-modal-field-label">
                Created By
                <i
                  class="fa-solid fa-lock model-field-lock"
                  :title="t('modelApis.createdByLockedTitle')"
                  :aria-label="t('modelApis.createdByLockedTitle')"
                />
              </span>
              <input :value="activeModelDraft.createdBy || t('modelApis.createdByPending')" disabled readonly />
            </label>

            <section class="model-modal-fieldset model-runtime-section" :class="{ open: modelRuntimeSectionOpen }">
              <button
                type="button"
                class="model-runtime-section-toggle"
                :aria-expanded="modelRuntimeSectionOpen"
                aria-controls="model-runtime-section-body"
                @click="toggleModelRuntimeSection"
              >
                <span class="model-runtime-section-title">
                  <i class="fa-solid fa-window-maximize" aria-hidden="true" />
                  <span>{{ t("modelApis.runtimeSection") }}</span>
                </span>
                <span class="model-runtime-section-meta">
                  <span class="model-runtime-section-summary">{{ modelRuntimeSectionSummary }}</span>
                  <i class="fa-solid fa-chevron-down model-runtime-section-chevron" aria-hidden="true" />
                </span>
              </button>
              <div v-show="modelRuntimeSectionOpen" id="model-runtime-section-body" class="model-runtime-section-body">
                <p class="model-modal-fieldset-help">
                  {{ t("modelApis.runtimeHelp") }}
                </p>
                <div class="model-context-presets" role="group" :aria-label="t('modelApis.contextPresetsAria')">
                  <button
                    v-for="preset in contextWindowPresets"
                    :key="preset.tokens"
                    type="button"
                    class="model-context-preset"
                    :class="{ active: isContextWindowPresetActive(preset.tokens) }"
                    @click="applyContextWindowPreset(preset.tokens)"
                  >
                    <span>{{ preset.label }}</span>
                    <small v-if="preset.hint">{{ preset.hint }}</small>
                  </button>
                </div>
                <label class="model-modal-field">
                  <span class="model-modal-field-label">{{ t("modelApis.contextWindowTokens") }}</span>
                  <input
                    class="mono"
                    type="number"
                    min="1"
                    step="1"
                    :placeholder="t('modelApis.contextWindowPlaceholder')"
                    :value="draftRuntimeCaps().contextWindowTokens ?? ''"
                    @input="
                      setDraftRuntimeCap(
                        'contextWindowTokens',
                        Number(($event.target as HTMLInputElement).value) || undefined,
                      )
                    "
                  />
                  <small class="model-modal-field-hint">
                    {{ t("modelApis.contextWindowHint") }}
                  </small>
                </label>
                <label class="model-modal-field">
                  <span class="model-modal-field-label">{{ t("modelApis.outputReserveTokens") }}</span>
                  <input
                    class="mono"
                    type="number"
                    min="1"
                    step="1"
                    :value="draftRuntimeCaps().defaultOutputReserveTokens ?? 4096"
                    @input="
                      setDraftRuntimeCap(
                        'defaultOutputReserveTokens',
                        Number(($event.target as HTMLInputElement).value) || 4096,
                      )
                    "
                  />
                  <small class="model-modal-field-hint">
                    {{ t("modelApis.outputReserveHint") }}
                  </small>
                </label>
                <button
                  type="button"
                  class="model-runtime-advanced-toggle"
                  :class="{ open: modelRuntimeAdvancedOpen }"
                  :aria-expanded="modelRuntimeAdvancedOpen"
                  @click="modelRuntimeAdvancedOpen = !modelRuntimeAdvancedOpen"
                >
                  <i class="fa-solid fa-sliders" aria-hidden="true" />
                  <span>{{ t("modelApis.advancedOptions") }}</span>
                  <i class="fa-solid fa-chevron-down model-runtime-advanced-chevron" aria-hidden="true" />
                </button>
                <div v-if="modelRuntimeAdvancedOpen" class="model-runtime-advanced">
                  <label class="model-modal-field">
                    <span class="model-modal-field-label">{{ t("modelApis.tokenizerLabel") }}</span>
                    <AppSelect
                      class="model-modal-select"
                      :model-value="draftRuntimeCaps().tokenizerProfile || 'o200k_base'"
                      :options="tokenizerProfileOptions"
                      :placeholder="t('modelApis.tokenizerPlaceholder')"
                      :aria-label="t('modelApis.tokenizerAria')"
                      @update:model-value="setDraftRuntimeCap('tokenizerProfile', String($event ?? 'o200k_base'))"
                    />
                    <small class="model-modal-field-hint">
                      {{ t("modelApis.tokenizerHint") }}
                    </small>
                  </label>
                  <label class="model-modal-field">
                    <span class="model-modal-field-label">{{ t("modelApis.outputLimitMode") }}</span>
                    <AppSelect
                      class="model-modal-select"
                      :model-value="draftRuntimeCaps().outputTokenLimitMode || 'max_tokens'"
                      :options="outputTokenLimitModeOptions"
                      :placeholder="t('modelApis.outputLimitPlaceholder')"
                      :aria-label="t('modelApis.outputLimitAria')"
                      @update:model-value="
                        setDraftRuntimeCap(
                          'outputTokenLimitMode',
                          String($event ?? 'max_tokens') as ModelRuntimeCapabilities['outputTokenLimitMode'],
                        )
                      "
                    />
                    <small class="model-modal-field-hint">
                      {{ t("modelApis.outputLimitHint") }}
                    </small>
                  </label>
                </div>
              </div>
            </section>

            <div class="model-modal-note">
              <i class="fa-solid fa-circle-info" />
              <div>
                <strong>{{ t("modelApis.smartHintTitle") }}</strong>
                <p>
                  {{
                    modelModalMode === "create" ? t("modelApis.smartHintCreate") : t("modelApis.smartHintEdit")
                  }}
                </p>
              </div>
            </div>
            <div
              v-if="modelModalFeedback"
              class="model-modal-feedback"
              :class="modelModalFeedback.tone"
              :role="modelModalFeedback.tone === 'success' ? 'status' : 'alert'"
              :aria-live="modelModalFeedback.tone === 'success' ? 'polite' : 'assertive'"
            >
              <i
                :class="
                  modelModalFeedback.tone === 'success' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'
                "
              />
              <span>{{ modelModalFeedback.message }}</span>
            </div>
          </div>

          <div class="model-modal-actions">
            <span>{{ t("modelApis.syncNote") }}</span>
            <div>
              <button
                class="model-modal-cancel"
                type="button"
                :disabled="savingModelConfig"
                @click="requestModelModalClose"
              >
                {{ t("modelApis.cancel") }}
              </button>
              <button
                v-if="modelModalMode === 'create'"
                class="model-modal-submit"
                type="button"
                data-action="save-model-config"
                :disabled="savingModelConfig"
                @click="saveDraftModelConfig"
              >
                <i v-if="savingModelConfig" class="fa-solid fa-spinner fa-spin" />
                {{ t("modelApis.createSubmit") }}
              </button>
              <button
                v-else
                class="model-modal-submit"
                type="button"
                data-action="save-model-config"
                :disabled="savingModelConfig"
                @click="saveEditedModelConfig"
              >
                <i v-if="savingModelConfig" class="fa-solid fa-spinner fa-spin" />
                {{ t("modelApis.saveSubmit") }}
              </button>
            </div>
          </div>
        </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div v-if="discardModelDraftVisible" class="model-confirmation-backdrop" @click.self="keepEditingModelDraft">
        <section
          ref="discardModelDraftRef"
          class="modal-card model-confirmation-card"
          role="dialog"
          aria-modal="true"
          :aria-label="t('modelApis.discardAria')"
        >
          <header>
            <span>Unsaved Changes</span>
            <h3>{{ t("modelApis.discardTitle") }}</h3>
            <p>{{ t("modelApis.discardBody") }}</p>
          </header>
          <footer>
            <button class="model-modal-cancel" type="button" data-modal-initial-focus @click="keepEditingModelDraft">
              {{ t("modelApis.keepEditing") }}
            </button>
            <button class="model-confirmation-danger" type="button" @click="confirmDiscardModelDraft">
              {{ t("modelApis.discardConfirm") }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div v-if="pendingModelDeletion" class="model-confirmation-backdrop" @click.self="closeModelDeletionConfirm">
        <section
          ref="modelDeletionConfirmRef"
          class="modal-card model-confirmation-card delete"
          role="dialog"
          aria-modal="true"
          :aria-label="t('modelApis.deleteAria')"
        >
          <header>
            <span>Danger Zone</span>
            <h3>{{ t("modelApis.deleteTitle") }}</h3>
            <p>{{ t("modelApis.deleteBody", { name: pendingModelDeletion.name }) }}</p>
          </header>
          <p v-if="modelDeleteError" class="model-confirmation-error" role="alert">{{ modelDeleteError }}</p>
          <footer>
            <button
              class="model-modal-cancel"
              type="button"
              data-modal-initial-focus
              :disabled="deletingModelConfig"
              @click="closeModelDeletionConfirm"
            >
              {{ t("modelApis.cancel") }}
            </button>
            <button
              class="model-confirmation-danger"
              type="button"
              data-action="confirm-delete-model-config"
              :disabled="deletingModelConfig"
              :aria-busy="deletingModelConfig"
              @click="confirmModelDeletion"
            >
              <i :class="deletingModelConfig ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-trash-can'" />
              {{ t("modelApis.deleteSubmit") }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>

    <div v-if="actionNote && !modelModalVisible" class="action-toast" role="status" aria-live="polite">
      <span>{{ actionNote }}</span>
      <button type="button" :aria-label="t('modelApis.closeNote')" @click="clearActionNote">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.model-config-page {
  min-width: 0;
  min-height: 0;
  color: #1e293b;
  font-family: Inter, "Noto Sans SC", sans-serif;
}

.model-config-reset,
.model-empty-state button,
.model-modal-cancel,
.model-modal-submit,
.model-modal-test,
.model-modal-close,
.model-copy-button,
.model-secret-toggle,
.model-modal-secret-input button {
  border: 0;
  font-family: inherit;
  cursor: pointer;
}

/* Transparent shell — ManagementList owns table/toolbar/footer chrome. */
.model-config-card.management-list-card {
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.model-config-table-wrap {
  position: relative;
  display: flex;
  min-height: 0;
  flex: 1 1 auto;
}

.model-config-table-wrap > .model-config-management-list {
  height: 100%;
  min-height: 0;
  flex: 1 1 auto;
}

.model-config-management-list :deep(.data-table tbody tr:hover .model-config-name-cell strong) {
  color: #4f46e5;
}

.model-config-name-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 12px;
  overflow: hidden;
}

.model-config-name-cell > div:last-child {
  display: block;
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
}

.model-config-icon {
  display: flex;
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 1px solid #d1fae5;
  border-radius: 8px;
  background: #ecfdf5;
  color: #059669;
  font-size: 12px;
  font-weight: 600;
}

.model-config-name-cell strong,
.model-config-name-cell .aw-table-title {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.8125rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: color 0.15s ease;
}

.model-config-name-cell span,
.model-config-name-cell .aw-table-subtitle {
  display: block;
  min-width: 0;
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

.model-provider-pill {
  display: inline-block;
  padding: 2px 6px;
  border: 1px solid rgba(199, 210, 254, 0.8);
  border-radius: 4px;
  background: #eef2ff;
  color: #4338ca;
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.25;
}

.model-mono-text,
.model-api-base,
.model-latency-value {
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: var(--aw-table-mono-size, 0.82rem);
  line-height: 1.35;
}

.model-mono-text {
  color: #475569;
}

.model-secret-text,
.model-name-text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-secret-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.model-secret-value {
  min-width: 0;
  flex: 1 1 auto;
}

.model-secret-toggle {
  display: inline-flex;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
  font-size: 12px;
  transition:
    background-color 0.2s ease,
    color 0.2s ease;
}

.model-secret-toggle:hover,
.model-secret-toggle:focus-visible {
  background: #f1f5f9;
  color: #0f172a;
}

.model-api-base-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.model-api-base {
  display: block;
  min-width: 0;
  max-width: none;
  flex: 1 1 auto;
  overflow: hidden;
  color: #334155;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-latency-badge {
  display: inline-flex;
  min-width: 86px;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  padding: 3px 8px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #f8fafc;
  color: #64748b;
  white-space: nowrap;
}

.model-latency-value {
  color: currentColor;
  font-weight: 700;
}

.model-latency-label {
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-weight: var(--aw-table-meta-weight, 400);
  line-height: 1.35;
}

.model-latency-badge.healthy {
  border-color: #bbf7d0;
  background: #ecfdf5;
  color: #047857;
}

.model-latency-badge.warning {
  border-color: #fde68a;
  background: #fffbeb;
  color: #b45309;
}

.model-latency-badge.danger {
  border-color: #fecdd3;
  background: #fff1f2;
  color: #be123c;
}

.model-copy-button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
  font-size: 12px;
  cursor: pointer;
  transition:
    background-color 0.2s ease,
    color 0.2s ease;
}

.model-copy-button:hover,
.model-copy-button:focus-visible {
  background: #f1f5f9;
  color: #1e293b;
}

.model-copy-button {
  background: #f8fafc;
}

.model-empty-state {
  text-align: center;
}

.registry-empty-state {
  padding: 64px 16px;
  text-align: center;
}

.model-config-management-list :deep(.registry-empty-state) {
  min-height: 220px;
  padding: 24px 20px;
  border: 0;
  border-radius: 0;
  background: #fff;
}

.model-config-management-list :deep(.registry-empty-state .model-empty-state-icon) {
  width: 48px;
  height: 48px;
  margin-bottom: 12px;
  font-size: 20px;
}

.model-config-management-list :deep(.registry-empty-state p) {
  margin-top: 8px;
}

.model-config-management-list :deep(.registry-empty-state button) {
  margin-top: 16px;
}

.model-config-mobile-card {
  position: relative;
  padding: 16px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
}

.model-config-mobile-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.model-mobile-actions-toggle {
  display: inline-flex;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #475569;
}

.model-mobile-actions-toggle:hover,
.model-mobile-actions-toggle:focus-visible {
  background: #f1f5f9;
  color: #0f172a;
}

.model-config-mobile-card dl {
  display: grid;
  gap: 8px;
  margin: 16px 0 0;
}

.model-config-mobile-card dl > div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.model-config-mobile-card dt {
  color: #64748b;
  font-size: 11px;
  font-weight: 600;
}

.model-config-mobile-card dd {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  color: #334155;
  font-size: 12px;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-mobile-actions-menu {
  display: grid;
  gap: 4px;
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid #e2e8f0;
}

.model-mobile-actions-menu button {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: 8px;
  padding: 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #334155;
  font: inherit;
  font-size: 12px;
  text-align: left;
}

.model-mobile-actions-menu button:hover,
.model-mobile-actions-menu button:focus-visible {
  background: #f1f5f9;
}

.model-mobile-actions-menu button.danger {
  color: #be123c;
}

.model-mobile-actions-menu button.danger:hover,
.model-mobile-actions-menu button.danger:focus-visible {
  background: #fff1f2;
}

.model-load-error-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  border-bottom: 1px solid #fecaca;
  background: #fef2f2;
  color: #b91c1c;
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
}

.model-load-error-banner > button {
  margin-left: auto;
  padding: 4px 10px;
  border: 1px solid #fecaca;
  border-radius: 6px;
  background: #fff;
  color: #b91c1c;
  font-size: 11px;
  font-weight: 600;
}

.model-load-error-banner > button:hover {
  background: #fee2e2;
}

.model-empty-state-icon.error {
  background: #fef2f2;
  color: #dc2626;
}

.model-empty-state-icon {
  display: flex;
  width: 64px;
  height: 64px;
  align-items: center;
  justify-content: center;
  margin: 0 auto 16px;
  border-radius: 999px;
  background: #f8fafc;
  color: #cbd5e1;
  font-size: 24px;
}

.registry-empty-state h2 {
  margin: 0;
  color: #1e293b;
  font-size: 15px;
  font-weight: 700;
  line-height: 22px;
}

.registry-empty-state p {
  margin: 10px auto 0;
  max-width: 380px;
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
  line-height: 20px;
}

.registry-empty-state .primary-button,
.registry-empty-state .ghost-button {
  margin-top: 20px;
}

.model-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  background: rgba(15, 23, 42, 0.6);
  backdrop-filter: blur(12px);
}

.model-confirmation-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3100;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  background: rgba(15, 23, 42, 0.6);
  backdrop-filter: blur(12px);
}

.model-modal-card {
  width: 100%;
  max-width: 560px;
  max-height: calc(100vh - 32px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid #f1f5f9;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 25px 50px -12px rgba(15, 23, 42, 0.25);
}

.model-confirmation-card {
  width: min(100%, 420px);
  padding: 24px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 25px 50px -12px rgba(15, 23, 42, 0.25);
}

.model-confirmation-card.delete {
  border-color: #fecaca;
}

.model-confirmation-card header > span {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.model-confirmation-card h3 {
  margin: 6px 0 0;
  color: #0f172a;
  font-size: 18px;
  font-weight: 700;
  line-height: 26px;
}

.model-confirmation-card p {
  margin: 8px 0 0;
  color: #475569;
  font-size: 12px;
  line-height: 20px;
}

.model-confirmation-card footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 20px;
}

.model-confirmation-danger {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 6px 14px;
  border: 1px solid #be123c;
  border-radius: 8px;
  background: #be123c;
  color: #fff;
  font-family: inherit;
  font-size: 12px;
  font-weight: 700;
  line-height: 16px;
}

.model-confirmation-danger:hover:not(:disabled),
.model-confirmation-danger:focus-visible {
  border-color: #9f1239;
  background: #9f1239;
}

.model-confirmation-danger:disabled {
  cursor: not-allowed;
  opacity: 0.64;
}

.model-confirmation-error {
  color: #b91c1c !important;
  font-weight: 700;
}

.model-modal-head {
  display: flex;
  align-items: center;
  gap: 16px;
  flex: 0 0 auto;
  padding: 24px;
  border-bottom: 1px solid #eef2f7;
  background: #fff;
  color: #0f172a;
}

.model-modal-head > div:nth-child(2) {
  min-width: 0;
  flex: 1 1 auto;
}

.model-modal-icon {
  display: flex;
  width: 56px;
  height: 56px;
  align-items: center;
  justify-content: center;
  border-radius: 14px;
  background: #d1f0d0;
  color: #15803d;
  font-size: 22px;
}

.model-modal-close {
  display: inline-flex;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
  transition:
    background-color 0.2s ease,
    color 0.2s ease;
}

.model-modal-close:hover,
.model-modal-close:focus-visible {
  background: #f1f5f9;
  color: #0f172a;
}

.model-modal-close:disabled {
  cursor: not-allowed;
  opacity: 0.64;
}

.model-modal-head h3 {
  margin: 0;
  color: #0f172a;
  font-size: 20px;
  font-weight: 700;
  line-height: 28px;
}

.model-modal-head p {
  margin: 2px 0 0;
  color: #94a3b8;
  font-size: 13px;
  font-weight: 500;
  line-height: 20px;
}

.model-modal-form {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 16px;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding: 24px;
  overscroll-behavior: contain;
}

.model-modal-fieldset {
  margin: 0;
  padding: 0;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #f8fafc;
  flex: 0 0 auto;
  overflow: visible;
}

.model-runtime-section {
  overflow: hidden;
}

.model-runtime-section-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  min-height: 52px;
  padding: 12px 14px;
  color: #0f172a;
  background: transparent;
  border: 0;
  cursor: pointer;
  text-align: left;
  transition: background-color 0.15s ease;
}

.model-runtime-section-toggle:hover {
  background: rgba(255, 255, 255, 0.55);
}

.model-runtime-section.open .model-runtime-section-toggle {
  border-bottom: 1px solid #e2e8f0;
  background: #fff;
}

.model-runtime-section-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-size: 13px;
  font-weight: 700;
}

.model-runtime-section-title > i {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: #0f766e;
  background: #f0fdfa;
  border: 1px solid #ccfbf1;
  border-radius: 8px;
  font-size: 12px;
}

.model-runtime-section-meta {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.model-runtime-section-summary {
  max-width: 180px;
  overflow: hidden;
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-runtime-section-chevron {
  color: #94a3b8;
  font-size: 11px;
  transition:
    transform 0.18s ease,
    color 0.15s ease;
}

.model-runtime-section.open .model-runtime-section-chevron {
  color: #0f766e;
  transform: rotate(180deg);
}

.model-runtime-section-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px 16px;
  background: #f8fafc;
}

.model-runtime-section-body .model-modal-fieldset-help {
  margin: 0;
}

.model-runtime-section-body .model-context-presets {
  margin: 0;
}

.model-runtime-section-body .model-modal-field {
  margin-bottom: 0;
}

.model-modal-fieldset-help {
  margin: 0 0 12px;
  color: #64748b;
  font-size: 12px;
  line-height: 1.45;
}

.model-modal-fieldset .model-modal-field {
  margin-bottom: 12px;
}

.model-modal-fieldset .model-modal-field:last-child {
  margin-bottom: 0;
}

.model-modal-fieldset :deep(.model-modal-select) {
  display: block;
  width: 100%;
}

.model-modal-fieldset :deep(.model-modal-select .el-select),
.model-modal-fieldset :deep(.model-modal-select .app-select) {
  width: 100%;
}

.model-modal-fieldset :deep(.model-modal-select .el-select__wrapper) {
  min-height: 44px;
  padding: 0 12px;
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 0 0 1px #e2e8f0 inset;
  font-size: 12px;
}

.model-context-presets {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 0 14px;
}

.model-context-preset {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  min-height: 44px;
  padding: 8px 12px;
  color: #334155;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  font-size: 13px;
  font-weight: 700;
  line-height: 1.2;
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease,
    box-shadow 0.15s ease;
}

.model-context-preset small {
  color: #64748b;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.model-context-preset:hover {
  border-color: #94a3b8;
  background: #f8fafc;
}

.model-context-preset.active {
  color: #0f766e;
  background: #f0fdfa;
  border-color: #5eead4;
  box-shadow: 0 0 0 1px rgba(13, 148, 136, 0.2);
}

.model-context-preset.active small {
  color: #0f766e;
}

.model-modal-field-hint {
  display: block;
  margin-top: 6px;
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.45;
}

.model-runtime-advanced-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin: 8px 0 0;
  min-height: 36px;
  padding: 0 12px;
  color: #334155;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease,
    color 0.15s ease,
    box-shadow 0.15s ease;
}

.model-runtime-advanced-toggle:hover {
  color: #0f172a;
  border-color: #cbd5e1;
  background: #f8fafc;
}

.model-runtime-advanced-toggle.open {
  color: #0f766e;
  background: #f0fdfa;
  border-color: #99f6e4;
  box-shadow: 0 0 0 1px rgba(13, 148, 136, 0.12);
}

.model-runtime-advanced-toggle > .fa-sliders {
  font-size: 11px;
  opacity: 0.85;
}

.model-runtime-advanced-chevron {
  margin-left: 2px;
  font-size: 10px;
  color: #94a3b8;
  transition: transform 0.15s ease;
}

.model-runtime-advanced-toggle.open .model-runtime-advanced-chevron {
  color: #0f766e;
  transform: rotate(180deg);
}

.model-runtime-advanced {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 10px;
  padding: 12px 14px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
}

.model-modal-field {
  display: block;
}

.model-modal-field-label {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
  color: #475569;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.05em;
  line-height: 14px;
  text-transform: uppercase;
}

.model-field-lock {
  color: #94a3b8;
  font-size: 10px;
}

.model-field-required {
  color: #b91c1c;
}

.model-modal-field input {
  width: 100%;
  min-height: 44px;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  outline: none;
  background: #f8fafc;
  color: #1e293b;
  font-family: inherit;
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
  transition:
    background-color 0.2s ease,
    border-color 0.2s ease,
    box-shadow 0.2s ease;
}

.model-modal-field input.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.model-modal-secret-input {
  position: relative;
}

.model-modal-secret-input input {
  padding-right: 44px;
}

.model-modal-secret-input button {
  position: absolute;
  top: 0;
  right: 0;
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border-radius: 0 8px 8px 0;
  background: transparent;
  color: #64748b;
  transition:
    background-color 0.2s ease,
    color 0.2s ease;
}

.model-modal-secret-input button:hover,
.model-modal-secret-input button:focus-visible {
  background: #f1f5f9;
  color: #0f172a;
}

.model-modal-field input:focus {
  border-color: rgba(16, 185, 129, 0.6);
  background: #fff;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.15);
}

.model-modal-field input[aria-invalid="true"] {
  border-color: #dc2626;
  background: #fff;
  box-shadow: 0 0 0 2px rgba(220, 38, 38, 0.12);
}

.model-field-error {
  display: none;
  margin-top: 6px;
  color: #b91c1c;
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
}

.model-field-error.visible {
  display: block;
}

.model-modal-field input:disabled {
  background: #f1f5f9;
  color: #64748b;
  cursor: not-allowed;
}

.model-modal-note {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px;
  border: 1px solid #c7d2fe;
  border-radius: 8px;
  background: #eef2ff;
  color: #4338ca;
  font-size: 10px;
  line-height: 14px;
}

.model-modal-note > i {
  margin-top: 2px;
  color: #6366f1;
  font-size: 12px;
}

.model-modal-note strong {
  font-weight: 700;
}

.model-modal-note p {
  margin: 2px 0 0;
  color: rgba(67, 56, 202, 0.9);
  font-size: 10px;
  font-weight: 300;
  line-height: 14px;
}

.model-modal-feedback {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 600;
  line-height: 14px;
}

.model-modal-feedback.success {
  border: 1px solid #bbf7d0;
  background: #ecfdf5;
  color: #047857;
}

.model-modal-feedback.error {
  border: 1px solid #fecaca;
  background: #fef2f2;
  color: #b91c1c;
}

.model-modal-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex: 0 0 auto;
  padding: 16px 24px;
  border-top: 1px solid #f1f5f9;
  background: #f8fafc;
}

.model-modal-actions > span {
  color: #64748b;
  font-size: 11px;
  font-weight: 500;
  line-height: 14px;
}

.model-modal-actions > div {
  display: flex;
  gap: 8px;
}

.model-modal-cancel {
  min-height: 44px;
  padding: 6px 14px;
  border-radius: 8px;
  background: transparent;
  color: #334155;
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
  transition: background-color 0.2s ease;
}

.model-modal-cancel:hover {
  background: #e2e8f0;
}

.model-modal-submit {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 6px 16px;
  border-radius: 8px;
  background: var(--aw-primary, #0d9488);
  color: #fff;
  box-shadow: 0 1px 2px rgba(13, 148, 136, 0.18);
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
  transition: background-color 0.2s ease;
}

.model-modal-test {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 6px 14px;
  border: 1px solid #bfdbfe;
  border-radius: 8px;
  background: #eff6ff;
  color: #2563eb;
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
  transition:
    background-color 0.2s ease,
    border-color 0.2s ease;
}

.model-modal-submit:hover:not(:disabled) {
  background: #0f766e;
}

.model-modal-test:hover:not(:disabled) {
  border-color: #93c5fd;
  background: #dbeafe;
}

.model-modal-cancel:disabled,
.model-modal-submit:disabled,
.model-modal-test:disabled {
  cursor: not-allowed;
  opacity: 0.64;
}

.model-credential-state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #b45309;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
}

.model-credential-state.configured {
  color: #047857;
}

@media (max-width: 900px) {
  .model-status-filter {
    width: 100%;
  }

  .model-modal-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .model-modal-actions > div {
    justify-content: flex-end;
  }
}
</style>
