/**
 * Providers domain store (ZKL-64 item 10).
 * Owns this domain's collections only. Secrets stay in action local params.
 */
import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import { requireActiveWorkspaceId } from "../services/integration/workspace";
import * as mappers from "../services/integration/mappers";
import type { CapabilityProvider, ProviderAsset } from "../types/domain";

const { normalizeProvider, providerWritePayload } = mappers;
type ProviderSyncResult = mappers.ProviderSyncResult;
type ProviderMaterializationResult = mappers.ProviderMaterializationResult;

export const useProvidersStore = defineStore("providers", {
  state: () => ({
    providers: [] as CapabilityProvider[],
    providerAssetsByProvider: {} as Record<string, ProviderAsset[]>,
    loading: false,
  }),
  getters: {
    // providers domain
  },
  actions: {
    workspaceID() {
      return requireActiveWorkspaceId();
    },
    async loadProviders() {
      const response = await apiClient.get<{ items: CapabilityProvider[] }>(
        `/workspaces/${this.workspaceID()}/providers`,
      );
      this.providers = response.data.items.map(normalizeProvider);
      return this.providers;
    },

    async createProvider(provider: CapabilityProvider) {
      const response = await apiClient.post<CapabilityProvider>(
        `/workspaces/${this.workspaceID()}/providers`,
        providerWritePayload(provider),
      );
      const created = normalizeProvider(response.data);
      this.providers = [created, ...this.providers];
      return created;
    },

    async updateProvider(provider: CapabilityProvider) {
      const response = await apiClient.patch<CapabilityProvider>(
        `/workspaces/${this.workspaceID()}/providers/${provider.id}`,
        {
          name: provider.name,
          driverKey: provider.driverKey,
          transport: provider.transport,
          endpointConfig: provider.endpointConfig,
          driverConfig: provider.driverConfig,
          discoveryMode: provider.discoveryMode,
          lockVersion: provider.lockVersion,
        },
      );
      const updated = normalizeProvider(response.data);
      this.providers = this.providers.map((item) => (item.id === updated.id ? updated : item));
      return updated;
    },

    async deleteProvider(providerId: string) {
      const provider = this.providers.find((item) => item.id === providerId);
      if (!provider) throw new Error(`Provider ${providerId} is not loaded.`);
      await apiClient.delete(
        `/workspaces/${this.workspaceID()}/providers/${providerId}?lockVersion=${provider.lockVersion}`,
      );
      this.providers = this.providers.filter((item) => item.id !== providerId);
      delete this.providerAssetsByProvider[providerId];
    },

    async syncProvider(providerId: string) {
      const response = await apiClient.post<ProviderSyncResult>(
        `/workspaces/${this.workspaceID()}/providers/${providerId}:sync`,
      );
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
  },
});
