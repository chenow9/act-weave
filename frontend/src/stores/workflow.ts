import { defineStore } from "pinia";

import { tt } from "../i18n/tt";
import { apiClient, toAPIError } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import type {
  Execution,
  SaveWorkflowDraftRequest,
  Workflow,
  WorkflowCompilation,
  WorkflowCompilationIssue,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
  WorkflowListQuery,
  WorkflowReadiness,
  WorkflowRevision,
  WorkflowRevisionDiff,
  WorkflowStatus,
  WorkflowSummary,
  WorkflowValidationResult,
  WorkflowExecution,
  WorkflowExecutionStep,
} from "../types/domain";
import { normalizeWorkflowGraphDraft } from "../utils/workflow-graph";
import { useWorkspaceStore } from "./workspaces";

interface WorkflowState {
  workflows: WorkflowSummary[];
  pageItems: WorkflowSummary[];
  pagination: ListPagination;
  listQuery: Required<Pick<WorkflowListQuery, "query" | "page" | "pageSize">> &
    Pick<WorkflowListQuery, "status" | "sortBy" | "sortOrder">;
  pageLoading: boolean;
  pageError: string | null;
  pageHasLoaded: boolean;
  workflowDetails: Record<string, Workflow>;
  executions: Execution[];
  executionById: Record<string, Execution>;
  revisionsByWorkflowId: Record<string, WorkflowRevision[]>;
  revisionDiffByWorkflowId: Record<string, WorkflowRevisionDiff | undefined>;
  readinessByWorkflowId: Record<string, WorkflowReadiness>;
  loading: boolean;
  selectedWorkflowId: string;
  activeDraft?: WorkflowDraftRecord;
  activeCompilation?: WorkflowCompilation;
  lastValidation?: WorkflowValidationResult;
  lastExecution?: Execution;
  lastSuccessfulTrialInputByWorkflowId: Record<string, Record<string, unknown>>;
}

export class WorkflowDraftConflictError extends Error {
  readonly latest: WorkflowDraftRecord;

  constructor(latest: WorkflowDraftRecord) {
    super(tt("workflow.draftConflict"));
    this.name = "WorkflowDraftConflictError";
    this.latest = latest;
  }
}

