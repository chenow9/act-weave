/**
 * Tools page model (ZKL-64 item 12).
 */
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import type { ManagementListColumn } from "../components/ManagementList.vue";
import type { ManagementRowAction } from "../components/ManagementRowActions.vue";
import type { ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { useModalFocus } from "../composables/useModalFocus";
import { useToolsStore } from "../stores/tools";
import { useProvidersStore } from "../stores/providers";
import { useConnectionsStore } from "../stores/connections";
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
import type {
  Tool,
  ToolErrorMapping,
  ToolListQuery,
  ToolRequestParam,
  ToolResponseField,
  ToolSchemaNode,
  ToolSchemaNodeType,
} from "../types/domain";
import { buildHTTPActionConfig, HTTP_ACTION_SCHEMA_VERSION } from "../utils/tool-http-action";

export function createToolsPageModel() {
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

  const toolsStore = useToolsStore();
  const providersStore = useProvidersStore();
  const connectionsStore = useConnectionsStore();
  const workspaces = useWorkspaceStore();
  const canEditWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "EDIT"),
  );
  const canTestWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "TEST"),
  );
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
  const contentTypeOptions = ["application/json", "application/x-www-form-urlencoded", "multipart/form-data"].map(
    (contentType) => ({
      label: contentType,
      value: contentType,
    }),
  );
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
    { label: "HTTP", value: "HTTP Tool" },
    { label: "Workflow", value: "Workflow Tool" },
  ];

  const draftTool = ref<ToolDraft>(defaultToolDraft());

  const hasToolRecords = computed(() => toolsStore.tools.length > 0);
  /** Tools whose bound connection is missing, unverified, failed, or expiring. */
  const connectionIssueTools = computed(() =>
    toolsStore.tools.filter((tool) => toolHasConnectionAttention(tool, connectionForTool(tool))),
  );
  const publishedWithConnectionIssueCount = computed(
    () => connectionIssueTools.value.filter((tool) => tool.status === "Published").length,
  );
  const toolSummaryItems = computed<ManagementSummaryItem[]>(() => {
    const tools = toolsStore.tools;
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
  const selectedTool = computed(
    () => toolsStore.tools.find((tool) => tool.id === selectedToolId.value) || toolsStore.tools[0],
  );
  const detailTool = computed(
    () => toolsStore.tools.find((tool) => tool.id === detailToolId.value) || selectedTool.value,
  );
  const detailConnection = computed(() => (detailTool.value ? connectionForTool(detailTool.value) : undefined));
  const draftConnection = computed(
    () =>
      connectionById(draftTool.value.connectionId, draftTool.value.workspaceId) ||
      connectionsForWorkspace(draftTool.value.workspaceId)[0],
  );
  const detailRequestContract = computed(() =>
    buildBodyContractFromRequestParams(detailTool.value?.requestParams || []),
  );
  const detailResponseNodes = computed(() => buildResponseContractFromFields(detailTool.value?.responseFields || []));
  const toolEditorTitle = computed(() => (toolEditorMode.value === "edit" ? "编辑 Tool" : "注册 Tool"));
  const toolColumns = computed<ManagementListColumn<Tool>[]>(() => [
    {
      key: "tool",
      label: "工具名称",
      width: 200,
      sortable: true,
      sortKey: "name",
      getValue: (tool) => `${tool.name} ${tool.description}`,
    },
    {
      key: "type",
      label: "工具类型",
      width: 95,
      hidable: true,
      sortable: true,
      sortKey: "protocol",
      getValue: getToolTypeLabel,
    },
    { key: "protocol", label: "协议类型", width: 95, hidable: true, getValue: toolProtocolLabel },
    { key: "method", label: "Method", width: 70, hidable: true, align: "center", sortable: true, getValue: methodOf },
    { key: "path", label: "Path", width: 170, hidable: true, getValue: pathOf },
    {
      key: "connection",
      label: "Provider / 服务连接",
      width: 140,
      hidable: true,
      getValue: toolProviderConnectionLabel,
    },
    {
      key: "status",
      label: "状态",
      width: 140,
      hidable: true,
      align: "center",
      sortable: true,
      sortKey: "status",
      getValue: (tool) => toolUnifiedStatus(tool).label,
    },
    { key: "version", label: "版本", width: 80, hidable: true, getValue: toolVersionLabel },
    {
      key: "updatedAt",
      label: "更新时间",
      width: 125,
      hidable: true,
      sortable: true,
      sortKey: "updatedAt",
      getValue: formatToolTableUpdatedAt,
    },
    { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
  ]);
  const hasUnsavedToolChanges = computed(
    () => toolEditorVisible.value && draftSnapshot.value !== serializeDraftForSnapshot(),
  );
  const saveStateLabel = computed(() => {
    if (saveState.value === "saving") return "保存中";
    if (saveState.value === "saved") return "已保存";
    if (saveState.value === "failed") return "保存失败";
    if (hasUnsavedToolChanges.value) return "有未保存修改";
    return "草稿未保存";
  });
  const draftPublishChecklist = computed(() =>
    buildToolPublishChecklist(buildDraftTool(), draftConnection.value, {
      agentImpactConfirmed: publishImpactConfirmed.value,
    }),
  );
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
    get: () =>
      draftTool.value.requestContract.filter((node) => ["Path", "Query", "Header"].includes(node.location || "")),
    set: (nextNodes: ToolSchemaNode[]) => {
      draftTool.value.requestContract = [...nextNodes, ...requestBodyContract.value];
    },
  });
  const requestBodyContract = computed({
    get: () =>
      draftTool.value.requestContract.filter((node) => !["Path", "Query", "Header"].includes(node.location || "")),
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
  const completedBaseRequiredCount = computed(
    () =>
      [
        draftTool.value.name.trim(),
        draftTool.value.workspaceId,
        draftTool.value.connectionId,
        draftTool.value.method,
        draftTool.value.path.trim().startsWith("/") ? draftTool.value.path : "",
      ].filter(Boolean).length,
  );
  const draftSuggestionCount = computed(
    () => Number(requestContractCount.value === 0) + Number(responseContractCount.value === 0),
  );
  const draftCompletionPercent = computed(() =>
    Math.min(
      100,
      Math.round((completedBaseRequiredCount.value / 5) * 70) +
        (requestContractCount.value > 0 ? 20 : 0) +
        (responseContractCount.value > 0 ? 10 : 0),
    ),
  );

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
    selectedToolId.value = toolsStore.tools[0]?.id || "";
  }

  watch(
    () => toolsStore.tools.length,
    () => {
      if (!toolsStore.tools.some((tool) => tool.id === selectedToolId.value)) {
        selectedToolId.value = toolsStore.tools[0]?.id || "";
      }
      if (detailToolId.value && !toolsStore.tools.some((tool) => tool.id === detailToolId.value)) {
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
      connectionId: connectionsStore.serviceConnections[0]?.id || "",
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
    return normalizeSchemaNode(
      param.schema || {
        id: newDraftRowId("request"),
        location: param.location,
        name: param.name,
        type: (param.type as ToolSchemaNodeType) || "string",
        required: param.required,
        description: param.description,
        valueSource: param.valueSource,
        defaultValue: param.defaultValue,
      },
    );
  }

  function responseFieldToSchemaNode(field: ToolResponseField): ToolSchemaNode {
    return normalizeSchemaNode(
      field.schema || {
        id: newDraftRowId("response"),
        name: field.name,
        type: (field.type as ToolSchemaNodeType) || "string",
        required: true,
        description: field.description,
      },
    );
  }

  function connectionsForWorkspace(workspaceId: string) {
    const scoped = toolsStore.toolConnectionsByWorkspace?.[workspaceId];
    if (scoped) return scoped;
    return workspaceId === workspaces.activeWorkspaceId ? connectionsStore.serviceConnections : [];
  }

  function connectionById(connectionId: string, workspaceId = workspaces.activeWorkspaceId) {
    return connectionsForWorkspace(workspaceId).find((connection) => connection.id === connectionId);
  }

  function connectionForTool(tool: Tool) {
    return connectionById(tool.connectionId, tool.workspaceId);
  }

  function providerForTool(tool: Tool) {
    return providersStore.providers.find((provider) => provider.id === tool.providerId);
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
    return String(tool.actionConfig?.method || "GET");
  }

  function pathOf(tool: Tool) {
    return String(tool.actionConfig?.path || "/");
  }

  function methodClass(tool: Tool) {
    return methodOf(tool).toLowerCase();
  }

  function statusClass(status: string) {
    return String(status || "draft")
      .toLowerCase()
      .replace(/\s+/g, "-");
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
    return toolsStore.tools.find((item) => item.id === tool.id) || tool;
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
    const actionPath = (draftTool.value.path || "/").startsWith("/")
      ? draftTool.value.path || "/"
      : `/${draftTool.value.path}`;
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
    return (
      rateLimitPolicyOptions.find((option) => option.value === policy) || { label: policy || "-", description: "" }
    );
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

  function closeFloatingMenus() {}

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
    return toolsStore.loadToolPage({
      query: overrides.query ?? query.value,
      status: overrides.status ?? (selectedStatusFilter.value === "all" ? undefined : selectedStatusFilter.value),
      type: overrides.type ?? (selectedToolTypeFilter.value === "all" ? undefined : selectedToolTypeFilter.value),
      page: overrides.page ?? toolsStore.toolPagination.page,
      pageSize: overrides.pageSize ?? toolsStore.toolPagination.pageSize,
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
      pageSize: toolsStore.toolPagination.pageSize,
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
    await toolsStore.deleteTool(tool.id);
    const page =
      toolsStore.toolPageItems.length === 0 && toolsStore.toolPagination.page > 1
        ? toolsStore.toolPagination.page - 1
        : toolsStore.toolPagination.page;
    await loadToolRegistry({ page });
    if (selectedToolId.value === tool.id) {
      selectedToolId.value = toolsStore.tools[0]?.id || "";
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
    const actionConfig = tool.actionConfig || {};
    const runtimePolicy = tool.runtimePolicy || {
      timeoutMs: 8000,
      retryCount: 0,
      backoffPolicy: "exponential",
      idempotencyPolicy: "header: Idempotency-Key",
      rateLimitPolicy: "60 rpm",
    };
    const requestParams = Array.isArray(tool.requestParams) ? tool.requestParams : [];
    const responseFields = Array.isArray(tool.responseFields) ? tool.responseFields : [];
    const errorMappings = Array.isArray(tool.errorMappings) ? tool.errorMappings : [];
    return {
      id: tool.id,
      name: tool.name || "",
      workspaceId: tool.workspaceId || workspaces.activeWorkspaceId || workspaces.items[0]?.id || "",
      connectionId: tool.connectionId || "",
      method: String(actionConfig.method || "GET"),
      path: String(actionConfig.path || "/"),
      contentType: String(actionConfig.contentType || "application/json"),
      description: tool.description || "",
      status: tool.status || "Draft",
      requestContract: requestParams.length
        ? requestParams.map((param) => requestParamToSchemaNode(param))
        : [normalizeSchemaNode({ location: "Body", name: "", type: "string", required: true, description: "" })],
      responseContract: responseFields.length
        ? responseFields.map((field) => responseFieldToSchemaNode(field))
        : [normalizeSchemaNode({ name: "", type: "string", required: true, description: "" })],
      errorMappings: errorMappings.map((mapping) => ({ ...mapping })),
      timeoutSeconds: Math.max(1, Math.round(Number(runtimePolicy.timeoutMs) / 1000) || 8),
      retryCount: Number(runtimePolicy.retryCount) || 0,
      backoffPolicy: String(runtimePolicy.backoffPolicy || "exponential"),
      idempotencyPolicy: String(runtimePolicy.idempotencyPolicy || "header: Idempotency-Key"),
      rateLimitPolicy: String(runtimePolicy.rateLimitPolicy || "60 rpm"),
    };
  }

  function openEditTool(tool: Tool) {
    try {
      if (tool.status === "Published") {
        setActionFeedback("已发布 Tool 的编辑会从该版本创建新的 Draft Version，原 Release 保持不变。", "success");
      }
      toolEditorMode.value = "edit";
      editingToolId.value = tool.id;
      draftStep.value = 1;
      actionNote.value = tool.status === "Published" ? actionNote.value : "";
      draftError.value = "";
      contractEditorTab.value = "Body";
      runtimeAdvancedOpen.value = false;
      toolDetailVisible.value = false;
      testDialogVisible.value = false;
      draftTool.value = buildDraftFromTool(tool);
      draftSnapshot.value = JSON.stringify(draftTool.value);
      saveState.value = "idle";
      publishImpactConfirmed.value = false;
      toolEditorVisible.value = true;
    } catch (error) {
      const message = error instanceof Error ? error.message : "未知错误";
      setActionFeedback(`无法打开编辑：${message}`, "error");
      draftError.value = `无法打开编辑：${message}`;
      toolEditorVisible.value = false;
    }
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
        draftTool.value.name.trim() &&
        draftTool.value.workspaceId &&
        draftTool.value.connectionId &&
        draftTool.value.method &&
        draftTool.value.path.trim().startsWith("/"),
      );
    }
    if (step === 2) {
      return (
        schemaNodesValid(draftTool.value.requestContract) &&
        schemaNodesValid(draftTool.value.responseContract) &&
        draftTool.value.errorMappings.every((mapping) =>
          Boolean(mapping.protocolStatus.trim() && mapping.errorCode.trim()),
        )
      );
    }
    return true;
  }

  function schemaNodesValid(nodes: ToolSchemaNode[]): boolean {
    return nodes.every(
      (node) =>
        Boolean(node.name.trim() && node.type) &&
        schemaNodesValid(node.children || []) &&
        (!node.item || schemaNodesValid([node.item])),
    );
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
      (total, node) =>
        total + 1 + countSchemaNodes(node.children || []) + (node.item ? countSchemaNodes([node.item]) : 0),
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
    return location === "Body"
      ? requestBodyContract.value.length
      : draftTool.value.requestContract.filter((node) => node.location === location).length;
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
      ? toolsStore.tools.find((tool) => tool.id === editingToolId.value) ||
        toolsStore.toolPageItems.find((tool) => tool.id === editingToolId.value)
      : undefined;
    const toolName = draftTool.value.name.trim() || "未命名 Tool";
    const providerId =
      existing?.providerId || draftConnection.value?.providerId || providersStore.providers[0]?.id || "";
    return {
      id: toolEditorMode.value === "edit" ? editingToolId.value : "",
      workspaceId: draftTool.value.workspaceId.trim() || "default",
      providerId,
      sourceAssetId: existing?.sourceAssetId,
      sourceEndpointId: existing?.sourceEndpointId,
      connectionId: draftTool.value.connectionId,
      defaultConnectionId: draftTool.value.connectionId,
      name: toolName,
      slug:
        existing?.slug ||
        toolName
          .toLocaleLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/^-|-$/g, "") ||
        `tool-${Date.now()}`,
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
      const published = await toolsStore.publishTool(current.id);
      setActionFeedback(
        current.status === "Disabled" ? `${published.name} 已重新发布。` : `${published.name} 已发布。`,
      );
    } catch (error) {
      setActionFeedback(toolActionErrorMessage(error, "发布失败，请稍后重试。"), "error");
    }
  }

  async function enableTool(tool: Tool) {
    const current = latestTool(tool);
    try {
      const enabled = await toolsStore.updateTool(current.id, { ...current, status: "Review" });
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
      const disabled = await toolsStore.updateTool(current.id, { ...current, status: "Disabled" });
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
          ? await toolsStore.updateTool(editingToolId.value, toolPayload)
          : await toolsStore.createTool(toolPayload);
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
    const tool = toolsStore.tools.find((item) => item.id === editingToolId.value);
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

  return {
    toolsStore,
    providersStore,
    connectionsStore,
    workspaces,
    canEditWorkspace,
    canTestWorkspace,
    router,
    query,
    selectedStatusFilter,
    selectedToolTypeFilter,
    selectedToolRowKeys,
    selectedToolId,
    detailToolId,
    detailTab,
    toolDetailVisible,
    toolDetailModalRef,
    toolEditorVisible,
    toolEditorModalRef,
    draftStep,
    toolEditorMode,
    editingToolId,
    actionNote,
    actionNoteTone,
    draftError,
    saveState,
    draftSnapshot,
    publishImpactConfirmed,
    testDialogVisible,
    testDialogTool,
    contractEditorTab,
    runtimeAdvancedOpen,
    riskConfirmationVisible,
    riskConfirmationModalRef,
    pendingRiskAction,
    toolEditorSteps,
    contractEditorTabs,
    detailTabs,
    methodOptions,
    contentTypeOptions,
    backoffPolicyOptions,
    rateLimitPolicyOptions,
    toolStatusOptions,
    toolStatusHelperText,
    statusTabs,
    toolTypeTabs,
    draftTool,
    hasToolRecords,
    connectionIssueTools,
    publishedWithConnectionIssueCount,
    toolSummaryItems,
    hasWorkspaceContext,
    workspaceOptions,
    selectedTool,
    detailTool,
    detailConnection,
    draftConnection,
    detailRequestContract,
    detailResponseNodes,
    toolEditorTitle,
    toolColumns,
    hasUnsavedToolChanges,
    saveStateLabel,
    draftPublishChecklist,
    draftChecklistHasBlockingErrors,
    draftChecklistHasWarnings,
    canPublishDraftTool,
    serviceConnectionOptions,
    requestTransportContract,
    requestBodyContract,
    responseBodyContract,
    activeRequestFlatContract,
    requestContractCount,
    responseContractCount,
    completedBaseRequiredCount,
    draftSuggestionCount,
    draftCompletionPercent,
    loadToolPageAssets,
    defaultToolDraft,
    newDraftRowId,
    normalizeSchemaNode,
    requestParamToSchemaNode,
    responseFieldToSchemaNode,
    connectionsForWorkspace,
    connectionById,
    connectionForTool,
    providerForTool,
    toolProtocolLabel,
    toolProviderConnectionLabel,
    workspaceLabel,
    workspaceDisplayLabel,
    methodOf,
    pathOf,
    methodClass,
    statusClass,
    toolStatusLabel,
    lifecycleStatus,
    testStatus,
    runStatus,
    governanceToneClass,
    toolUnifiedStatus,
    latestTool,
    setActionFeedback,
    hasPassingTest,
    canPublishTool,
    toolPublishActionLabel,
    toolPublishButtonLabel,
    toolAvailabilityActionLabel,
    toolAvailabilityButtonLabel,
    toolAvailabilityActionIcon,
    toolLastTestSummary,
    toolLastTestDetail,
    toolPublishReadinessLabel,
    authModeLabel,
    connectionDomainLabel,
    connectionBasePathLabel,
    endpointPreviewLabel,
    serviceConnectionStatusLabel,
    environmentLabel,
    backoffPolicyMeta,
    rateLimitPolicyMeta,
    paramsReady,
    timeoutLabel,
    retryLabel,
    toolSummaryMeta,
    toolVersionLabel,
    toolEndpointSummary,
    agentImpactLabel,
    formatToolTableUpdatedAt,
    serializeDraftForSnapshot,
    selectStatusFilter,
    selectToolTypeFilter,
    resetFilters,
    selectDetailTab,
    handleDetailTabKeydown,
    closeFloatingMenus,
    toolMenuActions,
    handleToolRowAction,
    loadToolRegistry,
    setToolSearch,
    changeToolPage,
    changeToolSort,
    openToolDetail,
    closeToolDetail,
    openToolTestDialog,
    deleteTool,
    openRiskConfirmation,
    closeRiskConfirmation,
    riskConfirmationTitle,
    riskConfirmationPrimaryLabel,
    confirmRiskAction,
    openCreateTool,
    buildDraftFromTool,
    openEditTool,
    closeToolEditor,
    goToDraftStep,
    isDraftStepComplete,
    schemaNodesValid,
    draftStepState,
    draftStepCanProceed,
    countSchemaNodes,
    maxSchemaDepth,
    contractSummary,
    requestLocationCount,
    contractEditorTabCount,
    contractEditorHint,
    addErrorMapping,
    removeErrorMapping,
    buildDraftTool,
    toolSaveErrorMessage,
    toolActionErrorMessage,
    publishTool,
    enableTool,
    disableTool,
    toggleToolAvailability,
    persistDraftTool,
    saveDraftTool,
    goPreviousStep,
    goNextStep,
    publishDraftTool,
  };
}
