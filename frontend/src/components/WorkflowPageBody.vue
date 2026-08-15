<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 13)
/** Workflow page body (ZKL-64 item 13). */
import AppSelect from "./AppSelect.vue";
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSummaryStrip from "./ManagementSummaryStrip.vue";
import WorkflowEditorPanel from "./WorkflowEditorPanel.vue";
import WorkflowExecutionTracePanel from "./workflow/WorkflowExecutionTracePanel.vue";
import WorkflowReadinessPanel from "./workflow/WorkflowReadinessPanel.vue";
import WorkflowRevisionDiff from "./workflow/WorkflowRevisionDiff.vue";
import WorkflowRevisionPanel from "./workflow/WorkflowRevisionPanel.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useI18n } from "vue-i18n";
import { useWorkflowPageContext } from "../composables/useWorkflowPageContext";

const { t } = useI18n();
const scp = useWorkflowPageContext();
const {
  workflowStore,
  workspaces,
  auth,
  hasWorkspaceContext,
  workflowQuery,
  workflowDetailVisible,
  workflowMetadataVisible,
  workflowMetadataMode,
  workflowDraft,
  workflowMetadataTouched,
  workflowActionNote,
  pendingRevisionActionId,
  pendingRevisionCompare,
  pendingWorkflowDisable,
  selectedNodeId,
  editorGraph,
  editorDraftLoadState,
  workflowDetailModalRef,
  workflowMetadataModalRef,
  workflowStatusOptions,
  workspaceOptions,
  workflowNameError,
  workflowWorkspaceError,
  canSaveWorkflowMetadata,
  workflowColumns,
  workflowSummaryItems,
  selectedWorkflow,
  selectedWorkflowReadiness,
  selectedWorkflowRevisions,
  selectedWorkflowRevisionDiff,
  selectedWorkflowCanPublish,
  activeTraceExecution,
  selectedWorkflowExecutions,
  selectedWorkflowRevisionEmptyText,
  selectedWorkflowDiffEmptyText,
  selectedWorkflowExecutionEmptyText,
  selectedWorkflowSteps,
  loadWorkflowPageAssets,
  trapModalFocus,
  workflowWorkspaceLabel,
  workflowNodeCount,
  workflowEdgeCount,
  statusClass,
  statusLabel,
  executionStatusLabel,
  validationLabel,
  workflowTriggerText,
  workflowExecutionCount,
  workflowSuccessRateLabel,
  workflowTableStatusLabel,
  workflowTableStatusClass,
  formatWorkflowUpdatedAt,
  setWorkflowSearch,
  changeWorkflowPage,
  changeWorkflowSort,
  clearWorkflowSearch,
  openWorkflowDetail,
  closeWorkflowDetail,
  closeWorkflowMetadata,
  openWorkflowEditor,
  openCreateWorkflow,
  openIntentGenerateEditor,
  openEditWorkflow,
  saveWorkflowMetadata,
  workflowMenuActions,
  handleWorkflowRowAction,
  validateWorkflow,
  openTrialRunDialog,
  publishWorkflow,
  activateRevision,
  rollbackRevision,
  compareRevision,
  disableWorkflowRuns,
  selectTraceNode,
} = scp;
void AppSelect;
void ManagementList;
void ManagementPageHeader;
void ManagementRowActions;
void ManagementSummaryStrip;
void WorkflowEditorPanel;
void WorkflowExecutionTracePanel;
void WorkflowReadinessPanel;
void WorkflowRevisionDiff;
void WorkflowRevisionPanel;
void WorkspaceContextState;
</script>

