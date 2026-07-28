/** Schema + Tool pure mappers (ZKL-64 item 10). */
import type {
  ServiceConnection,
  Tool,
  ToolRequestParam,
  ToolResponseField,
  ToolSchemaNode,
  ToolSchemaNodeType,
  ToolListQuery,
  ToolTestResult,
  ToolVersion,
} from "../../../types/domain";
import { getToolRunStatus } from "../../../utils/tool-governance";
import { getToolTypeLabel } from "../../../utils/tool-presentation";

export function createSchemaNodeId(prefix: string) {
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function asSchemaNodeType(value: unknown): ToolSchemaNodeType {
  if (value === "object" || value === "array" || value === "boolean" || value === "integer" || value === "number") {
    return value;
  }
  return "string";
}

export function asToolParameterValueSource(value: unknown) {
  return value === "SystemDefault" || value === "UserInput" ? value : undefined;
}

export function normalizeParameterLocation(value: unknown) {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim().toLocaleLowerCase();
  return (
    ({ path: "Path", query: "Query", header: "Header", body: "Body" } as Record<string, string>)[normalized] || value
  );
}

export function normalizeSchemaNode(node: unknown, fallback: Partial<ToolSchemaNode> = {}): ToolSchemaNode {
  const source = isRecord(node) ? node : {};
  const sourceRequired = Array.isArray(source.required)
    ? new Set(source.required.filter((value): value is string => typeof value === "string"))
    : new Set<string>();
  const sourceName =
    typeof source.name === "string"
      ? source.name
      : typeof source["x-actweave-parameter-name"] === "string"
        ? source["x-actweave-parameter-name"]
        : fallback.name || "";
  const sourceEnum = Array.isArray(source.enumValues)
    ? source.enumValues
    : Array.isArray(source.enum)
      ? source.enum
      : [];
  const normalized: ToolSchemaNode = {
    id: typeof source.id === "string" && source.id ? source.id : createSchemaNodeId(fallback.name || "schema"),
    name: sourceName,
    type: asSchemaNodeType(source.type ?? fallback.type),
    description: typeof source.description === "string" ? source.description : fallback.description || "",
    required: typeof source.required === "boolean" ? source.required : (fallback.required ?? false),
    location: normalizeParameterLocation(source.location ?? source["x-actweave-location"] ?? fallback.location),
    format: typeof source.format === "string" ? source.format : undefined,
    nullable: typeof source.nullable === "boolean" ? source.nullable : undefined,
    example: typeof source.example === "string" ? source.example : undefined,
    enumValues: sourceEnum.filter((item): item is string => typeof item === "string"),
    valueSource: asToolParameterValueSource(
      source.valueSource ?? source["x-actweave-value-source"] ?? fallback.valueSource,
    ),
    defaultValue: source.defaultValue ?? source.default ?? fallback.defaultValue,
    children: undefined,
    item: undefined,
    additionalProperties: undefined,
  };

  const children = Array.isArray(source.children)
    ? source.children.map((child) => normalizeSchemaNode(child))
    : isRecord(source.properties)
      ? Object.entries(source.properties).map(([name, child]) =>
          normalizeSchemaNode(child, { name, required: sourceRequired.has(name) }),
        )
      : [];
  if (children.length) {
    normalized.children = children;
  }

  const item = source.item ?? source.items;
  if (item) {
    normalized.item = normalizeSchemaNode(item, { name: "item", required: true });
  }

  if (isRecord(source.additionalProperties)) {
    normalized.additionalProperties = normalizeSchemaNode(source.additionalProperties, {
      name: "value",
      required: false,
    });
  }

  return normalized;
}

export function requestParamToSchema(param: ToolRequestParam): ToolSchemaNode {
  return normalizeSchemaNode(param.schema || param, {
    name: param.name,
    type: asSchemaNodeType(param.type),
    description: param.description,
    required: param.required,
    location: param.location,
  });
}

export function responseFieldToSchema(field: ToolResponseField): ToolSchemaNode {
  return normalizeSchemaNode(field.schema || field, {
    name: field.name,
    type: asSchemaNodeType(field.type),
    description: field.description,
    required: true,
  });
}

export function normalizeToolVersion(version: ToolVersionDTO): ToolVersion {
  return {
    ...version,
    actionConfig: version.actionConfig || {},
    inputSchema: version.inputSchema || {},
    outputSchema: version.outputSchema || {},
    errorMappings: version.errorMappings || {},
    runtimePolicy: version.runtimePolicy || {},
  };
}

export function normalizeToolTestResult(result?: ToolTestResult): ToolTestResult | undefined {
  if (!result) return undefined;
  const status = String(result.status || "")
    .trim()
    .toLocaleUpperCase();
  return {
    ...result,
    status: status === "SUCCEEDED" || status === "PASSED" || status === "TESTED" ? "Tested" : "Failed",
  };
}

export function normalizeToolErrorMappings(mappings: ToolVersionDTO["errorMappings"]): Tool["errorMappings"] {
  if (Array.isArray(mappings)) return mappings as Tool["errorMappings"];
  if (!isRecord(mappings)) return [];
  if (Array.isArray(mappings.mappings)) return mappings.mappings as Tool["errorMappings"];
  return Object.entries(mappings).flatMap(([protocolStatus, mapping]) => {
    if (!isRecord(mapping)) return [];
    const errorCode = String(mapping.errorCode || mapping.code || "").trim();
    if (!errorCode) return [];
    return [
      {
        protocolStatus,
        errorCode,
        agentAdvice: typeof mapping.agentAdvice === "string" ? mapping.agentAdvice : "",
      },
    ];
  });
}

export function toolFromDTO(
  tool: ToolDTO,
  workspaceId: string,
  rawVersions: ToolVersionDTO[],
  testResult?: ToolTestResult,
): Tool {
  const versions = rawVersions.map(normalizeToolVersion).sort((left, right) => left.versionNo - right.versionNo);
  const draftVersion =
    [...versions].reverse().find((version) => version.lifecycleStatus !== "PUBLISHED") || versions.at(-1);
  const actionConfig = draftVersion?.actionConfig || {};
  const inputNodes = schemaNodes(draftVersion?.inputSchema);
  const outputNodes = schemaNodes(draftVersion?.outputSchema);
  const runtimePolicy = draftVersion?.runtimePolicy || {};
  const errorMappings = normalizeToolErrorMappings(draftVersion?.errorMappings || {});
  const lifecycle = tool.status === "DISABLED" ? "Disabled" : lifecycleLabel(draftVersion?.lifecycleStatus);
  return {
    id: tool.id,
    workspaceId,
    providerId: tool.providerId,
    sourceAssetId: tool.sourceAssetId,
    sourceEndpointId: tool.sourceEndpointId,
    connectionId: draftVersion?.defaultConnectionId || tool.defaultConnectionId || "",
    defaultConnectionId: tool.defaultConnectionId,
    name: tool.name,
    slug: tool.slug,
    protocol: draftVersion?.executorType || "HTTP",
    actionConfig,
    actionConfigSchemaVersion: draftVersion?.actionSchemaVersion || "http.v1",
    description: tool.description,
    status: lifecycle,
    capabilityStatus: tool.status,
    activeReleaseId: tool.activeReleaseId,
    versions,
    draftVersion,
    requestParams: inputNodes.map((node) => ({
      location: node.location || "Body",
      name: node.name,
      type: node.type,
      required: node.required,
      description: node.description,
      schema: node,
    })),
    responseFields: outputNodes.map((node) => ({
      name: node.name,
      type: node.type,
      description: node.description,
      schema: node,
    })),
    errorMappings,
    runtimePolicy: {
      timeoutMs: Number(runtimePolicy.timeoutMs) || 8000,
      retryCount: Number(runtimePolicy.retryCount) || 0,
      backoffPolicy: String(runtimePolicy.backoffPolicy || "exponential"),
      idempotencyPolicy: String(runtimePolicy.idempotencyPolicy || "header: Idempotency-Key"),
      rateLimitPolicy: String(runtimePolicy.rateLimitPolicy || "60 rpm"),
    },
    lastTestResult: normalizeToolTestResult(testResult),
    createdBy: tool.createdBy,
    updatedBy: tool.updatedBy,
    createdAt: tool.createdAt,
    updatedAt: tool.updatedAt,
    lockVersion: tool.lockVersion,
  };
}

export function lifecycleLabel(status?: ToolVersion["lifecycleStatus"]): Tool["status"] {
  if (status === "REVIEW") return "Review";
  if (status === "TESTED") return "Tested";
  if (status === "PUBLISHED") return "Published";
  return "Draft";
}

export function schemaNodes(schema?: Record<string, unknown>) {
  if (!schema || !isRecord(schema)) return [];
  const properties = isRecord(schema.properties) ? schema.properties : schema;
  const requiredNames = Array.isArray(schema.required)
    ? new Set(schema.required.filter((value): value is string => typeof value === "string"))
    : new Set<string>();
  return Object.entries(properties)
    .filter(([name]) => !["type", "required", "description", "additionalProperties", "items"].includes(name))
    .map(([name, value]) => normalizeSchemaNode(value, { name, required: requiredNames.has(name) }));
}

export function toolDraftPayload(tool: Tool) {
  const draft = tool.draftVersion;
  return {
    ...(tool.sourceAssetId || draft?.providerAssetId
      ? { providerAssetId: tool.sourceAssetId || draft?.providerAssetId }
      : {}),
    ...(tool.connectionId ? { defaultConnectionId: tool.connectionId } : {}),
    actionSchemaVersion: tool.actionConfigSchemaVersion || draft?.actionSchemaVersion || "http.v1",
    actionConfig: tool.actionConfig || {},
    inputSchema: nodesToJSONSchema(tool.requestParams.map(requestParamToSchema)),
    outputSchema: nodesToJSONSchema(tool.responseFields.map(responseFieldToSchema)),
    errorMappings: Object.fromEntries(
      (tool.errorMappings || []).map((mapping) => [
        mapping.protocolStatus.toUpperCase(),
        {
          errorCode: mapping.errorCode,
          ...(mapping.agentAdvice ? { agentAdvice: mapping.agentAdvice } : {}),
        },
      ]),
    ),
    runtimePolicy: tool.runtimePolicy || {},
    riskLevel: draft?.riskLevel || "LOW",
    sideEffectLevel: draft?.sideEffectLevel || inferSideEffect(tool.actionConfig),
    requiresConfirmation: draft?.requiresConfirmation || false,
  };
}

export function nodesToJSONSchema(nodes: ToolSchemaNode[]): Record<string, unknown> {
  const properties = Object.fromEntries(
    nodes.filter((node) => node.name).map((node) => [node.name, nodeToJSONSchema(node)]),
  );
  const required = nodes.filter((node) => node.name && node.required).map((node) => node.name);
  return { type: "object", properties, ...(required.length ? { required } : {}) };
}

export function nodeToJSONSchema(node: ToolSchemaNode): Record<string, unknown> {
  const value: Record<string, unknown> = {
    type: node.type,
    ...(node.description ? { description: node.description } : {}),
    ...(node.location
      ? {
          "x-actweave-location": node.location.toLocaleLowerCase(),
          "x-actweave-parameter-name": node.name,
        }
      : {}),
    ...(node.format ? { format: node.format } : {}),
    ...(node.nullable !== undefined ? { nullable: node.nullable } : {}),
    ...(node.example !== undefined ? { example: node.example } : {}),
    ...(node.enumValues?.length ? { enum: node.enumValues } : {}),
    ...(node.defaultValue !== undefined ? { default: node.defaultValue } : {}),
    ...(node.valueSource ? { "x-actweave-value-source": node.valueSource } : {}),
  };
  if (node.type === "object") {
    const children = node.children || [];
    value.properties = Object.fromEntries(
      children.filter((child) => child.name).map((child) => [child.name, nodeToJSONSchema(child)]),
    );
    const required = children.filter((child) => child.name && child.required).map((child) => child.name);
    if (required.length) value.required = required;
    if (node.additionalProperties) value.additionalProperties = nodeToJSONSchema(node.additionalProperties);
  }
  if (node.type === "array" && node.item) value.items = nodeToJSONSchema(node.item);
  return value;
}

export function inferSideEffect(actionConfig: Record<string, unknown>) {
  const method = String(actionConfig.method || "GET").toUpperCase();
  return method === "GET" || method === "HEAD" ? "READ" : "WRITE";
}

export function toolSpecSignature(tool: Tool) {
  return JSON.stringify({
    connectionId: tool.connectionId,
    sourceAssetId: tool.sourceAssetId,
    actionConfig: tool.actionConfig,
    actionConfigSchemaVersion: tool.actionConfigSchemaVersion,
    requestParams: tool.requestParams,
    responseFields: tool.responseFields,
    errorMappings: tool.errorMappings,
    runtimePolicy: tool.runtimePolicy,
  });
}

export function slugify(value: string) {
  const slug = value
    .trim()
    .toLocaleLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
  return slug || `tool-${Date.now()}`;
}

export function numberValue(...values: unknown[]) {
  const value = values.find((item) => typeof item === "number" || (typeof item === "string" && item.trim() !== ""));
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

export function replaceByID<T extends { id: string }>(items: T[], replacement: T) {
  return items.some((item) => item.id === replacement.id)
    ? items.map((item) => (item.id === replacement.id ? replacement : item))
    : [replacement, ...items];
}

export interface ToolDTO {
  id: string;
  providerId: string;
  sourceAssetId?: string;
  defaultConnectionId?: string;
  sourceEndpointId?: string;
  name: string;
  slug: string;
  description: string;
  status: string;
  activeReleaseId?: string;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export interface ToolVersionDTO {
  id: string;
  versionNo: number;
  lifecycleStatus: ToolVersion["lifecycleStatus"];
  executorType: string;
  providerAssetId?: string;
  defaultConnectionId?: string;
  actionSchemaVersion: string;
  actionConfig: Record<string, unknown>;
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  errorMappings: Record<string, unknown> | unknown[];
  runtimePolicy: Record<string, unknown>;
  riskLevel: string;
  sideEffectLevel: string;
  requiresConfirmation: boolean;
  checksum: string;
  createdBy: string;
  updatedBy: string;
  publishedAt?: string;
  lockVersion: number;
}

export interface PublishToolDTO {
  releaseId: string;
  releaseNo: number;
  version: ToolVersionDTO;
  testId: string;
}

export interface GeneratedToolDTO {
  endpointId: string;
  tool: ToolDTO;
  draft: ToolVersionDTO;
}

export function filterTools(
  items: Tool[],
  query: string,
  status?: ToolListQuery["status"],
  type?: ToolListQuery["type"],
  resolveConnection: (tool: Tool) => ServiceConnection | undefined = () => undefined,
) {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((tool) => {
    const connection = status === "attention" || needle ? resolveConnection(tool) : undefined;
    if (status === "attention") {
      const runStatus = getToolRunStatus(tool, connection);
      if (runStatus.tone !== "danger" && runStatus.tone !== "warning") return false;
    }
    if (status && status !== "attention" && tool.status !== status) return false;
    if (type && getToolTypeLabel(tool) !== type) return false;
    if (!needle) return true;
    return [
      tool.name,
      tool.slug,
      tool.description,
      tool.protocol,
      tool.status,
      tool.actionConfig.path,
      connection?.name,
      connection?.alias,
      connection?.environment,
      connection?.protocolConfig.domain,
    ].some((value) =>
      String(value || "")
        .toLocaleLowerCase()
        .includes(needle),
    );
  });
}

export function sortTools(items: Tool[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set(["name", "protocol", "status", "updatedBy", "createdAt", "updatedAt"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const comparison = String(left[sortBy as keyof Tool] || "").localeCompare(
      String(right[sortBy as keyof Tool] || ""),
      "zh-Hans",
    );
    return order === "asc" ? comparison : -comparison;
  });
}
