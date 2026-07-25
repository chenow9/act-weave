import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { CapabilityProvider, OpenAPIImport, ServiceConnection, Tool, ToolVersion } from "../types/domain";
import { noAuthenticationContract } from "../utils/provider-auth";
import { useIntegrationStore } from "./integration";
import { useWorkspaceStore } from "./workspaces";

vi.mock("../services/api", () => ({
  apiClient: { delete: vi.fn(), get: vi.fn(), patch: vi.fn(), post: vi.fn() },
}));

describe("v1 provider, connection, and secret store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    useWorkspaceStore().activeWorkspaceId = "workspace-1";
    vi.resetAllMocks();
  });

  it("uses the HTTP_OPENAPI provider allowlist and supports sync, assets, and materialization", async () => {
    const provider = providerFixture();
    const asset = assetFixture();
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [provider] } })
      .mockResolvedValueOnce({ data: { items: [provider] } })
      .mockResolvedValueOnce({ data: { items: [asset] } })
      .mockResolvedValueOnce({ data: { items: [{ ...asset, materializedCapabilityId: "cap-1", status: "MATERIALIZED" }] } });
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: { id: "sync-1", status: "SUCCEEDED", discoveredCount: 1, changedCount: 1, errorSummary: {} } })
      .mockResolvedValueOnce({ data: { asset, capabilityId: "cap-1", draftVersionId: "draft-1", lifecycleStatus: "DRAFT" } });
    const store = useIntegrationStore();

    await store.loadProviders();
    await store.syncProvider(provider.id);
    await store.loadProviderAssets(provider.id);
    await store.materializeProviderAsset(provider.id, asset.id);

    expect(apiClient.get).toHaveBeenNthCalledWith(1, "/workspaces/workspace-1/providers");
    expect(apiClient.post).toHaveBeenNthCalledWith(1, "/workspaces/workspace-1/providers/provider-1:sync");
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/providers/provider-1/assets");
    expect(apiClient.post).toHaveBeenNthCalledWith(
      2,
      "/workspaces/workspace-1/providers/provider-1/assets/asset-1:materialize",
      {},
    );
  });

  it("submits only provider configuration fields", async () => {
    const provider = providerFixture({ id: "", lockVersion: 0 });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: providerFixture() });
    const store = useIntegrationStore();

    await store.createProvider(provider);

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/providers", {
      name: "Orders API",
      kind: "HTTP_OPENAPI",
      driverKey: "http_openapi",
      transport: "HTTP",
      endpointConfig: { sourceUri: "https://orders.example/openapi.json" },
      driverConfig: {},
      discoveryMode: "ON_DEMAND",
    });
  });

  it("loads provider-scoped connections and never submits plaintext credential fields", async () => {
    const provider = providerFixture();
    const connection = connectionDTO();
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [provider] } })
      .mockResolvedValueOnce({ data: { items: [connection] } });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: connection });
    const store = useIntegrationStore();
    await store.loadProviders();
    await store.loadServiceConnectionPage({ status: "UNVERIFIED" });
    const draft = connectionFixture({
      providerId: provider.id,
      credentialSecretId: "secret-1",
      authConfig: {
        ...connectionFixture().authConfig,
        mode: "api-key-secret",
        apiKeyName: "X-API-Key",
        apiSecretName: "X-API-Secret",
      },
    });

    await store.createServiceConnection(draft);

    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/providers/provider-1/connections");
    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/providers/provider-1/connections",
      expect.objectContaining({
        name: "Orders production",
        alias: "orders-prod",
        environment: "PRODUCTION",
        authMode: "API_KEY",
        credentialSecretId: "secret-1",
      }),
    );
    const payload = vi.mocked(apiClient.post).mock.calls.at(-1)?.[1] as Record<string, unknown>;
    expect(JSON.stringify(payload)).not.toMatch(/apiKeyValue|apiSecretValue|fixedToken|plaintext/i);
  });

  it("maps the default no-authentication scheme to the NONE API mode", async () => {
    const provider = providerFixture({ driverConfig: { authentication: noAuthenticationContract() } });
    const response = connectionDTO({ authMode: "NONE", authConfig: { schemeKey: "none", values: {} }, credentialConfigured: false });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: response });
    const store = useIntegrationStore();
    store.providers = [provider];
    const draft = connectionFixture({
      providerId: provider.id,
      authMode: "NONE",
      credentialConfigured: false,
      authConfig: {
        ...connectionFixture().authConfig,
        mode: "none",
        label: "无需认证",
        schemeKey: "none",
        values: {},
      },
    });

    await store.createServiceConnection(draft);

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/providers/provider-1/connections",
      expect.objectContaining({
        authMode: "NONE",
        authConfig: { schemeKey: "none", values: {} },
      }),
    );
  });

  it("updates, verifies, and deletes through workspace-scoped command paths with lockVersion", async () => {
    const provider = providerFixture();
    const store = useIntegrationStore();
    store.providers = [provider];
    const connection = connectionFixture();
    store.serviceConnectionCatalog = [connection];
    store.serviceConnectionPageItems = [connection];
    vi.mocked(apiClient.patch).mockResolvedValueOnce({ data: connectionDTO({ name: "Orders test", lockVersion: 2 }) });
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        ID: "verify-1",
        WorkspaceID: "workspace-1",
        ConnectionID: connection.id,
        Status: "SUCCEEDED",
        Diagnostics: { category: "OK", code: "CONNECTION_VERIFIED" },
        LatencyMS: 12,
        TestedBy: "user-1",
        TestedAt: "2026-07-15T03:00:00Z",
      },
    });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });

    const updated = await store.updateServiceConnection(connection.id, { ...connection, name: "Orders test" });
    await store.verifyConnection(connection.id);
    await store.deleteServiceConnection(connection.id);

    expect(apiClient.patch).toHaveBeenCalledWith(
      "/workspaces/workspace-1/connections/connection-1",
      expect.objectContaining({ name: "Orders test", lockVersion: 1 }),
    );
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/connections/connection-1:verify");
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/workspace-1/connections/connection-1?lockVersion=2");
    expect(updated.credentialConfigured).toBe(true);
  });

  it("rotates a secret with plaintext only in the command request and accepts metadata-only responses", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: { id: "secret-1", workspaceId: "workspace-1", name: "API key", kind: "API_KEY", configured: true, fingerprint: "sha256:abcd", activeVersionNo: 2, lockVersion: 2 },
    });
    const store = useIntegrationStore();

    const result = await store.rotateSecret("secret-1", "new-value", 1);

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/secrets/secret-1:rotate", {
      plaintext: "new-value",
      lockVersion: 1,
    });
    expect(result).not.toHaveProperty("plaintext");
    expect(JSON.stringify(result)).not.toContain("new-value");
  });
});

