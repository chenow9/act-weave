<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 17)
/** OpenAPI imports modals (ZKL-64 item 17). */
import { useI18n } from "vue-i18n";
import ToolSchemaTreeView from "./ToolSchemaTreeView.vue";
import { useOpenAPIImportsPageContext } from "../composables/useOpenAPIImportsPageContext";

const { t } = useI18n();
const scp = useOpenAPIImportsPageContext();
/* prettier-ignore */
const {
  connectionsStore, importModalVisible, importMode, selectedOpenAPIFile, selectedOpenAPIFilePreview, selectedImportId, detailLoading, detailError, actionNote, importingOpenAPI, generatingDraftsByImportId, deletingImportId, pendingDeleteImport, importDialogRef, detailDialogRef, deleteDialogRef,
  openapiDropdowns, importForm, importProviders, selectedWorkspaceOption, selectedProviderOption, selectedProviderCanImportOnline, selectedConnectionOption, selectedImport, selectedWorkspace, selectedConnection, selectedImportDetail,
  filteredImportEndpoints, visibleImportEndpoints, hasMoreImportEndpoints, endpointDetailQuery,
  toggleEndpointDetail, isEndpointDetailExpanded, showMoreImportEndpoints,
  canImportOpenAPI, selectedImportDetailVisible, workspaceLabel, providerLabel, canProviderImportOnline,
  statusClass, statusDotClass, connectionAddress, toggleOpenAPIDropdown, handleModalTab, closeImportModal, retryImportDetail, closeImportDetail, closeDeleteConfirm, confirmRemoveImport, dismissActionNote, selectImportProvider, selectImportConnection, selectOpenAPIFile, importOpenAPI, generateDrafts
} = scp;
void ToolSchemaTreeView;
</script>

