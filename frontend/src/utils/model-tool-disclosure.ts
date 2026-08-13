import type { ModelAgenticCapabilities, ModelApiConfig, ModelToolDisclosureUI } from "../types/domain";

/** List-badge kind. Empty / unknown capability fields fail closed to unverified. */
export type ToolCapabilityBadge = "native" | "function_calling" | "none" | "unverified";

const UI_TO_BADGE: Record<ModelToolDisclosureUI, ToolCapabilityBadge> = {
  hidden: "native",
  binary: "function_calling",
  unavailable: "none",
  unverified: "unverified",
};

function isToolDisclosureUI(value: string): value is ModelToolDisclosureUI {
  return value === "hidden" || value === "binary" || value === "unavailable" || value === "unverified";
}

function badgeFromAgenticCapabilities(
  caps: ModelAgenticCapabilities | Record<string, unknown> | null | undefined,
): ToolCapabilityBadge {
  if (!caps || typeof caps !== "object" || Array.isArray(caps)) return "unverified";
  const rec = caps as Record<string, unknown>;
  const toolCalling = typeof rec.toolCalling === "string" ? rec.toolCalling : "";
  switch (toolCalling) {
    case "native_client_search":
      return "native";
    case "function_calling":
      return "function_calling";
    case "none":
      return "none";
  }
  const modes = rec.toolSearchModes;
  if (Array.isArray(modes) && modes.length === 1 && modes[0] === "client") {
    return "native";
  }
  return "unverified";
}

/**
 * Resolve the list badge from toolDisclosureUI, then caps.
 * Never reads modelName. Unknown / empty fields are unverified, not native.
 */
export function resolveToolCapabilityBadge(
  config: Pick<ModelApiConfig, "toolDisclosureUI" | "agenticCapabilities" | "status">,
): ToolCapabilityBadge {
  const ui = typeof config.toolDisclosureUI === "string" ? config.toolDisclosureUI.trim() : "";
  if (ui) {
    return isToolDisclosureUI(ui) ? UI_TO_BADGE[ui] : "unverified";
  }
  if (config.status === "ERROR") return "unverified";
  return badgeFromAgenticCapabilities(config.agenticCapabilities);
}
