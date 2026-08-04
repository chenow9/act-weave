<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 17)
/** OpenAPI imports page body (ZKL-64 item 17). */
import { useI18n } from "vue-i18n";
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import OpenAPIImportsModals from "./OpenAPIImportsModals.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useOpenAPIImportsPageContext } from "../composables/useOpenAPIImportsPageContext";

const { t } = useI18n();
const scp = useOpenAPIImportsPageContext();
/* prettier-ignore */
const {
  openapiImports, openAPIQuickFilterOptions, router, canEditWorkspace, hasWorkspaceContext, query, openAPIStatusFilter, openAPIQuickFilterValue, selectedImportId, generatingDraftsByImportId, openAPIListLoading, openAPIListError, openAPIListHasLoaded, mobileImportActionMenuId, openAPIImportColumns, hasImportRecords,
  connectionById, workspaceLabel, statusClass, statusDotClass, hasOpenAPIIssues, issueText, importTime, changeOpenAPIPage, changeOpenAPISort, updateOpenAPISearch, resetOpenAPIFilters, updateOpenAPIQuickFilter, loadOpenAPIPageAssets, closeOpenAPIDropdowns, openImportModal, openImportDetail,
  openAPIImportMenuActions, handleOpenAPIImportRowAction, toggleMobileImportActions, openMobileImportDetail, generateMobileDrafts, requestMobileImportRemoval
} = scp;
void ManagementList;
void ManagementPageHeader;
void ManagementRowActions;
void ManagementSegmentedFilter;
void OpenAPIImportsModals;
void WorkspaceContextState;
</script>

