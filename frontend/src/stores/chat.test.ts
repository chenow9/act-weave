import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient, getAuthToken, refreshAuthSession } from "../services/api";
import type {
  AgentRun,
  ChatConfirmation,
  ChatMessage,
  ChatSession,
  WorkflowExecution,
  WorkspaceChatSession,
} from "../types/domain";
import { __resetChatStreamProjectorsForTests, useChatStore } from "./chat";

vi.mock("../services/api", () => ({
  apiClient: {
    defaults: { baseURL: "/api/v1" },
    get: vi.fn(),
    post: vi.fn(),
  },
  refreshAuthSession: vi.fn(),
  getAuthToken: vi.fn(() => ""),
}));

const workspaceId = "01911111-1111-7111-8111-111111111111";
const agentId = "01933333-3333-7333-8333-333333333333";
const conversationId = "01922222-2222-7222-8222-222222222222";

function sessionFixture(overrides: Partial<ChatSession> = {}): ChatSession {
  return {
    id: "01922222-2222-7222-8222-222222222222",
    agentId,
    title: "自动化控制台对话",
    status: "ACTIVE",
    createdAt: "2026-07-15T07:00:00Z",
    updatedAt: "2026-07-15T07:01:00Z",
    lockVersion: 1,
    ...overrides,
  };
}

function scopedSession(overrides: Partial<WorkspaceChatSession> = {}): WorkspaceChatSession {
  return { ...sessionFixture(), workspaceId, ...overrides };
}

function messageFixture(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: "01944444-4444-7444-8444-444444444444",
    role: "USER",
    content: "执行最近发布的自动化流程",
    contentSha256: "a".repeat(64),
    contentLength: 42,
    status: "PROCESSING",
    runId: "01955555-5555-7555-8555-555555555555",
    createdAt: "2026-07-15T07:01:00Z",
    ...overrides,
  };
}

function runFixture(overrides: Partial<AgentRun> = {}): AgentRun {
  return {
    id: "01955555-5555-7555-8555-555555555555",
    sessionId: sessionFixture().id,
    agentId,
    status: "RUNNING",
    triggerType: "CHAT_MESSAGE",
    triggeredByType: "USER",
    triggeredById: "01966666-6666-7666-8666-666666666666",
    traceId: "01977777-7777-7777-8777-777777777777",
    modelSnapshot: { modelConfigId: "01988888-8888-7888-8888-888888888888" },
    capabilitySnapshot: { releases: [] },
    inputSummary: { messageId: messageFixture().id },
    outputSummary: {},
    startedAt: "2026-07-15T07:01:00Z",
    lockVersion: 1,
    ...overrides,
  };
}

function confirmationFixture(overrides: Partial<ChatConfirmation> = {}): ChatConfirmation {
  return {
    id: "01999999-9999-7999-8999-999999999999",
    sessionId: sessionFixture().id,
    runId: runFixture().id,
    targetType: "WORKFLOW",
    targetReleaseId: "019aaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa",
    riskLevel: "HIGH",
    riskReasons: ["生产环境不可逆操作"],
    inputSummary: { rolloutId: "R-240630" },
    status: "PENDING",
    requestedBy: runFixture().triggeredById,
    createdAt: "2026-07-15T07:02:00Z",
    expiresAt: "2026-07-15T07:12:00Z",
    lockVersion: 1,
    cached: false,
    ...overrides,
  };
}

function executionFixture(): WorkflowExecution {
  return {
    id: "019bbbbb-bbbb-7bbb-8bbb-bbbbbbbbbbbb",
    workflowId: "019ccccc-cccc-7ccc-8ccc-cccccccccccc",
    revisionId: "019ddddd-dddd-7ddd-8ddd-dddddddddddd",
    agentRunId: runFixture().id,
    triggerType: "CHAT",
    triggeredByType: "USER",
    triggeredById: runFixture().triggeredById,
    traceId: runFixture().traceId,
    status: "SUCCEEDED",
    inputSummary: {},
    outputSummary: { result: "ok" },
    startedAt: "2026-07-15T07:01:00Z",
    finishedAt: "2026-07-15T07:03:00Z",
    lockVersion: 2,
  };
}

const itemId = "81000000-0000-4000-8000-000000000001";

