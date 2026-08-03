export interface User {
  id: string;
  username: string;
  email?: string;
  displayName: string;
  avatarUrl?: string;
  status: "ACTIVE" | "LOCKED" | "DISABLED";
  platformRole: "USER" | "PLATFORM_ADMIN";
  locale: string;
  timezone: string;
  lastLoginAt?: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
  /** Human-readable presentation label; authorization uses platformRole. */
  role: string;
}

export type PlatformRole = User["platformRole"];
export type UserStatus = User["status"];

export interface UserListQuery {
  query?: string;
  status?: UserStatus;
  platformRole?: PlatformRole;
  page?: number;
  pageSize?: number;
}

export interface UserWorkspaceMembership {
  workspaceId: string;
  workspaceSlug: string;
  workspaceDisplayName: string;
  workspaceStatus: "ACTIVE" | "DISABLED";
  role: WorkspaceRole;
  joinedAt: string;
  disabledAt?: string;
}

/** ZKL-74/ZKL-81 session-context-policy.v1|v2 patch (workspace/agent). Empty object inherits. */
export interface SessionContextPolicy {
  schemaVersion?: "session-context-policy.v1" | "session-context-policy.v2";
  mode?: "token_window" | "rolling_summary" | "disabled";
  maxInputTokens?: number;
  outputReserveTokens?: number;
  safetyMarginTokens?: number;
  maxRecentTurns?: number;
  summary?: {
    maxTokens?: number;
    minEvictedTurns?: number;
    maxGenerationPasses?: number;
  };
  /**
   * Agent-only (policy v2). When true, successful compact summary body is permanently
   * dual-written as PostgreSQL plaintext protocol projection (T4-B). Closing only affects new runs.
   * Default false. Workspace policy must not set this field.
   */
  aap?: {
    includeCompactionSummary?: boolean;
  };
}

/** ZKL-74 model-runtime.v1 hard capabilities (never merged into options). */
export interface ModelRuntimeCapabilities {
  schemaVersion?: "model-runtime.v1";
  contextWindowTokens?: number;
  defaultOutputReserveTokens?: number;
  outputTokenLimitMode?: "max_tokens" | "max_completion_tokens";
  tokenizerProfile?: "o200k_base" | "cl100k_base" | "byte_upper_bound" | string;
  tokenizerVersion?: string;
}

export interface Workspace {
  id: string;
  name: string;
  slug?: string;
  displayName: string;
  mode: string;
  status: "Active" | "Disabled";
  ownerUserId?: string;
  defaultAgentId: string;
  defaultModelConfigId?: string;
  modelConfigId: string;
  settings?: Record<string, unknown>;
  /** Dedicated context policy column; not nested under settings. */
  contextPolicy?: SessionContextPolicy | Record<string, unknown>;
  createdBy?: string;
  createdByUsername?: string;
  updatedBy?: string;
  updatedByUsername?: string;
  createdAt?: string;
  updatedAt?: string;
  lockVersion?: number;
  owner?: string;
  healthScore: number;
  toolCount?: number;
  workflowCount?: number;
  agentCount?: number;
  /** Effective role for the current principal (ZKL-64 D1-A). */
  currentUserRole?: WorkspaceRole;
}

export interface WorkspaceAccessibleSummary {
  total: number;
  active: number;
  production: number;
  boundAgents: number;
}

export type WorkspaceRole = "OWNER" | "ADMIN" | "EDITOR" | "OPERATOR" | "VIEWER";

export interface WorkspaceMember {
  userId: string;
  role: WorkspaceRole;
  invitedBy?: string;
  joinedAt: string;
  disabledAt?: string;
}

export interface WorkspaceMemberCandidate {
  userId: string;
  username: string;
  displayName: string;
  platformRole: PlatformRole;
}

export type SortOrder = "asc" | "desc";

export interface PaginatedListQuery {
  query?: string;
  status?: string;
  mode?: string;
  workspaceId?: string;
  page?: number;
  pageSize?: number;
  sortBy?: string;
  sortOrder?: SortOrder;
}

export interface WorkspaceListQuery extends PaginatedListQuery {
  status?: "Active" | "Disabled";
  mode?: "Production" | "Sandbox";
}

export interface AgentListQuery extends PaginatedListQuery {
  status?: "ACTIVE" | "DISABLED";
}

export interface ToolListQuery extends PaginatedListQuery {
  status?: "Draft" | "Review" | "Tested" | "Published" | "Disabled" | "attention";
  type?: "HTTP Tool" | "Workflow Tool";
}

export interface WorkflowListQuery extends PaginatedListQuery {
  status?: "Draft" | "Review" | "Published" | "Disabled";
}

export interface PaginatedListResponse<T> {
  items: T[];
  page: number;
  pageSize: number;
  total: number;
}

