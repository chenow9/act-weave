<script setup lang="ts">
import "@vue-flow/core/dist/style.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  BaseEdge,
  ConnectionMode,
  EdgeLabelRenderer,
  Handle,
  MarkerType,
  Position,
  VueFlow,
  type EdgeProps,
  type NodeDragEvent,
  type ViewportTransform,
  useVueFlow,
} from "@vue-flow/core";

import { WORKFLOW_GENERATE_HIGHLIGHT_MS } from "../../composables/workflow-generate-dock";
import type { WorkflowGraphDraft } from "../../types/domain";
import { primaryPortKey } from "../../utils/workflow-graph";
import { getWorkflowBranchOptions } from "./WorkflowEdgeInspector.vue";
import { LONG_EDGE_MIN_DX, SAME_LANE_EPS, buildFlowchartEdgePath } from "./workflow-edge-path";

const { t } = useI18n();

const props = defineProps<{
  graph: WorkflowGraphDraft;
  selectedNodeId: string;
  selectedEdgeId?: string;
  empty?: boolean;
  generating?: boolean;
  lockInteraction?: boolean;
  applyHighlightEpoch?: number;
}>();

const emit = defineEmits<{
  (event: "select-node", nodeId: string): void;
  (event: "select-edge", edgeId: string): void;
  (event: "open-node-context-menu", payload: { nodeId: string; position: { x: number; y: number } }): void;
  (event: "open-edge-context-menu", payload: { edgeId: string; position: { x: number; y: number } }): void;
  (event: "update-node-position", payload: { nodeId: string; position: { x: number; y: number } }): void;
  (event: "update-viewport", viewport: { x: number; y: number; zoom: number }): void;
  (
    event: "connect-nodes",
    payload: { sourceNodeId: string; sourcePort: string; targetNodeId: string; targetPort: string },
  ): void;
}>();

const { fitView, zoomIn, zoomOut } = useVueFlow();
const hasUserAdjustedViewport = ref(false);
const isProgrammaticViewportMove = ref(false);
const currentZoom = ref(props.graph.viewport?.zoom || 1);
const highlightActive = ref(false);
const showEmptyCanvas = computed(() => Boolean(props.empty) && props.graph.nodes.length === 0);
let highlightTimer = 0;

watch(
  () => props.applyHighlightEpoch,
  (epoch) => {
    if (!epoch) {
      return;
    }
    highlightActive.value = true;
    window.clearTimeout(highlightTimer);
    highlightTimer = window.setTimeout(() => {
      highlightActive.value = false;
    }, WORKFLOW_GENERATE_HIGHLIGHT_MS);
  },
);

onBeforeUnmount(() => {
  window.clearTimeout(highlightTimer);
});

onMounted(() => {
  void nextTick(() => fitCanvasView("auto"));
});

watch(
  () => props.graph.nodes.length,
  (nodeCount, previousNodeCount) => {
    if (nodeCount <= previousNodeCount) return;
    if (hasUserAdjustedViewport.value) return;
    void nextTick(() => fitCanvasView("auto"));
  },
);

const flowNodes = computed(() =>
  props.graph.nodes.map((node) => ({
    id: node.id,
    type: "workflow",
    position: node.position,
    draggable: !props.lockInteraction,
    selectable: true,
    data: {
      label: node.label,
      nodeType: node.type,
      // One handle per side only — branches share the same exit point.
      ports: visiblePortsForNode(node),
      selected: props.selectedNodeId === node.id,
    },
  })),
);

const miniMapNodes = computed(() => {
  if (!props.graph.nodes.length) return [];
  const xs = props.graph.nodes.map((node) => node.position.x);
  const ys = props.graph.nodes.map((node) => node.position.y);
  const minX = Math.min(...xs);
  const minY = Math.min(...ys);
  const width = Math.max(1, Math.max(...xs) - minX + 180);
  const height = Math.max(1, Math.max(...ys) - minY + 72);
  return props.graph.nodes.map((node) => ({
    id: node.id,
    left: `${((node.position.x - minX) / width) * 88 + 4}%`,
    top: `${((node.position.y - minY) / height) * 78 + 8}%`,
    selected: node.id === props.selectedNodeId,
  }));
});

/** At most one input (left) + one output (right) handle per node. */
function visiblePortsForNode(node: WorkflowGraphDraft["nodes"][number]) {
  const ports = Array.isArray(node.ports) ? node.ports : [];
  const input = ports.find((port) => port.direction === "input");
  const output = ports.find((port) => port.direction === "output");
  return [
    ...(input ? [{ key: input.key || "input", label: input.label || "Input", direction: "input" as const }] : []),
    ...(output ? [{ key: output.key || "output", label: output.label || "Output", direction: "output" as const }] : []),
  ];
}