describe("v1 Tool Version and OpenAPI Import store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    const workspaces = useWorkspaceStore();
    workspaces.activeWorkspaceId = "workspace-1";
    workspaces.items = [{ id: "workspace-1" } as never];
    vi.resetAllMocks();
  });

  it("creates Provider imports without legacy Agent or document fields", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { import: importDTO(), duplicateOfId: undefined } });
    const store = useIntegrationStore();

    await store.createOpenAPIImport({
      workspaceId: "workspace-1",
      providerId: "provider-1",
      connectionId: "connection-1",
      agentId: "legacy-agent",
      source: "legacy-source",
      rawContent: "openapi: 3.0.3",
    });

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/openapi-imports", {
      providerId: "provider-1",
      connectionId: "connection-1",
    });
    expect(JSON.stringify(vi.mocked(apiClient.post).mock.calls[0][1])).not.toMatch(/agentId|rawContent|source|file/i);
  });

  it("preserves nested JSON Schema and request locations when loading Tool versions", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [toolDTO()] } })
      .mockResolvedValueOnce({
        data: {
          items: [versionDTO({
            errorMappings: { "404": { errorCode: "USER_NOT_FOUND", agentAdvice: "检查用户 ID" } },
            inputSchema: {
              type: "object",
              properties: {
                trace: { type: "string", "x-actweave-location": "header", "x-actweave-parameter-name": "X-Trace-Id" },
              },
            },
            outputSchema: {
              type: "object",
              properties: {
                data: {
                  type: "array",
                  items: {
                    type: "object",
                    required: ["id"],
                    properties: {
                      id: { type: "integer" },
                      children: { type: "array", items: { type: "object", properties: { id: { type: "integer" } } } },
                    },
                  },
                },
              },
            },
          })],
        },
      });
    const store = useIntegrationStore();

    const [tool] = await store.loadTools();

    expect(tool.requestParams[0]).toMatchObject({ name: "X-Trace-Id", location: "Header" });
    expect(tool.responseFields[0].schema?.item?.children?.[0]).toMatchObject({ name: "id", type: "integer", required: true });
    expect(tool.responseFields[0].schema?.item?.children?.[1].item?.children?.[0]).toMatchObject({ name: "id", type: "integer" });
    expect(tool.errorMappings).toEqual([{ protocolStatus: "404", errorCode: "USER_NOT_FOUND", agentAdvice: "检查用户 ID" }]);
  });

  it("scopes Tool catalog to the active Workspace and resolves connections for the attention filter", async () => {
    const workspaces = useWorkspaceStore();
    workspaces.activeWorkspaceId = "workspace-1";
    workspaces.items = [{ id: "workspace-1" }, { id: "workspace-2" }] as never[];
    vi.mocked(apiClient.get).mockImplementation(async (url) => {
      if (url === "/workspaces/workspace-1/tools") return { data: { items: [toolDTO()] } } as never;
      if (url === "/workspaces/workspace-2/tools") throw new Error(`Unexpected GET ${url}`);
      if (url === "/workspaces/workspace-1/tools/tool-1/versions") {
        return { data: { items: [versionDTO({ lifecycleStatus: "PUBLISHED" })] } } as never;
      }
      if (url === "/workspaces/workspace-1/providers") return { data: { items: [providerFixture()] } } as never;
      if (url === "/workspaces/workspace-1/providers/provider-1/connections") {
        return { data: { items: [connectionDTO({ status: "ERROR" })] } } as never;
      }
      throw new Error(`Unexpected GET ${url}`);
    });
    const store = useIntegrationStore();

    await store.loadToolPage({ status: "attention", page: 1, pageSize: 10 });

    expect(store.toolPageItems).toMatchObject([{ id: "tool-1", workspaceId: "workspace-1" }]);
    expect(store.connectionForTool(store.toolPageItems[0])).toMatchObject({
      id: "connection-1",
      workspaceId: "workspace-1",
      status: "ERROR",
    });

    await store.loadToolPage({ status: undefined, type: "Workflow Tool", page: 1, pageSize: 10 });
    expect(store.toolPageItems).toEqual([]);

    await store.loadToolPage({ type: "HTTP Tool", page: 1, pageSize: 10 });
    expect(store.toolPageItems).toMatchObject([{ id: "tool-1" }]);
  });

  it("searches Tools by action Path and resolved Service Connection name", async () => {
    vi.mocked(apiClient.get).mockImplementation(async (url) => {
      if (url === "/workspaces/workspace-1/tools") return { data: { items: [toolDTO()] } } as never;
      if (url === "/workspaces/workspace-1/tools/tool-1/versions") {
        return {
          data: {
            items: [versionDTO({ actionConfig: { method: "GET", path: "/api/open/v1/users/{id}" } })],
          },
        } as never;
      }
      if (url === "/workspaces/workspace-1/providers") return { data: { items: [providerFixture()] } } as never;
      if (url === "/workspaces/workspace-1/providers/provider-1/connections") {
        return { data: { items: [connectionDTO({ name: "Neiops Production" })] } } as never;
      }
      throw new Error(`Unexpected GET ${url}`);
    });
    const store = useIntegrationStore();

    await store.loadToolPage({ query: "/api/open/v1/users/{id}", page: 1, pageSize: 10 });
    expect(store.toolPageItems).toMatchObject([{ id: "tool-1" }]);

    await store.loadToolPage({ query: "Neiops Production", page: 1, pageSize: 10 });
    expect(store.toolPageItems).toMatchObject([{ id: "tool-1" }]);
  });

  it("uploads a local OpenAPI file with Provider and Connection bindings", async () => {
    apiClient.post.mockResolvedValueOnce({ data: { import: importFixture() } });
    const store = useIntegrationStore();
    const file = new File(["{}"], "neiops-openapi.json", { type: "application/json" });

    await store.createOpenAPIFileImport({
      workspaceId: "workspace-1",
      providerId: "provider-1",
      connectionId: "connection-1",
    }, file);

    const form = apiClient.post.mock.calls[0]?.[1] as FormData;
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/openapi-imports/__command/upload", expect.any(FormData));
    expect(form.get("providerId")).toBe("provider-1");
    expect(form.get("connectionId")).toBe("connection-1");
    expect((form.get("file") as File).name).toBe("neiops-openapi.json");
  });

  it("edits the exact Draft Version and forks a published version before changing its spec", async () => {
    const published = versionDTO({ id: "version-1", lifecycleStatus: "PUBLISHED", versionNo: 1, lockVersion: 3 });
    const createdDraft = versionDTO({ id: "version-2", lifecycleStatus: "DRAFT", versionNo: 2, lockVersion: 1 });
    const updatedDraft = versionDTO({ ...createdDraft, actionConfig: { method: "POST", path: "/orders" }, lockVersion: 2 });
    const current = toolFixture({
      versions: [published],
      draftVersion: published,
      requestParams: [{
        location: "Header",
        name: "X-Trace-Id",
        type: "string",
        required: false,
        description: "trace",
      }],
    });
    const store = useIntegrationStore();
    store.tools = [current];
    vi.mocked(apiClient.patch)
      .mockResolvedValueOnce({ data: toolDTO({ lockVersion: 2 }) })
      .mockResolvedValueOnce({ data: updatedDraft });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: createdDraft });

    await store.updateTool(current.id, { ...current, actionConfig: { method: "POST", path: "/orders" } });

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/tools/tool-1/versions", {
      sourceVersionId: "version-1",
    });
    expect(apiClient.patch).toHaveBeenNthCalledWith(
      2,
      "/workspaces/workspace-1/tools/tool-1/versions/version-2",
      expect.objectContaining({ lifecycleStatus: "DRAFT", lockVersion: 1, draft: expect.objectContaining({ actionConfig: { method: "POST", path: "/orders" } }) }),
    );
    const versionPatch = vi.mocked(apiClient.patch).mock.calls[1]?.[1] as { draft: { inputSchema: { properties: Record<string, Record<string, unknown>> } } };
    expect(versionPatch.draft.inputSchema.properties["X-Trace-Id"]).toMatchObject({
      type: "string",
      "x-actweave-location": "header",
      "x-actweave-parameter-name": "X-Trace-Id",
    });
    expect(versionPatch.draft.inputSchema.properties["X-Trace-Id"]).not.toHaveProperty("location");
    expect((versionPatch as unknown as { draft: { errorMappings: Record<string, unknown> } }).draft.errorMappings).toEqual({});
  });

  it("tests the selected non-published version and sends connectionId plus input", async () => {
    const draft = versionDTO({ id: "version-2", lifecycleStatus: "REVIEW", versionNo: 2 });
    const tested = versionDTO({ ...draft, lifecycleStatus: "TESTED", lockVersion: 2 });
    const current = toolFixture({ versions: [draft], draftVersion: draft });
    const store = useIntegrationStore();
    store.tools = [current];
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { ...testDTO(), status: "SUCCEEDED", responseSummary: { httpStatus: 200 } } });
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [tested] } })
      .mockResolvedValueOnce({ data: toolDTO() });

    const result = await store.testTool(current.id, { orderId: "A-1" });

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/tools/tool-1/versions/version-2:test",
      { connectionId: "connection-1", input: { orderId: "A-1" } },
    );
    expect(store.tools[0].draftVersion?.lifecycleStatus).toBe("TESTED");
    expect(result).toMatchObject({ passed: true, responseStatus: 200, testResult: { status: "Tested" } });
    expect(store.tools[0].lastTestResult?.status).toBe("Tested");
  });

  it("publishes only the exact tested version with its optimistic lock", async () => {
    const tested = versionDTO({ id: "version-2", lifecycleStatus: "TESTED", versionNo: 2, lockVersion: 4 });
    const published = versionDTO({ ...tested, lifecycleStatus: "PUBLISHED", lockVersion: 5 });
    const current = toolFixture({ versions: [tested], draftVersion: tested, lastTestResult: testDTO() });
    const store = useIntegrationStore();
    store.tools = [current];
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { releaseId: "release-1", releaseNo: 1, version: published, testId: "test-1" } });
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: toolDTO({ activeReleaseId: "release-1", lockVersion: 2 }) });

    await store.publishTool(current.id);

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/tools/tool-1/versions/version-2:publish",
      { callableName: "lookup_order", callableDescription: "Lookup order", lockVersion: 4 },
    );
    expect(store.tools[0].versions[0].lifecycleStatus).toBe("PUBLISHED");
  });

  it("generates Tools for ready endpoint IDs through the workspace import command", async () => {
    const store = useIntegrationStore();
    const record = importFixture();
    store.openAPIImportCatalog = [record];
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: { items: [{ endpointId: "endpoint-1", tool: toolDTO(), draft: versionDTO() }] },
    });
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: { import: importDTO(), endpoints: [{ id: "endpoint-1", method: "GET", path: "/orders/{id}", operationId: "lookupOrder", summary: "Lookup", inputSchema: {}, outputSchema: {}, issues: [], ready: true, generatedCapabilityId: "tool-1" }] },
    });

    await store.generateToolDrafts(record.id);

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/openapi-imports/import-1:generate-tools",
      { endpointIds: ["endpoint-1"] },
    );
  });

  it("does not register OAuth token infrastructure as Agent-callable Tools", async () => {
    const store = useIntegrationStore();
    const record = importFixture();
    record.detail!.endpoints.push(
      { id: "endpoint-token", method: "POST", path: "/oauth2/token", operationId: "issueToken", summary: "Token", status: "READY", ready: true },
      { id: "endpoint-revoke", method: "POST", path: "/oauth2/revoke/", operationId: "revokeToken", summary: "Revoke", status: "READY", ready: true },
    );
    store.openAPIImportCatalog = [record];
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { items: [] } });
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { import: importDTO(), endpoints: [] } });

    await store.generateToolDrafts(record.id);

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/openapi-imports/import-1:generate-tools",
      { endpointIds: ["endpoint-1"] },
    );
  });
});