export const useWorkflowStore = defineStore("workflow", {
  state: (): WorkflowState => ({
    workflows: [],
    pageItems: [],
    pagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    listQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    pageLoading: false,
    pageError: null,
    pageHasLoaded: false,
    workflowDetails: {},
    executions: [],
    executionById: {},
    revisionsByWorkflowId: {},
    revisionDiffByWorkflowId: {},
    readinessByWorkflowId: {},
    loading: false,
    selectedWorkflowId: "",
    activeDraft: undefined,
    activeCompilation: undefined,
    lastValidation: undefined,
    lastExecution: undefined,
    lastSuccessfulTrialInputByWorkflowId: {},
  }),
  getters: {
    selectedWorkflow(state): WorkflowSummary | undefined {
      return state.workflows.find((workflow) => workflow.id === state.selectedWorkflowId) ?? state.workflows[0];
    },
    selectedWorkflowDetail(state): Workflow | undefined {
      return state.workflowDetails[state.selectedWorkflowId];
    },
  },
  actions: {
    async loadWorkflowAssets() {
      this.loading = true;
      try {
        await this.loadWorkflowPage({ page: 1 });
        this.selectedWorkflowId = this.workflows.some((workflow) => workflow.id === this.selectedWorkflowId)
          ? this.selectedWorkflowId
          : this.workflows[0]?.id || "";
      } finally {
        this.loading = false;
      }
    },
    async fetchWorkflowCatalog() {
      const workspaceIDs = await accessibleWorkspaceIDs();
      const results = await Promise.all(
        workspaceIDs.map(async (workspaceID) => {
          const response = await apiClient.get<{ items: WorkflowDTO[] }>(`/workspaces/${workspaceID}/workflows`);
          return Promise.all(
            response.data.items.map(async (value) => {
              const readinessResponse = await apiClient.get<WorkflowReadinessDTO>(
                `/workspaces/${workspaceID}/workflows/${value.id}/readiness`,
              );
              const readiness = readinessFromDTO(readinessResponse.data);
              this.readinessByWorkflowId[value.id] = readiness;
              return summaryFromDTO(
                value,
                workspaceID,
                readiness,
                {
                  nodeCount: value.nodeCount,
                  edgeCount: value.edgeCount,
                },
                this.workflows.find((item) => item.id === value.id),
              );
            }),
          );
        }),
      );
      return results.flat();
    },
    async loadWorkflowPage(query: WorkflowListQuery = {}) {
      this.pageLoading = true;
      this.pageError = null;
      const sortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.listQuery.sortBy;
      const sortOrder = sortBy
        ? query.sortOrder !== undefined
          ? query.sortOrder
          : this.listQuery.sortOrder
        : undefined;
      const request = {
        ...this.listQuery,
        ...query,
        query: query.query ?? this.listQuery.query,
        page: query.page ?? this.listQuery.page,
        pageSize: query.pageSize ?? this.listQuery.pageSize,
        sortBy,
        sortOrder,
      };
      try {
        const catalog = await this.fetchWorkflowCatalog();
        const filtered = filterWorkflows(catalog, request.query, request.status);
        const sorted = sortWorkflows(filtered, sortBy, sortOrder);
        const page = Math.max(1, request.page);
        const pageSize = Math.max(1, request.pageSize);
        this.workflows = catalog;
        this.pageItems = sorted.slice((page - 1) * pageSize, page * pageSize);
        this.pagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
        this.listQuery = { query: request.query, status: request.status, page, pageSize, sortBy, sortOrder };
        this.pageHasLoaded = true;
        return this.pageItems;
      } catch (error) {
        this.pageError = toAPIError(error).message;
        return this.pageItems;
      } finally {
        this.pageLoading = false;
      }
    },
    async loadWorkflow(workflowId: string, options: { force?: boolean } = {}) {
      if (!options.force && this.workflowDetails[workflowId]) return this.workflowDetails[workflowId];
      const workspaceID = this.workspaceIDFor(workflowId);
      const [metadataResponse, readiness] = await Promise.all([
        apiClient.get<WorkflowDTO>(`/workspaces/${workspaceID}/workflows/${workflowId}`),
        this.loadWorkflowReadiness(workflowId),
      ]);
      const workflow = workflowFromDTO(metadataResponse.data, workspaceID, readiness);
      this.setWorkflowDetail(workflow);
      this.selectedWorkflowId = workflow.id;
      return workflow;
    },
    async createWorkflow(workflow: Workflow & { graph?: WorkflowGraphDraft; schemaVersion?: string }) {
      const graph = workflow.graph || emptyWorkflowGraph();
      const response = await apiClient.post<{ workflow: WorkflowDTO; draft: WorkflowDraftDTO }>(
        `/workspaces/${workflow.workspaceId}/workflows`,
        {
          name: workflow.name,
          slug: workflow.slug || slugify(workflow.name),
          description: workflow.description,
          schemaVersion: workflow.schemaVersion || graph.schemaVersion || "workflow.graph.v1",
          graph,
        },
      );
      return this.adoptCreatedWorkflowResponse(workflow.workspaceId, response.data, response.headers?.etag);
    },
    adoptCreatedWorkflowResponse(workspaceId: string, response: WorkflowCreateResponseDTO, etag?: string) {
      const readiness = emptyReadiness();
      const created = workflowFromDTO(response.workflow, workspaceId, readiness);
      this.activeDraft = draftFromDTO(response.draft, workspaceId, created.id, etag);
      this.upsertWorkflow(created);
      this.selectedWorkflowId = created.id;
      return created;
    },
    async updateWorkflow(workflowId: string, workflow: Workflow) {
      const response = await apiClient.patch<WorkflowDTO>(
        `/workspaces/${workflow.workspaceId}/workflows/${workflowId}`,
        {
          name: workflow.name,
          slug: workflow.slug || slugify(workflow.name),
          description: workflow.description,
          status: workflow.status === "Disabled" ? "DISABLED" : "ACTIVE",
          lockVersion: workflow.lockVersion,
        },
      );
      const readiness = this.readinessByWorkflowId[workflowId] || workflow.readiness || emptyReadiness();
      const updated = workflowFromDTO(response.data, workflow.workspaceId, readiness);
      this.upsertWorkflow(updated);
      this.selectedWorkflowId = updated.id;
      return updated;
    },
    async loadWorkflowDraft(workflowId: string) {
      const workspaceID = this.workspaceIDFor(workflowId);
      // ZKL-56: Draft + Readiness in parallel; no new editor-context API.
      const [response, readiness] = await Promise.all([
        apiClient.get<WorkflowDraftDTO>(`/workspaces/${workspaceID}/workflows/${workflowId}/draft`),
        this.loadWorkflowReadiness(workflowId),
      ]);
      const draft = draftFromDTO(response.data, workspaceID, workflowId, response.headers?.etag);
      return {
        draft,
        latestCompilation: this.activeCompilation?.workflowId === workflowId ? this.activeCompilation : undefined,
        workflow: this.workflowDetails[workflowId],
        readiness,
      };
    },
    async saveWorkflowDraft(workflowId: string, draft: WorkflowDraftRecord) {
      const workspaceID = this.workspaceIDFor(workflowId);
      const payload: SaveWorkflowDraftRequest = {
        schemaVersion: draft.schemaVersion || draft.graph.schemaVersion,
        graph: draft.graph,
        draftVersion: draft.draftVersion,
        lockVersion: draft.lockVersion,
      };
      try {
        const response = await apiClient.put<WorkflowDraftDTO>(
          `/workspaces/${workspaceID}/workflows/${workflowId}/draft`,
          payload,
          { headers: { "If-Match": draft.etag || workflowDraftETag(draft) } },
        );
        const saved = draftFromDTO(response.data, workspaceID, workflowId, response.headers?.etag);
        this.selectedWorkflowId = workflowId;
        this.activeDraft = saved;
        this.activeCompilation = undefined;
        await this.loadWorkflowReadiness(workflowId);
        return saved;
      } catch (error) {
        if (toAPIError(error).status === 409) {
          const latest = (await this.loadWorkflowDraft(workflowId)).draft;
          this.activeDraft = latest;
          this.activeCompilation = undefined;
          throw new WorkflowDraftConflictError(latest);
        }
        throw error;
      }
    },
    async deleteWorkflow(workflowId: string) {
      const workflow = this.requireWorkflow(workflowId);
      await apiClient.delete(
        `/workspaces/${workflow.workspaceId}/workflows/${workflowId}?lockVersion=${workflow.lockVersion}`,
      );
      this.workflows = this.workflows.filter((item) => item.id !== workflowId);
      this.pageItems = this.pageItems.filter((item) => item.id !== workflowId);
      this.pagination = { ...this.pagination, total: Math.max(0, this.pagination.total - 1) };
      delete this.workflowDetails[workflowId];
      delete this.readinessByWorkflowId[workflowId];
      if (this.activeDraft?.workflowId === workflowId) {
        this.activeDraft = undefined;
        this.activeCompilation = undefined;
      }
      if (this.selectedWorkflowId === workflowId) this.selectedWorkflowId = this.workflows[0]?.id || "";
    },
    async validateWorkflow(workflowId: string) {
      const workspaceID = this.workspaceIDFor(workflowId);
      const response = await apiClient.post<WorkflowCompilationDTO>(
        `/workspaces/${workspaceID}/workflows/${workflowId}/draft:compile`,
      );
      const compilation = compilationFromDTO(response.data, workflowId);
      this.activeCompilation = compilation;
      this.lastValidation = {
        valid: compilation.status === "VALID",
        issues: compilation.issues.map((issue) => ({
          nodeId: issue.nodeId,
          edgeId: issue.edgeId,
          field: issue.fieldPath || issue.code,
          severity: issue.severity,
          message: issue.message,
        })),
        validatedAt: compilation.compiledAt,
      };
      await this.loadWorkflowReadiness(workflowId);
      return this.lastValidation;
    },
    async trialRunWorkflow(
      workflowId: string,
      input: Record<string, unknown>,
      outboundCredentials?: import("../types/domain").OutboundCredentialsEnvelope,
    ) {
      const workflow = this.requireWorkflow(workflowId);
      const readiness = this.readinessByWorkflowId[workflowId] || (await this.loadWorkflowReadiness(workflowId));
      const compilationID =
        this.activeCompilation?.workflowId === workflowId
          ? this.activeCompilation.id
          : readiness.compilationId || workflow.latestCompilationId;
      if (!compilationID) throw new Error("Compile the current Workflow Draft before trial run.");
      const body: Record<string, unknown> = { input };
      if (outboundCredentials) body.outboundCredentials = outboundCredentials;
      const response = await apiClient.post<WorkflowTrialDTO>(
        `/workspaces/${workflow.workspaceId}/workflows/${workflowId}/compilations/${compilationID}:trial`,
        body,
      );
      const execution = executionFromTrial(response.data, workflow);
      this.lastExecution = execution;
      this.executionById[execution.id] = execution;
      this.executions = [execution, ...this.executions.filter((item) => item.id !== execution.id)];
      if (execution.status === "Success") {
        this.lastSuccessfulTrialInputByWorkflowId[workflowId] = { ...input };
      }
      await this.loadWorkflowReadiness(workflowId);
      return execution;
    },
    /** Production execute against the active published revision (D4/D11 — not trial). */
    async executeProductionWorkflow(workflowId: string, input: Record<string, unknown> = {}) {
      const workflow = this.requireWorkflow(workflowId);
      const revisionId = workflow.activeRevisionId;
      if (!revisionId) {
        throw new Error("Publish and activate a revision before production run.");
      }
      const response = await apiClient.post<{
        executionId: string;
        workflowId: string;
        revisionId: string;
        status: string;
        traceId: string;
      }>(`/workspaces/${workflow.workspaceId}/workflows/${workflowId}/revisions/${revisionId}:execute`, {
        input,
        trigger: "console",
      });
      const status: Execution["status"] =
        response.data.status === "SUCCEEDED"
          ? "Success"
          : response.data.status === "RUNNING" || response.data.status === "PENDING"
            ? "Running"
            : response.data.status === "WAITING_CONFIRMATION"
              ? "Approval"
              : "Failed";
      const execution: Execution = {
        id: response.data.executionId,
        workflowId: response.data.workflowId,
        workflowVersion: response.data.revisionId,
        workspaceId: workflow.workspaceId,
        trigger: "Production",
        userId: "",
        traceId: response.data.traceId,
        status,
        durationMs: 0,
        inputSummary: JSON.stringify(input),
        outputSummary: response.data.status,
        rawPayloadObjectAddress: "",
        steps: [],
      };
      this.lastExecution = execution;
      this.executionById[execution.id] = execution;
      this.executions = [execution, ...this.executions.filter((item) => item.id !== execution.id)];
      return execution;
    },
    async publishWorkflow(workflowId: string) {
      const workflow = this.requireWorkflow(workflowId);
      const readiness = this.readinessByWorkflowId[workflowId] || (await this.loadWorkflowReadiness(workflowId));
      const compilationID =
        this.activeCompilation?.workflowId === workflowId
          ? this.activeCompilation.id
          : readiness.compilationId || workflow.latestCompilationId;
      if (!compilationID) throw new Error("Compile and trial the current Workflow Draft before publishing.");
      const response = await apiClient.post<WorkflowPublishDTO>(
        `/workspaces/${workflow.workspaceId}/workflows/${workflowId}/compilations/${compilationID}:publish`,
        {
          callableName: (workflow.slug || slugify(workflow.name)).replace(/-/g, "_"),
          callableDescription: workflow.description,
          riskLevel: "MEDIUM",
          sideEffectLevel: "WRITE",
          requiresConfirmation: true,
          publishNote: `Publish ${workflow.name}`,
        },
      );
      const revision = revisionFromDTO(response.data.revision, workflowId);
      this.revisionsByWorkflowId[workflowId] = upsertRevision(this.revisionsByWorkflowId[workflowId] || [], revision);
      const [updated, nextReadiness] = await Promise.all([
        apiClient.get<WorkflowDTO>(`/workspaces/${workflow.workspaceId}/workflows/${workflowId}`),
        this.loadWorkflowReadiness(workflowId),
      ]);
      const normalized = workflowFromDTO(updated.data, workflow.workspaceId, nextReadiness);
      this.upsertWorkflow(normalized);
      return { workflow: normalized, revisionId: revision.revisionId, revision, releaseId: response.data.releaseId };
    },
    /**
     * PLATFORM_ADMIN escape hatch: publish VALID compilation without a real trial.
     * Requires server tools.allowForcePublish and reason ≥ 8 chars.
     */
    async forcePublishWorkflow(workflowId: string, reason: string) {
      const workflow = this.requireWorkflow(workflowId);
      const readiness = this.readinessByWorkflowId[workflowId] || (await this.loadWorkflowReadiness(workflowId));
      const compilationID =
        this.activeCompilation?.workflowId === workflowId
          ? this.activeCompilation.id
          : readiness.compilationId || workflow.latestCompilationId;
      if (!compilationID) throw new Error("Compile the current Workflow Draft before force-publishing.");
      const trimmed = reason.trim();
      if (trimmed.length < 8) throw new Error(tt("workflow.forcePublishReasonMin"));
      const response = await apiClient.post<WorkflowPublishDTO & { force?: boolean }>(
        `/workspaces/${workflow.workspaceId}/workflows/${workflowId}/compilations/${compilationID}:force-publish`,
        {
          callableName: (workflow.slug || slugify(workflow.name)).replace(/-/g, "_"),
          callableDescription: workflow.description,
          riskLevel: "MEDIUM",
          sideEffectLevel: "WRITE",
          requiresConfirmation: true,
          publishNote: `Force publish ${workflow.name}`,
          reason: trimmed,
        },
      );
      const revision = revisionFromDTO(response.data.revision, workflowId);
      this.revisionsByWorkflowId[workflowId] = upsertRevision(this.revisionsByWorkflowId[workflowId] || [], revision);
      const [updated, nextReadiness] = await Promise.all([
        apiClient.get<WorkflowDTO>(`/workspaces/${workflow.workspaceId}/workflows/${workflowId}`),
        this.loadWorkflowReadiness(workflowId),
      ]);
      const normalized = workflowFromDTO(updated.data, workflow.workspaceId, nextReadiness);
      this.upsertWorkflow(normalized);
      return {
        workflow: normalized,
        revisionId: revision.revisionId,
        revision,
        releaseId: response.data.releaseId,
        force: true as const,
      };
    },
    async loadWorkflowRevisions(workflowId: string) {
      const workspaceID = this.workspaceIDFor(workflowId);
      const response = await apiClient.get<{ items: WorkflowRevisionDTO[] }>(
        `/workspaces/${workspaceID}/workflows/${workflowId}/revisions`,
      );
      const revisions = response.data.items.map((item) => revisionFromDTO(item, workflowId));
      this.revisionsByWorkflowId[workflowId] = revisions;
      return revisions;
    },
    async activateWorkflowRevision(workflowId: string, revisionId: string) {
      const workflow = this.requireWorkflow(workflowId);
      const response = await apiClient.post<WorkflowActivationDTO>(
        `/workspaces/${workflow.workspaceId}/workflows/${workflowId}/revisions/${revisionId}:activate`,
      );
      const revision = revisionFromDTO(response.data.revision, workflowId);
      this.revisionsByWorkflowId[workflowId] = upsertRevision(this.revisionsByWorkflowId[workflowId] || [], revision);
      const [metadata, readiness] = await Promise.all([
        apiClient.get<WorkflowDTO>(`/workspaces/${workflow.workspaceId}/workflows/${workflowId}`),
        this.loadWorkflowReadiness(workflowId),
      ]);
      const normalized = workflowFromDTO(metadata.data, workflow.workspaceId, readiness);
      this.upsertWorkflow(normalized);
      return { workflow: normalized, revision, releaseId: response.data.releaseId, eventType: response.data.eventType };
    },
    async rollbackWorkflowRevision(workflowId: string, revisionId: string) {
      return this.activateWorkflowRevision(workflowId, revisionId);
    },
    async loadWorkflowRevisionDiff(workflowId: string, leftRevisionId: string, rightRevisionId: string) {
      const workspaceID = this.workspaceIDFor(workflowId);
      const params = new URLSearchParams({ from: leftRevisionId, to: rightRevisionId });
      const response = await apiClient.get<WorkflowRevisionDiffDTO>(
        `/workspaces/${workspaceID}/workflows/${workflowId}/revisions:diff?${params.toString()}`,
      );
      const diff: WorkflowRevisionDiff = {
        workflowId,
        leftRevisionId: response.data.from.id,
        rightRevisionId: response.data.to.id,
        nodeChanges: [],
        edgeChanges: [],
        changes: response.data.changes,
        comparedAt: new Date().toISOString(),
      };
      this.revisionDiffByWorkflowId[workflowId] = diff;
      return diff;
    },
    async loadWorkflowReadiness(workflowId: string) {
      const workspaceID = this.workspaceIDFor(workflowId);
      const response = await apiClient.get<WorkflowReadinessDTO>(
        `/workspaces/${workspaceID}/workflows/${workflowId}/readiness`,
      );
      const readiness = readinessFromDTO(response.data);
      this.readinessByWorkflowId[workflowId] = readiness;
      const detail = this.workflowDetails[workflowId];
      if (detail) this.workflowDetails[workflowId] = { ...detail, readiness };
      this.workflows = this.workflows.map((item) =>
        item.id === workflowId ? { ...item, status: statusFromReadiness(readiness), readiness } : item,
      );
      this.pageItems = this.pageItems.map((item) =>
        item.id === workflowId ? { ...item, status: statusFromReadiness(readiness), readiness } : item,
      );
      return readiness;
    },
    async disableWorkflow(workflowId: string) {
      const current = this.workflowDetails[workflowId] || (await this.loadWorkflow(workflowId));
      return this.updateWorkflow(workflowId, { ...current, status: "Disabled" });
    },

    async loadExecutions(
      filter: {
        workflowId?: string;
        status?: string;
        traceId?: string;
        startedAfter?: string;
        startedBefore?: string;
        limit?: number;
      } = {},
    ) {
      const params = new URLSearchParams();
      if (filter.workflowId) params.set("workflowId", filter.workflowId);
      if (filter.status) params.set("status", filter.status);
      if (filter.traceId) params.set("traceId", filter.traceId);
      if (filter.startedAfter) params.set("startedAfter", filter.startedAfter);
      if (filter.startedBefore) params.set("startedBefore", filter.startedBefore);
      if (filter.limit) params.set("limit", String(filter.limit));
      const suffix = params.toString() ? `?${params.toString()}` : "";
      const workspaceIDs = filter.workflowId
        ? [this.workspaceIDFor(filter.workflowId)]
        : await accessibleWorkspaceIDs();
      const responses = await Promise.all(
        workspaceIDs.map(async (workspaceID) => {
          const response = await apiClient.get<{ items: WorkflowExecution[] }>(
            `/workspaces/${workspaceID}/executions${suffix}`,
          );
          return response.data.items.map((execution) => executionFromV1(execution, workspaceID, []));
        }),
      );
      this.executions = responses.flat().sort((left, right) => right.id.localeCompare(left.id));
      this.executionById = Object.fromEntries(this.executions.map((execution) => [execution.id, execution]));
      return this.executions;
    },
    async loadExecution(workspaceId: string, executionId: string) {
      const response = await apiClient.get<{ execution: WorkflowExecution; steps: WorkflowExecutionStep[] }>(
        `/workspaces/${workspaceId}/executions/${executionId}`,
      );
      const execution = executionFromV1(response.data.execution, workspaceId, response.data.steps);
      this.executionById[executionId] = execution;
      this.lastExecution = execution;
      this.executions = [execution, ...this.executions.filter((item) => item.id !== executionId)];
      return execution;
    },
    workspaceIDFor(workflowId: string) {
      const workspaceID =
        this.workflowDetails[workflowId]?.workspaceId ||
        this.workflows.find((item) => item.id === workflowId)?.workspaceId;
      if (!workspaceID) throw new Error(`Workflow ${workflowId} is not loaded.`);
      return workspaceID;
    },
    requireWorkflow(workflowId: string) {
      const detail = this.workflowDetails[workflowId];
      if (detail) return detail;
      const summary = this.workflows.find((item) => item.id === workflowId);
      if (!summary) throw new Error(`Workflow ${workflowId} is not loaded.`);
      return workflowFromSummary(summary);
    },
    upsertWorkflow(workflow: Workflow) {
      const existing = this.workflows.find((item) => item.id === workflow.id);
      const summary = summarizeWorkflow(workflow, existing);
      this.workflows = upsertByID(this.workflows, summary);
      if (this.pageItems.some((item) => item.id === workflow.id)) this.pageItems = upsertByID(this.pageItems, summary);
      this.setWorkflowDetail(workflow);
    },
    setWorkflowDetail(workflow: Workflow) {
      this.workflowDetails[workflow.id] = workflow;
      const existing = this.workflows.find((item) => item.id === workflow.id);
      const summary = summarizeWorkflow(workflow, existing);
      this.workflows = upsertByID(this.workflows, summary);
      if (this.pageItems.some((item) => item.id === workflow.id)) this.pageItems = upsertByID(this.pageItems, summary);
    },
  },
});

