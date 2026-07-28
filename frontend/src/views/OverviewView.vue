<script setup lang="ts">
import "./overview-page.css";
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";

import OverviewBarChart from "../components/OverviewBarChart.vue";
import OverviewSparkline from "../components/OverviewSparkline.vue";
import { defaultOverviewRange, useOverviewStore } from "../stores/overview";

const overview = useOverviewStore();
const router = useRouter();

const draftFrom = ref(overview.rangeFrom);
const draftTo = ref(overview.rangeTo);

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

const metricCards = computed(() => {
  const k = kpis.value;
  const inv = inventory.value;
  if (!k || !inv) {
    return [
      { key: "tool", label: "工具调用成功率", value: "—", detail: "加载中" },
      { key: "run", label: "Agent 链路成功率", value: "—", detail: "加载中" },
      { key: "wf", label: "工作流成功率", value: "—", detail: "加载中" },
      { key: "session", label: "区间会话数", value: "—", detail: "加载中" },
      { key: "tool-n", label: "工具调用次数", value: "—", detail: "加载中" },
      { key: "run-n", label: "Agent Run 次数", value: "—", detail: "加载中" },
      { key: "fail", label: "失败合计", value: "—", detail: "加载中" },
      { key: "assets", label: "资源规模", value: "—", detail: "加载中" },
    ];
  }
  const fails = k.toolCallsFailed + k.runsFailed + (k.workflowFailed || 0);
  return [
    {
      key: "tool",
      label: "工具调用成功率",
      value: formatRate(k.toolCallSuccessRate, k.toolCallsTotal),
      detail: k.toolCallsTotal
        ? `成功 ${k.toolCallsSucceeded} / 失败 ${k.toolCallsFailed} · 均延时 ${formatMs(k.avgToolLatencyMs)}`
        : "区间内暂无工具调用",
    },
    {
      key: "run",
      label: "Agent 链路成功率",
      value: formatRate(k.runSuccessRate, k.runsTotal),
      detail: k.runsTotal
        ? `成功 ${k.runsSucceeded} / 失败 ${k.runsFailed} · 均延时 ${formatMs(k.avgRunLatencyMs)}`
        : "区间内暂无 Agent Run",
    },
    {
      key: "wf",
      label: "工作流成功率",
      value: formatRate(k.workflowSuccessRate || 0, k.workflowTotal || 0),
      detail: k.workflowTotal
        ? `成功 ${k.workflowSucceeded} / 失败 ${k.workflowFailed} · 均延时 ${formatMs(k.avgWorkflowLatencyMs || 0)}`
        : "区间内暂无工作流执行",
    },
    {
      key: "session",
      label: "区间会话数",
      value: String(k.sessionCountPeriod),
      detail: `今日 ${k.sessionCountToday} · 日均 ${k.avgSessionsPerDay.toFixed(1)}`,
    },
    {
      key: "tool-n",
      label: "工具调用次数",
      value: String(k.toolCallsTotal),
      detail: `成功 ${k.toolCallsSucceeded} · 失败 ${k.toolCallsFailed}`,
    },
    {
      key: "run-n",
      label: "Agent Run 次数",
      value: String(k.runsTotal),
      detail: `成功 ${k.runsSucceeded} · 失败 ${k.runsFailed}`,
    },
    {
      key: "fail",
      label: "失败合计",
      value: String(fails),
      detail: `工具 ${k.toolCallsFailed} · Run ${k.runsFailed} · 工作流 ${k.workflowFailed || 0}`,
    },
    {
      key: "assets",
      label: "资源规模",
      value: String(inv.workspaceCount),
      detail: `Agent ${inv.agentCount} · 工具 ${inv.toolCount} · 工作流 ${inv.workflowCount}`,
    },
  ];
});

