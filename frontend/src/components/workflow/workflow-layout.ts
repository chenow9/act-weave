import type { WorkflowGraphDraft, WorkflowGraphNode } from "../../types/domain";

/**
 * Dify-inspired stage layout:
 * - Compact spacing inside linear stages
 * - Wider gaps at forks / stage boundaries
 * - Success continues right on the main rail
 * - Side / failure / loop tracks go BELOW (positive Y), never above the main story
 */
const LAYOUT_START_X = 48;
const LAYOUT_CENTER_Y = 260;
/**
 * Column pitch = left-edge distance between successive layers.
 * MUST exceed the visual card width (~180) so right-port of node N is left of
 * left-port of node N+1; otherwise every sequential edge is mis-drawn as a loop.
 */
const COLUMN_GAP_TIGHT = 240; // 180 card + 60 clear gap
/** Wider pitch after a fork / stage boundary. */
const COLUMN_GAP_STAGE = 280;
const ROW_GAP = 176;
const NODE_MIN_WIDTH = 180;
const NODE_MIN_HEIGHT = 92;
const BOX_GAP_X = 28;
const BOX_GAP_Y = 28;
const OVERLAP_THRESHOLD = 80;

export function autoLayoutWorkflowGraph(graph: WorkflowGraphDraft): WorkflowGraphDraft {
  if (!graph.nodes.length) {
    return graph;
  }

  const outgoing = buildAdjacency(graph, "out");
  const incoming = buildAdjacency(graph, "in");
  const layerById = buildLayers(graph, outgoing, incoming);
  const trackById = assignBranchTracks(graph, layerById, outgoing, incoming);
  const orderByLayer = orderNodesInLayers(graph, layerById, trackById);
  const positions = packLayerPositions(graph, layerById, orderByLayer, trackById, outgoing);

  // Resolve Y collisions only within a column; never drift X off the layer grid.
  const separated = separateOverlapsInColumns(graph.nodes, positions, layerById);
  const snappedX = snapToLayerColumns(separated, layerById, buildLayerXs(layerById, orderByLayer, outgoing));
  const normalized = normalizeOrigin(snappedX);

  return {
    ...graph,
    nodes: graph.nodes.map((node) => ({
      ...node,
      position: normalized.get(node.id) || { x: LAYOUT_START_X, y: LAYOUT_CENTER_Y },
    })),
  };
}

export function workflowGraphNeedsLayout(graph: WorkflowGraphDraft): boolean {
  const nodes = graph.nodes || [];
  if (nodes.length <= 1) return false;

  const allZeroOrMissing = nodes.every((n) => !n.position || (n.position.x === 0 && n.position.y === 0));
  if (allZeroOrMissing) return true;

  if (nodes.length >= 3) {
    const ys = nodes.map((n) => n.position?.y ?? 0);
    if (Math.max(...ys) - Math.min(...ys) < 24) {
      const out = buildAdjacency(graph, "out");
      if ([...out.values()].some((targets) => targets.length > 1)) return true;
    }
  }

  for (let i = 0; i < nodes.length; i += 1) {
    const a = nodes[i];
    const ax = a.position?.x ?? 0;
    const ay = a.position?.y ?? 0;
    for (let j = i + 1; j < nodes.length; j += 1) {
      const b = nodes[j];
      const bx = b.position?.x ?? 0;
      const by = b.position?.y ?? 0;
      if (Math.hypot(ax - bx, ay - by) < OVERLAP_THRESHOLD) return true;
    }
  }
  return false;
}

export function layoutWorkflowGraphIfNeeded(graph: WorkflowGraphDraft): WorkflowGraphDraft {
  return workflowGraphNeedsLayout(graph) ? autoLayoutWorkflowGraph(graph) : graph;
}

// --- branch tracks (success right, failure/loop BELOW) -----------------------

