import { expect, type Locator, type Page, type Route } from "@playwright/test";

import { createDefaultWorkflowGraphDraft } from "../../src/utils/workflow-graph";
import type {
  Agent,
  CompiledExecutionPlan,
  Execution,
  ExecutionStatus,
  ModelApiConfig,
  Tool,
  ToolRequestParam,
  ToolResponseField,
  Workspace,
  Workflow,
  WorkflowCompilation,
  WorkflowCompilationIssue,
  WorkflowDraftRecord,
  WorkflowGraphDraft,
  WorkflowReadiness,
  WorkflowReadinessStage,
  WorkflowRevision,
  WorkflowStatus,
  WorkflowSummary,
  WorkflowValidationResult,
} from "../../src/types/domain";

const environment = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env;

export const WORKFLOW_E2E = {
  baseURL: environment?.E2E_BASE_URL || "http://127.0.0.1:4173",
  fixedTime: "2026-07-18T09:15:00+08:00",
  workflowName: "E2E 条件分支编排",
  toolOptionLabel: "取消订单工具 · tool.cancel-order",
  revisionId: "rev-001",
} as const;

const FIXTURE_USER = {
  id: "user-chen-ops",
  username: "chen.ops",
  displayName: "Chen Ops",
  status: "ACTIVE",
  platformRole: "PLATFORM_ADMIN",
  locale: "zh-CN",
  timezone: "Asia/Singapore",
  createdAt: "2026-07-03T00:00:00.000Z",
  updatedAt: "2026-07-03T00:00:00.000Z",
  lockVersion: 1,
} as const;

const FIXTURE_TOKEN = "actweave-e2e-token";
const FIXTURE_WORKSPACE_ID = "ws-ops";
const FIXTURE_AGENT_ID = "agent-ops";
const FIXTURE_TOOL_ID = "tool.cancel-order";
const FIXTURE_MODEL_CONFIG_ID = "model-ops";
const FIXTURE_START_TIME = Date.parse("2026-07-03T09:15:00+08:00");

type WorkflowRecord = {
  workflow: Workflow;
  draft: WorkflowDraftRecord;
  compilation: WorkflowCompilation;
  revisions: WorkflowRevision[];
  executions: Execution[];
};

type WorkflowFixtureState = {
  workspaces: Workspace[];
  agents: Agent[];
  tools: Tool[];
  modelConfig: ModelApiConfig;
  workflows: Map<string, WorkflowRecord>;
  nextWorkflowId: number;
  nextDraftVersion: number;
  nextExecutionId: number;
  nextRevisionId: number;
  nextTimestampStep: number;
};

export async function installWorkflowApiMocks(page: Page) {
  const state = createWorkflowFixtureState();
  await page.route("**/api/**", async (route) => {
    await fulfillWorkflowRoute(route, state);
  });
  return state;
}

export async function installStableUi(page: Page) {
  await page.addStyleTag({
    content: `
      *, *::before, *::after {
        animation: none !important;
        transition: none !important;
        caret-color: transparent !important;
      }
    `,
  });
}

export async function loginAndOpenWorkflow(page: Page) {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("actweave.locale", "zh-CN");
    } catch {
      // ignore
    }
  });
  await page.goto("/login");
  const zh = page.locator('[data-testid="login-lang-zh-CN"]');
  if (await zh.isVisible().catch(() => false)) {
    await zh.click();
  }
  await expect(page.getByRole("heading", { name: "登录", exact: true })).toBeVisible({
    timeout: 15_000,
  });
  await page.locator('input[autocomplete="username"]').fill("chen.ops");
  await page.locator('input[autocomplete="current-password"]').fill("fixture-password");
  await page.getByRole("button", { name: /^登录$/ }).click();
  await expect(page).toHaveURL(/\/overview$/);
  await page.locator(".fluid-trigger").click();
  const workflowLink = page.locator('.fluid-content a[href="/workflow"]').first();
  await expect(workflowLink).toBeVisible();
  await workflowLink.click();
  await expect(page).toHaveURL(/\/workflow$/);
  await expect(page.getByRole("heading", { name: "编排", exact: true })).toBeVisible();
  await expect(page.locator(".workflow-management-list")).toBeVisible();
}

export async function selectAppOption(page: Page, root: Locator, optionText: string) {
  await root.first().click({ force: true });
  const option = page.locator(".app-select-popper .el-select-dropdown__item").filter({ hasText: optionText }).first();
  await expect(option).toBeVisible();
  await option.click();
}

