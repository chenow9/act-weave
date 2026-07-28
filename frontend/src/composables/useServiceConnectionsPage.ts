/**
 * Service Connections page composable entry (ZKL-64 item 11).
 * Thin Vue boundary over the page model (list / form / dialog orchestration).
 */
import { createServiceConnectionsPageModel } from "./service-connections-page-model";

export function useServiceConnectionsPage() {
  return createServiceConnectionsPageModel();
}