function assignBranchTracks(
  graph: WorkflowGraphDraft,
  layerById: Map<string, number>,
  outgoing: Map<string, string[]>,
  incoming: Map<string, string[]>,
): Map<string, number> {
  const track = new Map<string, number>();
  const byLayerAsc = [...graph.nodes].sort(
    (a, b) => (layerById.get(a.id) ?? 0) - (layerById.get(b.id) ?? 0) || a.id.localeCompare(b.id),
  );

  for (const node of byLayerAsc) {
    if (track.has(node.id)) continue;
    const preds = (incoming.get(node.id) || []).filter(
      (p) => (layerById.get(p) ?? 0) < (layerById.get(node.id) ?? 0),
    );
    if (!preds.length || node.type === "Start") {
      track.set(node.id, 0);
      continue;
    }
    const predTracks = preds.map((p) => track.get(p) ?? 0).sort((a, b) => a - b);
    track.set(node.id, predTracks[Math.floor(predTracks.length / 2)] ?? 0);
  }

  for (const node of byLayerAsc) {
    const parentLayer = layerById.get(node.id) ?? 0;
    const parentId = node.id;
    const forwardChildren = (outgoing.get(node.id) || [])
      .filter((c) => (layerById.get(c) ?? 0) > parentLayer)
      .sort(
        (a, b) =>
          branchPriority(graph, a, parentId) - branchPriority(graph, b, parentId) ||
          a.localeCompare(b),
      );
    if (forwardChildren.length < 2) continue;

    const parentTrack = track.get(node.id) ?? 0;
    // Main (success) stays on parent track; others stack BELOW: +1, +2, …
    // Priority 0 = success/main, higher = failure / secondary.
    forwardChildren.forEach((child, index) => {
      const t = parentTrack + index; // index 0 main, 1+ below
      track.set(child, t);
      propagateTrackAlongChain(child, t, layerById, outgoing, incoming, track);
    });
  }

  // Joins: median of forward preds — but never pull a pure-success tooling
  // path down just because failure also merges into End.
  for (const node of byLayerAsc) {
    if (node.type === "End") {
      // End sits on the main rail so the happy path stays left→right readable.
      track.set(node.id, 0);
      continue;
    }
    const preds = (incoming.get(node.id) || []).filter(
      (p) => (layerById.get(p) ?? 0) < (layerById.get(node.id) ?? 0),
    );
    if (preds.length < 2) continue;
    const ts = preds.map((p) => track.get(p) ?? 0).sort((a, b) => a - b);
    track.set(node.id, ts[Math.floor(ts.length / 2)] ?? 0);
  }

  for (const node of graph.nodes) {
    if (node.type === "Start") track.set(node.id, 0);
  }

  return track;
}

/** Lower = prefer as main success path (continue right on spine). */
function branchPriority(graph: WorkflowGraphDraft, nodeId: string, parentId?: string): number {
  // Edge branch label is the strongest signal (true/completed vs default/reject/failed).
  if (parentId) {
    const edge = graph.edges.find((e) => e.sourceNodeId === parentId && e.targetNodeId === nodeId);
    const raw = edge?.data && typeof edge.data === "object" ? (edge.data as Record<string, unknown>) : {};
    const branch = String(raw.branch ?? raw.label ?? "").toLowerCase().trim();
    if (branch) {
      if (/^(true|completed|success|ok|yes|pass|done|已完成|成功)/.test(branch) || branch === "true") {
        return 0;
      }
      if (
        /^(false|default|reject|fail|failed|running|timeout|error|no|else|其他|失败|超时|处理中|驳回)/.test(
          branch,
        )
      ) {
        return 40;
      }
    }
  }

  const node = graph.nodes.find((n) => n.id === nodeId);
  if (!node) return 50;
  const id = nodeId.toLowerCase();
  const label = (node.label || "").toLowerCase();
  if (/reject|fail|驳回|失败|超时/.test(id) || /reject|fail|驳回|失败|超时/.test(label)) {
    return 45;
  }
  // Success tooling (report, transform) stays on main rail.
  if (node.type === "Transform") return 0;
  if (node.type === "Tool" || node.type === "HTTP") {
    if (/report|success|result|word|export/.test(id) || /report|success|result|word/.test(label)) {
      return 0;
    }
    return 5;
  }
  if (node.type === "End") return 20; // failure / early end sits below success tooling
  if (node.type === "Condition") return 10;
  return 15;
}

