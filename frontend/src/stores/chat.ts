import { defineStore } from "pinia";

import { tt } from "../i18n/tt";
import { apiClient, getAuthToken, refreshAuthSession } from "../services/api";
import {
  applyStreamFrame as projectStreamFrame,
  createProjectionState,
  isTerminalRunStatus,
  networkDelayMs,
  NOT_READY_MAX_ATTEMPTS,
  notReadyDelayMs,
  NETWORK_MAX_ATTEMPTS,
  parseSSEBlock,
  sleep,
  toAssistantChatMessage,
  type ConsoleRunProjectionState,
  type ConsoleStreamEffects,
  type StreamFrame,
} from "../services/run-event-stream";
import type {
  AgentRun,
  AgentRunStatus,
  AgentRunStep,
  ChatConfirmation,
  ChatMessage,
  ChatRunSubmissionResult,
  ChatSession,
  WorkflowExecution,
  WorkflowExecutionStep,
  WorkspaceChatSession,
} from "../types/domain";

/** ZKL-56: per-run stream health for calibration / DEGRADED UX. */
export type RunStreamHealth = "CONNECTING" | "HEALTHY" | "RECONNECTING" | "CALIBRATING" | "DEGRADED";

interface ChatState {
  sessions: WorkspaceChatSession[];
  activeSessionId: string;
  messages: ChatMessage[];
  pendingConfirmation?: ChatConfirmation;
  pendingResumeToken?: string;
  executions: WorkflowExecution[];
  latestExecution?: WorkflowExecution;
  latestExecutionSteps: WorkflowExecutionStep[];
  latestRunId?: string;
  latestRun?: AgentRun;
  latestRunSteps: AgentRunStep[];
  runStatus?: AgentRunStatus;
  /** Per-run stream health (ZKL-56 UX-03). */
  streamHealthByRun: Record<string, RunStreamHealth>;
  runEventCursorByRun: Record<string, number>;
  loading: boolean;
  runStreamAbort?: AbortController;
  runStreamReconnect?: ReturnType<typeof setTimeout>;
  streamRunId?: string;
}

const calibrationInflight = new Map<string, Promise<void>>();

const activeSessionStorageKey = "actweave.chat.active-session.v1";
const confirmationStoragePrefix = "actweave.chat.confirmation.v1";

/** Per-run pure projectors (kept off reactive state to avoid deep watch churn). */
const projectors = new Map<string, ConsoleRunProjectionState>();

