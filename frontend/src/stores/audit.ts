import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import type { AuditEvent, AuditEventFilter, AuditExport, CreateAuditExportRequest } from "../types/domain";

interface AuditState {
  events: AuditEvent[];
  eventById: Record<string, AuditEvent>;
  selectedEvent?: AuditEvent;
  currentExport?: AuditExport;
  filters: AuditEventFilter;
  workspaceId: string;
  loading: boolean;
  exportLoading: boolean;
}

export const useAuditStore = defineStore("audit", {
  state: (): AuditState => ({
    events: [],
    eventById: {},
    selectedEvent: undefined,
    currentExport: undefined,
    filters: { limit: 100 },
    workspaceId: "",
    loading: false,
    exportLoading: false,
  }),
  getters: {
    selectedHasSensitiveDetail(state) {
      const value = state.selectedEvent;
      return Boolean(value && (value.sourceIp || value.userAgent || value.payload !== undefined));
    },
  },
  actions: {
    async loadEvents(workspaceId: string, filters: AuditEventFilter = {}) {
      this.loading = true;
      this.workspaceId = workspaceId;
      this.filters = { limit: 100, ...filters };
      try {
        const response = await apiClient.get<{ items: AuditEvent[] }>(
          `/workspaces/${workspaceId}/audit-events${auditQuerySuffix(this.filters)}`,
        );
        this.events = response.data.items;
        this.eventById = Object.fromEntries(this.events.map((event) => [event.id, event]));
        if (!this.events.some((event) => event.id === this.selectedEvent?.id)) {
          this.selectedEvent = this.events[0];
        }
        return response.data.items;
      } finally {
        this.loading = false;
      }
    },
    async loadEventDetail(workspaceId: string, eventId: string) {
      const response = await apiClient.get<AuditEvent>(`/workspaces/${workspaceId}/audit-events/${eventId}`);
      this.eventById[response.data.id] = response.data;
      this.selectedEvent = response.data;
      return response.data;
    },
    async createExport(workspaceId: string, filters?: AuditEventFilter, expiresInSeconds = 3600) {
      this.exportLoading = true;
      try {
        const request = auditExportRequest(filters || this.filters, expiresInSeconds);
        const response = await apiClient.post<AuditExport>(`/workspaces/${workspaceId}/audit-exports`, request);
        this.currentExport = response.data;
        return response.data;
      } finally {
        this.exportLoading = false;
      }
    },
    async loadExport(workspaceId: string, exportId?: string) {
      const targetExportId = exportId || this.currentExport?.id;
      if (!targetExportId) throw new Error("没有可查询的审计导出任务。");
      this.exportLoading = true;
      try {
        const response = await apiClient.get<AuditExport>(`/workspaces/${workspaceId}/audit-exports/${targetExportId}`);
        this.currentExport = response.data;
        return response.data;
      } finally {
        this.exportLoading = false;
      }
    },
  },
});

function auditQuerySuffix(filters: AuditEventFilter) {
  const params = new URLSearchParams();
  setQuery(params, "actorType", filters.actorType);
  setQuery(params, "actorId", filters.actorId);
  setQuery(params, "resourceType", filters.resourceType);
  setQuery(params, "resourceId", filters.resourceId);
  setQuery(params, "action", filters.action);
  for (const result of filters.results || []) params.append("result", result);
  setQuery(params, "requestId", filters.requestId);
  setQuery(params, "traceId", filters.traceId);
  setQuery(params, "occurredFrom", filters.occurredFrom);
  setQuery(params, "occurredUntil", filters.occurredUntil);
  setQuery(params, "beforeOccurredAt", filters.beforeOccurredAt);
  setQuery(params, "beforeId", filters.beforeId);
  if (filters.limit) params.set("limit", String(filters.limit));
  const query = params.toString();
  return query ? `?${query}` : "";
}

function auditExportRequest(filters: AuditEventFilter, expiresInSeconds: number): CreateAuditExportRequest {
  const request: CreateAuditExportRequest = { expiresInSeconds };
  copyValue(request, "actorType", filters.actorType);
  copyValue(request, "actorId", filters.actorId);
  copyValue(request, "resourceType", filters.resourceType);
  copyValue(request, "resourceId", filters.resourceId);
  copyValue(request, "action", filters.action);
  if (filters.results?.length) request.results = filters.results;
  copyValue(request, "requestId", filters.requestId);
  copyValue(request, "traceId", filters.traceId);
  copyValue(request, "occurredFrom", filters.occurredFrom);
  copyValue(request, "occurredUntil", filters.occurredUntil);
  return request;
}

function setQuery(params: URLSearchParams, key: string, value?: string) {
  if (value?.trim()) params.set(key, value.trim());
}

function copyValue<T extends object, K extends keyof T>(target: T, key: K, value?: T[K]) {
  if (value !== undefined && value !== "") target[key] = value;
}
