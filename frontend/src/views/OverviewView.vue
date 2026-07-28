<script setup lang="ts">
import "./overview-page.css";
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";

import OverviewCompositeChart from "../components/OverviewCompositeChart.vue";
import OverviewDonutChart, { type DonutSlice } from "../components/OverviewDonutChart.vue";
import { defaultOverviewRange, inclusiveDayCount, useOverviewStore } from "../stores/overview";

const overview = useOverviewStore();
const router = useRouter();

const draftFrom = ref(overview.rangeFrom);
const draftTo = ref(overview.rangeTo);
/** run = KPI + 趋势一屏；insights = 工具/空间榜 + 每日明细 */
const pane = ref<"run" | "insights">("run");

watch(
  () => [overview.rangeFrom, overview.rangeTo] as const,
  ([from, to]) => {
    draftFrom.value = from;
    draftTo.value = to;
  },
);

const kpis = computed(() => overview.kpis);
const series = computed(() => overview.series);
const inventory = computed(() => overview.inventory);
const rangeLabel = computed(() => overview.rangeLabel);
const dateLabels = computed(() => series.value.map((d) => d.date));

const maxDate = computed(() => defaultOverviewRange().to);

const failsTotal = computed(() => {
  const k = kpis.value;
  if (!k) return 0;
  return k.toolCallsFailed + k.runsFailed + (k.workflowFailed || 0);
});

const activePreset = computed(() => {
  const days = inclusiveDayCount(draftFrom.value, draftTo.value);
  if (draftTo.value !== maxDate.value) return 0;
  if (days === 7 || days === 14 || days === 30) return days;
  return 0;
});

const composite = computed(() => ({
  labels: dateLabels.value,
  runs: series.value.map((d) => d.runsTotal),
  tools: series.value.map((d) => d.toolCallsTotal),
  runRates: series.value.map((d) => (d.runsTotal > 0 ? (d.runsSucceeded / d.runsTotal) * 100 : 0)),
}));

const dailyRows = computed(() =>
  [...series.value].reverse().map((d) => {
    const runRate = d.runsTotal > 0 ? (d.runsSucceeded / d.runsTotal) * 100 : null;
    const toolRate = d.toolCallsTotal > 0 ? (d.toolCallsSucceeded / d.toolCallsTotal) * 100 : null;
    return {
      ...d,
      runRate,
      toolRate,
      workflowTotal: d.workflowTotal || 0,
      workflowSucceeded: d.workflowSucceeded || 0,
      workflowFailed: d.workflowFailed || 0,
    };
  }),
);

const topTools = computed(() => overview.metrics?.topTools || []);
const failingTools = computed(() => overview.metrics?.failingTools || []);
const topWorkspaces = computed(() => overview.metrics?.topWorkspaces || []);

/** Keep legend short so donut cards never need internal scroll on one screen. */
const DONUT_TOP_N = 4;

function toDonutSlices(
  items: Array<{ id: string; name: string; value: number; meta?: string }>,
): DonutSlice[] {
  const positive = items.filter((x) => x.value > 0);
  if (!positive.length) return [];
  const head = positive.slice(0, DONUT_TOP_N);
  const rest = positive.slice(DONUT_TOP_N);
  const slices: DonutSlice[] = head.map((x) => ({
    id: x.id,
    name: x.name,
    value: x.value,
    meta: x.meta,
  }));
  if (rest.length) {
    const sum = rest.reduce((s, x) => s + x.value, 0);
    slices.push({ id: "__other__", name: `其他 ${rest.length} 项`, value: sum });
  }
  return slices;
}

const failingDonut = computed(() =>
  toDonutSlices(
    failingTools.value.map((t) => ({
      id: t.id,
      name: t.name,
      value: t.failed,
      meta: formatPct(t.successRate),
    })),
  ),
);

const topToolsDonut = computed(() =>
  toDonutSlices(
    topTools.value.map((t) => ({
      id: t.id,
      name: t.name,
      value: t.total,
      meta: formatMs(t.avgLatencyMs || 0),
    })),
  ),
);

