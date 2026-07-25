import { describe, expect, it } from "vitest";

import type { WorkflowDraftRecord } from "../types/domain";
import {
  buildWorkflowObjectSchema,
  collapseNodePorts,
  createDefaultWorkflowGraphDraft,
  listWorkflowVariableReferences,
  listWorkflowVariableReferencesForNode,
  normalizeWorkflowGraphDraft,
  parseWorkflowObjectSchema,
  unwrapWorkflowVariableRef,
} from "./workflow-graph";

function draftRecordFixture(): WorkflowDraftRecord {
  return {
    workflowId: "wf-order-cancel-draft",
    draftVersion: "draft-v3",
    updatedAt: "2026-06-27T10:00:00Z",
    graph: {
      schemaVersion: "workflow.graph.v1",
      nodes: [
        {
          id: "start",
          type: "Start",
          label: "Start",
          position: { x: 120, y: 220 },
          ports: [{ key: "output", label: "Output", direction: "output" }],
          data: {
            inputSchema: {
              properties: {
                orderId: { type: "string" },
                reason: { type: "string" },
              },
            },
          },
          ui: {},
        },
        {
          id: "tool-1",
          type: "Tool",
          label: "Query Order",
          position: { x: 420, y: 220 },
          ports: [
            { key: "input", label: "Input", direction: "input" },
            { key: "result", label: "Result", direction: "output" },
          ],
          data: {
            outputSchema: {
              properties: {
                status: { type: "string" },
                order: { type: "object" },
              },
            },
          },
          ui: {},
        },
      ],
      edges: [],
      viewport: { x: 0, y: 0, zoom: 1 },
      ui: {},
    },
  };
}

