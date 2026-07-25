import { defineStore } from "pinia";

import { apiClient, toAPIError } from "../services/api";

export type AgentAccessStatus = "ACTIVE" | "DISABLED";
export type AgentAccessAuthMethod = "client_secret_basic" | "private_key_jwt";
export type AgentAccessCredentialType = "client_secret" | "jwk";
export type AgentAccessScope =
  | "agent:read"
  | "conversation:create"
  | "conversation:read"
  | "run:create"
  | "run:read"
  | "run:cancel"
  | "event:read"
  | "interaction:decide"
  | "artifact:read";

export interface AgentAccessClient {
  id: string;
  workspaceId: string;
  servicePrincipalId: string;
  clientId: string;
  name: string;
  status: AgentAccessStatus;
  authMethod: AgentAccessAuthMethod;
  jwksUri?: string;
  trustedSubjectIssuer?: string;
  trustedSubjectJwksUri?: string;
  allowedCorsOrigins: string[];
  tokenTtlSeconds: number;
  createdAt: string;
  updatedAt: string;
  disabledAt?: string;
  lockVersion: number;
}

export interface AgentAccessCredential {
  id: string;
  type: AgentAccessCredentialType;
  publicHint: string;
  validFrom: string;
  expiresAt?: string;
  lastUsedAt?: string;
  revokedAt?: string;
  createdAt: string;
  lockVersion: number;
}

export interface AgentAccessGrantPolicy {
  serviceDecision?: { enabled: boolean; maxRisk?: "low" | "medium" };
}

