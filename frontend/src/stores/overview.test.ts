import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { OverviewMetrics } from "../types/domain";
import { defaultOverviewRange, useOverviewStore } from "./overview";

vi.mock("../services/api", () => ({
  apiClient: {
    get: vi.fn(),
  },
}));

const metricsFixture: OverviewMetrics = {
  windowDays: 5,
  from: "2026-07-20T00:00:00Z",
  to: "2026-07-25T00:00:00Z",
  fromDate: "2026-07-20",
  toDate: "2026-07-24",
  workspaceCount: 3,
  kpis: {
    toolCallSuccessRate: 95,
    toolCallsTotal: 100,
    toolCallsSucceeded: 95,
    toolCallsFailed: 5,
    avgToolLatencyMs: 120,
    runSuccessRate: 98,
    runsTotal: 50,
    runsSucceeded: 49,
    runsFailed: 1,
    avgRunLatencyMs: 800,
    workflowSuccessRate: 100,
    workflowTotal: 10,
    workflowSucceeded: 10,
    workflowFailed: 0,
    avgWorkflowLatencyMs: 50,
    sessionCountToday: 3,
    sessionCountPeriod: 40,
    avgSessionsPerDay: 8,
  },
  series: [
    {
      date: "2026-07-20",
      sessions: 2,
      runsTotal: 4,
      runsSucceeded: 4,
      runsFailed: 0,
      toolCallsTotal: 8,
      toolCallsSucceeded: 8,
      toolCallsFailed: 0,
      workflowTotal: 1,
      workflowSucceeded: 1,
      workflowFailed: 0,
    },
  ],
  inventory: {
    workspaceCount: 3,
    agentCount: 5,
    toolCount: 20,
    workflowCount: 8,
    connectionTotal: 4,
    connectionVerified: 3,
    modelConfigTotal: 3,
    modelConfigVerified: 2,
    hasVerifiedModel: true,
  },
  topTools: [],
  failingTools: [],
  topWorkspaces: [],
};

describe("overview store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.mocked(apiClient.get).mockResolvedValue({ data: metricsFixture });
  });

  it("loads metrics with from/to date filter", async () => {
    const overview = useOverviewStore();
    await overview.setRange("2026-07-20", "2026-07-24");

    expect(apiClient.get).toHaveBeenCalledWith("/overview/metrics", {
      params: { from: "2026-07-20", to: "2026-07-24" },
    });
    expect(overview.rangeFrom).toBe("2026-07-20");
    expect(overview.rangeTo).toBe("2026-07-24");
    expect(overview.rangeLabel).toBe("2026-07-20 ~ 2026-07-24");
    expect(overview.kpis?.toolCallSuccessRate).toBe(95);
  });

  it("provides a default inclusive range ending today UTC", () => {
    const range = defaultOverviewRange(new Date("2026-07-25T12:00:00Z"));
    expect(range.to).toBe("2026-07-25");
    expect(range.from).toBe("2026-07-12");
  });
});
