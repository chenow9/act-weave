import { onBeforeUnmount, onMounted, ref, type Ref } from "vue";

import type { SmartDagFailureState } from "../stores/smartdag";
import type {
  Agent,
  FailureFeedback,
  ModelApiConfig,
  SmartDAGNodeExplanation,
  SmartGenerateTurn,
  WorkflowGraphDraft,
  WorkflowGraphNode,
} from "../types/domain";
import { displayNodeTitle } from "../components/workflow/workflow-node-visual";

export type WorkflowGenerateLeftTab = "generate" | "nodes";
export type WorkflowGeneratePresence = "open" | "sheet";
export type WorkflowGenerateAgentsLoadState = "idle" | "loading" | "loaded" | "failed";
export type WorkflowGenerateAutoOpenedReason = "list-intent" | "editor-toggle" | "revise-failure" | "deeplink" | null;
export type WorkflowGenerateFailureCtaKey =
  | "bind-model"
  | "switch-agent"
  | "new-attempt"
  | "retry-rewrite"
  | "end-session";

export const WORKFLOW_GENERATE_PROMPT_MAX = 2000;
export const WORKFLOW_GENERATE_NARROW_MEDIA = "(max-width: 1180px)";
export const WORKFLOW_GENERATE_HIGHLIGHT_MS = 180;

export type TranscriptRow =
  | { kind: "user"; id: string; text: string }
  | { kind: "assistant"; id: string; text: string; draftVersion?: number }
  | { kind: "pending"; id: "inflight" }
  | { kind: "failure"; id: string; code: string; message: string };

export type GenerateFailureCta = {
  key: WorkflowGenerateFailureCtaKey;
  labelKey: string;
  kind: "primary" | "secondary";
};

export type TranscriptSmartSnapshot = {
  turns: SmartGenerateTurn[];
  generating: boolean;
  goal: string;
  lastFailure?: Pick<SmartDagFailureState, "turnId" | "code" | "message">;
};

export function createEmptyWorkflowGraphDraft(): WorkflowGraphDraft {
  return {
    schemaVersion: "workflow.graph.v1",
    nodes: [],
    edges: [],
    viewport: { x: 0, y: 0, zoom: 1 },
    ui: {},
  };
}

export function agentHasUsableModel(agent?: Pick<Agent, "modelConfigId">, configs: ModelApiConfig[] = []): boolean {
  const modelConfigId = (agent?.modelConfigId || "").trim();
  if (!modelConfigId) return false;
  const config = configs.find((item) => item.id === modelConfigId);
  if (!config) {
    // Catalog may not be loaded; a non-empty binding is usable until proven disabled.
    return true;
  }
  return config.status !== "DISABLED" && Boolean(config.apiBase || config.modelName);
}

export function pickPreferredGenerateAgent(options: {
  agents: Agent[];
  modelConfigs?: ModelApiConfig[];
  draftAgentId?: string;
  sessionAgentId?: string;
  selectedAgentId?: string;
}): Agent | undefined {
  const configs = options.modelConfigs || [];
  const usable = (id?: string) => {
    const trimmed = (id || "").trim();
    if (!trimmed) return undefined;
    const agent = options.agents.find((item) => item.id === trimmed);
    return agent && agentHasUsableModel(agent, configs) ? agent : undefined;
  };
  return (
    usable(options.draftAgentId) ||
    usable(options.sessionAgentId) ||
    usable(options.selectedAgentId) ||
    options.agents.find((agent) => agentHasUsableModel(agent, configs))
  );
}

export function alreadyProjectedLastFailure(smart: TranscriptSmartSnapshot): boolean {
  const last = smart.turns[smart.turns.length - 1];
  const fail = smart.lastFailure;
  if (!last || last.guardOk || !fail) return false;
  if (fail.turnId && last.turnId === fail.turnId) return true;
  // captureTurnError may put details.turnId on lastFailure while the turn row is local-failed-N.
  return last.userMessage === smart.goal && (last.errorCode || "") === (fail.code || "");
}

