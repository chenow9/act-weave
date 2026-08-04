/** OpenAPI import pure mappers (ZKL-64 item 10). */
import { tt } from "../../../i18n/tt";
import type { OpenAPIImport } from "../../../types/domain";
import { normalizeSchemaNode } from "./schema-tools";

export function importFromDTO(
  record: OpenAPIImportDTO,
  workspaceId: string,
  endpoints?: OpenAPIEndpointDTO[],
): OpenAPIImport {
  const normalizedEndpoints = (endpoints || []).map((endpoint) => ({
    id: endpoint.id,
    method: endpoint.method,
    path: endpoint.path,
    operationId: endpoint.operationId,
    summary: endpoint.summary,
    status: endpoint.ready ? "READY" : "ISSUES",
    ready: endpoint.ready,
    generatedCapabilityId: endpoint.generatedCapabilityId,
    issues: Array.isArray(endpoint.issues) ? endpoint.issues.map(String) : [],
    requestContract: normalizeSchemaNode(endpoint.inputSchema, { name: "request", type: "object" }),
    responseContract: normalizeSchemaNode(endpoint.outputSchema, { name: "response", type: "object" }),
  }));
  return {
    id: record.id,
    workspaceId,
    providerId: record.providerId,
    connectionId: record.connectionId,
    source: record.fileName || record.sourceUri || "Provider OpenAPI",
    sourceType: record.sourceType,
    sourceUri: record.sourceUri,
    sourceRevision: record.sourceRevision,
    fileName: record.fileName,
    contentSha256: record.contentSha256,
    parserVersion: record.parserVersion,
    totalEndpoints: record.totalEndpoints,
    readyEndpoints: record.readyEndpoints,
    issueCount: record.issueCount,
    issues: record.issueCount ? [tt("openapi.endpointsNeedAttention", { n: record.issueCount })] : [],
    status: record.issueCount ? "Issues" : "Ready",
    createdAt: record.createdAt,
    updatedAt: record.updatedAt,
    detail: endpoints
      ? {
          endpoints: normalizedEndpoints,
          requestContract: normalizedEndpoints[0]?.requestContract || null,
          responseContract: normalizedEndpoints[0]?.responseContract || null,
        }
      : undefined,
  };
}

export interface OpenAPIImportDTO {
  id: string;
  providerId?: string;
  connectionId?: string;
  sourceType: string;
  sourceUri?: string;
  sourceRevision?: string;
  fileName: string;
  contentSha256: string;
  parserVersion: string;
  status: string;
  totalEndpoints: number;
  readyEndpoints: number;
  issueCount: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface OpenAPIEndpointDTO {
  id: string;
  method: string;
  path: string;
  operationId: string;
  summary: string;
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  issues: unknown[];
  ready: boolean;
  generatedCapabilityId?: string;
}

export function filterOpenAPIImports(items: OpenAPIImport[], query: string, status?: "Ready" | "Issues") {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((record) => {
    if (status && record.status !== status) return false;
    if (!needle) return true;
    return [record.fileName, record.source, record.sourceType, record.status].some((value) =>
      value.toLocaleLowerCase().includes(needle),
    );
  });
}

export function isAuthenticationInfrastructureEndpoint(path: string) {
  const normalized = `/${path.trim().replace(/^\/+|\/+$/g, "")}`.toLocaleLowerCase();
  return normalized === "/oauth2/token" || normalized === "/oauth2/revoke";
}

export function sortOpenAPIImports(items: OpenAPIImport[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set([
    "fileName",
    "totalEndpoints",
    "readyEndpoints",
    "issueCount",
    "status",
    "createdAt",
    "updatedAt",
  ]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const leftValue = left[sortBy as keyof OpenAPIImport];
    const rightValue = right[sortBy as keyof OpenAPIImport];
    const comparison =
      typeof leftValue === "number" && typeof rightValue === "number"
        ? leftValue - rightValue
        : String(leftValue || "").localeCompare(String(rightValue || ""), "zh-Hans");
    return order === "asc" ? comparison : -comparison;
  });
}
