import { describe, expect, it, vi, beforeEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import AgentDelegationPanel from "./AgentDelegationPanel.vue";

const mocks = vi.hoisted(() => ({
  listDelegationBindings: vi.fn(),
  listA2ARemotes: vi.fn(),
  listA2AExposures: vi.fn(),
  getA2ACapabilities: vi.fn(),
  updateDelegationBinding: vi.fn(),
  updateA2AExposure: vi.fn(),
  updateA2ARemote: vi.fn(),
  createDelegationBinding: vi.fn(),
  createA2AExposure: vi.fn(),
  createA2ARemote: vi.fn(),
  disableDelegationBinding: vi.fn(),
  disableA2AExposure: vi.fn(),
  disableA2ARemote: vi.fn(),
  previewA2AAgentCard: vi.fn(),
}));

vi.mock("../services/agentDelegation", () => mocks);

describe("AgentDelegationPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getA2ACapabilities.mockResolvedValue({
      allowAuthNone: false,
      authModes: ["AGENT_ACCESS"],
      softDisable: true,
    });
    mocks.listDelegationBindings.mockResolvedValue([
      {
        id: "b1",
        targetAgentId: "a2",
        callableName: "call_b",
        description: "old",
        mode: "INLINE",
        contextPolicy: "TASK_ONLY",
        enabled: true,
        version: 1,
      },
    ]);
    mocks.listA2ARemotes.mockResolvedValue([
      {
        id: "r1",
        callableName: "remote_x",
        description: "d",
        endpointUrl: "https://agent.example/a2a",
        agentCardUrl: "",
        allowedHosts: ["agent.example"],
        authSecretRef: "",
        timeoutMs: 60000,
        enabled: true,
        version: 2,
      },
    ]);
    mocks.listA2AExposures.mockResolvedValue([
      {
        id: "e1",
        agentId: "a1",
        publicName: "Pub",
        publicDescription: "desc",
        authMode: "AGENT_ACCESS",
        enabled: false,
        version: 3,
      },
    ]);
    mocks.updateDelegationBinding.mockResolvedValue({});
    mocks.updateA2AExposure.mockResolvedValue({});
    mocks.updateA2ARemote.mockResolvedValue({});
  });

  it("loads disabled items and supports optimistic version PATCH save/enable", async () => {
    const wrapper = mount(AgentDelegationPanel, {
      props: {
        workspaceId: "ws1",
        agentId: "a1",
        agentOptions: [
          { id: "a1", name: "Agent A" },
          { id: "a2", name: "Agent B" },
        ],
      },
    });
    await flushPromises();
    expect(wrapper.text()).toContain("call_b");
    expect(wrapper.text()).toContain("已停用");
    expect(wrapper.text()).toContain("remote_x");
    // authModes is authoritative: NONE absent when not listed by capabilities.
    const authSelects = wrapper.findAllComponents({ name: "AppSelect" });
    const authOptions = authSelects.flatMap((s) => (s.props("options") as { value: string }[]) || []);
    expect(authOptions.some((o) => o.value === "NONE")).toBe(false);

    // Re-enable exposure (optimistic version) — only disabled items have active 重新启用.
    const enableBtns = wrapper
      .findAll("button")
      .filter((b) => b.text() === "重新启用" && !(b.element as HTMLButtonElement).disabled);
    expect(enableBtns.length).toBeGreaterThan(0);
    await enableBtns[0].trigger("click");
    await flushPromises();
    expect(mocks.updateA2AExposure).toHaveBeenCalledWith(
      "ws1",
      "e1",
      expect.objectContaining({ expectedVersion: 3, enabled: true }),
    );

    // Save binding fields.
    const saveBinding = wrapper.find('[data-testid="save-binding"]');
    await saveBinding.trigger("click");
    await flushPromises();
    expect(mocks.updateDelegationBinding).toHaveBeenCalledWith(
      "ws1",
      "b1",
      expect.objectContaining({ expectedVersion: 1, mode: "INLINE" }),
    );

    // Save remote full fields.
    const saveRemote = wrapper.find('[data-testid="save-remote"]');
    await saveRemote.trigger("click");
    await flushPromises();
    expect(mocks.updateA2ARemote).toHaveBeenCalledWith(
      "ws1",
      "r1",
      expect.objectContaining({
        expectedVersion: 2,
        endpointUrl: "https://agent.example/a2a",
        allowedHosts: ["agent.example"],
        timeoutMs: 60000,
      }),
    );
  });

  it("lists NONE when capabilities.authModes includes it", async () => {
    mocks.getA2ACapabilities.mockResolvedValue({
      allowAuthNone: true,
      authModes: ["AGENT_ACCESS", "NONE"],
      softDisable: true,
    });
    const wrapper = mount(AgentDelegationPanel, {
      props: {
        workspaceId: "ws1",
        agentId: "a1",
        agentOptions: [{ id: "a2", name: "Agent B" }],
      },
    });
    await flushPromises();
    expect(mocks.getA2ACapabilities).toHaveBeenCalledWith("ws1");
    const authSelects = wrapper.findAllComponents({ name: "AppSelect" });
    const authOptions = authSelects.flatMap((s) => (s.props("options") as { value: string }[]) || []);
    expect(authOptions.some((o) => o.value === "NONE")).toBe(true);
  });

  it("saves full binding and remote field edit payloads", async () => {
    const wrapper = mount(AgentDelegationPanel, {
      props: {
        workspaceId: "ws1",
        agentId: "a1",
        agentOptions: [
          { id: "a1", name: "Agent A" },
          { id: "a2", name: "Agent B" },
        ],
      },
    });
    await flushPromises();
    const callable = wrapper.find('[data-testid="edit-binding-callable"]');
    await callable.setValue("call_b_renamed");
    await wrapper.find('[data-testid="save-binding"]').trigger("click");
    await flushPromises();
    expect(mocks.updateDelegationBinding).toHaveBeenCalledWith(
      "ws1",
      "b1",
      expect.objectContaining({
        expectedVersion: 1,
        callableName: "call_b_renamed",
        targetAgentId: "a2",
      }),
    );

    const remoteCallable = wrapper.find('[data-testid="edit-remote-callable"]');
    await remoteCallable.setValue("remote_y");
    await wrapper.find('[data-testid="save-remote"]').trigger("click");
    await flushPromises();
    expect(mocks.updateA2ARemote).toHaveBeenCalledWith(
      "ws1",
      "r1",
      expect.objectContaining({
        expectedVersion: 2,
        callableName: "remote_y",
        endpointUrl: "https://agent.example/a2a",
      }),
    );

    // Single secret-ref input for existing remote (no duplicate v-model).
    const secretInputs = wrapper.findAll('[data-testid="edit-remote-secret"]');
    expect(secretInputs.length).toBe(1);
  });
});
