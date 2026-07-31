<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 13)
/** Workflow editor panel (ZKL-64 item 13). */
import WorkflowEdgeInspector from "./workflow/WorkflowEdgeInspector.vue";
import WorkflowExecutionTracePanel from "./workflow/WorkflowExecutionTracePanel.vue";
import WorkflowGraphCanvas from "./workflow/WorkflowGraphCanvas.vue";
import WorkflowInspector from "./workflow/WorkflowInspector.vue";
import WorkflowIssuesPanel from "./workflow/WorkflowIssuesPanel.vue";
import WorkflowNodePalette from "./workflow/WorkflowNodePalette.vue";
import WorkflowReadinessPanel from "./workflow/WorkflowReadinessPanel.vue";
import WorkflowForcePublishDialog from "./workflow/WorkflowForcePublishDialog.vue";
import WorkflowTrialRunDialog from "./workflow/WorkflowTrialRunDialog.vue";
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
void WorkflowEdgeInspector;
void WorkflowExecutionTracePanel;
void WorkflowForcePublishDialog;
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
      <section class="workflow-editor-shell" aria-label="流程图编辑器">
        <header class="workflow-editor-topbar">
          <div>
            <span>流程画布编辑</span>
            <h3>{{ selectedWorkflow?.name || "流程图" }}</h3>
            <p>{{ workflowEditorFeedbackMessage }}</p>
          </div>
        </header>
        <div class="workflow-editor-banner loading" role="status">
          <strong>正在加载</strong>
          <small>正在加载最新草稿和编译结果，请稍候。</small>
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
        aria-label="流程图编辑器"
        :data-editor-dirty-state="editorDirtyState"
        tabindex="-1"
      >
        <header class="workflow-editor-topbar">
          <div class="workflow-editor-header-row">
            <div class="workflow-editor-meta">
              <span>流程画布编辑</span>
              <div class="workflow-editor-title-row">
                <h3>{{ selectedWorkflow?.name || "流程图" }}</h3>
                <button
                  class="workflow-editor-help-button"
                  type="button"
                  aria-label="画布操作说明"
                  :title="workflowEditorHelpText"
                >
                  <i class="fa-solid fa-circle-info" />
                </button>
              </div>
            </div>
            <div class="workflow-editor-readiness-strip" aria-label="流程发布状态">
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
              <div class="workflow-editor-action-group" role="group" aria-label="辅助操作">
                <button
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="openEditWorkflow()"
                >
                  基础信息
                </button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" aria-label="画布整理">
                <button
                  data-action="duplicate-selected-node"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedGraphNode || workflowEditorBusy"
                  @click="duplicateSelectedNode"
                >
                  复制节点
                </button>
                <button
                  data-action="auto-layout-editor-graph"
                  class="ghost-button"
                  type="button"
                  title="按拓扑分层展开节点，消除堆叠与交叉"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="applyAutoLayout"
                >
                  格式化画布
                </button>
              </div>
              <span class="workflow-editor-action-divider" aria-hidden="true" />
              <div class="workflow-editor-action-group" role="group" aria-label="校验运行">
                <button
                  data-action="validate-editor-workflow"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="validateEditorWorkflow"
                >
                  {{ pendingEditorAction === "validate" ? "正在检查…" : "检查问题" }}
                </button>
                <button
                  data-action="open-trial-run-dialog"
                  class="ghost-button"
                  type="button"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="trialRunEditorWorkflow"
                >
                  {{ pendingEditorAction === "trial-run" ? "正在准备…" : "模拟运行" }}
                </button>
                <button
                  v-if="canReviseFromFailure"
                  data-action="revise-draft-from-failure"
                  class="ghost-button"
                  type="button"
                  title="按编译/试运行问题回到智能编排修订草稿（只出新 Draft，不自动发布）"
                  :disabled="!selectedWorkflow || workflowEditorBusy"
                  @click="reviseDraftFromFailure"
                >
                  按问题修订草稿
                </button>
              </div>
            </div>
          </div>
          <div class="workflow-editor-primary-actions workflow-editor-actions" aria-label="关键操作">
            <button
              data-action="save-editor-draft"
              class="primary-button"
              type="button"
              :disabled="!selectedWorkflow || workflowEditorBusy"
              @click="saveEditorDraft"
            >
              {{ pendingEditorAction === "save" ? "正在保存…" : "保存画布" }}
            </button>
            <button
              data-action="publish-editor-workflow"
              class="workflow-editor-publish-button"
              type="button"
              :title="workflowEditorPublishTitle"
              :disabled="!selectedWorkflow || workflowEditorBusy || !selectedWorkflowCanPublish"
              @click="publishEditorWorkflow"
            >
              {{ pendingEditorAction === "publish" ? "正在发布…" : "发布上线" }}
            </button>
            <button
              v-if="canForcePublishWorkflow"
              data-action="force-publish-editor-workflow"
              class="workflow-editor-force-publish-button"
              type="button"
              :title="workflowEditorForcePublishTitle"
              :disabled="!selectedWorkflow || workflowEditorBusy || !selectedWorkflowCanForcePublish"
              @click="forcePublishEditorWorkflow"
            >
              {{ pendingEditorAction === "force-publish" ? "强制发布中…" : "强制发布" }}
            </button>
            <button
              class="workflow-editor-close-button"
              type="button"
              aria-label="退出编辑"
              title="退出编辑"
              :disabled="workflowEditorBusy"
              @click="closeWorkflowEditor()"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </div>
        </header>

        <div class="workflow-workbench workflow-workbench-full-bleed" @click="closeContextMenu">
          <WorkflowNodePalette
            class="workflow-workbench-column"
            :variable-refs="editorVariableRefs"
            @add-node="addNodeToDraft"
          />
          <WorkflowGraphCanvas
            class="workflow-workbench-main"
            :graph="editorGraph"
            :selected-node-id="selectedNodeId"
            :selected-edge-id="selectedEdgeId"
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
              ? "起止节点不可删除"
              : contextMenu.targetType === "node"
                ? "删除节点"
                : "删除连线"
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
