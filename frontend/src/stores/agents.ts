import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import type {
  Agent,
  AgentCapabilityBinding,
  AgentListQuery,
  CapabilityCatalogItem,
  PromptEnhancement,
} from "../types/domain";
import { buildContextPolicyPayload } from "../utils/session-context-config";
import { useWorkspaceStore } from "./workspaces";

interface AgentDTO {
  id: string;
  name: string;
  roleDescription: string;
  currentPromptRevisionId?: string;
  modelConfigId: string;
  isDefault: boolean;
  status: "ACTIVE" | "DISABLED";
  contextPolicy?: Agent["contextPolicy"];
  toolsCount: number;
  workflowsCount: number;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

interface CurrentPromptDTO {
  agentId: string;
  revisionId: string;
  revisionNo: number;
  systemPrompt: string;
  source: string;
  createdBy: string;
  createdAt: string;
}

interface CreatePreviewEnhancementDTO {
  runId: string;
  status: string;
  preview: boolean;
  output: string;
  createdAt: string;
  expiresAt?: string;
}

interface CreateAgentResponseDTO extends AgentDTO {
  initialPromptRevision?: { id: string; revisionNo: number; source: string };
  sourcePromptPreview?: { runId: string; linked: boolean; reason?: string };
}

interface AgentState {
  items: Agent[];
  pageItems: Agent[];
  pagination: ListPagination;
  listQuery: Required<Pick<AgentListQuery, "query" | "page" | "pageSize">> &
    Pick<AgentListQuery, "status" | "workspaceId" | "sortBy" | "sortOrder">;
  selectedAgentId: string;
  capabilitiesByWorkspace: Record<string, CapabilityCatalogItem[]>;
  bindingsByAgent: Record<string, AgentCapabilityBinding[]>;
  loading: boolean;
  pageLoading: boolean;
  pageError: string | null;
  pageHasLoaded: boolean;
}

export const useAgentStore = defineStore("agents", {
  state: (): AgentState => ({
    items: [],
    pageItems: [],
    pagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    listQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    selectedAgentId: "",
    capabilitiesByWorkspace: {},
    bindingsByAgent: {},
    loading: false,
    pageLoading: false,
    pageError: null,
    pageHasLoaded: false,
  }),
  getters: {
    selectedAgent: (state) => state.items.find((agent) => agent.id === state.selectedAgentId) || state.items[0],
  },
  actions: {
    async loadAgents(filter: { workspaceId?: string; query?: string } = {}) {
      this.loading = true;
      try {
        const catalog = await fetchAgentCatalog(filter.workspaceId);
        const query = filter.query?.trim().toLowerCase() || "";
        this.items = query ? catalog.filter((agent) => agentSearchText(agent).includes(query)) : catalog;
        if (!this.items.some((agent) => agent.id === this.selectedAgentId)) {
          this.selectedAgentId = this.items[0]?.id || "";
        }
        return this.items;
      } finally {
        this.loading = false;
      }
    },
    async loadAgentPage(query: AgentListQuery = {}) {
      this.pageLoading = true;
      this.pageError = null;
      const nextSortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.listQuery.sortBy;
      const nextSortOrder = query.sortOrder !== undefined ? query.sortOrder || undefined : this.listQuery.sortOrder;
      const requestQuery = {
        ...this.listQuery,
        ...query,
        query: query.query ?? this.listQuery.query,
        page: query.page ?? this.listQuery.page,
        pageSize: query.pageSize ?? this.listQuery.pageSize,
        sortBy: nextSortBy,
        sortOrder: nextSortBy ? nextSortOrder : undefined,
      };
      try {
        const catalog = await fetchAgentCatalog(requestQuery.workspaceId);
        this.items = catalog;
        const filtered = filterAgents(catalog, requestQuery);
        const sorted = sortAgents(filtered, requestQuery.sortBy, requestQuery.sortOrder);
        const pageSize = Math.max(1, requestQuery.pageSize);
        const pageCount = Math.max(1, Math.ceil(sorted.length / pageSize));
        const page = Math.min(Math.max(1, requestQuery.page), pageCount);
        this.pageItems = sorted.slice((page - 1) * pageSize, page * pageSize);
        this.pagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
        this.listQuery = {
          query: requestQuery.query,
          status: requestQuery.status,
          workspaceId: requestQuery.workspaceId,
          page,
          pageSize,
          sortBy: requestQuery.sortBy,
          sortOrder: requestQuery.sortOrder,
        };
        this.pageHasLoaded = true;
        if (!this.items.some((agent) => agent.id === this.selectedAgentId)) {
          this.selectedAgentId = this.items[0]?.id || "";
        }
        return this.pageItems;
      } catch (error) {
        this.pageError = error instanceof Error ? error.message : String(error);
        return this.pageItems;
      } finally {
        this.pageLoading = false;
      }
    },
    async createAgent(agent: Agent, options: { sourcePromptPreviewRunId?: string } = {}) {
      const response = await apiClient.post<AgentDTO & CreateAgentResponseDTO>(
        `/workspaces/${agent.workspaceId}/agents`,
        {
          name: agent.name,
          roleDescription: agent.roleDescription,
          modelConfigId: agent.modelConfigId,
          isDefault: agent.isDefault,
          systemPrompt: agent.systemPrompt,
          ...(options.sourcePromptPreviewRunId ? { sourcePromptPreviewRunId: options.sourcePromptPreviewRunId } : {}),
        },
      );
      const created = agentFromDTO(response.data, agent.workspaceId);
      this.upsertAgent(created);
      this.selectedAgentId = created.id;
      return {
        agent: created,
        sourcePromptPreview: response.data.sourcePromptPreview,
        initialPromptRevision: response.data.initialPromptRevision,
      };
    },
    async fetchCurrentPrompt(agent: Agent, signal?: AbortSignal) {
      const response = await apiClient.get<CurrentPromptDTO>(
        `/workspaces/${agent.workspaceId}/agents/${agent.id}/prompt-revisions/current`,
        { signal, headers: { "Cache-Control": "no-store" } },
      );
      return response.data;
    },
    async previewCreatePromptEnhancement(
      workspaceId: string,
      modelConfigId: string,
      input: string,
      signal?: AbortSignal,
    ) {
      const response = await apiClient.post<CreatePreviewEnhancementDTO>(
        `/workspaces/${workspaceId}/agents:preview-prompt-enhancement`,
        { modelConfigId, input },
        { timeout: 210_000, signal },
      );
      return response.data;
    },
    async updateAgent(agentId: string, agent: Agent) {
      const contextPolicy = buildContextPolicyPayload(agent.contextPolicy);
      const response = await apiClient.patch<AgentDTO>(`/workspaces/${agent.workspaceId}/agents/${agentId}`, {
        name: agent.name,
        roleDescription: agent.roleDescription,
        modelConfigId: agent.modelConfigId,
        status: agent.status,
        contextPolicy,
        lockVersion: agent.lockVersion,
      });
      const updated = agentFromDTO(response.data, agent.workspaceId);
      this.upsertAgent(updated);
      return updated;
    },
    async enhanceAgentPrompt(agent: Agent, input: string, options: { preview: boolean; lockVersion?: number }) {
      const response = await apiClient.post<PromptEnhancement>(
        `/workspaces/${agent.workspaceId}/agents/${agent.id}:enhance-prompt`,
        {
          input,
          preview: options.preview,
          ...(!options.preview ? { lockVersion: options.lockVersion ?? agent.lockVersion } : {}),
        },
        { timeout: 210_000 },
      );
      return response.data;
    },
    async deleteAgent(agentId: string) {
      const agent =
        this.items.find((item) => item.id === agentId) || this.pageItems.find((item) => item.id === agentId);
      if (!agent) throw new Error(`Agent ${agentId} is not loaded.`);
      await apiClient.delete(`/workspaces/${agent.workspaceId}/agents/${agentId}?lockVersion=${agent.lockVersion}`);
      this.items = this.items.filter((item) => item.id !== agentId);
      this.pageItems = this.pageItems.filter((item) => item.id !== agentId);
      this.pagination = { ...this.pagination, total: Math.max(0, this.pagination.total - 1) };
      delete this.bindingsByAgent[agentId];
      if (this.selectedAgentId === agentId) {
        this.selectedAgentId = this.items[0]?.id || "";
      }
    },
    async loadCapabilities(workspaceId: string) {
      const response = await apiClient.get<{ items: CapabilityCatalogItem[] }>(
        `/workspaces/${workspaceId}/capabilities`,
      );
      this.capabilitiesByWorkspace[workspaceId] = response.data.items;
      return response.data.items;
    },
    async loadAgentCapabilities(agent: Agent) {
      const response = await apiClient.get<{ items: AgentCapabilityBinding[] }>(
        `/workspaces/${agent.workspaceId}/agents/${agent.id}/capabilities`,
      );
      this.bindingsByAgent[agent.id] = response.data.items.map(normalizeBinding);
      return this.bindingsByAgent[agent.id];
    },
    async bindCapability(agent: Agent, capabilityId: string, input: AgentCapabilityBinding) {
      const response = await apiClient.put<AgentCapabilityBinding>(
        `/workspaces/${agent.workspaceId}/agents/${agent.id}/capabilities/${capabilityId}`,
        {
          versionPolicy: input.versionPolicy,
          pinnedReleaseId: input.versionPolicy === "PINNED" ? input.pinnedReleaseId : undefined,
          connectionId: input.connectionId || undefined,
          executionPolicyId: input.executionPolicyId || undefined,
          enabled: input.enabled,
          configOverrides: input.configOverrides || {},
          lockVersion: input.lockVersion,
        },
      );
      const binding = normalizeBinding(response.data);
      const current = this.bindingsByAgent[agent.id] || [];
      this.bindingsByAgent[agent.id] = current.some((item) => item.capabilityId === capabilityId)
        ? current.map((item) => (item.capabilityId === capabilityId ? binding : item))
        : [...current, binding];
      return binding;
    },
    async unbindCapability(agent: Agent, binding: AgentCapabilityBinding) {
      await apiClient.delete(
        `/workspaces/${agent.workspaceId}/agents/${agent.id}/capabilities/${binding.capabilityId}?lockVersion=${binding.lockVersion}`,
      );
      this.bindingsByAgent[agent.id] = (this.bindingsByAgent[agent.id] || []).filter(
        (item) => item.capabilityId !== binding.capabilityId,
      );
    },
    upsertAgent(agent: Agent) {
      this.items = this.items.some((item) => item.id === agent.id)
        ? this.items.map((item) => (item.id === agent.id ? agent : item))
        : [agent, ...this.items];
      if (this.pageItems.some((item) => item.id === agent.id)) {
        this.pageItems = this.pageItems.map((item) => (item.id === agent.id ? agent : item));
      }
    },
  },
});

async function fetchAgentCatalog(workspaceId?: string) {
  const workspaces = useWorkspaceStore();
  if (!workspaceId && !workspaces.items.length) await workspaces.load();
  // ZKL-64: single active-workspace catalog read; no multi-workspace fan-out.
  const id = workspaceId || workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
  if (!id) return [];
  const response = await apiClient.get<{ items: AgentDTO[] }>(`/workspaces/${id}/agents`);
  return response.data.items.map((agent) => agentFromDTO(agent, id));
}

function agentFromDTO(agent: AgentDTO, workspaceId: string): Agent {
  return {
    ...agent,
    workspaceId,
    currentPromptRevisionId: agent.currentPromptRevisionId,
    contextPolicy: agent.contextPolicy || {},
    systemPrompt: "",
  };
}

function normalizeBinding(binding: AgentCapabilityBinding): AgentCapabilityBinding {
  return {
    ...binding,
    pinnedReleaseId: binding.pinnedReleaseId || undefined,
    connectionId: binding.connectionId || undefined,
    executionPolicyId: binding.executionPolicyId || undefined,
    configOverrides: binding.configOverrides || {},
  };
}

function agentSearchText(agent: Agent) {
  return `${agent.name} ${agent.roleDescription} ${agent.workspaceId} ${agent.modelConfigId}`.toLowerCase();
}

function filterAgents(agents: Agent[], query: AgentListQuery) {
  const search = query.query?.trim().toLowerCase() || "";
  return agents.filter((agent) => {
    if (query.workspaceId && agent.workspaceId !== query.workspaceId) return false;
    if (query.status && agent.status !== query.status) return false;
    return !search || agentSearchText(agent).includes(search);
  });
}

function sortAgents(agents: Agent[], sortBy?: string, sortOrder?: "asc" | "desc") {
  if (!sortBy) return agents;
  const direction = sortOrder === "desc" ? -1 : 1;
  const value = (agent: Agent) => {
    if (sortBy === "workspace") return agent.workspaceId;
    if (sortBy === "model") return agent.modelConfigId;
    if (sortBy === "status") return agent.status;
    if (sortBy === "updatedAt") return agent.updatedAt;
    return agent.name;
  };
  return [...agents].sort((left, right) => value(left).localeCompare(value(right)) * direction);
}
