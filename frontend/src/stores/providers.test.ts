import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import { useProvidersStore } from "./providers";
import { useWorkspaceStore } from "./workspaces";

vi.mock("../services/api", () => ({
  apiClient: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

describe("providers store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    const workspaces = useWorkspaceStore();
    workspaces.items = [{ id: "ws-1", name: "ws-1", displayName: "WS", mode: "PRODUCTION", status: "ACTIVE" } as never];
    workspaces.activeWorkspaceId = "ws-1";
  });

  it("loadProviders maps and stores provider list", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        items: [
          {
            id: "p1",
            name: "HTTP",
            driverKey: "http.openapi",
            transport: "HTTP",
            endpointConfig: {},
            driverConfig: {},
            discoveryMode: "OPENAPI",
            status: "ACTIVE",
            lockVersion: 1,
          },
        ],
      },
    } as never);

    const store = useProvidersStore();
    const items = await store.loadProviders();
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/ws-1/providers");
    expect(items).toHaveLength(1);
    expect(store.providers[0]?.id).toBe("p1");
  });

  it("createProvider posts write payload without secret fields", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        id: "p2",
        name: "New",
        driverKey: "http.openapi",
        transport: "HTTP",
        endpointConfig: {},
        driverConfig: {},
        discoveryMode: "OPENAPI",
        status: "ACTIVE",
        lockVersion: 1,
      },
    } as never);
    const store = useProvidersStore();
    await store.createProvider({
      id: "",
      name: "New",
      driverKey: "http.openapi",
      transport: "HTTP",
      endpointConfig: {},
      driverConfig: {},
      discoveryMode: "OPENAPI",
      status: "ACTIVE",
      lockVersion: 0,
    } as never);
    const body = vi.mocked(apiClient.post).mock.calls[0]?.[1] as Record<string, unknown>;
    expect(JSON.stringify(body)).not.toMatch(/password|secret|plaintext/i);
    expect(store.providers[0]?.id).toBe("p2");
  });
});
