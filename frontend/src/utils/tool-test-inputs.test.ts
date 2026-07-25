import { describe, expect, it } from "vitest";

import type { ToolRequestParam } from "../types/domain";
import { buildDefaultToolTestInput } from "./tool-test-inputs";

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
});
