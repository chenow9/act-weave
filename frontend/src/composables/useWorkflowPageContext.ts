import { inject } from "vue";

export function useWorkflowPageContext() {
  const page = inject<any>("workflowPage");
  if (!page) throw new Error("workflowPage missing");
  return page;
}
