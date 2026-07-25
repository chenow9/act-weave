import type { WorkflowGraphDraft, WorkflowGraphNode } from "../../types/domain";

const LAYOUT_START_X = 120;
const LAYOUT_CENTER_Y = 320;
/** Clear horizontal gap between successive process steps (column pitch). */
const LAYOUT_COLUMN_GAP = 280;
/** Clear vertical gap between parallel lanes (row pitch). */
const LAYOUT_ROW_GAP = 200;
const NODE_MIN_WIDTH = 190;
const NODE_MIN_HEIGHT = 96;
const BOX_GAP_X = 40;
const BOX_GAP_Y = 28;
const OVERLAP_THRESHOLD = 96;

/**
 * Classic left-to-right process layout.
 *
 * Visual goals (user: 正常流程图 / 间距明确 / 上下均匀 / 关系简洁):
 * 1. Longest happy-path is one straight horizontal spine
 * 2. Side branches sit on parallel lanes with equal row spacing
 * 3. Same column pitch everywhere
 * 4. Join / End stay on the spine so the main story never zigzags
 */
export function autoLayoutWorkflowGraph(graph: WorkflowGraphDraft): WorkflowGraphDraft {
  if (!graph.nodes.length) {
    return graph;
  }

  const outgoing = buildAdjacency(graph, "out");
  const incoming = buildAdjacency(graph, "in");
  const layerById = buildLayers(graph, outgoing, incoming);
  const spine = findSpine(graph, outgoing, incoming);
  const spineSet = new Set(spine);
  const trackById = assignTracks(graph, spine, spineSet, outgoing, incoming, layerById);

  const maxLayer = Math.max(0, ...[...layerById.values()]);
  const positions = new Map<string, { x: number; y: number }>();

  for (const node of graph.nodes) {
    const layer = layerById.get(node.id) ?? 0;
    const track = trackById.get(node.id) ?? 0;
    positions.set(node.id, {
      x: LAYOUT_START_X + layer * LAYOUT_COLUMN_GAP,
      y: LAYOUT_CENTER_Y + track * LAYOUT_ROW_GAP,
    });
  }

  let orphanCol = maxLayer + 1;
  for (const node of graph.nodes) {
    if (layerById.has(node.id)) continue;
    positions.set(node.id, {
      x: LAYOUT_START_X + orphanCol * LAYOUT_COLUMN_GAP,
      y: LAYOUT_CENTER_Y,
    });
    orphanCol += 1;
  }

  const separated = separateOverlaps(graph.nodes, positions, trackById);
  const snapped = snapTracks(separated, trackById);
  const normalized = normalizeOrigin(snapped);

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

  for (let i = 0; i < nodes.length; i += 1) {
    const a = nodes[i];
    const ax = a.position?.x ?? 0;
    const ay = a.position?.y ?? 0;
    for (let j = i + 1; j < nodes.length; j += 1) {
      const b = nodes[j];
      const bx = b.position?.x ?? 0;
      const by = b.position?.y ?? 0;
      if (Math.hypot(ax - bx, ay - by) < OVERLAP_THRESHOLD) {
        return true;
      }
    }
  }
  return false;
}

export function layoutWorkflowGraphIfNeeded(graph: WorkflowGraphDraft): WorkflowGraphDraft {
  return workflowGraphNeedsLayout(graph) ? autoLayoutWorkflowGraph(graph) : graph;
}

// --- spine / tracks ----------------------------------------------------------

function findSpine(
  graph: WorkflowGraphDraft,
  outgoing: Map<string, string[]>,
  incoming: Map<string, string[]>,
): string[] {
  const starts = graph.nodes
    .filter((n) => n.type === "Start" || (incoming.get(n.id) || []).length === 0)
    .map((n) => n.id)
    .sort((a, b) => preferredRootOrder(graph, a) - preferredRootOrder(graph, b) || a.localeCompare(b));

  const ends = new Set(
    graph.nodes.filter((n) => n.type === "End" || (outgoing.get(n.id) || []).length === 0).map((n) => n.id),
  );

  let best: string[] = [];
  for (const start of starts.length ? starts : graph.nodes.map((n) => n.id)) {
    const path = dfsLongest(start, outgoing, ends, new Set());
    if (path.length > best.length) best = path;
  }
  if (!best.length && graph.nodes.length) best = [graph.nodes[0].id];
  return best;
}

