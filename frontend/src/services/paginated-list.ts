export const DEFAULT_PAGE_SIZE = 10;
export const PAGE_SIZE_OPTIONS = [10, 20, 50];

export interface ListPagination {
  page: number;
  pageSize: number;
  total: number;
  pageSizeOptions: number[];
}
