/**
 * Smart-DAG edge routing: port-side assignment + orchestration-page path geometry.
 *
 * Geometry reuses `buildFlowchartEdgePath` so same-lane links stay straight
 * (no stub elbows), matching the workflow 编排 canvas.
 */

import {
  SAME_LANE_EPS,
  buildFlowchartEdgePath,
  buildVerticalApproachDetour,
  type FlowchartEdgePath,
} from "./workflow-edge-path";

export type EdgeSide = "left" | "right" | "top" | "bottom";
export type EdgeKind = "sequence" | "branch" | "loop";

export interface NodeRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface EdgeRouteInput {
  from: NodeRect;
  to: NodeRect;
  kind: EdgeKind;
  /** Branch / edge label (used for failure vs success heuristics). */
  label?: string;
  /** Forced exit side (from unique side assignment). */
  outSide?: EdgeSide;
  /** Forced entry side. */
  inSide?: EdgeSide;
}

export interface EdgeAnchors {
  outSide: EdgeSide;
  inSide: EdgeSide;
  start: { x: number; y: number };
  end: { x: number; y: number };
}

export interface BranchPortSpec {
  key: string;
  label: string;
  side: EdgeSide;
}

/** Centre of top/bottom ports along the side. */
const PORT_T = 0.5;
/**
 * Shared horizontal rail for left/right ports (px from node top).
 * Matches the standard tool card mid-line (96/2).
 */
export const MAIN_RAIL_PORT_Y = 48;
/**
 * Distance from the card border to the port centre.
 * Port is 12px (r=6) + ~2px air gap → centre sits 8px outside the border.
 * Must match CSS `.smart-node-port.is-right { right: -14px }` etc.
 */
export const PORT_OUTSET = 8;
/** Visible port radius (12px circle). */
export const PORT_RADIUS = 6;
/**
 * How far past the path end the arrow tip extends (marker local → user space).
 * Path stroke stops at the arrow base; tip reaches the port rim.
 * Must match marker geometry: tip at x=9, refX≈1, markerWidth≈8 → ~6–7px.
 */
export const ARROW_TIP_EXTENT = 6;

export function nodeRectFromSize(x: number, y: number, size: { width: number; height: number }): NodeRect {
  return { x, y, w: size.width, h: size.height };
}

/**
 * Port anchor on a side — always the centre of the visible dot, outside the card.
 * - left / right → shared main rail Y
 * - top / bottom → side midpoint of the card box
 */
export function anchorOnSide(box: NodeRect, side: EdgeSide, _t = PORT_T): { x: number; y: number } {
  const railY = box.y + MAIN_RAIL_PORT_Y;
  switch (side) {
    case "right":
      return { x: box.x + box.w + PORT_OUTSET, y: railY };
    case "left":
      return { x: box.x - PORT_OUTSET, y: railY };
    case "top":
      return { x: box.x + box.w * PORT_T, y: box.y - PORT_OUTSET };
    case "bottom":
      return { x: box.x + box.w * PORT_T, y: box.y + box.h + PORT_OUTSET };
  }
}

/** Move from port centre to the outer rim (away from the node). */
function portRim(center: { x: number; y: number }, side: EdgeSide, radius = PORT_RADIUS) {
  switch (side) {
    case "right":
      return { x: center.x + radius, y: center.y };
    case "left":
      return { x: center.x - radius, y: center.y };
    case "top":
      return { x: center.x, y: center.y - radius };
    case "bottom":
      return { x: center.x, y: center.y + radius };
  }
}

function isFailureLabel(label: string): boolean {
  return /失败|超时|其他|reject|fail|false|error|default|else|驳回/i.test(label);
}

function isSuccessLabel(label: string): boolean {
  return /完成|成功|true|ok|done|pass|yes|已完成/i.test(label);
}

function isLoopLabel(label: string): boolean {
  return /处理中|running|poll|重试|retry|loop|继续/i.test(label);
}

function branchPreferSides(label: string): EdgeSide[] {
  if (isSuccessLabel(label)) return ["right", "bottom", "top"];
  if (isLoopLabel(label)) return ["bottom", "top", "right"];
  if (isFailureLabel(label)) return ["top", "bottom", "right"];
  return ["right", "bottom", "top"];
}

function branchRank(label: string): number {
  if (isSuccessLabel(label)) return 0;
  if (isLoopLabel(label)) return 1;
  if (isFailureLabel(label)) return 2;
  return 3;
}

/**
 * Assign each condition branch a unique exit side.
 * Left is reserved for the single input port.
 */
export function assignUniqueBranchSides(ports: Array<{ key: string; label: string }>): BranchPortSpec[] {
  const free: EdgeSide[] = ["right", "bottom", "top"];
  const used = new Set<EdgeSide>();
  const ranked = ports
    .map((p, index) => ({ ...p, index }))
    .sort((a, b) => branchRank(a.label) - branchRank(b.label) || a.index - b.index || a.key.localeCompare(b.key));

  const byKey = new Map<string, EdgeSide>();
  for (const p of ranked) {
    const preferred = branchPreferSides(p.label);
    const side = preferred.find((s) => !used.has(s) && free.includes(s)) || free.find((s) => !used.has(s)) || "right";
    used.add(side);
    byKey.set(p.key, side);
  }

  return ports.map((p) => ({
    key: p.key,
    label: p.label,
    side: byKey.get(p.key) || "right",
  }));
}

/**
 * Pick exit/entry sides. Anchors are always the single midpoint of that side.
 */
