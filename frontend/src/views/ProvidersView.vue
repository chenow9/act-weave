<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useIntegrationStore } from "../stores/integration";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  CapabilityProvider,
  ProviderAsset,
  ProviderAuthContract,
  ProviderAuthField,
  ProviderAuthScheme,
} from "../types/domain";
import {
  authSchemeSummary,
  defaultOAuthContract,
  noAuthenticationContract,
  providerAuthContract,
} from "../utils/provider-auth";
import { buildOutboundIdentityContract } from "./provider-outbound-identity";

type ProviderStatusFilter = "ALL" | "ACTIVE" | "ERROR" | "DISABLED";
type AuthEditorMode = "" | "NONE" | "OAUTH2_CLIENT";
type OAuthFieldKind = "TEXT" | "SELECT";
type TokenParameterSource = "FIELD" | "VALUE";
type SortOrder = "asc" | "desc";

interface OAuthFieldDraft {
  id: string;
  key: string;
  label: string;
  kind: OAuthFieldKind;
  required: boolean;
  placeholder: string;
  help: string;
  optionsText: string;
}

interface TokenParameterDraft {
  id: string;
  name: string;
  source: TokenParameterSource;
  field: string;
  value: string;
  required: boolean;
}

interface ProviderFormDraft {
  id: string;
  name: string;
  serviceBaseUrl: string;
  documentUrl: string;
  sourceRevision: string;
  allowedCIDRs: string;
  verificationMethod: "GET" | "HEAD" | "POST";
  verificationPath: string;
  expectedStatuses: string;
  discoveryMode: string;
  /** @deprecated legacy editor; dual-mode uses supportedModes instead. */
  authMode: AuthEditorMode;
  legacyAuthentication: boolean;
  schemeKey: string;
  displayName: string;
  description: string;
  tokenUrlTemplate: string;
  clientAuthMethod: "client_secret_basic" | "client_secret_post";
  extraFields: OAuthFieldDraft[];
  tokenParameters: TokenParameterDraft[];
  accessTokenPath: string;
  tokenTypePath: string;
  expiresInPath: string;
  renewalTokenPath: string;
  injectionHeaderName: string;
  injectionPrefix: string;
  refreshStrategy: "CLIENT_CREDENTIALS" | "REFRESH_TOKEN";
  preservedSchemes: ProviderAuthScheme[];
  /** Dual-mode outbound-identity.v1 (post hard-cutover). */
  supportBrokerObo: boolean;
  supportRequestPassthrough: boolean;
  brokerTokenEndpoint: string;
  brokerAudience: string;
  brokerAllowedScopes: string;
  businessInjectionHeader: string;
  businessInjectionPrefix: string;
}

interface ProviderSyncOutcome {
  id?: string;
  status: string;
  discoveredCount: number;
  changedCount: number;
  errorSummary: Record<string, unknown>;
}

let draftRowSequence = 0;

const integration = useIntegrationStore();
const workspaces = useWorkspaceStore();
const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));

const loading = ref(false);
const hasLoaded = ref(false);
const loadError = ref<string | null>(null);
const search = ref("");
const statusFilter = ref<ProviderStatusFilter>("ALL");
const page = ref(1);
const pageSize = ref(20);
const sortBy = ref<string>();
const sortOrder = ref<SortOrder>();

const expandedProviderId = ref("");
const assetLoadingProviderIds = ref<string[]>([]);
const assetErrors = ref<Record<string, string>>({});
const materializingAssetId = ref("");
const syncingProviderIds = ref<string[]>([]);
const syncResults = ref<Record<string, ProviderSyncOutcome>>({});

const editorVisible = ref(false);
const editorMode = ref<"create" | "edit">("create");
const editingProvider = ref<CapabilityProvider | null>(null);
const providerDraft = ref<ProviderFormDraft>(emptyProviderDraft());
const providerNameInput = ref<HTMLInputElement | null>(null);
const editorPanel = ref<HTMLElement | null>(null);
const saving = ref(false);
const formError = ref("");
/** Block-level zero-mode error for outbound identity (AC-09). */
const identityModeError = ref("");
const identityModeGroupRef = ref<HTMLElement | null>(null);

const pendingDeleteProvider = ref<CapabilityProvider | null>(null);
const deleteConfirmText = ref("");
const deleteError = ref("");
const deleting = ref(false);

const actionNote = ref("");
const actionTone = ref<"success" | "warning" | "error">("success");

const statusOptions = [
  { label: "全部", value: "ALL" },
  { label: "运行中", value: "ACTIVE" },
  { label: "异常", value: "ERROR" },
  { label: "已停用", value: "DISABLED" },
];
const discoveryModeOptions = computed(() => [
  { label: "不启用自动发现", value: "MANUAL" },
  { label: "按需发现 OpenAPI", value: "ON_DEMAND" },
  ...(providerDraft.value.discoveryMode === "POLLING"
    ? [{ label: "旧配置：定时轮询（当前无调度器）", value: "POLLING", disabled: true }]
    : []),
]);
const verificationMethodOptions = ["GET", "HEAD", "POST"].map((value) => ({ label: value, value }));
const clientAuthMethodOptions = ["client_secret_basic", "client_secret_post"].map((value) => ({ label: value, value }));
const oauthFieldKindOptions = [
  { label: "文本", value: "TEXT" },
  { label: "下拉选择", value: "SELECT" },
];
const tokenParameterSourceOptions = [
  { label: "Connection 字段", value: "FIELD" },
  { label: "固定值", value: "VALUE" },
];
const refreshStrategyOptions = [
  { label: "重新获取 Client Credentials", value: "CLIENT_CREDENTIALS" },
  { label: "使用 Refresh Token", value: "REFRESH_TOKEN" },
];

const providerColumns = computed<ManagementListColumn<CapabilityProvider>[]>(() => [
  { key: "name", label: "Provider", width: 244, sortable: true, sortKey: "name", getValue: (provider) => provider.name },
  { key: "status", label: "状态", width: 108, align: "center", headerAlign: "center", sortable: true, sortKey: "status", getValue: (provider) => provider.status },
  { key: "serviceBaseUrl", label: "运行地址", width: 250, hidable: true, getValue: providerServiceBaseUrl },
  { key: "documentUrl", label: "OpenAPI（可选）", width: 270, hidable: true, getValue: providerDocumentUrl },
  { key: "authentication", label: "认证", width: 190, hidable: true, getValue: authSchemeSummary },
  { key: "lastSyncedAt", label: "最近同步", width: 164, hidable: true, sortable: true, sortKey: "lastSyncedAt", getValue: (provider) => provider.lastSyncedAt || "" },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
]);

const filteredProviders = computed(() => {
  const needle = search.value.trim().toLocaleLowerCase();
  return integration.providers.filter((provider) => {
    if (statusFilter.value !== "ALL" && provider.status !== statusFilter.value) return false;
    if (!needle) return true;
    return [provider.name, provider.kind, provider.status, providerServiceBaseUrl(provider), providerDocumentUrl(provider), authSchemeSummary(provider)]
      .some((value) => value.toLocaleLowerCase().includes(needle));
  });
});

const sortedProviders = computed(() => {
  if (!sortBy.value || !sortOrder.value) return filteredProviders.value;
  const rows = [...filteredProviders.value];
  rows.sort((left, right) => {
    const leftValue = providerSortValue(left, sortBy.value || "");
    const rightValue = providerSortValue(right, sortBy.value || "");
    const comparison = leftValue.localeCompare(rightValue, "zh-Hans");
    return sortOrder.value === "asc" ? comparison : -comparison;
  });
  return rows;
});

const providerRows = computed(() => sortedProviders.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value));
const providerPagination = computed(() => ({ page: page.value, pageSize: pageSize.value, total: sortedProviders.value.length, pageSizeOptions: [10, 20, 50] }));
const availableOAuthFieldKeys = computed(() => ["clientId", "scope", ...providerDraft.value.extraFields.map((field) => field.key.trim()).filter(Boolean)]);
const availableOAuthFieldOptions = computed(() => [
  { label: "请选择", value: "" },
  ...availableOAuthFieldKeys.value.map((fieldKey) => ({ label: fieldKey, value: fieldKey })),
]);
const deleteConfirmMatches = computed(() => deleteConfirmText.value.trim() === pendingDeleteProvider.value?.name);

onMounted(async () => {
  window.addEventListener("keydown", handleGlobalKeydown);
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (hasWorkspaceContext.value) await loadProviders();
    else hasLoaded.value = true;
  } catch (error) {
    loadError.value = errorMessage(error, "加载 Provider 失败，请稍后重试。");
    hasLoaded.value = true;
  }
});

onBeforeUnmount(() => window.removeEventListener("keydown", handleGlobalKeydown));

async function loadProviders() {
  if (!hasWorkspaceContext.value) return;
  loading.value = true;
  loadError.value = null;
  try {
    await integration.loadProviders();
    clampPage();
  } catch (error) {
    loadError.value = errorMessage(error, "加载 Provider 失败，请稍后重试。");
  } finally {
    loading.value = false;
    hasLoaded.value = true;
  }
}

function updateSearch(value: string) {
  search.value = value;
  page.value = 1;
}

function updateStatusFilter(value: string) {
  statusFilter.value = value as ProviderStatusFilter;
  page.value = 1;
}

function resetFilters() {
  search.value = "";
  statusFilter.value = "ALL";
  page.value = 1;
}

function changePage(value: { page: number; pageSize: number }) {
  page.value = value.pageSize === pageSize.value ? value.page : 1;
  pageSize.value = value.pageSize;
}

function changeSort(value: { sortBy?: string; sortOrder?: SortOrder }) {
  sortBy.value = value.sortBy;
  sortOrder.value = value.sortOrder;
  page.value = 1;
}

function clampPage() {
  const lastPage = Math.max(1, Math.ceil(filteredProviders.value.length / pageSize.value));
  page.value = Math.min(page.value, lastPage);
}

function providerSortValue(provider: CapabilityProvider, key: string) {
  if (key === "name") return provider.name;
  if (key === "status") return provider.status;
  if (key === "lastSyncedAt") return provider.lastSyncedAt || "";
  return "";
}

function providerServiceBaseUrl(provider: CapabilityProvider) {
  const endpoint = asRecord(provider.endpointConfig);
  return firstString(endpoint.serviceBaseUrl, endpoint.baseUrl, endpoint.url) || "未配置";
}

