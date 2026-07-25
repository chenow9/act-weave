<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";

import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import ToolSchemaTreeView from "../components/ToolSchemaTreeView.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useIntegrationStore } from "../stores/integration";
import { parseOpenAPIPreview } from "../utils/openapi-preview";
import { buildBodyContractFromRequestParams, buildResponseContractFromFields } from "../utils/tool-schema-json";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  CapabilityProvider,
  OpenAPIImport,
  OpenAPIImportListQuery,
  OpenAPIImportRequest,
  ServiceConnection,
  ToolSchemaNode,
  ToolSchemaNodeType,
  Workspace,
} from "../types/domain";

type OpenAPIStatusFilter = "ALL" | "Ready" | "Issues";
type OpenAPIQuickFilter = "Issues" | "ALL";
type OpenAPIImportMode = "FILE" | "ONLINE";
type OpenAPIDropdownKey = "provider" | "connection";

const openAPIQuickFilterOptions = [
  { label: "待确认", value: "Issues" },
  { label: "全部", value: "ALL" },
];

const integration = useIntegrationStore();
const router = useRouter();
const workspaces = useWorkspaceStore();
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
  { key: "file", label: "导入文件", width: 310, sortable: true, sortKey: "fileName", getValue: (record) => `${record.fileName} ${record.source}` },
  { key: "connection", label: "服务连接", width: 180, hidable: true, sortable: true, sortKey: "connection", getValue: (record) => connectionById(record.connectionId || "")?.name || record.connectionId || "Provider 默认连接" },
  { key: "totalEndpoints", label: "接口数", width: 96, align: "center", headerAlign: "center", hidable: true, sortable: true, sortKey: "totalEndpoints", getValue: (record) => record.totalEndpoints },
  { key: "readyEndpoints", label: "可生成", width: 96, align: "center", headerAlign: "center", hidable: true, sortable: true, sortKey: "readyEndpoints", getValue: (record) => record.readyEndpoints },
  { key: "issues", label: "待处理", width: 140, hidable: true, sortable: true, sortKey: "issueCount", getValue: issueText },
  { key: "importTime", label: "导入时间", width: 132, hidable: true, sortable: true, sortKey: "createdAt", getValue: importTime },
  { key: "status", label: "状态", width: 112, align: "center", headerAlign: "center", hidable: true, sortable: true, sortKey: "status", getValue: (record) => record.status },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
]);
const hasImportRecords = computed(() => integration.openAPIImportRegistryTotal > 0);
const importProviders = computed(() => integration.providers || []);
const selectedWorkspaceOption = computed(() => workspaceById(importForm.value.workspaceId));
const selectedProviderOption = computed(() => importProviders.value.find((provider) => provider.id === importForm.value.providerId));
const selectedProviderCanImportOnline = computed(() => Boolean(selectedProviderOption.value && canProviderImportOnline(selectedProviderOption.value)));
const selectedConnectionOption = computed(() => connectionById(importForm.value.connectionId || ""));
const selectedImport = computed(() => integration.openAPIImportPageItems.find((record) => record.id === selectedImportId.value));
const selectedWorkspace = computed(() => workspaceById(selectedImport.value?.workspaceId || ""));
const selectedConnection = computed(() => connectionById(selectedImport.value?.connectionId || "") || integration.serviceConnections[0]);
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
const canImportOpenAPI = computed(() => {
  const workspaceId = importForm.value.workspaceId.trim();
  const providerId = importForm.value.providerId.trim();
  if (!workspaceId || !providerId) return false;
  if (importMode.value === "FILE") {
    return Boolean(selectedOpenAPIFile.value && !selectedOpenAPIFilePreview.value.error && selectedOpenAPIFilePreview.value.endpointCount);
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
    if (!integration.serviceConnections.some((connection) => connection.id === importForm.value.connectionId && connection.providerId === providerId)) {
      importForm.value.connectionId = integration.serviceConnections.find((connection) => connection.providerId === providerId)?.id || "";
    }
  },
);

function connectionById(connectionId: string) {
  return integration.serviceConnections.find((connection) => connection.id === connectionId);
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
  return (integration.providers || []).find((provider) => provider.id === providerId)?.name || providerId || "-";
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
  return `${connection.protocolConfig.domain}:${connection.protocolConfig.port}${connection.protocolConfig.basePath}`;
}

function selectedOpenAPIStatus() {
  return openAPIStatusFilter.value === "ALL" ? undefined : openAPIStatusFilter.value;
}

async function loadOpenAPIPage(overrides: OpenAPIImportListQuery = {}) {
  return integration.loadOpenAPIImportPage({
    query: query.value.trim(),
    status: selectedOpenAPIStatus(),
    page: overrides.page ?? integration.openAPIImportPagination.page,
    pageSize: overrides.pageSize ?? integration.openAPIImportPagination.pageSize,
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
    pageSize: integration.openAPIImportPagination.pageSize,
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
    await integration.loadProviders();
    await Promise.all([integration.loadServiceConnectionCatalog(), loadOpenAPIPage()]);
    importForm.value.workspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
    syncSelectedProvider();
    importForm.value.connectionId = integration.serviceConnections.find((connection) => connection.providerId === importForm.value.providerId)?.id || "";
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
  const detailed = record.detail ? record : await integration.loadOpenAPIImportDetail(record);
  selectedImportId.value = detailed.id;
  void focusModalRoot(() => detailDialogRef.value);
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
  selectedImportId.value = "";
  void restoreModalFocus();
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
      await integration.createOpenAPIFileImport(request, selectedOpenAPIFile.value);
    } else {
      await integration.createOpenAPIImport(request);
    }
    await loadOpenAPIPage({ page: 1 });
    showActionNote(`${selectedOpenAPIFile.value?.name || selectedProviderOption.value?.name || "Provider"} 已完成解析，可继续生成 Tool Draft。`);
    finishImportModal();
  } finally {
    importingOpenAPI.value = false;
  }
}

async function generateDrafts(record: OpenAPIImport) {
  if (generatingDraftsByImportId.value[record.id]) return;
  generatingDraftsByImportId.value = { ...generatingDraftsByImportId.value, [record.id]: true };
  try {
    const drafts = await integration.generateToolDrafts(record.id);
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
    await integration.deleteOpenAPIImport(record.id);
    const currentPage = integration.openAPIImportPagination.page;
    await loadOpenAPIPage();
    if (!integration.openAPIImportPageItems.length && currentPage > 1) {
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
</script>

<template>
  <div class="openapi-import-page management-page-grid management-page-grid--two-rows" @click="closeOpenAPIDropdowns">
    <ManagementPageHeader
      class="openapi-page-header"
      title="OpenAPI 导入"
      description="导入属于集成接入流程：选择已验证的服务连接，解析接口清单，再生成 Tool 草稿。Tool 管理页只接收草稿并补齐动作契约。"
      icon="fa-solid fa-file-import"
      eyebrow="OpenAPI Import"
    >
      <template #actions>
        <button class="ghost-button" type="button" @click="router.push('/connections')">
          <i class="fa-solid fa-plug" aria-hidden="true" />
          服务连接
        </button>
        <button
          class="primary-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? '导入 OpenAPI' : '请先创建或加入业务空间'"
          @click.stop="openImportModal($event)"
        >
          <i class="fa-solid fa-file-import" aria-hidden="true" />
          导入 OpenAPI
        </button>
      </template>
    </ManagementPageHeader>

    <section class="openapi-import-table-card management-list-card">
      <WorkspaceContextState
        v-if="!hasWorkspaceContext"
        feature="OpenAPI 导入"
        icon="fa-solid fa-file-circle-plus"
        @retry="loadOpenAPIPageAssets"
      />
      <ManagementList
        v-else
        class="openapi-import-management-list"
        :rows="integration.openAPIImportPageItems"
        :columns="openAPIImportColumns"
        row-key="id"
        :sticky-left-keys="['file']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:openapi-imports:columns"
        :selected-row-key="selectedImportId"
        :loading="openAPIListLoading"
        :error="openAPIListError"
        :has-loaded="openAPIListHasLoaded"
        :search="query"
        search-placeholder="搜索来源 / Provider / 服务连接..."
        search-aria-label="搜索 OpenAPI 导入记录"
        clear-search-aria-label="清除 OpenAPI 导入搜索"
        reset-aria-label="重置 OpenAPI 导入筛选"
        :reset-disabled="!query && openAPIStatusFilter === 'ALL'"
        :pagination="integration.openAPIImportPagination"
        :sort-by="integration.openAPIImportListQuery?.sortBy"
        :sort-order="integration.openAPIImportListQuery?.sortOrder"
        @update:search="updateOpenAPISearch"
        @reset="resetOpenAPIFilters"
        @page-change="changeOpenAPIPage"
        @sort-change="changeOpenAPISort"
        @select-row="openImportDetail"
      >
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="openAPIQuickFilterValue"
            :options="openAPIQuickFilterOptions"
            ariaLabel="OpenAPI 导入快捷筛选"
            @update:model-value="updateOpenAPIQuickFilter"
          />
        </template>

        <template #cell-file="{ row: record }">
          <div class="openapi-file-cell">
            <div><i class="fa-solid fa-file-code" /></div>
            <span>
              <strong class="aw-table-title">{{ record.fileName }}</strong>
              <small class="aw-table-subtitle">{{ record.source }}</small>
              <em class="aw-table-meta">{{ workspaceLabel(record.workspaceId) }} · {{ record.providerId || "Provider" }}</em>
            </span>
          </div>
        </template>
        <template #cell-connection="{ row: record }">
          <span class="openapi-connection-name aw-table-meta">{{ connectionById(record.connectionId || "")?.name || record.connectionId || "Provider 默认连接" }}</span>
        </template>
        <template #cell-totalEndpoints="{ row: record }">
          <span class="openapi-count-cell"><strong class="aw-table-title">{{ record.totalEndpoints }}</strong><small class="aw-table-meta">个</small></span>
        </template>
        <template #cell-readyEndpoints="{ row: record }">
          <span class="openapi-count-cell ready"><strong class="aw-table-title">{{ record.readyEndpoints }}</strong><small class="aw-table-meta">个</small></span>
        </template>
        <template #cell-issues="{ row: record }">
          <span class="openapi-issue-text aw-table-meta" :class="{ warning: hasOpenAPIIssues(record) }">{{ issueText(record) }}</span>
        </template>
        <template #cell-importTime="{ row: record }">
          <span class="openapi-time-text aw-table-meta">{{ importTime(record) }}</span>
        </template>
        <template #cell-status="{ row: record }">
          <span class="openapi-status-pill aw-table-pill" :class="statusClass(record.status)">
            <span :class="statusDotClass(record.status)" />
            {{ record.status }}
          </span>
        </template>
        <template #cell-actions="{ row: record }">
          <ManagementRowActions
            :menu-actions="openAPIImportMenuActions(record)"
            menu-label="更多操作"
            @action="handleOpenAPIImportRowAction($event, record)"
          />
        </template>

        <template #card="{ row: record }">
          <article class="openapi-import-mobile-card">
            <header>
              <div class="openapi-file-cell">
                <div><i class="fa-solid fa-file-code" /></div>
                <span>
                  <strong>{{ record.fileName }}</strong>
                  <small>{{ record.source }}</small>
                </span>
              </div>
              <button
                class="openapi-mobile-actions-toggle"
                type="button"
                aria-label="OpenAPI 导入记录更多操作"
                :aria-expanded="mobileImportActionMenuId === record.id"
                @click.stop="toggleMobileImportActions(record)"
              >
                <i class="fa-solid fa-ellipsis" />
              </button>
            </header>
            <dl>
              <div><dt>服务连接</dt><dd>{{ connectionById(record.connectionId || "")?.name || record.connectionId || "Provider 默认连接" }}</dd></div>
              <div><dt>接口</dt><dd>{{ record.totalEndpoints }} 个 / 可生成 {{ record.readyEndpoints }} 个</dd></div>
              <div><dt>状态</dt><dd><span class="openapi-status-pill" :class="statusClass(record.status)"><span :class="statusDotClass(record.status)" />{{ record.status }}</span></dd></div>
            </dl>
            <button class="openapi-mobile-detail-button" type="button" @click="openMobileImportDetail(record, $event)">查看导入详情</button>
            <div v-if="mobileImportActionMenuId === record.id" class="openapi-mobile-actions-menu" role="menu" aria-label="OpenAPI 导入记录操作">
              <button type="button" role="menuitem" @click="openMobileImportDetail(record, $event)"><i class="fa-solid fa-eye" />查看详情</button>
              <button type="button" role="menuitem" :disabled="Boolean(generatingDraftsByImportId[record.id])" @click="generateMobileDrafts(record)">
                <i class="fa-solid fa-wand-magic-sparkles" />生成 Tool 草稿
              </button>
              <button class="danger" type="button" role="menuitem" @click="requestMobileImportRemoval(record, $event)"><i class="fa-solid fa-trash" />删除记录</button>
            </div>
          </article>
        </template>

        <template #error="{ error }">
          <div v-if="integration.openAPIImportPageItems.length" class="openapi-load-error-banner" role="alert">
            <i class="fa-solid fa-triangle-exclamation" />
            <span>OpenAPI 导入记录加载失败：{{ error }}</span>
            <button class="ghost-button" type="button" data-openapi-load-retry @click="loadOpenAPIPageAssets">重试</button>
          </div>
          <div v-else class="openapi-empty-state openapi-load-error-state" role="alert">
            <div><i class="fa-solid fa-triangle-exclamation" /></div>
            <h4>OpenAPI 导入记录加载失败</h4>
            <p>{{ error }}</p>
            <button class="primary-button" type="button" data-openapi-load-retry @click="loadOpenAPIPageAssets">重试</button>
          </div>
        </template>

        <template #empty>
          <div v-if="!hasImportRecords" class="openapi-empty-state">
            <div><i class="fa-solid fa-file-circle-plus" /></div>
            <h4>暂无导入记录</h4>
            <p>选择已验证的服务连接，导入 OpenAPI 后再生成 Tool 草稿。</p>
            <button class="primary-button" type="button" @click="openImportModal($event)">导入 OpenAPI</button>
          </div>
          <div v-else class="openapi-empty-state compact">
            <div><i class="fa-solid fa-magnifying-glass" /></div>
            <h4>没有匹配导入记录</h4>
            <p>调整文件、来源或服务连接关键词</p>
            <button class="ghost-button" type="button" @click="resetOpenAPIFilters">清除搜索条件</button>
          </div>
        </template>
      </ManagementList>
    </section>

    <div v-if="importModalVisible" class="openapi-modal-backdrop" @click.self="closeImportModal">
      <section
        ref="importDialogRef"
        class="openapi-modal-card"
        role="dialog"
        aria-modal="true"
        aria-label="导入 OpenAPI"
        tabindex="-1"
        @click.stop
        @keydown.esc.stop.prevent="closeImportModal"
        @keydown.tab="handleModalTab($event, importDialogRef)"
      >
        <header class="openapi-modal-head">
          <div>
            <span><i class="fa-solid fa-file-import" /></span>
            <div>
              <h3>导入 OpenAPI</h3>
              <p>确认当前业务空间并选择 Provider、服务连接，解析接口清单</p>
            </div>
          </div>
          <button type="button" title="关闭" aria-label="关闭导入弹框" @click="closeImportModal">
            <i class="fa-solid fa-xmark" />
          </button>
        </header>

        <div class="openapi-modal-body">
          <div class="openapi-field">
            <label>当前业务空间</label>
            <div class="openapi-reference-select is-readonly" data-testid="openapi-current-workspace">
              <span><i class="fa-solid fa-layer-group" />{{ selectedWorkspaceOption ? `${selectedWorkspaceOption.name} · ${selectedWorkspaceOption.displayName}` : "未选择业务空间" }}</span>
              <small>在页面顶部切换</small>
            </div>
          </div>

          <div class="openapi-field">
            <label>导入方式</label>
            <div class="openapi-import-mode-tabs" role="tablist" aria-label="OpenAPI 导入方式">
              <button type="button" role="tab" :aria-selected="importMode === 'FILE'" :class="{ active: importMode === 'FILE' }" @click="importMode = 'FILE'">
                <i class="fa-solid fa-file-arrow-up" /> 本地文件
              </button>
              <button type="button" role="tab" :aria-selected="importMode === 'ONLINE'" :class="{ active: importMode === 'ONLINE' }" @click="importMode = 'ONLINE'">
                <i class="fa-solid fa-cloud-arrow-down" /> Provider 在线文档
              </button>
            </div>
          </div>

          <div class="openapi-field dropdown" @click.stop>
            <label>选择 Provider <b class="field-required-mark">*</b></label>
            <button
              class="openapi-reference-select"
              type="button"
              aria-haspopup="listbox"
              :aria-expanded="openapiDropdowns.provider"
              :disabled="!importForm.workspaceId || !importProviders.length"
              data-testid="openapi-provider-select"
              @click="toggleOpenAPIDropdown('provider')"
            >
              <span><i class="fa-solid fa-cubes" />{{ selectedProviderOption?.name || (importForm.workspaceId ? (importProviders.length ? "请选择 Provider" : "当前空间暂无 Provider") : "先选择业务空间") }}</span>
              <i class="fa-solid fa-chevron-down" :class="{ open: openapiDropdowns.provider }" />
            </button>
            <div v-if="openapiDropdowns.provider" class="openapi-select-menu" role="listbox">
              <button
                v-for="provider in importProviders"
                :key="provider.id"
                class="openapi-select-option"
                :class="{ selected: importForm.providerId === provider.id }"
                type="button"
                role="option"
                :aria-selected="importForm.providerId === provider.id"
                @click="selectImportProvider(provider.id)"
              >
                <span class="openapi-option-copy">
                  <strong>{{ provider.name }}</strong>
                  <small>{{ canProviderImportOnline(provider) ? "可在线导入" : "未配置在线 OpenAPI 文档" }}</small>
                </span>
                <i v-if="importForm.providerId === provider.id" class="fa-solid fa-circle-check" />
              </button>
            </div>
          </div>

          <div class="openapi-field dropdown" @click.stop>
            <label>选择服务连接</label>
            <button
              class="openapi-reference-select"
              type="button"
              aria-haspopup="listbox"
              :aria-expanded="openapiDropdowns.connection"
              :disabled="!importForm.providerId"
              data-testid="openapi-connection-select"
              @click="toggleOpenAPIDropdown('connection')"
            >
              <span><i class="fa-solid fa-plug" />{{ selectedConnectionOption?.name || (importForm.providerId ? "使用 Provider 默认连接" : "先选择 Provider") }}</span>
              <i class="fa-solid fa-chevron-down" :class="{ open: openapiDropdowns.connection }" />
            </button>
            <div v-if="openapiDropdowns.connection" class="openapi-select-menu" role="listbox">
              <button class="openapi-select-option" type="button" role="option" :aria-selected="!importForm.connectionId" @click="selectImportConnection('')">
                <span>使用 Provider 默认连接</span>
                <i v-if="!importForm.connectionId" class="fa-solid fa-circle-check" />
              </button>
              <button
                v-for="connection in integration.serviceConnections.filter((item) => item.providerId === importForm.providerId)"
                :key="connection.id"
                class="openapi-select-option"
                :class="{ selected: importForm.connectionId === connection.id }"
                type="button"
                role="option"
                :aria-selected="importForm.connectionId === connection.id"
                @click="selectImportConnection(connection.id)"
              >
                <span>{{ connection.name }}</span>
                <i v-if="importForm.connectionId === connection.id" class="fa-solid fa-circle-check" />
              </button>
            </div>
          </div>

          <div v-if="importMode === 'FILE'" class="openapi-field">
            <label>OpenAPI 文件 <b class="field-required-mark">*</b></label>
            <label class="openapi-file-picker">
              <span class="openapi-file-picker-button"><i class="fa-solid fa-folder-open" />选择文件</span>
              <span class="openapi-file-picker-name">{{ selectedOpenAPIFile?.name || "请选择 JSON 或 YAML 文件" }}</span>
              <span class="openapi-file-picker-meta">最大 4 MB</span>
              <input
                class="openapi-file-input"
                data-testid="openapi-file-input"
                type="file"
                accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml"
                @change="selectOpenAPIFile"
              />
            </label>
            <small v-if="selectedOpenAPIFilePreview.error" class="openapi-file-error" role="alert">{{ selectedOpenAPIFilePreview.error }}</small>
          </div>

          <div v-if="importMode === 'FILE' && selectedOpenAPIFilePreview.endpointCount" class="import-drawer-preview">
            <div>
              <i class="fa-solid fa-list-check" />
              <span>
                <strong>识别到 {{ selectedOpenAPIFilePreview.endpointCount }} 个接口</strong>
                <small>{{ selectedOpenAPIFilePreview.readyCount }} 个接口可生成 Tool 草稿，导入后可逐项确认。</small>
              </span>
            </div>
            <div class="openapi-preview-table">
              <div><strong>方法</strong><strong>路径</strong><strong>建议 Tool</strong><strong>状态</strong></div>
              <div v-for="row in selectedOpenAPIFilePreview.rows.slice(0, 6)" :key="`${row.method}:${row.path}`">
                <span>{{ row.method }}</span><span>{{ row.path }}</span><span>{{ row.suggestedTool }}</span><span>{{ row.statusText }}</span>
              </div>
            </div>
          </div>

          <div v-else-if="importMode === 'ONLINE' && selectedProviderCanImportOnline" class="import-drawer-preview">
            <div>
              <i class="fa-solid fa-cloud-arrow-down" />
              <span>
                <strong>Provider OpenAPI 来源</strong>
                <small>后端将从所选 Provider 的受管来源拉取并解析 OpenAPI 文档。</small>
              </span>
            </div>
            <div class="import-preview-empty">请求仅提交 Provider 和可选 Connection，不上传文件，也不绑定 Agent。</div>
          </div>
          <div v-else-if="importMode === 'ONLINE' && selectedProviderOption" class="import-drawer-preview unavailable" role="status" aria-live="polite">
            <div>
              <i class="fa-solid fa-circle-info" />
              <span>
                <strong>Provider 和 Connection 已加载</strong>
                <small>当前 Provider 未配置可在线读取的 OpenAPI 文档，暂时不能发起在线导入。</small>
              </span>
            </div>
            <div class="import-preview-empty">数据不会再从下拉框中隐藏。需要在线导入时，请到 Provider 管理补充文档地址并启用按需发现。</div>
          </div>
          <div v-else-if="importMode === 'ONLINE'" class="import-drawer-preview unavailable" role="status" aria-live="polite">
            <div>
              <i class="fa-solid fa-circle-info" />
              <span>
                <strong>当前空间暂无 Provider</strong>
                <small>请先在 Provider 管理中登记服务，再返回导入 OpenAPI。</small>
              </span>
            </div>
          </div>
        </div>

        <footer class="openapi-modal-actions">
          <span>导入后生成 Tool 草稿</span>
          <div>
            <button type="button" :disabled="importingOpenAPI" @click="closeImportModal">取消</button>
            <button type="button" :disabled="!canImportOpenAPI || importingOpenAPI" @click="importOpenAPI">
              <i v-if="importingOpenAPI" class="fa-solid fa-spinner fa-spin" />
              {{ importingOpenAPI ? "解析中" : "开始导入" }}
            </button>
          </div>
        </footer>
      </section>
    </div>

    <div v-if="selectedImportDetailVisible" class="openapi-modal-backdrop" @click.self="closeImportDetail">
      <section
        v-if="selectedImport"
        ref="detailDialogRef"
        class="openapi-modal-card openapi-detail-modal-card"
        role="dialog"
        aria-modal="true"
        aria-label="导入详情"
        tabindex="-1"
        @keydown.esc.stop.prevent="closeImportDetail"
        @keydown.tab="handleModalTab($event, detailDialogRef)"
      >
        <header class="openapi-modal-head">
          <div>
            <span><i class="fa-solid fa-file-code" /></span>
            <div>
              <h3>导入详情</h3>
              <p>查看导入归属、连接与结构化契约</p>
            </div>
          </div>
          <button type="button" title="关闭" aria-label="关闭详情弹框" @click="closeImportDetail">
            <i class="fa-solid fa-xmark" />
          </button>
        </header>
        <div class="openapi-detail-modal-body">
          <div class="openapi-detail-hero">
            <i class="fa-solid fa-file-code" />
            <div>
              <strong>{{ selectedImport.fileName }}</strong>
              <small>{{ selectedImport.source }} · {{ workspaceLabel(selectedImport.workspaceId) }} · {{ selectedImport.providerId || "Provider" }}</small>
            </div>
            <span class="openapi-status-pill" :class="statusClass(selectedImport.status)">
              <span :class="statusDotClass(selectedImport.status)" />
              {{ selectedImport.status }}
            </span>
          </div>
          <div class="openapi-detail-grid import-detail-grid">
            <div class="config-summary-item"><i class="fa-solid fa-layer-group" /><span>归属空间</span><strong>{{ selectedWorkspace?.name || selectedImport.workspaceId }}</strong></div>
            <div class="config-summary-item"><i class="fa-solid fa-cubes" /><span>来源 Provider</span><strong>{{ providerLabel(selectedImport.providerId) }}</strong></div>
            <div class="config-summary-item"><i class="fa-solid fa-plug-circle-bolt" /><span>服务连接</span><strong>{{ selectedConnection?.name }}</strong></div>
            <div class="config-summary-item"><i class="fa-solid fa-server" /><span>服务地址</span><strong>{{ connectionAddress(selectedConnection) }}</strong></div>
            <div class="config-summary-item"><i class="fa-solid fa-list-check" /><span>接口数量</span><strong>{{ selectedImport.totalEndpoints }}</strong></div>
            <div class="config-summary-item"><i class="fa-solid fa-wand-magic-sparkles" /><span>生成状态</span><strong>{{ selectedImport.readyEndpoints }} 个可生成</strong></div>
          </div>
          <ToolSchemaTreeView :nodes="selectedImportDetail?.requestTransport || []" title="请求参数" empty-text="当前导入记录未返回传输参数。" />
          <ToolSchemaTreeView :nodes="selectedImportDetail?.requestBodyNodes || []" title="请求体 Body" empty-text="当前导入记录未返回请求体结构。" />
          <ToolSchemaTreeView :nodes="selectedImportDetail?.responseNodes || []" title="响应结果" empty-text="当前导入记录未返回响应结构。" />
          <div v-if="selectedImportDetail?.endpoints.length" class="tool-schema-endpoint-list">
            <div class="editable-schema-head">
              <div>
                <strong>接口明细</strong>
                <span>按接口查看导入出的结构化契约。</span>
              </div>
            </div>
            <div v-for="endpoint in selectedImportDetail.endpoints" :key="`${endpoint.method}-${endpoint.path}`" class="tool-schema-endpoint-card">
              <div class="tool-schema-endpoint-head">
                <strong>{{ endpoint.method }} {{ endpoint.path }}</strong>
                <span>{{ endpoint.summary || endpoint.operationId || endpoint.status }}</span>
              </div>
              <ToolSchemaTreeView :nodes="endpoint.requestContract ? [endpoint.requestContract].flat() as ToolSchemaNode[] : []" title="请求体 Body" empty-text="无请求结构" />
              <ToolSchemaTreeView :nodes="endpoint.responseContract ? [endpoint.responseContract].flat() as ToolSchemaNode[] : []" title="响应结果" empty-text="无响应结构" />
            </div>
          </div>
        </div>
        <div class="drawer-footer-actions openapi-detail-actions">
          <button type="button" @click="closeImportDetail">关闭</button>
          <button type="button" :disabled="Boolean(generatingDraftsByImportId[selectedImport.id])" @click="generateDrafts(selectedImport)">
            <i v-if="generatingDraftsByImportId[selectedImport.id]" class="fa-solid fa-spinner fa-spin" />
            {{ generatingDraftsByImportId[selectedImport.id] ? "生成中" : "生成 Tool 草稿" }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="pendingDeleteImport" class="openapi-modal-backdrop" @click.self="closeDeleteConfirm">
      <section
        ref="deleteDialogRef"
        class="openapi-modal-card openapi-confirm-modal-card"
        role="dialog"
        aria-modal="true"
        aria-label="删除导入记录"
        tabindex="-1"
        @keydown.esc.stop.prevent="closeDeleteConfirm"
        @keydown.tab="handleModalTab($event, deleteDialogRef)"
      >
        <header class="openapi-modal-head">
          <div>
            <span><i class="fa-solid fa-triangle-exclamation" /></span>
            <div>
              <h3>删除导入记录</h3>
              <p>删除后需要重新导入才能再次生成草稿</p>
            </div>
          </div>
          <button type="button" title="关闭" aria-label="关闭删除确认弹框" :disabled="Boolean(deletingImportId)" @click="closeDeleteConfirm">
            <i class="fa-solid fa-xmark" />
          </button>
        </header>
        <div class="openapi-confirm-body">
          <strong>{{ pendingDeleteImport.fileName }}</strong>
          <p>确认删除这条 OpenAPI 导入记录？已生成的 Tool 草稿不会被自动删除。</p>
        </div>
        <footer class="openapi-modal-actions">
          <span>此操作会立即同步到后端</span>
          <div>
            <button type="button" :disabled="Boolean(deletingImportId)" @click="closeDeleteConfirm">取消</button>
            <button class="danger" type="button" :disabled="Boolean(deletingImportId)" @click="confirmRemoveImport">
              <i v-if="deletingImportId" class="fa-solid fa-spinner fa-spin" />
              {{ deletingImportId ? "删除中" : "确认删除" }}
            </button>
          </div>
        </footer>
      </section>
    </div>

    <div v-if="actionNote && !importModalVisible && !selectedImportId && !pendingDeleteImport" class="action-toast" role="status" aria-live="polite">
      <span>{{ actionNote }}</span>
      <button type="button" aria-label="关闭提示" @click="dismissActionNote">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.openapi-import-page {
  min-width: 0;
  min-height: 0;
  color: #1e293b;
  font-family: Inter, "Noto Sans SC", sans-serif;
}

.openapi-tip-card {
  display: flex;
  align-items: center;
  gap: 12px;
  border: 1px solid #c7d2fe;
  border-radius: 12px;
  background: linear-gradient(90deg, #eef2ff 0%, #eff6ff 100%);
  padding: 16px;
}

.openapi-tip-card > div:first-child {
  display: flex;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border-radius: 8px;
  background: #e0e7ff;
  color: #4f46e5;
}

.openapi-tip-card p {
  margin: 0;
  color: #312e81;
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
}

.openapi-tip-card button {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: 4px;
  margin-top: 4px;
  border: 0;
  background: transparent;
  color: #4f46e5;
  padding: 0 4px;
  font-size: 10px;
  font-weight: 700;
  line-height: 14px;
  cursor: pointer;
}

/* Transparent shell — ManagementList owns table/toolbar/footer chrome. */
.openapi-import-table-card.management-list-card {
  min-width: 0;
  min-height: 0;
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.openapi-import-management-list :deep(.data-table tbody tr:hover) .openapi-file-cell strong,
.openapi-import-management-list :deep(.data-table tbody tr:focus-visible) .openapi-file-cell strong {
  color: #4f46e5;
}

.openapi-load-error-banner {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: 10px;
  border-bottom: 1px solid #fecdd3;
  background: #fff1f2;
  color: #9f1239;
  padding: 10px 20px;
  font-size: 12px;
}

.openapi-load-error-banner span {
  flex: 1 1 auto;
}

.openapi-load-error-banner button {
  min-height: 36px;
  border: 0;
  border-radius: 6px;
  background: #be123c;
  color: #fff;
  padding: 6px 12px;
  font: inherit;
  font-weight: 700;
}

.openapi-load-error-state > div {
  background: #fff1f2;
  color: #be123c;
}

.openapi-import-mobile-card {
  position: relative;
  min-width: 0;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  padding: 14px;
}

.openapi-import-mobile-card > header {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}

.openapi-import-mobile-card .openapi-file-cell {
  min-width: 0;
}

.openapi-import-mobile-card .openapi-file-cell span {
  min-width: 0;
}

.openapi-import-mobile-card .openapi-file-cell strong,
.openapi-import-mobile-card .openapi-file-cell small {
  max-width: 220px;
}

.openapi-mobile-actions-toggle {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 0;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
}

.openapi-import-mobile-card dl {
  display: grid;
  gap: 8px;
  margin: 14px 0;
}

.openapi-import-mobile-card dl > div {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  align-items: center;
  gap: 8px;
}

.openapi-import-mobile-card dt,
.openapi-import-mobile-card dd {
  min-width: 0;
  margin: 0;
  font-size: 11px;
  line-height: 16px;
}

.openapi-import-mobile-card dt {
  color: #94a3b8;
}

.openapi-import-mobile-card dd {
  overflow: hidden;
  color: #334155;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-mobile-detail-button {
  width: 100%;
  min-height: 44px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  color: #334155;
  font-size: 12px;
  font-weight: 700;
}

.openapi-mobile-actions-menu {
  display: grid;
  gap: 4px;
  margin-top: 8px;
  border-top: 1px solid #f1f5f9;
  padding-top: 8px;
}

.openapi-mobile-actions-menu button {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: 8px;
  border: 0;
  border-radius: 6px;
  background: #f8fafc;
  color: #334155;
  padding: 8px 12px;
  font: inherit;
  font-size: 12px;
  text-align: left;
}

.openapi-mobile-actions-menu button.danger {
  background: #fff1f2;
  color: #be123c;
}

.openapi-file-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 12px;
  overflow: hidden;
}

.openapi-file-cell > span:last-child {
  display: block;
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
}

.openapi-file-cell > div {
  display: flex;
  width: 32px;
  height: 32px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 1px solid #dbeafe;
  border-radius: 8px;
  background: #eff6ff;
  color: #2563eb;
  font-size: 12px;
}

.openapi-file-cell strong,
.openapi-file-cell small,
.openapi-file-cell em {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-file-cell strong,
.openapi-file-cell .aw-table-title {
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
  transition: color 0.15s ease;
}

.openapi-file-cell small,
.openapi-file-cell .aw-table-subtitle {
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.8125rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
}

.openapi-file-cell em,
.openapi-file-cell .aw-table-meta {
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-style: normal;
  font-weight: var(--aw-table-meta-weight, 400);
  line-height: 1.35;
}

.openapi-connection-name {
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-weight: var(--aw-table-meta-weight, 400);
  line-height: 1.35;
}

.openapi-count-cell strong,
.openapi-count-cell .aw-table-title {
  color: var(--aw-table-title-color, #111827);
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
}

.openapi-count-cell.ready strong {
  color: #059669;
}

.openapi-count-cell small,
.openapi-count-cell .aw-table-meta {
  margin-left: 2px;
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
}

.openapi-issue-text,
.openapi-time-text {
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  line-height: 1.35;
}

.openapi-issue-text.warning {
  color: #d97706;
  font-weight: 600;
}

.openapi-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.25;
}

.openapi-status-pill > span {
  width: 4px;
  height: 4px;
  border-radius: 999px;
}

.openapi-status-pill.ready {
  background: rgba(209, 250, 229, 0.6);
  color: #047857;
}

.openapi-status-pill.review {
  background: rgba(254, 243, 199, 0.7);
  color: #b45309;
}

.openapi-status-pill .ready,
.openapi-status-pill > span.ready {
  background: #10b981;
}

.openapi-status-pill .review,
.openapi-status-pill > span.review {
  background: #f59e0b;
}

.openapi-empty-state {
  max-width: 320px;
  margin: 0 auto;
  padding: 64px 0;
  text-align: center;
}

.openapi-empty-state.compact {
  padding: 48px 0;
}

.openapi-empty-state > div {
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

.openapi-empty-state.compact > div {
  width: 48px;
  height: 48px;
  margin-bottom: 12px;
  font-size: 18px;
}

.openapi-empty-state h4 {
  margin: 0;
  color: #334155;
  font-size: 14px;
  font-weight: 700;
  line-height: 20px;
}

.openapi-empty-state p {
  margin: 8px 0 0;
  color: #94a3b8;
  font-size: 12px;
  line-height: 18px;
}

.openapi-empty-state.compact p {
  margin-top: 4px;
  font-size: 10px;
  line-height: 14px;
}

.openapi-empty-state .primary-button,
.openapi-empty-state .ghost-button {
  margin-top: 16px;
}

.openapi-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 23, 42, 0.6);
  padding: 16px;
  backdrop-filter: blur(8px);
}

.openapi-modal-card {
  position: relative;
  display: flex;
  flex-direction: column;
  width: min(100%, 672px);
  max-height: 90vh;
  overflow: hidden;
  border: 1px solid #f1f5f9;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 24px 48px -16px rgba(15, 23, 42, 0.34);
  animation: openapiScaleUp 0.25s cubic-bezier(0.25, 0.8, 0.25, 1) forwards;
}

.openapi-detail-modal-card {
  width: min(100%, 920px);
}

.openapi-modal-head {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: linear-gradient(90deg, #020617 0%, #05070c 100%);
  color: #fff;
  padding: 20px;
}

.openapi-modal-head > div {
  display: flex;
  align-items: center;
  gap: 10px;
}

.openapi-modal-head > div > span {
  display: flex;
  width: 32px;
  height: 32px;
  align-items: center;
  justify-content: center;
  border: 1px solid rgba(16, 185, 129, 0.3);
  border-radius: 8px;
  background: rgba(16, 185, 129, 0.1);
  color: #34d399;
}

.openapi-modal-head h3 {
  margin: 0;
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  line-height: 20px;
}

.openapi-modal-head p {
  margin: 0;
  color: #94a3b8;
  font-size: 10px;
  font-weight: 300;
  line-height: 14px;
}

.openapi-modal-head > button {
  display: flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.5);
  color: #94a3b8;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.openapi-modal-head > button:hover {
  background: #1e293b;
  color: #fff;
}

.openapi-modal-body {
  display: flex;
  min-height: 0;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 24px;
}

.openapi-field {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.openapi-field label,
.openapi-field > span {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 700;
  line-height: 14px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.openapi-field input,
.openapi-field textarea,
.openapi-reference-select {
  width: 100%;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #1e293b;
  padding: 10px 12px;
  font-size: 12px;
  line-height: 16px;
  outline: none;
  transition: background-color 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease;
}

.openapi-field input,
.openapi-reference-select {
  min-height: 44px;
}

.openapi-field textarea,
.openapi-field .mono {
  font-family: "Fira Code", ui-monospace, monospace;
}

.openapi-field input::placeholder,
.openapi-field textarea::placeholder {
  color: #94a3b8;
}

.openapi-field input:focus,
.openapi-field textarea:focus {
  border-color: rgba(16, 185, 129, 0.6);
  background: #fff;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.15);
}

.openapi-reference-select {
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  text-align: left;
}

.openapi-reference-select.is-readonly {
  cursor: default;
  background: #f1f5f9;
}

.openapi-reference-select.is-readonly > small {
  flex: 0 0 auto;
  color: #94a3b8;
  font-size: 10px;
}

.openapi-reference-select:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.openapi-reference-select > span {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.openapi-reference-select > span > i {
  color: #94a3b8;
}

.openapi-reference-select > i {
  color: #94a3b8;
  font-size: 10px;
  transition: transform 0.2s ease;
}

.openapi-reference-select > i.open {
  transform: rotate(180deg);
}

.openapi-select-menu {
  position: absolute;
  top: 100%;
  left: 0;
  z-index: 50;
  width: 100%;
  max-height: 160px;
  overflow-y: auto;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.05);
  margin-top: 4px;
  padding: 4px 0;
  animation: openapiScaleUp 0.18s ease forwards;
}

.openapi-select-option {
  display: flex;
  min-height: 44px;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  border: 0;
  background: transparent;
  color: #334155;
  padding: 8px 12px;
  font-size: 12px;
  line-height: 16px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.openapi-select-option:hover,
.openapi-select-option.selected {
  background: rgba(236, 253, 245, 0.6);
  color: #065f46;
}

.openapi-select-option.selected {
  font-weight: 700;
}

.openapi-select-option i {
  color: #10b981;
  font-size: 11px;
}

.openapi-option-copy {
  display: grid;
  min-width: 0;
  gap: 1px;
}

.openapi-option-copy strong,
.openapi-option-copy small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-option-copy strong {
  font-size: 12px;
  line-height: 16px;
}

.openapi-option-copy small {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 400;
  line-height: 14px;
}

.openapi-import-mode-tabs {
  display: flex;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  padding: 2px;
}

.openapi-import-mode-tabs button {
  flex: 1;
  min-height: 44px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #64748b;
  padding: 6px 10px;
  font-size: 10px;
  line-height: 14px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}

.openapi-import-mode-tabs button.active {
  background: #fff;
  color: #1e293b;
  font-weight: 700;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
}

.openapi-file-picker {
  display: grid;
  min-height: 44px;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #1e293b;
  padding: 10px 12px;
  font-size: 12px;
  cursor: pointer;
}

.openapi-file-picker-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #334155;
  font-weight: 600;
}

.openapi-file-picker-name {
  overflow: hidden;
  color: #1e293b;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-file-picker-meta {
  color: #94a3b8;
  font-size: 10px;
}

.openapi-file-input {
  display: none;
}

.openapi-file-error {
  color: #b91c1c;
  font-size: 10px;
  line-height: 15px;
}

.import-drawer-preview {
  border: 1px solid #c7d2fe;
  border-radius: 8px;
  background: #eef2ff;
  color: #4338ca;
  padding: 12px;
}

.import-drawer-preview.unavailable {
  border-color: #fed7aa;
  background: #fff7ed;
  color: #9a3412;
}

.import-drawer-preview.unavailable > div:first-child > i,
.import-drawer-preview.unavailable strong,
.import-drawer-preview.unavailable small,
.import-drawer-preview.unavailable .import-preview-empty {
  color: #9a3412;
}

.import-drawer-preview > div:first-child {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.import-drawer-preview > div:first-child > i {
  margin-top: 2px;
  color: #4f46e5;
  font-size: 12px;
}

.import-drawer-preview strong,
.import-drawer-preview small {
  display: block;
}

.import-drawer-preview strong {
  color: #3730a3;
  font-size: 11px;
  line-height: 16px;
}

.import-drawer-preview small,
.import-preview-empty {
  color: rgba(79, 70, 229, 0.9);
  font-size: 10px;
  font-weight: 300;
  line-height: 15px;
}

.openapi-preview-table {
  margin-top: 10px;
  overflow: hidden;
  border: 1px solid rgba(99, 102, 241, 0.18);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.65);
}

.openapi-preview-table > div {
  display: grid;
  grid-template-columns: 0.8fr 1.6fr 1.4fr 1fr;
  gap: 8px;
  padding: 8px 10px;
}

.openapi-preview-table span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-preview-table > div:first-child {
  background: rgba(99, 102, 241, 0.08);
  color: var(--aw-table-header-color, #6b7280);
  font-size: var(--aw-table-header-size, 0.75rem);
  font-weight: var(--aw-table-header-weight, 600);
}

.openapi-preview-table > div:not(:first-child) {
  border-top: 1px solid rgba(99, 102, 241, 0.12);
  color: var(--aw-table-body-color, #374151);
  font-size: var(--aw-table-body-size, 0.8125rem);
}

.openapi-modal-actions {
  position: sticky;
  bottom: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-top: 1px solid #f1f5f9;
  background: #f8fafc;
  padding: 16px 24px;
}

.openapi-modal-actions > span {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 300;
}

.openapi-modal-actions > div {
  display: flex;
  gap: 8px;
}

.openapi-modal-actions button,
.openapi-detail-actions button {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 0;
  border-radius: 8px;
  padding: 6px 14px;
  font-size: 12px;
  font-weight: 700;
  line-height: 16px;
  cursor: pointer;
}

.openapi-modal-actions button:first-child,
.openapi-detail-actions button:first-child {
  background: transparent;
  color: #334155;
}

.openapi-modal-actions button:last-child,
.openapi-detail-actions button:last-child {
  background: #020617;
  color: #fff;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.1);
}

.openapi-modal-actions button.danger {
  background: #be123c;
  color: #fff;
}

.openapi-modal-actions button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.openapi-detail-hero {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 20px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  padding: 16px;
}

.openapi-detail-modal-body {
  min-height: 0;
  overflow-y: auto;
}

.openapi-detail-hero > i {
  display: flex;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 1px solid #dbeafe;
  border-radius: 10px;
  background: #eff6ff;
  color: #2563eb;
}

.openapi-detail-hero > div {
  min-width: 0;
  flex: 1 1 auto;
}

.openapi-detail-hero strong,
.openapi-detail-hero small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-detail-hero strong {
  color: #0f172a;
  font-size: 14px;
  line-height: 20px;
}

.openapi-detail-hero small {
  margin-top: 2px;
  color: #64748b;
  font-size: 11px;
  line-height: 16px;
}

.openapi-detail-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin: 0 20px 20px;
}

.openapi-detail-grid .config-summary-item {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
  padding: 12px;
}

.openapi-detail-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  border-top: 1px solid #f1f5f9;
  background: #f8fafc;
  padding: 16px 20px;
}

.action-toast {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 3200;
  display: flex;
  max-width: min(420px, calc(100vw - 48px));
  align-items: center;
  gap: 12px;
  border-radius: 12px;
  background: #020617;
  color: #fff;
  padding: 10px 12px 10px 16px;
  font-size: 12px;
  box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.2);
}

.action-toast button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 0;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
  cursor: pointer;
}

.openapi-confirm-modal-card {
  width: min(100%, 520px);
}

.openapi-confirm-body {
  min-height: 0;
  overflow-y: auto;
  padding: 24px;
}

.openapi-confirm-body strong {
  display: block;
  overflow: hidden;
  color: #0f172a;
  font-size: 14px;
  line-height: 20px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.openapi-confirm-body p {
  margin: 8px 0 0;
  color: #475569;
  font-size: 12px;
  line-height: 18px;
}

@keyframes openapiScaleUp {
  from {
    opacity: 0;
    transform: scale(0.96);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

@media (max-width: 900px) {
  .openapi-page-header,
  .openapi-modal-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .openapi-detail-grid {
    grid-template-columns: 1fr;
  }
}
</style>
