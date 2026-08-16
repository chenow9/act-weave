import { describe, expect, it } from "vitest";

import type { ToolSchemaNode } from "../types/domain";
import { buildHTTPActionConfig, HTTP_ACTION_SCHEMA_VERSION, joinHttpEndpoint } from "./tool-http-action";

function node(name: string, location: string, required = false): ToolSchemaNode {
  return { id: name, name, location, type: "string", description: "", required };
}

describe("HTTP Tool action serialization", () => {
  it("uses the runtime schema version and retains Path/Header mappings", () => {
    expect(HTTP_ACTION_SCHEMA_VERSION).toBe("http.v1");
    expect(
      buildHTTPActionConfig("get", "/users/{id}", "application/json", [
        node("id", "Path"),
        node("X-Trace-Id", "Header"),
      ]),
    ).toEqual({
      method: "GET",
      path: "/users/{id}",
      parameters: [
        { name: "id", in: "path", input: "id", required: true },
        { name: "X-Trace-Id", in: "header", input: "X-Trace-Id" },
      ],
    });
  });

  it("keeps colliding Path and Body names as distinct inputs", () => {
    expect(
      buildHTTPActionConfig("put", "/scenes/{id}", "application/json", [
        node("id", "Path", true),
        node("id", "Body", true),
      ]),
    ).toEqual({
      method: "PUT",
      path: "/scenes/{id}",
      parameters: [
        { name: "id", in: "path", input: "id", required: true },
        { name: "id", in: "body", input: "body_id", required: true },
      ],
      requestBody: { contentType: "application/json" },
    });
  });

  it("joins domain, base path, and action path without doubling /api", () => {
    expect(joinHttpEndpoint("https://ai.zkys-tech.com/api", "/api", "/api/v1/recognition/scenes/{id}")).toBe(
      "https://ai.zkys-tech.com/api/v1/recognition/scenes/{id}",
    );
    expect(joinHttpEndpoint("http://127.0.0.1:18765", "/", "/ops/monthly-overview")).toBe(
      "http://127.0.0.1:18765/ops/monthly-overview",
    );
    expect(joinHttpEndpoint("https://api.example.com", "/v1", "/users")).toBe("https://api.example.com/v1/users");
    expect(joinHttpEndpoint("https://api.example.com/v1", "/v1", "/v1/users")).toBe("https://api.example.com/v1/users");
    expect(joinHttpEndpoint("", "/", "/ops/monthly-overview")).toBe("/ops/monthly-overview");
  });

  it("adds the selected content type when Body parameters exist", () => {
    expect(
      buildHTTPActionConfig("post", "/orders", "application/x-www-form-urlencoded", [node("orderId", "Body", true)]),
    ).toMatchObject({
      parameters: [{ name: "orderId", in: "body", input: "orderId", required: true }],
      requestBody: { contentType: "application/x-www-form-urlencoded" },
    });
  });
});
