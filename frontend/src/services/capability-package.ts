import { apiClient, apiErrorMessage } from "./api";

export type PackagePreviewAction = "create" | "update-draft" | "blocked" | "rejected";

export interface PackagePreviewItem {
  kind: "tool" | "workflow";
  slug: string;
  name: string;
  action: PackagePreviewAction;
  reason?: string;
  provider?: string;
  connection?: string;
  resolvedProviderId?: string;
  resolvedConnectionId?: string;
  existingId?: string;
}

export interface PackagePreview {
  items: PackagePreviewItem[];
  canImport: boolean;
  blocked: number;
  rejected: number;
}

export interface ConnectionBinding {
  provider: string;
  connection: string;
  targetConnection?: string;
  targetConnectionId?: string;
}

export interface PackageImportItem {
  kind: "tool" | "workflow";
  slug: string;
  name: string;
  action: string;
  id: string;
  draftId?: string;
}

function filenameFromDisposition(value: string | undefined, fallback: string) {
  if (!value) return fallback;
  const match = /filename="([^"]+)"/.exec(value);
  return match?.[1] || fallback;
}

function downloadText(filename: string, text: string) {
  const blob = new Blob([text], { type: "application/yaml;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  URL.revokeObjectURL(url);
}

export async function exportToolPackage(workspaceId: string, toolId: string) {
  const response = await apiClient.get<string>(`/workspaces/${workspaceId}/tools/${toolId}:export`, {
    responseType: "text",
    transformResponse: [(data) => data],
  });
  downloadText(filenameFromDisposition(response.headers["content-disposition"], "tool.actweave.yaml"), response.data);
}

export async function exportWorkflowPackage(workspaceId: string, workflowId: string) {
  const response = await apiClient.get<string>(`/workspaces/${workspaceId}/workflows/${workflowId}:export`, {
    responseType: "text",
    transformResponse: [(data) => data],
  });
  downloadText(
    filenameFromDisposition(response.headers["content-disposition"], "workflow.actweave.yaml"),
    response.data,
  );
}

export async function exportCapabilityPackage(
  workspaceId: string,
  input: { toolIds?: string[]; workflowIds?: string[] },
) {
  const response = await apiClient.post<string>(
    `/workspaces/${workspaceId}/packages:export`,
    { toolIds: input.toolIds || [], workflowIds: input.workflowIds || [] },
    { responseType: "text", transformResponse: [(data) => data] },
  );
  downloadText(
    filenameFromDisposition(response.headers["content-disposition"], "actweave-package.yaml"),
    response.data,
  );
}

export async function previewCapabilityPackage(
  workspaceId: string,
  yaml: string,
  connectionBindings: ConnectionBinding[] = [],
) {
  const response = await apiClient.post<{ preview: PackagePreview }>(`/workspaces/${workspaceId}/packages:preview`, {
    yaml,
    connectionBindings,
  });
  return response.data.preview;
}

export async function importCapabilityPackage(
  workspaceId: string,
  yaml: string,
  connectionBindings: ConnectionBinding[] = [],
) {
  const response = await apiClient.post<{ result: { items: PackageImportItem[] } }>(
    `/workspaces/${workspaceId}/packages:import`,
    { yaml, connectionBindings },
  );
  return response.data.result.items;
}

export function packageErrorMessage(error: unknown, fallback: string) {
  return apiErrorMessage(error, fallback);
}