export function resolveEdgeAnchors(input: EdgeRouteInput): EdgeAnchors {
  const { from, to, kind } = input;
  const label = input.label || "";

  const fromCx = from.x + from.w / 2;
  const fromCy = from.y + from.h / 2;
  const toCx = to.x + to.w / 2;
  const toCy = to.y + to.h / 2;
  const dx = toCx - fromCx;
  const dy = toCy - fromCy;

  let outSide: EdgeSide = input.outSide || "right";
  let inSide: EdgeSide = input.inSide || "left";

  if (!input.outSide || !input.inSide) {
    if (kind === "loop" || isLoopLabel(label)) {
      if (!input.outSide) outSide = "bottom";
      if (!input.inSide) inSide = "bottom";
    } else if (kind === "branch" && isFailureLabel(label)) {
      if (!input.outSide) outSide = "top";
      if (!input.inSide) {
        if (toCy > fromCy + from.h * 0.35) inSide = "top";
        else if (dx > 0) inSide = "left";
        else inSide = "top";
      }
    } else if (kind === "branch" && isSuccessLabel(label)) {
      if (!input.outSide) outSide = dx >= -12 ? "right" : "left";
      if (!input.inSide) inSide = dx >= -12 ? "left" : "right";
    } else if (Math.abs(dx) >= Math.abs(dy) * 0.85) {
      if (!input.outSide) outSide = dx >= 0 ? "right" : "left";
      if (!input.inSide) inSide = dx >= 0 ? "left" : "right";
    } else if (dy >= 0) {
      if (!input.outSide) outSide = "bottom";
      if (!input.inSide) inSide = "top";
    } else {
      if (!input.outSide) outSide = "top";
      if (!input.inSide) inSide = "bottom";
    }
  }

  if (input.outSide && !input.inSide) {
    if (kind === "loop" || isLoopLabel(label)) inSide = "bottom";
    else if (outSide === "top") inSide = dx > 0 ? "left" : "top";
    else if (outSide === "bottom") inSide = dx > 0 ? "left" : "bottom";
    else if (outSide === "right") inSide = "left";
    else inSide = "right";
  }

  return {
    outSide,
    inSide,
    start: anchorOnSide(from, outSide),
    end: anchorOnSide(to, inSide),
  };
}

/**
 * Route with the same geometry rules as the workflow 编排 canvas.
 *
 * Path ends at the *arrow base* (outside the port rim). The marker tip is drawn
 * past the path end (refX near triangle base) so it lands on the port rim —
 * dashes never render past the arrow tip.
 */
export function routeEdge(input: EdgeRouteInput): FlowchartEdgePath {
  const anchors = resolveEdgeAnchors(input);
  const { outSide, inSide } = anchors;
  // Leave from outer rim of exit port.
  const start = portRim(anchors.start, outSide, PORT_RADIUS);
  // Stroke stops here (arrow base). Tip extends ARROW_TIP_EXTENT further to the true rim.
  const end = portRim(anchors.end, inSide, PORT_RADIUS + ARROW_TIP_EXTENT);
  const sameLayoutLane = Math.abs(input.from.y - input.to.y) <= SAME_LANE_EPS;
  const kind = input.kind;
  const label = input.label || "";

  // Bottom↔bottom (poll loop): last segment MUST be vertical so the arrow points up into the port.
  if (kind === "loop" || isLoopLabel(label) || (outSide === "bottom" && inSide === "bottom")) {
    const span = Math.abs(start.x - end.x);
    return buildVerticalApproachDetour({
      sourceX: start.x,
      sourceY: start.y,
      targetX: end.x,
      targetY: end.y,
      side: 1,
      depth: Math.max(40, Math.min(88, span * 0.1 + 36)),
    });
  }

  // Top exit (failure) on same row: U above with vertical final approach into the entry side.
  if ((outSide === "top" || (kind === "branch" && isFailureLabel(label))) && sameLayoutLane) {
    if (inSide === "top") {
      return buildVerticalApproachDetour({
        sourceX: start.x,
        sourceY: start.y,
        targetX: end.x,
        targetY: end.y,
        side: -1,
        depth: 56,
      });
    }
    // Entering left/right after a top exit — use orthogonal rails.
    return buildFlowchartEdgePath({
      sourceX: start.x,
      sourceY: start.y,
      targetX: end.x,
      targetY: end.y,
      sameLane: false,
    });
  }

  if (sameLayoutLane && (outSide === "right" || outSide === "left") && (inSide === "left" || inSide === "right")) {
    return buildFlowchartEdgePath({
      sourceX: start.x,
      sourceY: start.y,
      targetX: end.x,
      targetY: end.y,
      sameLane: true,
    });
  }

  return buildFlowchartEdgePath({
    sourceX: start.x,
    sourceY: start.y,
    targetX: end.x,
    targetY: end.y,
    sameLane: false,
  });
}

export function buildEdgePath(input: EdgeRouteInput): string {
  return routeEdge(input).path;
}

export function edgeLabelPoint(input: EdgeRouteInput): { x: number; y: number } {
  const routed = routeEdge(input);
  return { x: routed.labelX, y: routed.labelY - 10 };
}

/**
 * @deprecated Prefer assignUniqueBranchSides for multi-branch nodes.
 */
export function conditionPortSide(label: string, _index = 0, _total = 1): { side: EdgeSide; t: number } {
  if (isLoopLabel(label)) return { side: "bottom", t: PORT_T };
  if (isFailureLabel(label)) return { side: "top", t: PORT_T };
  if (isSuccessLabel(label)) return { side: "right", t: PORT_T };
  return { side: "right", t: PORT_T };
}
