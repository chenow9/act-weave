import { describe, expect, it } from "vitest";

import {
  ARROW_CLEARANCE,
  HANDLE_RADIUS,
  LONG_EDGE_MIN_DX,
  SAME_ROW_DETOUR,
  buildFlowchartEdgePath,
  insetFlowchartPorts,
} from "./workflow-edge-path";

describe("buildFlowchartEdgePath", () => {
  it("draws a straight connector on the same lane even when handles differ in Y", () => {
    // Multi-port Condition→next: handle Y can differ by tens of px — must NOT U-loop.
    const { path } = buildFlowchartEdgePath({
      sourceX: 200,
      sourceY: 330,
      targetX: 420,
      targetY: 300,
      sameLane: true,
    });
    expect(path).toBe("M 200 330 L 420 300");
    expect(path).not.toMatch(/Q/);
  });

  it("detours same-lane multi-hop skips onto a parallel rail", () => {
    const { path, labelY } = buildFlowchartEdgePath({
      sourceX: 100,
      sourceY: 300,
      targetX: 100 + LONG_EDGE_MIN_DX + 80,
      targetY: 300,
      sameLane: true,
      detourSign: 1,
    });
    expect(labelY).toBe(300 + SAME_ROW_DETOUR);
    expect(path).toContain(String(300 + SAME_ROW_DETOUR));
  });

  it("uses an early L-bend for short cross-lane forks", () => {
    const { path } = buildFlowchartEdgePath({
      sourceX: 200,
      sourceY: 300,
      targetX: 340,
      targetY: 100,
      sameLane: false,
    });
    expect(path).toMatch(/M 200 300/);
    // Vertical segment near source (early bend), not near target.
    expect(path).toMatch(/224/);
  });

  it("holds the source rail for long cross-lane joins into End", () => {
    const { path, labelY } = buildFlowchartEdgePath({
      sourceX: 300,
      sourceY: 120,
      targetX: 1100,
      targetY: 300,
      sameLane: false,
    });
    expect(labelY).toBe(120);
    expect(path).toMatch(/1100 300|1100\.0 300/);
  });

  it("staggers merge slots so concurrent joins do not share one vertical", () => {
    const a = buildFlowchartEdgePath({
      sourceX: 300,
      sourceY: 120,
      targetX: 1000,
      targetY: 300,
      sameLane: false,
      mergeSlot: 0,
    });
    const b = buildFlowchartEdgePath({
      sourceX: 300,
      sourceY: 120,
      targetX: 1000,
      targetY: 300,
      sameLane: false,
      mergeSlot: 1,
    });
    expect(a.path).not.toBe(b.path);
  });

  it("leaves a stub before the arrow without sliding the label onto the source", () => {
    const { labelX, labelY, path } = buildFlowchartEdgePath({
      sourceX: 200,
      sourceY: 300,
      targetX: 400,
      targetY: 180,
      sameLane: false,
      preferEarlyBend: true,
    });
    expect(labelY).toBe(180);
    // After the early-bend corner (x=224), but not against the arrow (x=400).
    expect(labelX).toBeGreaterThan(224 + 20);
    expect(labelX).toBeLessThan(400 - 36);
    expect(path.endsWith("400 180")).toBe(true);
  });

  it("keeps a short same-lane label centered so it does not cover the source node", () => {
    const { labelX } = buildFlowchartEdgePath({
      sourceX: 200,
      sourceY: 200,
      targetX: 260,
      targetY: 200,
      sameLane: true,
    });
    expect(labelX).toBe(230);
  });

  it("pulls endpoints off handle centres so the arrow does not cover the port", () => {
    expect(
      insetFlowchartPorts({
        sourceX: 200,
        sourceY: 100,
        targetX: 400,
        targetY: 100,
      }),
    ).toEqual({
      sourceX: 200 + HANDLE_RADIUS,
      sourceY: 100,
      targetX: 400 - HANDLE_RADIUS - ARROW_CLEARANCE,
      targetY: 100,
    });
    expect(
      insetFlowchartPorts({
        sourceX: 200,
        sourceY: 100,
        targetX: 210,
        targetY: 100,
      }),
    ).toEqual({
      sourceX: 200,
      sourceY: 100,
      targetX: 210,
      targetY: 100,
    });
  });
});
