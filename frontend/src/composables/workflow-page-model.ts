import { tt } from "../i18n/tt";
/**
 * Workflow page model (ZKL-64 item 13).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  WORKFLOW_GENERATE_NARROW_MEDIA,
  WORKFLOW_GENERATE_PROMPT_MAX,
  agentHasUsableModel,
  createEmptyWorkflowGraphDraft,
  createWorkflowGenerateDockState,
  pickPreferredGenerateAgent as pickPreferredGenerateAgentFromCatalog,
  projectTranscript,
  resolveNodeAiReason,
  type WorkflowGenerateFailureCtaKey,
} from "./workflow-generate-dock";
import { bindPublishedWorkflowToSessionAgent as bindGeneratedDraftToSessionAgent } from "./workflow-publish-bind";
import type { ManagementListColumn } from "../components/ManagementList.vue";
import type { ManagementRowAction } from "../components/ManagementRowActions.vue";
import type { ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import {
  createWorkflowHistoryState,
  cloneWorkflowGraph,
  pushWorkflowHistoryState,
  sameWorkflowGraph,
  type WorkflowHistoryState,
} from "../components/workflow/workflow-history";
import { autoLayoutWorkflowGraph, layoutWorkflowGraphIfNeeded } from "../components/workflow/workflow-layout";
import { toAPIError } from "../services/api";
import { useAgentStore } from "../stores/agents";
import { useAuthStore } from "../stores/auth";
import { useModelConfigStore } from "../stores/modelConfigs";
import { useSmartDagStore, type SmartDAGTurnResult } from "../stores/smartdag";
import { useToolsStore } from "../stores/tools";
import { useWorkflowStore, WorkflowDraftConflictError } from "../stores/workflow";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Agent,
  Execution,
  FailureFeedback,
  Tool,
  Workflow,
  WorkflowCompilation,
  WorkflowCompilationIssue,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
  WorkflowGraphNode,
  WorkflowGraphNodeType,
  WorkflowReadiness,
  WorkflowReadinessBlocker,
  WorkflowListQuery,
  WorkflowSummary,
  WorkflowStatus,
} from "../types/domain";
import {
  createDefaultWorkflowGraphDraft,
  defaultPortsForNodeType,
  listWorkflowVariableReferences,
  listWorkflowVariableReferencesForNode,
  normalizeWorkflowGraphDraft,
} from "../utils/workflow-graph";
import { isAxiosError } from "axios";

export function createWorkflowPageModel() {
  interface WorkflowMetadataDraft {
    id: string;
    name: string;
    slug: string;
    workspaceId: string;
    description: string;
    status: WorkflowStatus;
  }

  interface WorkflowTrialRunInputField {
    key: string;
    type: string;
    required: boolean;
    description: string;
  }

  interface WorkflowContextMenuState {
    targetType: "node" | "edge";
    targetId: string;
    position: { x: number; y: number };
  }

  type EditorDraftLoadStatus = "loaded" | "failed" | "stale";
  type EditorActionKind = "save" | "validate" | "trial-run" | "publish" | "force-publish" | "generate";
  type EditorDraftLoadState = "idle" | "loading" | "loaded" | "failed";
  type GraphMutationOptions = {
    recordHistory?: boolean;
  };
  type WorkflowEditorReadinessStep = {
    key: string;
    label: string;
    icon: string;
    state: "ready" | "current" | "pending";
  };

  const workflowStore = useWorkflowStore();
  const toolsStore = useToolsStore();
  const workspaces = useWorkspaceStore();
  const auth = useAuthStore();
  const smart = useSmartDagStore();
  const agentStore = useAgentStore();
  const modelConfigStore = useModelConfigStore();
  const router = useRouter();
  const route = useRoute();
  const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));
  let editorDraftLoadRequestId = 0;
  let editorDraftTargetWorkflowId = "";
  const workflowToolCatalog = ref<Tool[]>([]);
  const workflowToolCatalogWorkspaceId = ref("");
  const workflowToolCatalogError = ref("");

  const workflowQuery = ref("");
  const workflowDetailVisible = ref(false);
  const workflowEditorVisible = ref(false);
  const workflowMetadataVisible = ref(false);
  const workflowMetadataMode = ref<"create" | "edit">("create");
  const workflowDraft = ref<WorkflowMetadataDraft>(newWorkflowDraft());
  const workflowMetadataTouched = ref(false);
  const workflowActionNote = ref("");
  const pendingEditorAction = ref<EditorActionKind>();
  const pendingWorkflowValidationId = ref("");
  const pendingTrialRun = ref(false);
  const pendingProductionRun = ref(false);
  const pendingRevisionActionId = ref("");
  const pendingRevisionCompare = ref(false);
  const pendingWorkflowDisable = ref(false);
  const selectedNodeId = ref("");
  const selectedEdgeId = ref("");
  const contextMenu = ref<WorkflowContextMenuState>();
  const editorGraph = ref<WorkflowGraphDraft>(createDefaultWorkflowGraphDraft());
  const editorHistory = ref<WorkflowHistoryState>(createWorkflowHistoryState());
  const editorDraftLoadState = ref<EditorDraftLoadState>("idle");
  const editorDraftLoadError = ref("");
  const trialRunVisible = ref(false);
  const trialRunTargetWorkflowId = ref("");
  const trialRunTargetWorkflowName = ref("");
  const forcePublishDialogVisible = ref(false);
  const forcePublishReasonDraft = ref("local-dev skip trial");
  const activeTraceExecutionId = ref("");
  const workflowDetailModalRef = ref<HTMLElement>();
  const workflowMetadataModalRef = ref<HTMLElement>();
  const workflowEditorShellRef = ref<HTMLElement>();
  const workflowFocusRestoreTarget = ref<HTMLElement>();
  const generateDock = createWorkflowGenerateDockState();
  let workflowQueryApplied = false;

  const workflowStatusOptions = [
    { label: tt("workflow.statusDraft"), value: "Draft" },
    { label: tt("workflow.statusReview"), value: "Review" },
    { label: tt("workflow.statusPublished"), value: "Published" },
    { label: tt("workflow.statusDisabled"), value: "Disabled" },
  ];
  const workflowEditorHelpText = tt("workflow.saveHint");

  const workspaceOptions = computed(() =>
    (workspaces.items || []).map((workspace) => ({
      label: `${workspace.name} (${workspace.displayName})`,
      value: workspace.id,
    })),
  );
  const workflowNameError = computed(() => (workflowDraft.value.name.trim() ? "" : tt("workflow.nameRequired")));
  const workflowWorkspaceError = computed(() =>
    workflowDraft.value.workspaceId.trim() ? "" : tt("workflow.workspaceRequired"),
  );
  const canSaveWorkflowMetadata = computed(() => !workflowNameError.value && !workflowWorkspaceError.value);
  const statusLabels: Record<WorkflowStatus, string> = {
    Draft: tt("workflow.statusDraft"),
    Review: tt("workflow.statusReview"),
    Published: tt("workflow.statusPublished"),
    Disabled: tt("workflow.statusDisabled"),
  };

  const executionStatusLabels: Record<string, string> = {
    Running: tt("workflow.execRunning"),
    Approval: tt("workflow.execApproval"),
    Success: tt("workflow.execSuccess"),
    Failed: tt("workflow.execFailed"),
  };

  const workflowColumns = computed<ManagementListColumn<WorkflowSummary>[]>(() => [
    {
      key: "workflow",
      label: tt("workflow.colName"),
      width: 300,
      sortable: true,
      sortKey: "name",
      getValue: (workflow) => `${workflow.name} ${workflow.description}`,
    },
    {
      key: "workspace",
      label: tt("workflow.colWorkspace"),
      width: 170,
      hidable: true,
      sortable: true,
      sortKey: "workspace",
      getValue: workflowWorkspaceLabel,
    },
    {
      key: "nodes",
      label: tt("workflow.colNodes"),
      width: 160,
      align: "right",
      hidable: true,
      sortable: true,
      sortKey: "nodeCount",
      getValue: (workflow) => `${workflowNodeCount(workflow)} / ${workflowEdgeCount(workflow)}`,
    },
    {
      key: "successRate",
      label: tt("workflow.colSuccess"),
      width: 160,
      align: "right",
      hidable: true,
      getValue: workflowSuccessRateLabel,
    },
    {
      key: "status",
      label: tt("workflow.colStatus"),
      width: 140,
      hidable: true,
      sortable: true,
      sortKey: "status",
      getValue: workflowTableStatusLabel,
    },
    {
      key: "updatedAt",
      label: tt("workflow.colUpdated"),
      width: 130,
      hidable: true,
      sortable: true,
      sortKey: "updatedAt",
      getValue: formatWorkflowUpdatedAt,
    },
    { key: "actions", label: tt("workflow.colActions"), width: 68, align: "right", headerAlign: "center" },
  ]);
  const workflowSummaryItems = computed<ManagementSummaryItem[]>(() => {
    const workflows = workflowStore.workflows;
    return [
      { label: tt("workflow.summaryTotal"), value: workflows.length, icon: "fa-solid fa-diagram-project" },
      {
        label: tt("workflow.summaryPublished"),
        value: workflows.filter((workflow) => workflow.status === "Published").length,
        icon: "fa-solid fa-circle-check",
      },
      {
        label: tt("workflow.summaryDraft"),
        value: workflows.filter((workflow) => workflow.status === "Draft").length,
        icon: "fa-solid fa-pen",
        tone: "warning",
      },
      {
        label: tt("workflow.summaryReview"),
        value: workflows.filter((workflow) => workflow.status === "Review").length,
        icon: "fa-solid fa-list-check",
        tone: "info",
      },
    ];
  });

  const selectedWorkflow = computed(() =>
    workflowStore.workflows.find((workflow) => workflow.id === workflowStore.selectedWorkflowId),
  );
  const selectedWorkflowDetail = computed(() => workflowStore.selectedWorkflowDetail);
  const selectedWorkflowReadiness = computed(
    () => selectedWorkflowDetail.value?.readiness || selectedWorkflow.value?.readiness,
  );
  const selectedWorkflowRevisions = computed(() =>
    selectedWorkflow.value ? workflowStore.revisionsByWorkflowId[selectedWorkflow.value.id] || [] : [],
  );
  const selectedWorkflowRevisionDiff = computed(() =>
    selectedWorkflow.value ? workflowStore.revisionDiffByWorkflowId[selectedWorkflow.value.id] : undefined,
  );
  const selectedWorkflowDraft = computed(() =>
    workflowStore.activeDraft?.workflowId === selectedWorkflow.value?.id ? workflowStore.activeDraft : undefined,
  );
  const hasPersistableDraft = computed(() =>
    Boolean(
      workflowStore.selectedWorkflowId &&
      workflowStore.activeDraft &&
      workflowStore.activeDraft.workflowId === workflowStore.selectedWorkflowId,
    ),
  );
  const workflowEditorBusy = computed(() => Boolean(pendingEditorAction.value) || smart.generating);
  const generateLock = computed(() => pendingEditorAction.value === "generate" || smart.generating);
  const generateSending = computed(() => smart.generating);
  const selectedWorkflowCanPublish = computed(() => Boolean(selectedWorkflowReadiness.value?.canPublish));
  /** PLATFORM_ADMIN force-publish: VALID compile enough; skips real trial. Server also gates. */
  const canForcePublishWorkflow = computed(
    () => auth.user?.platformRole === "PLATFORM_ADMIN" && Boolean(selectedWorkflow.value),
  );
  const selectedWorkflowCanForcePublish = computed(() => {
    if (!canForcePublishWorkflow.value) return false;
    const readiness = selectedWorkflowReadiness.value;
    if (!readiness) return false;
    // Need a current VALID compilation; trial optional.
    if (readiness.stage === "Disabled" || readiness.stage === "DraftMissing") return false;
    if (readiness.stage === "CompileRequired" || readiness.stage === "CompileFailed") return false;
    return Boolean(
      readiness.compilationValid ||
      readiness.canPublish ||
      readiness.stage === "TrialRequired" ||
      readiness.stage === "PublishReady",
    );
  });
  const workflowEditorReadinessSteps = computed(() =>
    buildWorkflowEditorReadinessSteps(selectedWorkflowReadiness.value),
  );
  const workflowEditorPublishTitle = computed(() => {
    if (selectedWorkflowCanPublish.value) {
      return tt("workflow.publish");
    }
    return workflowPublishBlockedTitle(selectedWorkflowReadiness.value?.stage);
  });
  const workflowEditorForcePublishTitle = computed(() => {
    if (!canForcePublishWorkflow.value) {
      return tt("workflow.forcePublishAdminOnly");
    }
    if (selectedWorkflowCanForcePublish.value) {
      return tt("workflow.forcePublishHint");
    }
    return tt("workflow.forcePublishNeedValid");
  });
  const selectedGraphNode = computed(() => editorGraph.value.nodes.find((node) => node.id === selectedNodeId.value));
  const selectedNodeAiReason = computed(() => resolveNodeAiReason(selectedGraphNode.value, smart.nodeExplanations));
  const generateTranscript = computed(() => projectTranscript(smart, generateDock.optimisticUserMessage.value));
  const generateAgentOptions = computed(() =>
    agentStore.items.map((item) => ({
      id: item.id,
      name: item.name,
      usable: agentHasUsableModel(item, modelConfigStore.items),
    })),
  );
  const selectedGenerateAgent = computed(
    () =>
      agentStore.items.find((item) => item.id === generateDock.selectedAgentId.value) ||
      pickPreferredGenerateAgentFromCatalog({
        agents: agentStore.items,
        modelConfigs: modelConfigStore.items,
        draftAgentId: draftGenerateAgentId(),
        sessionAgentId: smart.agentId,
        selectedAgentId: generateDock.selectedAgentId.value,
      }),
  );
  const selectedGenerateAgentUsable = computed(() =>
    agentHasUsableModel(selectedGenerateAgent.value, modelConfigStore.items),
  );
  const showGenerateDirtyChip = computed(
    () => isEditorDraftDirty() && String(editorGraph.value.ui?.generatedBy || "").startsWith("smart-dag"),
  );
  const generateLastFailure = computed(() => smart.lastFailure);
  const generateLastGuardReport = computed(() => smart.lastGuardReport);
  const generateReasoningSteps = computed(() => smart.reasoningSteps);
  const generateMissingCapabilities = computed(() => smart.missingCapabilities);
  const generateSessionClosed = computed(() => smart.sessionStatus === "CLOSED");
  const selectedGraphEdge = computed(() => editorGraph.value.edges.find((edge) => edge.id === selectedEdgeId.value));
  const canUndoEditorChange = computed(() => editorHistory.value.past.length > 0);
  const canRedoEditorChange = computed(() => editorHistory.value.future.length > 0);
  const editorDirtyState = computed(() => (isEditorDraftDirty() ? "dirty" : "saved"));
  const editorVariableRefs = computed(() =>
    listWorkflowVariableReferences(editorGraph.value).map((item) => `{{${item.key}}}`),
  );
  const selectedNodeVariableRefs = computed(() => {
    if (!selectedNodeId.value) {
      return editorVariableRefs.value;
    }
    return listWorkflowVariableReferencesForNode(editorGraph.value, selectedNodeId.value).map(
      (item) => `{{${item.key}}}`,
    );
  });
  const availableToolOptions = computed(() => {
    const workspaceId = selectedWorkflow.value?.workspaceId || selectedWorkflowDetail.value?.workspaceId || "";
    return workflowToolCatalog.value
      .filter((tool) => (!workspaceId || tool.workspaceId === workspaceId) && tool.status === "Published")
      .map((tool) => ({
        // Short label keeps the inspector menu under the trigger (no full UUID).
        label: tool.name?.trim() || tool.id,
        value: tool.id,
      }));
  });
  const activeCompilationIssues = computed<WorkflowCompilationIssue[]>(() =>
    workflowStore.activeDraft?.workflowId === selectedWorkflow.value?.id
      ? workflowStore.activeCompilation?.issues || []
      : [],
  );
  const trialRunInputSchema = computed<WorkflowTrialRunInputField[]>(() => {
    if (workflowStore.activeDraft?.workflowId === trialRunTargetWorkflowId.value) {
      return extractTrialRunInputSchema(workflowStore.activeDraft.graph);
    }
    return [];
  });
  const lastSuccessfulTrialInput = computed(() =>
    trialRunTargetWorkflowId.value
      ? workflowStore.lastSuccessfulTrialInputByWorkflowId[trialRunTargetWorkflowId.value]
      : undefined,
  );
  const activeTraceExecution = computed(() =>
    activeTraceExecutionId.value ? workflowStore.executionById[activeTraceExecutionId.value] : undefined,
  );

  const selectedWorkflowExecutions = computed(() => {
    const workflowId = selectedWorkflow.value?.id;
    if (!workflowId) return [];
    return workflowStore.executions.filter((execution) => execution.workflowId === workflowId).slice(0, 3);
  });
  const workflowEditorFeedbackMessage = computed(() => {
    if (editorDraftLoadState.value === "loading") return tt("workflow.loadingDraft");
    if (editorDraftLoadState.value === "failed")
      return workflowActionNote.value.trim() || tt("workflow.draftLoadFailedRetry");
    if (pendingEditorAction.value === "save") return tt("workflow.savingCanvas");
    if (pendingEditorAction.value === "validate") return tt("workflow.checkingIssuesBusy");
    if (pendingEditorAction.value === "trial-run") return tt("workflow.preparingTrial");
    if (pendingEditorAction.value === "publish") return tt("workflow.publishingDraft");
    if (pendingEditorAction.value === "force-publish") return tt("workflow.forcePublishingSkipTrial");
    if (pendingEditorAction.value === "generate") return tt("workflow.generateBusy");
    if (workflowActionNote.value.trim()) return workflowActionNote.value;
    return tt("workflow.saveHint");
  });
  const workflowLifecycleSteps = computed(() =>
    buildWorkflowLifecycleSteps(selectedWorkflowReadiness.value?.stage, selectedWorkflowExecutions.value.length),
  );
  const workflowLifecycleHint = computed(() => {
    const workflow = selectedWorkflow.value;
    if (!workflow) {
      return tt("workflow.lifecycleHintSelect");
    }
    return workflowNextAction(workflow);
  });
  const selectedWorkflowRevisionEmptyText = computed(() =>
    selectedWorkflow.value ? tt("workflow.revisionEmptyWithWorkflow") : tt("workflow.revisionEmptySelect"),
  );
  const selectedWorkflowDiffEmptyText = computed(() => {
    if (!selectedWorkflow.value) {
      return tt("workflow.diffEmptySelect");
    }
    return selectedWorkflowRevisions.value.length > 1 ? tt("workflow.diffEmptyHint") : tt("workflow.diffEmptyNeedTwo");
  });
  const selectedWorkflowExecutionEmptyText = computed(() =>
    selectedWorkflow.value ? tt("workflow.executionEmptyWithWorkflow") : tt("workflow.executionEmptySelect"),
  );

  const selectedWorkflowSteps = computed(() => {
    if (selectedWorkflowDraft.value) {
      return selectedWorkflowDraft.value.graph.nodes.map((node, index) => ({
        id: node.id,
        order: index + 1,
        title: node.label,
        type: nodeTypeLabel(node.type),
        detail: `${node.id} · ${node.type}`,
      }));
    }

    const workflow = selectedWorkflowDetail.value;
    if (!workflow) return [];
    return [];
  });

  onMounted(async () => {
    window.addEventListener("keydown", handleEditorKeydown);
    try {
      if (!workspaces.items.length) await workspaces.load();
      applyWorkspaceQuery();
      if (!hasWorkspaceContext.value) return;
      await loadWorkflowPageAssets();
      await applyWorkflowEntryQuery();
    } catch {
      // The shared Workspace state provides recovery actions when bootstrap fails.
    }
  });

  async function loadWorkflowPageAssets() {
    await workflowStore.loadWorkflowAssets();
  }

  onBeforeUnmount(() => {
    window.removeEventListener("keydown", handleEditorKeydown);
  });

  watch(workflowEditorVisible, (visible) => {
    if (visible) {
      generateDock.syncLeftTabForOpenEditor(hasPersistableDraft.value);
    }
  });

  watch(
    () => [workflowEditorVisible.value, selectedGraphNode.value?.type, selectedWorkflow.value?.workspaceId] as const,
    async ([visible, nodeType, workspaceId]) => {
      if (!visible || nodeType !== "Tool" || !workspaceId || workflowToolCatalogWorkspaceId.value === workspaceId) {
        return;
      }

      try {
        workflowToolCatalogError.value = "";
        workflowToolCatalog.value = await toolsStore.loadTools({ commit: false });
        workflowToolCatalogWorkspaceId.value = workspaceId;
      } catch (error) {
        workflowToolCatalog.value = [];
        workflowToolCatalogError.value = actionErrorMessage(error, tt("workflow.toolCatalogLoadFailed"));
      }
    },
  );

  watch(workflowDetailVisible, (visible) => {
    if (!visible) {
      invalidatePendingEditorLoad();
    }
  });

  function newWorkflowDraft(): WorkflowMetadataDraft {
    const defaultWorkspaceId = workspaces.activeWorkspaceId || workspaces.items?.[0]?.id || "default";
    return {
      id: "",
      name: "",
      slug: "",
      workspaceId: defaultWorkspaceId,
      description: "",
      status: "Draft",
    };
  }

  function cloneGraph(graph: WorkflowGraphDraft) {
    return cloneWorkflowGraph(normalizeWorkflowGraphDraft(graph));
  }

  function sameGraph(left: WorkflowGraphDraft, right: WorkflowGraphDraft) {
    return sameWorkflowGraph(left, right);
  }

  function isEditorDraftDirty() {
    // No selected workflow (E1 empty canvas) is clean. Missing/mismatched activeDraft stays dirty.
    if (!workflowStore.selectedWorkflowId) {
      return false;
    }
    const draft = workflowStore.activeDraft;
    if (!draft || draft.workflowId !== workflowStore.selectedWorkflowId) {
      return true;
    }
    return !sameGraph(editorGraph.value, draft.graph);
  }

  function generateWorkspaceId() {
    return workspaces.activeWorkspaceId || workspaces.items[0]?.id || "";
  }

  function draftGenerateAgentId() {
    const draftAgent = workflowStore.activeDraft?.graph?.ui?.agentId;
    return typeof draftAgent === "string" ? draftAgent : "";
  }

  function graphGenerateSessionId() {
    const sessionId = editorGraph.value.ui?.sessionId;
    return typeof sessionId === "string" ? sessionId.trim() : "";
  }

  function usableGenerateAgent(id?: string) {
    const trimmed = (id || "").trim();
    if (!trimmed) return undefined;
    const agent = agentStore.items.find((item) => item.id === trimmed);
    return agent && agentHasUsableModel(agent, modelConfigStore.items) ? agent : undefined;
  }

  function pickPreferredGenerateAgent() {
    return pickPreferredGenerateAgentFromCatalog({
      agents: agentStore.items,
      modelConfigs: modelConfigStore.items,
      draftAgentId: draftGenerateAgentId(),
      sessionAgentId: smart.agentId,
      selectedAgentId: generateDock.selectedAgentId.value,
    });
  }

  function confirmSwitchAgent() {
    return window.confirm(tt("workflow.generateSwitchAgentConfirm"));
  }

  function selectGenerateAgent(agent: Agent, options: { userInitiated?: boolean } = {}) {
    const workspaceId = generateWorkspaceId();
    const nextModel = (agent.modelConfigId || "").trim();
    const agentChanged = smart.agentId !== agent.id;
    if (agentChanged && options.userInitiated) {
      if (!confirmSwitchAgent()) return;
      smart.resetSessionOnly();
      generateDock.selectedAgentId.value = agent.id;
      smart.setContext(workspaceId, agent.id, nextModel);
      generateDock.agentPopoverOpen.value = false;
      return;
    }
    generateDock.selectedAgentId.value = agent.id;
    // setContext clears turns when the agent id changes. Auto/sync may only fill modelConfigId.
    if (!agentChanged || !smart.sessionId) {
      smart.setContext(workspaceId, agent.id, nextModel);
    }
  }

  function selectGenerateAgentById(agentId: string, userInitiated = false) {
    const agent = agentStore.items.find((item) => item.id === agentId);
    if (!agent) return;
    selectGenerateAgent(agent, { userInitiated });
  }

  function syncGenerateContext(): boolean {
    // Send uses the chip / live session agent. Never re-pick draft.ui.agentId over a user switch.
    const agent = usableGenerateAgent(generateDock.selectedAgentId.value) || usableGenerateAgent(smart.agentId);
    if (!agent) return false;
    if (smart.sessionId && smart.agentId && agent.id !== smart.agentId) {
      const sessionAgent = usableGenerateAgent(smart.agentId);
      if (!sessionAgent) return false;
      generateDock.selectedAgentId.value = sessionAgent.id;
      smart.setContext(generateWorkspaceId(), sessionAgent.id, sessionAgent.modelConfigId || "");
      return Boolean(smart.modelConfigId);
    }
    selectGenerateAgent(agent);
    return Boolean(smart.modelConfigId);
  }

  function hydrateGenerateAgentChip() {
    const selected = usableGenerateAgent(generateDock.selectedAgentId.value);
    if (selected) {
      selectGenerateAgent(selected);
      return;
    }
    if (smart.sessionId) {
      const sessionAgent = usableGenerateAgent(smart.agentId);
      if (sessionAgent) {
        selectGenerateAgent(sessionAgent);
        return;
      }
    }
    const agent = pickPreferredGenerateAgent();
    if (agent) selectGenerateAgent(agent);
  }

  let generatePrepareEpoch = 0;
  let generatePrepareInFlight: Promise<void> | undefined;
  const generateRestorePending = ref(false);

  async function restoreGenerateSessionIfNeeded(epoch: number) {
    const targetId = selectedWorkflow.value?.id || "";
    const graphSessionId = graphGenerateSessionId();
    if (smart.sessionId) {
      const mismatch =
        !smart.sessionWorkflowId ||
        (Boolean(targetId) && smart.sessionWorkflowId !== targetId) ||
        (Boolean(graphSessionId) && graphSessionId !== smart.sessionId);
      if (mismatch) {
        smart.resetSessionOnly();
      }
    }
    if (!graphSessionId) return;
    // User already switched Agent (session dropped). Do not revive the prior session.
    if (!smart.sessionId && generateDock.selectedAgentId.value) return;
    const workspaceId = generateWorkspaceId();
    if (!workspaceId) return;
    try {
      await smart.loadSession(workspaceId, graphSessionId, {
        adoptGraph: false,
        shouldApply: () => epoch === generatePrepareEpoch && pendingEditorAction.value !== "generate",
      });
    } catch {
      // CLOSED / GET failure: stay on a new attempt (S10). Do not adopt the graph.
    }
  }

  async function loadGenerateAgentsIfNeeded() {
    const workspaceId = generateWorkspaceId();
    if (!workspaceId) {
      generateDock.agentsLoadState.value = "failed";
      return;
    }
    if (generateDock.agentsLoadState.value === "failed" && !agentStore.items.length) {
      hydrateGenerateAgentChip();
      return;
    }
    if (generateDock.agentsLoadState.value === "loaded" && agentStore.items.length) {
      hydrateGenerateAgentChip();
      return;
    }
    generateDock.agentsLoadState.value = "loading";
    try {
      await agentStore.loadAgents({ workspaceId });
      try {
        await modelConfigStore.loadModelConfigs();
      } catch {
        // Missing catalog does not block a non-empty modelConfigId.
      }
      generateDock.agentsLoadState.value = "loaded";
      hydrateGenerateAgentChip();
    } catch {
      generateDock.agentsLoadState.value = "failed";
    }
  }

  function prepareGenerateDock() {
    if (pendingEditorAction.value === "generate" || smart.generating) {
      return generatePrepareInFlight || Promise.resolve();
    }
    if (generatePrepareInFlight) return generatePrepareInFlight;
    const epoch = ++generatePrepareEpoch;
    generateRestorePending.value = true;
    generatePrepareInFlight = (async () => {
      try {
        await restoreGenerateSessionIfNeeded(epoch);
        if (epoch !== generatePrepareEpoch || pendingEditorAction.value === "generate") return;
        await loadGenerateAgentsIfNeeded();
      } finally {
        if (epoch === generatePrepareEpoch) {
          generateRestorePending.value = false;
        }
        generatePrepareInFlight = undefined;
      }
    })();
    return generatePrepareInFlight;
  }

  function applyGeneratedDraftToEditor(result: SmartDAGTurnResult) {
    const draft = result.draft;
    if (!draft?.graph) return;
    workflowStore.selectedWorkflowId = result.workflow.id;
    editorGraph.value = layoutWorkflowGraphIfNeeded(cloneGraph(draft.graph));
    resetEditorHistory();
    syncSelection();
    generateDock.applyHighlightEpoch.value += 1;
    generateDock.optimisticUserMessage.value = null;
    void workflowStore.loadWorkflowReadiness(result.workflow.id);
    void loadWorkflowRegistry({ page: 1 });
  }

  async function sendGenerateTurn() {
    if (smart.generating || pendingEditorAction.value) return;
    if (generatePrepareInFlight) {
      await generatePrepareInFlight;
    }
    if (smart.generating || pendingEditorAction.value) return;
    const message = generateDock.prompt.value.trim();
    if (!message || [...message].length > WORKFLOW_GENERATE_PROMPT_MAX) return;

    // Chip / live session must write modelConfigId before persist / ensureSession.
    // Do not re-pick draft.ui.agentId — that would revert a confirmed Agent switch.
    if (!syncGenerateContext()) {
      workflowActionNote.value = tt("workflow.generateModelRequired");
      return;
    }

    const targetId = selectedWorkflow.value?.id;
    if (
      smart.sessionId &&
      ((!smart.sessionWorkflowId && Boolean(targetId)) ||
        (Boolean(smart.sessionWorkflowId) && smart.sessionWorkflowId !== (targetId || "")))
    ) {
      smart.resetSessionOnly();
    }

    pendingEditorAction.value = "generate";
    generatePrepareEpoch += 1;
    generateDock.generateLock.value = true;
    try {
      if (targetId && isEditorDraftDirty()) {
        const draft = workflowStore.activeDraft;
        if (!draft || draft.workflowId !== targetId) {
          workflowActionNote.value = tt("workflow.draftLoadFailedRetry");
          return;
        }
        try {
          const saved = await persistEditorDraft();
          if (!saved) return;
        } catch (error) {
          workflowActionNote.value = actionErrorMessage(error, tt("workflow.saveDraftFailed"));
          return;
        }
      }
      generateDock.optimisticUserMessage.value = message;
      generateDock.prompt.value = "";
      const result = await smart.sendTurn({
        workspaceId: generateWorkspaceId(),
        agentId: smart.agentId,
        message,
        ...(targetId ? { workflowId: targetId } : {}),
        feedback: generateDock.pendingFailureFeedback.value || undefined,
      });
      generateDock.pendingFailureFeedback.value = null;
      generateDock.failureFeedbackBannerHidden.value = false;
      applyGeneratedDraftToEditor(result);
    } catch {
      // lastFailure + transcript projection; canvas stays on the previous draft.
    } finally {
      pendingEditorAction.value = undefined;
      generateDock.generateLock.value = false;
    }
  }

  function handleGenerateFailureCta(key: WorkflowGenerateFailureCtaKey) {
    if (key === "bind-model") {
      void router.push({ name: "agents" });
      return;
    }
    if (key === "switch-agent") {
      generateDock.agentPopoverOpen.value = true;
      return;
    }
    if (key === "new-attempt") {
      smart.resetSessionOnly();
      generateDock.optimisticUserMessage.value = null;
      hydrateGenerateAgentChip();
      focusGenerateTextarea();
      return;
    }
    if (key === "retry-rewrite") {
      focusGenerateTextarea();
      return;
    }
    if (key === "end-session") {
      generateDock.endSessionConfirmVisible.value = true;
    }
  }

  function cancelEndGenerateSession() {
    generateDock.endSessionConfirmVisible.value = false;
  }

  async function confirmEndGenerateSession() {
    generateDock.endSessionConfirmVisible.value = false;
    generatePrepareEpoch += 1;
    generateRestorePending.value = false;
    try {
      await smart.closeSession();
    } catch {
      // Local session still ends so the dock can start a new attempt.
    }
    smart.resetSessionOnly();
    generateDock.optimisticUserMessage.value = null;
  }

  function focusGenerateTextarea() {
    void nextTick(() => {
      const textarea = workflowEditorShellRef.value?.querySelector<HTMLTextAreaElement>(
        "textarea.workflow-generate-prompt",
      );
      if (!textarea) return;
      textarea.focus();
      const end = textarea.value.length;
      textarea.setSelectionRange(end, end);
    });
  }

  function hideFailureFeedbackBanner() {
    generateDock.failureFeedbackBannerHidden.value = true;
  }

  function dismissPendingFailureFeedback() {
    generateDock.pendingFailureFeedback.value = null;
    generateDock.failureFeedbackBannerHidden.value = false;
  }

  function resetEditorHistory() {
    editorHistory.value = createWorkflowHistoryState();
  }

  function replaceEditorGraph(nextGraph: WorkflowGraphDraft, options: GraphMutationOptions = {}) {
    if (sameGraph(editorGraph.value, nextGraph)) {
      return;
    }

    if (options.recordHistory !== false) {
      editorHistory.value = pushWorkflowHistoryState(editorHistory.value, editorGraph.value);
    }

    editorGraph.value = cloneGraph(nextGraph);
    syncSelection();
  }

  function updateEditorGraph(
    updater: (current: WorkflowGraphDraft) => WorkflowGraphDraft,
    options: GraphMutationOptions = {},
  ) {
    replaceEditorGraph(updater(editorGraph.value), options);
  }

  async function ensureWorkspacesLoaded() {
    if (workspaces.items?.length) return;
    try {
      await workspaces.load();
    } catch {
      // AppShell usually preloads workspaces; keep the editor usable even if the refresh fails.
    }
  }

  async function loadEditorDraft(workflowId: string): Promise<EditorDraftLoadStatus> {
    const requestId = ++editorDraftLoadRequestId;
    editorDraftLoadState.value = "loading";
    editorDraftLoadError.value = "";

    try {
      const { draft, latestCompilation } = await workflowStore.loadWorkflowDraft(workflowId);
      // requestToken + workflowId fence: stale completions must not commit UI.
      if (requestId !== editorDraftLoadRequestId || editorDraftTargetWorkflowId !== workflowId) {
        return "stale" as const;
      }

      workflowStore.activeDraft = draft;
      workflowStore.activeCompilation = latestCompilation;
      // Unstack overlapping/zero-position graphs when opening the editor (HITL chains etc.).
      // Graph must come from the server Draft only — never synthesize a default empty graph here.
      editorGraph.value = layoutWorkflowGraphIfNeeded(cloneGraph(draft.graph));
      resetEditorHistory();
      syncSelection();
      editorDraftLoadState.value = "loaded";
      editorDraftLoadError.value = "";
      return "loaded" as const;
    } catch (error) {
      if (requestId !== editorDraftLoadRequestId || editorDraftTargetWorkflowId !== workflowId) {
        return "stale" as const;
      }

      // Failure retains prior legitimate state / detail context. Do not write a default empty graph
      // and do not clear selected workflow (ZKL-56 UX-01).
      editorDraftLoadState.value = "failed";
      const apiError = toAPIError(error);
      const requestIdHint = apiError.requestId ? tt("workflow.requestIdHint", { id: apiError.requestId }) : "";
      const rawMessage = apiError.message?.trim() || "";
      const detail =
        rawMessage && rawMessage !== tt("workflow.draftLoadFailed") && rawMessage !== "草稿加载失败"
          ? tt("workflow.detailPrefix", { message: rawMessage })
          : "";
      editorDraftLoadError.value = tt("workflow.draftLoadFailedWithDetail", { detail, requestId: requestIdHint });
      return "failed" as const;
    }
  }

  function invalidatePendingEditorLoad() {
    editorDraftLoadRequestId += 1;
    editorDraftTargetWorkflowId = "";
    editorDraftLoadState.value = "idle";
  }

  function closeWorkflowEditor(options: { restoreFocus?: boolean } = {}) {
    closeTrialRunDialog({ restoreFocus: false });
    invalidatePendingEditorLoad();
    closeContextMenu();
    pendingEditorAction.value = undefined;
    resetEditorHistory();
    generateDock.resetGenerateDock();
    workflowEditorVisible.value = false;
    stripGenerateQuery();
    if (options.restoreFocus !== false) {
      restoreWorkflowFocus();
    }
  }

  function currentWorkflowQuery(): Record<string, unknown> {
    return (
      (route?.query as Record<string, unknown> | undefined) ??
      Object.fromEntries(new URLSearchParams(window.location.search))
    );
  }

  function queryString(value: unknown): string {
    if (Array.isArray(value)) {
      return queryString(value[0]);
    }
    return typeof value === "string" ? value.trim() : "";
  }

  function isNarrowViewport() {
    return typeof window.matchMedia === "function" && window.matchMedia(WORKFLOW_GENERATE_NARROW_MEDIA).matches;
  }

  function applyWorkspaceQuery() {
    const workspaceId = queryString(currentWorkflowQuery().workspaceId);
    if (workspaceId && workspaces.items.some((item) => item.id === workspaceId)) {
      workspaces.selectWorkspace(workspaceId);
    }
  }

  async function applyWorkflowEntryQuery() {
    // Apply once on enter. Watching query would fight close, which strips generate.
    if (workflowQueryApplied) return;
    workflowQueryApplied = true;
    const query = currentWorkflowQuery();
    const editId = queryString(query.edit);
    if (editId) {
      const generatedWorkflow = workflowStore.workflows.find((workflow) => workflow.id === editId);
      if (generatedWorkflow) {
        await openWorkflowEditor(generatedWorkflow);
      }
      return;
    }
    if (queryString(query.generate) === "1") {
      await openIntentGenerateEditor();
    }
  }

  function workflowQueryWithoutGenerateKeys(options: { generate?: string } = {}) {
    const drop = new Set(["generate", "reviseSource", "feedbackSummary", "feedbackIssues", "compilationId", "agentId"]);
    const next: Record<string, string> = {};
    for (const [key, value] of Object.entries(currentWorkflowQuery())) {
      if (drop.has(key) || (key === "edit" && !workflowStore.selectedWorkflowId)) {
        continue;
      }
      const text = queryString(value);
      if (text) next[key] = text;
    }
    if (options.generate) {
      next.generate = options.generate;
    }
    return next;
  }

  function stripGenerateQuery() {
    const next = workflowQueryWithoutGenerateKeys();
    const current = currentWorkflowQuery();
    const currentKeys = Object.keys(current);
    const changed =
      currentKeys.some((key) => queryString(current[key]) !== (next[key] || "")) ||
      Object.keys(next).length !== currentKeys.filter((key) => queryString(current[key])).length;
    if (!changed) return;
    void router?.replace?.({ name: "workflow", query: next });
  }

  async function openIntentGenerateEditor() {
    captureWorkflowFocus();
    closeTrialRunDialog({ restoreFocus: false });
    closeWorkflowEditor({ restoreFocus: false });

    // Persist/send must not target the highlighted list row.
    workflowStore.selectedWorkflowId = "";
    workflowStore.activeDraft = undefined;
    workflowStore.activeCompilation = undefined;

    // Drop leftover OPEN Pinia session in memory. Closing the editor must not closeSession.
    smart.resetSessionOnly();

    editorGraph.value = createEmptyWorkflowGraphDraft();
    resetEditorHistory();
    selectedNodeId.value = "";
    selectedEdgeId.value = "";

    generateDock.leftTab.value = "generate";
    generateDock.generatePresence.value = isNarrowViewport() ? "sheet" : "open";
    generateDock.generateSheetOpen.value = true;
    generateDock.autoOpenedReason.value = "list-intent";
    workflowEditorVisible.value = true;

    await router?.replace?.({ name: "workflow", query: workflowQueryWithoutGenerateKeys({ generate: "1" }) });
    await prepareGenerateDock();
    await nextTick();
    focusGenerateTextarea();
  }

  function selectGenerateTab() {
    generateDock.selectGenerateTab();
    void prepareGenerateDock();
  }

  function selectNodesTab() {
    generateDock.selectNodesTab(hasPersistableDraft.value);
  }

  function toggleGenerateFromTopbar() {
    const opening = generateDock.leftTab.value !== "generate";
    generateDock.toggleGenerateFromTopbar(hasPersistableDraft.value);
    if (opening && generateDock.leftTab.value === "generate") {
      generateDock.autoOpenedReason.value = "editor-toggle";
      void prepareGenerateDock();
    }
  }

  function closeGenerateSheet() {
    if (generateLock.value) {
      return;
    }
    generateDock.closeGenerateSheet(hasPersistableDraft.value);
    void nextTick(() => {
      workflowEditorShellRef.value?.querySelector<HTMLButtonElement>('[data-action="open-generate-dock"]')?.focus();
    });
  }

  function captureWorkflowFocus() {
    if (document.activeElement instanceof HTMLElement) {
      workflowFocusRestoreTarget.value = document.activeElement;
    }
  }

  function restoreWorkflowFocus() {
    const target = workflowFocusRestoreTarget.value;
    workflowFocusRestoreTarget.value = undefined;
    if (!target?.isConnected) return;
    void nextTick(() => target.focus());
  }

  function focusFirstModalControl(root?: HTMLElement) {
    if (!root) return;
    const focusTarget = root.querySelector<HTMLElement>(
      'button:not(:disabled), [href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
    );
    (focusTarget || root).focus();
  }

  function trapModalFocus(event: KeyboardEvent, root?: HTMLElement) {
    if (!root || event.key !== "Tab") return;
    const focusable = Array.from(
      root.querySelectorAll<HTMLElement>(
        'button:not(:disabled), [href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((element) => element.offsetParent !== null || element === document.activeElement);
    if (!focusable.length) {
      event.preventDefault();
      root.focus();
      return;
    }
    const first = focusable[0]!;
    const last = focusable[focusable.length - 1]!;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function workflowWorkspaceLabel(workflow: WorkflowSummary) {
    if (workflow.workspaceName?.trim()) {
      return workflow.workspaceName;
    }

    const workspace = (workspaces.items || []).find((item) => item.id === workflow.workspaceId);
    if (workspace?.displayName?.trim()) {
      return workspace.displayName;
    }
    if (workspace?.name?.trim()) {
      return workspace.name;
    }
    return workflow.workspaceId;
  }

  function workflowNodeCount(workflow: WorkflowSummary) {
    if (selectedWorkflowDraft.value?.workflowId === workflow.id) {
      return selectedWorkflowDraft.value.graph.nodes.length;
    }
    return workflow.nodeCount;
  }

  function workflowEdgeCount(workflow: WorkflowSummary) {
    if (selectedWorkflowDraft.value?.workflowId === workflow.id) {
      return selectedWorkflowDraft.value.graph.edges.length;
    }
    return workflow.edgeCount;
  }

  function statusClass(status: string) {
    return status.toLowerCase().replace(/\s+/g, "-");
  }

  function statusLabel(status: WorkflowStatus) {
    return statusLabels[status] || status;
  }

  function executionStatusLabel(status?: string) {
    if (!status) return tt("workflow.notTrialRun");
    return executionStatusLabels[status] || status;
  }

  function validationLabel(workflow: WorkflowSummary) {
    if (typeof workflow.lastValidationValid !== "boolean") return tt("workflow.notValidated");
    return workflow.lastValidationValid
      ? tt("workflow.validationPassed")
      : tt("workflow.validationIssueCount", { n: workflow.lastValidationIssueCount || 0 });
  }

  function workflowLastExecution(workflow: WorkflowSummary): Execution | undefined {
    return workflowStore.executions.find((execution) => execution.workflowId === workflow.id);
  }

  function workflowLastResultLabel(workflow: WorkflowSummary) {
    return executionStatusLabel(workflow.lastTrialStatus || workflowLastExecution(workflow)?.status);
  }

  function workflowNextAction(workflow: WorkflowSummary) {
    const blocker = workflow.readiness?.blockers?.find((candidate) => candidate.action.trim());
    return localizedWorkflowReadinessAction(workflow.readiness?.stage, blocker);
  }

  function workflowReadinessFallbackAction(stage?: string) {
    switch (stage) {
      case "DraftMissing":
        return tt("workflow.actionDraftMissing");
      case "CompileRequired":
        return tt("workflow.actionCompileRequired");
      case "CompileFailed":
        return tt("workflow.actionCompileFailed");
      case "TrialRequired":
        return tt("workflow.actionTrialRequired");
      case "PublishReady":
        return tt("workflow.actionPublishReady");
      case "Published":
        return tt("workflow.actionPublished");
      case "Disabled":
        return tt("workflow.actionDisabled");
      default:
        return tt("workflow.waitReadiness");
    }
  }

  function localizedWorkflowReadinessAction(stage?: string, blocker?: WorkflowReadinessBlocker) {
    if (!blocker) {
      return workflowReadinessFallbackAction(stage);
    }

    switch (blocker.code) {
      case "draft_missing":
        return tt("workflow.actionDraftMissing");
      case "compile_required":
        return tt("workflow.actionCompileRequired");
      case "compile_failed":
        return tt("workflow.actionCompileFailed");
      case "trial_required":
        return tt("workflow.actionTrialRequired");
      case "workflow_disabled":
        return tt("workflow.actionDisabled");
      default:
        return blocker.action.trim() || workflowReadinessFallbackAction(stage);
    }
  }

  function workflowPublishBlockedTitle(stage?: string) {
    switch (stage) {
      case "DraftMissing":
        return tt("workflow.publishNeedDraft");
      case "CompileRequired":
        return tt("workflow.publishNeedCheck");
      case "CompileFailed":
        return tt("workflow.publishNeedFix");
      case "TrialRequired":
        return tt("workflow.publishNeedTrial");
      case "Disabled":
        return tt("workflow.publishNeedEnable");
      default:
        return tt("workflow.publishNotReady");
    }
  }

  /** CTA when compile failed or trial not successful — seed dock FailureFeedback (D14). */
  const canReviseFromFailure = computed(() => {
    const readiness = selectedWorkflowReadiness.value;
    if (!selectedWorkflow.value || !readiness) return false;
    if (readiness.stage === "CompileFailed") return true;
    if (readiness.stage === "TrialRequired" && readiness.trialCurrent && readiness.trialSuccessful === false)
      return true;
    if (activeCompilationIssues.value.some((issue) => issue.severity === "error")) return true;
    return false;
  });

  function reviseSourceForReadiness(stage?: string): "compile" | "trial" {
    if (stage === "TrialRequired" || stage === "PublishReady") return "trial";
    return "compile";
  }

  /** Stay in the editor: seed FailureFeedback for the next send (D14, never auto-publish). */
  function reviseDraftFromFailure() {
    const workflow = selectedWorkflow.value;
    if (!workflow) return;
    const readiness = selectedWorkflowReadiness.value;
    const source = reviseSourceForReadiness(readiness?.stage);
    const compilation = compilationForWorkflow(workflow.id);
    const issues = (compilation?.issues || activeCompilationIssues.value || []).map((issue) => ({
      code: issue.code || "UNKNOWN",
      nodeId: issue.nodeId,
      message: issue.message,
      suggestedAction: "regenerate" as const,
    }));
    const rawSummary =
      issues[0]?.message ||
      (source === "compile" ? tt("workflow.reviseCompileFailed") : tt("workflow.reviseTrialFailed"));
    const feedback: FailureFeedback = {
      source,
      workflowId: workflow.id,
      compilationId: compilation?.id,
      issues,
      rawSummary,
    };
    generateDock.pendingFailureFeedback.value = feedback;
    generateDock.failureFeedbackBannerHidden.value = false;
    generateDock.prompt.value = tt("workflow.generateRevisePrefill", {
      source:
        source === "trial" ? tt("workflow.generateReviseSourceTrial") : tt("workflow.generateReviseSourceCompile"),
      summary: rawSummary,
    });
    generateDock.autoOpenedReason.value = "revise-failure";
    generateDock.generatePresence.value = isNarrowViewport() ? "sheet" : "open";
    generateDock.selectGenerateTab();
    void prepareGenerateDock().then(() => {
      const agent = pickPreferredGenerateAgent();
      if (agent) selectGenerateAgent(agent);
      focusGenerateTextarea();
    });
  }

  function workflowReadinessStepState(ready: boolean, current: boolean): WorkflowEditorReadinessStep["state"] {
    if (ready) return "ready";
    if (current) return "current";
    return "pending";
  }

  function buildWorkflowEditorReadinessSteps(readiness?: WorkflowReadiness): WorkflowEditorReadinessStep[] {
    const stage = readiness?.stage || "DraftMissing";
    const draftReady = Boolean(readiness?.hasDraft);
    const compileReady = Boolean(readiness?.compilationCurrent && readiness.compilationValid);
    const trialReady = Boolean(readiness?.trialCurrent && readiness.trialSuccessful);
    const publishReady = Boolean(readiness?.published || readiness?.canPublish);

    return [
      {
        key: "draft",
        label: tt("workflow.summaryDraft"),
        icon: draftReady ? "fa-solid fa-check" : "fa-solid fa-clock",
        state: workflowReadinessStepState(draftReady, stage === "DraftMissing"),
      },
      {
        key: "compile",
        label: tt("workflow.readinessCompile"),
        icon: compileReady ? "fa-solid fa-check" : "fa-solid fa-clock",
        state: workflowReadinessStepState(compileReady, stage === "CompileRequired" || stage === "CompileFailed"),
      },
      {
        key: "trial",
        label: tt("workflow.readinessTrial"),
        icon: trialReady ? "fa-solid fa-check" : "fa-solid fa-clock",
        state: workflowReadinessStepState(trialReady, stage === "TrialRequired"),
      },
      {
        key: "publish",
        label: tt("workflow.readinessPublish"),
        icon: publishReady ? "fa-solid fa-check" : "fa-solid fa-clock",
        state: workflowReadinessStepState(publishReady, stage === "PublishReady" || stage === "Published"),
      },
    ];
  }

  function workflowTriggerText(workflow = selectedWorkflow.value) {
    if (!workflow) return tt("workflow.triggerNone");
    const execution = workflowLastExecution(workflow);
    if (execution?.trigger) return execution.trigger;
    return workflow.status === "Published" ? tt("workflow.triggerAgentOrManual") : tt("workflow.triggerAfterPublish");
  }

  function workflowExecutionCount(workflow: WorkflowSummary) {
    return workflowStore.executions.filter((execution) => execution.workflowId === workflow.id).length;
  }

  function workflowSuccessRateLabel(workflow: WorkflowSummary) {
    const executions = workflowStore.executions.filter((execution) => execution.workflowId === workflow.id);
    if (!executions.length) {
      return tt("workflow.successRateNone");
    }
    const successCount = executions.filter((execution) => execution.status === "Success").length;
    const rate = Math.round((successCount / executions.length) * 100);
    return tt("workflow.successRate", { rate, n: executions.length });
  }

  function workflowTableStatusLabel(workflow: WorkflowSummary) {
    if (workflow.status === "Published") return tt("workflow.tablePublished");
    if (workflow.status === "Disabled") return tt("workflow.tableDisabled");
    return tt("workflow.tableDevDraft");
  }

  function workflowTableStatusClass(workflow: WorkflowSummary) {
    if (workflow.status === "Published") return "published";
    if (workflow.status === "Disabled") return "disabled";
    return "draft";
  }

  function formatWorkflowUpdatedAt(workflow: WorkflowSummary) {
    if (!workflow.updatedAt) return "-";
    const timestamp = Date.parse(workflow.updatedAt);
    if (!Number.isFinite(timestamp)) return workflow.updatedAt;
    const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
    if (elapsedMinutes < 1) return tt("workflow.justNow");
    if (elapsedMinutes < 60) return tt("workflow.minutesAgo", { n: elapsedMinutes });
    const elapsedHours = Math.floor(elapsedMinutes / 60);
    if (elapsedHours < 24) return tt("workflow.hoursAgo", { n: elapsedHours });
    return tt("workflow.daysAgo", { n: Math.floor(elapsedHours / 24) });
  }

  function loadWorkflowRegistry(overrides: WorkflowListQuery = {}) {
    return workflowStore.loadWorkflowPage({
      query: overrides.query ?? workflowQuery.value,
      status: overrides.status,
      page: overrides.page ?? workflowStore.pagination.page,
      pageSize: overrides.pageSize ?? workflowStore.pagination.pageSize,
      ...(overrides.sortBy !== undefined ? { sortBy: overrides.sortBy, sortOrder: overrides.sortOrder } : {}),
    });
  }

  function setWorkflowSearch(value: string) {
    workflowQuery.value = value;
    void loadWorkflowRegistry({ query: value, page: 1 });
  }

  function changeWorkflowPage(pagination: { page: number; pageSize: number }) {
    void loadWorkflowRegistry(pagination);
  }

  function changeWorkflowSort(sort: { sortBy?: string; sortOrder?: "asc" | "desc" }) {
    void loadWorkflowRegistry({
      page: 1,
      pageSize: workflowStore.pagination.pageSize,
      sortBy: sort.sortBy ?? "",
      sortOrder: sort.sortOrder,
    });
  }

  function clearWorkflowSearch() {
    workflowQuery.value = "";
    void loadWorkflowRegistry({ query: "", page: 1 });
  }

  function buildWorkflowLifecycleSteps(stage?: string, executionCount = 0) {
    const currentStage = stage || "DraftMissing";
    return [
      { label: "Draft", active: currentStage !== "DraftMissing" },
      { label: "Compile", active: !["DraftMissing", "CompileRequired"].includes(currentStage) },
      { label: "Trial Run", active: ["TrialRequired", "PublishReady", "Published", "Disabled"].includes(currentStage) },
      { label: "Publish", active: ["PublishReady", "Published", "Disabled"].includes(currentStage) },
      { label: "Execute", active: executionCount > 0 || ["Published", "Disabled"].includes(currentStage) },
      { label: "Audit", active: executionCount > 0 || ["Published", "Disabled"].includes(currentStage) },
    ];
  }

  function nodeTypeLabel(type: string) {
    const labels: Record<string, string> = {
      Start: tt("workflow.nodeStart"),
      End: tt("workflow.nodeEnd"),
      Tool: tt("workflow.nodeTool"),
      HTTP: tt("workflow.nodeHttp"),
      SubWorkflow: tt("workflow.nodeSubWorkflow"),
      Transform: tt("workflow.nodeTransform"),
      Approval: tt("workflow.nodeApproval"),
      Condition: tt("workflow.nodeCondition"),
      Parallel: tt("workflow.nodeParallel"),
      ForEach: tt("workflow.nodeForEach"),
    };
    return labels[type] || type;
  }

  function selectWorkflow(workflow: WorkflowSummary) {
    workflowStore.selectedWorkflowId = workflow.id;
  }

  async function openWorkflowDetail(workflow: WorkflowSummary) {
    captureWorkflowFocus();
    invalidatePendingEditorLoad();
    selectWorkflow(workflow);
    try {
      await Promise.all([workflowStore.loadWorkflow(workflow.id), workflowStore.loadWorkflowRevisions(workflow.id)]);
    } catch {
      // Keep the detail modal usable from summary data if detail refresh fails.
    }
    workflowDetailVisible.value = true;
    void nextTick(() => focusFirstModalControl(workflowDetailModalRef.value));
  }

  function closeWorkflowDetail() {
    workflowDetailVisible.value = false;
    restoreWorkflowFocus();
  }

  function closeWorkflowMetadata() {
    workflowMetadataVisible.value = false;
    restoreWorkflowFocus();
  }

  async function openWorkflowEditor(workflow = selectedWorkflow.value) {
    if (!workflow) return;
    // Permission gate: no EDIT → no editor entry (backend still enforces 403).
    const userId = auth.user?.id || "";
    if (userId && !workspaces.can(workflow.workspaceId, userId, "EDIT")) {
      workflowActionNote.value = tt("workflow.noEditPermission");
      return;
    }
    captureWorkflowFocus();
    selectWorkflow(workflow);
    // Keep detail visible until Draft+Readiness succeed (ZKL-56 atomic handoff).
    // Do not open editor or clear detail on partial failure.
    workflowToolCatalog.value = [];
    workflowToolCatalogWorkspaceId.value = "";
    workflowToolCatalogError.value = "";
    editorDraftTargetWorkflowId = workflow.id;
    const status = await loadEditorDraft(workflow.id);
    if (status !== "loaded") {
      if (status === "failed") {
        workflowActionNote.value =
          editorDraftLoadError.value || tt("workflow.draftLoadFailedNamed", { name: workflow.name });
        // Ensure detail stays open for recovery / retry.
        workflowDetailVisible.value = true;
      }
      // stale: another open superseded this request — leave UI alone.
      return;
    }
    workflowActionNote.value = "";
    // Success: mount editor first, then close detail on next DOM flush.
    workflowEditorVisible.value = true;
    await nextTick();
    workflowDetailVisible.value = false;
    void nextTick(() => workflowEditorShellRef.value?.focus());
  }

  function syncSelection() {
    if (editorGraph.value.nodes.some((node) => node.id === selectedNodeId.value)) {
      return;
    }
    if (editorGraph.value.edges.some((edge) => edge.id === selectedEdgeId.value)) {
      selectedNodeId.value = "";
      return;
    }
    selectedEdgeId.value = "";
    selectedNodeId.value = editorGraph.value.nodes[0]?.id || "";
  }

  function setSelectedNode(nodeId: string) {
    selectedNodeId.value = nodeId;
    selectedEdgeId.value = "";
    closeContextMenu();
  }

  function setSelectedEdge(edgeId: string) {
    selectedEdgeId.value = edgeId;
    selectedNodeId.value = "";
    closeContextMenu();
  }

  function focusIssue(nodeId?: string) {
    if (!nodeId) return;
    selectedNodeId.value = nodeId;
    selectedEdgeId.value = "";
    closeContextMenu();
  }

  function focusEdgeIssue(edgeId?: string) {
    if (!edgeId) return;
    if (!editorGraph.value.edges.some((candidate) => candidate.id === edgeId)) {
      return;
    }
    selectedEdgeId.value = edgeId;
    selectedNodeId.value = "";
    closeContextMenu();
  }

  function updateSelectedNodeLabel(label: string) {
    updateEditorGraph((current) => ({
      ...current,
      nodes: current.nodes.map((node) => (node.id === selectedNodeId.value ? { ...node, label } : node)),
    }));
  }

  function updateSelectedNodeData(payload: { key: string; value: unknown }) {
    if (!selectedNodeId.value) {
      return;
    }

    updateEditorGraph((current) => ({
      ...current,
      nodes: current.nodes.map((node) =>
        node.id === selectedNodeId.value
          ? {
              ...node,
              data:
                payload.key === "__merge" &&
                payload.value &&
                typeof payload.value === "object" &&
                !Array.isArray(payload.value)
                  ? { ...node.data, ...(payload.value as Record<string, unknown>) }
                  : { ...node.data, [payload.key]: payload.value },
            }
          : node,
      ),
    }));
  }

  function updateSelectedEdgeData(payload: { key: string; value: unknown }) {
    if (!selectedEdgeId.value) {
      return;
    }

    updateEditorGraph((current) => ({
      ...current,
      edges: current.edges.map((edge) => {
        if (edge.id !== selectedEdgeId.value) {
          return edge;
        }
        const data = { ...edge.data };
        if (payload.key === "branch" && payload.value === "") {
          delete data.branch;
        } else {
          data[payload.key] = payload.value;
        }
        return { ...edge, data };
      }),
    }));
  }

  function updateNodePosition(payload: { nodeId: string; position: { x: number; y: number } }) {
    updateEditorGraph((current) => ({
      ...current,
      nodes: current.nodes.map((node) =>
        node.id === payload.nodeId
          ? { ...node, position: { x: Math.round(payload.position.x), y: Math.round(payload.position.y) } }
          : node,
      ),
    }));
  }

  function updateViewport(viewport: { x: number; y: number; zoom: number }) {
    updateEditorGraph((current) => ({
      ...current,
      viewport: {
        x: Math.round(viewport.x),
        y: Math.round(viewport.y),
        zoom: Number(viewport.zoom.toFixed(3)),
      },
    }));
  }

  function connectNodes(payload: {
    sourceNodeId: string;
    sourcePort: string;
    targetNodeId: string;
    targetPort: string;
  }) {
    const edgeId = `edge-${payload.sourceNodeId}-${payload.targetNodeId}-${payload.sourcePort}-${payload.targetPort}`;
    const exists = editorGraph.value.edges.some((edge) => edge.id === edgeId);
    if (exists) return;

    updateEditorGraph((current) => ({
      ...current,
      edges: [
        ...current.edges,
        {
          id: edgeId,
          sourceNodeId: payload.sourceNodeId,
          sourcePort: payload.sourcePort,
          targetNodeId: payload.targetNodeId,
          targetPort: payload.targetPort,
          data: {},
          ui: {},
        },
      ],
    }));
  }

  function deleteSelectedNode() {
    const node = selectedGraphNode.value;
    if (!node) {
      return;
    }

    if (isProtectedTerminalNode(node)) {
      closeContextMenu();
      workflowActionNote.value = tt("workflow.cannotDeleteTerminalNamed", { label: node.label });
      return;
    }

    const remainingNodes = editorGraph.value.nodes.filter((candidate) => candidate.id !== node.id);
    const removedEdges = editorGraph.value.edges.filter(
      (edge) => edge.sourceNodeId === node.id || edge.targetNodeId === node.id,
    );

    replaceEditorGraph({
      ...editorGraph.value,
      nodes: remainingNodes,
      edges: editorGraph.value.edges.filter((edge) => edge.sourceNodeId !== node.id && edge.targetNodeId !== node.id),
    });

    selectedEdgeId.value = "";
    selectedNodeId.value = remainingNodes[0]?.id || "";
    closeContextMenu();

    workflowActionNote.value =
      removedEdges.length > 0
        ? tt("workflow.deletedNodeWithEdges", { label: node.label, n: removedEdges.length })
        : tt("workflow.deletedNode", { label: node.label });
  }

  function deleteSelectedEdge() {
    const edge = selectedGraphEdge.value;
    if (!edge) {
      return;
    }

    replaceEditorGraph({
      ...editorGraph.value,
      edges: editorGraph.value.edges.filter((candidate) => candidate.id !== edge.id),
    });

    selectedEdgeId.value = "";
    closeContextMenu();
    workflowActionNote.value = tt("workflow.deletedEdge", { id: edge.id });
  }

  function closeContextMenu() {
    contextMenu.value = undefined;
  }

  function isProtectedTerminalNode(node?: WorkflowGraphDraft["nodes"][number]) {
    return node?.id === "start" || node?.id === "end";
  }

  function isContextTargetDeleteDisabled() {
    return contextMenu.value?.targetType === "node" && isProtectedTerminalNode(selectedGraphNode.value);
  }

  function normalizeContextMenuPosition(position: { x: number; y: number }) {
    if (typeof window === "undefined") {
      return position;
    }

    const menuWidth = 168;
    const menuHeight = 52;
    const padding = 12;

    return {
      x: Math.max(padding, Math.min(position.x, window.innerWidth - menuWidth - padding)),
      y: Math.max(padding, Math.min(position.y, window.innerHeight - menuHeight - padding)),
    };
  }

  function openNodeContextMenu(payload: { nodeId: string; position: { x: number; y: number } }) {
    selectedNodeId.value = payload.nodeId;
    selectedEdgeId.value = "";
    contextMenu.value = {
      targetType: "node",
      targetId: payload.nodeId,
      position: normalizeContextMenuPosition(payload.position),
    };
  }

  function openEdgeContextMenu(payload: { edgeId: string; position: { x: number; y: number } }) {
    selectedEdgeId.value = payload.edgeId;
    selectedNodeId.value = "";
    contextMenu.value = {
      targetType: "edge",
      targetId: payload.edgeId,
      position: normalizeContextMenuPosition(payload.position),
    };
  }

  function deleteContextTarget() {
    if (!contextMenu.value) {
      return;
    }

    if (contextMenu.value.targetType === "node") {
      deleteSelectedNode();
      return;
    }

    deleteSelectedEdge();
  }

  function portsForNodeType(type: WorkflowGraphNodeType) {
    return defaultPortsForNodeType(type);
  }

  function addNodeToDraft(nodeType: WorkflowGraphNodeType) {
    const sequence = editorGraph.value.nodes.filter((node) => node.type === nodeType).length + 1;
    const nodeId = `${nodeType.toLowerCase()}-${sequence}`;
    const node: WorkflowGraphNode = {
      id: nodeId,
      type: nodeType,
      label: nodeType === "End" ? "End" : `${nodeType} ${sequence}`,
      position: {
        x: 180 + editorGraph.value.nodes.length * 120,
        y: 140 + (editorGraph.value.nodes.length % 3) * 92,
      },
      ports: portsForNodeType(nodeType),
      data: {},
      ui: {},
    };
    replaceEditorGraph({ ...editorGraph.value, nodes: [...editorGraph.value.nodes, node] });
    selectedNodeId.value = node.id;
    selectedEdgeId.value = "";
    workflowActionNote.value = tt("workflow.addedNode", { label: node.label });
  }

  function duplicateSelectedNode() {
    const node = selectedGraphNode.value;
    if (!node) {
      return;
    }

    const copyIndex = editorGraph.value.nodes.filter((candidate) => candidate.type === node.type).length + 1;
    const duplicatedNode: WorkflowGraphNode = {
      ...cloneGraph({
        ...editorGraph.value,
        nodes: [node],
        edges: [],
        viewport: editorGraph.value.viewport,
        ui: editorGraph.value.ui,
      }).nodes[0],
      id: `${node.type.toLowerCase()}-${copyIndex}`,
      label: `${node.label} Copy`,
      position: {
        x: node.position.x + 160,
        y: node.position.y + 48,
      },
    };

    replaceEditorGraph({
      ...editorGraph.value,
      nodes: [...editorGraph.value.nodes, duplicatedNode],
    });
    selectedNodeId.value = duplicatedNode.id;
    selectedEdgeId.value = "";
    workflowActionNote.value = tt("workflow.duplicatedNode", { label: node.label });
  }

  function applyAutoLayout() {
    replaceEditorGraph(autoLayoutWorkflowGraph(editorGraph.value));
    workflowActionNote.value = tt("workflow.formattedLayout");
  }

  function undoEditorChange() {
    const previous = editorHistory.value.past.at(-1);
    if (!previous) {
      return;
    }

    editorHistory.value = {
      past: editorHistory.value.past.slice(0, -1),
      future: [cloneGraph(editorGraph.value), ...editorHistory.value.future],
    };
    editorGraph.value = cloneGraph(previous);
    syncSelection();
  }

  function redoEditorChange() {
    const [next, ...remainingFuture] = editorHistory.value.future;
    if (!next) {
      return;
    }

    editorHistory.value = {
      past: [...editorHistory.value.past, cloneGraph(editorGraph.value)],
      future: remainingFuture,
    };
    editorGraph.value = cloneGraph(next);
    syncSelection();
  }

  async function openCreateWorkflow() {
    captureWorkflowFocus();
    closeTrialRunDialog({ restoreFocus: false });
    closeWorkflowEditor({ restoreFocus: false });
    workflowMetadataMode.value = "create";
    workflowStore.selectedWorkflowId = "";
    workflowStore.activeDraft = undefined;
    workflowStore.activeCompilation = undefined;
    await ensureWorkspacesLoaded();
    workflowDraft.value = newWorkflowDraft();
    workflowMetadataTouched.value = false;
    editorGraph.value = createDefaultWorkflowGraphDraft();
    resetEditorHistory();
    syncSelection();
    workflowActionNote.value = "";
    workflowMetadataVisible.value = true;
    void nextTick(() => focusFirstModalControl(workflowMetadataModalRef.value));
  }

  async function openEditWorkflow(workflow = selectedWorkflow.value) {
    captureWorkflowFocus();
    invalidatePendingEditorLoad();
    if (!workflow) {
      await openCreateWorkflow();
      return;
    }
    const detail = workflowStore.workflowDetails[workflow.id] ?? (await workflowStore.loadWorkflow(workflow.id));
    await ensureWorkspacesLoaded();
    workflowMetadataMode.value = "edit";
    workflowDraft.value = {
      id: detail.id,
      name: detail.name,
      slug: detail.slug,
      workspaceId: detail.workspaceId,
      description: detail.description,
      status: detail.status,
    };
    workflowMetadataTouched.value = false;
    workflowActionNote.value = "";
    workflowMetadataVisible.value = true;
    void nextTick(() => focusFirstModalControl(workflowMetadataModalRef.value));
  }

  function buildWorkflowFromDraft(): Workflow & { graph: WorkflowGraphDraft; schemaVersion: string } {
    const source = workflowMetadataMode.value === "edit" ? selectedWorkflowDetail.value : undefined;
    const workspaceId = workflowDraft.value.workspaceId.trim() || "default";
    const isCreateMode = workflowMetadataMode.value === "create";
    const now = new Date().toISOString();
    return {
      id: workflowMetadataMode.value === "edit" ? workflowDraft.value.id : "",
      workspaceId,
      currentDraftId: source?.currentDraftId || "",
      activeRevisionId: source?.activeRevisionId,
      latestCompilationId: source?.latestCompilationId,
      name: workflowDraft.value.name.trim(),
      slug: workflowDraft.value.slug.trim(),
      description: workflowDraft.value.description.trim(),
      status: workflowDraft.value.status,
      nodeCount: isCreateMode ? 0 : source?.nodeCount,
      edgeCount: isCreateMode ? 0 : source?.edgeCount,
      lastValidationValid: source?.lastValidationResult?.valid,
      lastValidationIssueCount: source?.lastValidationResult?.issues.length,
      lastTrialExecutionId: source?.lastTrialExecutionId,
      lastTrialStatus: source?.lastTrialStatus,
      readiness: source?.readiness,
      createdBy: source?.createdBy || "",
      updatedBy: source?.updatedBy || "",
      createdAt: source?.createdAt || now,
      updatedAt: source?.updatedAt || now,
      lockVersion: source?.lockVersion || 1,
      graph: cloneGraph(editorGraph.value),
      schemaVersion: editorGraph.value.schemaVersion,
    };
  }

  async function saveWorkflowMetadata() {
    workflowMetadataTouched.value = true;
    if (!canSaveWorkflowMetadata.value) {
      void nextTick(() =>
        workflowMetadataModalRef.value?.querySelector<HTMLInputElement>("input[aria-invalid='true']")?.focus(),
      );
      return;
    }
    const payload = buildWorkflowFromDraft();
    const createdWorkflow = workflowMetadataMode.value === "create";
    if (createdWorkflow) {
      const created = await workflowStore.createWorkflow(payload);
      const { draft, latestCompilation } = await workflowStore.loadWorkflowDraft(created.id);
      workflowStore.activeDraft = draft;
      workflowStore.activeCompilation = latestCompilation;
      editorGraph.value = cloneGraph(draft.graph);
      resetEditorHistory();
      syncSelection();
      workflowStore.selectedWorkflowId = created.id;
      workflowActionNote.value = tt("workflow.createdWithCanvas", { name: created.name });
    } else {
      const updated = await workflowStore.updateWorkflow(workflowDraft.value.id, payload);
      workflowStore.selectedWorkflowId = updated.id;
      workflowActionNote.value = tt("workflow.savedAsStatus", {
        name: updated.name,
        status: statusLabel(updated.status),
      });
    }
    await loadWorkflowRegistry({ page: createdWorkflow ? 1 : workflowStore.pagination.page });
    closeWorkflowMetadata();
    workflowDetailVisible.value = true;
  }

  async function saveEditorDraft() {
    if (workflowEditorBusy.value) return;
    pendingEditorAction.value = "save";
    try {
      if (!selectedWorkflow.value) return;
      const saved = await persistEditorDraft();
      if (!saved) return;
      workflowActionNote.value = tt("workflow.draftSavedCheckIssues", { name: selectedWorkflow.value.name });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.saveDraftFailed"));
    } finally {
      pendingEditorAction.value = undefined;
    }
  }

  async function persistEditorDraft() {
    const workflowId = workflowStore.selectedWorkflowId;
    if (!workflowId || !workflowStore.activeDraft || workflowStore.activeDraft.workflowId !== workflowId) {
      return undefined;
    }
    const current = workflowStore.activeDraft;
    const payload: WorkflowDraftRecord = {
      ...current,
      graph: cloneGraph(editorGraph.value),
    };
    try {
      const saved = await workflowStore.saveWorkflowDraft(workflowId, payload);
      editorGraph.value = cloneGraph(saved.graph);
      resetEditorHistory();
      return saved;
    } catch (error) {
      if (error instanceof WorkflowDraftConflictError) {
        editorGraph.value = cloneGraph(error.latest.graph);
        resetEditorHistory();
        syncSelection();
      }
      throw error;
    }
  }

  function workflowMenuActions(workflow: WorkflowSummary): ManagementRowAction[] {
    const validating = pendingWorkflowValidationId.value === workflow.id;
    return [
      { key: "detail", label: tt("common.viewDetails"), icon: "fa-solid fa-eye", tone: "primary" },
      { key: "edit", label: tt("workflow.editGraph"), icon: "fa-solid fa-pen-ruler" },
      {
        key: "validate",
        label: tt("workflow.validateWorkflow"),
        icon: "fa-solid fa-list-check",
        loading: validating,
        disabled: validating,
        disabledReason: validating ? tt("workflow.validatingShort") : undefined,
      },
      {
        key: "trial-run",
        label: tt("workflow.readinessTrial"),
        icon: "fa-solid fa-vial",
        loading: pendingTrialRun.value,
        disabled: pendingTrialRun.value,
        disabledReason: pendingTrialRun.value ? tt("workflow.trialSubmitting") : undefined,
      },
      ...(workflow.activeRevisionId
        ? [
            {
              key: "production-run",
              label: tt("workflow.productionRun"),
              icon: "fa-solid fa-play",
              loading: pendingProductionRun.value,
              disabled: pendingProductionRun.value,
              disabledReason: pendingProductionRun.value ? tt("workflow.productionSubmitting") : undefined,
            } satisfies ManagementRowAction,
          ]
        : []),
      { key: "delete", label: tt("workflow.deleteWorkflow"), icon: "fa-solid fa-trash", tone: "danger" },
    ];
  }

  function handleWorkflowRowAction(actionKey: string, workflow: WorkflowSummary) {
    if (actionKey === "edit") {
      void openWorkflowEditor(workflow);
      return;
    }
    if (actionKey === "detail") {
      void openWorkflowDetail(workflow);
      return;
    }
    if (actionKey === "validate") {
      void validateWorkflow(workflow);
      return;
    }
    if (actionKey === "trial-run") {
      void openTrialRunDialog(workflow);
      return;
    }
    if (actionKey === "production-run") {
      void runWorkflowProduction(workflow);
      return;
    }
    if (actionKey === "delete") void deleteWorkflow(workflow);
  }

  async function deleteWorkflow(workflow: WorkflowSummary) {
    const wasSelected = selectedWorkflow.value?.id === workflow.id;
    await workflowStore.deleteWorkflow(workflow.id);
    const page =
      workflowStore.pageItems.length === 0 && workflowStore.pagination.page > 1
        ? workflowStore.pagination.page - 1
        : workflowStore.pagination.page;
    await loadWorkflowRegistry({ page });
    workflowActionNote.value = tt("workflow.deletedNamed", { name: workflow.name });
    if (wasSelected) {
      workflowDetailVisible.value = false;
      closeWorkflowEditor({ restoreFocus: false });
      editorGraph.value = createDefaultWorkflowGraphDraft();
      resetEditorHistory();
      syncSelection();
    }
  }

  async function validateWorkflow(workflow: WorkflowSummary) {
    if (pendingWorkflowValidationId.value) return;
    pendingWorkflowValidationId.value = workflow.id;
    try {
      const validation = await workflowStore.validateWorkflow(workflow.id);
      const firstIssue = validation.issues[0];
      workflowActionNote.value = validation.valid
        ? tt("workflow.validationPassedNamed", { name: workflow.name })
        : tt("workflow.validationFailedNamed", {
            name: workflow.name,
            n: validation.issues.length,
            message: firstIssue?.message || tt("workflow.validationOpenDetail"),
          });
    } finally {
      pendingWorkflowValidationId.value = "";
    }
  }

  async function validateEditorWorkflow() {
    if (workflowEditorBusy.value) return;
    pendingEditorAction.value = "validate";
    try {
      if (!selectedWorkflow.value) return;
      const saved = await persistEditorDraft();
      if (!saved) return;
      const validation = await workflowStore.validateWorkflow(selectedWorkflow.value.id);
      if (!validation.valid) {
        workflowActionNote.value = tt("workflow.draftSavedWithIssues", {
          name: selectedWorkflow.value.name,
          n: validation.issues.length,
          message: validation.issues[0]?.message || tt("workflow.checkIssuesPanel"),
        });
        focusFirstCompilationIssue();
        return;
      }
      workflowActionNote.value = tt("workflow.validationPassedNamed", { name: selectedWorkflow.value.name });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.validateFailed"));
    } finally {
      pendingEditorAction.value = undefined;
    }
  }

  async function runWorkflowTrial(
    workflowId: string,
    input: Record<string, unknown>,
    outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope,
  ) {
    const execution = await workflowStore.trialRunWorkflow(workflowId, input, outboundCredentials);
    activeTraceExecutionId.value = execution.id;
    const workflow = workflowStore.workflows.find((item) => item.id === workflowId) || selectedWorkflow.value;
    workflowActionNote.value = tt("workflow.trialRunCreated", {
      name: workflow?.name || tt("workflow.currentWorkflow"),
      id: execution.id,
      status: execution.status,
    });
    return execution;
  }

  async function runWorkflowProduction(workflow: WorkflowSummary) {
    if (pendingProductionRun.value) return;
    if (!workflow.activeRevisionId) {
      workflowActionNote.value = tt("workflow.noActiveRevision", { name: workflow.name });
      return;
    }
    pendingProductionRun.value = true;
    captureWorkflowFocus();
    try {
      const execution = await workflowStore.executeProductionWorkflow(workflow.id, {});
      activeTraceExecutionId.value = execution.id;
      workflowActionNote.value = tt("workflow.productionRunSubmitted", {
        name: workflow.name,
        id: execution.id,
        status: execution.status,
        trace: execution.traceId,
      });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.productionRunFailed"));
    } finally {
      pendingProductionRun.value = false;
    }
  }

  async function openTrialRunDialog(workflow = selectedWorkflow.value, options: { persistEditorDraft?: boolean } = {}) {
    if (!workflow) return;
    captureWorkflowFocus();
    selectWorkflow(workflow);

    if (options.persistEditorDraft) {
      const saved = await persistEditorDraft();
      if (!saved) return;
      const validation = await workflowStore.validateWorkflow(workflow.id);
      if (!validation.valid) {
        workflowActionNote.value = buildCompilationBlockedMessage(workflow, "trial");
        focusFirstCompilationIssue();
        return;
      }
    } else if (workflowStore.activeDraft?.workflowId !== workflow.id) {
      try {
        const { draft, latestCompilation } = await workflowStore.loadWorkflowDraft(workflow.id);
        workflowStore.activeDraft = draft;
        workflowStore.activeCompilation = latestCompilation;
      } catch {
        workflowActionNote.value = tt("workflow.draftLoadFailedNamed", { name: workflow.name });
        return;
      }
    }

    const readiness =
      workflowStore.readinessByWorkflowId[workflow.id] || (await workflowStore.loadWorkflowReadiness(workflow.id));
    if (!readiness.canTrial) {
      workflowActionNote.value = workflowNextAction({ ...workflow, readiness });
      focusFirstCompilationIssue();
      return;
    }

    trialRunTargetWorkflowId.value = workflow.id;
    trialRunTargetWorkflowName.value = workflow.name;
    trialRunVisible.value = true;
  }

  async function trialRunEditorWorkflow() {
    if (workflowEditorBusy.value) return;
    pendingEditorAction.value = "trial-run";
    try {
      if (!selectedWorkflow.value) return;
      await openTrialRunDialog(selectedWorkflow.value, { persistEditorDraft: true });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.trialPrepareFailed"));
    } finally {
      pendingEditorAction.value = undefined;
    }
  }

  function appendGenerateBindFailedNote() {
    workflowActionNote.value = [workflowActionNote.value, tt("workflow.generateBindFailed")].filter(Boolean).join(" ");
  }

  async function bindPublishedWorkflowToSessionAgent(workflow: Workflow) {
    await bindGeneratedDraftToSessionAgent(workflow, { onFailure: appendGenerateBindFailedNote });
  }

  async function publishWorkflow(workflow = selectedWorkflow.value) {
    if (!workflow) return;
    if (!workflow.readiness?.canPublish) {
      workflowActionNote.value = workflowNextAction(workflow);
      return;
    }
    const published = await workflowStore.publishWorkflow(workflow.id);
    // Write success first: bind/GET failure must not become publishFailed.
    workflowActionNote.value = tt("workflow.publishedOnline", { name: published.workflow.name });
    await bindPublishedWorkflowToSessionAgent(published.workflow);
  }

  async function activateRevision(revisionId: string) {
    if (!selectedWorkflow.value) return;
    pendingRevisionActionId.value = revisionId;
    try {
      const result = await workflowStore.activateWorkflowRevision(selectedWorkflow.value.id, revisionId);
      workflowActionNote.value = tt("workflow.activatedRevision", {
        name: result.workflow.name,
        revisionId,
      });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.activateRevisionFailed"));
    } finally {
      pendingRevisionActionId.value = "";
    }
  }

  async function rollbackRevision(revisionId: string) {
    if (!selectedWorkflow.value) return;
    pendingRevisionActionId.value = revisionId;
    try {
      const result = await workflowStore.rollbackWorkflowRevision(selectedWorkflow.value.id, revisionId);
      workflowActionNote.value = tt("workflow.rolledBackRevision", {
        name: result.workflow.name,
        revisionId,
      });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.rollbackRevisionFailed"));
    } finally {
      pendingRevisionActionId.value = "";
    }
  }

  async function compareRevision(leftRevisionId: string, rightRevisionId: string) {
    if (!selectedWorkflow.value) return;
    pendingRevisionCompare.value = true;
    try {
      await workflowStore.loadWorkflowRevisionDiff(selectedWorkflow.value.id, leftRevisionId, rightRevisionId);
      workflowActionNote.value = tt("workflow.comparedRevisions", {
        name: selectedWorkflow.value.name,
        left: leftRevisionId,
        right: rightRevisionId,
      });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.diffLoadFailed"));
    } finally {
      pendingRevisionCompare.value = false;
    }
  }

  async function disableWorkflowRuns() {
    if (!selectedWorkflow.value) return;
    pendingWorkflowDisable.value = true;
    try {
      const workflow = await workflowStore.disableWorkflow(selectedWorkflow.value.id);
      workflowActionNote.value = tt("workflow.disabledNewRuns", { name: workflow.name });
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.disableFailed"));
    } finally {
      pendingWorkflowDisable.value = false;
    }
  }

  async function publishEditorWorkflow() {
    if (workflowEditorBusy.value) return;
    pendingEditorAction.value = "publish";
    try {
      if (!selectedWorkflow.value) return;
      if (isEditorDraftDirty()) {
        const saved = await persistEditorDraft();
        if (!saved) return;
      }

      if (!selectedWorkflowCanPublish.value) {
        workflowActionNote.value = workflowNextAction(selectedWorkflow.value);
        return;
      }

      await publishWorkflow(selectedWorkflow.value);
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.publishFailed"));
    } finally {
      pendingEditorAction.value = undefined;
    }
  }

  function openForcePublishDialog() {
    if (workflowEditorBusy.value) return;
    if (!selectedWorkflow.value) return;
    if (!canForcePublishWorkflow.value) {
      workflowActionNote.value = tt("workflow.forcePublishAdminOnly");
      return;
    }
    if (!selectedWorkflowCanForcePublish.value) {
      workflowActionNote.value = tt("workflow.forcePublishNeedValidCompile");
      return;
    }
    forcePublishReasonDraft.value = "local-dev skip trial";
    forcePublishDialogVisible.value = true;
  }

  function closeForcePublishDialog() {
    if (pendingEditorAction.value === "force-publish") return;
    forcePublishDialogVisible.value = false;
  }

  /** Open styled force-publish dialog (replaces native window.prompt). */
  function forcePublishEditorWorkflow() {
    openForcePublishDialog();
  }

  async function confirmForcePublishEditorWorkflow(reason: string) {
    if (workflowEditorBusy.value) return;
    if (!selectedWorkflow.value) return;
    const trimmed = reason.trim();
    if (trimmed.length < 8) {
      workflowActionNote.value = tt("workflow.forcePublishCancelled");
      return;
    }
    pendingEditorAction.value = "force-publish";
    try {
      if (isEditorDraftDirty()) {
        const saved = await persistEditorDraft();
        if (!saved) return;
        forcePublishDialogVisible.value = false;
        // Dirty save invalidates compilation — force user to re-check before force publish.
        workflowActionNote.value = tt("workflow.forcePublishNeedCheckAfterSave");
        return;
      }
      const published = await workflowStore.forcePublishWorkflow(selectedWorkflow.value.id, trimmed);
      forcePublishDialogVisible.value = false;
      workflowActionNote.value = tt("workflow.forcePublished", { name: published.workflow.name });
      await bindPublishedWorkflowToSessionAgent(published.workflow);
    } catch (error) {
      workflowActionNote.value = actionErrorMessage(error, tt("workflow.forcePublishFailed"));
    } finally {
      pendingEditorAction.value = undefined;
    }
  }

  async function submitTrialRun(payload: {
    input: Record<string, unknown>;
    outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope;
  }) {
    const workflowId = trialRunTargetWorkflowId.value;
    if (!workflowId || pendingTrialRun.value) return;
    pendingTrialRun.value = true;
    try {
      await runWorkflowTrial(workflowId, payload.input, payload.outboundCredentials);
      closeTrialRunDialog();
    } catch (error) {
      const workflow = workflowStore.workflows.find((item) => item.id === workflowId) || selectedWorkflow.value;
      const latestCompilation = extractCompilationFromError(error);
      if (workflow && latestCompilation && isStaleCompilationForWorkflow(workflowId, latestCompilation)) {
        closeTrialRunDialog();
        workflowActionNote.value = tt("workflow.compilationStale", { name: workflow.name });
        return;
      }
      if (workflow && latestCompilation?.status === "Invalid") {
        closeTrialRunDialog();
        workflowActionNote.value = buildCompilationBlockedMessage(workflow, "trial");
        focusCompilationIssue(latestCompilation.issues[0]);
        return;
      }
      workflowActionNote.value = tt("workflow.trialRunFailed", {
        name: workflow?.name || tt("workflow.currentWorkflow"),
      });
    } finally {
      pendingTrialRun.value = false;
    }
  }

  function closeTrialRunDialog(options: { restoreFocus?: boolean } = {}) {
    trialRunVisible.value = false;
    trialRunTargetWorkflowId.value = "";
    trialRunTargetWorkflowName.value = "";
    if (options.restoreFocus !== false) {
      restoreWorkflowFocus();
    }
  }

  function compilationForWorkflow(workflowId: string) {
    const draft = workflowStore.activeDraft;
    const compilation = workflowStore.activeCompilation;
    if (draft?.workflowId !== workflowId) return undefined;
    if (compilation?.workflowId !== workflowId) return undefined;
    if (!isCompilationCurrentForDraft(compilation, draft)) return undefined;
    return compilation;
  }

  function focusFirstCompilationIssue() {
    focusCompilationIssue(activeCompilationIssues.value[0]);
  }

  function buildCompilationBlockedMessage(workflow: WorkflowSummary, action: "trial" | "publish") {
    const actionLabel = action === "trial" ? tt("workflow.trialRun") : tt("workflow.publish");
    if (hasStaleCompilation(workflow.id)) {
      return tt("workflow.compilationStaleAction", { name: workflow.name, action: actionLabel });
    }
    const compilation = compilationForWorkflow(workflow.id);
    if (!compilation) {
      return tt("workflow.compilationMissing", { name: workflow.name, action: actionLabel });
    }
    if (compilation.issues.length > 0) {
      return tt("workflow.compilationHasIssues", {
        name: workflow.name,
        n: compilation.issues.length,
        action: actionLabel,
      });
    }
    return tt("workflow.compilationUnusable", { name: workflow.name, action: actionLabel });
  }

  function focusCompilationIssue(issue?: WorkflowCompilationIssue) {
    if (!issue) return;
    if (issue.edgeId) {
      focusEdgeIssue(issue.edgeId);
      return;
    }
    if (issue.nodeId) {
      focusIssue(issue.nodeId);
    }
  }

  function selectTraceNode(nodeId: string) {
    if (!nodeId) return;
    selectedNodeId.value = nodeId;
    selectedEdgeId.value = "";
    closeContextMenu();
  }

  function isEditingTextInput(target: EventTarget | null) {
    if (!(target instanceof HTMLElement)) {
      return false;
    }
    const tagName = target.tagName;
    return target.isContentEditable || tagName === "INPUT" || tagName === "TEXTAREA" || tagName === "SELECT";
  }

  function shouldHandleEditorShortcut(event: KeyboardEvent) {
    if (
      !workflowEditorVisible.value ||
      workflowMetadataVisible.value ||
      trialRunVisible.value ||
      workflowEditorBusy.value
    ) {
      return false;
    }
    return !isEditingTextInput(event.target);
  }

  function handleEditorKeydown(event: KeyboardEvent) {
    if (!shouldHandleEditorShortcut(event)) {
      return;
    }

    const key = event.key.toLowerCase();
    const usesMeta = event.ctrlKey || event.metaKey;

    if (usesMeta && key === "z" && !event.shiftKey) {
      event.preventDefault();
      undoEditorChange();
      return;
    }

    if (usesMeta && ((key === "z" && event.shiftKey) || key === "y")) {
      event.preventDefault();
      redoEditorChange();
      return;
    }

    if (event.key === "Delete" || event.key === "Backspace") {
      if (selectedGraphEdge.value) {
        event.preventDefault();
        deleteSelectedEdge();
        return;
      }

      if (selectedGraphNode.value) {
        event.preventDefault();
        deleteSelectedNode();
      }
    }
  }

  function extractCompilationFromError(error: unknown): WorkflowCompilation | undefined {
    if (!isAxiosError(error)) {
      return undefined;
    }
    const payload = error.response?.data as { latestCompilation?: WorkflowCompilation } | undefined;
    const compilation = payload?.latestCompilation;
    if (!compilation) {
      return undefined;
    }
    return { ...compilation, status: normalizeCompilationStatus(compilation.status) };
  }

  function normalizeCompilationStatus(value: string | undefined): WorkflowCompilation["status"] {
    const upper = String(value || "")
      .trim()
      .toUpperCase();
    if (upper === "VALID") return "Valid";
    if (upper === "INVALID") return "Invalid";
    return "Pending";
  }

  function hasStaleCompilation(workflowId: string) {
    const draft = workflowStore.activeDraft;
    const compilation = workflowStore.activeCompilation;
    if (draft?.workflowId !== workflowId) {
      return false;
    }
    if (compilation?.workflowId !== workflowId) {
      return false;
    }
    return !isCompilationCurrentForDraft(compilation, draft);
  }

  function isStaleCompilationForWorkflow(workflowId: string, compilation: WorkflowCompilation) {
    const draft = workflowStore.activeDraft;
    if (draft?.workflowId !== workflowId) {
      return false;
    }
    return !isCompilationCurrentForDraft(compilation, draft);
  }

  function isCompilationCurrentForDraft(compilation: WorkflowCompilation, draft: WorkflowDraftRecord) {
    return (
      compilation.workflowId === draft.workflowId &&
      compilation.draftVersion === draft.draftVersion &&
      Date.parse(compilation.compiledAt) >= Date.parse(draft.updatedAt)
    );
  }

  function extractTrialRunInputSchema(graph: WorkflowGraphDraft): WorkflowTrialRunInputField[] {
    const startNode = graph.nodes.find((node) => node.type === "Start");
    const inputSchema = startNode?.data.inputSchema;

    if (Array.isArray(inputSchema)) {
      return inputSchema
        .flatMap((field) => {
          if (!field || typeof field !== "object") return [];
          const schemaField = field as Record<string, unknown>;
          const key = typeof schemaField.key === "string" ? schemaField.key : "";
          if (!key) return [];
          return [
            {
              key,
              type: typeof schemaField.type === "string" ? schemaField.type : "string",
              required: Boolean(schemaField.required),
              description: typeof schemaField.description === "string" ? schemaField.description : key,
            },
          ];
        })
        .sort((left, right) => left.key.localeCompare(right.key));
    }

    if (!inputSchema || typeof inputSchema !== "object") {
      return [];
    }

    const record = inputSchema as { properties?: unknown; required?: unknown } & Record<string, unknown>;
    const properties =
      record.properties && typeof record.properties === "object"
        ? (record.properties as Record<string, unknown>)
        : Object.fromEntries(Object.entries(record).filter(([key]) => key !== "required"));

    const required = new Set(
      Array.isArray(record.required)
        ? record.required.filter((value): value is string => typeof value === "string")
        : [],
    );

    return Object.entries(properties)
      .flatMap(([key, definition]) => {
        if (typeof definition === "string") {
          return [{ key, type: definition, required: required.has(key), description: key }];
        }
        if (!definition || typeof definition !== "object") {
          return [];
        }
        const schemaField = definition as Record<string, unknown>;
        return [
          {
            key,
            type: typeof schemaField.type === "string" ? schemaField.type : "string",
            required: required.has(key) || Boolean(schemaField.required),
            description: typeof schemaField.description === "string" ? schemaField.description : key,
          },
        ];
      })
      .sort((left, right) => left.key.localeCompare(right.key));
  }

  function actionErrorMessage(error: unknown, fallback: string) {
    if (isAxiosError(error)) {
      const message = (error.response?.data as { error?: unknown } | undefined)?.error;
      if (typeof message === "string" && message.trim()) {
        return message;
      }
    }
    if (error instanceof Error && error.message.trim()) {
      return error.message;
    }
    return fallback;
  }

  return {
    workflowStore,
    toolsStore,
    workspaces,
    auth,
    router,
    hasWorkspaceContext,
    editorDraftLoadRequestId,
    editorDraftTargetWorkflowId,
    workflowToolCatalog,
    workflowToolCatalogWorkspaceId,
    workflowToolCatalogError,
    workflowQuery,
    workflowDetailVisible,
    workflowEditorVisible,
    workflowMetadataVisible,
    workflowMetadataMode,
    workflowDraft,
    workflowMetadataTouched,
    workflowActionNote,
    pendingEditorAction,
    pendingWorkflowValidationId,
    pendingTrialRun,
    pendingProductionRun,
    pendingRevisionActionId,
    pendingRevisionCompare,
    pendingWorkflowDisable,
    selectedNodeId,
    selectedEdgeId,
    contextMenu,
    editorGraph,
    editorHistory,
    editorDraftLoadState,
    editorDraftLoadError,
    trialRunVisible,
    trialRunTargetWorkflowId,
    trialRunTargetWorkflowName,
    forcePublishDialogVisible,
    forcePublishReasonDraft,
    activeTraceExecutionId,
    workflowDetailModalRef,
    workflowMetadataModalRef,
    workflowEditorShellRef,
    workflowFocusRestoreTarget,
    workflowStatusOptions,
    workflowEditorHelpText,
    workspaceOptions,
    workflowNameError,
    workflowWorkspaceError,
    canSaveWorkflowMetadata,
    statusLabels,
    executionStatusLabels,
    workflowColumns,
    workflowSummaryItems,
    selectedWorkflow,
    selectedWorkflowDetail,
    selectedWorkflowReadiness,
    selectedWorkflowRevisions,
    selectedWorkflowRevisionDiff,
    selectedWorkflowDraft,
    hasPersistableDraft,
    leftTab: generateDock.leftTab,
    generatePresence: generateDock.generatePresence,
    generateLock,
    generateSending,
    generateRestorePending,
    generateSheetOpen: generateDock.generateSheetOpen,
    applyHighlightEpoch: generateDock.applyHighlightEpoch,
    prompt: generateDock.prompt,
    selectedAgentId: generateDock.selectedAgentId,
    agentsLoadState: generateDock.agentsLoadState,
    optimisticUserMessage: generateDock.optimisticUserMessage,
    agentPopoverOpen: generateDock.agentPopoverOpen,
    endSessionConfirmVisible: generateDock.endSessionConfirmVisible,
    generateTranscript,
    generateAgentOptions,
    selectedGenerateAgent,
    selectedGenerateAgentUsable,
    showGenerateDirtyChip,
    generateLastFailure,
    generateLastGuardReport,
    pendingFailureFeedback: generateDock.pendingFailureFeedback,
    failureFeedbackBannerHidden: generateDock.failureFeedbackBannerHidden,
    hideFailureFeedbackBanner,
    dismissPendingFailureFeedback,
    generateReasoningSteps,
    generateMissingCapabilities,
    generateSessionClosed,
    selectedNodeAiReason,
    selectGenerateTab,
    selectNodesTab,
    toggleGenerateFromTopbar,
    closeGenerateSheet,
    selectGenerateAgent,
    selectGenerateAgentById,
    syncGenerateContext,
    hydrateGenerateAgentChip,
    prepareGenerateDock,
    applyGeneratedDraftToEditor,
    sendGenerateTurn,
    handleGenerateFailureCta,
    confirmEndGenerateSession,
    cancelEndGenerateSession,
    workflowEditorBusy,
    selectedWorkflowCanPublish,
    canForcePublishWorkflow,
    selectedWorkflowCanForcePublish,
    workflowEditorReadinessSteps,
    workflowEditorPublishTitle,
    workflowEditorForcePublishTitle,
    selectedGraphNode,
    selectedGraphEdge,
    canUndoEditorChange,
    canRedoEditorChange,
    editorDirtyState,
    editorVariableRefs,
    selectedNodeVariableRefs,
    availableToolOptions,
    activeCompilationIssues,
    trialRunInputSchema,
    lastSuccessfulTrialInput,
    activeTraceExecution,
    selectedWorkflowExecutions,
    workflowEditorFeedbackMessage,
    workflowLifecycleSteps,
    workflowLifecycleHint,
    selectedWorkflowRevisionEmptyText,
    selectedWorkflowDiffEmptyText,
    selectedWorkflowExecutionEmptyText,
    selectedWorkflowSteps,
    loadWorkflowPageAssets,
    newWorkflowDraft,
    cloneGraph,
    sameGraph,
    isEditorDraftDirty,
    resetEditorHistory,
    replaceEditorGraph,
    updateEditorGraph,
    ensureWorkspacesLoaded,
    loadEditorDraft,
    invalidatePendingEditorLoad,
    closeWorkflowEditor,
    openIntentGenerateEditor,
    captureWorkflowFocus,
    restoreWorkflowFocus,
    focusFirstModalControl,
    trapModalFocus,
    workflowWorkspaceLabel,
    workflowNodeCount,
    workflowEdgeCount,
    statusClass,
    statusLabel,
    executionStatusLabel,
    validationLabel,
    workflowLastExecution,
    workflowLastResultLabel,
    workflowNextAction,
    workflowReadinessFallbackAction,
    localizedWorkflowReadinessAction,
    workflowPublishBlockedTitle,
    canReviseFromFailure,
    reviseSourceForReadiness,
    reviseDraftFromFailure,
    workflowReadinessStepState,
    buildWorkflowEditorReadinessSteps,
    workflowTriggerText,
    workflowExecutionCount,
    workflowSuccessRateLabel,
    workflowTableStatusLabel,
    workflowTableStatusClass,
    formatWorkflowUpdatedAt,
    loadWorkflowRegistry,
    setWorkflowSearch,
    changeWorkflowPage,
    changeWorkflowSort,
    clearWorkflowSearch,
    buildWorkflowLifecycleSteps,
    nodeTypeLabel,
    selectWorkflow,
    openWorkflowDetail,
    closeWorkflowDetail,
    closeWorkflowMetadata,
    openWorkflowEditor,
    syncSelection,
    setSelectedNode,
    setSelectedEdge,
    focusIssue,
    focusEdgeIssue,
    updateSelectedNodeLabel,
    updateSelectedNodeData,
    updateSelectedEdgeData,
    updateNodePosition,
    updateViewport,
    connectNodes,
    deleteSelectedNode,
    deleteSelectedEdge,
    closeContextMenu,
    isProtectedTerminalNode,
    isContextTargetDeleteDisabled,
    normalizeContextMenuPosition,
    openNodeContextMenu,
    openEdgeContextMenu,
    deleteContextTarget,
    portsForNodeType,
    addNodeToDraft,
    duplicateSelectedNode,
    applyAutoLayout,
    undoEditorChange,
    redoEditorChange,
    openCreateWorkflow,
    openEditWorkflow,
    buildWorkflowFromDraft,
    saveWorkflowMetadata,
    saveEditorDraft,
    persistEditorDraft,
    workflowMenuActions,
    handleWorkflowRowAction,
    deleteWorkflow,
    validateWorkflow,
    validateEditorWorkflow,
    runWorkflowTrial,
    runWorkflowProduction,
    openTrialRunDialog,
    trialRunEditorWorkflow,
    publishWorkflow,
    bindPublishedWorkflowToSessionAgent,
    activateRevision,
    rollbackRevision,
    compareRevision,
    disableWorkflowRuns,
    publishEditorWorkflow,
    forcePublishEditorWorkflow,
    openForcePublishDialog,
    closeForcePublishDialog,
    confirmForcePublishEditorWorkflow,
    submitTrialRun,
    closeTrialRunDialog,
    compilationForWorkflow,
    focusFirstCompilationIssue,
    buildCompilationBlockedMessage,
    focusCompilationIssue,
    selectTraceNode,
    isEditingTextInput,
    shouldHandleEditorShortcut,
    handleEditorKeydown,
    extractCompilationFromError,
    hasStaleCompilation,
    isStaleCompilationForWorkflow,
    isCompilationCurrentForDraft,
    extractTrialRunInputSchema,
    actionErrorMessage,
  };
}
