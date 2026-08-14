import {
  AgentAccessClient,
  MemoryTokenProvider,
  findA2UIPart,
  findOutputFileParts,
  isInputFileContentPart,
  joinTextParts,
  type AccessTokenMaterial,
  type AAPFile,
  type APIRun,
  type Conversation,
  type InputFileContentPart,
  type OutputFileContentPart,
  type ProtocolItem,
  type ReducedRunSnapshot,
  type RunResponse,
} from "@actweave/agent-client";

export interface OutboundStatus {
  required: boolean;
  connectionId: string | null;
  bound: boolean;
  expiresAt: string | null;
}

export interface BffConfig {
  workspaceId: string;
  agentId: string;
  aapBaseUrl: string;
  aapConfigured: boolean;
  outbound?: OutboundStatus;
}

export interface ChatTurnResult {
  conversationId: string;
  runId: string;
  accessToken: string;
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  /** OAuth expires_in seconds when BFF reports it (optional). */
  expiresIn?: number;
}

export async function fetchBffConfig(): Promise<BffConfig> {
  const res = await fetch("/bff/config");
  if (!res.ok) {
    throw new Error(`BFF config HTTP ${res.status}`);
  }
  return (await res.json()) as BffConfig;
}

/** Attach a write-only business ACCESS_TOKEN on the BFF (never stored in the browser after send). */
export async function attachOutboundCredential(input: {
  value: string;
  expiresAt?: string;
}): Promise<OutboundStatus> {
  const res = await fetch("/bff/outbound-credentials", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ value: input.value, expiresAt: input.expiresAt }),
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = payload?.error?.message || JSON.stringify(payload);
    throw new Error(msg || `outbound attach HTTP ${res.status}`);
  }
  return payload.outbound as OutboundStatus;
}

export async function clearOutboundCredential(): Promise<OutboundStatus> {
  const res = await fetch("/bff/outbound-credentials", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ clear: true }),
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = payload?.error?.message || JSON.stringify(payload);
    throw new Error(msg || `outbound clear HTTP ${res.status}`);
  }
  return payload.outbound as OutboundStatus;
}

/**
 * Mint (or re-mint) a short-lived AAP Access Token via the BFF.
 * Client Secret stays on the server; browser only holds the short token in memory.
 */
export async function mintAapAccessToken(): Promise<AccessTokenMaterial> {
  const res = await fetch("/bff/token", {
    method: "POST",
    headers: { Accept: "application/json" },
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = payload?.error?.message || JSON.stringify(payload);
    throw new Error(msg || `BFF token HTTP ${res.status}`);
  }
  const accessToken = String(payload.accessToken || "").trim();
  if (!accessToken) {
    throw new Error("BFF token response missing accessToken");
  }
  const material: AccessTokenMaterial = { accessToken };
  if (typeof payload.expiresIn === "number" && payload.expiresIn > 0) {
    material.expiresIn = payload.expiresIn;
  }
  return material;
}

/**
 * Token provider that can force-refresh on SSE 401 / reconnect.
 * Optional seed avoids an extra /bff/token round-trip right after createRun.
 */
export function createBffTokenProvider(seed?: AccessTokenMaterial): MemoryTokenProvider {
  let seeded = seed?.accessToken ? seed : null;
  return new MemoryTokenProvider({
    refresh: async () => {
      if (seeded?.accessToken) {
        const material = seeded;
        seeded = null;
        return material;
      }
      return mintAapAccessToken();
    },
  });
}

export async function startChatTurn(
  text: string,
  conversationId?: string,
  fileIds?: string[],
): Promise<ChatTurnResult> {
  const res = await fetch("/bff/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({
      text,
      conversationId,
      ...(fileIds && fileIds.length ? { fileIds } : {}),
    }),
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = payload?.error?.message || JSON.stringify(payload);
    throw new Error(msg || `chat HTTP ${res.status}`);
  }
  return payload as ChatTurnResult;
}

export interface UploadAttachmentInput {
  file: File;
  workspaceId: string;
  agentId: string;
  aapBaseUrl: string;
  /** Optional progress callback (0–1). */
  onProgress?: (phase: "intent" | "put" | "complete" | "ready", ratio: number) => void;
  signal?: AbortSignal;
}

/**
 * Browser-side AAP file lifecycle using a BFF-minted short access token.
 * Requires Grant scopes file:write + file:read and agentAccess.files enabled.
 */