const topWorkspacesDonut = computed(() =>
  toDonutSlices(
    topWorkspaces.value.map((ws) => ({
      id: ws.id,
      name: ws.name,
      // Prefer composite activity; fall back to sessions/runs.
      value: Math.max(ws.total || 0, (ws.sessions || 0) + (ws.runs || 0), ws.sessions || 0, ws.runs || 0),
      meta: `会话 ${ws.sessions || 0}`,
    })),
  ),
);

const FAILING_DONUT_COLORS = ["#f43f5e", "#fb7185", "#f97316", "#f59e0b", "#e11d48", "#be123c", "#94a3b8"];
const TOOLS_DONUT_COLORS = ["#4f46e5", "#6366f1", "#818cf8", "#3b82f6", "#0ea5e9", "#14b8a6", "#94a3b8"];
const WS_DONUT_COLORS = ["#0f9f6e", "#14b8a6", "#2dd4bf", "#059669", "#0d9488", "#10b981", "#94a3b8"];

const riskItems = computed(() =>
  overview.risks.map((risk) => ({
    ...risk,
    icon:
      risk.tone === "red"
        ? "fa-solid fa-triangle-exclamation"
        : risk.tone === "amber"
          ? "fa-solid fa-circle-exclamation"
          : "fa-solid fa-circle-check",
    actionRequired: risk.tone === "red" || risk.tone === "amber",
  })),
);

const hasActionRisk = computed(() => riskItems.value.some((r) => r.actionRequired));

async function applyRange() {
  if (!draftFrom.value || !draftTo.value) return;
  await overview.setRange(draftFrom.value, draftTo.value);
}

async function applyPreset(days: number) {
  const end = new Date(`${maxDate.value}T00:00:00Z`);
  const start = new Date(end);
  start.setUTCDate(start.getUTCDate() - (days - 1));
  const from = start.toISOString().slice(0, 10);
  const to = end.toISOString().slice(0, 10);
  draftFrom.value = from;
  draftTo.value = to;
  await overview.setRange(from, to);
}

function goLogs() {
  void router.push({ name: "logs" });
}

function goChat() {
  void router.push({ name: "chat" });
}

function formatRate(rate: number, total: number) {
  if (!total) return "—";
  return `${rate.toFixed(1)}%`;
}

