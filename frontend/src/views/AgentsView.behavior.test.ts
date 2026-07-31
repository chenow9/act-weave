import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import AgentsView from "./AgentsView.vue";

const loadAgentsMock = vi.fn();
const loadAgentPageMock = vi.fn();
const loadWorkspacesMock = vi.fn();
const loadModelConfigsMock = vi.fn();
const createAgentMock = vi.fn();
const updateAgentMock = vi.fn();
const enhanceAgentPromptMock = vi.fn();
const deleteAgentMock = vi.fn();
const loadCapabilitiesMock = vi.fn();
const loadAgentCapabilitiesMock = vi.fn();
const bindCapabilityMock = vi.fn();
const unbindCapabilityMock = vi.fn();

const agentStoreState = {
  items: [] as Array<Record<string, unknown>>,
  pageItems: [] as Array<Record<string, unknown>>,
  pagination: { page: 1, pageSize: 10, total: 0, pageSizeOptions: [10, 20, 50] },
  selectedAgentId: "",
  capabilitiesByWorkspace: {} as Record<string, Array<Record<string, unknown>>>,
  bindingsByAgent: {} as Record<string, Array<Record<string, unknown>>>,
};

const workspaceStoreState = {
  items: [] as Array<Record<string, unknown>>,
  activeWorkspaceId: "",
  can: vi.fn(() => true),
  roleFor: vi.fn(() => "EDITOR"),
};

const modelConfigStoreState = {
  items: [] as Array<Record<string, unknown>>,
};

vi.mock("../stores/agents", () => ({
  useAgentStore: () => ({
    ...agentStoreState,
    get selectedAgentId() {
      return agentStoreState.selectedAgentId;
    },
    set selectedAgentId(value: string) {
      agentStoreState.selectedAgentId = value;
    },
    get selectedAgent() {
      return (
        agentStoreState.items.find((agent) => agent.id === agentStoreState.selectedAgentId) ||
        agentStoreState.items[0] ||
        null
      );
    },
    loading: false,
    pageLoading: false,
    pageError: null,
    pageHasLoaded: true,
    loadAgents: loadAgentsMock,
    loadAgentPage: loadAgentPageMock,
    createAgent: createAgentMock,
    updateAgent: updateAgentMock,
    enhanceAgentPrompt: enhanceAgentPromptMock,
    deleteAgent: deleteAgentMock,
    loadCapabilities: loadCapabilitiesMock,
    loadAgentCapabilities: loadAgentCapabilitiesMock,
    bindCapability: bindCapabilityMock,
    unbindCapability: unbindCapabilityMock,
  }),
}));

vi.mock("../stores/workspaces", () => ({
  useWorkspaceStore: () => ({
    ...workspaceStoreState,
    get activeWorkspace() {
      return (
        workspaceStoreState.items.find((workspace) => workspace.id === workspaceStoreState.activeWorkspaceId) ||
        workspaceStoreState.items[0] ||
        null
      );
    },
    loading: false,
    load: loadWorkspacesMock,
  }),
}));

vi.mock("../stores/modelConfigs", () => ({
  useModelConfigStore: () => ({
    ...modelConfigStoreState,
    loading: false,
    loadModelConfigs: loadModelConfigsMock,
  }),
}));

vi.mock("../utils/markdown", () => ({
  renderMarkdown: (source: string, emptyText: string) => source || emptyText,
}));

function makeWorkspace(id: string, name: string) {
  return {
    id,
    name,
    displayName: `${name} Display`,
    owner: "Ops",
    mode: "Production",
    status: "Active",
    defaultAgentId: "agent-1",
    modelConfigId: "model-1",
    healthScore: 100,
    toolCount: 2,
    workflowCount: 1,
    agentCount: 3,
  };
}

function makeModelConfig() {
  return {
    id: "model-1",
    name: "Default model",
    provider: "OpenAI",
    apiKeyMasked: "sk-***",
    apiBase: "https://api.example.com",
    modelName: "gpt-4.1-mini",
    owner: "Ops",
    lastLatencyMs: 123,
    status: "Available",
  };
}