const flowEdges = computed(() => {
  const nodes = props.graph.nodes;
  const posById = new Map(nodes.map((node) => [node.id, node.position]));

  // Count concurrent inbound edges per target so merges can fan in (mergeSlot).
  const inboundCount = new Map<string, number>();
  const inboundIndex = new Map<string, number>();
  for (const edge of props.graph.edges) {
    inboundCount.set(edge.targetNodeId, (inboundCount.get(edge.targetNodeId) || 0) + 1);
  }

  // Stable detour side for same-lane multi-hops (skip-over-nodes).
  let sameLaneDetourIndex = 0;

  return props.graph.edges.map((edge) => {
    const sourcePos = posById.get(edge.sourceNodeId);
    const targetPos = posById.get(edge.targetNodeId);
    const sx = sourcePos?.x ?? 0;
    const sy = sourcePos?.y ?? 0;
    const tx = targetPos?.x ?? 0;
    const ty = targetPos?.y ?? 0;
    const sameLane = Math.abs(sy - ty) <= SAME_LANE_EPS;

    // Does this edge fly over other nodes on the same lane? If so, detour around them.
    let detourSign = 0;
    if (sameLane) {
      const lo = Math.min(sx, tx) + 60;
      const hi = Math.max(sx, tx) - 60;
      const hopsOverNodes = nodes.some((node) => {
        if (node.id === edge.sourceNodeId || node.id === edge.targetNodeId) return false;
        const nx = node.position?.x ?? 0;
        const ny = node.position?.y ?? 0;
        return Math.abs(ny - sy) <= SAME_LANE_EPS && nx > lo && nx < hi;
      });
      const longSpan = Math.abs(tx - sx) >= LONG_EDGE_MIN_DX;
      if (hopsOverNodes || longSpan) {
        // rejected → prefer below; default / others → above first, then alternate.
        const branch = typeof edge.data?.branch === "string" ? edge.data.branch.toLowerCase() : "";
        if (branch.includes("reject") || branch === "false" || branch === "default") {
          detourSign = branch.includes("reject") ? 1 : -1;
        } else {
          detourSign = sameLaneDetourIndex % 2 === 0 ? 1 : -1;
        }
        sameLaneDetourIndex += 1;
      }
    }

    const mergeSlot = (inboundCount.get(edge.targetNodeId) || 0) > 1 ? inboundIndex.get(edge.targetNodeId) || 0 : 0;
    if ((inboundCount.get(edge.targetNodeId) || 0) > 1) {
      inboundIndex.set(edge.targetNodeId, mergeSlot + 1);
    }

    const sourceNode = nodes.find((node) => node.id === edge.sourceNodeId);
    const targetNode = nodes.find((node) => node.id === edge.targetNodeId);
    // All outgoing / incoming edges share one geometric handle (branch is on edge data).
    const sourceHandle = primaryPortKey(sourceNode?.ports, "output");
    const targetHandle = primaryPortKey(targetNode?.ports, "input");

    return {
      id: edge.id,
      type: "workflow",
      source: edge.sourceNodeId,
      target: edge.targetNodeId,
      sourceHandle,
      targetHandle,
      label: branchLabel(edge.data?.branch),
      class: props.selectedEdgeId === edge.id ? "selected" : undefined,
      data: {
        sameLane,
        detourSign,
        mergeSlot,
      },
      style: {
        stroke: props.selectedEdgeId === edge.id ? "#0f766e" : "#64748b",
        strokeWidth: props.selectedEdgeId === edge.id ? 2.5 : 2,
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: props.selectedEdgeId === edge.id ? "#0f766e" : "#64748b",
      },
      animated: false,
      zIndex: 0,
    };
  });
});

/** Per-render path cache so template can read path + label coords once per edge. */
const edgePathMemo = new WeakMap<object, { path: string; labelX: number; labelY: number; key: string }>();

function edgePathCache(edgeProps: EdgeProps) {
  const sameLane = Boolean(edgeProps.data?.sameLane);
  const detourSign = Number(edgeProps.data?.detourSign || 0);
  const mergeSlot = Number(edgeProps.data?.mergeSlot || 0);
  const key = [
    edgeProps.sourceX,
    edgeProps.sourceY,
    edgeProps.targetX,
    edgeProps.targetY,
    sameLane ? 1 : 0,
    detourSign,
    mergeSlot,
  ].join("|");

  const cached = edgePathMemo.get(edgeProps);
  if (cached && cached.key === key) {
    return cached;
  }

  const built = buildFlowchartEdgePath({
    sourceX: edgeProps.sourceX,
    sourceY: edgeProps.sourceY,
    targetX: edgeProps.targetX,
    targetY: edgeProps.targetY,
    sameLane,
    detourSign: detourSign || undefined,
    mergeSlot,
  });

  const result = { ...built, key };
  edgePathMemo.set(edgeProps, result);
  return result;
}