export interface WorkflowDTO {
  id: string;
  currentDraftId: string;
  activeRevisionId?: string;
  latestCompilationId?: string;
  name: string;
  slug: string;
  description: string;
  status: string;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
  nodeCount: number;
  edgeCount: number;
}

export interface WorkflowDraftDTO {
  id: string;
  draftVersion: number;
  schemaVersion: string;
  graph: WorkflowGraphDraft;
  graphHash: string;
  updatedBy: string;
  updatedAt: string;
  lockVersion: number;
}

export interface WorkflowCreateResponseDTO {
  workflow: WorkflowDTO;
  draft: WorkflowDraftDTO;
}

interface WorkflowCompilationDTO {
  id: string;
  draftId: string;
  draftVersion: number;
  graphHash: string;
  compilerVersion: string;
  status: string;
  spec: WorkflowCompilation["spec"];
  plan: WorkflowCompilation["plan"];
  issues: WorkflowCompilationIssue[] | null;
  planHash: string;
  compiledBy: string;
  compiledAt: string;
}

interface WorkflowTrialDTO {
  id: string;
  compilationId: string;
  executionId: string;
  status: string;
  inputHash: string;
  startedBy: string;
  startedAt: string;
  finishedAt?: string;
}

interface WorkflowRevisionDTO {
  id: string;
  revisionNo: number;
  sourceCompilationId: string;
  draftSnapshot: WorkflowGraphDraft;
  specSnapshot: WorkflowRevision["spec"];
  planSnapshot: WorkflowRevision["plan"];
  planHash: string;
  status: string;
  publishNote: string;
  createdBy: string;
  createdAt: string;
  activatedAt?: string;
  retiredAt?: string;
}

