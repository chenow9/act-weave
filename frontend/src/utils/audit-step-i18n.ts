/**
 * Localize Agent Audit timeline step titles/content that the backend still
 * emits as fixed Chinese strings (agentaudit.BuildTimeline).
 *
 * Prefer step.type + structured fields; fall back to parsing legacy titles.
 */
import type { AgentAuditStep } from "../types/domain";

/** Backend MissingReasoningText and common placeholders. */
const MISSING_REASONING_MARKERS = new Set([
  "无推理数据",
  "No reasoning data",
  "(no data)",
  "（无数据）",
]);

type Translate = (key: string, params?: Record<string, unknown>) => string;

function stripStatusSuffix(title: string): { base: string; suffix: string } {
  const m = title.match(/^(.*?)(\s+\[[A-Z0-9_]+\])\s*$/);
  if (!m) return { base: title.trim(), suffix: "" };
  return { base: m[1].trim(), suffix: m[2] };
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return null;
}

function stringField(obj: Record<string, unknown> | null, keys: string[]): string {
  if (!obj) return "";
  for (const key of keys) {
    const v = obj[key];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

/** Extract tool / callable name from params or legacy Chinese/English titles. */
export function extractAuditToolName(step: Pick<AgentAuditStep, "title" | "params">): string {
  const fromParams = stringField(asRecord(step.params), [
    "callableName",
    "toolName",
    "name",
  ]);
  if (fromParams) return fromParams;

  const { base } = stripStatusSuffix((step.title || "").trim());
  const m = base.match(/^(?:工具调用|Tool call)\s*:\s*(.+)$/i);
  return m?.[1]?.trim() || "";
}

/**
 * Extract callable name and optional path segment from delegation titles like:
 *   Agent 调用: inventory_agent (019fb71d → f4d7493c)
 *   Agent call: inventory_agent (short → short) [FAILED]
 */
export function extractAuditDelegationParts(title: string): {
  name: string;
  path: string;
  statusSuffix: string;
} {
  const { base, suffix } = stripStatusSuffix((title || "").trim());
  let rest = base.replace(/^(?:Agent 调用|Agent call)\s*:\s*/i, "").trim();
  // Trailing path in parentheses (IDs / agent path) — keep as-is (not translated).
  const pathMatch = rest.match(/^(.*?)\s+(\([^()]*(?:→|->)[^()]*\))\s*$/);
  if (pathMatch) {
    return {
      name: pathMatch[1].trim(),
      path: pathMatch[2].trim(),
      statusSuffix: suffix,
    };
  }
  // Also accept any trailing single parenthetical group after a non-empty name.
  const anyParen = rest.match(/^(.*?)\s+(\([^)]+\))\s*$/);
  if (anyParen && anyParen[1].trim()) {
    return {
      name: anyParen[1].trim(),
      path: anyParen[2].trim(),
      statusSuffix: suffix,
    };
  }
  return { name: rest, path: "", statusSuffix: suffix };
}

function localizeCompactionTitle(raw: string, t: Translate): string {
  const { base, suffix } = stripStatusSuffix(raw);
  // Match backend compactStep titles (Chinese fixed strings).
  if (base.includes("退化为") || base.includes("token_window")) {
    return t("logs.stepCompactionFallback") + suffix;
  }
  if (base.includes("失败") || /fail/i.test(base)) {
    return t("logs.stepCompactionFailed") + suffix;
  }
  if (base.includes("完成") || /complet/i.test(base)) {
    return t("logs.stepCompactionCompleted") + suffix;
  }
  return t("logs.stepCompaction") + suffix;
}

/**
 * Human-readable step title for the current UI locale.
 * Ignores backend Chinese title language; keeps names, paths, and status tags.
 */
export function localizedAuditStepTitle(
  step: Pick<AgentAuditStep, "type" | "title" | "params" | "status">,
  t: Translate,
): string {
  const type = (step.type || "").toLowerCase().trim();
  const raw = (step.title || "").trim();
  const { suffix: statusFromTitle } = stripStatusSuffix(raw);

  switch (type) {
    case "input":
      return t("logs.stepInput") + statusFromTitle;
    case "output":
      return t("logs.stepOutput") + statusFromTitle;
    case "reasoning":
      return t("logs.stepReasoning") + statusFromTitle;
    case "tool": {
      const name = extractAuditToolName(step);
      if (name) return t("logs.stepTool", { name }) + statusFromTitle;
      return t("logs.stepToolDefault") + statusFromTitle;
    }
    case "agent_delegation": {
      const { name, path, statusSuffix } = extractAuditDelegationParts(raw);
      const status = statusSuffix || statusFromTitle;
      if (name) {
        const head = t("logs.stepDelegation", { name });
        return path ? `${head} ${path}${status}` : head + status;
      }
      return t("logs.stepDelegationDefault") + (path ? ` ${path}` : "") + status;
    }
    case "context_compaction":
      return localizeCompactionTitle(raw || t("logs.stepCompaction"), t);
    case "unknown": {
      const m = raw.match(/^(?:步骤|Step)\s*:\s*(.+)$/i);
      const label = m?.[1]?.trim() || raw || type;
      return t("logs.stepUnknown", { type: label });
    }
    default:
      // Unknown types: prefer raw title if present, else type key.
      if (raw) return raw;
      return t("logs.stepUnknown", { type: step.type || "unknown" });
  }
}

/** True when MODEL step has no displayable reasoning body. */
export function isMissingReasoningContent(
  step: Pick<AgentAuditStep, "type" | "content" | "contentState">,
): boolean {
  if ((step.type || "").toLowerCase() !== "reasoning") return false;
  const state = (step.contentState || "").toLowerCase();
  if (state === "missing" || state === "redacted") return true;
  const content = (step.content || "").trim();
  if (!content) return true;
  return MISSING_REASONING_MARKERS.has(content);
}
