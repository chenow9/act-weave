import { inject } from "vue";

export function useAgentsPageContext() {
  const page = inject<any>("agentsPage");
  if (!page) throw new Error("agentsPage missing");
  return page;
}