interface WorkflowReadinessDTO {
  stage: string;
  canCompile: boolean;
  canTrial: boolean;
  canPublish: boolean;
  compilationId?: string;
  compilationCurrent: boolean;
  compilationValid: boolean;
  trialCurrent: boolean;
  trialSuccessful: boolean;
  published: boolean;
  activeRevisionId?: string;
  blockers: WorkflowReadiness["blockers"];
  updatedAt: string;
}

interface WorkflowPublishDTO {
  revision: WorkflowRevisionDTO;
  releaseId: string;
  releaseNo: number;
  trialId: string;
}

interface WorkflowActivationDTO {
  revision: WorkflowRevisionDTO;
  releaseId: string;
  releaseNo: number;
  eventType: string;
}

interface WorkflowRevisionDiffDTO {
  from: WorkflowRevisionDTO;
  to: WorkflowRevisionDTO;
  changes: { draft: boolean; spec: boolean; plan: boolean; planHash: boolean };
}

async function accessibleWorkspaceIDs() {
  const store = useWorkspaceStore();
  if (!store.items.length) await store.load();
  // ZKL-64: never fan out over a page of workspaces — only active context.
  const activeId = store.activeWorkspaceId || store.items[0]?.id || "";
  if (!activeId) {
    throw new Error(tt("workflow.noWorkspaceAvailable"));
  }
  return [activeId];
}

