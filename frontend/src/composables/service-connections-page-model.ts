/**
 * Service Connections page model (ZKL-64 item 11).
 * Owns page UI state/actions; domain data in providers/connections stores.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import type { ManagementListColumn } from "../components/ManagementList.vue";
import type { ManagementRowAction } from "../components/ManagementRowActions.vue";
import { useConnectionsStore } from "../stores/connections";
import { useProvidersStore } from "../stores/providers";
import { useWorkspaceStore } from "../stores/workspaces";
import { normalizeServiceBaseURL } from "../utils/normalize-service-base-url";
import {
  authModeForScheme,
  connectionAuthValues,
  connectionProviderAuthScheme,
  credentialField,
  isProviderReadyForConnections,
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

/** Page model (list/form/dialog state). Vue entry is useServiceConnectionsPage. */
export function createServiceConnectionsPageModel() {
  type ConnectionStatusFilter = "ALL" | "VERIFIED" | "UNVERIFIED" | "ERROR" | "DISABLED";
  type ConnectionMigrationFilter = "ALL" | "MIGRATION_REQUIRED";
  type ConnectionModeFilter = "ALL" | OutboundIdentityMode;
  type ConnectionView = "list" | "detail" | "form";
  type ConnectionDropdownKey = "environment" | "verificationMethod" | "refreshMode";
  type ActionToastTone = "success" | "warning" | "error";
  type ConnectionCloseReason = "backdrop" | "escape" | "back" | "cancel";
  type VerificationCheckKey = "address" | "credential" | "testCall" | "refresh";
  type VerificationCheckStatus = "passed" | "failed" | "pending";
  type VerificationCheckDefinition = {
    key: VerificationCheckKey;
    label: string;
    desc: string;
    failedLabel: string;
    actionLabel: string;
    icon: string;
  };
  type ConnectionFormFieldKey = string;
  type ConnectionSubmitIntent = "draft" | "verify";
  type ConnectionVerificationPhase = "idle" | "saving" | "saveFailed" | "verifying" | "verificationFailed" | "passed";
  const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
  const OUTBOUND_MODES: OutboundIdentityMode[] = ["BROKER_OBO", "REQUEST_PASSTHROUGH"];

  const connectionsStore = useConnectionsStore();
  const providersStore = useProvidersStore();
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
  const impactPreview = ref<{
    impactConfirmationProof: string;
    machineCredentialWillChange?: boolean;
    expiresAt?: string;
  } | null>(null);
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
  const providerOptions = computed(() =>
    providersStore.providers.map((provider) => ({
      label: isProviderReadyForConnections(provider) ? provider.name : `${provider.name}（待完成配置）`,
      value: provider.id,
      disabled: !isProviderReadyForConnections(provider),
    })),
  );
  const readyProviderCount = computed(
    () => providersStore.providers.filter((provider) => isProviderReadyForConnections(provider)).length,
  );
  const migrationRequiredCount = computed(
    () => connectionsStore.serviceConnections.filter((c) => c.migrationState === "MIGRATION_REQUIRED").length,
  );

  const connectionColumns = computed<ManagementListColumn<ServiceConnection>[]>(() => [
    {
      key: "name",
      label: "连接名称",
      width: 200,
      sortable: true,
      sortKey: "name",
      getValue: (connection) => connection.name,
    },
    {
      key: "protocol",
      label: "协议",
      width: 84,
      align: "center",
      headerAlign: "center",
      hidable: true,
      sortable: true,
      sortKey: "protocol",
      getValue: (connection) => connection.protocol || "HTTP",
    },
    {
      key: "environment",
      label: "环境",
      width: 84,
      align: "center",
      headerAlign: "center",
      hidable: true,
      sortable: true,
      sortKey: "environment",
      getValue: (connection) => environmentLabel(connection.environment),
    },
    {
      key: "address",
      label: "地址 & 验证接口",
      width: 220,
      hidable: true,
      sortable: true,
      sortKey: "address",
      getValue: (connection) => connectionAddress(connection),
    },
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
      sortable: true,
      sortKey: "status",
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

  const hasConnectionRecords = computed(() => connectionsStore.serviceConnectionRegistryTotal > 0);
  const detailConnection = computed(() =>
    connectionsStore.serviceConnections.find((item) => item.id === detailConnectionId.value),
  );
  const selectedConnectionProvider = computed(() =>
    providersStore.providers.find((provider) => provider.id === draftConnection.value.providerId),
  );
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
  const connectionAuthSchemeOptions = computed(() =>
    providerAuthSchemes(selectedConnectionProvider.value).map((scheme) => ({
      label: scheme.displayName,
      value: scheme.key,
    })),
  );
  const selectedPublicAuthFields = computed(() => publicAuthFields(selectedAuthScheme.value));
  const selectedCredentialField = computed(() => credentialField(selectedAuthScheme.value));
  const legacyConnectionAuth = computed(
    () =>
      connectionFormMode.value === "edit" &&
      !draftConnection.value.outboundMode &&
      draftConnection.value.migrationState === "MIGRATION_REQUIRED",
  );
  const usesDualModeForm = computed(
    () =>
      Boolean(draftConnection.value.outboundMode) ||
      connectionFormMode.value === "create" ||
      draftConnection.value.migrationState === "MIGRATION_REQUIRED",
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
      broker.scopes = value
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
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
      draftConnection.value = {
        ...draftConnection.value,
        outboundIdentity: identity,
        outboundMode: "REQUEST_PASSTHROUGH",
      };
    },
  });
  const connectionFormTitle = computed(() => (connectionFormMode.value === "create" ? "新建服务连接" : "编辑服务连接"));
  const formSubmitting = computed(() => savingConnection.value || savingAndVerifyingConnection.value);
  const saveButtonText = computed(() => (savingConnection.value ? "保存中" : "保存草稿"));
  const saveAndVerifyButtonText = computed(() => {
    if (savingAndVerifyingConnection.value) return "验证中";
    return connectionFormMode.value === "create" ? "创建并验证" : "保存并验证";
  });
  const draftConnectionVerificationPreview = computed(
    () => connectionVerificationTarget(draftConnection.value) || "填写服务地址后显示验证接口",
  );
  const verificationPathDisplay = computed(
    () => draftConnection.value.protocolConfig.verificationPath.trim() || "使用服务根地址",
  );
  const authModeHelp = computed(() => authModeInstruction());
  const draftEnvironmentLabel = computed(() =>
    draftConnection.value.environment ? environmentLabel(draftConnection.value.environment) : "请选择使用环境",
  );
  const computedRefreshModeLabel = computed(
    () =>
      refreshModeOptions.find((option) => option.value === draftConnection.value.authConfig.refreshMode)?.label ||
      "重新获取访问凭证",
  );
  const needsRefreshConfig = computed(() => false);
  const showsTokenFieldPaths = computed(() => false);
  const connectionFormDirty = computed(
    () =>
      connectionCurrentView.value === "form" &&
      (credentialInputDirty.value || snapshotConnection(draftConnection.value) !== connectionFormSnapshot.value),
  );
  const deleteDialogDirty = computed(() => Boolean(deleteConfirmName.value.trim()));
  const deleteConfirmMatches = computed(() => deleteConfirmName.value.trim() === pendingDeleteConnection.value?.name);
  const verificationChecks: VerificationCheckDefinition[] = [
    {
      key: "address",
      label: "地址可访问",
      failedLabel: "地址不可访问",
      actionLabel: "编辑服务地址",
      desc: "域名、端口和 Base Path 拼出的地址是否可以访问",
      icon: "fa-solid fa-network-wired",
    },
    {
      key: "credential",
      label: "凭证已配置/已获取",
      failedLabel: "凭证获取失败",
      actionLabel: "编辑认证配置",
      desc: "固定 Token、API Key 或认证接口可以得到可注入的访问凭证",
      icon: "fa-solid fa-key",
    },
    {
      key: "testCall",
      label: "验证接口通过",
      failedLabel: "验证接口失败",
      actionLabel: "编辑验证接口",
      desc: "带上当前凭证请求验证接口，状态码和响应内容符合预期",
      icon: "fa-solid fa-vial",
    },
    {
      key: "refresh",
      label: "续期策略",
      failedLabel: "续期策略未就绪",
      actionLabel: "编辑凭证续期",
      desc: "凭证过期后可以按当前方式处理",
      icon: "fa-solid fa-rotate",
    },
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
      {
        label: "连接验证",
        passed,
        desc: passed
          ? "连接验证通过"
          : [verification.diagnostics.code, verification.diagnostics.detail].filter(Boolean).join(" · ") ||
            "连接验证失败",
      },
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
      await providersStore.loadProviders();
      await Promise.all([loadConnectionPage(), connectionsStore.loadServiceConnectionCatalog()]);
    } catch (error) {
      connectionLoadError.value = error instanceof Error && error.message ? error.message : "加载失败，请稍后重试。";
    } finally {
      connectionListLoading.value = false;
      connectionsHasLoaded.value = true;
    }
  }

  function providerRuntimeAddress(provider?: CapabilityProvider) {
    if (!provider) return "";
    const value =
      provider.endpointConfig.serviceBaseUrl ?? provider.endpointConfig.baseUrl ?? provider.endpointConfig.url;
    return typeof value === "string" ? value : "";
  }

  function providerVerification(provider?: CapabilityProvider) {
    const verification = provider?.endpointConfig.verification;
    return verification && typeof verification === "object" && !Array.isArray(verification)
      ? (verification as Record<string, unknown>)
      : {};
  }

  function selectConnectionProvider(providerId: string) {
    clearConnectionCredentialInput();
    clearMachineCredentialInput();
    draftConnection.value.providerId = providerId;
    const provider = providersStore.providers.find((item) => item.id === providerId);
    if (!provider) return;
    const scheme = providerAuthScheme(provider);
    const verification = providerVerification(provider);
    draftConnection.value.protocolConfig.domain = providerRuntimeAddress(provider);
    draftConnection.value.protocolConfig.verificationMethod =
      typeof verification.method === "string" ? verification.method : "GET";
    draftConnection.value.protocolConfig.verificationPath =
      typeof verification.path === "string" ? verification.path : "";
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
      draftConnection.value.authConfig = {
        ...draftConnection.value.authConfig,
        mode: "",
        label: "",
        schemeKey: "",
        values: {},
      };
      return;
    }
    draftConnection.value.authMode = authModeForScheme(scheme);
    draftConnection.value.authConfig = {
      ...draftConnection.value.authConfig,
      mode: uiModeForScheme(scheme),
      label: scheme.displayName,
      schemeKey: scheme.key,
      values: {},
    };
  }

  function selectConnectionAuthScheme(schemeKey: string) {
    const scheme = providerAuthScheme(selectedConnectionProvider.value, schemeKey);
    if (!scheme) return;
    clearConnectionCredentialInput();
    draftConnection.value.authMode = authModeForScheme(scheme);
    draftConnection.value.authConfig = {
      ...draftConnection.value.authConfig,
      mode: uiModeForScheme(scheme),
      label: scheme.displayName,
      schemeKey: scheme.key,
      values: {},
    };
    connectionFormErrors.value = {};
  }

  function selectedConnectionStatus() {
    return connectionStatusFilter.value === "ALL" ? undefined : connectionStatusFilter.value;
  }

  async function loadConnectionPage(overrides: ServiceConnectionListQuery = {}) {
    return connectionsStore.loadServiceConnectionPage({
      query: query.value.trim(),
      status: selectedConnectionStatus(),
      page: overrides.page ?? connectionsStore.serviceConnectionPagination.page,
      pageSize: overrides.pageSize ?? connectionsStore.serviceConnectionPagination.pageSize,
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
    await Promise.all([loadConnectionPage(overrides), connectionsStore.loadServiceConnectionCatalog()]);
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
    let rows = connectionsStore.serviceConnectionPageItems;
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
      pageSize: connectionsStore.serviceConnectionPagination.pageSize,
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
    const connection = connectionsStore.serviceConnections.find((item) => item.id === connectionId);
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
      container.querySelectorAll<HTMLElement>(
        "button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex='-1'])",
      ),
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

  function verificationCheckStatus(connection: ServiceConnection, _key: VerificationCheckKey): VerificationCheckStatus {
    const verification = connectionsStore.verificationByConnectionId[connection.id];
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
    const provider = providersStore.providers.find((item) => item.id === connection.providerId);
    const scheme = providerAuthScheme(provider, connection.authConfig.schemeKey);
    if (scheme?.oauth2) return `Header · ${scheme.oauth2.injection.headerName}`;
    if (connection.authConfig.credentialPlacement === "query") return "查询参数";
    if (connection.authConfig.mode === "fixed-token" || connection.authConfig.mode === "api-key-secret")
      return "请求 Header";
    return "认证接口返回后注入 Header";
  }

  function refreshModeLabel(connection: ServiceConnection) {
    const provider = providersStore.providers.find((item) => item.id === connection.providerId);
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
      const result = await connectionsStore.previewConnectionImpact(draftConnection.value.id, {
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
    const provider = providersStore.providers.find((item) => isProviderReadyForConnections(item));
    const scheme = providerAuthScheme(provider);
    const verification = providerVerification(provider);
    const supported = providerOutboundSupportedModes(provider);
    const defaultMode: OutboundIdentityMode | undefined =
      supported.length === 1
        ? supported[0]
        : supported.includes("REQUEST_PASSTHROUGH")
          ? "REQUEST_PASSTHROUGH"
          : supported[0];
    const outboundIdentity = defaultMode
      ? defaultMode === "BROKER_OBO"
        ? {
            schemaVersion: "outbound-connection.v1",
            mode: "BROKER_OBO",
            brokerObo: { clientId: "", scopes: [], maxTokenTtlSeconds: 300 },
          }
        : {
            schemaVersion: "outbound-connection.v1",
            mode: "REQUEST_PASSTHROUGH",
            requestPassthrough: { maxResidenceSeconds: 600 },
          }
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
        expectedStatus: Array.isArray(verification.expectedStatuses)
          ? verification.expectedStatuses.join(", ")
          : "200, 204",
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
            if (
              !connection.credentialConfigured &&
              !connection.credentialSecretId?.trim() &&
              !clientSecretInput.value?.value.trim()
            ) {
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
      const dynamicKey = Object.keys(connectionFormErrors.value).find((fieldKey) => fieldKey.startsWith("auth."));
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
      document
        .querySelector<HTMLElement>("#connection-verification-fields input, #connection-verification-fields button")
        ?.focus();
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
    const provider = providersStore.providers.find((item) => item.id === connection.providerId);
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
    const machinePem =
      usesDualModeForm.value && draftConnection.value.outboundMode === "BROKER_OBO" ? machineCredentialPlaintext() : "";
    const options: { machineCredentialPlaintext?: string; impactConfirmationProof?: string } = {};
    if (machinePem) options.machineCredentialPlaintext = machinePem;
    if (impactProof.value) options.impactConfirmationProof = impactProof.value;
    const saved = wasCreate
      ? await connectionsStore.createServiceConnection(draftConnection.value, credentialPlaintext, options)
      : await connectionsStore.updateServiceConnection(
          draftConnection.value.id,
          draftConnection.value,
          credentialPlaintext,
          options,
        );
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
        await reloadConnectionData({ page: wasCreate ? 1 : connectionsStore.serviceConnectionPagination.page });
      } catch (error) {
        const message = error instanceof Error && error.message ? error.message : "请稍后重试。";
        refreshError = `列表刷新失败：${message}`;
      }
      const savedMessage = wasCreate
        ? `${saved.name} 已保存，稍后需要验证后才能被 Tool 稳定使用。`
        : `${saved.name} 已仅保存，验证状态已重置。`;
      showActionNote(
        refreshError ? `${savedMessage} ${refreshError}` : savedMessage,
        wasCreate || refreshError ? "warning" : "success",
      );
      closeConnectionForm();
    } finally {
      savingConnection.value = false;
    }
  }

  async function verifyConnection(connection: ServiceConnection) {
    if (!connection.id || isConnectionVerifying(connection.id)) return;
    addVerifyingConnection(connection.id);
    try {
      const verification = await connectionsStore.verifyConnection(connection.id);
      await reloadConnectionData();
      const status: ServiceConnection["status"] = verification.status === "SUCCEEDED" ? "VERIFIED" : "ERROR";
      syncConnectionVerifiedState(connection.id, status);
      if (status === "VERIFIED") {
        showActionNote(`${connection.name} 已通过连接验证。`);
        return;
      }
      showActionNote(
        `${connection.name} 验证后仍需处理：${formatVerificationFailure(verification)}`,
        "warning",
      );
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
        verification = await connectionsStore.verifyConnection(saved.id);
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
        await reloadConnectionData({ page: wasCreate ? 1 : connectionsStore.serviceConnectionPagination.page });
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
      formSubmitError.value = formatVerificationFailure(verification);
      showActionNote(
        refreshError
          ? `${saved.name} 已保存，但验证仍需处理（${formatVerificationFailure(verification)}）；${refreshError}`
          : `${saved.name} 已保存，但验证仍需处理：${formatVerificationFailure(verification)}`,
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
      await connectionsStore.deleteServiceConnection(connection.id);
      const currentPage = connectionsStore.serviceConnectionPagination.page;
      await reloadConnectionData();
      if (!connectionsStore.serviceConnectionPageItems.length && currentPage > 1) {
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
    return (
      selectedAuthScheme.value?.description || "认证协议由 Provider 管理；这里只填写当前环境的账号字段和一次性凭据。"
    );
  }

  function formatVerificationFailure(verification: {
    diagnostics?: Record<string, string>;
    status?: string;
  }) {
    const code = verification.diagnostics?.code?.trim();
    const detail = verification.diagnostics?.detail?.trim();
    if (code && detail) return `${code}（${detail}）`;
    if (code) return code;
    if (detail) return detail;
    return "请检查 Provider 验证 URL、认证与网络连通性。";
  }

  function verificationModeLabel(connectionId: string) {
    const verification = connectionsStore.verificationByConnectionId[connectionId];
    if (verification) return `后端验证 · ${verification.latencyMs ?? 0}ms`;
    if (verification) return "后端已返回验证结果";
    return "尚未验证";
  }

  function verificationSummary(connectionId: string) {
    const verification = connectionsStore.verificationByConnectionId[connectionId];
    if (!verification) return "点击验证后会显示后端返回的稳定诊断码。";
    if (verification.status === "SUCCEEDED") {
      return `${verification.diagnostics.category || "OK"} · ${verification.diagnostics.code || "CONNECTION_VERIFIED"}`;
    }
    return formatVerificationFailure(verification);
  }

  return {
    UUID_PATTERN,
    OUTBOUND_MODES,
    connectionsStore,
    providersStore,
    router,
    workspaces,
    hasWorkspaceContext,
    query,
    connectionStatusFilter,
    connectionMigrationFilter,
    connectionModeFilter,
    connectionsHasLoaded,
    connectionListLoading,
    connectionLoadError,
    mobileConnectionActionMenuId,
    connectionCurrentView,
    detailConnectionId,
    connectionFormMode,
    draftConnection,
    actionNote,
    actionToastTone,
    testedConnectionIds,
    connectionNameInput,
    verificationSectionOpen,
    advancedSectionOpen,
    connectionFormErrors,
    connectionVerificationPhase,
    formVerificationFeedback,
    formSubmitError,
    serviceAddressInput,
    clientSecretInput,
    machineCredentialInput,
    credentialInputDirty,
    impactProof,
    impactPreview,
    impactLoading,
    switchModePending,
    environmentTrigger,
    connectionFormWorkspace,
    connectionFormSnapshot,
    savingConnection,
    savingAndVerifyingConnection,
    verifyingConnectionIds,
    pendingDeleteConnection,
    deleteConfirmName,
    deleteError,
    deletingConnection,
    discardDialogVisible,
    connectionDropdowns,
    connectionDropdownMenuIds,
    environmentOptions,
    refreshModeOptions,
    verificationMethodOptions,
    connectionStatusOptions,
    connectionMigrationOptions,
    connectionModeOptions,
    providerOptions,
    readyProviderCount,
    migrationRequiredCount,
    connectionColumns,
    hasConnectionRecords,
    detailConnection,
    selectedConnectionProvider,
    providerSupportedModes,
    hasProviderOutboundContract,
    draftOutboundMode,
    isMigrationConnection,
    selectedAuthScheme,
    connectionAuthSchemeOptions,
    selectedPublicAuthFields,
    selectedCredentialField,
    legacyConnectionAuth,
    usesDualModeForm,
    brokerClientId,
    brokerScopesText,
    passthroughMaxResidence,
    connectionFormTitle,
    formSubmitting,
    saveButtonText,
    saveAndVerifyButtonText,
    draftConnectionVerificationPreview,
    verificationPathDisplay,
    authModeHelp,
    draftEnvironmentLabel,
    computedRefreshModeLabel,
    needsRefreshConfig,
    showsTokenFieldPaths,
    connectionFormDirty,
    deleteDialogDirty,
    deleteConfirmMatches,
    verificationChecks,
    detailVerificationChecks,
    formVerificationChecks,
    formVerificationResultTitle,
    filteredConnectionRows,
    snapshotConnection,
    loadConnections,
    providerRuntimeAddress,
    providerVerification,
    selectConnectionProvider,
    selectConnectionAuthScheme,
    selectedConnectionStatus,
    loadConnectionPage,
    requestConnectionPage,
    reloadConnectionData,
    retryLoadConnections,
    resetConnectionFilters,
    updateConnectionSearch,
    updateConnectionStatusFilter,
    updateConnectionMigrationFilter,
    updateConnectionModeFilter,
    changeConnectionSort,
    changeConnectionPage,
    connectionMenuActions,
    handleConnectionRowAction,
    toggleMobileConnectionActions,
    closeMobileConnectionActions,
    closeConnectionFloatingMenus,
    openMobileConnectionPreview,
    openMobileConnectionEditor,
    verifyMobileConnection,
    requestMobileRemoveConnection,
    showActionNote,
    dismissActionNote,
    updateConnectionDetailUrl,
    syncConnectionViewFromLocation,
    copyConnectionText,
    focusConnectionName,
    isConnectionVerifying,
    addVerifyingConnection,
    removeVerifyingConnection,
    warnUnsavedConnectionForm,
    handleGlobalKeydown,
    trapConnectionFormFocus,
    statusLabel,
    supportsCredentialRenewalConfig,
    verificationCheckStatus,
    verificationCheckLabel,
    verificationCheckActionLabel,
    verificationCheckAction,
    statusClass,
    statusPillClass,
    statusDotClass,
    lastVerified,
    lastVerifiedTitle,
    connectionAddress,
    connectionAddressPrimary,
    verificationMethodLabel,
    endpointUrlParts,
    defaultPortForScheme,
    serviceEndpointAddress,
    joinURLPath,
    connectionVerificationTarget,
    verificationPathLabel,
    connectionPortLabel,
    credentialPlacementLabel,
    refreshModeLabel,
    normalizeServiceAddress,
    providerOutboundSupportedModes,
    outboundModeLabel,
    outboundModeCardTitle,
    outboundModeCardHint,
    selectOutboundMode,
    applyOutboundMode,
    loadImpactPreview,
    confirmModeSwitch,
    cancelModeSwitch,
    newConnection,
    resetConnectionFormUI,
    clearConnectionFormError,
    connectionHasCustomVerification,
    connectionHasAdvancedConfig,
    validateConnectionForm,
    machineCredentialPlaintext,
    clearMachineCredentialInput,
    focusFirstConnectionFormError,
    authFieldValue,
    updateAuthFieldValue,
    connectionCredentialPlaintext,
    clearConnectionCredentialInput,
    handleConnectionCredentialInput,
    focusFirstVerificationFailure,
    openCreateConnection,
    openConnectionPreview,
    closeConnectionPreview,
    toggleConnectionDropdown,
    closeConnectionDropdowns,
    closeConnectionDropdownAndRestoreFocus,
    connectionDropdownTrigger,
    connectionDropdownOptions,
    focusConnectionDropdownOption,
    focusSelectedConnectionDropdownOption,
    openConnectionDropdownFromKeyboard,
    handleConnectionOptionKeydown,
    selectEnvironment,
    selectVerificationMethod,
    toggleVerificationSection,
    toggleAdvancedSection,
    openConnectionEditor,
    requestCloseConnectionForm,
    closeConnectionForm,
    keepEditingConnectionForm,
    discardConnectionFormChanges,
    promoteSavedConnectionToEdit,
    persistConnectionDraft,
    saveConnection,
    verifyConnection,
    saveAndVerifyConnection,
    markConnectionVerified,
    syncConnectionVerifiedState,
    requestRemoveConnection,
    closeDeleteDialog,
    requestCloseDeleteDialog,
    confirmRemoveConnection,
    environmentLabel,
    normalizeAuthMode,
    authModeLabel,
    selectRefreshMode,
    authModeInstruction,
    verificationModeLabel,
    verificationSummary,
  };
}
