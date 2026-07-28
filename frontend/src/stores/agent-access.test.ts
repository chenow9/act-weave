import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

import { apiClient } from "../services/api";
import { useAgentAccessStore, type AgentAccessClient, type AgentAccessCredential } from "./agentAccess";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return {
    ...actual,
    apiClient: { get: vi.fn(), post: vi.fn() },
  };
});

describe("agent-access management store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("loads Workspace-scoped Clients without auto-opening the first client detail", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [client()] } } as never);
    const store = useAgentAccessStore();
    await store.load("workspace-1");
    expect(apiClient.get).toHaveBeenCalledTimes(1);
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/agent-access/clients");
    expect(store.clients).toHaveLength(1);
    expect(store.selectedClientId).toBe("");
    expect(store.credentials).toEqual([]);
  });

  it("loads public Credential metadata when opening a Client detail", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [credential()] } } as never)
      .mockResolvedValueOnce({ data: { items: [] } } as never);
    const store = useAgentAccessStore();
    store.workspaceId = "workspace-1";
    store.clients = [client()];
    await store.loadClientDetail("client-1");
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/agent-access/clients/client-1/credentials");
    expect(store.credentials[0]).toMatchObject({ publicHint: "…safe", lastUsedAt: "2026-07-20T01:00:00Z" });
  });

  it("returns a creation Secret once without retaining it in Pinia state", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: { client: client(), credential: credential(), secret: "awsk_live_once" },
    } as never);
    const store = useAgentAccessStore();
    store.workspaceId = "workspace-1";
    const result = await store.createClient({
      name: "Business",
      authMethod: "client_secret_basic",
      allowedCorsOrigins: [],
    });
    expect(result.secret).toBe("awsk_live_once");
    expect(JSON.stringify(store.$state)).not.toContain("awsk_live_once");
    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/agent-access/clients",
      expect.any(Object),
      expect.objectContaining({ headers: { "Idempotency-Key": expect.any(String) } }),
    );
  });

  it("sends lockVersion and a fresh Idempotency-Key for destructive commands", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: { credential: { ...credential(), revokedAt: "2026-07-20T02:00:00Z", lockVersion: 3 } },
    } as never);
    const store = useAgentAccessStore();
    store.workspaceId = "workspace-1";
    store.credentials = [credential()];
    await store.revokeCredential("client-1", credential());
    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/agent-access/clients/client-1/credentials/credential-1:revoke",
      { lockVersion: 2 },
      expect.objectContaining({ headers: { "Idempotency-Key": expect.any(String) } }),
    );
  });
});

function client(): AgentAccessClient {
  return {
    id: "client-1",
    workspaceId: "workspace-1",
    servicePrincipalId: "principal-1",
    clientId: "awcl_public",
    name: "Business",
    status: "ACTIVE",
    authMethod: "client_secret_basic",
    allowedCorsOrigins: [],
    tokenTtlSeconds: 600,
    createdAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
    lockVersion: 1,
  };
}

function credential(): AgentAccessCredential {
  return {
    id: "credential-1",
    type: "client_secret",
    publicHint: "…safe",
    validFrom: "2026-07-20T00:00:00Z",
    lastUsedAt: "2026-07-20T01:00:00Z",
    createdAt: "2026-07-20T00:00:00Z",
    lockVersion: 2,
  };
}