function workflowFromDTO(value: WorkflowDTO, workspaceId: string, readiness: WorkflowReadiness): Workflow {
  return {
    id: value.id,
    workspaceId,
    currentDraftId: value.currentDraftId,
    activeRevisionId: value.activeRevisionId,
    latestCompilationId: value.latestCompilationId,
    name: value.name,
    slug: value.slug,
    description: value.description,
    status: value.status === "DISABLED" ? "Disabled" : statusFromReadiness(readiness),
    readiness,
    createdBy: value.createdBy,
    updatedBy: value.updatedBy,
    createdAt: value.createdAt,
    updatedAt: value.updatedAt,
    lockVersion: value.lockVersion,
  };
}

function summaryFromDTO(
  value: WorkflowDTO,
  workspaceId: string,
  readiness: WorkflowReadiness,
  counts: Pick<WorkflowSummary, "nodeCount" | "edgeCount">,
  existing?: WorkflowSummary,
): WorkflowSummary {
  return summarizeWorkflow({ ...workflowFromDTO(value, workspaceId, readiness), ...counts }, existing);
}

function summarizeWorkflow(workflow: Workflow, existing?: WorkflowSummary): WorkflowSummary {
  return {
    id: workflow.id,
    workspaceId: workflow.workspaceId,
    currentDraftId: workflow.currentDraftId,
    activeRevisionId: workflow.activeRevisionId,
    latestCompilationId: workflow.latestCompilationId,
    workspaceName: existing?.workspaceName,
    name: workflow.name,
    slug: workflow.slug,
    description: workflow.description,
    status: workflow.status,
    nodeCount: workflow.nodeCount ?? existing?.nodeCount ?? 0,
    edgeCount: workflow.edgeCount ?? existing?.edgeCount ?? 0,
    lastValidationValid: workflow.lastValidationResult?.valid ?? existing?.lastValidationValid,
    lastValidationIssueCount: workflow.lastValidationResult?.issues.length ?? existing?.lastValidationIssueCount,
    lastTrialExecutionId: workflow.lastTrialExecutionId ?? existing?.lastTrialExecutionId,
    lastTrialStatus: workflow.lastTrialStatus ?? existing?.lastTrialStatus,
    readiness: workflow.readiness || existing?.readiness,
    createdBy: workflow.createdBy,
    updatedBy: workflow.updatedBy,
    createdAt: workflow.createdAt,
    updatedAt: workflow.updatedAt,
    lockVersion: workflow.lockVersion,
  };
}