<template>
  <div class="openapi-import-page management-page-grid management-page-grid--two-rows" @click="closeOpenAPIDropdowns">
    <ManagementPageHeader
      class="openapi-page-header"
      :title="t('openapi.title')"
      :description="t('openapi.description')"
      icon="fa-solid fa-file-import"
      :eyebrow="t('openapi.eyebrow')"
    >
      <template #actions>
        <button class="ghost-button" type="button" @click="router.push('/connections')">
          <i class="fa-solid fa-plug" aria-hidden="true" />
          {{ t("openapi.serviceConnections") }}
        </button>
        <button
          class="primary-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? t('openapi.importOpenAPI') : t('openapi.needWorkspace')"
          @click.stop="openImportModal($event)"
        >
          <i class="fa-solid fa-file-import" aria-hidden="true" />
          {{ t("openapi.importOpenAPI") }}
        </button>
      </template>
    </ManagementPageHeader>

    <section class="openapi-import-table-card management-list-card">
      <ManagementList
        class="openapi-import-management-list"
        :rows="hasWorkspaceContext ? openapiImports.openAPIImportPageItems : []"
        :columns="openAPIImportColumns"
        row-key="id"
        :sticky-left-keys="['file']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:openapi-imports:columns"
        :selected-row-key="selectedImportId"
        :loading="hasWorkspaceContext && openAPIListLoading"
        :error="hasWorkspaceContext ? openAPIListError : undefined"
        :has-loaded="hasWorkspaceContext ? openAPIListHasLoaded : true"
        :search="query"
        :search-placeholder="t('openapi.searchPlaceholder')"
        :search-aria-label="t('openapi.searchAria')"
        :clear-search-aria-label="t('openapi.clearSearchAria')"
        :reset-aria-label="t('openapi.resetAria')"
        :reset-disabled="!query && openAPIStatusFilter === 'ALL'"
        :pagination="openapiImports.openAPIImportPagination"
        :sort-by="openapiImports.openAPIImportListQuery?.sortBy"
        :sort-order="openapiImports.openAPIImportListQuery?.sortOrder"
        @update:search="updateOpenAPISearch"
        @reset="resetOpenAPIFilters"
        @page-change="changeOpenAPIPage"
        @sort-change="changeOpenAPISort"
        @select-row="openImportDetail"
      >
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="openAPIQuickFilterValue"
            :options="openAPIQuickFilterOptions"
            :ariaLabel="t('openapi.quickFilterAria')"
            @update:model-value="updateOpenAPIQuickFilter"
          />
        </template>

        <template #cell-file="{ row: record }">
          <div class="openapi-file-cell">
            <div><i class="fa-solid fa-file-code" /></div>
            <span>
              <strong class="aw-table-title">{{ record.fileName }}</strong>
              <small class="aw-table-subtitle">{{ record.source }}</small>
              <em class="aw-table-meta"
                >{{ workspaceLabel(record.workspaceId) }} · {{ record.providerId || "Provider" }}</em
              >
            </span>
          </div>
        </template>
        <template #cell-connection="{ row: record }">
          <span class="openapi-connection-name aw-table-meta">{{
            connectionById(record.connectionId || "")?.name ||
            record.connectionId ||
            t("openapi.defaultConnection")
          }}</span>
        </template>
        <template #cell-totalEndpoints="{ row: record }">
          <span class="openapi-count-cell"
            ><strong class="aw-table-title">{{ record.totalEndpoints }}</strong
            ><small v-if="t('openapi.countUnit')" class="aw-table-meta">{{ t("openapi.countUnit") }}</small></span
          >
        </template>
        <template #cell-readyEndpoints="{ row: record }">
          <span class="openapi-count-cell ready"
            ><strong class="aw-table-title">{{ record.readyEndpoints }}</strong
            ><small v-if="t('openapi.countUnit')" class="aw-table-meta">{{ t("openapi.countUnit") }}</small></span
          >
        </template>
        <template #cell-issues="{ row: record }">
          <span class="openapi-issue-text aw-table-meta" :class="{ warning: hasOpenAPIIssues(record) }">{{
            issueText(record)
          }}</span>
        </template>
        <template #cell-importTime="{ row: record }">
          <span class="openapi-time-text aw-table-meta">{{ importTime(record) }}</span>
        </template>
        <template #cell-status="{ row: record }">
          <span class="openapi-status-pill aw-table-pill" :class="statusClass(record.status)">
            <span :class="statusDotClass(record.status)" />
            {{ record.status }}
          </span>
        </template>
        <template #cell-actions="{ row: record }">
          <ManagementRowActions
            :menu-actions="openAPIImportMenuActions(record)"
            :menu-label="t('openapi.moreActions')"
            @action="handleOpenAPIImportRowAction($event, record)"
          />
        </template>

        <template #card="{ row: record }">
          <article class="openapi-import-mobile-card">
            <header>
              <div class="openapi-file-cell">
                <div><i class="fa-solid fa-file-code" /></div>
                <span>
                  <strong>{{ record.fileName }}</strong>
                  <small>{{ record.source }}</small>
                </span>
              </div>
              <button
                class="openapi-mobile-actions-toggle"
                type="button"
                :aria-label="t('openapi.moreActionsAria')"
                :aria-expanded="mobileImportActionMenuId === record.id"
                @click.stop="toggleMobileImportActions(record)"
              >
                <i class="fa-solid fa-ellipsis" />
              </button>
            </header>
            <dl>
              <div>
                <dt>{{ t("openapi.dtConnection") }}</dt>
                <dd>
                  {{
                    connectionById(record.connectionId || "")?.name ||
                    record.connectionId ||
                    t("openapi.defaultConnection")
                  }}
                </dd>
              </div>
              <div>
                <dt>{{ t("openapi.dtEndpoints") }}</dt>
                <dd>
                  {{
                    t("openapi.endpointsSummary", {
                      total: record.totalEndpoints,
                      ready: record.readyEndpoints,
                    })
                  }}
                </dd>
              </div>
              <div>
                <dt>{{ t("openapi.dtStatus") }}</dt>
                <dd>
                  <span class="openapi-status-pill" :class="statusClass(record.status)"
                    ><span :class="statusDotClass(record.status)" />{{ record.status }}</span
                  >
                </dd>
              </div>
            </dl>
            <button
              v-if="canEditWorkspace"
              class="openapi-mobile-detail-button"
              type="button"
              @click="openMobileImportDetail(record, $event)"
            >
              {{ t("openapi.viewImportDetail") }}
            </button>
            <div
              v-if="mobileImportActionMenuId === record.id"
              class="openapi-mobile-actions-menu"
              role="menu"
              :aria-label="t('openapi.mobileActionsAria')"
            >
              <button type="button" role="menuitem" @click="openMobileImportDetail(record, $event)">
                <i class="fa-solid fa-eye" />{{ t("openapi.viewDetail") }}
              </button>
              <button
                type="button"
                role="menuitem"
                :disabled="Boolean(generatingDraftsByImportId[record.id])"
                @click="generateMobileDrafts(record)"
              >
                <i class="fa-solid fa-wand-magic-sparkles" />{{ t("openapi.generateToolDrafts") }}
              </button>
              <button class="danger" type="button" role="menuitem" @click="requestMobileImportRemoval(record, $event)">
                <i class="fa-solid fa-trash" />{{ t("openapi.deleteRecord") }}
              </button>
            </div>
          </article>
        </template>

        <template #error="{ error }">
          <div v-if="openapiImports.openAPIImportPageItems.length" class="openapi-load-error-banner" role="alert">
            <i class="fa-solid fa-triangle-exclamation" />
            <span>{{ t("openapi.loadFailed", { error }) }}</span>
            <button class="ghost-button" type="button" data-openapi-load-retry @click="loadOpenAPIPageAssets">
              {{ t("openapi.retry") }}
            </button>
          </div>
          <div v-else class="openapi-empty-state openapi-load-error-state" role="alert">
            <div><i class="fa-solid fa-triangle-exclamation" /></div>
            <h4>{{ t("openapi.loadFailedTitle") }}</h4>
            <p>{{ error }}</p>
            <button class="primary-button" type="button" data-openapi-load-retry @click="loadOpenAPIPageAssets">
              {{ t("openapi.retry") }}
            </button>
          </div>
        </template>

        <template #empty>
          <WorkspaceContextState
            v-if="!hasWorkspaceContext"
            embedded-in-list
            :feature="t('openapi.featureName')"
            icon="fa-solid fa-file-circle-plus"
            @retry="loadOpenAPIPageAssets"
          />
          <div v-else-if="!hasImportRecords" class="openapi-empty-state">
            <div><i class="fa-solid fa-file-circle-plus" /></div>
            <h4>{{ t("openapi.emptyTitle") }}</h4>
            <p>{{ t("openapi.emptyBody") }}</p>
            <button v-if="canEditWorkspace" class="primary-button" type="button" @click="openImportModal($event)">
              {{ t("openapi.importOpenAPI") }}
            </button>
          </div>
          <div v-else class="openapi-empty-state compact">
            <div><i class="fa-solid fa-magnifying-glass" /></div>
            <h4>{{ t("openapi.noMatchTitle") }}</h4>
            <p>{{ t("openapi.noMatchBody") }}</p>
            <button class="ghost-button" type="button" @click="resetOpenAPIFilters">{{ t("openapi.clearSearch") }}</button>
          </div>
        </template>
      </ManagementList>
    </section>
    <OpenAPIImportsModals />
  </div>
</template>
