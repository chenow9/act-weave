/**
 * AAP protocol types for the SSE transport layer and shared wire envelope.
 * Domain models for the Run Reducer / API client live in models.ts.
 */

// Protocol event catalog is generated from the Schema Registry (M10-T1).
import {
  AAP_V1_EVENT_TYPES,
  isAAPV1EventType,
  type AAPV1EventType,
} from "./generated/protocol.gen.js";

export type JSONValue =
  | null
  | boolean
  | number
  | string
  | JSONValue[]
  | { [key: string]: JSONValue };

/** Wire envelope of a persisted Protocol Event (JSON `data:` payload). */
export interface ProtocolEventEnvelope {
  specVersion: string;
  type: string;
  eventId: string;
  streamId: string;
  sequence: number;
  occurredAt: string;
  workspaceId: string;
  agentId: string;
  conversationId: string;
  runId: string;
  traceId: string;
  data: JSONValue;
  [key: string]: JSONValue | undefined;
}

/** Transport-only signal; has no SSE id / sequence / eventId. */
export interface StreamErrorSignal {
  specVersion: string;
  type: "stream.error";
  occurredAt: string;
  error: {
    code: string;
    message: string;
    retryable: boolean;
    requestId?: string;
    traceId?: string;
    details?: Array<Record<string, JSONValue>>;
  };
}

/** One decoded SSE frame before AAP semantics are applied. */
export interface SSEFrame {
  id?: string;
  event?: string;
  data: string;
  retry?: number;
  comments: string[];
}

export type AAPSEMessage =
  | {
      kind: "protocol_event";
      /** SSE `id` (Run sequence cursor). */
      sseId: number;
      event: ProtocolEventEnvelope;
      /** True when the event type is not in the frozen v1 set; still advances cursor. */
      unknownType: boolean;
    }
  | {
      kind: "transport_signal";
      signal: StreamErrorSignal;
    }
  | {
      kind: "heartbeat";
      comment: string;
    }
  | {
      kind: "duplicate";
      eventId: string;
      sequence: number;
      sseId: number;
    }
  | {
      kind: "sequence_gap";
      expected: number;
      actual: number;
      /** Cursor to send as Last-Event-ID after disconnect. */
      lastEventId: string;
    }
  | {
      kind: "malformed";
      reason: string;
      frame: SSEFrame;
    };

/** Frozen v1 event catalog alias (generated from Schema Registry). */
export const AAP_V1_PROTOCOL_EVENT_TYPES = AAP_V1_EVENT_TYPES;

export type AAPV1ProtocolEventType = AAPV1EventType;

export function isAAPV1ProtocolEventType(value: string): value is AAPV1ProtocolEventType {
  return isAAPV1EventType(value);
}
