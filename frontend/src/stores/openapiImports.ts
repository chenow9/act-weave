/**
 * OpenAPI Imports domain store (ZKL-64 item 10).
 * Owns this domain's collections only. Secrets stay in action local params.
 */
import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import { requireActiveWorkspaceId, accessibleWorkspaceIDs } from "../services/integration/workspace";
import * as mappers from "../services/integration/mappers";
import type { OpenAPIImport, OpenAPIImportListQuery, OpenAPIImportRequest, ToolProtocol } from "../types/domain";
import { useToolsStore } from "./tools";

const {
  importFromDTO,
  replaceByID,
  isAuthenticationInfrastructureEndpoint,
  filterOpenAPIImports,
  sortOpenAPIImports,
  toolFromDTO,
} = mappers;
type OpenAPIImportDTO = mappers.OpenAPIImportDTO;
type OpenAPIEndpointDTO = mappers.OpenAPIEndpointDTO;
type GeneratedToolDTO = mappers.GeneratedToolDTO;

export const useOpenAPIImportsStore = defineStore("openapiImports", {
  state: () => ({
    openAPIImportPageItems: [] as OpenAPIImport[],
    openAPIImportPagination: {
      page: 1,
      pageSize: DEFAULT_PAGE_SIZE,
      total: 0,
      pageSizeOptions: [...PAGE_SIZE_OPTIONS],
    } as ListPagination,
    openAPIImportListQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE } as any,
    openAPIImportCatalog: [] as OpenAPIImport[],
    openAPIImportRegistryTotal: 0,
    protocols: [] as ToolProtocol[],
    loading: false,
  }),
  getters: {
    openAPIImports: (state) => state.openAPIImportCatalog,
  },
  actions: {
    workspaceID() {
      return requireActiveWorkspaceId();
    },
    async loadOpenAPIImportPage(query: OpenAPIImportListQuery = {}) {
      const nextSortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.openAPIImportListQuery.sortBy;
      const nextSortOrder =
        query.sortOrder !== undefined ? query.sortOrder || undefined : this.openAPIImportListQuery.sortOrder;
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
          const response = await apiClient.get<{ items: OpenAPIImportDTO[] }>(
            `/workspaces/${workspaceID}/openapi-imports`,
          );
          return response.data.items.map((record) => importFromDTO(record, workspaceID));
        }),
      );
      return responses.flat();
    },

    async loadOpenAPIImports() {
      return this.loadOpenAPIImportCatalog();
    },

    async loadProtocols(options: { commit?: boolean } = {}) {
      const protocols = [{ protocol: "HTTP", adapterName: "HTTP OpenAPI Provider" }] as ToolProtocol[];
      if (options.commit !== false) this.protocols = protocols;
      return protocols;
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
      const record =
        typeof recordOrID === "string"
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
      const record = [...this.openAPIImportCatalog, ...this.openAPIImportPageItems].find(
        (item) => item.id === importId,
      );
      if (!record) throw new Error(`OpenAPI import ${importId} is not loaded.`);
      await apiClient.delete(`/workspaces/${record.workspaceId}/openapi-imports/${importId}`);
      this.openAPIImportCatalog = this.openAPIImportCatalog.filter((record) => record.id !== importId);
      this.openAPIImportPageItems = this.openAPIImportPageItems.filter((record) => record.id !== importId);
      this.openAPIImportPagination = {
        ...this.openAPIImportPagination,
        total: Math.max(0, this.openAPIImportPagination.total - 1),
      };
    },

    async generateToolDrafts(importId: string) {
      const loaded = [...this.openAPIImportCatalog, ...this.openAPIImportPageItems].find(
        (item) => item.id === importId,
      );
      if (!loaded) throw new Error(`OpenAPI import ${importId} is not loaded.`);
      const record = loaded.detail ? loaded : await this.loadOpenAPIImportDetail(loaded);
      const endpointIds = (record.detail?.endpoints || [])
        .filter(
          (endpoint) =>
            endpoint.ready !== false &&
            !endpoint.generatedCapabilityId &&
            !isAuthenticationInfrastructureEndpoint(endpoint.path),
        )
        .map((endpoint) => endpoint.id)
        .filter((id): id is string => Boolean(id));
      if (!endpointIds.length) return [];
      const response = await apiClient.post<{ items: GeneratedToolDTO[] }>(
        `/workspaces/${record.workspaceId}/openapi-imports/${importId}:generate-tools`,
        { endpointIds },
      );
      const normalizedTools = response.data.items.map((item) =>
        toolFromDTO(item.tool, record.workspaceId, [item.draft]),
      );
      normalizedTools.forEach((tool) => useToolsStore().upsertTool(tool));
      await this.loadOpenAPIImportDetail(record);
      return normalizedTools;
    },
  },
});
