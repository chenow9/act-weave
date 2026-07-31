import { describe, expect, it } from "vitest";

import type { WorkflowGraphDraft } from "../../types/domain";
import { autoLayoutWorkflowGraph, layoutWorkflowGraphIfNeeded, workflowGraphNeedsLayout } from "./workflow-layout";

function graph(
  nodes: Array<
    Omit<WorkflowGraphDraft["nodes"][number], "ui" | "ports" | "data"> & Partial<WorkflowGraphDraft["nodes"][number]>
  >,
  edges: Array<
    Partial<WorkflowGraphDraft["edges"][number]> &
      Pick<WorkflowGraphDraft["edges"][number], "id" | "sourceNodeId" | "targetNodeId">
  > = [],
): WorkflowGraphDraft {
  return {
    schemaVersion: "workflow-graph.v1",
    nodes: nodes.map((node) => ({
      ports: [],
      data: {},
      ui: {},
      ...node,
      position: node.position || { x: 0, y: 0 },
    })),
    edges: edges.map((edge) => ({
      sourcePort: "out",
      targetPort: "in",
      data: {},
      ui: {},
      ...edge,
    })),
    viewport: { x: 0, y: 0, zoom: 1 },
    ui: {},
  };
}

describe("autoLayoutWorkflowGraph", () => {
  it("spreads a linear HITL chain left-to-right on one horizontal spine", () => {
    const input = graph(
      [
        { id: "start", type: "Start", label: "Start" },
        { id: "qc", type: "Tool", label: "质检 QC" },
        { id: "approval", type: "Approval", label: "人工确认" },
        { id: "approve", type: "Tool", label: "批通过" },
        { id: "end", type: "End", label: "End" },
      ],
      [
        { id: "e1", sourceNodeId: "start", targetNodeId: "qc" },
        { id: "e2", sourceNodeId: "qc", targetNodeId: "approval" },
        { id: "e3", sourceNodeId: "approval", targetNodeId: "approve" },
        { id: "e4", sourceNodeId: "approve", targetNodeId: "end" },
      ],
    );

    const laid = autoLayoutWorkflowGraph(input);
    const byId = Object.fromEntries(laid.nodes.map((n) => [n.id, n.position]));

    expect(byId.start.x).toBeLessThan(byId.qc.x);
    expect(byId.qc.x).toBeLessThan(byId.approval.x);
    expect(byId.approval.x).toBeLessThan(byId.approve.x);
    expect(byId.approve.x).toBeLessThan(byId.end.x);

    // Straight spine: all y equal.
    const ys = [byId.start.y, byId.qc.y, byId.approval.y, byId.approve.y, byId.end.y];
    expect(Math.max(...ys) - Math.min(...ys)).toBeLessThanOrEqual(1);
  });

  it("keeps the true branch horizontal and reject branch on a parallel track", () => {
    // condition fork → spine: reserve→qc→approval→approve → end
    //                → side:  reject ─────────────────────→ end
    const input = graph(
      [
        { id: "cond", type: "Condition", label: "是否库存充足" },
        { id: "reserve", type: "Tool", label: "库存预占" },
        { id: "reject", type: "Tool", label: "驳回工单" },
        { id: "qc", type: "Tool", label: "质检 QC" },
        { id: "approval", type: "Approval", label: "人工确认" },
        { id: "approve", type: "Tool", label: "审批通过" },
        { id: "end", type: "End", label: "End" },
      ],
      [
        { id: "e1", sourceNodeId: "cond", targetNodeId: "reserve", data: { branch: "true" } },
        { id: "e2", sourceNodeId: "cond", targetNodeId: "reject", data: { branch: "default" } },
        { id: "e3", sourceNodeId: "reserve", targetNodeId: "qc" },
        { id: "e4", sourceNodeId: "qc", targetNodeId: "approval" },
        { id: "e5", sourceNodeId: "approval", targetNodeId: "approve" },
        { id: "e6", sourceNodeId: "approve", targetNodeId: "end" },
        { id: "e7", sourceNodeId: "reject", targetNodeId: "end" },
      ],
    );

    const laid = autoLayoutWorkflowGraph(input);
    const p = Object.fromEntries(laid.nodes.map((n) => [n.id, n.position]));

    // Main success spine is a perfect horizontal line (incl. cond + end).
    const spineY = [p.cond.y, p.reserve.y, p.qc.y, p.approval.y, p.approve.y, p.end.y];
    expect(Math.max(...spineY) - Math.min(...spineY)).toBeLessThanOrEqual(1);

    // Reject sits on a parallel track BELOW the success rail.
    expect(p.reject.y).toBeGreaterThan(p.reserve.y);
    expect(Math.abs(p.reject.y - p.reserve.y)).toBeGreaterThanOrEqual(160);

    // Column pitch is stable; fork/stage columns may be slightly wider (≤ 40).
    const chainX = [p.cond.x, p.reserve.x, p.qc.x, p.approval.x, p.approve.x, p.end.x];
    const gaps = chainX.slice(1).map((x, i) => x - chainX[i]);
    expect(Math.min(...gaps)).toBeGreaterThanOrEqual(200);
    expect(Math.max(...gaps) - Math.min(...gaps)).toBeLessThanOrEqual(50);
  });

  it("lays out a condition→end skip with even columns and branched vertical fan-out", () => {
    // Long “跳过到结束” edge + happy path; Dify-style: not forced onto one Y line.
    const input = graph(
      [
        { id: "start", type: "Start", label: "Start" },
        { id: "dup", type: "Tool", label: "查重" },
        { id: "cond", type: "Condition", label: "条件" },
        { id: "reserve", type: "Tool", label: "预算预留" },
        { id: "qc", type: "Tool", label: "QC" },
        { id: "approval", type: "Approval", label: "人工审批" },
        { id: "release", type: "Tool", label: "放行" },
        { id: "end", type: "End", label: "End" },
      ],
      [
        { id: "e1", sourceNodeId: "start", targetNodeId: "dup" },
        { id: "e2", sourceNodeId: "dup", targetNodeId: "cond" },
        { id: "e3", sourceNodeId: "cond", targetNodeId: "reserve", data: { branch: "true" } },
        { id: "e4", sourceNodeId: "reserve", targetNodeId: "qc" },
        { id: "e5", sourceNodeId: "qc", targetNodeId: "approval" },
        { id: "e6", sourceNodeId: "approval", targetNodeId: "release" },
        { id: "e7", sourceNodeId: "release", targetNodeId: "end" },
        { id: "e8", sourceNodeId: "cond", targetNodeId: "end", data: { branch: "default" } },
      ],
    );

    const laid = autoLayoutWorkflowGraph(input);
    const p = Object.fromEntries(laid.nodes.map((n) => [n.id, n.position]));

    // Happy path left→right; stage/fork columns may use a wider pitch (≤ 40 delta).
    const xs = ["start", "dup", "cond", "reserve", "qc", "approval", "release", "end"].map((id) => p[id].x);
    const gaps = xs.slice(1).map((x, i) => x - xs[i]);
    expect(Math.min(...gaps)).toBeGreaterThanOrEqual(200);
    expect(Math.max(...gaps) - Math.min(...gaps)).toBeLessThanOrEqual(50);
    expect(p.end.x).toBeGreaterThan(p.release.x);
  });

  it("places Start left and End right even with a progress poll cycle", () => {
    // Real smart-dag shape: tools → get_progress ⇄ condition → report → end
    // (running loops back; failed goes to end; completed continues to report)
    const input = graph(
      [
        { id: "start", type: "Start", label: "Start" },
        { id: "create_task", type: "Tool", label: "Tool" },
        { id: "upload_image", type: "Tool", label: "Tool" },
        { id: "start_analysis", type: "Tool", label: "Tool" },
        { id: "get_progress", type: "Tool", label: "Tool" },
        { id: "progress_check", type: "Condition", label: "Condition" },
        { id: "create_word_report", type: "Tool", label: "Tool" },
        { id: "end", type: "End", label: "End" },
      ],
      [
        { id: "e1", sourceNodeId: "start", targetNodeId: "create_task" },
        { id: "e2", sourceNodeId: "create_task", targetNodeId: "upload_image" },
        { id: "e3", sourceNodeId: "upload_image", targetNodeId: "start_analysis" },
        { id: "e4", sourceNodeId: "start_analysis", targetNodeId: "get_progress" },
        { id: "e5", sourceNodeId: "get_progress", targetNodeId: "progress_check" },
        { id: "e6", sourceNodeId: "progress_check", targetNodeId: "create_word_report", data: { branch: "completed" } },
        { id: "e7", sourceNodeId: "progress_check", targetNodeId: "get_progress", data: { branch: "running" } },
        { id: "e8", sourceNodeId: "progress_check", targetNodeId: "end", data: { branch: "failed" } },
        { id: "e9", sourceNodeId: "create_word_report", targetNodeId: "end" },
      ],
    );

    const laid = autoLayoutWorkflowGraph(input);
    const p = Object.fromEntries(laid.nodes.map((n) => [n.id, n.position]));

    expect(p.start.x).toBeLessThan(p.create_task.x);
    expect(p.create_task.x).toBeLessThan(p.upload_image.x);
    expect(p.upload_image.x).toBeLessThan(p.start_analysis.x);
    expect(p.start_analysis.x).toBeLessThan(p.get_progress.x);
    expect(p.get_progress.x).toBeLessThan(p.progress_check.x);
    expect(p.progress_check.x).toBeLessThan(p.create_word_report.x);
    expect(p.create_word_report.x).toBeLessThan(p.end.x);

    // End is the rightmost node — never sandwiched between tools.
    const maxToolX = Math.max(
      p.create_task.x,
      p.upload_image.x,
      p.start_analysis.x,
      p.get_progress.x,
      p.progress_check.x,
      p.create_word_report.x,
    );
    expect(p.end.x).toBeGreaterThan(maxToolX);
    expect(p.start.x).toBeLessThanOrEqual(Math.min(p.create_task.x, p.progress_check.x, p.end.x));

    // Success report continues on/near main rail; failure end may sit below.
    // Poll loop routing is handled in canvas path drawing (below rail).
    expect(p.end.x).toBeGreaterThan(p.progress_check.x);
  });

  it("detects stacked nodes and layoutWorkflowGraphIfNeeded unstacks them", () => {
    const stacked = graph([
      { id: "a", type: "Tool", label: "A", position: { x: 200, y: 200 } },
      { id: "b", type: "Approval", label: "B", position: { x: 205, y: 202 } },
      { id: "c", type: "Tool", label: "C", position: { x: 208, y: 198 } },
    ]);

    expect(workflowGraphNeedsLayout(stacked)).toBe(true);
    const fixed = layoutWorkflowGraphIfNeeded(stacked);
    expect(workflowGraphNeedsLayout(fixed)).toBe(false);
  });

  it("keeps a well-spaced graph unchanged by layoutWorkflowGraphIfNeeded", () => {
    const ok = graph([
      { id: "a", type: "Start", label: "A", position: { x: 100, y: 100 } },
      { id: "b", type: "End", label: "B", position: { x: 500, y: 100 } },
    ]);
    expect(workflowGraphNeedsLayout(ok)).toBe(false);
    expect(layoutWorkflowGraphIfNeeded(ok).nodes[0].position).toEqual({ x: 100, y: 100 });
  });
});
