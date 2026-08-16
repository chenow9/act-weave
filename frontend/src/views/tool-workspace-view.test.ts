import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { setI18nLocale } from "../i18n";
import type { ServiceConnection, Tool } from "../types/domain";
import { createTestI18n } from "../test-utils/i18n";
import ToolWorkspaceView from "./ToolWorkspaceView.vue";

const routerPushMock = vi.fn();
const routeState = {
  name: "tool-detail" as string,
  params: { toolId: "tool-valid" } as Record<string, string>,
};

const loadToolVersionsMock = vi.fn();
const loadToolConnectionsMock = vi.fn();

const integrationState = {
  providers: [{ id: "provider-1", name: "订单 OpenAPI" }],
  serviceConnections: [] as ServiceConnection[],
  tools: [] as Tool[],
  toolPageItems: [] as Tool[],
  toolPagination: { page: 1, pageSize: 10, total: 0, pageSizeOptions: [10, 20, 50] },
  toolListQuery: { query: "", page: 1, pageSize: 10 },
  toolListSummary: { total: 0, published: 0, tested: 0, draft: 0, review: 0, disabled: 0 },
  toolPageLoading: false,
  toolPageError: null as string | null,
  toolPageHasLoaded: true,
  toolConnectionsByWorkspace: {} as Record<string, ServiceConnection[]>,
  loading: false,
  loadM2Assets: vi.fn(),
  loadToolPage: vi.fn(),
  loadToolVersions: loadToolVersionsMock,
  loadToolConnections: loadToolConnectionsMock,
  connectionForTool: vi.fn((tool: Tool) => {
    const list = integrationState.toolConnectionsByWorkspace[tool.workspaceId] || integrationState.serviceConnections;
    return list.find((c) => c.id === tool.connectionId);
  }),
};

const workspaceState = {
  items: [
    {
      id: "workspace-1",
      name: "订单空间",
      displayName: "订单空间",
      owner: "Ops",
      mode: "PRODUCTION",
      status: "ACTIVE",
    },
  ],
  activeWorkspaceId: "workspace-1",
  loading: false,
  load: vi.fn(),
  can: vi.fn(() => true),
  roleFor: vi.fn(() => "EDITOR"),
};

vi.mock("vue-router", () => ({
  useRouter: () => ({ push: routerPushMock }),
  useRoute: () => routeState,
  onBeforeRouteLeave: () => undefined,
}));

vi.mock("../stores/tools", () => ({ useToolsStore: () => integrationState }));
vi.mock("../stores/providers", () => ({ useProvidersStore: () => integrationState }));
vi.mock("../stores/connections", () => ({ useConnectionsStore: () => integrationState }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => workspaceState }));
vi.mock("../stores/auth", () => ({
  useAuthStore: () => ({ user: { id: "user-1", platformRole: "PLATFORM_ADMIN", username: "admin" }, loading: false }),
}));
vi.mock("../composables/useModalFocus", () => ({ useModalFocus: () => undefined }));

function makeTool(): Tool {
  return {
    id: "tool-valid",
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId: "connection-1",
    defaultConnectionId: "connection-1",
    name: "更新识别场景",
    slug: "update-scene",
    protocol: "HTTP",
    actionConfig: { method: "PUT", path: "/api/v1/recognition/scenes/{id}" },
    actionConfigSchemaVersion: "http.v1",
    description: "更新识别场景",
    status: "Published",
    capabilityStatus: "ACTIVE",
    versions: [{ id: "v1", checksum: "abc", versionNo: 1, lifecycleStatus: "PUBLISHED" } as Tool["versions"][number]],
    requestParams: [
      { location: "Path", name: "id", type: "string", required: true, description: "场景ID" },
      { location: "Body", name: "id", type: "string", required: false, description: "场景ID，新增时为空。" },
      { location: "Body", name: "sceneName", type: "string", required: true, description: "场景名称。" },
    ],
    responseFields: [{ name: "code", type: "string", description: "响应码" }],
    errorMappings: [],
    runtimePolicy: {
      timeoutMs: 8000,
      retryCount: 1,
      backoffPolicy: "exponential",
      idempotencyPolicy: "safe",
      rateLimitPolicy: "60 rpm",
    },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
  };
}

