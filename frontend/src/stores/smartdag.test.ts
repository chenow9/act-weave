import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { WorkflowGraphDraft } from "../types/domain";
import { normalizeWorkflowGraphDraft } from "../utils/workflow-graph";
import { useSmartDagStore } from "./smartdag";
import { useWorkflowStore } from "./workflow";

vi.mock("../services/api", () => ({
  apiClient: { post: vi.fn(), get: vi.fn() },
  toAPIError: (error: unknown) => {
    if (error && typeof error === "object" && "code" in error) return error;
    const message = error instanceof Error ? error.message : "request failed";
    return { message, code: "ERROR", status: 0 };
  },
}));

// sendTurn uses postLlmJobSse; bridge to apiClient.post mock for legacy tests.
vi.mock("../services/llm-job-sse", () => ({
  postLlmJobSse: vi.fn(async (options: { path: string; body: unknown }) => {
    const { apiClient } = await import("../services/api");
    const res = await apiClient.post(options.path, options.body);
    return (res as { data: unknown }).data;
  }),
}));

const graphV1: WorkflowGraphDraft = {
  schemaVersion: "workflow.graph.v1",
  nodes: [
    {
      id: "start",
      type: "Start",
      label: "接收请求",
      position: { x: 0, y: 0 },
      ports: [],
      data: {},
      ui: { generated: true },
    },
    {
      id: "end",
      type: "End",
      label: "返回结果",
      position: { x: 300, y: 0 },
      ports: [],
      data: {},
      ui: { generated: true },
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
  ui: { generatedBy: "smart-dag.v1", businessGoal: "处理供应商准入", confidence: 82 },
};

function legacyResponse() {
  return {
    data: {
      workflow: {
        id: "workflow-ai-1",
        currentDraftId: "draft-ai-1",
        name: "AI · 供应商准入",
        slug: "ai-supplier",
        description: "智能生成",
        status: "ACTIVE",
        createdBy: "user-1",
        updatedBy: "user-1",
        createdAt: "2026-07-15T00:00:00Z",
        updatedAt: "2026-07-15T00:00:00Z",
        lockVersion: 1,
        nodeCount: 2,
        edgeCount: 1,
      },
      draft: {
        id: "draft-ai-1",
        draftVersion: 1,
        schemaVersion: "workflow.graph.v1",
        graph: graphV1,
        graphHash: "hash",
        updatedBy: "user-1",
        updatedAt: "2026-07-15T00:00:00Z",
        lockVersion: 1,
      },
      reasoningSteps: [{ id: "goal", label: "解析目标", status: "COMPLETED", detail: "供应商准入" }],
      missingCapabilities: [
        { id: "gap", name: "供应商能力", reason: "未匹配 Tool", suggestedProtocol: "HTTP_OPENAPI" },
      ],
      nodeExplanations: [{ nodeId: "start", title: "接收请求", reason: "统一入口" }],
      availableToolIds: ["tool-1"],
      selectedToolIds: [],
      reasoning: "不虚构 Tool",
      confidence: 82,
    },
    headers: { etag: '"draft-1-1"' },
  };
}

function turnGraph(version: number, extraNode = false): WorkflowGraphDraft {
  const nodes = [
    {
      id: "start",
      type: "Start" as const,
      label: "Start",
      position: { x: 0, y: 0 },
      ports: [],
      data: {},
      ui: { generated: true },
    },
    {
      id: "tool-1",
      type: "Tool" as const,
      label: "查询支付",
      position: { x: 200, y: 0 },
      ports: [],
      data: { toolId: "tool-1" },
      ui: { generated: true },
    },
    {
      id: "end",
      type: "End" as const,
      label: "End",
      position: { x: 400, y: 0 },
      ports: [],
      data: {},
      ui: { generated: true },
    },
  ];
  if (extraNode) {
    nodes.splice(2, 0, {
      id: "approval-1",
      type: "Approval" as const,
      label: "审批",
      position: { x: 300, y: 0 },
      ports: [],
      data: {},
      ui: { generated: true },
    });
  }
  return {
    schemaVersion: "workflow.graph.v1",
    nodes,
    edges: [],
    viewport: { x: 0, y: 0, zoom: 1 },
    ui: { generatedBy: "smart-dag.v2", businessGoal: "query", confidence: 90, sessionId: "session-1" },
  };
}

function turnResponse(version: number, extraNode = false) {
  const graph = turnGraph(version, extraNode);
  return {
    data: {
      sessionId: "session-1",
      turnId: `turn-${version}`,
      generationId: `gen-${version}`,
      workflow: {
        id: "workflow-ai-2",
        currentDraftId: "draft-ai-2",
        name: "AI · 支付",
        slug: "ai-pay",
        description: "v2",
        status: "ACTIVE",
        createdBy: "user-1",
        updatedBy: "user-1",
        createdAt: "2026-07-15T00:00:00Z",
        updatedAt: "2026-07-15T00:00:00Z",
        lockVersion: version,
        nodeCount: graph.nodes.length,
        edgeCount: 0,
      },
      draft: {
        id: "draft-ai-2",
        draftVersion: version,
        schemaVersion: "workflow.graph.v1",
        graph,
        graphHash: `hash-${version}`,
        updatedBy: "user-1",
        updatedAt: "2026-07-15T00:00:00Z",
        lockVersion: version,
      },
      assistantMessage: `draftVersion=${version}`,
      reasoningSteps: [],
      missingCapabilities: [],
      nodeExplanations: [],
      availableToolIds: ["tool-1"],
      selectedToolIds: ["tool-1"],
      confidence: 90,
      guardReport: { ok: true, violations: [] },
      draftVersion: version,
      generatedBy: "smart-dag.v2",
    },
    headers: { etag: `"draft-${version}-${version}"` },
  };
}

describe("smart dag v1 store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
  });

  it("generates through the workspace v1 command and adopts the formal draft", async () => {
    vi.mocked(apiClient.post).mockResolvedValue(legacyResponse());
    const smart = useSmartDagStore();
    const result = await smart.generateDraft({ workspaceId: "workspace-1", goal: " 处理供应商准入 " });

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/workflows:generate", {
      goal: "处理供应商准入",
    });
    // adoptCreatedWorkflowResponse normalizes missing/empty ports for editor safety
    expect(result.draft?.graph).toEqual(normalizeWorkflowGraphDraft(graphV1));
    expect(smart.generatedWorkflow?.id).toBe("workflow-ai-1");
    expect(smart.generatedDraft?.etag).toBe('"draft-1-1"');
    expect(smart.missingCapabilities[0].id).toBe("gap");
    expect(smart.confidence).toBe(82);

    const workflows = useWorkflowStore();
    expect(workflows.selectedWorkflowId).toBe("workflow-ai-1");
    expect(workflows.activeDraft?.graph.ui.generatedBy).toBe("smart-dag.v1");
    expect(workflows.workflowDetails["workflow-ai-1"]?.workspaceId).toBe("workspace-1");

    smart.adoptDraft(smart.generatedWorkflow!, smart.generatedDraft!);
    expect(smart.confidence).toBe(82);
  });

  it("does not create a local fallback when generation fails", async () => {
    vi.mocked(apiClient.post).mockRejectedValue(new Error("offline"));
    const smart = useSmartDagStore();

    await expect(smart.generateDraft({ workspaceId: "workspace-1", goal: "生成流程" })).rejects.toThrow("offline");
    expect(smart.generatedWorkflow).toBeUndefined();
    expect(smart.generatedDraft).toBeUndefined();
    expect(useWorkflowStore().workflows).toEqual([]);
  });
});

