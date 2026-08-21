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
const grant = {
  id: "grant-1",
  agentId: "agent-1",
  scopes: ["agent:read", "run:create", "run:read", "event:read"],
  policy: {},
  status: "ACTIVE",
  validFrom: "2026-07-20T00:00:00Z",
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
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
    access.grants = [grant];
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
    expect(wrapper.get('[data-testid="export-handoff"]').exists()).toBe(true);
  });

  it("exports integrator env without a Client Secret and copies the packet", async () => {
    const writeText = vi.fn(async () => undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-testid="select-client-client-1"]').trigger("click");
    await flushPromises();
    await wrapper.get('[data-testid="export-handoff"]').trigger("click");
    await flushPromises();
    const preview = wrapper.get('[data-testid="export-preview"]').text();
    expect(preview).toContain("AAP_CLIENT_ID=awcl_public");
    expect(preview).toContain("AAP_WORKSPACE_ID=workspace-1");
    expect(preview).toContain("AAP_AGENT_ID=agent-1");
    expect(preview).toContain("AAP_SCOPES=agent:read run:create run:read event:read");
    expect(preview).toContain("AAP_CLIENT_SECRET=");
    expect(preview).not.toMatch(/AAP_CLIENT_SECRET=.+/);
    expect(preview).not.toContain("awsk_live_once");
    await wrapper.get('[data-testid="export-base-url"]').setValue("https://actweave.example.com/api/agent-access/v1");
    expect(wrapper.get('[data-testid="export-preview"]').text()).toContain(
      "AAP_BASE_URL=https://actweave.example.com/api/agent-access/v1",
    );
    await wrapper.get('[data-testid="export-format-json"]').trigger("click");
    const json = wrapper.get('[data-testid="export-preview"]').text();
    expect(JSON.parse(json).secrets.clientSecretIncluded).toBe(false);
    expect(json).not.toContain("awsk_");
    await wrapper.get('[data-testid="export-copy"]').trigger("click");
    expect(writeText).toHaveBeenCalled();
    expect(String(writeText.mock.calls[0]?.[0])).toContain('"clientSecretIncluded": false');
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

  it("copies the one-time secret from an icon in the secret box", async () => {
    const writeText = vi.fn(async () => undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-testid="create-client"]').trigger("click");
    const create = wrapper.get('[role="dialog"][aria-label="注册 Agent Access Client"]');
    await create.get('input[placeholder="例如：会员运营 App"]').setValue("Business App");
    await create
      .findAll("button")
      .find((button) => button.text().includes("创建 Client"))
      ?.trigger("click");
    await flushPromises();
    const copy = wrapper.get('[data-testid="copy-secret"]');
    expect(copy.text().replace(/\s/g, "")).toBe("");
    expect(copy.attributes("aria-label")).toBe("复制到剪贴板");
    await copy.trigger("click");
    await flushPromises();
    expect(writeText).toHaveBeenCalledWith("awsk_live_once");
    expect(wrapper.text()).toContain("已复制");
  });

  it("copies the one-time secret when the clipboard API is missing", async () => {
    Object.assign(navigator, { clipboard: undefined });
    const execCommand = vi.fn(() => true);
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      writable: true,
      value: execCommand,
    });
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-testid="create-client"]').trigger("click");
    const create = wrapper.get('[role="dialog"][aria-label="注册 Agent Access Client"]');
    await create.get('input[placeholder="例如：会员运营 App"]').setValue("Business App");
    await create
      .findAll("button")
      .find((button) => button.text().includes("创建 Client"))
      ?.trigger("click");
    await flushPromises();
    await wrapper.get('[data-testid="copy-secret"]').trigger("click");
    await flushPromises();
    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(wrapper.text()).toContain("已复制");
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
