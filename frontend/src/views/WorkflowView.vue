<script setup lang="ts">
import { isAxiosError } from "axios";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import WorkflowEdgeInspector from "../components/workflow/WorkflowEdgeInspector.vue";
import WorkflowExecutionTracePanel from "../components/workflow/WorkflowExecutionTracePanel.vue";
import WorkflowGraphCanvas from "../components/workflow/WorkflowGraphCanvas.vue";
import WorkflowInspector from "../components/workflow/WorkflowInspector.vue";
import WorkflowIssuesPanel from "../components/workflow/WorkflowIssuesPanel.vue";
import WorkflowNodePalette from "../components/workflow/WorkflowNodePalette.vue";
import WorkflowReadinessPanel from "../components/workflow/WorkflowReadinessPanel.vue";
import WorkflowRevisionDiff from "../components/workflow/WorkflowRevisionDiff.vue";
import WorkflowRevisionPanel from "../components/workflow/WorkflowRevisionPanel.vue";
import WorkflowTrialRunDialog from "../components/workflow/WorkflowTrialRunDialog.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { createWorkflowHistoryState, cloneWorkflowGraph, pushWorkflowHistoryState, sameWorkflowGraph, type WorkflowHistoryState } from "../components/workflow/workflow-history";
import { autoLayoutWorkflowGraph, layoutWorkflowGraphIfNeeded } from "../components/workflow/workflow-layout";
import { toAPIError } from "../services/api";
import { useAuthStore } from "../stores/auth";
import { useIntegrationStore } from "../stores/integration";
import { useWorkflowStore, WorkflowDraftConflictError } from "../stores/workflow";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Execution,
  Tool,
  Workflow,
  WorkflowCompilation,
  WorkflowCompilationIssue,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
  WorkflowGraphNode,
  WorkflowGraphNodeType,
  WorkflowReadiness,
  WorkflowReadinessBlocker,
  WorkflowListQuery,
  WorkflowSummary,
  WorkflowStatus,
} from "../types/domain";
import {
  createDefaultWorkflowGraphDraft,
  defaultPortsForNodeType,
  listWorkflowVariableReferences,
  listWorkflowVariableReferencesForNode,
  normalizeWorkflowGraphDraft,
} from "../utils/workflow-graph";

interface WorkflowMetadataDraft {
  id: string;
  name: string;
  slug: string;
  workspaceId: string;
  description: string;
  status: WorkflowStatus;
}

interface WorkflowTrialRunInputField {
  key: string;
  type: string;
  required: boolean;
  description: string;
}

interface WorkflowContextMenuState {
  targetType: "node" | "edge";
  targetId: string;
  position: { x: number; y: number };
}

type EditorDraftLoadStatus = "loaded" | "failed" | "stale";
type EditorActionKind = "save" | "validate" | "trial-run" | "publish";
type EditorDraftLoadState = "idle" | "loading" | "loaded" | "failed";
type GraphMutationOptions = {
  recordHistory?: boolean;
};
type WorkflowEditorReadinessStep = {
  key: string;
  label: string;
  icon: string;
  state: "ready" | "current" | "pending";
};

const workflowStore = useWorkflowStore();
const integration = useIntegrationStore();
const workspaces = useWorkspaceStore();
const auth = useAuthStore();
const router = useRouter();
const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));
let editorDraftLoadRequestId = 0;
let editorDraftTargetWorkflowId = "";
const workflowToolCatalog = ref<Tool[]>([]);
const workflowToolCatalogWorkspaceId = ref("");
const workflowToolCatalogError = ref("");

const workflowQuery = ref("");
const workflowDetailVisible = ref(false);
const workflowEditorVisible = ref(false);
const workflowMetadataVisible = ref(false);
const workflowMetadataMode = ref<"create" | "edit">("create");
const workflowDraft = ref<WorkflowMetadataDraft>(newWorkflowDraft());
const workflowMetadataTouched = ref(false);
const workflowActionNote = ref("");
const pendingEditorAction = ref<EditorActionKind>();
const pendingWorkflowValidationId = ref("");
const pendingTrialRun = ref(false);
const pendingProductionRun = ref(false);
const pendingRevisionActionId = ref("");
const pendingRevisionCompare = ref(false);
const pendingWorkflowDisable = ref(false);
const selectedNodeId = ref("");
const selectedEdgeId = ref("");
const contextMenu = ref<WorkflowContextMenuState>();
const editorGraph = ref<WorkflowGraphDraft>(createDefaultWorkflowGraphDraft());
const editorHistory = ref<WorkflowHistoryState>(createWorkflowHistoryState());
const editorDraftLoadState = ref<EditorDraftLoadState>("idle");
const editorDraftLoadError = ref("");
const trialRunVisible = ref(false);
const trialRunTargetWorkflowId = ref("");
const trialRunTargetWorkflowName = ref("");
const activeTraceExecutionId = ref("");
const workflowDetailModalRef = ref<HTMLElement>();
const workflowMetadataModalRef = ref<HTMLElement>();
const workflowEditorShellRef = ref<HTMLElement>();
const workflowFocusRestoreTarget = ref<HTMLElement>();

const workflowStatusOptions = [
  { label: "草稿", value: "Draft" },
  { label: "待审核", value: "Review" },
  { label: "已发布", value: "Published" },
  { label: "已停用", value: "Disabled" },
];
const workflowEditorHelpText = "保存画布会更新节点和连线；检查问题会重新编译；模拟运行只做测试；发布上线后 Agent 才能调用。";

const workspaceOptions = computed(() =>
  (workspaces.items || []).map((workspace) => ({
    label: `${workspace.name} (${workspace.displayName})`,
    value: workspace.id,
  })),
);
const workflowNameError = computed(() => (workflowDraft.value.name.trim() ? "" : "Workflow 名称必填"));
const workflowWorkspaceError = computed(() => (workflowDraft.value.workspaceId.trim() ? "" : "业务空间必填"));
const canSaveWorkflowMetadata = computed(() => !workflowNameError.value && !workflowWorkspaceError.value);
const statusLabels: Record<WorkflowStatus, string> = {
  Draft: "草稿",
  Review: "待审核",
  Published: "已发布",
  Disabled: "已停用",
};

const executionStatusLabels: Record<string, string> = {
  Running: "运行中",
  Approval: "待确认",
  Success: "成功",
  Failed: "失败",
};

const workflowColumns = computed<ManagementListColumn<WorkflowSummary>[]>(() => [
  { key: "workflow", label: "业务流程名", width: 300, sortable: true, sortKey: "name", getValue: (workflow) => `${workflow.name} ${workflow.description}` },
  { key: "workspace", label: "业务空间", width: 170, hidable: true, sortable: true, sortKey: "workspace", getValue: workflowWorkspaceLabel },
  { key: "nodes", label: "当前节点数", width: 160, align: "right", hidable: true, sortable: true, sortKey: "nodeCount", getValue: (workflow) => `${workflowNodeCount(workflow)} / ${workflowEdgeCount(workflow)}` },
  { key: "successRate", label: "运行成功率", width: 160, align: "right", hidable: true, getValue: workflowSuccessRateLabel },
  { key: "status", label: "当前状态", width: 140, hidable: true, sortable: true, sortKey: "status", getValue: workflowTableStatusLabel },
  { key: "updatedAt", label: "最近修改", width: 130, hidable: true, sortable: true, sortKey: "updatedAt", getValue: formatWorkflowUpdatedAt },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
]);
const workflowSummaryItems = computed<ManagementSummaryItem[]>(() => {
  const workflows = workflowStore.workflows;
  return [
    { label: "流程总数", value: workflows.length, icon: "fa-solid fa-diagram-project" },
    { label: "已发布", value: workflows.filter((workflow) => workflow.status === "Published").length, icon: "fa-solid fa-circle-check" },
    { label: "草稿", value: workflows.filter((workflow) => workflow.status === "Draft").length, icon: "fa-solid fa-pen", tone: "warning" },
    { label: "待审核", value: workflows.filter((workflow) => workflow.status === "Review").length, icon: "fa-solid fa-list-check", tone: "info" },
  ];
});

const selectedWorkflow = computed(() => workflowStore.selectedWorkflow);
const selectedWorkflowDetail = computed(() => workflowStore.selectedWorkflowDetail);
const selectedWorkflowReadiness = computed(() => selectedWorkflowDetail.value?.readiness || selectedWorkflow.value?.readiness);
const selectedWorkflowRevisions = computed(() => (selectedWorkflow.value ? workflowStore.revisionsByWorkflowId[selectedWorkflow.value.id] || [] : []));
const selectedWorkflowRevisionDiff = computed(() => (selectedWorkflow.value ? workflowStore.revisionDiffByWorkflowId[selectedWorkflow.value.id] : undefined));
const selectedWorkflowDraft = computed(() =>
  workflowStore.activeDraft?.workflowId === selectedWorkflow.value?.id ? workflowStore.activeDraft : undefined,
);
const workflowEditorBusy = computed(() => Boolean(pendingEditorAction.value));
const selectedWorkflowCanPublish = computed(() => Boolean(selectedWorkflowReadiness.value?.canPublish));
const workflowEditorReadinessSteps = computed(() => buildWorkflowEditorReadinessSteps(selectedWorkflowReadiness.value));
const workflowEditorPublishTitle = computed(() => {
  if (selectedWorkflowCanPublish.value) {
    return "发布上线";
  }
  return workflowPublishBlockedTitle(selectedWorkflowReadiness.value?.stage);
});
const selectedGraphNode = computed(() => editorGraph.value.nodes.find((node) => node.id === selectedNodeId.value));
const selectedGraphEdge = computed(() => editorGraph.value.edges.find((edge) => edge.id === selectedEdgeId.value));
const canUndoEditorChange = computed(() => editorHistory.value.past.length > 0);
const canRedoEditorChange = computed(() => editorHistory.value.future.length > 0);
const editorDirtyState = computed(() => (isEditorDraftDirty() ? "dirty" : "saved"));
const editorVariableRefs = computed(() => listWorkflowVariableReferences(editorGraph.value).map((item) => `{{${item.key}}}`));
const selectedNodeVariableRefs = computed(() => {
  if (!selectedNodeId.value) {
    return editorVariableRefs.value;
  }
  return listWorkflowVariableReferencesForNode(editorGraph.value, selectedNodeId.value).map((item) => `{{${item.key}}}`);
});
const availableToolOptions = computed(() => {
  const workspaceId = selectedWorkflow.value?.workspaceId || selectedWorkflowDetail.value?.workspaceId || "";
  return workflowToolCatalog.value
    .filter((tool) => (!workspaceId || tool.workspaceId === workspaceId) && tool.status === "Published")
    .map((tool) => ({
      label: `${tool.name} · ${tool.id}`,
      value: tool.id,
    }));
});
const activeCompilationIssues = computed<WorkflowCompilationIssue[]>(() =>
  workflowStore.activeDraft?.workflowId === selectedWorkflow.value?.id ? workflowStore.activeCompilation?.issues || [] : [],
);
const trialRunInputSchema = computed<WorkflowTrialRunInputField[]>(() => {
  if (workflowStore.activeDraft?.workflowId === trialRunTargetWorkflowId.value) {
    return extractTrialRunInputSchema(workflowStore.activeDraft.graph);
  }
  return [];
});
const lastSuccessfulTrialInput = computed(() =>
  trialRunTargetWorkflowId.value ? workflowStore.lastSuccessfulTrialInputByWorkflowId[trialRunTargetWorkflowId.value] : undefined,
);
const activeTraceExecution = computed(() => (activeTraceExecutionId.value ? workflowStore.executionById[activeTraceExecutionId.value] : undefined));

