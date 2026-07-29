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
import { useWorkflowPageContext } from "../composables/useWorkflowPageContext";

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
      title="编排"
      description="设计、校验、试跑与发布业务流程。"
      icon="fa-solid fa-diagram-project"
    >
      <template #actions>
        <button
          class="primary-button workflow-create-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? '新建编排' : '请先创建或加入业务空间'"
          @click="openCreateWorkflow"
        >
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          <span>新建编排</span>
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
        search-placeholder="搜索流程名称 / Slug / 状态..."
        search-aria-label="搜索流程名称、Slug 或状态"
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
            <small class="aw-table-subtitle" :title="workflow.description || '还没有填写用途说明'">{{
              workflow.description || "还没有填写用途说明"
            }}</small>
          </div>
        </template>
        <template #cell-workspace="{ row: workflow }"
          ><span class="workflow-workspace-cell aw-table-meta" :title="workflowWorkspaceLabel(workflow)">{{
            workflowWorkspaceLabel(workflow)
          }}</span></template
        >
        <template #cell-nodes="{ row: workflow }"
          ><span class="workflow-mono-cell aw-table-meta"
            >{{ workflowNodeCount(workflow) }} 步 / {{ workflowEdgeCount(workflow) }} 连接</span
          ></template
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
            menu-label="更多编排操作"
            @action="handleWorkflowRowAction($event, workflow)"
          />
        </template>
        <template #empty>
          <WorkspaceContextState
            v-if="!hasWorkspaceContext"
            embedded-in-list
            feature="流程编排"
            icon="fa-solid fa-diagram-project"
            @retry="loadWorkflowPageAssets"
          />
          <div v-else class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon">
              <i class="fa-solid fa-diagram-project" aria-hidden="true" />
            </div>
            <h2>{{ workflowStore.workflows.length ? "没有匹配到编排流程" : "暂无编排流程" }}</h2>
            <p>
              {{
                workflowStore.workflows.length
                  ? "换个关键词试试，或者清空筛选条件。"
                  : "新建第一个编排后，可以在这里查看用途、步骤、校验结果和最近执行。"
              }}
            </p>
            <button
              v-if="workflowStore.workflows.length"
              class="ghost-button"
              type="button"
              @click="clearWorkflowSearch"
            >
              清空筛选
            </button>
            <button v-else class="primary-button" type="button" @click="openCreateWorkflow">新建编排</button>
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
          aria-label="流程详情"
          tabindex="-1"
          @keydown.esc.stop.prevent="closeWorkflowDetail"
          @keydown.tab="trapModalFocus($event, workflowDetailModalRef)"
        >
          <div class="modal-card-head">
            <div>
              <span>Workflow Lifecycle</span>
              <h3>流程详情</h3>
            </div>
            <button class="icon-action-button" type="button" aria-label="收起流程详情" @click="closeWorkflowDetail">
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
                <p>{{ selectedWorkflow.description || "这个流程还没有填写用途说明。" }}</p>
              </div>

              <div class="workflow-detail-metrics">
                <span>
                  <strong>{{ workflowNodeCount(selectedWorkflow) }}</strong>
                  <small>步骤</small>
                </span>
                <span>
                  <strong>{{ workflowExecutionCount(selectedWorkflow) }}</strong>
                  <small>执行记录</small>
                </span>
                <span>
                  <strong>{{ validationLabel(selectedWorkflow) }}</strong>
                  <small>校验状态</small>
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
                <h4>这个流程做什么</h4>
                <p>{{ selectedWorkflow.description || "用于把多个业务动作串起来，减少人工重复处理。" }}</p>
              </div>

              <div class="workflow-readable-section">
                <h4>什么时候触发</h4>
                <p>{{ workflowTriggerText() }}</p>
              </div>

              <div class="workflow-readable-section">
                <h4>包含哪些步骤</h4>
                <div v-if="selectedWorkflowSteps.length" class="workflow-step-list">
                  <article v-for="step in selectedWorkflowSteps" :key="step.id" class="workflow-step-item">
                    <b>{{ step.order }}</b>
                    <span>
                      <strong>{{ step.title }}</strong>
                      <small>{{ step.type }} · {{ step.detail }}</small>
                    </span>
                  </article>
                </div>
                <p v-else>当前还没有可展示的步骤快照。进入流程图编辑器并保存草稿后，这里会显示主路径步骤。</p>
              </div>

              <div class="workflow-readable-section">
                <h4>最近执行</h4>
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
                <h4>试运行轨迹</h4>
                <WorkflowExecutionTracePanel
                  :execution="activeTraceExecution"
                  :selected-node-id="selectedNodeId"
                  @select-node="selectTraceNode"
                />
              </div>
            </section>
          </div>

          <div class="workflow-detail-actions">
            <button class="ghost-button" type="button" @click="closeWorkflowDetail">关闭</button>
            <button
              class="ghost-button"
              type="button"
              :disabled="!selectedWorkflow"
              @click="selectedWorkflow && validateWorkflow(selectedWorkflow)"
            >
              校验
            </button>
            <button
              class="ghost-button"
              type="button"
              :disabled="!selectedWorkflow"
              @click="selectedWorkflow && openTrialRunDialog(selectedWorkflow)"
            >
              试运行
            </button>
            <button class="ghost-button" type="button" :disabled="!selectedWorkflow" @click="openEditWorkflow()">
              编辑信息
            </button>
            <button
              class="ghost-button"
              type="button"
              :disabled="!selectedWorkflow || !selectedWorkflowCanPublish"
              @click="publishWorkflow()"
            >
              发布
            </button>
            <button
              v-if="selectedWorkflow && workspaces.can(selectedWorkflow.workspaceId, auth.user?.id || '', 'EDIT')"
              class="primary-button"
              type="button"
              :disabled="!selectedWorkflow || editorDraftLoadState === 'loading'"
              :aria-busy="editorDraftLoadState === 'loading' ? 'true' : undefined"
              @click="openWorkflowEditor()"
            >
              编辑流程图
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
          :aria-label="workflowMetadataMode === 'create' ? '新建编排' : '编辑编排'"
          tabindex="-1"
          @keydown.esc.stop.prevent="closeWorkflowMetadata"
          @keydown.tab="trapModalFocus($event, workflowMetadataModalRef)"
        >
          <div class="modal-card-head">
            <div>
              <span>Workflow Metadata</span>
              <h3>{{ workflowMetadataMode === "create" ? "新建编排" : "编辑编排" }}</h3>
            </div>
            <button
              class="icon-action-button"
              type="button"
              :aria-label="workflowMetadataMode === 'create' ? '收起新建编排' : '收起编辑编排'"
              @click="closeWorkflowMetadata"
            >
              <i class="fa-solid fa-xmark" />
            </button>
          </div>
          <div class="workflow-metadata-body">
            <div class="workflow-create-guide">
              <span><b>1</b> 基本信息</span>
              <span><b>2</b> 触发方式</span>
              <span><b>3</b> 步骤确认</span>
              <span><b>4</b> 保存发布</span>
            </div>
            <label class="drawer-field">
              <span>名称 <b class="field-required-mark">*</b></span>
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
                <span>业务空间 <b class="field-required-mark">*</b></span>
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
                <span>Slug</span>
                <input v-model="workflowDraft.slug" placeholder="留空时按名称生成" />
              </label>
            </div>
            <label class="drawer-field">
              <span>状态</span>
              <AppSelect v-model="workflowDraft.status" :options="workflowStatusOptions" />
            </label>
            <label class="drawer-field"
              ><span>这个流程做什么</span
              ><textarea v-model="workflowDraft.description" class="workflow-description-input" rows="4" />
            </label>
            <div class="drawer-schema-preview">
              <div>
                <i class="fa-solid fa-diagram-project" />
                <span>
                  <strong>{{ editorGraph.nodes.length }} 个步骤 / {{ editorGraph.edges.length }} 条连接</strong>
                  <small>保存后可在详情中查看，也可以进入编辑流程图调整步骤。</small>
                </span>
              </div>
            </div>
          </div>

          <div class="workflow-metadata-actions">
            <button class="ghost-button" type="button" @click="closeWorkflowMetadata">取消</button>
            <button
              class="primary-button"
              type="button"
              :disabled="!canSaveWorkflowMetadata"
              @click="saveWorkflowMetadata"
            >
              保存编排
            </button>
          </div>
        </section>
      </div>
    </Transition>

    <div v-if="workflowActionNote && !workflowMetadataVisible" class="action-toast" role="status" aria-live="polite">
      <span>{{ workflowActionNote }}</span>
      <button type="button" aria-label="隐藏提示" @click="workflowActionNote = ''">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>