export function projectTranscript(smart: TranscriptSmartSnapshot, optimisticUser: string | null): TranscriptRow[] {
  const rows: TranscriptRow[] = [];
  for (const turn of smart.turns) {
    rows.push({ kind: "user", id: `${turn.turnId}-u`, text: turn.userMessage });
    if (turn.guardOk && turn.assistantMessage) {
      rows.push({
        kind: "assistant",
        id: `${turn.turnId}-a`,
        text: turn.assistantMessage,
        draftVersion: turn.draftVersion ?? undefined,
      });
    } else if (!turn.guardOk) {
      rows.push({
        kind: "failure",
        id: `${turn.turnId}-f`,
        code: turn.errorCode || "GUARD_REJECTED",
        message: turn.assistantMessage || "",
      });
    }
  }
  const lastUser = [...smart.turns].reverse()[0]?.userMessage;
  const needOptimistic = Boolean(optimisticUser) && optimisticUser !== lastUser;
  if (smart.generating) {
    if (needOptimistic) rows.push({ kind: "user", id: "optimistic", text: optimisticUser! });
    rows.push({ kind: "pending", id: "inflight" });
  } else if (smart.lastFailure && !alreadyProjectedLastFailure(smart)) {
    if (needOptimistic) rows.push({ kind: "user", id: "optimistic", text: optimisticUser! });
    rows.push({
      kind: "failure",
      id: "last-failure",
      code: smart.lastFailure.code,
      message: smart.lastFailure.message,
    });
  }
  return rows;
}

export function resolveNodeAiReason(
  node: WorkflowGraphNode | undefined,
  explanations: SmartDAGNodeExplanation[] = [],
): string {
  if (!node) return "";
  const fromUi = typeof node.ui?.reason === "string" ? node.ui.reason.trim() : "";
  if (fromUi) return fromUi;
  return explanations.find((item) => item.nodeId === node.id)?.reason?.trim() || "";
}

export function failureCtas(code: string, sessionStatus?: string): GenerateFailureCta[] {
  // CTA source is the S7 code table, not store.recoveryActions.
  if (code === "AGENT_MODEL_REQUIRED") {
    return [
      { key: "bind-model", labelKey: "workflow.generateGoBindModel", kind: "primary" },
      { key: "switch-agent", labelKey: "workflow.generateSwitchAgent", kind: "secondary" },
    ];
  }
  if (code === "SESSION_CLOSED") {
    return [{ key: "new-attempt", labelKey: "workflow.generateNewAttempt", kind: "primary" }];
  }
  if (code === "SMART_DAG_TURN_IN_PROGRESS") {
    return [];
  }
  const ctas: GenerateFailureCta[] = [
    { key: "retry-rewrite", labelKey: "workflow.generateRetryRewrite", kind: "primary" },
  ];
  if (sessionStatus === "OPEN") {
    ctas.push({ key: "end-session", labelKey: "workflow.generateEndSession", kind: "secondary" });
  }
  return ctas;
}

export function visibleReviseIssues<T>(issues: T[], limit = 3): { preview: T[]; extra: T[] } {
  return { preview: issues.slice(0, limit), extra: issues.slice(limit) };
}

export function generateFailureDisplayKey(code: string): string {
  if (code === "AGENT_MODEL_REQUIRED") return "workflow.generateModelRequired";
  if (code === "GUARD_REJECTED") return "workflow.generateGuardRejected";
  if (code === "SMART_DAG_TURN_IN_PROGRESS") return "workflow.generateInProgress";
  if (code === "INTERNAL_ERROR" || code === "LLM_JOB_FAILED" || code === "NETWORK_ERROR") {
    return "workflow.generateInternalError";
  }
  return "workflow.generateFailureTitle";
}

export type GenerateNodePill = {
  nodeId: string;
  label: string;
};

/** Last-turn node pills for the assistant bubble. Dedupe by nodeId. */
export function assistantNodePills(explanations: SmartDAGNodeExplanation[] = []): GenerateNodePill[] {
  const pills: GenerateNodePill[] = [];
  const seen = new Set<string>();
  for (const item of explanations) {
    const nodeId = item.nodeId.trim();
    if (!nodeId || seen.has(nodeId)) continue;
    seen.add(nodeId);
    const label = displayNodeTitle({
      id: nodeId,
      type: "",
      label: item.title.trim(),
      typeLabel: item.title.trim() || nodeId,
    });
    pills.push({ nodeId, label });
  }
  return pills;
}

