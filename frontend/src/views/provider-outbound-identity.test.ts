import { describe, expect, it } from "vitest";

import { buildOutboundIdentityContract } from "./provider-outbound-identity";

const base = {
  supportBrokerObo: false,
  supportRequestPassthrough: false,
  brokerTokenEndpoint: "https://broker.example.com/oauth/token",
  brokerAudience: "api://orders",
  brokerAllowedScopes: "orders.read",
  businessInjectionHeader: "Authorization",
  businessInjectionPrefix: "Bearer",
};

describe("buildOutboundIdentityContract T3 fail-closed", () => {
  it("throws on zero modes and never invents a silent passthrough payload", () => {
    expect(() => buildOutboundIdentityContract({ ...base })).toThrow(/at least one supportedMode/);
    let returned: Record<string, unknown> | undefined;
    try {
      returned = buildOutboundIdentityContract({ ...base });
    } catch {
      returned = undefined;
    }
    expect(returned).toBeUndefined();
  });

  it("serializes passthrough-only and dual-mode DTO without extra modes", () => {
    expect(buildOutboundIdentityContract({ ...base, supportRequestPassthrough: true })).toMatchObject({
      schemaVersion: "outbound-identity.v1",
      supportedModes: ["REQUEST_PASSTHROUGH"],
      supportedSubjectTypes: ["USER"],
      requestPassthrough: { credentialTypes: ["ACCESS_TOKEN"] },
    });
    expect(buildOutboundIdentityContract({
      ...base,
      supportBrokerObo: true,
      supportRequestPassthrough: true,
    })).toMatchObject({
      supportedModes: ["BROKER_OBO", "REQUEST_PASSTHROUGH"],
      brokerObo: expect.objectContaining({
        machineAuthMethod: "private_key_jwt",
        allowedScopes: ["orders.read"],
      }),
      requestPassthrough: expect.objectContaining({ credentialTypes: ["ACCESS_TOKEN"] }),
    });
  });

  it("serializes broker-only without requestPassthrough block", () => {
    const identity = buildOutboundIdentityContract({ ...base, supportBrokerObo: true });
    expect(identity.supportedModes).toEqual(["BROKER_OBO"]);
    expect(identity.requestPassthrough).toBeUndefined();
    expect(identity.brokerObo).toBeDefined();
  });
});
