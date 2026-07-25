<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { autoLayoutWorkflowGraph, layoutWorkflowGraphIfNeeded } from "../components/workflow/workflow-layout";
import { apiErrorMessage } from "../services/api";
import { useAgentStore } from "../stores/agents";
import { useModelConfigStore } from "../stores/modelConfigs";
import { useSmartDagStore, type FailureFeedback, type FailureFeedbackIssue } from "../stores/smartdag";
import { useWorkflowStore } from "../stores/workflow";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Workflow,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
  WorkflowGraphNodeType,
  WorkflowSummary,
} from "../types/domain";

type BlueprintStatus = "published" | "review" | "draft";
type NodeTheme = "start" | "tool" | "condition" | "approval" | "transform" | "end";
type ToastTone = "success" | "error" | "info";

interface SmartNode {
  id: string;
  graphType: WorkflowGraphNodeType;
  type: string;
  title: string;
  desc: string;
  x: number;
  y: number;
  theme: NodeTheme;
  isAiDraft?: boolean;
  aiReason?: string;
}

interface SmartConnection {
  from: string;
  to: string;
}

interface SmartBlueprint {
  id: string;
  name: string;
  description: string;
  workspaceId: string;
  space: string;
  agent: string;
  automationMode: string;
  aiScore: number;
  status: BlueprintStatus;
  nodes: SmartNode[];
  connections: SmartConnection[];
}

const smart = useSmartDagStore();
const workflowStore = useWorkflowStore();
const workspaceStore = useWorkspaceStore();
const agentStore = useAgentStore();
const modelConfigStore = useModelConfigStore();
const router = useRouter();
const route = useRoute();
/** Seeded FailureFeedback from compile/trial failure CTA (P4.3 / D14). */
const pendingFailureFeedback = ref<FailureFeedback | null>(null);

const emptyBlueprint: SmartBlueprint = {
  id: "",
  name: "等待生成智能草稿",
  description: "选择业务空间并输入自然语言目标。",
  workspaceId: "",
  space: "未选择业务空间",
  agent: "未绑定 Agent",
  automationMode: "AI 协同草稿",
  aiScore: 0,
  status: "draft",
  nodes: [],
  connections: [],
};
const blueprints = ref<SmartBlueprint[]>([]);
const blueprintDrafts = new Map<string, WorkflowDraftRecord>();

const activeWorkspaceId = ref("");
const selectedAgentId = ref("");
const selectedBlueprintId = ref("");
const selectedNodeId = ref("");
const copilotPrompt = ref(smart.goal);
const blueprintPickerOpen = ref(false);
const listQuery = ref("");
const statusFilter = ref<"ALL" | BlueprintStatus>("ALL");
const listPage = ref(1);
const listPageSize = 4;
const canvasZoom = ref(1);
const canvasPan = ref({ x: 0, y: 0 });
const canvasPanning = ref({ active: false, pointerId: -1, startX: 0, startY: 0, originX: 0, originY: 0 });
const isNarrowViewport = ref(false);
const blueprintToolbarCompact = ref(false);
const leftPanelCollapsed = ref(false);
const rightPanelCollapsed = ref(false);
const focusMode = ref(false);
const aiStatus = ref({ isGenerating: false, activeStep: -1 });
const compilerIssues = ref<{ title: string; desc: string }[]>([]);
const toast = ref<{ show: boolean; message: string; tone: ToastTone }>({ show: false, message: "", tone: "info" });
const sandbox = ref({ show: false, inputJson: "{}" });
const sandboxError = ref("");
const canvasContainerRef = ref<HTMLElement>();
const blueprintModalRef = ref<HTMLElement>();
const blueprintSearchInputRef = ref<HTMLInputElement>();
const sandboxModalRef = ref<HTMLElement>();
const sandboxInputRef = ref<HTMLTextAreaElement>();
const lastFocusedElement = ref<HTMLElement>();
let toastTimer: number | undefined;

const workspaces = computed(() => workspaceStore.items || []);
const activeWorkspace = computed(() => workspaces.value.find((workspace) => workspace.id === activeWorkspaceId.value));
const workspaceAgents = computed(() =>
  (agentStore.items || []).filter((agent) => !activeWorkspaceId.value || agent.workspaceId === activeWorkspaceId.value),
);
const selectedAgent = computed(() => workspaceAgents.value.find((agent) => agent.id === selectedAgentId.value));
const selectedAgentModelConfig = computed(() => {
  const modelId = selectedAgent.value?.modelConfigId || "";
  if (!modelId) return undefined;
  return (modelConfigStore.items || []).find((config) => config.id === modelId);
});
const agentHasUsableModel = computed(() => {
  const agent = selectedAgent.value;
  if (!agent?.modelConfigId) return false;
  const config = selectedAgentModelConfig.value;
  if (!config) {
    // Catalog may not be loaded; treat non-empty modelConfigId as usable until proven otherwise.
    return Boolean(agent.modelConfigId);
  }
  return config.status !== "DISABLED" && Boolean(config.apiBase || config.modelName);
});
const canSendGenerateTurn = computed(
  () =>
    Boolean(activeWorkspaceId.value && selectedAgentId.value && agentHasUsableModel.value) &&
    smart.sessionStatus !== "CLOSED" &&
    !aiStatus.value.isGenerating &&
    !smart.generating,
);
const turnHistory = computed(() => smart.turns || []);
const currentBlueprint = computed(() => blueprints.value.find((workflow) => workflow.id === selectedBlueprintId.value) || emptyBlueprint);
/** Canvas re-render key: SoT is latest draft after each successful turn (P1.5.3). */
const canvasRenderKey = computed(
  () => `${smart.canvasEpoch}:${smart.generatedDraft?.id || ""}:${smart.generatedDraft?.draftVersion || 0}:${smart.generatedDraft?.graphHash || ""}`,
);
const currentWorkflow = computed(() =>
  workflowStore.workflowDetails[currentBlueprint.value.id] ||
  workflowStore.workflows.find((workflow) => workflow.id === currentBlueprint.value.id),
);
const selectedNode = computed(() => currentBlueprint.value.nodes.find((node) => node.id === selectedNodeId.value));
const hasAiDraft = computed(() => currentBlueprint.value.nodes.some((node) => node.isAiDraft));
const draftGenerated = computed(() => Boolean(currentBlueprint.value.id && blueprintDrafts.has(currentBlueprint.value.id)));
const currentReadiness = computed(() =>
  currentBlueprint.value.id ? workflowStore.readinessByWorkflowId[currentBlueprint.value.id] : undefined,
);
const canPublishSmartDraft = computed(() => Boolean(currentReadiness.value?.canPublish));
const averageAiScore = computed(() =>
  blueprints.value.length
    ? Math.round(blueprints.value.reduce((sum, item) => sum + item.aiScore, 0) / blueprints.value.length)
    : 0,
);

const aiSteps = ["解析业务目标和风险门槛", "匹配当前空间可调用能力", "推断可执行节点结构", "生成画布节点布局与连线", "写入正式 Workflow Draft"];
const canvasNodeSize = { width: 220, height: 136 };
const canvasDesktopMinWidth = 1180;

const filteredBlueprintList = computed(() => {
  const keyword = listQuery.value.trim().toLowerCase();
  return blueprints.value.filter((workflow) => {
    const matchesStatus = statusFilter.value === "ALL" || workflow.status === statusFilter.value;
    const matchesKeyword =
      !keyword ||
      [workflow.name, workflow.description, workflow.agent, workflow.space, workflow.automationMode, getStatusText(workflow.status)]
        .join(" ")
        .toLowerCase()
        .includes(keyword);
    return matchesStatus && matchesKeyword;
  });
});
const totalListPages = computed(() => Math.max(1, Math.ceil(filteredBlueprintList.value.length / listPageSize)));
const paginatedBlueprintList = computed(() =>
  filteredBlueprintList.value.slice((listPage.value - 1) * listPageSize, listPage.value * listPageSize),
);
const listPageNumbers = computed(() => Array.from({ length: totalListPages.value }, (_, index) => index + 1));
const paginationStart = computed(() =>
  filteredBlueprintList.value.length === 0 ? 0 : (listPage.value - 1) * listPageSize + 1,
);
const paginationEnd = computed(() => Math.min(listPage.value * listPageSize, filteredBlueprintList.value.length));

watch(totalListPages, (pageCount) => {
  if (listPage.value > pageCount) listPage.value = pageCount;
});

function closeToast() {
  if (toastTimer) window.clearTimeout(toastTimer);
  toastTimer = undefined;
  toast.value.show = false;
}

function showToast(message: string, tone: ToastTone = "info", duration = tone === "error" ? 8000 : 6000) {
  toast.value = { show: true, message, tone };
  if (toastTimer) window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(closeToast, duration);
}

function captureFocusBeforeModal() {
  lastFocusedElement.value = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
}

function restoreFocusAfterModal() {
  nextTick(() => {
    lastFocusedElement.value?.focus();
    lastFocusedElement.value = undefined;
  });
}

function openBlueprintPicker() {
  captureFocusBeforeModal();
  resetBlueprintFilters();
  blueprintPickerOpen.value = true;
  nextTick(() => blueprintSearchInputRef.value?.focus());
}

function closeBlueprintPicker(restoreFocus = true) {
  blueprintPickerOpen.value = false;
  if (restoreFocus) restoreFocusAfterModal();
}

function openSandbox() {
  if (!draftGenerated.value) {
    showToast("请先生成正式 Workflow Draft。", "error");
    return;
  }
  captureFocusBeforeModal();
  sandboxError.value = "";
  sandbox.value.show = true;
  nextTick(() => sandboxInputRef.value?.focus());
}

function closeSandbox(restoreFocus = true) {
  sandbox.value.show = false;
  sandboxError.value = "";
  if (restoreFocus) restoreFocusAfterModal();
}

function getFocusableElements(container?: HTMLElement) {
  if (!container) return [];
  return Array.from(
    container.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => !element.hasAttribute("disabled") && element.getAttribute("aria-hidden") !== "true");
}

function trapModalFocus(event: KeyboardEvent, container?: HTMLElement) {
  if (event.key !== "Tab") return;
  const focusableElements = getFocusableElements(container);
  if (!focusableElements.length) {
    event.preventDefault();
    container?.focus();
    return;
  }
  const firstElement = focusableElements[0];
  const lastElement = focusableElements[focusableElements.length - 1];
  if (event.shiftKey && document.activeElement === firstElement) {
    event.preventDefault();
    lastElement.focus();
    return;
  }
  if (!event.shiftKey && document.activeElement === lastElement) {
    event.preventDefault();
    firstElement.focus();
  }
}

function handleBlueprintModalKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    closeBlueprintPicker();
    return;
  }
  trapModalFocus(event, blueprintModalRef.value);
}

function handleSandboxModalKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    closeSandbox();
    return;
  }
  trapModalFocus(event, sandboxModalRef.value);
}

function getStatusText(status: BlueprintStatus) {
  if (status === "published") return "已发布";
  if (status === "review") return "待复核";
  return "AI 草稿";
}

function getStatusClass(status: BlueprintStatus) {
  return "is-" + status;
}

function getAutomationClass(mode: string) {
  if (mode.includes("风险")) return "is-risk";
  if (mode.includes("服务") || mode.includes("触达")) return "is-service";
  return "is-ai";
}

function getNodeTypeClass(theme: NodeTheme) {
  return "is-" + theme;
}

function getParameterSchema(type?: string) {
  if (type === "START") return '{"type":"object","properties":{}}';
  if (type === "TOOL CALL") return '{"toolId":"workspace capability UUID","inputMapping":{}}';
  if (type === "CONDITION") return '{"expression":"{{input.amount}} > 500"}';
  if (type === "APPROVAL") return '{"reason":"高风险动作需人工确认"}';
  if (type === "TRANSFORM") return '{"template":"{{workflowVars.businessGoal}}"}';
  return '{"output":{"kind":"ref","path":"nodeOutputs.result.result"}}';
}

function getConnectionPath(conn: SmartConnection) {
  const fromNode = currentBlueprint.value.nodes.find((node) => node.id === conn.from);
  const toNode = currentBlueprint.value.nodes.find((node) => node.id === conn.to);
  if (!fromNode || !toNode) return "";
  const startX = fromNode.x + 220;
  const startY = fromNode.y + 54;
  const endX = toNode.x;
  const endY = toNode.y + 54;
  const dx = Math.abs(endX - startX) * 0.45;
  return "M " + startX + " " + startY + " C " + (startX + dx) + " " + startY + ", " + (endX - dx) + " " + endY + ", " + endX + " " + endY;
}

