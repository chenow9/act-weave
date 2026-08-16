import { describe, expect, it } from "vitest";

import {
  displayBranchLabel,
  displayNodeTitle,
  humanizeIdentifier,
  isPlaceholderNodeLabel,
  looksLikeIdentifier,
  polishWorkflowGraphLabels,
  workflowEdgeColor,
  workflowEdgeTone,
  workflowNodeLibrary,
  workflowNodeVisual,
} from "./workflow-node-visual";

describe("workflow-node-visual", () => {
  it("exposes a Dify-like palette for every library type", () => {
    const library = workflowNodeLibrary();
    expect(library.map((item) => item.type)).toEqual([
      "Start",
      "Tool",
      "Condition",
      "SubWorkflow",
      "Transform",
      "Parallel",
      "ForEach",
      "Approval",
      "End",
    ]);
    expect(workflowNodeVisual("Condition").icon).toContain("code-branch");
    expect(workflowNodeVisual("Unknown").type).toBe("Tool");
  });

  it("treats type names and raw ids as placeholder labels", () => {
    expect(isPlaceholderNodeLabel("Start", "Start", "start")).toBe(true);
    expect(isPlaceholderNodeLabel("Tool", "Tool", "get_customer")).toBe(true);
    expect(isPlaceholderNodeLabel("查资质", "Tool", "get_customer")).toBe(false);
  });

  it("prefers a real title, then explanation, then a humanized id", () => {
    expect(
      displayNodeTitle({
        id: "get_customer",
        type: "Tool",
        label: "Tool",
        typeLabel: "工具调用",
        explanationTitle: "Start",
      }),
    ).toBe("Get Customer");
    expect(
      displayNodeTitle({
        id: "risk_approval",
        type: "Approval",
        label: "Approval",
        typeLabel: "人工确认",
        explanationTitle: "风控审批",
      }),
    ).toBe("风控审批");
    expect(
      displayNodeTitle({
        id: "start",
        type: "Start",
        label: "Start",
        typeLabel: "开始",
      }),
    ).toBe("开始");
    expect(
      displayNodeTitle({
        id: "start",
        type: "Start",
        label: "订单入口",
        typeLabel: "开始",
      }),
    ).toBe("订单入口");
    expect(looksLikeIdentifier("get_customer")).toBe(true);
    expect(humanizeIdentifier("write_approved")).toBe("Write Approved");
  });

  it("maps generated branch words onto locale keys and tones", () => {
    expect(displayBranchLabel("qualified", (key) => key)).toBe("workflow.branchTrue");
    expect(displayBranchLabel("rejected", (key) => key)).toBe("workflow.branchFailure");
    expect(displayBranchLabel("default", (key) => key)).toBe("workflow.branchDefault");
    expect(displayBranchLabel("custom-path", (key) => key)).toBe("custom-path");
    expect(workflowEdgeTone("qualified")).toBe("success");
    expect(workflowEdgeTone("rejected")).toBe("danger");
    expect(workflowEdgeTone("default")).toBe("muted");
    expect(workflowEdgeColor("danger")).toBe("#e11d48");
    expect(workflowEdgeColor("default", true)).toBe("#0f766e");
  });

  it("rewrites placeholder generated labels onto the draft graph", () => {
    const polished = polishWorkflowGraphLabels(
      {
        schemaVersion: "workflow.graph.v1",
        nodes: [
          {
            id: "get_customer",
            type: "Tool",
            label: "Tool",
            position: { x: 0, y: 0 },
            ports: [],
            data: {},
            ui: {},
          },
          {
            id: "start",
            type: "Start",
            label: "Start",
            position: { x: 0, y: 0 },
            ports: [],
            data: {},
            ui: {},
          },
        ],
        edges: [],
        viewport: { x: 0, y: 0, zoom: 1 },
        ui: {},
      },
      [{ nodeId: "get_customer", title: "拉取客户", reason: "" }],
      (type) => (type === "Start" ? "开始" : "工具调用"),
    );
    expect(polished.nodes[0].label).toBe("拉取客户");
    expect(polished.nodes[1].label).toBe("开始");
  });
});
