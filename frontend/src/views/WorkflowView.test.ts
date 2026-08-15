import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { AxiosError } from "axios";
import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import WorkflowGraphCanvas from "../components/workflow/WorkflowGraphCanvas.vue";
import { apiClient } from "../services/api";
import { useSmartDagStore } from "../stores/smartdag";
import { useWorkflowStore } from "../stores/workflow";
import { useWorkspaceStore } from "../stores/workspaces";
import WorkflowView from "./WorkflowView.vue";

const { routerReplace, routeQuery } = vi.hoisted(() => ({
  routerReplace: vi.fn(),
  routeQuery: {} as Record<string, unknown>,
}));

vi.mock("vue-router", () => ({
  useRouter: () => ({
    replace: routerReplace,
    push: vi.fn(),
  }),
  useRoute: () => ({
    name: "workflow",
    query: routeQuery,
  }),
}));

vi.mock("../services/api", () => ({
  apiClient: {
    delete: vi.fn(),
    get: vi.fn(),
    patch: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  toAPIError: (error: { message?: string; response?: { status?: number } }) => ({
    message: error.message || "request failed",
    status: error.response?.status,
  }),
}));

function workflowFixture(id = "wf-order-cancel-draft", name = "订单取消编排") {
  return {
    id,
    workspaceId: "order",
    currentDraftId: `draft-${id}`,
    latestCompilationId: "comp-1",
    name,
    slug: id,
    description: "查询订单状态并决定后续动作。",
    status: "Draft" as const,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
  };
}

function workflowSummaryFixture(id = "wf-order-cancel-draft", name = "订单取消编排") {
  return {
    id,
    workspaceId: "order",
    currentDraftId: `draft-${id}`,
    latestCompilationId: "comp-1",
    workspaceName: "订单中心",
    name,
    slug: id,
    description: "查询订单状态并决定后续动作。",
    status: "Draft" as const,
    nodeCount: 3,
    edgeCount: 2,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
  };
}

function readinessFixture(overrides: Record<string, unknown> = {}) {
  return {
    stage: "TrialRequired",
    canCompile: true,
    canTrial: true,
    canValidate: true,
    canTrialRun: true,
    canPublish: false,
    hasDraft: true,
    compilationCurrent: true,
    compilationValid: true,
    trialCurrent: false,
    trialSuccessful: false,
    published: false,
    blockers: [
      {
        code: "trial_required",
        message: "Workflow must pass a trial run for the current compiled draft.",
        action: "Run a trial against the latest compiled draft.",
        severity: "error",
      },
    ],
    updatedAt: "2026-07-02T09:00:00Z",
    ...overrides,
  };
}

function draftFixture() {
  return {
    id: "draft-wf-order-cancel-draft",
    workflowId: "wf-order-cancel-draft",
    draftVersion: 2,
    schemaVersion: "workflow.graph.v1",
    graphHash: "sha256:graph-2",
    updatedBy: "user-1",
    updatedAt: "2026-06-27T09:00:00Z",
    lockVersion: 2,
    etag: '"draft-2-2"',
    graph: {
      schemaVersion: "workflow.graph.v1",
      nodes: [
        {
          id: "start",
          type: "Start" as const,
          label: "Start",
          position: { x: 120, y: 180 },
          ports: [{ key: "output", label: "Output", direction: "output" as const }],
          data: {
            inputSchema: {
              required: ["orderId"],
              properties: {
                orderId: { type: "string", description: "订单 ID" },
              },
            },
          },
          ui: {},
        },
        {
          id: "tool-1",
          type: "Tool" as const,
          label: "查询订单",
          position: { x: 340, y: 180 },
          ports: [
            { key: "input", label: "Input", direction: "input" as const },
            { key: "output", label: "Output", direction: "output" as const },
          ],
          data: {},
          ui: {},
        },
        {
          id: "end",
          type: "End" as const,
          label: "End",
          position: { x: 560, y: 180 },
          ports: [{ key: "input", label: "Input", direction: "input" as const }],
          data: {},
          ui: {},
        },
      ],
      edges: [
        {
          id: "edge-start-tool",
          sourceNodeId: "start",
          sourcePort: "output",
          targetNodeId: "tool-1",
          targetPort: "input",
          data: {},
          ui: {},
        },
        {
          id: "edge-tool-end",
          sourceNodeId: "tool-1",
          sourcePort: "output",
          targetNodeId: "end",
          targetPort: "input",
          data: {},
          ui: {},
        },
      ],
      viewport: { x: 0, y: 0, zoom: 1 },
      ui: {},
    },
  };
}

function compilationFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: "comp-1",
    workflowId: "wf-order-cancel-draft",
    draftId: "draft-wf-order-cancel-draft",
    draftVersion: 2,
    graphHash: "sha256:graph-2",
    compilerVersion: "workflow-compiler.v1",
    status: "VALID" as const,
    spec: { workflowId: "wf-order-cancel-draft", nodes: [] },
    plan: { workflowId: "wf-order-cancel-draft", nodes: [] },
    issues: [],
    planHash: "sha256:plan-1",
    compiledBy: "user-1",
    compiledAt: "2026-06-27T09:05:00Z",
    ...overrides,
  };
}

function revisionFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: "rev-2",
    revisionNo: 2,
    sourceCompilationId: "comp-1",
    workflowId: "wf-order-cancel-draft",
    revisionId: "rev-2",
    status: "PUBLISHED" as const,
    draft: draftFixture().graph,
    draftSnapshot: draftFixture().graph,
    spec: { workflowId: "wf-order-cancel-draft", nodes: [] },
    specSnapshot: { workflowId: "wf-order-cancel-draft", nodes: [] },
    plan: { workflowId: "wf-order-cancel-draft", nodes: [] },
    planSnapshot: { workflowId: "wf-order-cancel-draft", nodes: [] },
    createdAt: "2026-07-03T02:00:00Z",
    createdBy: "user-chen-ops",
    planHash: "sha256:abcdef0123456789",
    activatedAt: "2026-07-03T02:05:00Z",
    metadata: {},
    ...overrides,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function mockWorkflowAssets(workflows: ReturnType<typeof workflowSummaryFixture>[]) {
  const mock = vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: workflows } });
  workflows.forEach((workflow) => mock.mockResolvedValueOnce({ data: workflow.readiness || readinessFixture() }));
  return mock;
}

function mountWorkflowView(options: Parameters<typeof mount>[1] = {}) {
  return mount(WorkflowView, {
    ...options,
    global: {
      ...options.global,
      directives: {
        ...options.global?.directives,
        loading: () => undefined,
      },
      stubs: {
        ...options.global?.stubs,
        AppSelect: {
          props: ["modelValue", "options"],
          emits: ["update:modelValue"],
          template:
            "<select class='app-select-stub' :value='modelValue' @change=\"$emit('update:modelValue', $event.target.value)\"><option value=''>请选择</option><option v-for='option in options' :key='option.value' :value='option.value'>{{ option.label }}</option></select>",
        },
        ElDrawer: {
          props: ["modelValue"],
          template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
        },
      },
    },
  });
}

