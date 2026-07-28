import { inject } from "vue";

export function useToolsPageContext() {
  const page = inject<any>("toolsPage");
  if (!page) throw new Error("toolsPage missing");
  return page;
}
