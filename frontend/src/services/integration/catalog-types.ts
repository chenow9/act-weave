/** Catalog load status for workspace-scoped Connection lookups (ZKL-56 / ZKL-64). */
export type CatalogLoadStatus = "IDLE" | "LOADING" | "LOADED" | "ERROR";

export interface CatalogLoadState {
  status: CatalogLoadStatus;
  errorCode?: string;
  requestId?: string;
}