export async function connectWorkflowNodes(page: Page, sourceNodeId: string, targetNodeId: string) {
  const sourceHandle = page.locator(`[data-node-id="${sourceNodeId}"] .workflow-flow-handle.output`).first();
  const targetHandle = page.locator(`[data-node-id="${targetNodeId}"] .workflow-flow-handle.input`).first();
  await expect(sourceHandle).toBeVisible();
  await expect(targetHandle).toBeVisible();
  const sourceBox = await sourceHandle.boundingBox();
  const targetBox = await targetHandle.boundingBox();
  if (!sourceBox || !targetBox) {
    throw new Error(`Unable to connect ${sourceNodeId} -> ${targetNodeId}: handle bounds missing`);
  }
  const sx = sourceBox.x + sourceBox.width / 2;
  const sy = sourceBox.y + sourceBox.height / 2;
  const tx = targetBox.x + targetBox.width / 2;
  const ty = targetBox.y + targetBox.height / 2;
  await page.mouse.move(sx, sy);
  await page.mouse.down();
  // Multi-hop drag improves Vue Flow connection hit-testing under production CSS.
  await page.mouse.move(sx + (tx - sx) * 0.35, sy + (ty - sy) * 0.35, { steps: 12 });
  await page.mouse.move(sx + (tx - sx) * 0.7, sy + (ty - sy) * 0.7, { steps: 12 });
  await page.mouse.move(tx, ty, { steps: 16 });
  await page.mouse.up();
  await expect(page.locator(`[data-id="${edgeId(sourceNodeId, targetNodeId)}"]`)).toBeVisible({ timeout: 10_000 });
}

export async function selectWorkflowEdge(page: Page, sourceNodeId: string, targetNodeId: string) {
  const edge = edgeId(sourceNodeId, targetNodeId);
  const target = page
    .getByRole("group", { name: new RegExp(`^Edge from ${sourceNodeId} to ${targetNodeId}$`) })
    .first();
  await expect(target).toBeVisible();
  await target.click({ force: true });
  await expect(page.locator(".workflow-edge-inspector")).toContainText(edge);
}

export function edgeId(sourceNodeId: string, targetNodeId: string, sourcePort = "output", targetPort = "input") {
  return `edge-${sourceNodeId}-${targetNodeId}-${sourcePort}-${targetPort}`;
}

function createWorkflowFixtureState(): WorkflowFixtureState {
  return {
    workspaces: [createWorkspace()],
    agents: [createAgent()],
    tools: [createPublishedTool()],
    modelConfig: createModelConfig(),
    workflows: new Map<string, WorkflowRecord>(),
    nextWorkflowId: 1,
    nextDraftVersion: 1,
    nextExecutionId: 1,
    nextRevisionId: 1,
    nextTimestampStep: 0,
  };
}