function calculateGraphBounds(nodes = currentBlueprint.value.nodes) {
  if (!nodes.length) {
    return { minX: 0, minY: 0, maxX: canvasNodeSize.width, maxY: canvasNodeSize.height, width: canvasNodeSize.width, height: canvasNodeSize.height };
  }
  const minX = Math.min(...nodes.map((node) => node.x));
  const minY = Math.min(...nodes.map((node) => node.y));
  const maxX = Math.max(...nodes.map((node) => node.x + canvasNodeSize.width));
  const maxY = Math.max(...nodes.map((node) => node.y + canvasNodeSize.height));
  return { minX, minY, maxX, maxY, width: maxX - minX, height: maxY - minY };
}

function fitCanvasToVisibleArea() {
  const container = canvasContainerRef.value;
  if (!container || window.innerWidth <= canvasDesktopMinWidth) return;

  const containerRect = container.getBoundingClientRect();
  const leftPanelRect = document.querySelector(".smart-copilot-panel:not(.collapsed)")?.getBoundingClientRect();
  const rightPanelRect = document.querySelector(".smart-property-panel:not(.collapsed)")?.getBoundingClientRect();
  const toolbarRect = document.querySelector(".smart-blueprint-toolbar:not(.is-compact) .smart-blueprint-toolbar-inner")?.getBoundingClientRect();
  const zoomDockRect = document.querySelector(".smart-zoom-dock")?.getBoundingClientRect();
  const safeLeft = Math.max(containerRect.left + 24, leftPanelRect ? leftPanelRect.right + 24 : containerRect.left + 24);
  const safeRight = Math.min(containerRect.right - 24, rightPanelRect ? rightPanelRect.left - 24 : containerRect.right - 24);
  const safeTop = Math.max(containerRect.top + 24, toolbarRect ? toolbarRect.bottom + 24 : containerRect.top + 24);
  const safeBottom = Math.min(containerRect.bottom - 24, zoomDockRect ? zoomDockRect.top - 24 : containerRect.bottom - 24);
  const safeWidth = Math.max(320, safeRight - safeLeft);
  const safeHeight = Math.max(260, safeBottom - safeTop);
  const bounds = calculateGraphBounds();
  const nextZoom = Math.min(1, Math.max(0.35, Math.min(safeWidth / (bounds.width + 96), safeHeight / (bounds.height + 96))));
  const safeCenterX = safeLeft - containerRect.left + safeWidth / 2;
  const safeCenterY = safeTop - containerRect.top + safeHeight / 2;

  canvasZoom.value = Number(nextZoom.toFixed(2));
  canvasPan.value = {
    x: Number((safeCenterX - (bounds.minX + bounds.width / 2) * nextZoom).toFixed(1)),
    y: Number((safeCenterY - (bounds.minY + bounds.height / 2) * nextZoom).toFixed(1)),
  };
}

function resetBlueprintFilters() {
  listQuery.value = "";
  statusFilter.value = "ALL";
  listPage.value = 1;
}

function setStatusFilter(status: "ALL" | BlueprintStatus) {
  statusFilter.value = status;
  listPage.value = 1;
}

function workflowStatus(value: WorkflowSummary | Workflow): BlueprintStatus {
  if (value.status === "Published") return "published";
  if (value.status === "Review") return "review";
  return "draft";
}

function nodeTheme(type: WorkflowGraphNodeType): NodeTheme {
  if (type === "Start") return "start";
  if (type === "End") return "end";
  if (type === "Tool" || type === "HTTP" || type === "SubWorkflow") return "tool";
  if (type === "Condition" || type === "Parallel" || type === "ForEach") return "condition";
  if (type === "Approval") return "approval";
  return "transform";
}

function nodeTypeText(type: WorkflowGraphNodeType) {
  if (type === "Start") return "START";
  if (type === "End") return "END";
  if (type === "Tool") return "TOOL CALL";
  return type.toUpperCase();
}

function blueprintFromDraft(workflow: WorkflowSummary | Workflow, draft: WorkflowDraftRecord, confidence = 90): SmartBlueprint {
  const workspace = workspaces.value.find((item) => item.id === workflow.workspaceId);
  // Auto-layout when LLM left nodes stacked so canvas is usable.
  const laidOut = layoutWorkflowGraphIfNeeded(draft.graph);
  return {
    id: workflow.id,
    name: workflow.name,
    description: workflow.description,
    workspaceId: workflow.workspaceId,
    space: workspace?.displayName || workspace?.name || workflow.workspaceId,
    agent: "正式 Workflow Draft",
    automationMode: "AI 生成 · v1",
    aiScore: confidence,
    status: workflowStatus(workflow),
    nodes: laidOut.nodes.map((node) => ({
      id: node.id,
      graphType: node.type,
      type: nodeTypeText(node.type),
      title: node.label || node.id,
      desc: typeof node.ui?.description === "string" ? node.ui.description : nodeDescription(node.type),
      x: node.position?.x || 0,
      y: node.position?.y || 0,
      theme: nodeTheme(node.type),
      isAiDraft: node.ui?.generated === true,
      aiReason: typeof node.ui?.reason === "string" ? node.ui.reason : undefined,
    })),
    connections: (laidOut.edges || [])
      .filter((edge) => edge.sourceNodeId && edge.targetNodeId)
      .map((edge) => ({ from: edge.sourceNodeId, to: edge.targetNodeId })),
  };
}

function nodeDescription(type: WorkflowGraphNodeType) {
  const descriptions: Record<WorkflowGraphNodeType, string> = {
    Start: "读取流程输入与业务上下文",
    End: "返回正式流程结果",
    Tool: "调用当前 Workspace 已发布能力",
    HTTP: "执行 HTTP 动作",
    SubWorkflow: "调用已发布子流程",
    Transform: "整理上游执行结果",
    Approval: "等待人工确认",
    Condition: "按表达式选择执行分支",
    Parallel: "并行处理多个分支",
    ForEach: "遍历集合中的每个项目",
  };
  return descriptions[type];
}

function registerBlueprint(workflow: WorkflowSummary | Workflow, draft: WorkflowDraftRecord, confidence = 90, select = true) {
  const blueprint = blueprintFromDraft(workflow, draft, confidence);
  const index = blueprints.value.findIndex((item) => item.id === blueprint.id);
  if (index >= 0) blueprints.value.splice(index, 1, blueprint);
  else blueprints.value.unshift(blueprint);
  blueprintDrafts.set(blueprint.id, draft);
  if (select) loadBlueprint(blueprint.id);
}

function loadBlueprint(id: string) {
  const workflow = blueprints.value.find((item) => item.id === id);
  const draft = blueprintDrafts.get(id);
  const detail = workflowStore.workflowDetails[id];
  if (!workflow || !draft || !detail) return;
  selectedBlueprintId.value = workflow.id;
  selectedNodeId.value = workflow.nodes[0]?.id || "";
  activeWorkspaceId.value = workflow.workspaceId;
  workflowStore.selectedWorkflowId = workflow.id;
  workflowStore.activeDraft = draft;
  smart.adoptDraft(detail, draft);
  copilotPrompt.value = smart.goal;
  compilerIssues.value = [];
  closeBlueprintPicker(false);
  nextTick(() => requestAnimationFrame(fitCanvasToVisibleArea));
}

function createBlankBlueprint(showMessage = true) {
  const workspace = activeWorkspace.value;
  if (!workspace) {
    if (showMessage) showToast("请先配置业务空间。", "error");
    return;
  }
  smart.resetDraft();
  const stamp = Date.now().toString().slice(-5);
  const localID = "local-smart-draft-" + stamp;
  const workflow: SmartBlueprint = {
    id: localID,
    name: "未命名智能草稿",
    description: "输入自然语言目标后，后端会创建正式 Workflow Draft。",
    workspaceId: workspace.id,
    space: workspace.displayName,
    agent: "等待生成",
    automationMode: "AI 协同草稿",
    aiScore: 0,
    status: "draft",
    nodes: [
      { id: "preview-start", graphType: "Start", type: "START", title: "定义业务目标", desc: "等待智能生成正式入口", x: 180, y: 240, theme: "start" },
      { id: "preview-end", graphType: "End", type: "END", title: "等待生成结果", desc: "生成成功后替换为正式 v1 画布", x: 560, y: 240, theme: "end" },
    ],
    connections: [{ from: "preview-start", to: "preview-end" }],
  };
  blueprints.value = [workflow, ...blueprints.value.filter((item) => !item.id.startsWith("local-smart-draft-"))];
  selectedBlueprintId.value = localID;
  selectedNodeId.value = "preview-start";
  compilerIssues.value = [{ title: "尚未生成正式草稿", desc: "输入业务目标后，系统会持久化 workflow.graph.v1，而不是创建本地假成功状态。" }];
  nextTick(() => requestAnimationFrame(fitCanvasToVisibleArea));
  if (showMessage) showToast("已准备新的智能草稿，请输入业务目标。", "success");
}

function syncGenerateContext() {
  const workspaceId = activeWorkspaceId.value || workspaceStore.activeWorkspaceId || "";
  const agentId = selectedAgentId.value || "";
  const modelConfigId = selectedAgent.value?.modelConfigId || "";
  smart.setContext(workspaceId, agentId, modelConfigId);
}

async function generateDraft() {
  if (aiStatus.value.isGenerating || smart.generating) {
    showToast("AI 正在生成当前草稿，请等待本次请求完成。", "info");
    return;
  }
  const goal = copilotPrompt.value.trim();
  if (!goal) {
    showToast("请先输入智能编排意图。", "error");
    return;
  }
  const workspaceId = activeWorkspaceId.value || workspaceStore.activeWorkspaceId;
  if (!workspaceId) {
    showToast("请先选择业务空间。", "error");
    return;
  }
  if (!selectedAgentId.value) {
    showToast("请先选择用于生成的 Agent。", "error");
    return;
  }
  if (!agentHasUsableModel.value) {
    showToast("当前 Agent 未配置可用模型，请先在 Agent 中绑定 Model Config。", "error");
    return;
  }
  syncGenerateContext();
  aiStatus.value = { isGenerating: true, activeStep: 0 };
  const stepTimer = window.setInterval(() => {
    aiStatus.value.activeStep = Math.min(aiStatus.value.activeStep + 1, aiSteps.length - 1);
  }, 220);
  try {
    const result = await smart.sendTurn({
      workspaceId,
      agentId: selectedAgentId.value,
      message: goal,
      workflowId: smart.generatedWorkflow?.id,
      feedback: pendingFailureFeedback.value || undefined,
    });
    // Feedback is one-shot seed for this revise turn (D14); clear after use.
    pendingFailureFeedback.value = null;
    if (!result.workflow || !result.draft) throw new Error("生成接口没有返回正式 Workflow Draft。");
    // Canvas SoT = turn-returned draft; force re-register blueprint (P1.5.3).
    // D5: turn only bumps draftVersion — never auto-publish from feedback path.
    registerBlueprint(result.workflow, result.draft, result.confidence || 90);
    nextTick(() => requestAnimationFrame(fitCanvasToVisibleArea));
    compilerIssues.value = [
      ...result.missingCapabilities.map((capability) => ({
        title: capability.name,
        desc: capability.reason,
      })),
      ...(result.guardReport && !result.guardReport.ok
        ? result.guardReport.violations.map((v) => ({ title: v.code, desc: v.message }))
        : []),
    ];
    copilotPrompt.value = "";
    showToast(
      result.draftVersion > 1
        ? `第 ${result.draftVersion} 版 Draft 已更新，画布已按最新图刷新。`
        : "正式 Workflow Draft 已生成，可继续多轮修订或完成生成。",
      "success",
    );
  } catch (error) {
    if (smart.lastGuardReport && !smart.lastGuardReport.ok) {
      compilerIssues.value = smart.lastGuardReport.violations.map((v) => ({
        title: v.code,
        desc: v.message,
      }));
    }
    // Toast is secondary; persistent recovery card (lastFailure) is the primary recovery surface.
    if (smart.lastErrorCode === "AGENT_MODEL_REQUIRED") {
      showToast("当前 Agent 未配置可用模型，请先在 Agent 中绑定 Model Config。", "error");
    } else if (smart.lastErrorCode === "SESSION_CLOSED") {
      showToast("生成会话已关闭，请新建会话后继续。", "error");
    } else if (smart.lastErrorCode === "GUARD_REJECTED") {
      showToast("本轮图校验未通过，已保留上一轮合法草稿。", "error");
    } else if (smart.lastFailure?.message) {
      showToast(smart.lastFailure.message, "error");
    } else {
      showToast(apiErrorMessage(error, "智能生成失败，未创建本地替代草稿。"), "error");
    }
  } finally {
    window.clearInterval(stepTimer);
    aiStatus.value = { isGenerating: false, activeStep: -1 };
  }
}

/** Explicit retry: GET-calibrate session when possible, reuse last user message, new turn IDs on server. */
async function retryLastGenerateTurn() {
  if (!smart.lastFailure?.retryable || !smart.recoveryActions.retry) {
    showToast("当前失败不可重试，请关闭或新建会话。", "info");
    return;
  }
  const message = (copilotPrompt.value || smart.goal || "").trim();
  if (!message) {
    showToast("没有可重试的用户意图，请重新输入。", "error");
    return;
  }
  if (smart.workspaceId && smart.sessionId) {
    try {
      await smart.loadSession(smart.workspaceId, smart.sessionId);
    } catch {
      // Best-effort calibrate; turn still goes with current lock if any.
    }
  }
  copilotPrompt.value = message;
  await generateDraft();
}

