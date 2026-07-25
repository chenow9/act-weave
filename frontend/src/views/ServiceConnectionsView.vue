<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useIntegrationStore } from "../stores/integration";
import { useWorkspaceStore } from "../stores/workspaces";
import { normalizeServiceBaseURL } from "../utils/normalize-service-base-url";
import {
  authModeForScheme,
  connectionAuthValues,
  connectionProviderAuthScheme,
  credentialField,
  isProviderReadyForConnections,
  legacySchemeForConnection,
  providerAuthScheme,
  providerAuthSchemes,
  publicAuthFields,
  uiModeForScheme,
} from "../utils/provider-auth";
import type {
  CapabilityProvider,
  OutboundIdentityMode,
  ServiceConnection,
  ServiceConnectionListQuery,
  ServiceConnectionVerification,
} from "../types/domain";

type ConnectionStatusFilter = "ALL" | "VERIFIED" | "UNVERIFIED" | "ERROR" | "DISABLED";
type ConnectionMigrationFilter = "ALL" | "MIGRATION_REQUIRED";
type ConnectionModeFilter = "ALL" | OutboundIdentityMode;
type ConnectionView = "list" | "detail" | "form";
type ConnectionDropdownKey = "environment" | "verificationMethod" | "refreshMode";
type ActionToastTone = "success" | "warning" | "error";
type ConnectionCloseReason = "backdrop" | "escape" | "back" | "cancel";
type VerificationCheckKey = "address" | "credential" | "testCall" | "refresh";
type VerificationCheckStatus = "passed" | "failed" | "pending";
type VerificationCheckDefinition = { key: VerificationCheckKey; label: string; desc: string; failedLabel: string; actionLabel: string; icon: string };
type ConnectionFormFieldKey = string;
type ConnectionSubmitIntent = "draft" | "verify";
type ConnectionVerificationPhase = "idle" | "saving" | "saveFailed" | "verifying" | "verificationFailed" | "passed";
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const OUTBOUND_MODES: OutboundIdentityMode[] = ["BROKER_OBO", "REQUEST_PASSTHROUGH"];

const integration = useIntegrationStore();
const router = useRouter();
const workspaces = useWorkspaceStore();
const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));
const query = ref("");
const connectionStatusFilter = ref<ConnectionStatusFilter>("ALL");
const connectionMigrationFilter = ref<ConnectionMigrationFilter>("ALL");
const connectionModeFilter = ref<ConnectionModeFilter>("ALL");
const connectionsHasLoaded = ref(false);
const connectionListLoading = ref(false);
const connectionLoadError = ref<string | null>(null);
const mobileConnectionActionMenuId = ref<string | null>(null);
const connectionCurrentView = ref<ConnectionView>("list");
const detailConnectionId = ref("");
const connectionFormMode = ref<"create" | "edit">("create");
const draftConnection = ref<ServiceConnection>(newConnection());
const actionNote = ref("");
const actionToastTone = ref<ActionToastTone>("success");
const testedConnectionIds = ref<string[]>([]);
const connectionNameInput = ref<HTMLInputElement | null>(null);
const verificationSectionOpen = ref(false);
const advancedSectionOpen = ref(false);
const connectionFormErrors = ref<Partial<Record<ConnectionFormFieldKey, string>>>({});
const connectionVerificationPhase = ref<ConnectionVerificationPhase>("idle");
const formVerificationFeedback = ref<ServiceConnectionVerification | null>(null);
const formSubmitError = ref("");
const serviceAddressInput = ref<HTMLInputElement | null>(null);
const clientSecretInput = ref<HTMLInputElement | null>(null);
const machineCredentialInput = ref<HTMLInputElement | null>(null);
const credentialInputDirty = ref(false);
const impactProof = ref("");
const impactPreview = ref<{ impactConfirmationProof: string; machineCredentialWillChange?: boolean; expiresAt?: string } | null>(null);
const impactLoading = ref(false);
const switchModePending = ref<OutboundIdentityMode | null>(null);
const environmentTrigger = ref<HTMLButtonElement | null>(null);
const connectionFormWorkspace = ref<HTMLElement | null>(null);
const connectionFormSnapshot = ref("");
const savingConnection = ref(false);
const savingAndVerifyingConnection = ref(false);
const verifyingConnectionIds = ref<string[]>([]);
const pendingDeleteConnection = ref<ServiceConnection | null>(null);
const deleteConfirmName = ref("");
const deleteError = ref("");
const deletingConnection = ref(false);
const discardDialogVisible = ref(false);
const connectionDropdowns = ref<Record<ConnectionDropdownKey, boolean>>({
  environment: false,
  verificationMethod: false,
  refreshMode: false,
});
const connectionDropdownMenuIds: Record<ConnectionDropdownKey, string> = {
  environment: "connection-environment-menu",
  verificationMethod: "connection-verification-method-menu",
  refreshMode: "connection-refresh-mode-menu",
};

const environmentOptions = [
  { label: "生产", value: "生产" },
  { label: "测试", value: "测试" },
];
const refreshModeOptions = [
  { label: "重新获取访问凭证", value: "same" },
  { label: "调用续期接口自动更新", value: "dedicated" },
  { label: "过期后人工处理", value: "none" },
];
const verificationMethodOptions = [
  { label: "GET", value: "GET" },
  { label: "POST", value: "POST" },
  { label: "HEAD", value: "HEAD" },
];
const connectionStatusOptions = [
  { label: "全部", value: "ALL" },
  { label: "已验证", value: "VERIFIED" },
  { label: "未验证", value: "UNVERIFIED" },
  { label: "错误", value: "ERROR" },
  { label: "已停用", value: "DISABLED" },
];
const connectionMigrationOptions = [
  { label: "全部", value: "ALL" },
  { label: "待迁移", value: "MIGRATION_REQUIRED" },
];
const connectionModeOptions = [
  { label: "全部", value: "ALL" },
  { label: "Broker / OBO", value: "BROKER_OBO" },
  { label: "请求透传", value: "REQUEST_PASSTHROUGH" },
];
const providerOptions = computed(() => integration.providers.map((provider) => ({
  label: isProviderReadyForConnections(provider) ? provider.name : `${provider.name}（待完成配置）`,
  value: provider.id,
  disabled: !isProviderReadyForConnections(provider),
})));
const readyProviderCount = computed(() => integration.providers.filter((provider) => isProviderReadyForConnections(provider)).length);
const migrationRequiredCount = computed(
  () => integration.serviceConnections.filter((c) => c.migrationState === "MIGRATION_REQUIRED").length,
);

const connectionColumns = computed<ManagementListColumn<ServiceConnection>[]>(() => [
  { key: "name", label: "连接名称", width: 200, sortable: true, sortKey: "name", getValue: (connection) => connection.name },
  {
    key: "protocol",
    label: "协议",
    width: 84,
    align: "center",
    headerAlign: "center",
    hidable: true,
    sortable: true, sortKey: "protocol",
    getValue: (connection) => connection.protocol || "HTTP",
  },
  {
    key: "environment",
    label: "环境",
    width: 84,
    align: "center",
    headerAlign: "center",
    hidable: true,
    sortable: true, sortKey: "environment",
    getValue: (connection) => environmentLabel(connection.environment),
  },
  { key: "address", label: "地址 & 验证接口", width: 220, hidable: true, sortable: true, sortKey: "address", getValue: (connection) => connectionAddress(connection) },
  {
    key: "outboundMode",
    label: "身份策略",
    width: 140,
    hidable: true,
    sortable: true,
    sortKey: "outboundMode",
    getValue: (connection) => outboundModeLabel(connection),
  },
  {
    key: "status",
    label: "配置状态",
    width: 120,
    align: "center",
    headerAlign: "center",
    hidable: true,
    sortable: true, sortKey: "status",
    getValue: (connection) => statusLabel(connection),
  },
  {
    key: "migrationState",
    label: "迁移",
    width: 100,
    align: "center",
    headerAlign: "center",
    hidable: true,
    getValue: (connection) => (connection.migrationState === "MIGRATION_REQUIRED" ? "需迁移" : "—"),
  },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
]);

const hasConnectionRecords = computed(() => integration.serviceConnectionRegistryTotal > 0);
const detailConnection = computed(() => integration.serviceConnections.find((item) => item.id === detailConnectionId.value));
const selectedConnectionProvider = computed(() => integration.providers.find((provider) => provider.id === draftConnection.value.providerId));
const providerSupportedModes = computed(() => providerOutboundSupportedModes(selectedConnectionProvider.value));
const hasProviderOutboundContract = computed(() => providerSupportedModes.value.length > 0);
const draftOutboundMode = computed(() => draftConnection.value.outboundMode);
const isMigrationConnection = computed(
  () => draftConnection.value.migrationState === "MIGRATION_REQUIRED" || draftConnection.value.status === "DISABLED",
);
const selectedAuthScheme = computed(() => {
  const provider = selectedConnectionProvider.value;
  if (connectionFormMode.value === "edit") return connectionProviderAuthScheme(provider, draftConnection.value);
  const schemeKey = draftConnection.value.authConfig.schemeKey;
  if (schemeKey) return providerAuthScheme(provider, schemeKey);
  return providerAuthScheme(provider);
});
const connectionAuthSchemeOptions = computed(() => providerAuthSchemes(selectedConnectionProvider.value).map((scheme) => ({
  label: scheme.displayName,
  value: scheme.key,
})));
const selectedPublicAuthFields = computed(() => publicAuthFields(selectedAuthScheme.value));
const selectedCredentialField = computed(() => credentialField(selectedAuthScheme.value));
const legacyConnectionAuth = computed(
  () =>
    connectionFormMode.value === "edit" &&
    !draftConnection.value.outboundMode &&
    draftConnection.value.migrationState === "MIGRATION_REQUIRED",
);
const usesDualModeForm = computed(
  () => Boolean(draftConnection.value.outboundMode) || connectionFormMode.value === "create" || draftConnection.value.migrationState === "MIGRATION_REQUIRED",
);
const brokerClientId = computed({
  get: () => {
    const broker = (draftConnection.value.outboundIdentity?.brokerObo || {}) as Record<string, unknown>;
    return String(broker.clientId || draftConnection.value.authConfig.clientId || "");
  },
  set: (value: string) => {
    const identity = { ...(draftConnection.value.outboundIdentity || {}) };
    const broker = { ...((identity.brokerObo as Record<string, unknown>) || {}) };
    broker.clientId = value;
    identity.mode = "BROKER_OBO";
    identity.schemaVersion = "outbound-connection.v1";
    identity.brokerObo = broker;
    draftConnection.value = { ...draftConnection.value, outboundIdentity: identity, outboundMode: "BROKER_OBO" };
  },
});
const brokerScopesText = computed({
  get: () => {
    const broker = (draftConnection.value.outboundIdentity?.brokerObo || {}) as Record<string, unknown>;
    const scopes = Array.isArray(broker.scopes) ? broker.scopes.map(String) : [];
    if (scopes.length) return scopes.join(" ");
    return draftConnection.value.authConfig.scope || "";
  },
  set: (value: string) => {
    const identity = { ...(draftConnection.value.outboundIdentity || {}) };
    const broker = { ...((identity.brokerObo as Record<string, unknown>) || {}) };
    broker.scopes = value.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean);
    identity.mode = "BROKER_OBO";
    identity.schemaVersion = "outbound-connection.v1";
    identity.brokerObo = broker;
    draftConnection.value = { ...draftConnection.value, outboundIdentity: identity, outboundMode: "BROKER_OBO" };
  },
});
const passthroughMaxResidence = computed({
  get: () => {
    const pt = (draftConnection.value.outboundIdentity?.requestPassthrough || {}) as Record<string, unknown>;
    return Number(pt.maxResidenceSeconds) || 600;
  },
  set: (value: number) => {
    const identity = { ...(draftConnection.value.outboundIdentity || {}) };
    identity.mode = "REQUEST_PASSTHROUGH";
    identity.schemaVersion = "outbound-connection.v1";
    identity.requestPassthrough = { maxResidenceSeconds: Number(value) || 600 };
    draftConnection.value = { ...draftConnection.value, outboundIdentity: identity, outboundMode: "REQUEST_PASSTHROUGH" };
  },
});
const connectionFormTitle = computed(() => (connectionFormMode.value === "create" ? "新建服务连接" : "编辑服务连接"));
const formSubmitting = computed(() => savingConnection.value || savingAndVerifyingConnection.value);
const saveButtonText = computed(() => (savingConnection.value ? "保存中" : "保存草稿"));
const saveAndVerifyButtonText = computed(() => {
  if (savingAndVerifyingConnection.value) return "验证中";
  return connectionFormMode.value === "create" ? "创建并验证" : "保存并验证";
});
const draftConnectionVerificationPreview = computed(() => connectionVerificationTarget(draftConnection.value) || "填写服务地址后显示验证接口");
const verificationPathDisplay = computed(() =>
  draftConnection.value.protocolConfig.verificationPath.trim() || "使用服务根地址",
);
const authModeHelp = computed(() => authModeInstruction());
const draftEnvironmentLabel = computed(() =>
  draftConnection.value.environment ? environmentLabel(draftConnection.value.environment) : "请选择使用环境",
);
const computedRefreshModeLabel = computed(
  () => refreshModeOptions.find((option) => option.value === draftConnection.value.authConfig.refreshMode)?.label || "重新获取访问凭证",
);
const needsRefreshConfig = computed(() => false);
const showsTokenFieldPaths = computed(() => false);
const connectionFormDirty = computed(() => connectionCurrentView.value === "form" &&
  (credentialInputDirty.value || snapshotConnection(draftConnection.value) !== connectionFormSnapshot.value));
const deleteDialogDirty = computed(() => Boolean(deleteConfirmName.value.trim()));
const deleteConfirmMatches = computed(() => deleteConfirmName.value.trim() === pendingDeleteConnection.value?.name);
const verificationChecks: VerificationCheckDefinition[] = [
  { key: "address", label: "地址可访问", failedLabel: "地址不可访问", actionLabel: "编辑服务地址", desc: "域名、端口和 Base Path 拼出的地址是否可以访问", icon: "fa-solid fa-network-wired" },
  { key: "credential", label: "凭证已配置/已获取", failedLabel: "凭证获取失败", actionLabel: "编辑认证配置", desc: "固定 Token、API Key 或认证接口可以得到可注入的访问凭证", icon: "fa-solid fa-key" },
  { key: "testCall", label: "验证接口通过", failedLabel: "验证接口失败", actionLabel: "编辑验证接口", desc: "带上当前凭证请求验证接口，状态码和响应内容符合预期", icon: "fa-solid fa-vial" },
  { key: "refresh", label: "续期策略", failedLabel: "续期策略未就绪", actionLabel: "编辑凭证续期", desc: "凭证过期后可以按当前方式处理", icon: "fa-solid fa-rotate" },
];
const detailVerificationChecks = computed(() => {
  const connection = detailConnection.value;
  if (!connection) return [];
  return verificationChecks.map((check) => {
    const status = verificationCheckStatus(connection, check.key);
    return {
      ...check,
      status,
      statusLabel: status === "failed" ? check.failedLabel : status === "passed" ? check.label : "尚未检查",
      actionLabel: verificationCheckActionLabel(connection, check),
    };
  });
});
const formVerificationChecks = computed(() => {
  const verification = formVerificationFeedback.value;
  if (!verification) return [];
  const passed = verification.status === "SUCCEEDED";
  return [
    { label: "连接验证", passed, desc: verification.diagnostics.code || (passed ? "连接验证通过" : "连接验证失败") },
    { label: "安全诊断", passed, desc: verification.diagnostics.category || "无诊断分类" },
  ];
});
const formVerificationResultTitle = computed(() => {
  if (connectionVerificationPhase.value === "saving") return "正在保存连接";
  if (connectionVerificationPhase.value === "saveFailed") return "未能保存连接";
  if (connectionVerificationPhase.value === "verifying") return "正在验证连接";
  if (connectionVerificationPhase.value === "verificationFailed") {
    return formVerificationFeedback.value ? "连接已保存，但验证未通过" : "连接已保存，但验证请求失败";
  }
  if (connectionVerificationPhase.value === "passed") return "连接验证通过";
  return "";
});

onMounted(async () => {
  window.addEventListener("keydown", handleGlobalKeydown);
  window.addEventListener("popstate", syncConnectionViewFromLocation);
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (hasWorkspaceContext.value) {
      await loadConnections();
      syncConnectionViewFromLocation();
    } else {
      connectionsHasLoaded.value = true;
    }
  } catch {
    connectionsHasLoaded.value = true;
  }
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleGlobalKeydown);
  window.removeEventListener("popstate", syncConnectionViewFromLocation);
});

function snapshotConnection(connection: ServiceConnection) {
  return JSON.stringify(connection);
}

async function loadConnections() {
  connectionListLoading.value = true;
  connectionLoadError.value = null;
  try {
    await integration.loadProviders();
    await Promise.all([loadConnectionPage(), integration.loadServiceConnectionCatalog()]);
  } catch (error) {
    connectionLoadError.value = error instanceof Error && error.message ? error.message : "加载失败，请稍后重试。";
  } finally {
    connectionListLoading.value = false;
    connectionsHasLoaded.value = true;
  }
}

