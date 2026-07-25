import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import AppSelect from "../components/AppSelect.vue";
import type { CapabilityProvider, ProviderAsset } from "../types/domain";
import { defaultOAuthContract } from "../utils/provider-auth";
import ProvidersView from "./ProvidersView.vue";

const fixture = vi.hoisted(() => ({ integration: null as any, workspaces: null as any }));

vi.mock("../stores/integration", () => ({ useIntegrationStore: () => fixture.integration }));
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
      store.providers = store.providers.map((item) => item.id === updated.id ? updated : item);
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
      stubs: {
        RouterLink: { template: "<a><slot /></a>" },
        teleport: true,
      },
    },
  });
}

async function setAppSelect(wrapper: ReturnType<typeof mountView>, testId: string, value: string) {
  const select = wrapper.findAllComponents(AppSelect).find((component) => component.attributes("data-testid") === testId);
  if (!select) throw new Error(`Unable to find AppSelect ${testId}`);
  select.vm.$emit("update:modelValue", value);
  await flushPromises();
}

describe("providers management view", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    fixture.integration = createIntegrationStore();
    fixture.workspaces = reactive({
      activeWorkspaceId: "workspace-1",
      items: [{ id: "workspace-1", name: "Workspace 1" }],
      load: vi.fn(async () => undefined),
    });
    vi.clearAllMocks();
  });

  it("renders Provider status, separate runtime and discovery addresses, and authentication summary", async () => {
    const wrapper = mountView();
    await flushPromises();

    expect(fixture.integration.loadProviders).toHaveBeenCalledTimes(1);
    expect(wrapper.text()).toContain("Orders Platform");
    expect(wrapper.text()).toContain("运行中");
    expect(wrapper.text()).toContain("https://api.example.com/v1");
    expect(wrapper.text()).toContain("https://docs.example.com/openapi.json");
    expect(wrapper.text()).toContain("Platform OAuth2");
  });

  it("creates a schema-version 2 Provider with a schema-driven OAuth2 contract", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    await wrapper.get('[data-testid="provider-name"]').setValue("Billing Platform");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("https://billing.example.com/v2/");
    await wrapper.get('[data-testid="provider-document-url"]').setValue("https://docs.billing.example.com/openapi.json");
    await setAppSelect(wrapper, "provider-verification-method", "HEAD");
    await wrapper.get('[data-testid="provider-verification-path"]').setValue("health");
    await wrapper.get('[data-testid="provider-expected-statuses"]').setValue("200, 204");
    await wrapper.get('[data-testid="provider-auth-oauth"]').setValue(true);
    await wrapper.get('[data-testid="provider-scheme-name"]').setValue("Billing OAuth2");
    await wrapper.get('[data-testid="provider-token-url-template"]').setValue("https://login.billing.example.com/{{tenantId}}/token");
    await setAppSelect(wrapper, "provider-client-auth-method", "client_secret_post");
    await wrapper.get('[data-testid="provider-add-auth-field"]').trigger("click");
    await wrapper.get('[data-testid="provider-extra-field-key-0"]').setValue("tenantId");
    await wrapper.get('[data-testid="provider-extra-field-label-0"]').setValue("Tenant ID");
    await wrapper.get('[data-testid="provider-add-token-parameter"]').trigger("click");
    await wrapper.get('[data-testid="provider-token-parameter-name-0"]').setValue("audience");
    await setAppSelect(wrapper, "provider-token-parameter-field-0", "tenantId");
    await wrapper.get('[data-testid="provider-access-token-path"]').setValue("data.access_token");
    await wrapper.get('[data-testid="provider-injection-header"]').setValue("X-Platform-Token");
    await wrapper.get('[data-testid="provider-injection-prefix"]').setValue("Token");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.integration.createProvider).toHaveBeenCalledTimes(1);
    const submitted = fixture.integration.createProvider.mock.calls[0][0] as CapabilityProvider;
    expect(submitted.endpointConfig).toEqual({
      schemaVersion: 2,
      serviceBaseUrl: "https://billing.example.com/v2",
      discovery: { documentUrl: "https://docs.billing.example.com/openapi.json" },
      verification: { method: "HEAD", path: "/health", expectedStatuses: [200, 204] },
    });
    expect(submitted.driverConfig.authentication).toMatchObject({
      version: "service-auth.v1",
      defaultSchemeKey: "oauth2-client",
      schemes: [{
        type: "OAUTH2_CLIENT",
        displayName: "Billing OAuth2",
        fields: expect.arrayContaining([
          expect.objectContaining({ key: "clientSecret", kind: "SECRET" }),
          expect.objectContaining({ key: "tenantId", kind: "TEXT" }),
        ]),
        oauth2: expect.objectContaining({
          tokenUrlTemplate: "https://login.billing.example.com/{{tenantId}}/token",
          clientAuthMethod: "client_secret_post",
          tokenParameters: [{ name: "audience", field: "tenantId" }],
          response: expect.objectContaining({ accessTokenPath: "data.access_token" }),
          injection: { headerName: "X-Platform-Token", prefix: "Token" },
        }),
      }],
    });
    expect(wrapper.find('[data-testid="provider-name"]').exists()).toBe(false);
  });

  it("creates a runtime Provider without an online OpenAPI document", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    expect(wrapper.text()).toContain("第三方未提供在线文档时可留空");
    expect(wrapper.get('[data-testid="provider-document-url"]').attributes("required")).toBeUndefined();
    expect(wrapper.findAllComponents(AppSelect).find((component) => component.attributes("data-testid") === "provider-discovery-mode")?.props("modelValue")).toBe("MANUAL");

    await wrapper.get('[data-testid="provider-name"]').setValue("Private Runtime");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("https://private.example.com/api/");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.integration.createProvider).toHaveBeenCalledTimes(1);
    const submitted = fixture.integration.createProvider.mock.calls[0][0] as CapabilityProvider;
    expect(submitted.discoveryMode).toBe("MANUAL");
    expect(submitted.endpointConfig).toEqual({
      schemaVersion: 2,
      serviceBaseUrl: "https://private.example.com/api",
      verification: { method: "GET", expectedStatuses: [200, 204] },
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
    fixture.integration.providers = [provider];

    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.text()).toContain("未配置（不启用自动发现）");
    await wrapper.get('[aria-label="Private Runtime 更多操作"]').trigger("click");
    const syncButton = wrapper.get('[data-action-key="sync"]');
    expect(syncButton.attributes("disabled")).toBeDefined();
    expect(syncButton.attributes("title")).toContain("不影响 Connection 和运行调用");
    await syncButton.trigger("click");
    expect(fixture.integration.syncProvider).not.toHaveBeenCalled();
  });

  it("hydrates and updates an existing Provider without losing its identity", async () => {
    fixture.integration.providers[0].endpointConfig.headers = { "X-Platform-Version": "2026-07" };
    fixture.integration.providers[0].endpointConfig.egress = {
      allowedHosts: ["api.example.com"], allowedPorts: [443], allowedCIDRs: ["10.20.0.0/16"], maxRedirects: 1,
    };
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="edit"]').trigger("click");
    expect((wrapper.get('[data-testid="provider-service-base-url"]').element as HTMLInputElement).value).toBe("https://api.example.com/v1");
    expect((wrapper.get('[data-testid="provider-token-url-template"]').element as HTMLInputElement).value).toBe("https://login.example.com/{{tenantId}}/token");
    await wrapper.get('[data-testid="provider-name"]').setValue("Orders Platform v2");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.integration.updateProvider).toHaveBeenCalledWith(expect.objectContaining({
      id: "provider-1",
      name: "Orders Platform v2",
      lockVersion: 3,
      endpointConfig: expect.objectContaining({
        headers: { "X-Platform-Version": "2026-07" },
        egress: {
          allowedHosts: ["api.example.com"], allowedPorts: [443], allowedCIDRs: ["10.20.0.0/16"], maxRedirects: 1,
        },
      }),
    }));
  });

  it("requires an explicit CIDR grant for a private literal runtime address", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-testid="provider-create"]').trigger("click");
    await wrapper.get('[data-testid="provider-name"]').setValue("Private Orders");
    await wrapper.get('[data-testid="provider-service-base-url"]').setValue("http://192.168.10.62:8000");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();
    expect(fixture.integration.createProvider).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("192.168.10.62/32");

    await wrapper.get('[data-testid="provider-allowed-cidrs"]').setValue("192.168.10.0/24");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();
    expect(fixture.integration.createProvider).toHaveBeenCalledTimes(1);
    expect(fixture.integration.createProvider.mock.calls[0][0].endpointConfig).toMatchObject({
      egress: { allowedCIDRs: ["192.168.10.0/24"] },
    });
  });

  it("edits a legacy Provider without forcing an authentication migration", async () => {
    const legacy = providerFixture();
    legacy.id = "legacy-provider";
    legacy.name = "Legacy API";
    legacy.driverConfig = { adapterOption: "preserve-me" };
    fixture.integration.providers = [legacy];
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[aria-label="Legacy API 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="edit"]').trigger("click");
    expect(wrapper.text()).toContain("原样保留旧认证");
    await wrapper.get('[data-testid="provider-name"]').setValue("Legacy API renamed");
    await wrapper.get('[data-testid="provider-save"]').trigger("submit");
    await flushPromises();

    expect(fixture.integration.updateProvider).toHaveBeenCalledWith(expect.objectContaining({
      name: "Legacy API renamed",
      driverConfig: { adapterOption: "preserve-me" },
    }));
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
    expect(fixture.integration.loadProviderAssets).toHaveBeenCalledWith("provider-1");
    expect(wrapper.text()).toContain("Get order");
    await wrapper.get('[data-testid="provider-materialize-asset-1"]').trigger("click");
    await flushPromises();
    expect(fixture.integration.materializeProviderAsset).toHaveBeenCalledWith("provider-1", "asset-1");

    await wrapper.get('[aria-label="Orders Platform 更多操作"]').trigger("click");
    await wrapper.get('[data-action-key="delete"]').trigger("click");
    await wrapper.get('[data-testid="provider-delete-confirm-input"]').setValue("Orders Platform");
    await wrapper.get('[data-testid="provider-delete-confirm"]').trigger("click");
    await flushPromises();
    expect(fixture.integration.deleteProvider).toHaveBeenCalledWith("provider-1");
    expect(wrapper.find('[data-testid="provider-delete-confirm-input"]').exists()).toBe(false);
  });
});

function providerFixture(): CapabilityProvider {
  const authentication = defaultOAuthContract("https://login.example.com/{{tenantId}}/token");
  authentication.schemes[0].displayName = "Platform OAuth2";
  authentication.schemes[0].fields.splice(0, 0, { key: "tenantId", label: "Tenant ID", kind: "TEXT", required: true });
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
    driverConfig: { authentication },
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
