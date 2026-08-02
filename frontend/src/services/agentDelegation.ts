import { apiClient } from "./api";
import type {
  AgentA2AExposure,
  AgentA2ARemoteBinding,
  AgentDelegationBinding,
} from "../types/domain";

/** Server-authoritative A2A management capabilities (do not hardcode client-side). */
export type A2ACapabilities = {
  allowAuthNone: boolean;
  authModes: string[];
  softDisable: boolean;
};

export async function getA2ACapabilities(workspaceId: string): Promise<A2ACapabilities> {
  const res = await apiClient.get<A2ACapabilities>(
    `/workspaces/${workspaceId}/a2a/capabilities`,
  );
  return {
    allowAuthNone: !!res.data?.allowAuthNone,
    authModes: Array.isArray(res.data?.authModes) ? res.data.authModes : ["AGENT_ACCESS"],
    softDisable: res.data?.softDisable !== false,
  };
}

export async function listDelegationBindings(workspaceId: string, agentId: string) {
  const res = await apiClient.get<{ items: AgentDelegationBinding[] }>(
    `/workspaces/${workspaceId}/agents/${agentId}/delegation-bindings`,
  );
  return res.data.items ?? [];
}

export async function createDelegationBinding(
  workspaceId: string,
  agentId: string,
  body: {
    targetAgentId: string;
    callableName: string;
    description?: string;
    mode?: string;
    contextPolicy?: string;
    enabled?: boolean;
  },
) {
  const res = await apiClient.post<AgentDelegationBinding>(
    `/workspaces/${workspaceId}/agents/${agentId}/delegation-bindings`,
    body,
  );
  return res.data;
}

export async function updateDelegationBinding(
  workspaceId: string,
  bindingId: string,
  body: {
    expectedVersion: number;
    targetAgentId?: string;
    callableName?: string;
    description?: string;
    mode?: string;
    contextPolicy?: string;
    enabled?: boolean;
  },
) {
  const res = await apiClient.patch<AgentDelegationBinding>(
    `/workspaces/${workspaceId}/delegation-bindings/${bindingId}`,
    body,
  );
  return res.data;
}

export async function disableDelegationBinding(
  workspaceId: string,
  bindingId: string,
  expectedVersion: number,
) {
  return apiClient.delete(`/workspaces/${workspaceId}/delegation-bindings/${bindingId}`, {
    data: { expectedVersion },
  });
}

export async function listA2AExposures(workspaceId: string) {
  const res = await apiClient.get<{ items: AgentA2AExposure[] }>(
    `/workspaces/${workspaceId}/a2a/exposures`,
  );
  return res.data.items ?? [];
}

export async function createA2AExposure(
  workspaceId: string,
  body: {
    agentId: string;
    publicName: string;
    publicDescription?: string;
    authMode?: string;
    enabled?: boolean;
  },
) {
  const res = await apiClient.post<AgentA2AExposure>(`/workspaces/${workspaceId}/a2a/exposures`, body);
  return res.data;
}

export async function updateA2AExposure(
  workspaceId: string,
  exposureId: string,
  body: {
    expectedVersion: number;
    publicName?: string;
    publicDescription?: string;
    authMode?: string;
    enabled?: boolean;
  },
) {
  const res = await apiClient.patch<AgentA2AExposure>(
    `/workspaces/${workspaceId}/a2a/exposures/${exposureId}`,
    body,
  );
  return res.data;
}

export async function disableA2AExposure(
  workspaceId: string,
  exposureId: string,
  expectedVersion: number,
) {
  return apiClient.delete(`/workspaces/${workspaceId}/a2a/exposures/${exposureId}`, {
    data: { expectedVersion },
  });
}

export async function previewA2AAgentCard(workspaceId: string, exposureId: string) {
  const res = await apiClient.get<Record<string, unknown>>(
    `/workspaces/${workspaceId}/a2a/exposures/${exposureId}/agent-card`,
  );
  return res.data;
}

export async function listA2ARemotes(workspaceId: string, agentId: string) {
  const res = await apiClient.get<{ items: AgentA2ARemoteBinding[] }>(
    `/workspaces/${workspaceId}/agents/${agentId}/a2a-remotes`,
  );
  return res.data.items ?? [];
}

export async function createA2ARemote(
  workspaceId: string,
  agentId: string,
  body: {
    callableName: string;
    description?: string;
    endpointUrl: string;
    agentCardUrl?: string;
    allowedHosts?: string[];
    authSecretRef?: string;
    timeoutMs?: number;
    enabled?: boolean;
  },
) {
  const res = await apiClient.post<AgentA2ARemoteBinding>(
    `/workspaces/${workspaceId}/agents/${agentId}/a2a-remotes`,
    body,
  );
  return res.data;
}

export async function disableA2ARemote(
  workspaceId: string,
  remoteId: string,
  expectedVersion: number,
) {
  return apiClient.delete(`/workspaces/${workspaceId}/a2a-remotes/${remoteId}`, {
    data: { expectedVersion },
  });
}

export async function updateA2ARemote(
  workspaceId: string,
  remoteId: string,
  body: {
    expectedVersion: number;
    callableName?: string;
    description?: string;
    endpointUrl?: string;
    agentCardUrl?: string;
    allowedHosts?: string[];
    authSecretRef?: string;
    timeoutMs?: number;
    enabled?: boolean;
  },
) {
  const res = await apiClient.patch<AgentA2ARemoteBinding>(
    `/workspaces/${workspaceId}/a2a-remotes/${remoteId}`,
    body,
  );
  return res.data;
}