function makeAgent(index: number) {
  return {
    id: `agent-${index}`,
    workspaceId: "workspace-1",
    name: `Agent ${index}`,
    roleDescription: `Role ${index}`,
    modelConfigId: "model-1",
    currentPromptRevisionId: `revision-${index}`,
    systemPrompt: "",
    isDefault: index === 1,
    status: "ACTIVE",
    toolsCount: index,
    workflowsCount: index,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-15T00:00:00Z",
    updatedAt: "2026-07-15T00:00:00Z",
    lockVersion: 3,
  };
}

function seedStores(agentCount: number) {
  agentStoreState.items = Array.from({ length: agentCount }, (_, index) => makeAgent(index + 1));
  agentStoreState.pageItems = [...agentStoreState.items];
  agentStoreState.pagination = { page: 1, pageSize: 10, total: agentCount, pageSizeOptions: [10, 20, 50] };
  agentStoreState.selectedAgentId = (agentStoreState.items[0]?.id as string) || "";
  workspaceStoreState.items = [makeWorkspace("workspace-1", "Workspace One")];
  workspaceStoreState.activeWorkspaceId = "workspace-1";
  modelConfigStoreState.items = [makeModelConfig()];
  agentStoreState.capabilitiesByWorkspace = {};
  agentStoreState.bindingsByAgent = {};
}

function mountAgentsView() {
  return mount(AgentsView, {
    attachTo: document.body,
    global: {
      directives: {
        loading: () => undefined,
      },
      plugins: [createPinia()],
      stubs: {
        AppSelect: {
          props: ["modelValue", "options", "ariaLabel", "ariaRequired"],
          emits: ["update:model-value"],
          template: `
            <div class="app-select-stub" role="combobox" :aria-label="ariaLabel" :aria-required="ariaRequired">
              <button
                v-for="option in options"
                :key="option.value"
                type="button"
                :data-value="option.value"
                @click="$emit('update:model-value', option.value)"
              >{{ option.label }}</button>
            </div>
          `,
        },
        transition: {
          props: ["name"],
          template: "<slot />",
        },
      },
    },
  });
}

async function openAgentRowMenu(wrapper: ReturnType<typeof mountAgentsView>, rowIndex = 0) {
  const triggers = wrapper.findAll('button[aria-label="更多操作"]');
  await triggers[rowIndex].trigger("click");
  await flushPromises();
}

async function selectAgentMenuAction(wrapper: ReturnType<typeof mountAgentsView>, actionKey: string, rowIndex = 0) {
  await openAgentRowMenu(wrapper, rowIndex);
  const item = document.body.querySelector<HTMLButtonElement>(`button[data-action-key="${actionKey}"]`);
  if (!item) throw new Error(`agent menu action ${actionKey} not found`);
  item.click();
  await flushPromises();
}