const selectedWorkflowExecutions = computed(() => {
  const workflowId = selectedWorkflow.value?.id;
  if (!workflowId) return [];
  return workflowStore.executions.filter((execution) => execution.workflowId === workflowId).slice(0, 3);
});
const workflowEditorFeedbackMessage = computed(() => {
  if (editorDraftLoadState.value === "loading") return "正在加载最新草稿和编译结果，请稍候…";
  if (editorDraftLoadState.value === "failed") return workflowActionNote.value.trim() || "草稿加载失败，请稍后重试。";
  if (pendingEditorAction.value === "save") return "正在保存当前画布，请稍候…";
  if (pendingEditorAction.value === "validate") return "正在检查节点配置和连线问题，请稍候…";
  if (pendingEditorAction.value === "trial-run") return "正在准备模拟运行，请稍候…";
  if (pendingEditorAction.value === "publish") return "正在发布当前草稿，请稍候…";
  if (workflowActionNote.value.trim()) return workflowActionNote.value;
  return "保存画布会更新节点和连线；检查问题会重新编译；模拟运行只做测试；发布上线后 Agent 才能调用。";
});
const workflowLifecycleSteps = computed(() => buildWorkflowLifecycleSteps(selectedWorkflowReadiness.value?.stage, selectedWorkflowExecutions.value.length));
const workflowLifecycleHint = computed(() => {
  const workflow = selectedWorkflow.value;
  if (!workflow) {
    return "先选择一个流程，查看当前闭环所处阶段和下一步动作。";
  }
  return workflowNextAction(workflow);
});
const selectedWorkflowRevisionEmptyText = computed(() =>
  selectedWorkflow.value ? "还没有发布版本。完成试运行并发布后，版本会显示在这里。" : "请选择一个流程后查看发布版本。",
);
const selectedWorkflowDiffEmptyText = computed(() => {
  if (!selectedWorkflow.value) {
    return "请选择一个流程后查看版本差异。";
  }
  return selectedWorkflowRevisions.value.length > 1 ? "点击历史版本的“对比”按钮，查看节点、配置和 branch label 的变化。" : "至少需要两个 revision 才能比较差异。";
});
const selectedWorkflowExecutionEmptyText = computed(() =>
  selectedWorkflow.value ? "还没有执行记录。可以先试运行一次，确认流程是否符合预期。" : "请选择一个流程后查看最近执行。",
);

const selectedWorkflowSteps = computed(() => {
  if (selectedWorkflowDraft.value) {
    return selectedWorkflowDraft.value.graph.nodes.map((node, index) => ({
      id: node.id,
      order: index + 1,
      title: node.label,
      type: nodeTypeLabel(node.type),
      detail: `${node.id} · ${node.type}`,
    }));
  }

  const workflow = selectedWorkflowDetail.value;
  if (!workflow) return [];
  return [];
});

onMounted(async () => {
  window.addEventListener("keydown", handleEditorKeydown);
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (!hasWorkspaceContext.value) return;
    await loadWorkflowPageAssets();
    const generatedWorkflowId = new URLSearchParams(window.location.search).get("edit") || "";
    const generatedWorkflow = workflowStore.workflows.find((workflow) => workflow.id === generatedWorkflowId);
    if (generatedWorkflow) {
      await openWorkflowEditor(generatedWorkflow);
    }
  } catch {
    // The shared Workspace state provides recovery actions when bootstrap fails.
  }
});

async function loadWorkflowPageAssets() {
  await workflowStore.loadWorkflowAssets();
}

onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleEditorKeydown);
});

watch(
  () => [workflowEditorVisible.value, selectedGraphNode.value?.type, selectedWorkflow.value?.workspaceId] as const,
  async ([visible, nodeType, workspaceId]) => {
    if (!visible || nodeType !== "Tool" || !workspaceId || workflowToolCatalogWorkspaceId.value === workspaceId) {
      return;
    }

    try {
      workflowToolCatalogError.value = "";
      workflowToolCatalog.value = await integration.loadTools({ commit: false });
      workflowToolCatalogWorkspaceId.value = workspaceId;
    } catch (error) {
      workflowToolCatalog.value = [];
      workflowToolCatalogError.value = actionErrorMessage(error, "工具目录加载失败，请稍后重试。");
    }
  },
);

watch(workflowDetailVisible, (visible) => {
  if (!visible) {
    invalidatePendingEditorLoad();
  }
});

function newWorkflowDraft(): WorkflowMetadataDraft {
  const defaultWorkspaceId = workspaces.activeWorkspaceId || workspaces.items?.[0]?.id || "default";
  return {
    id: "",
    name: "",
    slug: "",
    workspaceId: defaultWorkspaceId,
    description: "",
    status: "Draft",
  };
}

function cloneGraph(graph: WorkflowGraphDraft) {
  return cloneWorkflowGraph(normalizeWorkflowGraphDraft(graph));
}

function sameGraph(left: WorkflowGraphDraft, right: WorkflowGraphDraft) {
  return sameWorkflowGraph(left, right);
}

function isEditorDraftDirty() {
  const draft = workflowStore.activeDraft;
  if (!draft || draft.workflowId !== selectedWorkflow.value?.id) {
    return true;
  }
  return !sameGraph(editorGraph.value, draft.graph);
}

function resetEditorHistory() {
  editorHistory.value = createWorkflowHistoryState();
}

function replaceEditorGraph(nextGraph: WorkflowGraphDraft, options: GraphMutationOptions = {}) {
  if (sameGraph(editorGraph.value, nextGraph)) {
    return;
  }

  if (options.recordHistory !== false) {
    editorHistory.value = pushWorkflowHistoryState(editorHistory.value, editorGraph.value);
  }

  editorGraph.value = cloneGraph(nextGraph);
  syncSelection();
}

function updateEditorGraph(
  updater: (current: WorkflowGraphDraft) => WorkflowGraphDraft,
  options: GraphMutationOptions = {},
) {
  replaceEditorGraph(updater(editorGraph.value), options);
}

async function ensureWorkspacesLoaded() {
  if (workspaces.items?.length) return;
  try {
    await workspaces.load();
  } catch {
    // AppShell usually preloads workspaces; keep the editor usable even if the refresh fails.
  }
}

async function loadEditorDraft(workflowId: string): Promise<EditorDraftLoadStatus> {
  const requestId = ++editorDraftLoadRequestId;
  editorDraftLoadState.value = "loading";
  editorDraftLoadError.value = "";

  try {
    const { draft, latestCompilation } = await workflowStore.loadWorkflowDraft(workflowId);
    // requestToken + workflowId fence: stale completions must not commit UI.
    if (requestId !== editorDraftLoadRequestId || editorDraftTargetWorkflowId !== workflowId) {
      return "stale" as const;
    }

    workflowStore.activeDraft = draft;
    workflowStore.activeCompilation = latestCompilation;
    // Unstack overlapping/zero-position graphs when opening the editor (HITL chains etc.).
    // Graph must come from the server Draft only — never synthesize a default empty graph here.
    editorGraph.value = layoutWorkflowGraphIfNeeded(cloneGraph(draft.graph));
    resetEditorHistory();
    syncSelection();
    editorDraftLoadState.value = "loaded";
    editorDraftLoadError.value = "";
    return "loaded" as const;
  } catch (error) {
    if (requestId !== editorDraftLoadRequestId || editorDraftTargetWorkflowId !== workflowId) {
      return "stale" as const;
    }

    // Failure retains prior legitimate state / detail context. Do not write a default empty graph
    // and do not clear selected workflow (ZKL-56 UX-01).
    editorDraftLoadState.value = "failed";
    const apiError = toAPIError(error);
    const requestIdHint = apiError.requestId ? `（请求 ${apiError.requestId}）` : "";
    const detail = apiError.message?.trim() && apiError.message !== "草稿加载失败" ? `：${apiError.message}` : "";
    editorDraftLoadError.value = `草稿加载失败${detail}，请重试。${requestIdHint}`;
    return "failed" as const;
  }
}

function invalidatePendingEditorLoad() {
  editorDraftLoadRequestId += 1;
  editorDraftTargetWorkflowId = "";
  editorDraftLoadState.value = "idle";
}

function closeWorkflowEditor(options: { restoreFocus?: boolean } = {}) {
  closeTrialRunDialog({ restoreFocus: false });
  invalidatePendingEditorLoad();
  closeContextMenu();
  pendingEditorAction.value = undefined;
  resetEditorHistory();
  workflowEditorVisible.value = false;
  if (options.restoreFocus !== false) {
    restoreWorkflowFocus();
  }
}

function captureWorkflowFocus() {
  if (document.activeElement instanceof HTMLElement) {
    workflowFocusRestoreTarget.value = document.activeElement;
  }
}

function restoreWorkflowFocus() {
  const target = workflowFocusRestoreTarget.value;
  workflowFocusRestoreTarget.value = undefined;
  if (!target?.isConnected) return;
  void nextTick(() => target.focus());
}

function focusFirstModalControl(root?: HTMLElement) {
  if (!root) return;
  const focusTarget = root.querySelector<HTMLElement>(
    'button:not(:disabled), [href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
  );
  (focusTarget || root).focus();
}

