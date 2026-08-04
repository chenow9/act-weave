import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import AppSelect from "../components/AppSelect.vue";
import { setI18nLocale } from "../i18n";
import { createTestI18n } from "../test-utils/i18n";
import type { CapabilityProvider, ProviderAsset } from "../types/domain";
import ProvidersView from "./ProvidersView.vue";

const fixture = vi.hoisted(() => ({ providers: null as any, workspaces: null as any }));

vi.mock("../stores/providers", () => ({ useProvidersStore: () => fixture.providers }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixture.workspaces }));

function createIntegrationStore() {
  const provider = providerFixture();
  const asset = assetFixture();
  const store = reactive({
    providers: [provider] as CapabilityProvider[],
    providerAssetsByProvider: {} as Record<string, ProviderAsset[]>,
    loadProviders: vi.fn(async () => store.providers),
    createProvider: vi.fn(async (draft: CapabilityProvider) => {
      const created = { ...draft, id: "provider-created", lockVersion: 1 };
      store.providers = [created, ...store.providers];
      return created;
    }),
    updateProvider: vi.fn(async (draft: CapabilityProvider) => {
      const updated = { ...draft, lockVersion: draft.lockVersion + 1 };
      store.providers = store.providers.map((item) => (item.id === updated.id ? updated : item));
      return updated;
    }),
    deleteProvider: vi.fn(async (providerId: string) => {
      store.providers = store.providers.filter((item) => item.id !== providerId);
    }),
    syncProvider: vi.fn(async () => ({
      id: "sync-1",
      status: "FAILED",
      discoveredCount: 0,
      changedCount: 0,
      errorSummary: { code: "OPENAPI_FETCH_FAILED", message: "upstream unavailable" },
    })),
    loadProviderAssets: vi.fn(async (providerId: string) => {
      store.providerAssetsByProvider[providerId] = [asset];
      return store.providerAssetsByProvider[providerId];
    }),
    materializeProviderAsset: vi.fn(async () => ({ capabilityId: "capability-1" })),
  });
  return store;
}

function mountView() {
  return mount(ProvidersView, {
    attachTo: document.body,
    global: {
      plugins: [createTestI18n("zh-CN")],
      stubs: {
        RouterLink: { template: "<a><slot /></a>" },
        teleport: true,
        // Avoid Element Plus ElSelect recursive update loops under jsdom (ZKL-64 item 10).
        AppSelect: {
          name: "AppSelect",
          props: ["modelValue", "options", "placeholder", "disabled", "ariaLabel"],
          emits: ["update:modelValue"],
          template: `
            <select
              :value="modelValue"
              :disabled="disabled"
              :aria-label="ariaLabel"
              @change="$emit('update:modelValue', ($event.target).value)"
            >
              <option v-for="opt in options || []" :key="String(opt.value)" :value="opt.value" :disabled="opt.disabled">
                {{ opt.label }}
              </option>
            </select>
          `,
        },
      },
    },
  });
}

async function setAppSelect(wrapper: ReturnType<typeof mountView>, testId: string, value: string) {
  const select = wrapper
    .findAllComponents(AppSelect)
    .find((component) => component.attributes("data-testid") === testId);
  if (!select) throw new Error(`Unable to find AppSelect ${testId}`);
  select.vm.$emit("update:modelValue", value);
  await flushPromises();
}

