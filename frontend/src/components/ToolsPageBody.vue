<script setup lang="ts">
// @ts-nocheck — inject surface + slot row typing under page split (ZKL-64 item 12)
/** Tools page body: list + dialogs shell (ZKL-64 item 12). */
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import ManagementSummaryStrip from "./ManagementSummaryStrip.vue";
import ToolDetailPanel from "./ToolDetailPanel.vue";
import ToolEditorPanel from "./ToolEditorPanel.vue";
import ToolTestDialog from "./ToolTestDialog.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useToolsPageContext } from "../composables/useToolsPageContext";
import { getToolTypeLabel } from "../utils/tool-presentation";

const scp = useToolsPageContext();
const {
  toolsStore,
  router,
  query,
  selectedStatusFilter,
  selectedToolTypeFilter,
  selectedToolRowKeys,
  selectedTools,
  batchTesting,
  batchDeleting,
  batchForcePublishing,
  forcePublishReason,
  forcePublishReasonValid,
  canForcePublishTools,
  batchTestDialogVisible,
  batchTestModalRef,
  batchPassthroughToken,
  batchPassthroughExpiresAt,
  batchTestProgress,
  batchTestNeedsPassthrough,
  canTestWorkspace,
  actionNote,
  actionNoteTone,
  riskConfirmationVisible,
  riskConfirmationModalRef,
  pendingRiskAction,
  statusTabs,
  toolTypeTabs,
  hasToolRecords,
  toolSummaryItems,
  hasWorkspaceContext,
  toolColumns,
  testDialogVisible,
  testDialogTool,
  selectStatusFilter,
  selectToolTypeFilter,
  resetFilters,
  closeFloatingMenus,
  toolMenuActions,
  handleToolRowAction,
  loadToolPageAssets,
  setToolSearch,
  changeToolPage,
  changeToolSort,
  openCreateTool,
  closeRiskConfirmation,
  riskConfirmationTitle,
  riskConfirmationPrimaryLabel,
  riskConfirmationEyebrow,
  riskConfirmationDescription,
  riskImpactItems,
  riskConfirmationToneClass,
  riskConfirmationTargetName,
  riskConfirmationTargetMeta,
  openBatchDeleteConfirmation,
  openBatchForcePublishConfirmation,
  batchTestSelectedTools,
  closeBatchTestDialog,
  confirmBatchTestSelectedTools,
  confirmRiskAction,
  agentImpactLabel,
  toolVersionLabel,
  toolEndpointSummary,
  methodOf,
  pathOf,
  methodClass,
  statusClass,
  lifecycleStatus,
  governanceToneClass,
  toolUnifiedStatus,
  toolProtocolLabel,
  toolProviderConnectionLabel,
  providerForTool,
  connectionForTool,
  formatToolTableUpdatedAt,
} = scp;
void ManagementList;
void ManagementPageHeader;
void ManagementRowActions;
void ManagementSegmentedFilter;
void ManagementSummaryStrip;
void ToolDetailPanel;
void ToolEditorPanel;
void ToolTestDialog;
void WorkspaceContextState;
void getToolTypeLabel;
</script>

