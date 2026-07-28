/**
 * Smart DAG page composable entry (ZKL-64 item 14).
 */
import { createSmartDagPageModel } from "./smart-dag-page-model";

export function useSmartDagPage() {
  return createSmartDagPageModel();
}
