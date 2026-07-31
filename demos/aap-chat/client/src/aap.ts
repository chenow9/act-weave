import {
  AgentAccessClient,
  MemoryTokenProvider,
  type AccessTokenMaterial,
  type ProtocolItem,
  type ReducedRunSnapshot,
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
): Promise<ChatTurnResult> {
  const res = await fetch("/bff/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ text, conversationId }),
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = payload?.error?.message || JSON.stringify(payload);
    throw new Error(msg || `chat HTTP ${res.status}`);
  }
  return payload as ChatTurnResult;
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
  const content = (item as { content?: unknown }).content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  const parts: string[] = [];
  for (const block of content) {
    if (!block || typeof block !== "object") continue;
    const rec = block as Record<string, unknown>;
    if (rec.type === "text" && typeof rec.text === "string") {
      parts.push(rec.text);
    } else if (typeof rec.text === "string") {
      parts.push(rec.text);
    }
  }
  return parts.join("");
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
