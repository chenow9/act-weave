import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import {
  DEFAULT_PAGE_SIZE,
  PAGE_SIZE_OPTIONS,
  type ListPagination,
} from "../services/paginated-list";
import type {
  CapabilityProvider,
  OpenAPIImport,
  OpenAPIImportListQuery,
  OpenAPIImportRequest,
  PaginatedListQuery,
  ProviderAsset,
  ServiceConnection,
  ServiceConnectionListQuery,
  ServiceConnectionVerification,
  Tool,
  ToolListQuery,
  ToolRequestParam,
  ToolResponseField,
  ToolSchemaNode,
  ToolSchemaNodeType,
  ToolProtocol,
  ToolTestExecutionResult,
  ToolTestResult,
  ToolVersion,
} from "../types/domain";
import { getToolRunStatus } from "../utils/tool-governance";
import { getToolTypeLabel } from "../utils/tool-presentation";
import { useWorkspaceStore } from "./workspaces";

function createSchemaNodeId(prefix: string) {
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asSchemaNodeType(value: unknown): ToolSchemaNodeType {
  if (value === "object" || value === "array" || value === "boolean" || value === "integer" || value === "number") {
    return value;
  }
  return "string";
}

function asToolParameterValueSource(value: unknown) {
  return value === "SystemDefault" || value === "UserInput" ? value : undefined;
}

function normalizeParameterLocation(value: unknown) {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim().toLocaleLowerCase();
  return ({ path: "Path", query: "Query", header: "Header", body: "Body" } as Record<string, string>)[normalized] || value;
}

function normalizeSchemaNode(node: unknown, fallback: Partial<ToolSchemaNode> = {}): ToolSchemaNode {
  const source = isRecord(node) ? node : {};
  const sourceRequired = Array.isArray(source.required)
    ? new Set(source.required.filter((value): value is string => typeof value === "string"))
    : new Set<string>();
  const sourceName = typeof source.name === "string"
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
    required: typeof source.required === "boolean" ? source.required : fallback.required ?? false,
    location: normalizeParameterLocation(source.location ?? source["x-actweave-location"] ?? fallback.location),
    format: typeof source.format === "string" ? source.format : undefined,
    nullable: typeof source.nullable === "boolean" ? source.nullable : undefined,
    example: typeof source.example === "string" ? source.example : undefined,
    enumValues: sourceEnum.filter((item): item is string => typeof item === "string"),
    valueSource: asToolParameterValueSource(source.valueSource ?? source["x-actweave-value-source"] ?? fallback.valueSource),
    defaultValue: source.defaultValue ?? source.default ?? fallback.defaultValue,
    children: undefined,
    item: undefined,
    additionalProperties: undefined,
  };

  const children = Array.isArray(source.children)
    ? source.children.map((child) => normalizeSchemaNode(child))
    : isRecord(source.properties)
      ? Object.entries(source.properties).map(([name, child]) => normalizeSchemaNode(child, { name, required: sourceRequired.has(name) }))
      : [];
  if (children.length) {
    normalized.children = children;
  }

  const item = source.item ?? source.items;
  if (item) {
    normalized.item = normalizeSchemaNode(item, { name: "item", required: true });
  }

  if (isRecord(source.additionalProperties)) {
    normalized.additionalProperties = normalizeSchemaNode(source.additionalProperties, { name: "value", required: false });
  }

  return normalized;
}

function requestParamToSchema(param: ToolRequestParam): ToolSchemaNode {
  return normalizeSchemaNode(param.schema || param, {
    name: param.name,
    type: asSchemaNodeType(param.type),
    description: param.description,
    required: param.required,
    location: param.location,
  });
}

function responseFieldToSchema(field: ToolResponseField): ToolSchemaNode {
  return normalizeSchemaNode(field.schema || field, {
    name: field.name,
    type: asSchemaNodeType(field.type),
    description: field.description,
    required: true,
  });
}

function normalizeToolVersion(version: ToolVersionDTO): ToolVersion {
  return {
    ...version,
    actionConfig: version.actionConfig || {},
    inputSchema: version.inputSchema || {},
    outputSchema: version.outputSchema || {},
    errorMappings: version.errorMappings || {},
    runtimePolicy: version.runtimePolicy || {},
  };
}

function normalizeToolTestResult(result?: ToolTestResult): ToolTestResult | undefined {
  if (!result) return undefined;
  const status = String(result.status || "").trim().toLocaleUpperCase();
  return {
    ...result,
    status: status === "SUCCEEDED" || status === "PASSED" || status === "TESTED" ? "Tested" : "Failed",
  };
}

function normalizeToolErrorMappings(mappings: ToolVersionDTO["errorMappings"]): Tool["errorMappings"] {
  if (Array.isArray(mappings)) return mappings as Tool["errorMappings"];
  if (!isRecord(mappings)) return [];
  if (Array.isArray(mappings.mappings)) return mappings.mappings as Tool["errorMappings"];
  return Object.entries(mappings).flatMap(([protocolStatus, mapping]) => {
    if (!isRecord(mapping)) return [];
    const errorCode = String(mapping.errorCode || mapping.code || "").trim();
    if (!errorCode) return [];
    return [{
      protocolStatus,
      errorCode,
      agentAdvice: typeof mapping.agentAdvice === "string" ? mapping.agentAdvice : "",
    }];
  });
}

