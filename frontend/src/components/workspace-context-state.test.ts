import { readFileSync } from "node:fs";
import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import WorkspaceContextState from "./WorkspaceContextState.vue";

const fixture = vi.hoisted(() => ({
  router: { push: vi.fn() },
  workspaces: null as any,
}));

vi.mock("vue-router", () => ({ useRouter: () => fixture.router }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixture.workspaces }));

describe("WorkspaceContextState", () => {
  beforeEach(() => {
    fixture.workspaces = reactive({
      activeWorkspaceId: "",
      items: [],
      load: vi.fn(async () => []),
    });
    vi.clearAllMocks();
  });

  it("presents a product empty state with navigation and a clear recovery path", async () => {
    const wrapper = mount(WorkspaceContextState, { props: { feature: "工具管理" } });

    expect(wrapper.text()).toContain("还没有可用的业务空间");
    expect(wrapper.text()).toContain("工具管理需要归属于业务空间");
    expect(wrapper.text()).toContain("首次使用：先创建业务空间");
    expect(wrapper.text()).not.toContain("Select a Workspace");

    await wrapper.get(".primary-button").trigger("click");
    expect(fixture.router.push).toHaveBeenCalledWith("/workspaces");

    await wrapper.get(".ghost-button").trigger("click");
    await flushPromises();
    expect(fixture.workspaces.load).toHaveBeenCalledOnce();
    expect(wrapper.text()).toContain("当前账号仍没有可访问的业务空间");
  });

  it("asks the page to reload its resources after Workspace recovery", async () => {
    fixture.workspaces.load.mockImplementationOnce(async () => {
      fixture.workspaces.activeWorkspaceId = "workspace-1";
      fixture.workspaces.items = [{ id: "workspace-1" }];
      return fixture.workspaces.items;
    });
    const wrapper = mount(WorkspaceContextState, { props: { feature: "流程编排" } });

    await wrapper.get(".ghost-button").trigger("click");
    await flushPromises();

    expect(wrapper.emitted("retry")).toHaveLength(1);
  });
});

describe("Workspace-scoped management pages", () => {
  it("reuse the shared state and do not expose developer guard messages", () => {
    const pages = [
      "src/views/ToolsView.vue",
      "src/views/WorkflowView.vue",
      "src/views/ServiceConnectionsView.vue",
      "src/views/OpenAPIImportsView.vue",
      "src/views/ModelAPIConfigsView.vue",
    ];
    for (const page of pages) {
      const source = readFileSync(page, "utf8");
      expect(source).toContain("WorkspaceContextState");
      expect(source).toContain("hasWorkspaceContext");
      expect(source).toContain("请先创建或加入业务空间");
    }

    for (const store of [
      "src/stores/integration.ts",
      "src/stores/workflow.ts",
      "src/stores/modelConfigs.ts",
    ]) {
      expect(readFileSync(store, "utf8")).not.toContain("Select a Workspace");
    }
  });
});
