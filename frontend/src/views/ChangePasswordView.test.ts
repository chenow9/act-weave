import { createPinia, setActivePinia } from "pinia";
import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { useAuthStore } from "../stores/auth";
import ChangePasswordView from "./ChangePasswordView.vue";

const push = vi.fn();

vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
}));

vi.mock("../stores/auth", async () => {
  const actual = await vi.importActual<typeof import("../stores/auth")>("../stores/auth");
  return actual;
});

const currentDir = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(resolve(currentDir, "ChangePasswordView.vue"), "utf8");

describe("ChangePasswordView", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockReset();
  });

  it("reuses login visual language and stays outside AppShell concerns", () => {
    expect(source).toContain("login-page login-split");
    expect(source).toContain("login-left-panel");
    expect(source).toContain("login-right-panel");
    expect(source).toContain("login-primary-button");
    expect(source).toContain("修改密码并重新登录");
    expect(source).not.toContain("AppShell");
    expect(source).not.toContain("Canvas");
  });

  it("validates password mismatch and length without calling the store", async () => {
    const auth = useAuthStore();
    auth.changePassword = vi.fn();
    const wrapper = mount(ChangePasswordView);
    await wrapper.find("form").trigger("submit.prevent");
    // empty fields still hit HTML required, but set values for JS validators
    await wrapper.findAll("input")[0].setValue("current-password-1");
    await wrapper.findAll("input")[1].setValue("short");
    await wrapper.findAll("input")[2].setValue("short");
    await wrapper.find("form").trigger("submit.prevent");
    expect(wrapper.text()).toContain("新密码至少需要 12 位");
    expect(auth.changePassword).not.toHaveBeenCalled();

    await wrapper.findAll("input")[1].setValue("new-password-12");
    await wrapper.findAll("input")[2].setValue("different-pass");
    await wrapper.find("form").trigger("submit.prevent");
    expect(wrapper.text()).toContain("两次输入的新密码不一致");
    expect(auth.changePassword).not.toHaveBeenCalled();
  });

  it("submits once, disables while pending, clears form and navigates to login", async () => {
    const auth = useAuthStore();
    let resolveChange!: () => void;
    const pending = new Promise<void>((resolve) => {
      resolveChange = resolve;
    });
    auth.changePassword = vi.fn().mockReturnValue(pending);

    const wrapper = mount(ChangePasswordView);
    const inputs = wrapper.findAll("input");
    await inputs[0].setValue("current-password-1");
    await inputs[1].setValue("new-password-12");
    await inputs[2].setValue("new-password-12");

    const submitPromise = wrapper.find("form").trigger("submit.prevent");
    await wrapper.vm.$nextTick();
    expect(wrapper.find("button[type='submit']").attributes("disabled")).toBeDefined();

    // Second submit while pending must not double-call.
    await wrapper.find("form").trigger("submit.prevent");
    expect(auth.changePassword).toHaveBeenCalledTimes(1);
    expect(auth.changePassword).toHaveBeenCalledWith("current-password-1", "new-password-12");

    resolveChange();
    await submitPromise;
    await wrapper.vm.$nextTick();

    expect(push).toHaveBeenCalledWith({ name: "login", query: { passwordChanged: "1" } });
    expect((inputs[0].element as HTMLInputElement).value).toBe("");
    expect((inputs[1].element as HTMLInputElement).value).toBe("");
    expect((inputs[2].element as HTMLInputElement).value).toBe("");
  });
});