function propagateTrackAlongChain(
  startId: string,
  trackValue: number,
  layerById: Map<string, number>,
  outgoing: Map<string, string[]>,
  incoming: Map<string, string[]>,
  track: Map<string, number>,
) {
  let cursor = startId;
  const seen = new Set<string>([startId]);
  while (true) {
    const nexts = (outgoing.get(cursor) || []).filter(
      (n) => (layerById.get(n) ?? 0) > (layerById.get(cursor) ?? 0) && !seen.has(n),
    );
    if (nexts.length !== 1) break;
    const only = nexts[0];
    const ins = (incoming.get(only) || []).filter((p) => (layerById.get(p) ?? 0) < (layerById.get(only) ?? 0));
    if (ins.length > 1) break;
    track.set(only, trackValue);
    seen.add(only);
    cursor = only;
  }
}

function orderNodesInLayers(
  graph: WorkflowGraphDraft,
  layerById: Map<string, number>,
  trackById: Map<string, number>,
): Map<number, string[]> {
  const maxLayer = Math.max(0, ...layerById.values());
  const byLayer = new Map<number, string[]>();
  for (let L = 0; L <= maxLayer; L += 1) byLayer.set(L, []);
  for (const node of graph.nodes) {
    byLayer.get(layerById.get(node.id) ?? 0)!.push(node.id);
  }
  for (let L = 0; L <= maxLayer; L += 1) {
    const ids = byLayer.get(L) || [];
    ids.sort(
      (a, b) =>
        (trackById.get(a) ?? 0) - (trackById.get(b) ?? 0) ||
        preferredSiblingOrder(graph, a) - preferredSiblingOrder(graph, b) ||
        a.localeCompare(b),
    );
    byLayer.set(L, ids);
  }
  return byLayer;
}

function preferredSiblingOrder(graph: WorkflowGraphDraft, nodeId: string): number {
  const node = graph.nodes.find((n) => n.id === nodeId);
  if (!node) return 50;
  if (node.type === "Start") return 0;
  if (node.type === "Condition" || node.type === "Parallel" || node.type === "ForEach") return 5;
  if (node.type === "Approval") return 10;
  if (node.type === "Tool" || node.type === "HTTP" || node.type === "SubWorkflow") return 20;
  if (node.type === "Transform") return 30;
  if (node.type === "End") return 90;
  return 40;
}

function buildLayerXs(
  layerById: Map<string, number>,
  orderByLayer: Map<number, string[]>,
  outgoing: Map<string, string[]>,
): Map<number, number> {
  const maxLayer = Math.max(0, ...layerById.values());
  const layerX = new Map<number, number>();
  let x = LAYOUT_START_X;
  for (let L = 0; L <= maxLayer; L += 1) {
    layerX.set(L, x);
    const ids = orderByLayer.get(L) || [];
    const isFork = ids.some((id) => {
      const forward = (outgoing.get(id) || []).filter((c) => (layerById.get(c) ?? 0) > L);
      return forward.length > 1;
    });
    const nextHasMany = (orderByLayer.get(L + 1) || []).length > 1;
    x += isFork || nextHasMany ? COLUMN_GAP_STAGE : COLUMN_GAP_TIGHT;
  }
  return layerX;
}

