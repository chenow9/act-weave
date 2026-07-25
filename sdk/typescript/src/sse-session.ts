import { isAAPV1ProtocolEventType } from "./types.js";
import type {
  AAPSEMessage,
  ProtocolEventEnvelope,
  SSEFrame,
  StreamErrorSignal,
} from "./types.js";

export interface AAPSESessionOptions {
  /** When set, the first event must be sequence === lastSequence + 1. */
  initialLastSequence?: number;
  /** Max eventIds retained for at-least-once de-duplication. */
  maxSeenEventIds?: number;
}

/**
 * Applies AAP SSE semantics on top of decoded frames:
 * - eventId de-duplication (at-least-once)
 * - sequence gap detection (next > last+1 → reconnect with Last-Event-ID)
 * - stream.error transport signals never advance the cursor
 * - heartbeats never advance the cursor
 * - unknown protocol event types still advance the cursor
 */
export class AAPSESession {
  private lastSequence: number | null;
  private readonly seenEventIds = new Set<string>();
  private readonly seenOrder: string[] = [];
  private readonly maxSeenEventIds: number;
  private gapLatched = false;

  constructor(options: AAPSESessionOptions = {}) {
    this.lastSequence =
      options.initialLastSequence !== undefined && Number.isFinite(options.initialLastSequence)
        ? options.initialLastSequence
        : null;
    this.maxSeenEventIds = options.maxSeenEventIds ?? 10_000;
  }

  /** Last successfully applied Run sequence, or null before the first event. */
  getLastSequence(): number | null {
    return this.lastSequence;
  }

  /**
   * Value for the HTTP `Last-Event-ID` header on resume.
   * AAP uses the Run sequence as the SSE id.
   */
  getLastEventId(): string | undefined {
    return this.lastSequence === null ? undefined : String(this.lastSequence);
  }

  /** Headers for a GET resume / re-attach request. */
  resumeHeaders(extra?: HeadersInit): Headers {
    const headers = new Headers(extra);
    headers.set("Accept", "text/event-stream");
    const lastEventId = this.getLastEventId();
    if (lastEventId !== undefined) {
      headers.set("Last-Event-ID", lastEventId);
    }
    return headers;
  }

  /**
   * Process one SSE frame into zero or more AAP stream messages.
   * After a sequence gap, further protocol events are ignored until reset/reconnect.
   */
  pushFrame(frame: SSEFrame): AAPSEMessage[] {
    const messages: AAPSEMessage[] = [];

    // Heartbeat / comment-only frames (backend: `: ping <ts>`).
    if (
      frame.comments.length > 0 &&
      frame.data === "" &&
      frame.event === undefined &&
      frame.id === undefined
    ) {
      for (const comment of frame.comments) {
        messages.push({ kind: "heartbeat", comment });
      }
      return messages;
    }

    const eventType = frame.event ?? "";

    // Transport signal: no SSE id, must not advance cursor.
    if (eventType === "stream.error") {
      const signal = parseStreamError(frame.data);
      if (!signal) {
        messages.push({
          kind: "malformed",
          reason: "stream.error payload is not a valid transport signal",
          frame,
        });
        return messages;
      }
      messages.push({ kind: "transport_signal", signal });
      return messages;
    }

    if (this.gapLatched) {
      return messages;
    }

    if (frame.data === "") {
      // Non-data frames that are not heartbeats/errors are ignored.
      return messages;
    }

    let envelope: ProtocolEventEnvelope;
    try {
      envelope = JSON.parse(frame.data) as ProtocolEventEnvelope;
    } catch {
      messages.push({
        kind: "malformed",
        reason: "protocol event data is not JSON",
        frame,
      });
      return messages;
    }

    if (!isObject(envelope) || typeof envelope.eventId !== "string" ||
        typeof envelope.type !== "string" || typeof envelope.sequence !== "number" ||
        !Number.isInteger(envelope.sequence) || envelope.sequence < 1) {
      messages.push({
        kind: "malformed",
        reason: "protocol event envelope is missing eventId/type/sequence",
        frame,
      });
      return messages;
    }

    // Prefer envelope.type; event: field should match when present.
    if (eventType !== "" && eventType !== envelope.type) {
      messages.push({
        kind: "malformed",
        reason: `SSE event field "${eventType}" does not match JSON type "${envelope.type}"`,
        frame,
      });
      return messages;
    }

    const sseId = parseSSEId(frame.id, envelope.sequence);
    if (sseId === null) {
      messages.push({
        kind: "malformed",
        reason: "SSE id is missing or does not match sequence",
        frame,
      });
      return messages;
    }

    // De-duplicate by eventId first (at-least-once delivery).
    if (this.seenEventIds.has(envelope.eventId)) {
      messages.push({
        kind: "duplicate",
        eventId: envelope.eventId,
        sequence: envelope.sequence,
        sseId,
      });
      return messages;
    }

    // Sequence gap: disconnect and resume with Last-Event-ID = last applied.
    if (this.lastSequence !== null && envelope.sequence > this.lastSequence + 1) {
      this.gapLatched = true;
      messages.push({
        kind: "sequence_gap",
        expected: this.lastSequence + 1,
        actual: envelope.sequence,
        lastEventId: String(this.lastSequence),
      });
      return messages;
    }

    // Stale / rewound sequence without matching eventId — treat as duplicate-ish skip.
    if (this.lastSequence !== null && envelope.sequence <= this.lastSequence) {
      messages.push({
        kind: "duplicate",
        eventId: envelope.eventId,
        sequence: envelope.sequence,
        sseId,
      });
      return messages;
    }

    this.rememberEventId(envelope.eventId);
    this.lastSequence = envelope.sequence;
    messages.push({
      kind: "protocol_event",
      sseId,
      event: envelope,
      unknownType: !isAAPV1ProtocolEventType(envelope.type),
    });
    return messages;
  }

  /** Clear gap latch after the caller opens a new connection with Last-Event-ID. */
  clearGapLatch(): void {
    this.gapLatched = false;
  }

  private rememberEventId(eventId: string): void {
    if (this.seenEventIds.has(eventId)) {
      return;
    }
    this.seenEventIds.add(eventId);
    this.seenOrder.push(eventId);
    while (this.seenOrder.length > this.maxSeenEventIds) {
      const oldest = this.seenOrder.shift();
      if (oldest !== undefined) {
        this.seenEventIds.delete(oldest);
      }
    }
  }
}

function parseSSEId(id: string | undefined, sequence: number): number | null {
  if (id === undefined || id === "") {
    // Backend always sets id for protocol events; require it.
    return null;
  }
  if (!/^\d+$/.test(id)) {
    return null;
  }
  const value = Number.parseInt(id, 10);
  if (value !== sequence) {
    return null;
  }
  return value;
}

function parseStreamError(data: string): StreamErrorSignal | null {
  try {
    const value = JSON.parse(data) as StreamErrorSignal;
    if (
      !isObject(value) ||
      value.type !== "stream.error" ||
      typeof value.specVersion !== "string" ||
      typeof value.occurredAt !== "string" ||
      !isObject(value.error) ||
      typeof value.error.code !== "string" ||
      typeof value.error.message !== "string" ||
      typeof value.error.retryable !== "boolean"
    ) {
      return null;
    }
    // Must not look like a protocol event cursor carrier.
    if ("eventId" in value || "sequence" in value || "streamId" in value) {
      return null;
    }
    return value;
  } catch {
    return null;
  }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
