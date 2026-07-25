<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import AppSelect, { type AppSelectOption } from "../components/AppSelect.vue";
import { useAgentAuditStore } from "../stores/agentAudit";
import { useAuthStore } from "../stores/auth";
import { useWorkspaceStore } from "../stores/workspaces";
import type { AgentAuditStep } from "../types/domain";

const agentAudit = useAgentAuditStore();
const auth = useAuthStore();
const workspaces = useWorkspaceStore();
const pageRef = ref<HTMLElement | null>(null);
const timelineSentinelRef = ref<HTMLElement | null>(null);
const actionError = ref("");
const searchInput = ref("");
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

const pageSizeSelectOptions: AppSelectOption[] = [
  { label: "每页 10 条", value: 10 },
  { label: "每页 20 条", value: 20 },
  { label: "每页 50 条", value: 50 },
];
const pageCount = computed(() => agentAudit.pageCount);
const visiblePageNumbers = computed(() => {
  const count = pageCount.value;
  if (count <= 5) return Array.from({ length: count }, (_, index) => index + 1);
  const start = Math.min(Math.max(1, agentAudit.page - 2), count - 4);
  return Array.from({ length: 5 }, (_, index) => start + index);
});

onMounted(async () => {
  await runAction(async () => {
    if (!isPlatformAdmin.value) {
      actionError.value = "仅平台管理员可查看 Agent 全链路审计。";
      return;
    }
    if (!workspaces.items.length) await workspaces.load();
    if (!workspaceId.value) return;
    await agentAudit.loadTraces(workspaceId.value, { q: searchInput.value, page: 1 });
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
  }, "加载调用记录失败。");
}

async function searchList() {
  await refreshList({ page: 1 });
}

async function changePage(page: number) {
  if (page < 1 || page > pageCount.value || page === agentAudit.page) return;
  await refreshList({ page });
  await scrollAuditToTop();
}

async function changePageSize(value: string | number | boolean) {
  const pageSize = Number(value);
  if (!Number.isFinite(pageSize) || pageSize === agentAudit.pageSize) return;
  await refreshList({ page: 1, pageSize });
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
    .replace(
      /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi,
      "********",
    )
    .replace(/\b(?:sk|pk|key|token)[-_]?[A-Za-z0-9]{12,}\b/gi, "********");
}

function stepIcon(type: string) {
  if (type === "reasoning") return "fa-solid fa-brain";
  if (type === "tool") return "fa-solid fa-screwdriver-wrench";
  if (type === "output") return "fa-solid fa-circle-check";
  return "fa-solid fa-terminal";
}

function stepText(step: AgentAuditStep) {
  if (step.type === "reasoning" && (!step.content || step.contentState === "missing" || step.contentState === "redacted")) {
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
  <div ref="pageRef" class="agent-audit-page">
    <header class="agent-audit-header">
      <div class="agent-audit-brand">
        <div class="agent-audit-brand-icon"><i class="fa-solid fa-shield"></i></div>
        <div>
          <h1>Agent 审计中心</h1>
          <p class="muted">按 Trace ID 查看全链路运行时间轴（仅平台管理员）</p>
        </div>
      </div>
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
    </header>

    <p v-if="actionError" class="agent-audit-error">{{ actionError }}</p>

    <template v-if="isPlatformAdmin && agentAudit.view === 'list'">
      <div class="stats-grid">
        <div class="stat-card">
          <div class="stat-label"><i class="fa-solid fa-layer-group"></i> 总调用次数</div>
          <div class="stat-value">{{ agentAudit.stats.totalRuns.toLocaleString() }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label"><i class="fa-solid fa-circle-check"></i> 成功率</div>
          <div class="stat-value">{{ agentAudit.stats.successRate.toFixed(1) }}%</div>
        </div>
        <div class="stat-card">
          <div class="stat-label"><i class="fa-solid fa-circle-xmark"></i> 失败率</div>
          <div class="stat-value">{{ agentAudit.stats.failureRate.toFixed(1) }}%</div>
        </div>
        <div class="stat-card">
          <div class="stat-label"><i class="fa-solid fa-clock"></i> 平均耗时</div>
          <div class="stat-value">{{ formatLatency(agentAudit.stats.avgLatencyMs) }}</div>
        </div>
      </div>

      <div class="list-toolbar">
        <h2><i class="fa-solid fa-chart-bar"></i> 调用记录</h2>
        <div class="list-tools">
          <input
            v-model="searchInput"
            class="search-input"
            type="search"
            placeholder="搜索 Trace ID 或 User..."
            @keydown.enter="searchList"
          />
          <button type="button" class="btn" :disabled="agentAudit.loading" @click="searchList">
            {{ agentAudit.loading ? "加载中…" : "刷新" }}
          </button>
        </div>
      </div>

      <div class="table-card">
        <table class="trace-table">
          <thead>
            <tr>
              <th>Trace ID</th>
              <th>触发时间</th>
              <th>模型</th>
              <th>用户标识</th>
              <th>状态</th>
              <th>耗时</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="!agentAudit.items.length">
              <td colspan="7" class="empty">暂无调用记录</td>
            </tr>
            <tr
              v-for="item in agentAudit.items"
              :key="item.traceId"
              class="trace-row"
              @click="openDetail(item.traceId)"
            >
              <td class="mono aw-table-mono">{{ item.traceId }}</td>
              <td class="aw-table-meta">{{ formatTime(item.startedAt) }}</td>
              <td><span class="pill aw-table-pill">{{ item.model || "—" }}</span></td>
              <td class="aw-table-meta">{{ displayUserLabel(item.userLabel) }}</td>
              <td>
                <span class="status aw-table-pill" :class="item.status">{{ statusLabel(item.status) }}</span>
              </td>
              <td class="mono aw-table-mono">{{ formatLatency(item.latencyMs) }}</td>
              <td class="detail-cell">
                <span class="detail-link">详情 <i class="fa-solid fa-chevron-right" aria-hidden="true" /></span>
              </td>
            </tr>
          </tbody>
        </table>
        <nav class="trace-pagination" aria-label="调用记录分页">
          <span>共 {{ agentAudit.total }} 条 · 第 {{ agentAudit.page }} / {{ pageCount }} 页</span>
          <div class="trace-pagination-controls">
            <div class="trace-page-size">
              <AppSelect
                :model-value="agentAudit.pageSize"
                :options="pageSizeSelectOptions"
                :disabled="agentAudit.loading"
                compact
                aria-label="每页条数"
                @update:model-value="changePageSize"
              />
            </div>
            <button
              class="trace-page-button"
              type="button"
              aria-label="上一页"
              :disabled="agentAudit.loading || agentAudit.page <= 1"
              @click="changePage(agentAudit.page - 1)"
            >
              <i class="fa-solid fa-chevron-left" aria-hidden="true" />
            </button>
            <button
              v-for="pageNumber in visiblePageNumbers"
              :key="pageNumber"
              class="trace-page-button"
              :class="{ active: agentAudit.page === pageNumber }"
              type="button"
              :aria-label="`第 ${pageNumber} 页`"
              :aria-current="agentAudit.page === pageNumber ? 'page' : undefined"
              :disabled="agentAudit.loading"
              @click="changePage(pageNumber)"
            >
              {{ pageNumber }}
            </button>
            <button
              class="trace-page-button"
              type="button"
              aria-label="下一页"
              :disabled="agentAudit.loading || agentAudit.page >= pageCount"
              @click="changePage(agentAudit.page + 1)"
            >
              <i class="fa-solid fa-chevron-right" aria-hidden="true" />
            </button>
          </div>
        </nav>
      </div>
    </template>

    <template v-else-if="isPlatformAdmin && agentAudit.view === 'detail' && agentAudit.selected">
      <div class="detail-header">
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
            <span>总耗时: <strong>{{ formatLatency(agentAudit.selected.latencyMs) }}</strong></span>
          </div>
        </div>
      </div>

      <div v-if="agentAudit.detailLoading" class="empty">加载详情中…</div>
      <div v-else class="timeline">
        <div
          v-for="(step, index) in agentAudit.selected.steps"
          :key="stepKey(step, index)"
          class="timeline-item"
        >
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
  max-width: 1120px;
  margin: 0 auto;
  padding: 1.5rem 1.25rem 3rem;
}
.agent-audit-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  margin-bottom: 1.5rem;
  flex-wrap: wrap;
}
.agent-audit-brand {
  display: flex;
  gap: 0.85rem;
  align-items: center;
}
.agent-audit-brand h1 {
  margin: 0;
  font-size: 1.25rem;
}
.agent-audit-brand-icon {
  width: 2.25rem;
  height: 2.25rem;
  border-radius: 0.75rem;
  background: #10b981;
  color: white;
  display: grid;
  place-items: center;
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
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 1rem;
  margin-bottom: 1.5rem;
}
.stat-card {
  border: 1px solid #f3f4f6;
  border-radius: 1.25rem;
  padding: 1.1rem 1.2rem;
  background: white;
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.04);
}
.stat-label {
  color: #6b7280;
  font-size: 0.85rem;
  margin-bottom: 0.6rem;
  display: flex;
  gap: 0.4rem;
  align-items: center;
}
.stat-value {
  font-size: 1.75rem;
  font-weight: 700;
}
.list-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
  margin-bottom: 0.85rem;
}
.list-toolbar h2 {
  margin: 0;
  font-size: 1.1rem;
  display: flex;
  gap: 0.45rem;
  align-items: center;
}
.list-tools {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.search-input,
.btn {
  border: 1px solid #e5e7eb;
  border-radius: 0.75rem;
  padding: 0.55rem 0.8rem;
  background: #f9fafb;
  font-size: 0.875rem;
}
.search-input {
  min-width: 220px;
}
.btn {
  cursor: pointer;
  font-weight: 600;
}
.table-card {
  border: 1px solid #f3f4f6;
  border-radius: 1.25rem;
  overflow: hidden;
  background: white;
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.04);
}
.table-card .trace-table {
  display: table;
}
.trace-pagination {
  display: flex;
  min-height: 56px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  padding: 0 16px;
  border-top: 1px solid #f3f4f6;
  color: #6b7280;
  font-size: 0.8125rem;
  background: #fff;
}
.trace-pagination-controls {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.trace-page-size {
  width: 128px;
  flex: 0 0 auto;
}

.trace-page-size :deep(.app-select) {
  width: 100%;
}
.trace-page-button {
  min-width: 32px;
  min-height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0 8px;
  border: 0;
  border-radius: 0.5rem;
  background: transparent;
  color: #6b7280;
  font: inherit;
  font-size: 0.8125rem;
  cursor: pointer;
}
.trace-page-button:hover:not(:disabled),
.trace-page-button.active {
  background: #f3f4f6;
  color: #111827;
  font-weight: 700;
}
.trace-page-button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
.trace-table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
  font-family: var(--aw-table-font, Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif);
}
.trace-table th {
  background: #f9fafb;
  color: var(--aw-table-header-color, #6b7280);
  font-size: var(--aw-table-header-size, 0.75rem);
  font-weight: var(--aw-table-header-weight, 600);
  padding: 0.9rem 1rem;
}
.trace-table td {
  padding: 0.9rem 1rem;
  border-top: 1px solid #f9fafb;
  color: var(--aw-table-body-color, #374151);
  font-size: var(--aw-table-body-size, 0.8125rem);
  font-weight: var(--aw-table-body-weight, 400);
}
.trace-row {
  cursor: pointer;
}
.trace-row:hover {
  background: #fafafa;
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
.status {
  display: inline-flex;
  border-radius: 0.5rem;
  padding: 0.15rem 0.5rem;
  font-size: var(--aw-table-pill-size, 0.6875rem);
  font-weight: var(--aw-table-pill-weight, 600);
}
.status.success {
  background: #ecfdf5;
  color: #059669;
}
.status.error {
  background: #fff1f2;
  color: #e11d48;
}
.status.running {
  background: #eff6ff;
  color: #2563eb;
}
.trace-table th:last-child,
.trace-table td.detail-cell {
  width: 5.75rem;
  min-width: 5.75rem;
  max-width: 5.75rem;
  padding-right: 1rem;
  text-align: right;
  white-space: nowrap;
  vertical-align: middle;
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
