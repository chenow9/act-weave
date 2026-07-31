import type { Tool, ToolRequestParam, ToolSchemaNode } from "../types/domain";

type ActionParameter = {
  name: string;
  in?: string;
  input?: string;
  required?: boolean;
};

const COMMON_DEFAULTS: Record<string, unknown> = {
  pageNum: 1,
  pageNumber: 1,
  page: 1,
  pageSize: 10,
  size: 10,
  limit: 10,
  offset: 0,
  current: 1,
  perPage: 10,
  per_page: 10,
  page_num: 1,
  page_size: 10,
};

function fallbackValueForNode(node: ToolSchemaNode): unknown {
  if (node.valueSource === "SystemDefault" && node.defaultValue !== undefined) return node.defaultValue;
  if (node.defaultValue !== undefined) return node.defaultValue;
  if (node.example !== undefined && node.example !== "") return node.example;
  if (node.enumValues?.length) return node.enumValues[0];
  const common = COMMON_DEFAULTS[node.name];
  if (common !== undefined) return common;
  switch (node.type) {
    case "integer":
    case "number":
      return 0;
    case "boolean":
      return false;
    case "object":
      return Object.fromEntries(
        (node.children || [])
          .filter((child) => child.required || child.defaultValue !== undefined || child.example !== undefined)
          .map((child) => [child.name, fallbackValueForNode(child)]),
      );
    case "array":
      return node.item ? [fallbackValueForNode(node.item)] : [];
    default:
      // Path-like ids get a non-empty demo value so upstream path substitution works.
      if (/id$|Id$|ID$|key$|Key$|code$|Code$|slug$/i.test(node.name)) return "1";
      return "";
  }
}

function fallbackValueForParam(param: ToolRequestParam): unknown {
  if (param.valueSource === "SystemDefault" && param.defaultValue !== undefined) return param.defaultValue;
  if (param.defaultValue !== undefined) return param.defaultValue;
  if (param.schema) return fallbackValueForNode(param.schema);
  const common = COMMON_DEFAULTS[param.name];
  if (common !== undefined) return common;
  if (param.type === "integer" || param.type === "number") return 0;
  if (param.type === "boolean") return false;
  if (param.location === "Path" || /id$|Id$|ID$|key$|Key$|code$|Code$|slug$/i.test(param.name)) return "1";
  return "";
}

function shouldIncludeParam(param: ToolRequestParam): boolean {
  if (param.required) return true;
  if (param.location === "Path") return true;
  if (param.valueSource === "SystemDefault" && param.defaultValue !== undefined) return true;
  if (param.defaultValue !== undefined) return true;
  if (param.schema?.example !== undefined && param.schema.example !== "") return true;
  if (COMMON_DEFAULTS[param.name] !== undefined) return true;
  // Include optional query/body fields that are likely needed for list APIs.
  if (param.location === "Query") return true;
  return false;
}

function pathPlaceholders(path: string): string[] {
  const names: string[] = [];
  const re = /\{([^{}]+)\}/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(path))) {
    const name = match[1].trim();
    if (name && !names.includes(name)) names.push(name);
  }
  return names;
}

function normalizeLocation(value: unknown): string {
  const raw = String(value || "")
    .trim()
    .toLowerCase();
  if (raw === "path") return "Path";
  if (raw === "query") return "Query";
  if (raw === "header") return "Header";
  if (raw === "body") return "Body";
  return "Query";
}

/**
 * Merge requestParams with actionConfig.parameters + path placeholders so test UI
 * still has fields when OpenAPI left inputSchema empty.
 */
export function collectToolTestParams(tool: Pick<Tool, "requestParams" | "actionConfig">): ToolRequestParam[] {
  const byName = new Map<string, ToolRequestParam>();
  for (const param of tool.requestParams || []) {
    if (!param?.name) continue;
    byName.set(param.name, param);
  }

  const actionConfig = (tool.actionConfig || {}) as {
    path?: string;
    parameters?: ActionParameter[];
  };
  const parameters = Array.isArray(actionConfig.parameters) ? actionConfig.parameters : [];
  for (const item of parameters) {
    const name = String(item.input || item.name || "").trim();
    if (!name || byName.has(name)) continue;
    const location = normalizeLocation(item.in);
    byName.set(name, {
      location,
      name,
      type: "string",
      required: Boolean(item.required) || location === "Path",
      description: "",
    });
  }

  const path = String(actionConfig.path || "");
  for (const name of pathPlaceholders(path)) {
    if (byName.has(name)) continue;
    byName.set(name, {
      location: "Path",
      name,
      type: "string",
      required: true,
      description: "",
    });
  }

  // If still no query params on a GET-like list path, inject common pagination helpers
  // so 配置分页-style endpoints don't send bare {}.
  const method = String((tool.actionConfig as { method?: string })?.method || "GET").toUpperCase();
  const hasQuery = [...byName.values()].some((p) => p.location === "Query");
  if (method === "GET" && !hasQuery && !pathPlaceholders(path).length) {
    for (const [name, value] of Object.entries({ pageNum: 1, pageSize: 10 })) {
      if (byName.has(name)) continue;
      byName.set(name, {
        location: "Query",
        name,
        type: "integer",
        required: false,
        description: "自动补全的分页参数（OpenAPI 未声明时）",
        defaultValue: value,
        valueSource: "SystemDefault",
      });
    }
  }

  return [...byName.values()];
}

/**
 * Build default test input for Tool Runtime test / batch test.
 * Includes path params, required fields, system defaults, common pagination, and query fields.
 */
export function buildDefaultToolTestInput(
  paramsOrTool: ToolRequestParam[] | Pick<Tool, "requestParams" | "actionConfig">,
): Record<string, unknown> {
  const params = Array.isArray(paramsOrTool) ? paramsOrTool : collectToolTestParams(paramsOrTool);

  const entries = params.filter(shouldIncludeParam).map((param) => [param.name, fallbackValueForParam(param)] as const);

  // Never return completely empty for tools that have action path placeholders.
  if (!entries.length && !Array.isArray(paramsOrTool)) {
    const path = String(paramsOrTool.actionConfig?.path || "");
    return Object.fromEntries(pathPlaceholders(path).map((name) => [name, "1"]));
  }

  return Object.fromEntries(entries);
}
