import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";

const options = [
  { value: "ALL", label: "全部" },
  { value: "Available", label: "可用" },
  { value: "Attention", label: "需关注" },
];

function mountFilter(modelValue = "ALL") {
  return mount(ManagementSegmentedFilter, {
    props: { modelValue, options, ariaLabel: "连接状态筛选" },
  });
}

describe("ManagementSegmentedFilter", () => {
  it("renders a custom listbox matching aria-haspopup and emits clicked values", async () => {
    const wrapper = mountFilter();
    expect(wrapper.get(".management-filter-trigger").attributes("aria-haspopup")).toBe("listbox");
    expect(wrapper.get(".management-filter-trigger").attributes("aria-expanded")).toBe("false");
    await wrapper.get(".management-filter-trigger").trigger("click");
    expect(wrapper.get(".management-filter-trigger").attributes("aria-expanded")).toBe("true");
    expect(wrapper.get('[role="listbox"]').attributes("aria-label")).toBe("连接状态筛选");
    expect(wrapper.get('[role="option"][aria-selected="true"]').text()).toBe("全部");
    expect(wrapper.get('button[value="ALL"]').attributes("tabindex")).toBe("0");
    await wrapper.get('button[value="Available"]').trigger("click");
    expect(wrapper.emitted("update:modelValue")).toEqual([["Available"]]);
  });

  it("wraps Arrow navigation and supports Home and End", async () => {
    const wrapper = mountFilter("ALL");
    await wrapper.get('button[value="ALL"]').trigger("keydown", { key: "ArrowLeft" });
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual(["Attention"]);
    await wrapper.setProps({ modelValue: "Attention" });
    await wrapper.get('button[value="Attention"]').trigger("keydown", { key: "Home" });
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual(["ALL"]);
    await wrapper.get('button[value="Attention"]').trigger("keydown", { key: "End" });
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual(["Attention"]);
  });

  it("skips disabled options and provides a keyboard entry when nothing is selected", async () => {
    const wrapper = mount(ManagementSegmentedFilter, {
      props: {
        modelValue: "CUSTOM_SEARCH",
        ariaLabel: "测试筛选",
        options: [options[0], { ...options[1], disabled: true }, options[2]],
      },
    });
    expect(wrapper.get('button[value="ALL"]').attributes("tabindex")).toBe("0");
    expect(wrapper.find('[aria-selected="true"]').exists()).toBe(false);
    await wrapper.get('button[value="ALL"]').trigger("keydown", { key: "ArrowRight" });
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual(["Attention"]);
  });
});
