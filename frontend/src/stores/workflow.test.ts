import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { Workflow, WorkflowGraphDraft, WorkflowSummary, Workspace } from "../types/domain";
import { useWorkspaceStore } from "./workspaces";
import { useWorkflowStore, WorkflowDraftConflictError } from "./workflow";

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
  nodes: [
    {
      id: "start",
      type: "Start",
      label: "Start",
      position: { x: 0, y: 0 },
      ports: [{ key: "output", label: "Output", direction: "output" }],
      data: {},
      ui: {},
    },
    {
      id: "end",
      type: "End",
      label: "End",
      position: { x: 300, y: 0 },
      ports: [{ key: "input", label: "Input", direction: "input" }],
      data: {},
      ui: {},
    },
  ],
  edges: [
    {
      id: "start-end",
      sourceNodeId: "start",
      sourcePort: "output",
      targetNodeId: "end",
      targetPort: "input",
      data: {},
      ui: {},
    },
  ],
  viewport: { x: 0, y: 0, zoom: 1 },
  ui: {},
};

function workspace(id = "order"): Workspace {
  return {
    id,
    name: id,
    displayName: id,
    owner: "ops",
    mode: "Production",
    status: "Active",
    defaultAgentId: "",
    modelConfigId: "",
    healthScore: 100,
    toolCount: 0,
    workflowCount: 1,
    agentCount: 0,
  };
}

function workflowDTO(id = "wf-order") {
  return {
    id,
    currentDraftId: `draft-${id}`,
    activeRevisionId: undefined,
    latestCompilationId: "comp-1",
    name: "订单取消编排",
    slug: "order-cancel",
    description: "查询订单状态并决定后续动作。",
    status: "ACTIVE",
    createdBy: "user-1",
    updatedBy: "user-2",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 3,
    nodeCount: 2,
    edgeCount: 1,
  };
}

function readinessDTO(overrides: Record<string, unknown> = {}) {
  return {
    stage: "TRIAL_REQUIRED",
    canCompile: true,
    canTrial: true,
    canPublish: false,
    compilationId: "comp-1",
    compilationCurrent: true,
    compilationValid: true,
    trialCurrent: false,
    trialSuccessful: false,
    published: false,
    blockers: [],
    updatedAt: "2026-07-02T01:00:00Z",
    ...overrides,
  };
}

function draftDTO(overrides: Record<string, unknown> = {}) {
  return {
    id: "draft-wf-order",
    draftVersion: 4,
    schemaVersion: "workflow.graph.v1",
    graph,
    graphHash: "sha256:graph-4",
    updatedBy: "user-2",
    updatedAt: "2026-07-02T02:00:00Z",
    lockVersion: 7,
    ...overrides,
  };
}

function compilationDTO(overrides: Record<string, unknown> = {}) {
  return {
    id: "comp-1",
    draftId: "draft-wf-order",
    draftVersion: 4,
    graphHash: "sha256:graph-4",
    compilerVersion: "workflow-compiler.v1",
    status: "VALID",
    spec: { workflowId: "wf-order", nodes: [] },
    plan: { workflowId: "wf-order", nodes: [] },
    issues: [],
    planHash: "sha256:plan-1",
    compiledBy: "user-2",
    compiledAt: "2026-07-02T02:01:00Z",
    ...overrides,
  };
}

function revisionDTO(id = "rev-immutable-7", revisionNo = 7) {
  return {
    id,
    revisionNo,
    sourceCompilationId: "comp-1",
    draftSnapshot: graph,
    specSnapshot: { workflowId: "wf-order", nodes: [] },
    planSnapshot: { workflowId: "wf-order", nodes: [] },
    planHash: "sha256:plan-1",
    status: "PUBLISHED",
    publishNote: "verified",
    createdBy: "user-2",
    createdAt: "2026-07-02T03:00:00Z",
    activatedAt: "2026-07-02T03:01:00Z",
  };
}

function domainWorkflow(): Workflow {
  return {
    id: "wf-order",
    workspaceId: "order",
    currentDraftId: "draft-wf-order",
    latestCompilationId: "comp-1",
    name: "订单取消编排",
    slug: "order-cancel",
    description: "查询订单状态并决定后续动作。",
    status: "Draft",
    createdBy: "user-1",
    updatedBy: "user-2",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 3,
  };
}