async function closeGenerateSessionWithConfirm() {
  if (smart.generating || aiStatus.value.isGenerating) {
    showToast("生成进行中，不支持执行中取消。请等待结束后再关闭。", "info");
    return;
  }
  if (!window.confirm("确认关闭当前生成会话？已保留的 Draft 不会删除，关闭后不可继续本会话。")) {
    return;
  }
  try {
    if (smart.sessionId && smart.sessionStatus === "OPEN") {
      await smart.closeSession();
    }
    if (smart.lastFailure) {
      smart.lastFailure = {
        ...smart.lastFailure,
        sessionStatus: "CLOSED",
        retryable: false,
      };
    }
    showToast("会话已关闭。可新建会话继续；上一合法 Draft 仍保留。", "info");
  } catch (error) {
    showToast(apiErrorMessage(error, "关闭生成会话失败。"), "error");
  }
}

function startNewGenerateSession() {
  // Clear session identity so ensureSession creates a new OPEN session; keep draft canvas if any.
  smart.sessionId = "";
  smart.sessionStatus = "";
  smart.sessionLockVersion = 0;
  smart.turns = [];
  smart.lastFailure = undefined;
  smart.lastErrorCode = "";
  smart.lastGuardReport = undefined;
  showToast("已准备新建会话。发送意图后将创建新的生成会话。", "info");
}

/** 「完成生成」→ close session → enter compile/trial/publish lifecycle (P1.5.5). */
async function finishGeneration() {
  if (!smart.generatedWorkflow) {
    showToast("请先完成至少一轮成功生成。", "error");
    return;
  }
  try {
    if (smart.sessionId && smart.sessionStatus === "OPEN") {
      await smart.closeSession();
    }
    // P3.5: generation satisfaction ≠ Agent ready until publish + bind (D12).
    showToast(
      "已完成生成。生成满意不等于 Agent 已可用：仍须编译、试运行、发布并绑定到 Agent 后，对话台才能调用。",
      "success",
    );
    openInWorkflowEditor();
  } catch (error) {
    showToast(apiErrorMessage(error, "关闭生成会话失败。"), "error");
  }
}

function acceptAiDraft() {
  currentBlueprint.value.nodes = currentBlueprint.value.nodes.map((node) => ({ ...node, isAiDraft: false }));
  currentBlueprint.value.status = "review";
  currentBlueprint.value.automationMode = "AI 协同已采纳";
  showToast("已采纳当前生成结果；保存后可执行后端编译。", "success");
}

async function discardAiDraft() {
  const workflow = currentWorkflow.value;
  if (!workflow || !blueprintDrafts.has(workflow.id)) {
    createBlankBlueprint();
    return;
  }
  try {
    await workflowStore.deleteWorkflow(workflow.id);
    blueprintDrafts.delete(workflow.id);
    blueprints.value = blueprints.value.filter((item) => item.id !== workflow.id);
    smart.resetDraft();
    createBlankBlueprint(false);
    showToast("已废弃并删除生成的 Workflow Draft。", "info");
  } catch (error) {
    showToast(apiErrorMessage(error, "废弃草稿失败。"), "error");
  }
}

function graphFromBlueprint(draft: WorkflowDraftRecord, blueprint: SmartBlueprint): WorkflowGraphDraft {
  const visible = new Map(blueprint.nodes.map((node) => [node.id, node]));
  return {
    ...draft.graph,
    nodes: draft.graph.nodes
      .filter((node) => visible.has(node.id))
      .map((node) => {
        const edited = visible.get(node.id)!;
        return {
          ...node,
          label: edited.title.trim() || node.label,
          position: { x: edited.x, y: edited.y },
          ui: { ...node.ui, generated: Boolean(edited.isAiDraft), description: edited.desc, reason: edited.aiReason },
        };
      }),
    edges: draft.graph.edges.filter((edge) => visible.has(edge.sourceNodeId) && visible.has(edge.targetNodeId)),
    ui: { ...draft.graph.ui, businessGoal: copilotPrompt.value.trim() || draft.graph.ui.businessGoal },
  };
}

/** Manual format: Sugiyama-style layout shared with Workflow editor. */
function applyAutoLayout() {
  const blueprint = currentBlueprint.value;
  if (!blueprint.id || !blueprint.nodes.length) {
    showToast("当前没有可格式化的画布节点。", "error");
    return;
  }

  const draft = blueprintDrafts.get(blueprint.id);
  const graph: WorkflowGraphDraft = draft
    ? graphFromBlueprint(draft, blueprint)
    : {
        schemaVersion: "workflow-graph.v1",
        nodes: blueprint.nodes.map((node) => ({
          id: node.id,
          type: node.graphType,
          label: node.title,
          position: { x: node.x, y: node.y },
          ports: [],
          data: {},
          ui: {},
        })),
        edges: blueprint.connections.map((conn, index) => ({
          id: `layout-${conn.from}-${conn.to}-${index}`,
          sourceNodeId: conn.from,
          targetNodeId: conn.to,
          sourcePort: "out",
          targetPort: "in",
          data: {},
          ui: {},
        })),
        viewport: { x: 0, y: 0, zoom: 1 },
        ui: {},
      };

  const laidOut = autoLayoutWorkflowGraph(graph);
  const positionById = new Map(laidOut.nodes.map((node) => [node.id, node.position]));
  blueprint.nodes = blueprint.nodes.map((node) => {
    const position = positionById.get(node.id);
    if (!position) return node;
    return { ...node, x: position.x, y: position.y };
  });

  // Keep list entry in sync (currentBlueprint is a find() reference, but reassignment is safer).
  const index = blueprints.value.findIndex((item) => item.id === blueprint.id);
  if (index >= 0) {
    blueprints.value.splice(index, 1, { ...blueprint, nodes: blueprint.nodes });
  }

  nextTick(() => requestAnimationFrame(fitCanvasToVisibleArea));
  showToast("已格式化画布布局。", "success");
}

async function saveDraft(showSuccess = true) {
  const workflow = currentWorkflow.value;
  const draft = blueprintDrafts.get(currentBlueprint.value.id);
  if (!workflow || !draft) {
    if (showSuccess) showToast("请先生成正式 Workflow Draft，再保存画布。", "error");
    return undefined;
  }
  try {
    workflowStore.selectedWorkflowId = workflow.id;
    workflowStore.activeDraft = draft;
    const saved = await workflowStore.saveWorkflowDraft(workflow.id, {
      ...draft,
      graph: graphFromBlueprint(draft, currentBlueprint.value),
    });
    blueprintDrafts.set(workflow.id, saved);
    if (workflowStore.workflowDetails[workflow.id]) smart.adoptDraft(workflowStore.workflowDetails[workflow.id], saved);
    if (showSuccess) showToast("Workflow Draft 已保存；最新编译结果已失效，请重新检查。", "success");
    return saved;
  } catch (error) {
    showToast(apiErrorMessage(error, "保存 Workflow Draft 失败。"), "error");
    return undefined;
  }
}

async function validateSmartWorkflow() {
  const workflow = currentWorkflow.value;
  if (!workflow) {
    showToast("请先生成正式 Workflow Draft。", "error");
    return false;
  }
  const saved = await saveDraft(false);
  if (!saved) return false;
  try {
    const validation = await workflowStore.validateWorkflow(workflow.id);
    compilerIssues.value = validation.issues.map((issue) => ({
      title: issue.field || "编译问题",
      desc: issue.message,
    }));
    if (validation.valid) showToast("后端编译通过，可以执行模拟试运行。", "success");
    else showToast("后端编译发现 " + validation.issues.length + " 个问题。", "error");
    return validation.valid;
  } catch (error) {
    showToast(apiErrorMessage(error, "编译检查失败。"), "error");
    return false;
  }
}

function openInWorkflowEditor() {
  const workflow = currentWorkflow.value;
  if (!workflow) {
    showToast("请先生成正式 Workflow Draft，再进入普通编排。", "error");
    return;
  }
  workflowStore.selectedWorkflowId = workflow.id;
  void router.push({ name: "workflow", query: { edit: workflow.id } });
}

/**
 * P3.1 / P3.2: after formal publish, default-bind WORKFLOW capability to the
 * generate-session agentId (D12). Formal agent_capability_bindings only.
 */
async function bindPublishedWorkflowToSessionAgent(workflow: Workflow) {
  const draftAgentId = smart.generatedDraft?.graph?.ui?.agentId;
  const agentId = (
    smart.agentId ||
    selectedAgentId.value ||
    (typeof draftAgentId === "string" ? draftAgentId : "") ||
    ""
  ).trim();
  if (!agentId) {
    showToast(
      "Workflow 已发布，但未找到生成会话 Agent。请在 Agent 页将此 Workflow Capability 绑定后即可对话调用。",
      "info",
    );
    return;
  }
  let agent = agentStore.items.find((item) => item.id === agentId)
    || agentStore.pageItems.find((item) => item.id === agentId);
  if (!agent) {
    try {
      await agentStore.loadAgents({ workspaceId: workflow.workspaceId });
      agent = agentStore.items.find((item) => item.id === agentId);
    } catch {
      // fall through
    }
  }
  if (!agent) {
    showToast(
      `Workflow 已发布。请在 Agent 页将 Capability 绑定到 Agent（默认目标 ${agentId}）。`,
      "info",
    );
    return;
  }
  try {
    const existing = (agentStore.bindingsByAgent[agent.id] || []).find(
      (binding) => binding.capabilityId === workflow.id,
    );
    await agentStore.bindCapability(agent, workflow.id, {
      capabilityId: workflow.id,
      versionPolicy: "FOLLOW_ACTIVE",
      enabled: true,
      configOverrides: {},
      lockVersion: existing?.lockVersion ?? 0,
    });
    showToast(
      `Workflow 已发布并绑定到生成会话 Agent「${agent.name}」。可在 Console 对话台调用。`,
      "success",
    );
  } catch (error) {
    showToast(
      apiErrorMessage(
        error,
        `Workflow 已发布，但绑定 Agent 失败。请在 Agent 页手动绑定 Capability（目标 ${agent.name}）。`,
      ),
      "error",
    );
  }
}

async function publishWorkflow() {
  const workflow = currentWorkflow.value;
  if (!workflow) {
    showToast("请先生成正式 Workflow Draft。", "error");
    return;
  }
  try {
    const readiness = await workflowStore.loadWorkflowReadiness(workflow.id);
    if (!readiness.canPublish) {
      showToast("当前草稿尚未满足发布条件，请先完成编译和成功试运行。", "error");
      return;
    }
    const published = await workflowStore.publishWorkflow(workflow.id);
    const draft = blueprintDrafts.get(workflow.id);
    if (draft) registerBlueprint(published.workflow, draft, currentBlueprint.value.aiScore);
    // P3.2: productize post-publish bind to session agentId (not auto-publish).
    await bindPublishedWorkflowToSessionAgent(published.workflow);
  } catch (error) {
    showToast(apiErrorMessage(error, "发布 Workflow 失败。"), "error");
  }
}

function deleteNode(nodeId: string) {
  if (currentBlueprint.value.nodes.length <= 2) {
    showToast("正式草稿至少保留 Start 和 End 节点。", "error");
    return;
  }
  currentBlueprint.value.connections = currentBlueprint.value.connections.filter((conn) => conn.from !== nodeId && conn.to !== nodeId);
  currentBlueprint.value.nodes = currentBlueprint.value.nodes.filter((node) => node.id !== nodeId);
  selectedNodeId.value = currentBlueprint.value.nodes[0]?.id || "";
}

