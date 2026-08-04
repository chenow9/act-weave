<script setup lang="ts">
// @ts-nocheck — inject surface + slot row typing under page split (ZKL-64 item 11)
/** Service Connections page body (ZKL-64 item 11). */
import { useI18n } from "vue-i18n";

import AppSelect from "./AppSelect.vue";
import ConnectionDetailPanel from "./ConnectionDetailPanel.vue";
import ConnectionFormPanel from "./ConnectionFormPanel.vue";
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useServiceConnectionsPageContext } from "../composables/useServiceConnectionsPageContext";

const { t } = useI18n();

/** Match backend/env values that map to the test/sandbox environment CSS tone. */
function isTestEnvironment(environment: string) {
  const value = environment.trim();
  const lower = value.toLowerCase();
  return (
    lower === "sandbox" ||
    lower === "staging" ||
    lower === "test" ||
    lower === "development" ||
    value === "\u6d4b\u8bd5" // legacy Chinese env label still stored by some workspaces
  );
}

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
void ManagementPageHeader;
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
      <ManagementPageHeader
        class="connection-page-header"
        :title="t('connections.title')"
        :description="t('connections.description')"
        icon="fa-solid fa-plug-circle-bolt"
        :eyebrow="t('connections.eyebrow')"
      >
        <template #actions>
          <div class="connection-header-actions">
            <button
              class="ghost-button"
              type="button"
              :disabled="!hasWorkspaceContext"
              :title="hasWorkspaceContext ? t('connections.registerProvider') : t('connections.needWorkspace')"
              @click.stop="router.push('/providers')"
            >
              <i class="fa-solid fa-server" />
              {{ t("connections.manageProviders") }}
            </button>
            <button
              class="primary-button"
              type="button"
              :disabled="!hasWorkspaceContext || !readyProviderCount"
              :title="
                !hasWorkspaceContext
                  ? t('connections.needWorkspace')
                  : readyProviderCount
                    ? t('connections.create')
                    : t('connections.createNeedProvider')
              "
              @click.stop="openCreateConnection"
            >
              <i class="fa-solid fa-circle-plus" />
              {{ t("connections.create") }}
            </button>
          </div>
        </template>
      </ManagementPageHeader>

      <section class="connection-reference-table-card management-list-card">
        <div
          v-if="hasWorkspaceContext && migrationRequiredCount > 0"
          class="connection-migration-banner"
          role="status"
          data-testid="connection-migration-banner"
        >
          <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
          <div>
            <strong>{{ t("connections.migrationBannerTitle", { count: migrationRequiredCount }) }}</strong>
            <p>
              {{ t("connections.migrationBannerBody") }}
            </p>
          </div>
          <button
            type="button"
            class="connection-secondary-button"
            @click="updateConnectionMigrationFilter('MIGRATION_REQUIRED')"
          >
            {{ t("connections.viewMigrationOnly") }}
          </button>
        </div>
        <ManagementList
          class="connection-management-list"
          :rows="hasWorkspaceContext ? filteredConnectionRows : []"
          :columns="connectionColumns"
          row-key="id"
          :sticky-left-keys="['name']"
          :sticky-right-keys="['actions']"
          storage-key="actweave:service-connections:columns-v2"
          :selectable="false"
          :loading="hasWorkspaceContext && connectionListLoading"
          :error="hasWorkspaceContext ? connectionLoadError : undefined"
          :has-loaded="hasWorkspaceContext ? connectionsHasLoaded : true"
          :search="query"
          :pagination="connectionsStore.serviceConnectionPagination"
          :sort-by="connectionsStore.serviceConnectionListQuery?.sortBy"
          :sort-order="connectionsStore.serviceConnectionListQuery?.sortOrder"
          :search-placeholder="t('connections.searchPlaceholder')"
          :search-aria-label="t('connections.searchAria')"
          :clear-search-aria-label="t('connections.clearSearchAria')"
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
                :ariaLabel="t('connections.statusFilterAria')"
                @update:model-value="updateConnectionStatusFilter"
              />
              <ManagementSegmentedFilter
                :model-value="connectionMigrationFilter"
                :options="connectionMigrationOptions"
                :ariaLabel="t('connections.migrationFilterAria')"
                @update:model-value="updateConnectionMigrationFilter"
              />
              <ManagementSegmentedFilter
                :model-value="connectionModeFilter"
                :options="connectionModeOptions"
                :ariaLabel="t('connections.modeFilterAria')"
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
              :class="{ test: isTestEnvironment(connection.environment) }"
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
                  :aria-label="t('connections.fullNameAria', { name: connection.name })"
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
              >{{ t("connections.migrationRequired") }}</span
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
                    :aria-label="t('connections.fullAddressAria', { address: connectionAddress(connection) })"
                  >
                    {{ connectionAddressPrimary(connection).hostPort
                    }}<template v-if="connectionAddressPrimary(connection).basePath">{{
                      connectionAddressPrimary(connection).basePath
                    }}</template>
                  </strong>
                  <button
                    class="connection-copy-button"
                    type="button"
                    :aria-label="t('connections.copyAddressAria', { name: connection.name })"
                    @click.stop="copyConnectionText(connectionAddress(connection), t('connections.serviceAddress'))"
                  >
                    <i class="fa-regular fa-copy" />
                  </button>
                </div>
                <span
                  class="aw-table-subtitle connection-address-verify"
                  :title="`${verificationMethodLabel(connection)} ${verificationPathLabel(connection)}`"
                >
                  {{ t("connections.verifyPrefix") }} · {{ verificationMethodLabel(connection) }}
                  {{ verificationPathLabel(connection) }}
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
              :menu-label="t('connections.moreActions')"
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
                  :aria-label="t('connections.connectionActionsAria', { name: connection.name })"
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
                  :aria-label="t('connections.copyAddressAria', { name: connection.name })"
                  @click.stop="copyConnectionText(connectionAddress(connection), t('connections.serviceAddress'))"
                >
                  <i class="fa-regular fa-copy" />
                </button>
              </div>
              <dl>
                <div>
                  <dt>{{ t("connections.verificationEndpoint") }}</dt>
                  <dd>
                    {{ connection.protocolConfig.verificationMethod || "GET" }} {{ verificationPathLabel(connection) }}
                  </dd>
                </div>
                <div>
                  <dt>{{ t("connections.status") }}</dt>
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
                :aria-label="t('connections.connectionActionsAria', { name: connection.name })"
              >
                <button type="button" role="menuitem" @click.stop="openMobileConnectionPreview(connection)">
                  <i class="fa-solid fa-eye" />{{ t("connections.viewDetail") }}
                </button>
                <button type="button" role="menuitem" @click.stop="openMobileConnectionEditor(connection)">
                  <i class="fa-solid fa-pen-to-square" />{{ t("connections.editConnection") }}
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
                  />{{ t("connections.verifyConnection") }}
                </button>
                <button
                  class="danger"
                  type="button"
                  role="menuitem"
                  @click.stop="requestMobileRemoveConnection(connection)"
                >
                  <i class="fa-solid fa-trash-can" />{{ t("connections.deleteConnection") }}
                </button>
              </div>
            </article>
          </template>

          <template #error="{ error }">
            <div class="connection-load-error" role="alert">
              <div><i class="fa-solid fa-triangle-exclamation" /></div>
              <h4>{{ t("connections.loadFailedTitle") }}</h4>
              <p>{{ error }}</p>
              <button type="button" :aria-label="t('connections.retryLoadAria')" @click.stop="retryLoadConnections">
                {{ t("connections.retry") }}
              </button>
            </div>
          </template>

          <template #empty>
            <WorkspaceContextState
              v-if="!hasWorkspaceContext"
              embedded-in-list
              :feature="t('connections.featureName')"
              icon="fa-solid fa-plug-circle-xmark"
              @retry="loadConnections"
            />
            <div v-else class="connection-empty-state">
              <div><i class="fa-solid fa-plug-circle-xmark" /></div>
              <h4>
                {{ hasConnectionRecords ? t("connections.emptyNoMatch") : t("connections.emptyTitle") }}
              </h4>
              <p>
                {{ hasConnectionRecords ? t("connections.emptyNoMatchBody") : t("connections.emptyBody") }}
              </p>
              <button
                v-if="hasConnectionRecords"
                class="ghost-button"
                type="button"
                @click.stop="resetConnectionFilters"
              >
                {{ t("connections.resetFilters") }}
              </button>
              <button v-else class="primary-button" type="button" @click.stop="openCreateConnection">
                {{ t("connections.create") }}
              </button>
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
          <h2 id="connection-discard-title">{{ t("connections.discardTitle") }}</h2>
          <p>{{ t("connections.discardBody") }}</p>
        </header>
        <footer>
          <button class="connection-secondary-button" type="button" @click.stop="keepEditingConnectionForm">
            {{ t("connections.keepEditing") }}
          </button>
          <button class="connection-danger-button" type="button" @click.stop="discardConnectionFormChanges">
            <i class="fa-solid fa-trash" />
            {{ t("connections.discardConfirm") }}
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
          <h2 id="connection-delete-title">{{ t("connections.deleteTitle") }}</h2>
          <p>{{ t("connections.deleteBody") }}</p>
        </header>
        <label class="connection-field connection-delete-confirm-input">
          <span>{{ t("connections.deleteConfirmLabel") }}</span>
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
            {{ t("connections.cancel") }}
          </button>
          <button
            class="connection-danger-button"
            type="button"
            :disabled="deletingConnection"
            :aria-busy="deletingConnection ? 'true' : 'false'"
            @click.stop="confirmRemoveConnection"
          >
            <i :class="deletingConnection ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-trash'" />
            {{ t("connections.deleteConnection") }}
          </button>
        </footer>
      </div>
    </section>

    <div v-if="actionNote" class="action-toast" :class="actionToastTone" role="status" aria-live="polite">
      <i :class="actionToastTone === 'success' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
      <span>{{ actionNote }}</span>
      <button type="button" :aria-label="t('connections.closeNote')" @click.stop="dismissActionNote">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
  </div>
</template>
