import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ToolSchemaNode } from "../types/domain";
import ToolSchemaDualPane from "./ToolSchemaDualPane.vue";

const schemaNodes: ToolSchemaNode[] = [
  {
    id: "payload-node",
    name: "payload",
    type: "object",
    required: true,
    description: "请求体",
    location: "Body",
    children: [
      {
        id: "reason-node",
        name: "reason",
        type: "string",
        required: true,
        description: "拦截原因",
        children: [],
        item: null,
        additionalProperties: null,
      },
    ],
    item: null,
    additionalProperties: null,
  },
];

function mountDualPane() {
  return mount(ToolSchemaDualPane, {
    attachTo: document.body,
    props: {
      modelValue: schemaNodes,
      title: "请求体 Body 工作台",
      description: "编辑请求体",
      rootLabel: "Request Body Contract",
    },
  });
}

describe("tool schema dual pane behavior", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("renders compare mode from one readonly JSON projection and one structured projection", () => {
    const wrapper = mountDualPane();

    expect(wrapper.find(".tool-schema-linked-json").exists()).toBe(true);
    expect(wrapper.find(".tool-schema-linked-table").exists()).toBe(true);
    expect(wrapper.findComponent({ name: "ToolSchemaJsonEditor" }).exists()).toBe(false);
    expect(wrapper.find("[contenteditable='true']").exists()).toBe(false);
    expect(wrapper.find(".tool-schema-linked-json").text()).toContain('"payload"');
    expect(wrapper.find(".tool-schema-linked-json").text()).toContain('"reason"');

    wrapper.unmount();
  });

  it("uses the same persistent anchor color on JSON lines and table rows for each field", () => {
    const wrapper = mountDualPane();

    const codeLine = wrapper.get('[data-schema-field-id="reason-node"]');
    const tableRow = wrapper.get('tr[data-schema-field-id="reason-node"]');

    expect(codeLine.attributes("style")).toContain("--schema-anchor-color:");
    expect(codeLine.attributes("style")).toBe(tableRow.attributes("style"));

    wrapper.unmount();
  });

  it("links hover and click state bidirectionally between JSON lines and table rows", async () => {
    const wrapper = mountDualPane();
    const tableRow = wrapper.get('tr[data-schema-field-id="reason-node"]');

    await tableRow.trigger("mouseenter");
    expect(tableRow.classes()).toContain("is-linked");
    expect(wrapper.get('[data-schema-line-key="reason-node:0"]').classes()).toContain("is-linked");

    await tableRow.trigger("mouseleave");
    expect(tableRow.classes()).not.toContain("is-linked");

    await tableRow.trigger("click");
    expect(tableRow.classes()).toContain("is-selected");
    expect(wrapper.get('[data-schema-line-key="reason-node:0"]').classes()).toContain("is-selected");
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();

    const codeLine = wrapper.get('[data-schema-line-key="payload-node:0"]');
    await codeLine.trigger("click");
    expect(wrapper.get('tr[data-schema-field-id="payload-node"]').classes()).toContain("is-selected");

    wrapper.unmount();
  });
});