function toolFromDTO(tool: ToolDTO, workspaceId: string, rawVersions: ToolVersionDTO[], testResult?: ToolTestResult): Tool {
  const versions = rawVersions.map(normalizeToolVersion).sort((left, right) => left.versionNo - right.versionNo);
  const draftVersion = [...versions].reverse().find((version) => version.lifecycleStatus !== "PUBLISHED") || versions.at(-1);
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
    responseFields: outputNodes.map((node) => ({ name: node.name, type: node.type, description: node.description, schema: node })),
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

function lifecycleLabel(status?: ToolVersion["lifecycleStatus"]): Tool["status"] {
  if (status === "REVIEW") return "Review";
  if (status === "TESTED") return "Tested";
  if (status === "PUBLISHED") return "Published";
  return "Draft";
}

function schemaNodes(schema?: Record<string, unknown>) {
  if (!schema || !isRecord(schema)) return [];
  const properties = isRecord(schema.properties) ? schema.properties : schema;
  const requiredNames = Array.isArray(schema.required) ? new Set(schema.required.filter((value): value is string => typeof value === "string")) : new Set<string>();
  return Object.entries(properties)
    .filter(([name]) => !["type", "required", "description", "additionalProperties", "items"].includes(name))
    .map(([name, value]) => normalizeSchemaNode(value, { name, required: requiredNames.has(name) }));
}

function importFromDTO(record: OpenAPIImportDTO, workspaceId: string, endpoints?: OpenAPIEndpointDTO[]): OpenAPIImport {
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
    issues: record.issueCount ? [`${record.issueCount} 个端点需要处理`] : [],
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

interface IntegrationState {
  providers: CapabilityProvider[];
  providerAssetsByProvider: Record<string, ProviderAsset[]>;
  serviceConnectionPageItems: ServiceConnection[];
  serviceConnectionPagination: ListPagination;
  serviceConnectionListQuery: Required<Pick<PaginatedListQuery, "query" | "page" | "pageSize">> &
    Pick<ServiceConnectionListQuery, "status" | "sortBy" | "sortOrder">;
  serviceConnectionCatalog: ServiceConnection[];
  serviceConnectionRegistryTotal: number;
  toolConnectionsByWorkspace: Record<string, ServiceConnection[]>;
  tools: Tool[];
  toolPageItems: Tool[];
  toolPagination: ListPagination;
  toolListQuery: Required<Pick<PaginatedListQuery, "query" | "page" | "pageSize">> &
    Pick<ToolListQuery, "status" | "type" | "sortBy" | "sortOrder">;
  toolPageLoading: boolean;
  toolPageError: string | null;
  toolPageHasLoaded: boolean;
  protocols: ToolProtocol[];
  openAPIImportPageItems: OpenAPIImport[];
  openAPIImportPagination: ListPagination;
  openAPIImportListQuery: Required<Pick<PaginatedListQuery, "query" | "page" | "pageSize">> &
    Pick<OpenAPIImportListQuery, "status" | "sortBy" | "sortOrder">;
  openAPIImportCatalog: OpenAPIImport[];
  openAPIImportRegistryTotal: number;
  verificationByConnectionId: Record<string, ServiceConnectionVerification>;
  loading: boolean;
}

export const useIntegrationStore = defineStore("integration", {
  state: (): IntegrationState => ({
    providers: [],
    providerAssetsByProvider: {},
    serviceConnectionPageItems: [],
    serviceConnectionPagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    serviceConnectionListQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    serviceConnectionCatalog: [],
    serviceConnectionRegistryTotal: 0,
    toolConnectionsByWorkspace: {},
    tools: [],
    toolPageItems: [],
    toolPagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    toolListQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    toolPageLoading: false,
    toolPageError: null,
    toolPageHasLoaded: false,
    protocols: [],
    openAPIImportPageItems: [],
    openAPIImportPagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    openAPIImportListQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    openAPIImportCatalog: [],
    openAPIImportRegistryTotal: 0,
    verificationByConnectionId: {},
    loading: false,
  }),
  getters: {
    serviceConnections: (state) => state.serviceConnectionCatalog,
    openAPIImports: (state) => state.openAPIImportCatalog,
  },
  actions: {
    workspaceID() {
      const store = useWorkspaceStore();
      const workspaceID = store.activeWorkspaceId || store.items[0]?.id;
      if (!workspaceID) throw new Error("当前没有可用的业务空间。请先创建业务空间，或联系管理员加入已有空间。");
      return workspaceID;
    },
    async loadM2Assets() {
      this.loading = true;
      try {
        const [connections, tools, protocols, imports] = await Promise.all([
          this.loadServiceConnectionCatalog({ commit: false }),
          this.loadTools({ commit: false }),
          this.loadProtocols({ commit: false }),
          this.loadOpenAPIImportCatalog({ commit: false }),
        ]);
        this.serviceConnectionCatalog = connections;
        this.tools = tools;
        this.protocols = protocols;
        this.openAPIImportCatalog = imports;
      } finally {
        this.loading = false;
      }
    },
    async loadServiceConnectionPage(query: ServiceConnectionListQuery = {}) {
      const nextSortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.serviceConnectionListQuery.sortBy;
      const nextSortOrder = query.sortOrder !== undefined ? query.sortOrder || undefined : this.serviceConnectionListQuery.sortOrder;
      const requestQuery = {
        ...this.serviceConnectionListQuery,
        ...query,
        query: query.query ?? this.serviceConnectionListQuery.query,
        page: query.page ?? this.serviceConnectionListQuery.page,
        pageSize: query.pageSize ?? this.serviceConnectionListQuery.pageSize,
        sortBy: nextSortBy,
        sortOrder: nextSortBy ? nextSortOrder : undefined,
      };
      const catalog = await this.fetchServiceConnectionCatalog();
      const filtered = filterConnections(catalog, requestQuery.query, requestQuery.status);
      const sorted = sortConnections(filtered, requestQuery.sortBy, requestQuery.sortOrder);
      const page = Math.max(1, requestQuery.page);
      const pageSize = Math.max(1, requestQuery.pageSize);
      this.serviceConnectionPageItems = sorted.slice((page - 1) * pageSize, page * pageSize);
      this.serviceConnectionPagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
      this.serviceConnectionListQuery = {
        query: requestQuery.query,
        status: requestQuery.status,
        page,
        pageSize,
        sortBy: requestQuery.sortBy,
        sortOrder: requestQuery.sortOrder,
      };
      if (!requestQuery.query && !requestQuery.status) this.serviceConnectionRegistryTotal = catalog.length;
      return this.serviceConnectionPageItems;
    },
    async loadServiceConnectionCatalog(options: { commit?: boolean } = {}) {
      const connections = await this.fetchServiceConnectionCatalog();
      if (options.commit !== false) this.serviceConnectionCatalog = connections;
      this.serviceConnectionRegistryTotal = connections.length;
      return connections;
    },
    async fetchServiceConnectionCatalog() {
      const providers = this.providers.length ? this.providers : await this.loadProviders();
      const workspaceID = this.workspaceID();
      const responses = await Promise.all(
        providers.map(async (provider) => {
          const response = await apiClient.get<{ items: ConnectionDTO[] }>(
            `/workspaces/${workspaceID}/providers/${provider.id}/connections`,
          );
          return response.data.items.map((connection) => connectionFromDTO(connection, provider, workspaceID));
        }),
      );
      return responses.flat();
    },
    async loadOpenAPIImportPage(query: OpenAPIImportListQuery = {}) {
      const nextSortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.openAPIImportListQuery.sortBy;
      const nextSortOrder = query.sortOrder !== undefined ? query.sortOrder || undefined : this.openAPIImportListQuery.sortOrder;
      const requestQuery = {
        ...this.openAPIImportListQuery,
        ...query,
        query: query.query ?? this.openAPIImportListQuery.query,
        page: query.page ?? this.openAPIImportListQuery.page,
        pageSize: query.pageSize ?? this.openAPIImportListQuery.pageSize,
        sortBy: nextSortBy,
        sortOrder: nextSortBy ? nextSortOrder : undefined,
      };
      const catalog = await this.fetchOpenAPIImportCatalog();
      const filtered = filterOpenAPIImports(catalog, requestQuery.query, requestQuery.status);
      const sorted = sortOpenAPIImports(filtered, requestQuery.sortBy, requestQuery.sortOrder);
      const page = Math.max(1, requestQuery.page);
      const pageSize = Math.max(1, requestQuery.pageSize);
      const items = sorted.slice((page - 1) * pageSize, page * pageSize);
      this.openAPIImportPageItems = items;
      this.openAPIImportPagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
      this.openAPIImportListQuery = {
        query: requestQuery.query,
        status: requestQuery.status,
        page,
        pageSize,
        sortBy: requestQuery.sortBy,
        sortOrder: requestQuery.sortOrder,
      };
      if (!requestQuery.query && !requestQuery.status) this.openAPIImportRegistryTotal = catalog.length;
      return items;
    },
    async loadOpenAPIImportCatalog(options: { commit?: boolean } = {}) {
      const imports = await this.fetchOpenAPIImportCatalog();
      if (options.commit !== false) this.openAPIImportCatalog = imports;
      this.openAPIImportRegistryTotal = imports.length;
      return imports;
    },
    async fetchOpenAPIImportCatalog() {
      const workspaceIDs = await accessibleWorkspaceIDs();
      const responses = await Promise.all(
        workspaceIDs.map(async (workspaceID) => {
          const response = await apiClient.get<{ items: OpenAPIImportDTO[] }>(`/workspaces/${workspaceID}/openapi-imports`);
          return response.data.items.map((record) => importFromDTO(record, workspaceID));
        }),
      );
      return responses.flat();
    },
    async loadProtocols(options: { commit?: boolean } = {}) {
      const protocols = [{ protocol: "HTTP", adapterName: "HTTP OpenAPI Provider" }] as ToolProtocol[];
      if (options.commit !== false) this.protocols = protocols;
      return protocols;
    },
    async loadProviders() {
      const response = await apiClient.get<{ items: CapabilityProvider[] }>(`/workspaces/${this.workspaceID()}/providers`);
      this.providers = response.data.items.map(normalizeProvider);
      return this.providers;
    },
    async createProvider(provider: CapabilityProvider) {
      const response = await apiClient.post<CapabilityProvider>(`/workspaces/${this.workspaceID()}/providers`, providerWritePayload(provider));
      const created = normalizeProvider(response.data);
      this.providers = [created, ...this.providers];
      return created;
    },
    async updateProvider(provider: CapabilityProvider) {
      const response = await apiClient.patch<CapabilityProvider>(`/workspaces/${this.workspaceID()}/providers/${provider.id}`, {
        name: provider.name,
        driverKey: provider.driverKey,
        transport: provider.transport,
        endpointConfig: provider.endpointConfig,
        driverConfig: provider.driverConfig,
        discoveryMode: provider.discoveryMode,
        lockVersion: provider.lockVersion,
      });
      const updated = normalizeProvider(response.data);
      this.providers = this.providers.map((item) => (item.id === updated.id ? updated : item));
      return updated;
    },
    async deleteProvider(providerId: string) {
      const provider = this.providers.find((item) => item.id === providerId);
      if (!provider) throw new Error(`Provider ${providerId} is not loaded.`);
      await apiClient.delete(`/workspaces/${this.workspaceID()}/providers/${providerId}?lockVersion=${provider.lockVersion}`);
      this.providers = this.providers.filter((item) => item.id !== providerId);
      delete this.providerAssetsByProvider[providerId];
    },
    async syncProvider(providerId: string) {
      const response = await apiClient.post<ProviderSyncResult>(`/workspaces/${this.workspaceID()}/providers/${providerId}:sync`);
      await this.loadProviders();
      return response.data;
    },
    async loadProviderAssets(providerId: string) {
      const response = await apiClient.get<{ items: ProviderAsset[] }>(
        `/workspaces/${this.workspaceID()}/providers/${providerId}/assets`,
      );
      this.providerAssetsByProvider[providerId] = response.data.items;
      return response.data.items;
    },
    async materializeProviderAsset(providerId: string, assetId: string, defaultConnectionId?: string) {
      const response = await apiClient.post<ProviderMaterializationResult>(
        `/workspaces/${this.workspaceID()}/providers/${providerId}/assets/${assetId}:materialize`,
        defaultConnectionId ? { defaultConnectionId } : {},
      );
      await this.loadProviderAssets(providerId);
      return response.data;
    },
    async rotateSecret(secretId: string, plaintext: string, lockVersion: number) {
      const response = await apiClient.post<SecretReadDTO>(`/workspaces/${this.workspaceID()}/secrets/${secretId}:rotate`, {
        plaintext,
        lockVersion,
      });
      return response.data;
    },
    async createCredentialSecret(connectionName: string, plaintext: string, kind = "OAUTH2_CLIENT_SECRET") {
      const response = await apiClient.post<SecretReadDTO>(`/workspaces/${this.workspaceID()}/secrets`, {
        name: `connection-credential-${connectionName.trim()}-${Date.now()}`,
        kind,
        plaintext,
      });
      return response.data;
    },
    async createServiceConnection(
      connection: ServiceConnection,
      credentialPlaintext = "",
      options: { machineCredentialPlaintext?: string } = {},
    ) {
      const workspaceID = this.workspaceID();
      const provider = this.requireProvider(connection.providerId || this.providers[0]?.id);
      const response = await apiClient.post<ConnectionDTO>(
        `/workspaces/${workspaceID}/providers/${provider.id}/connections`,
        connectionWritePayload(connection, false, credentialPlaintext, {
          machineCredentialPlaintext: options.machineCredentialPlaintext,
        }),
      );
      const created = connectionFromDTO(response.data, provider, workspaceID);
      this.serviceConnectionCatalog = [created, ...this.serviceConnectionCatalog];
      this.serviceConnectionPageItems = [created, ...this.serviceConnectionPageItems];
      if (this.toolConnectionsByWorkspace[workspaceID]) {
        this.toolConnectionsByWorkspace[workspaceID] = [created, ...this.toolConnectionsByWorkspace[workspaceID]];
      }
      this.serviceConnectionPagination = { ...this.serviceConnectionPagination, total: this.serviceConnectionPagination.total + 1 };
      return created;
    },
    async updateServiceConnection(
      connectionId: string,
      connection: ServiceConnection,
      credentialPlaintext = "",
      options: { impactConfirmationProof?: string; metadataOnly?: boolean; machineCredentialPlaintext?: string } = {},
    ) {
      const workspaceID = this.workspaceID();
      const provider = this.requireProvider(connection.providerId);
      const response = await apiClient.patch<ConnectionDTO>(
        `/workspaces/${workspaceID}/connections/${connectionId}`,
        connectionWritePayload(connection, true, credentialPlaintext, options),
      );
      const updated = connectionFromDTO(response.data, provider, workspaceID);
      this.serviceConnectionCatalog = this.serviceConnectionCatalog.map((item) => (item.id === connectionId ? updated : item));
      this.serviceConnectionPageItems = this.serviceConnectionPageItems.map((item) => (item.id === connectionId ? updated : item));
      this.toolConnectionsByWorkspace[workspaceID] = (this.toolConnectionsByWorkspace[workspaceID] || [])
        .map((item) => (item.id === connectionId ? updated : item));
      return updated;
    },
    async previewConnectionImpact(
      connectionId: string,
      body: {
        changeKind: string;
        nonSecretChangeDescriptor?: Record<string, unknown>;
        machineCredentialWillChange?: boolean;
        expectedLockVersion: number;
      },
    ) {
      const workspaceID = this.workspaceID();
      const response = await apiClient.post<{
        impactConfirmationProof: string;
        machineCredentialWillChange?: boolean;
        expiresAt?: string;
      }>(`/workspaces/${workspaceID}/connections/${connectionId}:impact`, body);
      return response.data;
    },
    async testToolWithOutbound(
      toolId: string,
      input: Record<string, unknown>,
      outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope,
    ) {
      const workspaceID = this.workspaceID();
      const response = await apiClient.post(
        `/workspaces/${workspaceID}/tools/${toolId}:test`,
        outboundCredentials ? { input, outboundCredentials } : { input },
      );
      return response.data;
    },
    async trialWorkflowWithOutbound(
      workflowId: string,
      compilationId: string,
      input: Record<string, unknown>,
      outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope,
    ) {
      const workspaceID = this.workspaceID();
      const response = await apiClient.post(
        `/workspaces/${workspaceID}/workflows/${workflowId}/compilations/${compilationId}/__command/trial`,
        outboundCredentials ? { input, outboundCredentials } : { input },
      );
      return response.data;
    },
    async attachChatOutboundCredentials(
      sessionId: string,
      outboundCredentials: import("../types/domain").OutboundCredentialsEnvelope,
    ) {
      const workspaceID = this.workspaceID();
      const response = await apiClient.post<import("../types/domain").OutboundCredentialAttachmentResult>(
        `/workspaces/${workspaceID}/chat/sessions/${sessionId}/outbound-credentials`,
        { outboundCredentials },
      );
      return response.data;
    },
    async deleteServiceConnection(connectionId: string) {
      const workspaceID = this.workspaceID();
      const connection = this.serviceConnectionCatalog.find((item) => item.id === connectionId) || this.serviceConnectionPageItems.find((item) => item.id === connectionId);
      if (!connection) throw new Error(`Connection ${connectionId} is not loaded.`);
      await apiClient.delete(`/workspaces/${workspaceID}/connections/${connectionId}?lockVersion=${connection.lockVersion}`);
      this.serviceConnectionCatalog = this.serviceConnectionCatalog.filter((connection) => connection.id !== connectionId);
      this.serviceConnectionPageItems = this.serviceConnectionPageItems.filter((connection) => connection.id !== connectionId);
      this.toolConnectionsByWorkspace[workspaceID] = (this.toolConnectionsByWorkspace[workspaceID] || [])
        .filter((connection) => connection.id !== connectionId);
      this.serviceConnectionPagination = { ...this.serviceConnectionPagination, total: Math.max(0, this.serviceConnectionPagination.total - 1) };
      delete this.verificationByConnectionId[connectionId];
    },
    async verifyConnection(connectionId: string) {
      const workspaceID = this.workspaceID();
      const response = await apiClient.post<ConnectionVerificationDTO>(`/workspaces/${workspaceID}/connections/${connectionId}:verify`);
      const verification = verificationFromDTO(response.data);
      this.verificationByConnectionId[connectionId] = verification;
      const nextStatus: ServiceConnection["status"] = verification.status === "SUCCEEDED" ? "VERIFIED" : "ERROR";
      this.serviceConnectionCatalog = this.serviceConnectionCatalog.map((connection) =>
        connection.id === connectionId ? { ...connection, status: nextStatus } : connection,
      );
      this.serviceConnectionPageItems = this.serviceConnectionPageItems.map((connection) =>
        connection.id === connectionId ? { ...connection, status: nextStatus } : connection,
      );
      this.toolConnectionsByWorkspace[workspaceID] = (this.toolConnectionsByWorkspace[workspaceID] || []).map((connection) =>
        connection.id === connectionId ? { ...connection, status: nextStatus } : connection,
      );
      return verification;
    },
    requireProvider(providerId: string) {
      const provider = this.providers.find((item) => item.id === providerId);
      if (!provider) throw new Error("Select an HTTP OpenAPI Provider before saving a Connection.");
      return provider;
    },
    async loadOpenAPIImports() {
      return this.loadOpenAPIImportCatalog();
    },
    async loadTools(options: { commit?: boolean } = {}) {
      const tools = await this.fetchToolCatalog();
      if (options.commit !== false) {
        this.tools = tools;
      }
      return tools;
    },
    async fetchToolCatalog() {
      const workspaceIDs = await accessibleWorkspaceIDs();
      const responses = await Promise.all(
        workspaceIDs.map(async (workspaceID) => {
          const response = await apiClient.get<{ items: ToolDTO[] }>(`/workspaces/${workspaceID}/tools`);
          return Promise.all(
            response.data.items.map(async (tool) => {
              const versions = await apiClient.get<{ items: ToolVersionDTO[] }>(
                `/workspaces/${workspaceID}/tools/${tool.id}/versions`,
              );
              return toolFromDTO(tool, workspaceID, versions.data.items);
            }),
          );
        }),
      );
      return responses.flat();
    },
    async loadToolPage(query: ToolListQuery = {}) {
      this.toolPageLoading = true;
      this.toolPageError = null;
      const nextSortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.toolListQuery.sortBy;
      const nextSortOrder = query.sortOrder !== undefined ? query.sortOrder || undefined : this.toolListQuery.sortOrder;
      const requestQuery = {
        ...this.toolListQuery,
        ...query,
        query: query.query ?? this.toolListQuery.query,
        page: query.page ?? this.toolListQuery.page,
        pageSize: query.pageSize ?? this.toolListQuery.pageSize,
        sortBy: nextSortBy,
        sortOrder: nextSortBy ? nextSortOrder : undefined,
      };
      try {
        const catalog = await this.fetchToolCatalog();
        await this.loadToolConnections(catalog);
        const filtered = filterTools(catalog, requestQuery.query, requestQuery.status, requestQuery.type, (tool) => this.connectionForTool(tool));
        const sorted = sortTools(filtered, requestQuery.sortBy, requestQuery.sortOrder);
        const page = Math.max(1, requestQuery.page);
        const pageSize = Math.max(1, requestQuery.pageSize);
        this.tools = catalog;
        this.toolPageItems = sorted.slice((page - 1) * pageSize, page * pageSize);
        this.toolPagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
        this.toolListQuery = {
          query: requestQuery.query,
          status: requestQuery.status,
          type: requestQuery.type,
          page,
          pageSize,
          sortBy: requestQuery.sortBy,
          sortOrder: requestQuery.sortOrder,
        };
        this.toolPageHasLoaded = true;
        return this.toolPageItems;
      } catch (error) {
        this.toolPageError = error instanceof Error ? error.message : String(error);
        return this.toolPageItems;
      } finally {
        this.toolPageLoading = false;
      }
    },
    async loadToolConnections(tools?: Tool[]) {
      const activeWorkspaceID = this.workspaceID();
      const providerIDsByWorkspace = new Map<string, Set<string>>();
      for (const tool of tools || this.tools) {
        const providerIDs = providerIDsByWorkspace.get(tool.workspaceId) || new Set<string>();
        if (tool.providerId) providerIDs.add(tool.providerId);
        providerIDsByWorkspace.set(tool.workspaceId, providerIDs);
      }
      if (!providerIDsByWorkspace.has(activeWorkspaceID)) {
        providerIDsByWorkspace.set(activeWorkspaceID, new Set());
      }

      await Promise.all(
        [...providerIDsByWorkspace.entries()].map(([workspaceID, providerIDs]) =>
          this.loadToolWorkspaceContext(workspaceID, [...providerIDs]),
        ),
      );
      return this.toolConnectionsByWorkspace;
    },
    async loadToolWorkspaceContext(workspaceID: string, providerIDs: string[] = [], force = false) {
      if (!force && Object.prototype.hasOwnProperty.call(this.toolConnectionsByWorkspace, workspaceID)) {
        return this.toolConnectionsByWorkspace[workspaceID];
      }

      const providerResponse = await apiClient.get<{ items: CapabilityProvider[] }>(`/workspaces/${workspaceID}/providers`);
      const providers = providerResponse.data.items.map(normalizeProvider);
      const requestedProviderIDs = new Set(providerIDs);
      const targetProviders = requestedProviderIDs.size
        ? providers.filter((provider) => requestedProviderIDs.has(provider.id))
        : providers;
      const connectionResponses = await Promise.all(
        targetProviders.map(async (provider) => {
          const response = await apiClient.get<{ items: ConnectionDTO[] }>(
            `/workspaces/${workspaceID}/providers/${provider.id}/connections`,
          );
          return response.data.items.map((connection) => connectionFromDTO(connection, provider, workspaceID));
        }),
      );
      const connections = connectionResponses.flat();
      this.toolConnectionsByWorkspace = {
        ...this.toolConnectionsByWorkspace,
        [workspaceID]: connections,
      };
      if (workspaceID === this.workspaceID()) {
        this.providers = providers;
        this.serviceConnectionCatalog = connections;
        this.serviceConnectionRegistryTotal = connections.length;
      }
      return connections;
    },
    connectionForTool(tool: Tool) {
      const scopedConnections = this.toolConnectionsByWorkspace[tool.workspaceId];
      return (scopedConnections || this.serviceConnectionCatalog).find((connection) => connection.id === tool.connectionId);
    },
    async createOpenAPIImport(request: OpenAPIImportRequest) {
      const response = await apiClient.post<{ import: OpenAPIImportDTO; duplicateOfId?: string }>(
        `/workspaces/${request.workspaceId}/openapi-imports`,
        {
          providerId: request.providerId,
          ...(request.connectionId ? { connectionId: request.connectionId } : {}),
        },
      );
      const normalized = importFromDTO(response.data.import, request.workspaceId);
      this.openAPIImportCatalog = [normalized, ...this.openAPIImportCatalog];
      this.openAPIImportPageItems = [normalized, ...this.openAPIImportPageItems];
      this.openAPIImportPagination = { ...this.openAPIImportPagination, total: this.openAPIImportPagination.total + 1 };
      return normalized;
    },
    async createOpenAPIFileImport(request: OpenAPIImportRequest, file: File) {
      const form = new FormData();
      form.append("providerId", request.providerId);
      if (request.connectionId) form.append("connectionId", request.connectionId);
      form.append("file", file, file.name);
      const response = await apiClient.post<{ import: OpenAPIImportDTO; duplicateOfId?: string }>(
        `/workspaces/${request.workspaceId}/openapi-imports/__command/upload`,
        form,
      );
      const normalized = importFromDTO(response.data.import, request.workspaceId);
      this.openAPIImportCatalog = [normalized, ...this.openAPIImportCatalog];
      this.openAPIImportPageItems = [normalized, ...this.openAPIImportPageItems];
      this.openAPIImportPagination = { ...this.openAPIImportPagination, total: this.openAPIImportPagination.total + 1 };
      return normalized;
    },
    async loadOpenAPIImportDetail(recordOrID: OpenAPIImport | string) {
      const record = typeof recordOrID === "string"
        ? [...this.openAPIImportCatalog, ...this.openAPIImportPageItems].find((item) => item.id === recordOrID)
        : recordOrID;
      if (!record) throw new Error(`OpenAPI import ${recordOrID} is not loaded.`);
      const response = await apiClient.get<{ import: OpenAPIImportDTO; endpoints: OpenAPIEndpointDTO[] }>(
        `/workspaces/${record.workspaceId}/openapi-imports/${record.id}`,
      );
      const normalized = importFromDTO(response.data.import, record.workspaceId, response.data.endpoints);
      this.openAPIImportCatalog = replaceByID(this.openAPIImportCatalog, normalized);
      this.openAPIImportPageItems = replaceByID(this.openAPIImportPageItems, normalized);
      return normalized;
    },
    async deleteOpenAPIImport(importId: string) {
      const record = [...this.openAPIImportCatalog, ...this.openAPIImportPageItems].find((item) => item.id === importId);
      if (!record) throw new Error(`OpenAPI import ${importId} is not loaded.`);
      await apiClient.delete(`/workspaces/${record.workspaceId}/openapi-imports/${importId}`);
      this.openAPIImportCatalog = this.openAPIImportCatalog.filter((record) => record.id !== importId);
      this.openAPIImportPageItems = this.openAPIImportPageItems.filter((record) => record.id !== importId);
      this.openAPIImportPagination = { ...this.openAPIImportPagination, total: Math.max(0, this.openAPIImportPagination.total - 1) };
    },
    async generateToolDrafts(importId: string) {
      const loaded = [...this.openAPIImportCatalog, ...this.openAPIImportPageItems].find((item) => item.id === importId);
      if (!loaded) throw new Error(`OpenAPI import ${importId} is not loaded.`);
      const record = loaded.detail ? loaded : await this.loadOpenAPIImportDetail(loaded);
      const endpointIds = (record.detail?.endpoints || [])
        .filter((endpoint) => endpoint.ready !== false && !endpoint.generatedCapabilityId && !isAuthenticationInfrastructureEndpoint(endpoint.path))
        .map((endpoint) => endpoint.id)
        .filter((id): id is string => Boolean(id));
      if (!endpointIds.length) return [];
      const response = await apiClient.post<{ items: GeneratedToolDTO[] }>(
        `/workspaces/${record.workspaceId}/openapi-imports/${importId}:generate-tools`,
        { endpointIds },
      );
      const normalizedTools = response.data.items.map((item) => toolFromDTO(item.tool, record.workspaceId, [item.draft]));
      normalizedTools.forEach((tool) => this.upsertTool(tool));
      await this.loadOpenAPIImportDetail(record);
      return normalizedTools;
    },
    async createTool(tool: Tool) {
      const providerId = tool.providerId || this.serviceConnectionCatalog.find((item) => item.id === tool.connectionId)?.providerId || this.providers[0]?.id;
      if (!providerId) throw new Error("Select a Provider before creating a Tool.");
      const response = await apiClient.post<{ tool: ToolDTO; draft: ToolVersionDTO }>(`/workspaces/${tool.workspaceId}/tools`, {
        providerId,
        ...(tool.sourceAssetId ? { sourceAssetId: tool.sourceAssetId } : {}),
        ...(tool.connectionId ? { defaultConnectionId: tool.connectionId } : {}),
        ...(tool.sourceEndpointId ? { sourceEndpointId: tool.sourceEndpointId } : {}),
        name: tool.name,
        slug: tool.slug || slugify(tool.name),
        description: tool.description,
        draft: toolDraftPayload(tool),
      });
      const normalized = toolFromDTO(response.data.tool, tool.workspaceId, [response.data.draft]);
      this.upsertTool(normalized);
      return normalized;
    },
    async updateTool(toolId: string, tool: Tool) {
      const current = this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId) || tool;
      const response = await apiClient.patch<ToolDTO>(`/workspaces/${tool.workspaceId}/tools/${toolId}`, {
        name: tool.name,
        slug: tool.slug || slugify(tool.name),
        description: tool.description,
        status: tool.status === "Disabled" ? "DISABLED" : "ACTIVE",
        ...(tool.sourceAssetId ? { sourceAssetId: tool.sourceAssetId } : {}),
        ...(tool.connectionId ? { defaultConnectionId: tool.connectionId } : {}),
        lockVersion: tool.lockVersion,
      });
      let versions = current.versions;
      if (toolSpecSignature(current) !== toolSpecSignature(tool)) {
        let draft = [...versions].reverse().find((version) => version.lifecycleStatus !== "PUBLISHED");
        if (!draft) {
          const published = [...versions].reverse().find((version) => version.lifecycleStatus === "PUBLISHED");
          if (!published) throw new Error("Tool has no version to edit.");
          const created = await apiClient.post<ToolVersionDTO>(`/workspaces/${tool.workspaceId}/tools/${toolId}/versions`, {
            sourceVersionId: published.id,
          });
          draft = normalizeToolVersion(created.data);
          versions = [...versions, draft];
        }
        const updatedVersion = await apiClient.patch<ToolVersionDTO>(
          `/workspaces/${tool.workspaceId}/tools/${toolId}/versions/${draft.id}`,
          {
            draft: toolDraftPayload(tool),
            lifecycleStatus: tool.status === "Review" ? "REVIEW" : "DRAFT",
            lockVersion: draft.lockVersion,
          },
        );
        versions = versions.map((version) => (version.id === draft.id ? normalizeToolVersion(updatedVersion.data) : version));
      }
      const normalized = toolFromDTO(response.data, tool.workspaceId, versions, current.lastTestResult);
      this.upsertTool(normalized);
      return normalized;
    },
    async deleteTool(toolId: string) {
      const tool = this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId);
      if (!tool) throw new Error(`Tool ${toolId} is not loaded.`);
      await apiClient.delete(`/workspaces/${tool.workspaceId}/tools/${toolId}?lockVersion=${tool.lockVersion}`);
      this.tools = this.tools.filter((tool) => tool.id !== toolId);
      this.toolPageItems = this.toolPageItems.filter((tool) => tool.id !== toolId);
      this.toolPagination = { ...this.toolPagination, total: Math.max(0, this.toolPagination.total - 1) };
    },
    async testTool(toolId: string, inputParams: Record<string, unknown>) {
      const tool = this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId);
      const version = tool?.draftVersion;
      if (!tool || !version || version.lifecycleStatus === "PUBLISHED") throw new Error("Select an editable Tool Version before testing.");
      const response = await apiClient.post<ToolTestResult>(
        `/workspaces/${tool.workspaceId}/tools/${toolId}/versions/${version.id}:test`,
        { connectionId: tool.connectionId, input: inputParams },
      );
      const testResult = normalizeToolTestResult(response.data)!;
      const versionsResponse = await apiClient.get<{ items: ToolVersionDTO[] }>(`/workspaces/${tool.workspaceId}/tools/${toolId}/versions`);
      const metadata = await apiClient.get<ToolDTO>(`/workspaces/${tool.workspaceId}/tools/${toolId}`);
      const normalized = toolFromDTO(metadata.data, tool.workspaceId, versionsResponse.data.items, testResult);
      this.upsertTool(normalized);
      const responseStatus = numberValue(
        testResult.responseSummary.status,
        testResult.responseSummary.statusCode,
        testResult.responseSummary.httpStatus,
      );
      return {
        tool: normalized,
        testResult,
        requestInput: inputParams,
        responseStatus,
        responseBody: testResult.responseSummary.body ?? testResult.responseSummary,
        latencyMs: testResult.latencyMs || 0,
        passed: testResult.status === "Tested",
        errorMessage: testResult.errorCode || "",
      } satisfies ToolTestExecutionResult;
    },
    async publishTool(toolId: string) {
      const tool = this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId);
      const version = tool?.draftVersion;
      if (!tool || !version || version.lifecycleStatus !== "TESTED") throw new Error("Test the exact Draft Version before publishing it.");
      const response = await apiClient.post<PublishToolDTO>(
        `/workspaces/${tool.workspaceId}/tools/${toolId}/versions/${version.id}:publish`,
        {
          callableName: slugify(tool.slug || tool.name).replace(/-/g, "_"),
          callableDescription: tool.description,
          lockVersion: version.lockVersion,
        },
      );
      const metadata = await apiClient.get<ToolDTO>(`/workspaces/${tool.workspaceId}/tools/${toolId}`);
      const versions = tool.versions.map((item) => (item.id === version.id ? normalizeToolVersion(response.data.version) : item));
      const normalized = toolFromDTO(metadata.data, tool.workspaceId, versions, tool.lastTestResult);
      this.upsertTool(normalized);
      return normalized;
    },
    upsertTool(tool: Tool) {
      const index = this.tools.findIndex((item) => item.id === tool.id);
      if (index === -1) {
        this.tools = [...this.tools, tool];
      } else {
        this.tools = this.tools.map((item) => (item.id === tool.id ? tool : item));
      }
      if (this.toolPageItems.some((item) => item.id === tool.id)) {
        this.toolPageItems = this.toolPageItems.map((item) => (item.id === tool.id ? tool : item));
      }
    },
  },
});