function trapModalFocus(event: KeyboardEvent, root?: HTMLElement) {
  if (!root || event.key !== "Tab") return;
  const focusable = Array.from(
    root.querySelectorAll<HTMLElement>(
      'button:not(:disabled), [href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null || element === document.activeElement);
  if (!focusable.length) {
    event.preventDefault();
    root.focus();
    return;
  }
  const first = focusable[0]!;
  const last = focusable[focusable.length - 1]!;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function workflowWorkspaceLabel(workflow: WorkflowSummary) {
  if (workflow.workspaceName?.trim()) {
    return workflow.workspaceName;
  }

  const workspace = (workspaces.items || []).find((item) => item.id === workflow.workspaceId);
  if (workspace?.displayName?.trim()) {
    return workspace.displayName;
  }
  if (workspace?.name?.trim()) {
    return workspace.name;
  }
  return workflow.workspaceId;
}

function workflowNodeCount(workflow: WorkflowSummary) {
  if (selectedWorkflowDraft.value?.workflowId === workflow.id) {
    return selectedWorkflowDraft.value.graph.nodes.length;
  }
  return workflow.nodeCount;
}

function workflowEdgeCount(workflow: WorkflowSummary) {
  if (selectedWorkflowDraft.value?.workflowId === workflow.id) {
    return selectedWorkflowDraft.value.graph.edges.length;
  }
  return workflow.edgeCount;
}

function statusClass(status: string) {
  return status.toLowerCase().replace(/\s+/g, "-");
}

function statusLabel(status: WorkflowStatus) {
  return statusLabels[status] || status;
}

function executionStatusLabel(status?: string) {
  if (!status) return "未试运行";
  return executionStatusLabels[status] || status;
}

function validationLabel(workflow: WorkflowSummary) {
  if (typeof workflow.lastValidationValid !== "boolean") return "未校验";
  return workflow.lastValidationValid ? "校验通过" : `${workflow.lastValidationIssueCount || 0} 个问题`;
}

function workflowLastExecution(workflow: WorkflowSummary): Execution | undefined {
  return workflowStore.executions.find((execution) => execution.workflowId === workflow.id);
}

function workflowLastResultLabel(workflow: WorkflowSummary) {
  return executionStatusLabel(workflow.lastTrialStatus || workflowLastExecution(workflow)?.status);
}

function workflowNextAction(workflow: WorkflowSummary) {
  const blocker = workflow.readiness?.blockers?.find((candidate) => candidate.action.trim());
  return localizedWorkflowReadinessAction(workflow.readiness?.stage, blocker);
}

function workflowReadinessFallbackAction(stage?: string) {
  switch (stage) {
    case "DraftMissing":
      return "先创建或保存流程草稿，再继续校验。";
    case "CompileRequired":
      return "保存或检查当前草稿，生成最新编译结果。";
    case "CompileFailed":
      return "先修复编译问题，再继续试运行或发布。";
    case "TrialRequired":
      return "运行当前已编译草稿的试运行。";
    case "PublishReady":
      return "当前草稿已通过试运行，可以发布给 Agent 调用。";
    case "Published":
      return "已发布版本可供 Agent 调用。";
    case "Disabled":
      return "启用流程后才能继续校验、试运行或发布。";
    default:
      return "等待后端返回就绪状态。";
  }
}

function localizedWorkflowReadinessAction(stage?: string, blocker?: WorkflowReadinessBlocker) {
  if (!blocker) {
    return workflowReadinessFallbackAction(stage);
  }

  switch (blocker.code) {
    case "draft_missing":
      return "先创建或保存流程草稿，再继续校验。";
    case "compile_required":
      return "保存或检查当前草稿，生成最新编译结果。";
    case "compile_failed":
      return "先修复编译问题，再继续试运行或发布。";
    case "trial_required":
      return "运行当前已编译草稿的试运行。";
    case "workflow_disabled":
      return "启用流程后才能继续校验、试运行或发布。";
    default:
      return blocker.action.trim() || workflowReadinessFallbackAction(stage);
  }
}

function workflowPublishBlockedTitle(stage?: string) {
  switch (stage) {
    case "DraftMissing":
      return "需先保存流程草稿";
    case "CompileRequired":
      return "需先保存或检查最新草稿";
    case "CompileFailed":
      return "需先修复编译问题";
    case "TrialRequired":
      return "需先完成试运行";
    case "Disabled":
      return "需先启用流程";
    default:
      return "当前 readiness 尚未允许发布";
  }
}

/** P4.3: CTA when compile failed or trial not successful — seed SmartDag revise (D14). */
const canReviseFromFailure = computed(() => {
  const readiness = selectedWorkflowReadiness.value;
  if (!selectedWorkflow.value || !readiness) return false;
  if (readiness.stage === "CompileFailed") return true;
  if (readiness.stage === "TrialRequired" && readiness.trialCurrent && readiness.trialSuccessful === false) return true;
  if (activeCompilationIssues.value.some((issue) => issue.severity === "error")) return true;
  return false;
});

function reviseSourceForReadiness(stage?: string): "compile" | "trial" {
  if (stage === "TrialRequired" || stage === "PublishReady") return "trial";
  return "compile";
}

/**
 * Navigate to SmartDag generate session with seed workflow + failure feedback context.
 * Backend turn + feedback only bumps draftVersion (never auto-publish, D5).
 */
function reviseDraftFromFailure() {
  const workflow = selectedWorkflow.value;
  if (!workflow) return;
  const readiness = selectedWorkflowReadiness.value;
  const source = reviseSourceForReadiness(readiness?.stage);
  const compilation = compilationForWorkflow(workflow.id);
  const issues = (compilation?.issues || activeCompilationIssues.value || []).map((issue) => ({
    code: issue.code || "UNKNOWN",
    nodeId: issue.nodeId,
    message: issue.message,
    suggestedAction: "regenerate" as const,
  }));
  const uiAgent = workflowStore.activeDraft?.graph?.ui?.agentId;
  const agentId = typeof uiAgent === "string" ? uiAgent : "";
  void router.push({
    name: "smart-dag",
    query: {
      workspaceId: workflow.workspaceId,
      workflowId: workflow.id,
      reviseSource: source,
      ...(agentId ? { agentId } : {}),
      ...(compilation?.id ? { compilationId: compilation.id } : {}),
      feedbackSummary:
        issues[0]?.message ||
        (source === "compile" ? "编译失败，请按问题修订草稿" : "试运行失败，请按问题修订草稿"),
      // Encode issues compactly for SmartDag seed (structural CTA).
      feedbackIssues: JSON.stringify(issues.slice(0, 16)),
    },
  });
}

function workflowReadinessStepState(ready: boolean, current: boolean): WorkflowEditorReadinessStep["state"] {
  if (ready) return "ready";
  if (current) return "current";
  return "pending";
}

function buildWorkflowEditorReadinessSteps(readiness?: WorkflowReadiness): WorkflowEditorReadinessStep[] {
  const stage = readiness?.stage || "DraftMissing";
  const draftReady = Boolean(readiness?.hasDraft);
  const compileReady = Boolean(readiness?.compilationCurrent && readiness.compilationValid);
  const trialReady = Boolean(readiness?.trialCurrent && readiness.trialSuccessful);
  const publishReady = Boolean(readiness?.published || readiness?.canPublish);

  return [
    {
      key: "draft",
      label: "草稿",
      icon: draftReady ? "fa-solid fa-check" : "fa-solid fa-clock",
      state: workflowReadinessStepState(draftReady, stage === "DraftMissing"),
    },
    {
      key: "compile",
      label: "编译",
      icon: compileReady ? "fa-solid fa-check" : "fa-solid fa-clock",
      state: workflowReadinessStepState(compileReady, stage === "CompileRequired" || stage === "CompileFailed"),
    },
    {
      key: "trial",
      label: "模拟试运行",
      icon: trialReady ? "fa-solid fa-check" : "fa-solid fa-clock",
      state: workflowReadinessStepState(trialReady, stage === "TrialRequired"),
    },
    {
      key: "publish",
      label: "发布",
      icon: publishReady ? "fa-solid fa-check" : "fa-solid fa-clock",
      state: workflowReadinessStepState(publishReady, stage === "PublishReady" || stage === "Published"),
    },
  ];
}

function workflowTriggerText(workflow = selectedWorkflow.value) {
  if (!workflow) return "暂无触发条件";
  const execution = workflowLastExecution(workflow);
  if (execution?.trigger) return execution.trigger;
  return workflow.status === "Published" ? "由 Agent 或人工操作触发" : "发布后可配置触发条件";
}

function workflowExecutionCount(workflow: WorkflowSummary) {
  return workflowStore.executions.filter((execution) => execution.workflowId === workflow.id).length;
}

function workflowSuccessRateLabel(workflow: WorkflowSummary) {
  const executions = workflowStore.executions.filter((execution) => execution.workflowId === workflow.id);
  if (!executions.length) {
    return "100% (0 次)";
  }
  const successCount = executions.filter((execution) => execution.status === "Success").length;
  const rate = Math.round((successCount / executions.length) * 100);
  return `${rate}% (${executions.length} 次)`;
}

function workflowTableStatusLabel(workflow: WorkflowSummary) {
  if (workflow.status === "Published") return "已发布";
  if (workflow.status === "Disabled") return "已停用";
  return "开发中草稿";
}

function workflowTableStatusClass(workflow: WorkflowSummary) {
  if (workflow.status === "Published") return "published";
  if (workflow.status === "Disabled") return "disabled";
  return "draft";
}

function formatWorkflowUpdatedAt(workflow: WorkflowSummary) {
  if (!workflow.updatedAt) return "-";
  const timestamp = Date.parse(workflow.updatedAt);
  if (!Number.isFinite(timestamp)) return workflow.updatedAt;
  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
  if (elapsedMinutes < 1) return "刚刚";
  if (elapsedMinutes < 60) return `${elapsedMinutes} 分钟前`;
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) return `${elapsedHours} 小时前`;
  return `${Math.floor(elapsedHours / 24)} 天前`;
}

function loadWorkflowRegistry(overrides: WorkflowListQuery = {}) {
  return workflowStore.loadWorkflowPage({
    query: overrides.query ?? workflowQuery.value,
    status: overrides.status,
    page: overrides.page ?? workflowStore.pagination.page,
    pageSize: overrides.pageSize ?? workflowStore.pagination.pageSize,
    ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
  });
}

function setWorkflowSearch(value: string) {
  workflowQuery.value = value;
  void loadWorkflowRegistry({ query: value, page: 1 });
}

function changeWorkflowPage(pagination: { page: number; pageSize: number }) {
  void loadWorkflowRegistry(pagination);
}

function changeWorkflowSort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
  void loadWorkflowRegistry({
    page: 1,
    pageSize: workflowStore.pagination.pageSize,
    sortBy: sort.sortBy ?? "",
    sortOrder: sort.sortOrder,
  });
}

function clearWorkflowSearch() {
  workflowQuery.value = "";
  void loadWorkflowRegistry({ query: "", page: 1 });
}

function buildWorkflowLifecycleSteps(stage?: string, executionCount = 0) {
  const currentStage = stage || "DraftMissing";
  return [
    { label: "Draft", active: currentStage !== "DraftMissing" },
    { label: "Compile", active: !["DraftMissing", "CompileRequired"].includes(currentStage) },
    { label: "Trial Run", active: ["TrialRequired", "PublishReady", "Published", "Disabled"].includes(currentStage) },
    { label: "Publish", active: ["PublishReady", "Published", "Disabled"].includes(currentStage) },
    { label: "Execute", active: executionCount > 0 || ["Published", "Disabled"].includes(currentStage) },
    { label: "Audit", active: executionCount > 0 || ["Published", "Disabled"].includes(currentStage) },
  ];
}

function nodeTypeLabel(type: string) {
  const labels: Record<string, string> = {
    Start: "开始",
    End: "结束",
    Tool: "工具调用",
    HTTP: "HTTP 请求",
    SubWorkflow: "子流程",
    Transform: "参数整理",
    Approval: "人工确认",
    Condition: "条件判断",
    Parallel: "并行分支",
    ForEach: "循环",
  };
  return labels[type] || type;
}

function selectWorkflow(workflow: WorkflowSummary) {
  workflowStore.selectedWorkflowId = workflow.id;
}

async function openWorkflowDetail(workflow: WorkflowSummary) {
  captureWorkflowFocus();
  invalidatePendingEditorLoad();
  selectWorkflow(workflow);
  try {
    await Promise.all([
      workflowStore.loadWorkflow(workflow.id),
      workflowStore.loadWorkflowRevisions(workflow.id),
    ]);
  } catch {
    // Keep the detail modal usable from summary data if detail refresh fails.
  }
  workflowDetailVisible.value = true;
  void nextTick(() => focusFirstModalControl(workflowDetailModalRef.value));
}

function closeWorkflowDetail() {
  workflowDetailVisible.value = false;
  restoreWorkflowFocus();
}

function closeWorkflowMetadata() {
  workflowMetadataVisible.value = false;
  restoreWorkflowFocus();
}

async function openWorkflowEditor(workflow = selectedWorkflow.value) {
  if (!workflow) return;
  // Permission gate: no EDIT → no editor entry (backend still enforces 403).
  const userId = auth.user?.id || "";
  if (userId && !workspaces.can(workflow.workspaceId, userId, "EDIT")) {
    workflowActionNote.value = "当前角色无权编辑流程图。";
    return;
  }
  captureWorkflowFocus();
  selectWorkflow(workflow);
  // Keep detail visible until Draft+Readiness succeed (ZKL-56 atomic handoff).
  // Do not open editor or clear detail on partial failure.
  workflowToolCatalog.value = [];
  workflowToolCatalogWorkspaceId.value = "";
  workflowToolCatalogError.value = "";
  editorDraftTargetWorkflowId = workflow.id;
  const status = await loadEditorDraft(workflow.id);
  if (status !== "loaded") {
    if (status === "failed") {
      workflowActionNote.value =
        editorDraftLoadError.value || `${workflow.name} 草稿加载失败，请稍后重试。`;
      // Ensure detail stays open for recovery / retry.
      workflowDetailVisible.value = true;
    }
    // stale: another open superseded this request — leave UI alone.
    return;
  }
  workflowActionNote.value = "";
  // Success: mount editor first, then close detail on next DOM flush.
  workflowEditorVisible.value = true;
  await nextTick();
  workflowDetailVisible.value = false;
  void nextTick(() => workflowEditorShellRef.value?.focus());
}

function syncSelection() {
  if (editorGraph.value.nodes.some((node) => node.id === selectedNodeId.value)) {
    return;
  }
  if (editorGraph.value.edges.some((edge) => edge.id === selectedEdgeId.value)) {
    selectedNodeId.value = "";
    return;
  }
  selectedEdgeId.value = "";
  selectedNodeId.value = editorGraph.value.nodes[0]?.id || "";
}

function setSelectedNode(nodeId: string) {
  selectedNodeId.value = nodeId;
  selectedEdgeId.value = "";
  closeContextMenu();
}

function setSelectedEdge(edgeId: string) {
  selectedEdgeId.value = edgeId;
  selectedNodeId.value = "";
  closeContextMenu();
}

function focusIssue(nodeId?: string) {
  if (!nodeId) return;
  selectedNodeId.value = nodeId;
  selectedEdgeId.value = "";
  closeContextMenu();
}

function focusEdgeIssue(edgeId?: string) {
  if (!edgeId) return;
  if (!editorGraph.value.edges.some((candidate) => candidate.id === edgeId)) {
    return;
  }
  selectedEdgeId.value = edgeId;
  selectedNodeId.value = "";
  closeContextMenu();
}

function updateSelectedNodeLabel(label: string) {
  updateEditorGraph((current) => ({
    ...current,
    nodes: current.nodes.map((node) => (node.id === selectedNodeId.value ? { ...node, label } : node)),
  }));
}

function updateSelectedNodeData(payload: { key: string; value: unknown }) {
  if (!selectedNodeId.value) {
    return;
  }

  updateEditorGraph((current) => ({
    ...current,
    nodes: current.nodes.map((node) =>
      node.id === selectedNodeId.value
        ? {
            ...node,
            data:
              payload.key === "__merge" && payload.value && typeof payload.value === "object" && !Array.isArray(payload.value)
                ? { ...node.data, ...(payload.value as Record<string, unknown>) }
                : { ...node.data, [payload.key]: payload.value },
          }
        : node,
    ),
  }));
}

function updateSelectedEdgeData(payload: { key: string; value: unknown }) {
  if (!selectedEdgeId.value) {
    return;
  }

  updateEditorGraph((current) => ({
    ...current,
    edges: current.edges.map((edge) => {
      if (edge.id !== selectedEdgeId.value) {
        return edge;
      }
      const data = { ...edge.data };
      if (payload.key === "branch" && payload.value === "") {
        delete data.branch;
      } else {
        data[payload.key] = payload.value;
      }
      return { ...edge, data };
    }),
  }));
}

function updateNodePosition(payload: { nodeId: string; position: { x: number; y: number } }) {
  updateEditorGraph((current) => ({
    ...current,
    nodes: current.nodes.map((node) =>
      node.id === payload.nodeId ? { ...node, position: { x: Math.round(payload.position.x), y: Math.round(payload.position.y) } } : node,
    ),
  }));
}

function updateViewport(viewport: { x: number; y: number; zoom: number }) {
  updateEditorGraph((current) => ({
    ...current,
    viewport: {
      x: Math.round(viewport.x),
      y: Math.round(viewport.y),
      zoom: Number(viewport.zoom.toFixed(3)),
    },
  }));
}

function connectNodes(payload: { sourceNodeId: string; sourcePort: string; targetNodeId: string; targetPort: string }) {
  const edgeId = `edge-${payload.sourceNodeId}-${payload.targetNodeId}-${payload.sourcePort}-${payload.targetPort}`;
  const exists = editorGraph.value.edges.some((edge) => edge.id === edgeId);
  if (exists) return;

  updateEditorGraph((current) => ({
    ...current,
    edges: [
      ...current.edges,
      {
        id: edgeId,
        sourceNodeId: payload.sourceNodeId,
        sourcePort: payload.sourcePort,
        targetNodeId: payload.targetNodeId,
        targetPort: payload.targetPort,
        data: {},
        ui: {},
      },
    ],
  }));
}

function deleteSelectedNode() {
  const node = selectedGraphNode.value;
  if (!node) {
    return;
  }

  if (isProtectedTerminalNode(node)) {
    closeContextMenu();
    workflowActionNote.value = `${node.label} 是流程起止节点，不可删除。`;
    return;
  }

  const remainingNodes = editorGraph.value.nodes.filter((candidate) => candidate.id !== node.id);
  const removedEdges = editorGraph.value.edges.filter((edge) => edge.sourceNodeId === node.id || edge.targetNodeId === node.id);

  replaceEditorGraph({
    ...editorGraph.value,
    nodes: remainingNodes,
    edges: editorGraph.value.edges.filter((edge) => edge.sourceNodeId !== node.id && edge.targetNodeId !== node.id),
  });

  selectedEdgeId.value = "";
  selectedNodeId.value = remainingNodes[0]?.id || "";
  closeContextMenu();

  workflowActionNote.value =
    removedEdges.length > 0 ? `已删除节点 ${node.label}，并移除 ${removedEdges.length} 条关联连线。` : `已删除节点 ${node.label}。`;
}

function deleteSelectedEdge() {
  const edge = selectedGraphEdge.value;
  if (!edge) {
    return;
  }

  replaceEditorGraph({
    ...editorGraph.value,
    edges: editorGraph.value.edges.filter((candidate) => candidate.id !== edge.id),
  });

  selectedEdgeId.value = "";
  closeContextMenu();
  workflowActionNote.value = `已删除连线 ${edge.id}。`;
}

function closeContextMenu() {
  contextMenu.value = undefined;
}

function isProtectedTerminalNode(node?: WorkflowGraphDraft["nodes"][number]) {
  return node?.id === "start" || node?.id === "end";
}

function isContextTargetDeleteDisabled() {
  return contextMenu.value?.targetType === "node" && isProtectedTerminalNode(selectedGraphNode.value);
}

function normalizeContextMenuPosition(position: { x: number; y: number }) {
  if (typeof window === "undefined") {
    return position;
  }

  const menuWidth = 168;
  const menuHeight = 52;
  const padding = 12;

  return {
    x: Math.max(padding, Math.min(position.x, window.innerWidth - menuWidth - padding)),
    y: Math.max(padding, Math.min(position.y, window.innerHeight - menuHeight - padding)),
  };
}

function openNodeContextMenu(payload: { nodeId: string; position: { x: number; y: number } }) {
  selectedNodeId.value = payload.nodeId;
  selectedEdgeId.value = "";
  contextMenu.value = {
    targetType: "node",
    targetId: payload.nodeId,
    position: normalizeContextMenuPosition(payload.position),
  };
}

function openEdgeContextMenu(payload: { edgeId: string; position: { x: number; y: number } }) {
  selectedEdgeId.value = payload.edgeId;
  selectedNodeId.value = "";
  contextMenu.value = {
    targetType: "edge",
    targetId: payload.edgeId,
    position: normalizeContextMenuPosition(payload.position),
  };
}

function deleteContextTarget() {
  if (!contextMenu.value) {
    return;
  }

  if (contextMenu.value.targetType === "node") {
    deleteSelectedNode();
    return;
  }

  deleteSelectedEdge();
}

function portsForNodeType(type: WorkflowGraphNodeType) {
  return defaultPortsForNodeType(type);
}

function addNodeToDraft(nodeType: WorkflowGraphNodeType) {
  const sequence = editorGraph.value.nodes.filter((node) => node.type === nodeType).length + 1;
  const nodeId = `${nodeType.toLowerCase()}-${sequence}`;
  const node: WorkflowGraphNode = {
    id: nodeId,
    type: nodeType,
    label: nodeType === "End" ? "End" : `${nodeType} ${sequence}`,
    position: {
      x: 180 + editorGraph.value.nodes.length * 120,
      y: 140 + (editorGraph.value.nodes.length % 3) * 92,
    },
    ports: portsForNodeType(nodeType),
    data: {},
    ui: {},
  };
  replaceEditorGraph({ ...editorGraph.value, nodes: [...editorGraph.value.nodes, node] });
  selectedNodeId.value = node.id;
  selectedEdgeId.value = "";
  workflowActionNote.value = `已添加节点 ${node.label}，可在属性面板继续配置。`;
}

function duplicateSelectedNode() {
  const node = selectedGraphNode.value;
  if (!node) {
    return;
  }

  const copyIndex = editorGraph.value.nodes.filter((candidate) => candidate.type === node.type).length + 1;
  const duplicatedNode: WorkflowGraphNode = {
    ...cloneGraph({ ...editorGraph.value, nodes: [node], edges: [], viewport: editorGraph.value.viewport, ui: editorGraph.value.ui }).nodes[0],
    id: `${node.type.toLowerCase()}-${copyIndex}`,
    label: `${node.label} Copy`,
    position: {
      x: node.position.x + 160,
      y: node.position.y + 48,
    },
  };

  replaceEditorGraph({
    ...editorGraph.value,
    nodes: [...editorGraph.value.nodes, duplicatedNode],
  });
  selectedNodeId.value = duplicatedNode.id;
  selectedEdgeId.value = "";
  workflowActionNote.value = `已复制节点 ${node.label}。`;
}

function applyAutoLayout() {
  replaceEditorGraph(autoLayoutWorkflowGraph(editorGraph.value));
  workflowActionNote.value = "已格式化画布布局。";
}

function undoEditorChange() {
  const previous = editorHistory.value.past.at(-1);
  if (!previous) {
    return;
  }

  editorHistory.value = {
    past: editorHistory.value.past.slice(0, -1),
    future: [cloneGraph(editorGraph.value), ...editorHistory.value.future],
  };
  editorGraph.value = cloneGraph(previous);
  syncSelection();
}

function redoEditorChange() {
  const [next, ...remainingFuture] = editorHistory.value.future;
  if (!next) {
    return;
  }

  editorHistory.value = {
    past: [...editorHistory.value.past, cloneGraph(editorGraph.value)],
    future: remainingFuture,
  };
  editorGraph.value = cloneGraph(next);
  syncSelection();
}

async function openCreateWorkflow() {
  captureWorkflowFocus();
  closeTrialRunDialog({ restoreFocus: false });
  closeWorkflowEditor({ restoreFocus: false });
  workflowMetadataMode.value = "create";
  workflowStore.selectedWorkflowId = "";
  workflowStore.activeDraft = undefined;
  workflowStore.activeCompilation = undefined;
  await ensureWorkspacesLoaded();
  workflowDraft.value = newWorkflowDraft();
  workflowMetadataTouched.value = false;
  editorGraph.value = createDefaultWorkflowGraphDraft();
  resetEditorHistory();
  syncSelection();
  workflowActionNote.value = "";
  workflowMetadataVisible.value = true;
  void nextTick(() => focusFirstModalControl(workflowMetadataModalRef.value));
}

async function openEditWorkflow(workflow = selectedWorkflow.value) {
  captureWorkflowFocus();
  invalidatePendingEditorLoad();
  if (!workflow) {
    await openCreateWorkflow();
    return;
  }
  const detail = workflowStore.workflowDetails[workflow.id] ?? (await workflowStore.loadWorkflow(workflow.id));
  await ensureWorkspacesLoaded();
  workflowMetadataMode.value = "edit";
  workflowDraft.value = {
    id: detail.id,
    name: detail.name,
    slug: detail.slug,
    workspaceId: detail.workspaceId,
    description: detail.description,
    status: detail.status,
  };
  workflowMetadataTouched.value = false;
  workflowActionNote.value = "";
  workflowMetadataVisible.value = true;
  void nextTick(() => focusFirstModalControl(workflowMetadataModalRef.value));
}

function buildWorkflowFromDraft(): Workflow & { graph: WorkflowGraphDraft; schemaVersion: string } {
  const source = workflowMetadataMode.value === "edit" ? selectedWorkflowDetail.value : undefined;
  const workspaceId = workflowDraft.value.workspaceId.trim() || "default";
  const isCreateMode = workflowMetadataMode.value === "create";
  const now = new Date().toISOString();
  return {
    id: workflowMetadataMode.value === "edit" ? workflowDraft.value.id : "",
    workspaceId,
    currentDraftId: source?.currentDraftId || "",
    activeRevisionId: source?.activeRevisionId,
    latestCompilationId: source?.latestCompilationId,
    name: workflowDraft.value.name.trim(),
    slug: workflowDraft.value.slug.trim(),
    description: workflowDraft.value.description.trim(),
    status: workflowDraft.value.status,
    nodeCount: isCreateMode ? 0 : source?.nodeCount,
    edgeCount: isCreateMode ? 0 : source?.edgeCount,
    lastValidationValid: source?.lastValidationResult?.valid,
    lastValidationIssueCount: source?.lastValidationResult?.issues.length,
    lastTrialExecutionId: source?.lastTrialExecutionId,
    lastTrialStatus: source?.lastTrialStatus,
    readiness: source?.readiness,
    createdBy: source?.createdBy || "",
    updatedBy: source?.updatedBy || "",
    createdAt: source?.createdAt || now,
    updatedAt: source?.updatedAt || now,
    lockVersion: source?.lockVersion || 1,
    graph: cloneGraph(editorGraph.value),
    schemaVersion: editorGraph.value.schemaVersion,
  };
}

async function saveWorkflowMetadata() {
  workflowMetadataTouched.value = true;
  if (!canSaveWorkflowMetadata.value) {
    void nextTick(() => workflowMetadataModalRef.value?.querySelector<HTMLInputElement>("input[aria-invalid='true']")?.focus());
    return;
  }
  const payload = buildWorkflowFromDraft();
  const createdWorkflow = workflowMetadataMode.value === "create";
  if (createdWorkflow) {
    const created = await workflowStore.createWorkflow(payload);
    const { draft, latestCompilation } = await workflowStore.loadWorkflowDraft(created.id);
    workflowStore.activeDraft = draft;
    workflowStore.activeCompilation = latestCompilation;
    editorGraph.value = cloneGraph(draft.graph);
    resetEditorHistory();
    syncSelection();
    workflowStore.selectedWorkflowId = created.id;
    workflowActionNote.value = `${created.name} 已创建，并生成可编辑画布。`;
  } else {
    const updated = await workflowStore.updateWorkflow(workflowDraft.value.id, payload);
    workflowStore.selectedWorkflowId = updated.id;
    workflowActionNote.value = `${updated.name} 已保存为 ${updated.status}。`;
  }
  await loadWorkflowRegistry({ page: createdWorkflow ? 1 : workflowStore.pagination.page });
  closeWorkflowMetadata();
  workflowDetailVisible.value = true;
}

async function saveEditorDraft() {
  if (workflowEditorBusy.value) return;
  pendingEditorAction.value = "save";
  try {
    if (!selectedWorkflow.value) return;
    const saved = await persistEditorDraft();
    if (!saved) return;
    workflowActionNote.value = `${selectedWorkflow.value.name} 草稿已保存；请执行“检查问题”生成最新编译结果。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "保存草稿失败，请稍后重试。");
  } finally {
    pendingEditorAction.value = undefined;
  }
}

async function persistEditorDraft() {
  if (!selectedWorkflow.value || !workflowStore.activeDraft) return undefined;
  const current = workflowStore.activeDraft;
  const payload: WorkflowDraftRecord = {
    ...current,
    graph: cloneGraph(editorGraph.value),
  };
  try {
    const saved = await workflowStore.saveWorkflowDraft(selectedWorkflow.value.id, payload);
    editorGraph.value = cloneGraph(saved.graph);
    resetEditorHistory();
    return saved;
  } catch (error) {
    if (error instanceof WorkflowDraftConflictError) {
      editorGraph.value = cloneGraph(error.latest.graph);
      resetEditorHistory();
      syncSelection();
    }
    throw error;
  }
}

function workflowMenuActions(workflow: WorkflowSummary): ManagementRowAction[] {
  const validating = pendingWorkflowValidationId.value === workflow.id;
  return [
    { key: "detail", label: "查看详情", icon: "fa-solid fa-eye", tone: "primary" },
    { key: "edit", label: "编辑流程图", icon: "fa-solid fa-pen-ruler" },
    {
      key: "validate",
      label: "校验流程",
      icon: "fa-solid fa-list-check",
      loading: validating,
      disabled: validating,
      disabledReason: validating ? "校验中" : undefined,
    },
    {
      key: "trial-run",
      label: "模拟试运行",
      icon: "fa-solid fa-vial",
      loading: pendingTrialRun.value,
      disabled: pendingTrialRun.value,
      disabledReason: pendingTrialRun.value ? "模拟试运行提交中" : undefined,
    },
    ...(workflow.activeRevisionId
      ? [{
          key: "production-run",
          label: "生产运行",
          icon: "fa-solid fa-play",
          loading: pendingProductionRun.value,
          disabled: pendingProductionRun.value,
          disabledReason: pendingProductionRun.value ? "生产运行提交中" : undefined,
        } satisfies ManagementRowAction]
      : []),
    { key: "delete", label: "删除流程", icon: "fa-solid fa-trash", tone: "danger" },
  ];
}

function handleWorkflowRowAction(actionKey: string, workflow: WorkflowSummary) {
  if (actionKey === "edit") {
    void openWorkflowEditor(workflow);
    return;
  }
  if (actionKey === "detail") {
    void openWorkflowDetail(workflow);
    return;
  }
  if (actionKey === "validate") {
    void validateWorkflow(workflow);
    return;
  }
  if (actionKey === "trial-run") {
    void openTrialRunDialog(workflow);
    return;
  }
  if (actionKey === "production-run") {
    void runWorkflowProduction(workflow);
    return;
  }
  if (actionKey === "delete") void deleteWorkflow(workflow);
}

async function deleteWorkflow(workflow: WorkflowSummary) {
  const wasSelected = selectedWorkflow.value?.id === workflow.id;
  await workflowStore.deleteWorkflow(workflow.id);
  const page = workflowStore.pageItems.length === 0 && workflowStore.pagination.page > 1
    ? workflowStore.pagination.page - 1
    : workflowStore.pagination.page;
  await loadWorkflowRegistry({ page });
  workflowActionNote.value = `${workflow.name} 已删除。`;
  if (wasSelected) {
    workflowDetailVisible.value = false;
    closeWorkflowEditor({ restoreFocus: false });
    editorGraph.value = createDefaultWorkflowGraphDraft();
    resetEditorHistory();
    syncSelection();
  }
}

async function validateWorkflow(workflow: WorkflowSummary) {
  if (pendingWorkflowValidationId.value) return;
  pendingWorkflowValidationId.value = workflow.id;
  try {
    const validation = await workflowStore.validateWorkflow(workflow.id);
    const firstIssue = validation.issues[0];
    workflowActionNote.value = validation.valid
      ? `${workflow.name} 校验通过。`
      : `${workflow.name} 存在 ${validation.issues.length} 个问题：${firstIssue?.message || "请打开流程检查详情"}`;
  } finally {
    pendingWorkflowValidationId.value = "";
  }
}

async function validateEditorWorkflow() {
  if (workflowEditorBusy.value) return;
  pendingEditorAction.value = "validate";
  try {
    if (!selectedWorkflow.value) return;
    const saved = await persistEditorDraft();
    if (!saved) return;
    const validation = await workflowStore.validateWorkflow(selectedWorkflow.value.id);
    if (!validation.valid) {
      workflowActionNote.value = `${selectedWorkflow.value.name} 草稿已保存，但仍有 ${validation.issues.length} 个编译问题：${validation.issues[0]?.message || "请检查右侧问题面板"}`;
      focusFirstCompilationIssue();
      return;
    }
    workflowActionNote.value = `${selectedWorkflow.value.name} 校验通过。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "校验失败，请稍后重试。");
  } finally {
    pendingEditorAction.value = undefined;
  }
}

