import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import type { VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import { useWorkspaceStore } from "../stores/workspaces";
import SmartDagView from "./SmartDagView.vue";

const routerPushMock = vi.fn();
let wrapper: VueWrapper | undefined;

vi.mock("vue-router", () => ({
  useRouter: () => ({ push: routerPushMock }),
  useRoute: () => ({ query: {}, params: {}, path: "/smart-dag", name: "smart-dag" }),
}));

vi.mock("../services/api", () => ({
  apiClient: {
    delete: vi.fn(),
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  getAuthToken: () => "test-token",
  apiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error ? fallback + "（" + error.message + "）" : fallback,
  toAPIError: (error: { message?: string; response?: { status?: number }; code?: string }) => ({
    message: error.message || "request failed",
    status: error.response?.status,
    code: error.code || "ERROR",
  }),
}));

vi.mock("../services/llm-job-sse", () => ({
  postLlmJobSse: vi.fn(async (options: { path: string; body: unknown }) => {
    const { apiClient } = await import("../services/api");
    const res = await apiClient.post(options.path, options.body);
    return (res as { data: unknown }).data;
  }),
}));

const graph = {
  schemaVersion: "workflow.graph.v1",
  nodes: [
    {
      id: "start",
      type: "Start",
      label: "接收请求",
      position: { x: 120, y: 240 },
      ports: [{ key: "output", label: "Output", direction: "output" }],
      data: {},
      ui: { generated: true },
    },
    {
      id: "result",
      type: "Transform",
      label: "整理结果",
      position: { x: 420, y: 240 },
      ports: [
        { key: "input", label: "Input", direction: "input" },
        { key: "output", label: "Output", direction: "output" },
      ],
      data: { template: "done" },
      ui: { generated: true },
    },
    {
      id: "end",
      type: "End",
      label: "返回结果",
      position: { x: 720, y: 240 },
      ports: [{ key: "input", label: "Input", direction: "input" }],
      data: {},
      ui: { generated: true },
    },
  ],
  edges: [
    {
      id: "start-result",
      sourceNodeId: "start",
      sourcePort: "output",
      targetNodeId: "result",
      targetPort: "input",
      data: {},
      ui: {},
    },
    {
      id: "result-end",
      sourceNodeId: "result",
      sourcePort: "output",
      targetNodeId: "end",
      targetPort: "input",
      data: {},
      ui: {},
    },
  ],
  viewport: { x: 0, y: 0, zoom: 1 },
  ui: { generatedBy: "smart-dag.v1", businessGoal: "生成测试流程" },
};

const workflow = {
  id: "workflow-ai-1",
  currentDraftId: "draft-ai-1",
  name: "AI · 测试流程",
  slug: "ai-test",
  description: "智能生成测试流程",
  status: "ACTIVE",
  createdBy: "user-1",
  updatedBy: "user-1",
  createdAt: "2026-07-15T00:00:00Z",
  updatedAt: "2026-07-15T00:00:00Z",
  lockVersion: 1,
  nodeCount: 3,
  edgeCount: 2,
};

function draft(version = 1, lockVersion = 1) {
  return {
    id: "draft-ai-1",
    draftVersion: version,
    schemaVersion: "workflow.graph.v1",
    graph,
    graphHash: "graph-hash-" + version,
    updatedBy: "user-1",
    updatedAt: "2026-07-15T00:00:00Z",
    lockVersion,
  };
}

function readiness(overrides: Record<string, unknown> = {}) {
  return {
    stage: "TRIAL_REQUIRED",
    canCompile: true,
    canTrial: true,
    canPublish: false,
    compilationId: "comp-ai-1",
    compilationCurrent: true,
    compilationValid: true,
    trialCurrent: false,
    trialSuccessful: false,
    published: false,
    blockers: [],
    updatedAt: "2026-07-15T00:00:00Z",
    ...overrides,
  };
}

const agent = {
  id: "agent-1",
  name: "编排 Agent",
  roleDescription: "generate",
  modelConfigId: "model-1",
  isDefault: true,
  status: "ACTIVE",
  toolsCount: 0,
  workflowsCount: 0,
  createdBy: "user-1",
  updatedBy: "user-1",
  createdAt: "2026-07-15T00:00:00Z",
  updatedAt: "2026-07-15T00:00:00Z",
  lockVersion: 1,
};

function configureAPI() {
  vi.mocked(apiClient.get).mockImplementation(async (url: string) => {
    if (url.endsWith("/workflows")) return { data: { items: [] } };
    if (url.endsWith("/agents")) return { data: { items: [agent] } };
    if (url.endsWith("/model-configs")) {
      return {
        data: {
          items: [
            {
              id: "model-1",
              name: "test-model",
              provider: "openai",
              apiBase: "https://example.com/v1",
              modelName: "gpt-test",
              status: "VERIFIED",
              createdBy: "user-1",
              updatedBy: "user-1",
              createdAt: "2026-07-15T00:00:00Z",
              updatedAt: "2026-07-15T00:00:00Z",
              lockVersion: 1,
            },
          ],
        },
      };
    }
    if (url.endsWith("/readiness")) return { data: readiness() };
    if (url.endsWith("/draft")) return { data: draft(), headers: { etag: '"draft-1-1"' } };
    if (url.endsWith("/workflow-ai-1")) return { data: workflow };
    // Workflow tool catalog may prefetch paginated tools when inspectors open.
    if (url.includes("/tools?") || url.endsWith("/tools")) {
      return { data: { items: [], pagination: { page: 1, pageSize: 50, total: 0 } } };
    }
    throw new Error("Unexpected GET " + url);
  });
  vi.mocked(apiClient.post).mockImplementation(async (url: string) => {
    if (url.endsWith("/workflow-generate-sessions")) {
      return {
        data: {
          sessionId: "session-1",
          agentId: "agent-1",
          modelConfigId: "model-1",
          status: "OPEN",
        },
      };
    }
    if (url.includes("/workflow-generate-sessions/") && url.endsWith("/turns")) {
      return {
        data: {
          sessionId: "session-1",
          turnId: "turn-1",
          generationId: "gen-1",
          workflow,
          draft: draft(),
          assistantMessage: "ok",
          reasoningSteps: [],
          missingCapabilities: [],
          nodeExplanations: [],
          availableToolIds: [],
          selectedToolIds: [],
          confidence: 92,
          guardReport: { ok: true, violations: [] },
          draftVersion: 1,
          generatedBy: "smart-dag.v2",
        },
        headers: { etag: '"draft-1-1"' },
      };
    }
    if (url.endsWith("/workflows:generate")) {
      return {
        data: {
          workflow,
          draft: draft(),
          reasoningSteps: [],
          missingCapabilities: [],
          nodeExplanations: [],
          availableToolIds: [],
          selectedToolIds: [],
          reasoning: "formal v1 draft",
          confidence: 92,
        },
        headers: { etag: '"draft-1-1"' },
      };
    }
    if (url.endsWith("/draft:compile")) {
      return {
        data: {
          id: "comp-ai-1",
          draftId: "draft-ai-1",
          draftVersion: 2,
          graphHash: "graph-hash-2",
          compilerVersion: "workflowcompiler.v1",
          status: "VALID",
          spec: { workflowId: workflow.id, nodes: [] },
          plan: { workflowId: workflow.id, nodes: [] },
          issues: [],
          planHash: "plan-hash",
          compiledBy: "user-1",
          compiledAt: "2026-07-15T00:00:00Z",
        },
      };
    }
    if (url.includes("/compilations/comp-ai-1:trial")) {
      return {
        data: {
          id: "trial-1",
          compilationId: "comp-ai-1",
          executionId: "execution-1",
          status: "SUCCEEDED",
          inputHash: "hash",
          startedBy: "user-1",
          startedAt: "2026-07-15T00:00:00Z",
        },
      };
    }
    throw new Error("Unexpected POST " + url);
  });
  vi.mocked(apiClient.put).mockResolvedValue({ data: draft(2, 2), headers: { etag: '"draft-2-2"' } });
}

function mountSmartDagView() {
  const pinia = createPinia();
  setActivePinia(pinia);
  const workspaces = useWorkspaceStore();
  workspaces.items = [
    {
      id: "workspace-1",
      name: "workspace-1",
      displayName: "测试空间",
      mode: "PRODUCTION",
      status: "ACTIVE",
      defaultAgentId: "",
      modelConfigId: "",
      healthScore: 100,
    },
  ];
  workspaces.activeWorkspaceId = "workspace-1";
  wrapper = mount(SmartDagView, { attachTo: document.body, global: { plugins: [pinia] } });
  return wrapper;
}

async function generateFormalDraft(mounted: VueWrapper) {
  await flushPromises();
  // AppSelect is used for workspace + agent; pick agent (2nd) when present.
  const appSelects = mounted.findAllComponents({ name: "AppSelect" });
  if (appSelects.length >= 2) {
    await appSelects[1].vm.$emit("update:modelValue", "agent-1");
  } else {
    const agentSelect = mounted.findAll(".smart-copilot-panel select")[1];
    if (agentSelect) {
      await agentSelect.setValue("agent-1");
    }
  }
  await flushPromises();
  await mounted.get(".smart-copilot-panel textarea").setValue("生成测试流程");
  await mounted.get(".smart-copilot-actions .primary").trigger("click");
  await flushPromises();
  expect(mounted.text()).toContain("AI · 测试流程");
}

describe("smart dag view behavior", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    configureAPI();
    document.body.innerHTML = "";
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      callback(0);
      return 1;
    });
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("removes the editor surface from keyboard and accessibility access at or below 1180px", async () => {
    for (const width of [1024, 1100, 1180]) {
      vi.stubGlobal("innerWidth", width);
      const mounted = mountSmartDagView();
      await flushPromises();
      expect(mounted.get(".smart-orchestration-main").attributes("inert")).toBeDefined();
      expect(mounted.get(".smart-orchestration-main").attributes("aria-hidden")).toBe("true");
      mounted.unmount();
      wrapper = undefined;
    }
  });

  it("keeps the editor surface accessible above the desktop breakpoint", async () => {
    vi.stubGlobal("innerWidth", 1181);
    const mounted = mountSmartDagView();
    await flushPromises();
    expect(mounted.get(".smart-orchestration-main").attributes("inert")).toBeUndefined();
    expect(mounted.get(".smart-orchestration-main").attributes("aria-hidden")).toBeUndefined();
  });

  it("keeps invalid sandbox JSON inside the modal with an inline alert", async () => {
    vi.stubGlobal("innerWidth", 1400);
    const mounted = mountSmartDagView();
    await flushPromises();
    await generateFormalDraft(mounted);
    await mounted.get('button[aria-label="打开模拟试运行"]').trigger("click");
    const textarea = mounted.get(".smart-trial-modal textarea");
    await textarea.setValue("{bad");
    await mounted.get(".smart-trial-modal footer .primary").trigger("click");
    await flushPromises();
    expect(mounted.find(".smart-trial-modal").exists()).toBe(true);
    expect(mounted.get(".smart-trial-error").attributes("role")).toBe("alert");
    expect(mounted.get(".smart-trial-error").text()).toContain("JSON 格式错误");
    expect(mounted.get(".smart-trial-modal textarea").attributes("aria-invalid")).toBe("true");
  });

  it("runs the persisted draft through compile and trial before closing the modal", async () => {
    vi.stubGlobal("innerWidth", 1400);
    const mounted = mountSmartDagView();
    await flushPromises();
    await generateFormalDraft(mounted);
    const launcher = mounted.get('button[aria-label="打开模拟试运行"]');
    (launcher.element as HTMLElement).focus();
    await launcher.trigger("click");
    await mounted.get(".smart-trial-modal textarea").setValue('{"ticketId":"CS-1"}');
    await mounted.get(".smart-trial-modal footer .primary").trigger("click");
    await flushPromises();
    expect(apiClient.put).toHaveBeenCalled();
    expect(vi.mocked(apiClient.post).mock.calls.some(([url]) => String(url).endsWith("/draft:compile"))).toBe(true);
    expect(vi.mocked(apiClient.post).mock.calls.some(([url]) => String(url).includes(":trial"))).toBe(true);
    expect(mounted.find(".smart-trial-modal").exists()).toBe(false);
    expect(document.activeElement).toBe(launcher.element);
  });
});
