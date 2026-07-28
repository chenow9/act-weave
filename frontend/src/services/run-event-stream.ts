/**
 * Console run-event stream: protocol SSE parse + pure projection.
 *
 * Live SSE authority (SoT) is **protocol** event types (`item.delta`, `run.*`, …)
 * shared with AAP / SDK. `RUN_*` is **not** the legal sole whitelist for Console
 * live consumption — it exists only as a thin one-release secondary compat branch.
 *
 * Auth remains user JWT on `/api/v1/.../agent-runs/:id/events` (internal Console
 * entry). Does not depend on @actweave/agent-client or AAP base URL.
 *
 * See: docs/runbooks/protocol-event-console-vs-aap-entrypoints.md
 */

import type {
  AgentRun,
  AgentRunStatus,
  AgentRunStep,
  ChatConfirmation,
  ChatMessage,
  WorkflowExecution,
} from "../types/domain";

/**
 * Primary known protocol types for live SSE (Console main path).
 * Unknown types are still delivered as `kind: "unknown"` so the cursor advances.
 */
export const PROTOCOL_STREAM_EVENT_TYPES = [
  "run.accepted",
  "run.started",
  "run.waiting",
  "run.resumed",
  "run.completed",
  "run.failed",
  "run.cancelled",
  "item.started",
  "item.delta",
  "item.completed",
  "item.failed",
  "interaction.requested",
  "interaction.resolved",
  "interaction.expired",
  "usage.updated",
] as const;

export type ProtocolStreamEventType = (typeof PROTOCOL_STREAM_EVENT_TYPES)[number];

/**
 * Secondary thin-compat only — **not** the Console live whitelist.
 * Kept for one release so residual/test legacy frames still project; safe to delete
 * after callers/tests are fully protocol. Production stream SoT remains protocol.
 */
export const LEGACY_RUNTIME_EVENT_TYPES = [
  "RUN_STARTED",
  "STEP_STARTED",
  "STEP_COMPLETED",
  "RUN_WAITING_CONFIRMATION",
  "RUN_RESUMED",
  "RUN_COMPLETED",
  "RUN_FAILED",
  "RUN_CANCELLED",
] as const;

export type LegacyRuntimeEventType = (typeof LEGACY_RUNTIME_EVENT_TYPES)[number];

export type StreamEventKind = "protocol" | "legacy" | "unknown";

/** One decoded SSE event after id/event/data parsing. */
export interface StreamFrame {
  type: string;
  sequenceNo: number;
  runId: string;
  kind: StreamEventKind;
  /** Protocol envelope `data` object, or legacy payload root. */
  data: Record<string, unknown>;
  /** Full protocol envelope when present. */
  envelope?: Record<string, unknown>;
}

/** Pure projection state (no I/O). */
export interface ConsoleRunProjectionState {
  lastSequence: number;
  runStatus?: AgentRunStatus;
  /** itemId → streaming assistant bubble text */
  assistantByItemId: Record<string, { content: string; finalized: boolean }>;
  assistantOrder: string[];
}

/** Side-effects / UI patches derived from one frame. */
export interface ConsoleStreamEffects {
  sequenceNo: number;
  runId: string;
  type: string;
  kind: StreamEventKind;
  runStatus?: AgentRunStatus;
  /** Upsert assistant bubbles (id = protocol item id). */
  assistantMessages: Array<{
    id: string;
    content: string;
    status: ChatMessage["status"];
    runId?: string;
    finalized: boolean;
  }>;
  /** True when this frame is a known terminal run status. */
  terminal: boolean;
  /** Best-effort confirmation mapping from run.waiting / legacy. */
  pendingConfirmation?: ChatConfirmation;
  resumeToken?: string;
  clearPendingConfirmation?: boolean;
  /** Legacy payload passthrough. */
  legacyMessage?: ChatMessage;
  legacyRun?: AgentRun;
  legacyStep?: AgentRunStep;
  legacyExecution?: WorkflowExecution;
  /** Skip loadRun for high-frequency deltas. */
  skipLoadRun?: boolean;
}

/** 404 not-ready: 200ms → 500ms → 1s, max ~15 attempts (design §6.5). */
export const NOT_READY_MAX_ATTEMPTS = 15;
export const NOT_READY_BACKOFF_MS = [200, 500, 1000] as const;

/** General reconnect / 5xx: bounded like SDK (~8). */
export const NETWORK_MAX_ATTEMPTS = 8;

export function createProjectionState(): ConsoleRunProjectionState {
  return {
    lastSequence: 0,
    runStatus: undefined,
    assistantByItemId: {},
    assistantOrder: [],
  };
}

