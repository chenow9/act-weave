import { inject } from "vue";

export function useSmartDagPageContext() {
  const page = inject<any>("smartDagPage");
  if (!page) throw new Error("smartDagPage missing");
  return page;
}
