import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  CapabilityProvider,
  ProviderAsset,
  ServiceConnection,
  ServiceConnectionVerification,
} from "../types/domain";
import ServiceConnectionsView from "./ServiceConnectionsView.vue";

const secretID = "019f68d9-d405-7032-9b21-542a7bf46d22";
const fixture = vi.hoisted(() => ({
  connections: null as any,
  providers: null as any,
  workspaces: null as any,
  router: { push: vi.fn() },
}));

vi.mock("../stores/connections", () => ({ useConnectionsStore: () => fixture.connections }));
vi.mock("../stores/providers", () => ({ useProvidersStore: () => fixture.providers }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixture.workspaces }));
vi.mock("vue-router", () => ({ useRouter: () => fixture.router }));

function createStores() {
  const provider = providerFixture();
  const connection = connectionFixture();
  const providers = reactive({
    providers: [provider],
    providerAssetsByProvider: {} as Record<string, ProviderAsset[]>,
    loadProviders: vi.fn(async () => providers.providers),
    createProvider: vi.fn(async (draft: CapabilityProvider) => ({ ...draft, id: "provider-created", lockVersion: 1 })),
    updateProvider: vi.fn(async (draft: CapabilityProvider) => ({ ...draft, lockVersion: draft.lockVersion + 1 })),
    deleteProvider: vi.fn(async () => undefined),
    syncProvider: vi.fn(async () => ({
      id: "sync-1",
      status: "SUCCEEDED",
      discoveredCount: 1,
      changedCount: 1,
      errorSummary: {},
    })),
    loadProviderAssets: vi.fn(async (providerId: string) => {
      providers.providerAssetsByProvider[providerId] = [assetFixture()];
      return providers.providerAssetsByProvider[providerId];
    }),
    materializeProviderAsset: vi.fn(async () => ({ capabilityId: "cap-1" })),
  });
  const connections = reactive({
    serviceConnectionPageItems: [connection],
    serviceConnectionPagination: { page: 1, pageSize: 10, total: 1, pageSizeOptions: [10, 20, 50] },
    serviceConnectionListQuery: { query: "", page: 1, pageSize: 10 },
    serviceConnectionCatalog: [connection],
    serviceConnectionRegistryTotal: 1,
    verificationByConnectionId: {} as Record<string, ServiceConnectionVerification>,
    get serviceConnections() {
      return connections.serviceConnectionCatalog;
    },
    loadServiceConnectionPage: vi.fn(async () => connections.serviceConnectionPageItems),
    loadServiceConnectionCatalog: vi.fn(async () => connections.serviceConnectionCatalog),
    createCredentialSecret: vi.fn(async () => ({ id: secretID })),
    createServiceConnection: vi.fn(async (draft: ServiceConnection, _credential = "", _options = {}) => ({
      ...connection,
      ...draft,
      id: "connection-created",
      lockVersion: 1,
    })),
    updateServiceConnection: vi.fn(async (_id: string, draft: ServiceConnection) => ({
      ...draft,
      lockVersion: draft.lockVersion + 1,
    })),
    deleteServiceConnection: vi.fn(async () => undefined),
    verifyConnection: vi.fn(async () => verificationFixture()),
    previewConnectionImpact: vi.fn(async () => ({ impactConfirmationProof: "proof-1" })),
  });
  return { providers, connections };
}

function mountView() {
  return mount(ServiceConnectionsView, {
    attachTo: document.body,
    global: {
      directives: { loading: () => undefined },
      stubs: {
        teleport: true,
        AppSelect: {
          name: "AppSelect",
          props: ["modelValue", "options", "placeholder", "disabled", "ariaLabel"],
          emits: ["update:modelValue"],
          template: `
            <select
              :value="modelValue"
              :disabled="disabled"
              :aria-label="ariaLabel"
              data-testid="app-select-stub"
              @change="$emit('update:modelValue', ($event.target).value)"
            >
              <option v-for="opt in options || []" :key="String(opt.value)" :value="opt.value">{{ opt.label }}</option>
            </select>
          `,
        },
      },
    },
  });
}

