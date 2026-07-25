import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ModelApiConfig } from "../types/domain";
import ModelAPIConfigsView from "./ModelAPIConfigsView.vue";

const secretID = "019f68d9-d405-7032-9b21-542a7bf46d22";
const fixture = vi.hoisted(() => ({ store: null as any, workspaces: null as any }));
vi.mock("../stores/modelConfigs", () => ({ useModelConfigStore: () => fixture.store }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixture.workspaces }));

function createStore() {
  const model = modelFixture();
  const store = reactive({
    items: [model],
    selectedConfigId: "",
    loading: false,
    error: null as string | null,
    hasLoaded: true,
    pagination: { page: 1, pageSize: 10, total: 1, pageSizeOptions: [10, 20, 50] },
    listQuery: { query: "", page: 1, pageSize: 10 },
    loadModelConfigs: vi.fn(async () => store.items),
    createCredentialSecret: vi.fn(async () => ({ id: secretID, configured: true })),
    createModelConfig: vi.fn(async (draft: ModelApiConfig) => ({ ...draft, id: "model-created", credentialConfigured: true, credentialSecretId: undefined, lockVersion: 1 })),
    updateModelConfig: vi.fn(async (_id: string, draft: ModelApiConfig) => ({ ...draft, credentialSecretId: undefined, lockVersion: draft.lockVersion + 1 })),
    verifyModelConfig: vi.fn(async () => ({ ...model, status: "VERIFIED", lastLatencyMs: 24, lockVersion: 2 })),
    deleteModelConfig: vi.fn(async () => undefined),
  });
  return store;
}

function mountView() {
  return mount(ModelAPIConfigsView, {
    attachTo: document.body,
    global: { directives: { loading: () => undefined }, stubs: { teleport: true } },
  });
}

describe("model config v1 behavior", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    fixture.store = createStore();
    fixture.workspaces = reactive({
      activeWorkspaceId: "workspace-1",
      items: [{ id: "workspace-1", name: "Workspace 1" }],
      load: vi.fn(async () => undefined),
    });
    vi.clearAllMocks();
  });

  it("shows only configured state and never exposes a masked or revealable secret value", async () => {
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.get(".model-credential-state").text()).toContain("已配置");
    expect(wrapper.html()).not.toMatch(/apiKeyMasked|显示 API Key|隐藏 API Key|sk-/);
  });

  it("creates a Secret from an API key and submits only its reference", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".primary-button").trigger("click");
    await wrapper.get('input[placeholder="粘贴 API Key"]').setValue("api-key-value");
    await wrapper.get('[data-action="save-model-config"]').trigger("click");
    await flushPromises();
    expect(fixture.store.createModelConfig).toHaveBeenCalledWith(
      expect.objectContaining({ credentialSecretId: secretID, provider: "OpenAI Compatible" }),
    );
    expect(fixture.store.createCredentialSecret).toHaveBeenCalledWith(expect.any(String), "api-key-value");
  });

  it("requires an API key when creating a model configuration", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".primary-button").trigger("click");
    await wrapper.get('[data-action="save-model-config"]').trigger("click");
    await flushPromises();

    expect(fixture.store.createModelConfig).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("请输入 API Key");
  });

  it("leaves the replacement Secret ID blank when editing an already configured model", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="编辑模型配置"]').trigger("click");
    const secretInput = wrapper.get('input[placeholder="留空以保留现有凭据"]');
    expect((secretInput.element as HTMLInputElement).value).toBe("");
    await wrapper.get('[data-action="save-model-config"]').trigger("click");
    await flushPromises();
    expect(fixture.store.updateModelConfig).toHaveBeenCalledWith(
      "model-1",
      expect.objectContaining({ credentialSecretId: "" }),
    );
  });

  it("verifies only the persisted v1 resource and reloads its active page", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="测试模型 API 连接"]').trigger("click");
    await flushPromises();
    expect(fixture.store.verifyModelConfig).toHaveBeenCalledWith("model-1");
    expect(fixture.store.loadModelConfigs).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("验证通过");
  });

  it("filters with v1 status values", async () => {
    const wrapper = mountView();
    await flushPromises();
    const statusButton = wrapper.findAll(".model-status-filter button").find((item) => item.text().includes("未验证"));
    await statusButton!.trigger("click");
    await flushPromises();
    expect(fixture.store.loadModelConfigs).toHaveBeenLastCalledWith({ query: "", status: "UNVERIFIED", page: 1, pageSize: 10 });
  });
});

function modelFixture(overrides: Partial<ModelApiConfig> = {}): ModelApiConfig {
  return {
    id: "model-1",
    name: "Primary",
    provider: "OpenAI Compatible",
    apiBase: "https://models.example/v1",
    modelName: "reasoning-model",
    credentialConfigured: true,
    options: {},
    status: "UNVERIFIED",
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}