describe("workflow graph helpers", () => {
  it("normalizes smart-dag graphs with null ports and empty edge ports", () => {
    const graph = normalizeWorkflowGraphDraft({
      schemaVersion: "workflow.graph.v1",
      nodes: [
        {
          id: "start_1",
          type: "Start",
          label: "Start",
          position: { x: 80, y: 140 },
          ports: null as unknown as [],
          data: {},
          ui: {},
        },
        {
          id: "tool_1",
          type: "Tool",
          label: "Create Ticket",
          position: { x: 260, y: 140 },
          ports: undefined as unknown as [],
          data: { toolId: "t1" },
          ui: {},
        },
        {
          id: "end_1",
          type: "End",
          label: "End",
          position: { x: 440, y: 140 },
          ports: [],
          data: {},
          ui: {},
        },
      ],
      edges: [
        {
          id: "e1",
          sourceNodeId: "start_1",
          sourcePort: "",
          targetNodeId: "tool_1",
          targetPort: "",
          data: {},
          ui: {},
        },
        {
          id: "e2",
          sourceNodeId: "tool_1",
          sourcePort: "  ",
          targetNodeId: "end_1",
          targetPort: "",
          data: {},
          ui: {},
        },
      ],
      viewport: { x: 0, y: 0, zoom: 1 },
      ui: { generatedBy: "smart-dag.v2" },
    });

    expect(graph.nodes[0].ports).toEqual([{ key: "output", label: "Output", direction: "output" }]);
    expect(graph.nodes[1].ports.map((port) => port.key)).toEqual(["input", "output"]);
    expect(graph.nodes[2].ports).toEqual([{ key: "input", label: "Input", direction: "input" }]);
    expect(graph.edges[0]).toMatchObject({ sourcePort: "output", targetPort: "input" });
    expect(graph.edges[1]).toMatchObject({ sourcePort: "output", targetPort: "input" });
    // Must not throw when variable helpers run (editor open path)
    expect(() => listWorkflowVariableReferences(graph)).not.toThrow();
    expect(listWorkflowVariableReferences(graph).length).toBeGreaterThanOrEqual(0);
  });

  it("creates a default start to end scaffold", () => {
    const draft = createDefaultWorkflowGraphDraft();

    expect(draft.schemaVersion).toBe("workflow.graph.v1");
    expect(draft.nodes.map((node) => node.type)).toEqual(["Start", "End"]);
    expect(draft.edges).toEqual([
      {
        id: "edge-start-end",
        sourceNodeId: "start",
        sourcePort: "output",
        targetNodeId: "end",
        targetPort: "input",
        data: {},
        ui: {},
      },
    ]);
    expect(draft.viewport).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(draft.ui).toEqual({});
  });

  it("collapses multi-exit ports so branches share one output point", () => {
    expect(
      collapseNodePorts(
        [
          { key: "input", label: "Input", direction: "input" },
          { key: "result", label: "Result", direction: "output" },
          { key: "fallback", label: "Fallback", direction: "output" },
        ],
        "Condition",
      ),
    ).toEqual([
      { key: "input", label: "Input", direction: "input" },
      { key: "output", label: "Result", direction: "output" },
    ]);

    const graph = normalizeWorkflowGraphDraft({
      schemaVersion: "workflow.graph.v1",
      nodes: [
        {
          id: "cond",
          type: "Condition",
          label: "Cond",
          position: { x: 0, y: 0 },
          ports: [
            { key: "input", label: "In", direction: "input" },
            { key: "true", label: "True", direction: "output" },
            { key: "false", label: "False", direction: "output" },
          ],
          data: {},
          ui: {},
        },
        {
          id: "a",
          type: "Tool",
          label: "A",
          position: { x: 200, y: 0 },
          ports: defaultPortsLike(),
          data: {},
          ui: {},
        },
        {
          id: "b",
          type: "Tool",
          label: "B",
          position: { x: 200, y: 100 },
          ports: defaultPortsLike(),
          data: {},
          ui: {},
        },
      ],
      edges: [
        {
          id: "e1",
          sourceNodeId: "cond",
          sourcePort: "true",
          targetNodeId: "a",
          targetPort: "input",
          data: { branch: "true" },
          ui: {},
        },
        {
          id: "e2",
          sourceNodeId: "cond",
          sourcePort: "false",
          targetNodeId: "b",
          targetPort: "input",
          data: { branch: "default" },
          ui: {},
        },
      ],
      viewport: { x: 0, y: 0, zoom: 1 },
      ui: {},
    });

    expect(graph.nodes.find((n) => n.id === "cond")?.ports.map((p) => p.key)).toEqual(["input", "output"]);
    expect(graph.edges.every((e) => e.sourcePort === "output")).toBe(true);
    expect(graph.edges.map((e) => e.data.branch)).toEqual(["true", "default"]);
  });

  it("lists input and node output variable references deterministically", () => {
    const refs = listWorkflowVariableReferences(draftRecordFixture().graph);

    expect(refs).toEqual([
      { key: "input.orderId", label: "Input orderId", source: "input", type: "string" },
      { key: "input.reason", label: "Input reason", source: "input", type: "string" },
      { key: "nodeOutputs.tool-1.order", label: "Query Order order", source: "node-output", type: "object" },
      { key: "nodeOutputs.tool-1.status", label: "Query Order status", source: "node-output", type: "string" },
    ]);
  });

  it("lists input variable references from compact start schemas", () => {
    const draft = draftRecordFixture();
    draft.graph.nodes[0].data.inputSchema = {
      orderId: "string",
      dryRun: "boolean",
    };

    const refs = listWorkflowVariableReferences(draft.graph);

    expect(refs).toEqual(
      expect.arrayContaining([
        { key: "input.orderId", label: "Input orderId", source: "input", type: "string" },
        { key: "input.dryRun", label: "Input dryRun", source: "input", type: "boolean" },
      ]),
    );
  });

  it("lists only upstream node output references for a selected node", () => {
    const draft = draftRecordFixture();
    draft.graph.nodes.push(
      {
        id: "tool-2",
        type: "Tool",
        label: "Cancel Order",
        position: { x: 720, y: 220 },
        ports: [
          { key: "input", label: "Input", direction: "input" },
          { key: "result", label: "Result", direction: "output" },
        ],
        data: {
          outputSchema: {
            properties: {
              cancellationId: { type: "string" },
            },
          },
        },
        ui: {},
      },
      {
        id: "tool-3",
        type: "Tool",
        label: "Notify Customer",
        position: { x: 1020, y: 220 },
        ports: [
          { key: "input", label: "Input", direction: "input" },
          { key: "result", label: "Result", direction: "output" },
        ],
        data: {
          outputSchema: {
            properties: {
              notificationId: { type: "string" },
            },
          },
        },
        ui: {},
      },
    );
    draft.graph.edges = [
      { id: "edge-start-tool-1", sourceNodeId: "start", targetNodeId: "tool-1", data: {}, ui: {} },
      { id: "edge-tool-1-tool-2", sourceNodeId: "tool-1", targetNodeId: "tool-2", data: {}, ui: {} },
      { id: "edge-tool-2-tool-3", sourceNodeId: "tool-2", targetNodeId: "tool-3", data: {}, ui: {} },
    ];

    const refs = listWorkflowVariableReferencesForNode(draft.graph, "tool-2");

    expect(refs).toEqual([
      { key: "input.orderId", label: "Input orderId", source: "input", type: "string" },
      { key: "input.reason", label: "Input reason", source: "input", type: "string" },
      { key: "nodeOutputs.tool-1.order", label: "Query Order order", source: "node-output", type: "object" },
      { key: "nodeOutputs.tool-1.status", label: "Query Order status", source: "node-output", type: "string" },
    ]);
  });

  it("parses and rebuilds typed workflow object schemas", () => {
    const fields = parseWorkflowObjectSchema({
      type: "object",
      properties: {
        orderId: {
          type: "string",
          description: "订单 ID",
          enum: ["A10293", "B20991"],
          example: "A10293",
        },
        dryRun: {
          type: "boolean",
          description: "仅校验",
        },
      },
      required: ["orderId"],
    });

    expect(fields).toEqual([
      {
        key: "dryRun",
        type: "boolean",
        required: false,
        description: "仅校验",
        enumValues: [],
        example: "",
      },
      {
        key: "orderId",
        type: "string",
        required: true,
        description: "订单 ID",
        enumValues: ["A10293", "B20991"],
        example: "A10293",
      },
    ]);

    expect(
      buildWorkflowObjectSchema([
        {
          key: "orderId",
          type: "string",
          required: true,
          description: "订单 ID",
          enumValues: ["A10293", "B20991"],
          example: "A10293",
        },
        {
          key: "dryRun",
          type: "boolean",
          required: false,
          description: "仅校验",
          enumValues: [],
          example: "",
        },
      ]),
    ).toEqual({
      type: "object",
      properties: {
        orderId: {
          type: "string",
          description: "订单 ID",
          enum: ["A10293", "B20991"],
          example: "A10293",
        },
        dryRun: {
          type: "boolean",
          description: "仅校验",
        },
      },
      required: ["orderId"],
    });
  });

  it("unwraps workflow variable references for picker usage", () => {
    expect(unwrapWorkflowVariableRef("{{input.orderId}}")).toBe("input.orderId");
    expect(unwrapWorkflowVariableRef("{{ nodeOutputs.tool-1.status }}")).toBe("nodeOutputs.tool-1.status");
  });
});

function defaultPortsLike() {
  return [
    { key: "input", label: "Input", direction: "input" as const },
    { key: "output", label: "Output", direction: "output" as const },
  ];
}
