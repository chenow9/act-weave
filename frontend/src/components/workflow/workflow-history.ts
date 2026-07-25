import type { WorkflowGraphDraft } from "../../types/domain";

export interface WorkflowHistoryState {
  past: WorkflowGraphDraft[];
  future: WorkflowGraphDraft[];
}

export function createWorkflowHistoryState(): WorkflowHistoryState {
  return {
    past: [],
    future: [],
  };
}

export function pushWorkflowHistoryState(
  history: WorkflowHistoryState,
  current: WorkflowGraphDraft,
): WorkflowHistoryState {
  return {
    past: [...history.past, cloneWorkflowGraph(current)],
    future: [],
  };
}

export function restoreWorkflowHistoryCheckpoint(
  history: WorkflowHistoryState,
  checkpoint: WorkflowGraphDraft,
): WorkflowHistoryState {
  return {
    past: history.past,
    future: [cloneWorkflowGraph(checkpoint), ...history.future],
  };
}

export function cloneWorkflowGraph(graph: WorkflowGraphDraft): WorkflowGraphDraft {
  return JSON.parse(JSON.stringify(graph)) as WorkflowGraphDraft;
}

export function sameWorkflowGraph(left: WorkflowGraphDraft, right: WorkflowGraphDraft): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}