function providerRuntimeAddress(provider?: CapabilityProvider) {
  if (!provider) return "";
  const value = provider.endpointConfig.serviceBaseUrl ?? provider.endpointConfig.baseUrl ?? provider.endpointConfig.url;
  return typeof value === "string" ? value : "";
}

function providerVerification(provider?: CapabilityProvider) {
  const verification = provider?.endpointConfig.verification;
  return verification && typeof verification === "object" && !Array.isArray(verification)
    ? verification as Record<string, unknown>
    : {};
}

function selectConnectionProvider(providerId: string) {
  clearConnectionCredentialInput();
  clearMachineCredentialInput();
  draftConnection.value.providerId = providerId;
  const provider = integration.providers.find((item) => item.id === providerId);
  if (!provider) return;
  const scheme = providerAuthScheme(provider);
  const verification = providerVerification(provider);
  draftConnection.value.protocolConfig.domain = providerRuntimeAddress(provider);
  draftConnection.value.protocolConfig.verificationMethod = typeof verification.method === "string" ? verification.method : "GET";
  draftConnection.value.protocolConfig.verificationPath = typeof verification.path === "string" ? verification.path : "";
  draftConnection.value.protocolConfig.expectedStatus = Array.isArray(verification.expectedStatuses)
    ? verification.expectedStatuses.join(", ")
    : "200, 204";
  const supported = providerOutboundSupportedModes(provider);
  if (supported.length) {
    const mode =
      draftConnection.value.outboundMode && supported.includes(draftConnection.value.outboundMode)
        ? draftConnection.value.outboundMode
        : supported.length === 1
          ? supported[0]
          : supported.includes("REQUEST_PASSTHROUGH")
            ? "REQUEST_PASSTHROUGH"
            : supported[0];
    applyOutboundMode(mode!);
    return;
  }
  draftConnection.value.outboundMode = undefined;
  draftConnection.value.outboundIdentity = undefined;
  if (!scheme) {
    draftConnection.value.authMode = "";
    draftConnection.value.authConfig = { ...draftConnection.value.authConfig, mode: "", label: "", schemeKey: "", values: {} };
    return;
  }
  draftConnection.value.authMode = authModeForScheme(scheme);
  draftConnection.value.authConfig = {
    ...draftConnection.value.authConfig,
    mode: uiModeForScheme(scheme), label: scheme.displayName, schemeKey: scheme.key, values: {},
  };
}

function selectConnectionAuthScheme(schemeKey: string) {
  const scheme = providerAuthScheme(selectedConnectionProvider.value, schemeKey);
  if (!scheme) return;
  clearConnectionCredentialInput();
  draftConnection.value.authMode = authModeForScheme(scheme);
  draftConnection.value.authConfig = {
    ...draftConnection.value.authConfig,
    mode: uiModeForScheme(scheme), label: scheme.displayName, schemeKey: scheme.key, values: {},
  };
  connectionFormErrors.value = {};
}

function selectedConnectionStatus() {
  return connectionStatusFilter.value === "ALL" ? undefined : connectionStatusFilter.value;
}

async function loadConnectionPage(overrides: ServiceConnectionListQuery = {}) {
  return integration.loadServiceConnectionPage({
    query: query.value.trim(),
    status: selectedConnectionStatus(),
    page: overrides.page ?? integration.serviceConnectionPagination.page,
    pageSize: overrides.pageSize ?? integration.serviceConnectionPagination.pageSize,
    ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
  });
}

async function requestConnectionPage(overrides: ServiceConnectionListQuery = {}) {
  connectionListLoading.value = true;
  connectionLoadError.value = null;
  try {
    await loadConnectionPage(overrides);
  } catch (error) {
    connectionLoadError.value = error instanceof Error && error.message ? error.message : "加载失败，请稍后重试。";
  } finally {
    connectionListLoading.value = false;
    connectionsHasLoaded.value = true;
  }
}

async function reloadConnectionData(overrides: ServiceConnectionListQuery = {}) {
  await Promise.all([loadConnectionPage(overrides), integration.loadServiceConnectionCatalog()]);
}

async function retryLoadConnections() {
  await loadConnections();
}

function resetConnectionFilters() {
  query.value = "";
  connectionStatusFilter.value = "ALL";
  connectionMigrationFilter.value = "ALL";
  connectionModeFilter.value = "ALL";
  void requestConnectionPage({ page: 1 });
}

function updateConnectionSearch(value: string) {
  query.value = value;
  void requestConnectionPage({ page: 1 });
}

function updateConnectionStatusFilter(value: string) {
  connectionStatusFilter.value = value as ConnectionStatusFilter;
  void requestConnectionPage({ page: 1 });
}

function updateConnectionMigrationFilter(value: string) {
  connectionMigrationFilter.value = value as ConnectionMigrationFilter;
}

function updateConnectionModeFilter(value: string) {
  connectionModeFilter.value = value as ConnectionModeFilter;
}

const filteredConnectionRows = computed(() => {
  let rows = integration.serviceConnectionPageItems;
  if (connectionMigrationFilter.value === "MIGRATION_REQUIRED") {
    rows = rows.filter((c) => c.migrationState === "MIGRATION_REQUIRED");
  }
  if (connectionModeFilter.value !== "ALL") {
    rows = rows.filter((c) => c.outboundMode === connectionModeFilter.value);
  }
  return rows;
});

function changeConnectionSort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
  void requestConnectionPage({
    page: 1,
    pageSize: integration.serviceConnectionPagination.pageSize,
    sortBy: sort.sortBy ?? "",
    sortOrder: sort.sortOrder,
  });
}

function changeConnectionPage(pagination: { page: number; pageSize: number }) {
  void requestConnectionPage(pagination);
}

/** List-row menu order: detail → edit → verify → delete (ZKL-33). */
function connectionMenuActions(connection: ServiceConnection): ManagementRowAction[] {
  const verifying = isConnectionVerifying(connection.id);
  return [
    { key: "detail", label: "查看详情", icon: "fa-solid fa-eye", tone: "primary" },
    { key: "edit", label: "编辑连接", icon: "fa-solid fa-pen-to-square" },
    {
      key: "verify",
      label: "验证连接",
      icon: "fa-solid fa-vial",
      loading: verifying,
      disabled: verifying,
      disabledReason: verifying ? "验证中" : undefined,
    },
    { key: "delete", label: "删除连接", icon: "fa-solid fa-trash-can", tone: "danger" },
  ];
}

function handleConnectionRowAction(actionKey: string, connection: ServiceConnection) {
  if (actionKey === "detail") {
    openConnectionPreview(connection);
    return;
  }
  if (actionKey === "edit") {
    openConnectionEditor(connection);
    return;
  }
  if (actionKey === "verify") {
    void verifyConnection(connection);
    return;
  }
  if (actionKey === "delete") requestRemoveConnection(connection);
}

function toggleMobileConnectionActions(connectionId: string) {
  mobileConnectionActionMenuId.value = mobileConnectionActionMenuId.value === connectionId ? null : connectionId;
}

function closeMobileConnectionActions() {
  mobileConnectionActionMenuId.value = null;
}

function closeConnectionFloatingMenus() {
  closeConnectionDropdowns();
  closeMobileConnectionActions();
}

function openMobileConnectionPreview(connection: ServiceConnection) {
  closeMobileConnectionActions();
  openConnectionPreview(connection);
}

function openMobileConnectionEditor(connection: ServiceConnection) {
  closeMobileConnectionActions();
  openConnectionEditor(connection);
}

async function verifyMobileConnection(connection: ServiceConnection) {
  closeMobileConnectionActions();
  await verifyConnection(connection);
}

function requestMobileRemoveConnection(connection: ServiceConnection) {
  closeMobileConnectionActions();
  requestRemoveConnection(connection);
}

function showActionNote(message: string, tone: ActionToastTone = "success") {
  actionNote.value = message;
  actionToastTone.value = tone;
}

function dismissActionNote() {
  actionNote.value = "";
}

function updateConnectionDetailUrl(connectionId = "") {
  const url = new URL(window.location.href);
  if (connectionId) {
    url.searchParams.set("connectionId", connectionId);
  } else {
    url.searchParams.delete("connectionId");
  }
  const nextUrl = `${url.pathname}${url.search}${url.hash}`;
  if (nextUrl !== `${window.location.pathname}${window.location.search}${window.location.hash}`) {
    window.history.pushState({}, "", nextUrl);
  }
}

function syncConnectionViewFromLocation() {
  const connectionId = new URLSearchParams(window.location.search).get("connectionId") || "";
  if (!connectionId) {
    if (connectionCurrentView.value === "form" && connectionFormDirty.value) {
      discardDialogVisible.value = true;
      warnUnsavedConnectionForm();
      return;
    }
    if (connectionCurrentView.value === "detail" || connectionCurrentView.value === "form") {
      closeConnectionDropdowns();
      discardDialogVisible.value = false;
      detailConnectionId.value = "";
      connectionCurrentView.value = "list";
      actionNote.value = "";
    }
    return;
  }
  const connection = integration.serviceConnections.find((item) => item.id === connectionId);
  if (!connection) return;
  detailConnectionId.value = connection.id;
  connectionCurrentView.value = "detail";
  actionNote.value = "";
}

async function copyConnectionText(value: string, label: string) {
  if (!value) return;
  try {
    if (!navigator.clipboard) throw new Error("clipboard unavailable");
    await navigator.clipboard.writeText(value);
    showActionNote(`${label}已复制。`);
  } catch {
    showActionNote("当前浏览器不允许自动复制，请手动选择文本。", "warning");
  }
}

function focusConnectionName() {
  void nextTick(() => {
    connectionNameInput.value?.focus();
  });
}

function isConnectionVerifying(connectionId: string) {
  return verifyingConnectionIds.value.includes(connectionId);
}

function addVerifyingConnection(connectionId: string) {
  verifyingConnectionIds.value = [...new Set([...verifyingConnectionIds.value, connectionId])];
}

function removeVerifyingConnection(connectionId: string) {
  verifyingConnectionIds.value = verifyingConnectionIds.value.filter((id) => id !== connectionId);
}

function warnUnsavedConnectionForm() {
  showActionNote("表单有未保存内容；关闭表单需在确认框中确认放弃修改。", "warning");
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  if (connectionCurrentView.value === "form") {
    event.preventDefault();
    if (Object.values(connectionDropdowns.value).some(Boolean)) {
      closeConnectionDropdowns();
      return;
    }
    requestCloseConnectionForm("escape");
    return;
  }
  if (pendingDeleteConnection.value) {
    event.preventDefault();
    if (deleteDialogDirty.value) {
      showActionNote("删除确认已输入内容，请点击取消或完成删除。", "warning");
      return;
    }
    closeDeleteDialog();
  }
}

function trapConnectionFormFocus(event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  const container = connectionFormWorkspace.value;
  if (!container) return;
  const focusable = Array.from(
    container.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex='-1'])"),
  ).filter((element) => element.offsetParent !== null);
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

function statusLabel(connection: ServiceConnection) {
  if (!connection.id) return "未保存";
  if (testedConnectionIds.value.includes(connection.id)) return "可用";
  if (connection.status === "VERIFIED") return "可用";
  if (connection.status === "DISABLED") return "已停用";
  return "需处理";
}

function supportsCredentialRenewalConfig(connection: ServiceConnection) {
  return ["oauth2-client", "oauth2-mtls", "custom-token-api"].includes(connection.authConfig.mode);
}

function verificationCheckStatus(connection: ServiceConnection, key: VerificationCheckKey): VerificationCheckStatus {
  const verification = integration.verificationByConnectionId[connection.id];
  if (!verification) return statusLabel(connection) === "可用" ? "passed" : "pending";
  return verification.status === "SUCCEEDED" ? "passed" : "failed";
}

function verificationCheckLabel(connection: ServiceConnection, check: (typeof verificationChecks)[number]) {
  const status = verificationCheckStatus(connection, check.key);
  if (status === "passed") return check.label;
  if (status === "failed") return check.failedLabel;
  return "尚未检查";
}

function verificationCheckActionLabel(connection: ServiceConnection, check: VerificationCheckDefinition) {
  if (check.key === "refresh" && !supportsCredentialRenewalConfig(connection)) return "编辑认证配置";
  return check.actionLabel;
}

function verificationCheckAction(connection: ServiceConnection, check: VerificationCheckDefinition) {
  const actionLabel = verificationCheckActionLabel(connection, check);
  openConnectionEditor(connection);
  showActionNote(`${actionLabel}后请重新验证连接。`, "warning");
}

function statusClass(connection: ServiceConnection) {
  const label = statusLabel(connection);
  if (label === "可用") return "available";
  return "attention";
}

function statusPillClass(connection: ServiceConnection) {
  return statusClass(connection);
}

function statusDotClass(connection: ServiceConnection) {
  return statusClass(connection);
}

function lastVerified(connection: ServiceConnection) {
  if (testedConnectionIds.value.includes(connection.id)) return "刚刚";
  if (!connection.lastVerifiedAt) return "尚未验证";
  const timestamp = Date.parse(connection.lastVerifiedAt);
  if (!Number.isFinite(timestamp)) return connection.lastVerifiedAt;
  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
  if (elapsedMinutes < 1) return "刚刚";
  if (elapsedMinutes < 60) return `${elapsedMinutes} 分钟前`;
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) return `${elapsedHours} 小时前`;
  return `${Math.floor(elapsedHours / 24)} 天前`;
}

function lastVerifiedTitle(connection: ServiceConnection) {
  if (testedConnectionIds.value.includes(connection.id)) return "刚刚验证";
  if (!connection.lastVerifiedAt) return "尚未验证";
  const timestamp = Date.parse(connection.lastVerifiedAt);
  if (!Number.isFinite(timestamp)) return connection.lastVerifiedAt;
  return new Date(timestamp).toLocaleString("zh-CN");
}

function connectionAddress(connection: ServiceConnection) {
  return serviceEndpointAddress(connection) || "未配置地址";
}