async function fulfillWorkflowRoute(route: Route, state: WorkflowFixtureState) {
  const request = route.request();
  const url = new URL(request.url());
  const pathname = url.pathname;
  const method = request.method();

  if (pathname === "/api/v1/auth/login" && method === "POST") {
    await json(route, 200, {
      accessToken: FIXTURE_TOKEN,
      tokenType: "Bearer",
      expiresIn: 900,
      mustChangePassword: false,
      user: { ...FIXTURE_USER },
    });
    return;
  }

  if (pathname === "/api/v1/auth/refresh" && method === "POST") {
    await json(route, 401, { error: { code: "UNAUTHENTICATED", message: "No fixture refresh session." } });
    return;
  }

  if (pathname === "/api/v1/users/me" && method === "GET") {
    await json(route, 200, { ...FIXTURE_USER });
    return;
  }

  if (pathname === "/api/v1/workspaces" && method === "GET") {
    const items = state.workspaces.map(workspaceDTO);
    await json(route, 200, {
      items,
      nextCursor: "",
      page: 1,
      pageSize: items.length || 50,
      total: items.length,
      summary: { total: items.length, active: items.length, sandbox: 0, production: items.length },
    });
    return;
  }

  const workspaceRoot = `/api/v1/workspaces/${FIXTURE_WORKSPACE_ID}`;

  if (pathname === `${workspaceRoot}/members` && method === "GET") {
    await json(route, 200, { items: [] });
    return;
  }

  if (pathname === `${workspaceRoot}/model-configs` && method === "GET") {
    await json(route, 200, { items: [modelConfigDTO(state.modelConfig)] });
    return;
  }

  if (pathname === `${workspaceRoot}/agents` && method === "GET") {
    await json(route, 200, { items: state.agents.map(agentDTO) });
    return;
  }

  if (pathname === `${workspaceRoot}/tools` && method === "GET") {
    await json(route, 200, { items: state.tools.map(toolDTO) });
    return;
  }

  const toolVersionsMatch = pathname.match(/^\/api\/v1\/workspaces\/[^/]+\/tools\/([^/]+)\/versions$/);
  if (toolVersionsMatch?.[1] && method === "GET") {
    const tool = state.tools.find((item) => item.id === toolVersionsMatch[1]);
    await json(route, tool ? 200 : 404, tool ? { items: [toolVersionDTO(tool)] } : { error: { code: "NOT_FOUND" } });
    return;
  }

  if (pathname === `${workspaceRoot}/providers` && method === "GET") {
    await json(route, 200, { items: [] });
    return;
  }

  if (pathname === `${workspaceRoot}/executions` && method === "GET") {
    await json(route, 200, { items: [] });
    return;
  }

  if (pathname === `${workspaceRoot}/workflows` && method === "GET") {
    await json(route, 200, { items: Array.from(state.workflows.values()).map(workflowDTO) });
    return;
  }

  if (pathname === `${workspaceRoot}/workflows` && method === "POST") {
    const payload = request.postDataJSON() as Partial<Workflow>;
    const workflowId = `wf-e2e-${String(state.nextWorkflowId).padStart(3, "0")}`;
    state.nextWorkflowId += 1;
    const record = createWorkflow(payload, workflowId, state);
    state.workflows.set(workflowId, record);
    await json(route, 201, { workflow: workflowDTO(record), draft: draftDTO(record.draft) });
    return;
  }

  const readinessMatch = pathname.match(/^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)\/readiness$/);
  if (readinessMatch?.[1] && method === "GET") {
    const record = requireWorkflow(state, readinessMatch[1]);
    await json(route, 200, readinessDTO(record));
    return;
  }

  const draftMatch = pathname.match(/^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)\/draft$/);
  if (draftMatch?.[1] && method === "GET") {
    const record = requireWorkflow(state, draftMatch[1]);
    await json(route, 200, draftDTO(record.draft), {
      ETag: `"draft-${record.draft.draftVersion}-${record.draft.lockVersion}"`,
    });
    return;
  }

  if (draftMatch?.[1] && method === "PUT") {
    const record = requireWorkflow(state, draftMatch[1]);
    const payload = request.postDataJSON() as { draftVersion: number; graph: WorkflowGraphDraft };
    saveWorkflowDraftRecord(state, record, payload.graph);
    await json(route, 200, draftDTO(record.draft), {
      ETag: `"draft-${record.draft.draftVersion}-${record.draft.lockVersion}"`,
    });
    return;
  }

  const compileMatch = pathname.match(/^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)\/draft:compile$/);
  if (compileMatch?.[1] && method === "POST") {
    const record = requireWorkflow(state, compileMatch[1]);
    record.compilation = compileWorkflowGraph(record.workflow, record.draft, state.tools);
    record.workflow.latestCompilationId = record.compilation.id;
    record.workflow.lastValidationResult = compilationToValidation(record.compilation);
    record.workflow.readiness = computeReadiness(record.workflow, record.compilation, record.revisions);
    await json(route, 201, compilationDTO(record.compilation));
    return;
  }

  const revisionsMatch = pathname.match(/^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)\/revisions$/);
  if (revisionsMatch?.[1] && method === "GET") {
    const record = requireWorkflow(state, revisionsMatch[1]);
    await json(route, 200, { items: record.revisions.map(revisionDTO) });
    return;
  }

  const trialRunMatch = pathname.match(
    /^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)\/compilations\/([^/]+):trial$/,
  );
  if (trialRunMatch?.[1] && method === "POST") {
    const record = requireWorkflow(state, trialRunMatch[1]);
    const execution = runWorkflowTrialRecord(state, record);
    await json(route, 200, trialDTO(record, execution));
    return;
  }

  const publishMatch = pathname.match(
    /^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)\/compilations\/([^/]+):publish$/,
  );
  if (publishMatch?.[1] && method === "POST") {
    const record = requireWorkflow(state, publishMatch[1]);
    const revision = publishWorkflowRecord(state, record);
    await json(route, 201, {
      revision: revisionDTO(revision),
      releaseId: `release-${revision.revisionNo}`,
      releaseNo: revision.revisionNo,
      trialId: record.workflow.lastTrialExecutionId,
    });
    return;
  }

  const workflowDetailMatch = pathname.match(/^\/api\/v1\/workspaces\/[^/]+\/workflows\/([^/]+)$/);
  if (workflowDetailMatch?.[1] && method === "GET") {
    const record = requireWorkflow(state, workflowDetailMatch[1]);
    await json(route, 200, workflowDTO(record));
    return;
  }

  await json(route, 404, { error: { code: "NOT_FOUND", message: `Unhandled mock route: ${method} ${pathname}` } });
}

function workspaceDTO(workspace: Workspace) {
  return {
    id: workspace.id,
    slug: workspace.slug || workspace.name,
    displayName: workspace.displayName,
    mode: workspace.mode === "Sandbox" ? "SANDBOX" : "PRODUCTION",
    status: workspace.status === "Disabled" ? "DISABLED" : "ACTIVE",
    ownerUserId: workspace.ownerUserId,
    defaultAgentId: workspace.defaultAgentId,
    defaultModelConfigId: workspace.defaultModelConfigId,
    settings: workspace.settings || {},
    // ZKL-64 D1-A: frontend can() uses currentUserRole from workspace DTO (not members list).
    currentUserRole: "OWNER",
    createdBy: workspace.createdBy,
    updatedBy: workspace.updatedBy,
    createdAt: workspace.createdAt,
    updatedAt: workspace.updatedAt,
    lockVersion: workspace.lockVersion,
  };
}

function agentDTO(agent: Agent) {
  return {
    id: agent.id,
    name: agent.name,
    roleDescription: agent.roleDescription,
    currentPromptRevisionId: agent.currentPromptRevisionId,
    modelConfigId: agent.modelConfigId,
    isDefault: agent.isDefault,
    status: agent.status,
    toolsCount: agent.toolsCount,
    workflowsCount: agent.workflowsCount,
    createdBy: agent.createdBy,
    updatedBy: agent.updatedBy,
    createdAt: agent.createdAt,
    updatedAt: agent.updatedAt,
    lockVersion: agent.lockVersion,
  };
}

function modelConfigDTO(config: ModelApiConfig) {
  return { ...config };
}

