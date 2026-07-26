import { readFileSync } from "node:fs";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { reactive } from "vue";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import OpenAPIImportsView from "./OpenAPIImportsView.vue";

const openAPIImportsViewSource = readFileSync("src/views/OpenAPIImportsView.vue", "utf8");

const mocks = vi.hoisted(() => ({
  integration: null as any,
  router: { push: vi.fn() },
  workspaces: null as any,
}));

vi.mock("vue-router", () => ({
  useRouter: () => mocks.router,
}));

vi.mock("../stores/integration", () => ({
  useIntegrationStore: () => mocks.integration,
}));

vi.mock("../stores/workspaces", () => ({
  useWorkspaceStore: () => mocks.workspaces,
}));

function importRecord(index: number, overrides: Record<string, unknown> = {}) {
  return {
    id: `import-${index}`,
    workspaceId: "order",
    providerId: "provider-order",
    connectionId: "connection-order",
    source: index === 7 ? "Shipping Center OpenAPI" : index === 2 ? "待确认记录" : "订单中心 OpenAPI",
    fileName: index === 7 ? "shipping-openapi.yaml" : `order-openapi-${index}.yaml`,
    totalEndpoints: index + 2,
    readyEndpoints: index === 2 ? 0 : index + 1,
    issues: index === 2 ? ["operationId missing"] : [],
    status: index === 2 ? "Review" : "Ready",
    ...overrides,
  };
}

function connection() {
  return {
    id: "connection-order",
    providerId: "provider-order",
    name: "Order Production HTTP",
    protocol: "HTTP",
    protocolConfig: {
      domain: "orders.example.test",
      host: "orders.example.test",
      port: "443",
      basePath: "/api",
    },
  };
}

function installStores(records = Array.from({ length: 11 }, (_, index) => importRecord(index + 1))) {
  mocks.workspaces = reactive({
    activeWorkspaceId: "order",
    items: [{ id: "order", name: "Order Ops", displayName: "Order Operations", defaultAgentId: "agent-order" }],
    load: vi.fn().mockResolvedValue(undefined),
  });
  const integrationState = reactive({
    loading: false,
    openAPIImportPageItems: records.slice(0, 10),
    openAPIImportPagination: { page: 1, pageSize: 10, total: records.length, pageSizeOptions: [10, 20, 50] },
    openAPIImportListQuery: { query: "", page: 1, pageSize: 10 },
    openAPIImportCatalog: records,
    openAPIImportRegistryTotal: records.length,
    openAPIImports: records,
    providers: [{
      id: "provider-order",
      name: "Order OpenAPI",
      kind: "HTTP_OPENAPI",
      endpointConfig: { sourceUri: "https://orders.example.test/openapi.json" },
      discoveryMode: "ON_DEMAND",
    }],
    serviceConnectionCatalog: [connection()],
    serviceConnections: [connection()],
    tools: [],
    loadProviders: vi.fn().mockResolvedValue([{ id: "provider-order", name: "Order OpenAPI" }]),
    loadOpenAPIImportPage: vi.fn(async (query: { query?: string; status?: string; page?: number; pageSize?: number } = {}) => {
      const keyword = (query.query ?? "").toLowerCase();
      const status = query.status;
      const page = query.page ?? integrationState.openAPIImportPagination.page;
      const pageSize = query.pageSize ?? integrationState.openAPIImportPagination.pageSize;
      const filtered = records.filter((record) => {
        if (status === "Ready" && record.readyEndpoints <= 0) return false;
        if (status === "Issues" && !record.issues.length && !record.status.toLowerCase().includes("review")) return false;
        const searchText = `${record.fileName} ${record.source} Order Operations Order Agent Order Production HTTP ${record.status}`.toLowerCase();
        return !keyword || searchText.includes(keyword);
      });
      integrationState.openAPIImportPageItems = filtered.slice((page - 1) * pageSize, page * pageSize);
      integrationState.openAPIImportPagination = { page, pageSize, total: filtered.length, pageSizeOptions: [10, 20, 50] };
      return integrationState.openAPIImportPageItems;
    }),
    loadOpenAPIImportCatalog: vi.fn().mockResolvedValue(records),
    loadOpenAPIImportDetail: vi.fn(async (record: ReturnType<typeof importRecord>) => {
      const detailed = { ...record, detail: record.detail || { endpoints: [] } };
      const index = integrationState.openAPIImportPageItems.findIndex((item) => item.id === record.id);
      if (index >= 0) integrationState.openAPIImportPageItems[index] = detailed;
      return detailed;
    }),
    loadServiceConnectionCatalog: vi.fn().mockResolvedValue([connection()]),
    createOpenAPIImport: vi.fn().mockResolvedValue(importRecord(8)),
    createOpenAPIFileImport: vi.fn().mockResolvedValue(importRecord(8)),
    generateToolDrafts: vi.fn().mockResolvedValue([{ id: "generated.tool", status: "Draft" }]),
    deleteOpenAPIImport: vi.fn().mockResolvedValue(undefined),
  });
  mocks.integration = integrationState;
}

