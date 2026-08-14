import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { setI18nLocale } from "../i18n";
import type { ServiceConnection, Tool } from "../types/domain";
import { createTestI18n } from "../test-utils/i18n";
import ToolsView from "./ToolsView.vue";

const routerPushMock = vi.fn();
const loadM2AssetsMock = vi.fn();
const loadToolPageMock = vi.fn();
const loadWorkspacesMock = vi.fn();
const loadAgentsMock = vi.fn();

const integrationState = {
  providers: [{ id: "provider-1", name: "订单 OpenAPI" }],
  serviceConnections: [] as ServiceConnection[],
  tools: [] as Tool[],
  toolPageItems: [] as Tool[],
  toolPagination: { page: 1, pageSize: 10, total: 0, pageSizeOptions: [10, 20, 50] },
  toolListQuery: { query: "", page: 1, pageSize: 10 } as Record<string, unknown>,
  toolListSummary: { total: 0, published: 0, tested: 0, draft: 0, review: 0, disabled: 0 },
  toolPageLoading: false,
  toolPageError: null as string | null,
  toolPageHasLoaded: true,
  toolConnectionsByWorkspace: {} as Record<string, ServiceConnection[]>,
  protocols: [],
  openAPIImports: [],
  verificationByConnectionId: {},
  loading: false,
  loadM2Assets: loadM2AssetsMock,
  loadToolPage: loadToolPageMock,
  loadToolVersions: vi.fn(
    async (id: string) =>
      integrationState.tools.find((t) => t.id === id) || integrationState.toolPageItems.find((t) => t.id === id),
  ),
  connectionForTool: vi.fn((tool: Tool) => {
    const list = integrationState.toolConnectionsByWorkspace[tool.workspaceId] || integrationState.serviceConnections;
    return list.find((c) => c.id === tool.connectionId);
  }),
  deleteTool: vi.fn(),
  publishTool: vi.fn(),
  updateTool: vi.fn(),
  createTool: vi.fn(),
  testTool: vi.fn(),
  testToolWithOutbound: vi.fn(),
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
      defaultAgentId: "agent-1",
      modelConfigId: "model-1",
      healthScore: 100,
      toolCount: 2,
      workflowCount: 1,
      agentCount: 1,
    },
  ],
  activeWorkspaceId: "workspace-1",
  loading: false,
  load: loadWorkspacesMock,
  can: vi.fn(() => true),
  roleFor: vi.fn(() => "EDITOR"),
};

const agentState = {
  items: [
    {
      id: "agent-1",
      workspaceId: "workspace-1",
      name: "订单 Agent",
      roleDescription: "处理订单",
      modelConfigId: "model-1",
      systemPrompt: "You are helpful",
      isDefault: true,
      status: "ACTIVE",
      statusSource: "manual",
      toolsCount: 2,
      workflowsCount: 1,
    },
  ],
  selectedAgentId: "agent-1",
  loading: false,
  loadAgents: loadAgentsMock,
};

vi.mock("vue-router", () => ({
  useRouter: () => ({ push: routerPushMock }),
}));

vi.mock("../stores/tools", () => ({
  useToolsStore: () => integrationState,
}));
vi.mock("../stores/providers", () => ({
  useProvidersStore: () => integrationState,
}));
vi.mock("../stores/connections", () => ({
  useConnectionsStore: () => integrationState,
}));

vi.mock("../stores/workspaces", () => ({
  useWorkspaceStore: () => workspaceState,
}));

vi.mock("../stores/agents", () => ({
  useAgentStore: () => agentState,
}));

vi.mock("../stores/auth", () => ({
  useAuthStore: () => ({
    user: { id: "user-1", platformRole: "PLATFORM_ADMIN", username: "admin" },
    loading: false,
  }),
}));

vi.mock("../composables/useModalFocus", () => ({
  useModalFocus: () => undefined,
}));

