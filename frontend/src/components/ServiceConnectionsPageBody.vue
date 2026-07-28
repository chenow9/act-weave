<script setup lang="ts">
// @ts-nocheck — inject surface + slot row typing under page split (ZKL-64 item 11)
/** Service Connections page body (ZKL-64 item 11). */
import AppSelect from "./AppSelect.vue";
import ConnectionDetailPanel from "./ConnectionDetailPanel.vue";
import ConnectionFormPanel from "./ConnectionFormPanel.vue";
import ManagementList from "./ManagementList.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useServiceConnectionsPageContext } from "../composables/useServiceConnectionsPageContext";

/* eslint-disable @typescript-eslint/no-unused-vars -- inject surface for template */
const scp = useServiceConnectionsPageContext();
const {
  mode,
  connectionsStore,
  router,
  hasWorkspaceContext,
  query,
  connectionStatusFilter,
  connectionMigrationFilter,
  connectionModeFilter,
  connectionsHasLoaded,
  connectionListLoading,
  connectionLoadError,
  mobileConnectionActionMenuId,
  connectionCurrentView,
  actionNote,
  actionToastTone,
  pendingDeleteConnection,
  deleteConfirmName,
  deleteError,
  deletingConnection,
  discardDialogVisible,
  connectionStatusOptions,
  connectionMigrationOptions,
  connectionModeOptions,
  readyProviderCount,
  migrationRequiredCount,
  connectionColumns,
  hasConnectionRecords,
  filteredConnectionRows,
  loadConnections,
  retryLoadConnections,
  resetConnectionFilters,
  updateConnectionSearch,
  updateConnectionStatusFilter,
  updateConnectionMigrationFilter,
  updateConnectionModeFilter,
  changeConnectionSort,
  changeConnectionPage,
  connectionMenuActions,
  handleConnectionRowAction,
  toggleMobileConnectionActions,
  closeConnectionFloatingMenus,
  openMobileConnectionPreview,
  openMobileConnectionEditor,
  verifyMobileConnection,
  requestMobileRemoveConnection,
  dismissActionNote,
  copyConnectionText,
  isConnectionVerifying,
  statusLabel,
  statusPillClass,
  statusDotClass,
  lastVerified,
  lastVerifiedTitle,
  connectionAddress,
  connectionAddressPrimary,
  verificationMethodLabel,
  verificationPathLabel,
  outboundModeLabel,
  openCreateConnection,
  keepEditingConnectionForm,
  discardConnectionFormChanges,
  closeDeleteDialog,
  requestCloseDeleteDialog,
  confirmRemoveConnection,
  environmentLabel,
  authModeLabel,
} = scp;
/* eslint-enable @typescript-eslint/no-unused-vars */

void AppSelect;
void ConnectionDetailPanel;
void ConnectionFormPanel;
void ManagementList;
void ManagementRowActions;
void ManagementSegmentedFilter;
void WorkspaceContextState;
</script>