export async function uploadAttachment(input: UploadAttachmentInput): Promise<AAPFile> {
  const { file, workspaceId, agentId, aapBaseUrl, onProgress, signal } = input;
  const client = new AgentAccessClient({
    baseUrl: aapBaseUrl,
    tokenProvider: createBffTokenProvider(),
  });
  const idempotencyKey =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `demo-${Date.now()}-${Math.random().toString(16).slice(2)}`;

  onProgress?.("intent", 0.05);
  const created = await client.createFile(
    workspaceId,
    agentId,
    {
      filename: file.name || "attachment",
      mediaType: file.type || "application/octet-stream",
      sizeBytes: file.size,
    },
    { idempotencyKey, signal },
  );
  const upload = created.upload;
  const aapFile = created.file;
  if (!upload?.url || !aapFile?.id) {
    throw new Error("createFile missing upload URL or file id");
  }

  onProgress?.("put", 0.2);
  const bytes = await file.arrayBuffer();
  await client.putFileUpload(upload, bytes, { signal });

  onProgress?.("complete", 0.7);
  await client.completeFile(workspaceId, agentId, aapFile.id, {}, {
    idempotencyKey:
      typeof crypto !== "undefined" && "randomUUID" in crypto
        ? crypto.randomUUID()
        : `demo-complete-${Date.now()}`,
    signal,
  });

  onProgress?.("ready", 0.85);
  const ready = await client.waitUntilReady(workspaceId, agentId, aapFile.id, {
    signal,
    timeoutMs: 120_000,
    pollIntervalMs: 500,
  });
  onProgress?.("ready", 1);
  return ready;
}

export type AttachmentCardStatus = "pending" | "uploading" | "ready" | "error";

/** Composer / bubble attachment card. Wire never stores live URLs — only Object URLs. */
export interface AttachmentCard {
  id: string;
  localId: string;
  name: string;
  mediaType: string;
  sizeBytes: number;
  fileId?: string;
  previewUrl?: string;
  status: AttachmentCardStatus;
  error?: string;
}

export interface HydratedAttachment {
  name: string;
  mediaType: string;
  sizeBytes: number;
  previewUrl?: string;
}

/**
 * Assistant `output_file` parts from a run snapshot: assistant messages only,
 * first-seen `fileId` order, duplicates dropped.
 */
export function extractOutputFileParts(items: ProtocolItem[]): OutputFileContentPart[] {
  const seen = new Set<string>();
  const out: OutputFileContentPart[] = [];
  for (const item of items) {
    if (itemRole(item) !== "assistant") continue;
    for (const part of findOutputFileParts(item)) {
      const fileId = String(part.fileId || "").trim();
      if (!fileId || seen.has(fileId)) continue;
      seen.add(fileId);
      out.push(part);
    }
  }
  return out;
}

/**
 * User `input_file` parts from a run snapshot: user messages only,
 * first-seen `fileId` order, duplicates dropped.
 */
export function extractInputFileParts(items: ProtocolItem[]): InputFileContentPart[] {
  const seen = new Set<string>();
  const out: InputFileContentPart[] = [];
  for (const item of items) {
    for (const part of extractInputFilePartsFromItem(item)) {
      if (seen.has(part.fileId)) continue;
      seen.add(part.fileId);
      out.push(part);
    }
  }
  return out;
}

export function extractInputFilePartsFromItem(item: ProtocolItem): InputFileContentPart[] {
  if (itemRole(item) !== "user") return [];
  const rawContent = (item as { content?: unknown }).content;
  const content = Array.isArray(rawContent) ? rawContent : [];
  const seen = new Set<string>();
  const out: InputFileContentPart[] = [];
  for (const part of content) {
    if (!isInputFileContentPart(part)) continue;
    const fileId = String(part.fileId || "").trim();
    if (!fileId || seen.has(fileId)) continue;
    seen.add(fileId);
    out.push(part);
  }
  return out;
}

/** Map a wire file part onto the hydrate/reconcile shape (filename/size optional on input_file). */
export function filePartAsOutputRef(part: {
  fileId: string;
  filename?: unknown;
  mediaType?: unknown;
  sizeBytes?: unknown;
}): OutputFileContentPart {
  const filename = typeof part.filename === "string" && part.filename.trim() ? part.filename.trim() : undefined;
  const mediaType =
    typeof part.mediaType === "string" && part.mediaType.trim() ? part.mediaType.trim() : undefined;
  const sizeBytes =
    typeof part.sizeBytes === "number" && Number.isFinite(part.sizeBytes) && part.sizeBytes >= 0
      ? part.sizeBytes
      : undefined;
  return {
    type: "output_file",
    fileId: String(part.fileId || "").trim(),
    ...(filename ? { filename } : {}),
    ...(mediaType ? { mediaType } : {}),
    ...(sizeBytes !== undefined ? { sizeBytes } : {}),
  };
}