function providerDocumentUrl(provider: CapabilityProvider) {
  const endpoint = asRecord(provider.endpointConfig);
  const discovery = asRecord(endpoint.discovery);
  return firstString(discovery.documentUrl, endpoint.sourceUri) || "未配置（不启用自动发现）";
}

function hasProviderDocument(provider: CapabilityProvider) {
  const endpoint = asRecord(provider.endpointConfig);
  const discovery = asRecord(endpoint.discovery);
  return Boolean(firstString(discovery.documentUrl, endpoint.sourceUri));
}

function canSyncProvider(provider: CapabilityProvider) {
  return hasProviderDocument(provider) && provider.discoveryMode !== "MANUAL";
}

function providerSyncTitle(provider: CapabilityProvider) {
  if (!hasProviderDocument(provider)) return "未配置 OpenAPI 文档，无法自动同步；不影响 Connection 和运行调用";
  if (provider.discoveryMode === "MANUAL") return "发现策略为“不启用自动发现”；切换为按需发现后可同步";
  return "从 OpenAPI 文档同步能力";
}

function providerVerificationSummary(provider: CapabilityProvider) {
  const verification = asRecord(asRecord(provider.endpointConfig).verification);
  const statuses = Array.isArray(verification.expectedStatuses) ? verification.expectedStatuses.join(", ") : "200, 204";
  return `${firstString(verification.method) || "GET"} ${firstString(verification.path) || "/"} · ${statuses}`;
}

function statusLabel(status: string) {
  if (status === "ACTIVE") return "运行中";
  if (status === "ERROR") return "异常";
  if (status === "DISABLED") return "已停用";
  return status || "未知";
}

function statusTone(status: string) {
  if (status === "ACTIVE") return "active";
  if (status === "ERROR") return "error";
  if (status === "DISABLED") return "disabled";
  return "neutral";
}

function formatDate(value?: string) {
  if (!value) return "尚未同步";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN");
}

function isSyncing(providerId: string) {
  return syncingProviderIds.value.includes(providerId);
}

function isLoadingAssets(providerId: string) {
  return assetLoadingProviderIds.value.includes(providerId);
}

function providerMenuActions(provider: CapabilityProvider): ManagementRowAction[] {
  const syncing = isSyncing(provider.id);
  const syncAvailable = canSyncProvider(provider);
  return [
    {
      key: "edit",
      label: `编辑 ${provider.name}`,
      shortLabel: "编辑",
      icon: "fa-solid fa-pen-to-square",
    },
    {
      key: "sync",
      label: `同步 ${provider.name}`,
      shortLabel: "同步",
      icon: "fa-solid fa-rotate",
      tone: "primary",
      disabled: !syncAvailable,
      loading: syncing,
      disabledReason: !syncAvailable ? providerSyncTitle(provider) : undefined,
    },
    {
      key: "assets",
      label: `查看 ${provider.name} 的能力资产`,
      shortLabel: "查看能力资产",
      icon: "fa-solid fa-cubes",
    },
    {
      key: "delete",
      label: `删除 ${provider.name}`,
      shortLabel: "删除",
      icon: "fa-solid fa-trash-can",
      tone: "danger",
    },
  ];
}

function handleProviderRowAction(actionKey: string, provider: CapabilityProvider) {
  if (actionKey === "edit") {
    openEditEditor(provider);
    return;
  }
  if (actionKey === "sync") {
    void syncProvider(provider);
    return;
  }
  if (actionKey === "assets") {
    void toggleAssets(provider);
    return;
  }
  if (actionKey === "delete") requestDeleteProvider(provider);
}

async function syncProvider(provider: CapabilityProvider) {
  if (isSyncing(provider.id)) return;
  if (!canSyncProvider(provider)) {
    showActionNote(`${provider.name} 当前未启用 OpenAPI 自动发现；这不影响 Connection 和运行调用。`, "warning");
    return;
  }
  syncingProviderIds.value = [...syncingProviderIds.value, provider.id];
  delete syncResults.value[provider.id];
  try {
    const raw = await integration.syncProvider(provider.id) as ProviderSyncOutcome;
    const result = normalizeSyncOutcome(raw);
    syncResults.value = { ...syncResults.value, [provider.id]: result };
    if (result.status === "SUCCEEDED") {
      showActionNote(`${provider.name} 同步完成：发现 ${result.discoveredCount}，变化 ${result.changedCount}。`);
      if (expandedProviderId.value === provider.id) await loadAssets(provider.id);
    } else {
      showActionNote(`${provider.name} 同步失败：${syncErrorText(result)}`, "error");
    }
  } catch (error) {
    const message = errorMessage(error, "同步请求失败。");
    syncResults.value = {
      ...syncResults.value,
      [provider.id]: { status: "FAILED", discoveredCount: 0, changedCount: 0, errorSummary: { message } },
    };
    showActionNote(`${provider.name} 同步失败：${message}`, "error");
  } finally {
    syncingProviderIds.value = syncingProviderIds.value.filter((id) => id !== provider.id);
  }
}

async function toggleAssets(provider: CapabilityProvider) {
  if (expandedProviderId.value === provider.id) {
    expandedProviderId.value = "";
    return;
  }
  expandedProviderId.value = provider.id;
  await loadAssets(provider.id);
}

async function loadAssets(providerId: string) {
  if (isLoadingAssets(providerId)) return;
  assetLoadingProviderIds.value = [...assetLoadingProviderIds.value, providerId];
  assetErrors.value = { ...assetErrors.value, [providerId]: "" };
  try {
    await integration.loadProviderAssets(providerId);
  } catch (error) {
    assetErrors.value = { ...assetErrors.value, [providerId]: errorMessage(error, "加载能力资产失败。") };
  } finally {
    assetLoadingProviderIds.value = assetLoadingProviderIds.value.filter((id) => id !== providerId);
  }
}

async function materializeAsset(provider: CapabilityProvider, asset: ProviderAsset) {
  if (materializingAssetId.value || asset.materializedCapabilityId) return;
  materializingAssetId.value = asset.id;
  try {
    await integration.materializeProviderAsset(provider.id, asset.id);
    showActionNote(`${asset.name} 已物化为 Tool Draft。`);
  } catch (error) {
    showActionNote(errorMessage(error, `${asset.name} 物化失败。`), "error");
  } finally {
    materializingAssetId.value = "";
  }
}

function openCreateEditor() {
  identityModeError.value = "";
  editorMode.value = "create";
  editingProvider.value = null;
  providerDraft.value = emptyProviderDraft();
  formError.value = "";
  editorVisible.value = true;
  focusProviderName();
}

function openEditEditor(provider: CapabilityProvider) {
  editorMode.value = "edit";
  editingProvider.value = provider;
  providerDraft.value = draftFromProvider(provider);
  formError.value = "";
  identityModeError.value = "";
  editorVisible.value = true;
  focusProviderName();
}

function closeEditor() {
  if (saving.value) return;
  dismissEditor();
}

function dismissEditor() {
  editorVisible.value = false;
  editingProvider.value = null;
  formError.value = "";
  identityModeError.value = "";
}

function focusProviderName() {
  void nextTick(() => providerNameInput.value?.focus());
}

function handleAuthModeChange() {
  formError.value = "";
  if (providerDraft.value.authMode !== "OAUTH2_CLIENT") return;
  if (!providerDraft.value.schemeKey) providerDraft.value.schemeKey = "oauth2-client";
  if (!providerDraft.value.displayName) providerDraft.value.displayName = "OAuth2 Client Credentials";
  if (!providerDraft.value.accessTokenPath) providerDraft.value.accessTokenPath = "access_token";
  if (!providerDraft.value.injectionHeaderName) providerDraft.value.injectionHeaderName = "Authorization";
}

function addOAuthField() {
  providerDraft.value.extraFields.push({
    id: nextDraftRowId("field"), key: "", label: "", kind: "TEXT", required: false, placeholder: "", help: "", optionsText: "",
  });
}

function removeOAuthField(id: string) {
  providerDraft.value.extraFields = providerDraft.value.extraFields.filter((field) => field.id !== id);
  providerDraft.value.tokenParameters.forEach((parameter) => {
    if (parameter.field && !availableOAuthFieldKeys.value.includes(parameter.field)) parameter.field = "";
  });
}

function addTokenParameter() {
  providerDraft.value.tokenParameters.push({
    id: nextDraftRowId("parameter"), name: "", source: "FIELD", field: "", value: "", required: false,
  });
}

function removeTokenParameter(id: string) {
  providerDraft.value.tokenParameters = providerDraft.value.tokenParameters.filter((parameter) => parameter.id !== id);
}

function changeTokenParameterSource(parameter: TokenParameterDraft) {
  if (parameter.source === "FIELD") parameter.value = "";
  else parameter.field = "";
}

async function saveProvider() {
  if (saving.value) return;
  const validationError = validateProviderDraft(providerDraft.value);
  if (validationError) {
    formError.value = validationError;
    if (!providerDraft.value.supportBrokerObo && !providerDraft.value.supportRequestPassthrough) {
      identityModeError.value = "至少选择一种";
      await nextTick();
      const focusTarget =
        identityModeGroupRef.value?.querySelector<HTMLInputElement>('input[type="checkbox"]') ||
        identityModeGroupRef.value;
      focusTarget?.focus?.();
    } else {
      identityModeError.value = "";
    }
    return;
  }
  saving.value = true;
  formError.value = "";
  identityModeError.value = "";
  try {
    const provider = providerFromDraft(providerDraft.value, editingProvider.value);
    const saved = editorMode.value === "edit"
      ? await integration.updateProvider(provider)
      : await integration.createProvider(provider);
    showActionNote(`${saved.name} Provider 已${editorMode.value === "edit" ? "更新" : "创建"}。`);
    dismissEditor();
  } catch (error) {
    formError.value = errorMessage(error, "保存 Provider 失败，请检查端点和认证契约。");
  } finally {
    saving.value = false;
  }
}

function clearIdentityModeErrorIfResolved() {
  if (providerDraft.value.supportBrokerObo || providerDraft.value.supportRequestPassthrough) {
    identityModeError.value = "";
  }
}

function requestDeleteProvider(provider: CapabilityProvider) {
  pendingDeleteProvider.value = provider;
  deleteConfirmText.value = "";
  deleteError.value = "";
}

function closeDeleteDialog() {
  if (deleting.value) return;
  dismissDeleteDialog();
}

function dismissDeleteDialog() {
  pendingDeleteProvider.value = null;
  deleteConfirmText.value = "";
  deleteError.value = "";
}