function workflowFromSummary(value: WorkflowSummary): Workflow {
  return { ...value };
}

function draftFromDTO(
  value: WorkflowDraftDTO,
  workspaceId: string,
  workflowId: string,
  etag?: string,
): WorkflowDraftRecord {
  return {
    ...value,
    workflowId,
    // smart-dag.v2 / LLM graphs may omit ports; normalize before editor use
    graph: normalizeWorkflowGraphDraft(value.graph || emptyWorkflowGraph()),
    etag: etag || `"draft-${value.draftVersion}-${value.lockVersion}"`,
  };
}

function workflowDraftETag(value: Pick<WorkflowDraftRecord, "draftVersion" | "lockVersion">) {
  return `"draft-${value.draftVersion}-${value.lockVersion}"`;
}

function compilationFromDTO(value: WorkflowCompilationDTO, workflowId: string): WorkflowCompilation {
  return {
    ...value,
    workflowId,
    status: value.status as WorkflowCompilation["status"],
    issues: value.issues || [],
  };
}

function revisionFromDTO(value: WorkflowRevisionDTO, workflowId: string): WorkflowRevision {
  return {
    workflowId,
    revisionId: value.id,
    revisionNo: value.revisionNo,
    sourceCompilationId: value.sourceCompilationId,
    status: value.status === "PUBLISHED" ? "Published" : value.status === "DISABLED" ? "Disabled" : "Review",
    draft: value.draftSnapshot,
    spec: value.specSnapshot,
    plan: value.planSnapshot,
    createdAt: value.createdAt,
    createdBy: value.createdBy,
    publishNote: value.publishNote,
    planHash: value.planHash,
    activatedAt: value.activatedAt,
    retiredAt: value.retiredAt,
  };
}