describe("tool workspace page", () => {
  beforeEach(() => {
    setI18nLocale("zh-CN");
    routeState.name = "tool-detail";
    routeState.params = { toolId: "tool-valid" };
    integrationState.tools = [makeTool()];
    integrationState.toolPageItems = [...integrationState.tools];
    integrationState.serviceConnections = [
      {
        id: "connection-1",
        providerId: "provider-1",
        name: "识别平台",
        environment: "TEST",
        protocol: "HTTP",
        protocolConfig: {
          domain: "https://api.example.com",
          host: "",
          port: "443",
          basePath: "/",
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
      } as ServiceConnection,
    ];
    integrationState.toolConnectionsByWorkspace = { "workspace-1": integrationState.serviceConnections };
    loadToolVersionsMock.mockResolvedValue(makeTool());
    loadToolConnectionsMock.mockResolvedValue(integrationState.serviceConnections);
  });

  it("renders the contract canvas with Path and Body as separate groups", async () => {
    const wrapper = mount(ToolWorkspaceView, {
      attachTo: document.body,
      global: {
        plugins: [createTestI18n("zh-CN")],
        directives: { loading: () => undefined },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ToolTestDialog: { template: "<div />" },
        },
      },
    });
    await flushPromises();

    expect(wrapper.get("h1").text()).toBe("更新识别场景");
    expect(wrapper.findAll("h1")).toHaveLength(1);
    expect(wrapper.text()).toContain("Path 参数");
    expect(wrapper.text()).toContain("请求体");
    expect(wrapper.text()).not.toContain("body_id");
    expect(wrapper.text()).not.toContain("TOOL RUNTIME");
    expect(wrapper.text()).not.toContain("尚未导入契约");
    expect(wrapper.find(".tool-contract-canvas").exists()).toBe(true);
    expect(wrapper.find(".tool-workspace-aside").exists()).toBe(true);
    expect(wrapper.find(".tool-workspace-body").exists()).toBe(true);
    expect(wrapper.find(".tool-workspace-body.is-empty").exists()).toBe(false);
    expect(wrapper.find(".tool-workspace-identity-row").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("Base Path");
    expect(wrapper.text()).toContain("识别平台");
    wrapper.unmount();
  });

  it("uses a single-column facts strip when the contract is empty", async () => {
    const empty = makeTool();
    empty.id = "tool-empty";
    empty.name = "Harbor monthly ops overview";
    empty.description =
      "从业务系统获取本月经营概览。用户问经营概览、管理层四件事、收入对比上月/去年同期、新增客户与流失、华东华南华北西南区域收入、广告费用变化时必须调用。返回数字，禁止编造。";
    empty.requestParams = [];
    empty.responseFields = [];
    empty.actionConfig = { method: "GET", path: "/ops/monthly-overview" };
    integrationState.tools = [empty];
    integrationState.toolPageItems = [empty];
    loadToolVersionsMock.mockResolvedValue(empty);
    routeState.params = { toolId: "tool-empty" };

    const wrapper = mount(ToolWorkspaceView, {
      attachTo: document.body,
      global: {
        plugins: [createTestI18n("zh-CN")],
        directives: { loading: () => undefined },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ToolTestDialog: { template: "<div />" },
        },
      },
    });
    await flushPromises();

    expect(wrapper.get("h1").text()).toBe("Harbor monthly ops overview");
    expect(wrapper.find(".tool-workspace-body.is-empty").exists()).toBe(true);
    expect(wrapper.text()).toContain("无参数接口");
    expect(wrapper.text()).toContain("编辑契约");
    expect(wrapper.text()).not.toContain("尚未导入契约");
    expect(wrapper.find(".tool-workspace-lede").exists()).toBe(true);
    expect(wrapper.text()).toContain("可用");
    expect(wrapper.text()).toContain("测试");
    wrapper.unmount();
  });
});