async function confirmDeleteProvider() {
  const provider = pendingDeleteProvider.value;
  if (!provider || deleting.value || !deleteConfirmMatches.value) return;
  deleting.value = true;
  deleteError.value = "";
  try {
    await integration.deleteProvider(provider.id);
    if (expandedProviderId.value === provider.id) expandedProviderId.value = "";
    showActionNote(`${provider.name} Provider 已删除。`, "warning");
    dismissDeleteDialog();
    clampPage();
  } catch (error) {
    deleteError.value = errorMessage(error, "删除失败；请先检查关联的 Connection 和 Tool。");
  } finally {
    deleting.value = false;
  }
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  if (pendingDeleteProvider.value) {
    event.preventDefault();
    closeDeleteDialog();
    return;
  }
  if (editorVisible.value) {
    event.preventDefault();
    closeEditor();
  }
}

function trapEditorFocus(event: KeyboardEvent) {
  if (event.key !== "Tab" || !editorPanel.value) return;
  const focusable = Array.from(editorPanel.value.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])"))
    .filter((element) => element.offsetParent !== null);
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

function emptyProviderDraft(): ProviderFormDraft {
  const oauth = defaultOAuthContract("").schemes[0];
  return {
    id: "",
    name: "",
    serviceBaseUrl: "",
    documentUrl: "",
    sourceRevision: "",
    allowedCIDRs: "",
    verificationMethod: "GET",
    verificationPath: "",
    expectedStatuses: "200, 204",
    discoveryMode: "MANUAL",
    authMode: "NONE",
    legacyAuthentication: false,
    schemeKey: oauth.key,
    displayName: oauth.displayName,
    description: oauth.description || "",
    tokenUrlTemplate: oauth.oauth2?.tokenUrlTemplate || "",
    clientAuthMethod: oauth.oauth2?.clientAuthMethod || "client_secret_basic",
    extraFields: [],
    tokenParameters: [],
    accessTokenPath: oauth.oauth2?.response.accessTokenPath || "access_token",
    tokenTypePath: oauth.oauth2?.response.tokenTypePath || "token_type",
    expiresInPath: oauth.oauth2?.response.expiresInPath || "expires_in",
    renewalTokenPath: oauth.oauth2?.response.renewalTokenPath || "",
    injectionHeaderName: oauth.oauth2?.injection.headerName || "Authorization",
    injectionPrefix: oauth.oauth2?.injection.prefix || "Bearer",
    refreshStrategy: oauth.oauth2?.refreshStrategy || "CLIENT_CREDENTIALS",
    preservedSchemes: [],
    supportBrokerObo: false,
    supportRequestPassthrough: true,
    brokerTokenEndpoint: "",
    brokerAudience: "",
    brokerAllowedScopes: "",
    businessInjectionHeader: "Authorization",
    businessInjectionPrefix: "Bearer",
  };
}

function draftFromProvider(provider: CapabilityProvider): ProviderFormDraft {
  const draft = emptyProviderDraft();
  const endpoint = asRecord(provider.endpointConfig);
  const discovery = asRecord(endpoint.discovery);
  const verification = asRecord(endpoint.verification);
  const egress = asRecord(endpoint.egress);
  draft.id = provider.id;
  draft.name = provider.name;
  draft.serviceBaseUrl = firstString(endpoint.serviceBaseUrl, endpoint.baseUrl, endpoint.url);
  draft.documentUrl = firstString(discovery.documentUrl, endpoint.sourceUri);
  draft.sourceRevision = firstString(discovery.sourceRevision);
  const configuredCIDRs = Array.isArray(egress.allowedCIDRs)
    ? egress.allowedCIDRs
    : Array.isArray(egress.AllowedCIDRs) ? egress.AllowedCIDRs : [];
  draft.allowedCIDRs = configuredCIDRs.filter((value): value is string => typeof value === "string").join("\n");
  draft.verificationMethod = (["GET", "HEAD", "POST"].includes(firstString(verification.method).toUpperCase())
    ? firstString(verification.method).toUpperCase()
    : "GET") as ProviderFormDraft["verificationMethod"];
  draft.verificationPath = firstString(verification.path);
  draft.expectedStatuses = Array.isArray(verification.expectedStatuses) && verification.expectedStatuses.length
    ? verification.expectedStatuses.join(", ")
    : "200, 204";
  draft.discoveryMode = provider.discoveryMode || "ON_DEMAND";

  const outbound = asRecord((provider.driverConfig as Record<string, unknown> | undefined)?.outboundIdentity);
  if (outbound.schemaVersion === "outbound-identity.v1" || Array.isArray(outbound.supportedModes)) {
    const modes = Array.isArray(outbound.supportedModes) ? outbound.supportedModes.map((m) => String(m).toUpperCase()) : [];
    draft.supportBrokerObo = modes.includes("BROKER_OBO");
    draft.supportRequestPassthrough = modes.includes("REQUEST_PASSTHROUGH");
    const broker = asRecord(outbound.brokerObo);
    const passthrough = asRecord(outbound.requestPassthrough);
    const injection = asRecord(broker.businessInjection || passthrough.businessInjection);
    draft.brokerTokenEndpoint = firstString(broker.tokenEndpoint);
    draft.brokerAudience = firstString(broker.audience);
    draft.brokerAllowedScopes = Array.isArray(broker.allowedScopes) ? broker.allowedScopes.map(String).join(" ") : "";
    draft.businessInjectionHeader = firstString(injection.headerName) || "Authorization";
    draft.businessInjectionPrefix = firstString(injection.prefix) || "Bearer";
    draft.legacyAuthentication = false;
    draft.authMode = "";
    return draft;
  }

  const contract = providerAuthContract(provider);
  if (!contract) {
    draft.authMode = "";
    draft.legacyAuthentication = true;
    return draft;
  }
  const scheme = contract.schemes.find((item) => item.key === contract.defaultSchemeKey) || contract.schemes[0];
  draft.preservedSchemes = contract.schemes.filter((item) => item.key !== scheme.key);
  draft.legacyAuthentication = true;
  if (scheme.type === "NONE") {
    draft.authMode = "NONE";
    return draft;
  }
  hydrateOAuthDraft(draft, scheme);
  return draft;
}

function hydrateOAuthDraft(draft: ProviderFormDraft, scheme: ProviderAuthScheme) {
  const oauth = scheme.oauth2;
  draft.authMode = "OAUTH2_CLIENT";
  draft.schemeKey = scheme.key;
  draft.displayName = scheme.displayName;
  draft.description = scheme.description || "";
  if (!oauth) return;
  draft.tokenUrlTemplate = oauth.tokenUrlTemplate;
  draft.clientAuthMethod = oauth.clientAuthMethod;
  draft.accessTokenPath = oauth.response.accessTokenPath;
  draft.tokenTypePath = oauth.response.tokenTypePath || "";
  draft.expiresInPath = oauth.response.expiresInPath || "";
  draft.renewalTokenPath = oauth.response.renewalTokenPath || "";
  draft.injectionHeaderName = oauth.injection.headerName;
  draft.injectionPrefix = oauth.injection.prefix || "";
  draft.refreshStrategy = oauth.refreshStrategy || "CLIENT_CREDENTIALS";
  const reserved = new Set([oauth.clientIdField, oauth.credentialField, oauth.scopeField].filter(Boolean));
  draft.extraFields = scheme.fields.filter((field) => !reserved.has(field.key)).map(fieldToDraft);
  draft.tokenParameters = (oauth.tokenParameters || []).map((parameter) => ({
    id: nextDraftRowId("parameter"),
    name: parameter.name,
    source: parameter.field ? "FIELD" : "VALUE",
    field: parameter.field || "",
    value: parameter.value || "",
    required: Boolean(parameter.required),
  }));
}

function fieldToDraft(field: ProviderAuthField): OAuthFieldDraft {
  return {
    id: nextDraftRowId("field"),
    key: field.key,
    label: field.label,
    kind: field.kind === "SELECT" ? "SELECT" : "TEXT",
    required: Boolean(field.required),
    placeholder: field.placeholder || "",
    help: field.help || "",
    optionsText: (field.options || []).map((option) => `${option.label}=${option.value}`).join("\n"),
  };
}

function providerFromDraft(draft: ProviderFormDraft, existing: CapabilityProvider | null): CapabilityProvider {
  const expectedStatuses = parseStatuses(draft.expectedStatuses);
  const documentUrl = draft.documentUrl.trim();
  const preservedEndpoint: Record<string, unknown> = { ...(existing?.endpointConfig || {}) };
  for (const key of ["schemaVersion", "serviceBaseUrl", "discovery", "verification", "sourceUri", "sourceRevision", "baseUrl", "url"]) {
    delete preservedEndpoint[key];
  }
  const existingEgress = asRecord(preservedEndpoint.egress);
  const nextEgress: Record<string, unknown> = { ...existingEgress };
  delete nextEgress.allowedCIDRs;
  delete nextEgress.AllowedCIDRs;
  const allowedCIDRs = parseCIDRList(draft.allowedCIDRs);
  if (allowedCIDRs.length) nextEgress.allowedCIDRs = allowedCIDRs;
  if (Object.keys(nextEgress).length) preservedEndpoint.egress = nextEgress;
  else delete preservedEndpoint.egress;
  const endpointConfig: Record<string, unknown> = {
    ...preservedEndpoint,
    schemaVersion: 2,
    serviceBaseUrl: draft.serviceBaseUrl.trim().replace(/\/+$/, ""),
    ...(documentUrl ? {
      discovery: {
        documentUrl,
        ...(draft.sourceRevision.trim() ? { sourceRevision: draft.sourceRevision.trim() } : {}),
      },
    } : {}),
    verification: {
      method: draft.verificationMethod,
      ...(draft.verificationPath.trim() ? { path: normalizeVerificationPath(draft.verificationPath) } : {}),
      expectedStatuses,
    },
  };
  const driverConfig: Record<string, unknown> = { ...(existing?.driverConfig || {}) };
  // Hard-cutover: write outbound-identity.v1 only; reject legacy authentication for HTTP.
  delete driverConfig.authentication;
  driverConfig.outboundIdentity = buildOutboundIdentityContract(draft);
  return {
    id: existing?.id || "",
    name: draft.name.trim(),
    kind: "HTTP_OPENAPI",
    driverKey: existing?.driverKey || "http_openapi",
    transport: "HTTP",
    endpointConfig,
    driverConfig,
    discoveryMode: draft.discoveryMode || "ON_DEMAND",
    status: existing?.status || "ACTIVE",
    lastSyncedAt: existing?.lastSyncedAt,
    lastErrorCode: existing?.lastErrorCode,
    createdBy: existing?.createdBy || "",
    updatedBy: existing?.updatedBy || "",
    lockVersion: existing?.lockVersion || 0,
  };
}