<template>
  <div
    class="service-connections-page"
    :class="connectionCurrentView === 'list' ? ['management-page-grid', 'management-page-grid--two-rows'] : []"
    @click="closeConnectionFloatingMenus"
  >
    <template v-if="connectionCurrentView === 'list'">
      <header class="connection-page-header">
        <div>
          <span class="connection-eyebrow">Integration Access</span>
          <h1>服务连接</h1>
          <p>Provider 管理协议与端点，Connection 只管理账号身份、授权范围与 Secret 引用；凭据明文不进入页面状态。</p>
        </div>
        <div class="connection-header-actions">
          <button
            class="ghost-button"
            type="button"
            :disabled="!hasWorkspaceContext"
            :title="hasWorkspaceContext ? '注册 Provider' : '请先创建或加入业务空间'"
            @click.stop="router.push('/providers')"
          >
            <i class="fa-solid fa-server" />
            管理 Provider
          </button>
          <button
            class="primary-button"
            type="button"
            :disabled="!hasWorkspaceContext || !readyProviderCount"
            :title="
              !hasWorkspaceContext
                ? '请先创建或加入业务空间'
                : readyProviderCount
                  ? '新建服务连接'
                  : '请先完成 Provider 的端点与认证契约配置'
            "
            @click.stop="openCreateConnection"
          >
            <i class="fa-solid fa-circle-plus" />
            新建服务连接
          </button>
        </div>
      </header>

      <section class="connection-reference-table-card management-list-card">
        <WorkspaceContextState
          v-if="!hasWorkspaceContext"
          feature="Provider 与服务连接"
          icon="fa-solid fa-plug-circle-xmark"
          @retry="loadConnections"
        />
        <div
          v-if="hasWorkspaceContext && migrationRequiredCount > 0"
          class="connection-migration-banner"
          role="status"
          data-testid="connection-migration-banner"
        >
          <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
          <div>
            <strong>有 {{ migrationRequiredCount }} 个连接需完成出站身份迁移</strong>
            <p>
              硬切后旧共享账号连接为 DISABLED + MIGRATION_REQUIRED，不可执行；请 OWNER/ADMIN 打开编辑并选择 Broker/OBO
              或请求透传。
            </p>
          </div>
          <button
            type="button"
            class="connection-secondary-button"
            @click="updateConnectionMigrationFilter('MIGRATION_REQUIRED')"
          >
            只看待迁移
          </button>
        </div>
        <ManagementList
          v-if="hasWorkspaceContext"
          class="connection-management-list"
          :rows="filteredConnectionRows"
          :columns="connectionColumns"
          row-key="id"
          :sticky-left-keys="['name']"
          :sticky-right-keys="['actions']"
          storage-key="actweave:service-connections:columns-v2"
          :selectable="false"
          :loading="connectionListLoading"
          :error="connectionLoadError"
          :has-loaded="connectionsHasLoaded"
          :search="query"
          :pagination="connectionsStore.serviceConnectionPagination"
          :sort-by="connectionsStore.serviceConnectionListQuery?.sortBy"
          :sort-order="connectionsStore.serviceConnectionListQuery?.sortOrder"
          search-placeholder="搜索连接 / 域名 / IP / 策略"
          search-aria-label="搜索服务连接"
          clear-search-aria-label="清除服务连接搜索"
          :reset-disabled="
            !query &&
            connectionStatusFilter === 'ALL' &&
            connectionMigrationFilter === 'ALL' &&
            connectionModeFilter === 'ALL'
          "
          @update:search="updateConnectionSearch"
          @reset="resetConnectionFilters"
          @page-change="changeConnectionPage"
          @sort-change="changeConnectionSort"
        >
          <template #filters>
            <div class="connection-management-filters">
              <ManagementSegmentedFilter
                :model-value="connectionStatusFilter"
                :options="connectionStatusOptions"
                ariaLabel="服务连接状态筛选"
                @update:model-value="updateConnectionStatusFilter"
              />
              <ManagementSegmentedFilter
                :model-value="connectionMigrationFilter"
                :options="connectionMigrationOptions"
                ariaLabel="迁移状态筛选"
                @update:model-value="updateConnectionMigrationFilter"
              />
              <ManagementSegmentedFilter
                :model-value="connectionModeFilter"
                :options="connectionModeOptions"
                ariaLabel="身份策略筛选"
                @update:model-value="updateConnectionModeFilter"
              />
            </div>
          </template>

          <template #cell-protocol="{ row: connection }">
            <span class="connection-protocol-pill aw-table-pill">{{ connection.protocol || "HTTP" }}</span>
          </template>

          <template #cell-environment="{ row: connection }">
            <span
              class="connection-environment-value aw-table-pill"
              :class="{ test: environmentLabel(connection.environment) === '测试' }"
            >
              {{ environmentLabel(connection.environment) }}
            </span>
          </template>

          <template #cell-name="{ row: connection }">
            <div class="connection-name-cell">
              <div class="connection-table-icon"><i class="fa-solid fa-plug" /></div>
              <div>
                <strong
                  class="aw-table-title"
                  tabindex="0"
                  :title="connection.name"
                  :aria-label="`完整连接名称：${connection.name}`"
                  >{{ connection.name }}</strong
                >
                <span class="aw-table-subtitle"
                  >{{ environmentLabel(connection.environment) }} · {{ outboundModeLabel(connection) }}</span
                >
              </div>
            </div>
          </template>

          <template #cell-outboundMode="{ row: connection }">
            <span
              class="connection-outbound-mode aw-table-pill"
              :class="{
                broker: connection.outboundMode === 'BROKER_OBO',
                passthrough: connection.outboundMode === 'REQUEST_PASSTHROUGH',
                migrate: connection.migrationState === 'MIGRATION_REQUIRED' && !connection.outboundMode,
              }"
              :title="outboundModeLabel(connection)"
            >
              {{ outboundModeLabel(connection) }}
            </span>
          </template>

          <template #cell-migrationState="{ row: connection }">
            <span
              v-if="connection.migrationState === 'MIGRATION_REQUIRED'"
              class="connection-migration-badge"
              data-testid="migration-required-badge"
              >需迁移</span
            >
            <span v-else class="aw-table-meta">—</span>
          </template>

          <template #cell-address="{ row: connection }">
            <div class="connection-address-cell">
              <div class="connection-table-icon" aria-hidden="true"><i class="fa-solid fa-link" /></div>
              <div class="connection-address-body">
                <div class="connection-address-title-row">
                  <strong
                    class="aw-table-title connection-address-host"
                    tabindex="0"
                    :title="connectionAddress(connection)"
                    :aria-label="`完整服务地址：${connectionAddress(connection)}`"
                  >
                    {{ connectionAddressPrimary(connection).hostPort
                    }}<template v-if="connectionAddressPrimary(connection).basePath">{{
                      connectionAddressPrimary(connection).basePath
                    }}</template>
                  </strong>
                  <button
                    class="connection-copy-button"
                    type="button"
                    :aria-label="`复制 ${connection.name} 服务地址`"
                    @click.stop="copyConnectionText(connectionAddress(connection), '服务地址')"
                  >
                    <i class="fa-regular fa-copy" />
                  </button>
                </div>
                <span
                  class="aw-table-subtitle connection-address-verify"
                  :title="`${verificationMethodLabel(connection)} ${verificationPathLabel(connection)}`"
                >
                  验证 · {{ verificationMethodLabel(connection) }} {{ verificationPathLabel(connection) }}
                </span>
              </div>
            </div>
          </template>

          <template #cell-status="{ row: connection }">
            <div class="connection-status-stack">
              <span class="connection-status-pill aw-table-pill" :class="statusPillClass(connection)">
                <span class="connection-status-dot" :class="statusDotClass(connection)" />
                {{ statusLabel(connection) }}
              </span>
              <span class="aw-table-meta" :title="lastVerifiedTitle(connection)">{{ lastVerified(connection) }}</span>
            </div>
          </template>

          <template #cell-actions="{ row: connection }">
            <ManagementRowActions
              :menu-actions="connectionMenuActions(connection)"
              menu-label="更多操作"
              @action="handleConnectionRowAction($event, connection)"
            />
          </template>

          <template #card="{ row: connection }">
            <article class="connection-mobile-card">
              <header>
                <div class="connection-name-cell">
                  <div class="connection-table-icon"><i class="fa-solid fa-plug" /></div>
                  <div>
                    <strong :title="connection.name">{{ connection.name }}</strong>
                    <span
                      >{{ environmentLabel(connection.environment) }} ·
                      {{ authModeLabel(connection.authConfig.mode, connection.authConfig.label) }}</span
                    >
                  </div>
                </div>
                <button
                  class="connection-mobile-actions-toggle"
                  type="button"
                  :aria-label="`${connection.name}连接操作`"
                  :aria-expanded="mobileConnectionActionMenuId === connection.id"
                  @click.stop="toggleMobileConnectionActions(connection.id)"
                >
                  <i class="fa-solid fa-ellipsis" />
                </button>
              </header>
              <div class="connection-mobile-address">
                <code :title="connectionAddress(connection)">{{ connectionAddress(connection) }}</code>
                <button
                  type="button"
                  :aria-label="`复制 ${connection.name} 服务地址`"
                  @click.stop="copyConnectionText(connectionAddress(connection), '服务地址')"
                >
                  <i class="fa-regular fa-copy" />
                </button>
              </div>
              <dl>
                <div>
                  <dt>验证接口</dt>
                  <dd>
                    {{ connection.protocolConfig.verificationMethod || "GET" }} {{ verificationPathLabel(connection) }}
                  </dd>
                </div>
                <div>
                  <dt>状态</dt>
                  <dd>
                    <span class="connection-status-pill" :class="statusPillClass(connection)">{{
                      statusLabel(connection)
                    }}</span>
                  </dd>
                </div>
              </dl>
              <div
                v-if="mobileConnectionActionMenuId === connection.id"
                class="connection-mobile-actions-menu"
                role="menu"
                :aria-label="`${connection.name}连接操作`"
              >
                <button type="button" role="menuitem" @click.stop="openMobileConnectionPreview(connection)">
                  <i class="fa-solid fa-eye" />查看详情
                </button>
                <button type="button" role="menuitem" @click.stop="openMobileConnectionEditor(connection)">
                  <i class="fa-solid fa-pen-to-square" />编辑连接
                </button>
                <button
                  type="button"
                  role="menuitem"
                  :aria-busy="isConnectionVerifying(connection.id) ? 'true' : 'false'"
                  :disabled="isConnectionVerifying(connection.id)"
                  @click.stop="verifyMobileConnection(connection)"
                >
                  <i
                    :class="isConnectionVerifying(connection.id) ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-vial'"
                  />验证连接
                </button>
                <button
                  class="danger"
                  type="button"
                  role="menuitem"
                  @click.stop="requestMobileRemoveConnection(connection)"
                >
                  <i class="fa-solid fa-trash-can" />删除连接
                </button>
              </div>
            </article>
          </template>

          <template #error="{ error }">
            <div class="connection-load-error" role="alert">
              <div><i class="fa-solid fa-triangle-exclamation" /></div>
              <h4>服务连接加载失败</h4>
              <p>{{ error }}</p>
              <button type="button" aria-label="重试加载服务连接" @click.stop="retryLoadConnections">重试</button>
            </div>
          </template>

          <template #empty>
            <div class="connection-empty-state">
              <div><i class="fa-solid fa-plug-circle-xmark" /></div>
              <h4>{{ hasConnectionRecords ? "没有匹配连接" : "暂无服务连接" }}</h4>
              <p>
                {{
                  hasConnectionRecords
                    ? "调整连接名称、域名/IP 或认证方式关键词"
                    : "先创建服务连接，再让 Tool 引用它配置业务动作。"
                }}
              </p>
              <button v-if="hasConnectionRecords" type="button" @click.stop="resetConnectionFilters">
                重置查询条件
              </button>
              <button v-else type="button" @click.stop="openCreateConnection">新建服务连接</button>
            </div>
          </template>
        </ManagementList>
      </section>
    </template>

    <ConnectionDetailPanel />

    <ConnectionFormPanel />

    <section
      v-if="discardDialogVisible"
      class="connection-discard-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="connection-discard-title"
    >
      <div class="connection-delete-backdrop" @click.stop="keepEditingConnectionForm" />
      <div class="connection-delete-dialog connection-discard-dialog" @click.stop>
        <header>
          <span class="connection-eyebrow">Unsaved Changes</span>
          <h2 id="connection-discard-title">放弃未保存修改？</h2>
          <p>当前服务连接表单已有修改。放弃后，这些内容不会保存，也不会用于后续验证。</p>
        </header>
        <footer>
          <button class="connection-secondary-button" type="button" @click.stop="keepEditingConnectionForm">
            继续编辑
          </button>
          <button class="connection-danger-button" type="button" @click.stop="discardConnectionFormChanges">
            <i class="fa-solid fa-trash" />
            放弃修改
          </button>
        </footer>
      </div>
    </section>

    <section
      v-if="pendingDeleteConnection"
      class="connection-delete-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="connection-delete-title"
    >
      <div class="connection-delete-backdrop" @click.stop="requestCloseDeleteDialog('backdrop')" />
      <div class="connection-delete-dialog" @click.stop>
        <header>
          <span class="connection-eyebrow">Danger Zone</span>
          <h2 id="connection-delete-title">删除服务连接</h2>
          <p>删除后所有引用该 Connection 的 Binding 或默认连接都会被后端完整性规则校验；页面不提交手工引用计数。</p>
        </header>
        <label class="connection-field connection-delete-confirm-input">
          <span>输入连接名称确认</span>
          <input v-model="deleteConfirmName" :placeholder="pendingDeleteConnection.name" />
        </label>
        <p v-if="deleteError" class="connection-delete-error">{{ deleteError }}</p>
        <footer>
          <button
            class="connection-cancel-button"
            type="button"
            :disabled="deletingConnection"
            @click.stop="closeDeleteDialog"
          >
            取消
          </button>
          <button
            class="connection-danger-button"
            type="button"
            :disabled="deletingConnection"
            :aria-busy="deletingConnection ? 'true' : 'false'"
            @click.stop="confirmRemoveConnection"
          >
            <i :class="deletingConnection ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-trash'" />
            删除连接
          </button>
        </footer>
      </div>
    </section>

    <div v-if="actionNote" class="action-toast" :class="actionToastTone" role="status" aria-live="polite">
      <i :class="actionToastTone === 'success' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
      <span>{{ actionNote }}</span>
      <button type="button" aria-label="关闭提示" @click.stop="dismissActionNote">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>
