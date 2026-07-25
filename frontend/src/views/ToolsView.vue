<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import ToolContractHybridEditor from "../components/ToolContractHybridEditor.vue";
import ToolFlatContractEditor from "../components/ToolFlatContractEditor.vue";
import ToolSchemaTreeView from "../components/ToolSchemaTreeView.vue";
import ToolTestDialog from "../components/ToolTestDialog.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useModalFocus } from "../composables/useModalFocus";
import { useIntegrationStore } from "../stores/integration";
import {
  buildBodyContractFromRequestParams,
  buildRequestParamsFromContracts,
  buildResponseContractFromFields,
} from "../utils/tool-schema-json";
import {
  buildToolPublishChecklist,
  checklistHasBlockingErrors,
  checklistHasWarnings,
  getToolLifecycleStatus,
  getToolRunStatus,
  getToolTestStatus,
  getToolUnifiedStatus,
  hasPassingToolTest,
  toolHasConnectionAttention,
} from "../utils/tool-governance";
import { getToolProtocolLabel, getToolTypeLabel } from "../utils/tool-presentation";
import { useWorkspaceStore } from "../stores/workspaces";
import type { Tool, ToolErrorMapping, ToolListQuery, ToolRequestParam, ToolResponseField, ToolSchemaNode, ToolSchemaNodeType } from "../types/domain";
import { buildHTTPActionConfig, HTTP_ACTION_SCHEMA_VERSION } from "../utils/tool-http-action";

type ToolStatus = Tool["status"];
type ToolEditorMode = "create" | "edit";
type ContractEditorTab = "Path" | "Query" | "Header" | "Body" | "Response" | "Errors";
/** Includes store-side attention filter for connection-health issues. */
type ToolStatusFilter = "all" | NonNullable<ToolListQuery["status"]>;
type ToolTypeFilter = "all" | NonNullable<ToolListQuery["type"]>;
type DetailTabId = "base" | "connection" | "request" | "response" | "runtime" | "test";
type RiskActionType = "disable" | "enable" | "delete" | "";

interface ToolDraft {
  id: string;
  name: string;
  workspaceId: string;
  connectionId: string;
  method: string;
  path: string;
  contentType: string;
  description: string;
  status: ToolStatus;
  requestContract: ToolSchemaNode[];
  responseContract: ToolSchemaNode[];
  errorMappings: ToolErrorMapping[];
  timeoutSeconds: number;
  retryCount: number;
  backoffPolicy: string;
  idempotencyPolicy: string;
  rateLimitPolicy: string;
}

const integration = useIntegrationStore();
const workspaces = useWorkspaceStore();
const router = useRouter();

const query = ref("");
const selectedStatusFilter = ref<ToolStatusFilter>("all");
const selectedToolTypeFilter = ref<ToolTypeFilter>("all");
const selectedToolRowKeys = ref<Array<string | number>>([]);
const selectedToolId = ref("");
const detailToolId = ref("");
const detailTab = ref<DetailTabId>("base");
const toolDetailVisible = ref(false);
const toolDetailModalRef = ref<HTMLElement | null>(null);
const toolEditorVisible = ref(false);
const toolEditorModalRef = ref<HTMLElement | null>(null);
const draftStep = ref(1);
const toolEditorMode = ref<ToolEditorMode>("create");
const editingToolId = ref("");
const actionNote = ref("");
const actionNoteTone = ref<"success" | "error">("success");
const draftError = ref("");
const saveState = ref<"idle" | "saving" | "saved" | "failed" | "dirty">("idle");
const draftSnapshot = ref("");
const publishImpactConfirmed = ref(false);
const testDialogVisible = ref(false);
const testDialogTool = ref<Tool | null>(null);
const contractEditorTab = ref<ContractEditorTab>("Body");
const runtimeAdvancedOpen = ref(false);
const riskConfirmationVisible = ref(false);
const riskConfirmationModalRef = ref<HTMLElement | null>(null);
const pendingRiskAction = ref<{ type: RiskActionType; tool: Tool | null }>({ type: "", tool: null });

const toolEditorSteps = [
  ["基础与接口", "归属、连接与 Endpoint"],
  ["契约配置", "请求、响应与错误"],
  ["确认保存", "检查后保存草稿"],
];

const contractEditorTabs: Array<{ value: ContractEditorTab; label: string }> = [
  { value: "Path", label: "Path" },
  { value: "Query", label: "Query" },
  { value: "Header", label: "Header" },
  { value: "Body", label: "Body" },
  { value: "Response", label: "Response" },
  { value: "Errors", label: "Errors" },
];

const detailTabs: Array<{ id: DetailTabId; label: string; icon: string }> = [
  { id: "base", label: "基础信息", icon: "fa-solid fa-circle-info" },
  { id: "connection", label: "连接配置", icon: "fa-solid fa-server" },
  { id: "request", label: "入参配置", icon: "fa-solid fa-list-check" },
  { id: "response", label: "出参配置", icon: "fa-solid fa-code" },
  { id: "runtime", label: "运行策略", icon: "fa-solid fa-sliders" },
  { id: "test", label: "测试发布", icon: "fa-solid fa-vial" },
];

const methodOptions = ["GET", "POST", "PATCH", "DELETE"].map((method) => ({ label: method, value: method }));
const contentTypeOptions = ["application/json", "application/x-www-form-urlencoded", "multipart/form-data"].map((contentType) => ({
  label: contentType,
  value: contentType,
}));
const backoffPolicyOptions = [
  { label: "固定间隔", value: "fixed", description: "每次重试之间等待固定时长，适合稳定接口。" },
  { label: "指数退避", value: "exponential", description: "每次失败后逐步拉长等待时间，适合限流或拥塞场景。" },
  { label: "线性退避", value: "linear", description: "按固定增量拉长每次等待时间，策略温和。" },
];
const rateLimitPolicyOptions = [
  { label: "标准频率", value: "60 rpm", description: "每分钟最多 60 次，适合大多数普通业务接口。" },
  { label: "保守频率", value: "30 rpm", description: "每分钟最多 30 次，适合成本高或下游较脆弱的接口。" },
  { label: "高频率", value: "120 rpm", description: "每分钟最多 120 次，适合读多写少且稳定的接口。" },
];
const toolStatusOptions: Array<{ label: string; value: ToolStatus }> = [
  { label: "草稿", value: "Draft" },
  { label: "待评审", value: "Review" },
  { label: "已测试", value: "Tested" },
  { label: "已发布", value: "Published" },
  { label: "已停用", value: "Disabled" },
];
const toolStatusHelperText = "状态由测试、发布与停用动作驱动；修改运行配置后，系统会按后端规则自动回退。";
const statusTabs: Array<{ label: string; value: ToolStatusFilter }> = [
  { label: "全部状态", value: "all" },
  { label: "连接异常", value: "attention" },
  { label: "已发布", value: "Published" },
  { label: "已测试", value: "Tested" },
  { label: "待评审", value: "Review" },
  { label: "草稿", value: "Draft" },
  { label: "已停用", value: "Disabled" },
];
const toolTypeTabs: Array<{ label: string; value: ToolTypeFilter }> = [
  { label: "全部类型", value: "all" },
  { label: "HTTP Tool", value: "HTTP Tool" },
  { label: "Workflow Tool", value: "Workflow Tool" },
];

const draftTool = ref<ToolDraft>(defaultToolDraft());

const hasToolRecords = computed(() => integration.tools.length > 0);
/** Tools whose bound connection is missing, unverified, failed, or expiring. */
const connectionIssueTools = computed(() =>
  integration.tools.filter((tool) => toolHasConnectionAttention(tool, connectionForTool(tool))),
);
const publishedWithConnectionIssueCount = computed(
  () =>
    connectionIssueTools.value.filter((tool) => tool.status === "Published").length,
);
const toolSummaryItems = computed<ManagementSummaryItem[]>(() => {
  const tools = integration.tools;
  const publishedCount = tools.filter((tool) => tool.status === "Published").length;
  const pendingPublishCount = tools.filter((tool) => tool.status === "Tested").length;
  const connectionIssueCount = connectionIssueTools.value.length;
  const publishedIssues = publishedWithConnectionIssueCount.value;
  return [
    { label: "工具总数", value: tools.length, icon: "fa-solid fa-screwdriver-wrench" },
    {
      label: "已发布",
      value: publishedCount,
      icon: "fa-solid fa-circle-check",
      note: publishedIssues > 0 ? `${publishedIssues} 连接异常` : undefined,
      tone: publishedIssues > 0 ? "warning" : "default",
    },
    {
      label: "待发布",
      value: pendingPublishCount,
      icon: "fa-solid fa-vial",
      tone: "info",
    },
    {
      // Same definition as the connection alert + table attention filter (not lifecycle Review/Disabled).
      label: "需处理",
      value: connectionIssueCount,
      icon: "fa-solid fa-triangle-exclamation",
      note: connectionIssueCount > 0 ? "连接异常" : undefined,
      tone: connectionIssueCount > 0 ? "danger" : "warning",
    },
  ];
});
const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));
const workspaceOptions = computed(() =>
  workspaces.items.map((workspace) => ({
    label: `${workspace.name} (${workspace.displayName})`,
    value: workspace.id,
  })),
);
const selectedTool = computed(() => integration.tools.find((tool) => tool.id === selectedToolId.value) || integration.tools[0]);
const detailTool = computed(() => integration.tools.find((tool) => tool.id === detailToolId.value) || selectedTool.value);
const detailConnection = computed(() => (detailTool.value ? connectionForTool(detailTool.value) : undefined));
const draftConnection = computed(() =>
  connectionById(draftTool.value.connectionId, draftTool.value.workspaceId)
  || connectionsForWorkspace(draftTool.value.workspaceId)[0],
);
const detailRequestContract = computed(() => buildBodyContractFromRequestParams(detailTool.value?.requestParams || []));
const detailResponseNodes = computed(() => buildResponseContractFromFields(detailTool.value?.responseFields || []));
const toolEditorTitle = computed(() => (toolEditorMode.value === "edit" ? "编辑 Tool" : "注册 Tool"));
const toolColumns = computed<ManagementListColumn<Tool>[]>(() => [
  { key: "tool", label: "工具名称", width: 200, sortable: true, sortKey: "name", getValue: (tool) => `${tool.name} ${tool.description}` },
  { key: "type", label: "工具类型", width: 95, hidable: true, sortable: true, sortKey: "protocol", getValue: getToolTypeLabel },
  { key: "protocol", label: "协议类型", width: 95, hidable: true, getValue: toolProtocolLabel },
  { key: "method", label: "Method", width: 70, hidable: true, align: "center", sortable: true, getValue: methodOf },
  { key: "path", label: "Path", width: 170, hidable: true, getValue: pathOf },
  { key: "connection", label: "Provider / 服务连接", width: 140, hidable: true, getValue: toolProviderConnectionLabel },
  { key: "status", label: "状态", width: 140, hidable: true, align: "center", sortable: true, sortKey: "status", getValue: (tool) => toolUnifiedStatus(tool).label },
  { key: "version", label: "版本", width: 80, hidable: true, getValue: toolVersionLabel },
  { key: "updatedAt", label: "更新时间", width: 125, hidable: true, sortable: true, sortKey: "updatedAt", getValue: formatToolTableUpdatedAt },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
]);
const hasUnsavedToolChanges = computed(() => toolEditorVisible.value && draftSnapshot.value !== serializeDraftForSnapshot());
const saveStateLabel = computed(() => {
  if (saveState.value === "saving") return "保存中";
  if (saveState.value === "saved") return "已保存";
  if (saveState.value === "failed") return "保存失败";
  if (hasUnsavedToolChanges.value) return "有未保存修改";
  return "草稿未保存";
});
const draftPublishChecklist = computed(() => buildToolPublishChecklist(buildDraftTool(), draftConnection.value, {
  agentImpactConfirmed: publishImpactConfirmed.value,
}));
const draftChecklistHasBlockingErrors = computed(() => checklistHasBlockingErrors(draftPublishChecklist.value));
const draftChecklistHasWarnings = computed(() => checklistHasWarnings(draftPublishChecklist.value));
const canPublishDraftTool = computed(() => !draftChecklistHasBlockingErrors.value);
const serviceConnectionOptions = computed(() => {
  const connections = connectionsForWorkspace(draftTool.value.workspaceId);
  if (!connections.length) {
    return [{ label: "暂无服务连接", value: "", disabled: true }];
  }
  return connections.map((connection) => ({
    label: `${connection.name} · ${connection.protocolConfig.domain}`,
    value: connection.id,
  }));
});
const requestTransportContract = computed({
  get: () => draftTool.value.requestContract.filter((node) => ["Path", "Query", "Header"].includes(node.location || "")),
  set: (nextNodes: ToolSchemaNode[]) => {
    draftTool.value.requestContract = [...nextNodes, ...requestBodyContract.value];
  },
});
const requestBodyContract = computed({
  get: () => draftTool.value.requestContract.filter((node) => !["Path", "Query", "Header"].includes(node.location || "")),
  set: (nextNodes: ToolSchemaNode[]) => {
    draftTool.value.requestContract = [
      ...requestTransportContract.value,
      ...nextNodes.map((node) => ({
        ...node,
        location: node.location || "Body",
      })),
    ];
  },
});
const responseBodyContract = computed({
  get: () => draftTool.value.responseContract,
  set: (nextNodes: ToolSchemaNode[]) => {
    draftTool.value.responseContract = nextNodes;
  },
});
const activeRequestFlatContract = computed({
  get: () => draftTool.value.requestContract.filter((node) => node.location === contractEditorTab.value),
  set: (nextNodes: ToolSchemaNode[]) => {
    const activeLocation = contractEditorTab.value;
    if (!(["Path", "Query", "Header"] as ContractEditorTab[]).includes(activeLocation)) return;
    draftTool.value.requestContract = [
      ...draftTool.value.requestContract.filter((node) => node.location !== activeLocation),
      ...nextNodes.map((node) => ({ ...node, location: activeLocation })),
    ];
  },
});
const requestContractCount = computed(() => countSchemaNodes(draftTool.value.requestContract));
const responseContractCount = computed(() => countSchemaNodes(draftTool.value.responseContract));
const completedBaseRequiredCount = computed(() => [
  draftTool.value.name.trim(),
  draftTool.value.workspaceId,
  draftTool.value.connectionId,
  draftTool.value.method,
  draftTool.value.path.trim().startsWith("/") ? draftTool.value.path : "",
].filter(Boolean).length);
const draftSuggestionCount = computed(() => Number(requestContractCount.value === 0) + Number(responseContractCount.value === 0));
const draftCompletionPercent = computed(() => Math.min(100,
  Math.round((completedBaseRequiredCount.value / 5) * 70)
  + (requestContractCount.value > 0 ? 20 : 0)
  + (responseContractCount.value > 0 ? 10 : 0),
));

