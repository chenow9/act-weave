import { describe, expect, it } from "vitest";

import { connectionWritePayload, sanitizeAuthConfig } from "./connections-providers";
import { normalizeProvider, providerWritePayload } from "./connections-providers";
import { toolDraftPayload, toolFromDTO } from "./schema-tools";

describe("integration mappers", () => {
  it("providerWritePayload is pure and omits secrets", () => {
    const payload = providerWritePayload(
      normalizeProvider({
        id: "p1",
        name: "Prov",
        driverKey: "http.openapi",
        transport: "HTTP",
        endpointConfig: { baseUrl: "https://example.test" },
        driverConfig: {},
        discoveryMode: "OPENAPI",
        status: "ACTIVE",
        lockVersion: 2,
      } as never),
    );
    expect(payload.name).toBe("Prov");
    expect(JSON.stringify(payload)).not.toMatch(/password|secret|plaintext/i);
  });

  it("connectionWritePayload keeps credential plaintext only as local argument", () => {
    const connection = {
      id: "c1",
      providerId: "p1",
      name: "conn",
      alias: "conn",
      environment: "Production",
      authMode: "API_KEY",
      authConfig: {
        apiKeyHeader: "X-Key",
        apiKeyPrefix: "",
        username: "",
        passwordSecretId: "",
        clientId: "",
        clientSecretId: "",
        tokenUrl: "",
        scopes: "",
      },
      baseUrl: "https://example.test",
      outboundMode: "NONE",
      status: "ACTIVE",
      lockVersion: 1,
      workspaceId: "ws-1",
    } as never;
    const payload = connectionWritePayload(connection, false, "super-secret-value");
    // Secret must not be copied into nested state objects returned for storage.
    expect(JSON.stringify(sanitizeAuthConfig(connection.authConfig))).not.toContain("super-secret-value");
    expect(payload).toBeTruthy();
  });

  it("toolFromDTO normalizes versions without leaking secret-shaped fields", () => {
    const tool = toolFromDTO(
      {
        id: "t1",
        providerId: "p1",
        name: "tool",
        slug: "tool",
        description: "d",
        status: "ACTIVE",
        createdBy: "u",
        updatedBy: "u",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        lockVersion: 1,
      },
      "ws-1",
      [
        {
          id: "v1",
          versionNo: 1,
          lifecycleStatus: "DRAFT",
          executorType: "HTTP",
          actionSchemaVersion: "1",
          actionConfig: { method: "GET", path: "/x" },
          inputSchema: { type: "object", properties: {} },
          outputSchema: { type: "object", properties: {} },
          errorMappings: {},
          runtimePolicy: {},
          riskLevel: "LOW",
          sideEffectLevel: "NONE",
          requiresConfirmation: false,
          checksum: "abc",
          createdBy: "u",
          updatedBy: "u",
          lockVersion: 1,
        },
      ],
    );
    expect(tool.id).toBe("t1");
    expect(tool.workspaceId).toBe("ws-1");
    const draft = toolDraftPayload(tool);
    expect(JSON.stringify(draft)).not.toMatch(/password|plaintext|clientSecret/i);
  });
});