async function runWorkflowTrial(
  workflowId: string,
  input: Record<string, unknown>,
  outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope,
) {
  const execution = await workflowStore.trialRunWorkflow(workflowId, input, outboundCredentials);
  activeTraceExecutionId.value = execution.id;
  const workflow = workflowStore.workflows.find((item) => item.id === workflowId) || selectedWorkflow.value;
  workflowActionNote.value = `${workflow?.name || "当前流程"} 模拟试运行已生成 ${execution.id}，状态 ${execution.status}（非生产）。`;
  return execution;
}

async function runWorkflowProduction(workflow: WorkflowSummary) {
  if (pendingProductionRun.value) return;
  if (!workflow.activeRevisionId) {
    workflowActionNote.value = `${workflow.name} 尚无 active 发布版本，无法生产运行。请先完成模拟试运行并发布。`;
    return;
  }
  pendingProductionRun.value = true;
  captureWorkflowFocus();
  try {
    const execution = await workflowStore.executeProductionWorkflow(workflow.id, {});
    activeTraceExecutionId.value = execution.id;
    workflowActionNote.value = `${workflow.name} 生产运行已提交 ${execution.id}，状态 ${execution.status}，trace ${execution.traceId}。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "生产运行失败，请稍后重试。");
  } finally {
    pendingProductionRun.value = false;
  }
}

async function openTrialRunDialog(workflow = selectedWorkflow.value, options: { persistEditorDraft?: boolean } = {}) {
  if (!workflow) return;
  captureWorkflowFocus();
  selectWorkflow(workflow);

  if (options.persistEditorDraft) {
    const saved = await persistEditorDraft();
    if (!saved) return;
    const validation = await workflowStore.validateWorkflow(workflow.id);
    if (!validation.valid) {
      workflowActionNote.value = buildCompilationBlockedMessage(workflow, "试运行");
      focusFirstCompilationIssue();
      return;
    }
  } else if (workflowStore.activeDraft?.workflowId !== workflow.id) {
    try {
      const { draft, latestCompilation } = await workflowStore.loadWorkflowDraft(workflow.id);
      workflowStore.activeDraft = draft;
      workflowStore.activeCompilation = latestCompilation;
    } catch (error) {
      workflowActionNote.value = `${workflow.name} 草稿加载失败，请稍后重试。`;
      return;
    }
  }

  const readiness = workflowStore.readinessByWorkflowId[workflow.id] || await workflowStore.loadWorkflowReadiness(workflow.id);
  if (!readiness.canTrial) {
    workflowActionNote.value = workflowNextAction({ ...workflow, readiness });
    focusFirstCompilationIssue();
    return;
  }

  trialRunTargetWorkflowId.value = workflow.id;
  trialRunTargetWorkflowName.value = workflow.name;
  trialRunVisible.value = true;
}

async function trialRunEditorWorkflow() {
  if (workflowEditorBusy.value) return;
  pendingEditorAction.value = "trial-run";
  try {
    if (!selectedWorkflow.value) return;
    await openTrialRunDialog(selectedWorkflow.value, { persistEditorDraft: true });
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "试运行准备失败，请稍后重试。");
  } finally {
    pendingEditorAction.value = undefined;
  }
}

async function publishWorkflow(workflow = selectedWorkflow.value) {
  if (!workflow) return;
  if (!workflow.readiness?.canPublish) {
    workflowActionNote.value = workflowNextAction(workflow);
    return;
  }
  const published = await workflowStore.publishWorkflow(workflow.id);
  workflowActionNote.value = `${published.workflow.name} 已发布上线，可供 Agent 调用。`;
}

async function activateRevision(revisionId: string) {
  if (!selectedWorkflow.value) return;
  pendingRevisionActionId.value = revisionId;
  try {
    const result = await workflowStore.activateWorkflowRevision(selectedWorkflow.value.id, revisionId);
    workflowActionNote.value = `${result.workflow.name} 已切换到版本 ${revisionId}。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "激活版本失败，请稍后重试。");
  } finally {
    pendingRevisionActionId.value = "";
  }
}