function toolDTO(tool: Tool) {
  return {
    id: tool.id,
    providerId: tool.providerId,
    defaultConnectionId: tool.defaultConnectionId,
    name: tool.name,
    slug: tool.slug,
    description: tool.description,
    status: tool.capabilityStatus || "ACTIVE",
    activeReleaseId: tool.activeReleaseId,
    createdBy: tool.createdBy,
    updatedBy: tool.updatedBy,
    createdAt: tool.createdAt,
    updatedAt: tool.updatedAt,
    lockVersion: tool.lockVersion,
  };
}

function toolVersionDTO(tool: Tool) {
  return {
    id: "tool-version-cancel-order-1",
    versionNo: 1,
    lifecycleStatus: "PUBLISHED",
    executorType: "HTTP",
    defaultConnectionId: tool.defaultConnectionId,
    actionSchemaVersion: "http.v1",
    actionConfig: { method: "POST", path: "/orders/{orderId}:cancel" },
    inputSchema: {
      type: "object",
      required: tool.requestParams.filter((item) => item.required).map((item) => item.name),
      properties: Object.fromEntries(
        tool.requestParams.map((item) => [item.name, { type: item.type, description: item.description }]),
      ),
    },
    outputSchema: {
      type: "object",
      properties: Object.fromEntries(
        tool.responseFields.map((item) => [item.name, { type: item.type, description: item.description }]),
      ),
    },
    errorMappings: {},
    runtimePolicy: { ...tool.runtimePolicy },
    riskLevel: "MEDIUM",
    sideEffectLevel: "WRITE",
    requiresConfirmation: true,
    checksum: "sha256:tool-version-cancel-order-1",
    createdBy: tool.createdBy,
    updatedBy: tool.updatedBy,
    publishedAt: tool.updatedAt,
    lockVersion: 1,
  };
}

function workflowDTO(record: WorkflowRecord) {
  const workflow = record.workflow;
  return {
    id: workflow.id,
    currentDraftId: workflow.currentDraftId,
    activeRevisionId: workflow.activeRevisionId || undefined,
    latestCompilationId: workflow.latestCompilationId,
    name: workflow.name,
    slug: workflow.slug,
    description: workflow.description,
    status: workflow.status === "Disabled" ? "DISABLED" : "ACTIVE",
    createdBy: workflow.createdBy,
    updatedBy: workflow.updatedBy,
    createdAt: workflow.createdAt,
    updatedAt: workflow.updatedAt,
    lockVersion: workflow.lockVersion,
    nodeCount: record.draft.graph.nodes.length,
    edgeCount: record.draft.graph.edges.length,
  };
}

function draftDTO(draft: WorkflowDraftRecord) {
  return {
    id: draft.id,
    draftVersion: draft.draftVersion,
    schemaVersion: draft.schemaVersion,
    graph: cloneGraph(draft.graph),
    graphHash: draft.graphHash,
    updatedBy: draft.updatedBy,
    updatedAt: draft.updatedAt,
    lockVersion: draft.lockVersion,
  };
}

function compilationDTO(compilation: WorkflowCompilation) {
  return {
    id: compilation.id,
    draftId: compilation.draftId,
    draftVersion: compilation.draftVersion,
    graphHash: compilation.graphHash,
    compilerVersion: compilation.compilerVersion,
    status: compilation.status,
    spec: compilation.spec,
    plan: compilation.plan,
    issues: compilation.issues,
    planHash: compilation.planHash,
    compiledBy: compilation.compiledBy,
    compiledAt: compilation.compiledAt,
  };
}

function readinessDTO(record: WorkflowRecord) {
  const readiness = computeReadiness(record.workflow, record.compilation, record.revisions);
  const stages: Record<string, string> = {
    DraftMissing: "DRAFT_MISSING",
    CompileRequired: "COMPILE_REQUIRED",
    CompileFailed: "COMPILE_FAILED",
    TrialRequired: "TRIAL_REQUIRED",
    PublishReady: "PUBLISH_READY",
    Published: "PUBLISHED",
    Disabled: "DISABLED",
  };
  return {
    stage: stages[readiness.stage] || readiness.stage,
    canCompile: readiness.canCompile,
    canTrial: readiness.canTrial,
    canPublish: readiness.canPublish,
    compilationId: record.compilation.id,
    compilationCurrent: readiness.compilationCurrent,
    compilationValid: readiness.compilationValid,
    trialCurrent: readiness.trialCurrent,
    trialSuccessful: readiness.trialSuccessful,
    published: readiness.published,
    activeRevisionId: readiness.activeRevisionId,
    blockers: readiness.blockers,
    updatedAt: readiness.updatedAt,
  };
}

function trialDTO(record: WorkflowRecord, execution: Execution) {
  return {
    id: `trial-${execution.id}`,
    compilationId: record.compilation.id,
    executionId: execution.id,
    status: "SUCCEEDED",
    inputHash: "sha256:e2e-trial-input",
    startedBy: FIXTURE_USER.id,
    startedAt: nextTimestampFromDraft(record.compilation.compiledAt),
    finishedAt: nextTimestampFromDraft(nextTimestampFromDraft(record.compilation.compiledAt)),
  };
}

