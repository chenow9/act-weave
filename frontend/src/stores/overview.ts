import { defineStore } from "pinia";

import { tt } from "../i18n/tt";
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
        if (response.data.fromDate) this.rangeFrom = response.data.fromDate;
        if (response.data.toDate) this.rangeTo = response.data.toDate;
        this.risks = collectRisks(response.data);
      } catch (error) {
        this.error = error instanceof Error ? error.message : tt("overview.loadFailed");
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
    metrics.fromDate && metrics.toDate
      ? `${metrics.fromDate} ~ ${metrics.toDate}`
      : tt("overview.risk.recentDays", { n: metrics.windowDays });

  if (spaces === 0) {
    risks.push({
      tone: "amber",
      title: tt("overview.risk.noWorkspaceTitle"),
      detail: tt("overview.risk.noWorkspaceDetail"),
      action: { routeName: "workspaces", label: tt("overview.risk.openWorkspaces") },
    });
  }
  if (!inv.hasVerifiedModel) {
    risks.push({
      tone: "amber",
      title: tt("overview.risk.modelTitle"),
      detail: tt("overview.risk.modelDetail"),
      action: { routeName: "model-apis", label: tt("overview.risk.openModels") },
    });
  }
  if (inv.agentCount === 0 && spaces > 0) {
    risks.push({
      tone: "red",
      title: tt("overview.risk.noAgentTitle"),
      detail: tt("overview.risk.noAgentDetail"),
      action: { routeName: "agents", label: tt("overview.risk.openAgents") },
    });
  }
  if (inv.connectionTotal > 0 && inv.connectionVerified < inv.connectionTotal) {
    risks.push({
      tone: "amber",
      title: tt("overview.risk.connectionTitle"),
      detail: tt("overview.risk.connectionDetail", {
        n: inv.connectionTotal - inv.connectionVerified,
      }),
      action: { routeName: "connections", label: tt("overview.risk.openConnections"), query: { status: "UNVERIFIED" } },
    });
  }
  if (kpis.runsTotal >= 5 && kpis.runSuccessRate < 90) {
    risks.push({
      tone: "red",
      title: tt("overview.risk.runRateTitle"),
      detail: tt("overview.risk.runRateDetail", {
        span,
        rate: kpis.runSuccessRate.toFixed(1),
      }),
      action: {
        routeName: "logs",
        label: tt("overview.risk.openFailedTraces"),
        query: { status: "error", from: metrics.fromDate || "", to: metrics.toDate || "" },
      },
    });
  }
  if (kpis.toolCallsTotal >= 5 && kpis.toolCallSuccessRate < 90) {
    risks.push({
      tone: "red",
      title: tt("overview.risk.toolRateTitle"),
      detail: tt("overview.risk.toolRateDetail", {
        span,
        rate: kpis.toolCallSuccessRate.toFixed(1),
      }),
      action: {
        routeName: "logs",
        label: tt("overview.risk.openFailedTraces"),
        query: { status: "error", from: metrics.fromDate || "", to: metrics.toDate || "" },
      },
    });
  }
  return risks.length
    ? risks
    : [
        {
          tone: "cyan",
          title: tt("overview.risk.okTitle"),
          detail: tt("overview.risk.okDetail"),
        },
      ];
}