function readinessFromDTO(value: WorkflowReadinessDTO): WorkflowReadiness {
  const stage = normalizeReadinessStage(value.stage);
  return {
    stage,
    canCompile: value.canCompile,
    canTrial: value.canTrial,
    canValidate: value.canCompile,
    canTrialRun: value.canTrial,
    canPublish: value.canPublish,
    hasDraft: stage !== "DraftMissing",
    compilationId: value.compilationId,
    compilationCurrent: value.compilationCurrent,
    compilationValid: value.compilationValid,
    trialCurrent: value.trialCurrent,
    trialSuccessful: value.trialSuccessful,
    published: value.published,
    activeRevisionId: value.activeRevisionId,
    latestRevisionId: value.activeRevisionId,
    blockers: value.blockers || [],
    updatedAt: value.updatedAt,
  };
}

function normalizeReadinessStage(value: string): WorkflowReadiness["stage"] {
  const values: Record<string, WorkflowReadiness["stage"]> = {
    DRAFT_MISSING: "DraftMissing",
    COMPILE_REQUIRED: "CompileRequired",
    COMPILE_FAILED: "CompileFailed",
    TRIAL_REQUIRED: "TrialRequired",
    PUBLISH_READY: "PublishReady",
    PUBLISHED: "Published",
    DISABLED: "Disabled",
  };
  return values[value] || (value as WorkflowReadiness["stage"]);
}

function emptyReadiness(): WorkflowReadiness {
  return {
    stage: "CompileRequired",
    canCompile: true,
    canTrial: false,
    canValidate: true,
    canTrialRun: false,
    canPublish: false,
    hasDraft: true,
    compilationCurrent: false,
    compilationValid: false,
    trialCurrent: false,
    trialSuccessful: false,
    published: false,
    blockers: [],
    updatedAt: new Date(0).toISOString(),
  };
}

function statusFromReadiness(readiness: WorkflowReadiness): WorkflowStatus {
  if (readiness.stage === "Disabled") return "Disabled";
  if (readiness.published || readiness.stage === "Published") return "Published";
  if (readiness.stage === "PublishReady") return "Review";
  return "Draft";
}

