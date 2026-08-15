/**
 * Post-publish bind for generated drafts (D12).
 * Agent comes from the published draft's server graph.ui.agentId, not editor memory.
 */
import { useAgentStore } from "../stores/agents";
import { useSmartDagStore } from "../stores/smartdag";
import { useWorkflowStore } from "../stores/workflow";
import type { Workflow } from "../types/domain";

export async function resolveGenerateAgentId(workflow: Workflow): Promise<string> {
  const workflowStore = useWorkflowStore();
  const smart = useSmartDagStore();
  let draft = workflowStore.activeDraft;
  if (!draft || draft.workflowId !== workflow.id) {
    // List/detail after refresh: do not assign editorGraph from this GET.
    const loaded = await workflowStore.loadWorkflowDraft(workflow.id);
    draft = loaded.draft;
  }
  const fromDraft = typeof draft.graph?.ui?.agentId === "string" ? draft.graph.ui.agentId.trim() : "";
  if (fromDraft) return fromDraft;
  if (smart.sessionWorkflowId === workflow.id && smart.agentId.trim()) return smart.agentId.trim();
  return "";
}

export async function bindPublishedWorkflowToSessionAgent(
  workflow: Workflow,
  options: { onFailure?: () => void } = {},
): Promise<void> {
  try {
    // GET draft belongs here so list/detail refresh can bind without failing publish.
    const agentId = await resolveGenerateAgentId(workflow);
    if (!agentId) return;

    const agentStore = useAgentStore();
    let agent =
      agentStore.items.find((item) => item.id === agentId) || agentStore.pageItems.find((item) => item.id === agentId);
    if (!agent) {
      await agentStore.loadAgents({ workspaceId: workflow.workspaceId });
      agent = agentStore.items.find((item) => item.id === agentId);
    }
    if (!agent) {
      options.onFailure?.();
      return;
    }

    const existing = (agentStore.bindingsByAgent[agent.id] || []).find((binding) => binding.capabilityId === workflow.id);
    await agentStore.bindCapability(agent, workflow.id, {
      capabilityId: workflow.id,
      versionPolicy: "FOLLOW_ACTIVE",
      enabled: true,
      configOverrides: {},
      lockVersion: existing?.lockVersion ?? 0,
    });
  } catch {
    options.onFailure?.();
  }
}
