import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ToolTestDialog from "./ToolTestDialog.vue";
import type { Tool, ToolVersion } from "../types/domain";

const testToolMock = vi.fn();

vi.mock("../stores/tools", () => ({
  useToolsStore: () => ({
    testTool: testToolMock,
    testToolWithOutbound: vi.fn(),
  }),
}));

vi.mock("../stores/connections", () => ({
  useConnectionsStore: () => ({
    serviceConnections: [],
  }),
}));

function makeTool(): Tool {
  return {
    id: "tool-delete-region",
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    defaultConnectionId: "connection-1",
    name: "删除区域",
    slug: "delete-region",
    protocol: "HTTP",
    actionConfig: {
      method: "DELETE",
      path: "/api/inspection-area/region/{ids}",
    },
    actionConfigSchemaVersion: "http.action.v1",
    description: "删除区域",
    status: "Draft",
    capabilityStatus: "ACTIVE",
    versions: [],
    requestParams: [
      {
        location: "Path",
        name: "ids",
        type: "string",
        required: true,
        description: "区域 ID",
      },
    ],
    responseFields: [],
    errorMappings: [],
    runtimePolicy: {
      timeoutMs: 8000,
      retryCount: 1,
      backoffPolicy: "fixed",
      idempotencyPolicy: "none",
      rateLimitPolicy: "60 rpm",
    },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
  };
}

function mountDialog(tool = makeTool()) {
  return mount(ToolTestDialog, {
    attachTo: document.body,
    props: {
      modelValue: true,
      tool,
    },
  });
}

describe("tool test dialog behavior", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    document.body.innerHTML = "";
  });

  it("moves focus into the dialog and closes on Escape", async () => {
    const wrapper = mountDialog();
    await flushPromises();

    expect(document.activeElement).toBe(wrapper.get("[data-modal-initial-focus]").element);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();

    expect(wrapper.emitted("update:modelValue")).toEqual([[false]]);
    wrapper.unmount();
  });

  it("ignores rapid duplicate test clicks while a request is running", async () => {
    let resolveTest: (value: unknown) => void = () => undefined;
    testToolMock.mockReturnValue(
      new Promise((resolve) => {
        resolveTest = resolve;
      }),
    );
    const wrapper = mountDialog();
    await flushPromises();

    const runButton = wrapper.get(".tool-test-dialog-actions .primary-button");
    await runButton.trigger("click");
    await runButton.trigger("click");

    expect(testToolMock).toHaveBeenCalledTimes(1);

    resolveTest({
      tool: makeTool(),
      testResult: {
        toolId: "tool-delete-region",
        status: "Failed",
        connectivityPassed: false,
        responseSchemaPassed: false,
        errorMappingPassed: true,
        runtimePolicyPassed: true,
        rawPayloadObjectAddress: "",
        latencyMs: 10,
      },
      requestInput: { ids: "" },
      responseStatus: 401,
      responseBody: { code: "A0230", msg: "访问令牌无效或已过期" },
      latencyMs: 10,
      passed: false,
      errorMessage: "upstream returned HTTP 401",
    });
    await flushPromises();

    expect(wrapper.text()).toContain("服务连接凭证无效或已过期");
    expect(wrapper.text()).toContain("访问令牌无效或已过期");
    wrapper.unmount();
  });

  it("explains that a published-only Tool needs a new Draft before testing", async () => {
    const tool = makeTool();
    tool.versions = [{ lifecycleStatus: "PUBLISHED" } as ToolVersion];
    const wrapper = mountDialog(tool);
    await flushPromises();

    const runButton = wrapper.get(".tool-test-dialog-actions .primary-button");
    expect(runButton.attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("请先编辑 Tool 创建新的 Draft Version");
    await runButton.trigger("click");
    expect(testToolMock).not.toHaveBeenCalled();

    wrapper.unmount();
  });
});
