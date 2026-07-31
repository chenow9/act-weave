/**
 * Smart DAG page model (ZKL-64 item 14).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import {
  assignUniqueBranchSides,
  buildEdgePath,
  edgeLabelPoint,
  resolveEdgeAnchors,
  type EdgeKind,
  type EdgeSide,
} from "../components/workflow/workflow-edges";
import { autoLayoutWorkflowGraph, layoutWorkflowGraphIfNeeded } from "../components/workflow/workflow-layout";
import { apiErrorMessage } from "../services/api";
import { useAgentStore } from "../stores/agents";
import { useModelConfigStore } from "../stores/modelConfigs";
import { useSmartDagStore, type FailureFeedback, type FailureFeedbackIssue } from "../stores/smartdag";
import { useToolsStore } from "../stores/tools";
import { useWorkflowStore } from "../stores/workflow";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Workflow,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
  WorkflowGraphNode,
  WorkflowGraphNodeType,
  WorkflowSummary,
} from "../types/domain";

export function createSmartDagPageModel() {
  type BlueprintStatus = "published" | "review" | "draft";
  type NodeTheme = "start" | "tool" | "condition" | "approval" | "transform" | "end";
  type ToastTone = "success" | "error" | "info";

  interface SmartNode {
    id: string;
    graphType: WorkflowGraphNodeType;
    type: string;
    title: string;
    /** One-line subtitle (service / role), not a full paragraph. */
    desc: string;
    x: number;
    y: number;
    theme: NodeTheme;
    isAiDraft?: boolean;
    aiReason?: string;
    toolId?: string;
    toolName?: string;
    /** Condition branch keys for multi-port rendering. */
    branchPorts?: Array<{ key: string; label: string }>;
    /** Compact footer status, e.g. "配置完整". */
    statusHint?: string;
    /** End outcome: success | failure | generic. */
    endKind?: "success" | "failure" | "generic";
  }

  type SmartEdgeKind = "sequence" | "branch" | "loop";

  interface SmartConnection {
    from: string;
    to: string;
    /** Condition branch raw key from graph edge data. */
    branch?: string;
    /** Human label for branch edges. */
    label?: string;
    kind: SmartEdgeKind;
    /** Source port key for condition multi-port layout. */
    sourcePort?: string;
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
  const toolsStore = useToolsStore();
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
  const nodeDragging = ref({
    active: false,
    nodeId: "",
    pointerId: -1,
    startX: 0,
    startY: 0,
    originX: 0,
    originY: 0,
  });
  /** Dynamic workspace size so wide auto-layouts are not clipped at 1800×900. */
  const canvasWorkspaceSize = ref({ width: 2400, height: 1200 });
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
  const activeWorkspace = computed(() =>
    workspaces.value.find((workspace) => workspace.id === activeWorkspaceId.value),
  );
  const workspaceAgents = computed(() =>
    (agentStore.items || []).filter(
      (agent) => !activeWorkspaceId.value || agent.workspaceId === activeWorkspaceId.value,
    ),
  );
  const workspaceSelectOptions = computed(() =>
    workspaces.value.map((workspace) => ({
      label: workspace.displayName || workspace.name || workspace.id,
      value: workspace.id,
    })),
  );
  const agentSelectOptions = computed(() =>
    workspaceAgents.value.map((agent) => ({
      label: agent.modelConfigId ? agent.name : `${agent.name}（未绑定模型）`,
      value: agent.id,
    })),
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
  const currentBlueprint = computed(
    () => blueprints.value.find((workflow) => workflow.id === selectedBlueprintId.value) || emptyBlueprint,
  );
  /** Canvas re-render key: SoT is latest draft after each successful turn (P1.5.3). */
  const canvasRenderKey = computed(
    () =>
      `${smart.canvasEpoch}:${smart.generatedDraft?.id || ""}:${smart.generatedDraft?.draftVersion || 0}:${smart.generatedDraft?.graphHash || ""}`,
  );
  const currentWorkflow = computed(
    () =>
      workflowStore.workflowDetails[currentBlueprint.value.id] ||
      workflowStore.workflows.find((workflow) => workflow.id === currentBlueprint.value.id),
  );
  const selectedNode = computed(() => currentBlueprint.value.nodes.find((node) => node.id === selectedNodeId.value));
  const hasAiDraft = computed(() => currentBlueprint.value.nodes.some((node) => node.isAiDraft));
  const draftGenerated = computed(() =>
    Boolean(currentBlueprint.value.id && blueprintDrafts.has(currentBlueprint.value.id)),
  );
  const currentReadiness = computed(() =>
    currentBlueprint.value.id ? workflowStore.readinessByWorkflowId[currentBlueprint.value.id] : undefined,
  );
  const canPublishSmartDraft = computed(() => Boolean(currentReadiness.value?.canPublish));
  const averageAiScore = computed(() =>
    blueprints.value.length
      ? Math.round(blueprints.value.reduce((sum, item) => sum + item.aiScore, 0) / blueprints.value.length)
      : 0,
  );

  const aiSteps = [
    "解析业务目标和风险门槛",
    "匹配当前空间可调用能力",
    "推断可执行节点结构",
    "生成画布节点布局与连线",
    "写入正式 Workflow Draft",
  ];
  /** Compact card metrics for denser stage layout (readable near 55–70% zoom). */
  const canvasNodeSize = { width: 180, height: 96 };
  const canvasDesktopMinWidth = 1180;

  const filteredBlueprintList = computed(() => {
    const keyword = listQuery.value.trim().toLowerCase();
    return blueprints.value.filter((workflow) => {
      const matchesStatus = statusFilter.value === "ALL" || workflow.status === statusFilter.value;
      const matchesKeyword =
        !keyword ||
        [
          workflow.name,
          workflow.description,
          workflow.agent,
          workflow.space,
          workflow.automationMode,
          getStatusText(workflow.status),
        ]
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

  function resolveToolBinding(node: WorkflowGraphNode): { toolId: string; toolName: string } {
    const raw = node.data && typeof node.data === "object" ? (node.data as Record<string, unknown>).toolId : undefined;
    const toolId = typeof raw === "string" ? raw.trim() : "";
    if (!toolId) return { toolId: "", toolName: "" };
    const catalog =
      toolsStore.tools.find((item) => item.id === toolId) ||
      toolsStore.toolPageItems.find((item) => item.id === toolId);
    return { toolId, toolName: catalog?.name?.trim() || "" };
  }

  /** Prefer real tool name; fall back to humanized node id when LLM left label as "Tool". */
  function displayNodeTitle(node: WorkflowGraphNode, binding: { toolId: string; toolName: string }): string {
    const label = (node.label || "").trim();
    const generic = !label || /^(tool|condition|start|end|transform|approval|http)$/i.test(label);
    if (!generic) return label;
    if (binding.toolName) return binding.toolName;
    if (node.type === "Start") return "开始";
    if (node.type === "End") return "结束";
    if (node.type === "Condition") return "条件判断";
    if (node.type === "Transform") return "整理结果";
    // Humanize id: create_task → 创建 task, get_progress → 获取进度
    const humanized = node.id
      .replace(/[_-]+/g, " ")
      .replace(/\b\w/g, (ch) => ch.toUpperCase())
      .trim();
    return humanized || node.id;
  }

  function humanizeBranchLabel(raw?: string): string {
    const key = String(raw || "")
      .trim()
      .toLowerCase();
    if (!key) return "";
    if (["completed", "complete", "success", "succeeded", "true", "ok", "done"].includes(key)) return "已完成";
    if (["running", "processing", "pending", "in_progress", "in-progress", "wait"].includes(key)) return "处理中";
    if (["failed", "fail", "error", "timeout", "false", "default", "otherwise", "reject"].includes(key)) {
      return "失败/其他";
    }
    return raw || "";
  }

  function classifyEdge(fromId: string, toId: string, xOf: Map<string, number>, branch?: string): SmartEdgeKind {
    const fromX = xOf.get(fromId) ?? 0;
    const toX = xOf.get(toId) ?? 0;
    // True back-edge: target card is left of source card (poll loop).
    // Require a clear reverse delta so tight forward gaps never count as loops.
    if (toX + canvasNodeSize.width * 0.25 < fromX) return "loop";
    if (branch) return "branch";
    return "sequence";
  }

  function nodeBoxSize(_node?: Pick<SmartNode, "graphType">): { width: number; height: number } {
    // One card size for every node type — keeps ports on the main rail and hover stable.
    return { width: canvasNodeSize.width, height: canvasNodeSize.height };
  }

  function connectionRouteInput(conn: SmartConnection) {
    const fromNode = currentBlueprint.value.nodes.find((node) => node.id === conn.from);
    const toNode = currentBlueprint.value.nodes.find((node) => node.id === conn.to);
    if (!fromNode || !toNode) return null;

    const fromSize = nodeBoxSize(fromNode);
    const toSize = nodeBoxSize(toNode);
    let outSide: EdgeSide | undefined;

    // Condition: each branch gets a unique side (one port per side, midpoint only).
    if (fromNode.graphType === "Condition" && fromNode.branchPorts?.length) {
      const assigned = assignUniqueBranchSides(fromNode.branchPorts);
      const match =
        assigned.find(
          (p) => p.key === conn.sourcePort || p.key === conn.branch || p.label === conn.label || p.key === conn.label,
        ) || assigned[0];
      outSide = match?.side;
    }

    return {
      from: { x: fromNode.x, y: fromNode.y, w: fromSize.width, h: fromSize.height },
      to: { x: toNode.x, y: toNode.y, w: toSize.width, h: toSize.height },
      kind: (conn.kind || "sequence") as EdgeKind,
      label: conn.label || conn.branch || "",
      outSide,
    };
  }

  function getConnectionPath(conn: SmartConnection) {
    const input = connectionRouteInput(conn);
    if (!input) return "";
    return buildEdgePath(input);
  }

  function getConnectionLabelPoint(conn: SmartConnection): { x: number; y: number } | null {
    if (!conn.label) return null;
    const input = connectionRouteInput(conn);
    if (!input) return null;
    return edgeLabelPoint(input);
  }

  /** One port per side, always centred. */
  function getConditionPortStyle(side: EdgeSide): Record<string, string> {
    if (side === "bottom") {
      return { top: "auto", left: "50%", right: "auto", bottom: "0", transform: "translate(-50%, 100%)" };
    }
    if (side === "top") {
      return { top: "0", left: "50%", right: "auto", bottom: "auto", transform: "translate(-50%, -100%)" };
    }
    if (side === "left") {
      return { top: "50%", left: "0", right: "auto", bottom: "auto", transform: "translate(-100%, -50%)" };
    }
    return { top: "50%", left: "auto", right: "0", bottom: "auto", transform: "translate(100%, -50%)" };
  }

  /** Condition branch ports with unique sides for template rendering. */
  function getConditionBranchPorts(node: SmartNode): Array<{ key: string; label: string; side: EdgeSide }> {
    if (!node.branchPorts?.length) return [{ key: "out", label: "输出", side: "right" }];
    return assignUniqueBranchSides(node.branchPorts);
  }

  /**
   * Sides that must show a port dot on this node.
   * Edges always terminate at these centres — only render a side if used.
   */
  function getNodePortSides(node: SmartNode): EdgeSide[] {
    if (node.graphType === "Condition") {
      const sides = new Set<EdgeSide>(["left"]);
      for (const p of getConditionBranchPorts(node)) sides.add(p.side);
      return [...sides];
    }

    const sides = new Set<EdgeSide>();
    if (node.graphType !== "Start") sides.add("left");
    if (node.graphType !== "End") sides.add("right");

    for (const conn of currentBlueprint.value.connections || []) {
      if (conn.from !== node.id && conn.to !== node.id) continue;
      const input = connectionRouteInput(conn);
      if (!input) continue;
      const anchors = resolveEdgeAnchors(input);
      if (conn.from === node.id) sides.add(anchors.outSide);
      if (conn.to === node.id) sides.add(anchors.inSide);
    }
    return [...sides];
  }

  function portSideClass(side: EdgeSide): string {
    return `is-${side}`;
  }

  function portTitle(side: EdgeSide): string {
    if (side === "left") return "输入";
    if (side === "right") return "输出";
    if (side === "top") return "上端口";
    return "下端口";
  }

  function calculateGraphBounds(nodes = currentBlueprint.value.nodes) {
    if (!nodes.length) {
      return {
        minX: 0,
        minY: 0,
        maxX: canvasNodeSize.width,
        maxY: canvasNodeSize.height,
        width: canvasNodeSize.width,
        height: canvasNodeSize.height,
      };
    }
    const minX = Math.min(...nodes.map((node) => node.x));
    const minY = Math.min(...nodes.map((node) => node.y));
    const maxX = Math.max(...nodes.map((node) => node.x + nodeBoxSize(node).width));
    // Leave room for bottom-exit loop troughs under the lowest node.
    const maxY = Math.max(...nodes.map((node) => node.y + nodeBoxSize(node).height + 80));
    return { minX, minY, maxX, maxY, width: maxX - minX, height: maxY - minY };
  }

  /** Grow the pan/zoom workspace so nodes and edges are not clipped. */
  function refreshCanvasWorkspaceSize(nodes = currentBlueprint.value.nodes) {
    const bounds = calculateGraphBounds(nodes);
    const pad = 280;
    canvasWorkspaceSize.value = {
      width: Math.max(2400, Math.ceil(bounds.maxX + pad)),
      height: Math.max(1200, Math.ceil(bounds.maxY + pad)),
    };
  }

  function replaceBlueprint(blueprint: SmartBlueprint) {
    const index = blueprints.value.findIndex((item) => item.id === blueprint.id);
    if (index >= 0) blueprints.value.splice(index, 1, { ...blueprint, nodes: [...blueprint.nodes] });
    else blueprints.value.unshift({ ...blueprint, nodes: [...blueprint.nodes] });
  }

  function updateBlueprintNodePosition(nodeId: string, x: number, y: number) {
    const blueprint = currentBlueprint.value;
    if (!blueprint.id) return;
    const nodes = blueprint.nodes.map((node) => (node.id === nodeId ? { ...node, x, y } : node));
    replaceBlueprint({ ...blueprint, nodes });
    const draft = blueprintDrafts.get(blueprint.id);
    if (draft?.graph?.nodes) {
      blueprintDrafts.set(blueprint.id, {
        ...draft,
        graph: {
          ...draft.graph,
          nodes: draft.graph.nodes.map((node) => (node.id === nodeId ? { ...node, position: { x, y } } : node)),
        },
      });
    }
  }

  function fitCanvasToVisibleArea() {
    const container = canvasContainerRef.value;
    if (!container || window.innerWidth <= canvasDesktopMinWidth) return;

    const containerRect = container.getBoundingClientRect();
    const leftPanelRect = document.querySelector(".smart-copilot-panel:not(.collapsed)")?.getBoundingClientRect();
    const rightPanelRect = document.querySelector(".smart-property-panel:not(.collapsed)")?.getBoundingClientRect();
    const toolbarRect = document
      .querySelector(".smart-blueprint-toolbar:not(.is-compact) .smart-blueprint-toolbar-inner")
      ?.getBoundingClientRect();
    const zoomDockRect = document.querySelector(".smart-zoom-dock")?.getBoundingClientRect();
    const safeLeft = Math.max(
      containerRect.left + 24,
      leftPanelRect ? leftPanelRect.right + 24 : containerRect.left + 24,
    );
    const safeRight = Math.min(
      containerRect.right - 24,
      rightPanelRect ? rightPanelRect.left - 24 : containerRect.right - 24,
    );
    const safeTop = Math.max(containerRect.top + 24, toolbarRect ? toolbarRect.bottom + 24 : containerRect.top + 24);
    const safeBottom = Math.min(
      containerRect.bottom - 24,
      zoomDockRect ? zoomDockRect.top - 24 : containerRect.bottom - 24,
    );
    const safeWidth = Math.max(320, safeRight - safeLeft);
    const safeHeight = Math.max(260, safeBottom - safeTop);
    const bounds = calculateGraphBounds();
    // Prefer readability: floor at 0.55 so 8–10 nodes stay legible; pan for overflow.
    const fitZoom = Math.min(safeWidth / (bounds.width + 96), safeHeight / (bounds.height + 96));
    const nextZoom = Math.min(1, Math.max(0.55, fitZoom));
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
    if (type === "Start") return "开始";
    if (type === "End") return "结束";
    if (type === "Tool") return "工具";
    if (type === "Condition") return "条件";
    if (type === "Transform") return "变换";
    if (type === "Approval") return "审批";
    return type;
  }

  function blueprintFromDraft(
    workflow: WorkflowSummary | Workflow,
    draft: WorkflowDraftRecord,
    confidence = 90,
  ): SmartBlueprint {
    const workspace = workspaces.value.find((item) => item.id === workflow.workspaceId);
    // Always reflow so Smart canvas is readable (stage-compact + branch below).
    const laidOut = autoLayoutWorkflowGraph(layoutWorkflowGraphIfNeeded(draft.graph));
    if (draft.graph) {
      draft.graph = {
        ...draft.graph,
        nodes: draft.graph.nodes.map((node) => {
          const laid = laidOut.nodes.find((item) => item.id === node.id);
          return laid ? { ...node, position: { ...laid.position } } : node;
        }),
      };
    }

    const xOf = new Map<string, number>();
    laidOut.nodes.forEach((node) => {
      xOf.set(node.id, node.position?.x || 0);
    });

    // Collect condition branches from edges for multi-port UI.
    const branchesBySource = new Map<string, Array<{ key: string; label: string }>>();
    for (const edge of laidOut.edges || []) {
      if (!edge.sourceNodeId || !edge.targetNodeId) continue;
      const rawBranch =
        edge.data && typeof edge.data === "object"
          ? String((edge.data as Record<string, unknown>).branch || "").trim()
          : "";
      if (!rawBranch) continue;
      const label = humanizeBranchLabel(rawBranch);
      const list = branchesBySource.get(edge.sourceNodeId) || [];
      if (!list.some((item) => item.key === rawBranch)) {
        list.push({ key: rawBranch, label: label || rawBranch });
      }
      branchesBySource.set(edge.sourceNodeId, list);
    }

    // Infer end-kind: if all inbound edges are failure-like → failure end.
    const failureKeys = new Set(["failed", "fail", "error", "timeout", "false", "default", "otherwise", "reject"]);
    const endKindById = new Map<string, "success" | "failure" | "generic">();
    for (const node of laidOut.nodes) {
      if (node.type !== "End") continue;
      const inbound = (laidOut.edges || []).filter((e) => e.targetNodeId === node.id);
      if (!inbound.length) {
        endKindById.set(node.id, "generic");
        continue;
      }
      const branches = inbound.map((e) =>
        String((e.data as Record<string, unknown> | undefined)?.branch || "").toLowerCase(),
      );
      const allFailure = branches.every((b) => b && failureKeys.has(b));
      const anySuccess = branches.some((b) =>
        ["completed", "complete", "success", "succeeded", "true", "ok", "done"].includes(b),
      );
      if (allFailure) endKindById.set(node.id, "failure");
      else if (
        anySuccess ||
        inbound.some((e) => e.sourceNodeId?.includes("report") || e.sourceNodeId?.includes("word"))
      )
        endKindById.set(node.id, "success");
      else if (inbound.length === 1 && !branches[0]) endKindById.set(node.id, "success");
      else endKindById.set(node.id, "generic");
    }

    const nodes: SmartNode[] = laidOut.nodes.map((node) => {
      const binding = resolveToolBinding(node);
      const isTool = node.type === "Tool";
      const title = displayNodeTitle(node, binding);
      let desc = "";
      let statusHint = "";
      if (isTool) {
        // Keep canvas cards short — binding details live in the property panel.
        // Avoid repeating the same string as title (displayNodeTitle often == toolName).
        const toolLabel = binding.toolName || "";
        desc = !binding.toolId ? "未绑定工具" : toolLabel && toolLabel !== title ? toolLabel : "";
        statusHint = binding.toolId ? "" : "缺少 toolId";
      } else if (node.type === "Condition") {
        // Branch status is shown only on edge labels (flowing dashed lines).
        desc = "";
        statusHint = "";
      } else if (node.type === "Start") {
        desc = "流程入口";
      } else if (node.type === "End") {
        desc = ""; // title already carries success / failure wording
      } else if (node.type === "Transform") {
        desc = "构造返回结果";
      } else {
        desc = nodeDescription(node.type);
      }

      return {
        id: node.id,
        graphType: node.type,
        type: nodeTypeText(node.type),
        title:
          node.type === "End"
            ? endKindById.get(node.id) === "failure"
              ? "失败结束"
              : endKindById.get(node.id) === "success"
                ? "成功结束"
                : "结束"
            : title,
        desc,
        x: node.position?.x || 0,
        y: node.position?.y || 0,
        theme: nodeTheme(node.type),
        isAiDraft: node.ui?.generated === true,
        aiReason: typeof node.ui?.reason === "string" ? node.ui.reason : undefined,
        toolId: binding.toolId || undefined,
        toolName: binding.toolName || undefined,
        branchPorts: node.type === "Condition" ? branchesBySource.get(node.id) : undefined,
        statusHint: statusHint || undefined,
        endKind: node.type === "End" ? endKindById.get(node.id) : undefined,
      };
    });

    const connections: SmartConnection[] = (laidOut.edges || [])
      .filter((edge) => edge.sourceNodeId && edge.targetNodeId)
      .map((edge) => {
        const rawBranch =
          edge.data && typeof edge.data === "object"
            ? String((edge.data as Record<string, unknown>).branch || "").trim()
            : "";
        const kind = classifyEdge(edge.sourceNodeId, edge.targetNodeId, xOf, rawBranch || undefined);
        return {
          from: edge.sourceNodeId,
          to: edge.targetNodeId,
          branch: rawBranch || undefined,
          label: humanizeBranchLabel(rawBranch) || undefined,
          kind,
          sourcePort: rawBranch || edge.sourcePort || undefined,
        };
      });

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
      nodes,
      connections,
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

  function registerBlueprint(
    workflow: WorkflowSummary | Workflow,
    draft: WorkflowDraftRecord,
    confidence = 90,
    select = true,
  ) {
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
    refreshCanvasWorkspaceSize(workflow.nodes);
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
        {
          id: "preview-start",
          graphType: "Start",
          type: "START",
          title: "定义业务目标",
          desc: "等待智能生成正式入口",
          x: 180,
          y: 240,
          theme: "start",
        },
        {
          id: "preview-end",
          graphType: "End",
          type: "END",
          title: "等待生成结果",
          desc: "生成成功后替换为正式 v1 画布",
          x: 560,
          y: 240,
          theme: "end",
        },
      ],
      connections: [{ from: "preview-start", to: "preview-end", kind: "sequence" }],
    };
    blueprints.value = [workflow, ...blueprints.value.filter((item) => !item.id.startsWith("local-smart-draft-"))];
    selectedBlueprintId.value = localID;
    selectedNodeId.value = "preview-start";
    compilerIssues.value = [
      { title: "尚未生成正式草稿", desc: "输入业务目标后，系统会持久化 workflow.graph.v1，而不是创建本地假成功状态。" },
    ];
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
    // Prefetch full tool catalog so canvas shows real tool names after generation.
    try {
      if (workspaceStore.activeWorkspaceId !== workspaceId) {
        workspaceStore.activeWorkspaceId = workspaceId;
      }
      void toolsStore.loadTools({ commit: true });
    } catch {
      /* optional */
    }
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
    const blueprint = currentBlueprint.value;
    if (!blueprint.id) return;
    replaceBlueprint({
      ...blueprint,
      status: "review",
      automationMode: "AI 协同已采纳",
      nodes: blueprint.nodes.map((node) => ({ ...node, isAiDraft: false })),
    });
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
    const nextNodes = blueprint.nodes.map((node) => {
      const position = positionById.get(node.id);
      if (!position) return node;
      return { ...node, x: position.x, y: position.y };
    });
    replaceBlueprint({ ...blueprint, nodes: nextNodes });

    if (draft?.graph) {
      blueprintDrafts.set(blueprint.id, {
        ...draft,
        graph: {
          ...draft.graph,
          nodes: draft.graph.nodes.map((node) => {
            const position = positionById.get(node.id);
            return position ? { ...node, position: { ...position } } : node;
          }),
        },
      });
    }

    refreshCanvasWorkspaceSize(nextNodes);
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
      if (workflowStore.workflowDetails[workflow.id])
        smart.adoptDraft(workflowStore.workflowDetails[workflow.id], saved);
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
    let agent =
      agentStore.items.find((item) => item.id === agentId) || agentStore.pageItems.find((item) => item.id === agentId);
    if (!agent) {
      try {
        await agentStore.loadAgents({ workspaceId: workflow.workspaceId });
        agent = agentStore.items.find((item) => item.id === agentId);
      } catch {
        // fall through
      }
    }
    if (!agent) {
      showToast(`Workflow 已发布。请在 Agent 页将 Capability 绑定到 Agent（默认目标 ${agentId}）。`, "info");
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
      showToast(`Workflow 已发布并绑定到生成会话 Agent「${agent.name}」。可在 Console 对话台调用。`, "success");
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
    const blueprint = currentBlueprint.value;
    if (!blueprint.id) return;
    if (blueprint.nodes.length <= 2) {
      showToast("正式草稿至少保留 Start 和 End 节点。", "error");
      return;
    }
    const nodes = blueprint.nodes.filter((node) => node.id !== nodeId);
    const connections = blueprint.connections.filter((conn) => conn.from !== nodeId && conn.to !== nodeId);
    replaceBlueprint({ ...blueprint, nodes, connections });
    selectedNodeId.value = nodes[0]?.id || "";
  }

  function startNodeDrag(event: PointerEvent, nodeId: string) {
    if (event.button !== 0) return;
    const target = event.target instanceof HTMLElement ? event.target : undefined;
    if (target?.closest("button, input, textarea, select, a")) return;
    event.stopPropagation();
    const node = currentBlueprint.value.nodes.find((item) => item.id === nodeId);
    if (!node) return;
    selectedNodeId.value = nodeId;
    nodeDragging.value = {
      active: true,
      nodeId,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: node.x,
      originY: node.y,
    };
    (event.currentTarget as HTMLElement | null)?.setPointerCapture?.(event.pointerId);
  }

  function moveNodeDrag(event: PointerEvent) {
    const drag = nodeDragging.value;
    if (!drag.active || drag.pointerId !== event.pointerId) return;
    const scale = canvasZoom.value || 1;
    const x = Math.round(drag.originX + (event.clientX - drag.startX) / scale);
    const y = Math.round(drag.originY + (event.clientY - drag.startY) / scale);
    updateBlueprintNodePosition(drag.nodeId, x, y);
  }

  function endNodeDrag(event: PointerEvent) {
    const drag = nodeDragging.value;
    if (!drag.active || drag.pointerId !== event.pointerId) return;
    (event.currentTarget as HTMLElement | null)?.releasePointerCapture?.(event.pointerId);
    nodeDragging.value = {
      active: false,
      nodeId: "",
      pointerId: -1,
      startX: 0,
      startY: 0,
      originX: 0,
      originY: 0,
    };
    refreshCanvasWorkspaceSize();
  }

  function startCanvasPan(event: PointerEvent) {
    if (event.button !== 0) return;
    if (nodeDragging.value.active) return;
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
    if (nodeDragging.value.active) {
      moveNodeDrag(event);
      return;
    }
    const pan = canvasPanning.value;
    if (!pan.active || pan.pointerId !== event.pointerId) return;
    canvasPan.value = {
      x: pan.originX + event.clientX - pan.startX,
      y: pan.originY + event.clientY - pan.startY,
    };
  }

  function endCanvasPan(event: PointerEvent) {
    if (nodeDragging.value.active) {
      endNodeDrag(event);
      return;
    }
    const pan = canvasPanning.value;
    if (!pan.active || pan.pointerId !== event.pointerId) return;
    (event.currentTarget as HTMLElement).releasePointerCapture?.(event.pointerId);
    canvasPanning.value = { active: false, pointerId: -1, startX: 0, startY: 0, originX: 0, originY: 0 };
  }

  function zoomIn() {
    canvasZoom.value = Math.min(1.5, Number((canvasZoom.value + 0.1).toFixed(1)));
  }

  function zoomOut() {
    canvasZoom.value = Math.max(0.45, Number((canvasZoom.value - 0.1).toFixed(1)));
  }

  function resetCanvas() {
    refreshCanvasWorkspaceSize();
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
      if (!parsed || Array.isArray(parsed) || typeof parsed !== "object")
        throw new Error("试运行参数必须是 JSON 对象。");
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
      // Full catalog (paginated) so Tool node titles resolve by data.toolId.
      try {
        if (workspaceStore.activeWorkspaceId !== workspaceId) {
          workspaceStore.activeWorkspaceId = workspaceId;
        }
        await toolsStore.loadTools({ commit: true });
      } catch {
        // Tool name resolution degrades gracefully without catalog.
      }
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

  // When tool catalog loads after the draft, re-map toolId → display names.
  watch(
    () => toolsStore.tools.length,
    (count, prev) => {
      if (!count || count === prev) return;
      const blueprint = currentBlueprint.value;
      const draft = blueprint.id ? blueprintDrafts.get(blueprint.id) : undefined;
      if (!blueprint.id || !draft) return;
      const detail =
        workflowStore.workflowDetails[blueprint.id] || workflowStore.workflows.find((item) => item.id === blueprint.id);
      if (!detail) return;
      registerBlueprint(detail, draft, blueprint.aiScore, false);
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

    const agentId = (typeof q.agentId === "string" && q.agentId.trim()) || selectedAgentId.value || "";
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
      reviseSource === "trial" ||
      reviseSource === "production" ||
      reviseSource === "agent_run" ||
      reviseSource === "guard"
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

  return {
    smart,
    workflowStore,
    workspaceStore,
    agentStore,
    modelConfigStore,
    router,
    route,
    pendingFailureFeedback,
    emptyBlueprint,
    blueprints,
    blueprintDrafts,
    activeWorkspaceId,
    selectedAgentId,
    selectedBlueprintId,
    selectedNodeId,
    copilotPrompt,
    blueprintPickerOpen,
    listQuery,
    statusFilter,
    listPage,
    listPageSize,
    canvasZoom,
    canvasPan,
    canvasPanning,
    nodeDragging,
    canvasWorkspaceSize,
    isNarrowViewport,
    blueprintToolbarCompact,
    leftPanelCollapsed,
    rightPanelCollapsed,
    focusMode,
    aiStatus,
    compilerIssues,
    toast,
    sandbox,
    sandboxError,
    canvasContainerRef,
    blueprintModalRef,
    blueprintSearchInputRef,
    sandboxModalRef,
    sandboxInputRef,
    lastFocusedElement,
    toastTimer,
    workspaces,
    activeWorkspace,
    workspaceAgents,
    workspaceSelectOptions,
    agentSelectOptions,
    selectedAgent,
    selectedAgentModelConfig,
    agentHasUsableModel,
    canSendGenerateTurn,
    turnHistory,
    currentBlueprint,
    canvasRenderKey,
    currentWorkflow,
    selectedNode,
    hasAiDraft,
    draftGenerated,
    currentReadiness,
    canPublishSmartDraft,
    averageAiScore,
    aiSteps,
    canvasNodeSize,
    canvasDesktopMinWidth,
    filteredBlueprintList,
    totalListPages,
    paginatedBlueprintList,
    listPageNumbers,
    paginationStart,
    paginationEnd,
    closeToast,
    showToast,
    captureFocusBeforeModal,
    restoreFocusAfterModal,
    openBlueprintPicker,
    closeBlueprintPicker,
    openSandbox,
    closeSandbox,
    getFocusableElements,
    trapModalFocus,
    handleBlueprintModalKeydown,
    handleSandboxModalKeydown,
    getStatusText,
    getStatusClass,
    getAutomationClass,
    getNodeTypeClass,
    getParameterSchema,
    getConnectionPath,
    getConnectionLabelPoint,
    getConditionPortStyle,
    getConditionBranchPorts,
    getNodePortSides,
    portSideClass,
    portTitle,
    calculateGraphBounds,
    refreshCanvasWorkspaceSize,
    fitCanvasToVisibleArea,
    resetBlueprintFilters,
    setStatusFilter,
    workflowStatus,
    nodeTheme,
    nodeTypeText,
    blueprintFromDraft,
    nodeDescription,
    registerBlueprint,
    loadBlueprint,
    createBlankBlueprint,
    syncGenerateContext,
    generateDraft,
    retryLastGenerateTurn,
    closeGenerateSessionWithConfirm,
    startNewGenerateSession,
    finishGeneration,
    acceptAiDraft,
    discardAiDraft,
    graphFromBlueprint,
    applyAutoLayout,
    saveDraft,
    validateSmartWorkflow,
    openInWorkflowEditor,
    bindPublishedWorkflowToSessionAgent,
    publishWorkflow,
    deleteNode,
    startNodeDrag,
    moveNodeDrag,
    endNodeDrag,
    startCanvasPan,
    moveCanvasPan,
    endCanvasPan,
    zoomIn,
    zoomOut,
    resetCanvas,
    syncViewportMode,
    handleViewportResize,
    runSandboxTrial,
    loadPersistedBlueprints,
    applyReviseQuerySeed,
  };
}