function startCanvasPan(event: PointerEvent) {
  if (event.button !== 0) return;
  const target = event.target instanceof HTMLElement ? event.target : undefined;
  if (target?.closest(".smart-canvas-node, button, input, textarea, select, a")) return;
  canvasPanning.value = {
    active: true,
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    originX: canvasPan.value.x,
    originY: canvasPan.value.y,
  };
  (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
}

function moveCanvasPan(event: PointerEvent) {
  const pan = canvasPanning.value;
  if (!pan.active || pan.pointerId !== event.pointerId) return;
  canvasPan.value = {
    x: pan.originX + event.clientX - pan.startX,
    y: pan.originY + event.clientY - pan.startY,
  };
}

function endCanvasPan(event: PointerEvent) {
  const pan = canvasPanning.value;
  if (!pan.active || pan.pointerId !== event.pointerId) return;
  (event.currentTarget as HTMLElement).releasePointerCapture?.(event.pointerId);
  canvasPanning.value = { active: false, pointerId: -1, startX: 0, startY: 0, originX: 0, originY: 0 };
}

function zoomIn() {
  canvasZoom.value = Math.min(1.5, Number((canvasZoom.value + 0.1).toFixed(1)));
}

function zoomOut() {
  canvasZoom.value = Math.max(0.35, Number((canvasZoom.value - 0.1).toFixed(1)));
}

function resetCanvas() {
  fitCanvasToVisibleArea();
}

function syncViewportMode() {
  isNarrowViewport.value = window.innerWidth <= canvasDesktopMinWidth;
}

function handleViewportResize() {
  syncViewportMode();
  fitCanvasToVisibleArea();
}

async function runSandboxTrial() {
  sandboxError.value = "";
  let input: Record<string, unknown>;
  try {
    const parsed = JSON.parse(sandbox.value.inputJson);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error("试运行参数必须是 JSON 对象。");
    input = parsed as Record<string, unknown>;
  } catch (error) {
    const detail = error instanceof Error ? error.message : "请输入合法 JSON。";
    sandboxError.value = "JSON 格式错误：" + detail;
    nextTick(() => sandboxInputRef.value?.focus());
    return;
  }
  const workflow = currentWorkflow.value;
  if (!workflow || !(await validateSmartWorkflow())) return;
  try {
    const execution = await workflowStore.trialRunWorkflow(workflow.id, input);
    if (execution.status !== "Success") throw new Error("试运行未成功。");
    closeSandbox();
    showToast("后端模拟试运行成功，Workflow 已满足发布前试运行条件。", "success");
  } catch (error) {
    sandboxError.value = apiErrorMessage(error, "模拟试运行失败。");
    nextTick(() => sandboxInputRef.value?.focus());
  }
}

async function loadPersistedBlueprints() {
  try {
    if (!workspaces.value.length) await workspaceStore.load();
    activeWorkspaceId.value = workspaceStore.activeWorkspaceId || workspaces.value[0]?.id || "";
    if (activeWorkspaceId.value) {
      try {
        await agentStore.loadAgents({ workspaceId: activeWorkspaceId.value });
      } catch {
        // Agent catalog optional for viewing existing drafts.
      }
      try {
        await modelConfigStore.loadModelConfigs();
      } catch {
        // Model catalog optional; agent.modelConfigId still gates send.
      }
    }
    if (!selectedAgentId.value) {
      selectedAgentId.value = agentStore.selectedAgentId || workspaceAgents.value[0]?.id || "";
    }
    syncGenerateContext();
    await workflowStore.loadWorkflowAssets();
    for (const summary of workflowStore.workflows) {
      try {
        const { draft } = await workflowStore.loadWorkflowDraft(summary.id);
        const generatedBy = draft.graph.ui.generatedBy;
        if (generatedBy !== "smart-dag.v1" && generatedBy !== "smart-dag.v2") continue;
        const detail = await workflowStore.loadWorkflow(summary.id);
        const confidence = typeof draft.graph.ui.confidence === "number" ? draft.graph.ui.confidence : 90;
        registerBlueprint(detail, draft, confidence, false);
      } catch {
        // One inaccessible or stale workflow must not hide the rest of the blueprint hub.
      }
    }
    if (smart.generatedWorkflow && smart.generatedDraft) {
      registerBlueprint(smart.generatedWorkflow, smart.generatedDraft, smart.confidence || 90, false);
    }
    if (blueprints.value.length) loadBlueprint(blueprints.value[0].id);
    else createBlankBlueprint(false);
  } catch {
    createBlankBlueprint(false);
  }
}

watch(selectedAgentId, () => {
  syncGenerateContext();
});

watch(activeWorkspaceId, async (workspaceId) => {
  if (!workspaceId) return;
  try {
    await agentStore.loadAgents({ workspaceId });
    selectedAgentId.value = workspaceAgents.value[0]?.id || "";
    syncGenerateContext();
  } catch {
    // ignore
  }
});

// Re-bind canvas when store draft SoT changes after each turn.
watch(
  () => smart.canvasEpoch,
  () => {
    if (smart.generatedWorkflow && smart.generatedDraft) {
      registerBlueprint(smart.generatedWorkflow, smart.generatedDraft, smart.confidence || 90, false);
      nextTick(() => requestAnimationFrame(fitCanvasToVisibleArea));
    }
  },
);

/**
 * P4.3: seed generate session from Workflow compile/trial failure CTA.
 * Query: workspaceId, workflowId, agentId?, reviseSource, compilationId?, feedbackSummary, feedbackIssues.
 */
async function applyReviseQuerySeed() {
  const q = route.query;
  const workspaceId = typeof q.workspaceId === "string" ? q.workspaceId.trim() : "";
  const workflowId = typeof q.workflowId === "string" ? q.workflowId.trim() : "";
  const reviseSource = typeof q.reviseSource === "string" ? q.reviseSource.trim() : "";
  if (!workspaceId || !workflowId || !reviseSource) return;

  const agentId =
    (typeof q.agentId === "string" && q.agentId.trim()) ||
    selectedAgentId.value ||
    "";
  activeWorkspaceId.value = workspaceId;
  if (agentId) selectedAgentId.value = agentId;

  let issues: FailureFeedbackIssue[] = [];
  if (typeof q.feedbackIssues === "string" && q.feedbackIssues) {
    try {
      const parsed = JSON.parse(q.feedbackIssues) as FailureFeedbackIssue[];
      if (Array.isArray(parsed)) issues = parsed;
    } catch {
      issues = [];
    }
  }
  const source =
    reviseSource === "trial" || reviseSource === "production" || reviseSource === "agent_run" || reviseSource === "guard"
      ? reviseSource
      : "compile";
  const summary =
    typeof q.feedbackSummary === "string" && q.feedbackSummary.trim()
      ? q.feedbackSummary.trim()
      : source === "compile"
        ? "编译失败，请按问题修订草稿"
        : "试运行失败，请按问题修订草稿";

  pendingFailureFeedback.value = {
    source,
    workflowId,
    compilationId: typeof q.compilationId === "string" ? q.compilationId : undefined,
    issues,
    rawSummary: summary,
  };
  copilotPrompt.value = `请根据${source === "compile" ? "编译" : "试运行"}失败问题修订流程草稿（只出新 Draft，不自动发布）。\n${summary}`;

  try {
    const { draft } = await workflowStore.loadWorkflowDraft(workflowId);
    const detail = workflowStore.workflowDetails[workflowId];
    if (detail && draft) {
      registerBlueprint(detail, draft, 80);
      smart.adoptDraft(detail, draft);
      const draftAgent = draft.graph?.ui?.agentId;
      if (typeof draftAgent === "string" && draftAgent && !selectedAgentId.value) {
        selectedAgentId.value = draftAgent;
      }
    }
    syncGenerateContext();
    if (selectedAgentId.value && agentHasUsableModel.value) {
      await smart.ensureSession({ workflowId });
      showToast("已载入失败回流上下文，发送即可修订草稿（不会自动发布）。", "info");
    } else {
      showToast("已载入失败回流上下文，请选择已配置模型的 Agent 后发送修订。", "info");
    }
  } catch {
    showToast("失败回流上下文已准备，加载草稿失败时可直接发送修订意图。", "info");
  }
}

onMounted(async () => {
  syncViewportMode();
  window.addEventListener("resize", handleViewportResize);
  await loadPersistedBlueprints();
  await applyReviseQuerySeed();
  requestAnimationFrame(fitCanvasToVisibleArea);
});

onBeforeUnmount(() => {
  if (toastTimer) window.clearTimeout(toastTimer);
  window.removeEventListener("resize", handleViewportResize);
});
</script>

<template>
  <div class="smart-orchestration-page">
    <div v-if="toast.show" class="smart-toast" :class="`is-${toast.tone}`" role="status" aria-live="polite">
      <i class="fa-solid" :class="toast.tone === 'error' ? 'fa-circle-exclamation' : 'fa-circle-check'" />
      <span>{{ toast.message }}</span>
      <button type="button" aria-label="关闭提示" title="关闭提示" @click="closeToast">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
    <div class="smart-screen-warning" role="note">
      <strong>智能编排画布需要桌面宽度</strong>
      <span>当前页面包含左右面板和大画布，建议在 1180px 以上窗口完成编辑。</span>
    </div>
    <div class="smart-narrow-blocker" role="note">
      <strong>当前宽度不支持编辑</strong>
      <span>请切换到 1180px 以上桌面窗口，或使用浏览器全屏后继续编辑智能编排。</span>
    </div>

    <main class="smart-orchestration-main" :inert="isNarrowViewport ? true : undefined" :aria-hidden="isNarrowViewport ? 'true' : undefined">
      <div class="smart-blueprint-toolbar" :class="{ 'is-compact': blueprintToolbarCompact }">
        <div class="smart-blueprint-toolbar-inner">
          <button v-if="blueprintToolbarCompact" class="smart-blueprint-toolbar-restore-button" type="button" aria-label="展开蓝图工具栏" title="展开蓝图工具栏" @click="blueprintToolbarCompact = false">
            <i class="fa-solid fa-up-right-and-down-left-from-center" />
            <span>
              <small>当前蓝图</small>
              <strong>{{ currentBlueprint.name }}</strong>
            </span>
            <span class="smart-status-badge" :class="getStatusClass(currentBlueprint.status)">{{ getStatusText(currentBlueprint.status) }}</span>
          </button>
          <div v-if="blueprintToolbarCompact" class="smart-blueprint-toolbar-compact-actions">
            <button type="button" aria-label="保存画布" title="保存画布" @click="saveDraft()">
              <i class="fa-regular fa-floppy-disk" />
            </button>
            <button type="button" aria-label="打开蓝图" title="打开蓝图" @click="openBlueprintPicker">
              <i class="fa-solid fa-folder-open" />
            </button>
          </div>
          <template v-else>
            <div class="smart-current-blueprint">
              <div>
                <span>当前蓝图</span>
                <strong>{{ currentBlueprint.name }}</strong>
                <small>{{ currentBlueprint.space }} · AI {{ currentBlueprint.aiScore }}%</small>
              </div>
              <div class="smart-current-blueprint-actions">
                <span class="smart-status-badge" :class="getStatusClass(currentBlueprint.status)">{{ getStatusText(currentBlueprint.status) }}</span>
                <button class="smart-blueprint-toolbar-compact-button" type="button" aria-label="缩小蓝图工具栏" title="缩小蓝图工具栏" @click="blueprintToolbarCompact = true">
                  <i class="fa-solid fa-down-left-and-up-right-to-center" />
                  <span>收起</span>
                </button>
              </div>
            </div>
            <div class="smart-toolbar-actions">
              <button class="smart-toolbar-button" type="button" @click="saveDraft()">
                <i class="fa-regular fa-floppy-disk" />
                保存画布
              </button>
              <button
                class="smart-toolbar-button"
                type="button"
                data-testid="toolbar-auto-layout"
                data-action="auto-layout-smart-canvas"
                title="按拓扑分层展开节点，消除堆叠与交叉"
                :disabled="!currentBlueprint.id || !currentBlueprint.nodes.length"
                @click="applyAutoLayout"
              >
                <i class="fa-solid fa-diagram-project" />
                格式化画布
              </button>
              <button class="smart-publish-button" type="button" :data-readiness="canPublishSmartDraft ? 'ready' : 'blocked'" @click="publishWorkflow">
                <i class="fa-solid fa-rocket" />
                发布上线
              </button>
              <button class="smart-toolbar-button" type="button" data-testid="toolbar-open-blueprint" @click="openBlueprintPicker">
                <i class="fa-solid fa-folder-open" />
                打开蓝图
              </button>
              <button class="smart-dark-button" type="button" data-testid="toolbar-create-blueprint" @click="createBlankBlueprint()">
                <i class="fa-solid fa-plus" />
                新建草稿
              </button>
              <button class="smart-icon-button" type="button" aria-label="编译诊断" title="编译诊断" @click="validateSmartWorkflow">
                <i class="fa-solid fa-shield-heart" />
              </button>
              <button class="smart-icon-button" type="button" aria-label="打开模拟试运行" title="模拟跑" @click="openSandbox">
                <i class="fa-solid fa-flask" />
              </button>
            </div>
          </template>
        </div>
      </div>

      <div
        id="canvas-container"
        ref="canvasContainerRef"
        class="smart-canvas-container grid-matrix-bg canvas-grabbable"
        :class="{ 'is-panning': canvasPanning.active }"
        @pointerdown="startCanvasPan"
        @pointermove="moveCanvasPan"
        @pointerup="endCanvasPan"
        @pointercancel="endCanvasPan"
        @wheel.prevent="$event.deltaY < 0 ? zoomIn() : zoomOut()"
      >
        <div class="smart-canvas-hint">
          <i class="fa-regular fa-hand" />
          <span>拖动画布 · 滚轮缩放</span>
        </div>
        <div
          id="canvas-workspace"
          class="smart-canvas-workspace"
          :key="canvasRenderKey"
          :style="{ transform: `translate(${canvasPan.x}px, ${canvasPan.y}px) scale(${canvasZoom})` }"
        >
          <svg id="canvas-svg" class="smart-canvas-svg">
            <g v-for="(conn, idx) in currentBlueprint.connections" :key="`${conn.from}-${conn.to}-${idx}`">
              <path :d="getConnectionPath(conn)" stroke="rgba(20, 184, 166, 0.05)" stroke-width="10" fill="none" />
              <path :d="getConnectionPath(conn)" stroke="#0d9488" stroke-width="2.5" fill="none" class="connection-path" />
            </g>
          </svg>

          <article
            v-for="(node, idx) in currentBlueprint.nodes"
            :id="node.id"
            :key="node.id"
            class="smart-canvas-node"
            :class="{ selected: selectedNodeId === node.id, 'is-ai-draft': node.isAiDraft }"
            :data-node-id="node.id"
            :data-node-idx="idx"
            :style="{ transform: `translate(${node.x}px, ${node.y}px)` }"
            @click.stop="selectedNodeId = node.id"
          >
            <header>
              <span class="smart-node-type" :class="getNodeTypeClass(node.theme)">{{ node.type }}</span>
              <span v-if="node.isAiDraft" class="smart-ai-chip"><i class="fa-solid fa-sparkles" />AI Draft</span>
              <button type="button" aria-label="删除此节点" title="删除此节点" @click.stop="deleteNode(node.id)">
                <i class="fa-solid fa-trash-can" />
              </button>
            </header>
            <div>
              <strong>{{ node.title }}</strong>
              <small>{{ node.desc }}</small>
              <p v-if="node.aiReason">{{ node.aiReason }}</p>
            </div>
            <i v-if="node.type !== 'END'" class="smart-node-port output" />
            <i v-if="node.type !== 'START'" class="smart-node-port input" />
          </article>
        </div>
      </div>

      <div v-if="hasAiDraft" class="smart-ai-draft-banner">
        <div>
          <i class="fa-solid fa-wand-magic-sparkles" />
          <div>
            <strong>AI 已生成正式 Workflow Draft</strong>
            <span>草稿节点已持久化为 workflow.graph.v1。可继续微调、编译、试运行或进入普通编排。</span>
          </div>
        </div>
        <button type="button" @click="discardAiDraft">废弃草稿</button>
        <button type="button" class="primary" @click="acceptAiDraft">接受草稿</button>
      </div>

      <div class="smart-zoom-dock">
        <button type="button" aria-label="放大画布" title="放大画布" @click="zoomIn"><i class="fa-solid fa-plus" /></button>
        <span>{{ Math.round(canvasZoom * 100) }}%</span>
        <button type="button" aria-label="缩小画布" title="缩小画布" @click="zoomOut"><i class="fa-solid fa-minus" /></button>
        <i />
        <button type="button" aria-label="适配画布" title="适配画布" @click="resetCanvas"><i class="fa-solid fa-expand" /></button>
        <i />
        <button type="button" aria-label="切换专注模式" title="专注模式" :class="{ active: focusMode }" @click="focusMode = !focusMode"><i class="fa-solid fa-eye-slash" /></button>
      </div>

      <aside class="smart-copilot-panel glass-panel" :class="{ collapsed: leftPanelCollapsed, dimmed: focusMode }">
        <button v-if="leftPanelCollapsed" class="smart-collapse-button" type="button" aria-label="展开 AI Copilot 面板" title="展开 AI Copilot 面板" @click="leftPanelCollapsed = false">
          <i class="fa-solid fa-wand-magic-sparkles" />
        </button>
        <template v-else>
          <header>
            <span><i class="fa-solid fa-sparkles" />AI Copilot</span>
            <button type="button" aria-label="折叠 AI Copilot 面板" title="折叠 AI Copilot 面板" @click="leftPanelCollapsed = true"><i class="fa-solid fa-angles-left" /></button>
          </header>
          <h2>多轮智能生成</h2>
          <p>选择 Workspace 与 Agent 后，用自然语言多轮修订流程；每轮成功后画布按最新 Draft 刷新。</p>
          <p class="smart-publish-bind-hint" data-testid="smart-publish-bind-hint">
            生成满意 ≠ Agent 已可用：须完成编译、试运行、发布，并绑定到 Agent 后，Console 对话台才能调用本 Workflow。
          </p>

          <div class="smart-draft-summary">
            <span>当前草稿</span>
            <b>{{ currentBlueprint.name }}</b>
            <small>只保存 Workflow Draft，不自动发布；正式 binding 仅在 publish 之后（默认绑回生成会话 Agent）。{{ smart.sessionStatus ? `会话 ${smart.sessionStatus}` : "尚未开始会话" }}</small>
          </div>

          <label>
            <span>业务空间</span>
            <select v-model="activeWorkspaceId" :disabled="aiStatus.isGenerating || smart.generating">
              <option value="" disabled>请选择业务空间</option>
              <option v-for="workspace in workspaces" :key="workspace.id" :value="workspace.id">
                {{ workspace.displayName || workspace.name }}
              </option>
            </select>
          </label>

          <label>
            <span>生成 Agent</span>
            <select v-model="selectedAgentId" :disabled="aiStatus.isGenerating || smart.generating || !activeWorkspaceId">
              <option value="" disabled>请选择 Agent</option>
              <option v-for="agent in workspaceAgents" :key="agent.id" :value="agent.id">
                {{ agent.name }}{{ agent.modelConfigId ? "" : "（未绑定模型）" }}
              </option>
            </select>
            <em v-if="selectedAgentId && !agentHasUsableModel" class="smart-agent-model-hint">
              当前 Agent 未配置可用模型，请先在 Agent 设置中绑定 Model Config。
            </em>
          </label>

          <section v-if="turnHistory.length" class="smart-turn-history" aria-label="生成轮次历史">
            <strong>多轮对话（生成专用）</strong>
            <article v-for="turn in turnHistory" :key="turn.turnId" class="smart-turn-item" :class="{ 'is-failed': !turn.guardOk }">
              <span class="smart-turn-role">你 · #{{ turn.turnIndex }}</span>
              <p>{{ turn.userMessage }}</p>
              <span v-if="turn.assistantMessage" class="smart-turn-role">助手</span>
              <p v-if="turn.assistantMessage">{{ turn.assistantMessage }}</p>
              <small v-if="turn.draftVersion">draftVersion={{ turn.draftVersion }} · {{ turn.status }}</small>
              <small v-else-if="turn.errorCode">{{ turn.errorCode }}</small>
            </article>
          </section>

          <div v-if="smart.lastGuardReport && !smart.lastGuardReport.ok" class="smart-guard-report" role="alert">
            <strong>Guard 拒绝</strong>
            <p v-for="(v, idx) in smart.lastGuardReport.violations" :key="idx">{{ v.code }}：{{ v.message }}</p>
          </div>

          <!-- ZKL-56 DEF-02 / UX-04: persistent recovery card (not toast-only). -->
          <section
            v-if="smart.lastFailure"
            class="smart-recovery-card"
            role="alert"
            data-testid="smart-dag-recovery-card"
            aria-live="assertive"
          >
            <header>
              <strong>本轮生成未完成</strong>
              <span class="smart-recovery-stage">{{ smart.lastFailure.stage || "UNKNOWN" }}</span>
            </header>
            <p class="smart-recovery-message">{{ smart.lastFailure.message }}</p>
            <dl class="smart-recovery-meta">
              <div v-if="smart.lastFailure.code"><dt>错误码</dt><dd>{{ smart.lastFailure.code }}</dd></div>
              <div v-if="smart.lastFailure.sessionStatus || smart.sessionStatus">
                <dt>会话</dt>
                <dd>{{ smart.lastFailure.sessionStatus || smart.sessionStatus || "—" }}</dd>
              </div>
              <div v-if="smart.lastFailure.requestId"><dt>requestId</dt><dd>{{ smart.lastFailure.requestId }}</dd></div>
              <div v-if="smart.lastFailure.traceId"><dt>traceId</dt><dd>{{ smart.lastFailure.traceId }}</dd></div>
            </dl>
            <p class="smart-recovery-hint">
              上一合法 Draft 与输入已保留；失败不会自动发布。生成中不支持执行中取消。
            </p>
            <div class="smart-recovery-actions">
              <button
                v-if="smart.recoveryActions.retry"
                type="button"
                class="primary"
                data-testid="smart-dag-retry"
                :disabled="smart.generating || aiStatus.isGenerating"
                @click="retryLastGenerateTurn"
              >
                重试本轮
              </button>
              <button
                v-if="smart.recoveryActions.close"
                type="button"
                data-testid="smart-dag-close-session"
                :disabled="smart.generating || aiStatus.isGenerating"
                @click="closeGenerateSessionWithConfirm"
              >
                关闭会话
              </button>
              <button
                v-if="smart.recoveryActions.fixConfig"
                type="button"
                data-testid="smart-dag-fix-config"
                @click="showToast('请检查 Agent 模型绑定、工具目录或网络后重试。', 'info')"
              >
                修复配置
              </button>
              <button
                v-if="smart.recoveryActions.createNew"
                type="button"
                class="primary"
                data-testid="smart-dag-new-session"
                :disabled="smart.generating"
                @click="startNewGenerateSession"
              >
                新建会话
              </button>
            </div>
          </section>

          <div v-if="smart.missingCapabilities.length" class="smart-missing-capabilities">
            <strong>能力缺口</strong>
            <p v-for="cap in smart.missingCapabilities" :key="cap.id">{{ cap.name }} — {{ cap.reason }}</p>
          </div>

          <label>
            <span>本轮自然语言意图</span>
            <textarea
              v-model="copilotPrompt"
              rows="5"
              placeholder="输入业务目标或修订意图，例如：加审批节点、换支付查询工具。"
              :disabled="(!canSendGenerateTurn && !agentHasUsableModel) || smart.sessionStatus === 'CLOSED'"
            />
            <em>{{ copilotPrompt.length }} 字</em>
          </label>

          <div v-if="aiStatus.isGenerating" class="smart-ai-steps">
            <strong>AI 正在推理编排结构...</strong>
            <span v-for="(step, idx) in aiSteps" :key="step" :class="{ active: aiStatus.activeStep >= idx }">{{ idx + 1 }}. {{ step }}</span>
          </div>

          <div class="smart-copilot-actions">
            <button type="button" @click="copilotPrompt = ''">清空输入</button>
            <button type="button" @click="openBlueprintPicker">打开蓝图</button>
            <button
              type="button"
              class="primary"
              :disabled="!canSendGenerateTurn || !copilotPrompt.trim() || smart.sessionStatus === 'CLOSED'"
              :title="!agentHasUsableModel ? '请先为 Agent 配置模型' : smart.sessionStatus === 'CLOSED' ? '会话已关闭，请新建会话' : '发送本轮生成'"
              @click="generateDraft"
            >
              <i class="fa-solid fa-wand-magic-sparkles" />
              {{ aiStatus.isGenerating || smart.generating ? "AI 生成中..." : smart.generatedDraft ? "发送本轮修订" : "开始多轮生成" }}
            </button>
            <button
              v-if="draftGenerated"
              type="button"
              class="primary"
              :disabled="smart.generating"
              @click="finishGeneration"
            >
              完成生成
            </button>
            <button v-if="draftGenerated" type="button" @click="openInWorkflowEditor">进入普通编排</button>
          </div>
        </template>
      </aside>

      <aside class="smart-property-panel glass-panel" :class="{ collapsed: rightPanelCollapsed, dimmed: focusMode }">
        <button v-if="rightPanelCollapsed" class="smart-collapse-button" type="button" aria-label="展开属性面板" title="展开属性面板" @click="rightPanelCollapsed = false">
          <i class="fa-solid fa-sliders" />
        </button>
        <template v-else>
          <header>
            <div>
              <span><i class="fa-solid fa-sliders" />属性面板</span>
              <h2>{{ selectedNode?.title || "未选择节点" }}</h2>
            </div>
            <button type="button" aria-label="折叠属性面板" title="折叠属性面板" @click="rightPanelCollapsed = true"><i class="fa-solid fa-angles-right" /></button>
          </header>

          <template v-if="selectedNode">
            <label>
              <span>节点名称</span>
              <input v-model="selectedNode.title" type="text">
            </label>
            <label>
              <span>说明备注</span>
              <textarea v-model="selectedNode.desc" rows="2" />
            </label>
            <div v-if="selectedNode.aiReason" class="smart-ai-reason">
              <strong><i class="fa-solid fa-shield-halved" />AI 生成说明</strong>
              <p>{{ selectedNode.aiReason }}</p>
            </div>
            <div class="smart-schema-card">
              <span>输入端口 Schema (JSON)</span>
              <code>{{ getParameterSchema(selectedNode.type) }}</code>
            </div>
          </template>
          <div v-else class="smart-panel-empty">在画布上点击任意节点以修改其属性</div>

          <section class="smart-compiler-panel">
            <div>
              <strong><i class="fa-solid fa-circle-exclamation" />编译状态</strong>
              <span>{{ compilerIssues.length }} 条提示</span>
            </div>
            <article v-for="issue in compilerIssues" :key="issue.title">
              <strong>{{ issue.title }}</strong>
              <small>{{ issue.desc }}</small>
            </article>
            <div v-if="!compilerIssues.length" class="smart-compile-ok">
              <i class="fa-solid fa-check" />
              <strong>暂无后端编译问题</strong>
              <small>点击编译诊断后，以 Workflow v1 Compiler 和 Readiness 返回结果为准。</small>
            </div>
          </section>

          <button class="smart-save-draft-button" type="button" @click="saveDraft()">保存 Workflow Draft</button>
        </template>
      </aside>
    </main>

    <div v-if="blueprintPickerOpen" class="smart-modal-backdrop" @click.self="closeBlueprintPicker()">
      <div
        ref="blueprintModalRef"
        data-testid="blueprint-picker-modal"
        class="blueprint-picker-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="blueprint-picker-title"
        tabindex="-1"
        @keydown="handleBlueprintModalKeydown"
      >
        <header>
          <div>
            <span>Blueprint Hub</span>
            <h2 id="blueprint-picker-title">打开智能蓝图</h2>
            <p>智能编排默认直达画布，蓝图库仅用于切换已有流程、查看待复核资产与继续协作。</p>
          </div>
          <button type="button" aria-label="关闭蓝图库" title="关闭蓝图库" @click="closeBlueprintPicker()"><i class="fa-solid fa-xmark" /></button>
        </header>

        <section>
          <div class="smart-picker-toolbar">
            <label>
              <i class="fa-solid fa-magnifying-glass" />
              <input ref="blueprintSearchInputRef" v-model="listQuery" type="text" placeholder="搜索蓝图名称 / Agent / AI 策略...">
            </label>
            <div class="smart-status-filter" role="radiogroup" aria-label="蓝图状态筛选">
              <button type="button" role="radio" :aria-checked="statusFilter === 'ALL'" :class="{ active: statusFilter === 'ALL' }" @click="setStatusFilter('ALL')">全部状态</button>
              <button type="button" role="radio" :aria-checked="statusFilter === 'published'" :class="{ active: statusFilter === 'published' }" @click="setStatusFilter('published')">已发布</button>
              <button type="button" role="radio" :aria-checked="statusFilter === 'review'" :class="{ active: statusFilter === 'review' }" @click="setStatusFilter('review')">待复核</button>
              <button type="button" role="radio" :aria-checked="statusFilter === 'draft'" :class="{ active: statusFilter === 'draft' }" @click="setStatusFilter('draft')">AI 草稿</button>
            </div>
            <span>共 {{ filteredBlueprintList.length }} 条蓝图</span>
            <span>平均 AI 命中 {{ averageAiScore }}%</span>
          </div>

          <div class="smart-blueprint-grid">
            <article v-for="workflow in paginatedBlueprintList" :key="workflow.id" :class="{ selected: selectedBlueprintId === workflow.id }">
              <div>
                <strong>{{ workflow.name }}</strong>
                <span class="smart-status-badge" :class="getStatusClass(workflow.status)">{{ getStatusText(workflow.status) }}</span>
              </div>
              <p>{{ workflow.description }}</p>
              <div class="smart-blueprint-chips">
                <span :class="getAutomationClass(workflow.automationMode)">{{ workflow.automationMode }}</span>
                <span>{{ workflow.agent }}</span>
                <span>{{ workflow.space }}</span>
              </div>
              <div class="smart-blueprint-stats">
                <span><small>节点数</small><b>{{ workflow.nodes.length }}</b></span>
                <span><small>连线数</small><b>{{ workflow.connections.length }}</b></span>
                <span><small>AI 评分</small><b>{{ workflow.aiScore }}%</b></span>
              </div>
              <button type="button" @click="loadBlueprint(workflow.id)">{{ selectedBlueprintId === workflow.id ? "继续编辑" : "打开画布" }}</button>
            </article>
            <div v-if="!paginatedBlueprintList.length" class="smart-picker-empty">
              <i class="fa-solid fa-wand-magic-sparkles" />
              <strong>没有匹配到智能编排蓝图</strong>
              <span>换个关键词试试，或者清空筛选条件。</span>
            </div>
          </div>
        </section>

        <footer>
          <span v-if="filteredBlueprintList.length">显示第 {{ paginationStart }} - {{ paginationEnd }} 条，共 {{ filteredBlueprintList.length }} 条</span>
          <span v-else>当前没有可展示的数据</span>
          <div>
            <button type="button" :disabled="listPage === 1" @click="listPage -= 1">上一页</button>
            <button v-for="page in listPageNumbers" :key="page" type="button" :class="{ active: listPage === page }" @click="listPage = page">{{ page }}</button>
            <button type="button" :disabled="listPage === totalListPages" @click="listPage += 1">下一页</button>
          </div>
        </footer>
      </div>
    </div>

    <div v-if="sandbox.show" class="smart-modal-backdrop" @click.self="closeSandbox()">
      <div
        ref="sandboxModalRef"
        class="smart-trial-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="smart-trial-title"
        tabindex="-1"
        @keydown="handleSandboxModalKeydown"
      >
        <header>
          <i class="fa-solid fa-flask" />
          <div>
            <h2 id="smart-trial-title">智能编排模拟试运行</h2>
            <p>提供入参来执行测试沙箱，验证 AI 补全后的链路无误</p>
          </div>
          <button type="button" aria-label="关闭模拟试运行" title="关闭模拟试运行" @click="closeSandbox()"><i class="fa-solid fa-xmark" /></button>
        </header>
        <label>
          <span>测试实例流 ID</span>
          <input :value="currentBlueprint.id" readonly>
        </label>
        <label>
          <span>测试参数 (JSON Schema)</span>
          <textarea ref="sandboxInputRef" v-model="sandbox.inputJson" rows="4" :aria-invalid="Boolean(sandboxError)" :aria-describedby="sandboxError ? 'sandbox-json-error' : undefined" />
        </label>
        <p v-if="sandboxError" id="sandbox-json-error" class="smart-trial-error" role="alert">{{ sandboxError }}</p>
        <footer>
          <button type="button" @click="closeSandbox()">取消</button>
          <button type="button" class="primary" @click="runSandboxTrial">
            <i class="fa-solid fa-circle-play" />
            运行沙箱
          </button>
        </footer>
      </div>
    </div>
  </div>
</template>

<style scoped>
.smart-orchestration-page {
  position: relative;
  height: calc(100vh - 64px);
  min-height: 720px;
  margin: -24px;
  overflow: hidden;
  color: #1e293b;
  background: #fafbfd;
  font-family: Inter, "Noto Sans SC", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}

.smart-orchestration-page button,
.smart-orchestration-page input,
.smart-orchestration-page textarea {
  font-family: inherit;
}

.smart-orchestration-page button {
  cursor: pointer;
}

.smart-orchestration-page button:disabled {
  cursor: not-allowed;
}

.smart-orchestration-page button:focus-visible,
.smart-orchestration-page input:focus-visible,
.smart-orchestration-page textarea:focus-visible,
.blueprint-picker-modal:focus-visible,
.smart-trial-modal:focus-visible {
  outline: 3px solid rgb(20 184 166 / 0.45);
  outline-offset: 2px;
}

.smart-orchestration-main {
  position: relative;
  display: flex;
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.grid-matrix-bg {
  background-color: #fafbfd;
  background-image: radial-gradient(#cbd5e1 1.2px, transparent 1.2px);
  background-size: 20px 20px;
}

.glass-panel {
  background: rgb(255 255 255 / 0.88);
  backdrop-filter: blur(20px);
}

.canvas-grabbable {
  cursor: grab;
  touch-action: none;
}

.canvas-grabbable:active,
.canvas-grabbable.is-panning {
  cursor: grabbing;
}

.smart-blueprint-toolbar {
  position: absolute;
  top: 24px;
  right: 388px;
  left: 336px;
  z-index: 30;
  pointer-events: none;
}

.smart-blueprint-toolbar.is-compact {
  right: auto;
  left: 50%;
  width: min(408px, calc(100vw - 820px));
  min-width: 360px;
  transform: translateX(-50%);
}

.smart-blueprint-toolbar-inner {
  max-width: 760px;
  margin: 0 auto;
  padding: 8px;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  background: rgb(255 255 255 / 0.92);
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.08), 0 1px 10px rgb(15 23 42 / 0.02);
  backdrop-filter: blur(16px);
  pointer-events: auto;
}

.smart-blueprint-toolbar.is-compact .smart-blueprint-toolbar-inner {
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 6px;
  border-radius: 999px;
}

.smart-current-blueprint {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 16px;
  border: 1px solid rgb(226 232 240 / 0.8);
  border-radius: 12px;
  background: #f8fafc;
}

.smart-current-blueprint-actions {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
}

.smart-current-blueprint span:first-child,
.smart-blueprint-toolbar-restore-button small,
.smart-copilot-panel header span,
.smart-property-panel header span,
.blueprint-picker-modal header span {
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  line-height: 1;
  text-transform: uppercase;
}

.smart-current-blueprint strong {
  display: block;
  margin-top: 5px;
  color: #1e293b;
  font-size: 12px;
  font-weight: 700;
  line-height: 1.25;
}

.smart-current-blueprint small {
  display: block;
  margin-top: 4px;
  color: #64748b;
  font-size: 10px;
  line-height: 1.2;
}

.smart-toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 8px;
}

.smart-toolbar-button,
.smart-dark-button,
.smart-publish-button,
.smart-icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  min-width: 44px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  transition: background-color 0.16s ease, color 0.16s ease, border-color 0.16s ease, transform 0.16s ease;
}

