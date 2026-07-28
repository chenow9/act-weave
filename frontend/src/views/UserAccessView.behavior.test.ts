import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import ElementPlus from "element-plus";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import AppSelect from "../components/AppSelect.vue";
import ManagementList from "../components/ManagementList.vue";
import { APIError, apiClient, type AuthUserDTO } from "../services/api";
import UserAccessView from "./UserAccessView.vue";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return { ...actual, apiClient: { get: vi.fn(), patch: vi.fn(), post: vi.fn() } };
});

describe("user access management view", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("loads, filters and creates platform users", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce(userPage([userFixture()]))
      .mockResolvedValueOnce(userPage([userFixture({ username: "searched.user" })]))
      .mockResolvedValueOnce(userPage([userFixture(), userFixture({ id: "user-2", username: "new.user" })]));
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: userFixture({ id: "user-2", username: "new.user", displayName: "New User" }),
    });
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.text()).toContain("用户与权限");
    expect(wrapper.text()).toContain("platform.admin");
    expect(wrapper.findComponent(ManagementList).exists()).toBe(true);
    expect(wrapper.findAllComponents(AppSelect)).toHaveLength(2);
    expect(wrapper.find(".user-access-table").exists()).toBe(false);
    expect(wrapper.find(".user-row-actions").exists()).toBe(true);
    await wrapper.get('input[aria-label="搜索用户"]').setValue("searched");
    await flushPromises();
    expect(apiClient.get).toHaveBeenNthCalledWith(2, "/admin/users?query=searched&page=1&pageSize=10");

    await button(wrapper, "新建用户").trigger("click");
    const dialog = wrapper.get('[role="dialog"][aria-label="新建平台用户"]');
    expect(dialog.get('button[aria-label="关闭"]').classes()).toContain("icon-action-button");
    const inputs = dialog.findAll("input");
    await inputs[0].setValue("new.user");
    await inputs[1].setValue("New User");
    await inputs[3].setValue("temporary-password-1");
    await dialog.get("form").trigger("submit");
    await flushPromises();
    expect(apiClient.post).toHaveBeenCalledWith(
      "/admin/users",
      expect.objectContaining({
        username: "new.user",
        displayName: "New User",
        platformRole: "USER",
      }),
    );
  });

  it("uses required dropdowns for language and timezone fields", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce(userPage([userFixture()]));
    const wrapper = mountView();
    await flushPromises();

    await button(wrapper, "新建用户").trigger("click");
    await flushPromises();
    const createDialog = wrapper.get('[role="dialog"][aria-label="新建平台用户"]');
    expect(createDialog.find("select").exists()).toBe(false);
    expect(createDialog.findAll(".user-field-required")).toHaveLength(6);
    expect(createDialog.get('input[role="combobox"][aria-label="新用户语言"]').attributes("aria-required")).toBe(
      "true",
    );
    expect(createDialog.get('input[role="combobox"][aria-label="新用户时区"]').attributes("aria-required")).toBe(
      "true",
    );

    await button(createDialog, "取消").trigger("click");
    await selectSecurityAction(wrapper, "编辑用户资料");
    await flushPromises();
    const profileDialog = wrapper.get('[role="dialog"][aria-label="编辑用户资料"]');
    expect(profileDialog.find("select").exists()).toBe(false);
    expect(profileDialog.findAll(".user-field-required")).toHaveLength(3);
    expect(profileDialog.get('input[role="combobox"][aria-label="用户语言"]').attributes("aria-required")).toBe("true");
    expect(profileDialog.get('input[role="combobox"][aria-label="用户时区"]').attributes("aria-required")).toBe("true");
  });

  it("explains why create user is disabled and clears the reason when the form is valid", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce(userPage([userFixture()]));
    const wrapper = mountView();
    await flushPromises();

    await button(wrapper, "新建用户").trigger("click");
    const dialog = wrapper.get('[role="dialog"][aria-label="新建平台用户"]');
    const createButton = button(dialog, "创建用户");
    expect(createButton.attributes("disabled")).toBeDefined();
    expect(createButton.attributes("aria-describedby")).toBe("create-user-disabled-reason");
    expect(dialog.get("#create-user-disabled-reason").text()).toBe("请填写必填项：用户名、显示名称和临时密码。");

    const inputs = dialog.findAll("input");
    await inputs[0].setValue("chenow");
    await inputs[1].setValue("chenow测试账号");
    await inputs[3].setValue("123456789");
    expect(dialog.get("#create-user-disabled-reason").text()).toBe("临时密码至少需要 12 位（当前 9 位）。");
    expect(createButton.attributes("disabled")).toBeDefined();

    await inputs[3].setValue("123456789012");
    expect(dialog.find("#create-user-disabled-reason").exists()).toBe(false);
    expect(createButton.attributes("disabled")).toBeUndefined();
    expect(createButton.attributes("aria-describedby")).toBeUndefined();
  });

  it("shows request ids when last-administrator protection rejects a role change", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce(userPage([userFixture({ platformRole: "PLATFORM_ADMIN" })]));
    vi.mocked(apiClient.post).mockRejectedValueOnce(
      new APIError({
        status: 409,
        code: "CONFLICT",
        message: "The resource state conflicts.",
        requestId: "request-last-admin",
      }),
    );
    const wrapper = mountView();
    await flushPromises();
    await selectSecurityAction(wrapper, "降为普通用户");
    const dialog = wrapper.get('[role="dialog"][aria-label="移除平台管理员"]');
    expect(dialog.text()).toContain("最后一个有效平台管理员不能被降级");
    const confirmButton = button(dialog, "确认执行");
    expect(confirmButton.classes()).toEqual(expect.arrayContaining(["primary-button", "danger"]));
    await confirmButton.trigger("click");
    await flushPromises();
    expect(dialog.get('[role="alert"]').text()).toContain("request-last-admin");
  });

  it("loads and renders workspace memberships", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce(userPage([userFixture()]))
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              workspaceId: "workspace-1",
              workspaceSlug: "core",
              workspaceDisplayName: "核心空间",
              workspaceStatus: "ACTIVE",
              role: "EDITOR",
              joinedAt: "2026-07-15T03:00:00Z",
            },
          ],
        },
      });
    const wrapper = mountView();
    await flushPromises();
    await selectSecurityAction(wrapper, "查看业务空间");
    await flushPromises();
    const dialog = wrapper.get('[role="dialog"][aria-label="用户业务空间"]');
    expect(dialog.text()).toContain("核心空间");
    expect(dialog.text()).toContain("编辑者");
    expect(dialog.get('button[aria-label="关闭"]').classes()).toContain("icon-action-button");
    expect(dialog.get(".user-workspace-role").text()).toBe("编辑者");
    expect(dialog.get(".user-workspace-status").text()).toBe("空间正常");
    expect(apiClient.get).toHaveBeenLastCalledWith("/admin/users/user-1/workspaces?includeDisabled=true");
  });

  it("uses the shared destructive button style for password resets", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce(userPage([userFixture()]));
    const wrapper = mountView();
    await flushPromises();
    await selectSecurityAction(wrapper, "重置密码");
    const dialog = wrapper.get('[role="dialog"][aria-label="重置用户密码"]');
    expect(button(dialog, "重置密码").classes()).toEqual(expect.arrayContaining(["primary-button", "danger"]));
    expect(dialog.find(".danger-button").exists()).toBe(false);
  });
});

