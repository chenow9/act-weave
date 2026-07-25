import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { AuditEvent, AuditExport } from "../types/domain";
import { useAuditStore } from "./audit";

vi.mock("../services/api", () => ({
  apiClient: { get: vi.fn(), post: vi.fn() },
}));

const workspaceId = "01911111-1111-7111-8111-111111111111";

function eventFixture(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id: "01922222-2222-7222-8222-222222222222",
    occurredAt: "2026-07-15T07:00:00Z",
    actorType: "USER",
    actorId: "01933333-3333-7333-8333-333333333333",
    actorDisplay: "Audit Owner",
    action: "agent.changed",
    resourceType: "AGENT",
    resourceId: "01944444-4444-7444-8444-444444444444",
    result: "SUCCESS",
    requestId: "request-audit-detail",
    traceId: "trace-audit-detail",
    changes: { name: { from: "old", to: "new" } },
    metadata: { summary: "safe" },
    schemaVersion: "audit.v1",
    ...overrides,
  };
}

function exportFixture(overrides: Partial<AuditExport> = {}): AuditExport {
  return {
    id: "01955555-5555-7555-8555-555555555555",
    filterSnapshot: { traceId: "trace-audit-detail" },
    status: "PENDING",
    requestedBy: eventFixture().actorId || "",
    requestedAt: "2026-07-15T07:02:00Z",
    expiresAt: "2026-07-15T08:02:00Z",
    ...overrides,
  };
}

describe("audit v1 store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
  });

  it("uses exact audit filters and Workspace-scoped event routes", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [eventFixture()] } })
      .mockResolvedValueOnce({ data: eventFixture() });
    const audit = useAuditStore();

    const events = await audit.loadEvents(workspaceId, {
      actorType: "USER",
      actorId: eventFixture().actorId,
      resourceType: "AGENT",
      resourceId: eventFixture().resourceId,
      action: "agent.changed",
      results: ["SUCCESS", "DENIED"],
      requestId: "request-audit-detail",
      traceId: "trace-audit-detail",
      occurredFrom: "2026-07-15T00:00:00Z",
      occurredUntil: "2026-07-16T00:00:00Z",
      limit: 50,
    });
    await audit.loadEventDetail(workspaceId, eventFixture().id);

    const params = new URLSearchParams();
    params.set("actorType", "USER");
    params.set("actorId", eventFixture().actorId || "");
    params.set("resourceType", "AGENT");
    params.set("resourceId", eventFixture().resourceId || "");
    params.set("action", "agent.changed");
    params.append("result", "SUCCESS");
    params.append("result", "DENIED");
    params.set("requestId", "request-audit-detail");
    params.set("traceId", "trace-audit-detail");
    params.set("occurredFrom", "2026-07-15T00:00:00Z");
    params.set("occurredUntil", "2026-07-16T00:00:00Z");
    params.set("limit", "50");
    expect(apiClient.get).toHaveBeenNthCalledWith(1, `/workspaces/${workspaceId}/audit-events?${params.toString()}`);
    expect(apiClient.get).toHaveBeenNthCalledWith(2, `/workspaces/${workspaceId}/audit-events/${eventFixture().id}`);
    expect(events[0].action).toBe("agent.changed");
  });

  it("models role-cropped details without making a separate raw payload request", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: eventFixture() });
    const audit = useAuditStore();

    await audit.loadEventDetail(workspaceId, eventFixture().id);

    expect(audit.selectedHasSensitiveDetail).toBe(false);
    expect(apiClient.get).toHaveBeenCalledTimes(1);
    expect(apiClient.get).not.toHaveBeenCalledWith(expect.stringContaining("payload"));
  });

  it("creates an allowlisted export, refreshes status, and exposes only the short-lived download URL", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: exportFixture() });
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: exportFixture({
        status: "SUCCEEDED",
        completedAt: "2026-07-15T07:03:00Z",
        downloadUrl: "https://downloads.example.test/audit.zip?expires=300",
      }),
    });
    const audit = useAuditStore();
    const filters = {
      actorType: "USER",
      resourceType: "AGENT",
      action: "agent.changed",
      results: ["SUCCESS" as const],
      requestId: "request-audit-detail",
      traceId: "trace-audit-detail",
      occurredFrom: "2026-07-15T00:00:00Z",
      occurredUntil: "2026-07-16T00:00:00Z",
      beforeId: "must-not-export",
      limit: 500,
    };

    const created = await audit.createExport(workspaceId, filters, 3600);
    const completed = await audit.loadExport(workspaceId, created.id);

    expect(apiClient.post).toHaveBeenCalledWith(`/workspaces/${workspaceId}/audit-exports`, {
      actorType: "USER",
      resourceType: "AGENT",
      action: "agent.changed",
      results: ["SUCCESS"],
      requestId: "request-audit-detail",
      traceId: "trace-audit-detail",
      occurredFrom: "2026-07-15T00:00:00Z",
      occurredUntil: "2026-07-16T00:00:00Z",
      expiresInSeconds: 3600,
    });
    expect(apiClient.get).toHaveBeenCalledWith(`/workspaces/${workspaceId}/audit-exports/${created.id}`);
    expect(completed.status).toBe("SUCCEEDED");
    expect(completed.downloadUrl).toContain("expires=300");
  });
});