async function accessibleWorkspaceIDs() {
  const store = useWorkspaceStore();
  if (!store.items.length) await store.load();
  // Scope catalog reads to the shared top-bar active workspace so switching it refreshes page data.
  if (store.activeWorkspaceId) {
    const activeExists = !store.items.length || store.items.some((workspace) => workspace.id === store.activeWorkspaceId);
    if (activeExists) return [store.activeWorkspaceId];
  }
  const ids = store.items.map((workspace) => workspace.id);
  if (ids.length) return ids;
  throw new Error("当前没有可用的业务空间。请先创建业务空间，或联系管理员加入已有空间。");
}

function toolDraftPayload(tool: Tool) {
  const draft = tool.draftVersion;
  return {
    ...(tool.sourceAssetId || draft?.providerAssetId ? { providerAssetId: tool.sourceAssetId || draft?.providerAssetId } : {}),
    ...(tool.connectionId ? { defaultConnectionId: tool.connectionId } : {}),
    actionSchemaVersion: tool.actionConfigSchemaVersion || draft?.actionSchemaVersion || "http.v1",
    actionConfig: tool.actionConfig || {},
    inputSchema: nodesToJSONSchema(tool.requestParams.map(requestParamToSchema)),
    outputSchema: nodesToJSONSchema(tool.responseFields.map(responseFieldToSchema)),
    errorMappings: Object.fromEntries((tool.errorMappings || []).map((mapping) => [
      mapping.protocolStatus.toUpperCase(),
      {
        errorCode: mapping.errorCode,
        ...(mapping.agentAdvice ? { agentAdvice: mapping.agentAdvice } : {}),
      },
    ])),
    runtimePolicy: tool.runtimePolicy || {},
    riskLevel: draft?.riskLevel || "LOW",
    sideEffectLevel: draft?.sideEffectLevel || inferSideEffect(tool.actionConfig),
    requiresConfirmation: draft?.requiresConfirmation || false,
  };
}