.smart-toolbar-button {
  padding: 0 14px;
  border: 1px solid #e2e8f0;
  background: #fff;
  color: #334155;
}

.smart-toolbar-button i,
.smart-icon-button i {
  color: #64748b;
  font-size: 12px;
}

.smart-toolbar-button:hover,
.smart-icon-button:hover {
  border-color: #99f6e4;
  background: #f0fdfa;
  color: #0f172a;
  transform: translateY(-1px);
}

.smart-dark-button,
.smart-publish-button {
  padding: 0 16px;
  border: 0;
  color: #fff;
}

.smart-dark-button {
  background: #020617;
}

.smart-dark-button:hover {
  background: #1e293b;
}

.smart-publish-button {
  background: #059669;
  box-shadow: 0 1px 2px rgb(5 150 105 / 0.1);
}

.smart-publish-button[data-readiness="blocked"] {
  background: #64748b;
}

.smart-publish-button:hover {
  background: #047857;
}

.smart-icon-button {
  width: 44px;
  padding: 0;
  border: 1px solid #e2e8f0;
  background: #fff;
  color: #475569;
}

.smart-blueprint-toolbar-compact-button,
.smart-blueprint-toolbar-restore-button,
.smart-blueprint-toolbar-compact-actions button {
  display: inline-flex;
  min-width: 44px;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid #e2e8f0;
  background: #fff;
  color: #475569;
  transition: background-color 0.16s ease, border-color 0.16s ease, color 0.16s ease, transform 0.16s ease;
}

