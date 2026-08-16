/**
 * Tools page composable entry (ZKL-64 item 12).
 */
import { createToolsPageModel } from "./tools-page-model";

export function useToolsPage(options: { surface?: "list" | "workspace" } = {}) {
  return createToolsPageModel(options);
}