<template>
  <div class="page-grid tool-grid management-page-grid" v-loading="toolsStore.loading" @click="closeFloatingMenus">
    <ManagementPageHeader
      class="span-12"
      title="工具管理"
      description="管理工具契约、服务绑定、版本测试与发布状态。"
      icon="fa-solid fa-screwdriver-wrench"
    >
      <template #actions>
        <button class="ghost-button tool-header-secondary" type="button" @click="router.push('/openapi-imports')">
          <i class="fa-solid fa-file-import" />
          <span>导入 OpenAPI</span>
        </button>
        <button
          class="primary-button tool-header-primary"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? '创建工具' : '请先创建或加入业务空间'"
          @click="openCreateTool"
        >
          <i class="fa-solid fa-circle-plus" />
          <span>创建工具</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" :items="toolSummaryItems" />

    <section class="span-12 tool-runtime-card management-list-card">
      <div v-if="hasWorkspaceContext && hasToolRecords" class="tool-section-bar">
        <span
          ><i class="fa-solid fa-circle-info" />这里不再配置域名、端口和认证；这些属于服务连接。Tool
          关注业务名称、Endpoint、入参出参、重试超时和发布测试。</span
        >
        <button type="button" @click="router.push('/openapi-imports')">查看 OpenAPI 导入</button>
      </div>

      <ManagementList
        class="tool-management-list"
        :rows="hasWorkspaceContext ? toolsStore.toolPageItems : []"
        :columns="toolColumns"
        row-key="id"
        :sticky-left-keys="['tool']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:tools:columns"
        :selectable="false"
        checkable
        :checked-row-keys="selectedToolRowKeys"
        :row-selection-label="(tool: Tool) => `选择 ${tool.name}`"
        :loading="hasWorkspaceContext && toolsStore.toolPageLoading"
        :error="hasWorkspaceContext ? toolsStore.toolPageError : undefined"
        :has-loaded="hasWorkspaceContext ? toolsStore.toolPageHasLoaded : true"
        :search="query"
        search-placeholder="搜索 Tool / 连接 / 路径"
        search-aria-label="搜索 Tool、服务连接或路径"
        reset-label="重置"
        reset-aria-label="重置工具筛选"
        :pagination="toolsStore.toolPagination"
        :sort-by="toolsStore.toolListQuery?.sortBy"
        :sort-order="toolsStore.toolListQuery?.sortOrder"
        @update:checked-row-keys="selectedToolRowKeys = $event"
        @update:search="setToolSearch"
        @reset="resetFilters"
        @page-change="changeToolPage"
        @sort-change="changeToolSort"
      >
          <template #batch-actions>
            <button
              type="button"
              class="management-list-batch-action is-primary"
              :disabled="
                !selectedTools.length || batchTesting || batchDeleting || batchForcePublishing || !canTestWorkspace
              "
              :title="
                batchTesting
                  ? '批量测试进行中'
                  : !canTestWorkspace
                    ? '当前空间无测试权限'
                    : '使用默认入参批量测试已选 Tool'
              "
              @click="batchTestSelectedTools"
            >
              <i :class="['fa-solid', batchTesting ? 'fa-spinner fa-spin' : 'fa-vial']" aria-hidden="true" />
              <span>{{ batchTesting ? "测试中…" : "批量测试" }}</span>
            </button>
            <button
              v-if="canForcePublishTools"
              type="button"
              class="management-list-batch-action is-warning"
              :disabled="
                !selectedTools.some((t) => t.status !== 'Published') ||
                batchTesting ||
                batchDeleting ||
                batchForcePublishing
              "
              :title="
                batchForcePublishing
                  ? '强制发布进行中'
                  : '平台管理员：跳过实调测试，批量强制发布未发布 Tool（需服务端 allowForcePublish）'
              "
              @click="openBatchForcePublishConfirmation"
            >
              <i
                :class="['fa-solid', batchForcePublishing ? 'fa-spinner fa-spin' : 'fa-rocket']"
                aria-hidden="true"
              />
              <span>{{ batchForcePublishing ? "发布中…" : "强制发布" }}</span>
            </button>
            <button
              type="button"
              class="management-list-batch-action is-danger"
              :disabled="!selectedTools.length || batchTesting || batchDeleting || batchForcePublishing"
              :title="batchDeleting ? '批量删除进行中' : '批量删除已选 Tool'"
              @click="openBatchDeleteConfirmation"
            >
              <i :class="['fa-solid', batchDeleting ? 'fa-spinner fa-spin' : 'fa-trash']" aria-hidden="true" />
              <span>批量删除</span>
            </button>
          </template>
          <template #filters>
            <ManagementSegmentedFilter
              :model-value="selectedStatusFilter"
              :options="statusTabs"
              ariaLabel="工具状态筛选"
              @update:model-value="selectStatusFilter($event as ToolStatusFilter)"
            />
            <ManagementSegmentedFilter
              :model-value="selectedToolTypeFilter"
              :options="toolTypeTabs"
              ariaLabel="工具类型筛选"
              @update:model-value="selectToolTypeFilter($event as ToolTypeFilter)"
            />
          </template>
          <template #cell-tool="{ row: tool }">
            <div class="tool-entity-cell">
              <span class="tool-entity-icon" aria-hidden="true"><i class="fa-solid fa-screwdriver-wrench" /></span>
              <span class="tool-entity-copy">
                <strong class="aw-table-title" :title="tool.name">{{ tool.name }}</strong>
                <small class="aw-table-subtitle" :title="tool.description">{{ tool.description || "暂无描述" }}</small>
              </span>
            </div>
          </template>
          <template #cell-type="{ row: tool }"
            ><span class="tool-type-tag aw-table-pill">{{ getToolTypeLabel(tool) }}</span></template
          >
          <template #cell-protocol="{ row: tool }"
            ><span class="tool-protocol-cell aw-table-meta">{{ toolProtocolLabel(tool) }}</span></template
          >
          <template #cell-method="{ row: tool }"
            ><span class="tool-method-badge aw-table-pill" :class="methodClass(tool)">{{
              methodOf(tool)
            }}</span></template
          >
          <template #cell-path="{ row: tool }">
            <code class="tool-endpoint-summary aw-table-mono" :title="toolEndpointSummary(tool)">{{
              pathOf(tool)
            }}</code>
          </template>
          <template #cell-connection="{ row: tool }">
            <span class="tool-provider-connection" :title="toolProviderConnectionLabel(tool)">
              <strong class="aw-table-title">{{ providerForTool(tool)?.name || tool.providerId || "-" }}</strong>
              <small class="aw-table-subtitle">{{ connectionForTool(tool)?.name || "连接缺失" }}</small>
            </span>
          </template>
          <template #cell-status="{ row: tool }">
            <div
              class="tool-unified-status-cell"
              :title="toolUnifiedStatus(tool).description"
              :aria-label="toolUnifiedStatus(tool).description"
            >
              <span
                class="tool-status-pill aw-table-pill"
                :class="[statusClass(tool.status), governanceToneClass(lifecycleStatus(tool).tone)]"
              >
                <i aria-hidden="true" />{{ toolUnifiedStatus(tool).lifecycleLabel }}
              </span>
              <small
                v-if="toolUnifiedStatus(tool).runLabel"
                class="tool-status-attention"
                :class="governanceToneClass(toolUnifiedStatus(tool).tone)"
              >
                {{ toolUnifiedStatus(tool).runLabel }}
              </small>
            </div>
          </template>
          <template #cell-version="{ row: tool }"
            ><code class="tool-version-cell aw-table-mono">{{ toolVersionLabel(tool) }}</code></template
          >
          <template #cell-updatedAt="{ row: tool }"
            ><span class="tool-updated-cell aw-table-meta">{{ formatToolTableUpdatedAt(tool) }}</span></template
          >
          <template #cell-actions="{ row: tool }">
            <ManagementRowActions
              :menu-actions="toolMenuActions(tool)"
              menu-label="更多工具操作"
              @action="handleToolRowAction($event, tool)"
            />
          </template>
          <template #empty>
            <WorkspaceContextState
              v-if="!hasWorkspaceContext"
              embedded-in-list
              feature="工具管理"
              icon="fa-solid fa-screwdriver-wrench"
              @retry="loadToolPageAssets"
            />
            <div
              v-else-if="!hasToolRecords"
              class="empty-state registry-empty-state management-registry-empty-state"
            >
              <div class="management-empty-state-icon"><i class="fa-solid fa-box-open" /></div>
              <h2>暂无工具</h2>
              <p>可以注册 Tool，或者从 OpenAPI 导入生成草稿。</p>
              <div class="registry-empty-actions">
                <button class="primary-button" type="button" @click="openCreateTool">新建 Tool</button
                ><button class="ghost-button" type="button" @click="router.push('/openapi-imports')">
                  从 OpenAPI 生成
                </button>
              </div>
            </div>
            <div v-else class="empty-state registry-empty-state management-registry-empty-state">
              <div class="management-empty-state-icon"><i class="fa-solid fa-magnifying-glass" /></div>
              <h2>没有匹配的工具</h2>
              <p>调整工具名称、状态或路径关键词后再试。</p>
            </div>
          </template>
        </ManagementList>
    </section>

    <ToolDetailPanel />
    <ToolEditorPanel />

    <div v-if="riskConfirmationVisible" class="modal-backdrop" @click.self="closeRiskConfirmation">
      <section
        ref="riskConfirmationModalRef"
        class="modal-card tool-risk-confirmation-modal"
        :class="riskConfirmationToneClass()"
        role="dialog"
        aria-modal="true"
        :aria-label="riskConfirmationTitle()"
      >
        <div class="modal-card-head tool-risk-confirmation-head">
          <div>
            <span>{{ riskConfirmationEyebrow() }}</span>
            <h3>{{ riskConfirmationTitle() }}</h3>
          </div>
          <button
            class="icon-action-button"
            type="button"
            aria-label="关闭确认对话框"
            data-modal-initial-focus
            @click="closeRiskConfirmation"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="tool-risk-confirmation-body">
          <div class="tool-risk-target">
            <div class="tool-risk-target-icon" aria-hidden="true">
              <i class="fa-solid fa-wrench" />
            </div>
            <div class="tool-risk-target-copy">
              <strong>{{ riskConfirmationTargetName() }}</strong>
              <small v-if="riskConfirmationTargetMeta()">{{ riskConfirmationTargetMeta() }}</small>
            </div>
          </div>
          <p class="tool-risk-description">{{ riskConfirmationDescription() }}</p>
          <div class="tool-impact-summary" role="list" aria-label="影响面摘要">
            <div
              v-for="item in riskImpactItems()"
              :key="item.key"
              class="tool-impact-item"
              :class="`tone-${item.tone}`"
              role="listitem"
            >
              <span class="tool-impact-label">{{ item.label }}</span>
              <strong class="tool-impact-value">{{ item.value }}</strong>
            </div>
          </div>
          <label
            v-if="pendingRiskAction.type === 'batch-force-publish'"
            class="tool-force-publish-reason"
          >
            <span>强制发布原因（必填，至少 8 个字符）</span>
            <textarea
              v-model="forcePublishReason"
              rows="3"
              maxlength="500"
              placeholder="例如：生产环境无法安全实调 DELETE 类接口，已在预发完成契约核对。"
              :disabled="batchForcePublishing"
              aria-label="强制发布原因"
            />
            <small :class="{ error: forcePublishReason.trim().length > 0 && !forcePublishReasonValid }">
              {{ forcePublishReason.trim().length }}/500
              <template v-if="forcePublishReason.trim().length > 0 && !forcePublishReasonValid">
                · 至少 8 个字符
              </template>
            </small>
          </label>
        </div>
        <div class="tool-editor-actions tool-risk-confirmation-actions">
          <button
            class="ghost-button"
            type="button"
            :disabled="batchDeleting || batchForcePublishing"
            @click="closeRiskConfirmation"
          >
            取消
          </button>
          <button
            class="primary-button"
            :class="pendingRiskAction.type === 'enable' ? '' : 'danger'"
            type="button"
            :disabled="
              batchDeleting ||
              batchForcePublishing ||
              (pendingRiskAction.type === 'batch-force-publish' && !forcePublishReasonValid)
            "
            @click="confirmRiskAction"
          >
            <i
              v-if="batchDeleting || batchForcePublishing"
              class="fa-solid fa-spinner fa-spin"
              aria-hidden="true"
            />
            {{ riskConfirmationPrimaryLabel() }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="batchTestDialogVisible" class="modal-backdrop" @click.self="closeBatchTestDialog">
      <section
        ref="batchTestModalRef"
        class="modal-card tool-risk-confirmation-modal tool-batch-test-modal"
        role="dialog"
        aria-modal="true"
        aria-label="批量测试 Tool"
      >
        <div class="modal-card-head tool-risk-confirmation-head">
          <div>
            <span>批量测试</span>
            <h3>用默认入参测试已选 Tool</h3>
          </div>
          <button
            class="icon-action-button"
            type="button"
            aria-label="关闭批量测试"
            :disabled="batchTesting"
            @click="closeBatchTestDialog"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="tool-risk-confirmation-body">
          <p class="tool-risk-description">
            将对 <strong>{{ selectedTools.length }}</strong> 个已选 Tool 顺序执行测试：自动补全 Path/Query
            默认参数（如 pageNum=1、pageSize=10），结果仅汇总通过/失败数量。
          </p>
          <ul class="tool-batch-test-notes">
            <li>仅测试可编辑草稿（已发布且无新草稿的会跳过）</li>
            <li>缺少连接的 Tool 会跳过</li>
            <li>透传连接需填写一次业务 Token，应用到全部透传项</li>
          </ul>
          <section v-if="batchTestNeedsPassthrough" class="tool-batch-test-passthrough" aria-label="出站透传凭据">
            <header>
              <strong>出站请求透传</strong>
              <span>Token 为 write-only，不会写入历史或本地存储。请勿加 Bearer 前缀。</span>
            </header>
            <label>
              业务 Token
              <input
                v-model="batchPassthroughToken"
                type="password"
                autocomplete="off"
                placeholder="一次性业务 JWT"
                :disabled="batchTesting"
              />
            </label>
            <label>
              过期时间
              <input v-model="batchPassthroughExpiresAt" type="datetime-local" :disabled="batchTesting" />
            </label>
          </section>
          <p v-if="batchTesting" class="tool-batch-test-progress" role="status">
            测试中… {{ batchTestProgress.current }} / {{ batchTestProgress.total }}
          </p>
        </div>
        <div class="tool-editor-actions tool-risk-confirmation-actions">
          <button class="ghost-button" type="button" :disabled="batchTesting" @click="closeBatchTestDialog">
            取消
          </button>
          <button class="primary-button" type="button" :disabled="batchTesting" @click="confirmBatchTestSelectedTools">
            <i :class="['fa-solid', batchTesting ? 'fa-spinner fa-spin' : 'fa-vial']" aria-hidden="true" />
            {{ batchTesting ? "测试中…" : `开始测试 ${selectedTools.length} 项` }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="actionNote" class="action-toast" :class="{ error: actionNoteTone === 'error' }">{{ actionNote }}</div>
    <ToolTestDialog v-model="testDialogVisible" :tool="testDialogTool" />
  </div>
</template>
