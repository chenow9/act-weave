import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import WorkflowTrialRunDialog from "./WorkflowTrialRunDialog.vue";

function mountDialog() {
  return mount(WorkflowTrialRunDialog, {
    props: {
      visible: true,
      workflowName: "订单取消编排",
      inputSchema: [],
    },
  });
}

describe("WorkflowTrialRunDialog", () => {
  it("uses the shared modal layer above the full-screen workflow editor", () => {
    const wrapper = mountDialog();

    expect(wrapper.find(".workflow-trial-run-backdrop").classes()).toContain("workflow-modal-layer");
  });

  it("closes with Escape", async () => {
    const wrapper = mountDialog();

    await wrapper.get(".workflow-trial-run-dialog").trigger("keydown", { key: "Escape" });

    expect(wrapper.emitted("close")).toHaveLength(1);
  });
});