async function rollbackRevision(revisionId: string) {
  if (!selectedWorkflow.value) return;
  pendingRevisionActionId.value = revisionId;
  try {
    const result = await workflowStore.rollbackWorkflowRevision(selectedWorkflow.value.id, revisionId);
    workflowActionNote.value = `${result.workflow.name} 已回滚到版本 ${revisionId}。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "回滚版本失败，请稍后重试。");
  } finally {
    pendingRevisionActionId.value = "";
  }
}

async function compareRevision(leftRevisionId: string, rightRevisionId: string) {
  if (!selectedWorkflow.value) return;
  pendingRevisionCompare.value = true;
  try {
    await workflowStore.loadWorkflowRevisionDiff(selectedWorkflow.value.id, leftRevisionId, rightRevisionId);
    workflowActionNote.value = `${selectedWorkflow.value.name} 已比较版本 ${leftRevisionId} 与 ${rightRevisionId}。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "版本差异加载失败，请稍后重试。");
  } finally {
    pendingRevisionCompare.value = false;
  }
}

async function disableWorkflowRuns() {
  if (!selectedWorkflow.value) return;
  pendingWorkflowDisable.value = true;
  try {
    const workflow = await workflowStore.disableWorkflow(selectedWorkflow.value.id);
    workflowActionNote.value = `${workflow.name} 已停用，新的 published execution 将被阻止。`;
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "停用流程失败，请稍后重试。");
  } finally {
    pendingWorkflowDisable.value = false;
  }
}

async function publishEditorWorkflow() {
  if (workflowEditorBusy.value) return;
  pendingEditorAction.value = "publish";
  try {
    if (!selectedWorkflow.value) return;
    if (isEditorDraftDirty()) {
      const saved = await persistEditorDraft();
      if (!saved) return;
    }

    if (!selectedWorkflowCanPublish.value) {
      workflowActionNote.value = workflowNextAction(selectedWorkflow.value);
      return;
    }

    await publishWorkflow(selectedWorkflow.value);
  } catch (error) {
    workflowActionNote.value = actionErrorMessage(error, "发布失败，请稍后重试。");
  } finally {
    pendingEditorAction.value = undefined;
  }
}