/**
 * Delay for the Nth not-ready attempt (1-based).
 * attempt 1 → 200ms, 2 → 500ms, 3+ → 1000ms.
 */
export function notReadyDelayMs(attempt: number): number {
  if (attempt <= 1) return NOT_READY_BACKOFF_MS[0];
  if (attempt === 2) return NOT_READY_BACKOFF_MS[1];
  return NOT_READY_BACKOFF_MS[2];
}

/** Network/5xx backoff: ~500ms * 2^(n-1), capped at 8s. */
export function networkDelayMs(attempt: number): number {
  const base = 500;
  const exp = Math.min(Math.max(attempt, 1), 5);
  return Math.min(base * 2 ** (exp - 1), 8000);
}

export function isTerminalRunStatus(status?: AgentRunStatus): boolean {
  return status === "SUCCEEDED" || status === "FAILED" || status === "CANCELLED";
}

/** Map protocol run.status → Console AgentRunStatus. */
export function mapProtocolRunStatus(status: unknown): AgentRunStatus | undefined {
  if (typeof status !== "string") return undefined;
  switch (status) {
    case "accepted":
      return "PENDING";
    case "running":
      return "RUNNING";
    case "waiting_interaction":
      return "WAITING_CONFIRMATION";
    case "completed":
      return "SUCCEEDED";
    case "failed":
      return "FAILED";
    case "cancelled":
      return "CANCELLED";
    default:
      return undefined;
  }
}

export function isLegacyStepEvent(type: string): boolean {
  return type === "STEP_STARTED" || type === "STEP_COMPLETED";
}

/**
 * Parse one SSE block (`id` / `event` / `data` lines joined by `\n`).
 *
 * Classification order (main path is protocol; RUN_* is never sole gate):
 * 1. Protocol envelope / dotted protocol type → kind protocol|unknown
 * 2. Thin legacy RUN_* / STEP_* → kind legacy (secondary compat only)
 * 3. Anything else with a sequence → kind unknown (cursor advances)
 *
 * Heartbeats / empty / non-JSON are dropped (return undefined).
 */
export function parseSSEBlock(block: string, fallbackRunId: string): StreamFrame | undefined {
  let sequenceNo = 0;
  let type = "";
  const dataLines: string[] = [];
  for (const rawLine of block.split("\n")) {
    const line = rawLine.replace(/\r$/, "");
    if (!line || line.startsWith(":")) continue;
    if (line.startsWith("id:")) sequenceNo = Number(line.slice(3).trim());
    else if (line.startsWith("event:")) type = line.slice(6).trim();
    else if (line.startsWith("data:")) dataLines.push(line.slice(5).replace(/^\s/, ""));
  }
  if (!Number.isSafeInteger(sequenceNo) || sequenceNo < 1 || !type) return undefined;
  if (dataLines.length === 0) return undefined;

  let parsed: unknown;
  try {
    parsed = JSON.parse(dataLines.join("\n"));
  } catch {
    return undefined;
  }
  if (!isRecord(parsed)) return undefined;

  // Protocol wire (primary): full envelope in data: with nested `data` payload.
  if (isProtocolEnvelope(parsed)) {
    const envelopeType = typeof parsed.type === "string" ? parsed.type : type;
    const runId = typeof parsed.runId === "string" && parsed.runId ? parsed.runId : fallbackRunId;
    const data = isRecord(parsed.data) ? parsed.data : {};
    return {
      type: envelopeType || type,
      sequenceNo:
        typeof parsed.sequence === "number" && Number.isSafeInteger(parsed.sequence) ? parsed.sequence : sequenceNo,
      runId,
      kind: isKnownProtocolType(envelopeType) ? "protocol" : "unknown",
      data,
      envelope: parsed,
    };
  }

  // event: protocol type with data-only body (defensive primary path).
  if (type.includes(".")) {
    return {
      type,
      sequenceNo,
      runId: fallbackRunId,
      kind: isKnownProtocolType(type) ? "protocol" : "unknown",
      data: parsed,
    };
  }

  // Thin secondary compat: legacy RUN_* / STEP_* payload root (not sole whitelist).
  if (isLegacyType(type)) {
    return {
      type,
      sequenceNo,
      runId: fallbackRunId,
      kind: "legacy",
      data: parsed,
    };
  }

  // Completely unknown — still deliver so consumers can advance cursor without abort.
  return {
    type,
    sequenceNo,
    runId: fallbackRunId,
    kind: "unknown",
    data: parsed,
  };
}

/**
 * Pure apply: accumulate assistant text + map run status.
 * Non-contiguous sequences are accepted (Console may resume mid-stream).
 * Unknown types produce empty effects aside from sequence advancement.
 */