export interface ModelApiConfig {
  id: string;
  name: string;
  provider: string;
  apiBase: string;
  modelName: string;
  credentialConfigured: boolean;
  credentialSecretId?: string;
  options: Record<string, unknown>;
  /** Strict runtime capabilities; must not be merged into options. */
  runtimeCapabilities?: ModelRuntimeCapabilities | Record<string, unknown>;
  status: "UNVERIFIED" | "VERIFIED" | "ERROR" | "DISABLED";
  lastVerifiedAt?: string;
  lastLatencyMs?: number;
  lastErrorCode?: string;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export type ModelApiConfigStatusFilter = ModelApiConfig["status"];

export type ModelApiConfigListQuery = Omit<PaginatedListQuery, "status"> & { status?: ModelApiConfigStatusFilter };
export type ModelApiConfigPage = PaginatedListResponse<ModelApiConfig>;

export interface Agent {
  id: string;
  /** Client-side scope derived from the v1 workspace route. */
  workspaceId: string;
  name: string;
  roleDescription: string;
  modelConfigId: string;
  currentPromptRevisionId?: string;
  /** Create/enhancement input only; Agent read DTOs never contain prompt plaintext. */
  systemPrompt: string;
  isDefault: boolean;
  status: "ACTIVE" | "DISABLED";
  /** Agent-level context policy override; empty inherits workspace/platform. */
  contextPolicy?: SessionContextPolicy | Record<string, unknown>;
  toolsCount: number;
  workflowsCount: number;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export interface PromptEnhancement {
  runId: string;
  status: string;
  preview: boolean;
  output: string;
  inputObjectId?: string;
  outputObjectId?: string;
  acceptedRevisionId?: string;
  revisionNo?: number;
  createdAt?: string;
  expiresAt?: string;
}

export interface CurrentAgentPrompt {
  agentId: string;
  revisionId: string;
  revisionNo: number;
  systemPrompt: string;
  source: string;
  createdBy: string;
  createdAt: string;
}

export interface CapabilityReleaseDescriptor {
  capabilityId: string;
  releaseId: string;
  kind: "TOOL" | "WORKFLOW" | string;
  callableName: string;
  callableDescription: string;
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  riskLevel: string;
  sideEffectLevel: string;
  requiresConfirmation: boolean;
}

export interface CapabilityCatalogItem {
  id: string;
  kind: "TOOL" | "WORKFLOW" | string;
  name: string;
  slug: string;
  description: string;
  status: string;
  activeReleaseId?: string;
  boundAgentCount: number;
  activeRelease?: CapabilityReleaseDescriptor;
  createdBy: string;
  updatedBy: string;
  lockVersion: number;
}

export interface AgentCapabilityBinding {
  capabilityId: string;
  versionPolicy: "FOLLOW_ACTIVE" | "PINNED";
  pinnedReleaseId?: string;
  connectionId?: string;
  executionPolicyId?: string;
  enabled: boolean;
  configOverrides: Record<string, unknown>;
  resolvedRelease?: CapabilityReleaseDescriptor;
  lockVersion: number;
}

export interface RuntimeTopologyNode {
  index: number;
  name: string;
}

export interface RiskItem {
  tone: "amber" | "red" | "cyan";
  title: string;
  detail: string;
}

export interface ExecutionStep {
  name: string;
  status: "passed" | "running" | "pending" | "failed";
  detail: string;
}

/** GET /overview/metrics — aggregates across all accessible workspaces. */
export interface OverviewMetricsKPIs {
  toolCallSuccessRate: number;
  toolCallsTotal: number;
  toolCallsSucceeded: number;
  toolCallsFailed: number;
  avgToolLatencyMs: number;
  runSuccessRate: number;
  runsTotal: number;
  runsSucceeded: number;
  runsFailed: number;
  avgRunLatencyMs: number;
  workflowSuccessRate: number;
  workflowTotal: number;
  workflowSucceeded: number;
  workflowFailed: number;
  avgWorkflowLatencyMs: number;
  sessionCountToday: number;
  sessionCountPeriod: number;
  avgSessionsPerDay: number;
}

export interface OverviewDayPoint {
  date: string;
  sessions: number;
  runsTotal: number;
  runsSucceeded: number;
  runsFailed: number;
  toolCallsTotal: number;
  toolCallsSucceeded: number;
  toolCallsFailed: number;
  workflowTotal?: number;
  workflowSucceeded?: number;
  workflowFailed?: number;
}

export interface OverviewInventory {
  workspaceCount: number;
  agentCount: number;
  toolCount: number;
  workflowCount: number;
  connectionTotal: number;
  connectionVerified: number;
  modelConfigTotal: number;
  modelConfigVerified: number;
  hasVerifiedModel: boolean;
}

export interface OverviewEntityStat {
  id: string;
  name: string;
  total: number;
  succeeded: number;
  failed: number;
  successRate: number;
  avgLatencyMs?: number;
  sessions?: number;
  runs?: number;
  toolCalls?: number;
}

export interface OverviewMetrics {
  windowDays: number;
  from: string;
  to: string;
  /** Inclusive calendar bounds (YYYY-MM-DD). */
  fromDate?: string;
  toDate?: string;
  workspaceCount: number;
  workspaceIds?: string[];
  kpis: OverviewMetricsKPIs;
  series: OverviewDayPoint[];
  inventory: OverviewInventory;
  topTools?: OverviewEntityStat[];
  topWorkspaces?: OverviewEntityStat[];
  failingTools?: OverviewEntityStat[];
}

/** Dual-mode outbound identity (post hard-cutover). No third mode / NONE / shared account. */
export type OutboundIdentityMode = "BROKER_OBO" | "REQUEST_PASSTHROUGH";

export type MigrationState = "NONE" | "MIGRATION_REQUIRED";

export interface ServiceConnection {
  id: string;
  /** Client-side scope derived from the v1 workspace route. */
  workspaceId?: string;
  providerId: string;
  name: string;
  alias: string;
  environment: string;
  externalAccountRef?: string;
  protocol: string;
  protocolConfig: {
    domain: string;
    host: string;
    port: string;
    basePath: string;
    verificationMethod: string;
    verificationPath: string;
    expectedStatus: string;
    expectedResponseContains: string;
    commonHeaders: Record<string, string>;
  };
  protocolSchema: string;
  /**
   * Legacy shared-account fields kept populated with empty defaults for dual-mode
   * rows so existing list/helpers compile. New writes use outboundMode only.
   */
  authMode: string;
  authConfig: {
    mode: string;
    label: string;
    tokenUrl: string;
    schemeKey?: string;
    values?: Record<string, string>;
    clientId?: string;
    clientAuth?: string;
    scope?: string;
    refreshUrl: string;
    refreshMode: string;
    accessTokenPath: string;
    refreshTokenPath: string;
    expiresPath: string;
    injectionTemplate: string;
    retryOn401Policy: string;
    refreshFailurePolicy: string;
    credentialPlacement?: string;
    apiKeyName?: string;
    apiSecretName?: string;
    tokenHeaderName?: string;
    tokenPrefix?: string;
  };
  /** Fixed dual-mode policy: BROKER_OBO | REQUEST_PASSTHROUGH. */
  outboundMode?: OutboundIdentityMode;
  /** Server-derived policy version (read-only). */
  outboundIdentityPolicyVersion?: number;
  migrationState?: MigrationState;
  /** Non-secret outbound-connection.v1 descriptor (no Token / Secret IDs). */
  outboundIdentity?: Record<string, unknown>;
  /** True when machine credential is configured (Broker only). Never exposes Secret ID. */
  machineCredentialConfigured?: boolean;
  /** @deprecated Prefer machineCredentialConfigured; Secret IDs must not appear in UI state. */
  credentialSecretId?: string;
  credentialConfigured: boolean;
  credentialFingerprint?: string;
  grantedScopes: unknown[];
  policy: Record<string, unknown>;
  status: "UNVERIFIED" | "VERIFIED" | "ERROR" | "DISABLED" | "Available" | "Needs attention" | "Expiring soon";
  lastErrorCode?: string;
  createdBy: string;
  updatedBy: string;
  lockVersion: number;
  associatedToolCount?: number;
  lastVerifiedAt?: string;
}

/** Write-only outbound credentials envelope (request body only; never store value). */
export interface OutboundCredentialsEnvelope {
  schemaVersion: "outbound-credentials.v1";
  bindings: Array<{
    connectionId: string;
    credentialType: "ACCESS_TOKEN";
    /** writeOnly — never put in Pinia / localStorage / logs. */
    value: string;
    expiresAt: string;
  }>;
}

/** Response from POST .../chat/sessions/:id/outbound-credentials (no Token). */
export interface OutboundCredentialAttachmentResult {
  outboundCredentialAttachmentId: string;
  expiresAt: string;
}

export type ServiceConnectionListQuery = Omit<PaginatedListQuery, "status"> & { status?: ServiceConnection["status"] };
export type ServiceConnectionPage = PaginatedListResponse<ServiceConnection>;

export interface ServiceConnectionVerification {
  id: string;
  workspaceId: string;
  connectionId: string;
  status: string;
  diagnostics: Record<string, string>;
  latencyMs?: number;
  testedBy: string;
  testedAt: string;
  rawObjectId?: string;
}

export interface CapabilityProvider {
  id: string;
  name: string;
  kind: "HTTP_OPENAPI";
  driverKey: string;
  transport: "HTTP";
  endpointConfig: Record<string, unknown>;
  driverConfig: Record<string, unknown> & { authentication?: ProviderAuthContract };
  discoveryMode: string;
  status: string;
  lastSyncedAt?: string;
  lastErrorCode?: string;
  createdBy: string;
  updatedBy: string;
  lockVersion: number;
}

export type ProviderAuthSchemeType = "NONE" | "OAUTH2_CLIENT";
export type ProviderAuthFieldKind = "TEXT" | "SECRET" | "SELECT";

export interface ProviderAuthContract {
  version: "service-auth.v1";
  defaultSchemeKey: string;
  schemes: ProviderAuthScheme[];
}

export interface ProviderAuthScheme {
  key: string;
  type: ProviderAuthSchemeType;
  displayName: string;
  description?: string;
  fields: ProviderAuthField[];
  oauth2?: ProviderOAuth2Profile;
}

export interface ProviderAuthField {
  key: string;
  label: string;
  kind: ProviderAuthFieldKind;
  required?: boolean;
  placeholder?: string;
  help?: string;
  options?: Array<{ label: string; value: string }>;
}

export interface ProviderOAuth2Profile {
  tokenUrlTemplate: string;
  clientIdField: string;
  credentialField: string;
  clientAuthMethod: "client_secret_basic" | "client_secret_post";
  scopeField?: string;
  tokenParameters?: Array<{ name: string; field?: string; value?: string; required?: boolean }>;
  response: {
    accessTokenPath: string;
    tokenTypePath?: string;
    expiresInPath?: string;
    renewalTokenPath?: string;
  };
  injection: { headerName: string; prefix?: string };
  refreshStrategy?: "CLIENT_CREDENTIALS" | "REFRESH_TOKEN";
}

export interface ProviderAsset {
  id: string;
  kind: string;
  externalId: string;
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  metadata: Record<string, unknown>;
  sourceRevision?: string;
  sourceChecksum: string;
  materializedCapabilityId?: string;
  status: string;
}

export interface Tool {
  id: string;
  workspaceId: string;
  providerId: string;
  sourceAssetId?: string;
  sourceEndpointId?: string;
  connectionId: string;
  defaultConnectionId?: string;
  name: string;
  slug: string;
  protocol: string;
  actionConfig: Record<string, unknown>;
  actionConfigSchemaVersion: string;
  description: string;
  status: "Draft" | "Review" | "Tested" | "Published" | "Disabled";
  capabilityStatus: "ACTIVE" | "DISABLED" | string;
  activeReleaseId?: string;
  versions: ToolVersion[];
  draftVersion?: ToolVersion;
  requestParams: ToolRequestParam[];
  responseFields: ToolResponseField[];
  errorMappings: ToolErrorMapping[];
  runtimePolicy: ToolRuntimePolicy;
  lastTestResult?: ToolTestResult;
  /** Additive ZKL-56 historical test summary from list/detail API (null = unknown). */
  latestTest?: {
    status: string;
    testedAt: string;
    testedBy: string;
    errorCode?: string;
  } | null;
  createdBy: string;
  updatedBy: string;
  createdAt?: string;
  updatedAt?: string;
  lockVersion: number;
}

export interface ToolVersion {
  id: string;
  versionNo: number;
  lifecycleStatus: "DRAFT" | "REVIEW" | "TESTED" | "PUBLISHED";
  executorType: string;
  providerAssetId?: string;
  defaultConnectionId?: string;
  actionSchemaVersion: string;
  actionConfig: Record<string, unknown>;
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  errorMappings: Record<string, unknown> | unknown[];
  runtimePolicy: Record<string, unknown>;
  riskLevel: string;
  sideEffectLevel: string;
  requiresConfirmation: boolean;
  checksum: string;
  createdBy: string;
  updatedBy: string;
  publishedAt?: string;
  lockVersion: number;
}

export type ToolSchemaNodeType = "string" | "integer" | "number" | "boolean" | "object" | "array";
export type ToolParameterValueSource = "UserInput" | "SystemDefault";

export interface ToolSchemaNode {
  id: string;
  name: string;
  type: ToolSchemaNodeType;
  description: string;
  required: boolean;
  location?: string;
  format?: string;
  nullable?: boolean;
  example?: string;
  enumValues?: string[];
  valueSource?: ToolParameterValueSource;
  defaultValue?: unknown;
  children?: ToolSchemaNode[];
  item?: ToolSchemaNode | null;
  additionalProperties?: ToolSchemaNode | null;
}

export interface ToolRequestParam {
  location: string;
  name: string;
  type: string;
  required: boolean;
  description: string;
  valueSource?: ToolParameterValueSource;
  defaultValue?: unknown;
  schema?: ToolSchemaNode;
}

export interface ToolResponseField {
  name: string;
  type: string;
  description: string;
  schema?: ToolSchemaNode;
}

export interface ToolErrorMapping {
  protocolStatus: string;
  errorCode: string;
  agentAdvice: string;
}

export interface ToolRuntimePolicy {
  timeoutMs: number;
  retryCount: number;
  backoffPolicy: string;
  idempotencyPolicy: string;
  rateLimitPolicy: string;
}

export interface ToolTestResult {
  id: string;
  status: string;
  connectivityPassed: boolean;
  responseSchemaPassed: boolean;
  errorMappingPassed: boolean;
  runtimePolicyPassed: boolean;
  requestSummary: Record<string, unknown>;
  responseSummary: Record<string, unknown>;
  /** Interactive-only raw upstream body from test API (not stored in redacted summary). */
  responseBody?: unknown;
  requestBody?: unknown;
  latencyMs?: number;
  errorCode?: string;
  testedBy: string;
  testedAt: string;
}

export interface ToolTestExecutionResult {
  tool: Tool;
  testResult: ToolTestResult;
  requestInput: Record<string, unknown>;
  responseStatus: number;
  responseBody: unknown;
  latencyMs: number;
  passed: boolean;
  errorMessage: string;
}

export interface ToolProtocol {
  protocol: string;
  adapterName: string;
  adapterVersion: string;
  configSchema: Record<string, unknown>;
  requestSchemaCapability: string[];
  responseSchemaCapability: string[];
  testCapability: boolean;
  runtimePolicyCapability: boolean;
  status: string;
}

export interface OpenAPIEndpoint {
  id: string;
  method: string;
  path: string;
  operationId: string;
  summary: string;
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  generatedCapabilityId?: string;
  requestParams?: ToolRequestParam[];
  responseFields?: ToolResponseField[];
  issues?: string[];
  ready?: boolean;
}

export interface OpenAPIImportRequest {
  workspaceId: string;
  providerId: string;
  connectionId?: string;
}

export interface OpenAPIImport {
  id: string;
  workspaceId: string;
  providerId?: string;
  connectionId?: string;
  source: string;
  sourceType: string;
  sourceUri?: string;
  sourceRevision?: string;
  fileName: string;
  contentSha256: string;
  parserVersion: string;
  totalEndpoints: number;
  readyEndpoints: number;
  issueCount: number;
  issues: string[];
  status: string;
  createdAt?: string;
  updatedAt?: string;
  detail?: OpenAPIImportDetail;
}

export type OpenAPIImportListQuery = Omit<PaginatedListQuery, "status"> & { status?: "Ready" | "Issues" };
export type OpenAPIImportPage = PaginatedListResponse<OpenAPIImport>;

export interface OpenAPIImportDetail {
  endpoints: OpenAPIImportEndpointDetail[];
  requestContract?: ToolSchemaNode | null;
  responseContract?: ToolSchemaNode | null;
}

export interface OpenAPIImportEndpointDetail {
  id?: string;
  method: string;
  path: string;
  operationId: string;
  summary: string;
  status: string;
  ready?: boolean;
  generatedCapabilityId?: string;
  issues?: string[];
  requestContract?: ToolSchemaNode | null;
  responseContract?: ToolSchemaNode | null;
}

export type WorkflowStatus = "Draft" | "Review" | "Published" | "Disabled";
export type ExecutionStatus = "Running" | "Approval" | "Success" | "Failed";
export type ExecutionStepStatus =
  | "Queued"
  | "Running"
  | "Passed"
  | "Skipped"
  | "WaitingApproval"
  | "Failed"
  | "Cancelled";
export type WorkflowIssueStage = "graph" | "semantic" | "spec" | "plan" | "runtime";
export type WorkflowCompilationStatus = "PENDING" | "VALID" | "INVALID" | "Pending" | "Valid" | "Invalid";
export type WorkflowReadinessStage =
  | "DRAFT_MISSING"
  | "COMPILE_REQUIRED"
  | "COMPILE_FAILED"
  | "TRIAL_REQUIRED"
  | "PUBLISH_READY"
  | "PUBLISHED"
  | "DISABLED"
  | "DraftMissing"
  | "CompileRequired"
  | "CompileFailed"
  | "TrialRequired"
  | "PublishReady"
  | "Published"
  | "Disabled";
export type WorkflowGraphNodeType =
  | "Start"
  | "End"
  | "Tool"
  | "HTTP"
  | "SubWorkflow"
  | "Transform"
  | "Approval"
  | "Condition"
  | "Parallel"
  | "ForEach";
export type WorkflowGraphPortDirection = "input" | "output";

export interface WorkflowPosition {
  x: number;
  y: number;
}

export interface WorkflowVariable {
  name: string;
  type: string;
  required: boolean;
  description: string;
}

export interface WorkflowGraphPort {
  key: string;
  label: string;
  direction: WorkflowGraphPortDirection;
}

export interface WorkflowGraphNode {
  id: string;
  type: WorkflowGraphNodeType;
  label: string;
  position: WorkflowPosition;
  ports: WorkflowGraphPort[];
  data: Record<string, unknown>;
  ui: Record<string, unknown>;
}

export interface WorkflowGraphEdge {
  id: string;
  sourceNodeId: string;
  sourcePort: string;
  targetNodeId: string;
  targetPort: string;
  data: Record<string, unknown>;
  ui: Record<string, unknown>;
}

export interface WorkflowGraphDraft {
  schemaVersion: string;
  nodes: WorkflowGraphNode[];
  edges: WorkflowGraphEdge[];
  viewport: {
    x: number;
    y: number;
    zoom: number;
  };
  ui: Record<string, unknown>;
}

export interface WorkflowDraftRecord {
  id: string;
  workflowId: string;
  draftVersion: number;
  schemaVersion: string;
  graph: WorkflowGraphDraft;
  graphHash: string;
  updatedBy: string;
  updatedAt: string;
  lockVersion: number;
  etag: string;
}

export interface SaveWorkflowDraftRequest {
  schemaVersion: string;
  graph: WorkflowGraphDraft;
  draftVersion: number;
  lockVersion: number;
}

export interface WorkflowCompilationIssue {
  code: string;
  message: string;
  severity: string;
  sourceStage: WorkflowIssueStage;
  nodeId?: string;
  edgeId?: string;
  portKey?: string;
  fieldPath?: string;
  suggestion?: string;
}

export interface ExecutableNodeSpec {
  nodeId: string;
  type: string;
  config: Record<string, unknown>;
}

export interface ExecutableWorkflowSpec {
  workflowId: string;
  nodes: ExecutableNodeSpec[];
}

export interface ExecutionPlanNode {
  nodeId: string;
  type: string;
  dependencies?: string[];
  incomingBranch?: string;
  config?: Record<string, unknown>;
}

export interface CompiledExecutionPlan {
  workflowId: string;
  nodes: ExecutionPlanNode[];
}

export interface WorkflowCompilation {
  id: string;
  workflowId: string;
  draftId: string;
  draftVersion: number;
  graphHash: string;
  compilerVersion: string;
  status: WorkflowCompilationStatus;
  spec?: ExecutableWorkflowSpec;
  plan?: CompiledExecutionPlan;
  issues: WorkflowCompilationIssue[];
  planHash: string;
  compiledBy: string;
  compiledAt: string;
}

export interface WorkflowRevision {
  workflowId: string;
  revisionId: string;
  revisionNo: number;
  sourceCompilationId: string;
  status: WorkflowStatus;
  draft: WorkflowGraphDraft;
  spec: ExecutableWorkflowSpec;
  plan: CompiledExecutionPlan;
  createdAt: string;
  createdBy?: string;
  publishNote?: string;
  planHash?: string;
  activatedAt?: string;
  retiredAt?: string;
  metadata?: Record<string, unknown>;
}

export type WorkflowRevisionChangeType = "Added" | "Removed" | "TypeChanged" | "DataChanged" | "BranchChanged";

export interface WorkflowRevisionNodeChange {
  nodeId: string;
  changeType: WorkflowRevisionChangeType;
  leftType?: string;
  rightType?: string;
  leftLabel?: string;
  rightLabel?: string;
  leftData?: Record<string, unknown>;
  rightData?: Record<string, unknown>;
}

export interface WorkflowRevisionEdgeChange {
  edgeId: string;
  changeType: WorkflowRevisionChangeType;
  sourceNodeId?: string;
  targetNodeId?: string;
  leftBranch?: string;
  rightBranch?: string;
}

export interface WorkflowRevisionDiff {
  workflowId: string;
  leftRevisionId: string;
  rightRevisionId: string;
  nodeChanges: WorkflowRevisionNodeChange[];
  edgeChanges: WorkflowRevisionEdgeChange[];
  changes?: { draft: boolean; spec: boolean; plan: boolean; planHash: boolean };
  comparedAt: string;
}

export interface WorkflowReadinessBlocker {
  code: string;
  message: string;
  action: string;
  sourceStage?: WorkflowIssueStage;
  nodeId?: string;
  edgeId?: string;
  fieldPath?: string;
  severity: string;
}

export interface WorkflowReadiness {
  stage: WorkflowReadinessStage;
  canCompile: boolean;
  canTrial: boolean;
  canValidate: boolean;
  canTrialRun: boolean;
  canPublish: boolean;
  hasDraft: boolean;
  compilationId?: string;
  compilationCurrent: boolean;
  compilationValid: boolean;
  trialCurrent: boolean;
  trialSuccessful: boolean;
  published: boolean;
  activeRevisionId?: string;
  latestRevisionId?: string;
  blockers: WorkflowReadinessBlocker[];
  updatedAt: string;
}

export interface WorkflowValidationIssue {
  nodeId?: string;
  edgeId?: string;
  field: string;
  severity: string;
  message: string;
}

export interface EinoDAGMapping {
  schemaVersion: string;
  workflowId?: string;
  validationStatus?: string;
  nodeMappings?: Array<Record<string, unknown>>;
  edgeMappings?: Array<Record<string, unknown>>;
}

export interface WorkflowValidationResult {
  valid: boolean;
  issues: WorkflowValidationIssue[];
  einoDagMapping?: EinoDAGMapping;
  validatedAt?: string;
}

export interface WorkflowSummary {
  id: string;
  workspaceId: string;
  currentDraftId: string;
  activeRevisionId?: string;
  latestCompilationId?: string;
  workspaceName?: string;
  name: string;
  slug: string;
  description: string;
  status: WorkflowStatus;
  nodeCount: number;
  edgeCount: number;
  lastValidationValid?: boolean;
  lastValidationIssueCount?: number;
  lastTrialExecutionId?: string;
  lastTrialStatus?: ExecutionStatus;
  readiness?: WorkflowReadiness;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export interface Workflow {
  id: string;
  workspaceId: string;
  currentDraftId: string;
  latestCompilationId?: string;
  name: string;
  slug: string;
  description: string;
  status: WorkflowStatus;
  nodeCount?: number;
  edgeCount?: number;
  lastValidationValid?: boolean;
  lastValidationIssueCount?: number;
  lastTrialExecutionId?: string;
  lastTrialStatus?: ExecutionStatus;
  activeRevisionId?: string;
  lastValidationResult?: WorkflowValidationResult;
  einoDagMapping?: EinoDAGMapping;
  readiness?: WorkflowReadiness;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export type CapabilityKind = "Tool" | "Workflow";

export interface CapabilitySourceRef {
  toolId?: string;
  workflowId?: string;
  revisionId?: string;
}

export interface CapabilityDescriptor {
  id: string;
  kind: CapabilityKind;
  name: string;
  description: string;
  callableName: string;
  callableDescription: string;
  inputSchema: Record<string, unknown>;
  outputSchema?: Record<string, unknown>;
  riskLevel?: string;
  sideEffectLevel?: string;
  requiresConfirmation: boolean;
  version: string;
  sourceRef: CapabilitySourceRef;
  metadata?: Record<string, unknown>;
}

export interface ExecutionStepRecord {
  id: string;
  executionId: string;
  name: string;
  nodeId?: string;
  nodeType?: string;
  status: ExecutionStepStatus;
  inputSummary: string;
  outputSummary: string;
  errorMessage?: string;
  durationMs: number;
  rawPayloadObjectAddress?: string;
}

export interface Execution {
  id: string;
  workflowId: string;
  workflowVersion: string;
  workspaceId: string;
  trigger: string;
  userId: string;
  traceId: string;
  status: ExecutionStatus;
  durationMs: number;
  inputSummary: string;
  outputSummary: string;
  errorMessage?: string;
  rawPayloadObjectAddress: string;
  steps: ExecutionStepRecord[];
}

export type ChatSessionStatus = "ACTIVE" | "ARCHIVED";
export type ChatMessageRole = "USER" | "ASSISTANT" | "SYSTEM";
export type ChatMessageStatus = "PROCESSING" | "PENDING_CONFIRMATION" | "EXECUTED" | "FAILED";
export type ChatConfirmationStatus = "PENDING" | "CONFIRMED" | "CANCELLED" | "EXPIRED";
export type AgentTargetType = "TOOL" | "WORKFLOW";
export type AgentRunStatus = "PENDING" | "RUNNING" | "WAITING_CONFIRMATION" | "SUCCEEDED" | "FAILED" | "CANCELLED";
export type AgentRunStepStatus =
  | "QUEUED"
  | "RUNNING"
  | "WAITING_CONFIRMATION"
  | "SUCCEEDED"
  | "FAILED"
  | "SKIPPED"
  | "CANCELLED";

export interface ChatSession {
  id: string;
  agentId: string;
  title: string;
  status: ChatSessionStatus;
  latestRunId?: string;
  pendingConfirmationId?: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export interface WorkspaceChatSession extends ChatSession {
  workspaceId: string;
}

export interface ChatMessage {
  id: string;
  role: ChatMessageRole;
  content: string;
  contentSha256: string;
  contentLength: number;
  status: ChatMessageStatus;
  confirmationId?: string;
  runId?: string;
  createdAt: string;
  /** Write-only path: message send may include one-shot attachment id; never a Token value. */
  outboundCredentialAttachmentId?: string;
}

export interface ChatConfirmation {
  id: string;
  sessionId: string;
  runId: string;
  targetType: AgentTargetType;
  targetReleaseId: string;
  riskLevel: string;
  riskReasons: string[];
  inputSummary: unknown;
  status: ChatConfirmationStatus;
  requestedBy: string;
  confirmedBy?: string;
  createdAt: string;
  expiresAt: string;
  confirmedAt?: string;
  lockVersion: number;
  cached: boolean;
}

export interface ChatConfirmationCredential {
  confirmation: ChatConfirmation;
  resumeToken?: string;
}

export interface AgentRun {
  id: string;
  sessionId?: string;
  agentId: string;
  status: AgentRunStatus;
  triggerType: string;
  triggeredByType: string;
  triggeredById: string;
  traceId: string;
  modelSnapshot: unknown;
  capabilitySnapshot: unknown;
  inputSummary: unknown;
  outputSummary: unknown;
  errorCode?: string;
  startedAt: string;
  finishedAt?: string;
  lockVersion: number;
}

export interface AgentRunStep {
  id: string;
  sequenceNo: number;
  stepType: string;
  status: AgentRunStepStatus;
  capabilityReleaseId?: string;
  inputSummary: unknown;
  outputSummary: unknown;
  errorCode?: string;
  startedAt: string;
  finishedAt?: string;
}

export interface ChatRunSubmissionResult {
  session: ChatSession;
  message: ChatMessage;
  runId: string;
}

export interface WorkflowExecution {
  id: string;
  workflowId: string;
  revisionId?: string;
  agentRunId?: string;
  triggerType: string;
  triggeredByType: string;
  triggeredById: string;
  traceId: string;
  status: AgentRunStatus;
  inputSummary: unknown;
  outputSummary: unknown;
  errorCode?: string;
  startedAt: string;
  finishedAt?: string;
  lockVersion: number;
}

export interface WorkflowExecutionStep {
  id: string;
  nodeId: string;
  nodeType: string;
  sequenceNo: number;
  status: AgentRunStepStatus;
  inputSummary: unknown;
  outputSummary: unknown;
  errorCode?: string;
  startedAt: string;
  finishedAt?: string;
}

// ---------------------------------------------------------------------------
// Intelligent orchestration (smart-dag.v2) — FE/BE aligned DTOs (P6.1)
// Backend: generate_session.go generateSessionDTO / generateTurnDTO / applyTurn payload
// ---------------------------------------------------------------------------

export type SmartGenerateSessionStatus = "OPEN" | "CLOSED";
export type SmartGenerateTurnStatus = "SUCCEEDED" | "GUARD_REJECTED" | "FAILED";

export interface SmartDAGGuardViolation {
  code: string;
  message: string;
  nodeId?: string;
  fieldPath?: string;
}

export interface SmartDAGGuardReport {
  ok: boolean;
  violations: SmartDAGGuardViolation[];
}

/** POST/GET workflow-generate-sessions session payload (generateSessionDTO). */
export interface SmartGenerateSession {
  sessionId: string;
  agentId: string;
  modelConfigId: string;
  workflowId?: string | null;
  status: SmartGenerateSessionStatus;
  promptId?: string;
  promptHash?: string;
  constraints?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
  closedAt?: string | null;
}

/** Turn history row (generateTurnDTO). */
export interface SmartGenerateTurn {
  turnId: string;
  turnIndex: number;
  userMessage: string;
  assistantMessage?: string;
  generationId: string;
  guardOk: boolean;
  guardReport?: SmartDAGGuardReport;
  draftVersion?: number | null;
  status: SmartGenerateTurnStatus;
  errorCode?: string;
  promptId?: string;
  promptHash?: string;
  createdAt?: string;
}

export type FailureFeedbackSource = "compile" | "trial" | "production" | "agent_run" | "guard";
export type FailureFeedbackSuggestedAction =
  | "edit_mapping"
  | "replace_tool"
  | "add_approval"
  | "import_tool"
  | "regenerate";

export interface FailureFeedbackIssue {
  code: string;
  nodeId?: string;
  message: string;
  suggestedAction?: FailureFeedbackSuggestedAction;
}

/** D14 FailureFeedback — draft-only revise context (never auto-publish). */
export interface FailureFeedback {
  source: FailureFeedbackSource;
  workflowId: string;
  compilationId?: string;
  executionId?: string;
  runId?: string;
  issues: FailureFeedbackIssue[];
  missingCapabilities?: SmartDAGMissingCapability[];
  rawSummary?: string;
}

export interface SmartDAGReasoningStep {
  id: string;
  label: string;
  status: string;
  detail: string;
}

export interface SmartDAGMissingCapability {
  id: string;
  name: string;
  reason: string;
  suggestedProtocol: string;
}

export interface SmartDAGNodeExplanation {
  nodeId: string;
  title: string;
  reason: string;
}

/** Successful POST .../turns response (aligned with generate_session applyTurn payload). */
export interface SmartDAGTurnResponse {
  sessionId: string;
  turnId: string;
  generationId: string;
  workflow: Workflow;
  draft: WorkflowDraftRecord;
  assistantMessage?: string;
  reasoningSteps?: SmartDAGReasoningStep[];
  missingCapabilities?: SmartDAGMissingCapability[];
  nodeExplanations?: SmartDAGNodeExplanation[];
  availableToolIds?: string[];
  selectedToolIds?: string[];
  confidence?: number;
  guardReport?: SmartDAGGuardReport;
  draftVersion?: number;
  generatedBy?: string;
  promptId?: string;
  promptHash?: string;
  agentId?: string;
  modelConfigId?: string;
  /** Request correlation id (P6.3). */
  traceId?: string;
}

/** Draft graph UI audit stamps written by TurnService (smart-dag.v2). */
export interface SmartDAGDraftAuditUI {
  generatedBy?: "smart-dag.v2" | string;
  sessionId?: string;
  agentId?: string;
  modelConfigId?: string;
  promptId?: string;
  promptHash?: string;
  generationId?: string;
  traceId?: string;
  businessGoal?: string;
  revisedFrom?: Record<string, unknown>;
}

/** Agent full-trace debug audit (PLATFORM_ADMIN /logs). */
export type AgentAuditContentState = "plain" | "redacted" | "cipher" | "missing";

export interface AgentAuditStats {
  totalRuns: number;
  successRate: number;
  failureRate: number;
  avgLatencyMs: number;
}

/** Initiator of an agent run (USER / SYSTEM / service principal). */
export interface AgentAuditActor {
  type?: string;
  id?: string;
  username?: string;
  displayName?: string;
  /** Agent Access client id (when actor is a service principal). */
  clientId?: string;
  clientName?: string;
}

export interface AgentAuditTraceListItem {
  traceId: string;
  startedAt: string;
  finishedAt?: string;
  status: string;
  model: string;
  userLabel: string;
  user?: AgentAuditActor;
  latencyMs?: number;
  stepCount: number;
  runIds: string[];
}

export interface AgentAuditListResult {
  items: AgentAuditTraceListItem[];
  stats: AgentAuditStats;
  debugMode: boolean;
  /** Distinct traces matching the current list filter (for pagination). */
  total: number;
}

export interface AgentAuditStep {
  type: string;
  title: string;
  timeOffsetMs: number;
  latencyMs?: number;
  content?: string;
  contentState?: AgentAuditContentState;
  params?: unknown;
  result?: unknown;
  paramsState?: AgentAuditContentState;
  resultState?: AgentAuditContentState;
  runId?: string;
  stepId?: string;
  invocationId?: string;
  /** Nested agent attribution (AGENT_DELEGATION hierarchy). */
  agentId?: string;
  delegationId?: string;
  parentStepId?: string;
  parentDelegationId?: string;
  childRunId?: string;
  callerAgentId?: string;
  targetAgentId?: string;
  callerAgentName?: string;
  targetAgentName?: string;
  externalAgentRef?: string;
  mode?: string;
  protocol?: string;
  origin?: string;
  depth?: number;
  status?: string;
  errorCode?: string;
  errorMessage?: string;
  /** Remote A2A linkage from agent_run_delegations. */
  remoteTaskId?: string;
  remoteContextId?: string;
  remoteMessageId?: string;
  remoteEndpointRef?: string;
  protocolStatus?: string;
  /** Token usage — omit/null when unknown (never invent 0 for A2A without usage). */
  inputTokens?: number | null;
  outputTokens?: number | null;
  totalTokens?: number | null;
  tokensKnown?: boolean;
  /** Execution dispatch attempts (not finalize-outbox retries). */
  attemptCount?: number;
  retryCount?: number;
  children?: AgentAuditStep[];
  collapsed?: boolean;
}

/** Internal Agent→Agent binding. */
export interface AgentDelegationBinding {
  id: string;
  workspaceId: string;
  callerAgentId: string;
  targetAgentId: string;
  callableName: string;
  description: string;
  mode: "INLINE" | "TASK";
  contextPolicy: "TASK_ONLY" | "SUMMARY" | "SELECTED_MESSAGES";
  enabled: boolean;
  version: number;
  createdAt: string;
  updatedAt: string;
}

/** A2A inbound exposure. */
export interface AgentA2AExposure {
  id: string;
  workspaceId: string;
  agentId: string;
  publicName: string;
  publicDescription: string;
  enabled: boolean;
  authMode: "AGENT_ACCESS" | "NONE";
  version: number;
  createdAt: string;
  updatedAt: string;
}

/** A2A outbound remote binding. */
export interface AgentA2ARemoteBinding {
  id: string;
  workspaceId: string;
  callerAgentId: string;
  callableName: string;
  description: string;
  endpointUrl: string;
  agentCardUrl?: string;
  allowedHosts: string[];
  authSecretRef?: string;
  timeoutMs: number;
  enabled: boolean;
  version: number;
  createdAt: string;
  updatedAt: string;
}

export interface AgentAuditTraceDetail {
  traceId: string;
  startedAt: string;
  finishedAt?: string;
  latencyMs?: number;
  status: string;
  model: string;
  userLabel: string;
  user?: AgentAuditActor;
  debugMode: boolean;
  steps: AgentAuditStep[];
  runIds: string[];
  /** Full timeline length after build (not just this page). */
  stepTotal?: number;
  stepOffset?: number;
  stepLimit?: number;
  hasMore?: boolean;
}
