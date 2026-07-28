<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 16)
/** Agents page body list (ZKL-64 item 16). */
import AppSelect from "./AppSelect.vue";
import ManagementList from "./ManagementList.vue";
import ManagementPageHeader from "./ManagementPageHeader.vue";
import ManagementRowActions from "./ManagementRowActions.vue";
import ManagementSegmentedFilter from "./ManagementSegmentedFilter.vue";
import ManagementSummaryStrip from "./ManagementSummaryStrip.vue";
import AgentsStudioPanel from "./AgentsStudioPanel.vue";
import AgentsDialogs from "./AgentsDialogs.vue";
import WorkspaceContextState from "./WorkspaceContextState.vue";
import { useAgentsPageContext } from "../composables/useAgentsPageContext";

const scp = useAgentsPageContext();
/* prettier-ignore */
const {
  agents, canEditWorkspace, query, agentStatusFilter, pageInitialLoading, statusSourceText, selectedAgent, hasAgentRecords, agentSummaryItems, agentManagementFilterOptions, agentColumns, workspaceLabel, modelLabel, statusLabel, formatAgentUpdatedAt, statusTone,
  selectAgent, resetFilters, setAgentStatusFilter, enterCreateMode, openPromptDetail, agentMenuActions, handleAgentRowAction, setAgentSearch, changeAgentPage, changeAgentSort
} = scp;
void AppSelect;
void ManagementList;
void ManagementPageHeader;
void ManagementRowActions;
void ManagementSegmentedFilter;
void ManagementSummaryStrip;
void AgentsStudioPanel;
void AgentsDialogs;
void WorkspaceContextState;
</script>

<template>
  <div
    class="page-grid agent-grid management-page-grid"
    v-loading="pageInitialLoading"
    element-loading-text="正在加载 Agent Registry..."
  >
    <ManagementPageHeader
      class="span-12"
      title="Agent 管理"
      description="维护职责、绑定空间、模型配置与 Prompt Revision。"
      icon="fa-solid fa-user-gear"
    >
      <template #actions>
        <button
          v-if="canEditWorkspace"
          class="primary-button agent-create-button"
          type="button"
          @click="enterCreateMode"
        >
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          <span>新建 Agent</span>
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" :items="agentSummaryItems" />

    <section class="span-12 agent-registry-card management-list-card">
      <div v-if="hasAgentRecords" class="source-note">
        <i class="fa-solid fa-circle-info" aria-hidden="true" />
        <span>{{ statusSourceText }}</span>
      </div>
      <p v-if="hasAgentRecords" class="agent-narrow-notice" role="note">
        当前页面按桌面宽度设计；在窄视口下请左右滚动表格查看完整列。
      </p>

      <ManagementList
        class="agent-management-list"
        :rows="agents.pageItems"
        :columns="agentColumns"
        row-key="id"
        :sticky-left-keys="['identity']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:agents:columns"
        :selected-row-key="selectedAgent?.id"
        selection-tone="neutral"
        :loading="agents.pageLoading"
        :error="agents.pageError"
        :has-loaded="agents.pageHasLoaded"
        :search="query"
        search-placeholder="搜索 Agent / 角色职责..."
        search-aria-label="搜索 Agent 或角色职责"
        :reset-disabled="!query && agentStatusFilter === 'ALL'"
        :pagination="agents.pagination"
        :sort-by="agents.listQuery?.sortBy"
        :sort-order="agents.listQuery?.sortOrder"
        @select-row="selectAgent"
        @update:search="setAgentSearch"
        @reset="resetFilters"
        @page-change="changeAgentPage"
        @sort-change="changeAgentSort"
      >
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="agentStatusFilter"
            :options="agentManagementFilterOptions"
            ariaLabel="Agent 状态筛选"
            @update:model-value="setAgentStatusFilter($event as AgentStatusFilter)"
          />
        </template>
        <template #cell-identity="{ row: agent }">
          <button
            type="button"
            class="agent-identity-cell agent-select-button"
            :aria-pressed="selectedAgent?.id === agent.id"
            @click.stop="selectAgent(agent)"
          >
            <span class="agent-avatar"><i class="fa-solid fa-robot" aria-hidden="true" /></span>
            <span class="agent-identity-copy">
              <strong class="aw-table-title" :title="agent.name">{{ agent.name }}</strong>
              <small class="aw-table-subtitle" :title="agent.roleDescription">{{ agent.roleDescription }}</small>
            </span>
          </button>
        </template>
        <template #cell-workspace="{ row: agent }"
          ><span class="agent-workspace-pill aw-table-pill" :title="workspaceLabel(agent)">{{
            workspaceLabel(agent)
          }}</span></template
        >
        <template #cell-model="{ row: agent }">
          <span class="agent-model-chip aw-table-meta" :title="modelLabel(agent)"
            ><i class="fa-solid fa-microchip" aria-hidden="true" /><span>{{ modelLabel(agent) }}</span></span
          >
        </template>
        <template #cell-prompt="{ row: agent }">
          <span class="prompt-preview">
            <button
              type="button"
              class="prompt-preview-trigger"
              title="查看 Prompt Revision"
              :aria-label="`查看 ${agent.name} Prompt Revision`"
              @click.stop="openPromptDetail(agent)"
            >
              <i class="fa-solid fa-file-lines" aria-hidden="true" /><span>{{
                agent.currentPromptRevisionId ? "查看 Revision" : "暂无 Revision"
              }}</span>
            </button>
          </span>
        </template>
        <template #cell-status="{ row: agent }">
          <span :class="['agent-status-pill', 'aw-table-pill', statusTone(agent.status)]"
            ><span aria-hidden="true" />{{ statusLabel(agent.status) }}</span
          >
        </template>
        <template #cell-updatedAt="{ row: agent }"
          ><span class="agent-updated-at aw-table-meta">{{ formatAgentUpdatedAt(agent) }}</span></template
        >
        <template #cell-actions="{ row: agent }">
          <ManagementRowActions :menu-actions="agentMenuActions(agent)" @action="handleAgentRowAction($event, agent)" />
        </template>
        <template #empty>
          <div v-if="!hasAgentRecords" class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-robot" aria-hidden="true" /></div>
            <h2>暂无 Agent</h2>
            <p>创建 Agent 后再绑定业务空间、模型配置和 System Prompt。</p>
            <button class="primary-button" type="button" @click="enterCreateMode">创建 Agent</button>
          </div>
          <div v-else class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-magnifying-glass" aria-hidden="true" /></div>
            <h2>没有匹配的 Agent</h2>
            <p>调整搜索词或状态后再试，或切换顶部业务空间查看其他空间的 Agent。</p>
            <button class="ghost-button" type="button" @click="resetFilters">重置检索</button>
          </div>
        </template>
      </ManagementList>
    </section>

    <AgentsStudioPanel />
    <AgentsDialogs />
  </div>
</template>