export const useChatStore = defineStore("chat", {
  state: (): ChatState => ({
    sessions: [],
    activeSessionId: "",
    messages: [],
    pendingConfirmation: undefined,
    pendingResumeToken: undefined,
    executions: [],
    latestExecution: undefined,
    latestExecutionSteps: [],
    latestRunId: undefined,
    latestRun: undefined,
    latestRunSteps: [],
    runStatus: undefined,
    streamHealthByRun: {},
    runEventCursorByRun: {},
    loading: false,
    runStreamAbort: undefined,
    runStreamReconnect: undefined,
    streamRunId: undefined,
  }),
  getters: {
    activeSession(state): WorkspaceChatSession | undefined {
      return state.sessions.find((session) => session.id === state.activeSessionId);
    },
    activeStreamHealth(state): RunStreamHealth | undefined {
      const runId = state.streamRunId || state.latestRunId;
      return runId ? state.streamHealthByRun[runId] : undefined;
    },
  },
  actions: {
    async loadSessions(workspaceIds: string[]) {
      const uniqueWorkspaceIds = [...new Set(workspaceIds.filter(Boolean))];
      const responses = await Promise.all(
        uniqueWorkspaceIds.map(async (workspaceId) => {
          const response = await apiClient.get<{ items: ChatSession[] }>(`/workspaces/${workspaceId}/chat/sessions`);
          return response.data.items.map((session): WorkspaceChatSession => ({ ...session, workspaceId }));
        }),
      );
      this.sessions = responses.flat().sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
      const saved = readActiveSession();
      const currentIsAvailable = this.sessions.some((session) => session.id === this.activeSessionId);
      const savedIsAvailable = this.sessions.some(
        (session) => session.id === saved?.sessionId && session.workspaceId === saved.workspaceId,
      );
      if (!currentIsAvailable) {
        this.activeSessionId = savedIsAvailable ? saved?.sessionId || "" : this.sessions[0]?.id || "";
      }
      return this.sessions;
    },
    async createSession(workspaceId: string, agentId: string, title = tt("chat.defaultSessionTitle")) {
      const response = await apiClient.post<ChatSession>(`/workspaces/${workspaceId}/chat/sessions`, {
        agentId,
        title,
      });
      const session: WorkspaceChatSession = { ...response.data, workspaceId };
      this.upsertSession(session);
      this.activeSessionId = session.id;
      this.messages = [];
      this.resetRuntimeState();
      persistActiveSession(session);
      return session;
    },
    async loadSession(sessionId: string, workspaceId?: string) {
      const scope = workspaceId || this.sessions.find((session) => session.id === sessionId)?.workspaceId;
      if (!scope) throw new Error(tt("chat.errSessionWorkspaceUnknown"));
      const response = await apiClient.get<{ session: ChatSession; messages: ChatMessage[] }>(
        `/workspaces/${scope}/chat/sessions/${sessionId}`,
      );
      const session: WorkspaceChatSession = { ...response.data.session, workspaceId: scope };
      this.closeRunStream();
      this.upsertSession(session);
      this.activeSessionId = session.id;
      this.messages = response.data.messages;
      this.pendingConfirmation = undefined;
      this.pendingResumeToken = undefined;
      persistActiveSession(session);
      this.restorePendingConfirmation(session);
      if (session.latestRunId) {
        await this.loadRun(session.latestRunId, scope);
        if (!isTerminalRunStatus(this.runStatus)) this.subscribeRunStream(session.latestRunId, scope);
      } else {
        this.latestRun = undefined;
        this.latestRunId = undefined;
        this.latestRunSteps = [];
        this.runStatus = undefined;
      }
      return session;
    },
    async archiveSession(sessionId?: string) {
      const targetSessionId = sessionId || this.activeSessionId;
      const session = this.sessions.find((item) => item.id === targetSessionId);
      if (!session) throw new Error(tt("chat.errSessionNotFoundForArchive"));
      const response = await apiClient.post<ChatSession>(
        `/workspaces/${session.workspaceId}/chat/sessions/${session.id}:archive`,
        { lockVersion: session.lockVersion },
      );
      const archived: WorkspaceChatSession = { ...response.data, workspaceId: session.workspaceId };
      this.upsertSession(archived);
      if (archived.id === this.activeSessionId) this.closeRunStream();
      return archived;
    },
    async sendMessage(content: string, options: { outboundCredentialAttachmentId?: string } = {}) {
      const session = this.activeSession;
      if (!session) throw new Error(tt("chat.errSelectWorkspaceAndAgent"));
      if (session.status === "ARCHIVED") throw new Error(tt("chat.errArchivedCannotSend"));
      const localMessage = localUserMessage(content);
      this.messages = [...this.messages, localMessage];
      try {
        const body: { content: string; outboundCredentialAttachmentId?: string } = { content };
        // One-shot attach id only — never a Token value. Caller clears local state after send/fail.
        if (options.outboundCredentialAttachmentId?.trim()) {
          body.outboundCredentialAttachmentId = options.outboundCredentialAttachmentId.trim();
        }
        const response = await apiClient.post<ChatRunSubmissionResult>(
          `/workspaces/${session.workspaceId}/chat/sessions/${session.id}/messages`,
          body,
        );
        const updated: WorkspaceChatSession = { ...response.data.session, workspaceId: session.workspaceId };
        this.upsertSession(updated);
        this.messages = replaceLocalMessage(this.messages, response.data.message, localMessage.id);
        this.pendingConfirmation = undefined;
        this.pendingResumeToken = undefined;
        this.latestExecution = undefined;
        this.latestExecutionSteps = [];
        this.latestRunId = response.data.runId;
        this.latestRun = undefined;
        this.latestRunSteps = [];
        this.runStatus = "PENDING";
        this.subscribeRunStream(response.data.runId, session.workspaceId);
        return response.data;
      } catch (error) {
        this.messages = this.messages.filter((message) => message.id !== localMessage.id);
        throw error;
      }
    },
    async loadRun(runId: string, workspaceId?: string) {
      const scope = workspaceId || this.activeSession?.workspaceId;
      if (!scope) throw new Error(tt("chat.errRunWorkspaceUnknown"));
      const response = await apiClient.get<{ run: AgentRun; steps: AgentRunStep[] }>(
        `/workspaces/${scope}/agent-runs/${runId}`,
      );
      this.latestRun = response.data.run;
      this.latestRunId = response.data.run.id;
      this.latestRunSteps = [...response.data.steps].sort((left, right) => left.sequenceNo - right.sequenceNo);
      // Do not demote a terminal stream projection if GET races behind protocol events.
      if (!isTerminalRunStatus(this.runStatus) || isTerminalRunStatus(response.data.run.status)) {
        this.runStatus = response.data.run.status;
      }
      return response.data;
    },
    async loadExecutions(
      workspaceId: string,
      filter: {
        status?: string;
        traceId?: string;
        workflowId?: string;
        startedAfter?: string;
        startedBefore?: string;
        limit?: number;
      } = {},
    ) {
      const params = new URLSearchParams();
      if (filter.status) params.set("status", filter.status);
      if (filter.traceId) params.set("traceId", filter.traceId);
      if (filter.workflowId) params.set("workflowId", filter.workflowId);
      if (filter.startedAfter) params.set("startedAfter", filter.startedAfter);
      if (filter.startedBefore) params.set("startedBefore", filter.startedBefore);
      if (filter.limit) params.set("limit", String(filter.limit));
      const suffix = params.size ? `?${params.toString()}` : "";
      const response = await apiClient.get<{ items: WorkflowExecution[] }>(
        `/workspaces/${workspaceId}/executions${suffix}`,
      );
      this.executions = response.data.items;
      return response.data.items;
    },
    async loadExecution(workspaceId: string, executionId: string) {
      const response = await apiClient.get<{ execution: WorkflowExecution; steps: WorkflowExecutionStep[] }>(
        `/workspaces/${workspaceId}/executions/${executionId}`,
      );
      this.latestExecution = response.data.execution;
      this.latestExecutionSteps = response.data.steps;
      this.executions = [response.data.execution, ...this.executions.filter((item) => item.id !== executionId)];
      return response.data;
    },
    async confirmPending() {
      const session = this.activeSession;
      const confirmation = this.pendingConfirmation;
      if (!session || !confirmation) throw new Error(tt("chat.errNoPendingConfirmation"));
      if (!this.pendingResumeToken || confirmation.lockVersion < 1) {
        throw new Error(tt("chat.errConfirmationCredentialExpired"));
      }
      const response = await apiClient.post<ChatConfirmation>(
        `/workspaces/${session.workspaceId}/confirmations/${confirmation.id}:confirm`,
        { resumeToken: this.pendingResumeToken, lockVersion: confirmation.lockVersion },
      );
      this.pendingConfirmation = response.data;
      if (response.data.status !== "PENDING") this.clearConfirmationCredential(session.workspaceId, response.data.id);
      await this.refreshActiveRuntime();
      return response.data;
    },
    async cancelPending() {
      const session = this.activeSession;
      const confirmation = this.pendingConfirmation;
      if (!session || !confirmation || confirmation.lockVersion < 1)
        throw new Error(tt("chat.errNoCancellableConfirmation"));
      const response = await apiClient.post<ChatConfirmation>(
        `/workspaces/${session.workspaceId}/confirmations/${confirmation.id}:cancel`,
        { lockVersion: confirmation.lockVersion },
      );
      this.pendingConfirmation = response.data;
      if (response.data.status !== "PENDING") this.clearConfirmationCredential(session.workspaceId, response.data.id);
      await this.refreshActiveRuntime();
      return response.data;
    },
    async refreshActiveRuntime() {
      const session = this.activeSession;
      if (!session) return;
      await this.loadSession(session.id, session.workspaceId);
    },
    resetRuntimeState() {
      this.closeRunStream();
      projectors.clear();
      this.pendingConfirmation = undefined;
      this.pendingResumeToken = undefined;
      this.latestExecution = undefined;
      this.latestExecutionSteps = [];
      this.latestRunId = undefined;
      this.latestRun = undefined;
      this.latestRunSteps = [];
      this.runStatus = undefined;
    },
    restorePendingConfirmation(session: WorkspaceChatSession) {
      if (!session.pendingConfirmationId) return;
      const stored = readConfirmationCredential(session.workspaceId, session.pendingConfirmationId);
      if (stored?.confirmation.id === session.pendingConfirmationId) {
        this.pendingConfirmation = stored.confirmation;
        this.pendingResumeToken = stored.resumeToken;
        return;
      }
      this.pendingConfirmation = pendingConfirmationProjection(session);
    },
    upsertSession(session: WorkspaceChatSession) {
      const exists = this.sessions.some((item) => item.id === session.id);
      this.sessions = exists
        ? this.sessions.map((item) => (item.id === session.id ? session : item))
        : [session, ...this.sessions];
    },
    subscribeRunStream(runId: string, workspaceId?: string) {
      const scope = workspaceId || this.activeSession?.workspaceId;
      if (!runId || !scope || typeof fetch === "undefined") return;
      if (this.streamRunId === runId && this.runStreamAbort) return;
      this.closeRunStream();
      if (!projectors.has(runId)) projectors.set(runId, createProjectionState());
      this.streamHealthByRun[runId] = "CONNECTING";
      const controller = new AbortController();
      this.runStreamAbort = controller;
      this.streamRunId = runId;
      void this.consumeRunStream(scope, runId, controller);
    },
    /**
     * Consume protocol SSE (primary live path). Thin secondary RUN_* compat only.
     * - 404 not-ready: short bounded backoff (no user-facing toast)
     * - 401: best-effort auth refresh then resubscribe
     * - terminal status: close stream; optional loadSession calibrate
     */
    async consumeRunStream(workspaceId: string, runId: string, controller: AbortController) {
      let notReadyAttempts = 0;
      let networkAttempts = 0;
      let authRetried = false;

      while (!controller.signal.aborted && this.streamRunId === runId) {
        try {
          const cursor = this.runEventCursorByRun[runId] || 0;
          const response = await fetch(streamURL(workspaceId, runId), {
            credentials: "include",
            headers: streamHeaders(cursor),
            signal: controller.signal,
          });

          if (response.status === 404) {
            // Stream not ready / run-scope-not-found race — do not toast.
            notReadyAttempts += 1;
            if (notReadyAttempts > NOT_READY_MAX_ATTEMPTS) return;
            await sleep(notReadyDelayMs(notReadyAttempts), controller.signal);
            continue;
          }

          if (response.status === 401) {
            if (!authRetried) {
              authRetried = true;
              try {
                await refreshAuthForStream();
              } catch {
                // Fall through to reconnect with whatever token is present.
              }
              await sleep(notReadyDelayMs(1), controller.signal);
              continue;
            }
            // Second 401: stop without inventing a full auth system; leave session as-is.
            return;
          }

          if (!response.ok || !response.body) {
            networkAttempts += 1;
            if (networkAttempts > NETWORK_MAX_ATTEMPTS) return;
            await sleep(networkDelayMs(networkAttempts), controller.signal);
            continue;
          }

          notReadyAttempts = 0;
          networkAttempts = 0;
          authRetried = false;
          this.streamHealthByRun[runId] = "HEALTHY";

          for await (const frame of readSSEFrames(response.body, runId)) {
            if (controller.signal.aborted || this.streamRunId !== runId) return;
            this.applyStreamFrame(frame);
            if (isTerminalRunStatus(this.runStatus)) return;
          }

          if (controller.signal.aborted || this.streamRunId !== runId) return;
          if (isTerminalRunStatus(this.runStatus)) return;

          // Stream ended while run still active — reconnect with Last-Event-ID.
          networkAttempts += 1;
          if (networkAttempts > NETWORK_MAX_ATTEMPTS) {
            // Exhausted reconnect budget: calibrate GET within 5s (ZKL-56).
            await this.calibrateRunTerminal(runId, workspaceId, "stream_eof");
            return;
          }
          this.streamHealthByRun[runId] = "RECONNECTING";
          await sleep(networkDelayMs(networkAttempts), controller.signal);
        } catch (error) {
          if (controller.signal.aborted || this.streamRunId !== runId) return;
          if (isAbortError(error)) return;
          networkAttempts += 1;
          if (networkAttempts > NETWORK_MAX_ATTEMPTS) {
            await this.calibrateRunTerminal(runId, workspaceId, "network_exhausted");
            return;
          }
          this.streamHealthByRun[runId] = "RECONNECTING";
          try {
            await sleep(networkDelayMs(networkAttempts), controller.signal);
          } catch {
            return;
          }
        }
      }
    },
    /**
     * ZKL-56 GET calibration: singleflight per run, up to 3 attempts
     * (immediate / ~1.5s / ~3.5s) within a 5s deadline. Terminal is absorbing —
     * late RUNNING must not demote. Does not invent FAILED if server still RUNNING.
     */
    async calibrateRunTerminal(runId: string, workspaceId: string, _reason: string) {
      if (!runId || !workspaceId) return;
      const existing = calibrationInflight.get(runId);
      if (existing) {
        await existing;
        return;
      }
      const work = this.runCalibrationLoop(runId, workspaceId);
      calibrationInflight.set(runId, work);
      try {
        await work;
      } finally {
        calibrationInflight.delete(runId);
      }
    },
    async runCalibrationLoop(runId: string, workspaceId: string) {
      if (isTerminalRunStatus(this.runStatus) && this.latestRunId === runId) {
        return;
      }
      this.streamHealthByRun[runId] = "CALIBRATING";
      const started = Date.now();
      // immediate / ~1.5s / ~3.5s from start (design §4.3.2).
      const attemptAt = [0, 1500, 3500];
      for (let attempt = 0; attempt < attemptAt.length; attempt += 1) {
        const wait = attemptAt[attempt]! - (Date.now() - started);
        if (wait > 0) {
          await sleep(Math.min(wait, 5000 - (Date.now() - started))).catch(() => undefined);
        }
        if (Date.now() - started > 5000) break;
        try {
          await this.loadRun(runId, workspaceId);
          if (isTerminalRunStatus(this.runStatus)) {
            this.closeRunStream();
            this.streamHealthByRun[runId] = "HEALTHY";
            return;
          }
        } catch {
          // continue bounded retries
        }
      }
      // Still non-terminal after budget: DEGRADED, do not forge FAILED; composer stays gated by server status.
      if (!isTerminalRunStatus(this.runStatus)) {
        this.streamHealthByRun[runId] = "DEGRADED";
      }
    },
    closeRunStream() {
      this.runStreamAbort?.abort();
      if (this.runStreamReconnect) clearTimeout(this.runStreamReconnect);
      this.runStreamAbort = undefined;
      this.runStreamReconnect = undefined;
      // Keep per-run projectors for Last-Event-ID resume of the same run.
      this.streamRunId = undefined;
    },
    /**
     * Apply one decoded stream frame.
     * Live path is protocol-primary; legacy RUN_* is thin secondary compat only.
     */
    applyStreamFrame(frame: StreamFrame) {
      const prior = projectors.get(frame.runId) || createProjectionState();
      const { state, effects } = projectStreamFrame(prior, frame);
      projectors.set(frame.runId, state);

      this.runEventCursorByRun[frame.runId] = Math.max(this.runEventCursorByRun[frame.runId] || 0, effects.sequenceNo);

      this.applyStreamEffects(effects);

      const terminal = isTerminalRunStatus(this.runStatus);
      if (terminal) {
        this.closeRunStream();
        this.streamHealthByRun[frame.runId] = "HEALTHY";
        // Light calibrate: one GET for run/steps. Full bounded loop is for stream loss.
        const session = this.activeSession;
        if (session) {
          void this.loadRun(frame.runId, session.workspaceId).catch(() => undefined);
        }
        return;
      }

      // item.failed without terminal run → start bounded calibration.
      if (frame.type === "item.failed" && !isTerminalRunStatus(this.runStatus)) {
        const session = this.activeSession;
        if (session) {
          void this.calibrateRunTerminal(frame.runId, session.workspaceId, "item_failed");
        }
      }

      // Avoid mid-stream loadRun: a racing GET can overwrite live status (e.g. SUCCEEDED)
      // before item.delta finishes. Refresh only for confirmation waits or legacy step frames.
      const session = this.activeSession;
      const needsRuntimeRefresh =
        effects.runStatus === "WAITING_CONFIRMATION" || (effects.kind === "legacy" && !effects.skipLoadRun);
      if (session && needsRuntimeRefresh) {
        void this.loadRun(frame.runId, session.workspaceId).catch(() => undefined);
      }
    },
    applyStreamEffects(effects: ConsoleStreamEffects) {
      if (effects.runStatus) this.runStatus = effects.runStatus;

      for (const patch of effects.assistantMessages) {
        this.upsertAssistantMessage(toAssistantChatMessage(patch));
      }

      if (effects.legacyRun) this.applyRunUpdate(effects.legacyRun);
      if (effects.legacyStep) this.upsertRunStep(effects.legacyStep);
      if (effects.legacyMessage) this.appendMessage(effects.legacyMessage);
      if (effects.legacyExecution) this.latestExecution = effects.legacyExecution;

      if (effects.pendingConfirmation) {
        this.pendingConfirmation = effects.pendingConfirmation;
        this.pendingResumeToken = effects.resumeToken;
        const workspaceId = this.activeSession?.workspaceId;
        if (workspaceId) {
          persistConfirmationCredential(workspaceId, effects.pendingConfirmation, effects.resumeToken);
        }
        this.upsertActiveSession({ pendingConfirmationId: effects.pendingConfirmation.id });
      }
      if (effects.clearPendingConfirmation) {
        this.pendingConfirmation = undefined;
        this.pendingResumeToken = undefined;
        this.upsertActiveSession({ pendingConfirmationId: undefined });
      }
    },
    applyRunUpdate(run: AgentRun) {
      // Terminal is absorbing: late RUNNING/PENDING GET must not demote (ZKL-56).
      if (isTerminalRunStatus(this.runStatus) && !isTerminalRunStatus(run.status)) {
        this.latestRun = { ...run, status: this.runStatus! };
        this.latestRunId = run.id;
        this.upsertActiveSession({ latestRunId: run.id });
        return;
      }
      this.latestRun = run;
      this.latestRunId = run.id;
      this.runStatus = run.status;
      this.upsertActiveSession({ latestRunId: run.id });
    },
    upsertRunStep(step: AgentRunStep) {
      const exists = this.latestRunSteps.some((item) => item.id === step.id);
      this.latestRunSteps = (
        exists ? this.latestRunSteps.map((item) => (item.id === step.id ? step : item)) : [...this.latestRunSteps, step]
      ).sort((left, right) => left.sequenceNo - right.sequenceNo);
    },
    appendMessage(message: ChatMessage) {
      if (!this.messages.some((item) => item.id === message.id)) this.messages = [...this.messages, message];
    },
    upsertAssistantMessage(message: ChatMessage) {
      const index = this.messages.findIndex((item) => item.id === message.id);
      if (index < 0) {
        this.messages = [...this.messages, message];
        return;
      }
      const existing = this.messages[index];
      this.messages = this.messages.map((item, i) =>
        i === index
          ? {
              ...existing,
              ...message,
              // Preserve original createdAt once set.
              createdAt: existing.createdAt || message.createdAt,
            }
          : item,
      );
    },
    upsertActiveSession(patch: Partial<ChatSession>) {
      const session = this.activeSession;
      if (session) this.upsertSession({ ...session, ...patch });
    },
    clearConfirmationCredential(workspaceId: string, confirmationId: string) {
      removeSessionStorage(`${confirmationStoragePrefix}:${workspaceId}:${confirmationId}`);
      this.pendingResumeToken = undefined;
    },
  },
});

