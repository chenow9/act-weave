import { onBeforeUnmount, onMounted, ref, type Ref } from "vue";

import type { WorkflowGraphDraft } from "../types/domain";

export type WorkflowGenerateLeftTab = "generate" | "nodes";
export type WorkflowGeneratePresence = "open" | "sheet";

export const WORKFLOW_GENERATE_PROMPT_MAX = 2000;
export const WORKFLOW_GENERATE_NARROW_MEDIA = "(max-width: 1180px)";
export const WORKFLOW_GENERATE_HIGHLIGHT_MS = 180;

export function createEmptyWorkflowGraphDraft(): WorkflowGraphDraft {
  return {
    schemaVersion: "workflow.graph.v1",
    nodes: [],
    edges: [],
    viewport: { x: 0, y: 0, zoom: 1 },
    ui: {},
  };
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
  }

  return {
    leftTab,
    generatePresence,
    generateLock,
    generateSheetOpen,
    applyHighlightEpoch,
    prompt,
    syncLeftTabForOpenEditor,
    selectGenerateTab,
    selectNodesTab,
    toggleGenerateFromTopbar,
    closeGenerateSheet,
    resetGenerateDock,
  };
}