useModalFocus({
  visible: () => toolDetailVisible.value,
  modalRef: toolDetailModalRef,
  onClose: closeToolDetail,
});

useModalFocus({
  visible: () => toolEditorVisible.value,
  modalRef: toolEditorModalRef,
  onClose: closeToolEditor,
});

useModalFocus({
  visible: () => riskConfirmationVisible.value,
  modalRef: riskConfirmationModalRef,
  onClose: closeRiskConfirmation,
});

onMounted(async () => {
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (hasWorkspaceContext.value) await loadToolPageAssets();
  } catch {
    // The shared Workspace state provides recovery actions when bootstrap fails.
  }
});

async function loadToolPageAssets() {
  await loadToolRegistry({ page: 1 });
  selectedToolId.value = integration.tools[0]?.id || "";
}

watch(
  () => integration.tools.length,
  () => {
    if (!integration.tools.some((tool) => tool.id === selectedToolId.value)) {
      selectedToolId.value = integration.tools[0]?.id || "";
    }
    if (detailToolId.value && !integration.tools.some((tool) => tool.id === detailToolId.value)) {
      closeToolDetail();
    }
  },
);
watch(
  draftTool,
  () => {
    if (toolEditorVisible.value && saveState.value !== "saving" && saveState.value !== "failed") {
      saveState.value = hasUnsavedToolChanges.value ? "dirty" : saveState.value;
    }
  },
  { deep: true },
);

function defaultToolDraft(): ToolDraft {
  const workspaceId = workspaces.activeWorkspaceId || workspaces.items[0]?.id || "default";
  return {
    id: "",
    name: "拦截发货",
    workspaceId,
    connectionId: integration.serviceConnections[0]?.id || "",
    method: "POST",
    path: "/api/shipments/{shipmentId}/intercept",
    contentType: "application/json",
    description: "对未出库发货单执行拦截动作",
    status: "Draft",
    requestContract: [],
    responseContract: [],
    errorMappings: [],
    timeoutSeconds: 8,
    retryCount: 2,
    backoffPolicy: "exponential",
    idempotencyPolicy: "header: Idempotency-Key",
    rateLimitPolicy: "60 rpm",
  };
}

