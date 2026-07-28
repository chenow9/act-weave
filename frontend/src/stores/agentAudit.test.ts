import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

import { apiClient } from "../services/api";
import { useAgentAuditStore } from "./agentAudit";

vi.mock("../services/api", () => ({
  apiClient: {
    get: vi.fn(),
  },
}));

describe("agentAudit store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.mocked(apiClient.get).mockReset();
  });

  it("loads traces from agent-audit list API and records debugMode", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        items: [
          {
            traceId: "trace-1",
            startedAt: "2026-07-22T00:00:00Z",
            status: "success",
            model: "gpt-4o-mini",
            userLabel: "USER:u1",
            latencyMs: 1200,
            stepCount: 3,
            runIds: ["r1"],
          },
        ],
        stats: { totalRuns: 1, successRate: 100, failureRate: 0, avgLatencyMs: 1200 },
        debugMode: true,
        total: 49,
      },
    });
    const store = useAgentAuditStore();
    await store.loadTraces("ws-1", { q: "trace", page: 2, pageSize: 10 });
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/ws-1/agent-audit/traces?q=trace&limit=10&page=2");
    expect(store.items).toHaveLength(1);
    expect(store.debugMode).toBe(true);
    expect(store.stats.totalRuns).toBe(1);
    expect(store.total).toBe(49);
    expect(store.page).toBe(2);
    expect(store.pageSize).toBe(10);
    expect(store.pageCount).toBe(5);
  });

  it("loads detail timeline and switches to detail view", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        traceId: "trace-1",
        startedAt: "2026-07-22T00:00:00Z",
        status: "success",
        model: "gpt-4o-mini",
        userLabel: "USER:u1",
        debugMode: false,
        steps: [
          { type: "reasoning", title: "大模型推理", timeOffsetMs: 0, content: "无推理数据", contentState: "missing" },
        ],
        runIds: ["r1"],
        stepTotal: 1,
        stepOffset: 0,
        stepLimit: 30,
        hasMore: false,
      },
    });
    const store = useAgentAuditStore();
    await store.loadTraceDetail("ws-1", "trace-1");
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/ws-1/agent-audit/traces/trace-1?limit=30&offset=0");
    expect(store.view).toBe("detail");
    expect(store.selected?.steps[0]?.content).toBe("无推理数据");
    expect(store.detailHasMore).toBe(false);
    expect(store.debugMode).toBe(false);
    store.toggleMask();
    expect(store.isMasked).toBe(true);
  });

  it("appends timeline steps on loadMoreTraceSteps", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({
        data: {
          traceId: "trace-2",
          startedAt: "2026-07-22T00:00:00Z",
          status: "success",
          model: "gpt-5.4",
          userLabel: "USER:u1",
          debugMode: true,
          steps: [
            { type: "input", title: "用户输入", timeOffsetMs: 0, content: "hi" },
            { type: "reasoning", title: "大模型推理", timeOffsetMs: 10, content: "think" },
          ],
          runIds: ["r1"],
          stepTotal: 4,
          stepOffset: 0,
          stepLimit: 2,
          hasMore: true,
        },
      })
      .mockResolvedValueOnce({
        data: {
          traceId: "trace-2",
          startedAt: "2026-07-22T00:00:00Z",
          status: "success",
          model: "gpt-5.4",
          userLabel: "USER:u1",
          debugMode: true,
          steps: [
            { type: "tool", title: "工具调用: a", timeOffsetMs: 20 },
            { type: "output", title: "最终输出", timeOffsetMs: 30, content: "ok" },
          ],
          runIds: ["r1"],
          stepTotal: 4,
          stepOffset: 2,
          stepLimit: 2,
          hasMore: false,
        },
      });
    const store = useAgentAuditStore();
    store.detailStepLimit = 2;
    await store.loadTraceDetail("ws-1", "trace-2");
    expect(store.selected?.steps).toHaveLength(2);
    expect(store.detailHasMore).toBe(true);
    expect(store.detailStepOffset).toBe(2);
    await store.loadMoreTraceSteps("ws-1");
    expect(apiClient.get).toHaveBeenLastCalledWith("/workspaces/ws-1/agent-audit/traces/trace-2?limit=2&offset=2");
    expect(store.selected?.steps).toHaveLength(4);
    expect(store.selected?.steps[2]?.type).toBe("tool");
    expect(store.detailHasMore).toBe(false);
  });
});
