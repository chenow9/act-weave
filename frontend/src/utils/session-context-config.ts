import type { ModelRuntimeCapabilities, SessionContextPolicy } from "../types/domain";

/** Product defaults when Agent mode is rolling_summary (matches build payload fallbacks). */
export const DEFAULT_ROLLING_SUMMARY = {
  maxTokens: 2048,
  minEvictedTurns: 4,
  maxGenerationPasses: 2,
} as const;

/** Default recent-turn floor for rolling_summary so summary has material to cover. */
export const DEFAULT_ROLLING_SUMMARY_MAX_RECENT_TURNS = 20;

export function defaultRollingSummary(
  existing?: SessionContextPolicy["summary"] | null,
): NonNullable<SessionContextPolicy["summary"]> {
  return {
    maxTokens:
      existing?.maxTokens != null && Number(existing.maxTokens) > 0
        ? Math.floor(Number(existing.maxTokens))
        : DEFAULT_ROLLING_SUMMARY.maxTokens,
    minEvictedTurns:
      existing?.minEvictedTurns != null && Number(existing.minEvictedTurns) >= 0
        ? Math.floor(Number(existing.minEvictedTurns))
        : DEFAULT_ROLLING_SUMMARY.minEvictedTurns,
    maxGenerationPasses:
      existing?.maxGenerationPasses != null && Number(existing.maxGenerationPasses) > 0
        ? Math.floor(Number(existing.maxGenerationPasses))
        : DEFAULT_ROLLING_SUMMARY.maxGenerationPasses,
  };
}

/** Build model-runtime.v1 payload for API, or undefined when user left capabilities unset. */
export function buildRuntimeCapabilitiesPayload(
  caps: ModelRuntimeCapabilities | Record<string, unknown> | undefined | null,
): ModelRuntimeCapabilities | undefined {
  if (!caps || typeof caps !== "object") return undefined;
  const windowTokens = Number((caps as ModelRuntimeCapabilities).contextWindowTokens);
  if (!Number.isFinite(windowTokens) || windowTokens <= 0) return undefined;
  const reserve = Number((caps as ModelRuntimeCapabilities).defaultOutputReserveTokens);
  const profile = String((caps as ModelRuntimeCapabilities).tokenizerProfile || "o200k_base").trim();
  const version = String((caps as ModelRuntimeCapabilities).tokenizerVersion || "2026-01").trim();
  const limitMode = String((caps as ModelRuntimeCapabilities).outputTokenLimitMode || "max_tokens").trim();
  return {
    schemaVersion: "model-runtime.v1",
    contextWindowTokens: Math.floor(windowTokens),
    defaultOutputReserveTokens:
      Number.isFinite(reserve) && reserve > 0 ? Math.floor(reserve) : 4096,
    outputTokenLimitMode:
      limitMode === "max_completion_tokens" ? "max_completion_tokens" : "max_tokens",
    tokenizerProfile: profile || "o200k_base",
    tokenizerVersion: version || "2026-01",
  };
}

/** Normalize API capabilities for form binding. */
export function normalizeRuntimeCapabilities(
  caps: ModelRuntimeCapabilities | Record<string, unknown> | undefined | null,
): ModelRuntimeCapabilities {
  if (!caps || typeof caps !== "object") {
    return {
      schemaVersion: "model-runtime.v1",
      contextWindowTokens: undefined,
      defaultOutputReserveTokens: 4096,
      outputTokenLimitMode: "max_tokens",
      tokenizerProfile: "o200k_base",
      tokenizerVersion: "2026-01",
    };
  }
  const c = caps as ModelRuntimeCapabilities;
  return {
    schemaVersion: "model-runtime.v1",
    contextWindowTokens: c.contextWindowTokens ? Number(c.contextWindowTokens) : undefined,
    defaultOutputReserveTokens: c.defaultOutputReserveTokens
      ? Number(c.defaultOutputReserveTokens)
      : 4096,
    outputTokenLimitMode:
      c.outputTokenLimitMode === "max_completion_tokens"
        ? "max_completion_tokens"
        : "max_tokens",
    tokenizerProfile: String(c.tokenizerProfile || "o200k_base"),
    tokenizerVersion: String(c.tokenizerVersion || "2026-01"),
  };
}

/** Build session-context-policy.v1 for API. Empty / inherit → {}. */
export function buildContextPolicyPayload(
  policy: SessionContextPolicy | Record<string, unknown> | undefined | null,
): SessionContextPolicy | Record<string, never> {
  if (!policy || typeof policy !== "object") return {};
  const p = policy as SessionContextPolicy;
  const mode = String(p.mode || "").trim();
  if (!mode || mode === "inherit" || mode === "disabled") {
    // disabled still needs schema for explicit product choice
    if (mode === "disabled") {
      return { schemaVersion: "session-context-policy.v1", mode: "disabled" };
    }
    return {};
  }
  if (mode !== "token_window" && mode !== "rolling_summary") return {};
  const out: SessionContextPolicy = {
    schemaVersion: "session-context-policy.v1",
    mode,
  };
  if (p.maxInputTokens != null && Number(p.maxInputTokens) >= 0) {
    out.maxInputTokens = Math.floor(Number(p.maxInputTokens));
  }
  if (p.outputReserveTokens != null && Number(p.outputReserveTokens) > 0) {
    out.outputReserveTokens = Math.floor(Number(p.outputReserveTokens));
  }
  if (p.safetyMarginTokens != null && Number(p.safetyMarginTokens) >= 0) {
    out.safetyMarginTokens = Math.floor(Number(p.safetyMarginTokens));
  }
  if (p.maxRecentTurns != null && Number(p.maxRecentTurns) >= 0) {
    out.maxRecentTurns = Math.floor(Number(p.maxRecentTurns));
  }
  if (mode === "rolling_summary") {
    out.summary = defaultRollingSummary(p.summary);
    // Mode switch fills a default; if still unset here, apply product default once.
    if (out.maxRecentTurns == null) {
      out.maxRecentTurns = DEFAULT_ROLLING_SUMMARY_MAX_RECENT_TURNS;
    }
  }
  return out;
}

export function normalizeContextPolicy(
  policy: SessionContextPolicy | Record<string, unknown> | undefined | null,
): SessionContextPolicy {
  if (!policy || typeof policy !== "object") {
    return { mode: undefined };
  }
  const p = policy as SessionContextPolicy;
  const mode = p.mode;
  if (mode === "token_window" || mode === "rolling_summary" || mode === "disabled") {
    return {
      schemaVersion: "session-context-policy.v1",
      mode,
      maxInputTokens: p.maxInputTokens,
      outputReserveTokens: p.outputReserveTokens,
      safetyMarginTokens: p.safetyMarginTokens,
      maxRecentTurns: p.maxRecentTurns,
      summary: mode === "rolling_summary" ? defaultRollingSummary(p.summary) : p.summary,
    };
  }
  return { mode: undefined };
}
