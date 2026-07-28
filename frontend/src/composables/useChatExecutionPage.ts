/**
 * Chat execution page composable entry (ZKL-64 item 15).
 */
import { createChatExecutionPageModel } from "./chat-execution-page-model";

export function useChatExecutionPage() {
  return createChatExecutionPageModel();
}
