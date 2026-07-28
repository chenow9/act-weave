/**
 * Agents page composable entry (ZKL-64 item 16).
 */
import { createAgentsPageModel } from "./agents-page-model";

export function useAgentsPage() {
  return createAgentsPageModel();
}