async function submitTrialRun(payload: {
  input: Record<string, unknown>;
  outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope;
}) {
  const workflowId = trialRunTargetWorkflowId.value;
  if (!workflowId || pendingTrialRun.value) return;
  pendingTrialRun.value = true;
  try {
    await runWorkflowTrial(workflowId, payload.input, payload.outboundCredentials);
    closeTrialRunDialog();
  } catch (error) {
    const workflow = workflowStore.workflows.find((item) => item.id === workflowId) || selectedWorkflow.value;
    const latestCompilation = extractCompilationFromError(error);
    if (workflow && latestCompilation && isStaleCompilationForWorkflow(workflowId, latestCompilation)) {
      closeTrialRunDialog();
      workflowActionNote.value = `${workflow.name} 当前编译结果已过期，请先重新保存草稿后再试运行。`;
      return;
    }
    if (workflow && (latestCompilation?.status === "Invalid" || latestCompilation?.status === "INVALID")) {
      closeTrialRunDialog();
      workflowActionNote.value = buildCompilationBlockedMessage(workflow, "试运行");
      focusCompilationIssue(latestCompilation.issues[0]);
      return;
    }
    workflowActionNote.value = `${workflow?.name || "当前流程"} 试运行失败，请稍后重试。`;
  } finally {
    pendingTrialRun.value = false;
  }
}

function closeTrialRunDialog(options: { restoreFocus?: boolean } = {}) {
  trialRunVisible.value = false;
  trialRunTargetWorkflowId.value = "";
  trialRunTargetWorkflowName.value = "";
  if (options.restoreFocus !== false) {
    restoreWorkflowFocus();
  }
}

function compilationForWorkflow(workflowId: string) {
  const draft = workflowStore.activeDraft;
  const compilation = workflowStore.activeCompilation;
  if (draft?.workflowId !== workflowId) return undefined;
  if (compilation?.workflowId !== workflowId) return undefined;
  if (!isCompilationCurrentForDraft(compilation, draft)) return undefined;
  return compilation;
}

function focusFirstCompilationIssue() {
  focusCompilationIssue(activeCompilationIssues.value[0]);
}

function buildCompilationBlockedMessage(workflow: WorkflowSummary, action: "试运行" | "发布") {
  if (hasStaleCompilation(workflow.id)) {
    return `${workflow.name} 当前编译结果已过期，请先重新保存草稿后再${action}。`;
  }
  const compilation = compilationForWorkflow(workflow.id);
  if (!compilation) {
    return `${workflow.name} 暂无可用编译结果，请先保存草稿后再${action}。`;
  }
  if (compilation.issues.length > 0) {
    return `${workflow.name} 仍有 ${compilation.issues.length} 个编译问题，请先修复后再${action}。`;
  }
  return `${workflow.name} 当前编译结果不可用于${action}。`;
}

function focusCompilationIssue(issue?: WorkflowCompilationIssue) {
  if (!issue) return;
  if (issue.edgeId) {
    focusEdgeIssue(issue.edgeId);
    return;
  }
  if (issue.nodeId) {
    focusIssue(issue.nodeId);
  }
}

function selectTraceNode(nodeId: string) {
  if (!nodeId) return;
  selectedNodeId.value = nodeId;
  selectedEdgeId.value = "";
  closeContextMenu();
}

function isEditingTextInput(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tagName = target.tagName;
  return target.isContentEditable || tagName === "INPUT" || tagName === "TEXTAREA" || tagName === "SELECT";
}

function shouldHandleEditorShortcut(event: KeyboardEvent) {
  if (!workflowEditorVisible.value || workflowMetadataVisible.value || trialRunVisible.value || workflowEditorBusy.value) {
    return false;
  }
  return !isEditingTextInput(event.target);
}

function handleEditorKeydown(event: KeyboardEvent) {
  if (!shouldHandleEditorShortcut(event)) {
    return;
  }

  const key = event.key.toLowerCase();
  const usesMeta = event.ctrlKey || event.metaKey;

  if (usesMeta && key === "z" && !event.shiftKey) {
    event.preventDefault();
    undoEditorChange();
    return;
  }

  if (usesMeta && ((key === "z" && event.shiftKey) || key === "y")) {
    event.preventDefault();
    redoEditorChange();
    return;
  }

  if (event.key === "Delete" || event.key === "Backspace") {
    if (selectedGraphEdge.value) {
      event.preventDefault();
      deleteSelectedEdge();
      return;
    }

    if (selectedGraphNode.value) {
      event.preventDefault();
      deleteSelectedNode();
    }
  }
}

function extractCompilationFromError(error: unknown): WorkflowCompilation | undefined {
  if (!isAxiosError(error)) {
    return undefined;
  }
  const payload = error.response?.data as { latestCompilation?: WorkflowCompilation } | undefined;
  return payload?.latestCompilation;
}

function hasStaleCompilation(workflowId: string) {
  const draft = workflowStore.activeDraft;
  const compilation = workflowStore.activeCompilation;
  if (draft?.workflowId !== workflowId) {
    return false;
  }
  if (compilation?.workflowId !== workflowId) {
    return false;
  }
  return !isCompilationCurrentForDraft(compilation, draft);
}

function isStaleCompilationForWorkflow(workflowId: string, compilation: WorkflowCompilation) {
  const draft = workflowStore.activeDraft;
  if (draft?.workflowId !== workflowId) {
    return false;
  }
  return !isCompilationCurrentForDraft(compilation, draft);
}

function isCompilationCurrentForDraft(compilation: WorkflowCompilation, draft: WorkflowDraftRecord) {
  return (
    compilation.workflowId === draft.workflowId &&
    compilation.draftVersion === draft.draftVersion &&
    Date.parse(compilation.compiledAt) >= Date.parse(draft.updatedAt)
  );
}

function extractTrialRunInputSchema(graph: WorkflowGraphDraft): WorkflowTrialRunInputField[] {
  const startNode = graph.nodes.find((node) => node.type === "Start");
  const inputSchema = startNode?.data.inputSchema;

  if (Array.isArray(inputSchema)) {
    return inputSchema
      .flatMap((field) => {
        if (!field || typeof field !== "object") return [];
        const schemaField = field as Record<string, unknown>;
        const key = typeof schemaField.key === "string" ? schemaField.key : "";
        if (!key) return [];
        return [
          {
            key,
            type: typeof schemaField.type === "string" ? schemaField.type : "string",
            required: Boolean(schemaField.required),
            description: typeof schemaField.description === "string" ? schemaField.description : key,
          },
        ];
      })
      .sort((left, right) => left.key.localeCompare(right.key));
  }

  if (!inputSchema || typeof inputSchema !== "object") {
    return [];
  }

  const record = inputSchema as { properties?: unknown; required?: unknown } & Record<string, unknown>;
  const properties =
    record.properties && typeof record.properties === "object"
      ? (record.properties as Record<string, unknown>)
      : Object.fromEntries(Object.entries(record).filter(([key]) => key !== "required"));

  const required = new Set(
    Array.isArray(record.required) ? record.required.filter((value): value is string => typeof value === "string") : [],
  );

  return Object.entries(properties)
    .flatMap(([key, definition]) => {
      if (typeof definition === "string") {
        return [{ key, type: definition, required: required.has(key), description: key }];
      }
      if (!definition || typeof definition !== "object") {
        return [];
      }
      const schemaField = definition as Record<string, unknown>;
      return [
        {
          key,
          type: typeof schemaField.type === "string" ? schemaField.type : "string",
          required: required.has(key) || Boolean(schemaField.required),
          description: typeof schemaField.description === "string" ? schemaField.description : key,
        },
      ];
    })
    .sort((left, right) => left.key.localeCompare(right.key));
}

function actionErrorMessage(error: unknown, fallback: string) {
  if (isAxiosError(error)) {
    const message = (error.response?.data as { error?: unknown } | undefined)?.error;
    if (typeof message === "string" && message.trim()) {
      return message;
    }
  }
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return fallback;
}
</script>

