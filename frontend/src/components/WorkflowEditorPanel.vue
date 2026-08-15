<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from "vue";
import { useI18n } from "vue-i18n";
const { t } = useI18n();
// @ts-nocheck — inject surface under page split (ZKL-64 item 13)
/** Workflow editor panel (ZKL-64 item 13). */
import WorkflowEdgeInspector from "./workflow/WorkflowEdgeInspector.vue";
import WorkflowExecutionTracePanel from "./workflow/WorkflowExecutionTracePanel.vue";
import WorkflowGenerateDock from "./workflow/WorkflowGenerateDock.vue";
import WorkflowGraphCanvas from "./workflow/WorkflowGraphCanvas.vue";
import WorkflowInspector from "./workflow/WorkflowInspector.vue";
import WorkflowIssuesPanel from "./workflow/WorkflowIssuesPanel.vue";
import WorkflowNodePalette from "./workflow/WorkflowNodePalette.vue";
import WorkflowReadinessPanel from "./workflow/WorkflowReadinessPanel.vue";
import WorkflowForcePublishDialog from "./workflow/WorkflowForcePublishDialog.vue";
import WorkflowTrialRunDialog from "./workflow/WorkflowTrialRunDialog.vue";
import { useWorkflowGenerateNarrow } from "../composables/workflow-generate-dock";
import { useWorkflowPageContext } from "../composables/useWorkflowPageContext";

const scp = useWorkflowPageContext();
const {
  workflowToolCatalog,
  workflowToolCatalogError,
  workflowEditorVisible,
  pendingEditorAction,
  pendingTrialRun,
  selectedNodeId,
  selectedEdgeId,
  contextMenu,
  editorGraph,
  editorDraftLoadState,
  trialRunVisible,
  trialRunTargetWorkflowName,
  forcePublishDialogVisible,
  workflowEditorShellRef,
  workflowEditorHelpText,
  selectedWorkflow,
  hasWorkspaceContext,
  hasPersistableDraft,
  leftTab,
  generatePresence,
  generateLock,
  generateSheetOpen,
  applyHighlightEpoch,
  prompt,
  selectGenerateTab,
  selectNodesTab,
  toggleGenerateFromTopbar,
  closeGenerateSheet,
  workflowEditorBusy,
  selectedWorkflowCanPublish,
  workflowEditorReadinessSteps,
  workflowEditorPublishTitle,
  selectedGraphNode,
  selectedGraphEdge,
  editorDirtyState,
  editorVariableRefs,
  selectedNodeVariableRefs,
  availableToolOptions,
  activeCompilationIssues,
  trialRunInputSchema,
  lastSuccessfulTrialInput,
  activeTraceExecution,
  workflowEditorFeedbackMessage,
  closeWorkflowEditor,
  canReviseFromFailure,
  reviseDraftFromFailure,
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
  closeContextMenu,
  isContextTargetDeleteDisabled,
  openNodeContextMenu,
  openEdgeContextMenu,
  deleteContextTarget,
  addNodeToDraft,
  duplicateSelectedNode,
  applyAutoLayout,
  openEditWorkflow,
  saveEditorDraft,
  validateEditorWorkflow,
  trialRunEditorWorkflow,
  publishEditorWorkflow,
  forcePublishEditorWorkflow,
  closeForcePublishDialog,
  confirmForcePublishEditorWorkflow,
  submitTrialRun,
  closeTrialRunDialog,
  selectTraceNode,
  canForcePublishWorkflow,
  selectedWorkflowCanForcePublish,
  workflowEditorForcePublishTitle,
} = scp;
const isNarrowWorkbench = useWorkflowGenerateNarrow();
const isGenerateSheet = computed(
  () => leftTab.value === "generate" && generateSheetOpen.value && isNarrowWorkbench.value,
);
const canvasEmpty = computed(() => !hasPersistableDraft.value || editorGraph.value.nodes.length === 0);

function handleGenerateSheetKeydown(event: KeyboardEvent) {
  if (event.key !== "Escape" || !isGenerateSheet.value || generateLock.value) {
    return;
  }
  event.preventDefault();
  closeGenerateSheet();
}

