import { describe, expect, it } from "vitest";

import {
  extractPathPlaceholderNames,
  mergeRequestParamsFromActionConfig,
  nodesToJSONSchema,
  normalizeSchemaNode,
  schemaNodes,
  uniqueSchemaPropertyKey,
} from "./schema-tools";
import type { ToolRequestParam, ToolSchemaNode } from "../../../types/domain";

function param(partial: Partial<ToolRequestParam> & Pick<ToolRequestParam, "name" | "location">): ToolRequestParam {
  return {
    type: "string",
    required: false,
    description: "",
    ...partial,
  };
}

describe("request param mapping", () => {
  it("extracts unique path placeholders", () => {
    expect(extractPathPlaceholderNames("/api/v1/recognition/scenes/{id}/models/{modelId}")).toEqual(["id", "modelId"]);
    expect(extractPathPlaceholderNames("/users")).toEqual([]);
  });

  it("keeps same-named Path and Body fields as separate params", () => {
    const inputSchema = {
      type: "object",
      required: ["id", "sceneName"],
      properties: {
        id: {
          type: "string",
          description: "场景ID",
          "x-actweave-location": "path",
          "x-actweave-parameter-name": "id",
        },
        sceneName: {
          type: "string",
          description: "场景名称。",
          "x-actweave-location": "body",
          "x-actweave-parameter-name": "sceneName",
        },
        body_id: {
          type: "string",
          description: "场景ID，新增时为空。",
          "x-actweave-location": "body",
          "x-actweave-parameter-name": "id",
        },
      },
    };
    const schemaParams = schemaNodes(inputSchema).map((node) => ({
      location: node.location || "Body",
      name: node.name,
      type: node.type,
      required: node.required,
      description: node.description,
      schema: node,
    }));
    const merged = mergeRequestParamsFromActionConfig(schemaParams, {
      path: "/api/v1/recognition/scenes/{id}",
      method: "PUT",
      parameters: [
        { in: "body", name: "id", input: "body_id" },
        { in: "path", name: "id", input: "id", required: true },
        { in: "body", name: "sceneName", input: "sceneName", required: true },
      ],
    });

    expect(merged.filter((item) => item.location === "Path" && item.name === "id")).toHaveLength(1);
    expect(merged.filter((item) => item.location === "Body" && item.name === "id")).toHaveLength(1);
    expect(merged.some((item) => item.name === "body_id")).toBe(false);
    expect(merged.find((item) => item.location === "Path" && item.name === "id")?.description).toBe("场景ID");
    expect(merged.find((item) => item.location === "Body" && item.name === "id")?.description).toBe(
      "场景ID，新增时为空。",
    );
  });

  it("recovers path placeholders when the schema is empty", () => {
    const merged = mergeRequestParamsFromActionConfig([], {
      path: "/v1/orders/{orderId}",
      parameters: [{ in: "query", name: "verbose", input: "verbose" }],
    });
    expect(merged).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ location: "Path", name: "orderId", required: true }),
        expect.objectContaining({ location: "Query", name: "verbose" }),
      ]),
    );
  });

  it("disambiguates colliding property keys when writing JSON schema", () => {
    const nodes: ToolSchemaNode[] = [
      normalizeSchemaNode({ name: "id", location: "Path", type: "string", required: true, description: "path id" }),
      normalizeSchemaNode({ name: "id", location: "Body", type: "string", required: false, description: "body id" }),
    ];
    const used = new Set<string>();
    expect(uniqueSchemaPropertyKey(nodes[0], used)).toBe("id");
    used.add("id");
    expect(uniqueSchemaPropertyKey(nodes[1], used)).toBe("body_id");

    const schema = nodesToJSONSchema(nodes);
    const properties = schema.properties as Record<string, Record<string, unknown>>;
    expect(Object.keys(properties)).toEqual(["id", "body_id"]);
    expect(properties.id["x-actweave-location"]).toBe("path");
    expect(properties.body_id["x-actweave-parameter-name"]).toBe("id");
    expect(properties.body_id["x-actweave-location"]).toBe("body");
  });
});