describe("service connections v1 behavior", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    window.history.pushState({}, "", "/connections");
    const stores = createStores();
    fixture.providers = stores.providers;
    fixture.connections = stores.connections;
    fixture.workspaces = reactive({
      activeWorkspaceId: "workspace-1",
      items: [{ id: "workspace-1", name: "Workspace 1" }],
      load: vi.fn(async () => undefined),
      can: vi.fn(() => true),
      roleFor: vi.fn(() => "EDITOR"),
    });
    vi.clearAllMocks();
  });

  it("renders Connection fields from the selected Provider outbound-identity contract", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    expect(wrapper.text()).toContain("服务 API（Capability Provider）");
    expect(
      wrapper.findAll("input").some((input) => (input.element as HTMLInputElement).value === "https://orders.example"),
    ).toBe(true);
    expect(wrapper.html()).not.toContain("https://orders.example/openapi.json");
    expect(wrapper.get('[data-testid="connection-outbound-strategy"]').exists()).toBe(true);
    expect(wrapper.text()).toContain("出站身份策略");
    expect(wrapper.get('[data-testid="outbound-mode-REQUEST_PASSTHROUGH"]').attributes("aria-checked")).toBe("true");
    expect(wrapper.get('[data-testid="outbound-passthrough-fields"]').exists()).toBe(true);
    expect(wrapper.html()).not.toMatch(/apiKeyValue|apiSecretValue|fixedToken|Token 值/);
  });

  it("routes Provider management to the dedicated registry view", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get(".connection-header-actions .ghost-button").trigger("click");
    expect(fixture.router.push).toHaveBeenCalledWith("/providers");
  });

  it("creates a REQUEST_PASSTHROUGH connection without orphan Secret provisioning", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    await wrapper.get('input[placeholder="例如：昆仑平台"]').setValue("Billing production");
    await wrapper.get('[data-testid="passthrough-max-residence"]').setValue("900");
    await wrapper.get('[data-testid="connection-save-draft"]').trigger("click");
    await flushPromises();

    expect(fixture.connections.createCredentialSecret).not.toHaveBeenCalled();
    expect(fixture.connections.createServiceConnection).toHaveBeenCalledWith(
      expect.objectContaining({
        providerId: "provider-1",
        name: "Billing production",
        outboundMode: "REQUEST_PASSTHROUGH",
        outboundIdentity: expect.objectContaining({
          schemaVersion: "outbound-connection.v1",
          mode: "REQUEST_PASSTHROUGH",
          requestPassthrough: expect.objectContaining({ maxResidenceSeconds: 900 }),
        }),
      }),
      "",
      expect.any(Object),
    );
  });

  it("creates a BROKER_OBO connection with machine credential options", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    await wrapper.get('input[placeholder="例如：昆仑平台"]').setValue("Broker billing");
    await wrapper.get('[data-testid="outbound-mode-BROKER_OBO"]').trigger("click");
    await flushPromises();
    await wrapper.get('[data-testid="broker-client-id"]').setValue("billing-client");
    await wrapper
      .get('[data-testid="broker-machine-credential"]')
      .setValue("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----");
    await wrapper.get('[data-testid="connection-save-draft"]').trigger("click");
    await flushPromises();

    expect(fixture.connections.createCredentialSecret).not.toHaveBeenCalled();
    expect(fixture.connections.createServiceConnection).toHaveBeenCalledWith(
      expect.objectContaining({
        providerId: "provider-1",
        name: "Broker billing",
        outboundMode: "BROKER_OBO",
        outboundIdentity: expect.objectContaining({
          mode: "BROKER_OBO",
          brokerObo: expect.objectContaining({ clientId: "billing-client" }),
        }),
      }),
      "",
      expect.objectContaining({
        machineCredentialPlaintext: expect.stringContaining("BEGIN PRIVATE KEY"),
      }),
    );
  });

  it("treats an edited outbound form field as an unsaved form change", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    await wrapper.get('[data-testid="passthrough-max-residence"]').setValue("1200");
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    expect(wrapper.text()).toContain("放弃未保存修改？");
  });

  it("lets a new Connection choose Broker/OBO instead of the default passthrough mode", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get(".connection-header-actions .primary-button").trigger("click");
    expect(wrapper.get('[data-testid="outbound-mode-REQUEST_PASSTHROUGH"]').attributes("aria-checked")).toBe("true");
    await wrapper.get('[data-testid="outbound-mode-BROKER_OBO"]').trigger("click");
    await flushPromises();
    expect(wrapper.get('[data-testid="outbound-mode-BROKER_OBO"]').attributes("aria-checked")).toBe("true");
    expect(wrapper.get('[data-testid="outbound-broker-fields"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="outbound-passthrough-fields"]').exists()).toBe(false);
  });

  it("keeps a migration-required Connection marked until the wizard form is used", async () => {
    const legacy = migrationConnectionFixture();
    fixture.connections.serviceConnectionPageItems = [legacy];
    fixture.connections.serviceConnectionCatalog = [legacy];
    fixture.connections.serviceConnectionRegistryTotal = 1;
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.text()).toContain("需迁移");
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="编辑连接"]').trigger("click");
    expect(wrapper.get('[data-testid="connection-migration-wizard-hint"]').exists()).toBe(true);
    expect(wrapper.text()).toContain("迁移向导");
    expect(wrapper.text()).toContain("旧认证只读对照");
  });

  it("uses the v1 verification result and stable diagnostics", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="验证连接"]').trigger("click");
    await flushPromises();
    expect(fixture.connections.verifyConnection).toHaveBeenCalledWith("connection-1");
    expect(wrapper.text()).toContain("已通过连接验证");
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
    name: "Orders API",
    kind: "HTTP_OPENAPI",
    driverKey: "http_openapi",
    transport: "HTTP",
    endpointConfig: {
      schemaVersion: 2,
      serviceBaseUrl: "https://orders.example",
      discovery: { documentUrl: "https://orders.example/openapi.json" },
      verification: { method: "GET", path: "/health", expectedStatuses: [200] },
    },
    driverConfig: { outboundIdentity: outboundIdentityFixture() },
    discoveryMode: "ON_DEMAND",
    status: "ACTIVE",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
  };
}

