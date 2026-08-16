import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import { createTestI18n } from "../test-utils/i18n";
import ManagementDialog from "./ManagementDialog.vue";

function mountDialog(props: Record<string, unknown> = {}, slots: Record<string, string> = {}) {
  return mount(ManagementDialog, {
    props: {
      title: "新建业务空间",
      ...props,
    },
    slots: {
      default: "<p>表单内容</p>",
      footer: '<button class="primary-button" type="button">保存</button>',
      ...slots,
    },
    global: {
      plugins: [createTestI18n()],
    },
    attachTo: document.body,
  });
}

describe("ManagementDialog", () => {
  it("renders a localized product chrome without English eyebrows", async () => {
    const wrapper = mountDialog({
      eyebrow: "新建",
      description: "填写空间名称",
      icon: "fa-solid fa-layer-group",
    });
    await flushPromises();

    expect(wrapper.get("h2").text()).toBe("新建业务空间");
    expect(wrapper.get(".management-dialog-eyebrow").text()).toBe("新建");
    expect(wrapper.text()).not.toMatch(/WORKSPACE SETUP|DANGER ZONE|PROVIDER REGISTRY/i);
    expect(wrapper.get(".management-dialog-footer .primary-button").text()).toBe("保存");
    wrapper.unmount();
  });

  it("marks danger dialogs and keeps the confirm control in the shared footer", async () => {
    const wrapper = mountDialog({
      title: "删除业务空间",
      eyebrow: "危险操作",
      tone: "danger",
      size: "sm",
    });
    await flushPromises();

    expect(wrapper.get(".management-dialog-card").classes()).toContain("is-danger");
    expect(wrapper.get(".management-dialog-card").classes()).toContain("is-sm");
    expect(wrapper.get(".management-dialog-eyebrow").text()).toBe("危险操作");
    wrapper.unmount();
  });

  it("emits close from the header control and optional backdrop", async () => {
    const wrapper = mountDialog({ testid: "shared-dialog-backdrop" });
    await flushPromises();

    await wrapper.get(".management-dialog-close").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);

    await wrapper.get('[data-testid="shared-dialog-backdrop"]').trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    wrapper.unmount();
  });

  it("can keep a create form open when the backdrop is clicked", async () => {
    const wrapper = mountDialog({ closeOnBackdrop: false, testid: "create-backdrop" });
    await flushPromises();

    await wrapper.get('[data-testid="create-backdrop"]').trigger("click");
    expect(wrapper.emitted("close")).toBeUndefined();
    expect(wrapper.emitted("backdrop")).toHaveLength(1);
    wrapper.unmount();
  });
});
