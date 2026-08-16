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
import { useAuthStore } from "../stores/auth";
import { tt } from "../i18n/tt";
import { apiClient, apiErrorMessage } from "../services/api";
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
import { buildDefaultToolTestInput } from "../utils/tool-test-inputs";
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
  type RiskActionType = "disable" | "enable" | "delete" | "batch-delete" | "batch-force-publish" | "";

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
  const auth = useAuthStore();
  const maintainerLabels = ref<Record<string, string>>({});
  const canEditWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "EDIT"),
  );
  const canTestWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "TEST"),
  );
  const canPublishWorkspace = computed(() =>
    workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "PUBLISH"),
  );
  /** Force-publish is PLATFORM_ADMIN only (server also gates tools.allowForcePublish). */
  const canForcePublishTools = computed(
    () => auth.user?.platformRole === "PLATFORM_ADMIN" && canPublishWorkspace.value,
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
  const pendingRiskAction = ref<{ type: RiskActionType; tool: Tool | null; tools: Tool[] }>({
    type: "",
    tool: null,
    tools: [],
  });
  const batchTesting = ref(false);
  const batchDeleting = ref(false);
  const batchForcePublishing = ref(false);
  const forcePublishReason = ref("");
  const batchTestDialogVisible = ref(false);
  const batchTestModalRef = ref<HTMLElement | null>(null);
  const batchPassthroughToken = ref("");
  const batchPassthroughExpiresAt = ref("");
  const batchTestProgress = ref({ current: 0, total: 0 });

  const selectedTools = computed(() => {
    const keys = new Set(selectedToolRowKeys.value.map(String));
    if (!keys.size) return [] as Tool[];
    const byId = new Map<string, Tool>();
    for (const tool of [...toolsStore.toolPageItems, ...toolsStore.tools]) {
      if (keys.has(tool.id)) byId.set(tool.id, tool);
    }
    return Array.from(byId.values());
  });

  /** Selected tools whose connection needs REQUEST_PASSTHROUGH business token. */
  const batchTestNeedsPassthrough = computed(() =>
    selectedTools.value.some((tool) => connectionForTool(tool)?.outboundMode === "REQUEST_PASSTHROUGH"),
  );

  const batchTestPassthroughConnectionIds = computed(() => {
    const ids = new Set<string>();
    for (const tool of selectedTools.value) {
      const connection = connectionForTool(tool);
      if (connection?.outboundMode === "REQUEST_PASSTHROUGH" && tool.connectionId) {
        ids.add(tool.connectionId);
      }
    }
    return [...ids];
  });

  const toolEditorSteps = computed(() => [
    [tt("tools.stepBasicsTitle"), tt("tools.stepBasicsDesc")],
    [tt("tools.stepContractTitle"), tt("tools.stepContractDesc")],
    [tt("tools.stepConfirmTitle"), tt("tools.stepConfirmDesc")],
  ]);

  const contractEditorTabs: Array<{ value: ContractEditorTab; label: string }> = [
    { value: "Path", label: "Path" },
    { value: "Query", label: "Query" },
    { value: "Header", label: "Header" },
    { value: "Body", label: "Body" },
    { value: "Response", label: "Response" },
    { value: "Errors", label: "Errors" },
  ];

  // Labels via tt so language switch updates (computed-friendly accessors use these ids).
  const detailTabs = computed(() => [
    { id: "base" as DetailTabId, label: tt("tools.baseInfo"), icon: "fa-solid fa-circle-info" },
    { id: "connection" as DetailTabId, label: tt("tools.connection"), icon: "fa-solid fa-server" },
    { id: "request" as DetailTabId, label: tt("tools.request"), icon: "fa-solid fa-list-check" },
    { id: "response" as DetailTabId, label: tt("tools.response"), icon: "fa-solid fa-code" },
    { id: "runtime" as DetailTabId, label: tt("tools.runtime"), icon: "fa-solid fa-sliders" },
    { id: "test" as DetailTabId, label: tt("tools.testPublish"), icon: "fa-solid fa-vial" },
  ]);

  const methodOptions = ["GET", "POST", "PATCH", "DELETE"].map((method) => ({ label: method, value: method }));
  const contentTypeOptions = ["application/json", "application/x-www-form-urlencoded", "multipart/form-data"].map(
    (contentType) => ({
      label: contentType,
      value: contentType,
    }),
  );
  const backoffPolicyOptions = computed(() => [
    { label: tt("tools.backoffFixed"), value: "fixed", description: tt("tools.backoffFixedDesc") },
    {
      label: tt("tools.backoffExponential"),
      value: "exponential",
      description: tt("tools.backoffExponentialDesc"),
    },
    { label: tt("tools.backoffLinear"), value: "linear", description: tt("tools.backoffLinearDesc") },
  ]);
  const rateLimitPolicyOptions = computed(() => [
    { label: tt("tools.rateStandard"), value: "60 rpm", description: tt("tools.rateStandardDesc") },
    { label: tt("tools.rateConservative"), value: "30 rpm", description: tt("tools.rateConservativeDesc") },
    { label: tt("tools.rateHigh"), value: "120 rpm", description: tt("tools.rateHighDesc") },
  ]);
  const toolStatusOptions = computed(
    (): Array<{ label: string; value: ToolStatus }> => [
      { label: tt("tools.draft"), value: "Draft" },
      { label: tt("tools.review"), value: "Review" },
      { label: tt("tools.tested"), value: "Tested" },
      { label: tt("tools.published"), value: "Published" },
      { label: tt("tools.disabled"), value: "Disabled" },
    ],
  );
  const toolStatusHelperText = computed(() => tt("tools.statusHelper"));
  const statusTabs = computed(
    (): Array<{ label: string; value: ToolStatusFilter }> => [
      { label: tt("tools.statusAll"), value: "all" },
      { label: tt("tools.attention"), value: "attention" },
      { label: tt("tools.published"), value: "Published" },
      { label: tt("tools.tested"), value: "Tested" },
      { label: tt("tools.review"), value: "Review" },
      { label: tt("tools.draft"), value: "Draft" },
      { label: tt("tools.disabled"), value: "Disabled" },
    ],
  );
  const toolTypeTabs = computed(
    (): Array<{ label: string; value: ToolTypeFilter }> => [
      { label: tt("tools.typeAll"), value: "all" },
      { label: "HTTP", value: "HTTP Tool" },
      { label: "Workflow", value: "Workflow Tool" },
    ],
  );

  const draftTool = ref<ToolDraft>(defaultToolDraft());

  const hasToolRecords = computed(
    () =>
      (toolsStore.toolListSummary?.total ?? 0) > 0 ||
      (toolsStore.toolPagination?.total ?? 0) > 0 ||
      toolsStore.toolPageItems.length > 0,
  );
  /** Tools on the current page whose bound connection needs attention (KPI for attention is page-aware). */
  const connectionIssueTools = computed(() =>
    toolsStore.toolPageItems.filter((tool) => toolHasConnectionAttention(tool, connectionForTool(tool))),
  );
  const publishedWithConnectionIssueCount = computed(
    () => connectionIssueTools.value.filter((tool) => tool.status === "Published").length,
  );
  const toolSummaryItems = computed<ManagementSummaryItem[]>(() => {
    const summary = toolsStore.toolListSummary || {
      total: toolsStore.toolPagination?.total || 0,
      published: 0,
      tested: 0,
      draft: 0,
      review: 0,
      disabled: 0,
    };
    const publishedCount = summary.published;
    const pendingPublishCount = summary.tested;
    // Connection health still requires the connection catalog; show current-page issues as a soft signal.
    const connectionIssueCount = connectionIssueTools.value.length;
    const publishedIssues = publishedWithConnectionIssueCount.value;
    return [
      { label: tt("tools.summaryTotal"), value: summary.total, icon: "fa-solid fa-screwdriver-wrench" },
      {
        label: tt("tools.summaryPublished"),
        value: publishedCount,
        icon: "fa-solid fa-circle-check",
        note: publishedIssues > 0 ? tt("tools.publishedIssues", { n: publishedIssues }) : undefined,
        tone: publishedIssues > 0 ? "warning" : "default",
      },
      {
        label: tt("tools.summaryPending"),
        value: pendingPublishCount,
        icon: "fa-solid fa-vial",
        tone: "info",
      },
      {
        // Connection attention: page-scoped until a server-side connection-health filter exists.
        label: tt("tools.summaryAttention"),
        value: connectionIssueCount,
        icon: "fa-solid fa-triangle-exclamation",
        note: connectionIssueCount > 0 ? tt("tools.pageConnectionIssue") : undefined,
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
  const toolEditorTitle = computed(() =>
    toolEditorMode.value === "edit" ? tt("tools.editToolTitle") : tt("tools.registerToolTitle"),
  );
  const toolColumns = computed<ManagementListColumn<Tool>[]>(() => [
    {
      key: "tool",
      label: tt("tools.colName"),
      width: 200,
      sortable: true,
      sortKey: "name",
      getValue: (tool) => `${tool.name} ${tool.description}`,
    },
    {
      key: "type",
      label: tt("tools.colType"),
      width: 95,
      hidable: true,
      sortable: true,
      sortKey: "protocol",
      getValue: getToolTypeLabel,
    },
    { key: "protocol", label: tt("tools.colProtocol"), width: 95, hidable: true, getValue: toolProtocolLabel },
    { key: "method", label: "Method", width: 70, hidable: true, align: "center", sortable: true, getValue: methodOf },
    { key: "path", label: "Path", width: 170, hidable: true, getValue: pathOf },
    {
      key: "connection",
      label: tt("tools.colProvider"),
      width: 140,
      hidable: true,
      getValue: toolProviderConnectionLabel,
    },
    {
      key: "status",
      label: tt("tools.colStatus"),
      width: 140,
      hidable: true,
      align: "center",
      sortable: true,
      sortKey: "status",
      getValue: (tool) => toolUnifiedStatus(tool).label,
    },
    { key: "version", label: tt("tools.colVersion"), width: 80, hidable: true, getValue: toolVersionLabel },
    {
      key: "updatedAt",
      label: tt("tools.colUpdated"),
      width: 125,
      hidable: true,
      sortable: true,
      sortKey: "updatedAt",
      getValue: formatToolTableUpdatedAt,
    },
    { key: "actions", label: tt("tools.colActions"), width: 68, align: "right", headerAlign: "center" },
  ]);
  const hasUnsavedToolChanges = computed(
    () => toolEditorVisible.value && draftSnapshot.value !== serializeDraftForSnapshot(),
  );
  const saveStateLabel = computed(() => {
    if (saveState.value === "saving") return tt("tools.saving");
    if (saveState.value === "saved") return tt("tools.saved");
    if (saveState.value === "failed") return tt("tools.saveFailed");
    if (hasUnsavedToolChanges.value) return tt("tools.unsavedChanges");
    return tt("tools.draftUnsaved");
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
      return [{ label: tt("tools.noServiceConnections"), value: "", disabled: true }];
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
      name: "",
      workspaceId,
      connectionId: connectionsStore.serviceConnections[0]?.id || "",
      method: "POST",
      path: "",
      contentType: "application/json",
      description: "",
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
    const connection = connectionForTool(tool)?.name || tt("tools.connectionMissing");
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
    return toolStatusOptions.value.find((option) => option.value === status)?.label || status;
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
    if (!tool) return tt("tools.publishTool");
    return tool.status === "Disabled" ? tt("tools.republish") : tt("tools.publishTool");
  }

  function toolPublishButtonLabel(tool?: Tool | null) {
    if (!tool) return tt("tools.publishOnline");
    return tool.status === "Disabled" ? tt("tools.republish") : tt("tools.publishOnline");
  }

  function toolAvailabilityActionLabel(tool?: Tool | null) {
    return tool?.status === "Disabled" ? tt("tools.enableTool") : tt("tools.disableTool");
  }

  function toolAvailabilityButtonLabel(tool?: Tool | null) {
    return tool?.status === "Disabled" ? tt("common.enable") : tt("common.disable");
  }

  function toolAvailabilityActionIcon(tool?: Tool | null) {
    return tool?.status === "Disabled" ? "fa-solid fa-play" : "fa-solid fa-ban";
  }

  function toolLastTestSummary(tool?: Tool | null) {
    if (!tool?.lastTestResult) return tt("tools.govWaitTest");
    const latency = tool.lastTestResult.latencyMs ? ` · ${tool.lastTestResult.latencyMs}ms` : "";
    return hasPassingTest(tool)
      ? tt("tools.testPassedWithLatency", { latency })
      : tt("tools.testFailedWithLatency", { latency });
  }

  function toolLastTestDetail(tool?: Tool | null) {
    const result = tool?.lastTestResult;
    if (!result) return tt("tools.noTestRecordDetail");
    if (hasPassingTest(tool)) return tt("tools.allChecksPassedDetail");

    const failedChecks = [
      result.connectivityPassed === false ? tt("tools.connectivityFailed") : "",
      result.responseSchemaPassed === false ? tt("tools.responseSchemaFailed") : "",
      result.errorMappingPassed === false ? tt("tools.errorMappingFailed") : "",
      result.runtimePolicyPassed === false ? tt("tools.runtimePolicyFailed") : "",
    ].filter(Boolean);

    if (failedChecks.length) {
      return tt("tools.failedChecksDetail", { checks: failedChecks.join("、") });
    }
    return tt("tools.testNotPassedDetail");
  }

  function toolPublishReadinessLabel(tool?: Tool | null) {
    if (!tool) return tt("tools.publishNeedsTest");
    if (canPublishTool(tool)) {
      return tool.status === "Disabled" ? tt("tools.disabledCanRepublish") : tt("tools.testPassedCanPublish");
    }
    if (tool.status === "Published") {
      return tt("tools.alreadyPublished");
    }
    if (tool.status === "Disabled") {
      return tt("tools.disabledNeedRetest");
    }
    return tt("tools.publishNeedsTest");
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
    if (!connection) return tt("tools.connNotConfigured");
    if (connection.status === "Available" || connection.status === "VERIFIED") return tt("tools.connAvailable");
    if (connection.status === "Expiring soon") return tt("tools.connExpiring");
    if (["Needs attention", "UNVERIFIED", "ERROR"].includes(connection.status)) return tt("tools.connNeedsAttention");
    if (connection.status === "DISABLED") return tt("tools.connDisabled");
    return connection.status;
  }

  function environmentLabel(value: string) {
    return value || "-";
  }

  function backoffPolicyMeta(policy: string) {
    return (
      backoffPolicyOptions.value.find((option) => option.value === policy) || {
        label: policy || "-",
        description: "",
      }
    );
  }

  function rateLimitPolicyMeta(policy: string) {
    return (
      rateLimitPolicyOptions.value.find((option) => option.value === policy) || {
        label: policy || "-",
        description: "",
      }
    );
  }

  function paramsReady(tool: Tool) {
    return `${tool.requestParams.length}/${Math.max(tool.requestParams.length, tool.responseFields.length)}`;
  }

  function timeoutLabel(tool: Tool) {
    return `${Math.round(tool.runtimePolicy.timeoutMs / 1000)}s`;
  }

  function retryLabel(tool: Tool) {
    return tt("tools.retryCountLabel", { n: tool.runtimePolicy.retryCount });
  }

  function toolSummaryMeta(tool: Tool) {
    const workspace = workspaceDisplayLabel(tool.workspaceId);
    const connection = connectionForTool(tool)?.name || tool.connectionId || "-";
    return `${workspace} · ${connection} · ${timeoutLabel(tool)} / ${retryLabel(tool)} · ${toolVersionLabel(tool)}`;
  }

  function toolVersionLabel(tool: Tool) {
    const version = tool.draftVersion || [...tool.versions].sort((left, right) => right.versionNo - left.versionNo)[0];
    return version ? `v${version.versionNo}` : tt("tools.noVersion");
  }

  function toolEndpointSummary(tool: Tool) {
    return `${methodOf(tool)} ${pathOf(tool)}`;
  }

  function agentImpactLabel(tool: Tool) {
    return tool.activeReleaseId ? tt("tools.agentBindAvailable") : tt("tools.agentBindUnavailable");
  }

  function toolImpactSummary(tool: Tool) {
    return tool.activeReleaseId ? tt("tools.impactPublishedSummary") : tt("tools.impactDraftSummary");
  }

  function toolMaintainerLabel(tool: Tool) {
    const id = (tool.updatedBy || tool.createdBy || "").trim();
    const currentUser = auth.user;
    const name =
      maintainerLabels.value[id] || (currentUser?.id === id ? currentUser.username || currentUser.displayName : "");
    const actor = name || tt("tools.maintainerAccount");
    const time = tool.updatedAt ? formatToolTableUpdatedAt(tool) : "";
    return time ? `${actor} · ${time}` : actor;
  }

  async function resolveToolMaintainer(tool: Tool) {
    const id = (tool.updatedBy || tool.createdBy || "").trim();
    if (!id || maintainerLabels.value[id] || auth.user?.id === id) return;
    try {
      const response = await apiClient.get<{ items?: Array<{ id?: string; username?: string; displayName?: string }> }>(
        `/admin/users?query=${encodeURIComponent(id)}&page=1&pageSize=10`,
      );
      const match = response.data.items?.find((user) => user.id?.toLowerCase() === id.toLowerCase());
      if (match)
        maintainerLabels.value = { ...maintainerLabels.value, [id]: match.username || match.displayName || "" };
    } catch {
      /* Keep the human-safe fallback; raw UUID remains out of the primary UI. */
    }
  }

  function riskActionTools(): Tool[] {
    const { type, tool, tools } = pendingRiskAction.value;
    if (type === "batch-delete" || type === "batch-force-publish") return tools;
    return tool ? [tool] : [];
  }

  function riskConfirmationEyebrow() {
    const tools = riskActionTools();
    if (!tools.length) return tt("tools.riskEyebrowConfirm");
    if (pendingRiskAction.value.type === "batch-force-publish") return tt("tools.riskEyebrowForce");
    if (tools.some((tool) => tool.activeReleaseId)) return tt("tools.riskEyebrowHigh");
    return tt("tools.riskEyebrowConfirm");
  }

  function riskConfirmationDescription() {
    const { type } = pendingRiskAction.value;
    const tools = riskActionTools();
    if (!tools.length) return tt("tools.riskDescContinue");
    const publishedCount = tools.filter((tool) => tool.activeReleaseId).length;
    if (type === "batch-force-publish") {
      return tt("tools.riskDescForceBatch", { count: tools.length });
    }
    if (type === "batch-delete") {
      return publishedCount > 0
        ? tt("tools.riskDescBatchDeletePublished", { count: tools.length, published: publishedCount })
        : tt("tools.riskDescBatchDeleteDraft", { count: tools.length });
    }
    const tool = tools[0];
    const published = Boolean(tool.activeReleaseId);
    if (type === "delete") {
      return published ? tt("tools.riskDescDeletePublished") : tt("tools.riskDescDeleteDraft");
    }
    if (type === "disable") {
      return published ? tt("tools.riskDescDisablePublished") : tt("tools.riskDescDisableDraft");
    }
    if (type === "enable") {
      return tt("tools.riskDescEnable");
    }
    return tt("tools.riskDescContinue");
  }

  function riskImpactItems() {
    const { type } = pendingRiskAction.value;
    const tools = riskActionTools();
    if (!tools.length) return [];
    if (type === "batch-delete" || type === "batch-force-publish") {
      const publishedCount = tools.filter((tool) => tool.activeReleaseId).length;
      const forceTargets = tools.filter((tool) => tool.status !== "Published");
      return [
        {
          key: "count",
          label: type === "batch-force-publish" ? tt("tools.riskLabelForceCount") : tt("tools.riskLabelSelectedCount"),
          value:
            type === "batch-force-publish"
              ? tt("tools.riskValueForceCount", { force: forceTargets.length, selected: tools.length })
              : tt("tools.riskValueToolCount", { count: tools.length }),
          tone: "neutral",
        },
        {
          key: "binding",
          label: tt("tools.riskLabelAgentBinding"),
          value:
            type === "batch-force-publish"
              ? tt("tools.riskValueForceBinding")
              : publishedCount > 0
                ? tt("tools.riskValuePublishedMayBind", { count: publishedCount })
                : tt("tools.riskValueNonePublished"),
          tone: type === "batch-force-publish" || publishedCount > 0 ? "warn" : "ok",
        },
        {
          key: "workflow",
          label: type === "batch-force-publish" ? tt("tools.riskLabelTestGate") : tt("tools.riskLabelWorkflowRef"),
          value:
            type === "batch-force-publish"
              ? tt("tools.riskValueSkipTest")
              : publishedCount > 0
                ? tt("tools.riskValueWorkflowMayRef")
                : tt("tools.riskValueNoWorkflow"),
          tone: "warn",
        },
        {
          key: "names",
          label: tt("tools.riskLabelSampleNames"),
          value:
            tools
              .slice(0, 3)
              .map((tool) => tool.name)
              .join("、") + (tools.length > 3 ? tt("tools.riskValueNamesMore", { count: tools.length }) : ""),
          tone: "neutral",
        },
      ];
    }
    const tool = tools[0];
    const published = Boolean(tool.activeReleaseId);
    return [
      {
        key: "binding",
        label: tt("tools.riskLabelAgentBinding"),
        value: published ? tt("tools.riskValueMayBindCheck") : tt("tools.riskValueNotPublishedBind"),
        tone: published ? "warn" : "ok",
      },
      {
        key: "workflow",
        label: tt("tools.riskLabelWorkflowRef"),
        value: published ? tt("tools.riskValueWorkflowCheck") : tt("tools.riskValueNotPublishedWorkflow"),
        tone: published ? "warn" : "ok",
      },
      {
        key: "version",
        label: tt("tools.riskLabelVersion"),
        value: toolVersionLabel(tool),
        tone: "neutral",
      },
      {
        key: "endpoint",
        label: tt("tools.riskLabelEndpoint"),
        value: toolEndpointSummary(tool),
        tone: "neutral",
      },
    ];
  }

  function riskConfirmationToneClass() {
    if (pendingRiskAction.value.type === "batch-force-publish") return "is-elevated-risk";
    return riskActionTools().some((tool) => tool.activeReleaseId) ? "is-elevated-risk" : "is-standard-risk";
  }

  function riskConfirmationTargetName() {
    const { type, tool, tools } = pendingRiskAction.value;
    if (type === "batch-delete" || type === "batch-force-publish") {
      if (!tools.length) return tt("tools.riskNoToolSelected");
      if (tools.length === 1) return tools[0].name;
      return tt("tools.riskTargetMultiple", { name: tools[0].name, count: tools.length });
    }
    return tool?.name || tt("tools.unnamedTool");
  }

  function riskConfirmationTargetMeta() {
    const { type, tool, tools } = pendingRiskAction.value;
    if (type === "batch-force-publish") {
      return tt("tools.riskMetaForce", { count: tools.length });
    }
    if (type === "batch-delete") {
      const publishedCount = tools.filter((item) => item.activeReleaseId).length;
      return publishedCount > 0
        ? tt("tools.riskMetaBatchPublished", { count: tools.length, published: publishedCount })
        : tt("tools.riskMetaBatchNonePublished", { count: tools.length });
    }
    return tool ? toolEndpointSummary(tool) : "";
  }

  function formatToolTableUpdatedAt(tool: Tool) {
    if (!tool.updatedAt) return tt("common.noData");
    const date = new Date(tool.updatedAt);
    if (Number.isNaN(date.getTime())) return tt("common.noData");
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
    const tabs = detailTabs.value;
    const currentIndex = tabs.findIndex((tab) => tab.id === currentTabId);
    if (currentIndex < 0) return;

    const lastIndex = tabs.length - 1;
    const nextIndexByKey: Record<string, number> = {
      ArrowLeft: currentIndex === 0 ? lastIndex : currentIndex - 1,
      ArrowRight: currentIndex === lastIndex ? 0 : currentIndex + 1,
      Home: 0,
      End: lastIndex,
    };
    const nextIndex = nextIndexByKey[event.key];
    if (nextIndex === undefined) return;

    event.preventDefault();
    const nextTabId = tabs[nextIndex].id;
    selectDetailTab(nextTabId);
    void nextTick(() => document.getElementById(`tool-detail-tab-${nextTabId}`)?.focus());
  }

  function closeFloatingMenus() {}

  function toolMenuActions(tool: Tool): ManagementRowAction[] {
    const publishable = canPublishTool(tool);
    return [
      { key: "detail", label: tt("tools.viewDetail"), icon: "fa-solid fa-eye", tone: "primary" },
      { key: "test", label: tt("tools.testTool"), icon: "fa-solid fa-vial" },
      { key: "edit", label: tt("tools.editTool"), icon: "fa-solid fa-pen" },
      {
        key: "publish",
        label: toolPublishActionLabel(tool),
        icon: "fa-solid fa-cloud-arrow-up",
        tone: "primary",
        disabled: !publishable,
        disabledReason: publishable ? undefined : tt("tools.publishNeedsPassingTest"),
      },
      { key: "availability", label: toolAvailabilityActionLabel(tool), icon: toolAvailabilityActionIcon(tool) },
      { key: "delete", label: tt("tools.deleteTool"), icon: "fa-solid fa-trash", tone: "danger" },
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

  async function openToolDetail(tool: Tool) {
    closeFloatingMenus();
    selectedToolId.value = tool.id;
    detailToolId.value = tool.id;
    toolDetailVisible.value = true;
    void resolveToolMaintainer(tool);
    // List rows only carry headVersion summary; hydrate full versions for detail panel.
    if (!toolHasFullVersions(tool)) {
      try {
        await toolsStore.loadToolVersions(tool.id, tool.workspaceId);
      } catch {
        /* keep summary row visible */
      }
    }
  }

  function toolHasFullVersions(tool: Tool) {
    return (tool.versions || []).some((version) => Boolean(version.checksum));
  }

  function closeToolDetail() {
    toolDetailVisible.value = false;
    detailToolId.value = "";
  }

  async function openToolTestDialog(tool: Tool) {
    closeFloatingMenus();
    let target = tool;
    if (!toolHasFullVersions(tool)) {
      try {
        target = await toolsStore.loadToolVersions(tool.id, tool.workspaceId);
      } catch {
        /* fall through with list summary */
      }
    }
    testDialogTool.value = target;
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
    setActionFeedback(tt("tools.deletedFromRuntime", { name: tool.name }));
  }

  function openRiskConfirmation(type: RiskActionType, tool: Tool) {
    closeFloatingMenus();
    pendingRiskAction.value = { type, tool, tools: [] };
    riskConfirmationVisible.value = true;
  }

  function openBatchDeleteConfirmation() {
    const tools = selectedTools.value;
    if (!tools.length || batchDeleting.value || batchTesting.value || batchForcePublishing.value) return;
    closeFloatingMenus();
    pendingRiskAction.value = {
      type: "batch-delete",
      tool: tools[0] || null,
      tools: [...tools],
    };
    riskConfirmationVisible.value = true;
  }

  function openBatchForcePublishConfirmation() {
    if (!canForcePublishTools.value) return;
    const tools = selectedTools.value.filter((item) => item.status !== "Published");
    if (!tools.length || batchForcePublishing.value || batchDeleting.value || batchTesting.value) return;
    closeFloatingMenus();
    forcePublishReason.value = "";
    pendingRiskAction.value = {
      type: "batch-force-publish",
      tool: tools[0] || null,
      tools: [...tools],
    };
    riskConfirmationVisible.value = true;
  }

  function closeRiskConfirmation() {
    if (batchDeleting.value || batchForcePublishing.value) return;
    riskConfirmationVisible.value = false;
    forcePublishReason.value = "";
    pendingRiskAction.value = { type: "", tool: null, tools: [] };
  }

  function riskConfirmationTitle() {
    const action = pendingRiskAction.value.type;
    if (action === "batch-force-publish") {
      const count = pendingRiskAction.value.tools.length;
      return count > 1 ? tt("tools.forcePublishMany", { count }) : tt("tools.forcePublishOne");
    }
    if (action === "batch-delete") {
      const count = pendingRiskAction.value.tools.length;
      return count > 1 ? tt("tools.confirmDeleteMany", { count }) : tt("tools.confirmDeleteOne");
    }
    if (action === "delete") return tt("tools.confirmDeleteTool");
    if (action === "disable") return tt("tools.confirmDisableTool");
    if (action === "enable") return tt("tools.confirmEnableTool");
    return tt("tools.confirmAction");
  }

  function riskConfirmationPrimaryLabel() {
    const action = pendingRiskAction.value.type;
    if (action === "batch-force-publish") {
      return batchForcePublishing.value
        ? tt("tools.publishing")
        : pendingRiskAction.value.tools.length > 1
          ? tt("tools.confirmForcePublishMany", { count: pendingRiskAction.value.tools.length })
          : tt("tools.confirmForcePublish");
    }
    if (action === "batch-delete") {
      return batchDeleting.value
        ? tt("tools.deleting")
        : pendingRiskAction.value.tools.length > 1
          ? tt("tools.confirmDeleteManyItems", { count: pendingRiskAction.value.tools.length })
          : tt("tools.confirmDelete");
    }
    if (action === "delete") return tt("tools.confirmDelete");
    if (action === "disable") return tt("tools.confirmDisable");
    if (action === "enable") return tt("tools.confirmEnable");
    return tt("common.confirm");
  }

  const forcePublishReasonValid = computed(() => forcePublishReason.value.trim().length >= 8);

  async function confirmRiskAction() {
    const { type, tool, tools } = pendingRiskAction.value;
    if (!type) return;
    if (type === "batch-force-publish") {
      if (!tools.length || batchForcePublishing.value || !forcePublishReasonValid.value) return;
      batchForcePublishing.value = true;
      let success = 0;
      let failed = 0;
      let lastError = "";
      const reason = forcePublishReason.value.trim();
      try {
        for (const item of tools) {
          try {
            await toolsStore.forcePublishTool(item.id, reason);
            success += 1;
          } catch (error) {
            failed += 1;
            lastError = apiErrorMessage(error, tt("tools.forcePublishFailed"));
          }
        }
        selectedToolRowKeys.value = [];
        await loadToolRegistry();
        if (failed === 0) {
          setActionFeedback(tt("tools.forcePublishSuccess", { count: success }));
        } else {
          setActionFeedback(
            tt("tools.forcePublishPartial", {
              success,
              failed,
              error: lastError ? ` ${lastError}` : "",
            }),
            "error",
          );
        }
      } finally {
        batchForcePublishing.value = false;
        closeRiskConfirmation();
      }
      return;
    }
    if (type === "batch-delete") {
      if (!tools.length || batchDeleting.value) return;
      batchDeleting.value = true;
      let success = 0;
      let failed = 0;
      try {
        for (const item of tools) {
          try {
            await toolsStore.deleteTool(item.id);
            success += 1;
          } catch {
            failed += 1;
          }
        }
        selectedToolRowKeys.value = [];
        await loadToolRegistry();
        if (selectedToolId.value && !toolsStore.tools.some((item) => item.id === selectedToolId.value)) {
          selectedToolId.value = toolsStore.tools[0]?.id || "";
        }
        if (detailToolId.value && tools.some((item) => item.id === detailToolId.value)) {
          closeToolDetail();
        }
        if (failed === 0) {
          setActionFeedback(tt("tools.batchDeleteSuccess", { count: success }));
        } else {
          setActionFeedback(tt("tools.batchDeletePartial", { success, failed }), "error");
        }
      } finally {
        batchDeleting.value = false;
        closeRiskConfirmation();
      }
      return;
    }
    if (!tool) return;
    if (type === "delete") {
      await deleteTool(tool);
    } else if (type === "disable") {
      await disableTool(tool);
    } else if (type === "enable") {
      await enableTool(tool);
    }
    closeRiskConfirmation();
  }

  function toDatetimeLocalValue(date: Date) {
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
  }

  /** Open batch-test confirm dialog (default inputs + optional shared passthrough token). */
  function batchTestSelectedTools() {
    const tools = selectedTools.value;
    if (!tools.length || batchTesting.value || batchDeleting.value) return;
    batchPassthroughToken.value = "";
    batchPassthroughExpiresAt.value = toDatetimeLocalValue(new Date(Date.now() + 60 * 60 * 1000));
    batchTestProgress.value = { current: 0, total: tools.length };
    batchTestDialogVisible.value = true;
  }

  function closeBatchTestDialog() {
    if (batchTesting.value) return;
    batchTestDialogVisible.value = false;
    batchPassthroughToken.value = "";
  }

  function normalizePassthroughToken(raw: string) {
    return raw
      .trim()
      .replace(/^Bearer\s+/i, "")
      .trim();
  }

  function buildBatchOutboundEnvelope(
    connectionId: string,
  ): import("../types/domain").OutboundCredentialsEnvelope | undefined {
    if (!batchTestNeedsPassthrough.value) return undefined;
    const token = normalizePassthroughToken(batchPassthroughToken.value);
    if (!token || !connectionId) return undefined;
    const expiresDate = batchPassthroughExpiresAt.value
      ? new Date(batchPassthroughExpiresAt.value)
      : new Date(Date.now() + 60 * 60 * 1000);
    if (Number.isNaN(expiresDate.getTime()) || expiresDate.getTime() <= Date.now() + 2 * 60 * 1000) {
      return undefined;
    }
    return {
      schemaVersion: "outbound-credentials.v1",
      bindings: [
        {
          connectionId,
          credentialType: "ACCESS_TOKEN",
          value: token,
          expiresAt: expiresDate.toISOString(),
        },
      ],
    };
  }

  async function confirmBatchTestSelectedTools() {
    const tools = selectedTools.value;
    if (!tools.length || batchTesting.value || batchDeleting.value) return;

    if (batchTestNeedsPassthrough.value) {
      const token = normalizePassthroughToken(batchPassthroughToken.value);
      if (!token) {
        setActionFeedback(tt("tools.batchNeedPassthroughToken"), "error");
        return;
      }
      const expiresDate = batchPassthroughExpiresAt.value
        ? new Date(batchPassthroughExpiresAt.value)
        : new Date(Date.now() + 60 * 60 * 1000);
      if (Number.isNaN(expiresDate.getTime()) || expiresDate.getTime() <= Date.now() + 2 * 60 * 1000) {
        setActionFeedback(tt("tools.batchExpiresTooSoon"), "error");
        return;
      }
    }

    batchTesting.value = true;
    let passed = 0;
    let failed = 0;
    let skipped = 0;
    const failureHints: string[] = [];
    batchTestProgress.value = { current: 0, total: tools.length };
    try {
      for (let index = 0; index < tools.length; index += 1) {
        const tool = tools[index];
        batchTestProgress.value = { current: index + 1, total: tools.length };
        // Prefer hydrated tool for versions when list row is summary-only.
        let target = tool;
        if (!(tool.versions || []).some((version) => Boolean(version.checksum))) {
          try {
            target = await toolsStore.loadToolVersions(tool.id, tool.workspaceId);
          } catch {
            failed += 1;
            failureHints.push(`${tool.name}: ${tt("tools.batchLoadVersionFailed")}`);
            continue;
          }
        }
        const draft = target.draftVersion;
        if (!draft || draft.lifecycleStatus === "PUBLISHED") {
          skipped += 1;
          continue;
        }
        if (!target.connectionId) {
          skipped += 1;
          continue;
        }
        try {
          const connection = connectionForTool(target);
          const envelope =
            connection?.outboundMode === "REQUEST_PASSTHROUGH"
              ? buildBatchOutboundEnvelope(target.connectionId)
              : undefined;
          if (connection?.outboundMode === "REQUEST_PASSTHROUGH" && !envelope) {
            failed += 1;
            failureHints.push(`${tool.name}: ${tt("tools.batchMissingPassthroughToken")}`);
            continue;
          }
          const result = await toolsStore.testTool(target.id, buildDefaultToolTestInput(target), envelope);
          if (result.passed) passed += 1;
          else {
            failed += 1;
            if (failureHints.length < 5) {
              failureHints.push(
                `${tool.name}: ${result.errorMessage || `HTTP ${result.responseStatus}` || tt("tools.batchFailed")}`,
              );
            }
          }
        } catch (error) {
          failed += 1;
          if (failureHints.length < 5) {
            const message = error instanceof Error ? error.message : tt("tools.batchException");
            failureHints.push(`${tool.name}: ${message}`);
          }
        }
      }
      await loadToolRegistry();
      const parts = [tt("tools.batchPassedCount", { n: passed }), tt("tools.batchFailedCount", { n: failed })];
      if (skipped) parts.push(tt("tools.batchSkippedCount", { n: skipped }));
      const hint =
        failureHints.length > 0 ? tt("tools.batchExamplePrefix", { hints: failureHints.slice(0, 3).join("；") }) : "";
      setActionFeedback(
        tt("tools.batchTestDone", {
          count: tools.length,
          parts: parts.join("，"),
          skipHint: skipped ? tt("tools.batchSkipHint") : "",
          examples: hint,
        }),
        failed > 0 ? "error" : "success",
      );
      batchTestDialogVisible.value = false;
      batchPassthroughToken.value = "";
      selectedToolRowKeys.value = [];
    } finally {
      batchTesting.value = false;
      batchTestProgress.value = { current: 0, total: 0 };
    }
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

  async function openEditTool(tool: Tool) {
    try {
      let editable = tool;
      if (!toolHasFullVersions(tool)) {
        editable = await toolsStore.loadToolVersions(tool.id, tool.workspaceId);
      }
      if (editable.status === "Published") {
        setActionFeedback(tt("tools.editCreatesDraft"), "success");
      }
      toolEditorMode.value = "edit";
      editingToolId.value = editable.id;
      draftStep.value = 1;
      actionNote.value = editable.status === "Published" ? actionNote.value : "";
      draftError.value = "";
      contractEditorTab.value = "Body";
      runtimeAdvancedOpen.value = false;
      toolDetailVisible.value = false;
      testDialogVisible.value = false;
      draftTool.value = buildDraftFromTool(editable);
      draftSnapshot.value = JSON.stringify(draftTool.value);
      saveState.value = "idle";
      publishImpactConfirmed.value = false;
      toolEditorVisible.value = true;
    } catch (error) {
      const message = error instanceof Error ? error.message : tt("tools.unknownError");
      setActionFeedback(tt("tools.cannotOpenEdit", { message }), "error");
      draftError.value = tt("tools.cannotOpenEdit", { message });
      toolEditorVisible.value = false;
    }
  }

  function closeToolEditor() {
    if (hasUnsavedToolChanges.value && !window.confirm(tt("tools.unsavedLeaveConfirm"))) {
      return;
    }
    toolEditorVisible.value = false;
    draftStep.value = 1;
    toolEditorMode.value = "create";
    editingToolId.value = "";
  }

  function goToDraftStep(step: number) {
    const nextStep = Math.min(Math.max(step, 1), toolEditorSteps.value.length);
    if (nextStep > draftStep.value && !isDraftStepComplete(draftStep.value)) {
      draftError.value = tt("tools.completeRequiredStep");
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
      return tt("tools.contractUndefined");
    }
    return tt("tools.contractSummary", {
      nodes: countSchemaNodes(nodes),
      depth: maxSchemaDepth(nodes),
    });
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
    if (tab === "Body" || tab === "Response") return tt("tools.hintNestedContract", { tab });
    if (tab === "Errors") return tt("tools.hintErrorMapping");
    return tt("tools.hintFlatContract");
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
    const toolName = draftTool.value.name.trim() || tt("tools.unnamedTool");
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
      return tt("tools.saveExists");
    }
    if (responseError.includes("service connection not found")) {
      return tt("tools.saveConnectionMissing");
    }
    if (responseError.includes("workspace not found")) {
      return tt("tools.saveWorkspaceMissing");
    }
    return responseError || tt("tools.saveFailedRetry");
  }

  function toolActionErrorMessage(error: unknown, fallback: string) {
    const responseError = (error as { response?: { data?: { error?: string } } }).response?.data?.error || "";
    if (responseError.includes("tool must pass test before publish")) {
      return tt("tools.publishNeedTestFeedback");
    }
    return responseError || fallback;
  }

  async function publishTool(tool: Tool) {
    const current = latestTool(tool);
    if (!canPublishTool(current)) {
      setActionFeedback(tt("tools.publishNeedTestFeedback"), "error");
      return;
    }
    try {
      const published = await toolsStore.publishTool(current.id);
      setActionFeedback(
        current.status === "Disabled"
          ? tt("tools.republishedFeedback", { name: published.name })
          : tt("tools.publishedFeedback", { name: published.name }),
      );
    } catch (error) {
      setActionFeedback(toolActionErrorMessage(error, tt("tools.publishFailed")), "error");
    }
  }

  async function enableTool(tool: Tool) {
    const current = latestTool(tool);
    try {
      const enabled = await toolsStore.updateTool(current.id, { ...current, status: "Review" });
      setActionFeedback(
        hasPassingTest(enabled)
          ? tt("tools.enabledKeepTest", { name: enabled.name })
          : tt("tools.enabledToReview", { name: enabled.name }),
      );
    } catch (error) {
      setActionFeedback(toolActionErrorMessage(error, tt("tools.enableFailed")), "error");
    }
  }

  async function disableTool(tool: Tool) {
    const current = latestTool(tool);
    try {
      const disabled = await toolsStore.updateTool(current.id, { ...current, status: "Disabled" });
      setActionFeedback(tt("tools.disabledFeedback", { name: disabled.name }));
    } catch (error) {
      setActionFeedback(toolActionErrorMessage(error, tt("tools.disableFailed")), "error");
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
      draftError.value = tt("tools.selectConnectionFirst");
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
      setActionFeedback(
        wasEdit
          ? tt("tools.updatedFeedback", { name: saved.name })
          : tt("tools.savedAsDraftFeedback", { name: saved.name }),
      );
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
      draftError.value = tt("tools.completeRequiredStep");
      return;
    }
    draftError.value = "";
    goToDraftStep(draftStep.value + 1);
  }

  async function publishDraftTool() {
    await persistDraftTool(false);
    const tool = toolsStore.tools.find((item) => item.id === editingToolId.value);
    if (!tool) {
      draftError.value = tt("tools.saveDraftBeforePublish");
      return;
    }
    if (draftChecklistHasBlockingErrors.value) {
      draftError.value = tt("tools.publishBlocked");
      return;
    }
    if (draftChecklistHasWarnings.value && !window.confirm(tt("tools.publishWarningsConfirm"))) {
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
    toolImpactSummary,
    toolMaintainerLabel,
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
    riskConfirmationEyebrow,
    riskConfirmationDescription,
    riskImpactItems,
    riskConfirmationToneClass,
    riskConfirmationTargetName,
    riskConfirmationTargetMeta,
    selectedTools,
    batchTesting,
    batchDeleting,
    batchForcePublishing,
    forcePublishReason,
    forcePublishReasonValid,
    canForcePublishTools,
    batchTestDialogVisible,
    batchTestModalRef,
    batchPassthroughToken,
    batchPassthroughExpiresAt,
    batchTestProgress,
    batchTestNeedsPassthrough,
    batchTestPassthroughConnectionIds,
    openBatchDeleteConfirmation,
    openBatchForcePublishConfirmation,
    batchTestSelectedTools,
    closeBatchTestDialog,
    confirmBatchTestSelectedTools,
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
