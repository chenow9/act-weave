import { defineComponent, h, ref } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useModalFocus } from "./useModalFocus";

const FocusDialog = defineComponent({
  props: {
    visible: {
      type: Boolean,
      required: true,
    },
    label: {
      type: String,
      required: true,
    },
    onClose: {
      type: Function,
      required: true,
    },
  },
  setup(props) {
    const modalRef = ref<HTMLElement | null>(null);
    useModalFocus({
      visible: () => props.visible,
      modalRef,
      onClose: () => props.onClose(),
    });

    return () =>
      props.visible
        ? h("section", { ref: modalRef, role: "dialog", "aria-label": props.label }, [
            h("button", { "data-modal-initial-focus": true }, `关闭${props.label}`),
          ])
        : null;
  },
});

describe("useModalFocus", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });

  it("only closes the topmost modal on Escape when dialogs are stacked", async () => {
    const closeParent = vi.fn();
    const closeChild = vi.fn();
    const wrapper = mount(
      defineComponent({
        setup() {
          return { closeParent, closeChild };
        },
        components: { FocusDialog },
        template: `
          <FocusDialog visible label="父弹框" :on-close="closeParent" />
          <FocusDialog visible label="子弹框" :on-close="closeChild" />
        `,
      }),
      { attachTo: document.body },
    );
    await flushPromises();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();

    expect(closeChild).toHaveBeenCalledTimes(1);
    expect(closeParent).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});
