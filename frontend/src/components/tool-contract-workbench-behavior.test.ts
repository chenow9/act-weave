import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it } from "vitest";

import ToolContractWorkbench from "./ToolContractWorkbench.vue";

describe("tool contract workbench behavior", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });

  it("moves focus into the workbench and closes on Escape", async () => {
    const wrapper = mount(ToolContractWorkbench, {
      attachTo: document.body,
      props: {
        modelValue: [],
        visible: true,
        title: "请求体 Body 工作台",
        description: "编辑请求体",
        rootLabel: "Request Body Contract",
      },
      global: {
        stubs: {
          ToolSchemaJsonEditor: {
            template: "<div class='json-editor-stub' />",
          },
          ToolSchemaTreeEditor: {
            template: "<div class='tree-editor-stub' />",
          },
          ToolSchemaDualPane: {
            template: "<div class='dual-pane-stub' />",
          },
        },
      },
    });
    await flushPromises();

    expect(document.activeElement).toBe(wrapper.get("[data-modal-initial-focus]").element);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();

    expect(wrapper.emitted("update:visible")).toEqual([[false]]);
    wrapper.unmount();
  });
});
