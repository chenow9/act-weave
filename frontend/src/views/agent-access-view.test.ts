import { computed, reactive } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestI18n } from "../test-utils/i18n";
import AgentAccessView from "./AgentAccessView.vue";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));

const fixture = reactive({ canManage: true });
const client = {
  id: "client-1",
  workspaceId: "workspace-1",
  servicePrincipalId: "principal-1",
  clientId: "awcl_public",
  name: "Business App",
  status: "ACTIVE",
  authMethod: "client_secret_basic",
  allowedCorsOrigins: ["https://app.example.com"],
  tokenTtlSeconds: 600,
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
  lockVersion: 1,
} as const;
const credential = {
  id: "credential-1",
  type: "client_secret",
  publicHint: "…safe",
  validFrom: "2026-07-20T00:00:00Z",
  createdAt: "2026-07-20T00:00:00Z",
  lockVersion: 1,
} as const;
const access = reactive({
  clients: [client],
  selectedClientId: "" as string,
  credentials: [] as (typeof credential)[],
  grants: [] as unknown[],
  loading: false,
  detailLoading: false,
  mutating: false,
  error: "",
  hasLoaded: true,
  selectedClient: computed(() => access.clients.find((item) => item.id === access.selectedClientId)),
  activeCredentials: computed(() => access.credentials.filter((item) => !("revokedAt" in item && item.revokedAt))),
  activeGrants: computed(() => []),
  load: vi.fn(async () => [client]),
  loadClientDetail: vi.fn(async (clientId: string) => {
    access.selectedClientId = clientId;
    access.credentials = [credential];
    access.grants = [];
  }),
  clearSelection: vi.fn(() => {
    access.selectedClientId = "";
    access.credentials = [];
    access.grants = [];
  }),
  createClient: vi.fn(async () => {
    access.selectedClientId = client.id;
    access.credentials = [credential];
    return { client, credential, secret: "awsk_live_once" };
  }),
  setClientStatus: vi.fn(async () => client),
  rotateCredential: vi.fn(),
  createGrant: vi.fn(),
  revokeCredential: vi.fn(async () => credential),
  revokeGrant: vi.fn(),
});
const workspaces = reactive({
  activeWorkspaceId: "workspace-1",
  items: [{ id: "workspace-1", displayName: "Core" }],
  load: vi.fn(),
  loadMemberRoles: vi.fn(),
  requireWorkspace: vi.fn(() => ({ id: "workspace-1" })),
  can: vi.fn(() => fixture.canManage),
});
const agents = reactive({
  items: [{ id: "agent-1", workspaceId: "workspace-1", name: "Ops", status: "ACTIVE" }],
  loadAgents: vi.fn(),
});

vi.mock("../stores/agentAccess", () => ({ useAgentAccessStore: () => access }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => workspaces }));
vi.mock("../stores/agents", () => ({ useAgentStore: () => agents }));
vi.mock("../stores/auth", () => ({ useAuthStore: () => ({ user: { id: "user-1" } }) }));

describe("Agent Access management view", () => {
  beforeEach(() => {
    fixture.canManage = true;
    access.selectedClientId = "";
    access.credentials = [];
    access.grants = [];
    vi.clearAllMocks();
  });

  it("hides every write entry point for users without Workspace MANAGE", async () => {
    fixture.canManage = false;
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.get('[data-testid="readonly-notice"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="create-client"]').exists()).toBe(false);
    // Enter detail from list — write actions still hidden.
    await wrapper.get('[data-testid="select-client-client-1"]').trigger("click");
    await flushPromises();
    const actions = wrapper.findAll("button").map((button) => button.text());
    expect(actions).not.toContain("轮换凭证");
    expect(actions).not.toContain("撤销");
    expect(actions).not.toContain("禁用 Client");
  });

  it("shows a creation Secret once and clears it when the modal closes", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-testid="create-client"]').trigger("click");
    const dialog = wrapper.get('[role="dialog"][aria-label="注册 Agent Access Client"]');
    await dialog.get('input[placeholder="例如：会员运营 App"]').setValue("Business App");
    const submit = dialog.findAll("button").find((button) => button.text().includes("创建 Client"));
    await submit?.trigger("click");
    await flushPromises();
    expect(wrapper.get('[data-testid="one-time-secret"]').text()).toBe("awsk_live_once");
    const close = wrapper.findAll("button").find((button) => button.text().includes("我已安全保存"));
    await close?.trigger("click");
    expect(wrapper.find('[data-testid="one-time-secret"]').exists()).toBe(false);
    expect(JSON.stringify(access)).not.toContain("awsk_live_once");
  });

  it("opens client detail from the list and requires REVOKE before destructive actions", async () => {
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.find('[data-testid="select-client-client-1"]').exists()).toBe(true);
    await wrapper.get('[data-testid="select-client-client-1"]').trigger("click");
    await flushPromises();
    expect(access.loadClientDetail).toHaveBeenCalledWith("client-1");
    expect(wrapper.text()).toContain("轮换凭证");
    const revoke = wrapper.findAll("button").find((button) => button.text() === "撤销");
    await revoke?.trigger("click");
    const confirm = wrapper.get('[data-testid="confirm-danger"]');
    expect(confirm.attributes("disabled")).toBeDefined();
    await wrapper.get('[role="alertdialog"] input').setValue("REVOKE");
    expect(wrapper.get('[data-testid="confirm-danger"]').attributes("disabled")).toBeUndefined();
    await wrapper.get('[data-testid="confirm-danger"]').trigger("click");
    await flushPromises();
    expect(access.revokeCredential).toHaveBeenCalledWith("client-1", credential);
  });
});

function mountView() {
  return mount(AgentAccessView, {
    global: {
      plugins: [createTestI18n("zh-CN")],
      stubs: {
        ManagementSummaryStrip: { template: "<div data-testid='summary-strip' />" },
        ManagementPageHeader: {
          template: '<div><slot name="actions" /></div>',
        },
        ManagementList: {
          props: ["rows"],
          emits: ["select-row", "update:search", "reset", "page-change"],
          template: `
            <div data-testid="client-list">
              <button
                v-for="row in rows || []"
                :key="row.id"
                type="button"
                :data-testid="'select-client-' + row.id"
                @click="$emit('select-row', row)"
              >
                {{ row.name }}
              </button>
              <slot name="empty" />
            </div>
          `,
        },
        WorkspaceContextState: { template: "<div />" },
        AppSelect: {
          props: ["modelValue", "options", "ariaLabel"],
          emits: ["update:modelValue"],
          template: `
            <select
              :aria-label="ariaLabel"
              :value="modelValue"
              @change="$emit('update:modelValue', ($event.target).value)"
            >
              <option v-for="option in options || []" :key="String(option.value)" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          `,
        },
      },
    },
  });
}
