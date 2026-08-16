/**
 * Classic flowchart edges for left-to-right process diagrams.
 *
 * Rules (tuned against real canvas mess: multi-port U-loops, edges through nodes):
 * 1. Same layout lane → straight connector (port Y offset is fine as a light diagonal)
 * 2. Same lane but hops over intermediate nodes → parallel detour rail, then back
 * 3. Different lane → orthogonal: short hops bend early, long hops ride the source rail
 * 4. Multiple edges into one target stagger their final vertical segment
 */

const EXIT_OFFSET = 24;
const ENTER_OFFSET = 24;
const CORNER_RADIUS = 16;
const EARLY_BEND_DX = 220;

/** Vertical gap for same-lane skip detours (must clear a full node row). */
export const SAME_ROW_DETOUR = 160;
/** Horizontal span treated as a multi-hop skip (must exceed one layout column). */
export const LONG_EDGE_MIN_DX = 520;
/** Node-position Y delta under which two nodes share a horizontal lane. */
export const SAME_LANE_EPS = 48;
/** Visible handle radius (10px border-box circle). */
export const HANDLE_RADIUS = 5;
/** Air gap between the arrow tip and the handle rim. */
export const ARROW_CLEARANCE = 2;
/** Keep the label off the source handle / corner. */
const LABEL_CLEAR_FROM_SOURCE = 28;
/** Keep this much of the last segment after the label so a stub shows before the arrow. */
const LABEL_CLEAR_FROM_TARGET = 44;

export type FlowchartEdgePathInput = {
  sourceX: number;
  sourceY: number;
  targetX: number;
  targetY: number;
  /**
   * True when the *nodes* sit on the same horizontal lane (layout Y),
   * regardless of multi-port handle offsets.
   */
  sameLane?: boolean;
  /** -1 above / +1 below — only for same-lane multi-hop skips. */
  detourSign?: number;
  detourAmount?: number;
  /** 0..n — stagger final approach when several edges share a target. */
  mergeSlot?: number;
  /** Drop/rise near the source so failure / default rails miss the next spine card. */
  preferEarlyBend?: boolean;
};

export type FlowchartEdgePath = {
  path: string;
  labelX: number;
  labelY: number;
};

/**
 * Vue Flow reports handle *centres*. Pull the stroke off those centres so the
 * line leaves the source rim and the arrow tip kisses the target rim
 * instead of covering the port.
 */
export function insetFlowchartPorts(input: {
  sourceX: number;
  sourceY: number;
  targetX: number;
  targetY: number;
}): { sourceX: number; sourceY: number; targetX: number; targetY: number } {
  const span = Math.abs(input.targetX - input.sourceX);
  const budget = HANDLE_RADIUS * 2 + ARROW_CLEARANCE + 8;
  if (span <= budget) {
    return input;
  }
  const dirX = input.targetX >= input.sourceX ? 1 : -1;
  return {
    sourceX: input.sourceX + dirX * HANDLE_RADIUS,
    sourceY: input.sourceY,
    targetX: input.targetX - dirX * (HANDLE_RADIUS + ARROW_CLEARANCE),
    targetY: input.targetY,
  };
}

/**
 * U-turn that approaches the target on a vertical segment (same targetX).
 * Use for bottom→bottom loops / top→top skips so the arrow points into the port.
 */
export function buildVerticalApproachDetour(input: {
  sourceX: number;
  sourceY: number;
  targetX: number;
  targetY: number;
  /** +1 rail below, -1 rail above */
  side: 1 | -1;
  depth?: number;
}): FlowchartEdgePath {
  const depth = input.depth ?? 56;
  const railY =
    input.side > 0 ? Math.max(input.sourceY, input.targetY) + depth : Math.min(input.sourceY, input.targetY) - depth;
  return pathFromPoints([
    { x: input.sourceX, y: input.sourceY },
    { x: input.sourceX, y: railY },
    { x: input.targetX, y: railY },
    // Final segment is vertical → marker points straight into the port.
    { x: input.targetX, y: input.targetY },
  ]);
}

export function buildFlowchartEdgePath(input: FlowchartEdgePathInput): FlowchartEdgePath {
  const sx = input.sourceX;
  const sy = input.sourceY;
  const tx = input.targetX;
  const ty = input.targetY;
  const dx = tx - sx;
  const dy = ty - sy;
  const absDx = Math.abs(dx);
  const absDy = Math.abs(dy);
  const sameLane = input.sameLane ?? absDy <= 8;
  const detourSign = input.detourSign ?? 0;
  const mergeSlot = Math.max(0, input.mergeSlot ?? 0);

  // --- 1) Same lane, no detour: straight (fixes Condition multi-port U-loops) ---
  if (sameLane && detourSign === 0) {
    return pathFromPoints([
      { x: sx, y: sy },
      { x: tx, y: ty },
    ]);
  }

  // --- 2) Same lane, hop over intermediate nodes: parallel rail detour -----------
  if (sameLane && detourSign !== 0) {
    const detour = input.detourAmount ?? SAME_ROW_DETOUR;
    const railY = (sy + ty) / 2 + detourSign * detour;
    const dirX = dx >= 0 ? 1 : -1;
    const xLeave = sx + dirX * EXIT_OFFSET;
    // Stagger re-entry so stacked skips don't share one vertical.
    const xEnter = tx - dirX * (ENTER_OFFSET + mergeSlot * 18);
    return pathFromPoints([
      { x: sx, y: sy },
      { x: xLeave, y: sy },
      { x: xLeave, y: railY },
      { x: xEnter, y: railY },
      { x: xEnter, y: ty },
      { x: tx, y: ty },
    ]);
  }

  // --- 3) Different lanes: orthogonal rails ------------------------------------
  const dirX = dx >= 0 ? 1 : -1;
  const xLeave = sx + dirX * EXIT_OFFSET;
  // mergeSlot pushes the vertical drop leftward so concurrent joins fan in cleanly.
  const xEnter = tx - dirX * (ENTER_OFFSET + mergeSlot * 22);

  if (absDx <= EARLY_BEND_DX || input.preferEarlyBend) {
    // Short / side-branch fork: rise/drop near the source, then into target.
    return pathFromPoints([
      { x: sx, y: sy },
      { x: xLeave, y: sy },
      { x: xLeave, y: ty },
      { x: tx, y: ty },
    ]);
  }

  // Long side-lane (e.g. 驳回 → End): ride source rail the whole way, drop at the end.
  let bendX = xEnter;
  if ((bendX - xLeave) * dirX < EXIT_OFFSET) {
    bendX = (sx + tx) / 2 - mergeSlot * 16;
  }
  return pathFromPoints([
    { x: sx, y: sy },
    { x: xLeave, y: sy },
    { x: bendX, y: sy },
    { x: bendX, y: ty },
    { x: tx, y: ty },
  ]);
}