/** Compact list primary: host[:port] without scheme; base path kept as secondary suffix. */
function connectionAddressPrimary(connection: ServiceConnection) {
  const full = serviceEndpointAddress(connection);
  if (!full) {
    return { hostPort: "未配置地址", basePath: "", scheme: "" };
  }
  try {
    const parsed = new URL(/^https?:\/\//i.test(full) ? full : `http://${full}`);
    const port = parsed.port ? `:${parsed.port}` : "";
    const basePath = parsed.pathname === "/" ? "" : parsed.pathname.replace(/\/+$/, "") || "";
    return {
      hostPort: `${parsed.hostname}${port}`,
      basePath,
      scheme: parsed.protocol.replace(":", "").toLowerCase(),
    };
  } catch {
    return {
      hostPort: full.replace(/^https?:\/\//i, ""),
      basePath: "",
      scheme: "",
    };
  }
}

function verificationMethodLabel(connection: ServiceConnection) {
  return (connection.protocolConfig.verificationMethod || "GET").toUpperCase();
}

function endpointUrlParts(rawAddress: string) {
  const address = rawAddress.trim();
  if (!/^https?:\/\//i.test(address)) {
    return { scheme: "", host: address.replace(/\/.*$/, ""), port: "", path: "" };
  }
  try {
    const parsed = new URL(address);
    return {
      scheme: parsed.protocol.replace(":", "").toLowerCase(),
      host: parsed.hostname,
      port: parsed.port,
      path: parsed.pathname === "/" ? "" : parsed.pathname,
    };
  } catch {
    return { scheme: "", host: address, port: "", path: "" };
  }
}

function defaultPortForScheme(scheme: string) {
  if (scheme === "https") return "443";
  if (scheme === "http") return "80";
  return "";
}

function serviceEndpointAddress(connection: ServiceConnection, requireExplicitScheme = false) {
  // ZKL-56 DEF-01: absolute domain is sole source; no double port.
  const normalized = normalizeServiceBaseURL({
    domain: connection.protocolConfig.domain,
    host: connection.protocolConfig.host,
    port: connection.protocolConfig.port,
    basePath: connection.protocolConfig.basePath,
  });
  if (normalized) {
    if (requireExplicitScheme && !/^https?:\/\//i.test(normalized)) return "";
    return normalized;
  }
  const parts = endpointUrlParts(connection.protocolConfig.domain);
  const port = connection.protocolConfig.port || defaultPortForScheme(parts.scheme);
  const basePath = connection.protocolConfig.basePath || "";
  if (!parts.host) return "";
  if (!parts.scheme && requireExplicitScheme) return "";
  // If host already includes :port, do not append again.
  const hostHasPort = /:\d+$/.test(parts.host.replace(/^\[|\]$/g, ""));
  const scheme = parts.scheme || (["443", "8443", "9443"].includes(port) ? "https" : "http");
  return `${scheme}://${parts.host}${port && !hostHasPort ? `:${port}` : ""}${basePath}`;
}

function joinURLPath(basePath: string, childPath: string) {
  const base = basePath.trim();
  const child = childPath.trim();
  if (!child) return base;
  if (/^https?:\/\//i.test(child)) return child;
  if (!base || base === "/") return child.startsWith("/") ? child : `/${child}`;
  return `${base.replace(/\/+$/, "")}/${child.replace(/^\/+/, "")}`;
}

function connectionVerificationTarget(connection: ServiceConnection) {
  const baseAddress = serviceEndpointAddress(connection, true);
  if (!baseAddress) return "";
  const path = connection.protocolConfig.verificationPath || "";
  if (/^https?:\/\//i.test(path.trim())) return path.trim();
  try {
    const parsed = new URL(baseAddress);
    parsed.pathname = joinURLPath(parsed.pathname, path);
    parsed.search = "";
    parsed.hash = "";
    return parsed.toString();
  } catch {
    return baseAddress;
  }
}

function verificationPathLabel(connection: ServiceConnection) {
  return connection.protocolConfig.verificationPath || connection.protocolConfig.basePath || "/";
}

function connectionPortLabel(connection: ServiceConnection) {
  const parts = endpointUrlParts(connection.protocolConfig.domain);
  return connection.protocolConfig.port || parts.port || defaultPortForScheme(parts.scheme) || "未填写";
}

function credentialPlacementLabel(connection: ServiceConnection) {
  const provider = integration.providers.find((item) => item.id === connection.providerId);
  const scheme = providerAuthScheme(provider, connection.authConfig.schemeKey);
  if (scheme?.oauth2) return `Header · ${scheme.oauth2.injection.headerName}`;
  if (connection.authConfig.credentialPlacement === "query") return "查询参数";
  if (connection.authConfig.mode === "fixed-token" || connection.authConfig.mode === "api-key-secret") return "请求 Header";
  return "认证接口返回后注入 Header";
}

function refreshModeLabel(connection: ServiceConnection) {
  const provider = integration.providers.find((item) => item.id === connection.providerId);
  const scheme = providerAuthScheme(provider, connection.authConfig.schemeKey);
  if (scheme?.oauth2?.refreshStrategy === "REFRESH_TOKEN") return "使用 Renewal Token";
  if (scheme?.oauth2) return "重新执行 Client Credentials";
  return refreshModeOptions.find((option) => option.value === connection.authConfig.refreshMode)?.label || "未配置";
}

function normalizeServiceAddress() {
  const parts = endpointUrlParts(draftConnection.value.protocolConfig.domain);
  if (!parts.scheme || !parts.host) return;
  draftConnection.value.protocolConfig.domain = `${parts.scheme}://${parts.host}`;
  if (!draftConnection.value.protocolConfig.port) {
    draftConnection.value.protocolConfig.port = parts.port || defaultPortForScheme(parts.scheme);
  }
  if (!draftConnection.value.protocolConfig.basePath && parts.path) {
    draftConnection.value.protocolConfig.basePath = parts.path;
  }
}

function providerOutboundSupportedModes(provider?: CapabilityProvider | null): OutboundIdentityMode[] {
  if (!provider) return [];
  const identity = (provider.driverConfig as Record<string, unknown> | undefined)?.outboundIdentity;
  if (!identity || typeof identity !== "object") return [];
  const modes = (identity as { supportedModes?: unknown }).supportedModes;
  if (!Array.isArray(modes)) return [];
  return modes
    .map((m) => String(m).toUpperCase())
    .filter((m): m is OutboundIdentityMode => m === "BROKER_OBO" || m === "REQUEST_PASSTHROUGH");
}

function outboundModeLabel(connection: ServiceConnection): string {
  if (connection.migrationState === "MIGRATION_REQUIRED" && !connection.outboundMode) return "需迁移";
  if (connection.outboundMode === "BROKER_OBO") return "Broker / OBO";
  if (connection.outboundMode === "REQUEST_PASSTHROUGH") return "请求透传";
  return "—";
}

function outboundModeCardTitle(mode: OutboundIdentityMode): string {
  return mode === "BROKER_OBO" ? "Broker / OBO" : "本次请求透传";
}

function outboundModeCardHint(mode: OutboundIdentityMode): string {
  return mode === "BROKER_OBO"
    ? "用机器信任换当前用户的短期业务 Token；不保存用户 Token"
    : "每次执行由调用方附带 Token；离开运行后不可恢复";
}

function selectOutboundMode(mode: OutboundIdentityMode) {
  if (!providerSupportedModes.value.includes(mode) && hasProviderOutboundContract.value) return;
  const prev = draftConnection.value.outboundMode;
  if (connectionFormMode.value === "edit" && prev && prev !== mode && draftConnection.value.id) {
    switchModePending.value = mode;
    void loadImpactPreview(mode);
    return;
  }
  applyOutboundMode(mode);
}

function applyOutboundMode(mode: OutboundIdentityMode) {
  const identity: Record<string, unknown> = {
    schemaVersion: "outbound-connection.v1",
    mode,
  };
  if (mode === "BROKER_OBO") {
    identity.brokerObo = {
      clientId: brokerClientId.value || "",
      scopes: brokerScopesText.value.split(/[\s,]+/).filter(Boolean),
      maxTokenTtlSeconds: 300,
    };
  } else {
    identity.requestPassthrough = { maxResidenceSeconds: passthroughMaxResidence.value || 600 };
  }
  draftConnection.value = {
    ...draftConnection.value,
    outboundMode: mode,
    outboundIdentity: identity,
    authMode: mode,
  };
  clearConnectionFormError("outboundMode");
  switchModePending.value = null;
}

async function loadImpactPreview(mode: OutboundIdentityMode) {
  if (!draftConnection.value.id) {
    applyOutboundMode(mode);
    return;
  }
  impactLoading.value = true;
  impactPreview.value = null;
  impactProof.value = "";
  try {
    const result = await integration.previewConnectionImpact(draftConnection.value.id, {
      changeKind: "OUTBOUND_MODE_SWITCH",
      nonSecretChangeDescriptor: { from: draftConnection.value.outboundMode, to: mode },
      machineCredentialWillChange: mode === "BROKER_OBO",
      expectedLockVersion: draftConnection.value.lockVersion,
    });
    impactPreview.value = result;
    impactProof.value = result.impactConfirmationProof;
  } catch (error) {
    const message = error instanceof Error ? error.message : "影响预览失败";
    showActionNote(message, "error");
    switchModePending.value = null;
  } finally {
    impactLoading.value = false;
  }
}

function confirmModeSwitch() {
  if (!switchModePending.value || !impactProof.value) {
    showActionNote("影响范围已变化，请重新确认", "warning");
    return;
  }
  applyOutboundMode(switchModePending.value);
}

function cancelModeSwitch() {
  switchModePending.value = null;
  impactPreview.value = null;
  impactProof.value = "";
}

function newConnection(): ServiceConnection {
  const provider = integration.providers.find((item) => isProviderReadyForConnections(item));
  const scheme = providerAuthScheme(provider);
  const verification = providerVerification(provider);
  const supported = providerOutboundSupportedModes(provider);
  const defaultMode: OutboundIdentityMode | undefined =
    supported.length === 1 ? supported[0] : supported.includes("REQUEST_PASSTHROUGH") ? "REQUEST_PASSTHROUGH" : supported[0];
  const outboundIdentity = defaultMode
    ? defaultMode === "BROKER_OBO"
      ? { schemaVersion: "outbound-connection.v1", mode: "BROKER_OBO", brokerObo: { clientId: "", scopes: [], maxTokenTtlSeconds: 300 } }
      : { schemaVersion: "outbound-connection.v1", mode: "REQUEST_PASSTHROUGH", requestPassthrough: { maxResidenceSeconds: 600 } }
    : undefined;
  return {
    id: "",
    providerId: provider?.id || "",
    name: "",
    alias: "",
    environment: "",
    protocol: "HTTP",
    protocolConfig: {
      domain: providerRuntimeAddress(provider),
      host: "",
      port: "",
      basePath: "",
      verificationMethod: typeof verification.method === "string" ? verification.method : "GET",
      verificationPath: typeof verification.path === "string" ? verification.path : "",
      expectedStatus: Array.isArray(verification.expectedStatuses) ? verification.expectedStatuses.join(", ") : "200, 204",
      expectedResponseContains: "",
      commonHeaders: {},
    },
    protocolSchema: "http.connection.v1",
    authMode: defaultMode || (scheme ? authModeForScheme(scheme) : ""),
    authConfig: {
      mode: scheme ? uiModeForScheme(scheme) : "",
      label: scheme?.displayName || "",
      schemeKey: scheme?.key || "",
      values: {},
      tokenUrl: "",
      clientId: "",
      clientAuth: "",
      scope: "",
      refreshUrl: "",
      refreshMode: "",
      accessTokenPath: "",
      refreshTokenPath: "",
      expiresPath: "",
      injectionTemplate: "",
      retryOn401Policy: "",
      refreshFailurePolicy: "",
      credentialPlacement: "",
      apiKeyName: "",
      apiSecretName: "",
      tokenHeaderName: "",
      tokenPrefix: "",
    },
    outboundMode: defaultMode,
    outboundIdentity,
    migrationState: "NONE",
    machineCredentialConfigured: false,
    credentialSecretId: "",
    credentialConfigured: false,
    grantedScopes: [],
    policy: {},
    status: "UNVERIFIED",
    createdBy: "",
    updatedBy: "",
    lockVersion: 0,
  };
}

function resetConnectionFormUI() {
  verificationSectionOpen.value = false;
  advancedSectionOpen.value = false;
  connectionFormErrors.value = {};
  connectionVerificationPhase.value = "idle";
  formVerificationFeedback.value = null;
  formSubmitError.value = "";
  clearConnectionCredentialInput();
}

function clearConnectionFormError(field: ConnectionFormFieldKey) {
  if (!connectionFormErrors.value[field]) return;
  const nextErrors = { ...connectionFormErrors.value };
  delete nextErrors[field];
  connectionFormErrors.value = nextErrors;
}

function connectionHasCustomVerification(connection: ServiceConnection) {
  return Boolean(
    connection.protocolConfig.verificationPath.trim() ||
      (connection.protocolConfig.verificationMethod || "GET") !== "GET" ||
      (connection.protocolConfig.expectedStatus || "200-299") !== "200-299" ||
      connection.protocolConfig.expectedResponseContains.trim(),
  );
}

function connectionHasAdvancedConfig(connection: ServiceConnection) {
  const auth = connection.authConfig;
  const addressParts = endpointUrlParts(connection.protocolConfig.domain);
  const configuredPort = connection.protocolConfig.port.trim();
  const hasNonDefaultPort = Boolean(configuredPort && configuredPort !== defaultPortForScheme(addressParts.scheme));
  return Boolean(
    hasNonDefaultPort ||
      connection.protocolConfig.basePath.trim() ||
      auth.accessTokenPath.trim() ||
      auth.refreshTokenPath.trim() ||
      auth.expiresPath.trim() ||
      auth.refreshUrl.trim() ||
      auth.refreshMode === "dedicated",
  );
}

function validateConnectionForm(intent: ConnectionSubmitIntent) {
  const connection = draftConnection.value;
  const errors: Partial<Record<ConnectionFormFieldKey, string>> = {};
  if (!connection.name.trim()) errors.name = "请输入连接名称。";
  if (!connection.providerId) errors.address = "请选择已完成配置的 Provider。";
  if (intent === "verify") {
    if (!connection.environment.trim()) errors.environment = "请选择使用环境。";
  }
  if (usesDualModeForm.value) {
    if (!hasProviderOutboundContract.value && connectionFormMode.value === "create") {
      errors.outboundMode = "该 Provider 尚未声明用户态出站契约（supportedModes），请先到 Provider 管理完成配置。";
    } else if (!connection.outboundMode || !OUTBOUND_MODES.includes(connection.outboundMode)) {
      errors.outboundMode = "请选择出站身份策略：Broker / OBO 或 本次请求透传。";
    } else if (hasProviderOutboundContract.value && !providerSupportedModes.value.includes(connection.outboundMode)) {
      errors.outboundMode = "所选策略不在 Provider supportedModes 内。";
    } else if (connection.outboundMode === "BROKER_OBO") {
      if (!brokerClientId.value.trim()) errors["broker.clientId"] = "请输入 clientId。";
      if (
        !connection.machineCredentialConfigured &&
        !connection.credentialConfigured &&
        !machineCredentialInput.value?.value.trim()
      ) {
        errors["broker.machineCredential"] = "请输入机器凭据（private key PEM）。";
      }
    }
  } else {
    const scheme = selectedAuthScheme.value;
    if (!scheme && connectionFormMode.value === "create") {
      errors.authMode = "该 Provider 尚未发布认证契约，请先到 Provider 管理完成配置。";
    }
    if (scheme) {
      const values = connection.authConfig.values || {};
      for (const field of scheme.fields) {
        if (!field.required) continue;
        if (field.kind === "SECRET") {
          if (!connection.credentialConfigured && !connection.credentialSecretId?.trim() && !clientSecretInput.value?.value.trim()) {
            errors[`auth.${field.key}`] = `请输入${field.label}。`;
          }
        } else if (!values[field.key]?.trim()) {
          errors[`auth.${field.key}`] = `请输入${field.label}。`;
        }
      }
    }
  }
  const credentialSecretID = connection.credentialSecretId?.trim() || "";
  if (credentialSecretID && !UUID_PATTERN.test(credentialSecretID)) {
    errors.credentialSecretId = "Secret ID 必须是有效的 UUID，不能填写 API Key 或 Token。";
  }
  connectionFormErrors.value = errors;
  return Object.keys(errors).length === 0;
}

function machineCredentialPlaintext() {
  return machineCredentialInput.value?.value || "";
}

function clearMachineCredentialInput() {
  if (machineCredentialInput.value) machineCredentialInput.value.value = "";
}

function focusFirstConnectionFormError() {
  const targets: Array<[ConnectionFormFieldKey, HTMLElement | null]> = [
    ["name", connectionNameInput.value],
    ["environment", environmentTrigger.value],
    ["address", serviceAddressInput.value],
  ];
  const firstInvalid = targets.find(([field]) => Boolean(connectionFormErrors.value[field]));
  void nextTick(() => {
    if (firstInvalid?.[1]) {
      firstInvalid[1].focus();
      return;
    }
    const dynamicKey = Object.keys(connectionFormErrors.value).find((key) => key.startsWith("auth."));
    if (dynamicKey) {
      const field = document.querySelector<HTMLElement>(`[data-auth-field="${dynamicKey.slice(5)}"]`);
      (field?.querySelector<HTMLElement>("input, button") || field)?.focus();
    }
  });
}

function authFieldValue(key: string) {
  return draftConnection.value.authConfig.values?.[key] || "";
}

function updateAuthFieldValue(key: string, value: string) {
  draftConnection.value.authConfig.values = { ...(draftConnection.value.authConfig.values || {}), [key]: value };
  clearConnectionFormError(`auth.${key}`);
}

function connectionCredentialPlaintext() {
  return clientSecretInput.value?.value || "";
}

function clearConnectionCredentialInput() {
  if (clientSecretInput.value) clientSecretInput.value.value = "";
  credentialInputDirty.value = false;
}

function handleConnectionCredentialInput(event: Event) {
  credentialInputDirty.value = Boolean((event.target as HTMLInputElement).value);
  if (selectedCredentialField.value) clearConnectionFormError(`auth.${selectedCredentialField.value.key}`);
}

function focusFirstVerificationFailure(verification: ServiceConnectionVerification) {
  void nextTick(() => {
    if (verification.status !== "SUCCEEDED" && !draftConnection.value.credentialConfigured) {
      clientSecretInput.value?.focus();
      return;
    }
    document.querySelector<HTMLElement>("#connection-verification-fields input, #connection-verification-fields button")?.focus();
  });
}

function openCreateConnection() {
  draftConnection.value = newConnection();
  resetConnectionFormUI();
  connectionFormSnapshot.value = snapshotConnection(draftConnection.value);
  connectionFormMode.value = "create";
  connectionCurrentView.value = "form";
  actionNote.value = "";
  focusConnectionName();
}

function openConnectionPreview(connection: ServiceConnection) {
  detailConnectionId.value = connection.id;
  connectionCurrentView.value = "detail";
  actionNote.value = "";
  updateConnectionDetailUrl(connection.id);
}

function closeConnectionPreview() {
  detailConnectionId.value = "";
  connectionCurrentView.value = "list";
  updateConnectionDetailUrl();
}

function toggleConnectionDropdown(key: ConnectionDropdownKey) {
  const opening = !connectionDropdowns.value[key];
  connectionDropdowns.value = {
    environment: false,
    verificationMethod: false,
    refreshMode: false,
    [key]: opening,
  };
  if (opening) focusSelectedConnectionDropdownOption(key);
}

function closeConnectionDropdowns() {
  connectionDropdowns.value = {
    environment: false,
    verificationMethod: false,
    refreshMode: false,
  };
}

function closeConnectionDropdownAndRestoreFocus(key: ConnectionDropdownKey) {
  closeConnectionDropdowns();
  void nextTick(() => connectionDropdownTrigger(key)?.focus());
}

function connectionDropdownTrigger(key: ConnectionDropdownKey) {
  if (key === "environment") return environmentTrigger.value;
  return connectionFormWorkspace.value?.querySelector<HTMLElement>(`[data-dropdown-trigger="${key}"]`) || null;
}

function connectionDropdownOptions(key: ConnectionDropdownKey) {
  const menu = document.getElementById(connectionDropdownMenuIds[key]);
  return menu ? Array.from(menu.querySelectorAll<HTMLButtonElement>("[role='option']")) : [];
}

function focusConnectionDropdownOption(key: ConnectionDropdownKey, index: number) {
  void nextTick(() => {
    const options = connectionDropdownOptions(key);
    if (!options.length) return;
    const normalizedIndex = (index + options.length) % options.length;
    options[normalizedIndex].focus();
  });
}

function focusSelectedConnectionDropdownOption(key: ConnectionDropdownKey) {
  void nextTick(() => {
    const options = connectionDropdownOptions(key);
    if (!options.length) return;
    const selectedOption = options.find((option) => option.getAttribute("aria-selected") === "true");
    (selectedOption || options[0]).focus();
  });
}

function openConnectionDropdownFromKeyboard(event: KeyboardEvent, key: ConnectionDropdownKey) {
  if (!["Enter", " ", "ArrowDown", "ArrowUp"].includes(event.key)) return;
  event.preventDefault();
  connectionDropdowns.value = {
    environment: false,
    verificationMethod: false,
    refreshMode: false,
    [key]: true,
  };
  focusSelectedConnectionDropdownOption(key);
}

function handleConnectionOptionKeydown(event: KeyboardEvent, key: ConnectionDropdownKey, index: number) {
  const options = connectionDropdownOptions(key);
  if (!options.length) return;
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    focusConnectionDropdownOption(key, index + (event.key === "ArrowDown" ? 1 : -1));
    return;
  }
  if (event.key === "Home" || event.key === "End") {
    event.preventDefault();
    focusConnectionDropdownOption(key, event.key === "Home" ? 0 : options.length - 1);
    return;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeConnectionDropdownAndRestoreFocus(key);
    return;
  }
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    options[index].click();
  }
}

function selectEnvironment(environment: string) {
  draftConnection.value.environment = environment;
  clearConnectionFormError("environment");
  closeConnectionDropdownAndRestoreFocus("environment");
}

function selectVerificationMethod(method: string) {
  draftConnection.value.protocolConfig.verificationMethod = method;
  closeConnectionDropdownAndRestoreFocus("verificationMethod");
}

function toggleVerificationSection() {
  verificationSectionOpen.value = !verificationSectionOpen.value;
}

function toggleAdvancedSection() {
  advancedSectionOpen.value = !advancedSectionOpen.value;
}

function openConnectionEditor(connection: ServiceConnection) {
  const provider = integration.providers.find((item) => item.id === connection.providerId);
  const scheme = connectionProviderAuthScheme(provider, connection);
  const authConfig = scheme
    ? {
        ...newConnection().authConfig,
        ...connection.authConfig,
        mode: uiModeForScheme(scheme),
        label: scheme.displayName,
        schemeKey: scheme.key,
        values: connectionAuthValues(connection.authConfig),
      }
    : {
        ...connection.authConfig,
        ...(connection.authConfig.values ? { values: { ...connection.authConfig.values } } : {}),
      };
  draftConnection.value = {
    ...connection,
    environment: connection.environment.trim() ? environmentLabel(connection.environment) : "",
    protocolConfig: {
      ...connection.protocolConfig,
      verificationMethod: connection.protocolConfig.verificationMethod || "GET",
      expectedStatus: connection.protocolConfig.expectedStatus || "200-299",
      commonHeaders: { ...connection.protocolConfig.commonHeaders },
    },
    authConfig,
  };
  connectionFormSnapshot.value = snapshotConnection(draftConnection.value);
  connectionFormMode.value = "edit";
  connectionCurrentView.value = "form";
  actionNote.value = "";
  resetConnectionFormUI();
  verificationSectionOpen.value = connectionHasCustomVerification(draftConnection.value);
  advancedSectionOpen.value = connectionHasAdvancedConfig(draftConnection.value);
  focusConnectionName();
}

function requestCloseConnectionForm(_reason: ConnectionCloseReason) {
  if (connectionFormDirty.value) {
    discardDialogVisible.value = true;
    warnUnsavedConnectionForm();
    return;
  }
  closeConnectionForm();
}

function closeConnectionForm() {
  closeConnectionDropdowns();
  discardDialogVisible.value = false;
  connectionCurrentView.value = detailConnectionId.value ? "detail" : "list";
}

function keepEditingConnectionForm() {
  discardDialogVisible.value = false;
}

function discardConnectionFormChanges() {
  discardDialogVisible.value = false;
  closeConnectionForm();
}

function promoteSavedConnectionToEdit(saved: ServiceConnection, status: ServiceConnection["status"] = saved.status) {
  draftConnection.value = {
    ...saved,
    status,
    protocolConfig: {
      ...saved.protocolConfig,
      commonHeaders: { ...saved.protocolConfig.commonHeaders },
    },
    authConfig: { ...saved.authConfig },
    outboundIdentity: saved.outboundIdentity ? { ...saved.outboundIdentity } : undefined,
    outboundMode: saved.outboundMode,
    migrationState: saved.migrationState,
    machineCredentialConfigured: saved.machineCredentialConfigured,
  };
  connectionFormMode.value = "edit";
  detailConnectionId.value = saved.id;
  connectionFormSnapshot.value = snapshotConnection(draftConnection.value);
}

async function persistConnectionDraft(wasCreate: boolean) {
  const credentialPlaintext = usesDualModeForm.value
    ? ""
    : selectedCredentialField.value
      ? connectionCredentialPlaintext()
      : "";
  const machinePem = usesDualModeForm.value && draftConnection.value.outboundMode === "BROKER_OBO"
    ? machineCredentialPlaintext()
    : "";
  const options: { machineCredentialPlaintext?: string; impactConfirmationProof?: string } = {};
  if (machinePem) options.machineCredentialPlaintext = machinePem;
  if (impactProof.value) options.impactConfirmationProof = impactProof.value;
  const saved = wasCreate
    ? await integration.createServiceConnection(draftConnection.value, credentialPlaintext, options)
    : await integration.updateServiceConnection(draftConnection.value.id, draftConnection.value, credentialPlaintext, options);
  clearConnectionCredentialInput();
  clearMachineCredentialInput();
  impactProof.value = "";
  impactPreview.value = null;
  return saved;
}

async function saveConnection() {
  if (formSubmitting.value) return;
  if (!validateConnectionForm("draft")) {
    focusFirstConnectionFormError();
    return;
  }
  savingConnection.value = true;
  try {
    const wasCreate = connectionFormMode.value === "create";
    let saved: ServiceConnection;
    try {
      saved = await persistConnectionDraft(wasCreate);
    } catch (error) {
      const message = error instanceof Error && error.message ? error.message : "保存失败，请稍后重试。";
      showActionNote(`未能保存连接：${message}`, "error");
      return;
    }
    promoteSavedConnectionToEdit(saved);
    syncConnectionVerifiedState(saved.id, saved.status);
    let refreshError = "";
    try {
      await reloadConnectionData({ page: wasCreate ? 1 : integration.serviceConnectionPagination.page });
    } catch (error) {
      const message = error instanceof Error && error.message ? error.message : "请稍后重试。";
      refreshError = `列表刷新失败：${message}`;
    }
    const savedMessage = wasCreate
      ? `${saved.name} 已保存，稍后需要验证后才能被 Tool 稳定使用。`
      : `${saved.name} 已仅保存，验证状态已重置。`;
    showActionNote(refreshError ? `${savedMessage} ${refreshError}` : savedMessage, wasCreate || refreshError ? "warning" : "success");
    closeConnectionForm();
  } finally {
    savingConnection.value = false;
  }
}

async function verifyConnection(connection: ServiceConnection) {
  if (!connection.id || isConnectionVerifying(connection.id)) return;
  addVerifyingConnection(connection.id);
  try {
    const verification = await integration.verifyConnection(connection.id);
    await reloadConnectionData();
    const status: ServiceConnection["status"] = verification.status === "SUCCEEDED" ? "VERIFIED" : "ERROR";
    syncConnectionVerifiedState(connection.id, status);
    if (status === "VERIFIED") {
      showActionNote(`${connection.name} 已通过连接验证。`);
      return;
    }
    showActionNote(`${connection.name} 验证后仍需处理：${verification.diagnostics.code || "请检查 Provider、认证和凭据配置。"}`, "warning");
  } finally {
    removeVerifyingConnection(connection.id);
  }
}

async function saveAndVerifyConnection() {
  if (formSubmitting.value) return;
  if (!validateConnectionForm("verify")) {
    focusFirstConnectionFormError();
    return;
  }
  savingAndVerifyingConnection.value = true;
  formSubmitError.value = "";
  connectionVerificationPhase.value = "saving";
  formVerificationFeedback.value = null;
  verificationSectionOpen.value = true;
  const wasCreate = connectionFormMode.value === "create";
  try {
    let saved: ServiceConnection;
    try {
      saved = await persistConnectionDraft(wasCreate);
    } catch (error) {
      const message = error instanceof Error && error.message ? error.message : "保存失败，请稍后重试。";
      connectionVerificationPhase.value = "saveFailed";
      formSubmitError.value = message;
      showActionNote(`未能保存连接：${message}`, "error");
      return;
    }
    promoteSavedConnectionToEdit(saved);
    connectionVerificationPhase.value = "verifying";
    let verification: ServiceConnectionVerification;
    try {
      verification = await integration.verifyConnection(saved.id);
    } catch (error) {
      const message = error instanceof Error && error.message ? error.message : "请稍后重试。";
      connectionVerificationPhase.value = "verificationFailed";
      formSubmitError.value = `连接已保存，但验证请求失败：${message}`;
      showActionNote(formSubmitError.value, "error");
      return;
    }
    formVerificationFeedback.value = verification;
    const status: ServiceConnection["status"] = verification.status === "SUCCEEDED" ? "VERIFIED" : "ERROR";
    syncConnectionVerifiedState(saved.id, status);
    promoteSavedConnectionToEdit(saved, status);
    connectionVerificationPhase.value = status === "VERIFIED" ? "passed" : "verificationFailed";
    let refreshError = "";
    try {
      await reloadConnectionData({ page: wasCreate ? 1 : integration.serviceConnectionPagination.page });
    } catch (error) {
      const message = error instanceof Error && error.message ? error.message : "请稍后重试。";
      refreshError = `列表刷新失败：${message}`;
    }
    if (status === "VERIFIED") {
      showActionNote(
        refreshError ? `${saved.name} 已保存并通过验证，但${refreshError}` : `${saved.name} 已保存并通过验证。`,
        refreshError ? "warning" : "success",
      );
      closeConnectionForm();
      return;
    }
    formSubmitError.value = verification.diagnostics.code || "请检查连接配置。";
    showActionNote(
      refreshError ? `${saved.name} 已保存，但验证仍需处理；${refreshError}` : `${saved.name} 已保存，但验证仍需处理。`,
      "warning",
    );
    focusFirstVerificationFailure(verification);
  } finally {
    savingAndVerifyingConnection.value = false;
  }
}

function markConnectionVerified(connectionId: string) {
  testedConnectionIds.value = [...new Set([...testedConnectionIds.value, connectionId])];
}

function syncConnectionVerifiedState(connectionId: string, status: ServiceConnection["status"]) {
  if (status === "VERIFIED") {
    markConnectionVerified(connectionId);
    return;
  }
  testedConnectionIds.value = testedConnectionIds.value.filter((testedId) => testedId !== connectionId);
}

function requestRemoveConnection(connection: ServiceConnection) {
  pendingDeleteConnection.value = connection;
  deleteConfirmName.value = "";
  deleteError.value = "";
}

function closeDeleteDialog() {
  pendingDeleteConnection.value = null;
  deleteConfirmName.value = "";
  deleteError.value = "";
  deletingConnection.value = false;
}

function requestCloseDeleteDialog(_reason: "backdrop" | "escape") {
  if (deleteDialogDirty.value) {
    showActionNote("删除确认已输入内容，请点击取消或完成删除。", "warning");
    return;
  }
  closeDeleteDialog();
}

async function confirmRemoveConnection() {
  const connection = pendingDeleteConnection.value;
  if (!connection || deletingConnection.value) return;
  if (!deleteConfirmMatches.value) {
    deleteError.value = "请输入连接名称以确认删除。";
    return;
  }
  deletingConnection.value = true;
  deleteError.value = "";
  try {
    await integration.deleteServiceConnection(connection.id);
    const currentPage = integration.serviceConnectionPagination.page;
    await reloadConnectionData();
    if (!integration.serviceConnectionPageItems.length && currentPage > 1) {
      await loadConnectionPage({ page: currentPage - 1 });
    }
    testedConnectionIds.value = testedConnectionIds.value.filter((connectionId) => connectionId !== connection.id);
    if (detailConnectionId.value === connection.id) {
      detailConnectionId.value = "";
      connectionCurrentView.value = "list";
    }
    showActionNote(`${connection.name} 已删除。`, "warning");
    closeDeleteDialog();
  } catch (error) {
    const message = error instanceof Error && error.message ? error.message : "删除失败，请稍后重试。";
    deleteError.value = message;
    showActionNote(message, "error");
  } finally {
    deletingConnection.value = false;
  }
}

function environmentLabel(environment: string) {
  if (!environment.trim()) return "未选择";
  if (["测试", "Sandbox", "Staging", "TEST", "STAGING", "DEVELOPMENT"].includes(environment)) return "测试";
  return "生产";
}

function normalizeAuthMode(mode: string, label = "") {
  const value = mode.trim();
  if (["oauth2-client", "OAUTH2_CLIENT"].includes(value)) return "oauth2-client";
  if (["api-key-secret", "API_KEY", "API_KEY_HEADER"].includes(value)) return "api-key-secret";
  if (["fixed-token", "BEARER", "BEARER_TOKEN", "FIXED_TOKEN"].includes(value)) return "fixed-token";
  if (["none", "NONE", ""].includes(value)) return "none";
  if (label) return value;
  return value;
}

function authModeLabel(mode: string, fallback = "") {
  const normalizedMode = normalizeAuthMode(mode, fallback);
  const labels: Record<string, string> = {
    "oauth2-client": "OAuth2 Client Credentials",
    "api-key-secret": "API Key",
    "fixed-token": "Bearer Token",
    none: "无需认证",
  };
  return fallback || labels[normalizedMode] || mode || "未选择";
}

function selectRefreshMode(mode: string) {
  draftConnection.value.authConfig.refreshMode = mode;
  closeConnectionDropdownAndRestoreFocus("refreshMode");
}

function authModeInstruction() {
  return selectedAuthScheme.value?.description || "认证协议由 Provider 管理；这里只填写当前环境的账号字段和一次性凭据。";
}

function verificationModeLabel(connectionId: string) {
  const verification = integration.verificationByConnectionId[connectionId];
  if (verification) return `后端验证 · ${verification.latencyMs ?? 0}ms`;
  if (verification) return "后端已返回验证结果";
  return "尚未验证";
}

function verificationSummary(connectionId: string) {
  const verification = integration.verificationByConnectionId[connectionId];
  return verification ? `${verification.diagnostics.category || "UNKNOWN"} · ${verification.diagnostics.code || verification.status}` : "点击验证后会显示后端返回的稳定诊断码。";
}
</script>

<template>
  <div
    class="service-connections-page"
    :class="connectionCurrentView === 'list' ? ['management-page-grid', 'management-page-grid--two-rows'] : []"
    @click="closeConnectionFloatingMenus"
  >
    <template v-if="connectionCurrentView === 'list'">
      <header class="connection-page-header">
        <div>
          <span class="connection-eyebrow">Integration Access</span>
          <h1>服务连接</h1>
          <p>Provider 管理协议与端点，Connection 只管理账号身份、授权范围与 Secret 引用；凭据明文不进入页面状态。</p>
        </div>
        <div class="connection-header-actions">
          <button
            class="ghost-button"
            type="button"
            :disabled="!hasWorkspaceContext"
            :title="hasWorkspaceContext ? '注册 Provider' : '请先创建或加入业务空间'"
            @click.stop="router.push('/providers')"
          >
            <i class="fa-solid fa-server" />
            管理 Provider
          </button>
          <button
            class="primary-button"
            type="button"
            :disabled="!hasWorkspaceContext || !readyProviderCount"
            :title="!hasWorkspaceContext ? '请先创建或加入业务空间' : readyProviderCount ? '新建服务连接' : '请先完成 Provider 的端点与认证契约配置'"
            @click.stop="openCreateConnection"
          >
            <i class="fa-solid fa-circle-plus" />
            新建服务连接
          </button>
        </div>
      </header>

      <section class="connection-reference-table-card management-list-card">
        <WorkspaceContextState
          v-if="!hasWorkspaceContext"
          feature="Provider 与服务连接"
          icon="fa-solid fa-plug-circle-xmark"
          @retry="loadConnections"
        />
        <div
          v-if="hasWorkspaceContext && migrationRequiredCount > 0"
          class="connection-migration-banner"
          role="status"
          data-testid="connection-migration-banner"
        >
          <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
          <div>
            <strong>有 {{ migrationRequiredCount }} 个连接需完成出站身份迁移</strong>
            <p>硬切后旧共享账号连接为 DISABLED + MIGRATION_REQUIRED，不可执行；请 OWNER/ADMIN 打开编辑并选择 Broker/OBO 或请求透传。</p>
          </div>
          <button type="button" class="connection-secondary-button" @click="updateConnectionMigrationFilter('MIGRATION_REQUIRED')">
            只看待迁移
          </button>
        </div>
        <ManagementList
          v-if="hasWorkspaceContext"
          class="connection-management-list"
          :rows="filteredConnectionRows"
          :columns="connectionColumns"
          row-key="id"
          :sticky-left-keys="['name']"
          :sticky-right-keys="['actions']"
          storage-key="actweave:service-connections:columns-v2"
          :selectable="false"
          :loading="connectionListLoading"
          :error="connectionLoadError"
          :has-loaded="connectionsHasLoaded"
          :search="query"
          :pagination="integration.serviceConnectionPagination"
          :sort-by="integration.serviceConnectionListQuery?.sortBy"
          :sort-order="integration.serviceConnectionListQuery?.sortOrder"
          search-placeholder="搜索连接 / 域名 / IP / 策略"
          search-aria-label="搜索服务连接"
          clear-search-aria-label="清除服务连接搜索"
          :reset-disabled="!query && connectionStatusFilter === 'ALL' && connectionMigrationFilter === 'ALL' && connectionModeFilter === 'ALL'"
          @update:search="updateConnectionSearch"
          @reset="resetConnectionFilters"
          @page-change="changeConnectionPage"
          @sort-change="changeConnectionSort"
        >
          <template #filters>
            <div class="connection-management-filters">
              <ManagementSegmentedFilter
                :model-value="connectionStatusFilter"
                :options="connectionStatusOptions"
                ariaLabel="服务连接状态筛选"
                @update:model-value="updateConnectionStatusFilter"
              />
              <ManagementSegmentedFilter
                :model-value="connectionMigrationFilter"
                :options="connectionMigrationOptions"
                ariaLabel="迁移状态筛选"
                @update:model-value="updateConnectionMigrationFilter"
              />
              <ManagementSegmentedFilter
                :model-value="connectionModeFilter"
                :options="connectionModeOptions"
                ariaLabel="身份策略筛选"
                @update:model-value="updateConnectionModeFilter"
              />
            </div>
          </template>

          <template #cell-protocol="{ row: connection }">
            <span class="connection-protocol-pill aw-table-pill">{{ connection.protocol || "HTTP" }}</span>
          </template>

          <template #cell-environment="{ row: connection }">
            <span class="connection-environment-value aw-table-pill" :class="{ test: environmentLabel(connection.environment) === '测试' }">
              {{ environmentLabel(connection.environment) }}
            </span>
          </template>

          <template #cell-name="{ row: connection }">
            <div class="connection-name-cell">
              <div class="connection-table-icon"><i class="fa-solid fa-plug" /></div>
              <div>
                <strong class="aw-table-title" tabindex="0" :title="connection.name" :aria-label="`完整连接名称：${connection.name}`">{{ connection.name }}</strong>
                <span class="aw-table-subtitle">{{ environmentLabel(connection.environment) }} · {{ outboundModeLabel(connection) }}</span>
              </div>
            </div>
          </template>

          <template #cell-outboundMode="{ row: connection }">
            <span
              class="connection-outbound-mode aw-table-pill"
              :class="{
                broker: connection.outboundMode === 'BROKER_OBO',
                passthrough: connection.outboundMode === 'REQUEST_PASSTHROUGH',
                migrate: connection.migrationState === 'MIGRATION_REQUIRED' && !connection.outboundMode,
              }"
              :title="outboundModeLabel(connection)"
            >
              {{ outboundModeLabel(connection) }}
            </span>
          </template>

          <template #cell-migrationState="{ row: connection }">
            <span
              v-if="connection.migrationState === 'MIGRATION_REQUIRED'"
              class="connection-migration-badge"
              data-testid="migration-required-badge"
            >需迁移</span>
            <span v-else class="aw-table-meta">—</span>
          </template>

          <template #cell-address="{ row: connection }">
            <div class="connection-address-cell">
              <div class="connection-table-icon" aria-hidden="true"><i class="fa-solid fa-link" /></div>
              <div class="connection-address-body">
                <div class="connection-address-title-row">
                  <strong
                    class="aw-table-title connection-address-host"
                    tabindex="0"
                    :title="connectionAddress(connection)"
                    :aria-label="`完整服务地址：${connectionAddress(connection)}`"
                  >
                    {{ connectionAddressPrimary(connection).hostPort }}<template v-if="connectionAddressPrimary(connection).basePath">{{ connectionAddressPrimary(connection).basePath }}</template>
                  </strong>
                  <button
                    class="connection-copy-button"
                    type="button"
                    :aria-label="`复制 ${connection.name} 服务地址`"
                    @click.stop="copyConnectionText(connectionAddress(connection), '服务地址')"
                  >
                    <i class="fa-regular fa-copy" />
                  </button>
                </div>
                <span
                  class="aw-table-subtitle connection-address-verify"
                  :title="`${verificationMethodLabel(connection)} ${verificationPathLabel(connection)}`"
                >
                  验证 · {{ verificationMethodLabel(connection) }} {{ verificationPathLabel(connection) }}
                </span>
              </div>
            </div>
          </template>

          <template #cell-status="{ row: connection }">
            <div class="connection-status-stack">
              <span class="connection-status-pill aw-table-pill" :class="statusPillClass(connection)">
                <span class="connection-status-dot" :class="statusDotClass(connection)" />
                {{ statusLabel(connection) }}
              </span>
              <span class="aw-table-meta" :title="lastVerifiedTitle(connection)">{{ lastVerified(connection) }}</span>
            </div>
          </template>

          <template #cell-actions="{ row: connection }">
            <ManagementRowActions
              :menu-actions="connectionMenuActions(connection)"
              menu-label="更多操作"
              @action="handleConnectionRowAction($event, connection)"
            />
          </template>

          <template #card="{ row: connection }">
            <article class="connection-mobile-card">
              <header>
                <div class="connection-name-cell">
                  <div class="connection-table-icon"><i class="fa-solid fa-plug" /></div>
                  <div>
                    <strong :title="connection.name">{{ connection.name }}</strong>
                    <span>{{ environmentLabel(connection.environment) }} · {{ authModeLabel(connection.authConfig.mode, connection.authConfig.label) }}</span>
                  </div>
                </div>
                <button
                  class="connection-mobile-actions-toggle"
                  type="button"
                  :aria-label="`${connection.name}连接操作`"
                  :aria-expanded="mobileConnectionActionMenuId === connection.id"
                  @click.stop="toggleMobileConnectionActions(connection.id)"
                >
                  <i class="fa-solid fa-ellipsis" />
                </button>
              </header>
              <div class="connection-mobile-address">
                <code :title="connectionAddress(connection)">{{ connectionAddress(connection) }}</code>
                <button type="button" :aria-label="`复制 ${connection.name} 服务地址`" @click.stop="copyConnectionText(connectionAddress(connection), '服务地址')">
                  <i class="fa-regular fa-copy" />
                </button>
              </div>
              <dl>
                <div><dt>验证接口</dt><dd>{{ connection.protocolConfig.verificationMethod || "GET" }} {{ verificationPathLabel(connection) }}</dd></div>
                <div><dt>状态</dt><dd><span class="connection-status-pill" :class="statusPillClass(connection)">{{ statusLabel(connection) }}</span></dd></div>
              </dl>
              <div
                v-if="mobileConnectionActionMenuId === connection.id"
                class="connection-mobile-actions-menu"
                role="menu"
                :aria-label="`${connection.name}连接操作`"
              >
                <button type="button" role="menuitem" @click.stop="openMobileConnectionPreview(connection)"><i class="fa-solid fa-eye" />查看详情</button>
                <button type="button" role="menuitem" @click.stop="openMobileConnectionEditor(connection)"><i class="fa-solid fa-pen-to-square" />编辑连接</button>
                <button
                  type="button"
                  role="menuitem"
                  :aria-busy="isConnectionVerifying(connection.id) ? 'true' : 'false'"
                  :disabled="isConnectionVerifying(connection.id)"
                  @click.stop="verifyMobileConnection(connection)"
                >
                  <i :class="isConnectionVerifying(connection.id) ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-vial'" />验证连接
                </button>
                <button class="danger" type="button" role="menuitem" @click.stop="requestMobileRemoveConnection(connection)"><i class="fa-solid fa-trash-can" />删除连接</button>
              </div>
            </article>
          </template>

          <template #error="{ error }">
            <div class="connection-load-error" role="alert">
              <div><i class="fa-solid fa-triangle-exclamation" /></div>
              <h4>服务连接加载失败</h4>
              <p>{{ error }}</p>
              <button type="button" aria-label="重试加载服务连接" @click.stop="retryLoadConnections">重试</button>
            </div>
          </template>

          <template #empty>
            <div class="connection-empty-state">
              <div><i class="fa-solid fa-plug-circle-xmark" /></div>
              <h4>{{ hasConnectionRecords ? "没有匹配连接" : "暂无服务连接" }}</h4>
              <p>{{ hasConnectionRecords ? "调整连接名称、域名/IP 或认证方式关键词" : "先创建服务连接，再让 Tool 引用它配置业务动作。" }}</p>
              <button v-if="hasConnectionRecords" type="button" @click.stop="resetConnectionFilters">重置查询条件</button>
              <button v-else type="button" @click.stop="openCreateConnection">新建服务连接</button>
            </div>
          </template>
        </ManagementList>
      </section>
    </template>

    <section v-else-if="connectionCurrentView === 'detail' && detailConnection" class="connection-detail-page">
      <div class="connection-detail-topbar">
        <div>
          <button class="connection-detail-back" type="button" @click.stop="closeConnectionPreview">
            <i class="fa-solid fa-chevron-left" />
            返回连接列表
          </button>
          <span />
          <small>只读详情</small>
        </div>
        <div>
          <button
            class="connection-secondary-button"
            type="button"
            :aria-busy="isConnectionVerifying(detailConnection.id) ? 'true' : 'false'"
            :disabled="isConnectionVerifying(detailConnection.id)"
            @click.stop="verifyConnection(detailConnection)"
          >
            <i :class="isConnectionVerifying(detailConnection.id) ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-vial'" />
            {{ isConnectionVerifying(detailConnection.id) ? "验证中" : "验证连接" }}
          </button>
          <button class="connection-primary-button compact" type="button" @click.stop="openConnectionEditor(detailConnection)">
            <i class="fa-solid fa-pen" />
            编辑连接
          </button>
        </div>
      </div>

      <section class="connection-detail-hero">
        <div>
          <div class="connection-detail-hero-icon"><i class="fa-solid fa-plug" /></div>
          <div>
            <span class="connection-eyebrow">连接详情</span>
            <h2 tabindex="0" :title="detailConnection.name" :aria-label="`完整连接名称：${detailConnection.name}`">{{ detailConnection.name }}</h2>
            <p>
              <span tabindex="0" :title="connectionAddress(detailConnection)" :aria-label="`完整服务地址：${connectionAddress(detailConnection)}`">{{ connectionAddress(detailConnection) }}</span>
              <button class="connection-copy-button hero-copy" type="button" aria-label="复制详情服务地址" @click.stop="copyConnectionText(connectionAddress(detailConnection), '服务地址')">
                <i class="fa-regular fa-copy" />
              </button>
            </p>
          </div>
        </div>
        <span class="connection-status-pill large" :class="statusPillClass(detailConnection)">
          <span class="connection-status-dot" :class="statusDotClass(detailConnection)" />
          {{ statusLabel(detailConnection) }}
        </span>
      </section>

      <div class="connection-verdict-banner" :class="statusClass(detailConnection)">
        <i :class="statusLabel(detailConnection) === '可用' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
        <div>
          <strong>{{ statusLabel(detailConnection) === "可用" ? "当前连接可被 Tool 使用" : "当前连接需要处理" }}</strong>
          <span>{{ verificationSummary(detailConnection.id) }}</span>
        </div>
      </div>

      <section class="connection-detail-grid">
        <article class="connection-detail-card">
          <header class="connection-detail-card-head">
            <i class="fa-solid fa-link" />
            <strong>服务地址</strong>
            <span>— Tool 调用时实际访问的位置</span>
          </header>
          <div class="connection-detail-facts">
            <span><small>最终访问地址</small><code tabindex="0" :title="connectionAddress(detailConnection)" :aria-label="`完整最终访问地址：${connectionAddress(detailConnection)}`">{{ connectionAddress(detailConnection) }}</code></span>
            <span><small>验证接口</small><code tabindex="0" :title="`${detailConnection.protocolConfig.verificationMethod || 'GET'} ${verificationPathLabel(detailConnection)}`" :aria-label="`完整验证接口：${detailConnection.protocolConfig.verificationMethod || 'GET'} ${verificationPathLabel(detailConnection)}`">{{ detailConnection.protocolConfig.verificationMethod || "GET" }} {{ verificationPathLabel(detailConnection) }}</code></span>
            <span><small>协议</small><b>{{ detailConnection.protocol || "HTTP" }}</b></span>
            <span><small>端口</small><code>{{ connectionPortLabel(detailConnection) }}</code></span>
            <span><small>Base Path</small><code>{{ detailConnection.protocolConfig.basePath || "/" }}</code></span>
            <span><small>期望状态码</small><code>{{ detailConnection.protocolConfig.expectedStatus || "200-299" }}</code></span>
          </div>
        </article>

        <article class="connection-detail-card">
          <header class="connection-detail-card-head">
            <i class="fa-solid fa-key" />
            <strong>认证方式</strong>
          </header>
          <div class="connection-detail-facts">
            <span><small>认证类型</small><b>{{ authModeLabel(detailConnection.authConfig.mode, detailConnection.authConfig.label) }}</b></span>
            <span><small>凭证位置</small><b>{{ credentialPlacementLabel(detailConnection) }}</b></span>
            <span><small>使用环境</small><em>{{ environmentLabel(detailConnection.environment) }}</em></span>
            <span><small>凭证过期后</small><b>{{ refreshModeLabel(detailConnection) }}</b></span>
          </div>
        </article>

        <article class="connection-detail-card">
          <header class="connection-detail-card-head">
            <i class="fa-solid fa-vial-circle-check" />
            <strong>验证结果</strong>
            <span>{{ verificationModeLabel(detailConnection.id) }}</span>
          </header>
          <div class="connection-verification-plan">
              <div
                v-for="check in detailVerificationChecks"
                :key="check.label"
                class="connection-verification-item"
                :class="check.status"
              >
                <div><i :class="check.icon" /></div>
                <span>
                  <b>{{ check.statusLabel }}</b>
                  <small>{{ check.desc }}</small>
                </span>
                <button
                  v-if="check.status === 'failed'"
                  class="connection-inline-action"
                  type="button"
                  @click.stop="verificationCheckAction(detailConnection, check)"
                >
                  {{ check.actionLabel }}
                </button>
                <i
                  v-else
                  :class="check.status === 'passed' ? 'fa-solid fa-circle-check' : 'fa-regular fa-circle'"
                />
              </div>
          </div>
        </article>

      </section>
    </section>

    <section
      v-else-if="connectionCurrentView === 'form'"
      class="connection-form-modal"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="'connection-form-title'"
      @keydown="trapConnectionFormFocus"
    >
      <div class="connection-form-backdrop" @click.stop="requestCloseConnectionForm('backdrop')" />
      <div ref="connectionFormWorkspace" class="connection-form-workspace" @click.stop>
        <header class="connection-form-topbar">
          <div class="connection-form-title-lockup">
            <span class="connection-form-icon" aria-hidden="true">
              <i class="fa-solid fa-link" />
            </span>
            <div>
              <h2 id="connection-form-title">{{ connectionFormTitle }}</h2>
              <p>{{ connectionFormMode === "create" ? "选择 Provider 并填写认证身份；端点由 Provider 统一维护。" : "更新账号、授权与 Secret 引用；端点配置保持只读。" }}</p>
            </div>
          </div>
          <button class="connection-form-close" type="button" aria-label="关闭服务连接表单" :disabled="formSubmitting" @click.stop="requestCloseConnectionForm('cancel')">
            <i class="fa-solid fa-xmark" />
          </button>
        </header>

        <div class="connection-form-body">
          <div class="connection-form-single-column">
            <section class="connection-form-section basic" aria-labelledby="connection-basic-title">
              <header class="connection-section-heading">
                <span class="connection-section-icon" aria-hidden="true">
                  <i class="fa-solid fa-link" />
                </span>
                <div>
                  <h3 id="connection-basic-title">基本信息</h3>
                  <p>选择 Provider 与认证方式，保存后可立即验证 Connection。</p>
                </div>
              </header>
              <div class="connection-field-grid identity">
                <label class="connection-field">
                  <span>连接名称 <b class="connection-required-mark">*</b></span>
                  <input
                    ref="connectionNameInput"
                    v-model="draftConnection.name"
                    placeholder="例如：昆仑平台"
                    :aria-invalid="connectionFormErrors.name ? 'true' : 'false'"
                    :aria-describedby="connectionFormErrors.name ? 'connection-name-error' : undefined"
                    @input="clearConnectionFormError('name')"
                  />
                  <small v-if="connectionFormErrors.name" id="connection-name-error" class="connection-field-error">{{ connectionFormErrors.name }}</small>
                </label>
                <div class="connection-field dropdown" @click.stop>
                  <span>使用环境 <b class="connection-required-mark">*</b></span>
                  <button
                    ref="environmentTrigger"
                    data-testid="connection-environment-trigger"
                    data-dropdown-trigger="environment"
                    class="connection-reference-select"
                    type="button"
                    :aria-invalid="connectionFormErrors.environment ? 'true' : 'false'"
                    :aria-describedby="connectionFormErrors.environment ? 'connection-environment-error' : undefined"
                    :aria-label="`使用环境：${draftEnvironmentLabel}`"
                    aria-haspopup="listbox"
                    :aria-expanded="connectionDropdowns.environment ? 'true' : 'false'"
                    aria-controls="connection-environment-menu"
                    @click="toggleConnectionDropdown('environment')"
                    @keydown="openConnectionDropdownFromKeyboard($event, 'environment')"
                  >
                    <span>{{ draftEnvironmentLabel }}</span>
                    <i class="fa-solid fa-chevron-down" :class="{ open: connectionDropdowns.environment }" />
                  </button>
                  <div v-if="connectionDropdowns.environment" id="connection-environment-menu" class="connection-select-menu" role="listbox">
                    <button
                      v-for="(option, index) in environmentOptions"
                      :key="option.value"
                      class="connection-select-option"
                      :class="{ selected: draftConnection.environment === option.value }"
                      type="button"
                      role="option"
                      tabindex="-1"
                      :aria-selected="draftConnection.environment === option.value ? 'true' : 'false'"
                      @click="selectEnvironment(option.value)"
                      @keydown="handleConnectionOptionKeydown($event, 'environment', index)"
                    >
                      {{ option.label }}
                      <i v-if="draftConnection.environment === option.value" class="fa-solid fa-check" />
                    </button>
                  </div>
                  <small v-if="connectionFormErrors.environment" id="connection-environment-error" class="connection-field-error">{{ connectionFormErrors.environment }}</small>
                </div>
              </div>
              <label class="connection-field select-field">
                <span>服务 API（Capability Provider）<b class="connection-required-mark">*</b></span>
                <AppSelect
                  :model-value="draftConnection.providerId"
                  :options="providerOptions"
                  placeholder="请选择 HTTP OpenAPI Provider"
                  :disabled="connectionFormMode === 'edit'"
                  aria-label="服务 API（Capability Provider）"
                  :aria-required="true"
                  @update:model-value="selectConnectionProvider(String($event))"
                />
                <small class="connection-field-help">Provider 是服务 API 的可复用定义，统一维护端点、协议和发现策略；Connection 只保存某个环境下的账号与凭据。请先通过页面右上角“注册 Provider”创建它，再在这里选择。</small>
              </label>
              <label class="connection-field locked">
                <span>Provider 端点（只读）</span>
                <input ref="serviceAddressInput" :value="draftConnection.protocolConfig.domain || '未配置'" class="mono" disabled readonly />
              </label>

              <!-- Dual-mode outbound identity (UI v0.1): only BROKER_OBO | REQUEST_PASSTHROUGH -->
              <div class="connection-outbound-strategy" data-testid="connection-outbound-strategy">
                <header class="connection-outbound-strategy-head">
                  <strong>出站身份策略 <b class="connection-required-mark">*</b></strong>
                  <p>固定选择 Broker/OBO 或本次请求透传；不可使用共享账号 / NONE / 第三种模式。</p>
                </header>
                <div
                  v-if="isMigrationConnection || draftConnection.migrationState === 'MIGRATION_REQUIRED'"
                  class="connection-auth-contract-warning"
                  role="status"
                  data-testid="connection-migration-wizard-hint"
                >
                  <i class="fa-solid fa-triangle-exclamation" />
                  <div>
                    <strong>迁移向导：DISABLED + MIGRATION_REQUIRED</strong>
                    <p>旧认证只读对照。请选择目标策略、填写配置并保存验证后，迁移态才会清除。不自动推断 mode。</p>
                  </div>
                </div>
                <div v-if="!hasProviderOutboundContract" class="connection-auth-contract-warning" role="alert">
                  <i class="fa-solid fa-circle-exclamation" />
                  <div>
                    <strong>Provider 尚未声明用户态出站契约</strong>
                    <p>请先到「服务 Provider」配置 <code>outboundIdentity.supportedModes</code>（BROKER_OBO / REQUEST_PASSTHROUGH），再创建连接。</p>
                  </div>
                </div>
                <div v-else class="connection-outbound-cards" role="radiogroup" aria-label="出站身份策略">
                  <button
                    v-for="mode in OUTBOUND_MODES"
                    :key="mode"
                    type="button"
                    class="connection-outbound-card"
                    role="radio"
                    :aria-checked="draftOutboundMode === mode ? 'true' : 'false'"
                    :class="{ selected: draftOutboundMode === mode, disabled: !providerSupportedModes.includes(mode) }"
                    :disabled="!providerSupportedModes.includes(mode)"
                    :data-testid="`outbound-mode-${mode}`"
                    @click="selectOutboundMode(mode)"
                  >
                    <strong>{{ outboundModeCardTitle(mode) }}</strong>
                    <small>{{ outboundModeCardHint(mode) }}</small>
                    <span v-if="!providerSupportedModes.includes(mode)" class="connection-outbound-card-disabled">Provider 未支持</span>
                  </button>
                </div>
                <small v-if="connectionFormErrors.outboundMode" class="connection-field-error">{{ connectionFormErrors.outboundMode }}</small>

                <div v-if="switchModePending" class="connection-impact-preview" data-testid="connection-impact-preview" role="dialog" aria-label="确认更改出站策略">
                  <strong>确认更改出站策略</strong>
                  <p v-if="impactLoading">正在加载影响范围…</p>
                  <template v-else>
                    <p>切换后相关执行将使用新策略版本；进行中的临时 Token 将失效。不展示 Secret / Token / Broker body。</p>
                    <p class="connection-impact-stub">影响摘要：已发布 Tool / Agent binding / Workflow Revision（服务端 impact proof 已签发）。</p>
                    <div class="connection-impact-actions">
                      <button type="button" class="connection-secondary-button" @click="cancelModeSwitch">取消</button>
                      <button type="button" class="connection-primary-button" :disabled="!impactProof" @click="confirmModeSwitch">确认更改</button>
                    </div>
                  </template>
                </div>

                <div v-if="draftOutboundMode === 'BROKER_OBO'" class="connection-auth-fields" data-testid="outbound-broker-fields">
                  <p class="connection-field-help">需要 Subject · 配置级验证 · 按用户换 Token。机器凭据仅写配置态，响应不回显 Secret。</p>
                  <div class="connection-field-grid two">
                    <label class="connection-field">
                      <span>clientId <b class="connection-required-mark">*</b></span>
                      <input v-model="brokerClientId" class="mono" data-testid="broker-client-id" autocomplete="off" />
                      <small v-if="connectionFormErrors['broker.clientId']" class="connection-field-error">{{ connectionFormErrors["broker.clientId"] }}</small>
                    </label>
                    <label class="connection-field">
                      <span>scopes</span>
                      <input v-model="brokerScopesText" class="mono" data-testid="broker-scopes" placeholder="space 或逗号分隔" autocomplete="off" />
                    </label>
                  </div>
                  <label class="connection-field">
                    <span>机器凭据（private key PEM） <b v-if="!draftConnection.machineCredentialConfigured && !draftConnection.credentialConfigured" class="connection-required-mark">*</b></span>
                    <input
                      ref="machineCredentialInput"
                      type="password"
                      class="mono"
                      data-testid="broker-machine-credential"
                      autocomplete="new-password"
                      :placeholder="draftConnection.machineCredentialConfigured || draftConnection.credentialConfigured ? '留空保留现有；输入新值将替换' : '一次性机器凭据'"
                    />
                    <small>仅配置态；不保存用户业务 Token。已配置：{{ draftConnection.machineCredentialConfigured || draftConnection.credentialConfigured ? "是" : "否" }}</small>
                    <small v-if="connectionFormErrors['broker.machineCredential']" class="connection-field-error">{{ connectionFormErrors["broker.machineCredential"] }}</small>
                  </label>
                </div>

                <div v-else-if="draftOutboundMode === 'REQUEST_PASSTHROUGH'" class="connection-auth-fields" data-testid="outbound-passthrough-fields">
                  <p class="connection-field-help">需要 Subject · 每次请求需 Token · <strong>不持久化用户业务 Token</strong>。</p>
                  <label class="connection-field">
                    <span>maxResidenceSeconds</span>
                    <input v-model.number="passthroughMaxResidence" type="number" min="1" max="3600" class="mono" data-testid="passthrough-max-residence" />
                    <small>默认 600 秒；Token 仅驻留本次运行内存 Vault。</small>
                  </label>
                </div>
              </div>
            </section>

            <section class="connection-form-section connection-verification-section">
              <button data-testid="connection-verification-toggle" class="connection-disclosure-trigger" type="button" :aria-expanded="verificationSectionOpen ? 'true' : 'false'" aria-controls="connection-verification-fields" @click="toggleVerificationSection">
                <span class="connection-disclosure-copy">
                  <span class="connection-disclosure-icon verification" aria-hidden="true">
                    <i class="fa-solid fa-vial-circle-check" />
                  </span>
                  <span>
                    <strong>连接验证（推荐）</strong>
                    <small data-testid="verification-path-summary">{{ verificationPathDisplay }}</small>
                  </span>
                </span>
                <i class="fa-solid fa-chevron-down" :class="{ open: verificationSectionOpen }" />
              </button>
              <div v-if="verificationSectionOpen" id="connection-verification-fields" class="connection-disclosure-body">
                <div class="connection-field-grid two">
                  <div class="connection-field dropdown" @click.stop>
                    <span>验证方法</span>
                    <button
                      data-dropdown-trigger="verificationMethod"
                      class="connection-reference-select mono"
                      type="button"
                      disabled
                      :aria-label="`验证方法：${draftConnection.protocolConfig.verificationMethod || 'GET'}`"
                      aria-haspopup="listbox"
                      :aria-expanded="connectionDropdowns.verificationMethod ? 'true' : 'false'"
                      aria-controls="connection-verification-method-menu"
                      @click="toggleConnectionDropdown('verificationMethod')"
                      @keydown="openConnectionDropdownFromKeyboard($event, 'verificationMethod')"
                    >
                      <span>{{ draftConnection.protocolConfig.verificationMethod || "GET" }}</span>
                      <i class="fa-solid fa-chevron-down" :class="{ open: connectionDropdowns.verificationMethod }" />
                    </button>
                    <div v-if="connectionDropdowns.verificationMethod" id="connection-verification-method-menu" class="connection-select-menu" role="listbox">
                      <button
                        v-for="(option, index) in verificationMethodOptions"
                        :key="option.value"
                        class="connection-select-option mono"
                        :class="{ selected: draftConnection.protocolConfig.verificationMethod === option.value }"
                        type="button"
                        role="option"
                        tabindex="-1"
                        :aria-selected="draftConnection.protocolConfig.verificationMethod === option.value ? 'true' : 'false'"
                        @click="selectVerificationMethod(option.value)"
                        @keydown="handleConnectionOptionKeydown($event, 'verificationMethod', index)"
                      >
                        {{ option.label }}
                        <i v-if="draftConnection.protocolConfig.verificationMethod === option.value" class="fa-solid fa-check" />
                      </button>
                    </div>
                  </div>
                  <label class="connection-field"><span>验证路径（Provider 只读）</span><input :value="draftConnection.protocolConfig.verificationPath" class="mono" disabled readonly /></label>
                  <label class="connection-field"><span>期望状态码（Provider 只读）</span><input :value="draftConnection.protocolConfig.expectedStatus" class="mono" disabled readonly /></label>
                  <label class="connection-field"><span>响应包含（Provider 只读）</span><input :value="draftConnection.protocolConfig.expectedResponseContains" class="mono" disabled readonly /></label>
                </div>
                <div class="connection-address-preview"><i class="fa-solid fa-vial" /><span><small>实际验证请求</small><b>{{ draftConnection.protocolConfig.verificationMethod || "GET" }} {{ draftConnectionVerificationPreview }}</b></span></div>
                <div
                  v-if="connectionVerificationPhase !== 'idle'"
                  data-testid="connection-form-verification-result"
                  class="connection-form-verification-result"
                  :class="{
                    pending: connectionVerificationPhase === 'saving' || connectionVerificationPhase === 'verifying',
                    passed: connectionVerificationPhase === 'passed',
                    failed: connectionVerificationPhase === 'saveFailed' || connectionVerificationPhase === 'verificationFailed',
                  }"
                  role="status"
                  aria-live="polite"
                >
                  <strong>{{ formVerificationResultTitle }}</strong>
                  <p v-if="formSubmitError">{{ formSubmitError }}</p>
                  <div v-if="formVerificationChecks.length" class="connection-form-checks">
                    <span v-for="check in formVerificationChecks" :key="check.label" :class="{ passed: check.passed, failed: !check.passed }">
                      <i :class="check.passed ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
                      <b>{{ check.label }}</b>
                      <small>{{ check.desc }}</small>
                    </span>
                  </div>
                </div>
              </div>
            </section>

            <section class="connection-form-section connection-advanced-section">
              <button data-testid="connection-advanced-toggle" class="connection-disclosure-trigger" type="button" :aria-expanded="advancedSectionOpen ? 'true' : 'false'" aria-controls="connection-advanced-fields" @click="toggleAdvancedSection">
                <span class="connection-disclosure-copy">
                  <span class="connection-disclosure-icon advanced" aria-hidden="true">
                    <i class="fa-solid fa-sliders" />
                  </span>
                  <span>
                    <strong>高级设置</strong>
                    <small>Provider 运行端点与认证执行摘要（只读）</small>
                  </span>
                </span>
                <i class="fa-solid fa-chevron-down" :class="{ open: advancedSectionOpen }" />
              </button>
              <div v-if="advancedSectionOpen" id="connection-advanced-fields" class="connection-disclosure-body">
                <div class="connection-field-grid two">
                  <label class="connection-field"><span>端口（Provider 只读）</span><input :value="draftConnection.protocolConfig.port" class="mono" disabled readonly /></label>
                  <label class="connection-field"><span>Base Path（Provider 只读）</span><input :value="draftConnection.protocolConfig.basePath" class="mono" disabled readonly /></label>
                </div>
                <div v-if="showsTokenFieldPaths" class="connection-field-grid two">
                  <label class="connection-field"><span>访问凭证字段</span><input v-model="draftConnection.authConfig.accessTokenPath" class="mono" placeholder="access_token / token" /></label>
                  <label class="connection-field"><span>续期凭证字段</span><input v-model="draftConnection.authConfig.refreshTokenPath" class="mono" placeholder="refresh_token" /></label>
                  <label class="connection-field"><span>有效期字段</span><input v-model="draftConnection.authConfig.expiresPath" class="mono" placeholder="expires_in / expires_at" /></label>
                </div>
                <div v-if="needsRefreshConfig" class="connection-field dropdown" @click.stop>
                  <span>凭证过期后</span>
                  <button
                    data-dropdown-trigger="refreshMode"
                    class="connection-reference-select"
                    type="button"
                    :aria-label="`凭证过期后：${computedRefreshModeLabel}`"
                    aria-haspopup="listbox"
                    :aria-expanded="connectionDropdowns.refreshMode ? 'true' : 'false'"
                    aria-controls="connection-refresh-mode-menu"
                    @click="toggleConnectionDropdown('refreshMode')"
                    @keydown="openConnectionDropdownFromKeyboard($event, 'refreshMode')"
                  >
                    <span>{{ computedRefreshModeLabel }}</span>
                    <i class="fa-solid fa-chevron-down" :class="{ open: connectionDropdowns.refreshMode }" />
                  </button>
                  <div v-if="connectionDropdowns.refreshMode" id="connection-refresh-mode-menu" class="connection-select-menu" role="listbox">
                    <button
                      v-for="(option, index) in refreshModeOptions"
                      :key="option.value"
                      class="connection-select-option"
                      :class="{ selected: draftConnection.authConfig.refreshMode === option.value }"
                      type="button"
                      role="option"
                      tabindex="-1"
                      :aria-selected="draftConnection.authConfig.refreshMode === option.value ? 'true' : 'false'"
                      @click="selectRefreshMode(option.value)"
                      @keydown="handleConnectionOptionKeydown($event, 'refreshMode', index)"
                    >
                      {{ option.label }}
                      <i v-if="draftConnection.authConfig.refreshMode === option.value" class="fa-solid fa-check" />
                    </button>
                  </div>
                </div>
                <label v-if="draftConnection.authConfig.refreshMode === 'dedicated'" class="connection-field"><span>单独 Refresh Token 接口</span><input v-model="draftConnection.authConfig.refreshUrl" class="mono" placeholder="与获取访问凭证接口不一致时填写" /></label>
              </div>
            </section>
          </div>
        </div>
        <footer class="connection-form-actions">
          <button data-testid="connection-save-draft" class="connection-secondary-button" type="button" :disabled="formSubmitting" :aria-busy="savingConnection ? 'true' : 'false'" @click.stop="saveConnection"><i v-if="savingConnection" class="fa-solid fa-spinner fa-spin" />{{ saveButtonText }}</button>
          <button data-testid="connection-save-verify" class="connection-primary-button compact" type="button" :disabled="formSubmitting" :aria-busy="savingAndVerifyingConnection ? 'true' : 'false'" @click.stop="saveAndVerifyConnection"><i :class="savingAndVerifyingConnection ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-circle-check'" />{{ saveAndVerifyButtonText }}</button>
        </footer>
      </div>
    </section>

    <section v-if="discardDialogVisible" class="connection-discard-modal" role="dialog" aria-modal="true" aria-labelledby="connection-discard-title">
      <div class="connection-delete-backdrop" @click.stop="keepEditingConnectionForm" />
      <div class="connection-delete-dialog connection-discard-dialog" @click.stop>
        <header>
          <span class="connection-eyebrow">Unsaved Changes</span>
          <h2 id="connection-discard-title">放弃未保存修改？</h2>
          <p>当前服务连接表单已有修改。放弃后，这些内容不会保存，也不会用于后续验证。</p>
        </header>
        <footer>
          <button class="connection-secondary-button" type="button" @click.stop="keepEditingConnectionForm">继续编辑</button>
          <button class="connection-danger-button" type="button" @click.stop="discardConnectionFormChanges">
            <i class="fa-solid fa-trash" />
            放弃修改
          </button>
        </footer>
      </div>
    </section>

    <section v-if="pendingDeleteConnection" class="connection-delete-modal" role="dialog" aria-modal="true" aria-labelledby="connection-delete-title">
      <div class="connection-delete-backdrop" @click.stop="requestCloseDeleteDialog('backdrop')" />
      <div class="connection-delete-dialog" @click.stop>
        <header>
          <span class="connection-eyebrow">Danger Zone</span>
          <h2 id="connection-delete-title">删除服务连接</h2>
          <p>删除后所有引用该 Connection 的 Binding 或默认连接都会被后端完整性规则校验；页面不提交手工引用计数。</p>
        </header>
        <label class="connection-field connection-delete-confirm-input">
          <span>输入连接名称确认</span>
          <input v-model="deleteConfirmName" :placeholder="pendingDeleteConnection.name" />
        </label>
        <p v-if="deleteError" class="connection-delete-error">{{ deleteError }}</p>
        <footer>
          <button class="connection-cancel-button" type="button" :disabled="deletingConnection" @click.stop="closeDeleteDialog">取消</button>
          <button
            class="connection-danger-button"
            type="button"
            :disabled="deletingConnection"
            :aria-busy="deletingConnection ? 'true' : 'false'"
            @click.stop="confirmRemoveConnection"
          >
            <i :class="deletingConnection ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-trash'" />
            删除连接
          </button>
        </footer>
      </div>
    </section>

    <div v-if="actionNote" class="action-toast" :class="actionToastTone" role="status" aria-live="polite">
      <i :class="actionToastTone === 'success' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
      <span>{{ actionNote }}</span>
      <button type="button" aria-label="关闭提示" @click.stop="dismissActionNote">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.service-connections-page {
  min-width: 0;
  min-height: 0;
  color: #1e293b;
  font-family: Inter, "Noto Sans SC", sans-serif;
}

/* Non-list surfaces (detail/form) keep their own page padding; list uses management-page-grid. */
.service-connections-page:not(.management-page-grid) {
  min-height: 100%;
  padding: 24px;
}

.connection-detail-page {
  display: flex;
  flex-direction: column;
  gap: 24px;
  animation: connectionFadeUp 0.2s ease-out both;
}

.connection-page-header,
.connection-detail-topbar,
.connection-form-topbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.connection-page-header h1 {
  margin: 2px 0 0;
  color: #0f172a;
  font-size: 24px;
  font-weight: 700;
  line-height: 32px;
  letter-spacing: 0;
}

.connection-page-header p {
  max-width: 576px;
  margin: 6px 0 0;
  color: #64748b;
  font-size: 12px;
  font-weight: 400;
  line-height: 20px;
}

.connection-eyebrow {
  color: #059669;
  font-size: 10px;
  font-weight: 600;
  line-height: 14px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.connection-primary-button,
.connection-secondary-button,
.connection-cancel-button,
.connection-danger-button {
  border: 0;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
  white-space: nowrap;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.connection-primary-button {
  min-height: 44px;
  padding: 10px 16px;
  background: #020617;
  color: #fff;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
  font-weight: 500;
}

.connection-primary-button i {
  color: #34d399;
}

.connection-primary-button:hover {
  background: #0f172a;
  box-shadow: 0 4px 6px -1px rgba(15, 23, 42, 0.1), 0 2px 4px -2px rgba(15, 23, 42, 0.1);
}

.connection-primary-button:focus-visible,
.connection-secondary-button:focus-visible,
.connection-cancel-button:focus-visible,
.connection-danger-button:focus-visible,
.connection-detail-back:focus-visible,
.connection-select-option:focus-visible,
.connection-empty-state button:focus-visible,
.action-toast button:focus-visible {
  outline: 2px solid rgba(16, 185, 129, 0.75);
  outline-offset: 2px;
  box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.16);
}

.connection-primary-button:active {
  transform: scale(0.98);
}

.connection-primary-button.compact,
.connection-secondary-button,
.connection-cancel-button,
.connection-danger-button {
  min-height: 44px;
  padding: 8px 14px;
}

.connection-secondary-button {
  border: 1px solid #e2e8f0;
  background: #fff;
  color: #334155;
}

.connection-secondary-button i {
  color: #10b981;
}

.connection-secondary-button:hover,
.connection-cancel-button:hover {
  background: #f8fafc;
}

.connection-cancel-button {
  background: transparent;
  color: #334155;
}

.connection-primary-button:disabled,
.connection-secondary-button:disabled,
.connection-cancel-button:disabled,
.connection-danger-button:disabled {
  cursor: not-allowed;
  opacity: 0.62;
  transform: none;
}

.connection-danger-button {
  background: #e11d48;
  color: #fff;
}

.connection-danger-button:hover:not(:disabled) {
  background: #be123c;
}

.connection-reference-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 20px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 10px 30px -10px rgba(0, 0, 0, 0.04), 0 1px 3px rgba(0, 0, 0, 0.02);
}

.connection-reference-banner > div {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.connection-reference-banner i {
  margin-top: 2px;
  color: #94a3b8;
  font-size: 14px;
}

.connection-reference-banner p {
  margin: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 20px;
}

/* Transparent shell — ManagementList owns table card chrome (matches 业务空间). */
.connection-reference-table-card.management-list-card {
  min-width: 0;
  min-height: 0;
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.connection-detail-card:hover {
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.08);
}

.connection-header-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.connection-management-list :deep(.management-list-filters) {
  justify-content: flex-end;
}

.connection-management-filters {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  justify-content: flex-end;
  gap: 16px;
}

.connection-management-list :deep(.data-table td[data-column-key="tools"]),
.connection-management-list :deep(.data-table td[data-column-key="status"]) {
  text-align: center;
}

.connection-mobile-card {
  position: relative;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  padding: 16px;
}

.connection-mobile-card > header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.connection-mobile-card .connection-name-cell {
  min-width: 0;
}

.connection-mobile-card .connection-name-cell strong {
  max-width: min(210px, 52vw);
}

.connection-mobile-actions-toggle,
.connection-mobile-address button,
.connection-mobile-actions-menu button {
  border: 0;
  font: inherit;
}

.connection-mobile-actions-toggle,
.connection-mobile-address button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  flex: 0 0 44px;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
}

.connection-mobile-actions-toggle:hover,
.connection-mobile-address button:hover {
  background: #f1f5f9;
  color: #334155;
}

.connection-mobile-address {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 4px;
  margin-top: 14px;
  border: 1px solid #f1f5f9;
  border-radius: 8px;
  background: #f8fafc;
  padding-left: 12px;
}

.connection-mobile-address code {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: #475569;
  font-family: "Fira Code", monospace;
  font-size: 11px;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-mobile-card dl {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 14px 0 0;
}

.connection-mobile-card dl > div {
  min-width: 0;
  border: 1px solid #f1f5f9;
  border-radius: 8px;
  background: #fff;
  padding: 10px;
}

.connection-mobile-card dt {
  color: #94a3b8;
  font-size: 10px;
  line-height: 14px;
}

.connection-mobile-card dd {
  min-width: 0;
  overflow: hidden;
  margin: 4px 0 0;
  color: #334155;
  font-size: 11px;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-mobile-actions-menu {
  position: absolute;
  top: 60px;
  right: 16px;
  z-index: 20;
  display: grid;
  min-width: 180px;
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 16px 32px rgba(15, 23, 42, 0.16);
  padding: 6px;
}

.connection-mobile-actions-menu button {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: 10px;
  border-radius: 6px;
  background: transparent;
  color: #334155;
  padding: 10px 12px;
  text-align: left;
}

.connection-mobile-actions-menu button:hover:not(:disabled) {
  background: #f8fafc;
}

.connection-mobile-actions-menu button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.connection-mobile-actions-menu button.danger {
  color: #be123c;
}

.connection-load-error {
  display: grid;
  min-height: 320px;
  place-content: center;
  padding: 48px 20px;
  text-align: center;
}

.connection-load-error > div {
  display: flex;
  width: 48px;
  height: 48px;
  align-items: center;
  justify-content: center;
  margin: 0 auto 12px;
  border-radius: 999px;
  background: #fff1f2;
  color: #e11d48;
  font-size: 20px;
}

.connection-load-error h4 {
  margin: 0;
  color: #334155;
  font-size: 14px;
  line-height: 20px;
}

.connection-load-error p {
  margin: 4px 0 0;
  color: #be123c;
  font-size: 11px;
  line-height: 16px;
}

.connection-load-error button {
  min-height: 44px;
  margin-top: 16px;
  border: 0;
  border-radius: 8px;
  background: #020617;
  color: #fff;
  padding: 8px 16px;
  font-size: 12px;
  font-weight: 600;
}

.connection-name-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 12px;
  overflow: hidden;
}

.connection-name-cell > div:last-child {
  display: block;
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
}

.connection-table-icon,
.connection-detail-hero-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 1px solid rgba(167, 243, 208, 0.8);
  background: rgba(236, 253, 245, 0.6);
  color: #059669;
}

.connection-table-icon {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  font-size: 12px;
}

.connection-name-cell strong,
.connection-name-cell .aw-table-title {
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

.connection-name-cell strong:focus-visible,
.connection-address-cell code:focus-visible,
.connection-detail-hero h2:focus-visible,
.connection-detail-hero p span:focus-visible,
.connection-detail-facts code:focus-visible {
  outline: 2px solid rgba(16, 185, 129, 0.75);
  outline-offset: 2px;
  border-radius: 4px;
}

.connection-name-cell span,
.connection-name-cell .aw-table-subtitle {
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

.connection-address-preview b,
.connection-detail-hero p,
.connection-detail-facts code,
.connection-summary-title small,
.connection-field input.mono,
.connection-reference-select.mono,
.connection-select-option.mono {
  font-family: "Fira Code", monospace;
}

/* Mirror connection-name-cell: icon + title + subtitle. */
.connection-address-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 12px;
  overflow: hidden;
}

.connection-address-body {
  display: block;
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
}

.connection-address-title-row {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 4px;
}

.connection-address-host {
  display: block;
  min-width: 0;
  flex: 0 1 auto;
  max-width: 100%;
  overflow: hidden;
  color: var(--aw-table-title-color, #111827);
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: var(--aw-table-title-size, 0.8125rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-address-host:focus-visible {
  border-radius: 4px;
  outline: 0;
  box-shadow: 0 0 0 2px rgba(15, 159, 110, 0.28);
}

.connection-address-title-row .connection-copy-button {
  flex: 0 0 auto;
  opacity: 0.55;
}

.connection-address-cell:hover .connection-copy-button,
.connection-address-cell:focus-within .connection-copy-button {
  opacity: 1;
}

.connection-copy-button {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 28px;
  color: #94a3b8;
  background: transparent;
  border: 0;
  border-radius: 8px;
  cursor: pointer;
  font-size: 12px;
}

.connection-copy-button:hover,
.connection-copy-button:focus-visible {
  color: #047857;
  background: #ecfdf5;
  opacity: 1;
}

.connection-address-verify {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  margin-top: 2px;
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.75rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-tool-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 28px;
  border: 1px solid #f1f5f9;
  border-radius: 999px;
  background: #f8fafc;
  color: #334155;
  padding: 4px 10px;
  font-family: "Fira Code", monospace;
  font-size: 11px;
  font-weight: 700;
  line-height: 16px;
}

.connection-status-stack {
  display: flex;
  min-width: 0;
  max-width: 100%;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  overflow: hidden;
}

.connection-status-stack > span:last-child,
.connection-status-stack .aw-table-meta {
  max-width: 100%;
  overflow: hidden;
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.75rem);
  font-weight: var(--aw-table-meta-weight, 400);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-radius: 999px;
  padding: 4px 10px;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.25;
  white-space: nowrap;
}

.connection-status-pill.large {
  padding: 6px 12px;
  font-size: var(--aw-table-body-size, 0.9rem);
  line-height: 1.35;
}

.connection-status-pill.available {
  background: rgba(209, 250, 229, 0.7);
  color: #047857;
}

.connection-status-pill.expiring {
  background: rgba(254, 243, 199, 0.8);
  color: #d97706;
}

.connection-status-pill.attention {
  background: rgba(255, 228, 230, 0.8);
  color: #e11d48;
}

.connection-status-dot {
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 999px;
}

.connection-status-dot.available {
  background: #10b981;
  animation: connectionPulse 1.8s ease-in-out infinite;
}

.connection-status-dot.expiring {
  background: #f59e0b;
}

.connection-status-dot.attention {
  background: #f43f5e;
}

.connection-protocol-pill,
.connection-environment-value,
.connection-auth-mode {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-protocol-pill {
  min-height: 26px;
  padding: 4px 8px;
  border: 1px solid #dbeafe;
  border-radius: 6px;
  background: #eff6ff;
  color: #2563eb;
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.25;
}

.connection-environment-value {
  min-height: 26px;
  padding: 4px 8px;
  border-radius: 6px;
  background: #ecfdf5;
  color: #047857;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.25;
}

.connection-environment-value.test {
  background: #f8fafc;
  color: #64748b;
}

.connection-auth-mode {
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-weight: var(--aw-table-meta-weight, 400);
  line-height: 1.35;
}

.connection-empty-state {
  max-width: 320px;
  margin: 0 auto;
  min-height: 320px;
  padding: 72px 0;
  text-align: center;
}

.connection-empty-state > div {
  display: flex;
  width: 48px;
  height: 48px;
  align-items: center;
  justify-content: center;
  margin: 0 auto 12px;
  border-radius: 999px;
  background: #f1f5f9;
  color: #cbd5e1;
  font-size: 20px;
}

.connection-empty-state h4 {
  margin: 0;
  color: #334155;
  font-size: 14px;
  font-weight: 700;
  line-height: 20px;
}

.connection-empty-state p {
  margin: 4px 0 0;
  color: #94a3b8;
  font-size: 11px;
  line-height: 16px;
}

.connection-empty-state button {
  margin-top: 16px;
  border: 0;
  border-radius: 8px;
  background: #020617;
  color: #fff;
  min-height: 44px;
  padding: 8px 16px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.connection-detail-topbar {
  align-items: center;
  padding-bottom: 16px;
  border-bottom: 1px solid #e2e8f0;
}

.connection-detail-topbar > div {
  display: flex;
  align-items: center;
  gap: 12px;
}

.connection-detail-topbar small {
  color: #94a3b8;
  font-size: 12px;
  line-height: 16px;
}

.connection-detail-topbar span {
  width: 1px;
  height: 16px;
  background: #cbd5e1;
}

.connection-detail-back {
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 44px;
  padding: 10px 12px;
  font-size: 12px;
  font-weight: 700;
  line-height: 16px;
  cursor: pointer;
}

.connection-detail-back i {
  font-size: 10px;
}

.connection-detail-back:hover {
  background: #f1f5f9;
  color: #1e293b;
}

.connection-detail-hero,
.connection-detail-card {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 10px 30px -10px rgba(0, 0, 0, 0.04), 0 1px 3px rgba(0, 0, 0, 0.02);
}

.connection-detail-hero {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 24px;
}

.connection-detail-hero > div {
  display: flex;
  align-items: center;
  gap: 16px;
  min-width: 0;
}

.connection-detail-hero-icon {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  background: #ecfdf5;
  font-size: 18px;
}

.connection-detail-hero h2 {
  margin: 2px 0 0;
  color: #0f172a;
  font-size: 20px;
  font-weight: 700;
  line-height: 28px;
}

.connection-detail-hero p {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 2px 0 0;
  color: #64748b;
  font-size: 12px;
  line-height: 16px;
}

.connection-detail-hero p span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-copy-button.hero-copy {
  width: 44px;
  height: 44px;
  flex: 0 0 44px;
  margin: -14px 0;
  font-size: 14px;
}

.connection-verdict-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  border: 1px solid;
  border-radius: 12px;
  padding: 16px;
}

.connection-verdict-banner.available {
  border-color: #d1fae5;
  background: #ecfdf5;
  color: #065f46;
}

.connection-verdict-banner.expiring,
.connection-verdict-banner.attention {
  border-color: #fef3c7;
  background: #fffbeb;
  color: #92400e;
}

.connection-verdict-banner > i {
  font-size: 18px;
}

.connection-verdict-banner strong,
.connection-verdict-banner span {
  display: block;
}

.connection-verdict-banner strong {
  font-size: 14px;
  line-height: 20px;
}

.connection-verdict-banner span {
  margin-top: 2px;
  opacity: 0.8;
  font-size: 12px;
  line-height: 16px;
}

.connection-detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 24px;
}

.connection-detail-card {
  overflow: hidden;
  transition: box-shadow 0.15s ease;
}

.connection-detail-card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 46px;
  border-bottom: 1px solid #f1f5f9;
  background: rgba(248, 250, 252, 0.7);
  padding: 14px 20px;
}

.connection-detail-card-head i {
  color: #10b981;
  font-size: 12px;
}

.connection-detail-card-head strong {
  color: #64748b;
  font-family: "Fira Code", monospace;
  font-size: 10px;
  font-weight: 700;
  line-height: 14px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.connection-detail-card-head span {
  color: #94a3b8;
  font-size: 10px;
  line-height: 14px;
}

.connection-detail-facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
  padding: 20px;
}

.connection-detail-facts small {
  display: block;
  margin-bottom: 2px;
  color: #94a3b8;
  font-size: 10px;
  line-height: 14px;
}

.connection-detail-facts b,
.connection-detail-facts code {
  color: #334155;
  font-size: 12px;
  font-weight: 500;
  line-height: 16px;
  word-break: break-word;
}

.connection-detail-facts code {
  font-size: 11px;
}

.connection-detail-facts em {
  display: inline-flex;
  border: 1px solid #f1f5f9;
  border-radius: 999px;
  background: #f8fafc;
  color: #475569;
  padding: 2px 8px;
  font-family: "Fira Code", monospace;
  font-size: 10px;
  font-style: normal;
  line-height: 14px;
}

.connection-tool-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 20px;
}

.connection-tool-chips span {
  border: 1px solid #f1f5f9;
  border-radius: 999px;
  background: #f8fafc;
  color: #475569;
  padding: 4px 10px;
  font-family: "Fira Code", monospace;
  font-size: 10px;
  line-height: 14px;
}

.connection-detail-note {
  margin: 0;
  padding: 20px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 18px;
}

.connection-form-modal {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  background: rgba(15, 23, 42, 0.54);
  backdrop-filter: blur(8px);
}

.connection-form-backdrop {
  position: absolute;
  inset: 0;
}

.connection-form-workspace {
  position: relative;
  display: flex;
  width: min(100%, 760px);
  max-height: calc(100vh - 32px);
  flex-direction: column;
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  padding: 0;
  box-shadow: 0 25px 50px -12px rgba(15, 23, 42, 0.28);
  animation: connectionFadeUp 0.2s ease-out both;
}

.connection-form-topbar {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 20px 24px;
  border-bottom: 1px solid #eef2f7;
  background: #fff;
}

.connection-form-title-lockup {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 14px;
}

.connection-form-title-lockup > div {
  min-width: 0;
}

.connection-form-icon {
  display: inline-flex;
  width: 48px;
  height: 48px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border-radius: 12px;
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
  font-size: 18px;
}

.connection-form-topbar h2 {
  margin: 0;
  color: #0f172a;
  font-size: 20px;
  font-weight: 700;
  line-height: 28px;
}

.connection-form-topbar p {
  margin: 2px 0 0;
  color: #64748b;
  font-size: 13px;
  font-weight: 500;
  line-height: 20px;
}

.connection-form-close {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.connection-form-close:hover,
.connection-form-close:focus-visible {
  background: #f1f5f9;
  color: #0f172a;
}

.connection-form-body {
  min-height: 0;
  flex: 1 1 auto;
  overflow-y: auto;
  padding: 20px 24px;
  background: #f8fafc;
}

.connection-form-single-column {
  display: grid;
  gap: 12px;
}

.connection-form-section {
  overflow: visible;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  padding: 0 18px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.03);
}

.connection-form-section.basic {
  border-color: #eef2f7;
  border-radius: 12px;
  background: #fff;
  padding: 20px;
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.05);
}

.connection-section-heading {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 18px;
}

.connection-section-icon,
.connection-disclosure-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border-radius: 8px;
}