function executionFromTrial(value: WorkflowTrialDTO, workflow: Workflow): Execution {
  const status: Execution["status"] =
    value.status === "SUCCEEDED" ? "Success" : value.status === "RUNNING" ? "Running" : "Failed";
  return {
    id: value.executionId,
    workflowId: workflow.id,
    workflowVersion: `draft-${workflow.currentDraftId}`,
    workspaceId: workflow.workspaceId,
    trigger: "Trial (sandbox)",
    userId: value.startedBy,
    traceId: value.id,
    status,
    durationMs: 0,
    inputSummary: value.inputHash,
    outputSummary: status === "Success" ? "Workflow trial (sandbox) succeeded" : value.status,
    rawPayloadObjectAddress: "",
    steps: [],
  };
}

function executionFromV1(value: WorkflowExecution, workspaceId: string, steps: WorkflowExecutionStep[]): Execution {
  const status: Execution["status"] =
    value.status === "SUCCEEDED"
      ? "Success"
      : value.status === "RUNNING" || value.status === "PENDING"
        ? "Running"
        : value.status === "WAITING_CONFIRMATION"
          ? "Approval"
          : "Failed";
  const startedAt = Date.parse(value.startedAt);
  const finishedAt = value.finishedAt ? Date.parse(value.finishedAt) : Number.NaN;
  return {
    id: value.id,
    workflowId: value.workflowId,
    workflowVersion: value.revisionId || "",
    workspaceId,
    trigger: value.triggerType,
    userId: value.triggeredById,
    traceId: value.traceId,
    status,
    durationMs: Number.isFinite(startedAt) && Number.isFinite(finishedAt) ? Math.max(0, finishedAt - startedAt) : 0,
    inputSummary: runtimeSummaryText(value.inputSummary),
    outputSummary: runtimeSummaryText(value.outputSummary),
    errorMessage: value.errorCode,
    rawPayloadObjectAddress: "",
    steps: steps.map((step) => ({
      id: step.id,
      executionId: value.id,
      name: step.nodeType || step.nodeId,
      nodeId: step.nodeId,
      nodeType: step.nodeType,
      status:
        step.status === "SUCCEEDED"
          ? "Passed"
          : step.status === "RUNNING"
            ? "Running"
            : step.status === "WAITING_CONFIRMATION"
              ? "WaitingApproval"
              : step.status === "SKIPPED"
                ? "Skipped"
                : step.status === "CANCELLED"
                  ? "Cancelled"
                  : "Failed",
      inputSummary: runtimeSummaryText(step.inputSummary),
      outputSummary: runtimeSummaryText(step.outputSummary),
      errorMessage: step.errorCode,
      durationMs: step.finishedAt ? Math.max(0, Date.parse(step.finishedAt) - Date.parse(step.startedAt)) : 0,
    })),
  };
}

function runtimeSummaryText(value: unknown) {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value ?? {});
  } catch {
    return "{}";
  }
}

function emptyWorkflowGraph(): WorkflowGraphDraft {
  return { schemaVersion: "workflow.graph.v1", nodes: [], edges: [], viewport: { x: 0, y: 0, zoom: 1 }, ui: {} };
}

function filterWorkflows(items: WorkflowSummary[], query: string, status?: WorkflowStatus) {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((workflow) => {
    if (status && workflow.status !== status) return false;
    if (!needle) return true;
    return [workflow.name, workflow.slug, workflow.description, workflow.status].some((value) =>
      value.toLocaleLowerCase().includes(needle),
    );
  });
}

function sortWorkflows(items: WorkflowSummary[], sortBy?: string, sortOrder?: "asc" | "desc") {
  if (!sortBy || !sortOrder) return items;
  const allowed = new Set(["name", "workspace", "nodeCount", "status", "updatedAt", "createdAt"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const key = sortBy === "workspace" ? "workspaceName" : sortBy;
    const leftValue = left[key as keyof WorkflowSummary];
    const rightValue = right[key as keyof WorkflowSummary];
    const comparison =
      typeof leftValue === "number" && typeof rightValue === "number"
        ? leftValue - rightValue
        : String(leftValue || "").localeCompare(String(rightValue || ""), "zh-Hans");
    return sortOrder === "asc" ? comparison : -comparison;
  });
}

function slugify(value: string) {
  return (
    value
      .trim()
      .toLocaleLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || `workflow-${Date.now()}`
  );
}

function upsertByID<T extends { id: string }>(items: T[], replacement: T) {
  return items.some((item) => item.id === replacement.id)
    ? items.map((item) => (item.id === replacement.id ? replacement : item))
    : [replacement, ...items];
}

function upsertRevision(items: WorkflowRevision[], revision: WorkflowRevision) {
  const next = items.some((item) => item.revisionId === revision.revisionId)
    ? items.map((item) => (item.revisionId === revision.revisionId ? revision : item))
    : [revision, ...items];
  return next.sort((left, right) => right.revisionNo - left.revisionNo);
}