export function applyStreamFrame(
  state: ConsoleRunProjectionState,
  frame: StreamFrame,
): { state: ConsoleRunProjectionState; effects: ConsoleStreamEffects } {
  const next: ConsoleRunProjectionState = {
    lastSequence: Math.max(state.lastSequence, frame.sequenceNo),
    runStatus: state.runStatus,
    assistantByItemId: { ...state.assistantByItemId },
    assistantOrder: [...state.assistantOrder],
  };

  const effects: ConsoleStreamEffects = {
    sequenceNo: frame.sequenceNo,
    runId: frame.runId,
    type: frame.type,
    kind: frame.kind,
    assistantMessages: [],
    terminal: false,
  };

  if (frame.kind === "legacy") {
    applyLegacyFrame(next, frame, effects);
    effects.terminal = isTerminalRunStatus(effects.runStatus ?? next.runStatus);
    if (effects.runStatus) next.runStatus = effects.runStatus;
    return { state: next, effects };
  }

  if (frame.kind === "unknown") {
    // Ignore payload; sequence already advanced.
    return { state: next, effects };
  }

  applyProtocolFrame(next, frame, effects);
  if (effects.runStatus) next.runStatus = effects.runStatus;
  effects.terminal = isTerminalRunStatus(effects.runStatus ?? next.runStatus);
  return { state: next, effects };
}

function applyProtocolFrame(state: ConsoleRunProjectionState, frame: StreamFrame, effects: ConsoleStreamEffects): void {
  const { type, data, runId } = frame;

  if (type.startsWith("run.")) {
    const run = isRecord(data.run) ? data.run : undefined;
    const mapped = mapProtocolRunStatus(run?.status) ?? mapRunEventType(type);
    if (mapped) effects.runStatus = mapped;

    if (type === "run.waiting") {
      effects.runStatus = "WAITING_CONFIRMATION";
      // Best-effort: confirmation may live in data.confirmation or require loadSession.
      if (isRecord(data.confirmation)) {
        effects.pendingConfirmation = data.confirmation as unknown as ChatConfirmation;
      }
      if (typeof data.resumeToken === "string") effects.resumeToken = data.resumeToken;
    }
    if (type === "run.resumed") {
      effects.runStatus = "RUNNING";
      effects.clearPendingConfirmation = true;
    }
    if (type === "run.accepted" && !effects.runStatus) effects.runStatus = "PENDING";
    if (type === "run.started" && !effects.runStatus) effects.runStatus = "RUNNING";
    if (type === "run.completed") effects.runStatus = "SUCCEEDED";
    if (type === "run.failed") effects.runStatus = "FAILED";
    if (type === "run.cancelled") effects.runStatus = "CANCELLED";
    return;
  }

  switch (type) {
    case "item.started": {
      const item = isRecord(data.item) ? data.item : undefined;
      if (!item || typeof item.id !== "string") return;
      if (item.type === "message" && item.role === "assistant") {
        const content = extractMessageText(item);
        ensureAssistant(state, item.id, content, false);
        effects.assistantMessages.push({
          id: item.id,
          content,
          status: "PROCESSING",
          runId,
          finalized: false,
        });
      }
      return;
    }
    case "item.delta": {
      effects.skipLoadRun = true;
      const itemId = typeof data.itemId === "string" ? data.itemId : "";
      const delta = isRecord(data.delta) ? data.delta : undefined;
      if (!itemId || !delta) return;
      if (delta.type !== "text_delta") return;
      const text = typeof delta.text === "string" ? delta.text : "";
      const existing = state.assistantByItemId[itemId];
      // Resilient: create bubble if started was missed (reconnect / late join).
      const prev = existing?.content ?? "";
      const nextContent = prev + text;
      ensureAssistant(state, itemId, nextContent, false);
      effects.assistantMessages.push({
        id: itemId,
        content: nextContent,
        status: "PROCESSING",
        runId,
        finalized: false,
      });
      return;
    }
    case "item.completed": {
      const item = isRecord(data.item) ? data.item : undefined;
      if (!item || typeof item.id !== "string") return;
      if (item.type === "message" && (item.role === "assistant" || state.assistantByItemId[item.id])) {
        const content = extractMessageText(item) || state.assistantByItemId[item.id]?.content || "";
        ensureAssistant(state, item.id, content, true);
        effects.assistantMessages.push({
          id: item.id,
          content,
          status: "EXECUTED",
          runId,
          finalized: true,
        });
      }
      return;
    }
    case "item.failed": {
      const item = isRecord(data.item) ? data.item : undefined;
      if (!item || typeof item.id !== "string") return;
      if (state.assistantByItemId[item.id] || (item.type === "message" && item.role === "assistant")) {
        const content = extractMessageText(item) || state.assistantByItemId[item.id]?.content || "";
        ensureAssistant(state, item.id, content, true);
        effects.assistantMessages.push({
          id: item.id,
          content,
          status: "FAILED",
          runId,
          finalized: true,
        });
      }
      return;
    }
    default:
      // interaction.*, usage.updated, etc. — ignore for chat bubble projection.
      return;
  }
}

