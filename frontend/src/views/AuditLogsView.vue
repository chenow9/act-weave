<script setup lang="ts">
import "./audit-logs-page.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";

import AgentAuditStepNode from "../components/AgentAuditStepNode.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementSummaryStrip from "../components/ManagementSummaryStrip.vue";
import type { ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { apiClient } from "../services/api";
import { useAgentAuditStore } from "../stores/agentAudit";
import { useAuthStore } from "../stores/auth";
import { useWorkspaceStore } from "../stores/workspaces";
import type { AgentAuditActor, AgentAuditStep, AgentAuditTraceListItem } from "../types/domain";

const { t } = useI18n();
const agentAudit = useAgentAuditStore();
const auth = useAuthStore();
const workspaces = useWorkspaceStore();
const router = useRouter();
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
    label: t("logs.summaryTotal"),
    value: agentAudit.stats.totalRuns.toLocaleString(),
    icon: "fa-solid fa-layer-group",
  },
  {
    label: t("logs.summarySuccessRate"),
    value: `${agentAudit.stats.successRate.toFixed(1)}%`,
    icon: "fa-solid fa-circle-check",
    tone: "info",
  },
  {
    label: t("logs.summaryFailureRate"),
    value: `${agentAudit.stats.failureRate.toFixed(1)}%`,
    icon: "fa-solid fa-circle-xmark",
    tone: "warning",
  },
  {
    label: t("logs.summaryAvgLatency"),
    value: formatLatency(agentAudit.stats.avgLatencyMs),
    icon: "fa-solid fa-clock",
  },
]);

const auditColumns = computed<ManagementListColumn<AgentAuditTraceListItem>[]>(() => [
  {
    key: "traceId",
    label: t("logs.colTraceId"),
    width: 220,
    getValue: (row) => row.traceId,
  },
  {
    key: "startedAt",
    label: t("logs.colStartedAt"),
    width: 160,
    getValue: (row) => formatTime(row.startedAt),
  },
  {
    key: "model",
    label: t("logs.colModel"),
    width: 140,
    getValue: (row) => row.model || "—",
  },
  {
    key: "userLabel",
    label: t("logs.colUser"),
    width: 160,
    getValue: (row) => displayUserLabel(row.userLabel, row.user),
  },
  {
    key: "status",
    label: t("logs.colStatus"),
    width: 100,
    getValue: (row) => statusLabel(row.status),
  },
  {
    key: "latencyMs",
    label: t("logs.colLatency"),
    width: 100,
    getValue: (row) => formatLatency(row.latencyMs),
  },
  {
    key: "actions",
    label: t("logs.colActions"),
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
  // Sidebar re-entry should land on the list, not a previously opened detail
  // (view/selected live in Pinia and survive route remounts).
  teardownTimelineInfiniteScroll();
  agentAudit.goToList();
  await runAction(async () => {
    if (!isPlatformAdmin.value) {
      actionError.value = t("logs.adminOnly");
      return;
    }
    if (!workspaces.items.length) await workspaces.load();
    if (!workspaceId.value) return;
    await agentAudit.loadTraces(workspaceId.value, { q: searchInput.value, page: 1 });
    listHasLoaded.value = true;
  }, t("logs.initFailed"));
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
  }, t("logs.loadListFailed"));
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
    await resolveSelectedActorProfile();
  }, t("logs.loadDetailFailed"));
  // List→detail reuses the same scroll container; reset so the header is visible without manual scroll.
  await scrollAuditToTop();
  await setupTimelineInfiniteScroll();
}

/**
 * Best-effort fill username when the audit API still only has type+id.
 * Prefer username for display (searchable on 用户与权限).
 */
async function resolveSelectedActorProfile() {
  const selected = agentAudit.selected;
  if (!selected) return;
  const actor = selected.user;
  if (isHumanActorName(actor?.username) && !looksLikeUuid(actor?.username)) return;
  if (isHumanActorName(selected.userLabel) && !looksLikeUuid(selected.userLabel)) return;
  const kind = actorKind(actor, selected.userLabel);
  if (kind !== "USER" && kind !== "") return;
  await resolveUsername(actor, selected.userLabel);
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
  }, t("logs.loadMoreFailed"));
  // Keep observing; when hasMore becomes false, sentinel still sits at end (harmless).
}