.smart-blueprint-toolbar-compact-button {
  gap: 6px;
  padding: 0 12px;
  border-radius: 12px;
  font-size: 11px;
  font-weight: 700;
}

.smart-blueprint-toolbar-restore-button {
  flex: 1 1 auto;
  min-width: 0;
  gap: 10px;
  padding: 0 10px 0 14px;
  border-radius: 999px;
  text-align: left;
}

.smart-blueprint-toolbar-compact-actions {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 4px;
}

.smart-blueprint-toolbar-compact-actions button {
  width: 44px;
  padding: 0;
  border-radius: 999px;
}

.smart-blueprint-toolbar-restore-button > span:not(.smart-status-badge) {
  min-width: 0;
  flex: 1 1 auto;
}

.smart-blueprint-toolbar-restore-button small,
.smart-blueprint-toolbar-restore-button strong {
  display: block;
}

.smart-blueprint-toolbar-restore-button strong {
  overflow: hidden;
  color: #1e293b;
  font-size: 12px;
  line-height: 1.25;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.smart-blueprint-toolbar-compact-button:hover,
.smart-blueprint-toolbar-restore-button:hover,
.smart-blueprint-toolbar-compact-actions button:hover {
  border-color: #99f6e4;
  background: #f0fdfa;
  color: #0f172a;
  transform: translateY(-1px);
}

.smart-canvas-container {
  position: absolute;
  inset: 0;
  z-index: 10;
  overflow: hidden;
  user-select: none;
}

.smart-canvas-hint {
  position: absolute;
  z-index: 35;
  right: 388px;
  bottom: 112px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: rgb(255 255 255 / 0.88);
  color: #64748b;
  box-shadow: 0 10px 24px -18px rgb(15 23 42 / 0.2);
  font-size: 11px;
  font-weight: 700;
  pointer-events: none;
}

.smart-canvas-workspace {
  position: absolute;
  inset: 0;
  width: 1800px;
  height: 900px;
  transform-origin: top left;
  transition: transform 0.08s ease;
}

.smart-canvas-svg {
  position: absolute;
  inset: 0;
  z-index: 10;
  width: 100%;
  height: 100%;
  pointer-events: none;
}

.connection-path {
  stroke-dasharray: 6;
  animation: smart-flow 35s linear infinite;
}

@keyframes smart-flow {
  from {
    stroke-dashoffset: 400;
  }
  to {
    stroke-dashoffset: 0;
  }
}

.smart-canvas-node {
  position: absolute;
  z-index: 20;
  width: 220px;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  background: #fff;
  box-shadow: 0 10px 30px -10px rgb(0 0 0 / 0.04), 0 1px 3px rgb(0 0 0 / 0.02);
  cursor: grab;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;
}

.smart-canvas-node:hover {
  box-shadow: 0 20px 25px -5px rgb(13 148 136 / 0.06), 0 8px 10px -6px rgb(13 148 136 / 0.03);
}

.smart-canvas-node.selected {
  border-color: transparent;
  box-shadow: 0 0 0 3px rgb(13 148 136 / 0.15), 0 10px 20px -10px rgb(13 148 136 / 0.12);
}

.smart-canvas-node.is-ai-draft {
  border-style: dashed;
  border-color: #a5b4fc;
  background: rgb(238 242 255 / 0.36);
}

.smart-canvas-node header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px;
  border-bottom: 1px solid #f8fafc;
}

