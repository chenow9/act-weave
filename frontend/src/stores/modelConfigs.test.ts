import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { ModelApiConfig } from "../types/domain";
import { useModelConfigStore } from "./modelConfigs";
import { useWorkspaceStore } from "./workspaces";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return { ...actual, apiClient: { delete: vi.fn(), get: vi.fn(), patch: vi.fn(), post: vi.fn() } };
});

describe("v1 model config store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    useWorkspaceStore().activeWorkspaceId = "workspace-1";
    vi.resetAllMocks();
  });

  it("loads and locally filters the workspace model catalog", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: { items: [modelFixture(), modelFixture({ id: "model-2", name: "Backup", status: "ERROR" })] },
    });
    const store = useModelConfigStore();
    await store.loadModelConfigs({ status: "VERIFIED", page: 1, pageSize: 10 });
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/model-configs");
    expect(store.items.map((item) => item.id)).toEqual(["model-1"]);
  });

  it("never writes masked credentials and preserves an existing credential when replacement id is blank", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: modelFixture() });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({ data: modelFixture({ name: "Primary 2", lockVersion: 2 }) });
    const store = useModelConfigStore();
    const draft = modelFixture({ id: "", credentialSecretId: "secret-1", credentialConfigured: false });
    const created = await store.createModelConfig(draft);
    await store.updateModelConfig(created.id, { ...created, name: "Primary 2", credentialSecretId: "" });

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/model-configs",
      expect.objectContaining({ credentialSecretId: "secret-1" }),
    );
    const updatePayload = vi.mocked(apiClient.patch).mock.calls[0][1] as Record<string, unknown>;
    expect(updatePayload).not.toHaveProperty("credentialSecretId");
    expect(JSON.stringify(updatePayload)).not.toMatch(/masked|apiKey/i);
  });

  it("creates an encrypted Secret before submitting its reference", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { id: "secret-1", configured: true } });
    const store = useModelConfigStore();

    const secret = await store.createCredentialSecret("Primary", "api-key-value");

    expect(secret).toEqual({ id: "secret-1", configured: true });
    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/secrets",
      expect.objectContaining({
        kind: "API_KEY",
        plaintext: "api-key-value",
      }),
    );
  });

  it("uses verify command and lockVersion delete paths", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: modelFixture({ status: "VERIFIED", lockVersion: 2 }) });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });
    const store = useModelConfigStore();
    store.items = [modelFixture()];
    await store.verifyModelConfig("model-1");
    await store.deleteModelConfig("model-1");
    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/model-configs/model-1:verify",
      {},
      { timeout: 180_000 },
    );
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/workspace-1/model-configs/model-1?lockVersion=2");
  });

  it("saves disclosure via set-disclosure command", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: modelFixture({
        lockVersion: 3,
        toolDisclosureUI: "binary",
        toolDisclosurePolicy: { schemaVersion: "tool-disclosure.v1", mode: "carry_all" },
      }),
    });
    const store = useModelConfigStore();
    store.items = [modelFixture()];
    await store.setDisclosurePolicy("model-1", 2, "carry_all");
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/model-configs/model-1:set-disclosure", {
      lockVersion: 2,
      toolDisclosurePolicy: { schemaVersion: "tool-disclosure.v1", mode: "carry_all" },
    });
  });
});

function modelFixture(overrides: Partial<ModelApiConfig> = {}): ModelApiConfig {
  return {
    id: "model-1",
    name: "Primary",
    provider: "openai-compatible",
    apiBase: "https://models.example/v1",
    modelName: "reasoning-model",
    credentialConfigured: true,
    options: {},
    status: "VERIFIED",
    lastLatencyMs: 12,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}