function packLayerPositions(
  graph: WorkflowGraphDraft,
  layerById: Map<string, number>,
  orderByLayer: Map<number, string[]>,
  trackById: Map<string, number>,
  outgoing: Map<string, string[]>,
): Map<string, { x: number; y: number }> {
  const positions = new Map<string, { x: number; y: number }>();
  const maxLayer = Math.max(0, ...layerById.values());
  const layerX = buildLayerXs(layerById, orderByLayer, outgoing);

  // Tracks ≥ 0 stack below the main rail (positive Y).
  const usedTracks = [...new Set([...trackById.values()])].sort((a, b) => a - b);
  const trackIndex = new Map<number, number>();
  usedTracks.forEach((t, i) => trackIndex.set(t, i));

  for (let L = 0; L <= maxLayer; L += 1) {
    const ids = orderByLayer.get(L) || [];
    const colX = layerX.get(L) ?? LAYOUT_START_X;
    for (const id of ids) {
      const t = trackById.get(id) ?? 0;
      const idx = trackIndex.get(t) ?? 0;
      const y = LAYOUT_CENTER_Y + idx * ROW_GAP;
      positions.set(id, { x: colX, y });
    }
  }

  return positions;
}

function snapToLayerColumns(
  positions: Map<string, { x: number; y: number }>,
  layerById: Map<string, number>,
  layerX: Map<number, number>,
): Map<string, { x: number; y: number }> {
  const next = new Map(positions);
  for (const [id, pos] of next) {
    const L = layerById.get(id);
    if (L == null) continue;
    const colX = layerX.get(L);
    if (colX == null) continue;
    next.set(id, { x: colX, y: pos.y });
  }
  return next;
}

// --- layering ----------------------------------------------------------------

function buildLayers(
  graph: WorkflowGraphDraft,
  outgoing: Map<string, string[]>,
  incoming: Map<string, string[]>,
): Map<string, number> {
  const nodeIds = graph.nodes.map((n) => n.id);
  const remaining = new Map<string, number>();
  for (const id of nodeIds) remaining.set(id, (incoming.get(id) || []).length);

  const layer = new Map<string, number>();
  const queue: string[] = [];

  const depthFromPreds = (id: string, fallback: number) => {
    const preds = (incoming.get(id) || []).filter((p) => layer.has(p));
    if (!preds.length) return fallback;
    return Math.max(...preds.map((p) => layer.get(p) || 0)) + 1;
  };

  const place = (id: string, depth: number) => {
    if (layer.has(id)) return;
    layer.set(id, depth);
    remaining.set(id, 0);
    queue.push(id);
  };

  const processQueue = () => {
    while (queue.length > 0) {
      const id = queue.shift()!;
      for (const nextId of outgoing.get(id) || []) {
        if (layer.has(nextId)) {
          remaining.set(nextId, Math.max(0, (remaining.get(nextId) || 0) - 1));
          continue;
        }
        const left = Math.max(0, (remaining.get(nextId) || 0) - 1);
        remaining.set(nextId, left);
        if (left === 0) place(nextId, depthFromPreds(nextId, (layer.get(id) || 0) + 1));
      }
    }
  };

  const roots = nodeIds
    .filter((id) => (remaining.get(id) || 0) === 0)
    .sort((a, b) => preferredRootOrder(graph, a) - preferredRootOrder(graph, b) || a.localeCompare(b));
  for (const id of roots) place(id, 0);
  for (const node of graph.nodes) {
    if (node.type === "Start") place(node.id, 0);
  }
  processQueue();

  let guard = nodeIds.length + 2;
  while (layer.size < nodeIds.length && guard-- > 0) {
    let bestId = "";
    let bestScore = Number.NEGATIVE_INFINITY;
    let bestDepth = 0;
    for (const id of nodeIds) {
      if (layer.has(id)) continue;
      const layeredPreds = (incoming.get(id) || []).filter((p) => layer.has(p));
      const score = layeredPreds.length * 100 - preferredRootOrder(graph, id);
      const depth =
        layeredPreds.length > 0
          ? Math.max(...layeredPreds.map((p) => layer.get(p) || 0)) + 1
          : Math.max(0, ...layer.values(), 0) + 1;
      if (score > bestScore || (score === bestScore && (bestId === "" || depth < bestDepth))) {
        bestId = id;
        bestScore = score;
        bestDepth = depth;
      }
    }
    if (!bestId) break;
    place(bestId, bestDepth);
    processQueue();
  }

  for (const id of nodeIds) {
    if (!layer.has(id)) layer.set(id, Math.max(0, ...layer.values(), 0) + 1);
  }

  for (const node of graph.nodes) {
    if (node.type === "Start") layer.set(node.id, 0);
  }
  const nonEndMax = Math.max(
    0,
    ...graph.nodes.filter((n) => n.type !== "End").map((n) => layer.get(n.id) || 0),
  );
  for (const node of graph.nodes) {
    if (node.type === "End") layer.set(node.id, nonEndMax + 1);
  }

  return layer;
}

