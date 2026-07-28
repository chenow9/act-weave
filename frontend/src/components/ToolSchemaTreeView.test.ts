import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import VxeUITable from "vxe-table";

import type { ToolSchemaNode } from "../types/domain";
import ToolSchemaTreeView from "./ToolSchemaTreeView.vue";

function makeNode(partial: Partial<ToolSchemaNode> & Pick<ToolSchemaNode, "id" | "name">): ToolSchemaNode {
  return {
    type: "string",
    required: false,
    description: "",
    children: [],
    ...partial,
  };
}

function mountSchemaView(props: InstanceType<typeof ToolSchemaTreeView>["$props"]): VueWrapper {
  return mount(ToolSchemaTreeView, {
    props,
    global: {
      // Component also calls ensureVxe; double-install is harmless beyond a Vue warn.
      plugins: [VxeUITable],
    },
  });
}

describe("ToolSchemaTreeView scheme R", () => {
  it("renders field key without 字段 prefix, Chinese required labels, and muted empty description", async () => {
    const wrapper = mountSchemaView({
      title: "响应结果",
      nodes: [
        makeNode({ id: "n1", name: "id", type: "string", required: false }),
        makeNode({ id: "n2", name: "title", type: "string", required: true, description: "标题" }),
      ],
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    const text = wrapper.text();
    expect(text).toContain("响应结果");
    expect(text).toContain("2 个节点");
    expect(text).toContain("字段名");
    expect(text).toContain("类型");
    expect(text).toContain("必填");
    expect(text).toContain("说明");
    expect(text).not.toContain("Key Path");
    expect(text).not.toContain("Metadata");
    expect(text).not.toContain("Optional");
    expect(text).not.toContain("YES");
    expect(text).not.toMatch(/字段id/);
    expect(text).toContain("可选");
    expect(text).toContain("必填");
    expect(text).toContain("标题");
    expect(text).toContain("—");

    const names = wrapper.findAll(".tool-schema-view-name").map((n) => n.text());
    expect(names).toEqual(expect.arrayContaining(["id", "title"]));
    expect(wrapper.findAll(".tool-schema-view-node-tag")).toHaveLength(0);
    expect(wrapper.findAll(".tool-schema-view-path")).toHaveLength(0);

    wrapper.unmount();
  });

  it("shows nested path hint only when path differs from name", async () => {
    const wrapper = mountSchemaView({
      title: "请求体 Body",
      nodes: [
        makeNode({
          id: "root",
          name: "payload",
          type: "object",
          required: false,
          children: [makeNode({ id: "child", name: "sku", type: "string", required: true })],
        }),
      ],
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    const paths = wrapper.findAll(".tool-schema-view-path").map((n) => n.text());
    expect(paths.some((p) => p === "payload.sku" || p.includes("sku"))).toBe(true);
    expect(wrapper.text()).not.toMatch(/字段sku|字段payload/);
    wrapper.unmount();
  });
});