function revisionDTO(revision: WorkflowRevision) {
  return {
    id: revision.revisionId,
    revisionNo: revision.revisionNo,
    sourceCompilationId: revision.sourceCompilationId,
    draftSnapshot: cloneGraph(revision.draft),
    specSnapshot: revision.spec,
    planSnapshot: revision.plan,
    planHash: revision.planHash,
    status: "PUBLISHED",
    publishNote: revision.publishNote,
    createdBy: revision.createdBy,
    createdAt: revision.createdAt,
    activatedAt: revision.activatedAt,
    retiredAt: revision.retiredAt,
  };
}

function createWorkspace(): Workspace {
  return {
    id: FIXTURE_WORKSPACE_ID,
    name: "ops",
    slug: "ops",
    displayName: "Ops Center",
    ownerUserId: FIXTURE_USER.id,
    owner: "Ops Platform",
    mode: "PRODUCTION",
    status: "ACTIVE",
    defaultAgentId: FIXTURE_AGENT_ID,
    defaultModelConfigId: FIXTURE_MODEL_CONFIG_ID,
    modelConfigId: FIXTURE_MODEL_CONFIG_ID,
    settings: { region: "sg" },
    createdBy: FIXTURE_USER.id,
    updatedBy: FIXTURE_USER.id,
    createdAt: "2026-07-03T00:00:00.000Z",
    updatedAt: "2026-07-03T00:00:00.000Z",
    lockVersion: 1,
    healthScore: 99.2,
    toolCount: 1,
    workflowCount: 0,
    agentCount: 1,
  };
}

function createAgent(): Agent {
  return {
    id: FIXTURE_AGENT_ID,
    workspaceId: FIXTURE_WORKSPACE_ID,
    name: "Ops Executor",
    roleDescription: "Executes workflow drafts for order operations",
    modelConfigId: FIXTURE_MODEL_CONFIG_ID,
    systemPrompt: "Coordinate workflows for the operations team.",
    isDefault: true,
    status: "ACTIVE",
    toolsCount: 1,
    workflowsCount: 0,
    createdBy: FIXTURE_USER.id,
    updatedBy: FIXTURE_USER.id,
    createdAt: "2026-07-03T00:00:00.000Z",
    updatedAt: "2026-07-03T00:00:00.000Z",
    lockVersion: 1,
  };
}

function createModelConfig(): ModelApiConfig {
  return {
    id: FIXTURE_MODEL_CONFIG_ID,
    name: "Ops Model",
    provider: "OpenAI",
    apiBase: "https://api.openai.com/v1",
    modelName: "gpt-5",
    credentialConfigured: true,
    credentialSecretId: "secret-model-ops",
    options: {},
    lastLatencyMs: 420,
    status: "VERIFIED",
    lastVerifiedAt: "2026-07-03T00:00:00.000Z",
    createdBy: FIXTURE_USER.id,
    updatedBy: FIXTURE_USER.id,
    createdAt: "2026-07-03T00:00:00.000Z",
    updatedAt: "2026-07-03T00:00:00.000Z",
    lockVersion: 1,
  };
}

function createPublishedTool(): Tool {
  const requestParams: ToolRequestParam[] = [
    {
      location: "body",
      name: "orderId",
      type: "string",
      required: true,
      description: "需要取消的订单 ID",
    },
  ];
  const responseFields: ToolResponseField[] = [
    { name: "status", type: "string", description: "取消状态" },
    { name: "cancelledAt", type: "string", description: "取消时间" },
  ];

  return {
    id: FIXTURE_TOOL_ID,
    workspaceId: FIXTURE_WORKSPACE_ID,
    providerId: "provider-order-api",
    connectionId: "conn-order-api",
    defaultConnectionId: "conn-order-api",
    name: "取消订单工具",
    slug: "cancel-order",
    protocol: "HTTP",
    actionConfig: {},
    actionConfigSchemaVersion: "tool.http.v1",
    description: "取消指定订单",
    status: "Published",
    capabilityStatus: "ACTIVE",
    activeReleaseId: "tool-release-001",
    versions: [],
    requestParams,
    responseFields,
    errorMappings: [],
    runtimePolicy: {
      timeoutMs: 12000,
      retryCount: 0,
      backoffPolicy: "none",
      idempotencyPolicy: "safe",
      rateLimitPolicy: "default",
    },
    createdBy: FIXTURE_USER.id,
    updatedBy: FIXTURE_USER.id,
    createdAt: "2026-07-03T00:00:00.000Z",
    updatedAt: "2026-07-03T00:00:00.000Z",
    lockVersion: 1,
  };
}

