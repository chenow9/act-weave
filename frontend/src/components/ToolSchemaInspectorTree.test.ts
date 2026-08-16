import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import type { ToolSchemaNode } from "../types/domain";
import { createTestI18n } from "../test-utils/i18n";
import ToolSchemaInspectorTree from "./ToolSchemaInspectorTree.vue";

function makeNode(partial: Partial<ToolSchemaNode> & Pick<ToolSchemaNode, "id" | "name">): ToolSchemaNode {
  return {
    type: "string",
    required: false,
    description: "",
    children: [],
    item: null,
    additionalProperties: null,
    ...partial,
  };
}

describe("ToolSchemaInspectorTree", () => {
  it("renders the Body root and type badges instead of a table header", async () => {
    const wrapper = mount(ToolSchemaInspectorTree, {
      props: {
        rootLabel: "Body",
        nodes: [
          makeNode({ id: "id", name: "id", type: "string" }),
          makeNode({ id: "code", name: "code", type: "string", required: true }),
          makeNode({ id: "sort", name: "sort", type: "integer" }),
        ],
      },
      global: { plugins: [createTestI18n("zh-CN")] },
    });
    await flushPromises();

    const text = wrapper.text();
    expect(text).toContain("Body");
    expect(text).toContain("object");
    expect(text).toContain("str");
    expect(text).toContain("int");
    expect(text).toContain("id");
    expect(text).toContain("code");
    expect(text).toContain("✓");
    expect(text).not.toContain("字段名");
    expect(text).not.toContain("必填");
    expect(text).not.toContain("说明");
    expect(wrapper.findAll(".tool-schema-inspector-row").length).toBe(4);
    expect(wrapper.find("[title]").exists()).toBe(false);
    wrapper.unmount();
  });

  it("shows a styled tooltip immediately on hover instead of a native title", async () => {
    const wrapper = mount(ToolSchemaInspectorTree, {
      attachTo: document.body,
      props: {
        rootLabel: "Body",
        nodes: [makeNode({ id: "sceneName", name: "sceneName", type: "string", description: "场景名称。" })],
      },
      global: { plugins: [createTestI18n("zh-CN")] },
    });
    await flushPromises();

    const row = wrapper.get('[role="treeitem"]');
    expect(row.attributes("title")).toBeUndefined();
    await row.trigger("mouseenter");
    await new Promise((resolve) => setTimeout(resolve, 50));
    const tip = document.querySelector(".tool-schema-inspector-tooltip");
    expect(tip?.textContent?.trim()).toBe("场景名称。");
    wrapper.unmount();
  });

  it("keeps nested object children collapsed until expanded", async () => {
    const wrapper = mount(ToolSchemaInspectorTree, {
      props: {
        rootLabel: "Body",
        nodes: [
          makeNode({
            id: "models",
            name: "models",
            type: "array",
            item: makeNode({
              id: "item",
              name: "",
              type: "object",
              required: true,
              children: [makeNode({ id: "sku", name: "sku", type: "string" })],
            }),
          }),
        ],
      },
      global: { plugins: [createTestI18n("zh-CN")] },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("models");
    expect(wrapper.text()).not.toContain("sku");
    await wrapper.get('[aria-expanded="false"] .tool-schema-inspector-toggle').trigger("click");
    expect(wrapper.text()).toContain("数组元素");
    wrapper.unmount();
  });
});
