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
    createModelConfig: vi.fn(async (draft: ModelApiConfig) => ({
      ...draft,
      id: "model-created",
      credentialConfigured: true,
      credentialSecretId: undefined,
      lockVersion: 1,
    })),
    updateModelConfig: vi.fn(async (_id: string, draft: ModelApiConfig) => ({
      ...draft,
      credentialSecretId: undefined,
      lockVersion: draft.lockVersion + 1,
    })),
    verifyModelConfig: vi.fn(async () => ({ ...model, status: "VERIFIED", lastLatencyMs: 24, lockVersion: 2 })),
    deleteModelConfig: vi.fn(async () => undefined),
    setDisclosurePolicy: vi.fn(async () => ({ ...model, toolDisclosureUI: "binary" })),
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
      can: () => true,
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
      expect.objectContaining({ credentialSecretId: secretID, provider: "openai-compatible" }),
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
    await wrapper.get('button[aria-label="编辑"]').trigger("click");
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
    fixture.store.verifyModelConfig = vi.fn(async () => ({
      ...modelFixture({
        status: "VERIFIED",
        lastLatencyMs: 24,
        lockVersion: 2,
        toolDisclosureUI: "hidden",
        agenticCapabilities: { schemaVersion: "agentic-model.v1", toolSearchModes: ["client"] },
      }),
    }));
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="测试"]').trigger("click");
    await flushPromises();
    expect(fixture.store.verifyModelConfig).toHaveBeenCalledWith("model-1");
    expect(fixture.store.loadModelConfigs).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("已验证，按需加载已启用");
  });

  it("shows capability badges from toolDisclosureUI and never from modelName", async () => {
    fixture.store.items = [
      modelFixture({
        id: "model-native",
        name: "Native",
        modelName: "gpt-3.5-turbo",
        status: "VERIFIED",
        toolDisclosureUI: "hidden",
      }),
      modelFixture({
        id: "model-fc",
        name: "Function",
        modelName: "gpt-5.4",
        status: "VERIFIED",
        toolDisclosureUI: "binary",
      }),
      modelFixture({
        id: "model-none",
        name: "None",
        modelName: "o4-mini",
        status: "VERIFIED",
        toolDisclosureUI: "unavailable",
      }),
      modelFixture({
        id: "model-open",
        name: "Open",
        modelName: "gpt-5.4",
        status: "UNVERIFIED",
      }),
    ];
    fixture.store.pagination = { ...fixture.store.pagination, total: 4 };
    const wrapper = mountView();
    await flushPromises();
    const badges = wrapper.findAll("[data-capability-badge]").map((badge) => badge.text());
    expect(badges).toEqual(["原生按需", "函数调用", "无工具", "未验证"]);
    expect(wrapper.find('input[type="radio"]').exists()).toBe(false);
    expect(wrapper.text()).not.toContain("set-disclosure");
    expect(wrapper.html()).not.toMatch(/set-disclosure|setDisclosure/i);
  });

  it("uses function-calling verify copy when the probe grades FC", async () => {
    fixture.store.verifyModelConfig = vi.fn(async () =>
      modelFixture({
        status: "VERIFIED",
        lastLatencyMs: 40,
        lockVersion: 2,
        toolDisclosureUI: "binary",
        agenticCapabilities: { schemaVersion: "agentic-model.v2", toolCalling: "function_calling" },
      }),
    );
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="测试"]').trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("已验证，可用按需检索或全量携带工具");
    expect(wrapper.text()).not.toContain("按需加载已启用");
    expect(wrapper.text()).not.toContain("要等平台检索");
  });

  it("sends the canonical provider on the wire while showing the human label", async () => {
    const wrapper = mountView();
    await flushPromises();

    // The rendered surfaces are labels, so the pretty spelling must appear there
    // and the canonical wire value must not.
    const pill = wrapper.get(".model-provider-pill");
    expect(pill.text()).toBe("OpenAI Compatible");
    expect(pill.text()).not.toContain("openai-compatible");

    await wrapper.get(".primary-button").trigger("click");
    const providerInput = wrapper
      .findAll("input[disabled]")
      .find((input) => (input.element as HTMLInputElement).value === "OpenAI Compatible");
    expect(providerInput).toBeDefined();

    await wrapper.get('input[placeholder="粘贴 API Key"]').setValue("api-key-value");
    await wrapper.get('[data-action="save-model-config"]').trigger("click");
    await flushPromises();

    const createdDraft = fixture.store.createModelConfig.mock.calls[0][0];
    expect(createdDraft.provider).toBe("openai-compatible");

    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="编辑"]').trigger("click");
    await wrapper.get('[data-action="save-model-config"]').trigger("click");
    await flushPromises();

    const updatedDraft = fixture.store.updateModelConfig.mock.calls[0][1];
    expect(updatedDraft.provider).toBe("openai-compatible");
  });

  it("shows writable disclosure radios for function-calling models", async () => {
    fixture.store.items = [
      modelFixture({
        status: "VERIFIED",
        toolDisclosureUI: "binary",
        toolDisclosurePolicy: { schemaVersion: "tool-disclosure.v1", mode: "platform_on_demand" },
      }),
    ];
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="编辑"]').trigger("click");
    await flushPromises();
    expect(wrapper.get("[data-testid=model-disclosure]").text()).toContain("按需检索");
    expect(wrapper.get("[data-testid=model-disclosure]").text()).toContain("全量携带");
    expect(wrapper.text()).not.toContain("Classic");
    await wrapper.get('[data-action="set-disclosure"]').trigger("click");
    await flushPromises();
    expect(fixture.store.setDisclosurePolicy).toHaveBeenCalledWith("model-1", 1, "platform_on_demand");
  });

  it("does not mount an empty disclosure heading for unverified models", async () => {
    fixture.store.items = [modelFixture({ status: "UNVERIFIED", toolDisclosureUI: "unverified" })];
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="编辑"]').trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=model-disclosure]").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("工具披露");
  });

  it("filters with v1 status values", async () => {
    const wrapper = mountView();
    await flushPromises();
    const statusButton = wrapper.findAll(".model-status-filter button").find((item) => item.text().includes("未验证"));
    await statusButton!.trigger("click");
    await flushPromises();
    expect(fixture.store.loadModelConfigs).toHaveBeenLastCalledWith({
      query: "",
      status: "UNVERIFIED",
      page: 1,
      pageSize: 10,
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
    status: "UNVERIFIED",
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}