function branchLabel(branch: unknown) {
  if (typeof branch !== "string" || !branch) {
    return undefined;
  }
  return getWorkflowBranchOptions().find((option) => option.value === branch)?.label || branch;
}

function handleNodeDragStop(event: NodeDragEvent) {
  if (props.lockInteraction || !event.node.id || !event.node.position) {
    return;
  }

  emit("update-node-position", { nodeId: event.node.id, position: event.node.position });
}

function handleViewportChangeEnd(viewport: ViewportTransform) {
  currentZoom.value = viewport.zoom;
  emit("update-viewport", {
    x: viewport.x,
    y: viewport.y,
    zoom: viewport.zoom,
  });
  if (!isProgrammaticViewportMove.value) {
    hasUserAdjustedViewport.value = true;
  }
}

function handleViewportMoveStart() {
  if (!isProgrammaticViewportMove.value) {
    hasUserAdjustedViewport.value = true;
  }
}

function handleConnect(connection: {
  source?: string | null;
  sourceHandle?: string | null;
  target?: string | null;
  targetHandle?: string | null;
}) {
  if (props.lockInteraction) {
    return;
  }
  if (!connection.source || !connection.target || !connection.sourceHandle || !connection.targetHandle) {
    return;
  }

  const sourceNode = props.graph.nodes.find((node) => node.id === connection.source);
  const targetNode = props.graph.nodes.find((node) => node.id === connection.target);
  // Connections always land on the single shared output / input of each node.
  const sourcePort = primaryPortKey(sourceNode?.ports, "output");
  const targetPort = primaryPortKey(targetNode?.ports, "input");
  const sourcePortExists = (sourceNode?.ports || []).some((port) => port.direction === "output");
  const targetPortExists = (targetNode?.ports || []).some((port) => port.direction === "input");

  if (!sourcePortExists || !targetPortExists) {
    return;
  }

  emit("connect-nodes", {
    sourceNodeId: connection.source,
    sourcePort,
    targetNodeId: connection.target,
    targetPort,
  });
}

function pointerPosition(event: { clientX?: number; clientY?: number } | undefined) {
  return {
    x: Math.round(event?.clientX ?? 0),
    y: Math.round(event?.clientY ?? 0),
  };
}

function handleEdgeClick(...args: unknown[]) {
  const payload = args[0];
  const maybePayloadEdge =
    payload && typeof payload === "object" && "edge" in payload
      ? (payload as { edge?: { id?: string } }).edge
      : undefined;
  const maybeDirectEdge =
    payload && typeof payload === "object" && "id" in (payload as Record<string, unknown>)
      ? (payload as { id?: string })
      : undefined;
  const maybeEdge =
    args[1] && typeof args[1] === "object" && "id" in (args[1] as Record<string, unknown>)
      ? (args[1] as { id?: string })
      : undefined;
  const edgeId = maybePayloadEdge?.id || maybeDirectEdge?.id || maybeEdge?.id;

  if (!edgeId) {
    return;
  }

  emit("select-edge", edgeId);
}

function handleNodeContextMenu(nodeId: string, event: MouseEvent) {
  if (props.lockInteraction) {
    return;
  }
  emit("open-node-context-menu", {
    nodeId,
    position: pointerPosition(event),
  });
}

function handleEdgeContextMenu(payload: unknown) {
  if (props.lockInteraction) {
    return;
  }
  const maybePayload =
    payload && typeof payload === "object"
      ? (payload as {
          edge?: { id?: string };
          event?: { clientX?: number; clientY?: number; preventDefault?: () => void };
        })
      : undefined;
  const edgeId = maybePayload?.edge?.id;

  if (!edgeId) {
    return;
  }

  maybePayload?.event?.preventDefault?.();

  emit("open-edge-context-menu", {
    edgeId,
    position: pointerPosition(maybePayload.event),
  });
}

function zoomInCanvas() {
  hasUserAdjustedViewport.value = true;
  void zoomIn();
}

function zoomOutCanvas() {
  hasUserAdjustedViewport.value = true;
  void zoomOut();
}

function fitCanvasView(source: "auto" | "user" = "user") {
  if (source === "user") {
    hasUserAdjustedViewport.value = true;
  } else {
    isProgrammaticViewportMove.value = true;
  }

  void Promise.resolve(fitView({ padding: 0.2 })).finally(() => {
    if (source === "auto") {
      window.setTimeout(() => {
        isProgrammaticViewportMove.value = false;
      }, 0);
    }
  });
}
</script>