describe("smart dag multi-turn session store (P1.5)", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
  });

  it("creates session then sends turns; canvas SoT uses latest draft each turn", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "model-1");

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
      if (url.includes("/turns")) {
        const call = vi.mocked(apiClient.post).mock.calls.filter((c) => String(c[0]).includes("/turns")).length;
        return turnResponse(call, call === 2);
      }
      if (url.endsWith(":close")) {
        return { data: { sessionId: "session-1", status: "CLOSED" } };
      }
      throw new Error("unexpected " + url);
    });

    const epoch0 = smart.canvasEpoch;
    const t1 = await smart.sendTurn({
      workspaceId: "workspace-1",
      agentId: "agent-1",
      message: "查询支付状态",
    });
    expect(t1.draftVersion).toBe(1);
    expect(smart.canvasEpoch).toBe(epoch0 + 1);
    expect(smart.generatedDraft?.draftVersion).toBe(1);
    expect(smart.generatedDraft?.graph.nodes.some((n) => n.id === "approval-1")).toBe(false);
    expect(smart.turns).toHaveLength(1);
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/workflow-generate-sessions", {
      agentId: "agent-1",
    });

    const graphRefAfterTurn1 = smart.generatedDraft?.graph;
    const t2 = await smart.sendTurn({
      workspaceId: "workspace-1",
      agentId: "agent-1",
      message: "加审批",
      workflowId: smart.generatedWorkflow?.id,
    });
    expect(t2.draftVersion).toBe(2);
    expect(smart.canvasEpoch).toBe(epoch0 + 2);
    expect(smart.generatedDraft?.draftVersion).toBe(2);
    expect(smart.generatedDraft?.graph.nodes.some((n) => n.id === "approval-1")).toBe(true);
    // SoT draft object is replaced (force re-render), not mutated in place.
    expect(smart.generatedDraft?.graph).not.toBe(graphRefAfterTurn1);
    expect(smart.turns).toHaveLength(2);
    expect(useWorkflowStore().activeDraft?.graph.ui.generatedBy).toBe("smart-dag.v2");

    await smart.closeSession();
    expect(smart.sessionStatus).toBe("CLOSED");
    expect(apiClient.post).toHaveBeenCalledWith(
      "/workspaces/workspace-1/workflow-generate-sessions/session-1:close",
      {},
    );
  });

  it("posts failure feedback on turn body for draft-only revise (P4.2/D14)", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "model-1");
    vi.mocked(apiClient.post).mockImplementation(async (url: string) => {
      if (url.endsWith("/workflow-generate-sessions")) {
        return {
          data: {
            sessionId: "session-1",
            agentId: "agent-1",
            modelConfigId: "model-1",
            status: "OPEN",
            workflowId: "wf-1",
          },
        };
      }
      if (url.includes("/turns")) {
        return turnResponse(2, true);
      }
      throw new Error("unexpected " + url);
    });

    await smart.sendTurn({
      workspaceId: "workspace-1",
      agentId: "agent-1",
      message: "请按编译问题修订",
      workflowId: "wf-1",
      feedback: {
        source: "compile",
        workflowId: "wf-1",
        compilationId: "comp-1",
        issues: [{ code: "MAPPING_INVALID", message: "bad mapping", nodeId: "tool-1" }],
        rawSummary: "compile failed",
      },
    });

    const turnCall = vi.mocked(apiClient.post).mock.calls.find((c) => String(c[0]).includes("/turns"));
    expect(turnCall?.[1]).toEqual({
      message: "请按编译问题修订",
      feedback: {
        source: "compile",
        workflowId: "wf-1",
        compilationId: "comp-1",
        issues: [{ code: "MAPPING_INVALID", message: "bad mapping", nodeId: "tool-1" }],
        rawSummary: "compile failed",
      },
    });
    expect(turnCall?.[1]).not.toHaveProperty("expectedSessionLockVersion");
  });

  it("does not send expectedSessionLockVersion on the turn body", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "model-1");
    vi.mocked(apiClient.post).mockImplementation(async (url: string) => {
      if (url.endsWith("/workflow-generate-sessions")) {
        return {
          data: { sessionId: "session-1", agentId: "agent-1", modelConfigId: "model-1", status: "OPEN" },
        };
      }
      if (url.includes("/turns")) return turnResponse(1);
      throw new Error(url);
    });

    await smart.sendTurn({ workspaceId: "workspace-1", agentId: "agent-1", message: "查询支付状态" });
    const turnCall = vi.mocked(apiClient.post).mock.calls.find((c) => String(c[0]).includes("/turns"));
    expect(Object.keys((turnCall?.[1] as object) || {})).toEqual(["message"]);
  });

  it("does not create session when agent has no model config", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "");
    await expect(smart.sendTurn({ workspaceId: "workspace-1", agentId: "agent-1", message: "hi" })).rejects.toThrow(
      /未配置可用模型/,
    );
    expect(apiClient.post).not.toHaveBeenCalled();
    expect(smart.generatedDraft).toBeUndefined();
  });

  it("records guard failures without replacing prior draft SoT", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "model-1");
    let turnCount = 0;
    vi.mocked(apiClient.post).mockImplementation(async (url: string) => {
      if (url.endsWith("/workflow-generate-sessions")) {
        return {
          data: { sessionId: "session-1", agentId: "agent-1", modelConfigId: "model-1", status: "OPEN" },
        };
      }
      if (url.includes("/turns")) {
        turnCount += 1;
        if (turnCount === 1) return turnResponse(1);
        const err = new Error("guard") as Error & {
          response?: {
            data?: {
              error?: { code?: string };
              guardReport?: { ok: boolean; violations: { code: string; message: string }[] };
            };
          };
        };
        err.response = {
          data: {
            error: { code: "GUARD_REJECTED" },
            guardReport: { ok: false, violations: [{ code: "HALLUCINATED_TOOL_ID", message: "bad" }] },
          },
        };
        throw err;
      }
      throw new Error(url);
    });

    await smart.sendTurn({ workspaceId: "workspace-1", agentId: "agent-1", message: "ok" });
    const priorVersion = smart.generatedDraft?.draftVersion;
    const priorEpoch = smart.canvasEpoch;

    await expect(
      smart.sendTurn({
        workspaceId: "workspace-1",
        agentId: "agent-1",
        message: "bad",
        workflowId: smart.generatedWorkflow?.id,
      }),
    ).rejects.toThrow();
    expect(smart.generatedDraft?.draftVersion).toBe(priorVersion);
    expect(smart.canvasEpoch).toBe(priorEpoch);
    expect(smart.lastErrorCode).toBe("GUARD_REJECTED");
    expect(smart.lastGuardReport?.ok).toBe(false);
    expect(smart.turns.some((t) => t.status === "GUARD_REJECTED")).toBe(true);
  });

  it("resetSessionOnly clears session fields without bumping canvasEpoch", () => {
    const smart = useSmartDagStore();
    smart.sessionId = "session-e1";
    smart.sessionStatus = "OPEN";
    smart.sessionWorkflowId = "wf-a";
    smart.sessionLockVersion = 3;
    smart.goal = "旧意图";
    smart.reasoning = "旧推理";
    smart.confidence = 81;
    smart.reasoningSteps = [{ id: "goal", label: "解析", status: "COMPLETED", detail: "旧" }];
    smart.nodeExplanations = [{ nodeId: "start", title: "入口", reason: "旧" }];
    smart.missingCapabilities = [{ id: "gap", name: "缺能力", reason: "未匹配", suggestedProtocol: "HTTP_OPENAPI" }];
    smart.turns = [
      {
        turnId: "turn-1",
        turnIndex: 1,
        userMessage: "旧意图",
        generationId: "gen-1",
        guardOk: true,
        status: "SUCCEEDED",
      },
    ];
    smart.lastFailure = {
      stage: "MODEL_CALL",
      code: "SMART_DAG_MODEL_TIMEOUT",
      retryable: true,
      message: "timeout",
      requestId: "req-1",
      traceId: "tr-1",
    };
    smart.generatedDraft = {
      id: "draft-a",
      workflowId: "wf-a",
      draftVersion: 2,
      schemaVersion: "workflow.graph.v1",
      graph: graphV1,
      graphHash: "hash",
      updatedBy: "user-1",
      updatedAt: "2026-07-15T00:00:00Z",
      lockVersion: 2,
    };
    const epoch = smart.canvasEpoch;

    smart.resetSessionOnly();

    expect(smart.canvasEpoch).toBe(epoch);
    expect(smart.sessionId).toBe("");
    expect(smart.sessionStatus).toBe("");
    expect(smart.sessionWorkflowId).toBe("");
    expect(smart.turns).toEqual([]);
    expect(smart.lastFailure).toBeUndefined();
    expect(smart.generatedDraft).toBeUndefined();
    expect(smart.reasoning).toBe("");
    expect(smart.reasoningSteps).toEqual([]);
    expect(smart.goal).toBe("");
    expect(smart.confidence).toBe(0);
  });

  it("creates a new session when an unbound OPEN leftover is asked to bind to another workflow", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "model-1");
    smart.sessionId = "session-e1";
    smart.sessionStatus = "OPEN";
    smart.sessionWorkflowId = "";
    smart.generatedWorkflow = { id: "leftover-wf" } as never;
    smart.turns = [
      {
        turnId: "failed-1",
        turnIndex: 1,
        userMessage: "一句话",
        generationId: "",
        guardOk: false,
        status: "FAILED",
      },
    ];
    vi.mocked(apiClient.post).mockResolvedValue({
      data: {
        sessionId: "session-b",
        agentId: "agent-1",
        modelConfigId: "model-1",
        status: "OPEN",
        workflowId: "workflow-b",
      },
    });

    await smart.ensureSession({ workflowId: "workflow-b" });

    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/workflow-generate-sessions", {
      agentId: "agent-1",
      workflowId: "workflow-b",
    });
    expect(smart.sessionId).toBe("session-b");
    expect(smart.sessionWorkflowId).toBe("workflow-b");
    expect(smart.turns).toEqual([]);
  });

  it("loads session history without adopting graph when adoptGraph is false", async () => {
    const smart = useSmartDagStore();
    const epoch = smart.canvasEpoch;
    vi.mocked(apiClient.get).mockResolvedValue({
      data: {
        session: {
          sessionId: "session-1",
          agentId: "agent-1",
          modelConfigId: "model-1",
          status: "OPEN",
          workflowId: "wf-bound",
        },
        turns: [
          {
            turnId: "turn-1",
            turnIndex: 1,
            userMessage: "生成",
            generationId: "gen-1",
            guardOk: true,
            status: "SUCCEEDED",
          },
        ],
        workflow: turnResponse(1).data.workflow,
        draft: turnResponse(1).data.draft,
      },
    });

    await smart.loadSession("workspace-1", "session-1", { adoptGraph: false });

    expect(smart.sessionId).toBe("session-1");
    expect(smart.sessionStatus).toBe("OPEN");
    expect(smart.agentId).toBe("agent-1");
    expect(smart.sessionWorkflowId).toBe("wf-bound");
    expect(smart.turns).toHaveLength(1);
    expect(smart.generatedDraft).toBeUndefined();
    expect(smart.canvasEpoch).toBe(epoch);
    expect(useWorkflowStore().selectedWorkflowId).toBe("");
  });

  it("does not apply a loaded session when shouldApply returns false", async () => {
    const smart = useSmartDagStore();
    vi.mocked(apiClient.get).mockResolvedValue({
      data: {
        session: {
          sessionId: "session-stale",
          agentId: "agent-1",
          modelConfigId: "model-1",
          status: "OPEN",
          workflowId: "wf-bound",
        },
        turns: [
          {
            turnId: "turn-1",
            turnIndex: 1,
            userMessage: "生成",
            generationId: "gen-1",
            guardOk: true,
            status: "SUCCEEDED",
          },
        ],
      },
    });

    await smart.loadSession("workspace-1", "session-stale", { adoptGraph: false, shouldApply: () => false });

    expect(apiClient.get).toHaveBeenCalled();
    expect(smart.sessionId).toBe("");
    expect(smart.turns).toEqual([]);
  });

  it("binds sessionWorkflowId from payload workflow when the session row has none", async () => {
    const smart = useSmartDagStore();
    vi.mocked(apiClient.get).mockResolvedValue({
      data: {
        session: {
          sessionId: "session-1",
          agentId: "agent-1",
          modelConfigId: "model-1",
          status: "OPEN",
        },
        turns: [],
        workflow: turnResponse(1).data.workflow,
        draft: turnResponse(1).data.draft,
      },
    });

    await smart.loadSession("workspace-1", "session-1", { adoptGraph: false });

    expect(smart.sessionWorkflowId).toBe("workflow-ai-2");
    expect(smart.generatedDraft).toBeUndefined();
  });

  it("reuses the first-round session when the next turn requests the adopted workflow", async () => {
    const smart = useSmartDagStore();
    smart.setContext("workspace-1", "agent-1", "model-1");
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
      if (url.includes("/turns")) {
        const call = vi.mocked(apiClient.post).mock.calls.filter((c) => String(c[0]).includes("/turns")).length;
        return turnResponse(call);
      }
      throw new Error("unexpected " + url);
    });

    await smart.sendTurn({
      workspaceId: "workspace-1",
      agentId: "agent-1",
      message: "查询支付状态",
    });
    expect(smart.sessionId).toBe("session-1");
    expect(smart.sessionWorkflowId).toBe("workflow-ai-2");
    expect(smart.generatedWorkflow?.id).toBe("workflow-ai-2");

    await smart.sendTurn({
      workspaceId: "workspace-1",
      agentId: "agent-1",
      message: "加审批",
      workflowId: smart.generatedWorkflow?.id,
    });

    expect(smart.sessionId).toBe("session-1");
    expect(smart.sessionWorkflowId).toBe("workflow-ai-2");
    expect(
      vi.mocked(apiClient.post).mock.calls.filter((call) => String(call[0]).endsWith("/workflow-generate-sessions")),
    ).toHaveLength(1);
  });
});