/**
 * Reconcile bubble cards by fileId. Never reset ready/error/uploading rows
 * (or their previewUrl) just because another SSE snapshot arrived.
 */
export function reconcileAssistantAttachments(
  prev: AttachmentCard[] | undefined,
  parts: OutputFileContentPart[],
): { next: AttachmentCard[]; toHydrate: OutputFileContentPart[] } {
  const prevById = new Map<string, AttachmentCard>();
  for (const card of prev || []) {
    if (card.fileId) prevById.set(card.fileId, card);
  }
  const next: AttachmentCard[] = [];
  const toHydrate: OutputFileContentPart[] = [];
  const keep = new Set<string>();

  for (const part of parts) {
    const fileId = String(part.fileId || "").trim();
    if (!fileId) continue;
    keep.add(fileId);
    const existing = prevById.get(fileId);
    if (
      existing &&
      (existing.status === "ready" || existing.status === "error" || existing.status === "uploading")
    ) {
      next.push(existing);
      continue;
    }
    next.push(placeholderAttachment(part));
    toHydrate.push(part);
  }

  for (const [fileId, card] of prevById) {
    if (keep.has(fileId)) continue;
    if (card.previewUrl) URL.revokeObjectURL(card.previewUrl);
  }

  return { next, toHydrate };
}

export function placeholderAttachment(part: OutputFileContentPart): AttachmentCard {
  const fileId = String(part.fileId || "").trim();
  const size =
    typeof part.sizeBytes === "number" && Number.isFinite(part.sizeBytes) && part.sizeBytes >= 0
      ? part.sizeBytes
      : 0;
  return {
    id: fileId,
    localId: fileId,
    name: (part.filename && String(part.filename).trim()) || "attachment",
    mediaType: (part.mediaType && String(part.mediaType).trim()) || "application/octet-stream",
    sizeBytes: size,
    fileId,
    status: "uploading",
  };
}

/** Metadata via getFile; image preview via Bearer getFileContent — never links.content. */
export async function hydrateAttachment(options: {
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  fileId: string;
  mediaType?: string;
  filename?: string;
  sizeBytes?: number;
}): Promise<HydratedAttachment> {
  const client = new AgentAccessClient({
    baseUrl: options.aapBaseUrl,
    tokenProvider: createBffTokenProvider(),
  });
  const meta = await client.getFile(options.workspaceId, options.agentId, options.fileId);
  const name = meta.filename || options.filename || "attachment";
  const mediaType = meta.mediaType || options.mediaType || "application/octet-stream";
  const sizeBytes =
    typeof meta.sizeBytes === "number" && Number.isFinite(meta.sizeBytes)
      ? meta.sizeBytes
      : (options.sizeBytes ?? 0);

  let previewUrl: string | undefined;
  if (mediaType.startsWith("image/")) {
    previewUrl =
      (await fetchFileObjectUrl({
        aapBaseUrl: options.aapBaseUrl,
        workspaceId: options.workspaceId,
        agentId: options.agentId,
        fileId: options.fileId,
        mediaType,
      })) ?? undefined;
  }
  return { name, mediaType, sizeBytes, previewUrl };
}

/** Best-effort blob URL for READY image preview (Bearer content stream). */
export async function fetchFileObjectUrl(options: {
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  fileId: string;
  mediaType?: string;
}): Promise<string | null> {
  try {
    const blob = await fetchFileBlob({ ...options, prefer: "content" });
    return URL.createObjectURL(blob);
  } catch {
    return null;
  }
}

/** Bearer getFileContent (path A/B) as a Blob. Never use links.content as src. */
export async function fetchFileBlob(options: {
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  fileId: string;
  mediaType?: string;
  prefer?: "content" | "download";
}): Promise<Blob> {
  const client = new AgentAccessClient({
    baseUrl: options.aapBaseUrl,
    tokenProvider: createBffTokenProvider(),
  });
  const result = await client.getFileContent(
    options.workspaceId,
    options.agentId,
    options.fileId,
    options.prefer ? { prefer: options.prefer } : {},
  );
  const mime = result.contentType || options.mediaType || "application/octet-stream";
  return new Blob([result.body], { type: mime });
}

