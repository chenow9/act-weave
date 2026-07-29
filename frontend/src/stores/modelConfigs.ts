import { defineStore } from "pinia";

import { apiClient, toAPIError } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import type { ModelApiConfig, ModelApiConfigListQuery } from "../types/domain";
import { buildRuntimeCapabilitiesPayload } from "../utils/session-context-config";
import { useWorkspaceStore } from "./workspaces";

interface SecretReference {
  id: string;
  configured: boolean;
}

interface ModelConfigState {
  items: ModelApiConfig[];
  selectedConfigId: string;
  loading: boolean;
  error: string | null;
  hasLoaded: boolean;
  pagination: ListPagination;
  listQuery: Required<Pick<ModelApiConfigListQuery, "query" | "page" | "pageSize">> &
    Pick<ModelApiConfigListQuery, "status" | "sortBy" | "sortOrder">;
}

export const useModelConfigStore = defineStore("modelConfigs", {
  state: (): ModelConfigState => ({
    items: [],
    selectedConfigId: "",
    loading: false,
    error: null,
    hasLoaded: false,
    pagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    listQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
  }),
  getters: {
    selectedConfig: (state) => state.items.find((config) => config.id === state.selectedConfigId) || state.items[0],
  },
  actions: {
    workspaceID() {
      const store = useWorkspaceStore();
      const workspaceID = store.activeWorkspaceId || store.items[0]?.id;
      if (!workspaceID) throw new Error("当前没有可用的业务空间。请先创建业务空间，或联系管理员加入已有空间。");
      return workspaceID;
    },
    async fetchCatalog() {
      const response = await apiClient.get<{ items: ModelApiConfig[] }>(
        `/workspaces/${this.workspaceID()}/model-configs`,
      );
      return response.data.items.map(normalizeModelConfig);
    },
    async loadModelConfigs(query: ModelApiConfigListQuery = {}) {
      this.loading = true;
      this.error = null;
      const sortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.listQuery.sortBy;
      const sortOrder = sortBy
        ? query.sortOrder !== undefined
          ? query.sortOrder
          : this.listQuery.sortOrder
        : undefined;
      const requestQuery = {
        ...this.listQuery,
        ...query,
        query: query.query ?? this.listQuery.query,
        page: query.page ?? this.listQuery.page,
        pageSize: query.pageSize ?? this.listQuery.pageSize,
        sortBy,
        sortOrder,
      };
      try {
        const catalog = await this.fetchCatalog();
        const filtered = filterModelConfigs(catalog, requestQuery.query, requestQuery.status);
        const sorted = sortModelConfigs(filtered, sortBy, sortOrder);
        const page = Math.max(1, requestQuery.page);
        const pageSize = Math.max(1, requestQuery.pageSize);
        this.items = sorted.slice((page - 1) * pageSize, page * pageSize);
        this.pagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
        this.listQuery = {
          query: requestQuery.query,
          status: requestQuery.status,
          page,
          pageSize,
          sortBy,
          sortOrder,
        };
        if (!this.items.some((config) => config.id === this.selectedConfigId)) {
          this.selectedConfigId = this.items[0]?.id || "";
        }
        this.hasLoaded = true;
        return this.items;
      } catch (error) {
        this.error = toAPIError(error).message;
        return this.items;
      } finally {
        this.loading = false;
      }
    },
    async createModelConfig(config: ModelApiConfig) {
      const runtimeCapabilities = buildRuntimeCapabilitiesPayload(config.runtimeCapabilities);
      const response = await apiClient.post<ModelApiConfig>(`/workspaces/${this.workspaceID()}/model-configs`, {
        name: config.name,
        provider: config.provider,
        apiBase: config.apiBase,
        modelName: config.modelName,
        ...(config.credentialSecretId?.trim() ? { credentialSecretId: config.credentialSecretId.trim() } : {}),
        options: config.options || {},
        ...(runtimeCapabilities ? { runtimeCapabilities } : { runtimeCapabilities: {} }),
      });
      const created = normalizeModelConfig(response.data);
      this.upsertConfig(created);
      this.selectedConfigId = created.id;
      return created;
    },
    async createCredentialSecret(modelConfigName: string, plaintext: string) {
      const response = await apiClient.post<SecretReference>(`/workspaces/${this.workspaceID()}/secrets`, {
        name: `model-credential-${modelConfigName.trim()}-${Date.now()}`,
        kind: "API_KEY",
        plaintext,
      });
      return response.data;
    },
    async updateModelConfig(configId: string, config: ModelApiConfig) {
      const runtimeCapabilities = buildRuntimeCapabilitiesPayload(config.runtimeCapabilities);
      const response = await apiClient.patch<ModelApiConfig>(
        `/workspaces/${this.workspaceID()}/model-configs/${configId}`,
        {
          name: config.name,
          provider: config.provider,
          apiBase: config.apiBase,
          modelName: config.modelName,
          ...(config.credentialSecretId?.trim() ? { credentialSecretId: config.credentialSecretId.trim() } : {}),
          options: config.options || {},
          runtimeCapabilities: runtimeCapabilities || {},
          lockVersion: config.lockVersion,
        },
      );
      const updated = normalizeModelConfig(response.data);
      this.upsertConfig(updated);
      return updated;
    },
    async verifyModelConfig(configId: string) {
      const response = await apiClient.post<ModelApiConfig>(
        `/workspaces/${this.workspaceID()}/model-configs/${configId}:verify`,
      );
      const verified = normalizeModelConfig(response.data);
      this.upsertConfig(verified);
      return verified;
    },
    async deleteModelConfig(configId: string) {
      const config = this.items.find((item) => item.id === configId);
      if (!config) throw new Error(`Model configuration ${configId} is not loaded.`);
      await apiClient.delete(
        `/workspaces/${this.workspaceID()}/model-configs/${configId}?lockVersion=${config.lockVersion}`,
      );
      this.items = this.items.filter((item) => item.id !== configId);
      this.pagination = { ...this.pagination, total: Math.max(0, this.pagination.total - 1) };
      if (this.selectedConfigId === configId) this.selectedConfigId = this.items[0]?.id || "";
    },
    upsertConfig(config: ModelApiConfig) {
      this.items = this.items.some((item) => item.id === config.id)
        ? this.items.map((item) => (item.id === config.id ? config : item))
        : [config, ...this.items];
    },
  },
});

function normalizeModelConfig(config: ModelApiConfig): ModelApiConfig {
  return {
    ...config,
    credentialConfigured: Boolean(config.credentialConfigured),
    options: config.options || {},
    runtimeCapabilities: config.runtimeCapabilities || {},
    lastLatencyMs: config.lastLatencyMs ?? undefined,
    credentialSecretId: undefined,
  };
}

function filterModelConfigs(items: ModelApiConfig[], query: string, status?: ModelApiConfig["status"]) {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((config) => {
    if (status && config.status !== status) return false;
    if (!needle) return true;
    return [config.name, config.provider, config.apiBase, config.modelName, config.createdBy].some((value) =>
      value.toLocaleLowerCase().includes(needle),
    );
  });
}

function sortModelConfigs(items: ModelApiConfig[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set([
    "name",
    "provider",
    "apiBase",
    "modelName",
    "status",
    "lastLatencyMs",
    "createdBy",
    "updatedBy",
  ]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const comparison = String(left[sortBy as keyof ModelApiConfig] ?? "").localeCompare(
      String(right[sortBy as keyof ModelApiConfig] ?? ""),
      "zh-Hans",
      { numeric: true },
    );
    return order === "asc" ? comparison : -comparison;
  });
}
