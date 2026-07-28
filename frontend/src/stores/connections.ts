/**
 * Connections domain store (ZKL-64 item 10).
 * Owns this domain's collections only. Secrets stay in action local params.
 */
import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import { requireActiveWorkspaceId } from "../services/integration/workspace";
import * as mappers from "../services/integration/mappers";
import type { ServiceConnection, ServiceConnectionListQuery, ServiceConnectionVerification } from "../types/domain";
import { useProvidersStore } from "./providers";
import { useToolsStore } from "./tools";

const { connectionFromDTO, connectionWritePayload, verificationFromDTO, filterConnections, sortConnections } = mappers;
type ConnectionDTO = mappers.ConnectionDTO;
type ConnectionVerificationDTO = mappers.ConnectionVerificationDTO;
type SecretReadDTO = mappers.SecretReadDTO;

export const useConnectionsStore = defineStore("connections", {
  state: () => ({
    serviceConnectionPageItems: [] as ServiceConnection[],
    serviceConnectionPagination: {
      page: 1,
      pageSize: DEFAULT_PAGE_SIZE,
      total: 0,
      pageSizeOptions: [...PAGE_SIZE_OPTIONS],
    } as ListPagination,
    serviceConnectionListQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE } as any,
    serviceConnectionCatalog: [] as ServiceConnection[],
    serviceConnectionRegistryTotal: 0,
    verificationByConnectionId: {} as Record<string, ServiceConnectionVerification>,
    loading: false,
  }),
  getters: {
    serviceConnections: (state) => state.serviceConnectionCatalog,
  },
  actions: {
    workspaceID() {
      return requireActiveWorkspaceId();
    },
    async loadServiceConnectionPage(query: ServiceConnectionListQuery = {}) {
      const nextSortBy =
        query.sortBy !== undefined ? query.sortBy || undefined : this.serviceConnectionListQuery.sortBy;
      const nextSortOrder =
        query.sortOrder !== undefined ? query.sortOrder || undefined : this.serviceConnectionListQuery.sortOrder;
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
      this.serviceConnectionPagination = {
        page,
        pageSize,
        total: sorted.length,
        pageSizeOptions: [...PAGE_SIZE_OPTIONS],
      };
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
      const providers = useProvidersStore().providers.length
        ? useProvidersStore().providers
        : await useProvidersStore().loadProviders();
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

    async rotateSecret(secretId: string, plaintext: string, lockVersion: number) {
      const response = await apiClient.post<SecretReadDTO>(
        `/workspaces/${this.workspaceID()}/secrets/${secretId}:rotate`,
        {
          plaintext,
          lockVersion,
        },
      );
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
      const provider = this.requireProvider(connection.providerId || useProvidersStore().providers[0]?.id);
      const response = await apiClient.post<ConnectionDTO>(
        `/workspaces/${workspaceID}/providers/${provider.id}/connections`,
        connectionWritePayload(connection, false, credentialPlaintext, {
          machineCredentialPlaintext: options.machineCredentialPlaintext,
        }),
      );
      const created = connectionFromDTO(response.data, provider, workspaceID);
      this.serviceConnectionCatalog = [created, ...this.serviceConnectionCatalog];
      this.serviceConnectionPageItems = [created, ...this.serviceConnectionPageItems];
      if (useToolsStore().toolConnectionsByWorkspace[workspaceID]) {
        useToolsStore().toolConnectionsByWorkspace[workspaceID] = [
          created,
          ...useToolsStore().toolConnectionsByWorkspace[workspaceID],
        ];
      }
      this.serviceConnectionPagination = {
        ...this.serviceConnectionPagination,
        total: this.serviceConnectionPagination.total + 1,
      };
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
      this.serviceConnectionCatalog = this.serviceConnectionCatalog.map((item) =>
        item.id === connectionId ? updated : item,
      );
      this.serviceConnectionPageItems = this.serviceConnectionPageItems.map((item) =>
        item.id === connectionId ? updated : item,
      );
      useToolsStore().toolConnectionsByWorkspace[workspaceID] = (
        useToolsStore().toolConnectionsByWorkspace[workspaceID] || []
      ).map((item) => (item.id === connectionId ? updated : item));
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

    async deleteServiceConnection(connectionId: string) {
      const workspaceID = this.workspaceID();
      const connection =
        this.serviceConnectionCatalog.find((item) => item.id === connectionId) ||
        this.serviceConnectionPageItems.find((item) => item.id === connectionId);
      if (!connection) throw new Error(`Connection ${connectionId} is not loaded.`);
      await apiClient.delete(
        `/workspaces/${workspaceID}/connections/${connectionId}?lockVersion=${connection.lockVersion}`,
      );
      this.serviceConnectionCatalog = this.serviceConnectionCatalog.filter(
        (connection) => connection.id !== connectionId,
      );
      this.serviceConnectionPageItems = this.serviceConnectionPageItems.filter(
        (connection) => connection.id !== connectionId,
      );
      useToolsStore().toolConnectionsByWorkspace[workspaceID] = (
        useToolsStore().toolConnectionsByWorkspace[workspaceID] || []
      ).filter((connection) => connection.id !== connectionId);
      this.serviceConnectionPagination = {
        ...this.serviceConnectionPagination,
        total: Math.max(0, this.serviceConnectionPagination.total - 1),
      };
      delete this.verificationByConnectionId[connectionId];
    },

    async verifyConnection(connectionId: string) {
      const workspaceID = this.workspaceID();
      const response = await apiClient.post<ConnectionVerificationDTO>(
        `/workspaces/${workspaceID}/connections/${connectionId}:verify`,
      );
      const verification = verificationFromDTO(response.data);
      this.verificationByConnectionId[connectionId] = verification;
      const nextStatus: ServiceConnection["status"] = verification.status === "SUCCEEDED" ? "VERIFIED" : "ERROR";
      this.serviceConnectionCatalog = this.serviceConnectionCatalog.map((connection) =>
        connection.id === connectionId ? { ...connection, status: nextStatus } : connection,
      );
      this.serviceConnectionPageItems = this.serviceConnectionPageItems.map((connection) =>
        connection.id === connectionId ? { ...connection, status: nextStatus } : connection,
      );
      useToolsStore().toolConnectionsByWorkspace[workspaceID] = (
        useToolsStore().toolConnectionsByWorkspace[workspaceID] || []
      ).map((connection) => (connection.id === connectionId ? { ...connection, status: nextStatus } : connection));
      return verification;
    },

    requireProvider(providerId: string) {
      const provider = useProvidersStore().providers.find((item) => item.id === providerId);
      if (!provider) throw new Error("Select an HTTP OpenAPI Provider before saving a Connection.");
      return provider;
    },
  },
});