function newDraftRowId(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function normalizeSchemaNode(node: Partial<ToolSchemaNode>): ToolSchemaNode {
  return {
    id: node.id || newDraftRowId("schema"),
    name: node.name || "",
    type: (node.type as ToolSchemaNodeType) || "string",
    description: node.description || "",
    required: node.required ?? true,
    location: node.location,
    format: node.format,
    nullable: node.nullable,
    example: node.example,
    enumValues: node.enumValues || [],
    valueSource: node.valueSource,
    defaultValue: node.defaultValue,
    children: (node.children || []).map((child) => normalizeSchemaNode(child)),
    item: node.item ? normalizeSchemaNode(node.item) : null,
    additionalProperties: node.additionalProperties ? normalizeSchemaNode(node.additionalProperties) : null,
  };
}

function requestParamToSchemaNode(param: ToolRequestParam): ToolSchemaNode {
  return normalizeSchemaNode(param.schema || {
    id: newDraftRowId("request"),
    location: param.location,
    name: param.name,
    type: (param.type as ToolSchemaNodeType) || "string",
    required: param.required,
    description: param.description,
    valueSource: param.valueSource,
    defaultValue: param.defaultValue,
  });
}

function responseFieldToSchemaNode(field: ToolResponseField): ToolSchemaNode {
  return normalizeSchemaNode(field.schema || {
    id: newDraftRowId("response"),
    name: field.name,
    type: (field.type as ToolSchemaNodeType) || "string",
    required: true,
    description: field.description,
  });
}

function connectionsForWorkspace(workspaceId: string) {
  const scoped = integration.toolConnectionsByWorkspace?.[workspaceId];
  if (scoped) return scoped;
  return workspaceId === workspaces.activeWorkspaceId ? integration.serviceConnections : [];
}

function connectionById(connectionId: string, workspaceId = workspaces.activeWorkspaceId) {
  return connectionsForWorkspace(workspaceId).find((connection) => connection.id === connectionId);
}

function connectionForTool(tool: Tool) {
  return connectionById(tool.connectionId, tool.workspaceId);
}

function providerForTool(tool: Tool) {
  return integration.providers.find((provider) => provider.id === tool.providerId);
}

function toolProtocolLabel(tool: Tool) {
  return getToolProtocolLabel(tool, providerForTool(tool));
}

function toolProviderConnectionLabel(tool: Tool) {
  const provider = providerForTool(tool)?.name || tool.providerId || "-";
  const connection = connectionForTool(tool)?.name || "连接缺失";
  return `${provider} · ${connection}`;
}

function workspaceLabel(workspaceId: string) {
  const workspace = workspaces.items.find((item) => item.id === workspaceId);
  return workspace ? workspace.name : workspaceId;
}

function workspaceDisplayLabel(workspaceId: string) {
  const workspace = workspaces.items.find((item) => item.id === workspaceId);
  if (!workspace) return workspaceId;
  return `${workspace.name} (${workspace.displayName})`;
}

function methodOf(tool: Tool) {
  return String(tool.actionConfig.method || "GET");
}

function pathOf(tool: Tool) {
  return String(tool.actionConfig.path || "/");
}

function methodClass(tool: Tool) {
  return methodOf(tool).toLowerCase();
}

function statusClass(status: string) {
  return status.toLowerCase().replace(/\s+/g, "-");
}

function toolStatusLabel(status: ToolStatus) {
  return toolStatusOptions.find((option) => option.value === status)?.label || status;
}

function lifecycleStatus(tool: Tool) {
  return getToolLifecycleStatus(tool);
}

function testStatus(tool: Tool) {
  return getToolTestStatus(tool);
}

function runStatus(tool: Tool) {
  return getToolRunStatus(tool, connectionForTool(tool));
}

function governanceToneClass(tone: string) {
  return `tone-${tone}`;
}

function toolUnifiedStatus(tool: Tool) {
  return getToolUnifiedStatus(tool, connectionForTool(tool));
}

function latestTool(tool: Tool) {
  return integration.tools.find((item) => item.id === tool.id) || tool;
}

function setActionFeedback(message: string, tone: "success" | "error" = "success") {
  actionNote.value = message;
  actionNoteTone.value = tone;
}

function hasPassingTest(tool?: Tool | null) {
  return Boolean(tool && hasPassingToolTest(tool));
}

function canPublishTool(tool?: Tool | null) {
  return Boolean(tool && hasPassingTest(tool) && tool.status !== "Published");
}

function toolPublishActionLabel(tool?: Tool | null) {
  if (!tool) return "发布工具";
  return tool.status === "Disabled" ? "重新发布" : "发布工具";
}

function toolPublishButtonLabel(tool?: Tool | null) {
  if (!tool) return "发布上线";
  return tool.status === "Disabled" ? "重新发布" : "发布上线";
}

function toolAvailabilityActionLabel(tool?: Tool | null) {
  return tool?.status === "Disabled" ? "启用工具" : "停用工具";
}

function toolAvailabilityButtonLabel(tool?: Tool | null) {
  return tool?.status === "Disabled" ? "启用" : "停用";
}

function toolAvailabilityActionIcon(tool?: Tool | null) {
  return tool?.status === "Disabled" ? "fa-solid fa-play" : "fa-solid fa-ban";
}

function toolLastTestSummary(tool?: Tool | null) {
  if (!tool?.lastTestResult) return "等待测试";
  const latency = tool.lastTestResult.latencyMs ? ` · ${tool.lastTestResult.latencyMs}ms` : "";
  return hasPassingTest(tool) ? `已通过${latency}` : `未通过${latency}`;
}

function toolLastTestDetail(tool?: Tool | null) {
  const result = tool?.lastTestResult;
  if (!result) return "暂无测试记录，请先执行测试。";
  if (hasPassingTest(tool)) return "连通性、响应 Schema、错误映射与运行策略均已通过。";

  const failedChecks = [
    result.connectivityPassed === false ? "连通性未通过" : "",
    result.responseSchemaPassed === false ? "响应 Schema 未通过" : "",
    result.errorMappingPassed === false ? "错误映射未通过" : "",
    result.runtimePolicyPassed === false ? "运行策略未通过" : "",
  ].filter(Boolean);

  if (failedChecks.length) {
    return `失败项：${failedChecks.join("、")}。请打开测试弹窗查看下游响应并修复后重试。`;
  }
  return "测试未通过，请打开测试弹窗查看下游响应与错误详情。";
}

function toolPublishReadinessLabel(tool?: Tool | null) {
  if (!tool) return "发布前需先执行通过测试。";
  if (canPublishTool(tool)) {
    return tool.status === "Disabled" ? "当前已停用，但仍可直接重新发布。" : "最近一次测试已通过，可直接发布。";
  }
  if (tool.status === "Published") {
    return "当前已发布，可被 Agent 调用。";
  }
  if (tool.status === "Disabled") {
    return "当前已停用，启用后恢复为待评审；若需对 Agent 开放，请先重新测试并发布。";
  }
  return "发布前需先执行通过测试。";
}

function authModeLabel(connection = detailConnection.value) {
  if (!connection) return "-";
  return connection.authConfig.label || connection.authConfig.mode || "-";
}

function connectionDomainLabel(connection = detailConnection.value) {
  if (!connection) return "-";
  if (connection.protocolConfig.domain) {
    return connection.protocolConfig.domain;
  }
  const host = connection.protocolConfig.host || "";
  const port = connection.protocolConfig.port ? `:${connection.protocolConfig.port}` : "";
  return host ? `${host}${port}` : "-";
}

function connectionBasePathLabel(connection = detailConnection.value) {
  if (!connection) return "-";
  return connection.protocolConfig.basePath || "/";
}

function endpointPreviewLabel() {
  const domain = connectionDomainLabel(draftConnection.value);
  const basePath = connectionBasePathLabel(draftConnection.value).replace(/\/$/, "");
  const actionPath = (draftTool.value.path || "/").startsWith("/") ? draftTool.value.path || "/" : `/${draftTool.value.path}`;
  if (!basePath || basePath === "/" || actionPath === basePath || actionPath.startsWith(`${basePath}/`)) {
    return `${domain}${actionPath}`;
  }
  return `${domain}${basePath}${actionPath}`;
}

function serviceConnectionStatusLabel(connection = detailConnection.value) {
  if (!connection) return "未配置";
  if (connection.status === "Available" || connection.status === "VERIFIED") return "可用";
  if (connection.status === "Expiring soon") return "即将过期";
  if (["Needs attention", "UNVERIFIED", "ERROR"].includes(connection.status)) return "需处理";
  if (connection.status === "DISABLED") return "已停用";
  return connection.status;
}

function environmentLabel(value: string) {
  return value || "-";
}

function backoffPolicyMeta(policy: string) {
  return backoffPolicyOptions.find((option) => option.value === policy) || { label: policy || "-", description: "" };
}

function rateLimitPolicyMeta(policy: string) {
  return rateLimitPolicyOptions.find((option) => option.value === policy) || { label: policy || "-", description: "" };
}

function paramsReady(tool: Tool) {
  return `${tool.requestParams.length}/${Math.max(tool.requestParams.length, tool.responseFields.length)}`;
}

function timeoutLabel(tool: Tool) {
  return `${Math.round(tool.runtimePolicy.timeoutMs / 1000)}s`;
}

function retryLabel(tool: Tool) {
  return `${tool.runtimePolicy.retryCount} 次重试`;
}

function toolSummaryMeta(tool: Tool) {
  const workspace = workspaceDisplayLabel(tool.workspaceId);
  const connection = connectionForTool(tool)?.name || tool.connectionId || "-";
  return `${workspace} · ${connection} · ${timeoutLabel(tool)} / ${retryLabel(tool)} · ${toolVersionLabel(tool)}`;
}

function toolVersionLabel(tool: Tool) {
  const version = tool.draftVersion || [...tool.versions].sort((left, right) => right.versionNo - left.versionNo)[0];
  return version ? `v${version.versionNo}` : "未创建版本";
}

function toolEndpointSummary(tool: Tool) {
  return `${methodOf(tool)} ${pathOf(tool)}`;
}

function agentImpactLabel(tool: Tool) {
  return tool.activeReleaseId ? "可通过 Capability Binding 使用" : "尚未发布 Release";
}

function formatToolTableUpdatedAt(tool: Tool) {
  if (!tool.updatedAt) return "暂无数据";
  const date = new Date(tool.updatedAt);
  if (Number.isNaN(date.getTime())) return "暂无数据";
  return date.toLocaleString("sv-SE", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function serializeDraftForSnapshot() {
  return JSON.stringify(draftTool.value);
}

function selectStatusFilter(status: ToolStatusFilter) {
  selectedStatusFilter.value = status;
  selectedToolRowKeys.value = [];
  void loadToolRegistry({ status: status === "all" ? undefined : status, page: 1 });
}

function selectToolTypeFilter(type: ToolTypeFilter) {
  selectedToolTypeFilter.value = type;
  selectedToolRowKeys.value = [];
  void loadToolRegistry({ type: type === "all" ? undefined : type, page: 1 });
}

function resetFilters() {
  query.value = "";
  selectedStatusFilter.value = "all";
  selectedToolTypeFilter.value = "all";
  selectedToolRowKeys.value = [];
  void loadToolRegistry({ query: "", status: undefined, type: undefined, page: 1 });
}

function selectDetailTab(tabId: DetailTabId) {
  detailTab.value = tabId;
}

function handleDetailTabKeydown(event: KeyboardEvent, currentTabId: DetailTabId) {
  const currentIndex = detailTabs.findIndex((tab) => tab.id === currentTabId);
  if (currentIndex < 0) return;

  const lastIndex = detailTabs.length - 1;
  const nextIndexByKey: Record<string, number> = {
    ArrowLeft: currentIndex === 0 ? lastIndex : currentIndex - 1,
    ArrowRight: currentIndex === lastIndex ? 0 : currentIndex + 1,
    Home: 0,
    End: lastIndex,
  };
  const nextIndex = nextIndexByKey[event.key];
  if (nextIndex === undefined) return;

  event.preventDefault();
  const nextTabId = detailTabs[nextIndex].id;
  selectDetailTab(nextTabId);
  void nextTick(() => document.getElementById(`tool-detail-tab-${nextTabId}`)?.focus());
}

function closeFloatingMenus() {
}

function toolMenuActions(tool: Tool): ManagementRowAction[] {
  const publishable = canPublishTool(tool);
  return [
    { key: "detail", label: "查看工具详情", icon: "fa-solid fa-eye", tone: "primary" },
    { key: "test", label: "测试工具", icon: "fa-solid fa-vial" },
    { key: "edit", label: "编辑工具", icon: "fa-solid fa-pen" },
    {
      key: "publish",
      label: toolPublishActionLabel(tool),
      icon: "fa-solid fa-cloud-arrow-up",
      tone: "primary",
      disabled: !publishable,
      disabledReason: publishable ? undefined : "需先通过测试才能发布",
    },
    { key: "availability", label: toolAvailabilityActionLabel(tool), icon: toolAvailabilityActionIcon(tool) },
    { key: "delete", label: "删除工具", icon: "fa-solid fa-trash", tone: "danger" },
  ];
}

function handleToolRowAction(actionKey: string, tool: Tool) {
  if (actionKey === "detail") {
    openToolDetail(tool);
    return;
  }
  if (actionKey === "test") {
    openToolTestDialog(tool);
    return;
  }
  if (actionKey === "edit") {
    openEditTool(tool);
    return;
  }
  if (actionKey === "publish") {
    void publishTool(tool);
    return;
  }
  if (actionKey === "availability") {
    openRiskConfirmation(tool.status === "Disabled" ? "enable" : "disable", tool);
    return;
  }
  if (actionKey === "delete") openRiskConfirmation("delete", tool);
}

function loadToolRegistry(overrides: ToolListQuery = {}) {
  return integration.loadToolPage({
    query: overrides.query ?? query.value,
    status: overrides.status ?? (selectedStatusFilter.value === "all" ? undefined : selectedStatusFilter.value),
    type: overrides.type ?? (selectedToolTypeFilter.value === "all" ? undefined : selectedToolTypeFilter.value),
    page: overrides.page ?? integration.toolPagination.page,
    pageSize: overrides.pageSize ?? integration.toolPagination.pageSize,
    ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
  });
}

function setToolSearch(value: string) {
  query.value = value;
  selectedToolRowKeys.value = [];
  void loadToolRegistry({ query: value, page: 1 });
}

function changeToolPage(pagination: { page: number; pageSize: number }) {
  selectedToolRowKeys.value = [];
  void loadToolRegistry(pagination);
}

function changeToolSort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
  void loadToolRegistry({
    page: 1,
    pageSize: integration.toolPagination.pageSize,
    sortBy: sort.sortBy ?? "",
    sortOrder: sort.sortOrder,
  });
}

function openToolDetail(tool: Tool) {
  closeFloatingMenus();
  selectedToolId.value = tool.id;
  detailToolId.value = tool.id;
  toolDetailVisible.value = true;
}

function closeToolDetail() {
  toolDetailVisible.value = false;
  detailToolId.value = "";
}

function openToolTestDialog(tool: Tool) {
  closeFloatingMenus();
  testDialogTool.value = tool;
  testDialogVisible.value = true;
}

async function deleteTool(tool: Tool) {
  closeFloatingMenus();
  await integration.deleteTool(tool.id);
  const page = integration.toolPageItems.length === 0 && integration.toolPagination.page > 1
    ? integration.toolPagination.page - 1
    : integration.toolPagination.page;
  await loadToolRegistry({ page });
  if (selectedToolId.value === tool.id) {
    selectedToolId.value = integration.tools[0]?.id || "";
  }
  if (detailToolId.value === tool.id) {
    closeToolDetail();
  }
  setActionFeedback(`${tool.name} 已从 Tool Runtime 删除。`);
}

function openRiskConfirmation(type: RiskActionType, tool: Tool) {
  closeFloatingMenus();
  pendingRiskAction.value = { type, tool };
  riskConfirmationVisible.value = true;
}

function closeRiskConfirmation() {
  riskConfirmationVisible.value = false;
  pendingRiskAction.value = { type: "", tool: null };
}

function riskConfirmationTitle() {
  const action = pendingRiskAction.value.type;
  if (action === "delete") return "确认删除 Tool";
  if (action === "disable") return "确认停用 Tool";
  if (action === "enable") return "确认启用 Tool";
  return "确认操作";
}

function riskConfirmationPrimaryLabel() {
  const action = pendingRiskAction.value.type;
  if (action === "delete") return "确认删除";
  if (action === "disable") return "确认停用";
  if (action === "enable") return "确认启用";
  return "确认";
}

async function confirmRiskAction() {
  const { type, tool } = pendingRiskAction.value;
  if (!tool || !type) return;
  if (type === "delete") {
    await deleteTool(tool);
  } else if (type === "disable") {
    await disableTool(tool);
  } else if (type === "enable") {
    await enableTool(tool);
  }
  closeRiskConfirmation();
}

function openCreateTool() {
  toolEditorMode.value = "create";
  editingToolId.value = "";
  draftStep.value = 1;
  actionNote.value = "";
  draftError.value = "";
  contractEditorTab.value = "Body";
  runtimeAdvancedOpen.value = false;
  draftTool.value = defaultToolDraft();
  draftSnapshot.value = JSON.stringify(draftTool.value);
  saveState.value = "idle";
  publishImpactConfirmed.value = false;
  toolEditorVisible.value = true;
}

function buildDraftFromTool(tool: Tool): ToolDraft {
  return {
    id: tool.id,
    name: tool.name,
    workspaceId: tool.workspaceId,
    connectionId: tool.connectionId,
    method: String(tool.actionConfig.method || "GET"),
    path: String(tool.actionConfig.path || "/"),
    contentType: String(tool.actionConfig.contentType || "application/json"),
    description: tool.description,
    status: tool.status,
    requestContract: tool.requestParams.length
      ? tool.requestParams.map((param) => requestParamToSchemaNode(param))
      : [normalizeSchemaNode({ location: "Body", name: "", type: "string", required: true, description: "" })],
    responseContract: tool.responseFields.length
      ? tool.responseFields.map((field) => responseFieldToSchemaNode(field))
      : [normalizeSchemaNode({ name: "", type: "string", required: true, description: "" })],
    errorMappings: tool.errorMappings.map((mapping) => ({ ...mapping })),
    timeoutSeconds: Math.max(1, Math.round(tool.runtimePolicy.timeoutMs / 1000)),
    retryCount: tool.runtimePolicy.retryCount,
    backoffPolicy: tool.runtimePolicy.backoffPolicy,
    idempotencyPolicy: tool.runtimePolicy.idempotencyPolicy,
    rateLimitPolicy: tool.runtimePolicy.rateLimitPolicy,
  };
}

function openEditTool(tool: Tool) {
  if (tool.status === "Published") {
    setActionFeedback("已发布 Tool 的编辑会从该版本创建新的 Draft Version，原 Release 保持不变。", "success");
  }
  toolEditorMode.value = "edit";
  editingToolId.value = tool.id;
  draftStep.value = 1;
  actionNote.value = "";
  draftError.value = "";
  contractEditorTab.value = "Body";
  runtimeAdvancedOpen.value = false;
  toolDetailVisible.value = false;
  draftTool.value = buildDraftFromTool(tool);
  draftSnapshot.value = JSON.stringify(draftTool.value);
  saveState.value = "idle";
  publishImpactConfirmed.value = false;
  toolEditorVisible.value = true;
}

function closeToolEditor() {
  if (hasUnsavedToolChanges.value && !window.confirm("当前 Tool 有未保存修改，确认离开？")) {
    return;
  }
  toolEditorVisible.value = false;
  draftStep.value = 1;
  toolEditorMode.value = "create";
  editingToolId.value = "";
}

function goToDraftStep(step: number) {
  const nextStep = Math.min(Math.max(step, 1), toolEditorSteps.length);
  if (nextStep > draftStep.value && !isDraftStepComplete(draftStep.value)) {
    draftError.value = "请先补齐当前步骤的必填项。";
    return;
  }
  draftError.value = "";
  draftStep.value = nextStep;
}

function isDraftStepComplete(step: number) {
  if (step === 1) {
    return Boolean(
      draftTool.value.name.trim()
      && draftTool.value.workspaceId
      && draftTool.value.connectionId
      && draftTool.value.method
      && draftTool.value.path.trim().startsWith("/"),
    );
  }
  if (step === 2) {
    return schemaNodesValid(draftTool.value.requestContract)
      && schemaNodesValid(draftTool.value.responseContract)
      && draftTool.value.errorMappings.every((mapping) => Boolean(mapping.protocolStatus.trim() && mapping.errorCode.trim()));
  }
  return true;
}

function schemaNodesValid(nodes: ToolSchemaNode[]): boolean {
  return nodes.every((node) => Boolean(node.name.trim() && node.type)
    && schemaNodesValid(node.children || [])
    && (!node.item || schemaNodesValid([node.item])));
}

function draftStepState(step: number) {
  if (draftStep.value === step) return "active";
  if (step < draftStep.value) return isDraftStepComplete(step) ? "done" : "warning";
  return "pending";
}

function draftStepCanProceed() {
  return isDraftStepComplete(draftStep.value);
}

function countSchemaNodes(nodes: ToolSchemaNode[]): number {
  return nodes.reduce(
    (total, node) => total + 1 + countSchemaNodes(node.children || []) + (node.item ? countSchemaNodes([node.item]) : 0),
    0,
  );
}

function maxSchemaDepth(nodes: ToolSchemaNode[], depth = 1): number {
  if (!nodes.length) return 0;
  return Math.max(
    ...nodes.map((node) => {
      const childDepth = maxSchemaDepth(node.children || [], depth + 1);
      const itemDepth = node.item ? maxSchemaDepth([node.item], depth + 1) : 0;
      return Math.max(depth, childDepth, itemDepth);
    }),
  );
}

function contractSummary(nodes: ToolSchemaNode[]) {
  if (!nodes.length) {
    return "尚未定义";
  }
  return `${countSchemaNodes(nodes)} 个节点 · ${maxSchemaDepth(nodes)} 层嵌套`;
}

function requestLocationCount(location: "Path" | "Query" | "Header" | "Body") {
  return location === "Body" ? requestBodyContract.value.length : draftTool.value.requestContract.filter((node) => node.location === location).length;
}

function contractEditorTabCount(tab: ContractEditorTab) {
  if (tab === "Response") return responseBodyContract.value.length;
  if (tab === "Errors") return draftTool.value.errorMappings.length;
  return requestLocationCount(tab);
}

function contractEditorHint(tab: ContractEditorTab) {
  if (tab === "Body" || tab === "Response") return `${tab} 结构常含嵌套对象，使用字段树展开配置，右侧编辑详细属性。`;
  if (tab === "Errors") return "将协议状态与错误码转换为 Agent 可以理解和执行的处理建议。";
  return "Path / Query / Header 通常是扁平键值对，用表格快速填写更高效。";
}

function addErrorMapping() {
  draftTool.value.errorMappings.push({ protocolStatus: "", errorCode: "", agentAdvice: "" });
}

function removeErrorMapping(index: number) {
  draftTool.value.errorMappings.splice(index, 1);
}

function buildDraftTool(): Tool {
  const existing = editingToolId.value
    ? integration.tools.find((tool) => tool.id === editingToolId.value) || integration.toolPageItems.find((tool) => tool.id === editingToolId.value)
    : undefined;
  const toolName = draftTool.value.name.trim() || "未命名 Tool";
  const providerId = existing?.providerId || draftConnection.value?.providerId || integration.providers[0]?.id || "";
  return {
    id: toolEditorMode.value === "edit" ? editingToolId.value : "",
    workspaceId: draftTool.value.workspaceId.trim() || "default",
    providerId,
    sourceAssetId: existing?.sourceAssetId,
    sourceEndpointId: existing?.sourceEndpointId,
    connectionId: draftTool.value.connectionId,
    defaultConnectionId: draftTool.value.connectionId,
    name: toolName,
    slug: existing?.slug || toolName.toLocaleLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || `tool-${Date.now()}`,
    protocol: "HTTP",
    actionConfig: buildHTTPActionConfig(
      draftTool.value.method,
      draftTool.value.path,
      draftTool.value.contentType,
      draftTool.value.requestContract,
    ),
    actionConfigSchemaVersion: HTTP_ACTION_SCHEMA_VERSION,
    description: draftTool.value.description.trim(),
    status: draftTool.value.status,
    capabilityStatus: draftTool.value.status === "Disabled" ? "DISABLED" : "ACTIVE",
    activeReleaseId: existing?.activeReleaseId,
    versions: existing?.versions || [],
    draftVersion: existing?.draftVersion,
    requestParams: buildRequestParamsFromContracts(
      requestTransportContract.value
        .filter((node) => node.name.trim())
        .map((node) => ({
          location: node.location || "Query",
          name: node.name.trim(),
          type: node.type,
          required: node.required,
          description: node.description.trim(),
          valueSource: node.valueSource,
          defaultValue: node.defaultValue,
          schema: normalizeSchemaNode(node),
        })),
      requestBodyContract.value.map((node) => normalizeSchemaNode({ ...node, location: node.location || "Body" })),
    ),
    responseFields: responseBodyContract.value
      .filter((node) => node.name.trim())
      .map((node) => ({
        name: node.name.trim(),
        type: node.type,
        description: node.description.trim(),
        schema: normalizeSchemaNode(node),
      })),
    errorMappings: draftTool.value.errorMappings
      .filter((mapping) => mapping.protocolStatus.trim() || mapping.errorCode.trim() || mapping.agentAdvice.trim())
      .map((mapping) => ({
        protocolStatus: mapping.protocolStatus.trim(),
        errorCode: mapping.errorCode.trim(),
        agentAdvice: mapping.agentAdvice.trim(),
      })),
    runtimePolicy: {
      timeoutMs: Math.max(1, Number(draftTool.value.timeoutSeconds) || 8) * 1000,
      retryCount: Math.max(0, Number(draftTool.value.retryCount) || 0),
      backoffPolicy: draftTool.value.backoffPolicy.trim() || "exponential",
      idempotencyPolicy: draftTool.value.idempotencyPolicy.trim() || "header: Idempotency-Key",
      rateLimitPolicy: draftTool.value.rateLimitPolicy.trim() || "60 rpm",
    },
    createdBy: existing?.createdBy || "",
    updatedBy: existing?.updatedBy || "",
    createdAt: existing?.createdAt,
    updatedAt: existing?.updatedAt,
    lockVersion: existing?.lockVersion || 0,
  };
}

function toolSaveErrorMessage(error: unknown) {
  const responseError = (error as { response?: { data?: { error?: string } } }).response?.data?.error || "";
  if (responseError.includes("already exists")) {
    return "Tool 已存在，请检查名称或编辑已有 Tool。";
  }
  if (responseError.includes("service connection not found")) {
    return "服务连接不存在，请先选择可用的服务连接。";
  }
  if (responseError.includes("workspace not found")) {
    return "业务空间不存在，请从下拉列表中重新选择。";
  }
  return responseError || "保存 Tool 失败，请检查字段后重试。";
}

function toolActionErrorMessage(error: unknown, fallback: string) {
  const responseError = (error as { response?: { data?: { error?: string } } }).response?.data?.error || "";
  if (responseError.includes("tool must pass test before publish")) {
    return "发布前需要先执行通过测试。";
  }
  return responseError || fallback;
}

async function publishTool(tool: Tool) {
  const current = latestTool(tool);
  if (!canPublishTool(current)) {
    setActionFeedback("发布前需要先执行通过测试。", "error");
    return;
  }
  try {
    const published = await integration.publishTool(current.id);
    setActionFeedback(current.status === "Disabled" ? `${published.name} 已重新发布。` : `${published.name} 已发布。`);
  } catch (error) {
    setActionFeedback(toolActionErrorMessage(error, "发布失败，请稍后重试。"), "error");
  }
}

async function enableTool(tool: Tool) {
  const current = latestTool(tool);
  try {
    const enabled = await integration.updateTool(current.id, { ...current, status: "Review" });
    setActionFeedback(
      hasPassingTest(enabled)
        ? `${enabled.name} 已启用，保留通过测试结果；需要对 Agent 开放时请重新发布。`
        : `${enabled.name} 已启用，当前恢复为待评审。`,
    );
  } catch (error) {
    setActionFeedback(toolActionErrorMessage(error, "启用失败，请稍后重试。"), "error");
  }
}

async function disableTool(tool: Tool) {
  const current = latestTool(tool);
  try {
    const disabled = await integration.updateTool(current.id, { ...current, status: "Disabled" });
    setActionFeedback(`${disabled.name} 已停用。`);
  } catch (error) {
    setActionFeedback(toolActionErrorMessage(error, "停用失败，请稍后重试。"), "error");
  }
}

async function toggleToolAvailability(tool: Tool) {
  if (latestTool(tool).status === "Disabled") {
    await enableTool(tool);
    return;
  }
  await disableTool(tool);
}

async function persistDraftTool(closeAfterSave = false) {
  draftError.value = "";
  if (!draftTool.value.connectionId) {
    draftError.value = "请先选择服务连接。";
    draftStep.value = 1;
    return;
  }
  saveState.value = "saving";
  try {
    const wasEdit = toolEditorMode.value === "edit";
    const toolPayload = buildDraftTool();
    const saved =
      toolEditorMode.value === "edit" && editingToolId.value
        ? await integration.updateTool(editingToolId.value, toolPayload)
        : await integration.createTool(toolPayload);
    selectedToolId.value = saved.id;
    editingToolId.value = saved.id;
    toolEditorMode.value = "edit";
    draftTool.value = buildDraftFromTool(saved);
    draftSnapshot.value = JSON.stringify(draftTool.value);
    saveState.value = "saved";
    await loadToolRegistry({ page: 1 });
    setActionFeedback(wasEdit ? `${saved.name} 已更新。` : `${saved.name} 已保存为 Tool Draft，等待测试后发布。`);
    if (closeAfterSave) {
      closeToolEditor();
    }
  } catch (error) {
    saveState.value = "failed";
    draftError.value = toolSaveErrorMessage(error);
  }
}

async function saveDraftTool() {
  await persistDraftTool(true);
}

function goPreviousStep() {
  goToDraftStep(draftStep.value - 1);
}

function goNextStep() {
  if (!draftStepCanProceed()) {
    draftError.value = "请先补齐当前步骤的必填项。";
    return;
  }
  draftError.value = "";
  goToDraftStep(draftStep.value + 1);
}

async function publishDraftTool() {
  await persistDraftTool(false);
  const tool = integration.tools.find((item) => item.id === editingToolId.value);
  if (!tool) {
    draftError.value = "请先保存草稿后再发布。";
    return;
  }
  if (draftChecklistHasBlockingErrors.value) {
    draftError.value = "发布前检查存在阻断项，请修复后再发布。";
    return;
  }
  if (draftChecklistHasWarnings.value && !window.confirm("发布前检查仍有警告项，确认继续发布？")) {
    return;
  }
  await publishTool(tool);
  closeToolEditor();
}
</script>

<template>
  <div class="page-grid tool-grid management-page-grid" v-loading="integration.loading" @click="closeFloatingMenus">
    <ManagementPageHeader
      class="span-12"
      title="工具管理"
      description="管理工具契约、服务绑定、版本测试与发布状态。"
      icon="fa-solid fa-screwdriver-wrench"
    >
      <template #actions>
        <button class="ghost-button tool-header-secondary" type="button" @click="router.push('/openapi-imports')">
          <i class="fa-solid fa-file-import" />
          <span>导入 OpenAPI</span>
        </button>
        <button
          class="primary-button tool-header-primary"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? '创建工具' : '请先创建或加入业务空间'"
          @click="openCreateTool"
        >
          <i class="fa-solid fa-circle-plus" />
          <span>创建工具</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" :items="toolSummaryItems" />

    <section class="span-12 tool-runtime-card management-list-card">
      <WorkspaceContextState
        v-if="!hasWorkspaceContext"
        feature="工具管理"
        icon="fa-solid fa-screwdriver-wrench"
        @retry="loadToolPageAssets"
      />
      <template v-else>
      <div v-if="hasToolRecords" class="tool-section-bar">
        <span><i class="fa-solid fa-circle-info" />这里不再配置域名、端口和认证；这些属于服务连接。Tool 关注业务名称、Endpoint、入参出参、重试超时和发布测试。</span>
        <button type="button" @click="router.push('/openapi-imports')">查看 OpenAPI 导入</button>
      </div>

      <ManagementList
        class="tool-management-list"
        :rows="integration.toolPageItems"
        :columns="toolColumns"
        row-key="id"
        :sticky-left-keys="['tool']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:tools:columns"
        :selectable="false"
        checkable
        :checked-row-keys="selectedToolRowKeys"
        :row-selection-label="(tool: Tool) => `选择 ${tool.name}`"
        :loading="integration.toolPageLoading"
        :error="integration.toolPageError"
        :has-loaded="integration.toolPageHasLoaded"
        :search="query"
        search-placeholder="搜索 Tool / 连接 / 路径"
        search-aria-label="搜索 Tool、服务连接或路径"
        reset-label="重置"
        reset-aria-label="重置工具筛选"
        :pagination="integration.toolPagination"
        :sort-by="integration.toolListQuery?.sortBy"
        :sort-order="integration.toolListQuery?.sortOrder"
        @update:checked-row-keys="selectedToolRowKeys = $event"
        @update:search="setToolSearch"
        @reset="resetFilters"
        @page-change="changeToolPage"
        @sort-change="changeToolSort"
      >
        <template #filters>
          <ManagementSegmentedFilter :model-value="selectedStatusFilter" :options="statusTabs" ariaLabel="工具状态筛选" @update:model-value="selectStatusFilter($event as ToolStatusFilter)" />
          <ManagementSegmentedFilter :model-value="selectedToolTypeFilter" :options="toolTypeTabs" ariaLabel="工具类型筛选" @update:model-value="selectToolTypeFilter($event as ToolTypeFilter)" />
        </template>
        <template #cell-tool="{ row: tool }">
          <div class="tool-entity-cell">
            <span class="tool-entity-icon" aria-hidden="true"><i class="fa-solid fa-screwdriver-wrench" /></span>
            <span class="tool-entity-copy">
              <strong class="aw-table-title" :title="tool.name">{{ tool.name }}</strong>
              <small class="aw-table-subtitle" :title="tool.description">{{ tool.description || "暂无描述" }}</small>
            </span>
          </div>
        </template>
        <template #cell-type="{ row: tool }"><span class="tool-type-tag aw-table-pill">{{ getToolTypeLabel(tool) }}</span></template>
        <template #cell-protocol="{ row: tool }"><span class="tool-protocol-cell aw-table-meta">{{ toolProtocolLabel(tool) }}</span></template>
        <template #cell-method="{ row: tool }"><span class="tool-method-badge aw-table-pill" :class="methodClass(tool)">{{ methodOf(tool) }}</span></template>
        <template #cell-path="{ row: tool }">
          <code class="tool-endpoint-summary aw-table-mono" :title="toolEndpointSummary(tool)">{{ pathOf(tool) }}</code>
        </template>
        <template #cell-connection="{ row: tool }">
          <span class="tool-provider-connection" :title="toolProviderConnectionLabel(tool)">
            <strong class="aw-table-title">{{ providerForTool(tool)?.name || tool.providerId || "-" }}</strong>
            <small class="aw-table-subtitle">{{ connectionForTool(tool)?.name || "连接缺失" }}</small>
          </span>
        </template>
        <template #cell-status="{ row: tool }">
          <span
            class="tool-status-pill aw-table-pill"
            :class="[statusClass(tool.status), governanceToneClass(toolUnifiedStatus(tool).tone)]"
            :title="toolUnifiedStatus(tool).description"
          >
            <i />{{ toolUnifiedStatus(tool).label }}
          </span>
        </template>
        <template #cell-version="{ row: tool }"><code class="tool-version-cell aw-table-mono">{{ toolVersionLabel(tool) }}</code></template>
        <template #cell-updatedAt="{ row: tool }"><span class="tool-updated-cell aw-table-meta">{{ formatToolTableUpdatedAt(tool) }}</span></template>
        <template #cell-actions="{ row: tool }">
          <ManagementRowActions
            :menu-actions="toolMenuActions(tool)"
            menu-label="更多工具操作"
            @action="handleToolRowAction($event, tool)"
          />
        </template>
        <template #empty>
          <div v-if="!hasToolRecords" class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-box-open" /></div>
            <h2>暂无工具</h2>
            <p>可以注册 Tool，或者从 OpenAPI 导入生成草稿。</p>
            <div class="registry-empty-actions"><button class="primary-button" type="button" @click="openCreateTool">新建 Tool</button><button class="ghost-button" type="button" @click="router.push('/openapi-imports')">从 OpenAPI 生成</button></div>
          </div>
          <div v-else class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-magnifying-glass" /></div>
            <h2>没有匹配的工具</h2>
            <p>调整工具名称、状态或路径关键词后再试。</p>
          </div>
        </template>
      </ManagementList>
      </template>
    </section>

    <div v-if="toolDetailVisible" class="modal-backdrop" @click.self="closeToolDetail">
      <section v-if="detailTool" ref="toolDetailModalRef" class="modal-card tool-detail-modal-card" role="dialog" aria-modal="true" aria-label="工具详情">
        <div class="modal-card-head">
          <div>
            <span>Tool Runtime</span>
            <h3>工具详情</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="关闭工具详情" data-modal-initial-focus @click="closeToolDetail">
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="tool-detail-modal-body">
          <div class="tool-detail-hero">
            <span class="method" :class="methodClass(detailTool)">{{ methodOf(detailTool) }}</span>
            <div>
              <strong>{{ detailTool.name }}</strong>
              <small class="mono">{{ pathOf(detailTool) }}</small>
            </div>
            <div class="tool-detail-status-stack">
              <span class="tool-status-pill" :class="[statusClass(detailTool.status), governanceToneClass(lifecycleStatus(detailTool).tone)]"><i />{{ lifecycleStatus(detailTool).label }}</span>
              <span class="tool-status-pill" :class="governanceToneClass(testStatus(detailTool).tone)"><i />{{ testStatus(detailTool).label }}</span>
              <span class="tool-status-pill" :class="governanceToneClass(runStatus(detailTool).tone)"><i />{{ runStatus(detailTool).label }}</span>
            </div>
          </div>
          <div class="tool-detail-governance-strip">
            <span><b>版本</b>{{ toolVersionLabel(detailTool) }}</span>
            <span><b>最近测试</b>{{ toolLastTestSummary(detailTool) }}</span>
            <span><b>Capability Binding</b>{{ agentImpactLabel(detailTool) }}</span>
            <span><b>影响面</b>由独立 Capability Binding 管理</span>
          </div>
          <p class="form-helper">{{ detailTool.description }}</p>

          <div class="tool-detail-tabs" role="tablist" aria-label="工具详情分区">
            <button
              v-for="tab in detailTabs"
              :id="`tool-detail-tab-${tab.id}`"
              :key="tab.id"
              :class="{ active: detailTab === tab.id }"
              type="button"
              role="tab"
              :aria-selected="detailTab === tab.id"
              :aria-controls="`tool-detail-panel-${tab.id}`"
              :tabindex="detailTab === tab.id ? 0 : -1"
              @click="selectDetailTab(tab.id)"
              @keydown="handleDetailTabKeydown($event, tab.id)"
            >
              <i :class="tab.icon" />
              <span>{{ tab.label }}</span>
            </button>
          </div>

          <div id="tool-detail-panel-base" class="tool-detail-panel" role="tabpanel" aria-labelledby="tool-detail-tab-base" v-show="detailTab === 'base'" :hidden="detailTab !== 'base'">
            <div class="tool-config-grid">
              <div class="config-summary-item"><i class="fa-solid fa-user-gear" /><span>最近维护</span><strong>{{ detailTool.updatedBy || detailTool.createdBy || "-" }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-code-branch" /><span>版本</span><strong>{{ toolVersionLabel(detailTool) }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-layer-group" /><span>业务空间</span><strong>{{ workspaceDisplayLabel(detailTool.workspaceId) }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-cubes" /><span>来源 Provider</span><strong>{{ integration.providers.find((provider) => provider.id === detailTool.providerId)?.name || detailTool.providerId }}</strong></div>
            </div>
          </div>
          <div id="tool-detail-panel-connection" class="tool-detail-panel" role="tabpanel" aria-labelledby="tool-detail-tab-connection" v-show="detailTab === 'connection'" :hidden="detailTab !== 'connection'">
            <div class="tool-config-grid">
              <div class="config-summary-item"><i class="fa-solid fa-server" /><span>服务连接</span><strong>{{ detailConnection?.name || "服务连接未找到" }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-circle-check" /><span>连接状态</span><strong>{{ serviceConnectionStatusLabel() }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-globe" /><span>服务域名</span><strong>{{ connectionDomainLabel() }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-route" /><span>Base Path</span><strong>{{ connectionBasePathLabel() }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-key" /><span>认证方式</span><strong>{{ authModeLabel() }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-layer-group" /><span>环境</span><strong>{{ environmentLabel(detailConnection?.environment || "") }}</strong></div>
              <button class="ghost-button tool-detail-maintenance-action" type="button" @click="router.push('/connections')">
                <i class="fa-solid fa-screwdriver-wrench" />
                <span>去服务连接维护</span>
              </button>
            </div>
          </div>
          <div id="tool-detail-panel-request" class="tool-detail-panel tool-contract-section-stack" role="tabpanel" aria-labelledby="tool-detail-tab-request" v-show="detailTab === 'request'" :hidden="detailTab !== 'request'">
            <ToolSchemaTreeView :nodes="detailRequestContract.transportParams.map((param) => normalizeSchemaNode({
              id: `detail-request-${param.location}-${param.name}`,
              location: param.location,
              name: param.name,
              type: (param.type as ToolSchemaNodeType) || 'string',
              required: param.required,
              description: param.description,
            }))" title="请求参数" empty-text="暂无请求参数" />
            <ToolSchemaTreeView :nodes="detailRequestContract.bodyNodes" title="请求体 Body" empty-text="暂无请求体结构" />
          </div>
          <div id="tool-detail-panel-response" class="tool-detail-panel" role="tabpanel" aria-labelledby="tool-detail-tab-response" v-show="detailTab === 'response'" :hidden="detailTab !== 'response'">
            <ToolSchemaTreeView :nodes="detailResponseNodes" title="响应结果" empty-text="暂无响应结构" />
          </div>
          <div id="tool-detail-panel-runtime" class="tool-detail-panel" role="tabpanel" aria-labelledby="tool-detail-tab-runtime" v-show="detailTab === 'runtime'" :hidden="detailTab !== 'runtime'">
            <div class="tool-config-grid">
              <div class="config-summary-item"><i class="fa-solid fa-clock" /><span>超时时间</span><strong>{{ timeoutLabel(detailTool) }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-rotate" /><span>重试次数</span><strong>{{ retryLabel(detailTool) }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-arrow-trend-up" /><span>退避策略</span><strong>{{ backoffPolicyMeta(detailTool.runtimePolicy.backoffPolicy).label }}</strong></div>
              <div class="config-summary-item"><i class="fa-solid fa-gauge-high" /><span>限流策略</span><strong>{{ rateLimitPolicyMeta(detailTool.runtimePolicy.rateLimitPolicy).label }}</strong></div>
            </div>
          </div>
          <div id="tool-detail-panel-test" class="tool-detail-panel" role="tabpanel" aria-labelledby="tool-detail-tab-test" v-show="detailTab === 'test'" :hidden="detailTab !== 'test'">
            <div class="tool-test-card">
              <div class="tool-test-request"><strong>测试方式</strong><span>打开测试弹窗，填写默认入参并执行真实调用。</span></div>
              <div class="tool-test-result"><strong>当前状态</strong><span>{{ toolStatusLabel(detailTool.status) }}</span></div>
              <div class="tool-test-result"><strong>最近结果</strong><span>{{ toolLastTestSummary(detailTool) }}</span></div>
              <div class="tool-test-result"><strong>测试详情</strong><span>{{ toolLastTestDetail(detailTool) }}</span></div>
              <div id="tool-publish-readiness" class="tool-test-result"><strong>发布条件</strong><span>{{ toolPublishReadinessLabel(detailTool) }}</span></div>
              <div class="tool-publish-checklist compact">
                <div
                  v-for="item in buildToolPublishChecklist(detailTool, detailConnection, { agentImpactConfirmed: false })"
                  :key="item.id"
                  class="tool-publish-check-item"
                  :class="{ passed: item.passed, warning: !item.passed && item.severity === 'warning', error: !item.passed && item.severity === 'error' }"
                >
                  <i :class="item.passed ? 'fa-solid fa-check' : item.severity === 'warning' ? 'fa-solid fa-triangle-exclamation' : 'fa-solid fa-xmark'" />
                  <span>{{ item.label }}</span>
                </div>
              </div>
              <div class="tool-test-action-group">
                <button class="primary-button" type="button" @click="openToolTestDialog(detailTool)">执行测试</button>
                <button class="ghost-button" type="button" :disabled="!canPublishTool(detailTool)" :aria-describedby="'tool-publish-readiness'" @click="publishTool(detailTool)">
                  {{ toolPublishButtonLabel(detailTool) }}
                </button>
                <button class="ghost-button" type="button" @click="toggleToolAvailability(detailTool)">
                  {{ toolAvailabilityButtonLabel(detailTool) }}
                </button>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>

    <div v-if="toolEditorVisible" class="modal-backdrop tool-editor-backdrop tool-registration-workspace" @click.self="closeToolEditor">
      <section ref="toolEditorModalRef" class="modal-card tool-editor-modal-card tool-registration-card tool-hybrid-registration-card" role="dialog" aria-modal="true" :aria-label="toolEditorTitle">
        <div class="tool-hybrid-topbar">
          <div class="tool-hybrid-title-block">
            <span class="tool-hybrid-title-icon" aria-hidden="true"><i class="fa-solid fa-screwdriver-wrench" /></span>
            <div>
              <h3>{{ toolEditorTitle }}</h3>
              <p>配置 Agent 可调用的业务接口动作</p>
            </div>
          </div>
          <nav class="tool-hybrid-progress" aria-label="Tool 创建步骤">
            <template v-for="(step, index) in toolEditorSteps" :key="step[0]">
              <button
                :class="draftStepState(index + 1)"
                :aria-current="draftStep === index + 1 ? 'step' : undefined"
                type="button"
                @click="goToDraftStep(index + 1)"
              >
                <b><i v-if="draftStepState(index + 1) === 'done'" class="fa-solid fa-check" />{{ draftStepState(index + 1) === "done" ? "" : index + 1 }}</b>
                <span>{{ step[0] }}</span>
              </button>
              <i v-if="index < toolEditorSteps.length - 1" class="tool-hybrid-step-bar" />
            </template>
          </nav>
          <button class="tool-hybrid-close" type="button" :aria-label="`关闭${toolEditorTitle}`" data-modal-initial-focus @click="closeToolEditor"><i class="fa-solid fa-xmark" /></button>
        </div>

        <div class="tool-step-panel tool-hybrid-step-panel" :class="{ 'is-contract-step': draftStep === 2 }">
          <template v-if="draftStep === 1">
            <div class="tool-hybrid-basics-layout">
              <div class="tool-hybrid-form-stack">
                <section class="tool-hybrid-form-section">
                  <div class="tool-hybrid-section-head">
                    <div><span>01</span><strong>基础信息</strong></div>
                    <small>用于 Agent 识别和团队管理</small>
                  </div>
                  <label class="drawer-field"><span>Tool 名称 <b>*</b></span><input v-model="draftTool.name" placeholder="例如：拦截发货" /></label>
                  <label class="drawer-field">
                    <span>业务空间 <b>*</b></span>
                    <AppSelect v-model="draftTool.workspaceId" :options="workspaceOptions" placeholder="选择业务空间" />
                  </label>
                </section>

                <section class="tool-hybrid-form-section">
                  <div class="tool-hybrid-section-head">
                    <div><span>02</span><strong>接口动作</strong></div>
                    <small>连接地址与认证由服务连接继承</small>
                  </div>
                  <div class="tool-endpoint-fields">
                    <label class="drawer-field tool-method-field"><span>Method <b>*</b></span><AppSelect v-model="draftTool.method" :options="methodOptions" /></label>
                    <label class="drawer-field"><span>Endpoint Path <b>*</b></span><input v-model="draftTool.path" class="mono" placeholder="/api/resource/{id}" /></label>
                    <label class="drawer-field"><span>Content-Type</span><AppSelect v-model="draftTool.contentType" :options="contentTypeOptions" /></label>
                  </div>
                  <label class="drawer-field"><span>动作说明</span><textarea v-model="draftTool.description" rows="3" placeholder="说明这个 Tool 会执行什么业务动作" /></label>
                  <div class="tool-endpoint-preview">
                    <span class="method" :class="draftTool.method.toLowerCase()">{{ draftTool.method }}</span>
                    <strong>{{ endpointPreviewLabel() }}</strong>
                  </div>
                </section>
              </div>

              <aside class="connection-reference-card tool-connection-summary-card tool-hybrid-connection-card">
                <label class="drawer-field">
                  <span>服务连接 <b>*</b></span>
                  <AppSelect v-model="draftTool.connectionId" :options="serviceConnectionOptions" placeholder="选择服务连接" />
                </label>
                <div class="connection-reference-head tool-connection-summary-head">
                  <i class="fa-solid fa-server" />
                  <div>
                    <strong>{{ draftConnection?.name || "未选择服务连接" }}</strong>
                    <small>统一继承域名、Base Path 与认证方式。</small>
                  </div>
                  <span class="status-pill tool-connection-summary-status" :class="statusClass(draftConnection?.status || 'Disabled')">{{ draftConnection?.status || "未配置" }}</span>
                </div>
                <div class="tool-connection-summary-grid">
                  <div class="tool-connection-summary-meta"><i class="fa-solid fa-globe" /><div><span>服务域名</span><strong class="tool-connection-summary-value mono">{{ connectionDomainLabel(draftConnection) }}</strong></div></div>
                  <div class="tool-connection-summary-meta"><i class="fa-solid fa-route" /><div><span>Base Path</span><strong class="tool-connection-summary-value mono">{{ connectionBasePathLabel(draftConnection) }}</strong></div></div>
                  <div class="tool-connection-summary-meta"><i class="fa-solid fa-key" /><div><span>认证方式</span><strong class="tool-connection-summary-value">{{ authModeLabel(draftConnection) }}</strong></div></div>
                  <div class="tool-connection-summary-meta"><i class="fa-solid fa-layer-group" /><div><span>环境</span><strong class="tool-connection-summary-value">{{ environmentLabel(draftConnection?.environment || "") }}</strong></div></div>
                </div>
                <div class="tool-status-readonly tool-hybrid-draft-note">
                  <span class="status-pill" :class="statusClass(draftTool.status)">{{ toolStatusLabel(draftTool.status) }}</span>
                  <small>保存后进入草稿状态，测试与发布在详情页继续。</small>
                </div>
                <button class="ghost-button full tool-connection-summary-action" type="button" @click="router.push('/connections')">管理服务连接</button>
              </aside>
            </div>
          </template>

          <template v-else-if="draftStep === 2">
            <div class="tool-contract-context-bar">
              <span class="method" :class="draftTool.method.toLowerCase()">{{ draftTool.method }}</span>
              <strong class="mono">{{ draftTool.path || "/" }}</strong>
              <i />
              <span>继承服务连接：<b>{{ draftConnection?.name || "未选择服务连接" }}</b></span>
              <i />
              <span>Capability Binding：<b>发布后独立配置</b></span>
              <button type="button" @click="goToDraftStep(1)">编辑接口</button>
            </div>

            <div class="tool-contract-body-wrap">
              <aside class="tool-contract-side-tabs">
                <div role="tablist" aria-label="契约分组">
                  <button
                    v-for="tab in contractEditorTabs"
                    :key="tab.value"
                    type="button"
                    role="tab"
                    :aria-selected="contractEditorTab === tab.value"
                    :class="{ active: contractEditorTab === tab.value, supplemental: tab.value === 'Response' || tab.value === 'Errors', 'section-start': tab.value === 'Response' }"
                    @click="contractEditorTab = tab.value"
                  >
                    <span>{{ tab.label }}</span><b>{{ contractEditorTabCount(tab.value) }}</b>
                  </button>
                </div>
                <p>{{ contractEditorHint(contractEditorTab) }}</p>
              </aside>

              <section class="tool-contract-main-panel" role="tabpanel" :aria-label="`${contractEditorTab} 契约`">
                <ToolFlatContractEditor
                  v-if="contractEditorTab === 'Path' || contractEditorTab === 'Query' || contractEditorTab === 'Header'"
                  v-model="activeRequestFlatContract"
                  :location="contractEditorTab"
                />
                <ToolContractHybridEditor
                  v-else-if="contractEditorTab === 'Body'"
                  v-model="requestBodyContract"
                  title="请求体 Body"
                  description="复杂结构使用字段树维护；JSON 是只读派生预览。"
                  root-label="Request Body Contract"
                  compact
                />
                <ToolContractHybridEditor
                  v-else-if="contractEditorTab === 'Response'"
                  v-model="responseBodyContract"
                  title="成功响应"
                  description="描述 Agent 可以读取和引用的响应字段。"
                  root-label="Response Contract"
                  compact
                />
                <div v-else class="tool-error-mapping-panel">
                  <div class="tool-error-mapping-head">
                    <div><strong>错误映射</strong><span>把协议错误翻译为 Agent 可理解、可执行的建议。</span></div>
                    <button type="button" @click="addErrorMapping"><i class="fa-solid fa-plus" /> 新增映射</button>
                  </div>
                  <div v-if="draftTool.errorMappings.length" class="tool-error-mapping-table">
                    <div class="tool-error-mapping-row tool-error-mapping-header"><span>HTTP Status</span><span>Error Code</span><span>Agent 建议</span><span>操作</span></div>
                    <div v-for="(mapping, index) in draftTool.errorMappings" :key="`${index}-${mapping.errorCode}`" class="tool-error-mapping-row">
                      <input v-model="mapping.protocolStatus" inputmode="numeric" aria-label="HTTP Status" placeholder="409" />
                      <input v-model="mapping.errorCode" class="mono" aria-label="Error Code" placeholder="STATE_LOCKED" />
                      <input v-model="mapping.agentAdvice" aria-label="Agent 建议" placeholder="停止执行并转人工确认" />
                      <button class="tool-flat-delete" type="button" :aria-label="`删除错误映射 ${mapping.errorCode || index + 1}`" @click="removeErrorMapping(index)"><i class="fa-solid fa-xmark" /></button>
                    </div>
                  </div>
                  <div v-else class="tool-schema-empty"><span>暂无错误映射，点击“新增映射”开始配置。</span></div>
                </div>
              </section>
            </div>
          </template>

          <template v-else>
            <div class="tool-review-heading"><div><span>03</span><strong>确认并保存草稿</strong></div><small>测试调用与发布将在保存后的 Tool 详情中完成。</small></div>
            <div class="tool-review-summary-grid">
              <section><i class="fa-solid fa-wand-magic-sparkles" /><div><span>Tool</span><strong>{{ draftTool.name || "未命名 Tool" }}</strong><small>{{ workspaceDisplayLabel(draftTool.workspaceId) }} · Capability Binding 独立管理</small></div><button type="button" @click="goToDraftStep(1)">编辑</button></section>
              <section><i class="fa-solid fa-link" /><div><span>Endpoint</span><strong class="mono">{{ draftTool.method }} {{ draftTool.path || "/" }}</strong><small>{{ draftConnection?.name || "未选择服务连接" }}</small></div><button type="button" @click="goToDraftStep(1)">编辑</button></section>
              <section><i class="fa-solid fa-diagram-project" /><div><span>契约</span><strong>{{ requestContractCount }} 入参 · {{ responseContractCount }} 出参</strong><small>{{ draftTool.errorMappings.length }} 条错误映射</small></div><button type="button" @click="goToDraftStep(2)">编辑</button></section>
              <section><i class="fa-solid fa-gauge-high" /><div><span>运行策略</span><strong>{{ draftTool.timeoutSeconds }}s 超时 · {{ draftTool.retryCount }} 次重试</strong><small>{{ backoffPolicyMeta(draftTool.backoffPolicy).label }} · {{ draftTool.rateLimitPolicy }}</small></div><button type="button" @click="runtimeAdvancedOpen = !runtimeAdvancedOpen">{{ runtimeAdvancedOpen ? "收起" : "配置" }}</button></section>
            </div>

            <section class="tool-runtime-disclosure" :class="{ open: runtimeAdvancedOpen }">
              <button type="button" :aria-expanded="runtimeAdvancedOpen" @click="runtimeAdvancedOpen = !runtimeAdvancedOpen">
                <span><i class="fa-solid fa-sliders" /><strong>高级运行策略</strong><small>默认值适合大多数 HTTP Tool</small></span>
                <i :class="runtimeAdvancedOpen ? 'fa-solid fa-chevron-up' : 'fa-solid fa-chevron-down'" />
              </button>
              <div v-if="runtimeAdvancedOpen" class="tool-runtime-policy-inline">
                <div class="form-two">
                  <label class="drawer-field"><span>超时时间（秒）</span><input v-model.number="draftTool.timeoutSeconds" type="number" min="1" /></label>
                  <label class="drawer-field"><span>重试次数</span><input v-model.number="draftTool.retryCount" type="number" min="0" /></label>
                </div>
                <div class="form-two">
                  <label class="drawer-field"><span>退避策略</span><AppSelect v-model="draftTool.backoffPolicy" :options="backoffPolicyOptions.map((option) => ({ label: option.label, value: option.value }))" /></label>
                  <label class="drawer-field"><span>限流策略</span><AppSelect v-model="draftTool.rateLimitPolicy" :options="rateLimitPolicyOptions.map((option) => ({ label: option.label, value: option.value }))" /></label>
                </div>
                <label class="drawer-field"><span>幂等策略</span><input v-model="draftTool.idempotencyPolicy" /></label>
              </div>
            </section>

            <div class="tool-draft-save-note"><i class="fa-solid fa-circle-info" /><div><strong>保存后状态：草稿</strong><span>草稿不会立即开放给 Agent。保存后可在详情页执行测试，通过后再发布。</span></div></div>
          </template>
        </div>

        <p v-if="draftError" class="form-error tool-hybrid-form-error" role="alert">{{ draftError }}</p>

        <div class="tool-hybrid-footer">
          <div class="tool-hybrid-completion"><span>完成度 <b>{{ draftCompletionPercent }}%</b></span><i><b :style="{ width: `${draftCompletionPercent}%` }" /></i></div>
          <div class="tool-hybrid-stat">基础必填 <b>{{ completedBaseRequiredCount }}/5</b></div>
          <div v-if="draftSuggestionCount" class="tool-hybrid-stat warning"><i />建议检查 {{ draftSuggestionCount }}</div>
          <div v-else class="tool-hybrid-stat"><i class="fa-solid fa-circle-check" />契约已配置</div>
          <span class="tool-editor-action-spacer" />
          <button class="ghost" type="button" @click="closeToolEditor">取消</button>
          <button type="button" :disabled="saveState === 'saving'" @click="persistDraftTool(false)">保存草稿</button>
          <button type="button" :disabled="draftStep === 1" @click="goPreviousStep">上一步</button>
          <button v-if="draftStep < toolEditorSteps.length" class="primary" type="button" :disabled="!draftStepCanProceed()" @click="goNextStep">下一步</button>
          <button v-else class="primary" type="button" :disabled="!isDraftStepComplete(1) || !isDraftStepComplete(2) || saveState === 'saving'" @click="saveDraftTool">完成</button>
        </div>
      </section>
    </div>

    <div v-if="riskConfirmationVisible" class="modal-backdrop" @click.self="closeRiskConfirmation">
      <section ref="riskConfirmationModalRef" class="modal-card tool-risk-confirmation-modal" role="dialog" aria-modal="true" :aria-label="riskConfirmationTitle()">
        <div class="modal-card-head">
          <div>
            <span>Risk Control</span>
            <h3>{{ riskConfirmationTitle() }}</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="关闭风险确认" data-modal-initial-focus @click="closeRiskConfirmation">
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="tool-risk-confirmation-body">
          <strong>{{ pendingRiskAction.tool?.name }}</strong>
          <p>该操作可能影响已发布 Release 的 Capability Binding 或 Workflow 引用；请先确认影响面。</p>
          <div class="tool-impact-summary">
            <span><b>Capability Binding</b>{{ pendingRiskAction.tool ? agentImpactLabel(pendingRiskAction.tool) : "-" }}</span>
            <span><b>Workflow 引用</b>由发布态 Release 解析</span>
            <span><b>版本</b>{{ pendingRiskAction.tool ? toolVersionLabel(pendingRiskAction.tool) : "-" }}</span>
          </div>
        </div>
        <div class="tool-editor-actions">
          <button class="ghost-button" type="button" @click="closeRiskConfirmation">取消</button>
          <button class="primary-button danger" type="button" @click="confirmRiskAction">{{ riskConfirmationPrimaryLabel() }}</button>
        </div>
      </section>
    </div>

    <div v-if="actionNote" class="action-toast" :class="{ error: actionNoteTone === 'error' }">{{ actionNote }}</div>
    <ToolTestDialog v-model="testDialogVisible" :tool="testDialogTool" />
  </div>
</template>

<style scoped>
.tool-grid {
  row-gap: 16px;
  color: #0f172a;
}

.tool-page-header {
  align-items: flex-start;
  padding: 0;
}

.tool-page-header > div:first-child > span {
  color: #10b981;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.12em;
  line-height: 1;
  text-transform: uppercase;
}

.tool-page-header h2 {
  margin: 8px 0 0;
  color: #0f172a;
  font-size: 24px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1.15;
}

.tool-page-header p {
  max-width: 720px;
  margin: 8px 0 0;
  color: #64748b;
  font-size: 13px;
  font-weight: 400;
  line-height: 1.65;
}

.tool-page-header p b {
  color: #475569;
  font-weight: 600;
}

.tool-grid .page-header-actions {
  gap: 8px;
}

.tool-grid .tool-header-secondary,
.tool-grid .tool-header-primary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  padding: 0 16px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.04);
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease,
    transform 0.16s ease;
}

.tool-grid .tool-header-secondary {
  background: #fff;
  color: #475569;
}

.tool-grid .tool-header-secondary:hover {
  background: #f8fafc;
  color: #0f172a;
}

.tool-grid .tool-header-primary {
  border-color: var(--aw-green, #0f9f6e);
  background: var(--aw-green, #0f9f6e);
  color: #fff;
}

.tool-grid .tool-header-primary i {
  color: #fff;
}

.tool-grid .tool-header-primary:hover {
  background: #0b895f;
  border-color: #0b895f;
  color: #fff;
}

.tool-grid .tool-header-primary:active,
.tool-grid .tool-header-secondary:active {
  transform: translateY(1px);
}

.tool-summary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}

.tool-summary-card {
  position: relative;
  display: flex;
  min-height: 108px;
  flex-direction: column;
  align-items: flex-start;
  justify-content: space-between;
  overflow: hidden;
  padding: 16px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.06);
  color: #0f172a;
  text-align: left;
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.16s ease;
}

.tool-summary-card:hover,
.tool-summary-card.active {
  border-color: #d1fae5;
  box-shadow:
    0 0 0 3px rgb(16 185 129 / 0.08),
    0 14px 32px rgb(15 23 42 / 0.08);
}

.tool-summary-card:active {
  transform: translateY(1px);
}

.tool-summary-card span {
  color: #64748b;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1.2;
}

.tool-summary-card strong {
  margin-top: 18px;
  color: #0f172a;
  font-size: 26px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1;
}

.tool-summary-card > i {
  position: absolute;
  right: 16px;
  top: 16px;
  color: #94a3b8;
  font-size: 14px;
}

.tool-summary-card > i.emerald {
  color: #10b981;
}

.tool-summary-card > i.indigo {
  color: #6366f1;
}

.tool-summary-card > i.amber {
  color: #f59e0b;
}

/* Transparent shell — ManagementList owns table/toolbar/footer chrome. */
.tool-runtime-card.management-list-card {
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.tool-section-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 24px;
  border-bottom: 1px solid #f1f5f9;
  background: #f8fafc;
  color: #64748b;
  font-size: 12px;
  font-weight: 400;
  line-height: 1.5;
}

.tool-section-bar span {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.tool-section-bar i {
  color: #10b981;
}

.tool-section-bar button {
  padding: 0;
  border: 0;
  background: transparent;
  color: #059669;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
}

.tool-section-bar button:hover {
  color: #047857;
}

.tool-runtime-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 24px;
  border-bottom: 1px solid #f1f5f9;
  background: #fff;
}

.tool-search-box {
  position: relative;
  display: flex;
  width: 288px;
  max-width: 100%;
  align-items: center;
}

.tool-search-box i {
  position: absolute;
  left: 12px;
  color: #94a3b8;
  font-size: 12px;
}

.tool-search-box input {
  width: 100%;
  min-height: 44px;
  padding: 0 12px 0 34px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
  color: #0f172a;
  font-size: 12px;
  font-weight: 500;
  line-height: 44px;
  outline: none;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}

.tool-search-box input::placeholder {
  color: #94a3b8;
}

.tool-search-box input:focus {
  border-color: rgb(16 185 129 / 0.55);
  background: #fff;
  box-shadow: 0 0 0 3px rgb(16 185 129 / 0.12);
}

.tool-agent-filter {
  position: relative;
  width: 224px;
}

.tool-agent-filter-button {
  display: flex;
  width: 100%;
  min-height: 44px;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  color: #475569;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
}

.tool-agent-filter-button span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-agent-filter-button i {
  color: #94a3b8;
  font-size: 10px;
  transition: transform 0.16s ease;
}

.tool-agent-filter-button i.rotated {
  transform: rotate(180deg);
}

.tool-agent-filter-button:hover {
  background: #f8fafc;
  color: #0f172a;
}

.tool-agent-filter-menu {
  position: absolute;
  left: 0;
  top: calc(100% + 8px);
  z-index: 30;
  width: 100%;
  padding: 4px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 18px 40px rgb(15 23 42 / 0.12);
}

.tool-agent-filter-menu button {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #475569;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.15;
  text-align: left;
  transition:
    background-color 0.16s ease,
    color 0.16s ease;
}

.tool-agent-filter-menu button:hover,
.tool-agent-filter-menu button.active {
  background: #ecfdf5;
  color: #047857;
}

.tool-status-segmented {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  min-height: 44px;
  padding: 3px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}

.tool-status-segmented button {
  min-height: 36px;
  padding: 0 12px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    box-shadow 0.16s ease;
}

.tool-status-segmented button:hover {
  color: #0f172a;
}

.tool-status-segmented button.active {
  background: #fff;
  color: #0f172a;
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.08);
}

.tool-reset-button {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: 7px;
  padding: 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  color: #64748b;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
}

.tool-reset-button:hover {
  background: #f8fafc;
  color: #0f172a;
}

.tool-name-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  flex-direction: column;
  gap: 4px;
}

.tool-name-primary {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 8px;
}

.tool-name-primary strong {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
  line-height: 1.25;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-name-cell small {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  color: #94a3b8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 10px;
  font-weight: 600;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-entity-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 9px;
  overflow: hidden;
}

.tool-entity-icon {
  display: inline-grid;
  width: 27px;
  height: 27px;
  flex: 0 0 27px;
  place-items: center;
  border-radius: 8px;
  background: var(--aw-green-soft, #eaf8f2);
  color: var(--aw-green-ink, #087653);
  font-size: 10px;
}

.tool-entity-copy {
  display: block;
  flex: 1 1 auto;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
}

.tool-entity-copy strong,
.tool-entity-copy small {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-entity-copy strong,
.tool-entity-copy .aw-table-title {
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
}

.tool-entity-copy small,
.tool-entity-copy .aw-table-subtitle {
  margin-top: 2px;
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.8125rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
}

.tool-type-tag {
  display: inline-flex;
  min-height: 24px;
  align-items: center;
  padding: 0 7px;
  border-radius: 7px;
  background: var(--aw-soft-2, #f1f5f3);
  color: #52615b;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
}

.tool-protocol-cell {
  display: block;
  overflow: hidden;
  color: var(--aw-body, #4e5b56);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-provider-connection {
  display: block;
  min-width: 0;
  overflow: hidden;
}

.tool-provider-connection strong,
.tool-provider-connection small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-provider-connection strong,
.tool-provider-connection .aw-table-title {
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
}

.tool-provider-connection small,
.tool-provider-connection .aw-table-subtitle {
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.8125rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
}

.tool-version-cell {
  color: var(--aw-table-body-color, #374151);
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: var(--aw-table-mono-size, 0.82rem);
}

.tool-name-agent {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  color: #64748b;
  font-size: 11px;
  font-weight: 700;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-name-agent i {
  flex: 0 0 auto;
  color: #94a3b8;
  font-size: 10px;
}

.tool-summary-meta {
  display: block;
  max-width: 100%;
  overflow: hidden;
  color: #94a3b8;
  font-size: 11px;
  font-weight: 500;
  line-height: 1.25;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-method-badge {
  display: inline-flex;
  min-width: 42px;
  height: 22px;
  align-items: center;
  justify-content: center;
  padding: 0 8px;
  border: 1px solid #c7d2fe;
  border-radius: 7px;
  background: #eef2ff;
  color: #4f46e5;
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1;
}

.tool-method-badge.get {
  border-color: #bae6fd;
  background: #f0f9ff;
  color: #0284c7;
}

.tool-method-badge.post {
  border-color: #bbf7d0;
  background: #f0fdf4;
  color: #16a34a;
}

.tool-method-badge.patch {
  border-color: #fde68a;
  background: #fffbeb;
  color: #d97706;
}

.tool-method-badge.delete {
  border-color: #fecdd3;
  background: #fff1f2;
  color: #e11d48;
}

.tool-endpoint-summary {
  display: block;
  max-width: 260px;
  overflow: hidden;
  color: var(--aw-table-body-color, #374151);
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: var(--aw-table-mono-size, 0.82rem);
  font-weight: 400;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-endpoint-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.tool-copy-path-button {
  display: inline-flex;
  width: 28px;
  height: 28px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid #e2e8f0;
  border-radius: 7px;
  background: #fff;
  color: #94a3b8;
}

.tool-copy-path-button:hover {
  color: #047857;
  border-color: #bbf7d0;
  background: #f0fdf4;
}

.tool-agent-pill {
  display: inline-flex;
  max-width: 180px;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  padding: 4px 9px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #f8fafc;
  color: #475569;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-agent-pill i {
  color: #94a3b8;
  font-size: 10px;
}

.tool-unified-status-cell {
  display: flex;
  min-width: 128px;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
}

.tool-unified-status-cell small {
  max-width: 180px;
  overflow: hidden;
  color: #94a3b8;
  font-size: 11px;
  font-weight: 700;
  line-height: 1.25;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-status-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  max-width: 100%;
  padding: 4px 10px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #f8fafc;
  color: #64748b;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1.25;
  text-align: center;
  white-space: normal;
}

.tool-status-pill i {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: currentColor;
}

.tool-status-pill.published {
  border-color: #bbf7d0;
  background: #f0fdf4;
  color: #059669;
}

.tool-status-pill.tested {
  border-color: #c7d2fe;
  background: #eef2ff;
  color: #4f46e5;
}

.tool-status-pill.review {
  border-color: #fed7aa;
  background: #fff7ed;
  color: #ea580c;
}

.tool-status-pill.disabled {
  border-color: #e2e8f0;
  background: #f8fafc;
  color: #64748b;
}

.tool-status-pill.draft {
  border-color: #fde68a;
  background: #fffbeb;
  color: #d97706;
}

.tool-status-pill.tone-success {
  border-color: #bbf7d0;
  background: #f0fdf4;
  color: #059669;
}

.tool-status-pill.tone-info {
  border-color: #c7d2fe;
  background: #eef2ff;
  color: #4f46e5;
}

.tool-status-pill.tone-warning {
  border-color: #fed7aa;
  background: #fff7ed;
  color: #ea580c;
}

.tool-status-pill.tone-danger {
  border-color: #fecdd3;
  background: #fff1f2;
  color: #e11d48;
}

.tool-status-pill.tone-neutral {
  border-color: #e2e8f0;
  background: #f8fafc;
  color: #64748b;
}

.tool-updated-cell {
  color: var(--aw-table-meta-color, #6b7280);
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-weight: var(--aw-table-meta-weight, 400);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.tool-pagination {
  padding: 16px 20px;
  border-top: 1px solid #f1f5f9;
  background: #fff;
}

.tool-grid .tool-pagination > span {
  color: #94a3b8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 11px;
  font-weight: 600;
}

.tool-grid .page-step-button,
.tool-grid .page-number-button {
  min-width: 44px;
  width: 44px;
  height: 44px;
  border-radius: 8px;
  font-size: 12px;
}

.tool-grid .page-number-button.active {
  border-color: #020617;
  background: #020617;
  color: #fff;
}

.tool-detail-modal-card,
.tool-editor-modal-card {
  border-radius: 16px;
  border-color: #e2e8f0;
  box-shadow: 0 24px 60px rgb(15 23 42 / 0.18);
}

.tool-detail-modal-card .modal-card-head,
.tool-editor-modal-card .modal-card-head {
  border-bottom-color: #f1f5f9;
  background: #fff;
}

.tool-detail-modal-card .modal-card-head span,
.tool-editor-modal-card .modal-card-head span {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 800;
  letter-spacing: 0.1em;
  line-height: 1;
  text-transform: uppercase;
}

.tool-detail-modal-card .modal-card-head h3,
.tool-editor-modal-card .modal-card-head h3 {
  color: #0f172a;
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1.2;
}

.tool-detail-hero .method {
  display: inline-flex;
  min-width: 42px;
  height: 22px;
  align-items: center;
  justify-content: center;
  padding: 0 8px;
  border-radius: 7px;
  font-size: 10px;
  font-weight: 800;
}

.tool-detail-status-stack {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
  margin-left: auto;
}

.tool-detail-governance-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin: 14px 0 12px;
}

.tool-detail-governance-strip span,
.tool-impact-summary span {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
  font-size: 11px;
  font-weight: 700;
}

.tool-detail-governance-strip b,
.tool-impact-summary b {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 800;
}

.tool-registration-workspace {
  align-items: stretch;
  padding: 18px;
}

.tool-registration-card {
  width: min(1360px, calc(100vw - 36px));
  height: calc(100vh - 36px);
  max-height: none;
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr) auto auto;
}

.tool-registration-card .modal-card-head {
  flex: 0 0 auto;
}

.tool-save-state {
  display: inline-flex;
  margin-top: 6px;
  color: #94a3b8;
  font-size: 11px;
  font-weight: 800;
}

.tool-save-state.saved {
  color: #059669;
}

.tool-save-state.failed {
  color: #e11d48;
}

.tool-save-state.dirty {
  color: #d97706;
}

.tool-registration-card .tool-step-panel {
  min-height: 0;
  overflow: auto;
}

.tool-editor-progress button.warning b {
  border-color: #fed7aa;
  background: #fff7ed;
  color: #ea580c;
}

.tool-inline-contract-workbench {
  min-height: 520px;
  border: 1px solid #dbeafe;
  border-radius: 12px;
  background: #f8fafc;
  overflow: hidden;
}

.tool-publish-panel,
.tool-runtime-policy-inline {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tool-publish-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
}

.tool-publish-panel-head strong {
  display: block;
  color: #0f172a;
  font-size: 14px;
}

.tool-publish-panel-head span {
  color: #64748b;
  font-size: 12px;
}

.tool-publish-checklist {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.tool-publish-checklist.compact {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.tool-publish-check-item {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr);
  align-items: start;
  gap: 8px;
  padding: 10px;
  border: 1px solid #fecdd3;
  border-radius: 8px;
  background: #fff1f2;
  color: #9f1239;
}

.tool-publish-check-item.passed {
  border-color: #bbf7d0;
  background: #f0fdf4;
  color: #047857;
}

.tool-publish-check-item.warning {
  border-color: #fed7aa;
  background: #fff7ed;
  color: #c2410c;
}

.tool-publish-check-item > i {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: #fff;
  font-size: 11px;
}

.tool-publish-check-item strong,
.tool-publish-check-item span {
  display: block;
}

.tool-publish-check-item strong {
  color: inherit;
  font-size: 12px;
}

.tool-publish-check-item span {
  margin-top: 2px;
  color: #64748b;
  font-size: 11px;
  line-height: 1.45;
}

.tool-impact-confirm {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  color: #475569;
  font-size: 12px;
  font-weight: 700;
}

.tool-risk-confirmation-modal {
  width: min(560px, calc(100vw - 40px));
}

.tool-risk-confirmation-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
}

.tool-risk-confirmation-body > strong {
  color: #0f172a;
  font-size: 16px;
}

.tool-risk-confirmation-body p {
  margin: 0;
  color: #64748b;
  font-size: 13px;
  line-height: 1.6;
}

.tool-impact-summary {
  display: grid;
  grid-template-columns: 1fr;
  gap: 8px;
}

.tool-detail-hero .status-pill,
.tool-status-readonly .status-pill,
.tool-connection-summary-status {
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
}

.tool-hybrid-registration-card {
  width: min(1240px, calc(100vw - 36px));
  grid-template-rows: auto auto minmax(0, 1fr) auto auto;
  background: #f8fafc;
}

.tool-hybrid-progress {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding: 12px 24px;
  border-bottom: 1px solid #e2e8f0;
  background: #fff;
}

.tool-hybrid-progress button {
  min-height: 54px;
}

.tool-hybrid-progress button:not(:last-child)::after {
  content: "";
  position: absolute;
  top: 26px;
  right: -14px;
  width: 28px;
  height: 1px;
  background: #cbd5e1;
}

.tool-hybrid-step-panel {
  padding: 20px 24px 28px;
  background: #f8fafc;
}

.tool-hybrid-basics-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(300px, 0.8fr);
  align-items: start;
  gap: 18px;
}

.tool-hybrid-form-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.tool-hybrid-form-section,
.tool-hybrid-connection-card,
.tool-contract-tab-panel,
.tool-review-summary-grid section,
.tool-runtime-disclosure,
.tool-draft-save-note {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
}

.tool-hybrid-form-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px;
}

.tool-hybrid-section-head,
.tool-review-heading,
.tool-error-mapping-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.tool-hybrid-section-head > div,
.tool-review-heading > div {
  display: flex;
  align-items: center;
  gap: 9px;
}

.tool-hybrid-section-head > div > span,
.tool-review-heading > div > span {
  display: inline-flex;
  width: 28px;
  height: 28px;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: #ccfbf1;
  color: #0f766e;
  font-size: 11px;
  font-weight: 800;
}

.tool-hybrid-section-head strong,
.tool-review-heading strong,
.tool-error-mapping-head strong {
  color: #0f172a;
  font-size: 15px;
}

.tool-hybrid-section-head small,
.tool-review-heading small,
.tool-error-mapping-head span {
  color: #64748b;
  font-size: 12px;
}

.tool-hybrid-form-section .drawer-field > span b,
.tool-hybrid-connection-card .drawer-field > span b {
  color: #e11d48;
}

.tool-hybrid-form-section textarea {
  width: 100%;
  min-height: 76px;
  resize: vertical;
  border: 1px solid #cbd5e1;
  border-radius: 9px;
  padding: 10px 12px;
  color: #0f172a;
  font: inherit;
  font-size: 13px;
  line-height: 1.5;
}

.tool-endpoint-fields {
  display: grid;
  grid-template-columns: 140px minmax(260px, 1fr) 230px;
  gap: 12px;
}

.tool-endpoint-preview,
.tool-contract-context-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  padding: 11px 12px;
  border: 1px solid #ccfbf1;
  border-radius: 9px;
  background: #f0fdfa;
}

.tool-endpoint-preview strong,
.tool-contract-context-bar strong {
  min-width: 0;
  overflow: hidden;
  color: #0f172a;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-hybrid-connection-card {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px;
}

.tool-hybrid-connection-card .tool-connection-summary-grid {
  grid-template-columns: 1fr;
}

.tool-hybrid-draft-note {
  align-items: flex-start;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid #dbeafe;
  border-radius: 9px;
  background: #eff6ff;
}

.tool-contract-context-bar {
  margin-bottom: 14px;
  background: #fff;
}

.tool-contract-context-bar small {
  overflow: hidden;
  color: #64748b;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-contract-context-bar button,
.tool-review-summary-grid section > button {
  margin-left: auto;
  border: 0;
  background: transparent;
  color: #0f766e;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.tool-contract-tabs {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 12px;
}

.tool-contract-tabs button {
  min-height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  color: #64748b;
  font: inherit;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}

.tool-contract-tabs button b,
.tool-request-location-tabs button b {
  min-width: 22px;
  padding: 2px 6px;
  border-radius: 999px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 10px;
}

.tool-contract-tabs button.active {
  border-color: #5eead4;
  background: #f0fdfa;
  color: #0f766e;
  box-shadow: 0 0 0 1px #99f6e4 inset;
}

.tool-contract-tab-panel {
  min-height: 440px;
  padding: 16px;
}

.tool-request-location-tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 16px;
  padding: 4px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}

.tool-request-location-tabs button {
  min-height: 40px;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 0 16px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: #64748b;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.tool-request-location-tabs button.active {
  background: #fff;
  color: #0f766e;
  box-shadow: 0 1px 4px rgb(15 23 42 / 0.1);
}

.tool-error-mapping-head {
  margin-bottom: 14px;
}

.tool-error-mapping-head > div strong,
.tool-error-mapping-head > div span {
  display: block;
}

.tool-error-mapping-head > div span {
  margin-top: 4px;
}

.tool-error-mapping-table {
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
}

.tool-error-mapping-row {
  display: grid;
  grid-template-columns: 140px 220px minmax(280px, 1fr) 58px;
  gap: 10px;
  align-items: center;
  padding: 9px 12px;
  border-top: 1px solid #f1f5f9;
}

.tool-error-mapping-header {
  border-top: 0;
  background: #f8fafc;
  color: var(--aw-table-header-color, #6b7280);
  font-size: var(--aw-table-header-size, 0.75rem);
  font-weight: var(--aw-table-header-weight, 600);
}

.tool-error-mapping-row input {
  width: 100%;
  min-height: 40px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  padding: 0 10px;
  color: #0f172a;
  font: inherit;
  font-size: var(--aw-table-body-size, 0.8125rem);
}

.tool-review-heading {
  margin-bottom: 14px;
}

.tool-review-summary-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.tool-review-summary-grid section {
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr) auto;
  align-items: center;
  gap: 11px;
  padding: 15px;
}

.tool-review-summary-grid section > i {
  width: 38px;
  height: 38px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  background: #f0fdfa;
  color: #0f766e;
}

.tool-review-summary-grid section span,
.tool-review-summary-grid section strong,
.tool-review-summary-grid section small {
  display: block;
}

.tool-review-summary-grid section span {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 800;
  text-transform: uppercase;
}

.tool-review-summary-grid section strong {
  margin-top: 3px;
  overflow: hidden;
  color: #0f172a;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-review-summary-grid section small {
  margin-top: 3px;
  color: #64748b;
  font-size: 11px;
}

.tool-runtime-disclosure {
  margin-top: 14px;
  overflow: hidden;
}

.tool-runtime-disclosure > button {
  width: 100%;
  min-height: 58px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 16px;
  border: 0;
  background: #fff;
  color: #475569;
  font: inherit;
  cursor: pointer;
}

.tool-runtime-disclosure > button > span {
  display: flex;
  align-items: center;
  gap: 9px;
}

.tool-runtime-disclosure > button strong {
  color: #0f172a;
}

.tool-runtime-disclosure > button small {
  color: #64748b;
}

.tool-runtime-disclosure .tool-runtime-policy-inline {
  padding: 16px;
  border-top: 1px solid #e2e8f0;
  background: #f8fafc;
}

.tool-draft-save-note {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-top: 14px;
  padding: 14px;
  border-color: #bfdbfe;
  background: #eff6ff;
  color: #1d4ed8;
}

.tool-draft-save-note strong,
.tool-draft-save-note span {
  display: block;
}

.tool-draft-save-note strong {
  font-size: 13px;
}

.tool-draft-save-note span {
  margin-top: 3px;
  color: #475569;
  font-size: 12px;
}

.tool-hybrid-form-error {
  margin: 0;
  padding: 10px 24px;
  border-top: 1px solid #fecdd3;
  background: #fff1f2;
}

.tool-hybrid-editor-actions {
  width: 100%;
  min-height: 68px;
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  padding: 12px 24px;
  border-top: 1px solid #e2e8f0;
  background: #fff;
}

.tool-editor-action-spacer {
  flex: 1;
}

@media (max-height: 760px) and (min-width: 721px) {
  .tool-hybrid-registration-card .modal-card-head {
    padding: 10px 18px;
  }

  .tool-hybrid-progress {
    padding: 5px 20px;
  }

  .tool-hybrid-progress button {
    min-height: 42px;
  }

  .tool-hybrid-progress button:not(:last-child)::after {
    top: 20px;
  }

  .tool-hybrid-step-panel {
    padding: 10px 16px 14px;
  }

  .tool-contract-context-bar {
    margin-bottom: 8px;
    padding-block: 7px;
  }

  .tool-contract-tabs {
    margin-bottom: 8px;
  }

  .tool-contract-tabs button {
    min-height: 40px;
  }

  .tool-contract-tab-panel {
    min-height: 360px;
    padding: 10px;
  }

  .tool-request-location-tabs {
    margin-bottom: 9px;
  }

  .tool-request-location-tabs button {
    min-height: 34px;
  }

  .tool-hybrid-editor-actions {
    min-height: 56px;
    padding: 8px 18px;
  }

  .tool-hybrid-step-panel :deep(.tool-hybrid-contract-head > div > span) {
    display: none;
  }

  .tool-hybrid-step-panel :deep(.tool-hybrid-contract-meta) {
    padding: 6px 10px;
  }

  .tool-hybrid-step-panel :deep(.tool-hybrid-contract-meta small) {
    display: none;
  }
}

@media (max-width: 1080px) {
  .tool-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .tool-runtime-toolbar {
    flex-wrap: wrap;
  }

  .tool-search-box,
  .tool-agent-filter {
    width: min(100%, 320px);
  }

  .tool-hybrid-basics-layout {
    grid-template-columns: 1fr;
  }

  .tool-hybrid-connection-card {
    position: static;
  }

  .tool-endpoint-fields {
    grid-template-columns: 130px minmax(220px, 1fr);
  }

  .tool-endpoint-fields > :last-child {
    grid-column: 1 / -1;
  }
}

@media (max-width: 720px) {
  .tool-page-header {
    gap: 14px;
  }

  .tool-page-header h2 {
    font-size: 22px;
  }

  .tool-summary-grid {
    grid-template-columns: 1fr;
  }

  .tool-section-bar,
  .tool-runtime-toolbar {
    align-items: stretch;
    flex-direction: column;
    padding-inline: 16px;
  }

  .tool-search-box,
  .tool-agent-filter,
  .tool-status-segmented,
  .tool-reset-button {
    width: 100%;
  }

  .tool-status-segmented {
    justify-content: space-between;
  }

  .tool-registration-workspace {
    padding: 0;
  }

  .tool-hybrid-registration-card {
    width: 100vw;
    height: 100vh;
    border-radius: 0;
  }

  .tool-hybrid-progress {
    padding-inline: 10px;
  }

  .tool-hybrid-progress button > small,
  .tool-hybrid-progress button:not(:last-child)::after {
    display: none;
  }

  .tool-hybrid-step-panel {
    padding: 14px;
  }

  .tool-endpoint-fields,
  .tool-review-summary-grid {
    grid-template-columns: 1fr;
  }

  .tool-endpoint-fields > :last-child {
    grid-column: auto;
  }

  .tool-contract-tabs,
  .tool-request-location-tabs {
    overflow-x: auto;
  }

  .tool-request-location-tabs button {
    flex: 0 0 auto;
  }

  .tool-error-mapping-table {
    overflow-x: auto;
  }

  .tool-error-mapping-row {
    min-width: 720px;
  }
}

/* Project-aligned Tool registration workspace. */
.tool-editor-backdrop {
  align-items: center;
  overflow: hidden;
  padding: 32px;
  background: rgb(15 23 42 / 0.6);
  backdrop-filter: blur(12px);
}

.tool-hybrid-registration-card {
  width: min(1140px, calc(100vw - 64px));
  height: calc(100vh - 64px);
  min-height: 0;
  max-height: 880px;
  grid-template-rows: auto minmax(0, 1fr) auto auto;
  border: 1px solid rgb(241 245 249 / 0.95);
  border-radius: 12px;
  background: var(--aw-bg);
  box-shadow: 0 25px 50px -12px rgb(15 23 42 / 0.32);
}

.tool-hybrid-topbar {
  min-height: 82px;
  display: grid;
  grid-template-columns: minmax(280px, 1fr) auto minmax(280px, 1fr);
  align-items: center;
  gap: 24px;
  padding: 14px 20px;
  border-bottom: 1px solid var(--aw-border-soft);
  background: #fff;
}

.tool-hybrid-title-block {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
}

.tool-hybrid-title-icon {
  width: 48px;
  height: 48px;
  flex: 0 0 48px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 12px;
  background: #d1f0d0;
  color: #15803d;
  font-size: 19px;
}

.tool-hybrid-title-block h3 {
  margin: 0;
  color: var(--aw-text);
  font-size: 18px;
  font-weight: 700;
  line-height: 26px;
}

.tool-hybrid-title-block p {
  margin: 1px 0 0;
  color: #94a3b8;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
}

.tool-hybrid-progress {
  display: flex;
  grid-template-columns: none;
  align-items: center;
  gap: 10px;
  padding: 0;
  border: 0;
  background: transparent;
}

.tool-hybrid-progress button {
  min-height: 34px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0;
  border: 0;
  background: transparent;
  color: #94a3b8;
  font: inherit;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.tool-hybrid-progress button::after,
.tool-hybrid-progress button:not(:last-child)::after {
  display: none;
}

.tool-hybrid-progress button b {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--aw-border);
  border-radius: 50%;
  background: #fff;
  color: #94a3b8;
  font-size: 12px;
  font-weight: 500;
}

.tool-hybrid-progress button.active {
  color: var(--aw-text);
  font-weight: 700;
}

.tool-hybrid-progress button.active b {
  border-color: #0f172a;
  background: #0f172a;
  color: #fff;
}

.tool-hybrid-progress button.done b {
  border-color: #a7f3d0;
  background: #d1fae5;
  color: #047857;
}

.tool-hybrid-step-bar {
  width: 28px;
  height: 1px;
  display: block;
  background: var(--aw-border);
}

.tool-hybrid-close {
  width: 40px;
  height: 40px;
  justify-self: end;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--aw-muted);
  font-size: 15px;
  cursor: pointer;
  transition: color 0.16s ease, background-color 0.16s ease;
}

.tool-hybrid-close:hover,
.tool-hybrid-close:focus-visible {
  background: #f1f5f9;
  color: var(--aw-text);
}

.tool-hybrid-step-panel {
  padding: 18px 20px 0;
  background: var(--aw-bg);
}

.tool-hybrid-step-panel.is-contract-step {
  gap: 0;
  padding: 0;
}

.tool-contract-context-bar {
  min-height: 48px;
  margin: 16px 20px 0;
  padding: 10px 14px;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #fff;
  color: var(--aw-muted);
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.03);
  font-size: 12px;
}

.tool-contract-context-bar > i {
  width: 1px;
  height: 14px;
  flex: 0 0 auto;
  background: var(--aw-border);
}

.tool-contract-context-bar > span b {
  color: var(--aw-text);
  font-weight: 700;
}

.tool-contract-context-bar strong {
  flex: 0 1 auto;
  color: var(--aw-text);
  font-size: 12.5px;
}

.tool-contract-context-bar .method {
  padding: 3px 8px;
  border: 1px solid #bbf7d0;
  border-radius: 6px;
  background: #f0fdf4;
  color: #15803d;
  font-size: 10px;
  font-weight: 800;
}

.tool-contract-context-bar button {
  margin-left: auto;
  min-height: 32px;
  padding: 0 10px;
  border: 1px solid transparent;
  border-radius: 6px;
  color: var(--aw-cyan);
  font-size: 12px;
  font-weight: 700;
}

.tool-contract-context-bar button:hover,
.tool-contract-context-bar button:focus-visible {
  border-color: rgb(13 148 136 / 0.25);
  background: var(--aw-cyan-soft);
}

.tool-contract-body-wrap {
  min-height: 470px;
  display: flex;
  padding: 14px 20px 0;
}

.tool-contract-side-tabs {
  width: 184px;
  flex: 0 0 184px;
  padding: 8px;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.03);
}

.tool-contract-side-tabs button {
  width: 100%;
  min-height: 38px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 3px;
  padding: 8px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #475569;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.tool-contract-side-tabs button:hover {
  background: #f8fafc;
}

.tool-contract-side-tabs button.active {
  background: var(--aw-cyan-soft);
  color: #0f766e;
  box-shadow: inset 3px 0 0 var(--aw-cyan);
}

.tool-contract-side-tabs button.section-start {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--aw-border-soft);
  border-radius: 0 0 8px 8px;
}

.tool-contract-side-tabs button b {
  min-width: 24px;
  padding: 1px 7px;
  border-radius: 10px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
}

.tool-contract-side-tabs button.active b {
  background: #fff;
  color: #0f766e;
}

.tool-contract-side-tabs p {
  margin: 14px 0 0;
  padding: 10px;
  border-radius: 8px;
  background: #f8fafc;
  color: #94a3b8;
  font-size: 11px;
  line-height: 1.55;
}

.tool-contract-main-panel {
  min-width: 0;
  flex: 1;
  margin-left: 14px;
  padding: 14px;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.03);
}

.tool-error-mapping-panel {
  min-height: 360px;
}

.tool-error-mapping-head {
  margin-bottom: 14px;
}

.tool-error-mapping-head > button {
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 1px solid var(--aw-border);
  border-radius: 6px;
  background: #f8fafc;
  padding: 5px 10px;
  color: #475569;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.tool-error-mapping-row {
  grid-template-columns: 120px 190px minmax(240px, 1fr) 32px;
  padding: 9px 10px;
  border-color: #f0f1f3;
}

.tool-error-mapping-row input {
  min-height: 34px;
  border-color: transparent;
  background: transparent;
}

.tool-error-mapping-row input:hover,
.tool-error-mapping-row input:focus {
  border-color: #e5e7eb;
  background: #fafafa;
}

.tool-hybrid-footer {
  min-height: 72px;
  display: flex;
  align-items: center;
  gap: 22px;
  margin-top: 14px;
  padding: 14px 20px;
  border-top: 1px solid var(--aw-border-soft);
  background: #f8fafc;
}

.tool-hybrid-completion {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--aw-muted);
  font-size: 11px;
}

.tool-hybrid-completion span b,
.tool-hybrid-stat b {
  color: var(--aw-text);
}

.tool-hybrid-completion > i {
  width: 130px;
  height: 6px;
  overflow: hidden;
  border-radius: 4px;
  background: #e2e8f0;
}

.tool-hybrid-completion > i > b {
  height: 100%;
  display: block;
  border-radius: 4px;
  background: #0d9488;
}

.tool-hybrid-stat {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--aw-muted);
  font-size: 11px;
}

.tool-hybrid-stat.warning {
  color: #b45309;
}

.tool-hybrid-stat.warning > i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #f59e0b;
}

.tool-hybrid-footer > button {
  min-height: 42px;
  padding: 0 16px;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #fff;
  color: #334155;
  font: inherit;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: color 0.16s ease, background-color 0.16s ease, border-color 0.16s ease, transform 0.16s ease;
}

.tool-hybrid-footer > button.ghost {
  border-color: transparent;
  background: transparent;
}

.tool-hybrid-footer > button.primary {
  border-color: #020617;
  background: #020617;
  color: #fff;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.08);
}

.tool-hybrid-footer > button:hover:not(:disabled) {
  border-color: #cbd5e1;
  background: #e2e8f0;
}

.tool-hybrid-footer > button.primary:hover:not(:disabled) {
  border-color: #0f172a;
  background: #0f172a;
}

.tool-hybrid-footer > button:active:not(:disabled) {
  transform: scale(0.98);
}

.tool-hybrid-footer > button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

@media (max-height: 760px) and (min-width: 721px) {
  .tool-hybrid-topbar {
    min-height: 74px;
    padding-block: 10px;
  }

  .tool-contract-context-bar {
    margin-top: 12px;
  }

  .tool-contract-body-wrap {
    min-height: 376px;
    padding-top: 12px;
  }

  .tool-hybrid-footer {
    min-height: 64px;
    margin-top: 10px;
    padding-block: 10px;
  }
}

@media (max-width: 820px) {
  .tool-hybrid-registration-card {
    width: calc(100vw - 24px);
  }

  .tool-hybrid-topbar {
    grid-template-columns: 1fr auto;
  }

  .tool-hybrid-progress {
    grid-column: 1 / -1;
    grid-row: 2;
    justify-self: center;
  }

  .tool-hybrid-close {
    grid-column: 2;
    grid-row: 1;
  }

  .tool-contract-body-wrap {
    flex-direction: column;
  }

  .tool-contract-side-tabs {
    width: 100%;
    flex-basis: auto;
    padding: 0 0 12px;
    border: 0;
  }

  .tool-contract-side-tabs > div {
    display: flex;
    overflow-x: auto;
  }

  .tool-contract-side-tabs button {
    width: auto;
    flex: 0 0 auto;
  }

  .tool-contract-side-tabs p {
    display: none;
  }

  .tool-contract-main-panel {
    padding: 0;
  }

  .tool-hybrid-completion,
  .tool-hybrid-stat {
    display: none;
  }
}
</style>