function buildAuthenticationContract(draft: ProviderFormDraft): ProviderAuthContract {
  if (draft.authMode === "NONE") {
    const contract = noAuthenticationContract();
    contract.schemes.push(...draft.preservedSchemes.filter((scheme) => scheme.key !== contract.defaultSchemeKey));
    return contract;
  }
  const fields: ProviderAuthField[] = [
    { key: "clientId", label: "Client ID", kind: "TEXT", required: true, placeholder: "客户端标识" },
    { key: "clientSecret", label: "Client Secret", kind: "SECRET", required: true, help: "明文只用于创建或替换 Secret。" },
    { key: "scope", label: "Scope", kind: "TEXT", placeholder: "例如：read write" },
    ...draft.extraFields.map((field) => ({
      key: field.key.trim(),
      label: field.label.trim(),
      kind: field.kind,
      required: field.required,
      ...(field.placeholder.trim() ? { placeholder: field.placeholder.trim() } : {}),
      ...(field.help.trim() ? { help: field.help.trim() } : {}),
      ...(field.kind === "SELECT" ? { options: parseOptions(field.optionsText) } : {}),
    } as ProviderAuthField)),
  ];
  return {
    version: "service-auth.v1",
    defaultSchemeKey: draft.schemeKey.trim(),
    schemes: [{
      key: draft.schemeKey.trim(),
      type: "OAUTH2_CLIENT",
      displayName: draft.displayName.trim(),
      ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
      fields,
      oauth2: {
        tokenUrlTemplate: draft.tokenUrlTemplate.trim(),
        clientIdField: "clientId",
        credentialField: "clientSecret",
        clientAuthMethod: draft.clientAuthMethod,
        scopeField: "scope",
        tokenParameters: draft.tokenParameters.map((parameter) => ({
          name: parameter.name.trim(),
          ...(parameter.source === "FIELD" ? { field: parameter.field.trim() } : { value: parameter.value.trim() }),
          ...(parameter.required ? { required: true } : {}),
        })),
        response: {
          accessTokenPath: draft.accessTokenPath.trim(),
          ...(draft.tokenTypePath.trim() ? { tokenTypePath: draft.tokenTypePath.trim() } : {}),
          ...(draft.expiresInPath.trim() ? { expiresInPath: draft.expiresInPath.trim() } : {}),
          ...(draft.renewalTokenPath.trim() ? { renewalTokenPath: draft.renewalTokenPath.trim() } : {}),
        },
        injection: {
          headerName: draft.injectionHeaderName.trim(),
          ...(draft.injectionPrefix.trim() ? { prefix: draft.injectionPrefix.trim() } : {}),
        },
        refreshStrategy: draft.refreshStrategy,
      },
    }, ...draft.preservedSchemes.filter((scheme) => scheme.key !== draft.schemeKey.trim())],
  };
}

function validateProviderDraft(draft: ProviderFormDraft) {
  if (!draft.name.trim()) return "请输入 Provider 名称。";
  if (!validServiceBaseURL(draft.serviceBaseUrl)) return "请输入不含 Query 或 Fragment 的 HTTP(S) 运行地址。";
  if (draft.documentUrl.trim() && !validHTTPURL(draft.documentUrl)) return "OpenAPI 文档地址不是有效的 HTTP(S) URL；如无在线文档可留空。";
  if (draft.discoveryMode !== "MANUAL" && !draft.documentUrl.trim()) return "按需发现需要 OpenAPI 文档地址；如无在线文档，请选择“不启用自动发现”。";
  if (draft.verificationPath.trim() && (/^https?:\/\//i.test(draft.verificationPath.trim()) || draft.verificationPath.trim().startsWith("//"))) {
    return "验证路径必须是 Provider 运行地址下的相对路径。";
  }
  try {
    parseStatuses(draft.expectedStatuses);
  } catch {
    return "期望状态码应为 100–599 的逗号分隔数字，且不能重复。";
  }
  let allowedCIDRs: string[];
  try {
    allowedCIDRs = parseCIDRList(draft.allowedCIDRs, true);
  } catch {
    return "允许的私网 CIDR 格式无效；IPv4 示例：192.168.10.0/24。";
  }
  const privateAddress = privateLiteralAddress(draft.serviceBaseUrl);
  const privateAddressAllowed = privateAddress.includes(":")
    ? allowedCIDRs.some((cidr) => cidr.includes(":"))
    : allowedCIDRs.some((cidr) => cidrContainsIPv4(cidr, privateAddress));
  if (privateAddress && !privateAddressAllowed) {
    const singleHostCIDR = privateAddress.includes(":") ? `${privateAddress}/128` : `${privateAddress}/32`;
    return `私网运行地址 ${privateAddress} 需要显式加入允许的私网 CIDR（可使用 ${singleHostCIDR}）。`;
  }
  if (!draft.supportBrokerObo && !draft.supportRequestPassthrough) {
    return "至少选择一种";
  }
  if (!validHeaderName(draft.businessInjectionHeader || "Authorization")) {
    return "业务 Token 注入 Header 名称无效。";
  }
  if (draft.supportBrokerObo) {
    if (!validHTTPURL(draft.brokerTokenEndpoint)) return "请输入有效的 Broker Token Endpoint。";
    if (!draft.brokerAudience.trim()) return "请输入 Broker Audience。";
  }
  return "";
}

function validHTTPURL(value: string) {
  try {
    const url = new URL(value.trim());
    return ["http:", "https:"].includes(url.protocol) && Boolean(url.host) && !url.username && !url.password && !url.hash;
  } catch {
    return false;
  }
}

function validServiceBaseURL(value: string) {
  if (!validHTTPURL(value)) return false;
  const url = new URL(value.trim());
  return !url.search;
}

function validTokenURLTemplate(value: string) {
  const trimmed = value.trim();
  if (!/^https?:\/\//i.test(trimmed) || /[\r\n]/.test(trimmed)) return false;
  const authority = trimmed.replace(/^https?:\/\//i, "").split(/[/?#]/, 1)[0];
  if (!authority || authority.includes("{{") || authority.includes("}}")) return false;
  const placeholders = [...trimmed.matchAll(/\{\{\s*([A-Za-z][A-Za-z0-9._-]{0,63})\s*\}\}/g)].map((match) => match[1]);
  const stripped = trimmed.replace(/\{\{\s*[A-Za-z][A-Za-z0-9._-]{0,63}\s*\}\}/g, "value");
  return !/[{}]/.test(stripped) && placeholders.every((key) => availableOAuthFieldKeys.value.includes(key));
}

function validContractKey(value: string) {
  return /^[A-Za-z][A-Za-z0-9._-]{0,63}$/.test(value.trim());
}

function validJSONPath(value: string) {
  const parts = value.trim().split(".");
  return parts.length > 0 && parts.length <= 16 && parts.every(validContractKey);
}

function validHeaderName(value: string) {
  return /^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}$/.test(value.trim());
}

function parseStatuses(value: string) {
  const statuses = value.split(/[\s,]+/).filter(Boolean).map(Number);
  if (!statuses.length || statuses.some((status) => !Number.isInteger(status) || status < 100 || status > 599) || new Set(statuses).size !== statuses.length) {
    throw new Error("invalid statuses");
  }
  return statuses;
}

function parseCIDRList(value: string, validate = false) {
  const cidrs = [...new Set(value.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean))];
  if (validate && cidrs.some((cidr) => !validCIDR(cidr))) throw new Error("invalid CIDR");
  return cidrs;
}

function validCIDR(value: string) {
  const [address, prefixText, extra] = value.split("/");
  if (extra !== undefined || prefixText === undefined || !/^\d+$/.test(prefixText)) return false;
  const prefix = Number(prefixText);
  if (address.includes(":")) return /^[0-9A-Fa-f:.]+$/.test(address) && prefix >= 0 && prefix <= 128;
  return ipv4Number(address) !== null && prefix >= 0 && prefix <= 32;
}

function privateLiteralAddress(value: string) {
  try {
    const hostname = new URL(value.trim()).hostname.replace(/^\[|\]$/g, "");
    const number = ipv4Number(hostname);
    if (number === null) {
      const normalized = hostname.toLowerCase();
      return normalized === "::1" || /^(fc|fd)/.test(normalized) || /^fe[89ab]/.test(normalized) ? hostname : "";
    }
    const first = number >>> 24;
    const second = (number >>> 16) & 255;
    return first === 10 || first === 127 || (first === 169 && second === 254) ||
      (first === 172 && second >= 16 && second <= 31) || (first === 192 && second === 168)
      ? hostname : "";
  } catch {
    return "";
  }
}

function cidrContainsIPv4(cidr: string, address: string) {
  const [networkText, prefixText] = cidr.split("/");
  const network = ipv4Number(networkText);
  const target = ipv4Number(address);
  const prefix = Number(prefixText);
  if (network === null || target === null || !Number.isInteger(prefix) || prefix < 0 || prefix > 32) return false;
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
  return (network & mask) === (target & mask);
}

function ipv4Number(value: string) {
  const parts = value.split(".");
  if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part) || Number(part) > 255)) return null;
  return parts.reduce((result, part) => ((result << 8) | Number(part)) >>> 0, 0);
}

function parseOptions(value: string) {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).map((line) => {
    const separator = line.indexOf("=");
    if (separator < 0) return { label: line, value: line };
    return { label: line.slice(0, separator).trim(), value: line.slice(separator + 1).trim() };
  }).filter((option) => option.label && option.value);
}

function normalizeVerificationPath(value: string) {
  const trimmed = value.trim();
  return trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
}

function normalizeSyncOutcome(value: ProviderSyncOutcome): ProviderSyncOutcome {
  return {
    id: value?.id,
    status: String(value?.status || "FAILED"),
    discoveredCount: Number(value?.discoveredCount) || 0,
    changedCount: Number(value?.changedCount) || 0,
    errorSummary: asRecord(value?.errorSummary),
  };
}

function syncErrorText(result: ProviderSyncOutcome) {
  const summary = Object.entries(result.errorSummary).map(([key, value]) => `${key}: ${String(value)}`).join("；");
  return summary || result.status;
}

function showActionNote(message: string, tone: "success" | "warning" | "error" = "success") {
  actionNote.value = message;
  actionTone.value = tone;
}

