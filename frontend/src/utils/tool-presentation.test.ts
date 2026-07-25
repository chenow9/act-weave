import { describe, expect, it } from "vitest";

import type { CapabilityProvider, Tool } from "../types/domain";
import { getToolProtocolLabel, getToolTypeLabel } from "./tool-presentation";

function tool(overrides: Partial<Tool> = {}): Tool {
  return {
    id: "tool-1",
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    name: "查询订单",
    slug: "query-order",
    protocol: "HTTP",
    actionConfig: {},
    actionConfigSchemaVersion: "http.action.v1",
    description: "查询订单状态",
    status: "Published",
    capabilityStatus: "ACTIVE",
    versions: [],
    requestParams: [],
    responseFields: [],
    errorMappings: [],
    runtimePolicy: {
      timeoutMs: 3000,
      retryCount: 1,
      backoffPolicy: "fixed",
      idempotencyPolicy: "safe",
      rateLimitPolicy: "60 rpm",
    },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

const openAPIProvider = { kind: "HTTP_OPENAPI" } as CapabilityProvider;

describe("tool presentation", () => {
  it("maps HTTP and Workflow executors to the prototype Tool types", () => {
    expect(getToolTypeLabel(tool())).toBe("HTTP Tool");
    expect(getToolTypeLabel(tool({ protocol: "WORKFLOW" }))).toBe("Workflow Tool");
  });

  it("uses explicit protocol versions before the OpenAPI provider fallback", () => {
    expect(getToolProtocolLabel(tool({ actionConfig: { openapiVersion: "3.1" } }), openAPIProvider)).toBe("OpenAPI 3.1");
    expect(getToolProtocolLabel(tool(), openAPIProvider)).toBe("OpenAPI 3.0");
    expect(getToolProtocolLabel(tool({ protocol: "WORKFLOW" }), openAPIProvider)).toBe("Internal");
  });
});