function providerFixture(overrides: Partial<CapabilityProvider> = {}): CapabilityProvider {
  return {
    id: "provider-1",
    name: "Orders API",
    kind: "HTTP_OPENAPI",
    driverKey: "http_openapi",
    transport: "HTTP",
    endpointConfig: { sourceUri: "https://orders.example/openapi.json" },
    driverConfig: {},
    discoveryMode: "ON_DEMAND",
    status: "ACTIVE",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

function assetFixture() {
  return {
    id: "asset-1",
    kind: "TOOL",
    externalId: "orders.get",
    name: "Get order",
    description: "Get an order",
    inputSchema: { type: "object" },
    outputSchema: { type: "object" },
    metadata: {},
    sourceChecksum: "a".repeat(64),
    status: "ACTIVE",
  };
}

function connectionDTO(overrides: Record<string, unknown> = {}) {
  return {
    id: "connection-1",
    providerId: "provider-1",
    name: "Orders production",
    alias: "orders-prod",
    environment: "PRODUCTION",
    authMode: "API_KEY",
    authConfig: { headerName: "X-API-Key", placement: "header" },
    credentialConfigured: true,
    credentialFingerprint: "sha256:abcd",
    grantedScopes: [],
    policy: {},
    status: "UNVERIFIED",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

function connectionFixture(overrides: Partial<ServiceConnection> = {}): ServiceConnection {
  return {
    id: "connection-1",
    providerId: "provider-1",
    name: "Orders production",
    alias: "orders-prod",
    environment: "PRODUCTION",
    protocol: "HTTP",
    protocolConfig: { domain: "https://orders.example/openapi.json", host: "orders.example", port: "", basePath: "/openapi.json", verificationMethod: "GET", verificationPath: "", expectedStatus: "200-299", expectedResponseContains: "", commonHeaders: {} },
    protocolSchema: "provider.http-openapi.v1",
    authMode: "API_KEY",
    authConfig: { mode: "api-key-secret", label: "", tokenUrl: "", refreshUrl: "", refreshMode: "none", accessTokenPath: "", refreshTokenPath: "", expiresPath: "", injectionTemplate: "", retryOn401Policy: "", refreshFailurePolicy: "", credentialPlacement: "header", apiKeyName: "X-API-Key" },
    credentialConfigured: true,
    credentialFingerprint: "sha256:abcd",
    grantedScopes: [],
    policy: {},
    status: "UNVERIFIED",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

function toolDTO(overrides: Record<string, unknown> = {}) {
  return {
    id: "tool-1",
    providerId: "provider-1",
    defaultConnectionId: "connection-1",
    name: "Lookup order",
    slug: "lookup-order",
    description: "Lookup order",
    status: "ACTIVE",
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-15T00:00:00Z",
    updatedAt: "2026-07-15T00:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}

function versionDTO(overrides: Partial<ToolVersion> = {}): ToolVersion {
  return {
    id: "version-1",
    versionNo: 1,
    lifecycleStatus: "DRAFT",
    executorType: "HTTP",
    defaultConnectionId: "connection-1",
    actionSchemaVersion: "http.v1",
    actionConfig: { method: "GET", path: "/orders/{id}" },
    inputSchema: { type: "object", properties: {} },
    outputSchema: { type: "object", properties: {} },
    errorMappings: { mappings: [] },
    runtimePolicy: { timeoutMs: 8000, retryCount: 0 },
    riskLevel: "LOW",
    sideEffectLevel: "READ",
    requiresConfirmation: false,
    checksum: "a".repeat(64),
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

function toolFixture(overrides: Partial<Tool> = {}): Tool {
  const draft = versionDTO();
  return {
    id: "tool-1",
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    defaultConnectionId: "connection-1",
    name: "Lookup order",
    slug: "lookup-order",
    protocol: "HTTP",
    actionConfig: { method: "GET", path: "/orders/{id}" },
    actionConfigSchemaVersion: "http.v1",
    description: "Lookup order",
    status: "Draft",
    capabilityStatus: "ACTIVE",
    versions: [draft],
    draftVersion: draft,
    requestParams: [],
    responseFields: [],
    errorMappings: [],
    runtimePolicy: { timeoutMs: 8000, retryCount: 0, backoffPolicy: "exponential", idempotencyPolicy: "none", rateLimitPolicy: "60 rpm" },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

function testDTO() {
  return {
    id: "test-1",
    status: "PASSED",
    connectivityPassed: true,
    responseSchemaPassed: true,
    errorMappingPassed: true,
    runtimePolicyPassed: true,
    requestSummary: {},
    responseSummary: { status: 200, body: { ok: true } },
    latencyMs: 12,
    testedBy: "user-1",
    testedAt: "2026-07-15T00:00:00Z",
  };
}

function importDTO() {
  return {
    id: "import-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    sourceType: "PROVIDER_OPENAPI",
    sourceUri: "https://orders.example/openapi.json",
    fileName: "openapi.json",
    contentSha256: "b".repeat(64),
    parserVersion: "v1",
    status: "READY",
    totalEndpoints: 1,
    readyEndpoints: 1,
    issueCount: 0,
    createdBy: "user-1",
    createdAt: "2026-07-15T00:00:00Z",
    updatedAt: "2026-07-15T00:00:00Z",
  };
}

function importFixture(): OpenAPIImport {
  return {
    id: "import-1",
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    source: "Provider OpenAPI",
    sourceType: "PROVIDER_OPENAPI",
    fileName: "openapi.json",
    contentSha256: "b".repeat(64),
    parserVersion: "v1",
    totalEndpoints: 1,
    readyEndpoints: 1,
    issueCount: 0,
    issues: [],
    status: "Ready",
    detail: {
      endpoints: [{ id: "endpoint-1", method: "GET", path: "/orders/{id}", operationId: "lookupOrder", summary: "Lookup", status: "READY", ready: true }],
    },
  };
}
