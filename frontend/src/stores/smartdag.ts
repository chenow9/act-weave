import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import type {
  FailureFeedback,
  SmartDAGGuardReport,
  SmartDAGMissingCapability,
  SmartDAGNodeExplanation,
  SmartDAGReasoningStep,
  SmartDAGTurnResponse,
  SmartGenerateSession,
  SmartGenerateTurn,
  Workflow,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
} from "../types/domain";
import { useWorkflowStore, type WorkflowCreateResponseDTO } from "./workflow";

// Re-export aligned domain types so existing store consumers keep working (P6.1).
export type {
  FailureFeedback,
  FailureFeedbackIssue,
  SmartDAGGuardReport,
  SmartDAGGuardViolation,
  SmartDAGMissingCapability,
  SmartDAGNodeExplanation,
  SmartDAGReasoningStep,
  SmartDAGTurnResponse,
  SmartGenerateSession,
  SmartGenerateTurn,
} from "../types/domain";

/** Deep-clone graph for canvas SoT refresh without structuredClone (fails on Vue proxies). */
function cloneGraph(graph: WorkflowGraphDraft): WorkflowGraphDraft {
  return JSON.parse(JSON.stringify(graph)) as WorkflowGraphDraft;
}

function cloneDraft(draft: WorkflowDraftRecord): WorkflowDraftRecord {
  return {
    ...draft,
    graph: cloneGraph(draft.graph),
  };
}

export interface SmartDAGGenerateRequest {
  workspaceId: string;
  goal: string;
}

export interface SmartDAGTurnRequest {
  workspaceId: string;
  agentId: string;
  message: string;
  /** Continue multi-turn on existing workflow draft. */
  workflowId?: string;
  /** Optional FailureFeedback for revise-from-failure (D5/D14; draft only, never auto-publish). */
  feedback?: FailureFeedback;
}

export interface SmartDAGTurnResult {
  sessionId: string;
  turnId: string;
  generationId: string;
  workflow: Workflow;
  draft: WorkflowDraftRecord;
  assistantMessage?: string;
  reasoningSteps: SmartDAGReasoningStep[];
  missingCapabilities: SmartDAGMissingCapability[];
  nodeExplanations: SmartDAGNodeExplanation[];
  availableToolIds: string[];
  selectedToolIds: string[];
  confidence: number;
  guardReport?: SmartDAGGuardReport;
  draftVersion: number;
  generatedBy?: string;
  promptId?: string;
  promptHash?: string;
  agentId?: string;
  modelConfigId?: string;
  traceId?: string;
}

interface SmartDAGGenerateResponse extends WorkflowCreateResponseDTO {
  reasoningSteps: SmartDAGReasoningStep[];
  missingCapabilities: SmartDAGMissingCapability[];
  nodeExplanations: SmartDAGNodeExplanation[];
  availableToolIds: string[];
  selectedToolIds: string[];
  reasoning: string;
  confidence: number;
}

/** Local turn response shape: WorkflowCreateResponseDTO fields + smart-dag audit fields. */
type SmartDAGTurnHTTPResponse = SmartDAGTurnResponse & WorkflowCreateResponseDTO;

type CreateSessionResponse = SmartGenerateSession;

interface SmartDagState {
  generating: boolean;
  goal: string;
  workspaceId: string;
  agentId: string;
  sessionId: string;
  sessionStatus: "OPEN" | "CLOSED" | "";
  modelConfigId: string;
  turns: SmartGenerateTurn[];
  lastGuardReport?: SmartDAGGuardReport;
  lastErrorCode: string;
  generatedWorkflow?: Workflow;
  generatedDraft?: WorkflowDraftRecord;
  /** Canvas SoT version token: increments when draft is replaced from a turn. */
  canvasEpoch: number;
  reasoningSteps: SmartDAGReasoningStep[];
  missingCapabilities: SmartDAGMissingCapability[];
  nodeExplanations: SmartDAGNodeExplanation[];
  availableToolIds: string[];
  selectedToolIds: string[];
  reasoning: string;
  confidence: number;
}

