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

// --- Message content parts (assistant / protocol snapshots) -----------------

/** Text content part (always present on assistant messages; may be ""). */
export interface TextContentPart {
  type: "text";
  text: string;
  [key: string]: unknown;
}

/**
 * First-class A2UI surface part (assistant outbound only).
 * Attached on item.completed when enableA2UI; not streamed via text_delta.
 */
export interface A2UIContentPart {
  type: "a2ui";
  /** Declarative A2UI surface object (opaque to the SDK beyond type/size). */
  surface: Record<string, unknown>;
  version?: string;
  catalogId?: string;
  [key: string]: unknown;
}

/** File reference part (user createRun / inbound; may appear in snapshots). */
export interface InputFileContentPart {
  type: "input_file";
  fileId: string;
  mediaType?: string;
  [key: string]: unknown;
}

/**
 * Assistant outbound file part. Wire only stable `fileId` + display metadata —
 * never embed live download/presign URLs.
 */
export interface OutputFileContentPart {
  type: "output_file";
  fileId: string;
  mediaType?: string;
  filename?: string;
  sizeBytes?: number;
  [key: string]: unknown;
}

/** Known message content part arms + additive unknown parts. */
export type MessageContentPart =
  | TextContentPart
  | A2UIContentPart
  | InputFileContentPart
  | OutputFileContentPart
  | { type: string; [key: string]: unknown };

export function isTextContentPart(part: unknown): part is TextContentPart {
  return (
    isPlainObject(part) &&
    part.type === "text" &&
    typeof part.text === "string"
  );
}

export function isA2UIContentPart(part: unknown): part is A2UIContentPart {
  return (
    isPlainObject(part) &&
    part.type === "a2ui" &&
    isPlainObject(part.surface)
  );
}

export function isInputFileContentPart(part: unknown): part is InputFileContentPart {
  return (
    isPlainObject(part) &&
    part.type === "input_file" &&
    typeof part.fileId === "string"
  );
}

export function isOutputFileContentPart(part: unknown): part is OutputFileContentPart {
  return (
    isPlainObject(part) &&
    part.type === "output_file" &&
    typeof part.fileId === "string"
  );
}

/**
 * Resolve a message content array from either a content value or a ProtocolItem.
 */
function resolveMessageContent(contentOrItem: unknown): unknown[] {
  if (Array.isArray(contentOrItem)) {
    return contentOrItem;
  }
  if (isPlainObject(contentOrItem) && Array.isArray(contentOrItem.content)) {
    return contentOrItem.content;
  }
  return [];
}

/**
 * Join all `type: "text"` parts in order. Ignores a2ui / input_file / output_file / unknown parts.
 * Accepts a content array or a ProtocolItem with `content`.
 */
export function joinTextParts(contentOrItem: unknown): string {
  const parts = resolveMessageContent(contentOrItem);
  let out = "";
  for (const part of parts) {
    if (isTextContentPart(part)) {
      out += part.text;
    }
  }
  return out;
}

/**
 * Return the first `type: "a2ui"` part, if any. MVP allows at most one a2ui part.
 * Accepts a content array or a ProtocolItem with `content`.
 */
export function findA2UIPart(contentOrItem: unknown): A2UIContentPart | undefined {
  const parts = resolveMessageContent(contentOrItem);
  for (const part of parts) {
    if (isA2UIContentPart(part)) {
      return part;
    }
  }
  return undefined;
}

/**
 * Return `type: "output_file"` parts in order. Accepts a content array or a ProtocolItem.
 */
export function findOutputFileParts(contentOrItem: unknown): OutputFileContentPart[] {
  const parts = resolveMessageContent(contentOrItem);
  const files: OutputFileContentPart[] = [];
  for (const part of parts) {
    if (isOutputFileContentPart(part)) {
      files.push(part);
    }
  }
  return files;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
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

/** Text content part (always supported). */
export interface CreateRunTextPart {
  type: "text";
  text: string;
}

/**
 * File content part. Requires AAP files feature + RuntimeMultimodal for model E2E.
 * Wire only stable `fileId` — never embed live download/presign URLs.
 */
export interface CreateRunInputFilePart {
  type: "input_file";
  fileId: string;
  /** Optional declared media type; server may ignore or validate against file. */
  mediaType?: string;
}

export type CreateRunContentPart = CreateRunTextPart | CreateRunInputFilePart;

export interface CreateRunInputMessage {
  type: "message";
  role: "user";
  content: CreateRunContentPart[];
}

// --- AAP File resources (agent-access-v1 OpenAPI) -------------------------

/** File lifecycle status (GET is source of truth; no File SSE in v1). */
export type AAPFileStatus =
  | "pending_upload"
  | "uploaded"
  | "processing"
  | "ready"
  | "failed"
  | "expired"
  | string;

export type AAPFilePurpose = "GENERAL" | "VISION" | "DOCUMENT" | "TOOL_INPUT" | string;

export type AAPFileMediaType =
  | "image/png"
  | "image/jpeg"
  | "image/webp"
  | "image/gif"
  | "application/pdf"
  | string;

export interface AAPFileProcessingStage {
  stage: string;
  status: string;
}

export interface AAPFileProcessing {
  version: number;
  stages: AAPFileProcessingStage[];
}

export interface AAPFileLinks {
  /** Relative Bearer content path — not a live token URL. */
  content: string;
  [key: string]: string;
}

/** Public File resource. Must never include upload / presign / downloadUrl. */
export interface AAPFile {
  object: "file";
  id: string;
  agentId: string;
  status: AAPFileStatus;
  filename?: string;
  mediaType: string;
  detectedMediaType?: string;
  sizeBytes: number;
  sha256?: string;
  purpose: string;
  error?: ProtocolErrorValue;
  processing: AAPFileProcessing;
  artifacts: unknown[];
  links: AAPFileLinks;
  createdAt: string;
  updatedAt: string;
  readyAt?: string;
}

/** Write-only create fragment; never appears on subsequent GET. */
export interface FileUpload {
  method: "PUT" | string;
  url: string;
  /**
   * Headers required for the signed PUT. Must include Content-Type and
   * Content-Length exactly as returned by createFile.
   */
  headers: Record<string, string>;
  expiresAt: string;
}

export interface CreateFileRequest {
  filename?: string;
  mediaType: AAPFileMediaType;
  sizeBytes: number;
  sha256?: string;
  purpose?: AAPFilePurpose;
}

export interface CreateFileResponse {
  file: AAPFile;
  /** Present on create intent; omitted on idempotent replay when upload expired. */
  upload?: FileUpload;
  idempotent: boolean;
}

export interface CompleteFileRequest {
  sha256?: string;
}

export interface CompleteFileResponse {
  file: AAPFile;
  idempotent: boolean;
}

export interface GetFileResponse {
  file: AAPFile;
}

export interface MintFileDownloadResponse {
  /** Opaque download token id (not a MinIO key / JWT). */
  token: string;
  expiresAt: string;
  /** Relative proxy path, e.g. /api/agent-access/v1/files/downloads/{tokenId}. */
  url: string;
}

export interface FileContentResult {
  body: ArrayBuffer;
  contentType: string;
  /** Path used: Bearer content vs opaque download token. */
  via: "content" | "download";
}

/** Soft threshold (4 MiB) above which getFileContent prefers :download path B. */
export const SDK_PREFER_DOWNLOAD_TOKEN_BYTES = 4 * 1024 * 1024;

export function isTerminalFileStatus(status: string): boolean {
  return status === "ready" || status === "failed" || status === "expired";
}

export function isReadyFileStatus(status: string): boolean {
  return status === "ready";
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