.connection-section-icon {
  width: 34px;
  height: 34px;
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
  font-size: 13px;
}

.connection-section-heading h3,
.connection-disclosure-trigger strong {
  margin: 0;
  color: #0f172a;
  font-size: 14px;
  font-weight: 700;
  line-height: 20px;
}

.connection-section-heading p,
.connection-disclosure-trigger small,
.connection-field-help {
  margin: 3px 0 0;
  color: #64748b;
  font-size: 12px;
  line-height: 18px;
}

.connection-auth-fields {
  display: grid;
  gap: 14px;
  margin-top: 14px;
}

.connection-auth-contract-warning {
  display: flex;
  gap: 12px;
  margin-top: 14px;
  border: 1px solid #fde68a;
  border-radius: 10px;
  background: #fffbeb;
  color: #92400e;
  padding: 13px 14px;
}

.connection-auth-contract-warning p {
  margin: 3px 0 0;
  font-size: 12px;
  line-height: 18px;
}

.schema-driven-auth-fields :deep(.app-select .el-select__wrapper) {
  min-height: 44px;
  border-radius: 8px;
  padding: 0 12px;
}

.connection-provider-auth-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
  padding: 12px;
}

.connection-provider-auth-summary span {
  display: grid;
  gap: 4px;
  min-width: 0;
}