function streamURL(workspaceId: string, runId: string) {
  const baseURL = String(apiClient.defaults?.baseURL || "/api/v1").replace(/\/$/, "");
  return `${baseURL}/workspaces/${encodeURIComponent(workspaceId)}/agent-runs/${encodeURIComponent(runId)}/events`;
}

function streamHeaders(cursor: number) {
  const headers: Record<string, string> = { Accept: "text/event-stream" };
  const token = getAuthToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (cursor > 0) headers["Last-Event-ID"] = String(cursor);
  return headers;
}

async function* readSSEFrames(stream: ReadableStream<Uint8Array>, runId: string): AsyncGenerator<StreamFrame> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    while (true) {
      const { done, value } = await reader.read();
      buffer += decoder.decode(value, { stream: !done }).replace(/\r\n/g, "\n");
      let boundary = buffer.indexOf("\n\n");
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const frame = parseSSEBlock(block, runId);
        if (frame) yield frame;
        boundary = buffer.indexOf("\n\n");
      }
      if (done) break;
    }
  } finally {
    reader.releaseLock();
  }
}

/**
 * Best-effort token refresh for raw fetch SSE (axios interceptor does not apply).
 * Uses the same /auth/refresh cookie path as apiClient.
 */
async function refreshAuthForStream() {
  await refreshAuthSession();
}