onMounted(() => {
  window.addEventListener("keydown", handleGenerateSheetKeydown);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleGenerateSheetKeydown);
});
void WorkflowEdgeInspector;
void WorkflowExecutionTracePanel;
void WorkflowForcePublishDialog;
void WorkflowGenerateDock;
void WorkflowGraphCanvas;
void WorkflowInspector;
void WorkflowIssuesPanel;
void WorkflowNodePalette;
void WorkflowReadinessPanel;
void WorkflowTrialRunDialog;
</script>

<template>
  <div class="workflow-editor-panel-root">
    <div
      v-if="editorDraftLoadState === 'loading' && !workflowEditorVisible"
      class="workflow-editor-overlay workflow-editor-overlay-full-bleed"
    >
      <section class="workflow-editor-shell" :aria-label="t('workflow.editorAria')">
        <header class="workflow-editor-topbar">
          <div>
            <span>{{ t("workflow.canvasEdit") }}</span>
            <h3>{{ selectedWorkflow?.name || t("workflow.graphTitle") }}</h3>
            <p>{{ workflowEditorFeedbackMessage }}</p>
          </div>
        </header>
        <div class="workflow-editor-banner loading" role="status">
          <strong>{{ t("workflow.loadingTitle") }}</strong>
          <small>{{ t("workflow.loadingBody") }}</small>
        </div>
      </section>
    </div>

    <div
      v-if="workflowEditorVisible"
      class="workflow-editor-overlay workflow-editor-overlay-full-bleed"
      @click="closeContextMenu"
    >
      <section
        ref="workflowEditorShellRef"
        class="workflow-editor-shell"
        :aria-label="t('workflow.editorAria')"
        :data-editor-dirty-state="editorDirtyState"
        tabindex="-1"
      >
        <header class="workflow-editor-topbar">
          <div class="workflow-editor-header-row">
            <div class="workflow-editor-meta">
              <span>{{ t("workflow.canvasEdit") }}</span>
              <div class="workflow-editor-title-row">
                <h3>{{ selectedWorkflow?.name || t("workflow.graphTitle") }}</h3>
                <button
                  class="workflow-editor-help-button"
                  type="button"
                  :aria-label="t('workflow.canvasHelpAria')"
                  :title="workflowEditorHelpText"
                >
                  <i class="fa-solid fa-circle-info" />
                </button>
              </div>
            </div>
            <div class="workflow-editor-readiness-strip" :aria-label="t('workflow.publishStatusAria')">
              <span
                v-for="step in workflowEditorReadinessSteps"
                :key="step.key"
                class="workflow-editor-readiness-chip"
                :data-readiness-state="step.state"
              >
                <i :class="step.icon" />
                {{ step.label }}
              </span>
            </div>
          </div>
          <div class="workflow-editor-action-row">
            <div class="workflow-editor-secondary-actions workflow-editor-actions">
              <div class="workflow-editor-action-group" role="group" :aria-label="t('workflow.generateDockTitle')">
                <button
                  data-action="open-generate-dock"
                  class="ghost-button"
                  type="button"
                  :aria-pressed="leftTab === 'generate'"
                  @click="toggleGenerateFromTopbar"
                >
                  {{ t("workflow.generateOpenDock") }}
                </button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" :aria-label="t('workflow.auxActions')">
                <button
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="openEditWorkflow()"
                >
                  {{ t("workflow.basicInfo") }}
                </button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" :aria-label="t('workflow.canvasLayout')">
                <button
                  data-action="duplicate-selected-node"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedGraphNode || workflowEditorBusy"
                  @click="duplicateSelectedNode"
                >
                  {{ t("workflow.duplicateNode") }}
                </button>
                <button
                  data-action="auto-layout-editor-graph"
                  class="ghost-button"
                  type="button"
                  :title="t('workflow.formatCanvasTitle')"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="applyAutoLayout"
                >
                  {{ t("workflow.formatCanvas") }}
                </button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" :aria-label="t('workflow.validateRun')">
                <button
                  data-action="validate-editor-workflow"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="validateEditorWorkflow"
                >
                  {{ pendingEditorAction === "validate" ? t("workflow.checking") : t("workflow.checkIssues") }}
                </button>
                <button
                  data-action="open-trial-run-dialog"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="trialRunEditorWorkflow"
                >
                  {{ pendingEditorAction === "trial-run" ? t("workflow.preparing") : t("workflow.simulateRun") }}
                </button>
                <button
                  v-if="canReviseFromFailure"
                  data-action="revise-draft-from-failure"
                  class="ghost-button"
                  type="button"
                  :title="t('workflow.reviseDraftTitle')"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="reviseDraftFromFailure"
                >
                  {{ t("workflow.reviseDraft") }}
                </button>
              </div>
            </div>
          </div>
          <div
            class="workflow-editor-primary-actions workflow-editor-actions"
            :aria-label="t('workflow.primaryActions')"
          >
            <button
              data-action="save-editor-draft"
              class="primary-button"
              type="button"
              :disabled="!selectedWorkflow || workflowEditorBusy"
              @click="saveEditorDraft"
            >
              {{ pendingEditorAction === "save" ? t("workflow.saving") : t("workflow.saveCanvas") }}
            </button>
            <button
              data-action="publish-editor-workflow"
              class="workflow-editor-publish-button"
              type="button"
              :title="workflowEditorPublishTitle"
              :disabled="!selectedWorkflow || workflowEditorBusy || !selectedWorkflowCanPublish"
              @click="publishEditorWorkflow"
            >
              {{ pendingEditorAction === "publish" ? t("workflow.publishing") : t("workflow.publish") }}
            </button>
            <details v-if="canForcePublishWorkflow" class="workflow-editor-more-actions">
              <summary :aria-label="t('workflow.morePublishActions')" :title="t('workflow.morePublishActions')">
                <i class="fa-solid fa-ellipsis" aria-hidden="true" />
              </summary>
              <div>
                <button
                  data-action="force-publish-editor-workflow"
                  class="workflow-editor-force-publish-button"
                  type="button"
                  :title="workflowEditorForcePublishTitle"
                  :disabled="!selectedWorkflow || workflowEditorBusy || !selectedWorkflowCanForcePublish"
                  @click="forcePublishEditorWorkflow"
                >
                  <i class="fa-solid fa-bolt" aria-hidden="true" />
                  {{
                    pendingEditorAction === "force-publish" ? t("workflow.forcePublishing") : t("workflow.forcePublish")
                  }}
                </button>
              </div>
            </details>
            <button
              class="workflow-editor-close-button"
              type="button"
              :aria-label="t('workflow.exitEdit')"
              :title="t('workflow.exitEdit')"
              :disabled="workflowEditorBusy"
              @click="closeWorkflowEditor()"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </div>
        </header>

        <div
          class="workflow-workbench workflow-workbench-full-bleed"
          :class="{ 'is-generate-sheet': isGenerateSheet }"
          @click="closeContextMenu"
        >
          <div
            class="workflow-workbench-column workflow-workbench-left"
            :data-left-tab="leftTab"
            :data-generate-presence="generatePresence"
          >
            <div class="workflow-left-tabs" role="tablist" :aria-label="t('workflow.generateLeftTabsAria')">
              <button
                type="button"
                role="tab"
                class="ghost-button"
                :aria-selected="leftTab === 'generate'"
                @click="selectGenerateTab"
              >
                {{ t("workflow.generateDockTitle") }}
              </button>
              <button
                type="button"
                role="tab"
                class="ghost-button"
                :aria-selected="leftTab === 'nodes'"
                :disabled="!hasPersistableDraft"
                :title="hasPersistableDraft ? undefined : t('workflow.generateNodesDisabledHint')"
                @click="selectNodesTab"
              >
                {{ t("workflow.generateDockNodesTab") }}
              </button>
            </div>
            <WorkflowGenerateDock
              v-if="leftTab === 'generate'"
              class="workflow-left-card"
              :has-workspace-context="hasWorkspaceContext"
              :prompt="prompt"
              :sheet="isGenerateSheet"
              @update:prompt="prompt = $event"
              @close-sheet="closeGenerateSheet"
            />
            <WorkflowNodePalette
              v-else
              class="workflow-left-card"
              :variable-refs="editorVariableRefs"
              :disabled="!hasPersistableDraft || generateLock"
              @add-node="addNodeToDraft"
            />
          </div>
          <button
            v-if="isGenerateSheet"
            class="workflow-generate-sheet-backdrop"
            type="button"
            :aria-label="t('workflow.generateSheetDone')"
            @click="closeGenerateSheet"
          />
          <WorkflowGraphCanvas
            class="workflow-workbench-main"
            :graph="editorGraph"
            :selected-node-id="selectedNodeId"
            :selected-edge-id="selectedEdgeId"
            :empty="canvasEmpty"
            :generating="generateLock"
            :lock-interaction="generateLock"
            :apply-highlight-epoch="applyHighlightEpoch"
            @select-node="setSelectedNode"
            @select-edge="setSelectedEdge"
            @open-node-context-menu="openNodeContextMenu"
            @open-edge-context-menu="openEdgeContextMenu"
            @update-node-position="updateNodePosition"
            @update-viewport="updateViewport"
            @connect-nodes="connectNodes"
          />
          <aside
            class="workflow-workbench-column workflow-workbench-side workflow-workbench-side-scrollable workflow-scroll-fade"
            @click.stop
          >
            <WorkflowInspector
              v-if="selectedGraphNode"
              :node="selectedGraphNode"
              :tools="workflowToolCatalog"
              :tool-options="availableToolOptions"
              :variable-refs="selectedNodeVariableRefs"
              :tool-catalog-error="workflowToolCatalogError"
              @update-node-label="updateSelectedNodeLabel"
              @update-node-data="updateSelectedNodeData"
            />
            <WorkflowEdgeInspector
              v-else-if="selectedGraphEdge"
              :edge="selectedGraphEdge"
              @update-edge-data="updateSelectedEdgeData"
            />
            <WorkflowInspector
              v-else
              :node="selectedGraphNode"
              :tools="workflowToolCatalog"
              :tool-options="availableToolOptions"
              :variable-refs="selectedNodeVariableRefs"
              :tool-catalog-error="workflowToolCatalogError"
              @update-node-label="updateSelectedNodeLabel"
              @update-node-data="updateSelectedNodeData"
            />
            <WorkflowIssuesPanel
              :issues="activeCompilationIssues"
              :selected-node-id="selectedNodeId"
              :selected-edge-id="selectedEdgeId"
              :show-revise-cta="canReviseFromFailure"
              @focus-node="focusIssue"
              @focus-edge="focusEdgeIssue"
              @revise-from-failure="reviseDraftFromFailure"
            />
            <WorkflowExecutionTracePanel
              v-if="activeTraceExecution?.workflowId === selectedWorkflow?.id"
              :execution="activeTraceExecution"
              :selected-node-id="selectedNodeId"
              @select-node="selectTraceNode"
            />
          </aside>
        </div>
      </section>

      <div
        v-if="contextMenu"
        class="workflow-context-menu"
        :style="{ left: `${contextMenu.position.x}px`, top: `${contextMenu.position.y}px` }"
        @click.stop
      >
        <button
          data-action="delete-context-target"
          class="workflow-context-menu-item danger"
          type="button"
          :disabled="isContextTargetDeleteDisabled()"
          @click="deleteContextTarget"
        >
          {{
            isContextTargetDeleteDisabled()
              ? t("workflow.cannotDeleteTerminal")
              : contextMenu.targetType === "node"
                ? t("workflow.deleteNode")
                : t("workflow.deleteEdge")
          }}
        </button>
      </div>
    </div>

    <WorkflowTrialRunDialog
      v-if="trialRunVisible"
      :visible="trialRunVisible"
      :workflow-name="trialRunTargetWorkflowName"
      :input-schema="trialRunInputSchema"
      :last-successful-input="lastSuccessfulTrialInput"
      :submitting="pendingTrialRun"
      @close="closeTrialRunDialog"
      @submit="submitTrialRun"
    />

    <WorkflowForcePublishDialog
      :visible="forcePublishDialogVisible"
      :workflow-name="selectedWorkflow?.name"
      :submitting="pendingEditorAction === 'force-publish'"
      @close="closeForcePublishDialog"
      @submit="confirmForcePublishEditorWorkflow"
    />
  </div>
</template>
