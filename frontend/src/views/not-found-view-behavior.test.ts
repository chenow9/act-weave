import { mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";

const push = vi.fn();
vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
}));

import NotFoundView from "./NotFoundView.vue";

describe("NotFoundView behavior", () => {
  it("shows 404 copy and routes users back to overview", async () => {
    const wrapper = mount(NotFoundView);
    expect(wrapper.text()).toContain("404");
    expect(wrapper.text()).toContain("页面不存在");
    expect(wrapper.get("button").text()).toContain("返回空间总览");
    await wrapper.get("button").trigger("click");
    expect(push).toHaveBeenCalledWith({ name: "overview" });
  });
});
