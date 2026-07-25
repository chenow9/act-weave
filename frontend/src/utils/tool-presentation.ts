import type { CapabilityProvider, Tool } from "../types/domain";

export type ToolTypeLabel = "HTTP Tool" | "Workflow Tool";

export function getToolTypeLabel(tool: Tool): ToolTypeLabel {
  const executor = `${tool.protocol} ${tool.draftVersion?.executorType || ""}`.toLocaleLowerCase();
  return executor.includes("workflow") ? "Workflow Tool" : "HTTP Tool";
}

export function getToolProtocolLabel(tool: Tool, provider?: CapabilityProvider) {
  if (getToolTypeLabel(tool) === "Workflow Tool") return "Internal";

  const configuredVersion = [
    tool.actionConfig.openapiVersion,
    tool.actionConfig.openAPIVersion,
    tool.actionConfig.protocolVersion,
    tool.actionConfig.specVersion,
  ].find((value) => typeof value === "string" && value.trim());
  if (typeof configuredVersion === "string") {
    return configuredVersion.toLocaleLowerCase().startsWith("openapi")
      ? configuredVersion
      : `OpenAPI ${configuredVersion}`;
  }

  if (provider?.kind === "HTTP_OPENAPI") return "OpenAPI 3.0";
  return tool.protocol || "HTTP";
}