function stepKey(step: AgentAuditStep, index: number): string {
  return [step.stepId || step.runId || "", step.type, String(step.timeOffsetMs), String(index)].join(":");
}

onBeforeUnmount(() => {
  teardownTimelineInfiniteScroll();
  // Leaving via sidebar/other routes: next visit starts on the list page.
  agentAudit.goToList();
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
  if (status === "success") return t("logs.statusSuccess");
  if (status === "error") return t("logs.statusError");
  if (status === "running") return t("logs.statusRunning");
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
  if (state === "cipher") return { _state: "cipher", message: t("logs.cipherUnreadable") };
  if (state === "missing") return { _state: "missing", message: t("logs.noData") };
  if (data == null) return {};
  if (typeof data === "object") return maskValue("", data);
  if (agentAudit.isMasked && typeof data === "string") return maskSensitiveText(data);
  return data;
}

function actorKind(actor?: AgentAuditActor | null, fallbackLabel?: string | null): string {
  const fromActor = (actor?.type || "").toUpperCase();
  if (fromActor) return fromActor;
  const label = (fallbackLabel || "").trim();
  if (label.includes(":")) return label.split(":")[0].toUpperCase();
  return "";
}

/** True when a string is a resolved human name (not type keyword / bare UUID). */
function isHumanActorName(value?: string | null) {
  const v = (value || "").trim();
  if (!v) return false;
  if (/^(USER|SYSTEM|SERVICE_PRINCIPAL|SERVICE|用户|系统|服务账号|未知主体)$/i.test(v)) return false;
  if (/^(USER|SYSTEM|SERVICE_PRINCIPAL|SERVICE):/i.test(v)) return false;
  if (/^[0-9a-f]{8}-[0-9a-f-]{20,}$/i.test(v)) return false;
  return true;
}

/**
 * Primary label shown in the audit UI.
 * USER → login username first (searchable on /users).
 * SERVICE → client name / id.
 * Never prefer bare UUID when a human name exists.
 */
function actorPrimaryName(actor?: AgentAuditActor | null, fallbackLabel?: string | null) {
  const kind = actorKind(actor, fallbackLabel);
  if (kind === "USER" || kind === "") {
    if (isHumanActorName(actor?.username)) return actor!.username!.trim();
    if (isHumanActorName(actor?.displayName)) return actor!.displayName!.trim();
    if (isHumanActorName(fallbackLabel) && !looksLikeUuid(fallbackLabel)) return fallbackLabel!.trim();
  } else {
    if (isHumanActorName(actor?.clientName)) return actor!.clientName!.trim();
    if (isHumanActorName(actor?.displayName)) return actor!.displayName!.trim();
    if (isHumanActorName(actor?.username)) return actor!.username!.trim();
    if (isHumanActorName(actor?.clientId)) return actor!.clientId!.trim();
    if (isHumanActorName(fallbackLabel) && !looksLikeUuid(fallbackLabel)) return fallbackLabel!.trim();
  }

  // Unresolved profile: show short id only as last resort (will try resolve on open).
  const raw = (fallbackLabel || actor?.id || "").trim();
  if (raw) {
    const id = raw.includes(":") ? raw.slice(raw.indexOf(":") + 1).trim() : raw;
    if (id && agentAudit.isMasked) return t("logs.masked");
    if (id) return id.length > 12 ? `${id.slice(0, 8)}…` : id;
  }
  return t("logs.unknownActor");
}

function looksLikeUuid(value?: string | null) {
  const v = (value || "").trim();
  const id = v.includes(":") ? v.slice(v.indexOf(":") + 1).trim() : v;
  return /^[0-9a-f]{8}-[0-9a-f-]{20,}$/i.test(id);
}

function actorTypeLabel(type?: string) {
  const kind = (type || "").toUpperCase();
  if (kind === "USER") return t("logs.actorUser");
  if (kind === "SYSTEM") return t("logs.actorSystem");
  if (kind === "SERVICE" || kind === "SERVICE_PRINCIPAL") return t("logs.actorService");
  return type || t("logs.actorPrincipal");
}

/** List cell / compact label — always prefer real names; never collapse to「用户」. */
function displayUserLabel(label?: string | null, actor?: AgentAuditActor | null) {
  return actorPrimaryName(actor, label);
}

function actorTooltipLines(actor?: AgentAuditActor | null, fallbackLabel?: string | null): string[] {
  const lines: string[] = [];
  const kind = actorKind(actor, fallbackLabel);
  lines.push(t("logs.actorType", { type: actorTypeLabel(kind) }));
  if (actor?.displayName) lines.push(t("logs.actorDisplayName", { name: actor.displayName }));
  if (actor?.username) lines.push(t("logs.actorUsername", { name: actor.username }));
  if (actor?.clientName) lines.push(t("logs.actorClientName", { name: actor.clientName }));
  if (actor?.clientId && !agentAudit.isMasked) lines.push(t("logs.actorClientId", { id: actor.clientId }));
  else if (actor?.clientId && agentAudit.isMasked) lines.push(t("logs.actorClientIdMasked"));
  if (actor?.id && !agentAudit.isMasked) lines.push(t("logs.actorId", { id: actor.id }));
  else if (actor?.id && agentAudit.isMasked) lines.push(t("logs.actorIdMasked"));
  if (!actor?.displayName && !actor?.username && !actor?.clientName && fallbackLabel && !actor?.id) {
    lines.push(t("logs.actorFallback", { label: fallbackLabel }));
  }
  if (canOpenActor(actor, fallbackLabel)) {
    lines.push(t("logs.clickForDetail"));
  }
  return lines;
}

function canOpenActor(actor?: AgentAuditActor | null, fallbackLabel?: string | null) {
  const kind = actorKind(actor, fallbackLabel);
  if (kind === "SYSTEM") return false;
  // Need at least an id / username / client id to search with.
  if (actor?.username || actor?.displayName || actor?.id || actor?.clientId || actor?.clientName) return true;
  const label = (fallbackLabel || "").trim();
  if (/^(USER|SERVICE_PRINCIPAL|SERVICE):/i.test(label)) return true;
  return Boolean(label) && kind === "USER";
}

/** Navigate to user admin or Agent Access with a prefilled search. */
async function openActor(actor?: AgentAuditActor | null, fallbackLabel?: string | null) {
  if (!canOpenActor(actor, fallbackLabel)) return;
  const kind = actorKind(actor, fallbackLabel);
  const isService = kind === "SERVICE_PRINCIPAL" || kind === "SERVICE";

  if (isService) {
    const q =
      actor?.clientId?.trim() ||
      actor?.clientName?.trim() ||
      actor?.displayName?.trim() ||
      actor?.username?.trim() ||
      "";
    void router.push({ path: "/agent-access", query: q ? { q } : {} });
    return;
  }

  // Platform user: always search by username (admin search does not need UUID).
  let username = actor?.username?.trim() || "";
  if (!username || looksLikeUuid(username)) {
    username = (await resolveUsername(actor, fallbackLabel)) || "";
  }
  // Do not fall back to raw UUID — it will not match username search.
  if (!username || looksLikeUuid(username)) {
    actionError.value = t("logs.resolveUsernameFailed");
    return;
  }
  void router.push({ path: "/users", query: { q: username } });
}

/** Resolve USER actor to login username via admin users API. */
async function resolveUsername(
  actor?: AgentAuditActor | null,
  fallbackLabel?: string | null,
): Promise<string> {
  if (isHumanActorName(actor?.username) && !looksLikeUuid(actor?.username)) {
    return actor!.username!.trim();
  }
  const id = (actor?.id || extractActorId(fallbackLabel) || "").trim();
  if (!id) return "";
  try {
    const res = await apiClient.get<{
      items?: Array<{ id?: string; username?: string; displayName?: string }>;
    }>(`/admin/users?query=${encodeURIComponent(id)}&page=1&pageSize=10`);
    const match =
      res.data.items?.find((u) => u.id === id) ||
      res.data.items?.find((u) => (u.id || "").toLowerCase() === id.toLowerCase());
    if (match?.username) {
      // Patch selected detail so the UI label updates immediately.
      const selected = agentAudit.selected;
      if (selected) {
        agentAudit.selected = {
          ...selected,
          userLabel: match.username,
          user: {
            type: "USER",
            id: match.id || id,
            username: match.username,
            displayName: match.displayName || "",
          },
        };
      }
      return match.username;
    }
  } catch {
    // ignore
  }
  return "";
}

function extractActorId(label?: string | null) {
  const raw = (label || "").trim();
  if (!raw) return "";
  if (raw.includes(":")) return raw.slice(raw.indexOf(":") + 1).trim();
  return raw;
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
  if (type === "context_compaction") return "fa-solid fa-compress";
  if (type === "agent_delegation") return "fa-solid fa-sitemap";
  return "fa-solid fa-terminal";
}

/** Expand/collapse state for nested agent_delegation frames. */
const expandedDelegation = ref<Record<string, boolean>>({});

function isDelegationExpanded(step: AgentAuditStep, index: number) {
  const key = stepKey(step, index);
  if (key in expandedDelegation.value) return expandedDelegation.value[key];
  // Default: expanded for depth 0, collapsed when nested.
  return !(step.collapsed || (step.depth != null && step.depth > 1));
}

function toggleDelegation(step: AgentAuditStep, index: number) {
  const key = stepKey(step, index);
  expandedDelegation.value[key] = !isDelegationExpanded(step, index);
}

/** Human-readable delegation summary (avoid protocol jargon in the timeline). */
function delegationMeta(step: AgentAuditStep) {
  const bits: string[] = [];
  const protocol = (step.protocol || "").toUpperCase();
  const origin = (step.origin || "").toUpperCase();
  const mode = (step.mode || "").toUpperCase();

  if (protocol === "INTERNAL") bits.push(t("logs.delegationInternal"));
  else if (protocol === "A2A") bits.push(t("logs.delegationA2A"));
  else if (step.protocol) bits.push(step.protocol);

  if (mode === "INLINE") bits.push(t("logs.delegationInline"));
  else if (mode === "TASK") bits.push(t("logs.delegationTask"));
  else if (step.mode) bits.push(step.mode);

  // Origin only when it adds info beyond protocol (e.g. external inbound).
  if (origin === "EXTERNAL") bits.push(t("logs.delegationExternal"));
  else if (origin && origin !== "INTERNAL" && origin !== protocol) bits.push(origin);

  if (step.depth != null) {
    bits.push(step.depth === 0 ? t("logs.delegationTop") : t("logs.delegationDepth", { n: step.depth }));
  }

  const status = (step.status || "").toUpperCase();
  if (status === "SUCCEEDED" || status === "SUCCESS") bits.push(t("logs.statusSucceeded"));
  else if (status === "FAILED" || status === "ERROR") bits.push(t("logs.statusFailed"));
  else if (status === "RUNNING") bits.push(t("logs.statusInProgress"));
  else if (status === "TIMED_OUT") bits.push(t("logs.statusTimedOut"));
  else if (status === "CANCELLED") bits.push(t("logs.statusCancelled"));
  else if (step.status) bits.push(step.status);

  if (step.errorCode) bits.push(step.errorCode);
  return bits.join(" · ");
}

function stepText(step: AgentAuditStep) {
  if (
    step.type === "reasoning" &&
    (!step.content || step.contentState === "missing" || step.contentState === "redacted")
  ) {
    const fallback = step.content || t("logs.noReasoning");
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
      :title="t('logs.title')"
      :description="t('logs.description')"
      icon="fa-solid fa-clock-rotate-left"
      eyebrow="ACTWEAVE CONTROL PLANE"
    >
      <template #actions>
        <div class="agent-audit-mask" :class="{ off: !agentAudit.isMasked }">
          <i :class="agentAudit.isMasked ? 'fa-solid fa-shield' : 'fa-solid fa-shield-halved'"></i>
          <span>{{ t("logs.dataMasking") }}</span>
          <button
            type="button"
            class="toggle"
            :class="{ on: agentAudit.isMasked }"
            :disabled="!agentAudit.debugMode"
            :title="agentAudit.debugMode ? t('logs.toggleMask') : t('logs.maskFixed')"
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
          :search-placeholder="t('logs.searchPlaceholder')"
          :search-aria-label="t('logs.searchAria')"
          :reset-label="t('logs.reset')"
          :reset-aria-label="t('logs.resetAria')"
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
            <button
              type="button"
              class="aw-table-meta audit-user-chip audit-user-link"
              :title="actorTooltipLines(row.user, row.userLabel).join('\n')"
              :disabled="!canOpenActor(row.user, row.userLabel)"
              @click.stop="openActor(row.user, row.userLabel)"
            >
              <i
                :class="
                  actorKind(row.user, row.userLabel) === 'SERVICE_PRINCIPAL' ||
                  actorKind(row.user, row.userLabel) === 'SERVICE'
                    ? 'fa-solid fa-id-card'
                    : 'fa-solid fa-user'
                "
                aria-hidden="true"
              />
              {{ displayUserLabel(row.userLabel, row.user) }}
            </button>
          </template>
          <template #cell-status="{ row }">
            <span class="aw-table-pill audit-status-pill" :class="row.status">{{ statusLabel(row.status) }}</span>
          </template>
          <template #cell-latencyMs="{ row }">
            <span class="aw-table-mono">{{ formatLatency(row.latencyMs) }}</span>
          </template>
          <template #cell-actions>
            <span class="audit-detail-link">{{ t("logs.detail") }}</span>
          </template>
          <template #empty>
            <div class="empty-state management-registry-empty-state">
              <div class="management-empty-state-icon"><i class="fa-solid fa-chart-line" /></div>
              <h2>{{ t("logs.emptyTitle") }}</h2>
              <p>{{ t("logs.emptyBody") }}</p>
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
            {{ t("logs.traceDetail") }}
            <span class="mono pill">{{ agentAudit.selected.traceId }}</span>
          </h2>
          <div class="detail-meta muted">
            <span><i class="fa-solid fa-clock"></i> {{ formatTime(agentAudit.selected.startedAt) }}</span>
            <span><i class="fa-solid fa-microchip"></i> {{ agentAudit.selected.model }}</span>
            <span class="audit-user-hover">
              <i
                :class="
                  actorKind(agentAudit.selected.user, agentAudit.selected.userLabel) ===
                    'SERVICE_PRINCIPAL' ||
                  actorKind(agentAudit.selected.user, agentAudit.selected.userLabel) === 'SERVICE'
                    ? 'fa-solid fa-id-card'
                    : 'fa-solid fa-user'
                "
              ></i>
              <button
                type="button"
                class="audit-user-trigger"
                :disabled="!canOpenActor(agentAudit.selected.user, agentAudit.selected.userLabel)"
                @click="openActor(agentAudit.selected.user, agentAudit.selected.userLabel)"
              >
                {{ displayUserLabel(agentAudit.selected.userLabel, agentAudit.selected.user) }}
                <i class="fa-solid fa-arrow-up-right-from-square audit-user-go" aria-hidden="true" />
              </button>
              <div class="audit-user-card" role="tooltip">
                <strong>{{
                  actorPrimaryName(agentAudit.selected.user, agentAudit.selected.userLabel)
                }}</strong>
                <ul>
                  <li
                    v-for="(line, i) in actorTooltipLines(
                      agentAudit.selected.user,
                      agentAudit.selected.userLabel,
                    )"
                    :key="i"
                  >
                    {{ line }}
                  </li>
                </ul>
              </div>
            </span>
            <span
              >{{ t("logs.totalLatency") }}
              <strong>{{ formatLatency(agentAudit.selected.latencyMs) }}</strong></span
            >
          </div>
        </div>
      </div>

      <div v-if="agentAudit.detailLoading" class="span-12 empty">{{ t("logs.loadingDetail") }}</div>
      <div v-else class="span-12 timeline">
        <!-- Recursive node supports A→B→C… agent_delegation nesting of any depth -->
        <AgentAuditStepNode
          v-for="(step, index) in agentAudit.selected.steps"
          :key="stepKey(step, index)"
          :step="step"
          :index="index"
          :depth="0"
          :format-latency="formatLatency"
          :step-text="stepText"
          :display-json="displayJson"
          :step-icon="stepIcon"
          :delegation-meta="delegationMeta"
        />
        <div ref="timelineSentinelRef" class="timeline-sentinel" aria-hidden="true" />
        <div v-if="agentAudit.detailLoadingMore" class="timeline-loading muted">
          {{ t("logs.loadingMoreSteps") }}
        </div>
        <div v-else-if="agentAudit.detailHasMore" class="timeline-loading muted">
          {{ t("logs.scrollForMore") }}
        </div>
        <div v-else class="timeline-end">
          End of Trace
          <span v-if="(agentAudit.selected.stepTotal ?? 0) > 0" class="muted">
            {{ t("logs.stepTotal", { n: agentAudit.selected.stepTotal }) }}
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

.audit-user-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  margin: 0;
  padding: 0;
  color: #0f766e;
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
  text-align: left;
}

.audit-user-chip:disabled {
  color: #64748b;
  cursor: default;
}

.audit-user-chip i {
  color: #94a3b8;
  font-size: 11px;
}

.audit-user-link:hover:not(:disabled) {
  text-decoration: underline;
}

.audit-user-hover {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.audit-user-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 0;
  color: #0f766e;
  background: transparent;
  border: 0;
  border-bottom: 1px dashed rgba(13, 148, 136, 0.45);
  font: inherit;
  font-weight: 700;
  cursor: pointer;
}

.audit-user-trigger:disabled {
  color: #64748b;
  border-bottom-color: transparent;
  cursor: default;
}

.audit-user-trigger:hover:not(:disabled) {
  color: #0f766e;
  border-bottom-style: solid;
}

.audit-user-go {
  font-size: 10px;
  opacity: 0.75;
}

.audit-user-card {
  position: absolute;
  top: calc(100% + 8px);
  left: 0;
  z-index: 20;
  min-width: 200px;
  max-width: 280px;
  padding: 10px 12px;
  color: #e2e8f0;
  background: #0f172a;
  border: 1px solid #1e293b;
  border-radius: 10px;
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.28);
  opacity: 0;
  pointer-events: none;
  transform: translateY(4px);
  transition:
    opacity 0.15s ease,
    transform 0.15s ease;
}

