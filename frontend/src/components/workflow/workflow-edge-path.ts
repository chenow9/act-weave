/**
 * Classic flowchart edges for left-to-right process diagrams.
 *
 * Rules (tuned against real canvas mess: multi-port U-loops, edges through nodes):
 * 1. Same layout lane → straight connector (port Y offset is fine as a light diagonal)
 * 2. Same lane but hops over intermediate nodes → parallel detour rail, then back
 * 3. Different lane → orthogonal: short hops bend early, long hops ride the source rail
 * 4. Multiple edges into one target stagger their final vertical segment
 */

const EXIT_OFFSET = 20;
const ENTER_OFFSET = 20;
const CORNER_RADIUS = 10;
const EARLY_BEND_DX = 220;

/** Vertical gap for same-lane skip detours (must clear a full node row). */
export const SAME_ROW_DETOUR = 130;
/** Horizontal span treated as multi-hop when combined with intermediates. */
export const LONG_EDGE_MIN_DX = 300;
/** Node-position Y delta under which two nodes share a horizontal lane. */
export const SAME_LANE_EPS = 48;

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
};

export type FlowchartEdgePath = {
  path: string;
  labelX: number;
  labelY: number;
};

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

  if (absDx <= EARLY_BEND_DX) {
    // Short fork (e.g. Condition → 驳回): rise/drop near the source, then into target.
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
    return {
      path: `M ${fmt(a.x)} ${fmt(a.y)} L ${fmt(b.x)} ${fmt(b.y)}`,
      labelX: (a.x + b.x) / 2,
      labelY: (a.y + b.y) / 2,
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

function labelAnchor(points: Array<{ x: number; y: number }>): { x: number; y: number } {
  let bestLen = -1;
  let best = { x: points[0].x, y: points[0].y };
  for (let i = 0; i < points.length - 1; i += 1) {
    const a = points[i];
    const b = points[i + 1];
    const horizontal = Math.abs(a.y - b.y) < 0.5;
    const len = Math.hypot(b.x - a.x, b.y - a.y);
    const score = horizontal ? len + 1000 : len;
    if (score > bestLen) {
      bestLen = score;
      best = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
    }
  }
  return best;
}

function fmt(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(1);
}