describe("agents view behavior", () => {
  beforeAll(() => {
    if (typeof Range !== "undefined" && typeof Range.prototype.getClientRects !== "function") {
      Range.prototype.getClientRects = () => [] as unknown as DOMRectList;
    }
    if (typeof Range !== "undefined" && typeof Range.prototype.getBoundingClientRect !== "function") {
      Range.prototype.getBoundingClientRect = () => new DOMRect(0, 0, 0, 0);
    }
  });

  beforeEach(() => {
    document.body.innerHTML = "";
    setActivePinia(createPinia());
    vi.clearAllMocks();
    seedStores(1);
    loadAgentsMock.mockResolvedValue(agentStoreState.items);
    loadAgentPageMock.mockResolvedValue(agentStoreState.pageItems);
    loadWorkspacesMock.mockResolvedValue(workspaceStoreState.items);
    loadModelConfigsMock.mockResolvedValue(modelConfigStoreState.items);
    createAgentMock.mockResolvedValue(makeAgent(99));
    updateAgentMock.mockImplementation((_agentId, agent) => Promise.resolve({ ...agent }));
    enhanceAgentPromptMock.mockImplementation((_agent, input, options) =>
      Promise.resolve({
        runId: options.preview ? "run-preview" : "run-accepted",
        status: "SUCCEEDED",
        preview: options.preview,
        output: `${input}\n\n增强预览`,
        inputObjectId: "input-object",
        outputObjectId: "output-object",
        ...(options.preview ? {} : { acceptedRevisionId: "revision-next", revisionNo: 2 }),
      }),
    );
    deleteAgentMock.mockResolvedValue(undefined);
    loadCapabilitiesMock.mockImplementation(async () => {
      const items = [
        {
          id: "capability-1",
          kind: "TOOL",
          name: "查询订单",
          slug: "lookup-order",
          description: "查询订单",
          status: "ACTIVE",
          activeReleaseId: "release-1",
          boundAgentCount: 0,
          activeRelease: {
            releaseId: "release-1",
            capabilityId: "capability-1",
            kind: "TOOL",
            callableName: "lookup_order",
          },
          createdBy: "user-1",
          updatedBy: "user-1",
          lockVersion: 1,
        },
      ];
      agentStoreState.capabilitiesByWorkspace["workspace-1"] = items;
      return items;
    });
    loadAgentCapabilitiesMock.mockImplementation(async (agent) => {
      agentStoreState.bindingsByAgent[agent.id] = [];
      return [];
    });
    bindCapabilityMock.mockImplementation(async (agent, capabilityId, input) => {
      const saved = { ...input, capabilityId, lockVersion: 1 };
      agentStoreState.bindingsByAgent[agent.id] = [saved];
      return saved;
    });
    unbindCapabilityMock.mockResolvedValue(undefined);
  });

  it("hides pagination when the registry fits on a single page", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    expect(wrapper.find(".management-list-page-navigation").exists()).toBe(false);
    wrapper.unmount();
  });

  it("loads the Agent registry through the server page contract", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    expect(loadAgentPageMock).toHaveBeenCalledWith({
      query: "",
      status: undefined,
      workspaceId: "workspace-1",
      page: 1,
      pageSize: 10,
    });
    wrapper.unmount();
  });

  it("sorts from page one while retaining the Agent page size and filters", async () => {
    agentStoreState.pagination = { page: 3, pageSize: 20, total: 60, pageSizeOptions: [10, 20, 50] };
    const wrapper = mountAgentsView();
    await flushPromises();
    loadAgentPageMock.mockClear();

    await wrapper.get('th[data-column-key="identity"] button').trigger("click");
    await flushPromises();

    expect(loadAgentPageMock).toHaveBeenLastCalledWith({
      query: "",
      status: undefined,
      workspaceId: "workspace-1",
      page: 1,
      pageSize: 20,
      sortBy: "name",
      sortOrder: "asc",
    });
    wrapper.unmount();
  });

  it("shows plain Agent status filters and neutral row selection", async () => {
    seedStores(2);
    agentStoreState.items[1].status = "DISABLED";
    agentStoreState.pageItems = [...agentStoreState.items];
    loadAgentsMock.mockResolvedValue(agentStoreState.items);
    const wrapper = mountAgentsView();

    await flushPromises();

    expect(wrapper.findAll('[role="option"]').map((filter) => filter.text())).toEqual(["全部", "运行中", "暂停"]);
    expect(wrapper.findAll("tbody tr").every((row) => row.classes().includes("is-selection-neutral"))).toBe(true);
    expect(wrapper.get("tbody tr").classes()).toContain("is-selected");
    wrapper.unmount();
  });

  it("keeps Agent row actions menu-only including sole delete without promoting it", async () => {
    const wrapper = mountAgentsView();
    await flushPromises();

    const actionCell = wrapper.get('td[data-column-key="actions"]');
    expect(actionCell.findAll('button[data-action-kind="primary"]')).toHaveLength(0);
    expect(actionCell.get('button[aria-label="更多操作"]').exists()).toBe(true);

    await actionCell.get('button[aria-label="更多操作"]').trigger("click");
    await flushPromises();
    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多操作"]');
    expect(menu).not.toBeNull();
    expect(
      Array.from(menu!.querySelectorAll("[data-action-key]")).map((button) => button.getAttribute("data-action-key")),
    ).toEqual(["debug", "capabilities", "delete"]);
    expect(menu!.querySelector<HTMLButtonElement>('button[data-action-key="delete"]')?.disabled).toBe(true);
    expect(menu!.querySelector<HTMLButtonElement>('button[data-action-key="delete"]')?.title).toBe(
      "默认 Agent 不能删除",
    );
    wrapper.unmount();
  });

  it("keeps long Agent identity, workspace, and model content bounded with full-text titles", async () => {
    const wrapper = mountAgentsView();
    await flushPromises();

    expect(wrapper.get(".agent-identity-copy strong").attributes("title")).toBe("Agent 1");
    expect(wrapper.get(".agent-identity-copy small").attributes("title")).toBe("Role 1");
    expect(wrapper.get(".agent-workspace-pill").attributes("title")).toContain("Workspace One");
    expect(wrapper.get(".agent-model-chip").attributes("title")).toBe("gpt-4.1-mini");
    wrapper.unmount();
  });

  it("keeps a dirty delete dialog open on backdrop click and Esc, but allows explicit cancel", async () => {
    seedStores(2);
    loadAgentsMock.mockResolvedValue(agentStoreState.items);
    const wrapper = mountAgentsView();

    await flushPromises();

    await selectAgentMenuAction(wrapper, "delete", 1);
    await wrapper.vm.$nextTick();
    const input = wrapper.find(".agent-delete-confirm-input input");
    await input.setValue("wrong name");

    await wrapper.find(".agent-delete-backdrop").trigger("click");
    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(true);
    expect(wrapper.find(".action-toast").text()).toContain("已禁用当前删除确认弹框的遮罩和 Esc 关闭");

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(true);

    await wrapper.find(".agent-delete-dialog .ghost-button").trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(false);
    wrapper.unmount();
  });

  it("allows an untouched delete dialog to close with Esc", async () => {
    seedStores(2);
    loadAgentsMock.mockResolvedValue(agentStoreState.items);
    const wrapper = mountAgentsView();

    await flushPromises();

    await selectAgentMenuAction(wrapper, "delete", 1);
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(true);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(false);
    wrapper.unmount();
  });

  it("shows pagination when more than one page of agents exists", async () => {
    seedStores(11);
    loadAgentsMock.mockResolvedValue(agentStoreState.items);

    const wrapper = mountAgentsView();

    await flushPromises();

    expect(wrapper.find(".management-list-page-navigation").exists()).toBe(true);
    expect(wrapper.find(".management-list-page-navigation").text()).toContain("1 / 2");
    wrapper.unmount();
  });

  it("keeps a dirty Agent studio open on backdrop click, Esc, and back navigation", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    await wrapper.find(".agent-create-button").trigger("click");
    await wrapper.find(".agent-studio-panel input").setValue("Changed Agent Name");

    await wrapper.find(".agent-studio-backdrop").trigger("click");
    expect(wrapper.find(".agent-studio-panel").exists()).toBe(true);
    expect(wrapper.find(".action-toast").text()).toContain("请先创建/保存 Agent 或放弃改动");

    await wrapper.find(".agent-studio-backdrop").trigger("keydown", { key: "Escape" });
    expect(wrapper.find(".agent-studio-panel").exists()).toBe(true);

    await wrapper.find(".agent-back-button").trigger("click");
    expect(wrapper.find(".agent-studio-panel").exists()).toBe(true);

    await wrapper.find(".agent-studio-actions .ghost-button").trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".agent-studio-panel").exists()).toBe(false);
    wrapper.unmount();
  });

  it("keeps create save disabled until users make a valid explicit edit", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    await wrapper.find(".agent-create-button").trigger("click");
    const saveButton = wrapper.get(".agent-studio-actions .primary-button");
    expect(saveButton.attributes("disabled")).toBeDefined();

    await saveButton.trigger("click");
    expect(createAgentMock).not.toHaveBeenCalled();

    const nameInput = wrapper.get<HTMLInputElement>(".agent-studio-panel input[aria-label='Agent 运行名称']");
    await nameInput.setValue("新的售后 Agent");
    expect(wrapper.get(".agent-studio-actions .primary-button").attributes("disabled")).toBeUndefined();

    await nameInput.setValue("");
    expect(wrapper.get(".agent-studio-actions .primary-button").attributes("disabled")).toBeDefined();
    expect(nameInput.attributes("aria-invalid")).toBe("true");
    wrapper.unmount();
  });

  it("shows inline guidance when a dirty Agent studio close is blocked", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    await wrapper.find(".agent-create-button").trigger("click");
    await wrapper.find(".agent-studio-panel input").setValue("Changed Agent Name");
    await wrapper.find(".agent-studio-backdrop").trigger("click");

    expect(wrapper.find(".agent-studio-inline-warning").exists()).toBe(true);
    expect(wrapper.find(".agent-studio-inline-warning").text()).toContain("已有未保存修改");
    wrapper.unmount();
  });

  it("prevents default Agents from opening the destructive delete confirmation", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    await openAgentRowMenu(wrapper, 0);
    const deleteButton = document.body.querySelector<HTMLButtonElement>('button[data-action-key="delete"]');
    expect(deleteButton?.disabled).toBe(true);
    expect(deleteButton?.title).toBe("默认 Agent 不能删除");
    deleteButton?.click();
    await flushPromises();
    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(false);
    wrapper.unmount();
  });

  it("does not treat prompt enhancement input as an Agent PATCH field", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    await selectAgentMenuAction(wrapper, "debug");
    const input = wrapper.get("textarea[aria-label='AI 整理要求']");
    await input.setValue("强化生产行为边界");

    expect(updateAgentMock).not.toHaveBeenCalled();
    expect(wrapper.get(".agent-studio-actions .primary-button").attributes("disabled")).toBeDefined();
    expect(wrapper.find(".agent-prompt-save-review-dialog").exists()).toBe(false);
    wrapper.unmount();
  });

  it("loads the unified catalog and creates a pinned Connection binding", async () => {
    const wrapper = mountAgentsView();
    await flushPromises();

    await selectAgentMenuAction(wrapper, "capabilities");
    await flushPromises();

    expect(loadCapabilitiesMock).toHaveBeenCalledWith("workspace-1");
    expect(loadAgentCapabilitiesMock).toHaveBeenCalledWith(expect.objectContaining({ id: "agent-1" }));
    expect(wrapper.find(".agent-capability-dialog").text()).toContain("查询订单");
    await wrapper.get('.agent-capability-item .app-select-stub [data-value="PINNED"]').trigger("click");
    await wrapper.get(".agent-capability-item input[placeholder*='Workspace']").setValue("connection-1");
    await wrapper.get(".agent-capability-item .primary-button").trigger("click");
    await flushPromises();

    expect(bindCapabilityMock).toHaveBeenCalledWith(
      expect.objectContaining({ id: "agent-1" }),
      "capability-1",
      expect.objectContaining({
        versionPolicy: "PINNED",
        pinnedReleaseId: "release-1",
        connectionId: "connection-1",
        lockVersion: 0,
      }),
    );
    wrapper.unmount();
  });

  it("batch-binds all unbound catalog capabilities", async () => {
    agentStoreState.capabilitiesByWorkspace["workspace-1"] = [
      {
        id: "capability-1",
        kind: "TOOL",
        name: "查询订单",
        description: "按订单号查询",
        activeReleaseId: "release-1",
        activeRelease: { releaseId: "release-1", releaseNo: 1 },
      },
      {
        id: "capability-2",
        kind: "TOOL",
        name: "创建任务",
        description: "创建识别任务",
        activeReleaseId: "release-2",
        activeRelease: { releaseId: "release-2", releaseNo: 1 },
      },
    ];
    loadCapabilitiesMock.mockResolvedValue(agentStoreState.capabilitiesByWorkspace["workspace-1"]);
    loadAgentCapabilitiesMock.mockResolvedValue([]);
    bindCapabilityMock.mockImplementation(async (agent, capabilityId, input) => {
      const saved = { ...input, capabilityId, lockVersion: 1 };
      const list = agentStoreState.bindingsByAgent[agent.id] || [];
      agentStoreState.bindingsByAgent[agent.id] = [
        ...list.filter((item) => item.capabilityId !== capabilityId),
        saved,
      ];
      return saved;
    });

    const wrapper = mountAgentsView();
    await flushPromises();
    await selectAgentMenuAction(wrapper, "capabilities");
    await flushPromises();

    expect(wrapper.find("[data-action='batch-bind-all-unbound']").exists()).toBe(true);
    await wrapper.get("[data-action='batch-bind-all-unbound']").trigger("click");
    await flushPromises();

    expect(bindCapabilityMock).toHaveBeenCalledTimes(2);
    expect(bindCapabilityMock).toHaveBeenCalledWith(
      expect.objectContaining({ id: "agent-1" }),
      "capability-1",
      expect.objectContaining({ versionPolicy: "FOLLOW_ACTIVE" }),
    );
    expect(bindCapabilityMock).toHaveBeenCalledWith(
      expect.objectContaining({ id: "agent-1" }),
      "capability-2",
      expect.objectContaining({ versionPolicy: "FOLLOW_ACTIVE" }),
    );
    wrapper.unmount();
  });

  it("opens a Weaving preview instead of directly applying enhanced prompt text", async () => {
    const wrapper = mountAgentsView();

    await flushPromises();

    await selectAgentMenuAction(wrapper, "debug");
    await wrapper.get("textarea[aria-label='AI 整理要求']").setValue("强化执行边界");
    await wrapper.get(".agent-weave-button").trigger("click");
    await flushPromises();

    expect(enhanceAgentPromptMock).toHaveBeenCalledWith(expect.objectContaining({ id: "agent-1" }), "强化执行边界", {
      preview: true,
    });
    expect(wrapper.find(".agent-weave-preview-dialog").exists()).toBe(true);
    expect(wrapper.find(".agent-weave-preview-dialog .agent-prompt-diff-viewer").exists()).toBe(true);
    expect(wrapper.find(".agent-weave-preview-dialog .agent-prompt-markdown").exists()).toBe(false);
    expect(wrapper.find(".agent-weave-preview-dialog .agent-prompt-diff-viewer").text()).toContain("当前要求");
    expect(wrapper.find(".agent-weave-preview-dialog .agent-prompt-diff-viewer").text()).toContain("AI 建议");
    expect(wrapper.get("textarea[aria-label='AI 整理要求']").element.value).toBe("强化执行边界");

    await wrapper.get(".agent-weave-preview-dialog .primary-button").trigger("click");
    await flushPromises();

    expect(enhanceAgentPromptMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: "agent-1" }),
      "强化执行边界",
      { preview: false, lockVersion: 3 },
    );
    wrapper.unmount();
  });

  it("keeps destructive delete direct while preserving default Agent protection", async () => {
    seedStores(2);
    loadAgentsMock.mockResolvedValue(agentStoreState.items);
    const wrapper = mountAgentsView();

    await flushPromises();

    await openAgentRowMenu(wrapper, 0);
    expect(document.body.querySelector<HTMLButtonElement>('button[data-action-key="delete"]')?.disabled).toBe(true);
    document.body.querySelector('[role="menu"]')?.remove();
    await selectAgentMenuAction(wrapper, "delete", 1);
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".agent-delete-dialog").exists()).toBe(true);
    wrapper.unmount();
  });

  it("allows keyboard users to select Agents through an explicit row selection button", async () => {
    seedStores(2);
    loadAgentsMock.mockResolvedValue(agentStoreState.items);
    const wrapper = mountAgentsView();

    await flushPromises();

    expect(wrapper.find(".agent-registry-table tbody tr[role='button']").exists()).toBe(false);
    expect(wrapper.find(".agent-registry-table tbody tr[tabindex='0']").exists()).toBe(false);
    const rows = wrapper.findAll(".agent-select-button");
    expect(rows).toHaveLength(2);

    await rows[1].trigger("click");
    expect(agentStoreState.selectedAgentId).toBe("agent-2");

    await rows[0].trigger("click");
    expect(agentStoreState.selectedAgentId).toBe("agent-1");
    wrapper.unmount();
  });
});
