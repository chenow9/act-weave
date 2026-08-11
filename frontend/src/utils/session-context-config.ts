import { tt } from "../i18n/tt";
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
    defaultOutputReserveTokens: Number.isFinite(reserve) && reserve > 0 ? Math.floor(reserve) : 4096,
    outputTokenLimitMode: limitMode === "max_completion_tokens" ? "max_completion_tokens" : "max_tokens",
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
    defaultOutputReserveTokens: c.defaultOutputReserveTokens ? Number(c.defaultOutputReserveTokens) : 4096,
    outputTokenLimitMode: c.outputTokenLimitMode === "max_completion_tokens" ? "max_completion_tokens" : "max_tokens",
    tokenizerProfile: String(c.tokenizerProfile || "o200k_base"),
    tokenizerVersion: String(c.tokenizerVersion || "2026-01"),
  };
}

/** Platform-frozen compact waterlines (read-only in UI). */
export const COMPACTION_TRIGGER_BPS = 8000;
export const COMPACTION_TARGET_BPS = 6000;

/** Permanent-disclosure warning for Agent setting (T4-B). Locale-aware. */
export function compactionSummaryPermanenceWarning(): string {
  return tt("agents.compactionSummaryPermanenceWarning");
}

/** Agent-only AAP flag bag (session-context-policy.v2). Both default false. */
export type AapFlags = {
  includeCompactionSummary: boolean;
  enableA2UI: boolean;
};

/** Read aap flags with boolean defaults (missing / null → false). */
export function readAapFlags(
  aap?: SessionContextPolicy["aap"] | null,
): AapFlags {
  return {
    includeCompactionSummary: Boolean(aap?.includeCompactionSummary),
    enableA2UI: Boolean(aap?.enableA2UI),
  };
}

export function anyAapFlagTrue(flags: AapFlags): boolean {
  return flags.includeCompactionSummary || flags.enableA2UI;
}

/**
 * Merge a partial aap patch onto current flags without clobbering sibling keys.
 * Used by Agents Studio setters (KD-14).
 */
export function mergeAapFlags(
  current: SessionContextPolicy["aap"] | undefined | null,
  patch: Partial<{ includeCompactionSummary: boolean; enableA2UI: boolean }>,
): AapFlags {
  const base = readAapFlags(current);
  return {
    includeCompactionSummary:
      patch.includeCompactionSummary !== undefined
        ? Boolean(patch.includeCompactionSummary)
        : base.includeCompactionSummary,
    enableA2UI: patch.enableA2UI !== undefined ? Boolean(patch.enableA2UI) : base.enableA2UI,
  };
}

/** Emit aap object with both flags (never omit one key when emitting aap). */
export function aapFlagBag(flags: AapFlags): NonNullable<SessionContextPolicy["aap"]> {
  return {
    includeCompactionSummary: flags.includeCompactionSummary,
    enableA2UI: flags.enableA2UI,
  };
}

/**
 * Whether policy must use session-context-policy.v2.
 * Any aap flag true, or explicit v2, forces v2. Never emit v1+aap.
 */
export function needsSessionContextV2(
  policy: Pick<SessionContextPolicy, "schemaVersion" | "aap">,
  flags: AapFlags = readAapFlags(policy.aap),
): boolean {
  if (anyAapFlagTrue(flags)) return true;
  if (policy.schemaVersion === "session-context-policy.v2") return true;
  // aap object present without flags still cannot pair with v1
  if (policy.aap != null && typeof policy.aap === "object") return true;
  return false;
}

/** Build session-context-policy.v1/v2 for API. Empty / inherit → {}. */
export function buildContextPolicyPayload(
  policy: SessionContextPolicy | Record<string, unknown> | undefined | null,
): SessionContextPolicy | Record<string, never> {
  if (!policy || typeof policy !== "object") return {};
  const p = policy as SessionContextPolicy;
  const mode = String(p.mode || "").trim();
  const flags = readAapFlags(p.aap);
  const hasAapTrue = anyAapFlagTrue(flags);
  const emitAap = hasAapTrue || (p.schemaVersion === "session-context-policy.v2" && p.aap != null);
  const useV2 = hasAapTrue || p.schemaVersion === "session-context-policy.v2" || emitAap;

  if (!mode || mode === "inherit" || mode === "disabled") {
    // disabled still needs schema for explicit product choice
    if (mode === "disabled") {
      if (emitAap || hasAapTrue) {
        return {
          schemaVersion: "session-context-policy.v2",
          mode: "disabled",
          aap: aapFlagBag(flags),
        };
      }
      return { schemaVersion: "session-context-policy.v1", mode: "disabled" };
    }
    if (emitAap || hasAapTrue) {
      return {
        schemaVersion: "session-context-policy.v2",
        aap: aapFlagBag(flags),
      };
    }
    return {};
  }
  if (mode !== "token_window" && mode !== "rolling_summary") return {};
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
  // Agent-only flag bag; never emit v1+aap. Emit both flags together when present.
  if (emitAap) {
    out.schemaVersion = "session-context-policy.v2";
    out.aap = aapFlagBag(flags);
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
  const flags = readAapFlags(p.aap);
  const schemaVersion: SessionContextPolicy["schemaVersion"] = needsSessionContextV2(p, flags)
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
      out.aap = aapFlagBag(flags);
    }
    return out;
  }
  if (anyAapFlagTrue(flags) || (p.aap != null && schemaVersion === "session-context-policy.v2")) {
    return {
      schemaVersion: "session-context-policy.v2",
      mode: undefined,
      aap: aapFlagBag(flags),
    };
  }
  return { mode: undefined };
}