describe("workflow view P1.4", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    setActivePinia(createPinia());
    vi.resetAllMocks();
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key]);
    routerReplace.mockImplementation(async (location: { query?: Record<string, unknown> }) => {
      Object.keys(routeQuery).forEach((key) => delete routeQuery[key]);
      Object.assign(routeQuery, location.query || {});
    });
    useWorkspaceStore().items = [
      {
        id: "order",
        name: "order",
        displayName: "订单中心",
        owner: "Commerce Ops",
        mode: "PRODUCTION",
        status: "ACTIVE",
        defaultAgentId: "",
        modelConfigId: "",
        healthScore: 100,
        toolCount: 0,
        workflowCount: 1,
        agentCount: 0,
      },
    ];
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    if (!SVGElement.prototype.getBBox) {
      Object.defineProperty(SVGElement.prototype, "getBBox", {
        configurable: true,
        value: () => ({ x: 0, y: 0, width: 80, height: 24 }),
      });
    }
  });

  it("renders the reference orchestration dashboard table with compact workflow metadata", async () => {
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture(),
        readiness: readinessFixture(),
      },
    ]);

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-management-list").exists()).toBe(true);
    expect(wrapper.findAll(".management-summary-card")).toHaveLength(0);
    expect(wrapper.text()).toContain("编排");
    expect(wrapper.text()).toContain("业务流程名");
    expect(wrapper.text()).not.toContain("归属 Agent");
    expect(wrapper.text()).toContain("3 步 / 2 连接");
    expect(wrapper.text()).toContain("开发中草稿");
  });

  it("keeps canvas entry in the workflow list instead of duplicating it in the page header", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);
    const wrapper = mountWorkflowView();
    await flushPromises();

    expect(wrapper.find(".workflow-view-toggle").exists()).toBe(false);
    expect(wrapper.find(".workflow-canvas-select").exists()).toBe(false);
    expect(wrapper.find('button[aria-label="更多编排操作"]').exists()).toBe(true);
  });

  it("uses neutral row feedback and bounded workflow identity text", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);
    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get(".data-table tbody tr").classes()).toContain("is-selection-neutral");
    expect(wrapper.get(".workflow-name-cell strong").attributes("title")).toBe("订单取消编排");
    expect(wrapper.get(".workflow-name-cell small").attributes("title")).toContain("查询订单状态");
    expect(wrapper.get(".workflow-workspace-cell").attributes("title")).toBe("订单中心");
    expect(wrapper.find(".workflow-agent-chip").exists()).toBe(false);
  });

  it("loads workflow search results through the server page contract", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);
    const wrapper = mountWorkflowView();
    await flushPromises();

    vi.mocked(apiClient.get).mockClear();
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: { items: [workflowSummaryFixture()] },
    });
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture() });
    await wrapper.get('input[aria-label="搜索流程名称、Slug 或状态"]').setValue("订单");
    await flushPromises();

    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/order/workflows");
  });

  it("sorts workflows from page one through the server page contract", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);
    const wrapper = mountWorkflowView();
    await flushPromises();
    vi.mocked(apiClient.get).mockClear();
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [workflowSummaryFixture()] } })
      .mockResolvedValueOnce({ data: readinessFixture() });

    await wrapper.get('button[aria-label="按业务流程名升序排序"]').trigger("click");
    await flushPromises();

    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/order/workflows");
  });

  it("renders shared workflow row actions as a menu-only overflow in semantic order", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    const actionCell = wrapper.get('td[data-column-key="actions"]');
    expect(actionCell.findAll('button[data-action-kind="primary"]')).toHaveLength(0);
    expect(actionCell.get('button[aria-label="更多编排操作"]').exists()).toBe(true);

    await actionCell.get('button[aria-label="更多编排操作"]').trigger("click");
    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多编排操作"]');
    expect(menu).not.toBeNull();
    expect(Array.from(menu!.querySelectorAll("button")).map((button) => button.dataset.actionKey)).toEqual([
      "detail",
      "edit",
      "validate",
      "trial-run",
      "delete",
    ]);
    expect(menu!.querySelector('button[title="查看详情"]')?.getAttribute("aria-label")).toBe("查看详情");
    expect(menu!.querySelector('button[title="编辑流程图"]')?.getAttribute("aria-label")).toBe("编辑流程图");
    expect(menu!.querySelector('button[title="校验流程"]')?.getAttribute("aria-label")).toBe("校验流程");
    expect(menu!.querySelector('button[title="模拟试运行"]')?.getAttribute("aria-label")).toBe("模拟试运行");
    expect(menu!.querySelector('button[title="删除流程"]')?.getAttribute("aria-label")).toBe("删除流程");
  });

  it("makes workflow rows keyboard reachable and opens details with Enter", async () => {
    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: workflowFixture() })
      .mockResolvedValueOnce({ data: readinessFixture() })
      .mockResolvedValueOnce({ data: { items: [] } });

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    const row = wrapper.get(".data-table tbody tr");
    expect(row.attributes("role")).toBe("button");
    expect(row.attributes("tabindex")).toBe("0");

    await row.trigger("keydown", { key: "Enter" });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-detail-modal-card").exists()).toBe(true);
  });

  it("prevents duplicate workflow validation while a validation request is pending", async () => {
    const validation = deferred<{ data: ReturnType<typeof compilationFixture> }>();
    mockWorkflowAssets([workflowSummaryFixture()]);
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture() });
    vi.mocked(apiClient.post).mockImplementationOnce(() => validation.promise);

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    const validateButton = document.body.querySelector<HTMLButtonElement>('button[data-action-key="validate"]')!;
    validateButton.click();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    const pendingValidateButton = document.body.querySelector<HTMLButtonElement>('button[data-action-key="validate"]')!;
    expect(pendingValidateButton.disabled).toBe(true);
    expect(pendingValidateButton.getAttribute("aria-busy")).toBe("true");
    pendingValidateButton.click();

    expect(apiClient.post).toHaveBeenCalledTimes(1);

    validation.resolve({ data: compilationFixture() });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain("校验通过");
  });

  it("restores focus after closing the metadata modal", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);

    const wrapper = mountWorkflowView({ attachTo: document.body });

    await flushPromises();
    await wrapper.vm.$nextTick();

    const createButton = wrapper.get(".workflow-create-button");
    (createButton.element as HTMLElement).focus();
    await createButton.trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(document.activeElement).not.toBe(createButton.element);

    await wrapper.get(".workflow-metadata-modal-card").trigger("keydown", { key: "Escape" });
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-metadata-modal-card").exists()).toBe(false);
    expect(document.activeElement).toBe(createButton.element);

    wrapper.unmount();
  });

  it("blocks an empty Workflow name before sending a create request", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);
    const wrapper = mountWorkflowView();
    await flushPromises();

    await wrapper.get(".workflow-create-button").trigger("click");
    await flushPromises();

    const saveButton = wrapper.get(".workflow-metadata-actions .primary-button");
    expect(saveButton.attributes("disabled")).toBeDefined();
    await wrapper.get(".workflow-metadata-body input").trigger("blur");
    expect(wrapper.text()).toContain("Workflow 名称必填");
    expect(apiClient.post).not.toHaveBeenCalled();
  });

  it("shows a deterministic loading and error state while opening the workflow editor draft", async () => {
    const pendingDraft = deferred<{ data: ReturnType<typeof draftFixture>; headers: { etag: string } }>();
    const draftLoadError = new AxiosError("draft failed", "ERR_BAD_REQUEST", undefined, undefined, {
      status: 500,
      statusText: "Server Error",
      headers: {},
      config: { headers: {} } as never,
      data: { error: "draft load failed" },
    });

    mockWorkflowAssets([workflowSummaryFixture()]).mockImplementationOnce(() => pendingDraft.promise);
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain("正在加载");

    pendingDraft.reject(draftLoadError);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain("草稿加载失败");
    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(false);
  });

  it("shows no-tools guidance when the workspace has no published tools and surfaces tool catalog load errors", async () => {
    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: draftFixture(), headers: { etag: '"draft-2-2"' } })
      .mockResolvedValueOnce({ data: readinessFixture() })
      .mockRejectedValueOnce(new Error("tool catalog down"));

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('[data-node-id="tool-1"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain("还没有可绑定的已发布工具");
    expect(wrapper.text()).toContain("工具目录加载失败");
  });

  it("renders a consolidated editor header with grouped actions and localized readiness state", async () => {
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture(),
        readiness: readinessFixture({ canPublish: false }),
      },
    ])
      .mockResolvedValueOnce({ data: draftFixture(), headers: { etag: '"draft-2-2"' } })
      .mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    const topbar = wrapper.get(".workflow-editor-topbar");
    expect(topbar.find(".workflow-editor-header-row").exists()).toBe(true);
    expect(topbar.find(".workflow-editor-action-row").exists()).toBe(true);
    expect(topbar.find(".workflow-editor-meta").exists()).toBe(true);
    expect(topbar.find(".workflow-editor-readiness-strip").exists()).toBe(true);
    expect(topbar.find(".workflow-editor-primary-actions").exists()).toBe(true);
    expect(topbar.find(".workflow-editor-secondary-actions").exists()).toBe(true);
    expect(topbar.get(".workflow-editor-primary-actions").text()).toContain("保存画布");
    expect(topbar.get(".workflow-editor-primary-actions").text()).toContain("发布上线");
    expect(topbar.get(".workflow-editor-header-row").text()).not.toContain("保存画布");
    expect(topbar.get(".workflow-editor-action-row").text()).toContain("智能生成");
    expect(topbar.get(".workflow-editor-action-row").text()).toContain("基础信息");
    expect(topbar.get(".workflow-editor-action-row").text()).toContain("复制节点");
    expect(topbar.get(".workflow-editor-action-row").text()).toContain("检查问题");
    expect(topbar.get(".workflow-editor-action-row").text()).not.toContain("保存画布");
    expect(topbar.findAll(".workflow-editor-secondary-actions .workflow-editor-action-group")).toHaveLength(4);
    expect(topbar.findAll(".workflow-editor-secondary-actions .workflow-editor-action-divider")).toHaveLength(3);
    expect(topbar.find(".workflow-editor-dirty-pill").exists()).toBe(false);
    expect(topbar.find(".workflow-readiness-panel.compact").exists()).toBe(false);
    expect(topbar.text()).not.toContain("Run a trial against the latest compiled draft.");
    expect(topbar.text()).not.toContain("保存画布会更新节点和连线");
    expect(topbar.get(".workflow-editor-help-button").attributes("title")).toContain("保存画布会更新节点和连线");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("草稿");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("编译");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("试运行");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("发布");

    const publishButton = topbar.get('button[data-action="publish-editor-workflow"]');
    expect(publishButton.classes()).toContain("workflow-editor-publish-button");
    expect(publishButton.attributes("disabled")).toBeDefined();
    expect(publishButton.attributes("title")).toContain("需先完成试运行");
    expect(topbar.get(".workflow-editor-close-button").attributes("aria-label")).toBe("退出编辑");
  });

  it("loads revision diff and disables new runs from the detail drawer", async () => {
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture(),
        readiness: readinessFixture({
          stage: "Published",
          canPublish: true,
          trialCurrent: true,
          trialSuccessful: true,
          published: true,
          activeRevisionId: "rev-2",
          latestRevisionId: "rev-3",
          blockers: [],
        }),
      },
    ])
      .mockResolvedValueOnce({
        data: { ...workflowFixture(), status: "ACTIVE", activeRevisionId: "rev-2" },
      })
      .mockResolvedValueOnce({
        data: readinessFixture({
          stage: "Published",
          canPublish: true,
          trialCurrent: true,
          trialSuccessful: true,
          published: true,
          activeRevisionId: "rev-2",
          latestRevisionId: "rev-3",
          blockers: [],
        }),
      })
      .mockResolvedValueOnce({
        data: {
          items: [
            revisionFixture({
              id: "rev-3",
              revisionId: "rev-3",
              revisionNo: 3,
              createdAt: "2026-07-03T03:00:00Z",
            }),
            revisionFixture(),
          ],
        },
      })
      .mockResolvedValueOnce({
        data: {
          from: revisionFixture({ id: "rev-2", revisionId: "rev-2", revisionNo: 2 }),
          to: revisionFixture({ id: "rev-3", revisionId: "rev-3", revisionNo: 3 }),
          changes: { draft: true, spec: false, plan: true, planHash: true },
        },
      });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({
      data: { ...workflowFixture(), status: "DISABLED", activeRevisionId: "rev-2", lockVersion: 2 },
    });

    const wrapper = mountWorkflowView();

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="detail"]')!.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain("发布版本");
    expect(wrapper.text()).toContain("停用新执行");

    const compareButtons = wrapper.findAll("button").filter((button) => button.text() === "对比");
    expect(compareButtons.length).toBeGreaterThan(0);
    await compareButtons[0]!.trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(apiClient.get).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/revisions:diff?from=rev-2&to=rev-3",
    );
    expect(wrapper.text()).toContain("版本差异");
    expect(wrapper.text()).toContain("Plan Hash");

    const disableButton = wrapper.findAll("button").find((button) => button.text().includes("停用新执行"));
    expect(disableButton).toBeDefined();
    await disableButton!.trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(apiClient.patch).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft",
      expect.objectContaining({ status: "DISABLED", lockVersion: 1 }),
    );
    expect(wrapper.text()).toContain("已停用");
    expect(wrapper.text()).toContain("新的 published execution 将被阻止");
  });

  it("opens an existing workflow editor on the nodes tab with generate chrome available", async () => {
    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: draftFixture(), headers: { etag: '"draft-2-2"' } })
      .mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mountWorkflowView();
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    const tablist = wrapper.get('[role="tablist"][aria-label="生成与节点库"]');
    const tabs = tablist.findAll('[role="tab"]');
    expect(tabs).toHaveLength(2);
    expect(tabs[0].text()).toBe("智能生成");
    expect(tabs[0].attributes("aria-selected")).toBe("false");
    expect(tabs[1].text()).toBe("节点");
    expect(tabs[1].attributes("aria-selected")).toBe("true");
    expect(tabs[1].attributes("disabled")).toBeUndefined();
    expect(wrapper.find(".workflow-node-palette").exists()).toBe(true);
    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(false);
    expect(wrapper.getComponent(WorkflowGraphCanvas).props("empty")).toBe(false);

    await wrapper.get('[data-action="open-generate-dock"]').trigger("click");
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(true);
    expect(wrapper.find(".workflow-node-palette").exists()).toBe(false);
    expect(wrapper.findAll('[role="tablist"] [role="tab"]')[0].attributes("aria-selected")).toBe("true");
  });

  it("disables the nodes tab when there is no persistable draft", async () => {
    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: draftFixture(), headers: { etag: '"draft-2-2"' } })
      .mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mountWorkflowView();
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    useWorkflowStore().activeDraft = undefined;
    await wrapper.vm.$nextTick();

    const nodesTab = wrapper.findAll('[role="tablist"] [role="tab"]')[1];
    expect(nodesTab.attributes("disabled")).toBeDefined();
    expect(nodesTab.attributes("title")).toBe("生成草稿后才能加节点");
    expect(wrapper.getComponent(WorkflowGraphCanvas).props("empty")).toBe(true);
  });

  it("keeps the generate prompt when switching away from the generate tab", async () => {
    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: draftFixture(), headers: { etag: '"draft-2-2"' } })
      .mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mountWorkflowView();
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('[data-action="open-generate-dock"]').trigger("click");
    await wrapper.vm.$nextTick();
    await wrapper.get("textarea").setValue("供应商准入，先查资质");
    await wrapper.vm.$nextTick();

    await wrapper.findAll('[role="tablist"] [role="tab"]')[1].trigger("click");
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(false);

    await wrapper.get('[data-action="open-generate-dock"]').trigger("click");
    await wrapper.vm.$nextTick();
    expect((wrapper.get("textarea").element as HTMLTextAreaElement).value).toBe("供应商准入，先查资质");
  });

  it("lifts the left column above the narrow generate sheet backdrop", () => {
    const css = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "workflow-page.css"), "utf8");
    const match = css.match(/\.workflow-workbench\.is-generate-sheet\s+\.workflow-workbench-left\s*\{([^}]+)\}/);
    expect(match, "expected a sheet-state rule for .workflow-workbench-left").toBeTruthy();
    expect(match![1]).toMatch(/z-index:\s*4/);
    expect(match![1]).toMatch(/isolation:\s*auto/);
  });

  it("returns focus to the topbar generate button after closing the generate sheet", async () => {
    const previousMatchMedia = window.matchMedia;
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: String(query).includes("1180"),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
      onchange: null,
    }));

    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: draftFixture(), headers: { etag: '"draft-2-2"' } })
      .mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mountWorkflowView({ attachTo: document.body });
    try {
      await flushPromises();
      await wrapper.vm.$nextTick();

      await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
      document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]')!.click();
      await flushPromises();
      await wrapper.vm.$nextTick();

      await wrapper.get('[data-action="open-generate-dock"]').trigger("click");
      await wrapper.vm.$nextTick();
      expect(wrapper.find(".workflow-generate-sheet-backdrop").exists()).toBe(true);

      await wrapper.get('[data-action="close-generate-sheet"]').trigger("click");
      await flushPromises();
      await wrapper.vm.$nextTick();

      expect(document.activeElement).toBe(wrapper.get('[data-action="open-generate-dock"]').element);
    } finally {
      wrapper.unmount();
      window.matchMedia = previousMatchMedia;
    }
  });

  it("opens intent generate without putting the selected row's leftover draft", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]);
    const wrapper = mountWorkflowView();
    await flushPromises();
    await wrapper.vm.$nextTick();

    const workflows = useWorkflowStore();
    const leftover = draftFixture();
    workflows.selectedWorkflowId = leftover.workflowId;
    workflows.activeDraft = leftover;
    const smart = useSmartDagStore();
    smart.sessionId = "session-a";
    smart.sessionStatus = "OPEN";
    smart.sessionWorkflowId = leftover.workflowId;
    smart.workspaceId = "order";
    smart.agentId = "agent-1";
    smart.goal = "修订选中行";
    smart.turns = [
      {
        turnId: "turn-a",
        turnIndex: 1,
        userMessage: "修订选中行",
        generationId: "gen-a",
        guardOk: true,
        status: "SUCCEEDED",
      },
    ];

    await wrapper.get('[data-action="open-intent-generate"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(apiClient.put).not.toHaveBeenCalled();
    expect(workflows.selectedWorkflowId).toBe("");
    expect(workflows.activeDraft).toBeUndefined();
    expect(smart.sessionId).toBe("");
    expect(smart.sessionStatus).toBe("");
    expect(smart.sessionWorkflowId).toBe("");
    expect(smart.turns).toEqual([]);
    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(true);
    expect(wrapper.findAll('[role="tablist"] [role="tab"]')[0].attributes("aria-selected")).toBe("true");
    expect(wrapper.getComponent(WorkflowGraphCanvas).props("graph").nodes).toEqual([]);
    expect(wrapper.get(".workflow-editor-shell").attributes("data-editor-dirty-state")).toBe("saved");
  });

  it("opens the generate dock from generate=1 with an empty graph", async () => {
    routeQuery.generate = "1";
    mockWorkflowAssets([workflowSummaryFixture()]);

    const wrapper = mountWorkflowView();
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(true);
    expect(wrapper.findAll('[role="tablist"] [role="tab"]')[0].attributes("aria-selected")).toBe("true");
    expect(wrapper.getComponent(WorkflowGraphCanvas).props("graph").nodes).toEqual([]);
    expect(wrapper.getComponent(WorkflowGraphCanvas).props("empty")).toBe(true);
    expect(useWorkflowStore().selectedWorkflowId).toBe("");
    expect(routeQuery.generate).toBe("1");
  });

  it("strips generate from the query when closing an unsent intent-generate dock", async () => {
    routeQuery.generate = "1";
    mockWorkflowAssets([workflowSummaryFixture()]);

    const wrapper = mountWorkflowView();
    await flushPromises();
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(true);

    await wrapper.get('button[aria-label="退出编辑"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-generate-dock").exists()).toBe(false);
    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(false);
    expect(routeQuery.generate).toBeUndefined();
    expect(routerReplace).toHaveBeenCalledWith({ name: "workflow", query: {} });
  });
});
