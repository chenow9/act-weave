import { parse as parseYaml } from "yaml";

export interface OpenAPIPreviewRow {
  method: string;
  path: string;
  suggestedTool: string;
  statusText: string;
}

export interface OpenAPIPreviewResult {
  endpointCount: number;
  readyCount: number;
  rows: OpenAPIPreviewRow[];
  error: string;
}

const HTTP_METHODS = ["get", "post", "put", "delete", "patch", "options", "head", "trace"] as const;

export function parseOpenAPIPreview(source: string): OpenAPIPreviewResult {
  const trimmed = source.trim();
  if (!trimmed) {
    return emptyPreview();
  }

  let document: unknown;
  try {
    document = trimmed.startsWith("{") || trimmed.startsWith("[") ? JSON.parse(trimmed) : parseYaml(trimmed);
  } catch {
    return {
      ...emptyPreview(),
      error: "当前文档无法解析，请检查 OpenAPI JSON/YAML 格式。",
    };
  }

  if (!isRecord(document) || !isRecord(document.paths)) {
    return {
      ...emptyPreview(),
      error: "当前文档未识别到 OpenAPI paths。",
    };
  }

  const rows: OpenAPIPreviewRow[] = [];
  for (const [path, pathItem] of Object.entries(document.paths)) {
    if (!isRecord(pathItem)) {
      continue;
    }

    for (const method of HTTP_METHODS) {
      const operation = pathItem[method];
      if (!isRecord(operation)) {
        continue;
      }

      const suggestedTool = readPreferredString(operation.summary, operation.operationId) || fallbackToolId(method, path);
      rows.push({
        method: method.toUpperCase(),
        path,
        suggestedTool,
        statusText: suggestedTool ? "可生成" : "待确认",
      });
    }
  }

  rows.sort((left, right) => {
    if (left.path === right.path) {
      return left.method.localeCompare(right.method);
    }
    return left.path.localeCompare(right.path);
  });

  if (!rows.length) {
    return {
      ...emptyPreview(),
      error: "当前文档没有可识别的接口定义。",
    };
  }

  return {
    endpointCount: rows.length,
    readyCount: rows.filter((row) => row.statusText === "可生成").length,
    rows,
    error: "",
  };
}

function emptyPreview(): OpenAPIPreviewResult {
  return {
    endpointCount: 0,
    readyCount: 0,
    rows: [],
    error: "",
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readPreferredString(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }
  return "";
}

function fallbackToolId(method: string, path: string): string {
  const trimmed = path.replace(/^\/+|\/+$/g, "");
  if (!trimmed) {
    return `root.${method}`;
  }

  return `${trimmed
    .split("/")
    .filter(Boolean)
    .map((segment) => {
      if (segment.startsWith("{") && segment.endsWith("}")) {
        return `by-${segment.slice(1, -1)}`;
      }
      return segment;
    })
    .map(slugifySegment)
    .join(".")}.${method}`;
}

function slugifySegment(segment: string): string {
  return segment
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .replace(/[^a-zA-Z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .toLowerCase();
}
