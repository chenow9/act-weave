import { inject } from "vue";

export function useChatExecutionPageContext() {
  const page = inject<any>("chatExecutionPage");
  if (!page) throw new Error("chatExecutionPage missing");
  return page;
}
