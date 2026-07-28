<script setup lang="ts">
import "./audit-logs-page.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementSummaryStrip from "../components/ManagementSummaryStrip.vue";
import type { ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { useAgentAuditStore } from "../stores/agentAudit";
import { useAuthStore } from "../stores/auth";
import { useWorkspaceStore } from "../stores/workspaces";
import type { AgentAuditStep, AgentAuditTraceListItem } from "../types/domain";

const agentAudit = useAgentAuditStore();
const auth = useAuthStore();
const workspaces = useWorkspaceStore();
const pageRef = ref<HTMLElement | null>(null);
const timelineSentinelRef = ref<HTMLElement | null>(null);
const actionError = ref("");
const searchInput = ref("");
const listHasLoaded = ref(false);
let timelineObserver: IntersectionObserver | null = null;

const isPlatformAdmin = computed(() => auth.user?.platformRole === "PLATFORM_ADMIN");
/** Follow global topbar workspace switcher; no page-local workspace control. */
const workspaceId = computed(() => workspaces.activeWorkspaceId || workspaces.items[0]?.id || "");

/** Align with backend agentaudit.sensitiveKey: exact or substring match. */
const SENSITIVE_KEY_NEEDLES = [
  "password",
  "secret",
  "token",
  "authorization",
  "api_key",
  "apikey",
  "private_key",
  "credential",
  "cookie",
  "email",
  "phone",
  "body",
  "to",
];

const auditSummaryItems = computed<ManagementSummaryItem[]>(() => [
  {
    label: "总调用次数",
    value: agentAudit.stats.totalRuns.toLocaleString(),
    icon: "fa-solid fa-layer-group",
  },
  {
    label: "成功率",
    value: `${agentAudit.stats.successRate.toFixed(1)}%`,
    icon: "fa-solid fa-circle-check",
    tone: "info",
  },
  {
    label: "失败率",
    value: `${agentAudit.stats.failureRate.toFixed(1)}%`,
    icon: "fa-solid fa-circle-xmark",
    tone: "warning",
  },
  {
    label: "平均耗时",
    value: formatLatency(agentAudit.stats.avgLatencyMs),
    icon: "fa-solid fa-clock",
  },
]);

const auditColumns = computed<ManagementListColumn<AgentAuditTraceListItem>[]>(() => [
  {
    key: "traceId",
    label: "Trace ID",
    width: 220,
    getValue: (row) => row.traceId,
  },
  {
    key: "startedAt",
    label: "触发时间",
    width: 160,
    getValue: (row) => formatTime(row.startedAt),
  },
  {
    key: "model",
    label: "模型",
    width: 140,
    getValue: (row) => row.model || "—",
  },
  {
    key: "userLabel",
    label: "用户标识",
    width: 160,
    getValue: (row) => displayUserLabel(row.userLabel),
  },
  {
    key: "status",
    label: "状态",
    width: 100,
    getValue: (row) => statusLabel(row.status),
  },
  {
    key: "latencyMs",
    label: "耗时",
    width: 100,
    getValue: (row) => formatLatency(row.latencyMs),
  },
  {
    key: "actions",
    label: "操作",
    width: 96,
    align: "right",
    headerAlign: "right",
  },
]);

const auditPagination = computed(() => ({
  page: agentAudit.page,
  pageSize: agentAudit.pageSize,
  total: agentAudit.total,
  pageSizeOptions: [10, 20, 50],
}));

onMounted(async () => {
  await runAction(async () => {
    if (!isPlatformAdmin.value) {
      actionError.value = "仅平台管理员可查看 Agent 全链路审计。";
      return;
    }
    if (!workspaces.items.length) await workspaces.load();
    if (!workspaceId.value) return;
    await agentAudit.loadTraces(workspaceId.value, { q: searchInput.value, page: 1 });
    listHasLoaded.value = true;
  }, "全链路审计初始化失败，请稍后重试。");
});

watch(workspaceId, async (next, previous) => {
  if (!isPlatformAdmin.value || !next || next === previous) return;
  agentAudit.goToList();
  await refreshList({ page: 1 });
});

async function refreshList(overrides: { page?: number; pageSize?: number } = {}) {
  if (!workspaceId.value) return;
  await runAction(async () => {
    await agentAudit.loadTraces(workspaceId.value, {
      q: searchInput.value,
      page: overrides.page ?? agentAudit.page,
      pageSize: overrides.pageSize ?? agentAudit.pageSize,
    });
    listHasLoaded.value = true;
  }, "加载调用记录失败。");
}

async function searchList() {
  await refreshList({ page: 1 });
}

async function onAuditSearchUpdate(value: string) {
  searchInput.value = value;
  await searchList();
}

async function onAuditReset() {
  searchInput.value = "";
  await searchList();
}

async function onAuditPageChange(pagination: { page: number; pageSize: number }) {
  await refreshList({ page: pagination.page, pageSize: pagination.pageSize });
  await scrollAuditToTop();
}

function onAuditSelectRow(row: AgentAuditTraceListItem) {
  void openDetail(row.traceId);
}

async function openDetail(traceId: string) {
  if (!workspaceId.value) return;
  await runAction(async () => {
    await agentAudit.loadTraceDetail(workspaceId.value, traceId);
  }, "加载链路详情失败。");
  // List→detail reuses the same scroll container; reset so the header is visible without manual scroll.
  await scrollAuditToTop();
  await setupTimelineInfiniteScroll();
}

async function backToList() {
  teardownTimelineInfiniteScroll();
  agentAudit.goToList();
  await scrollAuditToTop();
}

function teardownTimelineInfiniteScroll() {
  timelineObserver?.disconnect();
  timelineObserver = null;
}

async function setupTimelineInfiniteScroll() {
  teardownTimelineInfiniteScroll();
  await nextTick();
  const sentinel = timelineSentinelRef.value;
  if (!sentinel || typeof IntersectionObserver === "undefined") return;
  const root = pageRef.value?.closest(".content-area") ?? null;
  timelineObserver = new IntersectionObserver(
    (entries) => {
      if (!entries.some((e) => e.isIntersecting)) return;
      void loadMoreTimeline();
    },
    { root: root instanceof Element ? root : null, rootMargin: "120px", threshold: 0 },
  );
  timelineObserver.observe(sentinel);
}

async function loadMoreTimeline() {
  if (!workspaceId.value || !agentAudit.detailHasMore || agentAudit.detailLoadingMore) return;
  await runAction(async () => {
    await agentAudit.loadMoreTraceSteps(workspaceId.value);
  }, "加载更多链路步骤失败。");
  // Keep observing; when hasMore becomes false, sentinel still sits at end (harmless).
}

function stepKey(step: AgentAuditStep, index: number): string {
  return [step.stepId || step.runId || "", step.type, String(step.timeOffsetMs), String(index)].join(":");
}

onBeforeUnmount(() => {
  teardownTimelineInfiniteScroll();
});

async function scrollAuditToTop() {
  await nextTick();
  const contentArea = pageRef.value?.closest(".content-area");
  if (contentArea instanceof HTMLElement) {
    contentArea.scrollTo({ top: 0, behavior: "smooth" });
    return;
  }
  pageRef.value?.scrollIntoView({ block: "start", behavior: "smooth" });
  window.scrollTo({ top: 0, behavior: "smooth" });
}

function formatLatency(ms?: number | null) {
  if (ms == null || Number.isNaN(ms)) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

function formatTime(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function statusLabel(status: string) {
  if (status === "success") return "成功";
  if (status === "error") return "失败";
  if (status === "running") return "运行中";
  return status || "—";
}

function isSensitiveKey(key: string) {
  const lower = key.toLowerCase().trim();
  if (!lower) return false;
  return SENSITIVE_KEY_NEEDLES.some((needle) => lower === needle || lower.includes(needle));
}

function maskValue(key: string, value: unknown): unknown {
  if (!agentAudit.isMasked) return value;
  if (isSensitiveKey(key)) return "********";
  if (value && typeof value === "object" && !Array.isArray(value)) {
    const out: Record<string, unknown> = {};
    for (const [childKey, child] of Object.entries(value as Record<string, unknown>)) {
      out[childKey] = maskValue(childKey, child);
    }
    return out;
  }
  if (Array.isArray(value)) return value.map((item, index) => maskValue(String(index), item));
  return value;
}

function displayJson(data: unknown, state?: string) {
  if (state === "cipher") return { _state: "cipher", message: "（密文不可读）" };
  if (state === "missing") return { _state: "missing", message: "（无数据）" };
  if (data == null) return {};
  if (typeof data === "object") return maskValue("", data);
  if (agentAudit.isMasked && typeof data === "string") return maskSensitiveText(data);
  return data;
}

/** Mask principal IDs / long opaque tokens in free-form labels while keeping type prefix. */
function displayUserLabel(label?: string | null) {
  if (!label) return "—";
  if (!agentAudit.isMasked) return label;
  const separator = label.indexOf(":");
  if (separator > 0) {
    return `${label.slice(0, separator)}:********`;
  }
  return "********";
}

function maskSensitiveText(text: string) {
  if (!text) return text;
  // UUID-like segments and long hex/base tokens
  return text
    .replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, "********")
    .replace(/\b(?:sk|pk|key|token)[-_]?[A-Za-z0-9]{12,}\b/gi, "********");
}

function stepIcon(type: string) {
  if (type === "reasoning") return "fa-solid fa-brain";
  if (type === "tool") return "fa-solid fa-screwdriver-wrench";
  if (type === "output") return "fa-solid fa-circle-check";
  return "fa-solid fa-terminal";
}

function stepText(step: AgentAuditStep) {
  if (
    step.type === "reasoning" &&
    (!step.content || step.contentState === "missing" || step.contentState === "redacted")
  ) {
    const fallback = step.content || "无推理数据";
    return agentAudit.isMasked ? maskSensitiveText(fallback) : fallback;
  }
  if (!step.content) return "";
  return agentAudit.isMasked ? maskSensitiveText(step.content) : step.content;
}

async function runAction(action: () => Promise<void>, fallback: string) {
  actionError.value = "";
  try {
    await action();
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : fallback;
  }
}
</script>

<template>
  <div ref="pageRef" class="page-grid management-page-grid agent-audit-page">
    <ManagementPageHeader
      class="span-12"
      title="Agent 审计中心"
      description="按 Trace ID 查看全链路运行时间轴（仅平台管理员）"
      icon="fa-solid fa-shield"
      eyebrow="ACTWEAVE CONTROL PLANE"
    >
      <template #actions>
        <div class="agent-audit-mask" :class="{ off: !agentAudit.isMasked }">
          <i :class="agentAudit.isMasked ? 'fa-solid fa-shield' : 'fa-solid fa-shield-halved'"></i>
          <span>数据脱敏</span>
          <button
            type="button"
            class="toggle"
            :class="{ on: agentAudit.isMasked }"
            :disabled="!agentAudit.debugMode"
            :title="agentAudit.debugMode ? '切换展示层脱敏' : 'debug 关闭时固定脱敏'"
            @click="agentAudit.toggleMask()"
          >
            <span class="dot"></span>
          </button>
        </div>
      </template>
    </ManagementPageHeader>

    <p v-if="actionError" class="span-12 agent-audit-error">{{ actionError }}</p>

    <template v-if="isPlatformAdmin && agentAudit.view === 'list'">
      <ManagementSummaryStrip class="span-12" :items="auditSummaryItems" />

      <section class="span-12 management-list-card agent-audit-list-card">
        <ManagementList
          class="agent-audit-management-list"
          :rows="agentAudit.items"
          :columns="auditColumns"
          row-key="traceId"
          storage-key="actweave:agent-audit:columns"
          :sticky-right-keys="['actions']"
          :selectable="true"
          :loading="agentAudit.loading"
          :has-loaded="listHasLoaded"
          :search="searchInput"
          search-placeholder="搜索 Trace ID 或 User..."
          search-aria-label="搜索调用记录"
          reset-label="重置"
          reset-aria-label="重置搜索"
          :pagination="auditPagination"
          @select-row="onAuditSelectRow"
          @update:search="onAuditSearchUpdate"
          @reset="onAuditReset"
          @page-change="onAuditPageChange"
        >
          <template #cell-traceId="{ row }">
            <code class="aw-table-mono">{{ row.traceId }}</code>
          </template>
          <template #cell-startedAt="{ row }">
            <span class="aw-table-meta">{{ formatTime(row.startedAt) }}</span>
          </template>
          <template #cell-model="{ row }">
            <span class="aw-table-pill audit-model-pill">{{ row.model || "—" }}</span>
          </template>
          <template #cell-userLabel="{ row }">
            <span class="aw-table-meta">{{ displayUserLabel(row.userLabel) }}</span>
          </template>
          <template #cell-status="{ row }">
            <span class="aw-table-pill audit-status-pill" :class="row.status">{{ statusLabel(row.status) }}</span>
          </template>
          <template #cell-latencyMs="{ row }">
            <span class="aw-table-mono">{{ formatLatency(row.latencyMs) }}</span>
          </template>
          <template #cell-actions>
            <span class="audit-detail-link">详情</span>
          </template>
          <template #empty>
            <div class="empty-state management-registry-empty-state">
              <div class="management-empty-state-icon"><i class="fa-solid fa-chart-line" /></div>
              <h2>暂无调用记录</h2>
              <p>在业务空间内产生 Agent 运行后，将在此按 Trace 展示。</p>
            </div>
          </template>
        </ManagementList>
      </section>
    </template>

    <template v-else-if="isPlatformAdmin && agentAudit.view === 'detail' && agentAudit.selected">
      <div class="span-12 detail-header">
        <button type="button" class="back-btn" @click="backToList">
          <i class="fa-solid fa-arrow-left"></i>
        </button>
        <div>
          <h2>
            链路详情
            <span class="mono pill">{{ agentAudit.selected.traceId }}</span>
          </h2>
          <div class="detail-meta muted">
            <span><i class="fa-solid fa-clock"></i> {{ formatTime(agentAudit.selected.startedAt) }}</span>
            <span><i class="fa-solid fa-microchip"></i> {{ agentAudit.selected.model }}</span>
            <span><i class="fa-solid fa-user"></i> {{ displayUserLabel(agentAudit.selected.userLabel) }}</span>
            <span
              >总耗时: <strong>{{ formatLatency(agentAudit.selected.latencyMs) }}</strong></span
            >
          </div>
        </div>
      </div>

      <div v-if="agentAudit.detailLoading" class="span-12 empty">加载详情中…</div>
      <div v-else class="span-12 timeline">
        <div v-for="(step, index) in agentAudit.selected.steps" :key="stepKey(step, index)" class="timeline-item">
          <div class="timeline-rail">
            <div class="timeline-icon" :class="step.type">
              <i :class="stepIcon(step.type)"></i>
            </div>
            <span class="time mono">{{ (step.timeOffsetMs / 1000).toFixed(2) }}s</span>
          </div>
          <div class="timeline-card" :class="step.type">
            <div class="timeline-card-head">
              <h3>{{ step.title }}</h3>
              <span v-if="step.latencyMs != null" class="mono pill">{{ formatLatency(step.latencyMs) }}</span>
            </div>
            <p v-if="stepText(step)" class="step-content" :class="{ reasoning: step.type === 'reasoning' }">
              {{ stepText(step) }}
            </p>
            <div v-if="step.type === 'tool'" class="tool-grid">
              <div>
                <div class="json-label"><i class="fa-solid fa-code"></i> 调用参数 (Params)</div>
                <pre class="json-view">{{ JSON.stringify(displayJson(step.params, step.paramsState), null, 2) }}</pre>
              </div>
              <div>
                <div class="json-label"><i class="fa-solid fa-wave-square"></i> 返回结果 (Result)</div>
                <pre class="json-view">{{ JSON.stringify(displayJson(step.result, step.resultState), null, 2) }}</pre>
              </div>
            </div>
          </div>
        </div>
        <div ref="timelineSentinelRef" class="timeline-sentinel" aria-hidden="true" />
        <div v-if="agentAudit.detailLoadingMore" class="timeline-loading muted">加载更多步骤…</div>
        <div v-else-if="agentAudit.detailHasMore" class="timeline-loading muted">下拉继续加载</div>
        <div v-else class="timeline-end">
          End of Trace
          <span v-if="(agentAudit.selected.stepTotal ?? 0) > 0" class="muted">
            · 共 {{ agentAudit.selected.stepTotal }} 步
          </span>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.agent-audit-page {
  min-width: 0;
}

.agent-audit-mask {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  background: #f9fafb;
  border: 1px solid #f3f4f6;
  border-radius: 999px;
  padding: 0.4rem 0.75rem;
  font-size: 0.875rem;
}
.agent-audit-mask.off {
  color: #d97706;
}
.toggle {
  width: 2.75rem;
  height: 1.6rem;
  border-radius: 999px;
  border: none;
  background: #e5e7eb;
  position: relative;
  cursor: pointer;
}
.toggle.on {
  background: #10b981;
}
.toggle .dot {
  position: absolute;
  top: 0.2rem;
  left: 0.2rem;
  width: 1.2rem;
  height: 1.2rem;
  border-radius: 999px;
  background: white;
  transition: transform 0.2s ease;
}
.toggle.on .dot {
  transform: translateX(1.1rem);
}
.toggle:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.agent-audit-list-card {
  min-width: 0;
}

.audit-model-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 6px;
  background: #f3f4f6;
  color: #374151;
}

.audit-status-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 6px;
}