export const useSmartDagStore = defineStore("smartdag", {
  state: (): SmartDagState => ({
    generating: false,
    goal: "",
    workspaceId: "",
    agentId: "",
    sessionId: "",
    sessionStatus: "",
    modelConfigId: "",
    turns: [],
    lastGuardReport: undefined,
    lastErrorCode: "",
    generatedWorkflow: undefined,
    generatedDraft: undefined,
    canvasEpoch: 0,
    reasoningSteps: [],
    missingCapabilities: [],
    nodeExplanations: [],
    availableToolIds: [],
    selectedToolIds: [],
    reasoning: "",
    confidence: 0,
  }),
  getters: {
    hasOpenSession: (state) => state.sessionId !== "" && state.sessionStatus === "OPEN",
    canSendTurn: (state) =>
      Boolean(state.workspaceId && state.agentId && state.modelConfigId && state.sessionStatus !== "CLOSED"),
  },
  actions: {
    setContext(workspaceId: string, agentId: string, modelConfigId = "") {
      const nextWorkspace = workspaceId.trim();
      const nextAgent = agentId.trim();
      // Changing workspace/agent invalidates the generate session (not ChatSession).
      if (nextWorkspace !== this.workspaceId || nextAgent !== this.agentId) {
        this.sessionId = "";
        this.sessionStatus = "";
        this.turns = [];
        this.lastGuardReport = undefined;
        this.lastErrorCode = "";
      }
      this.workspaceId = nextWorkspace;
      this.agentId = nextAgent;
      this.modelConfigId = modelConfigId.trim();
    },

    /**
     * Ensure an OPEN generate session exists for the current workspace+agent.
     * Creates via POST .../workflow-generate-sessions when needed.
     */
    async ensureSession(options: { workflowId?: string } = {}) {
      if (!this.workspaceId || !this.agentId) {
        throw new Error("请先选择业务空间和 Agent。");
      }
      if (!this.modelConfigId) {
        const error = new Error("当前 Agent 未配置可用模型，请先在 Agent 中绑定 Model Config。");
        (error as Error & { code?: string }).code = "AGENT_MODEL_REQUIRED";
        throw error;
      }
      if (this.sessionId && this.sessionStatus === "OPEN") {
        return {
          sessionId: this.sessionId,
          agentId: this.agentId,
          modelConfigId: this.modelConfigId,
          status: "OPEN" as const,
        };
      }
      const body: { agentId: string; workflowId?: string } = { agentId: this.agentId };
      if (options.workflowId) body.workflowId = options.workflowId;
      else if (this.generatedWorkflow?.id) body.workflowId = this.generatedWorkflow.id;

      const response = await apiClient.post<CreateSessionResponse>(
        `/workspaces/${this.workspaceId}/workflow-generate-sessions`,
        body,
      );
      this.sessionId = response.data.sessionId;
      this.sessionStatus = response.data.status || "OPEN";
      this.modelConfigId = response.data.modelConfigId || this.modelConfigId;
      this.agentId = response.data.agentId || this.agentId;
      this.turns = [];
      this.lastGuardReport = undefined;
      this.lastErrorCode = "";
      return response.data;
    },

    /**
     * Multi-turn product path: ensure session → POST turn → refresh canvas SoT from returned draft.
     */
    async sendTurn(request: SmartDAGTurnRequest): Promise<SmartDAGTurnResult> {
      this.generating = true;
      this.lastErrorCode = "";
      this.lastGuardReport = undefined;
      this.workspaceId = request.workspaceId.trim();
      this.agentId = request.agentId.trim();
      this.goal = request.message.trim();
      try {
        await this.ensureSession({ workflowId: request.workflowId });
        const body: { message: string; feedback?: FailureFeedback } = { message: this.goal };
        if (request.feedback) {
          body.feedback = request.feedback;
        }
        // LLM graph generation routinely exceeds the default 12s API timeout.
        const response = await apiClient.post<SmartDAGTurnHTTPResponse>(
          `/workspaces/${this.workspaceId}/workflow-generate-sessions/${this.sessionId}/turns`,
          body,
          { timeout: 210_000 },
        );
        return this.applyTurnResponse(response.data, response.headers?.etag);
      } catch (error) {
        this.captureTurnError(error);
        throw error;
      } finally {
        this.generating = false;
      }
    },

    applyTurnResponse(data: SmartDAGTurnHTTPResponse, etag?: string): SmartDAGTurnResult {
      const workflows = useWorkflowStore();
      const created = workflows.adoptCreatedWorkflowResponse(this.workspaceId, data, etag);
      // Force canvas re-render: SoT is the turn-returned draft (P1.5.3).
      this.generatedWorkflow = created;
      this.generatedDraft = workflows.activeDraft ? cloneDraft(workflows.activeDraft) : undefined;
      this.canvasEpoch += 1;

      this.reasoningSteps = data.reasoningSteps || [];
      this.missingCapabilities = data.missingCapabilities || [];
      this.nodeExplanations = data.nodeExplanations || [];
      this.availableToolIds = data.availableToolIds || [];
      this.selectedToolIds = data.selectedToolIds || [];
      this.reasoning = data.assistantMessage || "";
      this.confidence = data.confidence || 0;
      this.lastGuardReport = data.guardReport;
      if (data.agentId) this.agentId = data.agentId;
      if (data.modelConfigId) this.modelConfigId = data.modelConfigId;

      const turn: SmartGenerateTurn = {
        turnId: data.turnId,
        turnIndex: this.turns.length + 1,
        userMessage: this.goal,
        assistantMessage: data.assistantMessage,
        generationId: data.generationId,
        guardOk: data.guardReport?.ok !== false,
        guardReport: data.guardReport,
        draftVersion: data.draftVersion ?? this.generatedDraft?.draftVersion,
        status: "SUCCEEDED",
        promptId: data.promptId,
        promptHash: data.promptHash,
      };
      this.turns = [...this.turns, turn];

      return {
        sessionId: data.sessionId || this.sessionId,
        turnId: data.turnId,
        generationId: data.generationId,
        workflow: created,
        draft: this.generatedDraft!,
        assistantMessage: data.assistantMessage,
        reasoningSteps: this.reasoningSteps,
        missingCapabilities: this.missingCapabilities,
        nodeExplanations: this.nodeExplanations,
        availableToolIds: this.availableToolIds,
        selectedToolIds: this.selectedToolIds,
        confidence: this.confidence,
        guardReport: data.guardReport,
        draftVersion: data.draftVersion ?? this.generatedDraft?.draftVersion ?? 0,
        generatedBy: data.generatedBy,
        promptId: data.promptId,
        promptHash: data.promptHash,
        agentId: data.agentId || this.agentId,
        modelConfigId: data.modelConfigId || this.modelConfigId,
        traceId: data.traceId,
      };
    },

    captureTurnError(error: unknown) {
      const anyErr = error as {
        code?: string;
        response?: {
          data?: {
            error?: { code?: string };
            guardReport?: SmartDAGGuardReport;
            sessionId?: string;
            turnId?: string;
            generationId?: string;
            agentId?: string;
            promptHash?: string;
            traceId?: string;
          };
        };
      };
      const code = anyErr?.response?.data?.error?.code || anyErr?.code || "";
      this.lastErrorCode = code;
      const body = anyErr?.response?.data;
      const report = body?.guardReport;
      if (report) {
        this.lastGuardReport = report;
        this.turns = [
          ...this.turns,
          {
            turnId: body?.turnId || `local-failed-${this.turns.length + 1}`,
            turnIndex: this.turns.length + 1,
            userMessage: this.goal,
            assistantMessage: "本轮未通过校验，已保留上一轮合法草稿。",
            generationId: body?.generationId || "",
            guardOk: false,
            guardReport: report,
            status: code === "GUARD_REJECTED" ? "GUARD_REJECTED" : "FAILED",
            errorCode: code,
            promptHash: body?.promptHash,
          },
        ];
      }
    },

    async closeSession() {
      if (!this.workspaceId || !this.sessionId) {
        this.sessionStatus = "CLOSED";
        return { sessionId: this.sessionId, status: "CLOSED" as const };
      }
      const response = await apiClient.post<{ sessionId: string; status: "CLOSED"; closedAt?: string }>(
        `/workspaces/${this.workspaceId}/workflow-generate-sessions/${this.sessionId}:close`,
        {},
      );
      this.sessionStatus = "CLOSED";
      return response.data;
    },

    async loadSession(workspaceId: string, sessionId: string) {
      const response = await apiClient.get<{
        session: CreateSessionResponse;
        turns: SmartGenerateTurn[];
        draftVersion?: number;
        workflow?: WorkflowCreateResponseDTO["workflow"];
        draft?: WorkflowCreateResponseDTO["draft"];
      }>(`/workspaces/${workspaceId}/workflow-generate-sessions/${sessionId}`);
      this.workspaceId = workspaceId;
      this.sessionId = response.data.session.sessionId;
      this.sessionStatus = response.data.session.status;
      this.agentId = response.data.session.agentId;
      this.modelConfigId = response.data.session.modelConfigId;
      this.turns = (response.data.turns || []).map((turn, index) => ({
        ...turn,
        turnId: turn.turnId || (turn as { turnId?: string }).turnId || `turn-${index + 1}`,
        turnIndex: turn.turnIndex || index + 1,
      }));
      if (response.data.workflow && response.data.draft) {
        const workflows = useWorkflowStore();
        const created = workflows.adoptCreatedWorkflowResponse(
          workspaceId,
          { workflow: response.data.workflow, draft: response.data.draft },
          undefined,
        );
        this.generatedWorkflow = created;
        this.generatedDraft = workflows.activeDraft
          ? cloneDraft(workflows.activeDraft)
          : undefined;
        this.canvasEpoch += 1;
      }
      return response.data;
    },

    /** Legacy single-shot path kept for compatibility tests; FE main path uses sendTurn. */
    async generateDraft(request: SmartDAGGenerateRequest) {
      this.generating = true;
      this.workspaceId = request.workspaceId;
      this.goal = request.goal.trim();
      try {
        const response = await apiClient.post<SmartDAGGenerateResponse>(
          `/workspaces/${request.workspaceId}/workflows:generate`,
          { goal: this.goal },
        );
        const workflows = useWorkflowStore();
        const created = workflows.adoptCreatedWorkflowResponse(request.workspaceId, response.data, response.headers?.etag);
        this.generatedWorkflow = created;
        this.generatedDraft = workflows.activeDraft ? cloneDraft(workflows.activeDraft) : undefined;
        this.canvasEpoch += 1;
        this.reasoningSteps = response.data.reasoningSteps || [];
        this.missingCapabilities = response.data.missingCapabilities || [];
        this.nodeExplanations = response.data.nodeExplanations || [];
        this.availableToolIds = response.data.availableToolIds || [];
        this.selectedToolIds = response.data.selectedToolIds || [];
        this.reasoning = response.data.reasoning || "";
        this.confidence = response.data.confidence || 0;
        return {
          workflow: created,
          draft: this.generatedDraft,
          reasoningSteps: this.reasoningSteps,
          missingCapabilities: this.missingCapabilities,
          nodeExplanations: this.nodeExplanations,
          availableToolIds: this.availableToolIds,
          selectedToolIds: this.selectedToolIds,
          reasoning: this.reasoning,
          confidence: this.confidence,
        };
      } finally {
        this.generating = false;
      }
    },

    adoptDraft(workflow: Workflow, draft: WorkflowDraftRecord) {
      this.workspaceId = workflow.workspaceId;
      this.generatedWorkflow = workflow;
      this.generatedDraft = cloneDraft(draft);
      this.canvasEpoch += 1;
      this.goal = typeof draft.graph.ui.businessGoal === "string" ? draft.graph.ui.businessGoal : "";
      this.confidence = typeof draft.graph.ui.confidence === "number" ? draft.graph.ui.confidence : 0;
    },

    resetDraft() {
      this.generatedWorkflow = undefined;
      this.generatedDraft = undefined;
      this.sessionId = "";
      this.sessionStatus = "";
      this.turns = [];
      this.lastGuardReport = undefined;
      this.lastErrorCode = "";
      this.canvasEpoch += 1;
      this.reasoningSteps = [];
      this.missingCapabilities = [];
      this.nodeExplanations = [];
      this.availableToolIds = [];
      this.selectedToolIds = [];
      this.reasoning = "";
      this.confidence = 0;
    },
  },
});