function createWorkflow(payload: Partial<Workflow>, workflowId: string, state: WorkflowFixtureState): WorkflowRecord {
  const graph = createDefaultWorkflowGraphDraft();
  const draftVersion = nextDraftVersion(state);
  const updatedAt = nextTimestamp(state);
  const draft: WorkflowDraftRecord = {
    id: `draft-${workflowId}`,
    workflowId,
    draftVersion,
    schemaVersion: graph.schemaVersion,
    graph,
    graphHash: `sha256:graph-${draftVersion}`,
    updatedBy: FIXTURE_USER.id,
    updatedAt,
    lockVersion: 1,
    etag: `"draft-${draftVersion}-1"`,
  };
  const workflow: Workflow = {
    id: workflowId,
    workspaceId: payload.workspaceId || FIXTURE_WORKSPACE_ID,
    currentDraftId: draft.id,
    name: payload.name || WORKFLOW_E2E.workflowName,
    slug: payload.slug || "e2e-condition-workflow",
    description: payload.description || "校验 Tool 映射、条件分支和发布闭环",
    status: (payload.status as WorkflowStatus | undefined) || "Draft",
    nodeCount: graph.nodes.length,
    edgeCount: graph.edges.length,
    activeRevisionId: "",
    lastTrialExecutionId: "",
    lastTrialStatus: undefined,
    lastValidationResult: undefined,
    einoDagMapping: undefined,
    readiness: undefined,
    createdBy: FIXTURE_USER.id,
    updatedBy: FIXTURE_USER.id,
    createdAt: updatedAt,
    updatedAt,
    lockVersion: 1,
  };
  const compilation = compileWorkflowGraph(workflow, draft, state.tools);
  workflow.latestCompilationId = compilation.id;
  workflow.lastValidationResult = compilationToValidation(compilation);
  workflow.readiness = computeReadiness(workflow, compilation, []);
  return {
    workflow,
    draft,
    compilation,
    revisions: [],
    executions: [],
  };
}

function saveWorkflowDraftRecord(state: WorkflowFixtureState, record: WorkflowRecord, graph: WorkflowGraphDraft) {
  const draftVersion = nextDraftVersion(state);
  record.draft = {
    id: record.draft.id,
    workflowId: record.workflow.id,
    draftVersion,
    schemaVersion: graph.schemaVersion,
    graph: cloneGraph(graph),
    graphHash: `sha256:graph-${draftVersion}`,
    updatedBy: FIXTURE_USER.id,
    updatedAt: nextTimestamp(state),
    lockVersion: record.draft.lockVersion + 1,
    etag: `"draft-${draftVersion}-${record.draft.lockVersion + 1}"`,
  };
  record.workflow.nodeCount = record.draft.graph.nodes.length;
  record.workflow.edgeCount = record.draft.graph.edges.length;
  record.workflow.lastTrialExecutionId = "";
  record.workflow.lastTrialStatus = undefined;
  record.workflow.updatedAt = record.draft.updatedAt;
  record.workflow.updatedBy = FIXTURE_USER.id;
  record.workflow.lockVersion += 1;
  record.compilation = compileWorkflowGraph(record.workflow, record.draft, state.tools);
  record.workflow.latestCompilationId = record.compilation.id;
  record.workflow.lastValidationResult = compilationToValidation(record.compilation);
  record.workflow.readiness = computeReadiness(record.workflow, record.compilation, record.revisions);
}

function runWorkflowTrialRecord(state: WorkflowFixtureState, record: WorkflowRecord) {
  const executionId = `exec-${String(state.nextExecutionId).padStart(3, "0")}`;
  state.nextExecutionId += 1;
  const conditionNodeId = record.draft.graph.nodes.find((node) => node.type === "Condition")?.id || "condition-1";
  const toolNodeId = record.draft.graph.nodes.find((node) => node.type === "Tool")?.id || "tool-1";
  const endNodeId = record.draft.graph.nodes.find((node) => node.type === "End" && node.id !== "start")?.id || "end";
  const execution: Execution = {
    id: executionId,
    workflowId: record.workflow.id,
    workflowVersion: record.workflow.currentDraftId,
    workspaceId: record.workflow.workspaceId,
    trigger: "Trial run",
    userId: FIXTURE_USER.id,
    traceId: `trace-${executionId}`,
    status: "Success",
    durationMs: 1840,
    inputSummary: "{}",
    outputSummary: '{"status":"cancelled"}',
    errorMessage: "",
    rawPayloadObjectAddress: `s3://fixtures/${executionId}.json`,
    steps: [
      buildExecutionStep(executionId, "step-start", "Start", "Start", "Passed", "-", "draft input ready", 42),
      buildExecutionStep(
        executionId,
        "step-tool",
        toolNodeId,
        "Tool",
        "Passed",
        "orderId=ORDER-10001",
        'status="cancelled"',
        628,
      ),
      buildExecutionStep(
        executionId,
        "step-condition",
        conditionNodeId,
        "Condition",
        "Passed",
        "branch=true",
        "default route kept for fallback",
        94,
      ),
      buildExecutionStep(
        executionId,
        "step-end",
        endNodeId,
        "End",
        "Passed",
        'status="cancelled"',
        'result="published-ready"',
        37,
      ),
    ],
  };
  record.executions = [execution, ...record.executions];
  record.workflow.lastTrialExecutionId = execution.id;
  record.workflow.lastTrialStatus = execution.status as ExecutionStatus;
  record.workflow.readiness = computeReadiness(record.workflow, record.compilation, record.revisions);
  return execution;
}

