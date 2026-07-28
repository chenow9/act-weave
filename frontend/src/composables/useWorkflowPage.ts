/**
 * Workflow page composable entry (ZKL-64 item 13).
 */
import { createWorkflowPageModel } from "./workflow-page-model";

export function useWorkflowPage() {
  return createWorkflowPageModel();
}