export async function* followRunLive(options: {
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  runId: string;
  /** Optional seed from /bff/chat (avoids immediate re-mint). Force-refresh uses /bff/token. */
  accessToken?: string;
  expiresIn?: number;
  signal?: AbortSignal;
  /** SSE catch-up cursor; 0 replays the full durable event log. */
  initialLastSequence?: number;
}): AsyncGenerator<ReducedRunSnapshot> {
  const seed =
    options.accessToken && options.accessToken.trim()
      ? {
          accessToken: options.accessToken.trim(),
          ...(typeof options.expiresIn === "number" && options.expiresIn > 0
            ? { expiresIn: options.expiresIn }
            : {}),
        }
      : undefined;
  const client = new AgentAccessClient({
    baseUrl: options.aapBaseUrl,
    tokenProvider: createBffTokenProvider(seed),
  });
  for await (const { snapshot } of client.followRun(options.workspaceId, options.agentId, options.runId, {
    signal: options.signal,
    // Default true: on 401 / TOKEN_EXPIRED, MemoryTokenProvider re-mints via BFF.
    refreshOnAuthFailure: true,
    ...(typeof options.initialLastSequence === "number"
      ? { initialLastSequence: options.initialLastSequence }
      : {}),
  })) {
    yield snapshot;
    const status = snapshot.run?.status ? String(snapshot.run.status) : "";
    if (status && ["completed", "failed", "cancelled"].includes(status)) {
      return;
    }
  }
}

export function itemRole(item: ProtocolItem): "user" | "assistant" | "tool" | "other" {
  if (item.type === "message") {
    const role = String((item as { role?: string }).role || "").toLowerCase();
    if (role === "user") return "user";
    return "assistant";
  }
  if (item.type === "tool_call" || item.type === "workflow_step") return "tool";
  return "other";
}

export function extractMessageText(item: ProtocolItem): string {
  if (item.type !== "message") return "";
  // Prefer SDK helper (ignores a2ui / unknown parts). Fallback for plain string content.
  const joined = joinTextParts(item);
  if (joined) return joined;
  const content = (item as { content?: unknown }).content;
  if (typeof content === "string") return content;
  return "";
}

export interface A2UIPartExtract {
  version?: string;
  catalogId?: string;
  surface: unknown;
  /** Pretty JSON of the a2ui part (for debug / raw panel). */
  rawJson: string;
}

/**
 * Extract first a2ui part for real surface rendering (display-only; no actions).
 * Returns null when the item has no a2ui part.
 */
export function extractA2UIPart(item: ProtocolItem): A2UIPartExtract | null {
  if (item.type !== "message") return null;
  const part = findA2UIPart(item);
  if (!part) return null;
  try {
    const version = part.version ? String(part.version) : undefined;
    const catalogId = part.catalogId ? String(part.catalogId) : undefined;
    const surface = part.surface ?? null;
    const rawJson = JSON.stringify(
      {
        type: part.type,
        ...(version ? { version } : {}),
        ...(catalogId ? { catalogId } : {}),
        surface,
      },
      null,
      2,
    );
    return { version, catalogId, surface, rawJson };
  } catch {
    return null;
  }
}

/**
 * @deprecated Prefer extractA2UIPart + a2ui-render for real UI.
 * Pretty-print first a2ui part surface (legacy JSON preview).
 */
export function extractA2UIPreview(item: ProtocolItem): string | null {
  const part = extractA2UIPart(item);
  return part?.rawJson ?? null;
}

export function extractToolSummary(item: ProtocolItem): {
  name: string;
  status: string;
  detail: string;
} {
  const name =
    String((item as { name?: string }).name || (item as { toolName?: string }).toolName || item.type || "tool");
  const status = String(item.status || "unknown");
  const args =
    (item as { arguments?: unknown }).arguments ??
    (item as { argumentsJson?: unknown }).argumentsJson ??
    (item as { input?: unknown }).input;
  const output =
    (item as { output?: unknown }).output ??
    (item as { result?: unknown }).result ??
    (item as { error?: unknown }).error;
  let detail = "";
  try {
    detail = JSON.stringify(
      {
        ...(args !== undefined ? { arguments: args } : {}),
        ...(output !== undefined ? { output } : {}),
      },
      null,
      2,
    );
  } catch {
    detail = String(args ?? output ?? "");
  }
  return { name, status, detail };
}

