import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { describe, expect, it, vi } from "vitest";

import { useConnectionsStore } from "../stores/connections";
import { useProvidersStore } from "../stores/providers";
import { createTestI18n } from "../test-utils/i18n";
import CapabilityPackageDialog from "./CapabilityPackageDialog.vue";

function mountDialog(open = true) {
  const pinia = createPinia();
  setActivePinia(pinia);
  const connections = useConnectionsStore();
  connections.loadServiceConnectionCatalog = vi.fn().mockResolvedValue(undefined);
  const providers = useProvidersStore();
  providers.loadProviders = vi.fn().mockResolvedValue([]);

  return mount(CapabilityPackageDialog, {
    props: { open, workspaceId: "ws-1" },
    global: {
      plugins: [createTestI18n(), pinia],
      stubs: { AppSelect: true },
    },
    attachTo: document.body,
  });
}

describe("CapabilityPackageDialog", () => {
  it("uses a compact dropzone instead of a skinny full-width file chip", async () => {
    const wrapper = mountDialog();
    await flushPromises();

    const card = wrapper.get(".management-dialog-card");
    expect(card.classes()).toContain("is-md");
    expect(card.classes()).not.toContain("is-preview");

    const dropzone = wrapper.get('[data-testid="capability-package-dropzone"]');
    expect(dropzone.text()).toContain("选择配置文件");
    expect(dropzone.text()).toContain("拖放到此处");
    expect(wrapper.get(".management-dialog-description").text()).toContain("只生成草稿");
    expect(wrapper.get(".capability-package-note").text()).toContain("当前业务空间");
    expect(wrapper.find(".capability-package-preview").exists()).toBe(false);

    wrapper.unmount();
  });

  it("rejects a non-YAML drop without calling preview", async () => {
    const wrapper = mountDialog();
    await flushPromises();

    const file = new File(["not yaml"], "notes.txt", { type: "text/plain" });
    await wrapper.get('[data-testid="capability-package-dropzone"]').trigger("drop", {
      dataTransfer: { files: [file] },
    });
    await flushPromises();

    expect(wrapper.get('[role="alert"]').text()).toContain("请选择 YAML");
    expect(wrapper.find(".capability-package-preview").exists()).toBe(false);

    wrapper.unmount();
  });
});
