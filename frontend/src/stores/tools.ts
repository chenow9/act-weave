/**
 * Tools domain store (ZKL-64 item 10).
 * Owns this domain's collections only. Secrets stay in action local params.
 */
import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import { requireActiveWorkspaceId, accessibleWorkspaceIDs } from "../services/integration/workspace";
import type { CatalogLoadStatus } from "../services/integration/catalog-types";
import * as mappers from "../services/integration/mappers";
import type {
  CapabilityProvider,
  ServiceConnection,
  Tool,
  ToolListQuery,
  ToolTestExecutionResult,
  ToolTestResult,
} from "../types/domain";
import { useProvidersStore } from "./providers";
import { useConnectionsStore } from "./connections";

const {
  normalizeProvider,
  connectionFromDTO,
  toolFromDTO,
  normalizeToolVersion,
  normalizeToolTestResult,
  toolDraftPayload,
  toolSpecSignature,
  slugify,
  numberValue,
  filterTools,
  sortTools,
} = mappers;
type ConnectionDTO = mappers.ConnectionDTO;
type ToolDTO = mappers.ToolDTO;
type ToolVersionDTO = mappers.ToolVersionDTO;
type PublishToolDTO = mappers.PublishToolDTO;

export const useToolsStore = defineStore("tools", {
  state: () => ({
    tools: [] as Tool[],
    toolPageItems: [] as Tool[],
    toolPagination: {
      page: 1,
      pageSize: DEFAULT_PAGE_SIZE,
      total: 0,
      pageSizeOptions: [...PAGE_SIZE_OPTIONS],
    } as ListPagination,
    toolListQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE } as any,
    toolPageLoading: false,
    toolPageError: null as string | null,
    toolPageHasLoaded: false,
    toolConnectionsByWorkspace: {} as Record<string, ServiceConnection[]>,
    toolConnectionCatalogStateByWorkspace: {} as Record<
      string,
      { status: CatalogLoadStatus; errorCode?: string; requestId?: string }
    >,
    loading: false,
  }),
  getters: {
    // tools domain
  },
  actions: {
    workspaceID() {
      return requireActiveWorkspaceId();
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
        const filtered = filterTools(catalog, requestQuery.query, requestQuery.status, requestQuery.type, (tool) =>
          this.connectionForTool(tool),
        );
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
      const priorState = this.toolConnectionCatalogStateByWorkspace[workspaceID];
      if (
        !force &&
        priorState?.status === "LOADED" &&
        Object.prototype.hasOwnProperty.call(this.toolConnectionsByWorkspace, workspaceID)
      ) {
        return this.toolConnectionsByWorkspace[workspaceID];
      }
      // Force reload may retain prior entities for stable render, but status is LOADING
      // so availability does not treat mid-flight as MISSING (ZKL-56).
      this.toolConnectionCatalogStateByWorkspace = {
        ...this.toolConnectionCatalogStateByWorkspace,
        [workspaceID]: { status: "LOADING" as CatalogLoadStatus },
      };

      try {
        const providerResponse = await apiClient.get<{ items: CapabilityProvider[] }>(
          `/workspaces/${workspaceID}/providers`,
        );
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
        this.toolConnectionCatalogStateByWorkspace = {
          ...this.toolConnectionCatalogStateByWorkspace,
          [workspaceID]: { status: "LOADED" as CatalogLoadStatus },
        };
        if (workspaceID === this.workspaceID()) {
          const providersStore = useProvidersStore();
          const connectionsStore = useConnectionsStore();
          providersStore.providers = providers;
          connectionsStore.serviceConnectionCatalog = connections;
          connectionsStore.serviceConnectionRegistryTotal = connections.length;
        }
        return connections;
      } catch (error) {
        this.toolConnectionCatalogStateByWorkspace = {
          ...this.toolConnectionCatalogStateByWorkspace,
          [workspaceID]: { status: "ERROR" as CatalogLoadStatus, errorCode: "CATALOG_LOAD_FAILED" },
        };
        throw error;
      }
    },

    catalogStatusForWorkspace(workspaceID: string): CatalogLoadStatus {
      return this.toolConnectionCatalogStateByWorkspace[workspaceID]?.status || "IDLE";
    },

    connectionForTool(tool: Tool) {
      // Strict workspace key — never fall back to active workspace / global catalog.
      const scopedConnections = this.toolConnectionsByWorkspace[tool.workspaceId];
      if (!scopedConnections) {
        return undefined;
      }
      return scopedConnections.find((connection) => connection.id === tool.connectionId);
    },

    async createTool(tool: Tool) {
      const providerId =
        tool.providerId ||
        useConnectionsStore().serviceConnectionCatalog.find((item) => item.id === tool.connectionId)?.providerId ||
        useProvidersStore().providers[0]?.id;
      if (!providerId) throw new Error("Select a Provider before creating a Tool.");
      const response = await apiClient.post<{ tool: ToolDTO; draft: ToolVersionDTO }>(
        `/workspaces/${tool.workspaceId}/tools`,
        {
          providerId,
          ...(tool.sourceAssetId ? { sourceAssetId: tool.sourceAssetId } : {}),
          ...(tool.connectionId ? { defaultConnectionId: tool.connectionId } : {}),
          ...(tool.sourceEndpointId ? { sourceEndpointId: tool.sourceEndpointId } : {}),
          name: tool.name,
          slug: tool.slug || slugify(tool.name),
          description: tool.description,
          draft: toolDraftPayload(tool),
        },
      );
      const normalized = toolFromDTO(response.data.tool, tool.workspaceId, [response.data.draft]);
      this.upsertTool(normalized);
      return normalized;
    },

    async updateTool(toolId: string, tool: Tool) {
      const current =
        this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId) || tool;
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
          const created = await apiClient.post<ToolVersionDTO>(
            `/workspaces/${tool.workspaceId}/tools/${toolId}/versions`,
            {
              sourceVersionId: published.id,
            },
          );
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
        versions = versions.map((version) =>
          version.id === draft.id ? normalizeToolVersion(updatedVersion.data) : version,
        );
      }
      const normalized = toolFromDTO(response.data, tool.workspaceId, versions, current.lastTestResult);
      this.upsertTool(normalized);
      return normalized;
    },

    async deleteTool(toolId: string) {
      const tool =
        this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId);
      if (!tool) throw new Error(`Tool ${toolId} is not loaded.`);
      await apiClient.delete(`/workspaces/${tool.workspaceId}/tools/${toolId}?lockVersion=${tool.lockVersion}`);
      this.tools = this.tools.filter((tool) => tool.id !== toolId);
      this.toolPageItems = this.toolPageItems.filter((tool) => tool.id !== toolId);
      this.toolPagination = { ...this.toolPagination, total: Math.max(0, this.toolPagination.total - 1) };
    },

    async testTool(toolId: string, inputParams: Record<string, unknown>) {
      const tool =
        this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId);
      const version = tool?.draftVersion;
      if (!tool || !version || version.lifecycleStatus === "PUBLISHED")
        throw new Error("Select an editable Tool Version before testing.");
      const response = await apiClient.post<ToolTestResult>(
        `/workspaces/${tool.workspaceId}/tools/${toolId}/versions/${version.id}:test`,
        { connectionId: tool.connectionId, input: inputParams },
      );
      const testResult = normalizeToolTestResult(response.data)!;
      const versionsResponse = await apiClient.get<{ items: ToolVersionDTO[] }>(
        `/workspaces/${tool.workspaceId}/tools/${toolId}/versions`,
      );
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
      const tool =
        this.tools.find((item) => item.id === toolId) || this.toolPageItems.find((item) => item.id === toolId);
      const version = tool?.draftVersion;
      if (!tool || !version || version.lifecycleStatus !== "TESTED")
        throw new Error("Test the exact Draft Version before publishing it.");
      const response = await apiClient.post<PublishToolDTO>(
        `/workspaces/${tool.workspaceId}/tools/${toolId}/versions/${version.id}:publish`,
        {
          callableName: slugify(tool.slug || tool.name).replace(/-/g, "_"),
          callableDescription: tool.description,
          lockVersion: version.lockVersion,
        },
      );
      const metadata = await apiClient.get<ToolDTO>(`/workspaces/${tool.workspaceId}/tools/${toolId}`);
      const versions = tool.versions.map((item) =>
        item.id === version.id ? normalizeToolVersion(response.data.version) : item,
      );
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
