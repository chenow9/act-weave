import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CapabilityProvider, ProviderAsset, ServiceConnection, ServiceConnectionVerification } from "../types/domain";
import AppSelect from "../components/AppSelect.vue";
import { defaultOAuthContract } from "../utils/provider-auth";
import ServiceConnectionsView from "./ServiceConnectionsView.vue";

const secretID = "019f68d9-d405-7032-9b21-542a7bf46d22";
const fixture = vi.hoisted(() => ({ store: null as any, workspaces: null as any, router: { push: vi.fn() } }));

vi.mock("../stores/integration", () => ({ useIntegrationStore: () => fixture.store }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixture.workspaces }));
vi.mock("vue-router", () => ({ useRouter: () => fixture.router }));

function createStore() {
  const provider = providerFixture();
  const connection = connectionFixture();
  const store = reactive({
    providers: [provider],
    providerAssetsByProvider: {} as Record<string, ProviderAsset[]>,
    serviceConnectionPageItems: [connection],
    serviceConnectionPagination: { page: 1, pageSize: 10, total: 1, pageSizeOptions: [10, 20, 50] },
    serviceConnectionListQuery: { query: "", page: 1, pageSize: 10 },
    serviceConnectionCatalog: [connection],
    serviceConnectionRegistryTotal: 1,
    verificationByConnectionId: {} as Record<string, ServiceConnectionVerification>,
    get serviceConnections() {
      return store.serviceConnectionCatalog;
    },
    loadProviders: vi.fn(async () => store.providers),
    loadServiceConnectionPage: vi.fn(async () => store.serviceConnectionPageItems),
    loadServiceConnectionCatalog: vi.fn(async () => store.serviceConnectionCatalog),
    createCredentialSecret: vi.fn(async () => ({ id: secretID })),
    createServiceConnection: vi.fn(async (draft: ServiceConnection) => ({ ...connection, ...draft, id: "connection-created", lockVersion: 1 })),
    updateServiceConnection: vi.fn(async (_id: string, draft: ServiceConnection) => ({ ...draft, lockVersion: draft.lockVersion + 1 })),
    deleteServiceConnection: vi.fn(async () => undefined),
    verifyConnection: vi.fn(async () => verificationFixture()),
    createProvider: vi.fn(async (draft: CapabilityProvider) => ({ ...draft, id: "provider-created", lockVersion: 1 })),
    updateProvider: vi.fn(async (draft: CapabilityProvider) => ({ ...draft, lockVersion: draft.lockVersion + 1 })),
    deleteProvider: vi.fn(async () => undefined),
    syncProvider: vi.fn(async () => ({ id: "sync-1", status: "SUCCEEDED", discoveredCount: 1, changedCount: 1, errorSummary: {} })),
    loadProviderAssets: vi.fn(async (providerId: string) => {
      store.providerAssetsByProvider[providerId] = [assetFixture()];
      return store.providerAssetsByProvider[providerId];
    }),
    materializeProviderAsset: vi.fn(async () => ({ capabilityId: "cap-1" })),
  });
  return store;
}

function mountView() {
  return mount(ServiceConnectionsView, {
    attachTo: document.body,
    global: { directives: { loading: () => undefined }, stubs: { teleport: true } },
  });
}

