import { createPinia, setActivePinia } from "pinia";
import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useAuthStore } from "../stores/auth";
import LoginView from "./LoginView.vue";

const push = vi.fn();
const routeQuery = vi.hoisted(() => ({ value: {} as Record<string, string> }));

vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
  useRoute: () => ({ query: routeQuery.value }),
}));

describe("LoginView", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockReset();
    routeQuery.value = {};
  });

  it("explains that the previous session expired", () => {
    routeQuery.value = { sessionExpired: "1" };
    const wrapper = mount(LoginView);
    expect(wrapper.get('[data-testid="session-expired-notice"]').text()).toContain("登录已过期");
  });

  it("returns to a safe in-app path after sign-in", async () => {
    routeQuery.value = { redirect: "/model-apis" };
    const auth = useAuthStore();
    auth.login = vi.fn(async () => {
      auth.token = "ok";
      auth.mustChangePassword = false;
    });
    const wrapper = mount(LoginView);
    await wrapper.get("input[autocomplete='username']").setValue("admin");
    await wrapper.get("input[autocomplete='current-password']").setValue("secret-password");
    await wrapper.get("form").trigger("submit.prevent");
    expect(push).toHaveBeenCalledWith("/model-apis");
  });

  it("ignores an external redirect after sign-in", async () => {
    routeQuery.value = { redirect: "https://evil.example" };
    const auth = useAuthStore();
    auth.login = vi.fn(async () => {
      auth.token = "ok";
      auth.mustChangePassword = false;
    });
    const wrapper = mount(LoginView);
    await wrapper.get("input[autocomplete='username']").setValue("admin");
    await wrapper.get("input[autocomplete='current-password']").setValue("secret-password");
    await wrapper.get("form").trigger("submit.prevent");
    expect(push).toHaveBeenCalledWith({ name: "overview" });
  });
});