function preferredRootOrder(graph: WorkflowGraphDraft, nodeId: string): number {
  const node = graph.nodes.find((n) => n.id === nodeId);
  if (!node) return 50;
  if (node.type === "Start") return 0;
  if (node.type === "ForEach" || node.type === "Parallel") return 1;
  if (node.type === "End") return 99;
  return 10;
}

function buildAdjacency(graph: WorkflowGraphDraft, direction: "in" | "out"): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const node of graph.nodes) map.set(node.id, []);
  for (const edge of graph.edges) {
    if (edge.sourceNodeId === edge.targetNodeId) continue;
    if (!map.has(edge.sourceNodeId) || !map.has(edge.targetNodeId)) continue;
    if (direction === "in") {
      map.set(edge.targetNodeId, [...(map.get(edge.targetNodeId) || []), edge.sourceNodeId]);
    } else {
      map.set(edge.sourceNodeId, [...(map.get(edge.sourceNodeId) || []), edge.targetNodeId]);
    }
  }
  return map;
}

/** Only separate Y within the same layer/column — preserve stage column alignment. */
function separateOverlapsInColumns(
  nodes: WorkflowGraphNode[],
  positions: Map<string, { x: number; y: number }>,
  layerById: Map<string, number>,
): Map<string, { x: number; y: number }> {
  const next = new Map(positions);
  const byLayer = new Map<number, string[]>();
  for (const node of nodes) {
    const L = layerById.get(node.id) ?? 0;
    if (!byLayer.has(L)) byLayer.set(L, []);
    byLayer.get(L)!.push(node.id);
  }

  for (const ids of byLayer.values()) {
    if (ids.length < 2) continue;
    for (let sweep = 0; sweep < 6; sweep += 1) {
      let moved = false;
      const sorted = [...ids].sort((a, b) => (next.get(a)!.y - next.get(b)!.y) || a.localeCompare(b));
      for (let i = 0; i < sorted.length - 1; i += 1) {
        const a = next.get(sorted[i])!;
        const b = next.get(sorted[i + 1])!;
        const minDy = NODE_MIN_HEIGHT + BOX_GAP_Y;
        if (b.y - a.y >= minDy) continue;
        const mid = (a.y + b.y) / 2;
        next.set(sorted[i], { x: a.x, y: mid - minDy / 2 });
        next.set(sorted[i + 1], { x: b.x, y: mid + minDy / 2 });
        moved = true;
      }
      if (!moved) break;
    }
  }
  return next;
}

function normalizeOrigin(positions: Map<string, { x: number; y: number }>): Map<string, { x: number; y: number }> {
  const next = new Map(positions);
  let minX = Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  for (const pos of next.values()) {
    minX = Math.min(minX, pos.x);
    minY = Math.min(minY, pos.y);
    maxY = Math.max(maxY, pos.y);
  }
  const shiftX = LAYOUT_START_X - minX;
  const midY = (minY + maxY) / 2;
  const shiftY = LAYOUT_CENTER_Y - midY;
  for (const [id, pos] of next) {
    next.set(id, { x: Math.round(pos.x + shiftX), y: Math.round(pos.y + shiftY) });
  }
  return next;
}
