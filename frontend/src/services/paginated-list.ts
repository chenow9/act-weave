/**
 * Shared pagination helpers for management tables.
 * All list tables should use server-side page/pageSize and this response shape.
 */
export const DEFAULT_PAGE_SIZE = 10;
export const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

export interface ListPagination {
  page: number;
  pageSize: number;
  total: number;
  pageSizeOptions: number[];
}

export interface ServerPaginationMeta {
  page?: number;
  pageSize?: number;
  total?: number;
}

export interface PaginatedListParams {
  query?: string;
  page?: number;
  pageSize?: number;
  sortBy?: string;
  sortOrder?: "asc" | "desc";
  /** Extra filter keys (status, type, mode, …). */
  [key: string]: string | number | undefined;
}

/** Default list pagination state for Pinia stores. */
export function emptyListPagination(pageSize = DEFAULT_PAGE_SIZE): ListPagination {
  return {
    page: 1,
    pageSize,
    total: 0,
    pageSizeOptions: [...PAGE_SIZE_OPTIONS],
  };
}

export function positiveInteger(value: unknown, fallback: number): number {
  const n = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(n) || n < 1) return fallback;
  return Math.floor(n);
}

export function nonNegativeInteger(value: unknown, fallback: number): number {
  const n = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(n) || n < 0) return fallback;
  return Math.floor(n);
}

/**
 * Build query string for server list endpoints.
 * Always includes page + pageSize. Skips empty/undefined filters.
 */
export function buildListQueryString(params: PaginatedListParams): string {
  const search = new URLSearchParams();
  const page = positiveInteger(params.page, 1);
  const pageSize = positiveInteger(params.pageSize, DEFAULT_PAGE_SIZE);
  search.set("page", String(page));
  search.set("pageSize", String(pageSize));

  for (const [key, value] of Object.entries(params)) {
    if (key === "page" || key === "pageSize") continue;
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value).trim());
  }
  return search.toString();
}

/** Normalize server pagination metadata into store ListPagination. */
export function normalizeListPagination(
  response: ServerPaginationMeta | undefined | null,
  request: { page?: number; pageSize?: number },
  itemCount: number,
): ListPagination {
  const requestedPage = positiveInteger(request.page, 1);
  const requestedPageSize = positiveInteger(request.pageSize, DEFAULT_PAGE_SIZE);
  return {
    page: positiveInteger(response?.page, requestedPage),
    pageSize: positiveInteger(response?.pageSize, requestedPageSize),
    total: nonNegativeInteger(response?.total, itemCount),
    pageSizeOptions: [...PAGE_SIZE_OPTIONS],
  };
}

export type MergeListQueryInput<T extends PaginatedListParams> = T & {
  page?: number;
  pageSize?: number;
  query?: string;
};

/** Merge override query with previous list query (shared by page models / stores). */
export function mergeListQuery<T extends PaginatedListParams>(previous: T, overrides: Partial<T> = {}): T {
  return {
    ...previous,
    ...overrides,
    query: overrides.query !== undefined ? overrides.query : previous.query,
    page: overrides.page ?? previous.page ?? 1,
    pageSize: overrides.pageSize ?? previous.pageSize ?? DEFAULT_PAGE_SIZE,
  } as T;
}