.smart-canvas-node header button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #64748b;
  font-size: 10px;
}

.smart-canvas-node header button:hover {
  background: #f1f5f9;
  color: #e11d48;
}

.smart-canvas-node > div {
  padding: 16px;
}

.smart-canvas-node strong,
.smart-canvas-node small {
  display: block;
}

.smart-canvas-node strong {
  color: #1e293b;
  font-size: 12px;
  font-weight: 700;
  line-height: 1.3;
}

.smart-canvas-node small {
  margin-top: 3px;
  overflow: hidden;
  color: #64748b;
  font-size: 11px;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.smart-canvas-node p {
  display: -webkit-box;
  margin: 8px 0 0;
  overflow: hidden;
  color: #4f46e5;
  font-size: 10px;
  line-height: 1.4;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.smart-node-type,
.smart-ai-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  text-transform: uppercase;
}

.smart-node-type.is-start {
  border-color: rgb(153 246 228 / 0.5);
  background: #f0fdfa;
  color: #0f766e;
}

.smart-node-type.is-tool {
  border-color: rgb(199 210 254 / 0.5);
  background: #eef2ff;
  color: #4338ca;
}

.smart-node-type.is-condition {
  border-color: rgb(253 230 138 / 0.5);
  background: #fffbeb;
  color: #b45309;
}

.smart-node-type.is-approval {
  border-color: rgb(186 230 253 / 0.5);
  background: #f0f9ff;
  color: #0369a1;
}

.smart-node-type.is-transform {
  border-color: rgb(167 243 208 / 0.5);
  background: #ecfdf5;
  color: #047857;
}

.smart-node-type.is-end {
  border-color: rgb(254 205 211 / 0.5);
  background: #fff1f2;
  color: #be123c;
}

.smart-ai-chip {
  background: #eef2ff;
  color: #4338ca;
  font-size: 8px;
  border-color: #e0e7ff;
  text-transform: none;
}

.smart-node-port {
  position: absolute;
  top: 50%;
  width: 12px;
  height: 12px;
  border: 2px solid #0d9488;
  border-radius: 999px;
  background: #fff;
  transform: translateY(-50%);
}

.smart-node-port.output {
  right: -6px;
}

.smart-node-port.input {
  left: -6px;
  border-color: #cbd5e1;
}

.smart-copilot-panel,
.smart-property-panel {
  position: absolute;
  z-index: 20;
  top: 24px;
  bottom: 96px;
  overflow: auto;
  border: 1px solid rgb(226 232 240 / 0.5);
  border-radius: 24px;
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.08), 0 1px 10px rgb(15 23 42 / 0.02);
  transition: opacity 0.18s ease, width 0.18s ease;
}

.smart-copilot-panel {
  left: 24px;
  width: 292px;
  padding: 20px;
}

.smart-property-panel {
  right: 24px;
  width: 340px;
  padding: 20px;
}

.smart-copilot-panel.collapsed,
.smart-property-panel.collapsed {
  top: 50%;
  bottom: auto;
  width: 56px;
  height: 56px;
  padding: 0;
  transform: translateY(-50%);
}

.smart-copilot-panel.dimmed,
.smart-property-panel.dimmed {
  opacity: 0.2;
}

.smart-copilot-panel.dimmed:hover,
.smart-property-panel.dimmed:hover {
  opacity: 1;
}

.smart-collapse-button {
  display: flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  margin: 5px;
  border: 0;
  border-radius: 16px;
  background: #e0e7ff;
  color: #4338ca;
}

.smart-copilot-panel header,
.smart-property-panel header,
.smart-trial-modal header,
.blueprint-picker-modal header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.smart-copilot-panel header button,
.smart-property-panel header button,
.blueprint-picker-modal header button,
.smart-trial-modal header button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #64748b;
}

.smart-copilot-panel header button:hover,
.smart-property-panel header button:hover,
.blueprint-picker-modal header button:hover,
.smart-trial-modal header button:hover {
  background: #f1f5f9;
  color: #475569;
}

.smart-copilot-panel h2,
.smart-property-panel h2,
.blueprint-picker-modal h2,
.smart-trial-modal h2 {
  margin: 6px 0 0;
  color: #1e293b;
  font-size: 16px;
  font-weight: 700;
  line-height: 1.25;
}

.smart-copilot-panel p,
.blueprint-picker-modal p,
.smart-trial-modal p {
  margin: 4px 0 0;
  color: #64748b;
  font-size: 11px;
  line-height: 1.55;
}

.smart-publish-bind-hint {
  margin-top: 10px !important;
  padding: 10px 12px;
  border: 1px solid #fde68a;
  border-radius: 12px;
  background: #fffbeb;
  color: #92400e !important;
  font-size: 11px;
  line-height: 1.5;
}

.smart-draft-summary {
  margin-top: 18px;
  padding: 14px;
  border: 1px solid #f1f5f9;
  border-radius: 16px;
  background: rgb(248 250 252 / 0.75);
}

.smart-draft-summary span,
.smart-copilot-panel label > span,
.smart-property-panel label > span,
.smart-schema-card span,
.smart-trial-modal label > span {
  display: block;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  line-height: 1;
  text-transform: uppercase;
}

.smart-draft-summary b {
  display: block;
  margin-top: 8px;
  overflow: hidden;
  color: #1e293b;
  font-size: 12px;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.smart-draft-summary small {
  display: block;
  margin-top: 6px;
  color: #64748b;
  font-size: 10px;
  line-height: 1.45;
}

.smart-copilot-panel label,
.smart-property-panel label,
.smart-trial-modal label {
  position: relative;
  display: block;
  margin-top: 16px;
}

.smart-copilot-panel textarea,
.smart-property-panel textarea,
.smart-property-panel input,
.smart-trial-modal textarea,
.smart-trial-modal input {
  width: 100%;
  margin-top: 8px;
  padding: 12px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  outline: 0;
  background: #fff;
  color: #1e293b;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.5;
  resize: none;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.smart-copilot-panel textarea:focus,
.smart-property-panel textarea:focus,
.smart-property-panel input:focus,
.smart-trial-modal textarea:focus {
  border-color: #6366f1;
  box-shadow: 0 0 0 3px rgb(99 102 241 / 0.12);
}

.smart-copilot-panel label em {
  position: absolute;
  right: 12px;
  bottom: 10px;
  color: #64748b;
  font-family: "Fira Code", ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  font-style: normal;
}

.smart-ai-steps {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
  padding: 14px;
  border: 1px solid #e0e7ff;
  border-radius: 16px;
  background: rgb(238 242 255 / 0.6);
}

.smart-ai-steps strong {
  width: 100%;
  color: #4338ca;
  font-size: 10px;
  font-weight: 700;
}

.smart-ai-steps span {
  padding: 4px 8px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: rgb(255 255 255 / 0.65);
  color: #64748b;
  font-size: 10px;
  line-height: 1;
}

.smart-ai-steps span.active {
  border-color: #c7d2fe;
  background: #fff;
  color: #4338ca;
}

.smart-agent-model-hint {
  display: block;
  margin-top: 6px;
  color: #b45309;
  font-size: 11px;
  font-style: normal;
  line-height: 1.4;
}

.smart-turn-history {
  margin-top: 16px;
  max-height: 220px;
  overflow: auto;
  padding: 12px;
  border: 1px solid #e2e8f0;
  border-radius: 14px;
  background: rgb(248 250 252 / 0.9);
}

.smart-turn-history > strong {
  display: block;
  margin-bottom: 8px;
  color: #334155;
  font-size: 11px;
}

.smart-turn-item {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid #e2e8f0;
}

.smart-turn-item:first-of-type {
  margin-top: 0;
  padding-top: 0;
  border-top: 0;
}

.smart-turn-item.is-failed {
  border-left: 3px solid #f59e0b;
  padding-left: 8px;
}

.smart-turn-role {
  display: block;
  color: #64748b;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
}

.smart-turn-item p {
  margin: 4px 0 0;
  color: #1e293b;
  font-size: 12px;
  line-height: 1.45;
}

.smart-turn-item small {
  display: block;
  margin-top: 4px;
  color: #94a3b8;
  font-size: 10px;
}

.smart-guard-report,
.smart-missing-capabilities,
.smart-recovery-card {
  margin-top: 12px;
  padding: 12px;
  border-radius: 12px;
  border: 1px solid #fecaca;
  background: #fef2f2;
  color: #991b1b;
  font-size: 12px;
}

.smart-missing-capabilities {
  border-color: #fde68a;
  background: #fffbeb;
  color: #92400e;
}

.smart-recovery-card {
  border-color: #fca5a5;
  background: #fff1f2;
}

.smart-recovery-card header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}

.smart-recovery-stage {
  display: inline-flex;
  padding: 2px 8px;
  border-radius: 999px;
  background: #fee2e2;
  color: #9f1239;
  font-size: 11px;
  font-weight: 600;
}

.smart-recovery-message {
  margin: 0 0 8px;
  font-weight: 500;
  color: #7f1d1d;
}

.smart-recovery-meta {
  display: grid;
  gap: 4px;
  margin: 0 0 8px;
}

.smart-recovery-meta div {
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 6px;
}

.smart-recovery-meta dt {
  margin: 0;
  color: #9f1239;
  font-weight: 600;
}

.smart-recovery-meta dd {
  margin: 0;
  word-break: break-all;
  color: #881337;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
}

.smart-recovery-hint {
  margin: 0 0 10px;
  color: #9f1239;
  opacity: 0.9;
}

.smart-recovery-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.smart-recovery-actions button {
  min-height: 36px;
  padding: 0 12px;
  border-radius: 10px;
  border: 1px solid #fecdd3;
  background: #fff;
  color: #9f1239;
  cursor: pointer;
}

.smart-recovery-actions button.primary {
  border-color: #e11d48;
  background: #e11d48;
  color: #fff;
}

.smart-recovery-actions button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.smart-copilot-panel select {
  width: 100%;
  min-height: 44px;
  margin-top: 8px;
  padding: 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  color: #0f172a;
  font-size: 12px;
}

.smart-copilot-actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid #f1f5f9;
}

.smart-copilot-actions button,
.smart-ai-draft-banner button,
.smart-save-draft-button,
.smart-trial-modal footer button {
  min-height: 44px;
  min-width: 44px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  color: #475569;
  font-size: 11px;
  font-weight: 700;
  transition: background-color 0.16s ease, color 0.16s ease, transform 0.16s ease;
}

.smart-copilot-actions .primary,
.smart-trial-modal footer .primary {
  grid-column: 1 / -1;
  border-color: #4f46e5;
  background: #4f46e5;
  color: #fff;
}

.smart-copilot-actions button:hover,
.smart-ai-draft-banner button:hover,
.smart-save-draft-button:hover,
.smart-trial-modal footer button:hover,
.smart-blueprint-grid article > button:hover,
.blueprint-picker-modal footer button:hover,
.smart-status-filter button:hover {
  transform: translateY(-1px);
}

.smart-ai-reason {
  margin-top: 16px;
  padding: 14px;
  border: 1px solid #e0e7ff;
  border-radius: 14px;
  background: rgb(238 242 255 / 0.5);
}

