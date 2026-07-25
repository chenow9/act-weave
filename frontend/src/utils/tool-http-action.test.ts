import { describe, expect, it } from "vitest";

import type { ToolSchemaNode } from "../types/domain";
import { buildHTTPActionConfig, HTTP_ACTION_SCHEMA_VERSION } from "./tool-http-action";

function node(name: string, location: string, required = false): ToolSchemaNode {
  return { id: name, name, location, type: "string", description: "", required };
}

describe("HTTP Tool action serialization", () => {
  it("uses the runtime schema version and retains Path/Header mappings", () => {
    expect(HTTP_ACTION_SCHEMA_VERSION).toBe("http.v1");
    expect(buildHTTPActionConfig("get", "/users/{id}", "application/json", [
      node("id", "Path"),
      node("X-Trace-Id", "Header"),
    ])).toEqual({
      method: "GET",
      path: "/users/{id}",
      parameters: [
        { name: "id", in: "path", input: "id", required: true },
        { name: "X-Trace-Id", in: "header", input: "X-Trace-Id" },
      ],
    });
  });

  it("adds the selected content type when Body parameters exist", () => {
    expect(buildHTTPActionConfig("post", "/orders", "application/x-www-form-urlencoded", [node("orderId", "Body", true)]))
      .toMatchObject({
        parameters: [{ name: "orderId", in: "body", input: "orderId", required: true }],
        requestBody: { contentType: "application/x-www-form-urlencoded" },
      });
  });
});
