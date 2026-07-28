import { describe, expect, it } from "vitest";

import {
  buildBodyContractFromRequestParams,
  buildRequestParamsFromContracts,
  formatContractJson,
  parseContractJson,
  serializeContractNodesToJson,
} from "./tool-schema-json";
import type { ToolRequestParam, ToolSchemaNode } from "../types/domain";

function objectNode(overrides: Partial<ToolSchemaNode> = {}): ToolSchemaNode {
  return {
    id: overrides.id || "node-root",
    name: overrides.name || "payload",
    type: overrides.type || "object",
    description: overrides.description || "",
    required: overrides.required ?? true,
    location: overrides.location,
    children: overrides.children || [],
    item: overrides.item ?? null,
    additionalProperties: overrides.additionalProperties ?? null,
    enumValues: overrides.enumValues || [],
  };
}

describe("tool-schema-json", () => {
  it("serializes nested object and array nodes into formatted JSON text", () => {
    const nodes: ToolSchemaNode[] = [
      objectNode({
        children: [
          {
            id: "status",
            name: "status",
            type: "string",
            description: "状态",
            required: true,
            children: [],
            item: null,
            additionalProperties: null,
          },
          {
            id: "items",
            name: "items",
            type: "array",
            description: "明细",
            required: true,
            children: [],
            item: objectNode({
              id: "item-node",
              name: "item",
              children: [
                {
                  id: "sku",
                  name: "sku",
                  type: "string",
                  description: "SKU",
                  required: true,
                  children: [],
                  item: null,
                  additionalProperties: null,
                },
              ],
            }),
            additionalProperties: null,
          },
        ],
      }),
    ];

    const json = serializeContractNodesToJson(nodes);

    expect(json).toContain('"payload"');
    expect(json).toContain('"items"');
    expect(json).toContain('"sku"');
  });

  it("parses valid JSON text back into nested schema nodes", () => {
    const parsed = parseContractJson(`{
      "payload": {
        "type": "object",
        "description": "请求体",
        "properties": {
          "status": { "type": "string", "required": true, "description": "状态" },
          "items": {
            "type": "array",
            "required": true,
            "items": {
              "type": "object",
              "properties": {
                "sku": { "type": "string", "required": true, "description": "SKU" }
              }
            }
          }
        }
      }
    }`);

    expect(parsed.ok).toBe(true);
    if (!parsed.ok) {
      throw new Error("expected valid parse");
    }

    expect(parsed.nodes[0]?.children?.[1]?.item?.children?.[0]?.name).toBe("sku");
  });

  it("parses JSONC text with comments and trailing commas", () => {
    const parsed = parseContractJson(`{
      // 请求体根节点
      "payload": {
        "type": "object",
        "description": "请求体",
        "properties": {
          "status": {
            "type": "string",
            "required": true, // 必填状态
          },
        },
      },
    }`);

    expect(parsed.ok).toBe(true);
    if (!parsed.ok) {
      throw new Error("expected valid JSONC parse");
    }

    expect(parsed.nodes[0]?.children?.[0]?.name).toBe("status");
    expect(parsed.nodes[0]?.children?.[0]?.required).toBe(true);
  });

  it("returns line-aware validation errors for invalid JSON text", () => {
    const parsed = parseContractJson(`{
      "payload": {
        "type": "object",
        "properties": {
          "status": { "type": "string" "required": true }
        }
      }
    }`);

    expect(parsed.ok).toBe(false);
    if (parsed.ok) {
      throw new Error("expected invalid parse");
    }

    expect(parsed.error.message).toContain("JSON");
    expect(parsed.error.line).toBeGreaterThan(0);
  });

  it("extracts JSON5 line and column fallback from colon-formatted parser messages", () => {
    const parsed = parseContractJson('{ "payload": ');

    expect(parsed.ok).toBe(false);
    if (parsed.ok) {
      throw new Error("expected invalid parse");
    }

    expect(parsed.error.line).toBe(1);
    expect(parsed.error.column).toBe(14);
  });

  it("splits transport parameters from request body contract and rebuilds request params", () => {
    const requestParams: ToolRequestParam[] = [
      {
        location: "Path",
        name: "shipmentId",
        type: "string",
        required: true,
        description: "发货单ID",
      },
      {
        location: "Query",
        name: "verbose",
        type: "boolean",
        required: false,
        description: "是否冗长",
      },
      {
        location: "Body",
        name: "payload",
        type: "object",
        required: true,
        description: "请求体",
        schema: objectNode({
          children: [
            {
              id: "reason",
              name: "reason",
              type: "string",
              description: "原因",
              required: true,
              children: [],
              item: null,
              additionalProperties: null,
            },
          ],
        }),
      },
    ];

    const split = buildBodyContractFromRequestParams(requestParams);

    expect(split.transportParams).toHaveLength(2);
    expect(split.bodyNodes[0]?.children?.[0]?.name).toBe("reason");

    const rebuilt = buildRequestParamsFromContracts(split.transportParams, split.bodyNodes);

    expect(rebuilt.some((param) => param.location === "Path" && param.name === "shipmentId")).toBe(true);
    expect(rebuilt.some((param) => param.location === "Body" && param.schema?.children?.[0]?.name === "reason")).toBe(
      true,
    );
  });

  it("preserves system default metadata when rebuilding request params", () => {
    const requestParams: ToolRequestParam[] = [
      {
        location: "Query",
        name: "pageSize",
        type: "integer",
        required: true,
        description: "每页数量",
        valueSource: "SystemDefault",
        defaultValue: 20,
      },
      {
        location: "Body",
        name: "pageNum",
        type: "integer",
        required: true,
        description: "页码",
        valueSource: "SystemDefault",
        defaultValue: 1,
      },
    ];

    const split = buildBodyContractFromRequestParams(requestParams);
    const rebuilt = buildRequestParamsFromContracts(split.transportParams, split.bodyNodes);

    expect(rebuilt.find((param) => param.name === "pageSize")).toMatchObject({
      valueSource: "SystemDefault",
      defaultValue: 20,
    });
    expect(rebuilt.find((param) => param.name === "pageNum")).toMatchObject({
      valueSource: "SystemDefault",
      defaultValue: 1,
    });
  });

  it("formats editor JSON consistently", () => {
    expect(formatContractJson('{"payload":{"type":"object"}}')).toBe('{\n  "payload": {\n    "type": "object"\n  }\n}');
  });

  it("formats JSONC input into normalized JSON output", () => {
    expect(
      formatContractJson(`{
        // root
        "payload": {
          "type": "object",
        },
      }`),
    ).toBe('{\n  "payload": {\n    "type": "object"\n  }\n}');
  });
});
