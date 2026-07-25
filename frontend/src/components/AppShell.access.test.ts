import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import AppShell from "./AppShell.vue";

const fixtures = vi.hoisted(() => ({
  auth: {
    user: { username: "access.user", role: "User", platformRole: "USER" },
    logout: vi.fn(),
  },
  workspaces: {
    activeWorkspace: null as any,
    activeWorkspaceId: "",
    items: [] as any[],
    load: vi.fn(async () => []),
    selectWorkspace: vi.fn(),
  },
  overview: { load: vi.fn(async () => undefined) },
  router: { push: vi.fn() },
  route: { path: "/overview" },
}));

vi.mock("../stores/auth", () => ({ useAuthStore: () => fixtures.auth }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixtures.workspaces }));
vi.mock("../stores/overview", () => ({ useOverviewStore: () => fixtures.overview }));
vi.mock("vue-router", () => ({
  useRoute: () => fixtures.route,
  useRouter: () => fixtures.router,
}));

describe("AppShell platform-administrator navigation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fixtures.auth.user.platformRole = "USER";
    fixtures.route.path = "/overview";
    fixtures.workspaces.activeWorkspace = null;
    fixtures.workspaces.activeWorkspaceId = "";
    fixtures.workspaces.items = [];
  });

  it("hides user management from ordinary users", () => {
    expect(mountShell().text()).not.toContain("用户与权限");
  });

  it("shows user management to platform administrators", () => {
    fixtures.auth.user.platformRole = "PLATFORM_ADMIN";
    const wrapper = mountShell();
    expect(wrapper.text()).toContain("用户与权限");
    expect(wrapper.get('a[href="/users"]').exists()).toBe(true);
  });

  it("shows and switches the active Workspace from the global topbar outside overview", async () => {
    fixtures.route.path = "/agents";
    fixtures.workspaces.activeWorkspaceId = "workspace-1";
    fixtures.workspaces.items = [
      { id: "workspace-1", name: "legacy", displayName: "Legacy Workspace", status: "Active", mode: "Production" },
      { id: "workspace-2", name: "neiops", displayName: "NeiOps", status: "Active", mode: "Production" },
    ];
    fixtures.workspaces.activeWorkspace = { ...fixtures.workspaces.items[0], healthScore: 75 };
    const wrapper = mountShell();

    expect(wrapper.get('[data-testid="workspace-switcher"]').text()).toContain("Legacy Workspace");
    await wrapper.get('[data-testid="workspace-switcher"]').trigger("click");
    expect(wrapper.get('[role="dialog"][aria-label="选择业务空间"]').exists()).toBe(true);
    await wrapper.get('[data-workspace-id="workspace-2"]').trigger("click");
    expect(fixtures.workspaces.selectWorkspace).toHaveBeenCalledWith("workspace-2");
  });

  it("hides the workspace switcher on the platform-wide overview page", () => {
    fixtures.route.path = "/overview";
    fixtures.workspaces.activeWorkspaceId = "workspace-1";
    fixtures.workspaces.items = [
      { id: "workspace-1", name: "legacy", displayName: "Legacy Workspace", status: "Active", mode: "Production" },
    ];
    fixtures.workspaces.activeWorkspace = { ...fixtures.workspaces.items[0], healthScore: 75 };
    const wrapper = mountShell();
    expect(wrapper.find('[data-testid="workspace-switcher"]').exists()).toBe(false);
  });

  it("opens and filters the fluid navigation center", async () => {
    const wrapper = mountShell();

    expect(wrapper.get(".fluid-trigger").attributes("aria-expanded")).toBe("false");
    await wrapper.get(".fluid-trigger").trigger("click");
    expect(wrapper.get(".fluid-trigger").attributes("aria-expanded")).toBe("true");
    await wrapper.get('input[aria-label="搜索模块"]').setValue("OpenAPI");
    expect(wrapper.findAll(".island-module").map((item) => item.text())).toEqual(["OpenAPI 导入"]);
    await wrapper.get(".nav-scrim").trigger("click");
    expect(wrapper.get(".fluid-trigger").attributes("aria-expanded")).toBe("false");
  });

  it("resets the navigation scroll position and focus after selecting a menu item", async () => {
    const wrapper = mountShell();
    document.body.appendChild(wrapper.element);
    const trigger = wrapper.get<HTMLButtonElement>(".fluid-trigger");

    await trigger.trigger("click");
    const navigation = wrapper.get<HTMLElement>(".fluid-island");
    const toolsLink = wrapper.get<HTMLAnchorElement>('a[href="/tools"]');
    navigation.element.scrollTop = 18;
    toolsLink.element.focus();
    toolsLink.element.addEventListener("click", (event) => event.preventDefault(), { once: true });

    await toolsLink.trigger("click");
    await wrapper.vm.$nextTick();

    expect(navigation.element.scrollTop).toBe(0);
    expect(document.activeElement).toBe(trigger.element);
    wrapper.unmount();
  });
});

function mountShell() {
  return mount(AppShell, {
    global: {
      stubs: {
        RouterLink: { props: ["to"], template: '<a :href="to"><slot /></a>' },
        RouterView: { template: "<div />" },
      },
    },
  });
}
