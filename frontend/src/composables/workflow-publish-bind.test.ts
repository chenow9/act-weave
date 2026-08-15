import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import { useAgentStore } from "../stores/agents";
import { useSmartDagStore } from "../stores/smartdag";
import { useWorkflowStore } from "../stores/workflow";
import { useWorkspaceStore } from "../stores/workspaces";
import type { Workflow, WorkflowGraphDraft } from "../types/domain";
import { bindPublishedWorkflowToSessionAgent, resolveGenerateAgentId } from "./workflow-publish-bind";

vi.mock("../services/api", () => ({
  apiClient: {
    delete: vi.fn(),
    get: vi.fn(),
    patch: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  toAPIError: (error: { message?: string; response?: { status?: number } }) => ({
    message: error.message || "request failed",
    status: error.response?.status,
  }),
}));

const graph: WorkflowGraphDraft = {
  schemaVersion: "workflow.graph.v1",
  nodes: [],
  edges: [],
  viewport: { x: 0, y: 0, zoom: 1 },
  ui: { generatedBy: "smart-dag.v2", agentId: "agent-draft" },
};

function workflow(id = "wf-order"): Workflow {
  return {
    id,
    workspaceId: "order",
    currentDraftId: `draft-${id}`,
    latestCompilationId: "comp-1",
    name: "AI · 供应商准入",
    slug: id,
    description: "",
    status: "Draft",
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
    nodeCount: 0,
    edgeCount: 0,
  };
}

function draftRecord(overrides: Record<string, unknown> = {}) {
  return {
    id: "draft-wf-order",
    workflowId: "wf-order",
    draftVersion: 3,
    schemaVersion: "workflow.graph.v1",
    graph,
    graphHash: "sha256:graph",
    updatedBy: "user-1",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 3,
    etag: '"draft-3-3"',
    ...overrides,
  };
}

function seedWorkflow(store = useWorkflowStore()) {
  store.workflows = [
    {
      ...workflow(),
      workspaceName: "订单中心",
    },
  ];
}

describe("resolveGenerateAgentId", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
    useWorkspaceStore().items = [
      {
        id: "order",
        name: "order",
        displayName: "订单中心",
        owner: "ops",
        mode: "PRODUCTION",
        status: "ACTIVE",
        defaultAgentId: "",
        modelConfigId: "",
        healthScore: 100,
        toolCount: 0,
        workflowCount: 1,
        agentCount: 1,
      },
    ];
    seedWorkflow();
  });

  it("uses matching activeDraft ui.agentId without GET", async () => {
    useWorkflowStore().activeDraft = draftRecord();
    const agentId = await resolveGenerateAgentId(workflow());
    expect(agentId).toBe("agent-draft");
    expect(apiClient.get).not.toHaveBeenCalled();
  });

  it("GETs the server draft when activeDraft is missing after refresh", async () => {
    vi.mocked(apiClient.get).mockImplementation(async (url: string) => {
      const path = String(url);
      if (path.endsWith("/draft")) {
        return { data: { ...draftRecord(), graph }, headers: { etag: '"draft-3-3"' } };
      }
      if (path.endsWith("/readiness")) {
        return { data: { stage: "PublishReady", canCompile: true, canTrial: true, canPublish: true, blockers: [] } };
      }
      throw new Error(path);
    });

    const agentId = await resolveGenerateAgentId(workflow());
    expect(agentId).toBe("agent-draft");
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order/draft");
    expect(useWorkflowStore().activeDraft).toBeUndefined();
  });

  it("falls back to session agentId only when sessionWorkflowId matches", async () => {
    useWorkflowStore().activeDraft = draftRecord({
      graph: { ...graph, ui: { generatedBy: "smart-dag.v2" } },
    });
    const smart = useSmartDagStore();
    smart.sessionWorkflowId = "wf-other";
    smart.agentId = "agent-session";
    expect(await resolveGenerateAgentId(workflow())).toBe("");

    smart.sessionWorkflowId = "wf-order";
    expect(await resolveGenerateAgentId(workflow())).toBe("agent-session");
  });

  it("returns empty for hand-authored drafts with no ui.agentId", async () => {
    useWorkflowStore().activeDraft = draftRecord({
      graph: { ...graph, ui: {} },
    });
    expect(await resolveGenerateAgentId(workflow())).toBe("");
  });
});

describe("bindPublishedWorkflowToSessionAgent", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
    useWorkspaceStore().items = [
      {
        id: "order",
        name: "order",
        displayName: "订单中心",
        owner: "ops",
        mode: "PRODUCTION",
        status: "ACTIVE",
        defaultAgentId: "",
        modelConfigId: "",
        healthScore: 100,
        toolCount: 0,
        workflowCount: 1,
        agentCount: 1,
      },
    ];
    seedWorkflow();
  });

  it("bindCapability using GET draft ui.agentId after refresh", async () => {
    vi.mocked(apiClient.get).mockImplementation(async (url: string) => {
      const path = String(url);
      if (path.endsWith("/draft")) {
        return { data: { ...draftRecord(), graph }, headers: { etag: '"draft-3-3"' } };
      }
      if (path.endsWith("/readiness")) {
        return { data: { stage: "PublishReady", canCompile: true, canTrial: true, canPublish: true, blockers: [] } };
      }
      if (path.endsWith("/agents")) {
        return {
          data: {
            items: [
              {
                id: "agent-draft",
                name: "草稿 Agent",
                roleDescription: "",
                modelConfigId: "model-1",
                isDefault: false,
                status: "ACTIVE",
                toolsCount: 0,
                workflowsCount: 0,
                createdBy: "user-1",
                updatedBy: "user-1",
                createdAt: "2026-07-01T00:00:00Z",
                updatedAt: "2026-07-01T00:00:00Z",
                lockVersion: 1,
              },
            ],
          },
        };
      }
      throw new Error(path);
    });
    vi.mocked(apiClient.put).mockResolvedValue({
      data: {
        capabilityId: "wf-order",
        versionPolicy: "FOLLOW_ACTIVE",
        enabled: true,
        configOverrides: {},
        lockVersion: 1,
      },
    });

    await bindPublishedWorkflowToSessionAgent(workflow());

    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order/draft");
    expect(apiClient.put).toHaveBeenCalledWith(
      "/workspaces/order/agents/agent-draft/capabilities/wf-order",
      expect.objectContaining({
        versionPolicy: "FOLLOW_ACTIVE",
        enabled: true,
        lockVersion: 0,
      }),
    );
  });

  it("does not throw when GET draft fails and reports onFailure", async () => {
    vi.mocked(apiClient.get).mockImplementation(async (url: string) => {
      const path = String(url);
      if (path.endsWith("/draft")) throw new Error("draft unavailable");
      if (path.endsWith("/readiness")) {
        return { data: { stage: "PublishReady", canCompile: true, canTrial: true, canPublish: true, blockers: [] } };
      }
      throw new Error(path);
    });
    const onFailure = vi.fn();

    await expect(bindPublishedWorkflowToSessionAgent(workflow(), { onFailure })).resolves.toBeUndefined();
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(apiClient.put).not.toHaveBeenCalled();
  });

  it("skips bind when the draft has no generate agentId", async () => {
    useWorkflowStore().activeDraft = draftRecord({
      graph: { ...graph, ui: {} },
    });
    const onFailure = vi.fn();

    await bindPublishedWorkflowToSessionAgent(workflow(), { onFailure });

    expect(onFailure).not.toHaveBeenCalled();
    expect(apiClient.put).not.toHaveBeenCalled();
    expect(useAgentStore().items).toHaveLength(0);
  });
});
