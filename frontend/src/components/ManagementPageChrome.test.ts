import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementSummaryStrip from "./ManagementSummaryStrip.vue";

describe("management page chrome", () => {
  it("renders the shared prototype page title and action region", () => {
    const wrapper = mount(ManagementPageHeader, {
      props: {
        title: "工具管理",
        description: "管理工具契约、服务绑定、版本测试与发布状态。",
        icon: "fa-solid fa-screwdriver-wrench",
        eyebrow: "构建",
      },
      slots: { actions: '<button type="button">创建工具</button>' },
    });

    expect(wrapper.get("h1").text()).toBe("工具管理");
    expect(wrapper.get(".management-page-eyebrow").text()).toBe("构建");
    expect(wrapper.get(".management-page-icon i").classes()).toContain("fa-screwdriver-wrench");
    expect(wrapper.get(".management-page-actions").text()).toBe("创建工具");
  });

  it("renders the four-column shared metric strip with notes and tones", () => {
    const wrapper = mount(ManagementSummaryStrip, {
      props: {
        items: [
          { label: "工具总数", value: 38, icon: "fa-solid fa-screwdriver-wrench" },
          { label: "已发布", value: 26, note: "68.4%", icon: "fa-solid fa-circle-check" },
          { label: "待发布", value: 5, icon: "fa-solid fa-vial", tone: "info" },
          { label: "需处理", value: 3, icon: "fa-solid fa-triangle-exclamation", tone: "warning" },
        ],
      },
    });

    expect(wrapper.findAll("article")).toHaveLength(4);
    expect(wrapper.text()).toContain("已发布26");
    expect(wrapper.get("small").text()).toBe("68.4%");
    expect(wrapper.find(".tone-info").exists()).toBe(true);
    expect(wrapper.find(".tone-warning").exists()).toBe(true);
  });
});