// --- Conversation replay (PR-4b): sessionStorage + getConversation/getRun ---

/** Namespaced by workspace+agent so a BFF target switch does not replay the wrong conversation. */
export function conversationStorageKey(workspaceId: string, agentId: string): string {
  return `aap-chat:${workspaceId}:${agentId}:conversationId`;
}

function sessionStore(): Pick<Storage, "getItem" | "setItem" | "removeItem"> | null {
  try {
    if (typeof sessionStorage === "undefined") return null;
    return sessionStorage;
  } catch {
    return null;
  }
}

export function readStoredConversationId(workspaceId: string, agentId: string): string {
  const ws = workspaceId.trim();
  const agent = agentId.trim();
  if (!ws || !agent) return "";
  try {
    return String(sessionStore()?.getItem(conversationStorageKey(ws, agent)) || "").trim();
  } catch {
    return "";
  }
}

export function writeStoredConversationId(
  workspaceId: string,
  agentId: string,
  conversationId: string,
): void {
  const ws = workspaceId.trim();
  const agent = agentId.trim();
  if (!ws || !agent) return;
  const key = conversationStorageKey(ws, agent);
  const id = conversationId.trim();
  try {
    const store = sessionStore();
    if (!store) return;
    if (!id) store.removeItem(key);
    else store.setItem(key, id);
  } catch {
    // Private mode / blocked storage must not break Live send.
  }
}

export function clearStoredConversationId(workspaceId: string, agentId: string): void {
  writeStoredConversationId(workspaceId, agentId, "");
}

export function httpStatusOf(err: unknown): number {
  if (!err || typeof err !== "object") return 0;
  const status = (err as { status?: unknown }).status;
  return typeof status === "number" && Number.isFinite(status) ? status : 0;
}

/** 4xx that means the stored id is gone or invalid — drop the key so boot does not retry. */
export function shouldDropStoredConversation(err: unknown): boolean {
  const status = httpStatusOf(err);
  return status === 400 || status === 404 || status === 422;
}

