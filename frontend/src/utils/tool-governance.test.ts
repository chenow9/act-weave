import { describe, expect, it } from "vitest";

import {
  buildToolPublishChecklist,
  getToolLifecycleStatus,
  getToolRunStatus,
  getToolTestStatus,
} from "./tool-governance";
import type { ServiceConnection, Tool } from "../types/domain";

function makeTool(overrides: Partial<Tool> = {}): Tool {
  return {
    id: "tool-1",
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    defaultConnectionId: "connection-1",
    name: "查询订单",
    slug: "query-order",
    protocol: "HTTP",
    actionConfig: { method: "GET", path: "/orders/{orderId}" },
    actionConfigSchemaVersion: "http.action.v1",
    description: "查询订单状态",
    status: "Draft",
    capabilityStatus: "ACTIVE",
    versions: [],
    requestParams: [
      {
        location: "Path",
        name: "orderId",
        type: "string",
        required: true,
        description: "订单 ID",
      },
    ],
    responseFields: [
      {
        name: "status",
        type: "string",
        description: "订单状态",
      },
    ],
    errorMappings: [],
    runtimePolicy: {
      timeoutMs: 8000,
      retryCount: 2,
      backoffPolicy: "exponential",
      idempotencyPolicy: "safe",
      rateLimitPolicy: "60 rpm",
    },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
    ...overrides,
  };
}

function makeConnection(overrides: Partial<ServiceConnection> = {}): ServiceConnection {
  return {
    id: "connection-1",
    name: "订单 API",
    environment: "生产",
    protocol: "HTTP",
    protocolConfig: {
      domain: "https://api.example.com",
      host: "",
      port: "",
      basePath: "/api",
      verificationMethod: "GET",
      verificationPath: "/health",
      expectedStatus: "200-299",
      expectedResponseContains: "",
      commonHeaders: {},
    },
    protocolSchema: "http.connection.v1",
    authConfig: {
      mode: "fixed-token",
      label: "固定 Token",
      tokenUrl: "",
      refreshUrl: "",
      refreshMode: "none",
      accessTokenPath: "",
      refreshTokenPath: "",
      expiresPath: "",
      injectionTemplate: "",
      retryOn401Policy: "",
      refreshFailurePolicy: "",
    },
    status: "Available",
    associatedToolCount: 1,
    ...overrides,
  };
}

describe("tool governance helpers", () => {
  it("treats backend TESTED/PUBLISHED lifecycle as proof of a passing exact-version test", () => {
    expect(getToolTestStatus(makeTool({ status: "Tested", lastTestResult: undefined })).label).toBe("测试通过");
    expect(getToolTestStatus(makeTool({ status: "Published", lastTestResult: undefined })).label).toBe("测试通过");
  });

  it("separates lifecycle, test, and run status instead of deriving one mixed tag", () => {
    const tool = makeTool({
      status: "Published",
      lastTestResult: {
        toolId: "tool-1",
        status: "Failed",
        connectivityPassed: false,
        responseSchemaPassed: true,
        errorMappingPassed: true,
        runtimePolicyPassed: true,
        rawPayloadObjectAddress: "",
        latencyMs: 72,
      },
    });

    expect(getToolLifecycleStatus(tool).label).toBe("已发布");
    expect(getToolTestStatus(tool).label).toBe("测试失败");
    expect(getToolRunStatus(tool, makeConnection({ status: "Needs attention" })).label).toBe("连接需处理");
  });

  it("builds a publish checklist with blocking errors and non-blocking warnings", () => {
    const checklist = buildToolPublishChecklist(
      makeTool({
        actionConfig: { method: "GET", path: "/orders/{orderId}/{missingId}" },
        requestParams: [
          {
            location: "Path",
            name: "orderId",
            type: "string",
            required: true,
            description: "订单 ID",
          },
        ],
      }),
      makeConnection(),
      { agentImpactConfirmed: false },
    );

    expect(checklist.some((item) => item.severity === "error" && item.id === "path-params-match")).toBe(true);
    expect(checklist.some((item) => item.severity === "warning" && item.id === "agent-impact-confirmed")).toBe(true);
  });
});