function nextDraftRowId(prefix: string) {
  draftRowSequence += 1;
  return `${prefix}-${draftRowSequence}`;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function firstString(...values: unknown[]) {
  return values.find((value): value is string => typeof value === "string" && Boolean(value.trim())) || "";
}

function errorMessage(error: unknown, fallback: string) {
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}
</script>

<template>
  <div class="providers-page management-page-grid management-page-grid--two-rows">
    <header class="providers-page-header">
      <div>
        <span class="providers-eyebrow">Integration Registry</span>
        <h1>服务 Provider</h1>
        <p>统一维护服务运行端点、OpenAPI 发现来源、验证策略与版本化认证契约，再由 Connection 绑定具体环境和凭据。</p>
      </div>
      <div class="providers-header-actions">
        <RouterLink class="ghost-button" to="/connections">
          <i class="fa-solid fa-plug-circle-bolt" aria-hidden="true" />服务连接
        </RouterLink>
        <button data-testid="provider-create" class="primary-button" type="button" :disabled="!hasWorkspaceContext" @click="openCreateEditor">
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />新建 Provider
        </button>
      </div>
    </header>

    <section class="providers-list-card management-list-card">
      <WorkspaceContextState
        v-if="!hasWorkspaceContext"
        feature="服务 Provider"
        icon="fa-solid fa-cloud-arrow-down"
        @retry="loadProviders"
      />
      <ManagementList
        v-else
        class="providers-management-list"
        :rows="providerRows"
        :columns="providerColumns"
        row-key="id"
        :sticky-left-keys="['name']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:providers:columns"
        :selectable="false"
        :expanded-row-key="expandedProviderId"
        :loading="loading"
        :error="loadError"
        :has-loaded="hasLoaded"
        :search="search"
        :pagination="providerPagination"
        :sort-by="sortBy"
        :sort-order="sortOrder"
        search-placeholder="搜索 Provider / 地址 / 认证方式"
        search-aria-label="搜索服务 Provider"
        clear-search-aria-label="清除 Provider 搜索"
        :reset-disabled="!search && statusFilter === 'ALL'"
        @update:search="updateSearch"
        @reset="resetFilters"
        @page-change="changePage"
        @sort-change="changeSort"
      >
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="statusFilter"
            :options="statusOptions"
            ariaLabel="Provider 状态筛选"
            @update:model-value="updateStatusFilter"
          />
        </template>

        <template #cell-name="{ row: provider }">
          <div class="provider-name-cell">
            <span class="provider-icon"><i class="fa-solid fa-cloud" aria-hidden="true" /></span>
            <span>
              <strong class="aw-table-title">{{ provider.name }}</strong>
              <small class="aw-table-subtitle">{{ provider.kind }}</small>
            </span>
          </div>
        </template>
        <template #cell-status="{ row: provider }">
          <span class="provider-status-pill aw-table-pill" :class="statusTone(provider.status)"><i aria-hidden="true" />{{ statusLabel(provider.status) }}</span>
        </template>
        <template #cell-serviceBaseUrl="{ row: provider }"><code class="provider-address aw-table-mono">{{ providerServiceBaseUrl(provider) }}</code></template>
        <template #cell-documentUrl="{ row: provider }"><code class="provider-address aw-table-mono">{{ providerDocumentUrl(provider) }}</code></template>
        <template #cell-authentication="{ row: provider }"><span class="provider-auth-summary aw-table-meta">{{ authSchemeSummary(provider) }}</span></template>
        <template #cell-lastSyncedAt="{ row: provider }"><span class="provider-muted aw-table-meta">{{ formatDate(provider.lastSyncedAt) }}</span></template>
        <template #cell-actions="{ row: provider }">
          <ManagementRowActions
            :menu-actions="providerMenuActions(provider)"
            :menu-label="`${provider.name} 更多操作`"
            @action="handleProviderRowAction($event, provider)"
          />
        </template>

        <template #row-detail="{ row: provider }">
          <section class="provider-detail-row" :aria-label="`${provider.name} Provider 详情`">
            <div class="provider-contract-summary">
              <div><span>运行端点</span><code>{{ providerServiceBaseUrl(provider) }}</code></div>
              <div><span>OpenAPI 自动发现</span><code>{{ providerDocumentUrl(provider) }}</code></div>
              <div><span>连接验证</span><strong>{{ providerVerificationSummary(provider) }}</strong></div>
              <div><span>认证契约</span><strong>{{ authSchemeSummary(provider) }}</strong></div>
            </div>
            <div v-if="syncResults[provider.id]" :data-testid="`provider-sync-result-${provider.id}`" class="provider-sync-result" :class="syncResults[provider.id].status === 'SUCCEEDED' ? 'succeeded' : 'failed'" role="status">
              <i :class="syncResults[provider.id].status === 'SUCCEEDED' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
              <span>
                <strong>{{ syncResults[provider.id].status === "SUCCEEDED" ? "最近一次同步成功" : "最近一次同步失败" }}</strong>
                <small v-if="syncResults[provider.id].status === 'SUCCEEDED'">发现 {{ syncResults[provider.id].discoveredCount }}，变化 {{ syncResults[provider.id].changedCount }}</small>
                <small v-else>{{ syncErrorText(syncResults[provider.id]) }}</small>
              </span>
            </div>
            <div class="provider-assets-heading">
              <div><h3>能力资产</h3><p>{{ hasProviderDocument(provider) ? "同步 OpenAPI 后，可将端点物化为 Tool Draft。" : "未配置在线文档，不启用自动发现；已有资产仍可查看和物化。" }}</p></div>
              <button type="button" :disabled="isLoadingAssets(provider.id)" @click="loadAssets(provider.id)"><i :class="isLoadingAssets(provider.id) ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-rotate'" />刷新资产</button>
            </div>
            <p v-if="assetErrors[provider.id]" class="provider-inline-error" role="alert">{{ assetErrors[provider.id] }}</p>
            <div v-if="isLoadingAssets(provider.id) && !(integration.providerAssetsByProvider[provider.id] || []).length" class="provider-assets-state" role="status">正在加载能力资产…</div>
            <div v-else-if="!(integration.providerAssetsByProvider[provider.id] || []).length" class="provider-assets-state">{{ hasProviderDocument(provider) ? "尚无资产，请先同步 Provider。" : "尚无资产。可继续用此 Provider 创建 Connection；能力可在获得文档后再同步。" }}</div>
            <div v-else class="provider-assets-list">
              <article v-for="asset in integration.providerAssetsByProvider[provider.id]" :key="asset.id">
                <span class="asset-method">{{ String(asset.metadata?.method || asset.kind || "API").toUpperCase() }}</span>
                <div><strong>{{ asset.name }}</strong><small>{{ asset.description || asset.externalId }}</small></div>
                <span class="asset-status">{{ asset.materializedCapabilityId ? "已物化" : asset.status }}</span>
                <button :data-testid="`provider-materialize-${asset.id}`" type="button" :disabled="Boolean(asset.materializedCapabilityId) || Boolean(materializingAssetId)" @click="materializeAsset(provider, asset)">
                  <i :class="materializingAssetId === asset.id ? 'fa-solid fa-spinner fa-spin' : asset.materializedCapabilityId ? 'fa-solid fa-circle-check' : 'fa-solid fa-wand-magic-sparkles'" />
                  {{ asset.materializedCapabilityId ? "已生成 Tool" : "物化为 Tool" }}
                </button>
              </article>
            </div>
          </section>
        </template>

        <template #card="{ row: provider }">
          <article class="provider-mobile-card">
            <header><div class="provider-name-cell"><span class="provider-icon"><i class="fa-solid fa-cloud" /></span><span><strong>{{ provider.name }}</strong><small>{{ provider.kind }}</small></span></div><span class="provider-status-pill" :class="statusTone(provider.status)"><i />{{ statusLabel(provider.status) }}</span></header>
            <dl><div><dt>运行地址</dt><dd><code>{{ providerServiceBaseUrl(provider) }}</code></dd></div><div><dt>发现地址</dt><dd><code>{{ providerDocumentUrl(provider) }}</code></dd></div><div><dt>认证</dt><dd>{{ authSchemeSummary(provider) }}</dd></div><div><dt>最近同步</dt><dd>{{ formatDate(provider.lastSyncedAt) }}</dd></div></dl>
            <div class="provider-actions mobile"><button type="button" @click="openEditEditor(provider)"><i class="fa-solid fa-pen" />编辑</button><button type="button" :title="providerSyncTitle(provider)" :disabled="isSyncing(provider.id) || !canSyncProvider(provider)" @click="syncProvider(provider)"><i class="fa-solid fa-rotate" />同步</button><button type="button" @click="toggleAssets(provider)"><i class="fa-solid fa-cubes" />资产</button><button class="danger" type="button" @click="requestDeleteProvider(provider)"><i class="fa-solid fa-trash-can" />删除</button></div>
          </article>
        </template>

        <template #empty>
          <div class="providers-empty-state">
            <span><i class="fa-solid fa-cloud-arrow-down" /></span>
            <h2>{{ search || statusFilter !== "ALL" ? "没有符合条件的 Provider" : "还没有服务 Provider" }}</h2>
            <p>{{ search || statusFilter !== "ALL" ? "调整搜索或状态筛选后重试。" : "先登记运行端点和认证契约；OpenAPI 文档可稍后补充，再创建服务连接。" }}</p>
            <button v-if="!search && statusFilter === 'ALL'" type="button" @click="openCreateEditor">新建 Provider</button>
          </div>
        </template>
        <template #error="{ error }"><div class="providers-error-state" role="alert"><strong>Provider 加载失败</strong><p>{{ error }}</p><button type="button" @click="loadProviders">重新加载</button></div></template>
      </ManagementList>
    </section>

    <div v-if="editorVisible" class="provider-modal-backdrop" @click.self="closeEditor">
      <section ref="editorPanel" class="provider-editor" role="dialog" aria-modal="true" aria-labelledby="provider-editor-title" @keydown="trapEditorFocus">
        <header>
          <div><span class="provider-editor-icon"><i class="fa-solid fa-cloud" /></span><span><small>Provider Registry</small><h2 id="provider-editor-title">{{ editorMode === "create" ? "新建服务 Provider" : "编辑服务 Provider" }}</h2></span></div>
          <button type="button" aria-label="关闭 Provider 编辑器" :disabled="saving" @click="closeEditor"><i class="fa-solid fa-xmark" /></button>
        </header>
        <form @submit.prevent="saveProvider">
          <div class="provider-editor-body">
            <p v-if="formError" class="provider-form-error" role="alert"><i class="fa-solid fa-circle-exclamation" />{{ formError }}</p>
            <section class="provider-form-section">
              <div class="provider-section-heading"><span>1</span><div><h3>服务与发现</h3><p>运行地址用于 Tool 调用；OpenAPI 文档仅用于自动发现和同步，不影响 Connection 或运行调用。</p></div></div>
              <div class="provider-form-grid two">
                <label><span>Provider 名称 <b>*</b></span><input ref="providerNameInput" v-model="providerDraft.name" data-testid="provider-name" required placeholder="例如：订单服务" /></label>
                <label><span>发现策略</span><AppSelect v-model="providerDraft.discoveryMode" class="provider-form-select" data-testid="provider-discovery-mode" :options="discoveryModeOptions" aria-label="发现策略" /></label>
              </div>
              <label><span>服务运行地址 <b>*</b></span><input v-model="providerDraft.serviceBaseUrl" data-testid="provider-service-base-url" class="mono" required placeholder="https://api.example.com/v1" /><small>正式调用使用的 Base URL，不要填写 OpenAPI 文档路径。</small></label>
              <label><span>OpenAPI 文档地址 <em>可选</em></span><input v-model="providerDraft.documentUrl" data-testid="provider-document-url" class="mono" inputmode="url" placeholder="https://api.example.com/openapi.json" /><small>仅用于自动发现和同步能力。第三方未提供在线文档时可留空，不影响保存 Provider、创建 Connection 或正式调用。</small></label>
              <label><span>允许的私网 CIDR <em>按需</em></span><textarea v-model="providerDraft.allowedCIDRs" data-testid="provider-allowed-cidrs" class="mono" rows="2" placeholder="192.168.10.0/24" /><small>仅内网或回环地址需要显式授权，每行一个 CIDR。公网地址请留空；单个 IPv4 地址可使用 /32。</small></label>
            </section>

            <section class="provider-form-section">
              <div class="provider-section-heading"><span>2</span><div><h3>连接验证</h3><p>Connection 验证会基于运行地址和以下规则发起安全请求。</p></div></div>
              <div class="provider-form-grid verification">
                <label><span>方法</span><AppSelect v-model="providerDraft.verificationMethod" class="provider-form-select" data-testid="provider-verification-method" :options="verificationMethodOptions" aria-label="连接验证方法" /></label>
                <label><span>相对路径</span><input v-model="providerDraft.verificationPath" data-testid="provider-verification-path" class="mono" placeholder="/health" /></label>
                <label><span>期望状态码</span><input v-model="providerDraft.expectedStatuses" data-testid="provider-expected-statuses" class="mono" placeholder="200, 204" /></label>
              </div>
            </section>

            <section
              class="provider-form-section authentication provider-identity-section"
              data-testid="provider-outbound-identity"
              :class="{ 'has-identity-error': Boolean(identityModeError) }"
              :aria-invalid="identityModeError ? 'true' : undefined"
            >
              <div class="provider-section-heading">
                <span>3</span>
                <div>
                  <h3>用户调用身份</h3>
                  <p>
                    选择这个 Provider 支持的身份方式（可多选）。创建 Connection 时，必须从已支持的方式中选择且只能选择一种；不支持共享账号或免鉴权。
                  </p>
                </div>
              </div>
              <p v-if="providerDraft.legacyAuthentication" class="provider-migration-note">
                <i class="fa-solid fa-triangle-exclamation" />
                检测到旧版 service-auth 契约。保存时将硬切为 <code>outbound-identity.v1</code>；请勾选至少一种模式并补全字段。
              </p>
              <p
                v-if="identityModeError"
                class="provider-identity-error"
                data-testid="provider-identity-mode-error"
                role="alert"
              >
                {{ identityModeError }}
              </p>
              <div
                ref="identityModeGroupRef"
                class="provider-auth-choice provider-identity-choice"
                role="group"
                aria-label="支持的身份方式（可多选）"
                :aria-describedby="identityModeError ? 'provider-identity-mode-error-text' : undefined"
              >
                <label
                  class="provider-identity-card"
                  :class="{ selected: providerDraft.supportBrokerObo }"
                >
                  <input
                    v-model="providerDraft.supportBrokerObo"
                    data-testid="provider-mode-broker"
                    type="checkbox"
                    :disabled="saving"
                    @change="clearIdentityModeErrorIfResolved"
                  />
                  <span class="provider-identity-card-body">
                    <b v-if="providerDraft.supportBrokerObo" class="provider-identity-badge" aria-hidden="true">
                      <i class="fa-solid fa-check" />已支持
                    </b>
                    <i class="fa-solid fa-key provider-identity-icon" aria-hidden="true" />
                    <strong>Broker / OBO</strong>
                    <small>平台按当前用户身份换取短期业务 Token</small>
                  </span>
                </label>
                <label
                  class="provider-identity-card"
                  :class="{ selected: providerDraft.supportRequestPassthrough }"
                >
                  <input
                    v-model="providerDraft.supportRequestPassthrough"
                    data-testid="provider-mode-passthrough"
                    type="checkbox"
                    :disabled="saving"
                    @change="clearIdentityModeErrorIfResolved"
                  />
                  <span class="provider-identity-card-body">
                    <b v-if="providerDraft.supportRequestPassthrough" class="provider-identity-badge" aria-hidden="true">
                      <i class="fa-solid fa-check" />已支持
                    </b>
                    <i class="fa-solid fa-right-left provider-identity-icon" aria-hidden="true" />
                    <strong>本次请求透传</strong>
                    <small>调用方为本次请求提供 Token，平台只用于本次调用且不会保存</small>
                  </span>
                </label>
              </div>
              <p
                v-if="identityModeError"
                id="provider-identity-mode-error-text"
                class="sr-only"
              >{{ identityModeError }}</p>
              <div class="provider-form-grid two">
                <label><span>业务 Token 注入 Header <b>*</b></span><input v-model="providerDraft.businessInjectionHeader" data-testid="provider-injection-header" class="mono" placeholder="Authorization" /></label>
                <label><span>前缀</span><input v-model="providerDraft.businessInjectionPrefix" data-testid="provider-injection-prefix" class="mono" placeholder="Bearer" /></label>
              </div>
              <div v-if="providerDraft.supportBrokerObo" class="provider-oauth-editor" data-testid="provider-broker-fields">
                <div class="provider-form-grid two">
                  <label><span>Broker Token Endpoint <b>*</b></span><input v-model="providerDraft.brokerTokenEndpoint" data-testid="provider-broker-token-endpoint" class="mono" placeholder="https://broker.example.com/oauth/token" /></label>
                  <label><span>Audience <b>*</b></span><input v-model="providerDraft.brokerAudience" data-testid="provider-broker-audience" class="mono" placeholder="api://resource" /></label>
                </div>
                <label><span>Allowed Scopes</span><input v-model="providerDraft.brokerAllowedScopes" data-testid="provider-broker-scopes" class="mono" placeholder="orders.read inventory.read" /><small>空格或逗号分隔；Connection 选择的 scope 必须在此 allowlist 内。</small></label>
                <p class="provider-field-help">平台使用 private_key_jwt 向 Broker 证明自身身份；当前仅支持用户主体（USER）。</p>
              </div>
              <p v-if="providerDraft.supportRequestPassthrough" class="provider-field-help" data-testid="provider-passthrough-summary">
                仅接收 Access Token。调用方每次提供 Token 及有效期；平台不写入会话、历史或本地存储。
              </p>
              <details class="provider-identity-tech" data-testid="provider-identity-tech-details">
                <summary>查看技术约束</summary>
                <ul>
                  <li>契约：<code>outbound-identity.v1</code>；模式仅 <code>BROKER_OBO</code> / <code>REQUEST_PASSTHROUGH</code>。</li>
                  <li>主体类型：仅 <code>USER</code>。</li>
                  <li>Broker 机器认证：<code>private_key_jwt</code>。</li>
                  <li>透传凭据类型：<code>ACCESS_TOKEN</code>；调用方提供 <code>expiresAt</code>。</li>
                  <li>用户业务 Token 不进入 Provider / Connection 配置、日志、本地存储、会话历史或 Revision。</li>
                </ul>
              </details>
            </section>
          </div>
          <footer><button class="ghost-button" type="button" :disabled="saving" @click="closeEditor">取消</button><button data-testid="provider-save" class="primary-button" type="submit" :disabled="saving"><i :class="saving ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-floppy-disk'" />{{ saving ? "保存中" : "保存 Provider" }}</button></footer>
        </form>
      </section>
    </div>

    <div v-if="pendingDeleteProvider" class="provider-modal-backdrop" @click.self="closeDeleteDialog">
      <section class="provider-delete-dialog" role="dialog" aria-modal="true" aria-labelledby="provider-delete-title">
        <span class="provider-delete-icon"><i class="fa-solid fa-trash-can" /></span>
        <h2 id="provider-delete-title">删除 Provider</h2>
        <p>删除 <strong>{{ pendingDeleteProvider.name }}</strong> 后将无法继续同步；已有 Connection 和 Tool 也可能失效。</p>
        <label><span>输入 Provider 名称确认</span><input v-model="deleteConfirmText" data-testid="provider-delete-confirm-input" autocomplete="off" /></label>
        <p v-if="deleteError" class="provider-form-error" role="alert">{{ deleteError }}</p>
        <footer><button type="button" :disabled="deleting" @click="closeDeleteDialog">取消</button><button data-testid="provider-delete-confirm" class="danger" type="button" :disabled="deleting || !deleteConfirmMatches" @click="confirmDeleteProvider"><i :class="deleting ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-trash-can'" />确认删除</button></footer>
      </section>
    </div>

    <div v-if="actionNote" class="provider-action-toast" :class="actionTone" role="status" aria-live="polite"><i :class="actionTone === 'error' ? 'fa-solid fa-circle-exclamation' : actionTone === 'warning' ? 'fa-solid fa-triangle-exclamation' : 'fa-solid fa-circle-check'" />{{ actionNote }}<button type="button" aria-label="关闭提示" @click="actionNote = ''"><i class="fa-solid fa-xmark" /></button></div>
  </div>
</template>

<style scoped>
.providers-page { min-width: 0; min-height: 0; }
.providers-page-header { min-width: 0; display: flex; align-items: flex-end; justify-content: space-between; gap: 24px; }
.providers-page-header h1 { margin: 4px 0 0; color: #0f172a; font-size: 23px; line-height: 1.18; font-weight: 760; letter-spacing: -0.028em; }
.providers-page-header p { max-width: 760px; margin: 10px 0 0; color: #64748b; font-size: 12px; line-height: 1.55; }
.providers-eyebrow { color: var(--aw-subtle, #96a19d); font-size: 9px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
.providers-header-actions, .provider-actions { display: flex; align-items: center; gap: 8px; }
.providers-header-actions .ghost-button { text-decoration: none; }
/* Transparent shell — ManagementList owns table/toolbar/footer chrome. */
.providers-list-card.management-list-card {
  min-width: 0;
  min-height: 0;
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}
.providers-management-list { height: 100%; }
.providers-management-list :deep(.data-table tbody tr:hover .provider-name-cell strong) { color: #4f46e5; }
.provider-name-cell { display: flex; min-width: 0; max-width: 100%; align-items: center; gap: 12px; overflow: hidden; }
.provider-name-cell > span:last-child { display: grid; min-width: 0; max-width: 100%; flex: 1 1 auto; gap: 2px; overflow: hidden; }
.provider-name-cell strong, .provider-name-cell small { min-width: 0; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.provider-name-cell strong, .provider-name-cell .aw-table-title { color: var(--aw-table-title-color, #111827); font-size: var(--aw-table-title-size, 0.9rem); font-weight: var(--aw-table-title-weight, 600); line-height: 1.35; transition: color .15s ease; }
.provider-name-cell small, .provider-name-cell .aw-table-subtitle { color: var(--aw-table-subtitle-color, #6b7280); font-size: var(--aw-table-subtitle-size, 0.8125rem); font-weight: var(--aw-table-subtitle-weight, 400); line-height: 1.35; }
.provider-icon { width: 32px; height: 32px; display: grid; flex: 0 0 32px; place-items: center; border: 1px solid #d1fae5; border-radius: 8px; background: #ecfdf5; color: #059669; font-size: var(--aw-table-pill-size, 0.75rem); }
.provider-status-pill { display: inline-flex; align-items: center; gap: 6px; padding: 5px 9px; border-radius: 999px; font-size: var(--aw-table-pill-size, 0.75rem); font-weight: var(--aw-table-pill-weight, 600); white-space: nowrap; }
.provider-status-pill i { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.provider-status-pill.active { background: #eaf8f4; color: #07806e; }
.provider-status-pill.error { background: #fff1f2; color: #dc2626; }
.provider-status-pill.disabled, .provider-status-pill.neutral { background: #f1f5f9; color: #64748b; }
.provider-address { display: block; overflow: hidden; color: var(--aw-table-body-color, #374151); font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, monospace); font-size: var(--aw-table-mono-size, 0.82rem); text-overflow: ellipsis; white-space: nowrap; }
.provider-auth-summary, .provider-muted { color: var(--aw-table-meta-color, #6b7280); font-size: var(--aw-table-meta-size, 0.8125rem); font-weight: var(--aw-table-meta-weight, 400); }
.provider-actions { justify-content: flex-end; }
.provider-actions button { min-width: 36px; height: 34px; display: inline-flex; align-items: center; justify-content: center; gap: 6px; padding: 0 10px; border: 1px solid #e2e8f0; border-radius: 8px; background: #fff; color: #64748b; font: inherit; cursor: pointer; }
.provider-actions button:hover { border-color: #99d8ca; color: #0d9488; }
.provider-actions button.danger:hover, .provider-actions button.danger { color: #dc2626; }
.provider-actions button:disabled { cursor: not-allowed; opacity: .45; }
.provider-detail-row { display: grid; gap: 14px; padding: 18px 20px 20px; background: #f8fafc; }
.provider-contract-summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; }
.provider-contract-summary > div { min-width: 0; display: grid; gap: 5px; padding: 12px; border: 1px solid #e2e8f0; border-radius: 10px; background: #fff; }
.provider-contract-summary span { color: #94a3b8; font-size: 10px; font-weight: 700; text-transform: uppercase; }
.provider-contract-summary code, .provider-contract-summary strong { overflow: hidden; color: #334155; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.provider-sync-result { display: flex; align-items: center; gap: 10px; padding: 11px 13px; border-radius: 9px; }
.provider-sync-result > span { display: grid; gap: 2px; }
.provider-sync-result small { font-size: 11px; }
.provider-sync-result.succeeded { background: #eaf8f4; color: #07806e; }
.provider-sync-result.failed { background: #fff1f2; color: #b91c1c; }
.provider-assets-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.provider-assets-heading h3 { margin: 0; color: #0f172a; font-size: 14px; }
.provider-assets-heading p { margin: 3px 0 0; color: #64748b; font-size: 11px; }
.provider-assets-heading button, .provider-assets-list button { min-height: 34px; display: inline-flex; align-items: center; gap: 6px; padding: 0 11px; border: 1px solid #dbe3ef; border-radius: 8px; background: #fff; color: #475569; font: inherit; font-size: 11px; font-weight: 700; cursor: pointer; }
.provider-assets-list { display: grid; gap: 8px; }
.provider-assets-list article { min-width: 0; display: grid; grid-template-columns: auto minmax(0, 1fr) auto auto; align-items: center; gap: 12px; padding: 11px 12px; border: 1px solid #e8edf4; border-radius: 10px; background: #fff; }
.provider-assets-list article > div { min-width: 0; display: grid; gap: 3px; }
.provider-assets-list strong, .provider-assets-list small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.provider-assets-list strong { color: #1e293b; font-size: 12px; }
.provider-assets-list small { color: #94a3b8; font-size: 10px; }
.asset-method { padding: 4px 7px; border-radius: 6px; background: #eef6ff; color: #2563eb; font: 700 10px ui-monospace, monospace; }
.asset-status { color: #64748b; font-size: 10px; }
.provider-assets-state { padding: 22px; border: 1px dashed #cbd5e1; border-radius: 10px; color: #94a3b8; text-align: center; font-size: 12px; }
.provider-inline-error { margin: 0; color: #b91c1c; font-size: 12px; }
.provider-mobile-card { display: grid; gap: 14px; padding: 16px; border: 1px solid #e2e8f0; border-radius: 12px; background: #fff; }
.provider-mobile-card > header { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.provider-mobile-card dl { display: grid; gap: 8px; margin: 0; }
.provider-mobile-card dl > div { display: grid; grid-template-columns: 84px minmax(0, 1fr); gap: 10px; }
.provider-mobile-card dt { color: #94a3b8; font-size: 10px; }
.provider-mobile-card dd { min-width: 0; margin: 0; overflow: hidden; color: #475569; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.provider-actions.mobile { display: grid; grid-template-columns: repeat(4, 1fr); }
.providers-empty-state, .providers-error-state { min-height: 330px; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 36px; text-align: center; }
.providers-empty-state > span { width: 58px; height: 58px; display: grid; place-items: center; border-radius: 18px; background: #eff8f6; color: #0d9488; font-size: 24px; }
.providers-empty-state h2, .providers-error-state strong { margin: 16px 0 0; color: #0f172a; font-size: 18px; }
.providers-empty-state p, .providers-error-state p { max-width: 520px; margin: 8px 0 0; color: #64748b; font-size: 12px; line-height: 1.7; }
.providers-empty-state button, .providers-error-state button {
  min-height: 37px;
  margin-top: 18px;
  padding: 0 13px;
  border: 1px solid var(--aw-green, #0f9f6e);
  border-radius: 10px;
  background: var(--aw-green, #0f9f6e);
  color: #fff;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 6px 14px rgba(15, 159, 110, 0.14);
}
.provider-modal-backdrop { position: fixed; z-index: 3000; inset: 0; display: grid; place-items: center; padding: 24px; background: rgba(15, 23, 42, .55); backdrop-filter: blur(3px); }
.provider-editor { width: min(960px, calc(100vw - 32px)); max-height: calc(100vh - 32px); display: flex; overflow: hidden; flex-direction: column; border: 1px solid rgba(255,255,255,.5); border-radius: 16px; background: #fff; box-shadow: 0 30px 80px rgba(15, 23, 42, .28); }
.provider-editor > header { display: flex; flex: 0 0 auto; align-items: center; justify-content: space-between; gap: 16px; padding: 18px 22px; border-bottom: 1px solid #e9eef5; }
.provider-editor > header > div { display: flex; align-items: center; gap: 12px; }
.provider-editor > header > div > span:last-child { display: grid; gap: 2px; }
.provider-editor > header small { color: #0d9488; font-size: 9px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
.provider-editor h2 { margin: 0; color: #0f172a; font-size: 19px; }
.provider-editor > header > button { width: 38px; height: 38px; border: 1px solid #e2e8f0; border-radius: 9px; background: #fff; color: #64748b; cursor: pointer; }
.provider-editor-icon { width: 40px; height: 40px; display: grid; place-items: center; border-radius: 11px; background: #eaf8f4; color: #0d9488; }
.provider-editor form { min-height: 0; display: flex; flex: 1 1 auto; flex-direction: column; }
.provider-editor-body { min-height: 0; display: grid; gap: 14px; overflow: auto; padding: 18px 22px 24px; background: #f8fafc; }
.provider-editor form > footer { display: flex; flex: 0 0 auto; justify-content: flex-end; gap: 10px; padding: 14px 22px; border-top: 1px solid #e9eef5; background: #fff; }
.provider-form-section { display: grid; gap: 14px; padding: 18px; border: 1px solid #e5ebf3; border-radius: 13px; background: #fff; }
.provider-section-heading { display: flex; align-items: flex-start; gap: 10px; }
.provider-section-heading > span { width: 25px; height: 25px; display: grid; flex: 0 0 25px; place-items: center; border-radius: 8px; background: #eaf8f4; color: #07806e; font-size: 11px; font-weight: 800; }
.provider-section-heading h3 { margin: 0; color: #0f172a; font-size: 14px; }
.provider-section-heading p { margin: 3px 0 0; color: #64748b; font-size: 11px; line-height: 1.5; }
.provider-form-section label, .provider-delete-dialog label { min-width: 0; display: grid; gap: 6px; color: #334155; font-size: 11px; font-weight: 700; }
.provider-form-section label > span b { color: #dc2626; }
.provider-form-section input, .provider-form-section textarea, .provider-delete-dialog input { width: 100%; box-sizing: border-box; border: 1px solid #dbe3ef; border-radius: 8px; outline: none; background-color: #f8fafc; color: #1e293b; font-family: inherit; font-size: 12px; font-weight: 500; line-height: 1.4; }
.provider-form-section input:not([type="radio"]):not([type="checkbox"]), .provider-delete-dialog input { height: 44px; min-height: 44px; margin: 0; padding: 0 12px; }
.provider-form-select :deep(.el-select__wrapper) { min-height: 44px; border-radius: 8px; }
.provider-form-section textarea { min-height: 76px; padding: 10px 12px; resize: vertical; }
.provider-form-section input:focus, .provider-form-section textarea:focus, .provider-delete-dialog input:focus { border-color: rgba(13, 148, 136, .6); background: #fff; box-shadow: 0 0 0 2px rgba(13, 148, 136, .14); }
.provider-form-section label small { color: #94a3b8; font-size: 10px; font-weight: 500; line-height: 1.5; }
.provider-form-section label > span em { margin-left: 5px; color: #0d9488; font-size: 9px; font-style: normal; font-weight: 750; }
.provider-form-grid { display: grid; gap: 12px; }
.provider-form-grid > label { align-self: start; }
.provider-form-grid.two { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.provider-form-grid.three, .provider-form-grid.verification { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace !important; }
.provider-form-error { display: flex; align-items: flex-start; gap: 8px; margin: 0; padding: 11px 13px; border: 1px solid #fecdd3; border-radius: 9px; background: #fff1f2; color: #b91c1c; font-size: 12px; line-height: 1.55; }
.provider-migration-note { display: flex; gap: 8px; margin: 0; padding: 10px 12px; border: 1px solid #fde68a; border-radius: 8px; background: #fffbeb; color: #92400e; font-size: 11px; line-height: 1.55; }
.provider-auth-choice,
.provider-identity-choice {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.provider-identity-card {
  position: relative;
  display: block;
  cursor: pointer;
}
/* Visually hidden but focusable native checkbox — never pointer-events:none. */
.provider-identity-card > input[type="checkbox"] {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
.provider-identity-card-body {
  position: relative;
  min-height: 92px;
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr);
  grid-template-areas:
    "icon title"
    "icon desc";
  column-gap: 10px;
  row-gap: 3px;
  align-content: center;
  padding: 14px 72px 14px 13px;
  border: 1px solid #dbe3ef;
  border-radius: 10px;
  background: #f8fafc;
  transition: border-color 0.16s ease, background-color 0.16s ease, box-shadow 0.16s ease;
}
.provider-identity-icon {
  grid-area: icon;
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: 9px;
  background: #fff;
  color: #64748b;
  align-self: start;
}
.provider-identity-card-body > strong {
  grid-area: title;
  color: #0f172a;
  font-size: 13px;
  font-weight: 800;
}
.provider-identity-card-body > small {
  grid-area: desc;
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.45;
}
.provider-identity-badge {
  position: absolute;
  top: 10px;
  right: 10px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(13, 148, 136, 0.12);
  color: #0d9488;
  font-size: 12px;
  font-weight: 800;
  line-height: 1.3;
}
.provider-identity-badge i {
  font-size: 10px;
}
.provider-identity-card:hover .provider-identity-card-body {
  border-color: #b6c3d6;
}
.provider-identity-card.selected .provider-identity-card-body {
  border-color: #80cbbb;
  background: #effaf7;
  box-shadow: 0 0 0 2px rgba(13, 148, 136, 0.08);
}
.provider-identity-card.selected .provider-identity-icon {
  color: #0d9488;
}
.provider-identity-card > input[type="checkbox"]:focus-visible + .provider-identity-card-body {
  outline: 2px solid rgba(13, 148, 136, 0.55);
  outline-offset: 2px;
}
.provider-identity-error {
  margin: 0 0 10px;
  padding: 8px 10px;
  border: 1px solid rgba(220, 38, 38, 0.24);
  border-radius: 8px;
  background: #fff1f2;
  color: #b91c1c;
  font-size: 12px;
  font-weight: 700;
}
.provider-identity-section.has-identity-error .provider-identity-card-body {
  border-color: rgba(220, 38, 38, 0.28);
}
.provider-identity-tech {
  margin-top: 10px;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
  font-size: 12px;
}
.provider-identity-tech summary {
  cursor: pointer;
  font-weight: 700;
  color: #0f172a;
}
.provider-identity-tech summary:focus-visible {
  outline: 2px solid rgba(13, 148, 136, 0.55);
  outline-offset: 2px;
}
.provider-identity-tech ul {
  margin: 8px 0 0;
  padding-left: 18px;
  display: grid;
  gap: 4px;
  line-height: 1.5;
}
.provider-identity-tech code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
/* Legacy selector kept for any residual markup. */
.provider-auth-choice > label { position: relative; cursor: pointer; }
.provider-auth-choice label > span { min-height: 76px; display: flex; align-items: center; gap: 10px; padding: 13px; border: 1px solid #dbe3ef; border-radius: 10px; background: #f8fafc; }
.provider-auth-choice label > span > i { width: 34px; height: 34px; display: grid; flex: 0 0 34px; place-items: center; border-radius: 9px; background: #fff; color: #64748b; }
.provider-auth-choice label > span > strong, .provider-auth-choice label > span > small { display: block; }
.provider-auth-choice label > span > small { margin-top: 3px; color: #94a3b8; font-weight: 500; }
.provider-auth-choice label.selected > span { border-color: #80cbbb; background: #effaf7; box-shadow: 0 0 0 2px rgba(13, 148, 136, .08); }
.provider-auth-choice label.selected > span > i { color: #0d9488; }
.provider-oauth-editor { display: grid; gap: 14px; padding-top: 4px; }
.provider-built-in-fields { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; padding: 10px 12px; border-radius: 9px; background: #f1f5f9; color: #64748b; font-size: 10px; }
.provider-built-in-fields > span { margin-right: auto; font-weight: 700; }
.provider-built-in-fields code { padding: 4px 7px; border-radius: 6px; background: #fff; color: #475569; }
.provider-repeatable-heading { display: flex; align-items: center; justify-content: space-between; gap: 14px; padding-top: 5px; }
.provider-repeatable-heading h4 { margin: 0; color: #1e293b; font-size: 12px; }
.provider-repeatable-heading p { margin: 3px 0 0; color: #94a3b8; font-size: 10px; line-height: 1.5; }
.provider-repeatable-heading button { min-height: 34px; display: inline-flex; flex: 0 0 auto; align-items: center; gap: 6px; padding: 0 10px; border: 1px solid #dbe3ef; border-radius: 8px; background: #fff; color: #0d9488; font: inherit; font-size: 10px; font-weight: 700; cursor: pointer; }
.provider-repeatable-empty { padding: 14px; border: 1px dashed #cbd5e1; border-radius: 9px; color: #94a3b8; text-align: center; font-size: 10px; }
.provider-repeatable-card { display: grid; gap: 11px; padding: 13px; border: 1px solid #e2e8f0; border-radius: 10px; background: #fbfdff; }
.provider-repeatable-card > header { display: flex; align-items: center; justify-content: space-between; color: #475569; font-size: 11px; }
.provider-repeatable-card > header button { width: 30px; height: 30px; border: 0; border-radius: 7px; background: #fff1f2; color: #dc2626; cursor: pointer; }
.provider-checkbox { display: flex !important; min-height: 42px; align-items: center; align-self: end; gap: 8px !important; padding: 0 4px; }
.provider-checkbox input { width: 16px; min-height: 16px; }
.provider-form-section details { border: 1px solid #e2e8f0; border-radius: 10px; background: #fbfdff; }
.provider-form-section summary { padding: 12px 13px; color: #334155; font-size: 11px; font-weight: 750; cursor: pointer; }
.provider-form-section details > div { padding: 0 13px 13px; }
.provider-delete-dialog { width: min(460px, calc(100vw - 32px)); display: grid; justify-items: center; gap: 12px; padding: 24px; border-radius: 15px; background: #fff; box-shadow: 0 30px 80px rgba(15,23,42,.3); text-align: center; }
.provider-delete-icon { width: 50px; height: 50px; display: grid; place-items: center; border-radius: 15px; background: #fff1f2; color: #dc2626; font-size: 19px; }
.provider-delete-dialog h2 { margin: 0; color: #0f172a; font-size: 19px; }
.provider-delete-dialog > p { margin: 0; color: #64748b; font-size: 12px; line-height: 1.65; }
.provider-delete-dialog label { width: 100%; text-align: left; }
.provider-delete-dialog footer { width: 100%; display: flex; justify-content: flex-end; gap: 9px; margin-top: 4px; }
.provider-delete-dialog footer button { min-height: 40px; display: inline-flex; align-items: center; gap: 7px; padding: 0 14px; border: 1px solid #dbe3ef; border-radius: 8px; background: #fff; color: #475569; font: inherit; font-size: 11px; font-weight: 700; cursor: pointer; }
.provider-delete-dialog footer button.danger { border-color: #dc2626; background: #dc2626; color: #fff; }
.provider-delete-dialog footer button:disabled { cursor: not-allowed; opacity: .45; }
.provider-action-toast { position: fixed; z-index: 3200; right: 24px; bottom: 24px; max-width: min(520px, calc(100vw - 48px)); display: flex; align-items: center; gap: 9px; padding: 12px 14px; border: 1px solid #bfe9df; border-radius: 10px; background: #fff; color: #07806e; box-shadow: 0 14px 38px rgba(15,23,42,.16); font-size: 12px; }
.provider-action-toast.error { border-color: #fecdd3; color: #b91c1c; }
.provider-action-toast.warning { border-color: #fde68a; color: #92400e; }
.provider-action-toast button { margin-left: auto; border: 0; background: transparent; color: inherit; cursor: pointer; }
@media (max-width: 1180px) { .provider-contract-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 820px) {
  .providers-page-header { align-items: stretch; flex-direction: column; }
  .providers-header-actions { justify-content: stretch; }
  .providers-header-actions > * { flex: 1 1 0; }
  .provider-form-grid.two, .provider-form-grid.three, .provider-form-grid.verification, .provider-auth-choice { grid-template-columns: 1fr; }
  .provider-modal-backdrop { padding: 8px; }
  .provider-editor { max-height: calc(100vh - 16px); }
}
@media (max-width: 620px) {
  .provider-contract-summary { grid-template-columns: 1fr; }
  .provider-assets-list article { grid-template-columns: auto minmax(0, 1fr); }
  .provider-assets-list article > button, .provider-assets-list article > .asset-status { grid-column: 2; justify-self: start; }
  .provider-actions.mobile { grid-template-columns: repeat(2, 1fr); }
}
</style>