function pathFromPoints(raw: Array<{ x: number; y: number }>): FlowchartEdgePath {
  const points = dedupePoints(raw);
  if (points.length === 1) {
    const p = points[0];
    return { path: `M ${fmt(p.x)} ${fmt(p.y)}`, labelX: p.x, labelY: p.y };
  }
  if (points.length === 2) {
    const a = points[0];
    const b = points[1];
    const label = labelOnSegment(a, b, true);
    return {
      path: `M ${fmt(a.x)} ${fmt(a.y)} L ${fmt(b.x)} ${fmt(b.y)}`,
      labelX: label.x,
      labelY: label.y,
    };
  }

  let d = `M ${fmt(points[0].x)} ${fmt(points[0].y)}`;
  for (let i = 1; i < points.length - 1; i += 1) {
    d += bend(points[i - 1], points[i], points[i + 1], CORNER_RADIUS);
  }
  const last = points[points.length - 1];
  d += ` L ${fmt(last.x)} ${fmt(last.y)}`;

  const label = labelAnchor(points);
  return { path: d, labelX: label.x, labelY: label.y };
}

function bend(
  a: { x: number; y: number },
  b: { x: number; y: number },
  c: { x: number; y: number },
  radius: number,
): string {
  const d1 = Math.hypot(b.x - a.x, b.y - a.y);
  const d2 = Math.hypot(c.x - b.x, c.y - b.y);
  if (d1 < 0.5 || d2 < 0.5) return ` L ${fmt(b.x)} ${fmt(b.y)}`;
  if (
    (Math.abs(a.x - b.x) < 0.5 && Math.abs(b.x - c.x) < 0.5) ||
    (Math.abs(a.y - b.y) < 0.5 && Math.abs(b.y - c.y) < 0.5)
  ) {
    return ` L ${fmt(b.x)} ${fmt(b.y)}`;
  }

  const r = Math.min(radius, d1 / 2, d2 / 2);
  const ux1 = (b.x - a.x) / d1;
  const uy1 = (b.y - a.y) / d1;
  const ux2 = (c.x - b.x) / d2;
  const uy2 = (c.y - b.y) / d2;
  return ` L ${fmt(b.x - ux1 * r)} ${fmt(b.y - uy1 * r)} Q ${fmt(b.x)} ${fmt(b.y)} ${fmt(b.x + ux2 * r)} ${fmt(b.y + uy2 * r)}`;
}

function dedupePoints(points: Array<{ x: number; y: number }>): Array<{ x: number; y: number }> {
  const out: Array<{ x: number; y: number }> = [];
  for (const p of points) {
    const prev = out[out.length - 1];
    if (prev && Math.abs(prev.x - p.x) < 0.5 && Math.abs(prev.y - p.y) < 0.5) continue;
    out.push(p);
  }
  return out;
}

function labelOnSegment(
  a: { x: number; y: number },
  b: { x: number; y: number },
  isLast: boolean,
): { x: number; y: number } {
  const len = Math.hypot(b.x - a.x, b.y - a.y);
  let t = 0.5;
  if (len > 8) {
    const tMin = LABEL_CLEAR_FROM_SOURCE / len;
    const tMax = 1 - (isLast ? LABEL_CLEAR_FROM_TARGET : LABEL_CLEAR_FROM_SOURCE) / len;
    if (tMin <= tMax) {
      t = Math.min(tMax, Math.max(tMin, 0.5));
    }
    // Segment too short for both paddings: stay centered so we do not
    // slide the pill onto the source node or the corner.
  }
  return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
}

function labelAnchor(points: Array<{ x: number; y: number }>): { x: number; y: number } {
  let bestScore = -1;
  let bestIndex = 0;
  for (let i = 0; i < points.length - 1; i += 1) {
    const a = points[i];
    const b = points[i + 1];
    const horizontal = Math.abs(a.y - b.y) < 0.5;
    const len = Math.hypot(b.x - a.x, b.y - a.y);
    const score = horizontal ? len + 1000 : len;
    if (score > bestScore) {
      bestScore = score;
      bestIndex = i;
    }
  }
  return labelOnSegment(points[bestIndex], points[bestIndex + 1], bestIndex === points.length - 2);
}

function fmt(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(1);
}