<template>
  <div class="workflow-center-page workflow-orchestration-page management-page-grid" v-loading="workflowStore.loading">
    <ManagementPageHeader
      class="workflow-orchestration-header"
      :title="t('workflow.title')"
      :description="t('workflow.subtitle')"
      icon="fa-solid fa-diagram-project"
    >
      <template #actions>
        <button
          class="ghost-button workflow-generate-from-intent-button"
          type="button"
          data-action="open-intent-generate"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? t('workflow.generateFromIntent') : t('workspaces.emptyHint')"
          @click="openIntentGenerateEditor"
        >
          <i class="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />
          <span>{{ t("workflow.generateFromIntent") }}</span>
        </button>
        <button
          class="primary-button workflow-create-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? t('workflow.newWorkflow') : t('workspaces.emptyHint')"
          @click="openCreateWorkflow"
        >
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          <span>{{ t("workflow.newWorkflow") }}</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip :items="workflowSummaryItems" />

    <section class="workflow-center-panel workflow-orchestration-table-card management-list-card">
      <ManagementList
        class="workflow-management-list"
        :rows="hasWorkspaceContext ? workflowStore.pageItems : []"
        :columns="workflowColumns"
        row-key="id"
        :sticky-left-keys="['workflow']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:workflows:columns"
        :selected-row-key="selectedWorkflow?.id"
        selection-tone="neutral"
        :loading="hasWorkspaceContext && workflowStore.pageLoading"
        :error="hasWorkspaceContext ? workflowStore.pageError : undefined"
        :has-loaded="hasWorkspaceContext ? workflowStore.pageHasLoaded : true"
        :search="workflowQuery"
        :search-placeholder="t('workflow.searchPlaceholder')"
        :search-aria-label="t('workflow.searchAria')"
        :reset-disabled="!workflowQuery"
        :pagination="workflowStore.pagination"
        :sort-by="workflowStore.listQuery?.sortBy"
        :sort-order="workflowStore.listQuery?.sortOrder"
        @select-row="openWorkflowDetail"
        @update:search="setWorkflowSearch"
        @reset="clearWorkflowSearch"
        @page-change="changeWorkflowPage"
        @sort-change="changeWorkflowSort"
      >
        <template #cell-workflow="{ row: workflow }">
          <div class="workflow-name-cell">
            <strong class="aw-table-title" :title="workflow.name">{{ workflow.name }}</strong>
            <small class="aw-table-subtitle" :title="workflow.description || t('workflow.noDescription')">{{
              workflow.description || t("workflow.noDescription")
            }}</small>
          </div>
        </template>
        <template #cell-workspace="{ row: workflow }"
          ><span class="workflow-workspace-cell aw-table-meta" :title="workflowWorkspaceLabel(workflow)">{{
            workflowWorkspaceLabel(workflow)
          }}</span></template
        >
        <template #cell-nodes="{ row: workflow }"
          ><span class="workflow-mono-cell aw-table-meta">{{
            t("workflow.stepsEdges", { steps: workflowNodeCount(workflow), edges: workflowEdgeCount(workflow) })
          }}</span></template
        >
        <template #cell-successRate="{ row: workflow }"
          ><span class="workflow-success-cell aw-table-meta">{{ workflowSuccessRateLabel(workflow) }}</span></template
        >
        <template #cell-status="{ row: workflow }"
          ><span class="workflow-status-badge aw-table-pill" :class="workflowTableStatusClass(workflow)">{{
            workflowTableStatusLabel(workflow)
          }}</span></template
        >
        <template #cell-updatedAt="{ row: workflow }"
          ><span class="workflow-updated-at aw-table-meta">{{ formatWorkflowUpdatedAt(workflow) }}</span></template
        >
        <template #cell-actions="{ row: workflow }">
          <ManagementRowActions
            :menu-actions="workflowMenuActions(workflow)"
            :menu-label="t('workflow.moreActions')"
            @action="handleWorkflowRowAction($event, workflow)"
          />
        </template>
        <template #empty>
          <WorkspaceContextState
            v-if="!hasWorkspaceContext"
            embedded-in-list
            :feature="t('workflow.featureName')"
            icon="fa-solid fa-diagram-project"
            @retry="loadWorkflowPageAssets"
          />
          <div v-else class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon">
              <i class="fa-solid fa-diagram-project" aria-hidden="true" />
            </div>
            <h2>{{ workflowStore.workflows.length ? t("workflow.noMatchTitle") : t("workflow.emptyTitle") }}</h2>
            <p>
              {{ workflowStore.workflows.length ? t("workflow.noMatchBody") : t("workflow.emptyBody") }}
            </p>
            <button
              v-if="workflowStore.workflows.length"
              class="ghost-button"
              type="button"
              @click="clearWorkflowSearch"
            >
              {{ t("workflow.clearFilters") }}
            </button>
            <div v-else class="registry-empty-actions">
              <button
                class="ghost-button"
                type="button"
                data-action="open-intent-generate"
                @click="openIntentGenerateEditor"
              >
                {{ t("workflow.generateFromIntent") }}
              </button>
              <button class="primary-button" type="button" @click="openCreateWorkflow">
                {{ t("workflow.newWorkflow") }}
              </button>
            </div>
          </div>
        </template>
      </ManagementList>
    </section>

    <Transition name="modal-fade">
      <div v-if="workflowDetailVisible" class="modal-backdrop" @click.self="closeWorkflowDetail">
        <section
          v-if="selectedWorkflow"
          ref="workflowDetailModalRef"
          class="modal-card workflow-detail-modal-card"
          role="dialog"
          aria-modal="true"
          :aria-label="t('workflow.detailTitle')"
          tabindex="-1"
          @keydown.esc.stop.prevent="closeWorkflowDetail"
          @keydown.tab="trapModalFocus($event, workflowDetailModalRef)"
        >
          <div class="modal-card-head">
            <div>
              <span>Workflow Lifecycle</span>
              <h3>{{ t("workflow.detailTitle") }}</h3>
            </div>
            <button
              class="icon-action-button"
              type="button"
              :aria-label="t('workflow.closeDetail')"
              @click="closeWorkflowDetail"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </div>
          <div class="workflow-detail-modal-body">
            <section class="workflow-detail-panel">
              <div class="workflow-detail-hero">
                <span class="status-pill" :class="statusClass(selectedWorkflow.status)">{{
                  statusLabel(selectedWorkflow.status)
                }}</span>
                <h3>{{ selectedWorkflow.name }}</h3>
                <p>{{ selectedWorkflow.description || t("workflow.noDescriptionLong") }}</p>
              </div>

              <div class="workflow-detail-metrics">
                <span>
                  <strong>{{ workflowNodeCount(selectedWorkflow) }}</strong>
                  <small>{{ t("workflow.steps") }}</small>
                </span>
                <span>
                  <strong>{{ workflowExecutionCount(selectedWorkflow) }}</strong>
                  <small>{{ t("workflow.executions") }}</small>
                </span>
                <span>
                  <strong>{{ validationLabel(selectedWorkflow) }}</strong>
                  <small>{{ t("workflow.validation") }}</small>
                </span>
              </div>

              <WorkflowReadinessPanel :readiness="selectedWorkflowReadiness" />
              <WorkflowRevisionPanel
                :revisions="selectedWorkflowRevisions"
                :readiness="selectedWorkflowReadiness"
                :busy-revision-id="pendingRevisionActionId"
                :workflow-status="selectedWorkflow.status"
                :disable-busy="pendingWorkflowDisable || pendingRevisionCompare"
                :empty-text="selectedWorkflowRevisionEmptyText"
                @activate="activateRevision"
                @rollback="rollbackRevision"
                @compare="compareRevision"
                @disable="disableWorkflowRuns"
              />
              <WorkflowRevisionDiff :diff="selectedWorkflowRevisionDiff" :empty-text="selectedWorkflowDiffEmptyText" />
              <div class="workflow-readable-section">
                <h4>{{ t("workflow.whatItDoes") }}</h4>
                <p>{{ selectedWorkflow.description || t("workflow.defaultPurpose") }}</p>
              </div>

              <div class="workflow-readable-section">
                <h4>{{ t("workflow.whenTriggered") }}</h4>
                <p>{{ workflowTriggerText() }}</p>
              </div>

              <div class="workflow-readable-section">
                <h4>{{ t("workflow.whichSteps") }}</h4>
                <div v-if="selectedWorkflowSteps.length" class="workflow-step-list">
                  <article v-for="step in selectedWorkflowSteps" :key="step.id" class="workflow-step-item">
                    <b>{{ step.order }}</b>
                    <span>
                      <strong>{{ step.title }}</strong>
                      <small>{{ step.type }} · {{ step.detail }}</small>
                    </span>
                  </article>
                </div>
                <p v-else>{{ t("workflow.noSteps") }}</p>
              </div>

              <div class="workflow-readable-section">
                <h4>{{ t("workflow.recentRuns") }}</h4>
                <div v-if="selectedWorkflowExecutions.length" class="workflow-execution-list">
                  <article
                    v-for="execution in selectedWorkflowExecutions"
                    :key="execution.id"
                    class="workflow-execution-item"
                  >
                    <span class="status-pill" :class="statusClass(execution.status)">{{
                      executionStatusLabel(execution.status)
                    }}</span>
                    <span>
                      <strong>{{ execution.trigger }}</strong>
                      <small
                        >{{ execution.durationMs }} ms · {{ execution.outputSummary || execution.inputSummary }}</small
                      >
                    </span>
                  </article>
                </div>
                <p v-else>{{ selectedWorkflowExecutionEmptyText }}</p>
              </div>

              <div v-if="activeTraceExecution?.workflowId === selectedWorkflow.id" class="workflow-readable-section">
                <h4>{{ t("workflow.trialTrace") }}</h4>
                <WorkflowExecutionTracePanel
                  :execution="activeTraceExecution"
                  :selected-node-id="selectedNodeId"
                  @select-node="selectTraceNode"
                />
              </div>
            </section>
          </div>

          <div class="workflow-detail-actions">
            <button class="ghost-button" type="button" @click="closeWorkflowDetail">{{ t("workflow.close") }}</button>
            <button
              class="ghost-button"
              type="button"
              :disabled="!selectedWorkflow"
              @click="selectedWorkflow && validateWorkflow(selectedWorkflow)"
            >
              {{ t("workflow.validate") }}
            </button>
            <button
              class="ghost-button"
              type="button"
              :disabled="!selectedWorkflow"
              @click="selectedWorkflow && openTrialRunDialog(selectedWorkflow)"
            >
              {{ t("workflow.trialRun") }}
            </button>
            <button class="ghost-button" type="button" :disabled="!selectedWorkflow" @click="openEditWorkflow()">
              {{ t("workflow.editInfo") }}
            </button>
            <button
              class="ghost-button"
              type="button"
              :disabled="!selectedWorkflow || !selectedWorkflowCanPublish"
              @click="publishWorkflow()"
            >
              {{ t("workflow.publish") }}
            </button>
            <button
              v-if="selectedWorkflow && workspaces.can(selectedWorkflow.workspaceId, auth.user?.id || '', 'EDIT')"
              class="primary-button"
              type="button"
              :disabled="!selectedWorkflow || editorDraftLoadState === 'loading'"
              :aria-busy="editorDraftLoadState === 'loading' ? 'true' : undefined"
              @click="openWorkflowEditor()"
            >
              {{ t("workflow.openEditor") }}
            </button>
          </div>
        </section>
      </div>
    </Transition>

    <WorkflowEditorPanel />

    <Transition name="modal-fade">
      <div v-if="workflowMetadataVisible" class="modal-backdrop" @click.self="closeWorkflowMetadata">
        <section
          ref="workflowMetadataModalRef"
          class="modal-card workflow-metadata-modal-card"
          role="dialog"
          aria-modal="true"
          :aria-label="workflowMetadataMode === 'create' ? t('workflow.newWorkflow') : t('workflow.editInfo')"
          tabindex="-1"
          @keydown.esc.stop.prevent="closeWorkflowMetadata"
          @keydown.tab="trapModalFocus($event, workflowMetadataModalRef)"
        >
          <div class="modal-card-head">
            <div>
              <span>Workflow Metadata</span>
              <h3>{{ workflowMetadataMode === "create" ? t("workflow.newWorkflow") : t("workflow.editInfo") }}</h3>
            </div>
            <button
              class="icon-action-button"
              type="button"
              :aria-label="workflowMetadataMode === 'create' ? t('workflow.newWorkflow') : t('workflow.editInfo')"
              @click="closeWorkflowMetadata"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </div>
          <div class="workflow-metadata-body">
            <div class="workflow-create-guide">
              <span><b>1</b> {{ t("workflow.createGuide1") }}</span>
              <span><b>2</b> {{ t("workflow.createGuide2") }}</span>
              <span><b>3</b> {{ t("workflow.createGuide3") }}</span>
              <span><b>4</b> {{ t("workflow.createGuide4") }}</span>
            </div>
            <label class="drawer-field">
              <span>{{ t("common.name") }} <b class="field-required-mark">*</b></span>
              <input
                v-model="workflowDraft.name"
                required
                aria-required="true"
                :aria-invalid="workflowMetadataTouched && Boolean(workflowNameError)"
                aria-describedby="workflow-name-error"
                @blur="workflowMetadataTouched = true"
              />
              <small
                v-if="workflowMetadataTouched && workflowNameError"
                id="workflow-name-error"
                class="field-error"
                role="alert"
                >{{ workflowNameError }}</small
              >
            </label>
            <div class="form-two">
              <label class="drawer-field">
                <span>{{ t("workflow.colWorkspace") }} <b class="field-required-mark">*</b></span>
                <AppSelect
                  class="workflow-workspace-select"
                  v-model="workflowDraft.workspaceId"
                  :options="workspaceOptions"
                  :aria-required="true"
                  :aria-invalid="workflowMetadataTouched && Boolean(workflowWorkspaceError)"
                />
                <small v-if="workflowMetadataTouched && workflowWorkspaceError" class="field-error" role="alert">{{
                  workflowWorkspaceError
                }}</small>
              </label>
              <label class="drawer-field">
                <span>{{ t("workflow.slug") }}</span>
                <input v-model="workflowDraft.slug" :placeholder="t('workflow.slugPlaceholder')" />
              </label>
            </div>
            <label class="drawer-field">
              <span>{{ t("workflow.status") }}</span>
              <AppSelect v-model="workflowDraft.status" :options="workflowStatusOptions" />
            </label>
            <label class="drawer-field"
              ><span>{{ t("workflow.whatItDoes") }}</span
              ><textarea v-model="workflowDraft.description" class="workflow-description-input" rows="4" />
            </label>
            <div class="drawer-schema-preview">
              <div>
                <i class="fa-solid fa-diagram-project" />
                <span>
                  <strong>{{
                    t("workflow.stepsEdges", { steps: editorGraph.nodes.length, edges: editorGraph.edges.length })
                  }}</strong>
                  <small>{{ t("workflow.stepsPreviewHint") }}</small>
                </span>
              </div>
            </div>
          </div>

          <div class="workflow-metadata-actions">
            <button class="ghost-button" type="button" @click="closeWorkflowMetadata">{{ t("common.cancel") }}</button>
            <button
              class="primary-button"
              type="button"
              :disabled="!canSaveWorkflowMetadata"
              @click="saveWorkflowMetadata"
            >
              {{ t("workflow.saveWorkflow") }}
            </button>
          </div>
        </section>
      </div>
    </Transition>

    <div v-if="workflowActionNote && !workflowMetadataVisible" class="action-toast" role="status" aria-live="polite">
      <span>{{ workflowActionNote }}</span>
      <button type="button" :aria-label="t('common.dismiss')" @click="workflowActionNote = ''">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>