.audit-user-hover:hover .audit-user-card,
.audit-user-hover:focus-within .audit-user-card {
  opacity: 1;
  pointer-events: auto;
  transform: translateY(0);
}

.audit-user-card strong {
  display: block;
  margin-bottom: 6px;
  color: #fff;
  font-size: 13px;
  font-weight: 700;
}

.audit-user-card ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

.audit-user-card li {
  color: #94a3b8;
  font-size: 11px;
  line-height: 1.5;
}

.delegation-toggle {
  border: none;
  background: transparent;
  cursor: pointer;
  margin-right: 0.35rem;
  color: inherit;
  padding: 0 0.15rem;
}

.delegation-children {
  margin-top: 0.75rem;
  padding-left: 0.75rem;
  border-left: 2px solid #e5e7eb;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.timeline-item.nested {
  display: flex;
  gap: 0.75rem;
}

.timeline-card.nested {
  flex: 1;
  background: #fafafa;
}

/* Parent-scoped legacy timeline helpers (recursive nodes use AgentAuditStepNode styles). */
.timeline-icon.input {
  color: #1d4ed8;
  background: #eff6ff;
  border-color: #bfdbfe;
}
.timeline-icon.reasoning {
  color: #7c3aed;
  background: #f5f3ff;
  border-color: #ddd6fe;
}
.timeline-icon.agent_delegation {
  color: #4338ca;
  background: #e0e7ff;
  border-color: #c7d2fe;
}
.timeline-icon.tool {
  color: #b45309;
  background: #fffbeb;
  border-color: #fde68a;
}
.timeline-icon.output {
  color: #047857;
  background: #ecfdf5;
  border-color: #a7f3d0;
}
.timeline-icon.context_compaction {
  color: #0f766e;
  background: #f0fdfa;
  border-color: #99f6e4;
}
.timeline-icon.is-error {
  color: #b91c1c;
  background: #fef2f2;
  border-color: #fecaca;
}

.timeline-card.agent_delegation {
  border-color: #c7d2fe;
}

.timeline-card.FAILED,
.timeline-card.CANCELLED,
.timeline-card.TIMED_OUT,
.timeline-card.ERROR {
  border-color: #fecaca;
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
  color: #64748b;
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