.audit-status-pill.success {
  background: #ecfdf5;
  color: #059669;
}
.audit-status-pill.error {
  background: #fff1f2;
  color: #e11d48;
}
.audit-status-pill.running {
  background: #eff6ff;
  color: #2563eb;
}

/* Keep header/body of 操作 column on the same right edge. */
.agent-audit-management-list :deep(.data-table th[data-column-key="actions"]),
.agent-audit-management-list :deep(.data-table td[data-column-key="actions"]) {
  text-align: right;
  vertical-align: middle;
}

.agent-audit-management-list :deep(.data-table th[data-column-key="actions"] .data-table-sort-button),
.agent-audit-management-list :deep(.data-table th[data-column-key="actions"]) {
  justify-content: flex-end;
}

.audit-detail-link {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  min-width: 100%;
  color: #0f766e;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
}

.mono {
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: var(--aw-table-mono-size, 0.75rem);
}
.pill {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  background: #f3f4f6;
  border-radius: 0.5rem;
  padding: 0.15rem 0.5rem;
  font-size: var(--aw-table-pill-size, 0.6875rem);
  font-weight: var(--aw-table-pill-weight, 600);
}
.detail-link {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.35rem;
  color: #059669;
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1;
  white-space: nowrap;
}
.detail-link i {
  font-size: 0.7rem;
  flex: 0 0 auto;
}
.empty {
  text-align: center;
  color: #9ca3af;
  padding: 2rem;
}
.detail-header {
  display: flex;
  gap: 1rem;
  align-items: flex-start;
  margin-bottom: 1.5rem;
}
.back-btn {
  width: 2.5rem;
  height: 2.5rem;
  border-radius: 1rem;
  border: 1px solid #f3f4f6;
  background: white;
  cursor: pointer;
}
.detail-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.85rem;
  font-size: 0.875rem;
  margin-top: 0.35rem;
}
.timeline {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}
.timeline-item {
  display: flex;
  gap: 1rem;
}
.timeline-rail {
  width: 3.5rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.35rem;
  flex-shrink: 0;
}
.timeline-icon {
  width: 2.75rem;
  height: 2.75rem;
  border-radius: 1rem;
  border: 1px solid #e5e7eb;
  display: grid;
  place-items: center;
  background: white;
}
.timeline-icon.tool {
  color: #3b82f6;
  background: #eff6ff;
  border-color: #dbeafe;
}
.timeline-icon.output {
  color: #059669;
  background: #ecfdf5;
  border-color: #d1fae5;
}
.timeline-icon.reasoning {
  color: #6b7280;
  background: #f9fafb;
}
.time {
  color: #9ca3af;
  font-size: 0.65rem;
}
.timeline-card {
  flex: 1;
  border: 1px solid #f3f4f6;
  border-radius: 1.25rem;
  padding: 1.1rem 1.25rem;
  background: white;
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.04);
}
.timeline-card.reasoning {
  background: #fafafa;
  border-style: dashed;
}
.timeline-card-head {
  display: flex;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}
.timeline-card-head h3 {
  margin: 0;
  font-size: 1rem;
}
.step-content {
  margin: 0;
  color: #374151;
  line-height: 1.55;
  font-size: 0.9rem;
}
.step-content.reasoning {
  color: #6b7280;
  font-style: italic;
}
.tool-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 1rem;
  margin-top: 0.85rem;
}
.json-label {
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: #6b7280;
  margin-bottom: 0.4rem;
}
.json-view {
  margin: 0;
  background: #f9fafb;
  border: 1px solid #f3f4f6;
  border-radius: 0.85rem;
  padding: 0.85rem;
  font-size: 0.78rem;
  overflow: auto;
  max-height: 280px;
}
.timeline-sentinel {
  height: 1px;
  width: 100%;
}
.timeline-loading {
  text-align: center;
  font-size: 0.8rem;
  padding: 0.5rem 0 0.25rem;
}
.timeline-end {
  text-align: center;
  color: #9ca3af;
  font-size: 0.7rem;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  margin-top: 1rem;
}
.muted {
  color: #6b7280;
  margin: 0.15rem 0 0;
  font-size: 0.85rem;
}
.agent-audit-error {
  color: #b91c1c;
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 0.75rem;
  padding: 0.75rem 1rem;
}
</style>
