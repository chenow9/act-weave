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

/** Platform-frozen compact waterlines (read-only in UI). */
export const COMPACTION_TRIGGER_BPS = 8000;
export const COMPACTION_TARGET_BPS = 6000;

/** Permanent-disclosure warning for Agent setting (T4-B). */
export const COMPACTION_SUMMARY_PERMANENCE_WARNING =
  "开启后，成功 Compact 的摘要正文将永久以 PostgreSQL 明文协议投影及备份保留；关闭只影响后续新 run，不会删除既有 run 正文。";

/** Build session-context-policy.v1/v2 for API. Empty / inherit → {}. */
export function buildContextPolicyPayload(
  policy: SessionContextPolicy | Record<string, unknown> | undefined | null,
): SessionContextPolicy | Record<string, never> {
  if (!policy || typeof policy !== "object") return {};
  const p = policy as SessionContextPolicy;
  const mode = String(p.mode || "").trim();
  const includeSummary = Boolean(p.aap?.includeCompactionSummary);
  if (!mode || mode === "inherit" || mode === "disabled") {
    // disabled still needs schema for explicit product choice
    if (mode === "disabled") {
      if (includeSummary) {
        return {
          schemaVersion: "session-context-policy.v2",
          mode: "disabled",
          aap: { includeCompactionSummary: true },
        };
      }
      return { schemaVersion: "session-context-policy.v1", mode: "disabled" };
    }
    if (includeSummary) {
      return {
        schemaVersion: "session-context-policy.v2",
        aap: { includeCompactionSummary: true },
      };
    }
    return {};
  }
  if (mode !== "token_window" && mode !== "rolling_summary") return {};
  const useV2 = includeSummary || p.schemaVersion === "session-context-policy.v2";
  const out: SessionContextPolicy = {
    schemaVersion: useV2 ? "session-context-policy.v2" : "session-context-policy.v1",
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
  // Agent-only disclosure; default omit/false — never upgrade v1 save without explicit aap.
  if (includeSummary) {
    out.aap = { includeCompactionSummary: true };
  } else if (p.schemaVersion === "session-context-policy.v2" && p.aap) {
    out.aap = { includeCompactionSummary: false };
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
  const include = Boolean(p.aap?.includeCompactionSummary);
  const schemaVersion: SessionContextPolicy["schemaVersion"] =
    include || p.schemaVersion === "session-context-policy.v2"
      ? "session-context-policy.v2"
      : "session-context-policy.v1";
  if (mode === "token_window" || mode === "rolling_summary" || mode === "disabled") {
    const out: SessionContextPolicy = {
      schemaVersion,
      mode,
      maxInputTokens: p.maxInputTokens,
      outputReserveTokens: p.outputReserveTokens,
      safetyMarginTokens: p.safetyMarginTokens,
      maxRecentTurns: p.maxRecentTurns,
      summary: mode === "rolling_summary" ? defaultRollingSummary(p.summary) : p.summary,
    };
    if (schemaVersion === "session-context-policy.v2") {
      out.aap = { includeCompactionSummary: include };
    }
    return out;
  }
  if (include) {
    return {
      schemaVersion: "session-context-policy.v2",
      mode: undefined,
      aap: { includeCompactionSummary: true },
    };
  }
  return { mode: undefined };
}