function protocolEnvelope(type: string, sequence: number, data: Record<string, unknown>) {
  return {
    specVersion: "1.0",
    type,
    eventId: `a1000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`,
    streamId: `run:${runFixture().id}`,
    sequence,
    occurredAt: "2026-07-20T01:00:00Z",
    workspaceId,
    agentId,
    conversationId,
    runId: runFixture().id,
    traceId: "test-trace",
    data,
  };
}

function protocolSSE(type: string, sequence: number, data: Record<string, unknown>) {
  return `id: ${sequence}\nevent: ${type}\ndata: ${JSON.stringify(protocolEnvelope(type, sequence, data))}\n\n`;
}

function sseResponse(body: string, status = 200) {
  return new Response(body, { status, headers: { "Content-Type": "text/event-stream" } });
}

describe("chat v1 store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
    sessionStorage.clear();
    __resetChatStreamProjectorsForTests();
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => undefined)),
    );
    vi.mocked(getAuthToken).mockReturnValue("");
  });

  it("aggregates current-user sessions by workspace and creates with the exact nested route", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [sessionFixture()] } });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: sessionFixture({ title: "新会话" }) });
    const chat = useChatStore();

    await chat.loadSessions([workspaceId]);
    const created = await chat.createSession(workspaceId, sessionFixture().agentId, "新会话");

    expect(apiClient.get).toHaveBeenCalledWith(`/workspaces/${workspaceId}/chat/sessions`);
    expect(apiClient.post).toHaveBeenCalledWith(`/workspaces/${workspaceId}/chat/sessions`, {
      agentId: sessionFixture().agentId,
      title: "新会话",
    });
    expect(created.workspaceId).toBe(workspaceId);
    expect(chat.activeSessionId).toBe(created.id);
  });

  it("submits a permanent message, replaces the optimistic row, and starts v1 SSE", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        session: sessionFixture({ latestRunId: runFixture().id, lockVersion: 2 }),
        message: messageFixture(),
        runId: runFixture().id,
      },
    });
    const chat = useChatStore();
    chat.sessions = [scopedSession()];
    chat.activeSessionId = sessionFixture().id;

    const pending = chat.sendMessage("执行最近发布的自动化流程");
    expect(chat.messages[0]).toMatchObject({ role: "USER", status: "PROCESSING" });
    await pending;

    expect(apiClient.post).toHaveBeenCalledWith(
      `/workspaces/${workspaceId}/chat/sessions/${sessionFixture().id}/messages`,
      { content: "执行最近发布的自动化流程" },
    );
    expect(chat.messages).toEqual([messageFixture()]);
    expect(chat.runStatus).toBe("PENDING");
    expect(fetch).toHaveBeenCalledWith(
      `/api/v1/workspaces/${workspaceId}/agent-runs/${runFixture().id}/events`,
      expect.objectContaining({ credentials: "include", headers: { Accept: "text/event-stream" } }),
    );
    chat.closeRunStream();
  });

  it("uses the shared session refresher when Run SSE returns 401", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(sseResponse("", 401))
      .mockResolvedValueOnce(
        sseResponse(
          protocolSSE("run.completed", 1, {
            run: {
              id: runFixture().id,
              conversationId,
              agentId,
              status: "completed",
              trigger: "message",
              startedAt: "2026-07-20T01:00:00Z",
            },
          }),
        ),
      );
    vi.mocked(refreshAuthSession).mockResolvedValue({} as never);
    vi.mocked(apiClient.get).mockResolvedValue({ data: { run: runFixture({ status: "SUCCEEDED" }), steps: [] } });
    const chat = useChatStore();
    chat.sessions = [scopedSession({ latestRunId: runFixture().id })];
    chat.activeSessionId = sessionFixture().id;

    chat.subscribeRunStream(runFixture().id);

    await vi.waitFor(() => expect(refreshAuthSession).toHaveBeenCalledTimes(1), { timeout: 5000 });
    await vi.waitFor(() => expect(chat.runStatus).toBe("SUCCEEDED"), { timeout: 5000 });
    chat.closeRunStream();
  });

  it("loads Run and Step Timeline in one request and restores a server pending marker", async () => {
    const pendingSession = sessionFixture({
      latestRunId: runFixture().id,
      pendingConfirmationId: confirmationFixture().id,
    });
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({
        data: { session: pendingSession, messages: [messageFixture({ confirmationId: confirmationFixture().id })] },
      })
      .mockResolvedValueOnce({
        data: {
          run: runFixture({ status: "WAITING_CONFIRMATION" }),
          steps: [
            {
              id: "step-1",
              sequenceNo: 1,
              stepType: "MODEL",
              status: "WAITING_CONFIRMATION",
              inputSummary: {},
              outputSummary: {},
              startedAt: "2026-07-15T07:01:00Z",
            },
          ],
        },
      });
    const chat = useChatStore();
    chat.sessions = [scopedSession()];

    await chat.loadSession(sessionFixture().id);

    expect(apiClient.get).toHaveBeenNthCalledWith(1, `/workspaces/${workspaceId}/chat/sessions/${sessionFixture().id}`);
    expect(apiClient.get).toHaveBeenNthCalledWith(2, `/workspaces/${workspaceId}/agent-runs/${runFixture().id}`);
    expect(chat.latestRunSteps[0].sequenceNo).toBe(1);
    expect(chat.pendingConfirmation?.id).toBe(confirmationFixture().id);
    expect(chat.pendingConfirmation?.status).toBe("PENDING");
    chat.closeRunStream();
  });

  it("consumes protocol SSE (item.delta + run.completed) and streams assistant text without refresh", async () => {
    const streamBody =
      protocolSSE("run.started", 2, {
        run: {
          id: runFixture().id,
          conversationId,
          agentId,
          status: "running",
          trigger: "message",
          startedAt: "2026-07-20T01:00:00Z",
        },
      }) +
      protocolSSE("item.started", 3, {
        item: {
          id: itemId,
          type: "message",
          status: "in_progress",
          role: "assistant",
          content: [{ type: "text", text: "" }],
        },
      }) +
      protocolSSE("item.delta", 4, {
        itemId,
        delta: { type: "text_delta", index: 0, text: "已查询到" },
      }) +
      protocolSSE("item.delta", 5, {
        itemId,
        delta: { type: "text_delta", index: 0, text: "当前启用的全部部门" },
      }) +
      protocolSSE("item.completed", 6, {
        item: {
          id: itemId,
          type: "message",
          status: "completed",
          role: "assistant",
          content: [{ type: "text", text: "已查询到当前启用的全部部门" }],
        },
      }) +
      protocolSSE("run.completed", 7, {
        run: {
          id: runFixture().id,
          conversationId,
          agentId,
          status: "completed",
          trigger: "message",
          startedAt: "2026-07-20T01:00:00Z",
          completedAt: "2026-07-20T01:00:08Z",
        },
      });

    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(sseResponse(streamBody));
    vi.mocked(apiClient.get).mockResolvedValue({ data: { run: runFixture({ status: "SUCCEEDED" }), steps: [] } });
    vi.mocked(getAuthToken).mockReturnValue("jwt-token");
    const chat = useChatStore();
    chat.sessions = [scopedSession({ latestRunId: runFixture().id })];
    chat.activeSessionId = sessionFixture().id;
    chat.runEventCursorByRun[runFixture().id] = 1;

    chat.subscribeRunStream(runFixture().id);
    await vi.waitFor(() => expect(chat.runStatus).toBe("SUCCEEDED"));

    expect(fetchMock).toHaveBeenCalledWith(
      `/api/v1/workspaces/${workspaceId}/agent-runs/${runFixture().id}/events`,
      expect.objectContaining({
        headers: { Accept: "text/event-stream", Authorization: "Bearer jwt-token", "Last-Event-ID": "1" },
      }),
    );
    expect(chat.runEventCursorByRun[runFixture().id]).toBe(7);
    const assistant = chat.messages.find((message) => message.role === "ASSISTANT");
    expect(assistant?.content).toBe("已查询到当前启用的全部部门");
    expect(assistant?.id).toBe(itemId);
  });

  it("ignores unknown protocol event types without failing the stream", async () => {
    const streamBody =
      protocolSSE("future.annotation", 2, { annotation: { kind: "additive" } }) +
      protocolSSE("item.delta", 3, {
        itemId,
        delta: { type: "text_delta", index: 0, text: "仍可见" },
      }) +
      protocolSSE("run.completed", 4, {
        run: {
          id: runFixture().id,
          conversationId,
          agentId,
          status: "completed",
          trigger: "message",
          startedAt: "2026-07-20T01:00:00Z",
        },
      });

    vi.mocked(fetch).mockResolvedValueOnce(sseResponse(streamBody));
    vi.mocked(apiClient.get).mockResolvedValue({ data: { run: runFixture({ status: "SUCCEEDED" }), steps: [] } });
    const chat = useChatStore();
    chat.sessions = [scopedSession({ latestRunId: runFixture().id })];
    chat.activeSessionId = sessionFixture().id;

    chat.subscribeRunStream(runFixture().id);
    await vi.waitFor(() => expect(chat.runStatus).toBe("SUCCEEDED"));
    expect(chat.messages.some((message) => message.content === "仍可见")).toBe(true);
    chat.closeRunStream();
  });

  it("retries 404 not-ready with short backoff then consumes protocol events", async () => {
    const streamBody =
      protocolSSE("item.delta", 1, {
        itemId,
        delta: { type: "text_delta", index: 0, text: "ready" },
      }) +
      protocolSSE("run.completed", 2, {
        run: {
          id: runFixture().id,
          conversationId,
          agentId,
          status: "completed",
          trigger: "message",
          startedAt: "2026-07-20T01:00:00Z",
        },
      });

    const fetchMock = vi.mocked(fetch);
    fetchMock
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "NOT_FOUND" } }), { status: 404 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "NOT_FOUND" } }), { status: 404 }))
      .mockResolvedValueOnce(sseResponse(streamBody));
    vi.mocked(apiClient.get).mockResolvedValue({ data: { run: runFixture({ status: "SUCCEEDED" }), steps: [] } });

    const chat = useChatStore();
    chat.sessions = [scopedSession({ latestRunId: runFixture().id })];
    chat.activeSessionId = sessionFixture().id;
    chat.subscribeRunStream(runFixture().id);

    // 404 backoff: 200ms + 500ms then success (bounded; no user-facing failure path).
    await vi.waitFor(() => expect(chat.runStatus).toBe("SUCCEEDED"), { timeout: 5000 });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(chat.messages.some((message) => message.content === "ready")).toBe(true);
    chat.closeRunStream();
  });

  it("keeps stream open across non-terminal frames and reconnects until run.completed", async () => {
    const first =
      protocolSSE("item.started", 2, {
        item: {
          id: itemId,
          type: "message",
          status: "in_progress",
          role: "assistant",
          content: [{ type: "text", text: "" }],
        },
      }) +
      protocolSSE("item.delta", 3, {
        itemId,
        delta: { type: "text_delta", index: 0, text: "部分" },
      });
    const second =
      protocolSSE("item.completed", 4, {
        item: {
          id: itemId,
          type: "message",
          status: "completed",
          role: "assistant",
          content: [{ type: "text", text: "部分完整" }],
        },
      }) +
      protocolSSE("run.completed", 5, {
        run: {
          id: runFixture().id,
          conversationId,
          agentId,
          status: "completed",
          trigger: "message",
          startedAt: "2026-07-20T01:00:00Z",
        },
      });

    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(sseResponse(first)).mockResolvedValueOnce(sseResponse(second));
    // Protocol mid-stream does not call loadRun; only terminal calibrate does.
    // Always return SUCCEEDED so terminal loadRun cannot demote stream status.
    vi.mocked(apiClient.get).mockResolvedValue({ data: { run: runFixture({ status: "SUCCEEDED" }), steps: [] } });

    const chat = useChatStore();
    chat.sessions = [scopedSession({ latestRunId: runFixture().id })];
    chat.activeSessionId = sessionFixture().id;
    chat.runStatus = "RUNNING";
    chat.runEventCursorByRun[runFixture().id] = 1;

    chat.subscribeRunStream(runFixture().id);
    await vi.waitFor(() => expect(chat.runEventCursorByRun[runFixture().id]).toBe(3));
    expect(chat.runStatus).toBe("RUNNING");
    expect(chat.messages.some((message) => message.content.includes("部分"))).toBe(true);

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2), { timeout: 5000 });
    await vi.waitFor(() => expect(chat.runStatus).toBe("SUCCEEDED"), { timeout: 5000 });
    expect(chat.messages.some((message) => message.content === "部分完整")).toBe(true);
    chat.closeRunStream();
  });

  it("still accepts thin secondary legacy RUN_WAITING_CONFIRMATION (one-release compat, not sole whitelist)", async () => {
    const payload = JSON.stringify({ confirmation: confirmationFixture(), resumeToken: "r".repeat(40) });
    vi.mocked(fetch).mockResolvedValueOnce(sseResponse(`id: 3\nevent: RUN_WAITING_CONFIRMATION\ndata: ${payload}\n\n`));
    vi.mocked(apiClient.get).mockResolvedValue({
      data: { run: runFixture({ status: "WAITING_CONFIRMATION" }), steps: [] },
    });
    const chat = useChatStore();
    chat.sessions = [scopedSession({ latestRunId: runFixture().id })];
    chat.activeSessionId = sessionFixture().id;
    chat.subscribeRunStream(runFixture().id);
    await vi.waitFor(() => expect(chat.pendingResumeToken).toBe("r".repeat(40)));

    setActivePinia(createPinia());
    __resetChatStreamProjectorsForTests();
    const refreshed = useChatStore();
    refreshed.sessions = [
      scopedSession({ latestRunId: runFixture().id, pendingConfirmationId: confirmationFixture().id }),
    ];
    refreshed.restorePendingConfirmation(refreshed.sessions[0]);

    expect(refreshed.pendingConfirmation).toEqual(confirmationFixture());
    expect(refreshed.pendingResumeToken).toBe("r".repeat(40));
    chat.closeRunStream();
  });

  it("confirms and cancels only through requester-scoped v1 commands", async () => {
    const chat = useChatStore();
    chat.sessions = [scopedSession()];
    chat.activeSessionId = sessionFixture().id;
    chat.pendingConfirmation = confirmationFixture();
    chat.pendingResumeToken = "r".repeat(40);
    chat.refreshActiveRuntime = vi.fn();
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({
        data: confirmationFixture({ status: "CONFIRMED", confirmedBy: runFixture().triggeredById }),
      })
      .mockResolvedValueOnce({ data: confirmationFixture({ id: "confirm-cancel", status: "CANCELLED" }) });

    await chat.confirmPending();
    expect(apiClient.post).toHaveBeenNthCalledWith(
      1,
      `/workspaces/${workspaceId}/confirmations/${confirmationFixture().id}:confirm`,
      { resumeToken: "r".repeat(40), lockVersion: 1 },
    );

    chat.pendingConfirmation = confirmationFixture({ id: "confirm-cancel" });
    await chat.cancelPending();
    expect(apiClient.post).toHaveBeenNthCalledWith(
      2,
      `/workspaces/${workspaceId}/confirmations/confirm-cancel:cancel`,
      { lockVersion: 1 },
    );
  });

  it("archives with lockVersion while retaining loaded messages", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: sessionFixture({ status: "ARCHIVED", lockVersion: 2 }) });
    const chat = useChatStore();
    chat.sessions = [scopedSession()];
    chat.activeSessionId = sessionFixture().id;
    chat.messages = [messageFixture()];

    await chat.archiveSession();

    expect(apiClient.post).toHaveBeenCalledWith(
      `/workspaces/${workspaceId}/chat/sessions/${sessionFixture().id}:archive`,
      { lockVersion: 1 },
    );
    expect(chat.activeSession?.status).toBe("ARCHIVED");
    expect(chat.messages).toEqual([messageFixture()]);
  });

  it("lists and loads workflow executions with exact v1 filters and step detail", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [executionFixture()] } })
      .mockResolvedValueOnce({ data: { execution: executionFixture(), steps: [] } });
    const chat = useChatStore();

    await chat.loadExecutions(workspaceId, {
      status: "SUCCEEDED",
      traceId: executionFixture().traceId,
      workflowId: executionFixture().workflowId,
      startedAfter: "2026-07-15T00:00:00Z",
      limit: 20,
    });
    await chat.loadExecution(workspaceId, executionFixture().id);

    const query = new URLSearchParams({
      status: "SUCCEEDED",
      traceId: executionFixture().traceId,
      workflowId: executionFixture().workflowId,
      startedAfter: "2026-07-15T00:00:00Z",
      limit: "20",
    });
    expect(apiClient.get).toHaveBeenNthCalledWith(1, `/workspaces/${workspaceId}/executions?${query.toString()}`);
    expect(apiClient.get).toHaveBeenNthCalledWith(2, `/workspaces/${workspaceId}/executions/${executionFixture().id}`);
    expect(chat.latestExecution?.status).toBe("SUCCEEDED");
  });
});