function nodesToJSONSchema(nodes: ToolSchemaNode[]): Record<string, unknown> {
  const properties = Object.fromEntries(nodes.filter((node) => node.name).map((node) => [node.name, nodeToJSONSchema(node)]));
  const required = nodes.filter((node) => node.name && node.required).map((node) => node.name);
  return { type: "object", properties, ...(required.length ? { required } : {}) };
}

function nodeToJSONSchema(node: ToolSchemaNode): Record<string, unknown> {
  const value: Record<string, unknown> = {
    type: node.type,
    ...(node.description ? { description: node.description } : {}),
    ...(node.location ? {
      "x-actweave-location": node.location.toLocaleLowerCase(),
      "x-actweave-parameter-name": node.name,
    } : {}),
    ...(node.format ? { format: node.format } : {}),
    ...(node.nullable !== undefined ? { nullable: node.nullable } : {}),
    ...(node.example !== undefined ? { example: node.example } : {}),
    ...(node.enumValues?.length ? { enum: node.enumValues } : {}),
    ...(node.defaultValue !== undefined ? { default: node.defaultValue } : {}),
    ...(node.valueSource ? { "x-actweave-value-source": node.valueSource } : {}),
  };
  if (node.type === "object") {
    const children = node.children || [];
    value.properties = Object.fromEntries(children.filter((child) => child.name).map((child) => [child.name, nodeToJSONSchema(child)]));
    const required = children.filter((child) => child.name && child.required).map((child) => child.name);
    if (required.length) value.required = required;
    if (node.additionalProperties) value.additionalProperties = nodeToJSONSchema(node.additionalProperties);
  }
  if (node.type === "array" && node.item) value.items = nodeToJSONSchema(node.item);
  return value;
}

