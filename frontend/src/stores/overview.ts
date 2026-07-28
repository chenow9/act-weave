import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import type { OverviewMetrics, RiskItem } from "../types/domain";

const DEFAULT_WINDOW_DAYS = 14;
const MAX_WINDOW_DAYS = 366;

export type OverviewDateRange = {
  from: string; // YYYY-MM-DD inclusive
  to: string; // YYYY-MM-DD inclusive
};

interface OverviewState {
  metrics: OverviewMetrics | null;
  risks: RiskItem[];
  rangeFrom: string;
  rangeTo: string;
  loading: boolean;
  error: string;
}

function formatDateUTC(d: Date): string {
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function startOfUTCDay(d = new Date()): Date {
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
}

/** Default range: last 14 inclusive UTC days ending today. */
export function defaultOverviewRange(now = new Date()): OverviewDateRange {
  const end = startOfUTCDay(now);
  const start = new Date(end);
  start.setUTCDate(start.getUTCDate() - (DEFAULT_WINDOW_DAYS - 1));
  return { from: formatDateUTC(start), to: formatDateUTC(end) };
}

export function inclusiveDayCount(from: string, to: string): number {
  const a = Date.parse(`${from}T00:00:00Z`);
  const b = Date.parse(`${to}T00:00:00Z`);
  if (!Number.isFinite(a) || !Number.isFinite(b) || b < a) return 0;
  return Math.floor((b - a) / 86_400_000) + 1;
}

export const useOverviewStore = defineStore("overview", {
  state: (): OverviewState => {
    const range = defaultOverviewRange();
    return {
      metrics: null,
      risks: [],
      rangeFrom: range.from,
      rangeTo: range.to,
      loading: false,
      error: "",
    };
  },
  getters: {
    kpis: (state) => state.metrics?.kpis ?? null,
    series: (state) => state.metrics?.series ?? [],
    inventory: (state) => state.metrics?.inventory ?? null,
    workspaceCount: (state) => state.metrics?.workspaceCount ?? state.metrics?.inventory?.workspaceCount ?? 0,
    rangeLabel: (state) => `${state.rangeFrom} ~ ${state.rangeTo}`,
    windowDays: (state) => state.metrics?.windowDays ?? inclusiveDayCount(state.rangeFrom, state.rangeTo),
  },
  actions: {
    /** Platform overview: all accessible workspaces for the selected date range. */
    async load(range?: Partial<OverviewDateRange>) {
      this.loading = true;
      this.error = "";
      try {
        const next = normalizeRange({
          from: range?.from ?? this.rangeFrom,
          to: range?.to ?? this.rangeTo,
        });
        this.rangeFrom = next.from;
        this.rangeTo = next.to;
        const response = await apiClient.get<OverviewMetrics>(`/overview/metrics`, {
          params: { from: next.from, to: next.to },
        });
        this.metrics = response.data;
        // Prefer server-normalized bounds when present.
        if (response.data.fromDate) this.rangeFrom = response.data.fromDate;
        if (response.data.toDate) this.rangeTo = response.data.toDate;
        this.risks = collectRisks(response.data);
      } catch (error) {
        this.error = error instanceof Error ? error.message : "加载空间总览失败";
        throw error;
      } finally {
        this.loading = false;
      }
    },

    async setRange(from: string, to: string) {
      await this.load({ from, to });
    },
  },
});

function normalizeRange(range: OverviewDateRange): OverviewDateRange {
  let from = (range.from || "").trim();
  let to = (range.to || "").trim();
  const fallback = defaultOverviewRange();
  if (!/^\d{4}-\d{2}-\d{2}$/.test(from)) from = fallback.from;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(to)) to = fallback.to;
  if (to < from) {
    const tmp = from;
    from = to;
    to = tmp;
  }
  const days = inclusiveDayCount(from, to);
  if (days > MAX_WINDOW_DAYS) {
    // Keep `to`, pull `from` back to max window.
    const end = new Date(`${to}T00:00:00Z`);
    end.setUTCDate(end.getUTCDate() - (MAX_WINDOW_DAYS - 1));
    from = formatDateUTC(end);
  }
  return { from, to };
}

function collectRisks(metrics: OverviewMetrics): RiskItem[] {
  const risks: RiskItem[] = [];
  const inv = metrics.inventory;
  const kpis = metrics.kpis;
  const spaces = metrics.workspaceCount || inv.workspaceCount || 0;
  const span =
    metrics.fromDate && metrics.toDate ? `${metrics.fromDate} ~ ${metrics.toDate}` : `近 ${metrics.windowDays} 天`;

  if (spaces === 0) {
    risks.push({ tone: "amber", title: "尚无可访问业务空间", detail: "请先创建或加入至少一个业务空间。" });
  }
  if (!inv.hasVerifiedModel) {
    risks.push({
      tone: "amber",
      title: "模型配置未就绪",
      detail: "所有可访问空间中尚未发现已验证的模型配置。",
    });
  }
  if (inv.agentCount === 0 && spaces > 0) {
    risks.push({ tone: "red", title: "尚未创建 Agent", detail: "请在业务空间中创建并启用至少一个 Agent。" });
  }
  if (inv.connectionTotal > 0 && inv.connectionVerified < inv.connectionTotal) {
    risks.push({
      tone: "amber",
      title: "连接需要处理",
      detail: `${inv.connectionTotal - inv.connectionVerified} 个服务连接尚未通过验证（跨全部可访问空间）。`,
    });
  }
  if (kpis.runsTotal >= 5 && kpis.runSuccessRate < 90) {
    risks.push({
      tone: "red",
      title: "链路成功率偏低",
      detail: `${span} 全空间 Agent Run 成功率为 ${kpis.runSuccessRate.toFixed(1)}%。`,
    });
  }
  if (kpis.toolCallsTotal >= 5 && kpis.toolCallSuccessRate < 90) {
    risks.push({
      tone: "red",
      title: "工具调用成功率偏低",
      detail: `${span} 全空间工具调用成功率为 ${kpis.toolCallSuccessRate.toFixed(1)}%。`,
    });
  }
  return risks.length
    ? risks
    : [{ tone: "cyan", title: "运行状态正常", detail: "当前筛选区间内跨全部可访问空间未发现明显风险。" }];
}