export function unwrapRunResponse(payload: unknown): APIRun | null {
  if (!payload || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  if (rec.run && typeof rec.run === "object") {
    const inner = rec.run as Record<string, unknown>;
    if (typeof inner.id === "string") return rec.run as APIRun;
  }
  if (typeof rec.id === "string" && Array.isArray(rec.items)) {
    return payload as APIRun;
  }
  return null;
}

/** True when GET run has no items or message snapshots were truncated / not projected. */
export function runItemsNeedCatchUp(run: APIRun | null | undefined): boolean {
  if (!run) return true;
  const items = Array.isArray(run.items) ? run.items : [];
  if (!items.length) return true;
  for (const item of items) {
    if (!item || typeof item !== "object") return true;
    if (item.type !== "message") continue;
    if (!Array.isArray((item as { content?: unknown }).content)) return true;
  }
  return false;
}

/** GET conversation lists newest first; replay oldest → newest. */
export function chronologicalRuns(
  conversation: Pick<Conversation, "runs"> | null | undefined,
): Array<{ id: string; startedAt?: string }> {
  const runs = Array.isArray(conversation?.runs) ? conversation.runs.slice() : [];
  runs.reverse();
  const out: Array<{ id: string; startedAt?: string }> = [];
  for (const run of runs) {
    const id = String(run?.id || "").trim();
    if (!id) continue;
    out.push({
      id,
      ...(run.startedAt ? { startedAt: String(run.startedAt) } : {}),
    });
  }
  return out;
}

export interface ReplayMessage {
  id: string;
  role: "user" | "assistant";
  text: string;
  a2ui?: A2UIPartExtract;
  tools?: Array<{ name: string; status: string; detail: string }>;
  files: OutputFileContentPart[];
  createdAt?: string;
}

export function replayMessagesFromItems(items: ProtocolItem[], createdAt?: string): ReplayMessage[] {
  const messages: ReplayMessage[] = [];
  let pendingTools: Array<{ name: string; status: string; detail: string }> = [];

  for (const item of items) {
    if (itemRole(item) === "tool") {
      pendingTools.push(extractToolSummary(item));
      continue;
    }
    if (item.type !== "message") continue;
    const rawRole = String((item as { role?: string }).role || "").toLowerCase();
    if (rawRole !== "user" && rawRole !== "assistant") continue;

    const files =
      rawRole === "user"
        ? extractInputFilePartsFromItem(item).map(filePartAsOutputRef)
        : findOutputFileParts(item);
    const text = extractMessageText(item);
    const a2ui = rawRole === "assistant" ? extractA2UIPart(item) ?? undefined : undefined;
    if (!text.trim() && !files.length && !a2ui) continue;

    const msg: ReplayMessage = {
      id: String(item.id || "").trim(),
      role: rawRole,
      text,
      files,
      ...(a2ui ? { a2ui } : {}),
      ...(createdAt ? { createdAt } : {}),
    };
    if (rawRole === "assistant" && pendingTools.length) {
      msg.tools = pendingTools;
      pendingTools = [];
    }
    messages.push(msg);
  }

  if (pendingTools.length) {
    const lastAssistant = [...messages].reverse().find((m) => m.role === "assistant");
    if (lastAssistant) {
      lastAssistant.tools = [...(lastAssistant.tools || []), ...pendingTools];
    } else {
      messages.push({
        id: "",
        role: "assistant",
        text: "",
        tools: pendingTools,
        files: [],
        ...(createdAt ? { createdAt } : {}),
      });
    }
  }

  return messages;
}

export interface ConversationReplayClient {
  getConversation(
    workspaceId: string,
    agentId: string,
    conversationId: string,
    options?: { signal?: AbortSignal },
  ): Promise<Conversation | null>;
  getRun(
    workspaceId: string,
    agentId: string,
    runId: string,
    options?: { signal?: AbortSignal },
  ): Promise<RunResponse | APIRun | null>;
  followRun(
    workspaceId: string,
    agentId: string,
    runId: string,
    options?: { signal?: AbortSignal; initialLastSequence?: number; refreshOnAuthFailure?: boolean },
  ): AsyncIterable<{ snapshot: ReducedRunSnapshot }>;
}

export interface ConversationReplayResult {
  conversationId: string;
  messages: ReplayMessage[];
}

function liveReplayClient(aapBaseUrl: string): ConversationReplayClient {
  return new AgentAccessClient({
    baseUrl: aapBaseUrl,
    tokenProvider: createBffTokenProvider(),
  });
}

async function catchUpRunItems(
  client: ConversationReplayClient,
  workspaceId: string,
  agentId: string,
  runId: string,
  signal?: AbortSignal,
): Promise<ProtocolItem[] | null> {
  let items: ProtocolItem[] | null = null;
  try {
    for await (const step of client.followRun(workspaceId, agentId, runId, {
      signal,
      initialLastSequence: 0,
      refreshOnAuthFailure: true,
    })) {
      if (Array.isArray(step.snapshot?.items)) items = step.snapshot.items;
    }
  } catch {
    return items;
  }
  return items;
}

async function loadRunItemsForReplay(
  client: ConversationReplayClient,
  workspaceId: string,
  agentId: string,
  runId: string,
  signal?: AbortSignal,
): Promise<ProtocolItem[]> {
  let run: APIRun | null = null;
  try {
    run = unwrapRunResponse(await client.getRun(workspaceId, agentId, runId, { signal }));
  } catch {
    run = null;
  }
  let items = Array.isArray(run?.items) ? run.items : [];
  if (!runItemsNeedCatchUp(run)) return items;
  const caught = await catchUpRunItems(client, workspaceId, agentId, runId, signal);
  return caught ?? items;
}

/**
 * Rebuild transcript + file parts from durable conversation/run reads.
 * Needs conversation:read + run:read. followRun is only a fallback when items
 * are incomplete (event:read is already in default BFF scopes, not required).
 */
export async function restoreConversationReplay(options: {
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  conversationId: string;
  signal?: AbortSignal;
  client?: ConversationReplayClient;
}): Promise<ConversationReplayResult> {
  const conversationId = options.conversationId.trim();
  if (!conversationId) return { conversationId: "", messages: [] };

  const client = options.client ?? liveReplayClient(options.aapBaseUrl);
  const conversation = await client.getConversation(
    options.workspaceId,
    options.agentId,
    conversationId,
    { signal: options.signal },
  );
  if (!conversation?.id) {
    throw new Error("conversation not found");
  }

  const messages: ReplayMessage[] = [];
  for (const run of chronologicalRuns(conversation)) {
    const items = await loadRunItemsForReplay(
      client,
      options.workspaceId,
      options.agentId,
      run.id,
      options.signal,
    );
    messages.push(...replayMessagesFromItems(items, run.startedAt));
  }
  return { conversationId: String(conversation.id).trim() || conversationId, messages };
}