function isAbortError(error: unknown) {
  return (
    (error instanceof DOMException && error.name === "AbortError") ||
    (error instanceof Error && error.name === "AbortError")
  );
}

function localUserMessage(content: string): ChatMessage {
  return {
    id: `local-user-${Date.now()}-${Math.random().toString(36).slice(2)}`,
    role: "USER",
    content,
    contentSha256: "",
    contentLength: new TextEncoder().encode(content).byteLength,
    status: "PROCESSING",
    createdAt: new Date().toISOString(),
  };
}

function replaceLocalMessage(messages: ChatMessage[], serverMessage: ChatMessage, localMessageId: string) {
  if (messages.some((message) => message.id === serverMessage.id))
    return messages.filter((message) => message.id !== localMessageId);
  return messages.map((message) => (message.id === localMessageId ? serverMessage : message));
}

function pendingConfirmationProjection(session: WorkspaceChatSession): ChatConfirmation {
  return {
    id: session.pendingConfirmationId || "",
    sessionId: session.id,
    runId: session.latestRunId || "",
    targetType: "WORKFLOW",
    targetReleaseId: "",
    riskLevel: "HIGH",
    riskReasons: [tt("chat.riskWaitingOriginalRequester")],
    inputSummary: {},
    status: "PENDING",
    requestedBy: "",
    createdAt: session.updatedAt,
    expiresAt: "",
    lockVersion: 0,
    cached: false,
  };
}

