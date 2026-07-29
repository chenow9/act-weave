/**
 * OpenAPI imports page model (ZKL-64 item 17).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import type { ManagementListColumn } from "../components/ManagementList.vue";
import type { ManagementRowAction } from "../components/ManagementRowActions.vue";
import { useOpenAPIImportsStore } from "../stores/openapiImports";
import { useProvidersStore } from "../stores/providers";
import { useConnectionsStore } from "../stores/connections";
import { normalizeServiceBaseURL } from "../utils/normalize-service-base-url";
import { parseOpenAPIPreview } from "../utils/openapi-preview";
import { buildBodyContractFromRequestParams, buildResponseContractFromFields } from "../utils/tool-schema-json";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  CapabilityProvider,
  OpenAPIImport,
  OpenAPIImportListQuery,
  OpenAPIImportRequest,
  ServiceConnection,
  ToolSchemaNodeType,
  Workspace,
} from "../types/domain";

export function createOpenAPIImportsPageModel() {
  type OpenAPIStatusFilter = "ALL" | "Ready" | "Issues";
  type OpenAPIQuickFilter = "Issues" | "ALL";
  type OpenAPIImportMode = "FILE" | "ONLINE";
  type OpenAPIDropdownKey = "provider" | "connection";

  const openAPIQuickFilterOptions = [
    { label: "待确认", value: "Issues" },
    { label: "全部", value: "ALL" },
  ];

  const openapiImports = useOpenAPIImportsStore();
  const providersStore = useProvidersStore();
  const connectionsStore = useConnectionsStore();

  const router = useRouter();
  const workspaces = useWorkspaceStore();
  const canEditWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "EDIT"),
  );
  const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));

  const query = ref("");
  const openAPIStatusFilter = ref<OpenAPIStatusFilter>("ALL");
  const openAPIQuickFilterValue = computed<OpenAPIQuickFilter | "">(() => {
    if (!query.value && openAPIStatusFilter.value === "Issues") return "Issues";
    if (!query.value && openAPIStatusFilter.value === "ALL") return "ALL";
    return "";
  });
  const importModalVisible = ref(false);
  const importMode = ref<OpenAPIImportMode>("FILE");
  const selectedOpenAPIFile = ref<File | null>(null);
  const selectedOpenAPIFilePreview = ref(parseOpenAPIPreview(""));
  const selectedImportId = ref("");
  /** Detail shell local state (T2=A): open shell first, then load. */
  const detailLoading = ref(false);
  const detailError = ref("");
  /** Accordion key for endpoint schema trees — avoid mounting 100+ vxe tables at once. */
  const expandedEndpointKey = ref("");
  const endpointDetailQuery = ref("");
  const endpointDetailVisibleLimit = ref(40);
  let detailRequestSeq = 0;
  const actionNote = ref("");
  const importingOpenAPI = ref(false);
  const generatingDraftsByImportId = ref<Record<string, boolean>>({});
  const deletingImportId = ref("");
  const pendingDeleteImport = ref<OpenAPIImport | null>(null);
  const openAPIListLoading = ref(true);
  const openAPIListError = ref("");
  const openAPIListHasLoaded = ref(false);
  const mobileImportActionMenuId = ref("");
  const importDialogRef = ref<HTMLElement | null>(null);
  const detailDialogRef = ref<HTMLElement | null>(null);
  const deleteDialogRef = ref<HTMLElement | null>(null);
  const lastModalTrigger = ref<HTMLElement | null>(null);
  const actionNoteTimer = ref<ReturnType<typeof window.setTimeout> | null>(null);
  const openapiDropdowns = ref<Record<OpenAPIDropdownKey, boolean>>({
    provider: false,
    connection: false,
  });
  const importForm = ref<OpenAPIImportRequest>({
    workspaceId: "",
    providerId: "",
    connectionId: "",
  });

  const openAPIImportColumns = computed<ManagementListColumn<OpenAPIImport>[]>(() => [
    {
      key: "file",
      label: "导入文件",
      width: 310,
      sortable: true,
      sortKey: "fileName",
      getValue: (record) => `${record.fileName} ${record.source}`,
    },
    {
      key: "connection",
      label: "服务连接",
      width: 180,
      hidable: true,
      sortable: true,
      sortKey: "connection",
      getValue: (record) =>
        connectionById(record.connectionId || "")?.name || record.connectionId || "Provider 默认连接",
    },
    {
      key: "totalEndpoints",
      label: "接口数",
      width: 96,
      align: "center",
      headerAlign: "center",
      hidable: true,
      sortable: true,
      sortKey: "totalEndpoints",
      getValue: (record) => record.totalEndpoints,
    },
    {
      key: "readyEndpoints",
      label: "可生成",
      width: 96,
      align: "center",
      headerAlign: "center",
      hidable: true,
      sortable: true,
      sortKey: "readyEndpoints",
      getValue: (record) => record.readyEndpoints,
    },
    {
      key: "issues",
      label: "待处理",
      width: 140,
      hidable: true,
      sortable: true,
      sortKey: "issueCount",
      getValue: issueText,
    },
    {
      key: "importTime",
      label: "导入时间",
      width: 132,
      hidable: true,
      sortable: true,
      sortKey: "createdAt",
      getValue: importTime,
    },
    {
      key: "status",
      label: "状态",
      width: 112,
      align: "center",
      headerAlign: "center",
      hidable: true,
      sortable: true,
      sortKey: "status",
      getValue: (record) => record.status,
    },
    { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
  ]);
  const hasImportRecords = computed(() => openapiImports.openAPIImportRegistryTotal > 0);
  const importProviders = computed(() => providersStore.providers || []);
  const selectedWorkspaceOption = computed(() => workspaceById(importForm.value.workspaceId));
  const selectedProviderOption = computed(() =>
    importProviders.value.find((provider) => provider.id === importForm.value.providerId),
  );
  const selectedProviderCanImportOnline = computed(() =>
    Boolean(selectedProviderOption.value && canProviderImportOnline(selectedProviderOption.value)),
  );
  const selectedConnectionOption = computed(() => connectionById(importForm.value.connectionId || ""));
  const selectedImport = computed(() =>
    openapiImports.openAPIImportPageItems.find((record) => record.id === selectedImportId.value),
  );
  const selectedWorkspace = computed(() => workspaceById(selectedImport.value?.workspaceId || ""));
  const selectedConnection = computed(
    () => connectionById(selectedImport.value?.connectionId || "") || connectionsStore.serviceConnections[0],
  );
  const selectedImportDetail = computed(() => {
    const record = selectedImport.value;
    if (!record) return null;
    const detail = record.detail;
    const requestParams = (detail?.requestContract ? [detail.requestContract] : [])
      .flat()
      .filter(Boolean)
      .map((node) => ({
        location: node.location || "Body",
        name: node.name,
        type: node.type,
        required: node.required,
        description: node.description,
        schema: node,
      }));
    return {
      openapiImports,
      providersStore,
      connectionsStore,
      requestTransport: buildBodyContractFromRequestParams(requestParams).transportParams.map((param, index) => ({
        id: `import-request-${index}-${param.location}-${param.name}`,
        location: param.location,
        name: param.name,
        type: (param.type as ToolSchemaNodeType) || "string",
        required: param.required,
        description: param.description,
        children: [],
        item: null,
        additionalProperties: null,
      })),
      requestBodyNodes: buildBodyContractFromRequestParams(requestParams).bodyNodes,
      responseNodes: buildResponseContractFromFields(
        (detail?.responseContract ? [detail.responseContract] : [])
          .flat()
          .filter(Boolean)
          .map((node) => ({
            name: node.name,
            type: node.type,
            description: node.description,
            schema: node,
          })),
      ),
      endpoints: detail?.endpoints || [],
    };
  });

  const filteredImportEndpoints = computed(() => {
    const endpoints = selectedImportDetail.value?.endpoints || [];
    const needle = endpointDetailQuery.value.trim().toLowerCase();
    if (!needle) return endpoints;
    return endpoints.filter((endpoint) => {
      const haystack = [
        endpoint.method,
        endpoint.path,
        endpoint.summary,
        endpoint.operationId,
        endpoint.status,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(needle);
    });
  });

  const visibleImportEndpoints = computed(() =>
    filteredImportEndpoints.value.slice(0, endpointDetailVisibleLimit.value),
  );

  const hasMoreImportEndpoints = computed(
    () => filteredImportEndpoints.value.length > visibleImportEndpoints.value.length,
  );

  function endpointDetailKey(endpoint: { method?: string; path?: string; id?: string }) {
    return endpoint.id || `${endpoint.method || "GET"} ${endpoint.path || ""}`;
  }

  function toggleEndpointDetail(endpoint: { method?: string; path?: string; id?: string }) {
    const key = endpointDetailKey(endpoint);
    expandedEndpointKey.value = expandedEndpointKey.value === key ? "" : key;
  }

  function isEndpointDetailExpanded(endpoint: { method?: string; path?: string; id?: string }) {
    return expandedEndpointKey.value === endpointDetailKey(endpoint);
  }

  function showMoreImportEndpoints() {
    endpointDetailVisibleLimit.value += 40;
  }
  const canImportOpenAPI = computed(() => {
    const workspaceId = importForm.value.workspaceId.trim();
    const providerId = importForm.value.providerId.trim();
    if (!workspaceId || !providerId) return false;
    if (importMode.value === "FILE") {
      return Boolean(
        selectedOpenAPIFile.value &&
        !selectedOpenAPIFilePreview.value.error &&
        selectedOpenAPIFilePreview.value.endpointCount,
      );
    }
    return selectedProviderCanImportOnline.value;
  });
  const selectedImportDetailVisible = computed({
    get: () => Boolean(selectedImportId.value),
    set: (visible: boolean) => {
      if (!visible) selectedImportId.value = "";
    },
  });

  onMounted(() => {
    void loadOpenAPIPageAssets();
  });

  onBeforeUnmount(() => {
    if (actionNoteTimer.value) {
      window.clearTimeout(actionNoteTimer.value);
    }
  });

  watch(
    () => importForm.value.workspaceId,
    (workspaceId) => {
      if (!workspaceId) {
        importForm.value.providerId = "";
        importForm.value.connectionId = "";
        return;
      }
      syncSelectedProvider();
    },
  );
  watch(
    () => importForm.value.providerId,
    (providerId) => {
      if (!providerId) {
        importForm.value.connectionId = "";
        return;
      }
      if (
        !connectionsStore.serviceConnections.some(
          (connection) => connection.id === importForm.value.connectionId && connection.providerId === providerId,
        )
      ) {
        importForm.value.connectionId =
          connectionsStore.serviceConnections.find((connection) => connection.providerId === providerId)?.id || "";
      }
    },
  );

  function connectionById(connectionId: string) {
    return connectionsStore.serviceConnections.find((connection) => connection.id === connectionId);
  }

  function workspaceById(workspaceId: string): Workspace | undefined {
    return workspaces.items.find((workspace) => workspace.id === workspaceId);
  }

  function workspaceLabel(workspaceId: string) {
    const workspace = workspaceById(workspaceId);
    if (!workspace) return workspaceId || "-";
    return `${workspace.name} (${workspace.displayName})`;
  }

  function providerLabel(providerId?: string) {
    return (providersStore.providers || []).find((provider) => provider.id === providerId)?.name || providerId || "-";
  }

  function providerOpenAPIDocumentUrl(provider: CapabilityProvider) {
    const endpointConfig = provider.endpointConfig || {};
    const discovery = endpointConfig.discovery;
    const documentUrl =
      discovery && typeof discovery === "object" && !Array.isArray(discovery)
        ? (discovery as Record<string, unknown>).documentUrl
        : undefined;
    if (typeof documentUrl === "string" && documentUrl.trim()) return documentUrl.trim();
    const legacySourceUri = endpointConfig.sourceUri;
    return typeof legacySourceUri === "string" ? legacySourceUri.trim() : "";
  }

  function canProviderImportOnline(provider: CapabilityProvider) {
    return provider.discoveryMode?.trim().toUpperCase() !== "MANUAL" && Boolean(providerOpenAPIDocumentUrl(provider));
  }

  function syncSelectedProvider() {
    const providers = importProviders.value;
    const current = providers.find((provider) => provider.id === importForm.value.providerId);
    importForm.value.providerId = current?.id || providers[0]?.id || "";
  }

  function statusClass(status: string) {
    return status.toLowerCase().replace(/\s+/g, "-");
  }

  function statusDotClass(status: string) {
    return statusClass(status);
  }

  function hasOpenAPIIssues(record: OpenAPIImport) {
    return record.issues.length > 0 || record.status.toLowerCase().includes("review");
  }

  function issueText(record: OpenAPIImport) {
    if (!record.issues.length) return "无阻塞项";
    return `${record.issues.length} 个阻塞项`;
  }

  function importTime(record: OpenAPIImport) {
    const timestamp = record.updatedAt || record.createdAt;
    if (!timestamp) return "暂无数据";
    const date = new Date(timestamp);
    if (Number.isNaN(date.getTime())) return "暂无数据";
    return date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  }

  function connectionAddress(connection?: ServiceConnection) {
    if (!connection) return "-";
    // ZKL-56 DEF-01 / UX-05: never double-append port when domain is already absolute.
    const normalized = normalizeServiceBaseURL({
      domain: connection.protocolConfig.domain,
      host: connection.protocolConfig.host,
      port: connection.protocolConfig.port,
      basePath: connection.protocolConfig.basePath,
    });
    if (normalized) return normalized;
    // Illegal / incomplete config — do not invent a second port.
    const domain = (connection.protocolConfig.domain || "").trim();
    return domain || "未配置地址";
  }

  function selectedOpenAPIStatus() {
    return openAPIStatusFilter.value === "ALL" ? undefined : openAPIStatusFilter.value;
  }

  async function loadOpenAPIPage(overrides: OpenAPIImportListQuery = {}) {
    return openapiImports.loadOpenAPIImportPage({
      query: query.value.trim(),
      status: selectedOpenAPIStatus(),
      page: overrides.page ?? openapiImports.openAPIImportPagination.page,
      pageSize: overrides.pageSize ?? openapiImports.openAPIImportPagination.pageSize,
      ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
    });
  }

  async function requestOpenAPIPage(overrides: OpenAPIImportListQuery = {}) {
    openAPIListLoading.value = true;
    openAPIListError.value = "";
    try {
      await loadOpenAPIPage(overrides);
    } catch (error) {
      openAPIListError.value = error instanceof Error ? error.message : String(error);
    } finally {
      openAPIListLoading.value = false;
      openAPIListHasLoaded.value = true;
    }
  }

  function changeOpenAPIPage(pagination: { page: number; pageSize: number }) {
    void requestOpenAPIPage(pagination);
  }

  function changeOpenAPISort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
    void requestOpenAPIPage({
      page: 1,
      pageSize: openapiImports.openAPIImportPagination.pageSize,
      sortBy: sort.sortBy ?? "",
      sortOrder: sort.sortOrder,
    });
  }

  function updateOpenAPISearch(value: string) {
    query.value = value;
    void requestOpenAPIPage({ page: 1 });
  }

  function resetOpenAPIFilters() {
    query.value = "";
    openAPIStatusFilter.value = "ALL";
    void requestOpenAPIPage({ page: 1 });
  }

  function updateOpenAPIQuickFilter(value: string) {
    if (value === "Issues") {
      query.value = "";
      openAPIStatusFilter.value = "Issues";
      void requestOpenAPIPage({ page: 1 });
    } else {
      resetOpenAPIFilters();
    }
  }

  async function loadOpenAPIPageAssets() {
    if (openAPIListLoading.value && openAPIListHasLoaded.value) return;
    openAPIListLoading.value = true;
    openAPIListError.value = "";
    try {
      await workspaces.load();
      if (!hasWorkspaceContext.value) return;
      await providersStore.loadProviders();
      await Promise.all([connectionsStore.loadServiceConnectionCatalog(), loadOpenAPIPage()]);
      importForm.value.workspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
      syncSelectedProvider();
      importForm.value.connectionId =
        connectionsStore.serviceConnections.find((connection) => connection.providerId === importForm.value.providerId)
          ?.id || "";
    } catch (error) {
      openAPIListError.value = error instanceof Error ? error.message : String(error);
    } finally {
      openAPIListHasLoaded.value = true;
      openAPIListLoading.value = false;
    }
  }

  function setOpenAPIStatusFilter(value: OpenAPIStatusFilter) {
    openAPIStatusFilter.value = value;
    void requestOpenAPIPage({ page: 1 });
  }

  function toggleOpenAPIDropdown(key: OpenAPIDropdownKey) {
    openapiDropdowns.value = {
      provider: false,
      connection: false,
      [key]: !openapiDropdowns.value[key],
    };
  }

  function closeOpenAPIDropdowns() {
    openapiDropdowns.value = {
      provider: false,
      connection: false,
    };
  }

  function rememberModalTrigger(event?: Event) {
    if (event?.currentTarget instanceof HTMLElement) {
      lastModalTrigger.value = event.currentTarget;
    }
  }

  function focusableElements(root: HTMLElement) {
    return Array.from(
      root.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((element) => element.offsetParent !== null || element === root);
  }

  async function focusModalRoot(getRoot: () => HTMLElement | null) {
    await nextTick();
    const root = getRoot();
    if (!root) return;
    (focusableElements(root)[0] || root).focus();
  }

  async function restoreModalFocus() {
    await nextTick();
    const target = lastModalTrigger.value;
    lastModalTrigger.value = null;
    if (target && document.contains(target)) {
      target.focus();
    }
  }

  function handleModalTab(event: KeyboardEvent, root: HTMLElement | null) {
    if (!root) return;
    const elements = focusableElements(root);
    if (!elements.length) {
      event.preventDefault();
      root.focus();
      return;
    }
    const first = elements[0];
    const last = elements[elements.length - 1];
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

  function openImportModal(event?: Event) {
    rememberModalTrigger(event);
    closeOpenAPIDropdowns();
    importModalVisible.value = true;
    void focusModalRoot(() => importDialogRef.value);
  }

  function finishImportModal() {
    importModalVisible.value = false;
    closeOpenAPIDropdowns();
    void restoreModalFocus();
  }

  function closeImportModal(_event?: Event) {
    if (importingOpenAPI.value) return;
    finishImportModal();
  }

  async function openImportDetail(record: OpenAPIImport, event?: Event) {
    if (!event && document.activeElement instanceof HTMLElement && document.activeElement !== document.body) {
      lastModalTrigger.value = document.activeElement;
    } else {
      rememberModalTrigger(event);
    }
    closeOpenAPIDropdowns();
    // Open stable shell immediately (list row is enough for header identity).
    selectedImportId.value = record.id;
    detailError.value = "";
    expandedEndpointKey.value = "";
    endpointDetailQuery.value = "";
    endpointDetailVisibleLimit.value = 40;
    void focusModalRoot(() => detailDialogRef.value);

    if (record.detail) {
      detailLoading.value = false;
      return;
    }
    await fetchImportDetail(record);
  }

  async function fetchImportDetail(record: OpenAPIImport) {
    const requestId = ++detailRequestSeq;
    const targetId = record.id;
    detailLoading.value = true;
    detailError.value = "";
    try {
      await openapiImports.loadOpenAPIImportDetail(record);
      if (requestId !== detailRequestSeq || selectedImportId.value !== targetId) return;
    } catch (error) {
      if (requestId !== detailRequestSeq || selectedImportId.value !== targetId) return;
      detailError.value = error instanceof Error ? error.message : String(error) || "加载导入详情失败，请重试。";
    } finally {
      if (requestId === detailRequestSeq) detailLoading.value = false;
    }
  }

  async function retryImportDetail() {
    const record = selectedImport.value;
    if (!record) return;
    await fetchImportDetail(record);
  }

  /** List-row menu order: detail → generate → delete (ZKL-33; re-applied after ZKL-31 merge conflict). */
  function openAPIImportMenuActions(record: OpenAPIImport): ManagementRowAction[] {
    const generating = Boolean(generatingDraftsByImportId.value[record.id]);
    return [
      { key: "detail", label: "查看详情", icon: "fa-solid fa-eye", tone: "primary" },
      {
        key: "generate",
        label: "生成 Tool 草稿",
        icon: "fa-solid fa-wand-magic-sparkles",
        loading: generating,
        disabled: generating,
        disabledReason: generating ? "生成中" : undefined,
      },
      { key: "delete", label: "删除记录", icon: "fa-solid fa-trash", tone: "danger" },
    ];
  }

  function handleOpenAPIImportRowAction(actionKey: string, record: OpenAPIImport, event?: Event) {
    if (actionKey === "detail") {
      void openImportDetail(record, event);
      return;
    }
    if (actionKey === "generate") {
      void generateDrafts(record);
      return;
    }
    if (actionKey === "delete") requestRemoveImport(record, event);
  }

  function toggleMobileImportActions(record: OpenAPIImport) {
    mobileImportActionMenuId.value = mobileImportActionMenuId.value === record.id ? "" : record.id;
  }

  function openMobileImportDetail(record: OpenAPIImport, event?: Event) {
    mobileImportActionMenuId.value = "";
    openImportDetail(record, event);
  }

  function generateMobileDrafts(record: OpenAPIImport) {
    mobileImportActionMenuId.value = "";
    void generateDrafts(record);
  }

  function requestMobileImportRemoval(record: OpenAPIImport, event?: Event) {
    mobileImportActionMenuId.value = "";
    requestRemoveImport(record, event);
  }

  function closeImportDetail() {
    // Bump seq + clear id first so heavy endpoint trees unmount before focus restore.
    detailRequestSeq += 1;
    expandedEndpointKey.value = "";
    endpointDetailQuery.value = "";
    endpointDetailVisibleLimit.value = 40;
    detailLoading.value = false;
    detailError.value = "";
    selectedImportId.value = "";
    void nextTick(() => restoreModalFocus());
  }

  function requestRemoveImport(record: OpenAPIImport, event?: Event) {
    rememberModalTrigger(event);
    pendingDeleteImport.value = record;
    void focusModalRoot(() => deleteDialogRef.value);
  }

  function closeDeleteConfirm() {
    if (deletingImportId.value) return;
    pendingDeleteImport.value = null;
    void restoreModalFocus();
  }

  async function confirmRemoveImport() {
    if (!pendingDeleteImport.value || deletingImportId.value) return;
    const record = pendingDeleteImport.value;
    await removeImport(record);
    pendingDeleteImport.value = null;
    void restoreModalFocus();
  }

  function dismissActionNote() {
    actionNote.value = "";
    if (actionNoteTimer.value) {
      window.clearTimeout(actionNoteTimer.value);
      actionNoteTimer.value = null;
    }
  }

  function showActionNote(message: string) {
    dismissActionNote();
    actionNote.value = message;
    actionNoteTimer.value = window.setTimeout(() => {
      actionNote.value = "";
      actionNoteTimer.value = null;
    }, 6000);
  }

  function selectImportProvider(providerId: string) {
    importForm.value.providerId = providerId;
    closeOpenAPIDropdowns();
  }

  function selectImportConnection(connectionId: string) {
    importForm.value.connectionId = connectionId;
    closeOpenAPIDropdowns();
  }

  async function selectOpenAPIFile(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0] || null;
    selectedOpenAPIFile.value = file;
    selectedOpenAPIFilePreview.value = parseOpenAPIPreview(file ? await file.text() : "");
  }

  function buildImportRequest(): OpenAPIImportRequest {
    return {
      workspaceId: importForm.value.workspaceId.trim(),
      providerId: importForm.value.providerId.trim(),
      ...(importForm.value.connectionId?.trim() ? { connectionId: importForm.value.connectionId.trim() } : {}),
    };
  }

  async function importOpenAPI() {
    if (importingOpenAPI.value || !canImportOpenAPI.value) return;
    importingOpenAPI.value = true;
    const request = buildImportRequest();
    try {
      if (importMode.value === "FILE" && selectedOpenAPIFile.value) {
        await openapiImports.createOpenAPIFileImport(request, selectedOpenAPIFile.value);
      } else {
        await openapiImports.createOpenAPIImport(request);
      }
      await loadOpenAPIPage({ page: 1 });
      showActionNote(
        `${selectedOpenAPIFile.value?.name || selectedProviderOption.value?.name || "Provider"} 已完成解析，可继续生成 Tool Draft。`,
      );
      finishImportModal();
    } finally {
      importingOpenAPI.value = false;
    }
  }

  async function generateDrafts(record: OpenAPIImport) {
    if (generatingDraftsByImportId.value[record.id]) return;
    generatingDraftsByImportId.value = { ...generatingDraftsByImportId.value, [record.id]: true };
    try {
      const drafts = await openapiImports.generateToolDrafts(record.id);
      await loadOpenAPIPage();
      showActionNote(
        drafts.length
          ? `${record.source} 已生成 ${drafts.length} 个 Tool Draft，可到工具管理中补齐参数契约并发布。`
          : `${record.source} 没有生成新 Tool Draft，可能是 Tool ID 已存在。`,
      );
    } finally {
      const { [record.id]: _removed, ...rest } = generatingDraftsByImportId.value;
      generatingDraftsByImportId.value = rest;
    }
  }

  async function removeImport(record: OpenAPIImport) {
    if (deletingImportId.value) return;
    deletingImportId.value = record.id;
    try {
      await openapiImports.deleteOpenAPIImport(record.id);
      const currentPage = openapiImports.openAPIImportPagination.page;
      await loadOpenAPIPage();
      if (!openapiImports.openAPIImportPageItems.length && currentPage > 1) {
        await loadOpenAPIPage({ page: currentPage - 1 });
      }
      if (selectedImportId.value === record.id) {
        selectedImportId.value = "";
      }
      showActionNote(`${record.fileName} 已从导入记录中删除。`);
    } finally {
      deletingImportId.value = "";
    }
  }

  return {
    openapiImports,
    providersStore,
    connectionsStore,
    openAPIQuickFilterOptions,
    router,
    workspaces,
    canEditWorkspace,
    hasWorkspaceContext,
    query,
    openAPIStatusFilter,
    openAPIQuickFilterValue,
    importModalVisible,
    importMode,
    selectedOpenAPIFile,
    selectedOpenAPIFilePreview,
    selectedImportId,
    detailLoading,
    detailError,
    detailRequestSeq,
    actionNote,
    importingOpenAPI,
    generatingDraftsByImportId,
    deletingImportId,
    pendingDeleteImport,
    openAPIListLoading,
    openAPIListError,
    openAPIListHasLoaded,
    mobileImportActionMenuId,
    importDialogRef,
    detailDialogRef,
    deleteDialogRef,
    lastModalTrigger,
    actionNoteTimer,
    openapiDropdowns,
    importForm,
    openAPIImportColumns,
    hasImportRecords,
    importProviders,
    selectedWorkspaceOption,
    selectedProviderOption,
    selectedProviderCanImportOnline,
    selectedConnectionOption,
    selectedImport,
    selectedWorkspace,
    selectedConnection,
    selectedImportDetail,
    filteredImportEndpoints,
    visibleImportEndpoints,
    hasMoreImportEndpoints,
    expandedEndpointKey,
    endpointDetailQuery,
    endpointDetailVisibleLimit,
    endpointDetailKey,
    toggleEndpointDetail,
    isEndpointDetailExpanded,
    showMoreImportEndpoints,
    canImportOpenAPI,
    selectedImportDetailVisible,
    connectionById,
    workspaceById,
    workspaceLabel,
    providerLabel,
    providerOpenAPIDocumentUrl,
    canProviderImportOnline,
    syncSelectedProvider,
    statusClass,
    statusDotClass,
    hasOpenAPIIssues,
    issueText,
    importTime,
    connectionAddress,
    selectedOpenAPIStatus,
    loadOpenAPIPage,
    requestOpenAPIPage,
    changeOpenAPIPage,
    changeOpenAPISort,
    updateOpenAPISearch,
    resetOpenAPIFilters,
    updateOpenAPIQuickFilter,
    loadOpenAPIPageAssets,
    setOpenAPIStatusFilter,
    toggleOpenAPIDropdown,
    closeOpenAPIDropdowns,
    rememberModalTrigger,
    focusableElements,
    focusModalRoot,
    restoreModalFocus,
    handleModalTab,
    openImportModal,
    finishImportModal,
    closeImportModal,
    openImportDetail,
    fetchImportDetail,
    retryImportDetail,
    openAPIImportMenuActions,
    handleOpenAPIImportRowAction,
    toggleMobileImportActions,
    openMobileImportDetail,
    generateMobileDrafts,
    requestMobileImportRemoval,
    closeImportDetail,
    requestRemoveImport,
    closeDeleteConfirm,
    confirmRemoveImport,
    dismissActionNote,
    showActionNote,
    selectImportProvider,
    selectImportConnection,
    selectOpenAPIFile,
    buildImportRequest,
    importOpenAPI,
    generateDrafts,
    removeImport,
  };
}