function dfsLongest(
  nodeId: string,
  outgoing: Map<string, string[]>,
  ends: Set<string>,
  visiting: Set<string>,
): string[] {
  if (visiting.has(nodeId)) return [nodeId];
  visiting.add(nodeId);
  const nexts = (outgoing.get(nodeId) || []).slice().sort((a, b) => a.localeCompare(b));
  let bestSuffix: string[] = [];
  for (const next of nexts) {
    const suffix = dfsLongest(next, outgoing, ends, visiting);
    if (suffix.length > bestSuffix.length) bestSuffix = suffix;
  }
  visiting.delete(nodeId);
  return [nodeId, ...bestSuffix];
}

/**
 * Track 0 = main spine.
 * Side branches: first free lane above (-1), then below (+1), alternating —
 * keeps a single side-branch diagram “top branch + main line” which reads naturally.
 */
function assignTracks(
  graph: WorkflowGraphDraft,
  spine: string[],
  spineSet: Set<string>,
  outgoing: Map<string, string[]>,
  incoming: Map<string, string[]>,
  layerById: Map<string, number>,
): Map<string, number> {
  const track = new Map<string, number>();
  for (const id of spine) track.set(id, 0);

  const queue = [...spine];
  const seen = new Set(spine);
  let above = 0;
  let below = 0;

  while (queue.length) {
    const id = queue.shift()!;
    const parentTrack = track.get(id) ?? 0;
    const children = (outgoing.get(id) || []).slice().sort((a, b) => {
      // Spine child first so the happy path keeps track 0.
      const as = spineSet.has(a) ? 0 : 1;
      const bs = spineSet.has(b) ? 0 : 1;
      if (as !== bs) return as - bs;
      // Prefer "true" / approved-looking ids first is handled by spine; remaining stable by id.
      return a.localeCompare(b);
    });

    const nonSpineChildren = children.filter((c) => !spineSet.has(c));

    for (const child of children) {
      if (track.has(child)) {
        if (!seen.has(child)) {
          seen.add(child);
          queue.push(child);
        }
        continue;
      }

      if (spineSet.has(child)) {
        track.set(child, 0);
      } else if (children.length === 1) {
        track.set(child, parentTrack);
      } else if (nonSpineChildren.includes(child) && children.some((c) => spineSet.has(c))) {
        // Spine continues + N side branches: stack sides above first, then below.
        const sideIndex = nonSpineChildren.indexOf(child);
        if (sideIndex % 2 === 0) {
          above += 1;
          track.set(child, parentTrack === 0 ? -above : parentTrack - 1);
        } else {
          below += 1;
          track.set(child, parentTrack === 0 ? below : parentTrack + 1);
        }
      } else {
        // Pure multi-way fork with no spine child among them.
        const forkIndex = nonSpineChildren.indexOf(child);
        if (forkIndex % 2 === 0) {
          above += 1;
          track.set(child, -above);
        } else {
          below += 1;
          track.set(child, below);
        }
      }

      if (!seen.has(child)) {
        seen.add(child);
        queue.push(child);
      }
    }
  }

  for (const node of graph.nodes) {
    if (track.has(node.id)) continue;
    const preds = (incoming.get(node.id) || []).filter((p) => track.has(p));
    if (preds.length) {
      const avg = preds.reduce((s, p) => s + (track.get(p) || 0), 0) / preds.length;
      track.set(node.id, Math.round(avg));
    } else {
      below += 1;
      track.set(node.id, below);
    }
  }

  // End + multi-in joins sit on the spine (main story line).
  for (const node of graph.nodes) {
    if (node.type === "End" || spineSet.has(node.id)) {
      track.set(node.id, 0);
      continue;
    }
    const preds = (incoming.get(node.id) || []).filter((p) => track.has(p));
    if (preds.length >= 2) {
      const tracks = new Set(preds.map((p) => track.get(p)));
      if (tracks.size >= 2) track.set(node.id, 0);
    }
  }

  // Straighten linear side chains onto parent track.
  const byLayer = [...graph.nodes].sort(
    (a, b) => (layerById.get(a.id) ?? 0) - (layerById.get(b.id) ?? 0) || a.id.localeCompare(b.id),
  );
  for (const node of byLayer) {
    if (spineSet.has(node.id) || node.type === "End") continue;
    const preds = (incoming.get(node.id) || []).filter((p) => track.has(p));
    if (preds.length !== 1) continue;
    const pred = preds[0];
    const successors = outgoing.get(pred) || [];
    if (successors.length === 1 && successors[0] === node.id) {
      track.set(node.id, track.get(pred) || 0);
    }
  }

  return track;
}

