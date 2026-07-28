import { inject } from "vue";

export function useOpenAPIImportsPageContext() {
  const page = inject<any>("openapiImportsPage");
  if (!page) throw new Error("openapiImportsPage missing");
  return page;
}