function publishWorkflowRecord(state: WorkflowFixtureState, record: WorkflowRecord) {
  const revisionId = `rev-${String(state.nextRevisionId).padStart(3, "0")}`;
  state.nextRevisionId += 1;
  const createdAt = nextTimestamp(state);
  const revision: WorkflowRevision = {
    workflowId: record.workflow.id,
    revisionId,
    revisionNo: state.nextRevisionId - 1,
    sourceCompilationId: record.compilation.id,
    status: "Published",
    draft: cloneGraph(record.draft.graph),
    spec: record.compilation.spec || { workflowId: record.workflow.id, nodes: [] },
    plan: record.compilation.plan || { workflowId: record.workflow.id, nodes: [] },
    createdAt,
    createdBy: FIXTURE_USER.username,
    publishNote: "Playwright E2E publish baseline",
    planHash: "sha256:e2eclosedloop0001",
    activatedAt: createdAt,
    metadata: { source: "playwright-e2e" },
  };
  record.revisions = [revision, ...record.revisions];
  record.workflow.status = "Published";
  record.workflow.activeRevisionId = revision.revisionId;
  record.workflow.readiness = computeReadiness(record.workflow, record.compilation, record.revisions);
  return revision;
}

function compileWorkflowGraph(workflow: Workflow, draft: WorkflowDraftRecord, tools: Tool[]): WorkflowCompilation {
  const issues = collectCompilationIssues(draft.graph, tools);
  return {
    id: `comp-${draft.workflowId}-${draft.draftVersion}`,
    workflowId: workflow.id,
    draftId: draft.id,
    draftVersion: draft.draftVersion,
    graphHash: draft.graphHash,
    compilerVersion: "workflow-compiler.v1",
    status: issues.length ? "INVALID" : "VALID",
    spec: {
      workflowId: workflow.id,
      nodes: draft.graph.nodes.map((node) => ({
        nodeId: node.id,
        type: node.type,
        config: { ...node.data },
      })),
    },
    plan: buildExecutionPlan(workflow.id, draft.graph),
    issues,
    planHash: `sha256:plan-${draft.draftVersion}`,
    compiledBy: FIXTURE_USER.id,
    compiledAt: nextTimestampFromDraft(draft.updatedAt),
  };
}

function collectCompilationIssues(graph: WorkflowGraphDraft, tools: Tool[]): WorkflowCompilationIssue[] {
  const issues: WorkflowCompilationIssue[] = [];

  for (const node of graph.nodes) {
    if (node.type !== "Tool") {
      continue;
    }

    const toolId = typeof node.data.toolId === "string" ? node.data.toolId : "";
    const selectedTool = tools.find((tool) => tool.id === toolId && tool.status === "Published");
    if (!selectedTool) {
      issues.push({
        code: "ToolBindingMissing",
        message: "Tool 节点还没有绑定已发布工具。",
        severity: "error",
        sourceStage: "semantic",
        nodeId: node.id,
        fieldPath: "toolId",
      });
      continue;
    }

    const requiredParams = selectedTool.requestParams.filter(
      (param) => param.required && param.valueSource !== "SystemDefault",
    );
    const inputMapping =
      node.data.inputMapping && typeof node.data.inputMapping === "object"
        ? (node.data.inputMapping as Record<string, unknown>)
        : {};
    for (const param of requiredParams) {
      if (!inputMapping[param.name]) {
        issues.push({
          code: "ToolInputMappingMissing",
          message: `Tool 节点缺少 ${param.name} 参数映射。`,
          severity: "error",
          sourceStage: "semantic",
          nodeId: node.id,
          fieldPath: `inputMapping.${param.name}`,
        });
      }
    }
  }

  for (const node of graph.nodes) {
    if (node.type !== "Condition") {
      continue;
    }

    const outgoing = graph.edges.filter((edge) => edge.sourceNodeId === node.id);
    if (outgoing.length < 2) {
      continue;
    }

    const hasDefaultBranch = outgoing.some((edge) => edge.data?.branch === "default");
    if (!hasDefaultBranch) {
      issues.push({
        code: "ConditionDefaultBranchMissing",
        message: "Condition 节点存在多条外连线时，必须保留一个 default 分支。",
        severity: "error",
        sourceStage: "graph",
        nodeId: node.id,
        edgeId: outgoing[0]?.id,
        fieldPath: "edge.data.branch",
      });
    }
  }

  return issues;
}

function buildExecutionPlan(workflowId: string, graph: WorkflowGraphDraft): CompiledExecutionPlan {
  return {
    workflowId,
    nodes: graph.nodes.map((node) => ({
      nodeId: node.id,
      type: node.type,
      dependencies: graph.edges.filter((edge) => edge.targetNodeId === node.id).map((edge) => edge.sourceNodeId),
      config: { ...node.data },
    })),
  };
}

