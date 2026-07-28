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
  confirmRiskAction,
  agentImpactLabel,
  toolVersionLabel,
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
  toolEndpointSummary,
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
      <WorkspaceContextState
        v-if="!hasWorkspaceContext"
        feature="工具管理"
        icon="fa-solid fa-screwdriver-wrench"
        @retry="loadToolPageAssets"
      />
      <template v-else>
        <div v-if="hasToolRecords" class="tool-section-bar">
          <span
            ><i class="fa-solid fa-circle-info" />这里不再配置域名、端口和认证；这些属于服务连接。Tool
            关注业务名称、Endpoint、入参出参、重试超时和发布测试。</span
          >
          <button type="button" @click="router.push('/openapi-imports')">查看 OpenAPI 导入</button>
        </div>

        <ManagementList
          class="tool-management-list"
          :rows="toolsStore.toolPageItems"
          :columns="toolColumns"
          row-key="id"
          :sticky-left-keys="['tool']"
          :sticky-right-keys="['actions']"
          storage-key="actweave:tools:columns"
          :selectable="false"
          checkable
          :checked-row-keys="selectedToolRowKeys"
          :row-selection-label="(tool: Tool) => `选择 ${tool.name}`"
          :loading="toolsStore.toolPageLoading"
          :error="toolsStore.toolPageError"
          :has-loaded="toolsStore.toolPageHasLoaded"
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
            <div v-if="!hasToolRecords" class="empty-state registry-empty-state management-registry-empty-state">
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
      </template>
    </section>

    <ToolDetailPanel />
    <ToolEditorPanel />

    <div v-if="riskConfirmationVisible" class="modal-backdrop" @click.self="closeRiskConfirmation">
      <section
        ref="riskConfirmationModalRef"
        class="modal-card tool-risk-confirmation-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="riskConfirmationTitle()"
      >
        <div class="modal-card-head">
          <div>
            <span>Risk Control</span>
            <h3>{{ riskConfirmationTitle() }}</h3>
          </div>
          <button
            class="icon-action-button"
            type="button"
            aria-label="关闭风险确认"
            data-modal-initial-focus
            @click="closeRiskConfirmation"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="tool-risk-confirmation-body">
          <strong>{{ pendingRiskAction.tool?.name }}</strong>
          <p>该操作可能影响已发布 Release 的 Capability Binding 或 Workflow 引用；请先确认影响面。</p>
          <div class="tool-impact-summary">
            <span
              ><b>Capability Binding</b
              >{{ pendingRiskAction.tool ? agentImpactLabel(pendingRiskAction.tool) : "-" }}</span
            >
            <span><b>Workflow 引用</b>由发布态 Release 解析</span>
            <span><b>版本</b>{{ pendingRiskAction.tool ? toolVersionLabel(pendingRiskAction.tool) : "-" }}</span>
          </div>
        </div>
        <div class="tool-editor-actions">
          <button class="ghost-button" type="button" @click="closeRiskConfirmation">取消</button>
          <button class="primary-button danger" type="button" @click="confirmRiskAction">
            {{ riskConfirmationPrimaryLabel() }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="actionNote" class="action-toast" :class="{ error: actionNoteTone === 'error' }">{{ actionNote }}</div>
    <ToolTestDialog v-model="testDialogVisible" :tool="testDialogTool" />
  </div>
</template>