export function latestTranscriptDraftVersion(rows: TranscriptRow[]): number | undefined {
  for (let index = rows.length - 1; index >= 0; index -= 1) {
    const row = rows[index];
    if (row.kind === "assistant" && typeof row.draftVersion === "number") {
      return row.draftVersion;
    }
  }
  return undefined;
}

export function useWorkflowGenerateNarrow(): Ref<boolean> {
  const isNarrow = ref(false);

  onMounted(() => {
    if (typeof window.matchMedia !== "function") {
      return;
    }
    const media = window.matchMedia(WORKFLOW_GENERATE_NARROW_MEDIA);
    const sync = () => {
      isNarrow.value = media.matches;
    };
    sync();
    media.addEventListener("change", sync);
    onBeforeUnmount(() => media.removeEventListener("change", sync));
  });

  return isNarrow;
}

export function createWorkflowGenerateDockState() {
  const leftTab = ref<WorkflowGenerateLeftTab>("generate");
  const generatePresence = ref<WorkflowGeneratePresence>("open");
  const generateLock = ref(false);
  const generateSheetOpen = ref(false);
  const applyHighlightEpoch = ref(0);
  const prompt = ref("");
  const selectedAgentId = ref("");
  const agentsLoadState = ref<WorkflowGenerateAgentsLoadState>("idle");
  const pendingFailureFeedback = ref<FailureFeedback | null>(null);
  const failureFeedbackBannerHidden = ref(false);
  const optimisticUserMessage = ref<string | null>(null);
  const autoOpenedReason = ref<WorkflowGenerateAutoOpenedReason>(null);
  const agentPopoverOpen = ref(false);
  const endSessionConfirmVisible = ref(false);

  function syncLeftTabForOpenEditor(hasPersistableDraft: boolean) {
    leftTab.value = hasPersistableDraft ? "nodes" : "generate";
    generateSheetOpen.value = !hasPersistableDraft;
  }

  function selectGenerateTab() {
    leftTab.value = "generate";
    generateSheetOpen.value = true;
  }

  function selectNodesTab(hasPersistableDraft: boolean) {
    if (!hasPersistableDraft) {
      return;
    }
    leftTab.value = "nodes";
    generateSheetOpen.value = false;
    agentPopoverOpen.value = false;
    endSessionConfirmVisible.value = false;
  }

  function toggleGenerateFromTopbar(hasPersistableDraft: boolean) {
    if (leftTab.value === "generate") {
      selectNodesTab(hasPersistableDraft);
      return;
    }
    selectGenerateTab();
  }

  function closeGenerateSheet(hasPersistableDraft: boolean) {
    if (generateLock.value) {
      return;
    }
    generateSheetOpen.value = false;
    agentPopoverOpen.value = false;
    endSessionConfirmVisible.value = false;
    if (hasPersistableDraft) {
      leftTab.value = "nodes";
    }
  }

  function resetGenerateDock() {
    leftTab.value = "generate";
    generatePresence.value = "open";
    generateLock.value = false;
    generateSheetOpen.value = false;
    applyHighlightEpoch.value = 0;
    prompt.value = "";
    selectedAgentId.value = "";
    agentsLoadState.value = "idle";
    pendingFailureFeedback.value = null;
    failureFeedbackBannerHidden.value = false;
    optimisticUserMessage.value = null;
    autoOpenedReason.value = null;
    agentPopoverOpen.value = false;
    endSessionConfirmVisible.value = false;
  }

  return {
    leftTab,
    generatePresence,
    generateLock,
    generateSheetOpen,
    applyHighlightEpoch,
    prompt,
    selectedAgentId,
    agentsLoadState,
    pendingFailureFeedback,
    failureFeedbackBannerHidden,
    optimisticUserMessage,
    autoOpenedReason,
    agentPopoverOpen,
    endSessionConfirmVisible,
    syncLeftTabForOpenEditor,
    selectGenerateTab,
    selectNodesTab,
    toggleGenerateFromTopbar,
    closeGenerateSheet,
    resetGenerateDock,
  };
}

export type GenerateAgentOption = {
  id: string;
  name: string;
  usable: boolean;
};