function seedWorkflow() {
  const store = useWorkflowStore();
  const value = domainWorkflow();
  store.workflowDetails[value.id] = value;
  store.workflows = [{ ...value, nodeCount: 2, edgeCount: 1 } satisfies WorkflowSummary];
  store.pageItems = [...store.workflows];
  store.readinessByWorkflowId[value.id] = {
    stage: "TrialRequired",
    canCompile: true,
    canTrial: true,
    canValidate: true,
    canTrialRun: true,
    canPublish: false,
    hasDraft: true,
    compilationId: "comp-1",
    compilationCurrent: true,
    compilationValid: true,
    trialCurrent: false,
    trialSuccessful: false,
    published: false,
    blockers: [],
    updatedAt: "2026-07-02T01:00:00Z",
  };
  return store;
}

describe("workflow v1 store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
    useWorkspaceStore().items = [workspace()];
  });

  it("scopes workflow catalog to the active Workspace without agent ownership or query parameters", async () => {
    const workspaces = useWorkspaceStore();
    workspaces.items = [workspace("order"), workspace("support")];
    workspaces.activeWorkspaceId = "order";
    vi.mocked(apiClient.get).mockImplementation(async (url) => {
      if (url.endsWith("/workflows")) {
        const id = url.includes("/support/") ? "wf-support" : "wf-order";
        return { data: { items: [workflowDTO(id)] } };
      }
      return { data: readinessDTO() };
    });

    const store = useWorkflowStore();
    await store.loadWorkflowPage({ query: "订单", page: 1, pageSize: 10 });

    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/order/workflows");
    expect(apiClient.get).not.toHaveBeenCalledWith("/workspaces/support/workflows");
    expect(vi.mocked(apiClient.get).mock.calls.flat().join(" ")).not.toContain("agent");
    expect(store.workflows).toHaveLength(1);
    expect(store.pageItems).toHaveLength(1);
    expect(store.pageItems[0]?.id).toBe("wf-order");
  });

  it("creates a workflow with metadata and the single canonical graph", async () => {
    vi.mocked(apiClient.post).mockResolvedValue({ data: { workflow: workflowDTO(), draft: draftDTO() } });
    const store = useWorkflowStore();

    await store.createWorkflow({ ...domainWorkflow(), graph, schemaVersion: graph.schemaVersion });

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/order/workflows", {
      name: "订单取消编排",
      slug: "order-cancel",
      description: "查询订单状态并决定后续动作。",
      schemaVersion: "workflow.graph.v1",
      graph,
    });
    const body = vi.mocked(apiClient.post).mock.calls[0]?.[1] as Record<string, unknown>;
    expect(body).not.toHaveProperty("agentId");
    expect(body).not.toHaveProperty("dsl");
    expect(body).not.toHaveProperty("canvasGraph");
    expect(store.activeDraft?.workflowId).toBe("wf-order");
  });

  it("loads and saves the unique draft with ETag, draftVersion, and lockVersion", async () => {
    const store = seedWorkflow();
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: draftDTO(), headers: { etag: '"draft-4-7"' } })
      .mockResolvedValueOnce({ data: readinessDTO() })
      .mockResolvedValueOnce({
        data: readinessDTO({ stage: "COMPILE_REQUIRED", canTrial: false, compilationCurrent: false }),
      });
    vi.mocked(apiClient.put).mockResolvedValue({
      data: draftDTO({ draftVersion: 5, lockVersion: 8 }),
      headers: { etag: '"draft-5-8"' },
    });

    const loaded = await store.loadWorkflowDraft("wf-order");
    await store.saveWorkflowDraft("wf-order", { ...loaded.draft, graph: { ...graph, ui: { dirty: true } } });

    expect(apiClient.put).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order/draft",
      {
        schemaVersion: "workflow.graph.v1",
        graph: { ...graph, ui: { dirty: true } },
        draftVersion: 4,
        lockVersion: 7,
      },
      { headers: { "If-Match": '"draft-4-7"' } },
    );
    expect(store.activeDraft?.draftVersion).toBe(5);
    expect(store.activeDraft?.etag).toBe('"draft-5-8"');
  });

  it("reloads the latest draft and raises an explicit conflict", async () => {
    const store = seedWorkflow();
    const current = { ...draftDTO(), workflowId: "wf-order", etag: '"draft-4-7"' };
    vi.mocked(apiClient.put).mockRejectedValue({ message: "conflict", response: { status: 409 } });
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: draftDTO({ draftVersion: 6, lockVersion: 9 }), headers: { etag: '"draft-6-9"' } })
      .mockResolvedValueOnce({ data: readinessDTO({ stage: "COMPILE_REQUIRED" }) });

    await expect(store.saveWorkflowDraft("wf-order", current)).rejects.toBeInstanceOf(WorkflowDraftConflictError);
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order/draft");
    expect(store.activeDraft?.draftVersion).toBe(6);
  });

  it("compiles the current draft through the command endpoint", async () => {
    const store = seedWorkflow();
    vi.mocked(apiClient.post).mockResolvedValue({ data: compilationDTO() });
    vi.mocked(apiClient.get).mockResolvedValue({ data: readinessDTO() });

    const validation = await store.validateWorkflow("wf-order");

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order/draft:compile");
    expect(validation.valid).toBe(true);
    expect(store.activeCompilation?.id).toBe("comp-1");
  });

  it("trial-runs the exact compilation ID and records its execution ID", async () => {
    const store = seedWorkflow();
    vi.mocked(apiClient.post).mockResolvedValue({
      data: {
        id: "trial-9",
        compilationId: "comp-1",
        executionId: "exec-9",
        status: "SUCCEEDED",
        inputHash: "sha256:input",
        startedBy: "user-2",
        startedAt: "2026-07-02T04:00:00Z",
        finishedAt: "2026-07-02T04:00:01Z",
      },
    });
    vi.mocked(apiClient.get).mockResolvedValue({
      data: readinessDTO({ stage: "PUBLISH_READY", canPublish: true, trialCurrent: true, trialSuccessful: true }),
    });

    const execution = await store.trialRunWorkflow("wf-order", { orderId: "A-1" });

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order/compilations/comp-1:trial", {
      input: { orderId: "A-1" },
    });
    expect(execution.id).toBe("exec-9");
  });

  it("publishes a compilation and preserves the immutable revision ID", async () => {
    const store = seedWorkflow();
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { revision: revisionDTO(), releaseId: "release-7", releaseNo: 7, trialId: "trial-9" },
    });
    vi.mocked(apiClient.get).mockImplementation(async (url) =>
      url.endsWith("/readiness")
        ? { data: readinessDTO({ stage: "PUBLISHED", published: true, activeRevisionId: "rev-immutable-7" }) }
        : { data: { ...workflowDTO(), activeRevisionId: "rev-immutable-7" } },
    );

    const result = await store.publishWorkflow("wf-order");

    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order/compilations/comp-1:publish",
      expect.objectContaining({ callableName: "order_cancel", publishNote: "Publish 订单取消编排" }),
    );
    expect(result.revisionId).toBe("rev-immutable-7");
    expect(store.revisionsByWorkflowId["wf-order"]?.[0]?.revisionNo).toBe(7);
  });

  it("lists, diffs, and activates revisions by immutable IDs", async () => {
    const store = seedWorkflow();
    vi.mocked(apiClient.get).mockImplementation(async (url) => {
      if (url.endsWith("/revisions")) return { data: { items: [revisionDTO("rev-a", 1), revisionDTO("rev-b", 2)] } };
      if (url.includes("revisions:diff"))
        return {
          data: {
            from: revisionDTO("rev-a", 1),
            to: revisionDTO("rev-b", 2),
            changes: { draft: true, spec: false, plan: true, planHash: true },
          },
        };
      if (url.endsWith("/readiness"))
        return { data: readinessDTO({ stage: "PUBLISHED", published: true, activeRevisionId: "rev-a" }) };
      return { data: { ...workflowDTO(), activeRevisionId: "rev-a" } };
    });
    vi.mocked(apiClient.post).mockResolvedValue({
      data: {
        revision: revisionDTO("rev-a", 1),
        releaseId: "release-8",
        releaseNo: 8,
        eventType: "WORKFLOW_REVISION_ACTIVATED",
      },
    });

    await store.loadWorkflowRevisions("wf-order");
    const diff = await store.loadWorkflowRevisionDiff("wf-order", "rev-a", "rev-b");
    await store.activateWorkflowRevision("wf-order", "rev-a");

    expect(apiClient.get).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order/revisions:diff?from=rev-a&to=rev-b",
    );
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order/revisions/rev-a:activate");
    expect(diff.changes).toEqual({ draft: true, spec: false, plan: true, planHash: true });
  });

  it("updates metadata with lockVersion and deletes using optimistic locking", async () => {
    const store = seedWorkflow();
    vi.mocked(apiClient.patch).mockResolvedValue({ data: { ...workflowDTO(), name: "新名称", lockVersion: 4 } });
    vi.mocked(apiClient.delete).mockResolvedValue({ data: undefined });

    await store.updateWorkflow("wf-order", { ...domainWorkflow(), name: "新名称" });
    await store.deleteWorkflow("wf-order");

    expect(apiClient.patch).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order", {
      name: "新名称",
      slug: "order-cancel",
      description: "查询订单状态并决定后续动作。",
      status: "ACTIVE",
      lockVersion: 3,
    });
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order?lockVersion=4");
  });
});