<template>
  <div v-if="importModalVisible" class="openapi-modal-backdrop" @click.self="closeImportModal">
    <section
      ref="importDialogRef"
      class="openapi-modal-card"
      role="dialog"
      aria-modal="true"
      :aria-label="t('openapi.importModalAria')"
      tabindex="-1"
      @click.stop
      @keydown.esc.stop.prevent="closeImportModal"
      @keydown.tab="handleModalTab($event, importDialogRef)"
    >
      <header class="openapi-modal-head">
        <div>
          <span><i class="fa-solid fa-file-import" /></span>
          <div>
            <h3>{{ t("openapi.importModalTitle") }}</h3>
            <p>{{ t("openapi.importModalSubtitle") }}</p>
          </div>
        </div>
        <button
          type="button"
          :title="t('openapi.close')"
          :aria-label="t('openapi.closeImportAria')"
          @click="closeImportModal"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </header>

      <div class="openapi-modal-body">
        <div class="openapi-field">
          <label>{{ t("openapi.currentWorkspace") }}</label>
          <div class="openapi-reference-select is-readonly" data-testid="openapi-current-workspace">
            <span
              ><i class="fa-solid fa-layer-group" />{{
                selectedWorkspaceOption
                  ? `${selectedWorkspaceOption.name} · ${selectedWorkspaceOption.displayName}`
                  : t("openapi.noWorkspaceSelected")
              }}</span
            >
            <small>{{ t("openapi.switchAtTop") }}</small>
          </div>
        </div>

        <div class="openapi-field">
          <label>{{ t("openapi.importMode") }}</label>
          <div class="openapi-import-mode-tabs" role="tablist" :aria-label="t('openapi.importModeAria')">
            <button
              type="button"
              role="tab"
              :aria-selected="importMode === 'FILE'"
              :class="{ active: importMode === 'FILE' }"
              @click="importMode = 'FILE'"
            >
              <i class="fa-solid fa-file-arrow-up" /> {{ t("openapi.localFile") }}
            </button>
            <button
              type="button"
              role="tab"
              :aria-selected="importMode === 'ONLINE'"
              :class="{ active: importMode === 'ONLINE' }"
              @click="importMode = 'ONLINE'"
            >
              <i class="fa-solid fa-cloud-arrow-down" /> {{ t("openapi.providerOnlineDocs") }}
            </button>
          </div>
        </div>

        <div class="openapi-field dropdown" @click.stop>
          <label>{{ t("openapi.selectProvider") }} <b class="field-required-mark">*</b></label>
          <button
            class="openapi-reference-select"
            type="button"
            aria-haspopup="listbox"
            :aria-expanded="openapiDropdowns.provider"
            :disabled="!importForm.workspaceId || !importProviders.length"
            data-testid="openapi-provider-select"
            @click="toggleOpenAPIDropdown('provider')"
          >
            <span
              ><i class="fa-solid fa-cubes" />{{
                selectedProviderOption?.name ||
                (importForm.workspaceId
                  ? importProviders.length
                    ? t("openapi.pleaseSelectProvider")
                    : t("openapi.noProviderInWorkspace")
                  : t("openapi.selectWorkspaceFirst"))
              }}</span
            >
            <i class="fa-solid fa-chevron-down" :class="{ open: openapiDropdowns.provider }" />
          </button>
          <div v-if="openapiDropdowns.provider" class="openapi-select-menu" role="listbox">
            <button
              v-for="provider in importProviders"
              :key="provider.id"
              class="openapi-select-option"
              :class="{ selected: importForm.providerId === provider.id }"
              type="button"
              role="option"
              :aria-selected="importForm.providerId === provider.id"
              @click="selectImportProvider(provider.id)"
            >
              <span class="openapi-option-copy">
                <strong>{{ provider.name }}</strong>
                <small>{{
                  canProviderImportOnline(provider)
                    ? t("openapi.canImportOnline")
                    : t("openapi.onlineDocsNotConfigured")
                }}</small>
              </span>
              <i v-if="importForm.providerId === provider.id" class="fa-solid fa-circle-check" />
            </button>
          </div>
        </div>

        <div class="openapi-field dropdown" @click.stop>
          <label>{{ t("openapi.selectConnection") }}</label>
          <button
            class="openapi-reference-select"
            type="button"
            aria-haspopup="listbox"
            :aria-expanded="openapiDropdowns.connection"
            :disabled="!importForm.providerId"
            data-testid="openapi-connection-select"
            @click="toggleOpenAPIDropdown('connection')"
          >
            <span
              ><i class="fa-solid fa-plug" />{{
                selectedConnectionOption?.name ||
                (importForm.providerId ? t("openapi.useProviderDefault") : t("openapi.selectProviderFirst"))
              }}</span
            >
            <i class="fa-solid fa-chevron-down" :class="{ open: openapiDropdowns.connection }" />
          </button>
          <div v-if="openapiDropdowns.connection" class="openapi-select-menu" role="listbox">
            <button
              class="openapi-select-option"
              type="button"
              role="option"
              :aria-selected="!importForm.connectionId"
              @click="selectImportConnection('')"
            >
              <span>{{ t("openapi.useProviderDefault") }}</span>
              <i v-if="!importForm.connectionId" class="fa-solid fa-circle-check" />
            </button>
            <button
              v-for="connection in connectionsStore.serviceConnections.filter(
                (item) => item.providerId === importForm.providerId,
              )"
              :key="connection.id"
              class="openapi-select-option"
              :class="{ selected: importForm.connectionId === connection.id }"
              type="button"
              role="option"
              :aria-selected="importForm.connectionId === connection.id"
              @click="selectImportConnection(connection.id)"
            >
              <span>{{ connection.name }}</span>
              <i v-if="importForm.connectionId === connection.id" class="fa-solid fa-circle-check" />
            </button>
          </div>
        </div>

        <div v-if="importMode === 'FILE'" class="openapi-field">
          <label>{{ t("openapi.openapiFile") }} <b class="field-required-mark">*</b></label>
          <label class="openapi-file-picker">
            <span class="openapi-file-picker-button"
              ><i class="fa-solid fa-folder-open" />{{ t("openapi.chooseFile") }}</span
            >
            <span class="openapi-file-picker-name">{{
              selectedOpenAPIFile?.name || t("openapi.chooseJsonOrYaml")
            }}</span>
            <span class="openapi-file-picker-meta">{{ t("openapi.maxFileSize") }}</span>
            <input
              class="openapi-file-input"
              data-testid="openapi-file-input"
              type="file"
              accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml"
              @change="selectOpenAPIFile"
            />
          </label>
          <small v-if="selectedOpenAPIFilePreview.error" class="openapi-file-error" role="alert">{{
            selectedOpenAPIFilePreview.error
          }}</small>
        </div>

        <div v-if="importMode === 'FILE' && selectedOpenAPIFilePreview.endpointCount" class="import-drawer-preview">
          <div>
            <i class="fa-solid fa-list-check" />
            <span>
              <strong>{{
                t("openapi.detectedEndpoints", { n: selectedOpenAPIFilePreview.endpointCount })
              }}</strong>
              <small>{{ t("openapi.readyEndpointsHint", { n: selectedOpenAPIFilePreview.readyCount }) }}</small>
            </span>
          </div>
          <div class="openapi-preview-table">
            <div>
              <strong>{{ t("openapi.previewMethod") }}</strong
              ><strong>{{ t("openapi.previewPath") }}</strong
              ><strong>{{ t("openapi.previewSuggestedTool") }}</strong
              ><strong>{{ t("openapi.previewStatus") }}</strong>
            </div>
            <div v-for="row in selectedOpenAPIFilePreview.rows.slice(0, 6)" :key="`${row.method}:${row.path}`">
              <span>{{ row.method }}</span
              ><span>{{ row.path }}</span
              ><span>{{ row.suggestedTool }}</span
              ><span>{{ row.statusText }}</span>
            </div>
          </div>
        </div>

        <div v-else-if="importMode === 'ONLINE' && selectedProviderCanImportOnline" class="import-drawer-preview">
          <div>
            <i class="fa-solid fa-cloud-arrow-down" />
            <span>
              <strong>{{ t("openapi.providerOpenapiSource") }}</strong>
              <small>{{ t("openapi.providerOpenapiSourceHint") }}</small>
            </span>
          </div>
          <div class="import-preview-empty">{{ t("openapi.onlineImportNote") }}</div>
        </div>
        <div
          v-else-if="importMode === 'ONLINE' && selectedProviderOption"
          class="import-drawer-preview unavailable"
          role="status"
          aria-live="polite"
        >
          <div>
            <i class="fa-solid fa-circle-info" />
            <span>
              <strong>{{ t("openapi.providerConnectionLoaded") }}</strong>
              <small>{{ t("openapi.onlineUnavailable") }}</small>
            </span>
          </div>
          <div class="import-preview-empty">{{ t("openapi.onlineUnavailableHint") }}</div>
        </div>
        <div
          v-else-if="importMode === 'ONLINE'"
          class="import-drawer-preview unavailable"
          role="status"
          aria-live="polite"
        >
          <div>
            <i class="fa-solid fa-circle-info" />
            <span>
              <strong>{{ t("openapi.noProviderTitle") }}</strong>
              <small>{{ t("openapi.noProviderHint") }}</small>
            </span>
          </div>
        </div>
      </div>

      <footer class="openapi-modal-actions">
        <span>{{ t("openapi.importThenDrafts") }}</span>
        <div>
          <button type="button" :disabled="importingOpenAPI" @click="closeImportModal">{{ t("openapi.cancel") }}</button>
          <button type="button" :disabled="!canImportOpenAPI || importingOpenAPI" @click="importOpenAPI">
            <i v-if="importingOpenAPI" class="fa-solid fa-spinner fa-spin" />
            {{ importingOpenAPI ? t("openapi.parsing") : t("openapi.startImport") }}
          </button>
        </div>
      </footer>
    </section>
  </div>

  <div v-if="selectedImportDetailVisible" class="openapi-modal-backdrop" @click.self="closeImportDetail">
    <section
      v-if="selectedImport"
      ref="detailDialogRef"
      class="openapi-modal-card openapi-detail-modal-card"
      role="dialog"
      aria-modal="true"
      :aria-label="t('openapi.detailModalAria')"
      tabindex="-1"
      @keydown.esc.stop.prevent="closeImportDetail"
      @keydown.tab="handleModalTab($event, detailDialogRef)"
    >
      <header class="openapi-modal-head openapi-detail-modal-head">
        <div>
          <span><i class="fa-solid fa-file-code" /></span>
          <div>
            <h3>{{ t("openapi.detailTitle") }}</h3>
            <p>{{ t("openapi.detailSubtitle") }}</p>
          </div>
        </div>
        <button
          type="button"
          :title="t('openapi.close')"
          :aria-label="t('openapi.closeDetailAria')"
          @click="closeImportDetail"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </header>
      <div class="openapi-detail-modal-body" data-testid="openapi-detail-body">
        <div
          v-if="detailLoading"
          class="openapi-detail-state"
          data-testid="openapi-detail-loading"
          role="status"
          aria-live="polite"
        >
          <i class="fa-solid fa-spinner fa-spin" aria-hidden="true" />
          <strong>{{ t("openapi.detailLoading") }}</strong>
          <p>{{ t("openapi.detailLoadingHint") }}</p>
        </div>
        <div
          v-else-if="detailError"
          class="openapi-detail-state is-error"
          data-testid="openapi-detail-error"
          role="alert"
        >
          <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
          <strong>{{ t("openapi.detailLoadFailedTitle") }}</strong>
          <p>{{ detailError }}</p>
          <div class="openapi-detail-state-actions">
            <button type="button" data-testid="openapi-detail-retry" @click="retryImportDetail">
              {{ t("openapi.retry") }}
            </button>
            <button type="button" data-testid="openapi-detail-error-close" @click="closeImportDetail">
              {{ t("openapi.close") }}
            </button>
          </div>
        </div>
        <template v-else>
          <div class="openapi-detail-hero">
            <i class="fa-solid fa-file-code" />
            <div>
              <strong :title="selectedImport.fileName">{{ selectedImport.fileName }}</strong>
              <small
                :title="`${selectedImport.source} · ${workspaceLabel(selectedImport.workspaceId)} · ${selectedImport.providerId || 'Provider'}`"
                >{{ selectedImport.source }} · {{ workspaceLabel(selectedImport.workspaceId) }} ·
                {{ selectedImport.providerId || "Provider" }}</small
              >
            </div>
            <span class="openapi-status-pill" :class="statusClass(selectedImport.status)">
              <span :class="statusDotClass(selectedImport.status)" />
              {{ selectedImport.status }}
            </span>
          </div>
          <div class="openapi-detail-grid import-detail-grid">
            <div class="config-summary-item">
              <i class="fa-solid fa-layer-group" />
              <span>{{ t("openapi.workspaceOwned") }}</span>
              <strong :title="selectedWorkspace?.name || selectedImport.workspaceId">{{
                selectedWorkspace?.name || selectedImport.workspaceId
              }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-cubes" />
              <span>{{ t("openapi.sourceProvider") }}</span>
              <strong :title="providerLabel(selectedImport.providerId)">{{
                providerLabel(selectedImport.providerId)
              }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-plug-circle-bolt" />
              <span>{{ t("openapi.colConnection") }}</span>
              <strong :title="selectedConnection?.name || ''">{{ selectedConnection?.name }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-server" />
              <span>{{ t("openapi.serviceAddress") }}</span>
              <strong :title="connectionAddress(selectedConnection)">{{
                connectionAddress(selectedConnection)
              }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-list-check" />
              <span>{{ t("openapi.endpointCount") }}</span>
              <strong>{{ selectedImport.totalEndpoints }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-wand-magic-sparkles" />
              <span>{{ t("openapi.generationStatus") }}</span>
              <strong>{{ t("openapi.readyCount", { n: selectedImport.readyEndpoints }) }}</strong>
            </div>
          </div>
          <!-- Aggregate trees only when there is no per-endpoint list (avoids double cost). -->
          <div
            v-if="
              !selectedImportDetail?.endpoints?.length &&
              ((selectedImportDetail?.requestTransport || []).length ||
                (selectedImportDetail?.requestBodyNodes || []).length ||
                (selectedImportDetail?.responseNodes || []).length)
            "
            class="openapi-detail-schema-stack"
          >
            <ToolSchemaTreeView
              :nodes="selectedImportDetail?.requestTransport || []"
              :title="t('openapi.requestParams')"
              :empty-text="t('openapi.requestParamsEmpty')"
            />
            <ToolSchemaTreeView
              :nodes="selectedImportDetail?.requestBodyNodes || []"
              :title="t('openapi.requestBody')"
              :empty-text="t('openapi.requestBodyEmpty')"
            />
            <ToolSchemaTreeView
              :nodes="selectedImportDetail?.responseNodes || []"
              :title="t('openapi.responseResult')"
              :empty-text="t('openapi.responseEmpty')"
            />
          </div>
          <div v-if="selectedImportDetail?.endpoints?.length" class="tool-schema-endpoint-list">
            <div class="editable-schema-head openapi-endpoint-list-head">
              <div>
                <strong>{{ t("openapi.endpointDetails") }}</strong>
                <span>{{
                  t("openapi.endpointDetailsHint", { n: selectedImportDetail.endpoints.length })
                }}</span>
              </div>
              <label class="openapi-endpoint-search">
                <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
                <input
                  v-model="endpointDetailQuery"
                  type="search"
                  :placeholder="t('openapi.searchEndpointsPlaceholder')"
                  :aria-label="t('openapi.searchEndpointsAria')"
                />
              </label>
            </div>
            <p v-if="!filteredImportEndpoints.length" class="openapi-endpoint-empty">
              {{ t("openapi.noMatchingEndpoints") }}
            </p>
            <div
              v-for="endpoint in visibleImportEndpoints"
              :key="`${endpoint.method}-${endpoint.path}`"
              class="tool-schema-endpoint-card"
              :class="{ open: isEndpointDetailExpanded(endpoint) }"
            >
              <button
                type="button"
                class="tool-schema-endpoint-head tool-schema-endpoint-toggle"
                :aria-expanded="isEndpointDetailExpanded(endpoint)"
                @click="toggleEndpointDetail(endpoint)"
              >
                <span class="tool-schema-endpoint-title">
                  <strong :title="`${endpoint.method} ${endpoint.path}`"
                    >{{ endpoint.method }} {{ endpoint.path }}</strong
                  >
                  <span>{{ endpoint.summary || endpoint.operationId || endpoint.status }}</span>
                </span>
                <i class="fa-solid fa-chevron-down tool-schema-endpoint-chevron" aria-hidden="true" />
              </button>
              <div v-if="isEndpointDetailExpanded(endpoint)" class="tool-schema-endpoint-body">
                <ToolSchemaTreeView
                  :nodes="endpoint.requestContract ? ([endpoint.requestContract].flat() as ToolSchemaNode[]) : []"
                  :title="t('openapi.requestBody')"
                  :empty-text="t('openapi.noRequestStructure')"
                />
                <ToolSchemaTreeView
                  :nodes="endpoint.responseContract ? ([endpoint.responseContract].flat() as ToolSchemaNode[]) : []"
                  :title="t('openapi.responseResult')"
                  :empty-text="t('openapi.noResponseStructure')"
                />
              </div>
            </div>
            <button
              v-if="hasMoreImportEndpoints"
              type="button"
              class="openapi-endpoint-more"
              @click="showMoreImportEndpoints"
            >
              {{
                t("openapi.showMore", {
                  shown: visibleImportEndpoints.length,
                  total: filteredImportEndpoints.length,
                })
              }}
            </button>
          </div>
        </template>
      </div>
      <div class="drawer-footer-actions openapi-detail-actions">
        <button type="button" @click="closeImportDetail">{{ t("openapi.close") }}</button>
        <button
          type="button"
          :disabled="detailLoading || Boolean(detailError) || Boolean(generatingDraftsByImportId[selectedImport.id])"
          @click="generateDrafts(selectedImport)"
        >
          <i v-if="generatingDraftsByImportId[selectedImport.id]" class="fa-solid fa-spinner fa-spin" />
          {{
            generatingDraftsByImportId[selectedImport.id]
              ? t("openapi.generating")
              : t("openapi.generateToolDrafts")
          }}
        </button>
      </div>
    </section>
  </div>

  <div v-if="pendingDeleteImport" class="openapi-modal-backdrop" @click.self="closeDeleteConfirm">
    <section
      ref="deleteDialogRef"
      class="openapi-modal-card openapi-confirm-modal-card"
      role="dialog"
      aria-modal="true"
      :aria-label="t('openapi.deleteModalAria')"
      tabindex="-1"
      @keydown.esc.stop.prevent="closeDeleteConfirm"
      @keydown.tab="handleModalTab($event, deleteDialogRef)"
    >
      <header class="openapi-modal-head">
        <div>
          <span><i class="fa-solid fa-triangle-exclamation" /></span>
          <div>
            <h3>{{ t("openapi.deleteTitle") }}</h3>
            <p>{{ t("openapi.deleteSubtitle") }}</p>
          </div>
        </div>
        <button
          type="button"
          :title="t('openapi.close')"
          :aria-label="t('openapi.closeDeleteAria')"
          :disabled="Boolean(deletingImportId)"
          @click="closeDeleteConfirm"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </header>
      <div class="openapi-confirm-body">
        <strong>{{ pendingDeleteImport.fileName }}</strong>
        <p>{{ t("openapi.deleteConfirmBody") }}</p>
      </div>
      <footer class="openapi-modal-actions">
        <span>{{ t("openapi.deleteSyncNote") }}</span>
        <div>
          <button type="button" :disabled="Boolean(deletingImportId)" @click="closeDeleteConfirm">
            {{ t("openapi.cancel") }}
          </button>
          <button class="danger" type="button" :disabled="Boolean(deletingImportId)" @click="confirmRemoveImport">
            <i v-if="deletingImportId" class="fa-solid fa-spinner fa-spin" />
            {{ deletingImportId ? t("openapi.deleting") : t("openapi.confirmDelete") }}
          </button>
        </div>
      </footer>
    </section>
  </div>

  <div
    v-if="actionNote && !importModalVisible && !selectedImportId && !pendingDeleteImport"
    class="action-toast"
    role="status"
    aria-live="polite"
  >
    <span>{{ actionNote }}</span>
    <button type="button" :aria-label="t('openapi.dismissNoteAria')" @click="dismissActionNote">
      <i class="fa-solid fa-xmark" />
    </button>
  </div>
</template>