function compilationToValidation(compilation: WorkflowCompilation): WorkflowValidationResult {
  return {
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
}

function computeReadiness(
  workflow: Workflow,
  compilation: WorkflowCompilation,
  revisions: WorkflowRevision[],
): WorkflowReadiness {
  const blockers =
    compilation.status === "INVALID"
      ? compilation.issues.map((issue) => ({
          code: issue.code,
          message: issue.message,
          action: "修复编译问题后再试运行或发布。",
          sourceStage: issue.sourceStage,
          nodeId: issue.nodeId,
          edgeId: issue.edgeId,
          fieldPath: issue.fieldPath,
          severity: issue.severity,
        }))
      : [];
  let stage: WorkflowReadinessStage = "TrialRequired";
  let canPublish = false;
  let published = false;

  if (workflow.status === "Disabled") {
    stage = "Disabled";
  } else if (compilation.status === "INVALID") {
    stage = "CompileFailed";
  } else if (revisions.length > 0 && workflow.activeRevisionId) {
    stage = "Published";
    published = true;
  } else if (workflow.lastTrialStatus === "Success" && workflow.lastTrialExecutionId) {
    stage = "PublishReady";
    canPublish = true;
  }

  return {
    stage,
    canCompile: true,
    canTrial: compilation.status === "VALID",
    canValidate: true,
    canTrialRun: compilation.status === "VALID",
    canPublish,
    hasDraft: true,
    compilationCurrent: true,
    compilationValid: compilation.status === "Valid",
    trialCurrent: Boolean(workflow.lastTrialExecutionId),
    trialSuccessful: workflow.lastTrialStatus === "Success",
    published,
    activeRevisionId: workflow.activeRevisionId || undefined,
    latestRevisionId: revisions[0]?.revisionId || undefined,
    blockers,
    updatedAt: nextTimestampFromDraft(compilation.compiledAt),
  };
}

function summarizeWorkflowRecord(record: WorkflowRecord, state: WorkflowFixtureState): WorkflowSummary {
  const workspace = state.workspaces.find((item) => item.id === record.workflow.workspaceId);
  return {
    id: record.workflow.id,
    workspaceId: record.workflow.workspaceId,
    currentDraftId: record.workflow.currentDraftId,
    latestCompilationId: record.workflow.latestCompilationId,
    workspaceName: workspace?.displayName || workspace?.name,
    name: record.workflow.name,
    slug: record.workflow.slug,
    description: record.workflow.description,
    status: record.workflow.status,
    nodeCount: record.workflow.nodeCount || 0,
    edgeCount: record.workflow.edgeCount || 0,
    lastValidationValid: record.workflow.lastValidationResult?.valid,
    lastValidationIssueCount: record.workflow.lastValidationResult?.issues.length,
    lastTrialExecutionId: record.workflow.lastTrialExecutionId || undefined,
    lastTrialStatus: record.workflow.lastTrialStatus,
    activeRevisionId: record.workflow.activeRevisionId || undefined,
    readiness: record.workflow.readiness,
    createdBy: record.workflow.createdBy,
    updatedBy: record.workflow.updatedBy,
    createdAt: record.workflow.createdAt,
    updatedAt: record.workflow.updatedAt,
    lockVersion: record.workflow.lockVersion,
  };
}

function buildExecutionStep(
  executionId: string,
  id: string,
  nodeId: string,
  nodeType: string,
  status: Execution["steps"][number]["status"],
  inputSummary: string,
  outputSummary: string,
  durationMs: number,
) {
  return {
    id,
    executionId,
    name: `${nodeType} step`,
    nodeId,
    nodeType,
    status,
    inputSummary,
    outputSummary,
    errorMessage: "",
    durationMs,
    rawPayloadObjectAddress: `s3://fixtures/${executionId}/${id}.json`,
  };
}

function requireWorkflow(state: WorkflowFixtureState, workflowId: string) {
  const record = state.workflows.get(workflowId);
  if (!record) {
    throw new Error(`Missing workflow fixture record for ${workflowId}`);
  }
  return record;
}

function nextDraftVersion(state: WorkflowFixtureState) {
  const version = state.nextDraftVersion;
  state.nextDraftVersion += 1;
  return version;
}

function nextTimestamp(state: WorkflowFixtureState) {
  const value = new Date(FIXTURE_START_TIME + state.nextTimestampStep * 60_000).toISOString();
  state.nextTimestampStep += 1;
  return value;
}

function nextTimestampFromDraft(value: string) {
  return new Date(Date.parse(value) + 1_000).toISOString();
}

function cloneWorkflow(workflow: Workflow) {
  return JSON.parse(JSON.stringify(workflow)) as Workflow;
}

function cloneDraft(draft: WorkflowDraftRecord) {
  return JSON.parse(JSON.stringify(draft)) as WorkflowDraftRecord;
}

function cloneCompilation(compilation: WorkflowCompilation) {
  return JSON.parse(JSON.stringify(compilation)) as WorkflowCompilation;
}

function cloneExecution(execution: Execution) {
  return JSON.parse(JSON.stringify(execution)) as Execution;
}

function cloneRevision(revision: WorkflowRevision) {
  return JSON.parse(JSON.stringify(revision)) as WorkflowRevision;
}

function cloneValidation(validation?: WorkflowValidationResult) {
  if (!validation) {
    return { valid: true, issues: [] } satisfies WorkflowValidationResult;
  }
  return JSON.parse(JSON.stringify(validation)) as WorkflowValidationResult;
}

function cloneGraph(graph: WorkflowGraphDraft) {
  return JSON.parse(JSON.stringify(graph)) as WorkflowGraphDraft;
}

async function json(route: Route, status: number, body: unknown, headers: Record<string, string> = {}) {
  await route.fulfill({
    status,
    contentType: "application/json",
    headers,
    body: JSON.stringify(body),
  });
}