.smart-ai-reason strong {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #4338ca;
  font-size: 10px;
  font-weight: 700;
}

.smart-ai-reason p {
  margin: 8px 0 0;
  color: #312e81;
  font-size: 10px;
  font-weight: 500;
  line-height: 1.5;
}

.smart-schema-card,
.smart-compiler-panel {
  margin-top: 16px;
}

.smart-schema-card {
  padding: 12px;
  border: 1px solid #f1f5f9;
  border-radius: 14px;
  background: #f8fafc;
}

.smart-schema-card code {
  display: block;
  margin-top: 6px;
  padding: 10px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  color: #4f46e5;
  font-family: "Fira Code", ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  line-height: 1.5;
  white-space: normal;
}

.smart-panel-empty {
  margin-top: 18px;
  padding: 24px 16px;
  border: 1px dashed #e2e8f0;
  border-radius: 16px;
  color: #64748b;
  font-size: 12px;
  text-align: center;
}

.smart-compiler-panel {
  padding-top: 16px;
  border-top: 1px solid #e2e8f0;
}

.smart-compiler-panel > div:first-child {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.smart-compiler-panel > div:first-child strong {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #1e293b;
  font-size: 12px;
}

.smart-compiler-panel > div:first-child span {
  padding: 3px 8px;
  border-radius: 999px;
  background: #ecfdf5;
  color: #047857;
  font-size: 10px;
  font-weight: 700;
}

.smart-compiler-panel article {
  margin-top: 10px;
  padding: 12px;
  border: 1px solid rgb(251 191 36 / 0.5);
  border-radius: 12px;
  background: rgb(255 251 235 / 0.65);
}

.smart-compiler-panel article strong,
.smart-compiler-panel article small {
  display: block;
}

.smart-compiler-panel article strong {
  color: #92400e;
  font-size: 11px;
}

.smart-compiler-panel article small {
  margin-top: 4px;
  color: #64748b;
  font-size: 10px;
  line-height: 1.45;
}

.smart-compile-ok {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  margin-top: 12px;
  padding: 16px;
  border: 1px solid #d1fae5;
  border-radius: 16px;
  background: rgb(236 253 245 / 0.35);
  text-align: center;
}

.smart-compile-ok i {
  display: inline-flex;
  width: 32px;
  height: 32px;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: #10b981;
  color: #fff;
  font-size: 12px;
}

.smart-compile-ok strong {
  margin-top: 8px;
  color: #064e3b;
  font-size: 12px;
}

.smart-compile-ok small {
  margin-top: 4px;
  color: rgb(5 150 105 / 0.75);
  font-size: 10px;
  line-height: 1.45;
}

.smart-save-draft-button {
  width: 100%;
  margin-top: 16px;
  border-color: #0d9488;
  background: #f0fdfa;
  color: #0f766e;
}

.smart-status-badge {
  display: inline-flex;
  align-items: center;
  padding: 3px 8px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
}

.smart-status-badge.is-published {
  border-color: #ccfbf1;
  background: #f0fdfa;
  color: #0f766e;
}

.smart-status-badge.is-review {
  border-color: #fef3c7;
  background: #fffbeb;
  color: #b45309;
}

.smart-status-badge.is-draft {
  border-color: #e0e7ff;
  background: #eef2ff;
  color: #4338ca;
}

.smart-ai-draft-banner,
.smart-zoom-dock {
  position: absolute;
  z-index: 40;
  left: 50%;
  transform: translateX(-50%);
  border: 1px solid #e2e8f0;
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.08), 0 1px 10px rgb(15 23 42 / 0.02);
}

.smart-ai-draft-banner {
  bottom: 94px;
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: 640px;
  padding: 16px 20px;
  border-color: #1e293b;
  border-radius: 16px;
  background: rgb(2 6 23 / 0.95);
  color: #fff;
}

.smart-ai-draft-banner > div {
  display: flex;
  align-items: center;
  gap: 12px;
}

.smart-ai-draft-banner > div > i {
  display: flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: 12px;
  background: #4f46e5;
  font-size: 12px;
}

.smart-ai-draft-banner strong,
.smart-ai-draft-banner span {
  display: block;
}

.smart-ai-draft-banner strong {
  font-size: 12px;
}

.smart-ai-draft-banner span {
  margin-top: 3px;
  color: #cbd5e1;
  font-size: 10px;
}

.smart-ai-draft-banner button {
  min-width: 82px;
  border-color: #1e293b;
  background: transparent;
  color: #fff;
}

.smart-ai-draft-banner button.primary {
  border-color: #4f46e5;
  background: #4f46e5;
}

.smart-zoom-dock {
  bottom: 32px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px;
  border-radius: 16px;
  background: rgb(255 255 255 / 0.9);
  backdrop-filter: blur(16px);
}

.smart-zoom-dock button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: #475569;
}

.smart-zoom-dock button:hover,
.smart-zoom-dock button.active {
  background: #f1f5f9;
}

.smart-zoom-dock span {
  min-width: 45px;
  color: #64748b;
  font-family: "Fira Code", ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  font-weight: 700;
  text-align: center;
}

.smart-zoom-dock > i {
  width: 1px;
  height: 20px;
  background: #e2e8f0;
}

.smart-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgb(15 23 42 / 0.38);
  backdrop-filter: blur(8px);
}

.blueprint-picker-modal {
  width: min(1120px, 100%);
  max-height: calc(100vh - 56px);
  overflow: hidden;
  border: 1px solid #f1f5f9;
  border-radius: 28px;
  background: #fff;
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.08), 0 1px 10px rgb(15 23 42 / 0.02);
}

.blueprint-picker-modal > header {
  padding: 20px 24px;
  border-bottom: 1px solid #f1f5f9;
}

.blueprint-picker-modal > section {
  max-height: calc(100vh - 238px);
  overflow: auto;
  padding: 24px;
  background: #fafbfd;
}

.smart-picker-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 20px;
}

.smart-picker-toolbar label {
  position: relative;
  flex: 0 0 320px;
}

.smart-picker-toolbar label i {
  position: absolute;
  top: 50%;
  left: 14px;
  color: #64748b;
  font-size: 12px;
  transform: translateY(-50%);
}

.smart-picker-toolbar input {
  width: 100%;
  min-height: 44px;
  padding: 0 14px 0 36px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  color: #1e293b;
  font-size: 12px;
  outline: 0;
}

.smart-status-filter {
  display: flex;
  gap: 2px;
  padding: 2px;
  border: 1px solid rgb(226 232 240 / 0.8);
  border-radius: 12px;
  background: #fff;
}

.smart-status-filter button {
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border: 0;
  border-radius: 9px;
  background: transparent;
  color: #64748b;
  font-size: 11px;
  font-weight: 600;
}

.smart-status-filter button.active {
  background: #fff;
  color: #0f172a;
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.08);
}

.smart-picker-toolbar > span {
  padding: 6px 10px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #fff;
  color: #64748b;
  font-size: 11px;
}

.smart-blueprint-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.smart-blueprint-grid article {
  padding: 20px;
  border: 1px solid #f1f5f9;
  border-radius: 16px;
  background: #fff;
  box-shadow: 0 10px 30px -10px rgb(0 0 0 / 0.04), 0 1px 3px rgb(0 0 0 / 0.02);
}

.smart-blueprint-grid article.selected {
  border-color: #99f6e4;
  box-shadow: 0 0 0 3px rgb(13 148 136 / 0.12), 0 10px 30px -10px rgb(0 0 0 / 0.04);
}

.smart-blueprint-grid article > div:first-child {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.smart-blueprint-grid article strong {
  color: #1e293b;
  font-size: 14px;
  font-weight: 700;
}

.smart-blueprint-grid article p {
  margin: 8px 0 0;
  color: #64748b;
  font-size: 11px;
  line-height: 1.55;
}

.smart-blueprint-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}

.smart-blueprint-chips span {
  padding: 4px 8px;
  border-radius: 8px;
  background: #f8fafc;
  color: #64748b;
  font-size: 10px;
  font-weight: 600;
}

.smart-blueprint-chips .is-risk {
  background: #fff1f2;
  color: #be123c;
}

.smart-blueprint-chips .is-service {
  background: #f0fdfa;
  color: #0f766e;
}

.smart-blueprint-chips .is-ai {
  background: #eef2ff;
  color: #4338ca;
}

.smart-blueprint-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin-top: 16px;
}

.smart-blueprint-stats span {
  padding: 8px 12px;
  border: 1px solid #f1f5f9;
  border-radius: 12px;
  background: #f8fafc;
}

.smart-blueprint-stats small,
.smart-blueprint-stats b {
  display: block;
}

.smart-blueprint-stats small {
  color: #64748b;
  font-size: 9px;
}

.smart-blueprint-stats b {
  margin-top: 3px;
  color: #1e293b;
  font-family: "Fira Code", ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 14px;
}

.smart-blueprint-grid article > button {
  float: right;
  min-height: 44px;
  min-width: 44px;
  margin-top: 16px;
  padding: 0 14px;
  border: 0;
  border-radius: 12px;
  background: #f0fdfa;
  color: #0f766e;
  font-size: 12px;
  font-weight: 700;
}

.smart-picker-empty {
  grid-column: 1 / -1;
  padding: 64px 24px;
  border: 1px solid #f1f5f9;
  border-radius: 16px;
  background: #fff;
  color: #64748b;
  text-align: center;
}

.smart-picker-empty i {
  color: #cbd5e1;
  font-size: 22px;
}

.smart-picker-empty strong,
.smart-picker-empty span {
  display: block;
}

.smart-picker-empty strong {
  margin-top: 12px;
  color: #334155;
  font-size: 14px;
}

.smart-picker-empty span {
  margin-top: 4px;
  font-size: 11px;
}

.blueprint-picker-modal footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 24px;
  border-top: 1px solid #f1f5f9;
  background: #fff;
  color: #64748b;
  font-size: 11px;
}

.blueprint-picker-modal footer div {
  display: flex;
  align-items: center;
  gap: 8px;
}

.blueprint-picker-modal footer button {
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  color: #334155;
  font-size: 12px;
  font-weight: 700;
}

.blueprint-picker-modal footer button.active {
  border-color: #0d9488;
  background: #0d9488;
  color: #fff;
}

.blueprint-picker-modal footer button:disabled {
  cursor: not-allowed;
  background: #f1f5f9;
  color: #cbd5e1;
}

.smart-trial-modal {
  width: 480px;
  padding: 24px;
  border: 1px solid #f1f5f9;
  border-radius: 24px;
  background: #fff;
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.08), 0 1px 10px rgb(15 23 42 / 0.02);
}

.smart-trial-modal header > i {
  display: flex;
  width: 40px;
  height: 40px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 12px;
  background: #eef2ff;
  color: #4f46e5;
}

.smart-trial-modal footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid #f1f5f9;
}

.smart-trial-modal footer button {
  padding: 0 16px;
}

.smart-trial-error {
  margin: 8px 0 0;
  padding: 10px 12px;
  border: 1px solid #fecdd3;
  border-radius: 12px;
  background: #fff1f2;
  color: #be123c;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.45;
}

.smart-toast {
  position: fixed;
  z-index: 3200;
  bottom: 24px;
  left: 50%;
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 12px 20px;
  border-radius: 16px;
  background: #0f172a;
  color: #fff;
  box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.2);
  font-size: 12px;
  font-weight: 700;
  transform: translateX(-50%);
}

.smart-toast i {
  color: #14b8a6;
}

.smart-toast.is-error i {
  color: #fb7185;
}

.smart-toast button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  margin: -10px -14px -10px 0;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: #cbd5e1;
}

.smart-toast button:hover {
  background: rgb(255 255 255 / 0.12);
  color: #fff;
}

.smart-screen-warning {
  display: none;
}

.smart-narrow-blocker {
  display: none;
}

@media (max-width: 1180px) {
  .smart-screen-warning {
    display: none;
  }

  .smart-narrow-blocker {
    position: fixed;
    top: 76px;
    right: 16px;
    left: 16px;
    z-index: 140;
    display: block;
    padding: 16px;
    border: 1px solid #fde68a;
    border-radius: 16px;
    background: #fffbeb;
    color: #92400e;
    box-shadow: 0 20px 40px -15px rgb(15 23 42 / 0.18);
  }

  .smart-narrow-blocker strong,
  .smart-narrow-blocker span {
    display: block;
  }

  .smart-narrow-blocker strong {
    font-size: 14px;
  }

  .smart-narrow-blocker span {
    margin-top: 6px;
    font-size: 12px;
    line-height: 1.45;
  }

  .smart-orchestration-main {
    pointer-events: none;
  }

  .smart-blueprint-toolbar,
  .smart-copilot-panel,
  .smart-property-panel,
  .smart-zoom-dock,
  .smart-canvas-hint {
    display: none;
  }

  .smart-canvas-container {
    opacity: 0.45;
  }
}
</style>
