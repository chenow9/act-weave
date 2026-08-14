import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestI18n } from "../test-utils/i18n";
import AppShell from "./AppShell.vue";

const fixtures = vi.hoisted(() => ({
  auth: {
    user: { username: "access.user", role: "User", platformRole: "USER", lockVersion: 1 },
    logout: vi.fn(),
    clearSession: vi.fn(),
    applyUser: vi.fn(),
    loading: false,
    error: "",
  },
  workspaces: {
    activeWorkspace: null as any,
    activeWorkspaceId: "",
    items: [] as any[],
    summary: { total: 0 },
    load: vi.fn(async () => []),
    selectWorkspace: vi.fn(),
    fetchWorkspacePage: vi.fn(async () => ({ items: [] })),
    upsertInList: vi.fn(),
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
    fixtures.auth.loading = false;
    fixtures.auth.error = "";
    fixtures.auth.logout.mockResolvedValue(undefined);
    fixtures.route.path = "/overview";
    fixtures.workspaces.activeWorkspace = null;
    fixtures.workspaces.activeWorkspaceId = "";
    fixtures.workspaces.items = [];
    fixtures.workspaces.summary = { total: 0 };
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
      { id: "workspace-1", name: "legacy", displayName: "Legacy Workspace", status: "ACTIVE", mode: "PRODUCTION" },
      { id: "workspace-2", name: "neiops", displayName: "NeiOps", status: "ACTIVE", mode: "PRODUCTION" },
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
      { id: "workspace-1", name: "legacy", displayName: "Legacy Workspace", status: "ACTIVE", mode: "PRODUCTION" },
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

  it("renders English glossary labels when locale is en", async () => {
    const wrapper = mountShell("en");
    await wrapper.get(".fluid-trigger").trigger("click");
    expect(wrapper.text()).toContain("Smart Orchestration");
    expect(wrapper.text()).toContain("Run Console");
    expect(wrapper.text()).toContain("ActWeave");
    expect(wrapper.text()).not.toContain("织行");
  });

  it("waits for server logout before navigating to login", async () => {
    const wrapper = mountShell();
    await wrapper.get('[data-testid="user-menu-trigger"]').trigger("click");
    await wrapper.get('[data-testid="sign-out"]').trigger("click");

    await vi.waitFor(() => expect(fixtures.auth.logout).toHaveBeenCalledTimes(1));
    expect(fixtures.router.push).toHaveBeenCalledWith({ name: "login" });
    expect(wrapper.find('[data-testid="sign-out-error"]').exists()).toBe(false);
  });

  it("keeps the app visible and offers retry when server logout fails", async () => {
    fixtures.auth.error = "退出失败，会话尚未撤销，请重试。";
    fixtures.auth.logout.mockRejectedValueOnce(new Error("offline"));
    const wrapper = mountShell();
    await wrapper.get('[data-testid="user-menu-trigger"]').trigger("click");
    await wrapper.get('[data-testid="sign-out"]').trigger("click");

    await vi.waitFor(() => expect(wrapper.get('[data-testid="sign-out-error"]').exists()).toBe(true));
    expect(fixtures.router.push).not.toHaveBeenCalledWith({ name: "login" });
    expect(wrapper.get('[data-testid="sign-out-error"]').text()).toContain("会话尚未撤销");
  });
});

function mountShell(locale: "zh-CN" | "en" = "zh-CN") {
  return mount(AppShell, {
    global: {
      plugins: [createTestI18n(locale)],
      stubs: {
        RouterLink: { props: ["to"], template: '<a :href="to"><slot /></a>' },
        RouterView: { template: "<div />" },
      },
    },
  });
}