.connection-provider-auth-summary small {
  color: #64748b;
  font-size: 11px;
}

.connection-provider-auth-summary code {
  overflow: hidden;
  color: #334155;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-disclosure-body {
  display: grid;
  gap: 14px;
  margin-top: 0;
  border-top: 1px solid #eef2f7;
  padding: 16px 0 18px;
}

.connection-form-verification-result {
  border: 1px solid #fecaca;
  border-radius: 10px;
  background: #fef2f2;
  color: #991b1b;
  padding: 14px;
}

.connection-form-verification-result.passed {
  border-color: #a7f3d0;
  background: #ecfdf5;
  color: #065f46;
}

.connection-form-verification-result.pending {
  border-color: #bfdbfe;
  background: #eff6ff;
  color: #1e40af;
}

.connection-form-verification-result p {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 18px;
}

.connection-form-checks {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-top: 12px;
}

.connection-form-checks > span {
  display: grid;
  grid-template-columns: 18px 1fr;
  gap: 2px 6px;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.72);
  padding: 10px;
}

.connection-form-checks > span.passed {
  background: #ecfdf5;
  color: #065f46;
}

.connection-form-checks > span.failed {
  background: #fff1f2;
  color: #be123c;
}

.connection-form-checks small {
  grid-column: 2;
  font-size: 11px;
  line-height: 16px;
}

