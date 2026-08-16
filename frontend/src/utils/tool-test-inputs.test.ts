import { describe, expect, it } from "vitest";

import type { Tool, ToolRequestParam } from "../types/domain";
import { buildDefaultToolTestInput, collectToolTestParams } from "./tool-test-inputs";

describe("tool test input defaults", () => {
  it("builds default test input values from request schema examples and fallback types", () => {
    const params: ToolRequestParam[] = [
      {
        location: "Path",
        name: "shipmentId",
        type: "string",
        required: true,
        description: "发货单 ID",
        schema: { id: "a", name: "shipmentId", type: "string", description: "", required: true, example: "S8820" },
      },
      {
        location: "Body",
        name: "payload",
        type: "object",
        required: true,
        description: "请求体",
        schema: {
          id: "b",
          name: "payload",
          type: "object",
          description: "",
          required: true,
          children: [{ id: "c", name: "force", type: "boolean", description: "", required: true }],
        },
      },
    ];

    expect(buildDefaultToolTestInput(params)).toEqual({
      shipmentId: "S8820",
      payload: { force: false },
    });
  });

  it("uses declared system defaults for generated test input", () => {
    const params: ToolRequestParam[] = [
      {
        location: "Query",
        name: "pageSize",
        type: "integer",
        required: true,
        description: "每页数量",
        valueSource: "SystemDefault",
        defaultValue: 20,
      },
    ];

    expect(buildDefaultToolTestInput(params)).toEqual({
      pageSize: 20,
    });
  });

  it("fills path placeholders and common pagination when OpenAPI schema is empty", () => {
    const tool = {
      requestParams: [],
      actionConfig: {
        method: "GET",
        path: "/api/v1/configs/key/{key}",
        parameters: [{ in: "path", name: "key", input: "key", required: true }],
      },
    } as Pick<Tool, "requestParams" | "actionConfig">;

    expect(collectToolTestParams(tool).map((p) => p.name)).toEqual(["key"]);
    expect(buildDefaultToolTestInput(tool)).toEqual({ key: "1" });
  });

  it("injects pageNum/pageSize for empty GET list endpoints", () => {
    const tool = {
      requestParams: [],
      actionConfig: {
        method: "GET",
        path: "/api/v1/configs",
        parameters: [],
      },
    } as Pick<Tool, "requestParams" | "actionConfig">;

    expect(buildDefaultToolTestInput(tool)).toEqual({
      pageNum: 1,
      pageSize: 10,
    });
  });

  it("includes optional query params with common defaults", () => {
    const params: ToolRequestParam[] = [
      {
        location: "Query",
        name: "pageNum",
        type: "integer",
        required: false,
        description: "",
      },
      {
        location: "Query",
        name: "pageSize",
        type: "integer",
        required: false,
        description: "",
      },
      {
        location: "Query",
        name: "keyword",
        type: "string",
        required: false,
        description: "",
      },
    ];
    expect(buildDefaultToolTestInput(params)).toEqual({
      pageNum: 1,
      pageSize: 10,
      keyword: "",
    });
  });

  it("keeps Path and Body id as separate fields without showing body_id", () => {
    const tool = {
      requestParams: [
        { location: "Path", name: "id", type: "string", required: true, description: "场景ID" },
        { location: "Body", name: "id", type: "string", required: true, description: "场景ID，新增时为空。" },
      ],
      actionConfig: {
        method: "PUT",
        path: "/scenes/{id}",
        parameters: [
          { in: "path", name: "id", input: "id", required: true },
          { in: "body", name: "id", input: "body_id", required: true },
        ],
      },
    } as Pick<Tool, "requestParams" | "actionConfig">;

    const params = collectToolTestParams(tool);
    expect(params.map((item) => `${item.location}:${item.name}`)).toEqual(["Path:id", "Body:id"]);
    expect(params.some((item) => item.name === "body_id")).toBe(false);
    expect(params.find((item) => item.location === "Body")?.inputKey).toBe("body_id");
    expect(buildDefaultToolTestInput(tool)).toEqual({
      id: "1",
      body_id: "1",
    });
  });
});