describe("providers management view", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    setI18nLocale("zh-CN");
    fixture.providers = createIntegrationStore();
    fixture.workspaces = reactive({
      activeWorkspaceId: "workspace-1",
      items: [{ id: "workspace-1", name: "Workspace 1" }],
      load: vi.fn(async () => undefined),
      can: vi.fn(() => true),
      roleFor: vi.fn(() => "EDITOR"),
    });
    vi.clearAllMocks();
  });

  it("renders Provider status, separate runtime and discovery addresses, and authentication summary", async () => {
    const wrapper = mountView();
    await flushPromises();

    expect(fixture.providers.loadProviders).toHaveBeenCalledTimes(1);
    expect(wrapper.text()).toContain("Orders Platform");
    expect(wrapper.text()).toContain("运行中");
    expect(wrapper.text()).toContain("https://api.example.com/v1");
    expect(wrapper.text()).toContain("https://docs.example.com/openapi.json");
    expect(wrapper.text()).toContain("Broker / OBO");
    expect(wrapper.text()).toContain("请求透传");
  });

  it("creates a schema-version 2 Provider with outbound-identity.v1 dual-mode contract", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    await wrapper.get('[data-testid="provider-name"]').setValue("Billing Platform");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("https://billing.example.com/v2/");
    await wrapper
      .get('[data-testid="provider-document-url"]')
      .setValue("https://docs.billing.example.com/openapi.json");
    await setAppSelect(wrapper, "provider-verification-method", "HEAD");
    await wrapper.get('[data-testid="provider-verification-path"]').setValue("health");
    await wrapper.get('[data-testid="provider-expected-statuses"]').setValue("200, 204");

    // Default create draft enables REQUEST_PASSTHROUGH; also enable Broker/OBO fields.
    await wrapper.get('[data-testid="provider-mode-broker"]').setValue(true);
    await flushPromises();
    await wrapper
      .get('[data-testid="provider-broker-token-endpoint"]')
      .setValue("https://broker.billing.example.com/oauth/token");
    await wrapper.get('[data-testid="provider-broker-audience"]').setValue("api://billing");
    await wrapper.get('[data-testid="provider-broker-scopes"]').setValue("orders.read inventory.read");
    await wrapper.get('[data-testid="provider-injection-header"]').setValue("X-Platform-Token");
    await wrapper.get('[data-testid="provider-injection-prefix"]').setValue("Token");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.providers.createProvider).toHaveBeenCalledTimes(1);
    const submitted = fixture.providers.createProvider.mock.calls[0][0] as CapabilityProvider;
    expect(submitted.endpointConfig).toEqual({
      schemaVersion: 2,
      serviceBaseUrl: "https://billing.example.com/v2",
      discovery: { documentUrl: "https://docs.billing.example.com/openapi.json" },
      verification: { method: "HEAD", path: "/health", expectedStatuses: [200, 204] },
    });
    expect(submitted.driverConfig.authentication).toBeUndefined();
    expect(submitted.driverConfig.outboundIdentity).toMatchObject({
      schemaVersion: "outbound-identity.v1",
      supportedModes: ["BROKER_OBO", "REQUEST_PASSTHROUGH"],
      supportedSubjectTypes: ["USER"],
      brokerObo: expect.objectContaining({
        tokenEndpoint: "https://broker.billing.example.com/oauth/token",
        audience: "api://billing",
        allowedScopes: ["orders.read", "inventory.read"],
        businessInjection: { headerName: "X-Platform-Token", prefix: "Token" },
      }),
      requestPassthrough: expect.objectContaining({
        credentialTypes: ["ACCESS_TOKEN"],
        businessInjection: { headerName: "X-Platform-Token", prefix: "Token" },
      }),
    });
    expect(wrapper.find('[data-testid="provider-name"]').exists()).toBe(false);
  });

  it("creates a runtime Provider without an online OpenAPI document", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    expect(wrapper.text()).toContain("第三方未提供在线文档时可留空");
    expect(wrapper.get('[data-testid="provider-document-url"]').attributes("required")).toBeUndefined();
    expect(
      wrapper
        .findAllComponents(AppSelect)
        .find((component) => component.attributes("data-testid") === "provider-discovery-mode")
        ?.props("modelValue"),
    ).toBe("MANUAL");

    await wrapper.get('[data-testid="provider-name"]').setValue("Private Runtime");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("https://private.example.com/api/");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.providers.createProvider).toHaveBeenCalledTimes(1);
    const submitted = fixture.providers.createProvider.mock.calls[0][0] as CapabilityProvider;
    expect(submitted.discoveryMode).toBe("MANUAL");
    expect(submitted.endpointConfig).toEqual({
      schemaVersion: 2,
      serviceBaseUrl: "https://private.example.com/api",
      verification: { method: "GET", expectedStatuses: [200, 204] },
    });
    expect(submitted.driverConfig.outboundIdentity).toMatchObject({
      schemaVersion: "outbound-identity.v1",
      supportedModes: ["REQUEST_PASSTHROUGH"],
    });
  });

  it("marks discovery as optional and disables sync when no document is configured", async () => {
    const provider = providerFixture();
    provider.id = "provider-without-document";
    provider.name = "Private Runtime";
    provider.discoveryMode = "MANUAL";
    provider.endpointConfig = {
      schemaVersion: 2,
      serviceBaseUrl: "https://private.example.com/api",
      verification: { method: "GET", path: "/health", expectedStatuses: [200] },
    };
    fixture.providers.providers = [provider];

    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.text()).toContain("未配置（不启用自动发现）");
    await wrapper.get('[aria-label="Private Runtime 更多操作"]').trigger("click");
    const syncButton = wrapper.get('[data-action-key="sync"]');
    expect(syncButton.attributes("disabled")).toBeDefined();
    expect(syncButton.attributes("title")).toContain("不影响 Connection 和运行调用");
    await syncButton.trigger("click");
    expect(fixture.providers.syncProvider).not.toHaveBeenCalled();
  });

  it("hydrates and updates an existing Provider without losing its identity", async () => {
    fixture.providers.providers[0].endpointConfig.headers = { "X-Platform-Version": "2026-07" };
    fixture.providers.providers[0].endpointConfig.egress = {
      allowedHosts: ["api.example.com"],
      allowedPorts: [443],
      allowedCIDRs: ["10.20.0.0/16"],
      maxRedirects: 1,
    };
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="edit"]').trigger("click");
    expect((wrapper.get('[data-testid="provider-service-base-url"]').element as HTMLInputElement).value).toBe(
      "https://api.example.com/v1",
    );
    expect((wrapper.get('[data-testid="provider-broker-token-endpoint"]').element as HTMLInputElement).value).toBe(
      "https://broker.example.com/oauth/token",
    );
    expect((wrapper.get('[data-testid="provider-broker-audience"]').element as HTMLInputElement).value).toBe(
      "api://orders",
    );
    await wrapper.get('[data-testid="provider-name"]').setValue("Orders Platform v2");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.providers.updateProvider).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "provider-1",
        name: "Orders Platform v2",
        lockVersion: 3,
        endpointConfig: expect.objectContaining({
          headers: { "X-Platform-Version": "2026-07" },
          egress: {
            allowedHosts: ["api.example.com"],
            allowedPorts: [443],
            allowedCIDRs: ["10.20.0.0/16"],
            maxRedirects: 1,
          },
        }),
        driverConfig: expect.objectContaining({
          outboundIdentity: expect.objectContaining({
            schemaVersion: "outbound-identity.v1",
            supportedModes: expect.arrayContaining(["BROKER_OBO", "REQUEST_PASSTHROUGH"]),
          }),
        }),
      }),
    );
  });

  it("requires an explicit CIDR grant for a private literal runtime address", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    await wrapper.get('[data-testid="provider-name"]').setValue("Private Orders");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("http://192.168.10.62:8000");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();
    expect(fixture.providers.createProvider).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("192.168.10.62/32");

    await wrapper.get('[data-testid="provider-allowed-cidrs"]').setValue("192.168.10.0/24");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();
    expect(fixture.providers.createProvider).toHaveBeenCalledTimes(1);
    expect(fixture.providers.createProvider.mock.calls[0][0].endpointConfig).toMatchObject({
      egress: { allowedCIDRs: ["192.168.10.0/24"] },
    });
  });

  it("edits a legacy Provider and hard-cuts authentication to outbound-identity.v1", async () => {
    const legacy = providerFixture();
    legacy.id = "legacy-provider";
    legacy.name = "Legacy API";
    legacy.driverConfig = {
      adapterOption: "preserve-me",
      authentication: { version: "service-auth.v1", defaultSchemeKey: "oauth2-client", schemes: [] },
    };
    fixture.providers.providers = [legacy];
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[aria-label="Legacy API 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="edit"]').trigger("click");
    expect(wrapper.text()).toContain("outbound-identity.v1");
    await wrapper.get('[data-testid="provider-name"]').setValue("Legacy API renamed");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    const updated = fixture.providers.updateProvider.mock.calls[0][0] as CapabilityProvider;
    expect(updated.name).toBe("Legacy API renamed");
    expect(updated.driverConfig.adapterOption).toBe("preserve-me");
    expect(updated.driverConfig.authentication).toBeUndefined();
    expect(updated.driverConfig.outboundIdentity).toMatchObject({
      schemaVersion: "outbound-identity.v1",
      supportedModes: expect.arrayContaining(["REQUEST_PASSTHROUGH"]),
    });
  });

  it("shows a failed sync result instead of reporting success", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="assets"]').trigger("click");
    await flushPromises();
    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="sync"]').trigger("click");
    await flushPromises();

    expect(wrapper.get('[data-testid="provider-sync-result-provider-1"]').text()).toContain("最近一次同步失败");
    expect(wrapper.get('[data-testid="provider-sync-result-provider-1"]').text()).toContain("OPENAPI_FETCH_FAILED");
    expect(wrapper.text()).not.toContain("Orders Platform 同步完成");
  });

  it("expands assets, materializes an endpoint, and deletes a Provider with confirmation", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="assets"]').trigger("click");
    await flushPromises();
    expect(fixture.providers.loadProviderAssets).toHaveBeenCalledWith("provider-1");
    expect(wrapper.text()).toContain("Get order");
    await wrapper.get('[data-testid="provider-materialize-asset-1"]').trigger("click");
    await flushPromises();
    expect(fixture.providers.materializeProviderAsset).toHaveBeenCalledWith("provider-1", "asset-1");

    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="delete"]').trigger("click");
    await wrapper.get('[data-testid="provider-delete-confirm-input"]').setValue("Orders Platform");
    await wrapper.get('[data-testid="provider-delete-confirm"]').trigger("click");
    await flushPromises();
    expect(fixture.providers.deleteProvider).toHaveBeenCalledWith("provider-1");
    expect(wrapper.find('[data-testid="provider-delete-confirm-input"]').exists()).toBe(false);
  });

  it("shows fixed short menu labels for long Provider names while aria keeps full names and row identity", async () => {
    const longName = "超长中文服务提供者名称加EnglishSuffix-ABCDEFGHIJKLMNOP";
    fixture.providers.providers = [
      { ...providerFixture(), id: "provider-long-a", name: longName },
      { ...providerFixture(), id: "provider-long-b", name: longName },
    ];
    const wrapper = mountView();
    await flushPromises();

    const triggers = wrapper.findAll(`[aria-label="${longName} 更多操作"]`);
    expect(triggers).toHaveLength(2);

    await triggers[0].trigger("click");
    await flushPromises();
    let menu = document.body.querySelector<HTMLElement>(`[role="menu"][aria-label="${longName} 更多操作"]`);
    expect(menu).not.toBeNull();
    const visible = Array.from(menu!.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).map(
      (item) => item.querySelector("span")?.textContent,
    );
    expect(visible).toEqual(["编辑", "同步", "查看能力资产", "删除"]);
    const assets = menu!.querySelector<HTMLButtonElement>('button[data-action-key="assets"]');
    expect(assets?.getAttribute("aria-label")).toBe(`查看 ${longName} 的能力资产`);
    expect(assets?.getAttribute("title")).toBe(`查看 ${longName} 的能力资产`);
    const deleteItem = menu!.querySelector<HTMLButtonElement>('button[data-action-key="delete"]');
    expect(deleteItem?.className).toContain("tone-danger");

    // Close first menu, open second duplicate-name row and ensure edit binds second id.
    document.body.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    await flushPromises();
    await triggers[1].trigger("click");
    await flushPromises();
    menu = document.body.querySelector<HTMLElement>(`[role="menu"][aria-label="${longName} 更多操作"]`);
    menu!.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await flushPromises();
    expect((wrapper.get('[data-testid="provider-name"]').element as HTMLInputElement).value).toBe(longName);
    // Saving should call update with the second provider id.
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();
    expect(fixture.providers.updateProvider).toHaveBeenCalledWith(
      expect.objectContaining({ id: "provider-long-b", name: longName }),
    );
  });

  it("shows dual-support badges and layered identity copy without Connection multi-strategy wording", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    await flushPromises();

    expect(wrapper.get('[data-testid="provider-outbound-identity"]').text()).toContain("用户调用身份");
    expect(wrapper.get('[data-testid="provider-outbound-identity"]').text()).toContain("可多选");
    expect(wrapper.get('[data-testid="provider-outbound-identity"]').text()).toContain("只能选择一种");
    expect(wrapper.text()).toContain("平台按当前用户身份换取短期业务 Token");
    expect(wrapper.text()).toContain("调用方为本次请求提供 Token");

    // Default create enables passthrough; enable broker too → both 已支持.
    await wrapper.get('[data-testid="provider-mode-broker"]').setValue(true);
    await flushPromises();
    const badges = wrapper.findAll(".provider-identity-badge").map((node) => node.text());
    expect(badges).toEqual(["已支持", "已支持"]);
    expect(wrapper.find('[data-testid="provider-broker-fields"]').exists()).toBe(true);

    await wrapper.get('[data-testid="provider-mode-broker"]').setValue(false);
    await flushPromises();
    expect(wrapper.find('[data-testid="provider-broker-fields"]').exists()).toBe(false);
    // Unchecking broker must not clear passthrough.
    expect((wrapper.get('[data-testid="provider-mode-passthrough"]').element as HTMLInputElement).checked).toBe(true);

    const tech = wrapper.get('[data-testid="provider-identity-tech-details"]');
    expect(tech.text()).toContain("查看技术约束");
    expect(tech.text()).toContain("USER");
    expect(tech.text()).toContain("private_key_jwt");
    expect(tech.text()).toContain("ACCESS_TOKEN");
    expect(tech.text()).toContain("expiresAt");
  });

  it("blocks zero identity modes with block alert and never calls store/API or silent passthrough", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    await flushPromises();

    await wrapper.get('[data-testid="provider-name"]').setValue("No Mode Provider");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("https://api.example.com/v1");
    await wrapper.get('[data-testid="provider-mode-passthrough"]').setValue(false);
    await wrapper.get('[data-testid="provider-mode-broker"]').setValue(false);
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(wrapper.get('[data-testid="provider-identity-mode-error"]').text()).toBe("至少选择一种");
    expect(wrapper.get('[data-testid="provider-identity-mode-error"]').attributes("role")).toBe("alert");
    expect(fixture.providers.createProvider).not.toHaveBeenCalled();
    expect(fixture.providers.updateProvider).not.toHaveBeenCalled();
  });
});