/** Secondary thin-compat projection for residual RUN_* frames (not production SoT). */
function applyLegacyFrame(state: ConsoleRunProjectionState, frame: StreamFrame, effects: ConsoleStreamEffects): void {
  const payload = frame.data;
  if (isRecord(payload.run)) effects.legacyRun = payload.run as unknown as AgentRun;
  if (isRecord(payload.step)) effects.legacyStep = payload.step as unknown as AgentRunStep;
  if (isRecord(payload.message)) effects.legacyMessage = payload.message as unknown as ChatMessage;
  if (isRecord(payload.execution)) effects.legacyExecution = payload.execution as unknown as WorkflowExecution;

  if (frame.type === "RUN_WAITING_CONFIRMATION") {
    effects.runStatus = "WAITING_CONFIRMATION";
    if (isRecord(payload.confirmation)) {
      effects.pendingConfirmation = payload.confirmation as unknown as ChatConfirmation;
    }
    if (typeof payload.resumeToken === "string") effects.resumeToken = payload.resumeToken;
  } else if (frame.type === "RUN_RESUMED") {
    effects.runStatus = "RUNNING";
    effects.clearPendingConfirmation = true;
  } else if (frame.type === "RUN_STARTED") {
    effects.runStatus = "RUNNING";
  } else if (frame.type === "RUN_COMPLETED") {
    effects.runStatus = "SUCCEEDED";
  } else if (frame.type === "RUN_FAILED") {
    effects.runStatus = "FAILED";
  } else if (frame.type === "RUN_CANCELLED") {
    effects.runStatus = "CANCELLED";
  }

  // STEP_* carries step status — never promote to run terminal (ZKL-8).
  if (typeof payload.status === "string" && !isLegacyStepEvent(frame.type)) {
    effects.runStatus = payload.status as AgentRunStatus;
  }

  if (effects.legacyRun?.status) {
    effects.runStatus = effects.legacyRun.status;
  }

  // Keep projector runStatus in sync for terminal checks.
  if (effects.runStatus) state.runStatus = effects.runStatus;
}

function mapRunEventType(type: string): AgentRunStatus | undefined {
  switch (type) {
    case "run.accepted":
      return "PENDING";
    case "run.started":
    case "run.resumed":
      return "RUNNING";
    case "run.waiting":
      return "WAITING_CONFIRMATION";
    case "run.completed":
      return "SUCCEEDED";
    case "run.failed":
      return "FAILED";
    case "run.cancelled":
      return "CANCELLED";
    default:
      return undefined;
  }
}

function ensureAssistant(state: ConsoleRunProjectionState, itemId: string, content: string, finalized: boolean): void {
  if (!state.assistantByItemId[itemId]) {
    state.assistantOrder.push(itemId);
  }
  state.assistantByItemId[itemId] = { content, finalized };
}

function extractMessageText(item: Record<string, unknown>): string {
  if (!Array.isArray(item.content)) return "";
  const parts: string[] = [];
  for (const part of item.content) {
    if (isRecord(part) && part.type === "text" && typeof part.text === "string") {
      parts.push(part.text);
    }
  }
  return parts.join("");
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isProtocolEnvelope(value: Record<string, unknown>): boolean {
  return (
    typeof value.specVersion === "string" &&
    typeof value.type === "string" &&
    typeof value.sequence === "number" &&
    "data" in value
  );
}

function isKnownProtocolType(type: string): boolean {
  return (PROTOCOL_STREAM_EVENT_TYPES as readonly string[]).includes(type);
}

function isLegacyType(type: string): boolean {
  return (LEGACY_RUNTIME_EVENT_TYPES as readonly string[]).includes(type);
}

/** Abort-aware sleep for backoff loops. */
export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError());
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function abortError(): DOMException {
  return new DOMException("Aborted", "AbortError");
}

/** Build a ChatMessage projection for an assistant stream bubble. */
export function toAssistantChatMessage(patch: ConsoleStreamEffects["assistantMessages"][number]): ChatMessage {
  return {
    id: patch.id,
    role: "ASSISTANT",
    content: patch.content,
    contentSha256: "",
    contentLength: new TextEncoder().encode(patch.content).byteLength,
    status: patch.status,
    runId: patch.runId,
    createdAt: new Date().toISOString(),
  };
}
