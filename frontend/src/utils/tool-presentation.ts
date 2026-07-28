import type { CapabilityProvider, Tool } from "../types/domain";

/** Filter / list-query value (API contract). */
export type ToolTypeKey = "HTTP Tool" | "Workflow Tool";

/** Short UI label — no trailing "Tool". */
export type ToolTypeLabel = "HTTP" | "Workflow";

export function getToolTypeKey(tool: Tool): ToolTypeKey {
  const executor = `${tool.protocol} ${tool.draftVersion?.executorType || ""}`.toLocaleLowerCase();
  return executor.includes("workflow") ? "Workflow Tool" : "HTTP Tool";
}

export function getToolTypeLabel(tool: Tool): ToolTypeLabel {
  return getToolTypeKey(tool) === "Workflow Tool" ? "Workflow" : "HTTP";
}

export function getToolProtocolLabel(tool: Tool, provider?: CapabilityProvider) {
  if (getToolTypeKey(tool) === "Workflow Tool") return "Internal";

  const actionConfig = tool.actionConfig || {};
  const configuredVersion = [
    actionConfig.openapiVersion,
    actionConfig.openAPIVersion,
    actionConfig.protocolVersion,
    actionConfig.specVersion,
  ].find((value) => typeof value === "string" && value.trim());
  if (typeof configuredVersion === "string") {
    return configuredVersion.toLocaleLowerCase().startsWith("openapi")
      ? configuredVersion
      : `OpenAPI ${configuredVersion}`;
  }

  if (provider?.kind === "HTTP_OPENAPI") return "OpenAPI 3.0";
  return tool.protocol || "HTTP";
}
