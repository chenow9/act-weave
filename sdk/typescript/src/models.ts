/**
 * AAP v1 domain models used by the Run Reducer and API client.
 * Items and interactions are kept as JSON-shaped records so unknown additive
 * fields survive round-trips without hiding final snapshots.
 */

import type { JSONValue } from "./types.js";

export type RunStatus =
  | "accepted"
  | "running"
  | "waiting_interaction"
  | "completed"
  | "failed"
  | "cancelled"
  | string;

export type RunTrigger = "message" | "api" | "workflow" | "system" | string;

export interface ProtocolErrorValue {
  code: string;
  message: string;
  retryable: boolean;
  details?: JSONValue | undefined;
}

export interface ProtocolRun {
  id: string;
  conversationId: string;
  agentId: string;
  status: RunStatus;
  trigger: RunTrigger;
  startedAt: string;
  completedAt?: string;
  error?: ProtocolErrorValue;
}

export interface ProtocolUsage {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

/** Ordered Item union snapshot (message / tool_call / workflow_step / …). */
export interface ProtocolItem {
  id: string;
  type: string;
  status: string;
  [key: string]: unknown;
}

/** AAP context_compaction item (ZKL-81). Additive; old clients ignore unknown type. */
export interface ContextCompactionItem extends ProtocolItem {
  type: "context_compaction";
  result?: "completed" | "fallback" | "failed" | "building" | string;
  triggerBps?: number;
  targetBps?: number;
  beforeTokens?: number;
  afterTokens?: number;
  effectiveMaxInputTokens?: number;
  coverageStartMessageId?: string;
  coverageEndMessageId?: string;
  sourceMessageCount?: number;
  passes?: number;
  reused?: boolean;
  summaryId?: string;
  summaryDigest?: string;
  fallbackFrom?: string;
  fallbackTo?: string;
  fallbackStage?: string;
  errorCode?: string;
  contentIncluded: boolean;
  /** Permanent plaintext only when contentIncluded && result===completed (T4-B). */
  summary?: string;
}

export function isContextCompactionItem(item: ProtocolItem): item is ContextCompactionItem {
  return item?.type === "context_compaction";
}

export interface ProtocolInteraction {
  id: string;
  kind: string;
  status: string;
  version?: number;
  [key: string]: unknown;
}

export interface ReducedRunSnapshot {
  run: ProtocolRun | null;
  items: ProtocolItem[];
  interactions: ProtocolInteraction[];
  usage: ProtocolUsage | null;
  lastSequence: number;
}

/** Wire API resources (OpenAPI agent-access-v1). */
export interface AgentProfile {
  object: "agent_profile";
  id: string;
  name: string;
  description: string;
  version: string;
  supportedContent: unknown[];
  capabilities: unknown[];
  interactionRequirements: Record<string, unknown>;
}

export interface RunSummary {
  object: "run";
  id: string;
  status: RunStatus;
  version: number;
  startedAt: string;
  completedAt?: string;
  errorCode?: string;
}

export interface Conversation {
  object: "conversation";
  id: string;
  agentId: string;
  title: string;
  status: "active" | "archived" | string;
  version: number;
  latestRunId?: string;
  createdAt: string;
  updatedAt: string;
  runs: RunSummary[];
}

export interface CreateConversationResponse {
  conversation: Conversation;
  idempotent: boolean;
}

export interface CreateRunInputMessage {
  type: "message";
  role: "user";
  content: Array<{ type: "text"; text: string }>;
}

/** Write-only REQUEST_PASSTHROUGH envelope; never appears in responses. */
export interface OutboundCredentialsEnvelope {
  schemaVersion: "outbound-credentials.v1";
  bindings: Array<{
    connectionId: string;
    credentialType: "ACCESS_TOKEN";
    /** Write-only business access token; must not be logged. */
    value: string;
    expiresAt: string;
  }>;
}

export interface CreateRunRequest {
  conversationId?: string;
  input: CreateRunInputMessage[];
  stream: boolean;
  metadata?: Record<string, string>;
  /** Optional write-only passthrough credentials for REQUEST_PASSTHROUGH connections. */
  outboundCredentials?: OutboundCredentialsEnvelope;
}

export interface APIRun {
  object: "run";
  id: string;
  conversationId: string;
  agentId: string;
  status: RunStatus;
  version: number;
  startedAt: string;
  completedAt?: string;
  error?: ProtocolErrorValue;
  items: ProtocolItem[];
  links: { events: string; [key: string]: string };
}

export interface RunResponse {
  run: APIRun;
  idempotent?: boolean;
}

export interface InteractionDecisionResponse {
  interaction: ProtocolInteraction;
  idempotent: boolean;
  links: Record<string, string>;
}

export type InteractionDecision = "approve" | "decline" | "cancel";

export interface APIErrorBody {
  error: ProtocolErrorValue;
}

export function isTerminalRunStatus(status: string): boolean {
  return status === "completed" || status === "failed" || status === "cancelled";
}

export function deepCloneJSON<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