function inferSideEffect(actionConfig: Record<string, unknown>) {
  const method = String(actionConfig.method || "GET").toUpperCase();
  return method === "GET" || method === "HEAD" ? "READ" : "WRITE";
}

function toolSpecSignature(tool: Tool) {
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

function slugify(value: string) {
  const slug = value.trim().toLocaleLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  return slug || `tool-${Date.now()}`;
}

function numberValue(...values: unknown[]) {
  const value = values.find((item) => typeof item === "number" || (typeof item === "string" && item.trim() !== ""));
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function replaceByID<T extends { id: string }>(items: T[], replacement: T) {
  return items.some((item) => item.id === replacement.id)
    ? items.map((item) => (item.id === replacement.id ? replacement : item))
    : [replacement, ...items];
}

interface ToolDTO {
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

interface ToolVersionDTO {
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

interface PublishToolDTO {
  releaseId: string;
  releaseNo: number;
  version: ToolVersionDTO;
  testId: string;
}

interface OpenAPIImportDTO {
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

interface OpenAPIEndpointDTO {
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

interface GeneratedToolDTO {
  endpointId: string;
  tool: ToolDTO;
  draft: ToolVersionDTO;
}

interface ConnectionDTO {
  id: string;
  providerId: string;
  name: string;
  alias: string;
  environment: string;
  externalAccountRef?: string;
  /** Dual-mode fields from hard-cutover DTO (legacy authMode not returned). */
  outboundIdentity?: Record<string, unknown> | null;
  outboundIdentityPolicyVersion?: number;
  migrationState?: string;
  machineCredentialConfigured?: boolean;
  /** Legacy fields may be absent after hard cutover. */
  authMode?: string;
  authConfig?: Record<string, unknown>;
  credentialConfigured?: boolean;
  credentialFingerprint?: string;
  grantedScopes: unknown[];
  policy: Record<string, unknown>;
  status: ServiceConnection["status"];
  lastVerifiedAt?: string;
  lastErrorCode?: string;
  createdBy: string;
  updatedBy: string;
  lockVersion: number;
}

interface ConnectionVerificationDTO {
  ID: string;
  WorkspaceID: string;
  ConnectionID: string;
  Status: string;
  Diagnostics: Record<string, string>;
  LatencyMS?: number;
  TestedBy: string;
  TestedAt: string;
  RawObjectID?: string;
}

interface ProviderSyncResult {
  id: string;
  status: string;
  discoveredCount: number;
  changedCount: number;
  errorSummary: Record<string, unknown>;
}

interface ProviderMaterializationResult {
  asset: ProviderAsset;
  capabilityId: string;
  draftVersionId: string;
  lifecycleStatus: string;
}

interface SecretReadDTO {
  id: string;
  workspaceId: string;
  name: string;
  kind: string;
  configured: boolean;
  fingerprint?: string;
  activeVersionNo?: number;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

function normalizeProvider(provider: CapabilityProvider): CapabilityProvider {
  return {
    ...provider,
    kind: "HTTP_OPENAPI",
    transport: "HTTP",
    endpointConfig: asRecord(provider.endpointConfig),
    driverConfig: asRecord(provider.driverConfig),
  };
}

function providerWritePayload(provider: CapabilityProvider) {
  return {
    name: provider.name,
    kind: "HTTP_OPENAPI",
    driverKey: provider.driverKey,
    transport: "HTTP",
    endpointConfig: provider.endpointConfig || {},
    driverConfig: provider.driverConfig || {},
    discoveryMode: provider.discoveryMode,
  };
}

function emptyAuthConfig(): ServiceConnection["authConfig"] {
  return {
    mode: "",
    label: "",
    schemeKey: "",
    values: {},
    tokenUrl: "",
    clientId: "",
    clientAuth: "",
    scope: "",
    refreshUrl: "",
    refreshMode: "",
    accessTokenPath: "",
    refreshTokenPath: "",
    expiresPath: "",
    injectionTemplate: "",
    retryOn401Policy: "",
    refreshFailurePolicy: "",
    credentialPlacement: "",
    apiKeyName: "",
    apiSecretName: "",
    tokenHeaderName: "",
    tokenPrefix: "",
  };
}

function parseOutboundMode(identity: Record<string, unknown> | null | undefined): ServiceConnection["outboundMode"] {
  const mode = firstString(identity?.mode).toUpperCase();
  if (mode === "BROKER_OBO" || mode === "REQUEST_PASSTHROUGH") return mode;
  return undefined;
}

function connectionFromDTO(value: ConnectionDTO, provider: CapabilityProvider, workspaceId?: string): ServiceConnection {
  const auth = asRecord(value.authConfig);
  const contractValues = stringRecord(auth.values);
  const endpoint = asRecord(provider.endpointConfig);
  const sourceAddress = firstString(endpoint.serviceBaseUrl, endpoint.baseUrl, endpoint.url, endpoint.endpoint);
  const parts = splitEndpoint(sourceAddress);
  const outboundIdentity = value.outboundIdentity && typeof value.outboundIdentity === "object"
    ? (value.outboundIdentity as Record<string, unknown>)
    : undefined;
  const outboundMode = parseOutboundMode(outboundIdentity);
  const uiMode = uiAuthMode(value.authMode || "");
  return {
    id: value.id,
    workspaceId,
    providerId: value.providerId,
    name: value.name,
    alias: value.alias,
    environment: value.environment,
    externalAccountRef: value.externalAccountRef,
    protocol: provider.transport,
    protocolConfig: {
      domain: sourceAddress,
      host: parts.host,
      port: parts.port,
      basePath: parts.path,
      verificationMethod: firstString(auth.verificationMethod) || "GET",
      verificationPath: firstString(auth.verificationPath),
      expectedStatus: firstString(auth.expectedStatus) || "200-299",
      expectedResponseContains: firstString(auth.expectedResponseContains),
      commonHeaders: stringRecord(auth.commonHeaders),
    },
    protocolSchema: "provider.http-openapi.v1",
    authMode: value.authMode || "",
    authConfig: {
      ...emptyAuthConfig(),
      mode: uiMode,
      label: firstString(auth.label),
      schemeKey: firstString(auth.schemeKey),
      values: contractValues,
      tokenUrl: firstString(auth.tokenUrl),
      clientId: firstString(contractValues.clientId, auth.clientId),
      clientAuth: firstString(auth.clientAuth) || "client_secret_basic",
      scope: firstString(contractValues.scope, auth.scope),
      refreshUrl: firstString(auth.refreshUrl),
      refreshMode: firstString(auth.refreshMode) || "none",
      accessTokenPath: firstString(auth.accessTokenPath),
      refreshTokenPath: firstString(auth.refreshTokenPath),
      expiresPath: firstString(auth.expiresPath),
      injectionTemplate: firstString(auth.injectionTemplate),
      retryOn401Policy: firstString(auth.retryOn401Policy),
      refreshFailurePolicy: firstString(auth.refreshFailurePolicy),
      credentialPlacement: firstString(auth.credentialPlacement, auth.placement) || "header",
      apiKeyName: firstString(auth.apiKeyName, auth.headerName),
      apiSecretName: firstString(auth.apiSecretName),
      tokenHeaderName: firstString(auth.tokenHeaderName, auth.headerName),
      tokenPrefix: firstString(auth.tokenPrefix),
    },
    outboundMode,
    outboundIdentity,
    outboundIdentityPolicyVersion: value.outboundIdentityPolicyVersion,
    migrationState: value.migrationState === "MIGRATION_REQUIRED" ? "MIGRATION_REQUIRED" : value.migrationState === "NONE" ? "NONE" : undefined,
    machineCredentialConfigured: Boolean(value.machineCredentialConfigured),
    credentialSecretId: undefined,
    credentialConfigured: Boolean(value.credentialConfigured || value.machineCredentialConfigured),
    credentialFingerprint: value.credentialFingerprint,
    grantedScopes: Array.isArray(value.grantedScopes) ? value.grantedScopes : [],
    policy: asRecord(value.policy),
    status: value.status,
    lastVerifiedAt: value.lastVerifiedAt,
    lastErrorCode: value.lastErrorCode,
    createdBy: value.createdBy,
    updatedBy: value.updatedBy,
    lockVersion: value.lockVersion,
  };
}

function buildOutboundIdentityPayload(connection: ServiceConnection): Record<string, unknown> | null {
  const mode = connection.outboundMode;
  if (mode !== "BROKER_OBO" && mode !== "REQUEST_PASSTHROUGH") return null;
  if (mode === "BROKER_OBO") {
    const existing = (connection.outboundIdentity?.brokerObo || {}) as Record<string, unknown>;
    const clientId = firstString(existing.clientId, connection.authConfig.clientId).trim();
    const scopesRaw = existing.scopes;
    const scopes = Array.isArray(scopesRaw)
      ? scopesRaw.map(String)
      : String(connection.authConfig.scope || "")
          .split(/[\s,]+/)
          .map((s) => s.trim())
          .filter(Boolean);
    const maxTokenTtlSeconds = Number(existing.maxTokenTtlSeconds) || 300;
    return {
      schemaVersion: "outbound-connection.v1",
      mode: "BROKER_OBO",
      brokerObo: { clientId, scopes, maxTokenTtlSeconds },
    };
  }
  const existing = (connection.outboundIdentity?.requestPassthrough || {}) as Record<string, unknown>;
  const maxResidenceSeconds = Number(existing.maxResidenceSeconds) || 600;
  return {
    schemaVersion: "outbound-connection.v1",
    mode: "REQUEST_PASSTHROUGH",
    requestPassthrough: { maxResidenceSeconds },
  };
}

function connectionWritePayload(
  connection: ServiceConnection,
  includeLock: boolean,
  credentialPlaintext = "",
  options: { impactConfirmationProof?: string; metadataOnly?: boolean; machineCredentialPlaintext?: string } = {},
) {
  const dual = buildOutboundIdentityPayload(connection);
  const payload: Record<string, unknown> = {
    name: connection.name,
    alias: connection.alias.trim() || connection.name.trim().toLocaleLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, ""),
    environment: apiEnvironment(connection.environment),
    ...(connection.externalAccountRef?.trim() ? { externalAccountRef: connection.externalAccountRef.trim() } : {}),
    grantedScopes: connection.grantedScopes || [],
    policy: connection.policy || {},
  };
  if (dual) {
    payload.outboundIdentity = dual;
    if (options.machineCredentialPlaintext) {
      payload.machineCredential = { kind: "PRIVATE_KEY_PEM", plaintext: options.machineCredentialPlaintext };
    }
    if (options.impactConfirmationProof) {
      payload.impactConfirmationProof = options.impactConfirmationProof;
    }
    if (options.metadataOnly) payload.metadataOnly = true;
  } else {
    // Legacy path (non dual-mode) — still rejected by backend for HTTP targets after hard cut.
    const authConfig = sanitizeAuthConfig(connection.authConfig);
    payload.authMode = apiAuthMode(connection.authConfig.mode || connection.authMode);
    payload.authConfig = authConfig;
    if (connection.credentialSecretId?.trim()) payload.credentialSecretId = connection.credentialSecretId.trim();
    if (credentialPlaintext) payload.credential = { kind: "OAUTH2_CLIENT_SECRET", plaintext: credentialPlaintext };
  }
  if (includeLock) payload.lockVersion = connection.lockVersion;
  return payload;
}

function sanitizeAuthConfig(value: ServiceConnection["authConfig"]) {
  if (value.schemeKey && value.values) {
    return {
      schemeKey: value.schemeKey,
      values: Object.fromEntries(Object.entries(value.values).map(([key, item]) => [key, item.trim()]).filter(([, item]) => item !== "")),
    };
  }
  const blocked = new Set(["apiKeyValue", "apiSecretValue", "fixedToken", "password", "tokenValue", "secretValue"]);
  return Object.fromEntries(
    Object.entries(value).filter(([key, item]) => !blocked.has(key) && item !== "" && item !== undefined),
  );
}

function verificationFromDTO(value: ConnectionVerificationDTO): ServiceConnectionVerification {
  return {
    id: value.ID,
    workspaceId: value.WorkspaceID,
    connectionId: value.ConnectionID,
    status: value.Status,
    diagnostics: asRecord(value.Diagnostics) as Record<string, string>,
    latencyMs: value.LatencyMS,
    testedBy: value.TestedBy,
    testedAt: value.TestedAt,
    rawObjectId: value.RawObjectID,
  };
}

function filterConnections(items: ServiceConnection[], query: string, status?: ServiceConnection["status"]) {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((connection) => {
    if (status && connection.status !== status) return false;
    if (!needle) return true;
    return [
      connection.name,
      connection.alias,
      connection.environment,
      connection.authMode,
      connection.outboundMode || "",
      connection.migrationState || "",
      connection.protocolConfig.domain,
    ].some((value) => value.toLocaleLowerCase().includes(needle));
  });
}

function filterTools(
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
    ].some((value) => String(value || "").toLocaleLowerCase().includes(needle));
  });
}

function sortTools(items: Tool[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set(["name", "protocol", "status", "updatedBy", "createdAt", "updatedAt"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const comparison = String(left[sortBy as keyof Tool] || "").localeCompare(String(right[sortBy as keyof Tool] || ""), "zh-Hans");
    return order === "asc" ? comparison : -comparison;
  });
}

function filterOpenAPIImports(items: OpenAPIImport[], query: string, status?: "Ready" | "Issues") {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((record) => {
    if (status && record.status !== status) return false;
    if (!needle) return true;
    return [record.fileName, record.source, record.sourceType, record.status].some((value) => value.toLocaleLowerCase().includes(needle));
  });
}

function isAuthenticationInfrastructureEndpoint(path: string) {
  const normalized = `/${path.trim().replace(/^\/+|\/+$/g, "")}`.toLocaleLowerCase();
  return normalized === "/oauth2/token" || normalized === "/oauth2/revoke";
}

function sortOpenAPIImports(items: OpenAPIImport[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set(["fileName", "totalEndpoints", "readyEndpoints", "issueCount", "status", "createdAt", "updatedAt"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const leftValue = left[sortBy as keyof OpenAPIImport];
    const rightValue = right[sortBy as keyof OpenAPIImport];
    const comparison = typeof leftValue === "number" && typeof rightValue === "number"
      ? leftValue - rightValue
      : String(leftValue || "").localeCompare(String(rightValue || ""), "zh-Hans");
    return order === "asc" ? comparison : -comparison;
  });
}

function sortConnections(items: ServiceConnection[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set(["name", "environment", "authMode", "status", "createdBy", "updatedBy"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const comparison = String(left[sortBy as keyof ServiceConnection] || "").localeCompare(
      String(right[sortBy as keyof ServiceConnection] || ""),
      "zh-Hans",
    );
    return order === "asc" ? comparison : -comparison;
  });
}

function apiEnvironment(value: string) {
  if (value === "测试" || value === "TEST") return "TEST";
  if (value === "STAGING" || value === "Staging") return "STAGING";
  if (value === "DEVELOPMENT" || value === "Development") return "DEVELOPMENT";
  return "PRODUCTION";
}

function apiAuthMode(value: string) {
  const modes: Record<string, string> = {
    none: "NONE",
    "api-key-secret": "API_KEY",
    "fixed-token": "BEARER",
    "oauth2-client": "OAUTH2_CLIENT",
    "oauth2-mtls": "OAUTH2_MTLS",
    "custom-token-api": "CUSTOM_TOKEN",
  };
  return modes[value] || value || "NONE";
}

function uiAuthMode(value: string) {
  const modes: Record<string, string> = {
    API_KEY: "api-key-secret",
    BEARER: "fixed-token",
    OAUTH2_CLIENT: "oauth2-client",
    OAUTH2_MTLS: "oauth2-mtls",
    CUSTOM_TOKEN: "custom-token-api",
    NONE: "",
  };
  return modes[value] ?? value;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function stringRecord(value: unknown): Record<string, string> {
  return Object.fromEntries(Object.entries(asRecord(value)).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
}

function firstString(...values: unknown[]) {
  return values.find((value): value is string => typeof value === "string") || "";
}

function splitEndpoint(value: string) {
  try {
    const parsed = new URL(value);
    return { host: parsed.hostname, port: parsed.port, path: parsed.pathname === "/" ? "" : parsed.pathname };
  } catch {
    return { host: "", port: "", path: "" };
  }
}