function makeConnection(id = "connection-1"): ServiceConnection {
  return {
    id,
    providerId: "provider-1",
    name: "昆仑平台",
    environment: "生产",
    protocol: "HTTP",
    protocolConfig: {
      domain: "https://api.example.com",
      host: "",
      port: "443",
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
  };
}

function makeTool(id: string, connectionId: string, name = id): Tool {
  return {
    id,
    workspaceId: "workspace-1",
    providerId: "provider-1",
    connectionId,
    defaultConnectionId: connectionId,
    name,
    slug: id,
    protocol: "HTTP",
    actionConfig: { method: "GET", path: "/orders/{orderId}" },
    actionConfigSchemaVersion: "http.action.v1",
    description: "查询订单",
    status: "Review",
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
    lastTestResult: {
      id: `test-${id}`,
      status: "FAILED",
      connectivityPassed: true,
      responseSchemaPassed: false,
      errorMappingPassed: true,
      runtimePolicyPassed: true,
      requestSummary: {},
      responseSummary: {},
      latencyMs: 10,
      testedBy: "user-1",
      testedAt: "2026-07-15T00:00:00Z",
    },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
  };
}

function mountToolsView() {
  return mount(ToolsView, {
    attachTo: document.body,
    global: {
      plugins: [createTestI18n("zh-CN")],
      directives: {
        loading: () => undefined,
      },
      stubs: {
        AppSelect: {
          props: ["modelValue", "options"],
          emits: ["update:modelValue"],
          template:
            "<select class='app-select-stub' :value='modelValue' @change=\"$emit('update:modelValue', $event.target.value)\"><option v-for='option in options' :key='option.value' :value='option.value'>{{ option.label }}</option></select>",
        },
        ToolSchemaTreeView: { template: "<div class='schema-view-stub' />" },
        ToolTestDialog: { template: "<div class='tool-test-dialog-stub' />" },
      },
    },
  });
}

async function triggerToolMenuAction(wrapper: ReturnType<typeof mountToolsView>, actionKey: string, rowIndex = 0) {
  await wrapper.findAll('button[aria-label="更多工具操作"]')[rowIndex].trigger("click");
  await wrapper.vm.$nextTick();
  const action = document.body.querySelector<HTMLButtonElement>(
    `[role="menu"][aria-label="更多工具操作"] button[data-action-key="${actionKey}"]`,
  );
  expect(action).not.toBeNull();
  action!.click();
  await wrapper.vm.$nextTick();
}

describe("tools view detail behavior", () => {
  beforeEach(() => {
    setI18nLocale("zh-CN");
    document.body.innerHTML = "";
    vi.clearAllMocks();
    workspaceState.items = [
      {
        id: "workspace-1",
        name: "订单空间",
        displayName: "订单空间",
        owner: "Ops",
        mode: "PRODUCTION",
        status: "ACTIVE",
        defaultAgentId: "agent-1",
        modelConfigId: "model-1",
        healthScore: 100,
        toolCount: 2,
        workflowCount: 1,
        agentCount: 1,
      },
    ];
    workspaceState.activeWorkspaceId = "workspace-1";
    integrationState.serviceConnections = [makeConnection()];
    integrationState.tools = [
      makeTool("tool-missing-connection", "missing-connection", "缺失连接 Tool"),
      makeTool("tool-valid", "connection-1", "有效连接 Tool"),
    ];
    integrationState.toolPageItems = [...integrationState.tools];
    integrationState.toolPagination = {
      page: 1,
      pageSize: 10,
      total: integrationState.tools.length,
      pageSizeOptions: [10, 20, 50],
    };
    integrationState.toolListSummary = {
      total: integrationState.tools.length,
      published: integrationState.tools.filter((t) => t.status === "Published").length,
      tested: integrationState.tools.filter((t) => t.status === "Tested").length,
      draft: integrationState.tools.filter((t) => t.status === "Draft").length,
      review: integrationState.tools.filter((t) => t.status === "Review").length,
      disabled: integrationState.tools.filter((t) => t.status === "Disabled").length,
    };
    integrationState.toolConnectionsByWorkspace = {
      "workspace-1": integrationState.serviceConnections,
    };
    loadM2AssetsMock.mockResolvedValue(undefined);
    loadToolPageMock.mockResolvedValue(integrationState.toolPageItems);
    loadWorkspacesMock.mockResolvedValue(undefined);
    loadAgentsMock.mockResolvedValue(agentState.items);
  });

  it("shows the shared product empty state instead of a developer guard when no Workspace is available", async () => {
    workspaceState.items = [];
    workspaceState.activeWorkspaceId = "";
    const wrapper = mountToolsView();
    await flushPromises();

    expect(wrapper.get(".workspace-context-state").text()).toContain("还没有可用的业务空间");
    expect(wrapper.text()).not.toContain("Select a Workspace");
    expect(wrapper.get(".tool-header-primary").attributes("disabled")).toBeDefined();
    expect(loadM2AssetsMock).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it("loads the Tool registry through the server page contract", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    expect(loadToolPageMock).toHaveBeenCalledWith({
      query: "",
      status: undefined,
      type: undefined,
      page: 1,
      pageSize: 10,
    });
    wrapper.unmount();
  });

  it("aligns 需处理 KPI with connection attention and uses scheme-A dual status cell", async () => {
    integrationState.tools = [
      { ...makeTool("tool-published-broken", "missing-connection", "已发布坏连接"), status: "Published" },
      { ...makeTool("tool-published-ok", "connection-1", "已发布好连接"), status: "Published" },
      { ...makeTool("tool-review", "connection-1", "待评审 Tool"), status: "Review" },
    ];
    integrationState.toolPageItems = [...integrationState.tools];
    integrationState.toolPagination = { page: 1, pageSize: 10, total: 3, pageSizeOptions: [10, 20, 50] };
    integrationState.toolListSummary = { total: 3, published: 2, tested: 0, draft: 0, review: 1, disabled: 0 };
    integrationState.toolConnectionsByWorkspace = {
      "workspace-1": integrationState.serviceConnections,
    };

    const wrapper = mountToolsView();
    await flushPromises();

    const summary = wrapper.get(".management-summary-strip").text();
    // Missing connection on first published tool => 需处理 counts connection issues (1), not Review (1).
    expect(summary).toMatch(/工具总数\s*3/);
    expect(summary).toMatch(/已发布\s*2/);
    expect(summary).toMatch(/需处理\s*1/);
    expect(wrapper.find(".tool-connection-alert").exists()).toBe(false);

    // Scheme A: lifecycle pill stays short; connection problem is a secondary line.
    const statusCells = wrapper.findAll('td[data-column-key="status"] .tool-unified-status-cell');
    expect(statusCells.length).toBeGreaterThan(0);
    const brokenCell = statusCells.find(
      (cell) => cell.text().includes("连接缺失") || cell.text().includes("连接需处理"),
    );
    expect(brokenCell).toBeTruthy();
    expect(brokenCell!.find(".tool-status-pill").text()).toContain("已发布");
    expect(brokenCell!.find(".tool-status-attention").text()).toMatch(/连接/);
    expect(brokenCell!.find(".tool-status-pill").text()).not.toContain("·");

    loadToolPageMock.mockClear();
    await wrapper.get('button[aria-label="工具状态筛选"]').trigger("click");
    await wrapper.get('button[role="option"][value="attention"]').trigger("click");
    await flushPromises();
    expect(loadToolPageMock).toHaveBeenLastCalledWith({
      query: "",
      status: "attention",
      type: undefined,
      page: 1,
      pageSize: 10,
    });
    wrapper.unmount();
  });

  it("sorts from page one while retaining the Tool page size and filters", async () => {
    integrationState.toolPagination = { page: 3, pageSize: 20, total: 60, pageSizeOptions: [10, 20, 50] };
    const wrapper = mountToolsView();
    await flushPromises();
    loadToolPageMock.mockClear();

    await wrapper.get('button[aria-label="按工具名称升序排序"]').trigger("click");
    await flushPromises();

    expect(loadToolPageMock).toHaveBeenLastCalledWith({
      query: "",
      status: undefined,
      type: undefined,
      page: 1,
      pageSize: 20,
      sortBy: "name",
      sortOrder: "asc",
    });
    wrapper.unmount();
  });

  it("renders a menu-only overflow with tool actions in semantic order", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    const actionCell = wrapper.get('td[data-column-key="actions"]');
    expect(actionCell.findAll('button[data-action-kind="primary"]')).toHaveLength(0);
    expect(actionCell.get('button[aria-label="更多工具操作"]').exists()).toBe(true);

    expect(wrapper.find('.management-filter-select[aria-label="按 Agent 筛选工具"]').exists()).toBe(false);
    await actionCell.get('button[aria-label="更多工具操作"]').trigger("click");
    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多工具操作"]');
    expect(menu).not.toBeNull();
    expect(Array.from(menu!.querySelectorAll("button")).map((button) => button.dataset.actionKey)).toEqual([
      "detail",
      "test",
      "edit",
      "publish",
      "availability",
      "delete",
    ]);
    expect(menu!.querySelector<HTMLButtonElement>('button[data-action-key="publish"]')?.disabled).toBe(true);
    wrapper.unmount();
  });

  it("renders compact two-line Tool identity cells and controlled row checkboxes", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    expect(wrapper.findAll("tbody tr").every((row) => !row.attributes("role"))).toBe(true);
    expect(wrapper.get(".tool-entity-copy strong").attributes("title")).toBe("缺失连接 Tool");
    expect(wrapper.get(".tool-entity-copy small").attributes("title")).toBe("查询订单");
    expect(wrapper.findAll('.data-table-checkbox[aria-label^="选择 "]')).toHaveLength(2);
    await wrapper.get('.data-table-checkbox[aria-label="选择 缺失连接 Tool"]').setValue(true);
    const batchBar = wrapper.get(".management-list-batch-bar");
    expect(batchBar.text()).toContain("已选 1 项");
    expect(batchBar.text()).toContain("批量测试");
    expect(batchBar.text()).toContain("批量删除");
    wrapper.unmount();
  });

  it("applies the prototype Tool type filter through the shared page query contract", async () => {
    const wrapper = mountToolsView();
    await flushPromises();
    loadToolPageMock.mockClear();

    await wrapper.get('button[aria-label="工具类型筛选"]').trigger("click");
    await wrapper.get('button[role="option"][value="Workflow Tool"]').trigger("click");
    await flushPromises();

    expect(loadToolPageMock).toHaveBeenLastCalledWith({
      query: "",
      status: undefined,
      type: "Workflow Tool",
      page: 1,
      pageSize: 10,
    });
    wrapper.unmount();
  });

  it("keeps destructive Tool actions behind the existing confirmation dialog", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    await wrapper.get('button[aria-label="更多工具操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="delete"]')!.click();
    await wrapper.vm.$nextTick();

    const modal = wrapper.get(".tool-risk-confirmation-modal");
    expect(modal.text()).toContain("确认删除 Tool");
    expect(modal.text()).not.toContain("Risk Control");
    expect(modal.text()).toMatch(/操作确认|高风险操作/);
    expect(modal.text()).toContain("Agent 绑定");
    expect(modal.text()).not.toContain("由发布态 Release 解析");
    wrapper.unmount();
  });

  it("does not show a different service connection when the tool connection is missing", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    await triggerToolMenuAction(wrapper, "detail");
    await wrapper.find("#tool-detail-tab-connection").trigger("click");

    expect(wrapper.find("#tool-detail-panel-connection").text()).toContain("服务连接未找到");
    expect(wrapper.find("#tool-detail-panel-connection").text()).not.toContain("昆仑平台");
    expect(wrapper.find(".tool-detail-maintenance-action").exists()).toBe(true);
    wrapper.unmount();
  });

  it("keeps every tab aria-controls target present while supporting keyboard tab changes", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    await triggerToolMenuAction(wrapper, "detail");
    const tabs = wrapper.findAll('[role="tab"]');
    expect(tabs).toHaveLength(6);

    for (const tab of tabs) {
      const panelId = tab.attributes("aria-controls");
      expect(panelId).toBeTruthy();
      expect(wrapper.find(`#${panelId}`).exists()).toBe(true);
    }

    await wrapper.find("#tool-detail-tab-base").trigger("keydown", { key: "ArrowRight" });
    expect(wrapper.find("#tool-detail-tab-connection").attributes("aria-selected")).toBe("true");

    await wrapper.find("#tool-detail-tab-connection").trigger("keydown", { key: "End" });
    expect(wrapper.find("#tool-detail-tab-test").attributes("aria-selected")).toBe("true");
    wrapper.unmount();
  });

  it("preserves the active detail tab when opening tools consecutively", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    await triggerToolMenuAction(wrapper, "detail", 0);
    await wrapper.find("#tool-detail-tab-test").trigger("click");
    await wrapper.find('button[aria-label="关闭工具详情"]').trigger("click");
    await triggerToolMenuAction(wrapper, "detail", 1);

    expect(wrapper.find("#tool-detail-tab-test").attributes("aria-selected")).toBe("true");
    wrapper.unmount();
  });

  it("opens the tool editor when 编辑工具 is chosen from the row menu", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    await triggerToolMenuAction(wrapper, "edit", 1);

    const editor = wrapper.find('.tool-editor-modal-card[role="dialog"]');
    expect(editor.exists()).toBe(true);
    expect(editor.attributes("aria-label") || editor.text()).toMatch(/编辑|有效连接 Tool/);
    expect(wrapper.find(".tool-hybrid-topbar").exists()).toBe(true);
    expect(wrapper.find(".tool-hybrid-step-panel").exists()).toBe(true);
    wrapper.unmount();
  });

  it("still opens the editor when the tool payload is incomplete", async () => {
    integrationState.tools = [
      {
        ...makeTool("tool-partial", "connection-1", "残缺 Tool"),
        actionConfig: undefined as unknown as Tool["actionConfig"],
        runtimePolicy: undefined as unknown as Tool["runtimePolicy"],
        requestParams: undefined as unknown as Tool["requestParams"],
        responseFields: undefined as unknown as Tool["responseFields"],
        errorMappings: undefined as unknown as Tool["errorMappings"],
      },
    ];
    integrationState.toolPageItems = [...integrationState.tools];
    integrationState.toolPagination = { page: 1, pageSize: 10, total: 1, pageSizeOptions: [10, 20, 50] };

    const wrapper = mountToolsView();
    await flushPromises();

    await triggerToolMenuAction(wrapper, "edit", 0);

    expect(wrapper.find('.tool-editor-modal-card[role="dialog"]').exists()).toBe(true);
    expect(wrapper.text()).not.toContain("无法打开编辑");
    wrapper.unmount();
  });

  it("styles detail tabs as an active segmented control, not bare native buttons", async () => {
    const wrapper = mountToolsView();
    await flushPromises();

    await triggerToolMenuAction(wrapper, "detail", 0);
    const tabs = wrapper.find(".tool-detail-tabs");
    expect(tabs.exists()).toBe(true);
    expect(tabs.findAll('[role="tab"]')).toHaveLength(6);
    expect(wrapper.find("#tool-detail-tab-base").classes()).toContain("active");
    expect(wrapper.find(".tool-detail-modal-body").exists()).toBe(true);
    expect(wrapper.find('[data-status-layer="lifecycle"]').exists()).toBe(true);
    expect(wrapper.find('[data-status-layer="test"]').exists()).toBe(true);
    expect(wrapper.find('[data-status-layer="run"]').exists()).toBe(true);
    wrapper.unmount();
  });
});
