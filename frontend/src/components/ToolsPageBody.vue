<script setup lang="ts">
// @ts-nocheck — inject surface + slot row typing under page split (ZKL-64 item 12)
/** Tools page body: list + dialogs shell (ZKL-64 item 12). */
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import ManagementSummaryStrip from "./ManagementSummaryStrip.vue";
import ToolTestDialog from "./ToolTestDialog.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useI18n } from "vue-i18n";
import { useToolsPageContext } from "../composables/useToolsPageContext";
import { getToolTypeLabel } from "../utils/tool-presentation";

const { t } = useI18n();
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
void ToolTestDialog;
void WorkspaceContextState;
void getToolTypeLabel;
</script>

<template>
  <div class="page-grid tool-grid management-page-grid" v-loading="toolsStore.loading" @click="closeFloatingMenus">
    <ManagementPageHeader
      class="span-12"
      :title="t('tools.title')"
      :description="t('tools.subtitle')"
      :eyebrow="t('nav.section.build')"
      icon="fa-solid fa-screwdriver-wrench"
    >
      <template #actions>
        <button class="ghost-button tool-header-secondary" type="button" @click="router.push('/openapi-imports')">
          <i class="fa-solid fa-file-import" />
          <span>{{ t("common.import") }} OpenAPI</span>
        </button>
        <button
          class="primary-button tool-header-primary"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? t('tools.newTool') : t('workspaces.emptyHint')"
          @click="openCreateTool"
        >
          <i class="fa-solid fa-circle-plus" />
          <span>{{ t("tools.newTool") }}</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" compact :items="toolSummaryItems" />

    <section class="span-12 tool-runtime-card management-list-card">
      <ManagementList
        class="tool-management-list"
        :rows="hasWorkspaceContext ? toolsStore.toolPageItems : []"
        :columns="toolColumns"
        row-key="id"
        :sticky-left-keys="['tool']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:tools:columns:v2"
        :selectable="false"
        checkable
        :checked-row-keys="selectedToolRowKeys"
        :row-selection-label="(tool: Tool) => t('tools.selectTool', { name: tool.name })"
        :loading="hasWorkspaceContext && toolsStore.toolPageLoading"
        :error="hasWorkspaceContext ? toolsStore.toolPageError : undefined"
        :has-loaded="hasWorkspaceContext ? toolsStore.toolPageHasLoaded : true"
        :search="query"
        :search-placeholder="t('tools.searchPlaceholder')"
        :search-aria-label="t('tools.searchAria')"
        :reset-label="t('tools.reset')"
        :reset-aria-label="t('tools.resetAria')"
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
                ? t('tools.batchTesting')
                : !canTestWorkspace
                  ? t('tools.noTestPerm')
                  : t('tools.batchTestTitle')
            "
            @click="batchTestSelectedTools"
          >
            <i :class="['fa-solid', batchTesting ? 'fa-spinner fa-spin' : 'fa-vial']" aria-hidden="true" />
            <span>{{ batchTesting ? t("tools.testing") : t("tools.batchTest") }}</span>
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
            :title="batchForcePublishing ? t('tools.forcePublishing') : t('tools.forcePublishTitle')"
            @click="openBatchForcePublishConfirmation"
          >
            <i :class="['fa-solid', batchForcePublishing ? 'fa-spinner fa-spin' : 'fa-rocket']" aria-hidden="true" />
            <span>{{ batchForcePublishing ? t("tools.publishing") : t("tools.forcePublish") }}</span>
          </button>
          <button
            type="button"
            class="management-list-batch-action is-danger"
            :disabled="!selectedTools.length || batchTesting || batchDeleting || batchForcePublishing"
            :title="batchDeleting ? t('tools.batchDeleting') : t('tools.batchDeleteTitle')"
            @click="openBatchDeleteConfirmation"
          >
            <i :class="['fa-solid', batchDeleting ? 'fa-spinner fa-spin' : 'fa-trash']" aria-hidden="true" />
            <span>{{ t("tools.batchDelete") }}</span>
          </button>
        </template>
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="selectedStatusFilter"
            :options="statusTabs"
            :ariaLabel="t('tools.statusFilterAria')"
            @update:model-value="selectStatusFilter($event as ToolStatusFilter)"
          />
          <ManagementSegmentedFilter
            :model-value="selectedToolTypeFilter"
            :options="toolTypeTabs"
            :ariaLabel="t('tools.typeFilterAria')"
            @update:model-value="selectToolTypeFilter($event as ToolTypeFilter)"
          />
        </template>
        <template #cell-tool="{ row: tool }">
          <div class="tool-entity-cell">
            <span class="tool-entity-icon" aria-hidden="true"><i class="fa-solid fa-screwdriver-wrench" /></span>
            <span class="tool-entity-copy">
              <router-link
                class="aw-table-title tool-name-link"
                :title="tool.name"
                :to="{ name: 'tool-detail', params: { toolId: tool.id } }"
                >{{ tool.name }}</router-link
              >
              <small class="aw-table-subtitle" :title="tool.description">{{
                tool.description || t("tools.noDescription")
              }}</small>
            </span>
          </div>
        </template>
        <template #cell-type="{ row: tool }"
          ><span class="tool-type-tag aw-table-pill">{{ getToolTypeLabel(tool) }}</span></template
        >
        <template #cell-protocol="{ row: tool }"
          ><span class="tool-protocol-cell aw-table-meta">{{ toolProtocolLabel(tool) }}</span></template
        >
        <template #cell-endpoint="{ row: tool }">
          <span class="tool-endpoint-cell">
            <span class="tool-method-badge aw-table-pill" :class="methodClass(tool)">{{ methodOf(tool) }}</span>
            <code class="tool-endpoint-summary aw-table-mono" :title="toolEndpointSummary(tool)">{{
              pathOf(tool)
            }}</code>
          </span>
        </template>
        <template #cell-method="{ row: tool }"
          ><span class="tool-method-badge aw-table-pill" :class="methodClass(tool)">{{
            methodOf(tool)
          }}</span></template
        >
        <template #cell-path="{ row: tool }">
          <code class="tool-endpoint-summary aw-table-mono" :title="toolEndpointSummary(tool)">{{ pathOf(tool) }}</code>
        </template>
        <template #cell-connection="{ row: tool }">
          <span class="tool-provider-connection" :title="toolProviderConnectionLabel(tool)">
            <strong class="aw-table-title">{{ providerForTool(tool)?.name || tool.providerId || "-" }}</strong>
            <small class="aw-table-subtitle">{{ connectionForTool(tool)?.name || t("tools.connectionMissing") }}</small>
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
            :menu-label="t('tools.moreActions')"
            @action="handleToolRowAction($event, tool)"
          />
        </template>
        <template #empty>
          <WorkspaceContextState
            v-if="!hasWorkspaceContext"
            embedded-in-list
            :feature="t('tools.featureName')"
            icon="fa-solid fa-screwdriver-wrench"
            @retry="loadToolPageAssets"
          />
          <div v-else-if="!hasToolRecords" class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-box-open" /></div>
            <h2>{{ t("tools.emptyTitle") }}</h2>
            <p>{{ t("tools.emptyBody") }}</p>
            <div class="registry-empty-actions">
              <button class="primary-button" type="button" @click="openCreateTool">{{ t("tools.createTool") }}</button
              ><button class="ghost-button" type="button" @click="router.push('/openapi-imports')">
                {{ t("tools.fromOpenapi") }}
              </button>
            </div>
          </div>
          <div v-else class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-magnifying-glass" /></div>
            <h2>{{ t("tools.noMatchTitle") }}</h2>
            <p>{{ t("tools.noMatchBody") }}</p>
          </div>
        </template>
      </ManagementList>
    </section>

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
            :aria-label="t('tools.closeConfirm')"
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
          <div class="tool-impact-summary" role="list" :aria-label="t('tools.impactSummaryAria')">
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
          <label v-if="pendingRiskAction.type === 'batch-force-publish'" class="tool-force-publish-reason">
            <span>{{ t("tools.forceReasonLabel") }}</span>
            <textarea
              v-model="forcePublishReason"
              rows="3"
              maxlength="500"
              :placeholder="t('tools.forceReasonPlaceholder')"
              :disabled="batchForcePublishing"
              :aria-label="t('tools.forceReasonAria')"
            />
            <small :class="{ error: forcePublishReason.trim().length > 0 && !forcePublishReasonValid }">
              {{ forcePublishReason.trim().length }}/500
              <template v-if="forcePublishReason.trim().length > 0 && !forcePublishReasonValid">
                · {{ t("tools.forceReasonMin") }}
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
            {{ t("common.cancel") }}
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
            <i v-if="batchDeleting || batchForcePublishing" class="fa-solid fa-spinner fa-spin" aria-hidden="true" />
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
        :aria-label="t('tools.batchTestAria')"
      >
        <div class="modal-card-head tool-risk-confirmation-head">
          <div>
            <span>{{ t("tools.batchTestEyebrow") }}</span>
            <h3>{{ t("tools.batchTestHeading") }}</h3>
          </div>
          <button
            class="icon-action-button"
            type="button"
            :aria-label="t('tools.closeBatchTestAria')"
            :disabled="batchTesting"
            @click="closeBatchTestDialog"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
        <div class="tool-risk-confirmation-body">
          <p class="tool-risk-description">
            {{ t("tools.batchTestDescription", { count: selectedTools.length }) }}
          </p>
          <ul class="tool-batch-test-notes">
            <li>{{ t("tools.batchNoteDraftOnly") }}</li>
            <li>{{ t("tools.batchNoteMissingConn") }}</li>
            <li>{{ t("tools.batchNotePassthrough") }}</li>
          </ul>
          <section
            v-if="batchTestNeedsPassthrough"
            class="tool-batch-test-passthrough"
            :aria-label="t('tools.outboundCredsAria')"
          >
            <header>
              <strong>{{ t("tools.outboundPassthroughTitle") }}</strong>
              <span>{{ t("tools.outboundBatchHelp") }}</span>
            </header>
            <label>
              {{ t("tools.businessToken") }}
              <input
                v-model="batchPassthroughToken"
                type="password"
                autocomplete="off"
                :placeholder="t('tools.businessJwtPlaceholder')"
                :disabled="batchTesting"
              />
            </label>
            <label>
              {{ t("tools.expiresAt") }}
              <input v-model="batchPassthroughExpiresAt" type="datetime-local" :disabled="batchTesting" />
            </label>
          </section>
          <p v-if="batchTesting" class="tool-batch-test-progress" role="status">
            {{ t("tools.batchProgress", { current: batchTestProgress.current, total: batchTestProgress.total }) }}
          </p>
        </div>
        <div class="tool-editor-actions tool-risk-confirmation-actions">
          <button class="ghost-button" type="button" :disabled="batchTesting" @click="closeBatchTestDialog">
            {{ t("common.cancel") }}
          </button>
          <button class="primary-button" type="button" :disabled="batchTesting" @click="confirmBatchTestSelectedTools">
            <i :class="['fa-solid', batchTesting ? 'fa-spinner fa-spin' : 'fa-vial']" aria-hidden="true" />
            {{ batchTesting ? t("tools.testing") : t("tools.startTestCount", { count: selectedTools.length }) }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="actionNote" class="action-toast" :class="{ error: actionNoteTone === 'error' }">{{ actionNote }}</div>
    <ToolTestDialog v-model="testDialogVisible" :tool="testDialogTool" />
  </div>
</template>