const trafficBar = computed(() => ({
  labels: dateLabels.value,
  series: [
    { key: "sessions", label: "会话", color: "#0d9488", values: series.value.map((d) => d.sessions) },
    { key: "runs", label: "Agent Run", color: "#2563eb", values: series.value.map((d) => d.runsTotal) },
    { key: "tools", label: "工具调用", color: "#4f46e5", values: series.value.map((d) => d.toolCallsTotal) },
    {
      key: "wf",
      label: "工作流",
      color: "#0891b2",
      values: series.value.map((d) => d.workflowTotal || 0),
    },
  ],
}));

const outcomeBar = computed(() => ({
  labels: dateLabels.value,
  series: [
    { key: "run-ok", label: "Run 成功", color: "#0d9488", values: series.value.map((d) => d.runsSucceeded) },
    { key: "run-fail", label: "Run 失败", color: "#dc2626", values: series.value.map((d) => d.runsFailed) },
    { key: "tool-ok", label: "工具成功", color: "#10b981", values: series.value.map((d) => d.toolCallsSucceeded) },
    { key: "tool-fail", label: "工具失败", color: "#d97706", values: series.value.map((d) => d.toolCallsFailed) },
  ],
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

const maxDate = computed(() => defaultOverviewRange().to);

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

function goSmartDag() {
  void router.push({ name: "smart-dag" });
}

function goLogs() {
  void router.push({ name: "logs" });
}

function goChat() {
  void router.push({ name: "chat" });
}

function goWorkspaces() {
  void router.push({ name: "workspaces" });
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
</script>

<template>
  <div class="page-grid overview-page" v-loading="overview.loading">
    <header class="page-header span-12">
      <div>
        <span>Workspace Overview</span>
        <h2>空间总览</h2>
        <p>汇总全部可访问业务空间的运行指标：成功率、会话量、工具/工作流执行与资源健康度。</p>
      </div>
      <div class="page-header-actions overview-header-actions">
        <div class="overview-date-filter" role="group" aria-label="统计时间筛选">
          <label class="overview-date-field">
            <span>开始</span>
            <input v-model="draftFrom" type="date" :max="draftTo || maxDate" aria-label="开始日期" />
          </label>
          <span class="overview-date-sep">至</span>
          <label class="overview-date-field">
            <span>结束</span>
            <input v-model="draftTo" type="date" :min="draftFrom || undefined" :max="maxDate" aria-label="结束日期" />
          </label>
          <button class="ghost-button" type="button" :disabled="overview.loading" @click="applyRange">查询</button>
          <div class="overview-date-presets">
            <button type="button" class="ghost-button compact" @click="applyPreset(7)">近 7 天</button>
            <button type="button" class="ghost-button compact" @click="applyPreset(14)">近 14 天</button>
            <button type="button" class="ghost-button compact" @click="applyPreset(30)">近 30 天</button>
          </div>
        </div>
        <button class="ghost-button" type="button" @click="goLogs">
          <i class="fa-solid fa-terminal" />
          审计日志
        </button>
        <button class="primary-button" type="button" @click="goChat">
          <i class="fa-regular fa-comment-dots" />
          运行调试台
        </button>
      </div>
    </header>

    <p v-if="overview.error" class="span-12 overview-error">{{ overview.error }}</p>

    <section v-for="metric in metricCards" :key="metric.key" class="metric-card overview-metric">
      <span>{{ metric.label }}</span>
      <strong>{{ metric.value }}</strong>
      <small>{{ metric.detail }}</small>
    </section>

    <section class="panel span-8">
      <div class="panel-title">
        <strong>每日流量</strong>
        <span>{{ rangeLabel }} · 鼠标悬停查看当日明细</span>
      </div>
      <OverviewBarChart v-if="series.length" :labels="trafficBar.labels" :series="trafficBar.series" :height="240" />
      <div v-else class="empty-state">暂无时序数据</div>
    </section>

    <section class="panel span-4">
      <div class="panel-title">
        <strong>趋势快照</strong>
        <span>悬停数据点查看</span>
      </div>
      <div class="overview-spark-grid">
        <article class="overview-spark-card">
          <header>
            <strong>工具成功率</strong>
            <small>{{ formatRate(kpis?.toolCallSuccessRate || 0, kpis?.toolCallsTotal || 0) }}</small>
          </header>
          <OverviewSparkline
            :values="series.map((d) => (d.toolCallsTotal > 0 ? (d.toolCallsSucceeded / d.toolCallsTotal) * 100 : 0))"
            :labels="dateLabels"
            unit="%"
            stroke="#4f46e5"
            fill="rgba(79,70,229,0.08)"
          />
        </article>
        <article class="overview-spark-card">
          <header>
            <strong>链路成功率</strong>
            <small>{{ formatRate(kpis?.runSuccessRate || 0, kpis?.runsTotal || 0) }}</small>
          </header>
          <OverviewSparkline
            :values="series.map((d) => (d.runsTotal > 0 ? (d.runsSucceeded / d.runsTotal) * 100 : 0))"
            :labels="dateLabels"
            unit="%"
            stroke="#2563eb"
            fill="rgba(37,99,235,0.08)"
          />
        </article>
        <article class="overview-spark-card">
          <header>
            <strong>每日会话</strong>
            <small>今日 {{ kpis?.sessionCountToday ?? 0 }}</small>
          </header>
          <OverviewSparkline
            :values="series.map((d) => d.sessions)"
            :labels="dateLabels"
            stroke="#0d9488"
            fill="rgba(13,148,136,0.08)"
          />
        </article>
      </div>
    </section>

    <section class="panel span-8">
      <div class="panel-title">
        <strong>成功 / 失败分布</strong>
        <span>{{ rangeLabel }} · 悬停查看当日明细</span>
      </div>
      <OverviewBarChart v-if="series.length" :labels="outcomeBar.labels" :series="outcomeBar.series" :height="240" />
      <div v-else class="empty-state">暂无结果数据</div>
    </section>

    <section class="panel span-4">
      <div class="panel-title">
        <strong>风险与配置</strong>
        <span>需要关注</span>
      </div>
      <div class="risk-list">
        <article v-for="risk in overview.risks" :key="risk.title" :class="['risk-item', risk.tone]">
          <i />
          <span>
            <strong>{{ risk.title }}</strong>
            <small>{{ risk.detail }}</small>
          </span>
        </article>
      </div>
      <div class="overview-inventory">
        <p>
          <span>业务空间</span><strong>{{ inventory?.workspaceCount ?? "—" }}</strong>
        </p>
        <p>
          <span>Agent</span><strong>{{ inventory?.agentCount ?? "—" }}</strong>
        </p>
        <p>
          <span>工具</span><strong>{{ inventory?.toolCount ?? "—" }}</strong>
        </p>
        <p>
          <span>工作流</span><strong>{{ inventory?.workflowCount ?? "—" }}</strong>
        </p>
        <p>
          <span>连接（已验证/全部）</span>
          <strong>{{ inventory ? `${inventory.connectionVerified}/${inventory.connectionTotal}` : "—" }}</strong>
        </p>
        <p>
          <span>模型（已验证/全部）</span>
          <strong>{{ inventory ? `${inventory.modelConfigVerified}/${inventory.modelConfigTotal}` : "—" }}</strong>
        </p>
      </div>
      <div class="overview-side-actions">
        <button class="ghost-button" type="button" @click="goSmartDag">
          <i class="fa-solid fa-wand-magic-sparkles" />
          智能编排
        </button>
        <button class="ghost-button" type="button" @click="goWorkspaces">
          <i class="fa-solid fa-cubes" />
          业务空间
        </button>
      </div>
    </section>

    <section class="panel span-12">
      <div class="panel-title">
        <strong>每日明细</strong>
        <span>{{ rangeLabel }} · 共 {{ dailyRows.length }} 天</span>
      </div>
      <div v-if="dailyRows.length" class="overview-table-wrap">
        <table class="overview-data-table">
          <thead>
            <tr>
              <th>日期</th>
              <th>会话</th>
              <th>Run</th>
              <th>Run 成功</th>
              <th>Run 失败</th>
              <th>链路成功率</th>
              <th>工具调用</th>
              <th>工具成功</th>
              <th>工具失败</th>
              <th>工具成功率</th>
              <th>工作流</th>
              <th>工作流失败</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in dailyRows" :key="row.date">
              <td>{{ row.date }}</td>
              <td>{{ row.sessions }}</td>
              <td>{{ row.runsTotal }}</td>
              <td>{{ row.runsSucceeded }}</td>
              <td :class="{ 'is-warn': row.runsFailed > 0 }">{{ row.runsFailed }}</td>
              <td>{{ formatPct(row.runRate) }}</td>
              <td>{{ row.toolCallsTotal }}</td>
              <td>{{ row.toolCallsSucceeded }}</td>
              <td :class="{ 'is-warn': row.toolCallsFailed > 0 }">{{ row.toolCallsFailed }}</td>
              <td>{{ formatPct(row.toolRate) }}</td>
              <td>{{ row.workflowTotal }}</td>
              <td :class="{ 'is-warn': row.workflowFailed > 0 }">{{ row.workflowFailed }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">暂无每日明细</div>
    </section>

    <section class="panel span-4">
      <div class="panel-title">
        <strong>调用最多的工具</strong>
        <span>Top {{ topTools.length }}</span>
      </div>
      <div v-if="topTools.length" class="overview-table-wrap">
        <table class="overview-data-table overview-data-table--compact">
          <thead>
            <tr>
              <th>工具</th>
              <th>调用</th>
              <th>失败</th>
              <th>成功率</th>
              <th>均延时</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in topTools" :key="row.id">
              <td class="is-name" :title="row.name">{{ row.name }}</td>
              <td>{{ row.total }}</td>
              <td :class="{ 'is-warn': row.failed > 0 }">{{ row.failed }}</td>
              <td>{{ formatPct(row.successRate) }}</td>
              <td>{{ formatMs(row.avgLatencyMs || 0) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">暂无工具调用</div>
    </section>

    <section class="panel span-4">
      <div class="panel-title">
        <strong>失败较多的工具</strong>
        <span>Top {{ failingTools.length }}</span>
      </div>
      <div v-if="failingTools.length" class="overview-table-wrap">
        <table class="overview-data-table overview-data-table--compact">
          <thead>
            <tr>
              <th>工具</th>
              <th>失败</th>
              <th>调用</th>
              <th>成功率</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in failingTools" :key="row.id">
              <td class="is-name" :title="row.name">{{ row.name }}</td>
              <td class="is-warn">{{ row.failed }}</td>
              <td>{{ row.total }}</td>
              <td>{{ formatPct(row.successRate) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">区间内无工具失败</div>
    </section>

    <section class="panel span-4">
      <div class="panel-title">
        <strong>最活跃业务空间</strong>
        <span>Top {{ topWorkspaces.length }}</span>
      </div>
      <div v-if="topWorkspaces.length" class="overview-table-wrap">
        <table class="overview-data-table overview-data-table--compact">
          <thead>
            <tr>
              <th>空间</th>
              <th>会话</th>
              <th>Run</th>
              <th>工具</th>
              <th>Run 成功率</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in topWorkspaces" :key="row.id">
              <td class="is-name" :title="row.name">{{ row.name }}</td>
              <td>{{ row.sessions || 0 }}</td>
              <td>{{ row.runs || 0 }}</td>
              <td>{{ row.toolCalls || 0 }}</td>
              <td>{{ row.runs ? formatPct(row.successRate) : "—" }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">暂无空间活跃度</div>
    </section>
  </div>
</template>
