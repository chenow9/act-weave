import { describe, expect, it } from "vitest";

import type { CapabilityProvider, ServiceConnection } from "../types/domain";
import {
  connectionAuthValues,
  connectionProviderAuthScheme,
  defaultOAuthContract,
  isProviderReadyForConnections,
  providerAuthScheme,
} from "./provider-auth";

describe("provider authentication contract", () => {
  it("keeps protocol details on Provider and exposes schema-driven Connection fields", () => {
    const provider = fixture();
    const scheme = providerAuthScheme(provider);
    expect(scheme?.oauth2?.tokenUrlTemplate).toBe("https://login.example/{{tenantId}}/token");
    expect(scheme?.fields.map((field) => field.key)).toContain("clientId");
    expect(isProviderReadyForConnections(provider)).toBe(true);
  });

  it("does not pretend a legacy Provider has a writable authentication contract", () => {
    const provider = fixture();
    provider.driverConfig = {};
    expect(providerAuthScheme(provider)).toBeNull();
    expect(isProviderReadyForConnections(provider)).toBe(false);
  });

  it("accepts dual-mode outbound-identity.v1 as ready for Connection create", () => {
    const provider = fixture();
    provider.driverConfig = {
      outboundIdentity: {
        schemaVersion: "outbound-identity.v1",
        supportedModes: ["BROKER_OBO", "REQUEST_PASSTHROUGH"],
        supportedSubjectTypes: ["USER"],
        requestPassthrough: {
          credentialTypes: ["ACCESS_TOKEN"],
          businessInjection: { headerName: "Authorization", prefix: "Bearer" },
        },
      },
    };
    expect(isProviderReadyForConnections(provider)).toBe(true);
  });

  it("does not infer a Provider scheme or migrate flat OAuth fields for a legacy Connection", () => {
    const connection = legacyConnection();

    expect(connectionProviderAuthScheme(fixture(), connection)).toBeNull();
    expect(connectionAuthValues(connection.authConfig)).toEqual({});
  });

  it("resolves a Connection scheme only from its explicit scheme key", () => {
    const connection = legacyConnection();
    connection.authConfig.schemeKey = "oauth2-client";
    connection.authConfig.values = { clientId: "new-client" };

    expect(connectionProviderAuthScheme(fixture(), connection)?.key).toBe("oauth2-client");
    expect(connectionAuthValues(connection.authConfig)).toEqual({ clientId: "new-client" });
  });
});

function legacyConnection(): ServiceConnection {
  return {
    id: "connection-1", providerId: "provider-1", name: "Legacy OAuth", alias: "legacy-oauth", environment: "PRODUCTION",
    protocol: "HTTP", protocolSchema: "provider.http-openapi.v1",
    protocolConfig: { domain: "https://api.example", host: "api.example", port: "", basePath: "", verificationMethod: "GET", verificationPath: "", expectedStatus: "200-299", expectedResponseContains: "", commonHeaders: {} },
    authMode: "OAUTH2_CLIENT",
    authConfig: { mode: "oauth2-client", label: "Legacy OAuth2", tokenUrl: "https://legacy.example/token", clientId: "legacy-client", clientAuth: "client_secret_post", scope: "orders.read", refreshUrl: "", refreshMode: "none", accessTokenPath: "access_token", refreshTokenPath: "", expiresPath: "expires_in", injectionTemplate: "", retryOn401Policy: "", refreshFailurePolicy: "" },
    credentialConfigured: true, grantedScopes: [], policy: {}, status: "UNVERIFIED", createdBy: "user-1", updatedBy: "user-1", lockVersion: 1,
  };
}

function fixture(): CapabilityProvider {
  const authentication = defaultOAuthContract("https://login.example/{{tenantId}}/token");
  authentication.schemes[0].fields.splice(0, 0, { key: "tenantId", label: "Tenant", kind: "TEXT", required: true });
  return {
    id: "provider-1", name: "Orders", kind: "HTTP_OPENAPI", driverKey: "http_openapi", transport: "HTTP",
    endpointConfig: { schemaVersion: 2, serviceBaseUrl: "https://api.example", discovery: { documentUrl: "https://api.example/openapi.json" } },
    driverConfig: { authentication }, discoveryMode: "ON_DEMAND", status: "ACTIVE", createdBy: "user-1", updatedBy: "user-1", lockVersion: 1,
  };
}
