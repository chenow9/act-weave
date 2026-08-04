import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { flushPromises, mount } from "@vue/test-utils";
import { reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { setI18nLocale } from "../i18n";
import { createTestI18n } from "../test-utils/i18n";
import ChatExecutionView from "./ChatExecutionView.vue";

const currentDir = dirname(fileURLToPath(import.meta.url));
const chatModelSource = readFileSync(resolve(currentDir, "../composables/chat-execution-page-model.ts"), "utf8");

const fixture = vi.hoisted(() => ({
  chat: null as any,
  workspaces: null as any,
  agents: null as any,
  auth: null as any,
  tools: null as any,
  connections: null as any,
}));

vi.mock("../stores/chat", () => ({ useChatStore: () => fixture.chat }));
vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixture.workspaces }));
vi.mock("../stores/agents", () => ({ useAgentStore: () => fixture.agents }));
vi.mock("../stores/auth", () => ({ useAuthStore: () => fixture.auth }));
vi.mock("../stores/tools", () => ({ useToolsStore: () => fixture.tools }));
vi.mock("../stores/connections", () => ({ useConnectionsStore: () => fixture.connections }));
vi.mock("../utils/markdown", () => ({ renderMarkdown: (value: string) => value }));
vi.mock("../services/api", () => ({
  toAPIError: (error: unknown) => ({
    message: error instanceof Error ? error.message : String(error),
  }),
}));

function createChatStore() {
  const session = {
    id: "session-1",
    workspaceId: "ws-1",
    agentId: "agent-1",
    title: "调试会话",
    status: "ACTIVE",
    lockVersion: 3,
    createdAt: "2026-07-26T00:00:00Z",
    updatedAt: "2026-07-26T00:00:00Z",
  };
  let resolveArchive!: (value: typeof session) => void;
  const archivePromise = new Promise<typeof session>((resolve) => {
    resolveArchive = resolve;
  });
  const store = reactive({
    sessions: [session],
    activeSessionId: session.id,
    activeSession: session,
    messages: [{ id: "m-1", role: "user", content: "hello", createdAt: "2026-07-26T00:00:00Z" }],
    runStatus: undefined as string | undefined,
    latestRun: undefined,
    latestRunId: undefined,
    latestRunSteps: [] as unknown[],
    pendingConfirmation: undefined,
    archiveSession: vi.fn(async () => {
      store.activeSession = await archivePromise;
      store.sessions = [store.activeSession];
      return store.activeSession;
    }),
    loadSession: vi.fn(async () => session),
    loadSessions: vi.fn(async () => [session]),
    createSession: vi.fn(async () => session),
    sendMessage: vi.fn(async () => undefined),
    confirmPending: vi.fn(async () => undefined),
    cancelPending: vi.fn(async () => undefined),
    closeRunStream: vi.fn(),
    subscribeRunStream: vi.fn(),
    clear: vi.fn(),
  });
  return { store, resolveArchive, session };
}

function mountView() {
  if (!Element.prototype.scrollTo) {
    Element.prototype.scrollTo = function scrollTo() {};
  }
  return mount(ChatExecutionView, {
    attachTo: document.body,
    global: {
      plugins: [createTestI18n("zh-CN")],
      directives: {
        loading: () => undefined,
      },
      stubs: {
        RouterLink: { template: "<a><slot /></a>" },
        teleport: true,
      },
    },
  });
}

describe("chat execution view FE-02 archive busy", () => {
  let archiveControl: ReturnType<typeof createChatStore>;

  beforeEach(() => {
    document.body.innerHTML = "";
    setI18nLocale("zh-CN");
    archiveControl = createChatStore();
    fixture.chat = archiveControl.store;
    fixture.workspaces = reactive({
      items: [{ id: "ws-1", name: "ws", displayName: "空间一" }],
      activeWorkspaceId: "ws-1",
      load: vi.fn(async () => undefined),
      can: vi.fn(() => true),
      roleFor: vi.fn(() => "EDITOR"),
    });
    fixture.agents = reactive({
      items: [{ id: "agent-1", workspaceId: "ws-1", name: "Agent One", status: "Active" }],
      load: vi.fn(async () => undefined),
      loadAgents: vi.fn(async () => undefined),
    });
    fixture.workspaces.selectWorkspace = vi.fn();
    fixture.auth = reactive({
      user: { id: "user-1", username: "ops", displayName: "Ops" },
    });
    fixture.tools = reactive({
      attachChatOutboundCredentials: vi.fn(async () => ({ ok: true })),
    });
    fixture.connections = reactive({
      serviceConnections: [],
      serviceConnectionCatalog: [],
      loadServiceConnectionCatalog: vi.fn(async () => []),
    });
    vi.clearAllMocks();
  });

  it("guards double archive clicks to a single store POST and exposes non-danger busy state", async () => {
    const wrapper = mountView();
    await flushPromises();

    const button = wrapper.get(".chat-inline-action");
    expect(button.text()).toBe("归档");
    expect(button.attributes("title")).toContain("消息会永久保留");
    expect(button.classes().join(" ")).not.toMatch(/danger|destructive/i);

    await button.trigger("click");
    await button.trigger("click");
    await flushPromises();

    expect(fixture.chat.archiveSession).toHaveBeenCalledTimes(1);
    expect(wrapper.get(".chat-inline-action").attributes("disabled")).toBeDefined();
    expect(wrapper.get(".chat-inline-action").attributes("aria-busy")).toBe("true");
    expect(wrapper.get(".chat-inline-action").text()).toBe("归档中…");

    archiveControl.resolveArchive({
      ...archiveControl.session,
      status: "ARCHIVED",
      lockVersion: 4,
    });
    await flushPromises();

    expect(wrapper.find(".chat-inline-action").exists()).toBe(false);
    expect(wrapper.text()).toContain("已归档");
    expect(fixture.chat.messages).toHaveLength(1);
    wrapper.unmount();
  });

  it("tracks archivingSession in the page model orchestration surface", () => {
    expect(chatModelSource).toContain("archivingSession");
  });
});