<template>
  <div class="workflow-center-page workflow-orchestration-page management-page-grid" v-loading="workflowStore.loading">
    <ManagementPageHeader
      class="workflow-orchestration-header"
      title="编排"
      description="设计、校验、试跑与发布业务流程。"
      icon="fa-solid fa-diagram-project"
    >
      <template #actions>
        <button
          class="primary-button workflow-create-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? '新建编排' : '请先创建或加入业务空间'"
          @click="openCreateWorkflow"
        >
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          <span>新建编排</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip :items="workflowSummaryItems" />

    <section class="workflow-center-panel workflow-orchestration-table-card management-list-card">
      <WorkspaceContextState
        v-if="!hasWorkspaceContext"
        feature="流程编排"
        icon="fa-solid fa-diagram-project"
        @retry="loadWorkflowPageAssets"
      />
      <ManagementList
        v-else
        class="workflow-management-list"
        :rows="workflowStore.pageItems"
        :columns="workflowColumns"
        row-key="id"
        :sticky-left-keys="['workflow']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:workflows:columns"
        :selected-row-key="selectedWorkflow?.id"
        selection-tone="neutral"
        :loading="workflowStore.pageLoading"
        :error="workflowStore.pageError"
        :has-loaded="workflowStore.pageHasLoaded"
        :search="workflowQuery"
        search-placeholder="搜索流程名称 / Slug / 状态..."
        search-aria-label="搜索流程名称、Slug 或状态"
        :reset-disabled="!workflowQuery"
        :pagination="workflowStore.pagination"
        :sort-by="workflowStore.listQuery?.sortBy"
        :sort-order="workflowStore.listQuery?.sortOrder"
        @select-row="openWorkflowDetail"
        @update:search="setWorkflowSearch"
        @reset="clearWorkflowSearch"
        @page-change="changeWorkflowPage"
        @sort-change="changeWorkflowSort"
      >
        <template #cell-workflow="{ row: workflow }">
          <div class="workflow-name-cell">
            <strong class="aw-table-title" :title="workflow.name">{{ workflow.name }}</strong>
            <small class="aw-table-subtitle" :title="workflow.description || '还没有填写用途说明'">{{ workflow.description || "还没有填写用途说明" }}</small>
          </div>
        </template>
        <template #cell-workspace="{ row: workflow }"><span class="workflow-workspace-cell aw-table-meta" :title="workflowWorkspaceLabel(workflow)">{{ workflowWorkspaceLabel(workflow) }}</span></template>
        <template #cell-nodes="{ row: workflow }"><span class="workflow-mono-cell aw-table-meta">{{ workflowNodeCount(workflow) }} 步 / {{ workflowEdgeCount(workflow) }} 连接</span></template>
        <template #cell-successRate="{ row: workflow }"><span class="workflow-success-cell aw-table-meta">{{ workflowSuccessRateLabel(workflow) }}</span></template>
        <template #cell-status="{ row: workflow }"><span class="workflow-status-badge aw-table-pill" :class="workflowTableStatusClass(workflow)">{{ workflowTableStatusLabel(workflow) }}</span></template>
        <template #cell-updatedAt="{ row: workflow }"><span class="workflow-updated-at aw-table-meta">{{ formatWorkflowUpdatedAt(workflow) }}</span></template>
        <template #cell-actions="{ row: workflow }">
          <ManagementRowActions
            :menu-actions="workflowMenuActions(workflow)"
            menu-label="更多编排操作"
            @action="handleWorkflowRowAction($event, workflow)"
          />
        </template>
        <template #empty>
          <div class="workflow-empty-state">
            <div><i class="fa-solid fa-diagram-project" /></div>
            <strong>{{ workflowStore.workflows.length ? "没有匹配到编排流程" : "暂无编排流程" }}</strong>
            <span>{{ workflowStore.workflows.length ? "换个关键词试试，或者清空筛选条件。" : "新建第一个编排后，可以在这里查看用途、步骤、校验结果和最近执行。" }}</span>
            <button v-if="workflowStore.workflows.length" class="ghost-button" type="button" @click="clearWorkflowSearch">清空筛选</button>
            <button v-else class="primary-button" type="button" @click="openCreateWorkflow">新建编排</button>
          </div>
        </template>
      </ManagementList>
    </section>

    <Transition name="modal-fade">
      <div v-if="workflowDetailVisible" class="modal-backdrop" @click.self="closeWorkflowDetail">
        <section
          v-if="selectedWorkflow"
          ref="workflowDetailModalRef"
          class="modal-card workflow-detail-modal-card"
          role="dialog"
          aria-modal="true"
          aria-label="流程详情"
          tabindex="-1"
          @keydown.esc.stop.prevent="closeWorkflowDetail"
          @keydown.tab="trapModalFocus($event, workflowDetailModalRef)"
        >
        <div class="modal-card-head">
          <div>
            <span>Workflow Lifecycle</span>
            <h3>流程详情</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="收起流程详情" @click="closeWorkflowDetail">
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="workflow-detail-modal-body">
          <section class="workflow-detail-panel">
            <div class="workflow-detail-hero">
              <span class="status-pill" :class="statusClass(selectedWorkflow.status)">{{ statusLabel(selectedWorkflow.status) }}</span>
              <h3>{{ selectedWorkflow.name }}</h3>
              <p>{{ selectedWorkflow.description || "这个流程还没有填写用途说明。" }}</p>
            </div>

            <div class="workflow-detail-metrics">
              <span>
                <strong>{{ workflowNodeCount(selectedWorkflow) }}</strong>
                <small>步骤</small>
              </span>
              <span>
                <strong>{{ workflowExecutionCount(selectedWorkflow) }}</strong>
                <small>执行记录</small>
              </span>
              <span>
                <strong>{{ validationLabel(selectedWorkflow) }}</strong>
                <small>校验状态</small>
              </span>
            </div>

            <WorkflowReadinessPanel :readiness="selectedWorkflowReadiness" />
            <WorkflowRevisionPanel
              :revisions="selectedWorkflowRevisions"
              :readiness="selectedWorkflowReadiness"
              :busy-revision-id="pendingRevisionActionId"
              :workflow-status="selectedWorkflow.status"
              :disable-busy="pendingWorkflowDisable || pendingRevisionCompare"
              :empty-text="selectedWorkflowRevisionEmptyText"
              @activate="activateRevision"
              @rollback="rollbackRevision"
              @compare="compareRevision"
              @disable="disableWorkflowRuns"
            />
            <WorkflowRevisionDiff :diff="selectedWorkflowRevisionDiff" :empty-text="selectedWorkflowDiffEmptyText" />
            <div class="workflow-readable-section">
              <h4>这个流程做什么</h4>
              <p>{{ selectedWorkflow.description || "用于把多个业务动作串起来，减少人工重复处理。" }}</p>
            </div>

            <div class="workflow-readable-section">
              <h4>什么时候触发</h4>
              <p>{{ workflowTriggerText() }}</p>
            </div>

            <div class="workflow-readable-section">
              <h4>包含哪些步骤</h4>
              <div v-if="selectedWorkflowSteps.length" class="workflow-step-list">
                <article v-for="step in selectedWorkflowSteps" :key="step.id" class="workflow-step-item">
                  <b>{{ step.order }}</b>
                  <span>
                    <strong>{{ step.title }}</strong>
                    <small>{{ step.type }} · {{ step.detail }}</small>
                  </span>
                </article>
              </div>
              <p v-else>当前还没有可展示的步骤快照。进入流程图编辑器并保存草稿后，这里会显示主路径步骤。</p>
            </div>

            <div class="workflow-readable-section">
              <h4>最近执行</h4>
              <div v-if="selectedWorkflowExecutions.length" class="workflow-execution-list">
                <article v-for="execution in selectedWorkflowExecutions" :key="execution.id" class="workflow-execution-item">
                  <span class="status-pill" :class="statusClass(execution.status)">{{ executionStatusLabel(execution.status) }}</span>
                  <span>
                    <strong>{{ execution.trigger }}</strong>
                    <small>{{ execution.durationMs }} ms · {{ execution.outputSummary || execution.inputSummary }}</small>
                  </span>
                </article>
              </div>
              <p v-else>{{ selectedWorkflowExecutionEmptyText }}</p>
            </div>

            <div v-if="activeTraceExecution?.workflowId === selectedWorkflow.id" class="workflow-readable-section">
              <h4>试运行轨迹</h4>
              <WorkflowExecutionTracePanel
                :execution="activeTraceExecution"
                :selected-node-id="selectedNodeId"
                @select-node="selectTraceNode"
              />
            </div>
          </section>
        </div>

        <div class="workflow-detail-actions">
          <button class="ghost-button" type="button" @click="closeWorkflowDetail">关闭</button>
          <button class="ghost-button" type="button" :disabled="!selectedWorkflow" @click="selectedWorkflow && validateWorkflow(selectedWorkflow)">校验</button>
          <button class="ghost-button" type="button" :disabled="!selectedWorkflow" @click="selectedWorkflow && openTrialRunDialog(selectedWorkflow)">试运行</button>
          <button class="ghost-button" type="button" :disabled="!selectedWorkflow" @click="openEditWorkflow()">编辑信息</button>
          <button class="ghost-button" type="button" :disabled="!selectedWorkflow || !selectedWorkflowCanPublish" @click="publishWorkflow()">发布</button>
          <button
            v-if="selectedWorkflow && workspaces.can(selectedWorkflow.workspaceId, auth.user?.id || '', 'EDIT')"
            class="primary-button"
            type="button"
            :disabled="!selectedWorkflow || editorDraftLoadState === 'loading'"
            :aria-busy="editorDraftLoadState === 'loading' ? 'true' : undefined"
            @click="openWorkflowEditor()"
          >
            编辑流程图
          </button>
        </div>
        </section>
      </div>
    </Transition>

    <div
      v-if="editorDraftLoadState === 'loading' && !workflowEditorVisible"
      class="workflow-editor-overlay workflow-editor-overlay-full-bleed"
    >
      <section class="workflow-editor-shell" aria-label="流程图编辑器">
        <header class="workflow-editor-topbar">
          <div>
            <span>流程画布编辑</span>
            <h3>{{ selectedWorkflow?.name || "流程图" }}</h3>
            <p>{{ workflowEditorFeedbackMessage }}</p>
          </div>
        </header>
        <div class="workflow-editor-banner loading" role="status">
          <strong>正在加载</strong>
          <small>正在加载最新草稿和编译结果，请稍候。</small>
        </div>
      </section>
    </div>

    <div
      v-if="workflowEditorVisible"
      class="workflow-editor-overlay workflow-editor-overlay-full-bleed"
      @click="closeContextMenu"
    >
      <section
        ref="workflowEditorShellRef"
        class="workflow-editor-shell"
        aria-label="流程图编辑器"
        :data-editor-dirty-state="editorDirtyState"
        tabindex="-1"
      >
        <header class="workflow-editor-topbar">
          <div class="workflow-editor-header-row">
            <div class="workflow-editor-meta">
              <span>流程画布编辑</span>
              <div class="workflow-editor-title-row">
                <h3>{{ selectedWorkflow?.name || "流程图" }}</h3>
                <button class="workflow-editor-help-button" type="button" aria-label="画布操作说明" :title="workflowEditorHelpText">
                  <i class="fa-solid fa-circle-info" />
                </button>
              </div>
            </div>
            <div class="workflow-editor-readiness-strip" aria-label="流程发布状态">
              <span
                v-for="step in workflowEditorReadinessSteps"
                :key="step.key"
                class="workflow-editor-readiness-chip"
                :data-readiness-state="step.state"
              >
                <i :class="step.icon" />
                {{ step.label }}
              </span>
            </div>
          </div>
          <div class="workflow-editor-action-row">
            <div class="workflow-editor-secondary-actions workflow-editor-actions">
              <div class="workflow-editor-action-group" role="group" aria-label="辅助操作">
                <button class="ghost-button" type="button" :disabled="!selectedWorkflow || workflowEditorBusy" @click="openEditWorkflow()">基础信息</button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" aria-label="画布整理">
                <button
                  data-action="duplicate-selected-node"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedGraphNode || workflowEditorBusy"
                  @click="duplicateSelectedNode"
                >
                  复制节点
                </button>
                <button
                  data-action="auto-layout-editor-graph"
                  class="ghost-button"
                  type="button"
                  title="按拓扑分层展开节点，消除堆叠与交叉"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="applyAutoLayout"
                >
                  格式化画布
                </button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" aria-label="校验运行">
                <button data-action="validate-editor-workflow" class="ghost-button" type="button" :disabled="!selectedWorkflow || workflowEditorBusy" @click="validateEditorWorkflow">
                  {{ pendingEditorAction === "validate" ? "正在检查…" : "检查问题" }}
                </button>
                <button data-action="open-trial-run-dialog" class="ghost-button" type="button" :disabled="!selectedWorkflow || workflowEditorBusy" @click="trialRunEditorWorkflow">
                  {{ pendingEditorAction === "trial-run" ? "正在准备…" : "模拟运行" }}
                </button>
                <button
                  v-if="canReviseFromFailure"
                  data-action="revise-draft-from-failure"
                  class="ghost-button"
                  type="button"
                  title="按编译/试运行问题回到智能编排修订草稿（只出新 Draft，不自动发布）"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="reviseDraftFromFailure"
                >
                  按问题修订草稿
                </button>
              </div>
            </div>
          </div>
          <div class="workflow-editor-primary-actions workflow-editor-actions" aria-label="关键操作">
            <button data-action="save-editor-draft" class="primary-button" type="button" :disabled="!selectedWorkflow || workflowEditorBusy" @click="saveEditorDraft">
              {{ pendingEditorAction === "save" ? "正在保存…" : "保存画布" }}
            </button>
            <button
              data-action="publish-editor-workflow"
              class="workflow-editor-publish-button"
              type="button"
              :title="workflowEditorPublishTitle"
              :disabled="!selectedWorkflow || workflowEditorBusy || !selectedWorkflowCanPublish"
              @click="publishEditorWorkflow"
            >
              {{ pendingEditorAction === "publish" ? "正在发布…" : "发布上线" }}
            </button>
            <button
              class="workflow-editor-close-button"
              type="button"
              aria-label="退出编辑"
              title="退出编辑"
              :disabled="workflowEditorBusy"
              @click="closeWorkflowEditor()"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </div>
        </header>

        <div class="workflow-workbench workflow-workbench-full-bleed" @click="closeContextMenu">
          <WorkflowNodePalette class="workflow-workbench-column" :variable-refs="editorVariableRefs" @add-node="addNodeToDraft" />
          <WorkflowGraphCanvas
            class="workflow-workbench-main"
            :graph="editorGraph"
            :selected-node-id="selectedNodeId"
            :selected-edge-id="selectedEdgeId"
            @select-node="setSelectedNode"
            @select-edge="setSelectedEdge"
            @open-node-context-menu="openNodeContextMenu"
            @open-edge-context-menu="openEdgeContextMenu"
            @update-node-position="updateNodePosition"
            @update-viewport="updateViewport"
            @connect-nodes="connectNodes"
          />
          <aside class="workflow-workbench-column workflow-workbench-side workflow-workbench-side-scrollable workflow-scroll-fade" @click.stop>
            <WorkflowInspector
              v-if="selectedGraphNode"
              :node="selectedGraphNode"
              :tools="workflowToolCatalog"
              :tool-options="availableToolOptions"
              :variable-refs="selectedNodeVariableRefs"
              :tool-catalog-error="workflowToolCatalogError"
              @update-node-label="updateSelectedNodeLabel"
              @update-node-data="updateSelectedNodeData"
            />
            <WorkflowEdgeInspector
              v-else-if="selectedGraphEdge"
              :edge="selectedGraphEdge"
              @update-edge-data="updateSelectedEdgeData"
            />
            <WorkflowInspector
              v-else
              :node="selectedGraphNode"
              :tools="workflowToolCatalog"
              :tool-options="availableToolOptions"
              :variable-refs="selectedNodeVariableRefs"
              :tool-catalog-error="workflowToolCatalogError"
              @update-node-label="updateSelectedNodeLabel"
              @update-node-data="updateSelectedNodeData"
            />
            <WorkflowIssuesPanel
              :issues="activeCompilationIssues"
              :selected-node-id="selectedNodeId"
              :selected-edge-id="selectedEdgeId"
              :show-revise-cta="canReviseFromFailure"
              @focus-node="focusIssue"
              @focus-edge="focusEdgeIssue"
              @revise-from-failure="reviseDraftFromFailure"
            />
            <WorkflowExecutionTracePanel
              v-if="activeTraceExecution?.workflowId === selectedWorkflow?.id"
              :execution="activeTraceExecution"
              :selected-node-id="selectedNodeId"
              @select-node="selectTraceNode"
            />
          </aside>
        </div>
      </section>

      <div
        v-if="contextMenu"
        class="workflow-context-menu"
        :style="{ left: `${contextMenu.position.x}px`, top: `${contextMenu.position.y}px` }"
        @click.stop
      >
        <button
          data-action="delete-context-target"
          class="workflow-context-menu-item danger"
          type="button"
          :disabled="isContextTargetDeleteDisabled()"
          @click="deleteContextTarget"
        >
          {{ isContextTargetDeleteDisabled() ? "起止节点不可删除" : contextMenu.targetType === "node" ? "删除节点" : "删除连线" }}
        </button>
      </div>
    </div>

    <WorkflowTrialRunDialog
      v-if="trialRunVisible"
      :visible="trialRunVisible"
      :workflow-name="trialRunTargetWorkflowName"
      :input-schema="trialRunInputSchema"
      :last-successful-input="lastSuccessfulTrialInput"
      :submitting="pendingTrialRun"
      @close="closeTrialRunDialog"
      @submit="submitTrialRun"
    />

    <Transition name="modal-fade">
      <div v-if="workflowMetadataVisible" class="modal-backdrop" @click.self="closeWorkflowMetadata">
        <section
          ref="workflowMetadataModalRef"
          class="modal-card workflow-metadata-modal-card"
          role="dialog"
          aria-modal="true"
          :aria-label="workflowMetadataMode === 'create' ? '新建编排' : '编辑编排'"
          tabindex="-1"
          @keydown.esc.stop.prevent="closeWorkflowMetadata"
          @keydown.tab="trapModalFocus($event, workflowMetadataModalRef)"
        >
        <div class="modal-card-head">
          <div>
            <span>Workflow Metadata</span>
            <h3>{{ workflowMetadataMode === "create" ? "新建编排" : "编辑编排" }}</h3>
          </div>
          <button class="icon-action-button" type="button" :aria-label="workflowMetadataMode === 'create' ? '收起新建编排' : '收起编辑编排'" @click="closeWorkflowMetadata">
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="workflow-metadata-body">
          <div class="workflow-create-guide">
            <span><b>1</b> 基本信息</span>
            <span><b>2</b> 触发方式</span>
            <span><b>3</b> 步骤确认</span>
            <span><b>4</b> 保存发布</span>
          </div>
          <label class="drawer-field">
            <span>名称 <b class="field-required-mark">*</b></span>
            <input
              v-model="workflowDraft.name"
              required
              aria-required="true"
              :aria-invalid="workflowMetadataTouched && Boolean(workflowNameError)"
              aria-describedby="workflow-name-error"
              @blur="workflowMetadataTouched = true"
            />
            <small v-if="workflowMetadataTouched && workflowNameError" id="workflow-name-error" class="field-error" role="alert">{{ workflowNameError }}</small>
          </label>
          <div class="form-two">
            <label class="drawer-field">
              <span>业务空间 <b class="field-required-mark">*</b></span>
              <AppSelect
                class="workflow-workspace-select"
                v-model="workflowDraft.workspaceId"
                :options="workspaceOptions"
                :aria-required="true"
                :aria-invalid="workflowMetadataTouched && Boolean(workflowWorkspaceError)"
              />
              <small v-if="workflowMetadataTouched && workflowWorkspaceError" class="field-error" role="alert">{{ workflowWorkspaceError }}</small>
            </label>
            <label class="drawer-field">
              <span>Slug</span>
              <input v-model="workflowDraft.slug" placeholder="留空时按名称生成" />
            </label>
          </div>
          <label class="drawer-field">
            <span>状态</span>
            <AppSelect v-model="workflowDraft.status" :options="workflowStatusOptions" />
          </label>
          <label class="drawer-field"><span>这个流程做什么</span><textarea v-model="workflowDraft.description" class="workflow-description-input" rows="4" /></label>
          <div class="drawer-schema-preview">
            <div>
              <i class="fa-solid fa-diagram-project" />
              <span>
                <strong>{{ editorGraph.nodes.length }} 个步骤 / {{ editorGraph.edges.length }} 条连接</strong>
                <small>保存后可在详情中查看，也可以进入编辑流程图调整步骤。</small>
              </span>
            </div>
          </div>
        </div>

        <div class="workflow-metadata-actions">
          <button class="ghost-button" type="button" @click="closeWorkflowMetadata">取消</button>
          <button class="primary-button" type="button" :disabled="!canSaveWorkflowMetadata" @click="saveWorkflowMetadata">保存编排</button>
        </div>
        </section>
      </div>
    </Transition>

    <div v-if="workflowActionNote && !workflowMetadataVisible" class="action-toast" role="status" aria-live="polite">
      <span>{{ workflowActionNote }}</span>
      <button type="button" aria-label="隐藏提示" @click="workflowActionNote = ''">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.workflow-orchestration-page {
  width: 100%;
  max-width: 100%;
  margin: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 24px;
  color: #0f172a;
}