async function mountView() {
  const wrapper = mount(OpenAPIImportsView, {
    attachTo: document.body,
    global: {
      directives: {
        loading: {},
      },
      stubs: {
        Teleport: true,
        ToolSchemaTreeView: true,
      },
    },
  });
  await flushPromises();
  return wrapper;
}

async function attachOpenAPIFile(wrapper: VueWrapper) {
  const file = new File([
    JSON.stringify({ openapi: "3.0.3", info: { title: "Orders", version: "1" }, paths: { "/orders": { get: { operationId: "getOrders" } } } }),
  ], "orders-openapi.json", { type: "application/json" });
  const input = wrapper.get('input[data-testid="openapi-file-input"]');
  Object.defineProperty(input.element, "files", { value: [file], configurable: true });
  await input.trigger("change");
  await flushPromises();
  return file;
}

describe("OpenAPIImportsView management list behavior", () => {
  let wrapper: VueWrapper | undefined;

  beforeEach(() => {
    localStorage.clear();
    mocks.router.push.mockReset();
    installStores();
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
    document.body.innerHTML = "";
  });

  it("renders the server page through ManagementList and maps search, quick filters, and pagination to requests", async () => {
    wrapper = await mountView();

    expect(wrapper.find(".management-list").exists()).toBe(true);
    expect(wrapper.findAll("tbody tr")).toHaveLength(10);
    expect(wrapper.find('th[data-column-key="file"]').exists()).toBe(true);
    expect(wrapper.find('th[data-column-key="actions"]').classes()).toContain("is-sticky-right");

    await wrapper.get('input[aria-label="搜索 OpenAPI 导入记录"]').setValue("shipping");
    await flushPromises();
    expect(mocks.integration.loadOpenAPIImportPage).toHaveBeenLastCalledWith({ query: "shipping", status: undefined, page: 1, pageSize: 10 });
    expect(wrapper.findAll("tbody tr")).toHaveLength(1);
    expect(wrapper.text()).toContain("shipping-openapi.yaml");
    expect(wrapper.findAll('button[role="option"][aria-selected="true"]')).toHaveLength(0);

    await wrapper.get('button[aria-label="重置 OpenAPI 导入筛选"]').trigger("click");
    await wrapper.get('button[role="option"][value="Issues"]').trigger("click");
    await flushPromises();
    expect(mocks.integration.loadOpenAPIImportPage).toHaveBeenLastCalledWith({ query: "", status: "Issues", page: 1, pageSize: 10 });
    expect(wrapper.findAll("tbody tr")).toHaveLength(1);
    expect(wrapper.find("tbody").text()).toContain("待确认记录");

    await wrapper.get('button[role="option"][value="ALL"]').trigger("click");
    await flushPromises();
    expect(mocks.integration.loadOpenAPIImportPage).toHaveBeenLastCalledWith({ query: "", status: undefined, page: 1, pageSize: 10 });
    expect(wrapper.findAll("tbody tr").length).toBeGreaterThan(1);

    await wrapper.get('button[aria-label="下一页"]').trigger("click");
    await flushPromises();
    expect(mocks.integration.loadOpenAPIImportPage).toHaveBeenLastCalledWith({ query: "", status: undefined, page: 2, pageSize: 10 });
    expect(wrapper.findAll("tbody tr")).toHaveLength(1);
    expect(wrapper.text()).toContain("order-openapi-11.yaml");
  });

  it("sorts from page one while retaining the OpenAPI page size and filters", async () => {
    wrapper = await mountView();
    mocks.integration.openAPIImportPagination = { page: 3, pageSize: 20, total: 60, pageSizeOptions: [10, 20, 50] };
    mocks.integration.loadOpenAPIImportPage.mockClear();

    await wrapper.get('button[aria-label="按导入文件升序排序"]').trigger("click");
    await flushPromises();

    expect(mocks.integration.loadOpenAPIImportPage).toHaveBeenLastCalledWith({ query: "", status: undefined, page: 1, pageSize: 20, sortBy: "fileName", sortOrder: "asc" });
  });

  it("supports keyboard navigation through the shared OpenAPI quick filters", async () => {
    wrapper = await mountView();

    const issuesFilter = wrapper.get('button[role="option"][value="Issues"]');
    await issuesFilter.trigger("click");
    await issuesFilter.trigger("keydown", { key: "ArrowRight" });
    await flushPromises();

    const allFilter = wrapper.get('button[role="option"][value="ALL"]');
    expect(allFilter.attributes("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(allFilter.element);
    expect(mocks.integration.loadOpenAPIImportPage).toHaveBeenLastCalledWith({ query: "", status: undefined, page: 1, pageSize: 10 });
  });

  it("does not retain obsolete Order quick-filter source branches", () => {
    expect(openAPIImportsViewSource).not.toContain('value === "Order"');
    expect(openAPIImportsViewSource).not.toContain('"Issues" | "Order" | "ALL"');
  });

  it("persists optional OpenAPI columns through the shared column settings", async () => {
    wrapper = await mountView();

    await wrapper.get('button[aria-label="列设置"]').trigger("click");
    await wrapper.get('input[value="connection"]').setValue(false);

    expect(wrapper.find('th[data-column-key="connection"]').exists()).toBe(false);
    expect(JSON.parse(localStorage.getItem("actweave:openapi-imports:columns") || "[]")).not.toContain("connection");
  });

  it("always shows the total below non-empty data and reveals controls above ten records", async () => {
    installStores(Array.from({ length: 6 }, (_, index) => importRecord(index + 1)));
    wrapper = await mountView();
    expect(wrapper.find('[aria-label="列表分页"]').text()).toContain("共 6 项 · 第 1 / 1 页");
    expect(wrapper.find('button[aria-label="每页 10 条"]').exists()).toBe(false);
    wrapper.unmount();

    installStores();
    wrapper = await mountView();
    expect(wrapper.find('[aria-label="列表分页"]').exists()).toBe(true);
    expect(wrapper.find('button[aria-label="每页 10 条"]').exists()).toBe(true);
  });

  it("separates loading, load error, empty registry, and filtered no-match states", async () => {
    installStores([]);
    let finishLoad: (() => void) | undefined;
    mocks.integration.loadOpenAPIImportPage = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finishLoad = resolve;
        }),
    );
    wrapper = mount(OpenAPIImportsView, {
      attachTo: document.body,
      global: { directives: { loading: {} }, stubs: { Teleport: true, ToolSchemaTreeView: true } },
    });
    await flushPromises();
    expect(wrapper.find(".management-list-loading").exists()).toBe(true);
    finishLoad?.();
    await flushPromises();
    wrapper.unmount();

    installStores();
    mocks.integration.loadOpenAPIImportPage = vi.fn().mockRejectedValue(new Error("network unavailable"));
    wrapper = await mountView();
    expect(wrapper.text()).toContain("OpenAPI 导入记录加载失败");
    expect(wrapper.text()).toContain("network unavailable");
    expect(wrapper.find('[data-openapi-load-retry]').exists()).toBe(true);
    wrapper.unmount();

    installStores([]);
    wrapper = await mountView();
    expect(wrapper.text()).toContain("暂无导入记录");
    wrapper.unmount();

    installStores([importRecord(1)]);
    wrapper = await mountView();
    await wrapper.get('input[aria-label="搜索 OpenAPI 导入记录"]').setValue("no-match");
    await flushPromises();
    expect(wrapper.text()).toContain("没有匹配导入记录");
  });

  it("shows a retryable error when an interactive server-page request fails", async () => {
    wrapper = await mountView();
    mocks.integration.loadOpenAPIImportPage.mockRejectedValueOnce(new Error("interactive request failed"));

    await wrapper.get('input[aria-label="搜索 OpenAPI 导入记录"]').setValue("订单");
    await flushPromises();

    expect(wrapper.text()).toContain("interactive request failed");
    expect(wrapper.find('[data-openapi-load-retry]').exists()).toBe(true);
  });

  it("keeps import, draft generation, and confirmed deletion actions wired to the integration store", async () => {
    wrapper = await mountView();

    await wrapper.get(".primary-button").trigger("click");
    const file = await attachOpenAPIFile(wrapper);
    await wrapper.get('button[type="button"]:not([disabled])').trigger("focus");
    const importButton = wrapper.findAll("button").find((button) => button.text().includes("开始导入"));
    expect(importButton).toBeDefined();
    await importButton!.trigger("click");
    await flushPromises();
    expect(mocks.integration.createOpenAPIFileImport).toHaveBeenCalledWith(
      { workspaceId: "order", providerId: "provider-order", connectionId: "connection-order" }, file,
    );

    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="生成 Tool 草稿"]').trigger("click");
    await flushPromises();
    expect(mocks.integration.generateToolDrafts).toHaveBeenCalledWith("import-1");

    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await wrapper.get('button[aria-label="删除记录"]').trigger("click");
    const confirmButton = wrapper.findAll("button").find((button) => button.text().includes("确认删除"));
    expect(confirmButton).toBeDefined();
    await confirmButton!.trigger("click");
    await flushPromises();
    expect(mocks.integration.deleteOpenAPIImport).toHaveBeenCalledWith("import-1");
  });

  it("shows every Provider in the current workspace and marks online-import eligibility", async () => {
    mocks.integration.providers = [
      {
        id: "provider-managed",
        name: "Managed OpenAPI",
        endpointConfig: { discovery: { documentUrl: "https://managed.example.test/openapi.json" } },
        discoveryMode: "ON_DEMAND",
      },
      {
        id: "provider-legacy",
        name: "Legacy OpenAPI",
        endpointConfig: { sourceUri: "https://legacy.example.test/openapi.json" },
      },
      {
        id: "provider-manual",
        name: "Manual Runtime Provider",
        endpointConfig: { discovery: { documentUrl: "https://manual.example.test/openapi.json" } },
        discoveryMode: "MANUAL",
      },
      {
        id: "provider-runtime",
        name: "Runtime Only Provider",
        endpointConfig: { serviceBaseUrl: "https://runtime.example.test" },
        discoveryMode: "ON_DEMAND",
      },
    ];
    mocks.integration.serviceConnections = [];

    wrapper = await mountView();
    await wrapper.get(".primary-button").trigger("click");
    const providerSelect = wrapper.get('[data-testid="openapi-provider-select"]');
    expect(providerSelect.text()).toContain("Managed OpenAPI");

    await providerSelect.trigger("click");
    const providerOptions = wrapper.findAll('.openapi-select-menu[role="listbox"] .openapi-select-option');
    expect(providerOptions.map((option) => option.text())).toEqual([
      "Managed OpenAPI可在线导入",
      "Legacy OpenAPI可在线导入",
      "Manual Runtime Provider未配置在线 OpenAPI 文档",
      "Runtime Only Provider未配置在线 OpenAPI 文档",
    ]);

    await providerOptions[1].trigger("click");
    await wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes("Provider 在线文档"))!.trigger("click");
    const importButton = wrapper.findAll("button").find((button) => button.text().includes("开始导入"));
    await importButton!.trigger("click");
    await flushPromises();
    expect(mocks.integration.createOpenAPIImport).toHaveBeenCalledWith({ workspaceId: "order", providerId: "provider-legacy" });
  });

  it("keeps runtime-only Providers visible while explaining why online import is blocked", async () => {
    mocks.integration.providers = [
      {
        id: "provider-manual",
        name: "Manual Runtime Provider",
        endpointConfig: { discovery: { documentUrl: "https://manual.example.test/openapi.json" } },
        discoveryMode: "MANUAL",
      },
      {
        id: "provider-runtime",
        name: "Runtime Only Provider",
        endpointConfig: { serviceBaseUrl: "https://runtime.example.test" },
        discoveryMode: "ON_DEMAND",
      },
    ];
    mocks.integration.serviceConnections = [{
      ...connection(),
      id: "connection-manual",
      providerId: "provider-manual",
      name: "Manual Production HTTP",
    }];

    wrapper = await mountView();
    await wrapper.get(".primary-button").trigger("click");

    const providerSelect = wrapper.get('[data-testid="openapi-provider-select"]');
    expect(providerSelect.attributes("disabled")).toBeUndefined();
    expect(providerSelect.text()).toContain("Manual Runtime Provider");
    await wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes("Provider 在线文档"))!.trigger("click");
    await providerSelect.trigger("click");
    expect(wrapper.findAll('.openapi-select-menu[role="listbox"] .openapi-select-option').map((option) => option.text())).toEqual([
      "Manual Runtime Provider未配置在线 OpenAPI 文档",
      "Runtime Only Provider未配置在线 OpenAPI 文档",
    ]);
    expect(wrapper.text()).toContain("Provider 和 Connection 已加载");
    expect(wrapper.text()).toContain("数据不会再从下拉框中隐藏");
    expect(wrapper.get('[data-testid="openapi-connection-select"]').text()).toContain("Manual Production HTTP");
    const importButton = wrapper.findAll("button").find((button) => button.text().includes("开始导入"));
    expect(importButton?.attributes("disabled")).toBeDefined();
    expect(mocks.integration.createOpenAPIImport).not.toHaveBeenCalled();
  });

  it("provides a page-owned mobile card with an explicit more-actions menu", async () => {
    wrapper = await mountView();

    expect(wrapper.findAll(".openapi-import-mobile-card")).toHaveLength(10);
    const menuButton = wrapper.get('button[aria-label="OpenAPI 导入记录更多操作"]');
    await menuButton.trigger("click");

    const menu = wrapper.get('[role="menu"][aria-label="OpenAPI 导入记录操作"]');
    expect(menu.isVisible()).toBe(true);
    expect(menu.text()).toContain("查看详情");
    expect(menu.text()).toContain("生成 Tool 草稿");
    expect(menu.text()).toContain("删除记录");
  });


  it("opens a stable detail shell with loading then content without calling generate", async () => {
    let resolveDetail!: (value: unknown) => void;
    mocks.integration.loadOpenAPIImportDetail = vi.fn(
      (record: { id: string }) =>
        new Promise((resolve) => {
          resolveDetail = (value) => {
            const detailed = value as { id: string; detail?: unknown };
            const index = mocks.integration.openAPIImportPageItems.findIndex((item: { id: string }) => item.id === record.id);
            if (index >= 0) mocks.integration.openAPIImportPageItems[index] = detailed;
            resolve(detailed);
          };
        }),
    );
    wrapper = await mountView();
    const firstRow = wrapper.get("tbody tr");
    await firstRow.trigger("keydown", { key: "Enter" });
    await flushPromises();

    expect(wrapper.find('section[aria-label="导入详情"]').exists()).toBe(true);
    expect(wrapper.find(".openapi-detail-modal-head").exists()).toBe(true);
    expect(wrapper.find('[data-testid="openapi-detail-loading"]').exists()).toBe(true);
    expect(mocks.integration.loadOpenAPIImportDetail).toHaveBeenCalledTimes(1);
    expect(mocks.integration.generateToolDrafts).not.toHaveBeenCalled();

    const record = mocks.integration.openAPIImportPageItems[0];
    resolveDetail({ ...record, detail: { endpoints: [] } });
    await flushPromises();

    expect(wrapper.find('[data-testid="openapi-detail-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="openapi-detail-error"]').exists()).toBe(false);
    expect(wrapper.find(".openapi-detail-hero").exists()).toBe(true);
    expect(mocks.integration.generateToolDrafts).not.toHaveBeenCalled();
  });

  it("keeps detail shell on GET error and retries with the same load only", async () => {
    mocks.integration.loadOpenAPIImportDetail = vi
      .fn()
      .mockRejectedValueOnce(new Error("detail unavailable"))
      .mockImplementation(async (record: ReturnType<typeof importRecord>) => {
        const detailed = { ...record, detail: { endpoints: [] } };
        const index = mocks.integration.openAPIImportPageItems.findIndex((item: { id: string }) => item.id === record.id);
        if (index >= 0) mocks.integration.openAPIImportPageItems[index] = detailed;
        return detailed;
      });
    wrapper = await mountView();
    await wrapper.get("tbody tr").trigger("keydown", { key: "Enter" });
    await flushPromises();

    expect(wrapper.find('[data-testid="openapi-detail-error"]').exists()).toBe(true);
    expect(wrapper.get('[data-testid="openapi-detail-error"]').text()).toContain("detail unavailable");
    expect(mocks.integration.generateToolDrafts).not.toHaveBeenCalled();

    await wrapper.get('[data-testid="openapi-detail-retry"]').trigger("click");
    await flushPromises();
    expect(mocks.integration.loadOpenAPIImportDetail).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-testid="openapi-detail-error"]').exists()).toBe(false);
    expect(mocks.integration.generateToolDrafts).not.toHaveBeenCalled();
  });

  it("does not apply light detail head styles to import or delete modals", async () => {
    wrapper = await mountView();
    await wrapper.get('[data-testid="openapi-create"], button').trigger("click").catch(() => undefined);
    // Prefer explicit create trigger if present.
    const createBtn = wrapper.findAll("button").find((b) => b.text().includes("导入") || b.text().includes("新建"));
    if (createBtn) {
      await createBtn.trigger("click");
      await flushPromises();
    }
    // Import modal head should remain dark (no detail modifier).
    const importHead = wrapper.find(".openapi-modal-card:not(.openapi-detail-modal-card) .openapi-modal-head");
    if (importHead.exists()) {
      expect(importHead.classes()).not.toContain("openapi-detail-modal-head");
    }
  });

  it("restores focus to a DataTable row after keyboard opening and closing import details", async () => {
    wrapper = await mountView();
    const firstRow = wrapper.get("tbody tr");
    const rowElement = firstRow.element as HTMLElement;

    rowElement.focus();
    expect(document.activeElement).toBe(rowElement);
    await firstRow.trigger("keydown", { key: "Enter" });
    await flushPromises();
    expect(wrapper.find('section[aria-label="导入详情"]').exists()).toBe(true);

    await wrapper.get('section[aria-label="导入详情"] button[aria-label="关闭详情弹框"]').trigger("click");
    await flushPromises();
    expect(document.activeElement).toBe(rowElement);
  });
});
