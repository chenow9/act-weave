<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 17)
/** OpenAPI imports page body (ZKL-64 item 17). */
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import OpenAPIImportsModals from "./OpenAPIImportsModals.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useOpenAPIImportsPageContext } from "../composables/useOpenAPIImportsPageContext";

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
      title="OpenAPI 导入"
      description="导入属于集成接入流程：选择已验证的服务连接，解析接口清单，再生成 Tool 草稿。Tool 管理页只接收草稿并补齐动作契约。"
      icon="fa-solid fa-file-import"
      eyebrow="OpenAPI Import"
    >
      <template #actions>
        <button class="ghost-button" type="button" @click="router.push('/connections')">
          <i class="fa-solid fa-plug" aria-hidden="true" />
          服务连接
        </button>
        <button
          class="primary-button"
          type="button"
          :disabled="!hasWorkspaceContext"
          :title="hasWorkspaceContext ? '导入 OpenAPI' : '请先创建或加入业务空间'"
          @click.stop="openImportModal($event)"
        >
          <i class="fa-solid fa-file-import" aria-hidden="true" />
          导入 OpenAPI
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
        search-placeholder="搜索来源 / Provider / 服务连接..."
        search-aria-label="搜索 OpenAPI 导入记录"
        clear-search-aria-label="清除 OpenAPI 导入搜索"
        reset-aria-label="重置 OpenAPI 导入筛选"
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
            ariaLabel="OpenAPI 导入快捷筛选"
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
            connectionById(record.connectionId || "")?.name || record.connectionId || "Provider 默认连接"
          }}</span>
        </template>
        <template #cell-totalEndpoints="{ row: record }">
          <span class="openapi-count-cell"
            ><strong class="aw-table-title">{{ record.totalEndpoints }}</strong
            ><small class="aw-table-meta">个</small></span
          >
        </template>
        <template #cell-readyEndpoints="{ row: record }">
          <span class="openapi-count-cell ready"
            ><strong class="aw-table-title">{{ record.readyEndpoints }}</strong
            ><small class="aw-table-meta">个</small></span
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
            menu-label="更多操作"
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
                aria-label="OpenAPI 导入记录更多操作"
                :aria-expanded="mobileImportActionMenuId === record.id"
                @click.stop="toggleMobileImportActions(record)"
              >
                <i class="fa-solid fa-ellipsis" />
              </button>
            </header>
            <dl>
              <div>
                <dt>服务连接</dt>
                <dd>
                  {{ connectionById(record.connectionId || "")?.name || record.connectionId || "Provider 默认连接" }}
                </dd>
              </div>
              <div>
                <dt>接口</dt>
                <dd>{{ record.totalEndpoints }} 个 / 可生成 {{ record.readyEndpoints }} 个</dd>
              </div>
              <div>
                <dt>状态</dt>
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
              查看导入详情
            </button>
            <div
              v-if="mobileImportActionMenuId === record.id"
              class="openapi-mobile-actions-menu"
              role="menu"
              aria-label="OpenAPI 导入记录操作"
            >
              <button type="button" role="menuitem" @click="openMobileImportDetail(record, $event)">
                <i class="fa-solid fa-eye" />查看详情
              </button>
              <button
                type="button"
                role="menuitem"
                :disabled="Boolean(generatingDraftsByImportId[record.id])"
                @click="generateMobileDrafts(record)"
              >
                <i class="fa-solid fa-wand-magic-sparkles" />生成 Tool 草稿
              </button>
              <button class="danger" type="button" role="menuitem" @click="requestMobileImportRemoval(record, $event)">
                <i class="fa-solid fa-trash" />删除记录
              </button>
            </div>
          </article>
        </template>

        <template #error="{ error }">
          <div v-if="openapiImports.openAPIImportPageItems.length" class="openapi-load-error-banner" role="alert">
            <i class="fa-solid fa-triangle-exclamation" />
            <span>OpenAPI 导入记录加载失败：{{ error }}</span>
            <button class="ghost-button" type="button" data-openapi-load-retry @click="loadOpenAPIPageAssets">
              重试
            </button>
          </div>
          <div v-else class="openapi-empty-state openapi-load-error-state" role="alert">
            <div><i class="fa-solid fa-triangle-exclamation" /></div>
            <h4>OpenAPI 导入记录加载失败</h4>
            <p>{{ error }}</p>
            <button class="primary-button" type="button" data-openapi-load-retry @click="loadOpenAPIPageAssets">
              重试
            </button>
          </div>
        </template>

        <template #empty>
          <WorkspaceContextState
            v-if="!hasWorkspaceContext"
            embedded-in-list
            feature="OpenAPI 导入"
            icon="fa-solid fa-file-circle-plus"
            @retry="loadOpenAPIPageAssets"
          />
          <div v-else-if="!hasImportRecords" class="openapi-empty-state">
            <div><i class="fa-solid fa-file-circle-plus" /></div>
            <h4>暂无导入记录</h4>
            <p>选择已验证的服务连接，导入 OpenAPI 后再生成 Tool 草稿。</p>
            <button v-if="canEditWorkspace" class="primary-button" type="button" @click="openImportModal($event)">
              导入 OpenAPI
            </button>
          </div>
          <div v-else class="openapi-empty-state compact">
            <div><i class="fa-solid fa-magnifying-glass" /></div>
            <h4>没有匹配导入记录</h4>
            <p>调整文件、来源或服务连接关键词</p>
            <button class="ghost-button" type="button" @click="resetOpenAPIFilters">清除搜索条件</button>
          </div>
        </template>
      </ManagementList>
    </section>
    <OpenAPIImportsModals />
  </div>
</template>