function formatMs(ms: number) {
  if (!ms || !Number.isFinite(ms)) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

function formatPct(rate: number | null) {
  if (rate == null) return "—";
  return `${rate.toFixed(1)}%`;
}

function exportCsv() {
  if (!dailyRows.value.length) return;
  const header = [
    "日期",
    "会话",
    "Run总计",
    "Run成功",
    "Run失败",
    "链路成功率",
    "工具调用",
    "工具失败",
    "工具成功率",
  ];
  const lines = dailyRows.value.map((row) =>
    [
      row.date,
      row.sessions,
      row.runsTotal,
      row.runsSucceeded,
      row.runsFailed,
      row.runRate == null ? "" : row.runRate.toFixed(1),
      row.toolCallsTotal,
      row.toolCallsFailed,
      row.toolRate == null ? "" : row.toolRate.toFixed(1),
    ].join(","),
  );
  const blob = new Blob([[header.join(","), ...lines].join("\n")], {
    type: "text/csv;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `actweave-overview-${overview.rangeFrom}_${overview.rangeTo}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}
</script>

<template>
  <div
    class="overview-page overview-page--aesthetic"
    :class="{ 'is-insights': pane === 'insights', 'is-run': pane === 'run' }"
    v-loading="overview.loading"
  >
    <!-- Fixed chrome stack: identical on both panes so nothing jumps on switch -->
    <div class="overview-chrome">
      <header class="overview-hero">
        <div class="overview-hero-copy">
          <span class="overview-eyebrow">Workspace Overview</span>
          <h1>空间总览</h1>
          <p>汇总全部可访问业务空间的运行指标：成功率、会话量、工具/工作流执行与资源健康度。</p>
        </div>

        <div class="overview-hero-actions">
          <div class="overview-glass-filter" role="group" aria-label="统计时间筛选">
            <label class="overview-glass-field">
              <i class="fa-regular fa-calendar" aria-hidden="true" />
              <input v-model="draftFrom" type="date" :max="draftTo || maxDate" aria-label="开始日期" />
            </label>
            <span class="overview-glass-sep" aria-hidden="true">–</span>
            <label class="overview-glass-field">
              <input
                v-model="draftTo"
                type="date"
                :min="draftFrom || undefined"
                :max="maxDate"
                aria-label="结束日期"
              />
            </label>
            <button class="overview-glass-query" type="button" :disabled="overview.loading" @click="applyRange">
              查询
            </button>
          </div>

          <div class="overview-preset-group" role="group" aria-label="快捷区间">
            <button
              type="button"
              :class="['overview-preset-btn', { active: activePreset === 7 }]"
              @click="applyPreset(7)"
            >
              近7天
            </button>
            <button
              type="button"
              :class="['overview-preset-btn', { active: activePreset === 14 }]"
              @click="applyPreset(14)"
            >
              近14天
            </button>
            <button
              type="button"
              :class="['overview-preset-btn', { active: activePreset === 30 }]"
              @click="applyPreset(30)"
            >
              近30天
            </button>
          </div>

          <button class="overview-btn overview-btn--ghost" type="button" @click="goLogs">
            <i class="fa-solid fa-terminal" aria-hidden="true" />
            审计日志
          </button>
          <button class="overview-btn overview-btn--primary" type="button" @click="goChat">
            <i class="fa-regular fa-comment-dots" aria-hidden="true" />
            运行调试台
          </button>
        </div>
      </header>

      <p v-if="overview.error" class="overview-error" role="alert">{{ overview.error }}</p>

      <div class="overview-pane-switch" role="tablist" aria-label="总览视图切换">
        <button
          type="button"
          role="tab"
          :aria-selected="pane === 'run'"
          :class="['overview-pane-btn', { active: pane === 'run' }]"
          @click="pane = 'run'"
        >
          <i class="fa-solid fa-chart-line" aria-hidden="true" />
          运行概览
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="pane === 'insights'"
          :class="['overview-pane-btn', { active: pane === 'insights' }]"
          @click="pane = 'insights'"
        >
          <i class="fa-solid fa-table-list" aria-hidden="true" />
          洞察明细
          <span class="overview-pane-hint">工具 / 空间 / 日报</span>
        </button>
      </div>
    </div>

    <!-- KPI row (always on run pane) -->
    <section v-show="pane === 'run'" class="overview-kpi-grid" aria-label="核心指标">
      <article class="overview-kpi-card">
        <div class="overview-kpi-head">
          <span class="overview-kpi-icon overview-kpi-icon--brand"><i class="fa-solid fa-link" /></span>
          <h3>Agent 链路成功率</h3>
        </div>
        <div class="overview-kpi-value">
          {{ formatRate(kpis?.runSuccessRate || 0, kpis?.runsTotal || 0) }}
        </div>
        <div class="overview-kpi-foot">
          <div>
            <span>总运行</span>
            <strong>{{ kpis?.runsTotal ?? "—" }} 次</strong>
          </div>
          <div class="is-end">
            <span>均延时</span>
            <strong>{{ formatMs(kpis?.avgRunLatencyMs || 0) }}</strong>
          </div>
        </div>
      </article>

      <article class="overview-kpi-card">
        <div class="overview-kpi-head">
          <span class="overview-kpi-icon overview-kpi-icon--accent"><i class="fa-solid fa-wrench" /></span>
          <h3>工具调用成功率</h3>
        </div>
        <div class="overview-kpi-value">
          {{ formatRate(kpis?.toolCallSuccessRate || 0, kpis?.toolCallsTotal || 0) }}
        </div>
        <div class="overview-kpi-foot">
          <div>
            <span>总调用</span>
            <strong>{{ kpis?.toolCallsTotal ?? "—" }} 次</strong>
          </div>
          <div class="is-end">
            <span>工作流成功率</span>
            <strong>{{ formatRate(kpis?.workflowSuccessRate || 0, kpis?.workflowTotal || 0) }}</strong>
          </div>
        </div>
      </article>

      <article class="overview-kpi-card">
        <div class="overview-kpi-head">
          <span class="overview-kpi-icon overview-kpi-icon--purple"><i class="fa-solid fa-users" /></span>
          <h3>区间会话数</h3>
        </div>
        <div class="overview-kpi-value overview-kpi-value--inline">
          <span>{{ kpis?.sessionCountPeriod ?? "—" }}</span>
          <small v-if="kpis" class="overview-kpi-chip">
            <i class="fa-solid fa-arrow-trend-up" aria-hidden="true" />
            日均 {{ kpis.avgSessionsPerDay.toFixed(1) }}
          </small>
        </div>
        <div class="overview-kpi-foot">
          <div>
            <span>业务空间</span>
            <strong>{{ inventory?.workspaceCount ?? "—" }} 个</strong>
          </div>
          <div class="is-end">
            <span>Agent / 工具</span>
            <strong>{{ inventory ? `${inventory.agentCount} / ${inventory.toolCount}` : "—" }}</strong>
          </div>
        </div>
      </article>

      <article class="overview-kpi-card overview-kpi-card--danger">
        <div class="overview-kpi-glow" aria-hidden="true" />
        <div class="overview-kpi-head">
          <span class="overview-kpi-icon overview-kpi-icon--danger">
            <i class="fa-solid fa-triangle-exclamation" />
          </span>
          <h3>失败异常合计</h3>
        </div>
        <div class="overview-kpi-value overview-kpi-value--danger">
          <span>{{ kpis ? failsTotal : "—" }}</span>
          <small>次执行失败</small>
        </div>
        <div class="overview-kpi-foot overview-kpi-foot--danger">
          <div>
            <span>失败链路 (Run)</span>
            <strong>{{ kpis?.runsFailed ?? "—" }} 次</strong>
          </div>
          <div class="is-end">
            <span>失败工具</span>
            <strong>{{ kpis?.toolCallsFailed ?? "—" }} 次</strong>
          </div>
        </div>
      </article>
    </section>

    <!-- Trend + risks (run pane) -->
    <div v-show="pane === 'run'" class="overview-mid-grid">
      <section class="overview-panel overview-panel--chart">
        <div class="overview-panel-head">
          <div>
            <h2>流量与运行质量趋势</h2>
            <p>柱状图展示并发调用量，折线图反映链路成功率波形 · {{ rangeLabel }}</p>
          </div>
          <div class="overview-chart-legend">
            <span><i class="is-run" /> Agent Run</span>
            <span><i class="is-tool" /> 工具调用</span>
            <span><i class="is-rate" /> 成功率</span>
          </div>
        </div>
        <OverviewCompositeChart
          v-if="series.length"
          :labels="composite.labels"
          :runs="composite.runs"
          :tools="composite.tools"
          :run-rates="composite.runRates"
          :height="260"
        />
        <div v-else class="overview-empty">暂无时序数据</div>
      </section>

      <section class="overview-panel overview-panel--risk">
        <div class="overview-panel-head">
          <h2>风险与健康度</h2>
          <span v-if="hasActionRisk" class="overview-action-badge">Action Required</span>
          <span v-else class="overview-action-badge is-ok">Healthy</span>
        </div>

        <ul class="overview-risk-list">
          <li v-for="risk in riskItems" :key="risk.title" :class="['overview-risk-item', `tone-${risk.tone}`]">
            <span class="overview-risk-icon" aria-hidden="true"><i :class="risk.icon" /></span>
            <div>
              <h4>{{ risk.title }}</h4>
              <p>{{ risk.detail }}</p>
            </div>
          </li>
        </ul>

        <div class="overview-health-grid">
          <div class="overview-health-tile">
            <span>模型配置 (正常/全部)</span>
            <strong>
              {{ inventory ? `${inventory.modelConfigVerified}/${inventory.modelConfigTotal}` : "—" }}
              <i
                v-if="inventory && inventory.modelConfigVerified >= inventory.modelConfigTotal && inventory.modelConfigTotal > 0"
                class="fa-solid fa-circle-check is-ok"
                aria-hidden="true"
              />
              <i v-else class="fa-solid fa-triangle-exclamation is-warn" aria-hidden="true" />
            </strong>
          </div>
          <div class="overview-health-tile">
            <span>连接状态 (正常/全部)</span>
            <strong>
              {{ inventory ? `${inventory.connectionVerified}/${inventory.connectionTotal}` : "—" }}
              <i
                v-if="inventory && inventory.connectionVerified >= inventory.connectionTotal && inventory.connectionTotal > 0"
                class="fa-solid fa-circle-check is-ok"
                aria-hidden="true"
              />
              <i v-else class="fa-solid fa-triangle-exclamation is-warn" aria-hidden="true" />
            </strong>
          </div>
        </div>
      </section>
    </div>

    <!-- Insights pane: donuts + standard daily table (one screen) -->
    <div v-show="pane === 'insights'" class="overview-insights-pane">
      <div class="overview-donut-grid">
        <section class="overview-panel overview-panel--donut">
          <div class="overview-panel-head overview-panel-head--compact">
            <div class="overview-section-title">
              <i class="bar is-danger" aria-hidden="true" />
              <h2>高频失败工具</h2>
            </div>
            <span class="overview-chip">按失败次数</span>
          </div>
          <OverviewDonutChart
            :slices="failingDonut"
            :colors="FAILING_DONUT_COLORS"
            :size="112"
            empty-text="区间内无工具失败"
            value-label="失败合计"
          />
        </section>

        <section class="overview-panel overview-panel--donut">
          <div class="overview-panel-head overview-panel-head--compact">
            <div class="overview-section-title">
              <i class="bar is-accent" aria-hidden="true" />
              <h2>最活跃调用工具</h2>
            </div>
            <span class="overview-chip">按调用量</span>
          </div>
          <OverviewDonutChart
            :slices="topToolsDonut"
            :colors="TOOLS_DONUT_COLORS"
            :size="112"
            empty-text="暂无工具调用"
            value-label="调用合计"
          />
        </section>

        <section class="overview-panel overview-panel--donut">
          <div class="overview-panel-head overview-panel-head--compact">
            <div class="overview-section-title">
              <i class="bar is-brand" aria-hidden="true" />
              <h2>最活跃业务空间</h2>
            </div>
            <span class="overview-chip">按活跃度</span>
          </div>
          <OverviewDonutChart
            :slices="topWorkspacesDonut"
            :colors="WS_DONUT_COLORS"
            :size="112"
            empty-text="暂无空间活跃度"
            value-label="活跃合计"
          />
        </section>
      </div>

      <section class="overview-panel overview-panel--daily">
        <div class="overview-panel-head overview-panel-head--compact">
          <div class="overview-section-title">
            <i class="bar is-brand" aria-hidden="true" />
            <h2>每日明细报表</h2>
          </div>
          <div class="overview-daily-actions">
            <span class="overview-chip">{{ rangeLabel }} · {{ dailyRows.length }} 天</span>
            <button class="overview-export-btn" type="button" :disabled="!dailyRows.length" @click="exportCsv">
              <i class="fa-solid fa-download" aria-hidden="true" />
              导出 CSV
            </button>
          </div>
        </div>
        <div v-if="dailyRows.length" class="overview-data-table-shell">
          <table class="overview-data-table-std">
            <thead>
              <tr>
                <th>日期</th>
                <th class="is-num">会话</th>
                <th class="is-num">Run 总计</th>
                <th class="is-num">Run 成功</th>
                <th class="is-num">Run 失败</th>
                <th class="is-num">链路成功率</th>
                <th class="is-num">工具调用</th>
                <th class="is-num">工具失败</th>
                <th class="is-num">工具成功率</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in dailyRows" :key="row.date">
                <td class="is-date">{{ row.date }}</td>
                <td class="is-num">{{ row.sessions }}</td>
                <td class="is-num">{{ row.runsTotal }}</td>
                <td class="is-num is-success">{{ row.runsSucceeded }}</td>
                <td class="is-num" :class="{ 'is-danger': row.runsFailed > 0 }">
                  {{ row.runsFailed > 0 ? row.runsFailed : "—" }}
                </td>
                <td class="is-num is-strong">{{ formatPct(row.runRate) }}</td>
                <td class="is-num">{{ row.toolCallsTotal }}</td>
                <td class="is-num" :class="{ 'is-danger': row.toolCallsFailed > 0 }">
                  {{ row.toolCallsFailed > 0 ? row.toolCallsFailed : "—" }}
                </td>
                <td class="is-num is-strong">{{ formatPct(row.toolRate) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="overview-empty">暂无每日明细</div>
      </section>
    </div>
  </div>
</template>