function mountView() {
  return mount(UserAccessView, {
    attachTo: document.body,
    global: { plugins: [createPinia(), ElementPlus], directives: { loading: () => undefined } },
  });
}

async function selectSecurityAction(wrapper: VueWrapper, label: string) {
  await wrapper.get('button[aria-label="用户安全操作"]').trigger("click");
  // Prefer aria-label (full action name); visible menu text may be shortLabel after FE-03.
  const item = [...document.body.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(
    (candidate) =>
      candidate.getAttribute("aria-label")?.includes(label) ||
      candidate.getAttribute("title")?.includes(label) ||
      candidate.textContent?.includes(label),
  );
  if (!item) throw new Error(`security action ${label} not found`);
  item.click();
  await flushPromises();
}

function button(wrapper: VueWrapper | ReturnType<VueWrapper["get"]>, label: string) {
  const match = wrapper.findAll("button").find((item) => item.text().includes(label));
  if (!match) throw new Error(`button ${label} not found`);
  return match;
}

function userPage(items: AuthUserDTO[]) {
  return { data: { items, pagination: { page: 1, pageSize: 10, total: items.length } } };
}

function userFixture(overrides: Partial<AuthUserDTO> = {}): AuthUserDTO {
  return {
    id: "user-1",
    username: "platform.admin",
    displayName: "Platform Admin",
    status: "ACTIVE",
    platformRole: "USER",
    locale: "zh-CN",
    timezone: "Asia/Singapore",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}