// --- layering ----------------------------------------------------------------

function buildLayers(
  graph: WorkflowGraphDraft,
  outgoing: Map<string, string[]>,
  incoming: Map<string, string[]>,
): Map<string, number> {
  const nodeIds = new Set(graph.nodes.map((n) => n.id));
  const indegree = new Map<string, number>();
  for (const id of nodeIds) indegree.set(id, (incoming.get(id) || []).length);

  const layer = new Map<string, number>();
  const queue: string[] = [];
  const remaining = new Map(indegree);

  for (const [id, deg] of remaining) {
    if (deg === 0) {
      queue.push(id);
      layer.set(id, 0);
    }
  }
  queue.sort((a, b) => preferredRootOrder(graph, a) - preferredRootOrder(graph, b) || a.localeCompare(b));

  while (queue.length > 0) {
    const id = queue.shift()!;
    const depth = layer.get(id) || 0;
    for (const nextId of outgoing.get(id) || []) {
      const candidate = depth + 1;
      if ((layer.get(nextId) ?? -1) < candidate) layer.set(nextId, candidate);
      const left = (remaining.get(nextId) || 0) - 1;
      remaining.set(nextId, left);
      if (left === 0) queue.push(nextId);
    }
  }

  let fallback = 0;
  for (const node of graph.nodes) {
    if (!layer.has(node.id)) {
      layer.set(node.id, fallback);
      fallback += 1;
    }
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

function separateOverlaps(
  nodes: WorkflowGraphNode[],
  positions: Map<string, { x: number; y: number }>,
  trackById: Map<string, number>,
): Map<string, { x: number; y: number }> {
  const next = new Map(positions);
  const ids = nodes.map((n) => n.id);
  for (let sweep = 0; sweep < 6; sweep += 1) {
    let moved = false;
    for (let i = 0; i < ids.length; i += 1) {
      for (let j = i + 1; j < ids.length; j += 1) {
        const aId = ids[i];
        const bId = ids[j];
        const a = next.get(aId)!;
        const b = next.get(bId)!;
        const dx = b.x - a.x;
        const dy = b.y - a.y;
        const minDx = NODE_MIN_WIDTH + BOX_GAP_X;
        const minDy = NODE_MIN_HEIGHT + BOX_GAP_Y;
        if (Math.abs(dx) >= minDx || Math.abs(dy) >= minDy) continue;

        const sameTrack = (trackById.get(aId) ?? 0) === (trackById.get(bId) ?? 0);
        if (sameTrack || Math.abs(dx) < minDx) {
          const push = (minDx - Math.abs(dx || 0.1)) / 2 + 1;
          const sign = dx === 0 ? (aId < bId ? 1 : -1) : Math.sign(dx);
          next.set(aId, { x: a.x - sign * push, y: a.y });
          next.set(bId, { x: b.x + sign * push, y: b.y });
        } else {
          const push = (minDy - Math.abs(dy || 0.1)) / 2 + 1;
          const sign = dy === 0 ? (aId < bId ? 1 : -1) : Math.sign(dy);
          next.set(aId, { x: a.x, y: a.y - sign * push });
          next.set(bId, { x: b.x, y: b.y + sign * push });
        }
        moved = true;
      }
    }
    if (!moved) break;
  }
  return next;
}

function snapTracks(
  positions: Map<string, { x: number; y: number }>,
  trackById: Map<string, number>,
): Map<string, { x: number; y: number }> {
  const used = new Set<number>();
  for (const t of trackById.values()) used.add(t);

  // Uniform rails relative to spine (track 0).
  const trackY = new Map<number, number>();
  trackY.set(0, LAYOUT_CENTER_Y);
  for (const t of used) {
    if (t === 0) continue;
    trackY.set(t, LAYOUT_CENTER_Y + t * LAYOUT_ROW_GAP);
  }

  const next = new Map<string, { x: number; y: number }>();
  for (const [id, pos] of positions) {
    const t = trackById.get(id) ?? 0;
    next.set(id, { x: pos.x, y: trackY.get(t) ?? pos.y });
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
  // Center the whole diagram vertically so top/bottom padding looks even.
  const midY = (minY + maxY) / 2;
  const shiftY = LAYOUT_CENTER_Y - midY;
  for (const [id, pos] of next) {
    next.set(id, { x: Math.round(pos.x + shiftX), y: Math.round(pos.y + shiftY) });
  }
  return next;
}
