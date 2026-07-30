import { describe, expect, it } from "vitest";

import {
  assignUniqueBranchSides,
  buildEdgePath,
  conditionPortSide,
  resolveEdgeAnchors,
  routeEdge,
} from "./workflow-edges";

const from = { x: 100, y: 100, w: 180, h: 96 };
const toRight = { x: 360, y: 100, w: 180, h: 96 };
const toLeft = { x: 0, y: 100, w: 180, h: 96 };
const toBelow = { x: 100, y: 320, w: 180, h: 96 };

describe("workflow-edges flowchart routing", () => {
  it("routes sequence edges as a straight same-lane connector (no stub elbows)", () => {
    const a = resolveEdgeAnchors({ from, to: toRight, kind: "sequence" });
    expect(a.outSide).toBe("right");
    expect(a.inSide).toBe("left");
    // Port centres sit outside the card border.
    expect(a.start.x).toBe(from.x + from.w + 8);
    expect(a.end.x).toBe(toRight.x - 8);
    const path = buildEdgePath({ from, to: toRight, kind: "sequence" });
    // Stroke ends at arrow base (rim + ARROW_TIP_EXTENT outward).
    // left centre 352, base at 352 - (6+6) = 340
    expect(path).toBe("M 294 148 L 340 148");
    expect(path).not.toMatch(/Q /);
  });

  it("keeps L/R ports on a shared rail so condition → tool is perfectly horizontal", () => {
    const cond = { x: 300, y: 100, w: 180, h: 96 };
    const tool = { x: 540, y: 100, w: 180, h: 96 };
    const path = buildEdgePath({
      from: cond,
      to: tool,
      kind: "branch",
      label: "已完成",
      outSide: "right",
    });
    // left centre 532, base at 532 - 12 = 520
    expect(path).toBe("M 494 148 L 520 148");
    expect(path).not.toMatch(/Q /);
  });

  it("aligns progress → condition left/right ports on the same Y", () => {
    const tool = { x: 100, y: 200, w: 180, h: 96 };
    const cond = { x: 340, y: 200, w: 180, h: 96 };
    const path = buildEdgePath({ from: tool, to: cond, kind: "sequence" });
    // left centre 332, base at 332 - 12 = 320
    expect(path).toBe("M 294 248 L 320 248");
  });

  it("routes loops on a below-rail detour with vertical final approach into the port", () => {
    const a = resolveEdgeAnchors({ from, to: toLeft, kind: "loop", label: "处理中" });
    expect(a.outSide).toBe("bottom");
    expect(a.inSide).toBe("bottom");
    const routed = routeEdge({ from, to: toLeft, kind: "loop", label: "处理中" });
    // Detour sits below the bottom ports.
    expect(routed.labelY).toBeGreaterThan(from.y + from.h);
    // Last segment must be vertical (same targetX) so the arrow points up into the circle.
    const nums = routed.path.match(/-?\d+(\.\d+)?/g)?.map(Number) || [];
    const x1 = nums[nums.length - 4];
    const y1 = nums[nums.length - 3];
    const x2 = nums[nums.length - 2];
    const y2 = nums[nums.length - 1];
    expect(x1).toBe(x2);
    expect(y1).toBeGreaterThan(y2); // approaching upward
  });

  it("routes failure branches out the top toward the target", () => {
    const a = resolveEdgeAnchors({
      from,
      to: toRight,
      kind: "branch",
      label: "失败/其他",
    });
    expect(a.outSide).toBe("top");
    const routed = routeEdge({
      from,
      to: toRight,
      kind: "branch",
      label: "失败/其他",
    });
    expect(routed.path.startsWith("M ")).toBe(true);
  });

  it("routes vertically dominant targets with orthogonal bend", () => {
    const a = resolveEdgeAnchors({ from, to: toBelow, kind: "sequence" });
    expect(a.outSide).toBe("bottom");
    expect(a.inSide).toBe("top");
    const path = buildEdgePath({ from, to: toBelow, kind: "sequence" });
    expect(path.startsWith("M ")).toBe(true);
  });

  it("assigns each condition branch a unique side", () => {
    const assigned = assignUniqueBranchSides([
      { key: "completed", label: "已完成" },
      { key: "running", label: "处理中" },
      { key: "failed", label: "失败/其他" },
    ]);
    const sides = assigned.map((p) => p.side);
    expect(new Set(sides).size).toBe(3);
    expect(assigned.find((p) => p.key === "completed")?.side).toBe("right");
    expect(assigned.find((p) => p.key === "running")?.side).toBe("bottom");
    expect(assigned.find((p) => p.key === "failed")?.side).toBe("top");
    expect(sides.includes("left")).toBe(false);
  });

  it("conditionPortSide uses centre only", () => {
    expect(conditionPortSide("已完成").t).toBe(0.5);
    expect(conditionPortSide("处理中").side).toBe("bottom");
    expect(conditionPortSide("失败/其他").side).toBe("top");
  });
});
