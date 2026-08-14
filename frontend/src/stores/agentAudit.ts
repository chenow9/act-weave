import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import type {
  AgentAuditListResult,
  AgentAuditTraceDetail,
  AgentAuditTraceListItem,
  AgentAuditStats,
} from "../types/domain";

export interface AgentAuditListParams {
  q?: string;
  status?: string;
  from?: string;
  to?: string;
  page?: number;
  pageSize?: number;
}

interface AgentAuditState {
  workspaceId: string;
  items: AgentAuditTraceListItem[];
  stats: AgentAuditStats;
  debugMode: boolean;
  selected?: AgentAuditTraceDetail;
  searchQuery: string;
  statusFilter: string;
  rangeFrom: string;
  rangeTo: string;
  page: number;
  pageSize: number;
  total: number;
  isMasked: boolean;
  loading: boolean;
  detailLoading: boolean;
  /** True while fetching the next timeline page (infinite scroll). */
  detailLoadingMore: boolean;
  detailHasMore: boolean;
  detailStepOffset: number;
  detailStepLimit: number;
  view: "list" | "detail";
}

const emptyStats = (): AgentAuditStats => ({
  totalRuns: 0,
  successRate: 0,
  failureRate: 0,
  avgLatencyMs: 0,
});

const DEFAULT_PAGE_SIZE = 10;
/** Default timeline page size for detail infinite scroll. */
export const DEFAULT_DETAIL_STEP_LIMIT = 30;

export const useAgentAuditStore = defineStore("agentAudit", {
  state: (): AgentAuditState => ({
    workspaceId: "",
    items: [],
    stats: emptyStats(),
    debugMode: false,
    selected: undefined,
    searchQuery: "",
    statusFilter: "",
    rangeFrom: "",
    rangeTo: "",
    page: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    total: 0,
    isMasked: true,
    loading: false,
    detailLoading: false,
    detailLoadingMore: false,
    detailHasMore: false,
    detailStepOffset: 0,
    detailStepLimit: DEFAULT_DETAIL_STEP_LIMIT,
    view: "list",
  }),
  getters: {
    pageCount(state): number {
      return Math.max(1, Math.ceil(state.total / state.pageSize));
    },
  },
  actions: {
    async loadTraces(workspaceId: string, params: AgentAuditListParams | string = {}): Promise<void> {
      const normalized = typeof params === "string" ? { q: params } : params;

      this.loading = true;
      this.workspaceId = workspaceId;
      if (normalized.q !== undefined) this.searchQuery = normalized.q;
      if (normalized.status !== undefined) this.statusFilter = normalized.status;
      if (normalized.from !== undefined) this.rangeFrom = normalized.from;
      if (normalized.to !== undefined) this.rangeTo = normalized.to;
      if (normalized.page !== undefined) this.page = Math.max(1, normalized.page);
      if (normalized.pageSize !== undefined) {
        this.pageSize = Math.min(100, Math.max(1, normalized.pageSize));
      }

      try {
        const queryParams = new URLSearchParams();
        if (this.searchQuery.trim()) queryParams.set("q", this.searchQuery.trim());
        if (this.statusFilter) queryParams.set("status", this.statusFilter);
        if (this.rangeFrom) queryParams.set("from", this.rangeFrom);
        if (this.rangeTo) queryParams.set("to", this.rangeTo);
        queryParams.set("limit", String(this.pageSize));
        queryParams.set("page", String(this.page));
        const suffix = queryParams.toString() ? `?${queryParams.toString()}` : "";
        const response = await apiClient.get<AgentAuditListResult>(
          `/workspaces/${workspaceId}/agent-audit/traces${suffix}`,
        );
        this.items = response.data.items || [];
        this.stats = response.data.stats || emptyStats();
        this.total = Number(response.data.total) || 0;
        this.debugMode = Boolean(response.data.debugMode);
        if (!this.debugMode) {
          this.isMasked = true;
        }
        // Clamp page if filter/total shrunk under us.
        const maxPage = Math.max(1, Math.ceil(this.total / this.pageSize) || 1);
        if (this.page > maxPage) {
          this.page = maxPage;
          await this.loadTraces(workspaceId, { page: maxPage });
          return;
        }
      } finally {
        this.loading = false;
      }
    },
    async loadTraceDetail(workspaceId: string, traceId: string) {
      this.detailLoading = true;
      this.detailLoadingMore = false;
      this.detailHasMore = false;
      this.detailStepOffset = 0;
      try {
        const query = new URLSearchParams({
          limit: String(this.detailStepLimit),
          offset: "0",
        });
        const response = await apiClient.get<AgentAuditTraceDetail>(
          `/workspaces/${workspaceId}/agent-audit/traces/${encodeURIComponent(traceId)}?${query}`,
        );
        this.selected = {
          ...response.data,
          steps: response.data.steps || [],
        };
        this.debugMode = Boolean(response.data.debugMode);
        this.detailHasMore = Boolean(response.data.hasMore);
        this.detailStepOffset = (response.data.stepOffset ?? 0) + (response.data.steps?.length ?? 0);
        if (typeof response.data.stepLimit === "number" && response.data.stepLimit > 0) {
          this.detailStepLimit = response.data.stepLimit;
        }
        this.view = "detail";
        return response.data;
      } finally {
        this.detailLoading = false;
      }
    },
    /** Append the next timeline page when the user scrolls near the end. */
    async loadMoreTraceSteps(workspaceId: string) {
      if (!this.selected?.traceId || !this.detailHasMore || this.detailLoadingMore || this.detailLoading) {
        return;
      }
      this.detailLoadingMore = true;
      try {
        const query = new URLSearchParams({
          limit: String(this.detailStepLimit),
          offset: String(this.detailStepOffset),
        });
        const response = await apiClient.get<AgentAuditTraceDetail>(
          `/workspaces/${workspaceId}/agent-audit/traces/${encodeURIComponent(this.selected.traceId)}?${query}`,
        );
        const nextSteps = response.data.steps || [];
        this.selected = {
          ...this.selected,
          ...response.data,
          // Keep accumulated steps; response meta overwrites header fields.
          steps: [...(this.selected.steps || []), ...nextSteps],
        };
        this.detailHasMore = Boolean(response.data.hasMore);
        this.detailStepOffset = (response.data.stepOffset ?? this.detailStepOffset) + nextSteps.length;
        return response.data;
      } finally {
        this.detailLoadingMore = false;
      }
    },
    goToList() {
      this.view = "list";
      this.selected = undefined;
      this.detailLoading = false;
      this.detailLoadingMore = false;
      this.detailHasMore = false;
      this.detailStepOffset = 0;
    },
    toggleMask() {
      if (!this.debugMode) {
        this.isMasked = true;
        return;
      }
      this.isMasked = !this.isMasked;
    },
  },
});