.connection-disclosure-trigger {
  display: flex;
  width: 100%;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border: 0;
  background: transparent;
  color: #0f172a;
  padding: 7px 0;
  text-align: left;
  cursor: pointer;
}

.connection-disclosure-copy {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 11px;
}

.connection-disclosure-copy > span:last-child,
.connection-disclosure-copy small {
  display: block;
  min-width: 0;
}

.connection-disclosure-icon {
  width: 32px;
  height: 32px;
  font-size: 12px;
}

.connection-disclosure-icon.verification {
  background: var(--aw-cyan-soft);
  color: var(--aw-cyan);
}

.connection-disclosure-icon.advanced {
  background: var(--aw-bg);
  color: var(--aw-muted);
}

.connection-disclosure-trigger > i {
  color: #64748b;
  transition: transform 0.2s ease;
}

.connection-disclosure-trigger > i.open {
  transform: rotate(180deg);
}

.connection-field-grid {
  display: grid;
  gap: 14px;
}

.connection-field-grid.identity {
  grid-template-columns: minmax(0, 1fr) 240px;
}

.connection-field-grid.two {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.connection-field-grid.token {
  grid-template-columns: minmax(140px, 0.8fr) minmax(0, 1.4fr) minmax(120px, 0.8fr);
  gap: 12px;
}

.connection-field {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.connection-field > span {
  color: #334155;
  font-family: inherit;
  font-size: 12px;
  font-weight: 700;
  line-height: 18px;
  letter-spacing: 0;
  text-transform: none;
}

.connection-required-mark {
  color: #e11d48;
  font-style: normal;
}

.connection-field input,
.connection-reference-select {
  width: 100%;
  min-height: 44px;
  border: 1px solid #dbe3ec;
  border-radius: 8px;
  background: #f8fafc;
  color: #1e293b;
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
  outline: none;
  transition: background-color 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease;
}

.connection-field input[aria-invalid="true"],
.connection-reference-select[aria-invalid="true"] {
  border-color: #be123c;
  box-shadow: 0 0 0 2px rgba(190, 18, 60, 0.12);
}

.connection-field-error {
  color: #be123c;
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
}

.connection-field input::placeholder {
  color: #94a3b8;
}

.connection-field input:hover,
.connection-reference-select:hover {
  border-color: #cbd5e1;
  background: #fff;
}

.connection-field input:focus,
.connection-reference-select:focus {
  outline: none;
  border-color: rgba(16, 185, 129, 0.6);
  background: #fff;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.15);
}

.connection-reference-select {
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  text-align: left;
}

.connection-reference-select i {
  color: #94a3b8;
  font-size: 10px;
  transition: transform 0.2s ease;
}

.connection-reference-select i.open {
  transform: rotate(180deg);
}

.connection-select-menu {
  position: absolute;
  top: 100%;
  left: 0;
  z-index: 50;
  width: 100%;
  margin-top: 4px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.12), 0 4px 10px -4px rgba(0, 0, 0, 0.06);
  padding: 4px 0;
  animation: connectionFadeUp 0.2s ease-out both;
}

.connection-select-option {
  display: flex;
  width: 100%;
  min-height: 44px;
  align-items: center;
  justify-content: space-between;
  border: 0;
  background: transparent;
  color: #334155;
  padding: 8px 12px;
  font-size: 12px;
  line-height: 16px;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.connection-select-option:hover {
  background: #f8fafc;
}

.connection-select-option.selected {
  background: rgba(236, 253, 245, 0.5);
  color: #059669;
  font-weight: 700;
}

.connection-select-option i {
  color: #10b981;
  font-size: 10px;
}

.credential-pair-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.connection-address-preview {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  padding: 10px 12px;
}

.connection-address-preview i {
  color: #94a3b8;
  font-size: 12px;
}

.connection-address-preview small {
  display: block;
  color: #94a3b8;
  font-size: 10px;
  line-height: 14px;
}

.connection-address-preview b {
  display: block;
  overflow: hidden;
  color: #334155;
  font-size: 12px;
  font-weight: 500;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-form-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin: 0;
  padding: 16px 24px;
  border-top: 1px solid #eef2f7;
  background: #f8fafc;
}

.connection-verification-plan {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 20px;
}

.connection-verification-plan.compact {
  padding: 0;
}

.connection-verification-item {
  display: flex;
  align-items: center;
  gap: 12px;
}

.connection-verification-item > div {
  display: flex;
  width: 28px;
  height: 28px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border-radius: 999px;
  background: #f8fafc;
  color: #cbd5e1;
}

.connection-verification-item.passed > div {
  background: #ecfdf5;
  color: #10b981;
}

.connection-verification-item.failed > div {
  background: #fff1f2;
  color: #e11d48;
}

.connection-verification-item i {
  font-size: 11px;
}

.connection-verification-item > i {
  margin-left: auto;
  color: #e2e8f0;
  font-size: 16px;
}

.connection-verification-item.passed > i {
  color: #10b981;
}

.connection-inline-action {
  margin-left: auto;
  border: 1px solid #fecdd3;
  border-radius: 8px;
  background: #fff1f2;
  color: #be123c;
  min-height: 36px;
  padding: 0 10px;
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
}

.connection-inline-action:hover {
  background: #ffe4e6;
}

.connection-verification-item b,
.connection-verification-item small {
  display: block;
}

.connection-verification-item b {
  color: #334155;
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
}

.connection-verification-item small {
  margin-top: 2px;
  color: #94a3b8;
  font-size: 10px;
  line-height: 14px;
}

.connection-delete-modal,
.connection-discard-modal {
  position: fixed;
  inset: 0;
  z-index: 3100;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}

.connection-delete-backdrop {
  position: absolute;
  inset: 0;
  background: rgba(15, 23, 42, 0.42);
  backdrop-filter: blur(2px);
}

.connection-delete-dialog {
  position: relative;
  width: min(100%, 440px);
  border: 1px solid #fecdd3;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 20px 60px rgba(15, 23, 42, 0.2);
  padding: 24px;
  animation: connectionFadeUp 0.2s ease-out both;
}

.connection-delete-dialog header h2 {
  margin: 4px 0 0;
  color: #0f172a;
  font-size: 18px;
  line-height: 26px;
}

.connection-delete-dialog header p {
  margin: 8px 0 0;
  color: #475569;
  font-size: 12px;
  line-height: 20px;
}

.connection-delete-confirm-input {
  margin-top: 18px;
}

.connection-delete-error {
  margin: 8px 0 0;
  color: #be123c;
  font-size: 12px;
  font-weight: 700;
  line-height: 18px;
}

.connection-delete-dialog footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 20px;
}

.action-toast.warning {
  color: #92400e;
  background: #fffbeb;
  border-color: rgba(217, 119, 6, 0.26);
}

.action-toast.error {
  color: #b91c1c;
  background: #fef2f2;
  border-color: rgba(220, 38, 38, 0.2);
}

.action-toast button {
  width: 44px;
  min-width: 44px;
  height: 44px;
}

@keyframes connectionPulse {
  0%,
  100% {
    opacity: 0.85;
  }
  50% {
    opacity: 0.45;
  }
}

@keyframes connectionFadeUp {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@media (max-width: 1180px) {
  .connection-detail-grid {
    grid-template-columns: 1fr;
  }

  .connection-page-header,
  .connection-reference-banner,
  .connection-detail-hero {
    flex-direction: column;
    align-items: stretch;
  }
}

.connection-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

@media (max-width: 760px) {
  .connection-form-modal {
    padding: 8px;
  }

  .connection-form-workspace {
    max-height: calc(100vh - 16px);
  }

  .connection-form-title-lockup {
    align-items: flex-start;
  }

  .connection-form-icon {
    width: 42px;
    height: 42px;
  }

  .service-connections-page {
    padding: 16px;
  }

  .connection-detail-topbar {
    flex-direction: column;
    align-items: stretch;
  }

  .connection-management-filters {
    align-items: stretch;
    flex-direction: column;
  }

  .connection-form-topbar,
  .connection-form-body,
  .connection-form-actions {
    padding-left: 16px;
    padding-right: 16px;
  }

  .connection-field-grid.identity,
  .connection-field-grid.two,
  .connection-field-grid.token,
  .credential-pair-row,
  .connection-detail-facts {
    grid-template-columns: 1fr;
  }
}

.connection-migration-banner {
  display: flex; gap: 12px; align-items: flex-start; margin: 0 0 12px; padding: 12px 14px;
  border: 1px solid #fbbf24; border-radius: 10px; background: #fffbeb; color: #92400e;
}
.connection-migration-banner > i { margin-top: 2px; color: #d97706; }
.connection-migration-banner strong { display: block; font-size: 13px; }
.connection-migration-banner p { margin: 4px 0 0; font-size: 12px; line-height: 1.4; }
.connection-migration-badge {
  display: inline-flex; padding: 2px 8px; border-radius: 999px; background: #fef3c7; color: #b45309;
  border: 1px solid #fcd34d; font-size: 11px; font-weight: 700;
}
.connection-outbound-mode.aw-table-pill.broker { background: #ecfdf5; color: #047857; border-color: #a7f3d0; }
.connection-outbound-mode.aw-table-pill.passthrough { background: #eff6ff; color: #1d4ed8; border-color: #bfdbfe; }
.connection-outbound-mode.aw-table-pill.migrate { background: #fffbeb; color: #b45309; border-color: #fcd34d; }
.connection-outbound-strategy { margin-top: 8px; padding-top: 12px; border-top: 1px solid #e2e8f0; }
.connection-outbound-strategy-head strong { display: block; margin-bottom: 4px; }
.connection-outbound-strategy-head p { margin: 0 0 10px; color: #64748b; font-size: 12px; }
.connection-outbound-cards { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.connection-outbound-card {
  display: flex; flex-direction: column; gap: 4px; align-items: flex-start;
  padding: 12px; border: 1px solid #cbd5e1; border-radius: 10px; background: #fff; cursor: pointer; text-align: left;
}
.connection-outbound-card.selected { border-color: #2563eb; box-shadow: 0 0 0 1px #2563eb; background: #eff6ff; }
.connection-outbound-card.disabled, .connection-outbound-card:disabled { opacity: 0.55; cursor: not-allowed; }
.connection-outbound-card strong { font-size: 13px; color: #0f172a; }
.connection-outbound-card small { color: #64748b; font-size: 11px; line-height: 1.35; }
.connection-outbound-card-disabled { font-size: 10px; color: #b45309; }
.connection-impact-preview { margin-top: 12px; padding: 12px; border: 1px solid #fcd34d; border-radius: 10px; background: #fffbeb; }
.connection-impact-actions { display: flex; gap: 8px; margin-top: 8px; }

</style>