describe("service connections v1 behavior", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    window.history.pushState({}, "", "/connections");
    fixture.store = createStore();
    fixture.workspaces = reactive({
      activeWorkspaceId: "workspace-1",
      items: [{ id: "workspace-1", name: "Workspace 1" }],
      load: vi.fn(async () => undefined),
    });
    vi.clearAllMocks();
  });

  it("renders Connection fields from the selected Provider authentication contract", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    expect(wrapper.text()).toContain("服务 API（Capability Provider）");
    expect(wrapper.findAll("input").some((input) => (input.element as HTMLInputElement).value === "https://orders.example")).toBe(true);
    expect(wrapper.html()).not.toContain("https://orders.example/openapi.json");
    expect(wrapper.findAll("input").some((input) => (input.element as HTMLInputElement).value === "OAuth2 Client Credentials")).toBe(true);
    expect(wrapper.text()).toContain("Token Endpoint（Provider）");
    expect(wrapper.find('input[type="password"]').exists()).toBe(true);
    expect(wrapper.html()).not.toMatch(/apiKeyValue|apiSecretValue|fixedToken|Token 值/);
  });

  it("routes Provider management to the dedicated registry view", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get(".connection-header-actions .ghost-button").trigger("click");
    expect(fixture.router.push).toHaveBeenCalledWith("/providers");
  });

  it("creates an OAuth2 connection with a Secret reference instead of raw credentials", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    await wrapper.get('input[placeholder="例如：昆仑平台"]').setValue("Billing production");
    await wrapper.get('input[placeholder="客户端标识"]').setValue("billing-client");
    await wrapper.get('input[type="password"]').setValue("billing-secret");
    await wrapper.get('[data-testid="connection-save-draft"]').trigger("click");
    await flushPromises();

    expect(fixture.store.createServiceConnection).toHaveBeenCalledWith(
      expect.objectContaining({ providerId: "provider-1", authConfig: expect.objectContaining({ values: expect.objectContaining({ clientId: "billing-client" }) }) }),
      "billing-secret",
    );
  });

  it("submits the credential with Connection provisioning instead of creating an orphan Secret first", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    await wrapper.get('input[placeholder="例如：昆仑平台"]').setValue("OAuth billing");
    await wrapper.get('input[placeholder="客户端标识"]').setValue("billing-client");
    await wrapper.get('input[type="password"]').setValue("billing-secret");
    await wrapper.get('[data-testid="connection-save-draft"]').trigger("click");
    await flushPromises();

    expect(fixture.store.createCredentialSecret).not.toHaveBeenCalled();
    expect(fixture.store.createServiceConnection).toHaveBeenCalledWith(
      expect.objectContaining({ authConfig: expect.objectContaining({ values: expect.objectContaining({ clientId: "billing-client" }) }) }),
      "billing-secret",
    );
  });

  it("treats a write-only credential as an unsaved form change", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    await wrapper.get('input[type="password"]').setValue("unsaved-secret");
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    expect(wrapper.text()).toContain("放弃未保存修改？");
  });

  it("lets a new Connection choose a non-default Provider authentication scheme", async () => {
    const contract = fixture.store.providers[0].driverConfig.authentication;
    contract.schemes.push({ key: "none", type: "NONE", displayName: "Public access", fields: [] });
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    const authSelect = wrapper.findAllComponents(AppSelect).find((item) => item.props("ariaLabel") === "认证方式");
    expect(authSelect).toBeDefined();
    authSelect!.vm.$emit("update:modelValue", "none");
    await flushPromises();
    expect(wrapper.find('input[type="password"]').exists()).toBe(false);
  });

  it("keeps a flat OAuth Connection legacy after its Provider publishes a contract", async () => {
    const legacy = legacyOAuthConnectionFixture();
    fixture.store.serviceConnectionPageItems = [legacy];
    fixture.store.serviceConnectionCatalog = [legacy];
    fixture.store.serviceConnectionRegistryTotal = 1;
    const originalAuthConfig = structuredClone(legacy.authConfig);
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="编辑连接"]').trigger("click");
    expect(wrapper.text()).toContain("这是旧版认证配置");
    expect(wrapper.text()).toContain("即使 Provider 已升级认证契约");
    expect(wrapper.text()).not.toContain("Token Endpoint（Provider）");

    await wrapper.get('[data-testid="connection-save-draft"]').trigger("click");
    await flushPromises();

    expect(fixture.store.updateServiceConnection).toHaveBeenCalledOnce();
    const savedDraft = fixture.store.updateServiceConnection.mock.calls[0][1] as ServiceConnection;
    expect(savedDraft.authConfig).toEqual(originalAuthConfig);
    expect(savedDraft.authConfig.schemeKey).toBeUndefined();
    expect(savedDraft.authConfig.values).toBeUndefined();
    expect(savedDraft.authConfig.tokenUrl).toBe("https://legacy.example/token");
    expect(savedDraft.authConfig.clientAuth).toBe("client_secret_post");
  });

  it("uses the v1 verification result and stable diagnostics", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="验证连接"]').trigger("click");
    await flushPromises();
    expect(fixture.store.verifyConnection).toHaveBeenCalledWith("connection-1");
    expect(wrapper.text()).toContain("已通过连接验证");
  });
});