function outboundIdentityFixture(): Record<string, unknown> {
  return {
    schemaVersion: "outbound-identity.v1",
    supportedModes: ["BROKER_OBO", "REQUEST_PASSTHROUGH"],
    supportedSubjectTypes: ["USER"],
    brokerObo: {
      tokenEndpoint: "https://broker.example.com/oauth/token",
      audience: "api://orders",
      grantType: "urn:ietf:params:oauth:grant-type:token-exchange",
      allowedScopes: ["orders.read"],
      businessInjection: { headerName: "Authorization", prefix: "Bearer" },
    },
    requestPassthrough: {
      credentialTypes: ["ACCESS_TOKEN"],
      businessInjection: { headerName: "Authorization", prefix: "Bearer" },
    },
  };
}

function providerFixture(): CapabilityProvider {
  return {
    id: "provider-1",
    name: "Orders Platform",
    kind: "HTTP_OPENAPI",
    driverKey: "http_openapi",
    transport: "HTTP",
    endpointConfig: {
      schemaVersion: 2,
      serviceBaseUrl: "https://api.example.com/v1",
      discovery: { documentUrl: "https://docs.example.com/openapi.json" },
      verification: { method: "GET", path: "/health", expectedStatuses: [200, 204] },
    },
    driverConfig: { outboundIdentity: outboundIdentityFixture() },
    discoveryMode: "ON_DEMAND",
    status: "ACTIVE",
    lastSyncedAt: "2026-07-16T02:00:00Z",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 3,
  };
}

function assetFixture(): ProviderAsset {
  return {
    id: "asset-1",
    kind: "ENDPOINT",
    externalId: "orders.get",
    name: "Get order",
    description: "Read one order",
    inputSchema: {},
    outputSchema: {},
    metadata: { method: "GET", path: "/orders/{id}" },
    sourceChecksum: "a".repeat(64),
    status: "ACTIVE",
  };
}
