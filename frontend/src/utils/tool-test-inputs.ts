import type { ToolRequestParam, ToolSchemaNode } from "../types/domain";

function fallbackValueForNode(node: ToolSchemaNode): unknown {
  if (node.valueSource === "SystemDefault" && node.defaultValue !== undefined) return node.defaultValue;
  if (node.example !== undefined && node.example !== "") return node.example;
  switch (node.type) {
    case "integer":
    case "number":
      return 0;
    case "boolean":
      return false;
    case "object":
      return Object.fromEntries(
        (node.children || [])
          .filter((child) => child.required)
          .map((child) => [child.name, fallbackValueForNode(child)]),
      );
    case "array":
      return node.item ? [fallbackValueForNode(node.item)] : [];
    default:
      return "";
  }
}

export function buildDefaultToolTestInput(params: ToolRequestParam[]): Record<string, unknown> {
  return Object.fromEntries(
    params
      .filter((param) => param.required)
      .map((param) => {
        if (param.valueSource === "SystemDefault" && param.defaultValue !== undefined)
          return [param.name, param.defaultValue];
        if (param.schema) return [param.name, fallbackValueForNode(param.schema)];
        if (param.type === "integer" || param.type === "number") return [param.name, 0];
        if (param.type === "boolean") return [param.name, false];
        return [param.name, ""];
      }),
  );
}