function assetFixture(): ProviderAsset {
  return {
    id: "asset-1",
    kind: "TOOL",
    externalId: "orders.get",
    name: "Get order",
    description: "",
    inputSchema: {},
    outputSchema: {},
    metadata: {},
    sourceChecksum: "a".repeat(64),
    status: "ACTIVE",
  };
}

function connectionFixture(): ServiceConnection {
  return {
    id: "connection-1",
    providerId: "provider-1",
    name: "Orders production",
    alias: "orders-prod",
    environment: "PRODUCTION",
    protocol: "HTTP",
    protocolConfig: {
      domain: "https://orders.example",
      host: "orders.example",
      port: "",
      basePath: "",
      verificationMethod: "GET",
      verificationPath: "/health",
      expectedStatus: "200",
      expectedResponseContains: "",
      commonHeaders: {},
    },
    protocolSchema: "http.connection.v1",
    authMode: "REQUEST_PASSTHROUGH",
    authConfig: {
      mode: "",
      label: "请求透传",
      tokenUrl: "",
      refreshUrl: "",
      refreshMode: "none",
      accessTokenPath: "",
      refreshTokenPath: "",
      expiresPath: "",
      injectionTemplate: "",
      retryOn401Policy: "",
      refreshFailurePolicy: "",
      credentialPlacement: "header",
    },
    outboundMode: "REQUEST_PASSTHROUGH",
    outboundIdentity: {
      schemaVersion: "outbound-connection.v1",
      mode: "REQUEST_PASSTHROUGH",
      requestPassthrough: { maxResidenceSeconds: 600 },
    },
    migrationState: "NONE",
    credentialConfigured: true,
    credentialFingerprint: "sha256:abcd",
    grantedScopes: [],
    policy: {},
    status: "UNVERIFIED",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
  };
}

function migrationConnectionFixture(): ServiceConnection {
  return {
    ...connectionFixture(),
    id: "connection-migration",
    name: "Legacy OAuth production",
    status: "DISABLED",
    migrationState: "MIGRATION_REQUIRED",
    outboundMode: undefined,
    outboundIdentity: undefined,
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
  return {
    id: "verify-1",
    workspaceId: "workspace-1",
    connectionId: "connection-1",
    status: "SUCCEEDED",
    diagnostics: { category: "OK", code: "CONNECTION_VERIFIED" },
    latencyMs: 12,
    testedBy: "user-1",
    testedAt: "2026-07-15T03:00:00Z",
  };
}