function persistActiveSession(session: WorkspaceChatSession) {
  writeSessionStorage(
    activeSessionStorageKey,
    JSON.stringify({ sessionId: session.id, workspaceId: session.workspaceId }),
  );
}

function readActiveSession(): { sessionId: string; workspaceId: string } | undefined {
  const value = readSessionStorage(activeSessionStorageKey);
  if (!value) return undefined;
  try {
    return JSON.parse(value) as { sessionId: string; workspaceId: string };
  } catch {
    return undefined;
  }
}

function persistConfirmationCredential(workspaceId: string, confirmation: ChatConfirmation, resumeToken?: string) {
  writeSessionStorage(
    `${confirmationStoragePrefix}:${workspaceId}:${confirmation.id}`,
    JSON.stringify({ confirmation, resumeToken }),
  );
}

function readConfirmationCredential(workspaceId: string, confirmationId: string) {
  const value = readSessionStorage(`${confirmationStoragePrefix}:${workspaceId}:${confirmationId}`);
  if (!value) return undefined;
  try {
    return JSON.parse(value) as { confirmation: ChatConfirmation; resumeToken?: string };
  } catch {
    return undefined;
  }
}

function readSessionStorage(key: string) {
  try {
    return typeof sessionStorage === "undefined" ? undefined : sessionStorage.getItem(key) || undefined;
  } catch {
    return undefined;
  }
}

function writeSessionStorage(key: string, value: string) {
  try {
    if (typeof sessionStorage !== "undefined") sessionStorage.setItem(key, value);
  } catch {
    // Storage can be disabled without breaking server-backed session recovery.
  }
}

function removeSessionStorage(key: string) {
  try {
    if (typeof sessionStorage !== "undefined") sessionStorage.removeItem(key);
  } catch {
    // Storage can be disabled without breaking server-backed session recovery.
  }
}

/** Test helper: reset module-level projector map between tests. */
export function __resetChatStreamProjectorsForTests() {
  projectors.clear();
}