function providerFixture(): CapabilityProvider {
  return { id: "provider-1", name: "Orders API", kind: "HTTP_OPENAPI", driverKey: "http_openapi", transport: "HTTP", endpointConfig: { schemaVersion: 2, serviceBaseUrl: "https://orders.example", discovery: { documentUrl: "https://orders.example/openapi.json" }, verification: { method: "GET", path: "/health", expectedStatuses: [200] } }, driverConfig: { authentication: defaultOAuthContract("https://login.example/token") }, discoveryMode: "ON_DEMAND", status: "ACTIVE", createdBy: "user-1", updatedBy: "user-1", lockVersion: 1 };
}

function assetFixture(): ProviderAsset {
  return { id: "asset-1", kind: "TOOL", externalId: "orders.get", name: "Get order", description: "", inputSchema: {}, outputSchema: {}, metadata: {}, sourceChecksum: "a".repeat(64), status: "ACTIVE" };
}

function connectionFixture(): ServiceConnection {
  return {
    id: "connection-1", providerId: "provider-1", name: "Orders production", alias: "orders-prod", environment: "PRODUCTION", protocol: "HTTP",
    protocolConfig: { domain: "https://orders.example/openapi.json", host: "orders.example", port: "", basePath: "/openapi.json", verificationMethod: "GET", verificationPath: "", expectedStatus: "200-299", expectedResponseContains: "", commonHeaders: {} },
    protocolSchema: "provider.http-openapi.v1", authMode: "API_KEY",
    authConfig: { mode: "api-key-secret", label: "API Key", tokenUrl: "", refreshUrl: "", refreshMode: "none", accessTokenPath: "", refreshTokenPath: "", expiresPath: "", injectionTemplate: "", retryOn401Policy: "", refreshFailurePolicy: "", credentialPlacement: "header", apiKeyName: "X-API-Key" },
    credentialConfigured: true, credentialFingerprint: "sha256:abcd", grantedScopes: [], policy: {}, status: "UNVERIFIED", createdBy: "user-1", updatedBy: "user-1", lockVersion: 1,
  };
}

function legacyOAuthConnectionFixture(): ServiceConnection {
  return {
    ...connectionFixture(),
    id: "connection-legacy-oauth",
    name: "Legacy OAuth production",
    authMode: "OAUTH2_CLIENT",
    authConfig: {
      mode: "oauth2-client",
      label: "Legacy OAuth2 Client Credentials",
      tokenUrl: "https://legacy.example/token",
      clientId: "legacy-client",
      clientAuth: "client_secret_post",
      scope: "orders.read",
      refreshUrl: "",
      refreshMode: "none",
      accessTokenPath: "access_token",
      refreshTokenPath: "refresh_token",
      expiresPath: "expires_in",
      injectionTemplate: "Authorization: Bearer {{accessToken}}",
      retryOn401Policy: "refresh-once",
      refreshFailurePolicy: "fail-closed",
      credentialPlacement: "header",
      tokenHeaderName: "Authorization",
      tokenPrefix: "Bearer",
    },
  };
}

function verificationFixture(): ServiceConnectionVerification {
  return { id: "verify-1", workspaceId: "workspace-1", connectionId: "connection-1", status: "SUCCEEDED", diagnostics: { category: "OK", code: "CONNECTION_VERIFIED" }, latencyMs: 12, testedBy: "user-1", testedAt: "2026-07-15T03:00:00Z" };
}