<template>
  <section class="workflow-graph-canvas workflow-graph-canvas-grid" :class="{ 'is-apply-highlight': highlightActive }">
    <div v-if="showEmptyCanvas" class="workflow-graph-empty">
      <i class="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />
      <strong>{{ t("workflow.generateEmptyCanvasTitle") }}</strong>
    </div>
    <div
      v-if="props.generating"
      class="workflow-graph-generating"
      role="status"
      :aria-label="t('workflow.generateOverlayAria')"
    >
      <span class="workflow-editor-dirty-pill">{{ t("workflow.generateInProgress") }}</span>
    </div>
    <VueFlow
      :nodes="flowNodes"
      :edges="flowEdges"
      :default-viewport="props.graph.viewport"
      :fit-view-on-init="true"
      :nodes-connectable="!props.lockInteraction"
      :nodes-draggable="!props.lockInteraction"
      :elements-selectable="true"
      :zoom-on-scroll="true"
      :pan-on-drag="true"
      :connection-mode="ConnectionMode.Loose"
      class="workflow-flow"
      @node-drag-stop="handleNodeDragStop"
      @move-start="handleViewportMoveStart"
      @viewport-change-end="handleViewportChangeEnd"
      @connect="handleConnect"
      @edge-click="handleEdgeClick"
      @edge-context-menu="handleEdgeContextMenu"
    >
      <template #edge-workflow="edgeProps">
        <BaseEdge
          :id="edgeProps.id"
          :path="edgePathCache(edgeProps).path"
          :marker-end="edgeProps.markerEnd"
          :style="edgeProps.style"
          :interaction-width="20"
        />
        <EdgeLabelRenderer v-if="edgeProps.label">
          <div
            class="workflow-flow-edge-label nodrag nopan"
            :class="{ selected: props.selectedEdgeId === edgeProps.id }"
            :style="{
              transform: `translate(-50%, -50%) translate(${edgePathCache(edgeProps).labelX}px, ${edgePathCache(edgeProps).labelY}px)`,
            }"
          >
            {{ edgeProps.label }}
          </div>
        </EdgeLabelRenderer>
      </template>
      <template #node-workflow="nodeProps">
        <button
          class="workflow-flow-node"
          :class="{ selected: props.selectedNodeId === nodeProps.id }"
          :data-node-id="nodeProps.id"
          type="button"
          @click.stop="emit('select-node', nodeProps.id)"
          @contextmenu.prevent.stop="handleNodeContextMenu(nodeProps.id, $event)"
        >
          <Handle
            v-for="port in nodeProps.data.ports || []"
            :id="port.key"
            :key="port.key"
            :type="port.direction === 'input' ? 'target' : 'source'"
            :position="port.direction === 'input' ? Position.Left : Position.Right"
            :aria-label="
              t('workflow.portAria', {
                label: nodeProps.data.label,
                direction: port.direction === 'input' ? t('workflow.portInput') : t('workflow.portOutput'),
              })
            "
            :title="port.direction === 'input' ? t('workflow.portInput') : t('workflow.portOutputShared')"
            class="workflow-flow-handle"
            :class="port.direction"
          />
          <span class="workflow-flow-node-type">{{ nodeProps.data.nodeType }}</span>
          <strong>{{ nodeProps.data.label }}</strong>
          <small>{{ nodeProps.id }}</small>
        </button>
      </template>
      <div class="workflow-flow-controls" role="group" :aria-label="t('workflow.canvasZoomAria')">
        <span class="workflow-flow-scale">{{ Math.round(currentZoom * 100) }}%</span>
        <button type="button" :aria-label="t('workflow.zoomIn')" @click.stop="zoomInCanvas">
          <i class="fa-solid fa-plus" aria-hidden="true" />
        </button>
        <button type="button" :aria-label="t('workflow.zoomOut')" @click.stop="zoomOutCanvas">
          <i class="fa-solid fa-minus" aria-hidden="true" />
        </button>
        <button
          class="workflow-fit-all-button"
          type="button"
          :aria-label="t('workflow.fitAllNodes')"
          @click.stop="fitCanvasView()"
        >
          <i class="fa-solid fa-compress" aria-hidden="true" />
          <span>{{ t("workflow.fitAllNodes") }}</span>
        </button>
        <span class="workflow-flow-node-count">{{ t("workflow.nodeCount", { n: props.graph.nodes.length }) }}</span>
      </div>
      <button
        class="workflow-flow-minimap"
        type="button"
        :aria-label="t('workflow.minimapFitAria')"
        @click.stop="fitCanvasView()"
      >
        <span
          v-for="node in miniMapNodes"
          :key="node.id"
          :class="{ selected: node.selected }"
          :style="{ left: node.left, top: node.top }"
        />
      </button>
    </VueFlow>
  </section>
</template>
