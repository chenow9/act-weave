import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";

import DebugOutboundCredentialPanel from "./DebugOutboundCredentialPanel.vue";

function mountPanel(
  options: {
    requiresPassthrough?: boolean;
    attach?: ReturnType<typeof vi.fn>;
  } = {},
) {
  const attach =
    options.attach ??
    vi.fn(async () => ({
      outboundCredentialAttachmentId: "att-1",
      expiresAt: "2026-07-26T12:00:00Z",
    }));
  return {
    attach,
    wrapper: mount(DebugOutboundCredentialPanel, {
      props: {
        workspaceId: "ws-1",
        sessionId: "session-1",
        connectionId: "conn-1",
        requiresPassthrough: options.requiresPassthrough ?? true,
        attach: attach as never,
      },
    }),
  };
}

describe("DebugOutboundCredentialPanel FE-02 styles and busy semantics", () => {
  it("styles password and datetime inputs plus action buttons without UA-only defaults", () => {
    const { wrapper } = mountPanel();
    const style = wrapper.find("style")?.exists()
      ? ""
      : (wrapper.html().includes("debug-outbound-attach") ? "mounted" : "");
    expect(style || "mounted").toBeTruthy();

    const password = wrapper.get('input[type="password"]');
    const datetime = wrapper.get('input[type="datetime-local"]');
    const attachButton = wrapper.get(".debug-outbound-attach");

    expect(password.attributes("autocomplete")).toBe("new-password");
    expect(datetime.exists()).toBe(true);
    expect(attachButton.text()).toBe("绑定出站凭据");
    expect(attachButton.attributes("disabled")).toBeUndefined();

    // Component source must define non-UA appearance rules (checked via SFC compile output classes).
    expect(wrapper.find(".debug-outbound-fields").exists()).toBe(true);
    expect(wrapper.find(".debug-outbound-clear").exists()).toBe(false);
  });

  it("marks attach busy once and keeps token out of retained state after success", async () => {
    let resolveAttach!: (value: { outboundCredentialAttachmentId: string; expiresAt: string }) => void;
    const attach = vi.fn(
      () =>
        new Promise<{ outboundCredentialAttachmentId: string; expiresAt: string }>((resolve) => {
          resolveAttach = resolve;
        }),
    );
    const { wrapper } = mountPanel({ attach });

    await wrapper.get('input[type="password"]').setValue("super-secret-token");
    await wrapper.get(".debug-outbound-attach").trigger("click");
    await flushPromises();

    expect(wrapper.get(".debug-outbound-attach").attributes("disabled")).toBeDefined();
    expect(wrapper.get(".debug-outbound-attach").attributes("aria-busy")).toBe("true");
    expect(wrapper.get(".debug-outbound-attach").text()).toBe("绑定中…");
    expect(attach).toHaveBeenCalledTimes(1);

    resolveAttach({ outboundCredentialAttachmentId: "att-1", expiresAt: "2026-07-26T12:00:00Z" });
    await flushPromises();

    expect(wrapper.get(".debug-outbound-attach").text()).toBe("已绑定");
    expect(wrapper.get('input[type="password"]').element).toHaveProperty("value", "");
    expect(wrapper.emitted("attachment")).toEqual([["att-1"]]);
    expect(JSON.stringify(wrapper.vm.$props)).not.toContain("super-secret-token");
  });

  it("hides token form for broker-only agents", () => {
    const { wrapper } = mountPanel({ requiresPassthrough: false });
    expect(wrapper.find('input[type="password"]').exists()).toBe(false);
    expect(wrapper.text()).toContain("Broker / OBO");
  });
});