.workflow-orchestration-page > * {
  grid-column: 1 / -1;
  min-width: 0;
}

.workflow-orchestration-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.workflow-orchestration-header > div:first-child > span {
  display: inline-block;
  color: #059669;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  line-height: 1;
  text-transform: uppercase;
}

.workflow-orchestration-header h2 {
  margin: 4px 0 0;
  color: #0f172a;
  font-size: 24px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1.18;
}

.workflow-orchestration-header p {
  margin: 6px 0 0;
  color: #64748b;
  font-size: 12px;
  font-weight: 400;
  line-height: 1.5;
}

.workflow-orchestration-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.workflow-orchestration-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 24px;
}

.workflow-stat-card {
  display: flex;
  min-height: 132px;
  flex-direction: column;
  justify-content: space-between;
  padding: 20px;
  border: 1px solid #f1f5f9;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 10px 30px -10px rgb(0 0 0 / 0.04), 0 1px 3px rgb(0 0 0 / 0.02);
}

.workflow-stat-card > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.workflow-stat-card small {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1;
  text-transform: uppercase;
}

.workflow-stat-card span {
  display: inline-flex;
  width: 32px;
  height: 32px;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
}

.workflow-stat-card span i {
  font-size: 12px;
}

.workflow-stat-card strong {
  display: block;
  margin-top: 16px;
  color: #0f172a;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 30px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1;
}

.workflow-stat-card em {
  display: block;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #f8fafc;
  color: #64748b;
  font-size: 10px;
  font-style: normal;
  font-weight: 300;
  line-height: 1.35;
}

.workflow-stat-card.emerald small,
.workflow-stat-card.emerald strong,
.workflow-stat-card.emerald span {
  color: #059669;
}

.workflow-stat-card.emerald span {
  background: rgb(236 253 245 / 0.65);
}

.workflow-stat-card.amber small,
.workflow-stat-card.amber strong,
.workflow-stat-card.amber span {
  color: #d97706;
}

.workflow-stat-card.amber span {
  background: #fffbeb;
}

.workflow-stat-card.rose small,
.workflow-stat-card.rose span {
  color: #e11d48;
}

.workflow-stat-card.rose span {
  background: #fff1f2;
}

/* Transparent shell — ManagementList owns table/toolbar/footer chrome. */
.workflow-orchestration-table-card.management-list-card {
  display: block;
  min-width: 0;
  overflow: visible;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.workflow-table-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 20px;
  border-bottom: 1px solid #f1f5f9;
  background: #fff;
}

.workflow-search-field {
  position: relative;
  display: block;
  width: 320px;
  flex: 0 0 auto;
}

.workflow-search-field > i {
  position: absolute;
  left: 14px;
  top: 50%;
  color: #94a3b8;
  font-size: 12px;
  transform: translateY(-50%);
}

.workflow-search-field input {
  width: 100%;
  height: 44px;
  padding: 0 48px 0 36px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #1e293b;
  font-size: 12px;
  font-weight: 500;
  line-height: 44px;
  outline: none;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}

.workflow-search-field input::placeholder {
  color: #64748b;
}

.workflow-search-field input:focus {
  border-color: rgb(16 185 129 / 0.55);
  background: #fff;
  box-shadow: 0 0 0 3px rgb(16 185 129 / 0.12);
}

.workflow-search-field button {
  position: absolute;
  right: 2px;
  top: 50%;
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: #94a3b8;
  transform: translateY(-50%);
}

.workflow-search-field button:hover {
  color: #64748b;
}

.workflow-list-filter-note {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border: 1px solid #f1f5f9;
  border-radius: 999px;
  background: #f8fafc;
  color: #64748b;
  font-size: 11px;
  font-weight: 500;
  line-height: 1;
}

.workflow-list-filter-note span {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: #10b981;
}

.workflow-name-cell {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
}

.workflow-name-cell strong,
.workflow-name-cell .aw-table-title {
  display: block;
  max-width: 100%;
  overflow: hidden;
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-name-cell small,
.workflow-name-cell .aw-table-subtitle {
  display: block;
  max-width: 100%;
  margin-top: 3px;
  overflow: hidden;
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.8125rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-workspace-cell {
  display: block;
  max-width: 100%;
  overflow: hidden;
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-mono-cell,
.workflow-success-cell {
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.workflow-success-cell {
  color: #059669 !important;
}

.workflow-status-badge {
  display: inline-flex;
  align-items: center;
  padding: 3px 8px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  background: #f8fafc;
  color: #64748b;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  line-height: 1;
  white-space: nowrap;
}

.workflow-status-badge.published {
  border-color: #ccfbf1;
  background: #f0fdfa;
  color: #0f766e;
}

.workflow-status-badge.draft {
  border-color: #fef3c7;
  background: #fffbeb;
  color: #b45309;
}

.workflow-status-badge.disabled {
  border-color: #e2e8f0;
  background: #f8fafc;
  color: #64748b;
}

.workflow-empty-state {
  display: flex;
  min-height: 250px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48px 24px;
  text-align: center;
}

.workflow-empty-state div {
  display: flex;
  width: 56px;
  height: 56px;
  align-items: center;
  justify-content: center;
  border: 1px solid #f1f5f9;
  border-radius: 16px;
  background: #f8fafc;
}

.workflow-empty-state i {
  color: #cbd5e1;
  font-size: 20px;
}

.workflow-empty-state strong {
  margin-top: 16px;
  color: #334155;
  font-size: 14px;
  font-weight: 700;
}

.workflow-empty-state span {
  margin-top: 4px;
  color: #64748b;
  font-size: 11px;
}

.workflow-empty-state .primary-button,
.workflow-empty-state .ghost-button {
  margin-top: 14px;
}

.workflow-pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 24px;
  border-top: 1px solid #f1f5f9;
  background: rgb(248 250 252 / 0.4);
}

.workflow-pagination,
.workflow-pagination button {
  font-size: 11px;
  font-weight: 600;
}

.workflow-pagination > div:first-child {
  color: #64748b;
}

.workflow-pagination > div:last-child,
.workflow-pagination > div:last-child > span {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.workflow-pagination button {
  min-height: 44px;
  padding: 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  color: #334155;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    border-color 0.16s ease;
}

.workflow-pagination button:not(:disabled):hover {
  background: #f8fafc;
}

.workflow-pagination button:disabled {
  cursor: not-allowed;
  background: #f1f5f9;
  color: #cbd5e1;
}

.workflow-pagination span button {
  min-width: 44px;
  padding: 0 10px;
}

.workflow-pagination span button.active {
  border-color: #0d9488;
  background: #0d9488;
  color: #fff;
  box-shadow: 0 1px 3px rgb(13 148 136 / 0.18);
}

.workflow-detail-modal-card,
.workflow-metadata-modal-card {
  border-radius: 16px;
  border-color: #e2e8f0;
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.12), 0 1px 10px rgb(15 23 42 / 0.04);
}

.workflow-editor-overlay {
  background: #fafbfd;
}

.workflow-editor-shell {
  background: #fff;
}

.workflow-workbench {
  background: #fff;
}

@media (max-width: 1180px) {
  .workflow-orchestration-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .workflow-orchestration-header,
  .workflow-table-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .workflow-orchestration-actions {
    align-items: flex-start;
  }
}

@media (max-width: 720px) {
  .workflow-orchestration-summary {
    grid-template-columns: 1fr;
  }

  .workflow-orchestration-actions {
    flex-direction: column;
    align-items: stretch;
  }

  .workflow-search-field,
  .workflow-orchestration-actions > .primary-button {
    width: 100%;
  }
}
</style>