export interface AgentAccessGrant {
  id: string;
  agentId: string;
  scopes: AgentAccessScope[];
  policy: AgentAccessGrantPolicy;
  status: "ACTIVE" | "REVOKED";
  validFrom: string;
  expiresAt?: string;
  revokedAt?: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export interface CreateAgentAccessClientInput {
  name: string;
  authMethod: AgentAccessAuthMethod;
  jwksUri?: string;
  jwkThumbprint?: string;
  credentialPublicHint?: string;
  trustedSubjectIssuer?: string;
  trustedSubjectJwksUri?: string;
  allowedCorsOrigins: string[];
  tokenTtlSeconds?: number;
}

export interface RotateAgentAccessCredentialInput {
  type: AgentAccessCredentialType;
  jwkThumbprint?: string;
  publicHint?: string;
  expiresAt?: string;
  replacesCredentialId: string;
  replacesLockVersion: number;
  overlapSeconds: number;
}

export interface CreateAgentAccessGrantInput {
  agentId: string;
  scopes: AgentAccessScope[];
  policy: AgentAccessGrantPolicy;
  validFrom?: string;
  expiresAt?: string;
}

interface AgentAccessState {
  workspaceId: string;
  clients: AgentAccessClient[];
  selectedClientId: string;
  credentials: AgentAccessCredential[];
  grants: AgentAccessGrant[];
  loading: boolean;
  detailLoading: boolean;
  mutating: boolean;
  error: string;
  hasLoaded: boolean;
}

export const useAgentAccessStore = defineStore("agentAccess", {
  state: (): AgentAccessState => ({
    workspaceId: "",
    clients: [],
    selectedClientId: "",
    credentials: [],
    grants: [],
    loading: false,
    detailLoading: false,
    mutating: false,
    error: "",
    hasLoaded: false,
  }),
  getters: {
    selectedClient: (state) => state.clients.find((client) => client.id === state.selectedClientId),
    activeCredentials: (state) => state.credentials.filter((credential) => !credential.revokedAt),
    activeGrants: (state) => state.grants.filter((grant) => grant.status === "ACTIVE"),
  },
  actions: {
    async load(workspaceId: string) {
      this.loading = true;
      this.error = "";
      this.workspaceId = workspaceId;
      try {
        const response = await apiClient.get<{ items: AgentAccessClient[] }>(
          `/workspaces/${workspaceId}/agent-access/clients`,
        );
        if (this.workspaceId !== workspaceId) return this.clients;
        this.clients = response.data.items;
        if (!this.clients.some((client) => client.id === this.selectedClientId)) {
          this.selectedClientId = this.clients[0]?.id || "";
        }
        if (this.selectedClientId) await this.loadClientDetail(this.selectedClientId);
        else {
          this.credentials = [];
          this.grants = [];
        }
        this.hasLoaded = true;
        return this.clients;
      } catch (error) {
        this.error = toAPIError(error).message;
        throw error;
      } finally {
        this.loading = false;
      }
    },
    async loadClientDetail(clientId: string) {
      if (!this.workspaceId) return;
      this.detailLoading = true;
      this.selectedClientId = clientId;
      try {
        const [credentials, grants] = await Promise.all([
          apiClient.get<{ items: AgentAccessCredential[] }>(
            `/workspaces/${this.workspaceId}/agent-access/clients/${clientId}/credentials`,
          ),
          apiClient.get<{ items: AgentAccessGrant[] }>(
            `/workspaces/${this.workspaceId}/agent-access/clients/${clientId}/grants`,
          ),
        ]);
        if (this.selectedClientId !== clientId) return;
        this.credentials = credentials.data.items;
        this.grants = grants.data.items;
      } finally {
        this.detailLoading = false;
      }
    },
    async createClient(input: CreateAgentAccessClientInput) {
      return this.mutate(async () => {
        const response = await apiClient.post<{
          client: AgentAccessClient;
          credential: AgentAccessCredential;
          secret?: string;
        }>(`/workspaces/${this.workspaceId}/agent-access/clients`, input, commandConfig());
        this.clients = [response.data.client, ...this.clients.filter((item) => item.id !== response.data.client.id)];
        this.selectedClientId = response.data.client.id;
        this.credentials = [response.data.credential];
        this.grants = [];
        // Plaintext is returned to the caller only; it is never retained in Pinia state.
        return { ...response.data };
      });
    },
    async setClientStatus(client: AgentAccessClient, status: AgentAccessStatus) {
      return this.mutate(async () => {
        const command = status === "ACTIVE" ? "enable" : "disable";
        const response = await apiClient.post<{ client: AgentAccessClient }>(
          `/workspaces/${this.workspaceId}/agent-access/clients/${client.id}:${command}`,
          { lockVersion: client.lockVersion },
          commandConfig(),
        );
        this.upsertClient(response.data.client);
        return response.data.client;
      });
    },
    async rotateCredential(clientId: string, input: RotateAgentAccessCredentialInput) {
      return this.mutate(async () => {
        const response = await apiClient.post<{
          credential: AgentAccessCredential;
          replacedCredentialExpiresAt?: string;
          secret?: string;
        }>(
          `/workspaces/${this.workspaceId}/agent-access/clients/${clientId}/credentials`,
          input,
          commandConfig(),
        );
        await this.loadClientDetail(clientId);
        return { ...response.data };
      });
    },
    async revokeCredential(clientId: string, credential: AgentAccessCredential) {
      return this.mutate(async () => {
        const response = await apiClient.post<{ credential: AgentAccessCredential }>(
          `/workspaces/${this.workspaceId}/agent-access/clients/${clientId}/credentials/${credential.id}:revoke`,
          { lockVersion: credential.lockVersion },
          commandConfig(),
        );
        this.credentials = this.credentials.map((item) =>
          item.id === response.data.credential.id ? response.data.credential : item,
        );
        return response.data.credential;
      });
    },
    async createGrant(clientId: string, input: CreateAgentAccessGrantInput) {
      return this.mutate(async () => {
        const response = await apiClient.post<{ grant: AgentAccessGrant }>(
          `/workspaces/${this.workspaceId}/agent-access/clients/${clientId}/grants`,
          input,
          commandConfig(),
        );
        this.grants = [response.data.grant, ...this.grants];
        return response.data.grant;
      });
    },
    async revokeGrant(clientId: string, grant: AgentAccessGrant) {
      return this.mutate(async () => {
        const response = await apiClient.post<{ grant: AgentAccessGrant }>(
          `/workspaces/${this.workspaceId}/agent-access/clients/${clientId}/grants/${grant.id}:revoke`,
          { lockVersion: grant.lockVersion },
          commandConfig(),
        );
        this.grants = this.grants.map((item) => (item.id === response.data.grant.id ? response.data.grant : item));
        return response.data.grant;
      });
    },
    clear() {
      this.workspaceId = "";
      this.clients = [];
      this.selectedClientId = "";
      this.credentials = [];
      this.grants = [];
      this.error = "";
      this.hasLoaded = false;
    },
    upsertClient(client: AgentAccessClient) {
      this.clients = this.clients.some((item) => item.id === client.id)
        ? this.clients.map((item) => (item.id === client.id ? client : item))
        : [client, ...this.clients];
    },
    async mutate<T>(operation: () => Promise<T>) {
      this.mutating = true;
      this.error = "";
      try {
        return await operation();
      } catch (error) {
        this.error = toAPIError(error).message;
        throw error;
      } finally {
        this.mutating = false;
      }
    },
  },
});

function commandConfig() {
  return { headers: { "Idempotency-Key": newCommandID() } };
}

function newCommandID() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (value) =>
    (Number(value) ^ (Math.random() * 16 >> Number(value) / 4)).toString(16),
  );
}
