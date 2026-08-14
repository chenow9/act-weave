import { tt } from "../i18n/tt";
import type { ServiceConnection, Tool, ToolRequestParam } from "../types/domain";

export type GovernanceTone = "neutral" | "success" | "warning" | "danger" | "info";

export interface GovernanceStatusMeta {
  label: string;
  tone: GovernanceTone;
  description: string;
}

export interface PublishChecklistItem {
  id: string;
  label: string;
  passed: boolean;
  severity: "error" | "warning";
  detail: string;
}

export interface PublishChecklistOptions {
  agentImpactConfirmed?: boolean;
}

export function getToolLifecycleStatus(tool: Tool): GovernanceStatusMeta {
  const map: Record<Tool["status"], GovernanceStatusMeta> = {
    Draft: { label: tt("tools.govDraft"), tone: "warning", description: tt("tools.govDraftDesc") },
    Review: { label: tt("tools.govReview"), tone: "warning", description: tt("tools.govReviewDesc") },
    Tested: { label: tt("tools.govTested"), tone: "info", description: tt("tools.govTestedDesc") },
    Published: { label: tt("tools.govPublished"), tone: "success", description: tt("tools.govPublishedDesc") },
    Disabled: { label: tt("tools.govDisabled"), tone: "neutral", description: tt("tools.govDisabledDesc") },
  };
  return map[tool.status] || { label: tool.status, tone: "neutral", description: tt("tools.govUnknown") };
}

export function getToolTestStatus(tool: Tool): GovernanceStatusMeta {
  if (hasPassingToolTest(tool)) {
    return { label: tt("tools.govTestPass"), tone: "success", description: tt("tools.govTestPassDesc") };
  }
  // Prefer additive latestTest; null/missing → never infer pass from lifecycle.
  if (tool.latestTest) {
    if (tool.latestTest.status === "FAILED") {
      return {
        label: tt("tools.govTestFail"),
        tone: "danger",
        description: tool.latestTest.errorCode
          ? tt("tools.govTestFailCode", { code: tool.latestTest.errorCode })
          : tt("tools.govTestFailDesc"),
      };
    }
  }
  if (!tool.lastTestResult && !tool.latestTest) {
    // Draft / Review: still waiting for first test. Published / Tested / Disabled
    // without retained history: historical result is unknown (do not treat as pass).
    if (tool.status === "Draft" || tool.status === "Review") {
      return { label: tt("tools.govWaitTest"), tone: "warning", description: tt("tools.govWaitTestDesc") };
    }
    return { label: tt("tools.govTestUnknown"), tone: "neutral", description: tt("tools.govTestUnknownDesc") };
  }
  if (!tool.lastTestResult) {
    if (tool.status === "Draft" || tool.status === "Review") {
      return { label: tt("tools.govWaitTest"), tone: "warning", description: tt("tools.govWaitTestDesc") };
    }
    return { label: tt("tools.govTestUnknown"), tone: "neutral", description: tt("tools.govTestUnknownShort") };
  }
  return { label: tt("tools.govTestFail"), tone: "danger", description: tt("tools.govTestFailDesc") };
}

export function hasPassingToolTest(tool: Tool): boolean {
  // ZKL-56: never infer success from Published lifecycle alone.
  if (tool.latestTest) {
    return tool.latestTest.status === "SUCCEEDED";
  }
  if (tool.lastTestResult) {
    return tool.lastTestResult.status === "SUCCEEDED" || tool.lastTestResult.status === "Tested";
  }
  return false;
}

const CONNECTION_DANGER_STATUSES = new Set(["Needs attention", "UNVERIFIED", "ERROR", "DISABLED"]);

/**
 * Catalog-aware availability (ZKL-56 §4.6.2).
 * Only LOADED + entity absent → MISSING; LOADING/ERROR must not show MISSING.
 */
export type CatalogAvailabilityInput = {
  catalogStatus?: "IDLE" | "LOADING" | "LOADED" | "ERROR";
  connection?: ServiceConnection;
};

export function getToolRunStatus(
  tool: Tool,
  connectionOrOptions?: ServiceConnection | CatalogAvailabilityInput,
): GovernanceStatusMeta {
  const options: CatalogAvailabilityInput =
    connectionOrOptions && "catalogStatus" in (connectionOrOptions as object)
      ? (connectionOrOptions as CatalogAvailabilityInput)
      : { connection: connectionOrOptions as ServiceConnection | undefined };
  const connection = options.connection;
  const catalogStatus = options.catalogStatus;

  if (tool.status === "Disabled") {
    return { label: tt("tools.govRunDisabled"), tone: "neutral", description: tt("tools.govRunDisabledDesc") };
  }
  if (catalogStatus === "IDLE" || catalogStatus === "LOADING") {
    return { label: tt("tools.govConnLoading"), tone: "info", description: tt("tools.govConnLoadingDesc") };
  }
  if (catalogStatus === "ERROR") {
    return { label: tt("tools.govConnUnknown"), tone: "warning", description: tt("tools.govConnUnknownDesc") };
  }
  if (!connection) {
    // Only true missing after LOADED (or when catalog status unknown and caller passed no connection).
    return { label: tt("tools.govConnMissing"), tone: "danger", description: tt("tools.govConnMissingDesc") };
  }
  if ((connection as ServiceConnection & { migrationState?: string }).migrationState === "MIGRATION_REQUIRED") {
    return { label: tt("tools.govConnMigrate"), tone: "warning", description: tt("tools.govConnMigrateDesc") };
  }
  if (CONNECTION_DANGER_STATUSES.has(connection.status)) {
    const reason = connectionStatusReason(connection.status);
    return {
      label: tt("tools.govConnAttention"),
      tone: "danger",
      description: reason,
    };
  }
  if (connection.status === "Expiring soon") {
    return { label: tt("tools.govCredExpiring"), tone: "warning", description: tt("tools.govCredExpiringDesc") };
  }
  if (connection.status === "Available" || connection.status === "VERIFIED") {
    return { label: tt("tools.govCallable"), tone: "success", description: tt("tools.govCallableDesc") };
  }
  return { label: tt("tools.govNoObs"), tone: "neutral", description: tt("tools.govNoObsDesc") };
}

/** True when the Tool's bound connection blocks or degrades safe invocation. */
export function toolHasConnectionAttention(tool: Tool, connection?: ServiceConnection): boolean {
  if (tool.status === "Disabled") return false;
  const run = getToolRunStatus(tool, connection);
  return run.tone === "danger" || run.tone === "warning";
}

export interface ToolUnifiedStatusMeta extends GovernanceStatusMeta {
  /** Lifecycle label alone (e.g. Published). */
  lifecycleLabel: string;
  /** Connection/run label when it overrides or composes the pill. */
  runLabel?: string;
  /** True when status is driven by connection health rather than pure lifecycle. */
  connectionAttention: boolean;
}

/**
 * Table status model (scheme A):
 * - lifecycleLabel → primary pill (lifecycle tone)
 * - runLabel → secondary attention line when connection/test needs attention
 * - label remains a composite string for search/sort/getValue/title fallbacks
 */
export function getToolUnifiedStatus(tool: Tool, connection?: ServiceConnection): ToolUnifiedStatusMeta {
  const lifecycle = getToolLifecycleStatus(tool);
  const test = getToolTestStatus(tool);
  const run = getToolRunStatus(tool, connection);

  if (run.tone === "danger" || run.tone === "warning") {
    const publishedHint = tool.status === "Published" ? tt("tools.govPublishedUnsafe") : lifecycle.description;
    return {
      label: `${lifecycle.label} · ${run.label}`,
      tone: run.tone,
      description: tt("tools.govStatusCompose", { run: run.description, hint: publishedHint }),
      lifecycleLabel: lifecycle.label,
      runLabel: run.label,
      connectionAttention: true,
    };
  }

  if (test.tone === "danger") {
    return {
      label: `${lifecycle.label} · ${test.label}`,
      tone: test.tone,
      description: test.description,
      lifecycleLabel: lifecycle.label,
      runLabel: test.label,
      connectionAttention: false,
    };
  }

  return {
    label: lifecycle.label,
    tone: lifecycle.tone,
    description: lifecycle.description,
    lifecycleLabel: lifecycle.label,
    connectionAttention: false,
  };
}

function connectionStatusReason(status: string): string {
  switch (status) {
    case "UNVERIFIED":
      return tt("tools.govReasonUnverified");
    case "ERROR":
      return tt("tools.govReasonError");
    case "DISABLED":
      return tt("tools.govReasonDisabled");
    case "Needs attention":
      return tt("tools.govReasonAttention");
    default:
      return tt("tools.govReasonDefault");
  }
}

export function buildToolPublishChecklist(
  tool: Tool,
  connection?: ServiceConnection,
  options: PublishChecklistOptions = {},
): PublishChecklistItem[] {
  const path = String(tool.actionConfig.path || "");
  const method = String(tool.actionConfig.method || "");
  const pathParamNames = extractPathParamNames(path);
  const declaredPathParams = new Set(
    tool.requestParams.filter((param) => param.location === "Path").map((param) => param.name),
  );
  const missingPathParams = pathParamNames.filter((name) => !declaredPathParams.has(name));
  const testPassed = hasPassingToolTest(tool);

  return [
    {
      id: "base-info-complete",
      label: tt("tools.checkBaseInfo"),
      passed: Boolean(tool.name.trim() && tool.description.trim() && (tool.updatedBy || tool.createdBy || "").trim()),
      severity: "error",
      detail: tt("tools.checkBaseInfoDetail"),
    },
    {
      id: "connection-available",
      label: tt("tools.checkConnection"),
      passed: Boolean(connection && ["Available", "VERIFIED"].includes(connection.status)),
      severity: "error",
      detail: connection
        ? tt("tools.checkConnectionDetailStatus", { status: connection.status })
        : tt("tools.checkConnectionDetailMissing"),
    },
    {
      id: "method-endpoint-configured",
      label: tt("tools.checkMethodEndpoint"),
      passed: Boolean(method && path.startsWith("/")),
      severity: "error",
      detail: tt("tools.checkMethodEndpointDetail"),
    },
    {
      id: "path-params-match",
      label: tt("tools.checkPathParams"),
      passed: missingPathParams.length === 0,
      severity: "error",
      detail: missingPathParams.length
        ? tt("tools.checkPathParamsMissing", { names: missingPathParams.join("、") })
        : tt("tools.checkPathParamsOk"),
    },
    {
      id: "request-contract-valid",
      label: tt("tools.checkRequestContract"),
      passed: hasNamedFields(tool.requestParams),
      severity: "error",
      detail: tt("tools.checkRequestContractDetail"),
    },
    {
      id: "response-contract-valid",
      label: tt("tools.checkResponseContract"),
      passed: tool.responseFields.every((field) => Boolean(field.name.trim() && field.type.trim())),
      severity: "error",
      detail: tt("tools.checkResponseContractDetail"),
    },
    {
      id: "latest-test-passed",
      label: tt("tools.checkLatestTest"),
      passed: testPassed,
      severity: "error",
      detail: testPassed ? tt("tools.checkLatestTestOk") : tt("tools.checkLatestTestNeed"),
    },
    {
      id: "timeout-policy-configured",
      label: tt("tools.checkTimeout"),
      passed: tool.runtimePolicy.timeoutMs > 0,
      severity: "error",
      detail: tt("tools.checkTimeoutDetail"),
    },
    {
      id: "retry-policy-configured",
      label: tt("tools.checkRetry"),
      passed: tool.runtimePolicy.retryCount >= 0 && Boolean(tool.runtimePolicy.backoffPolicy),
      severity: "error",
      detail: tt("tools.checkRetryDetail"),
    },
    {
      id: "agent-impact-confirmed",
      label: tt("tools.checkAgentImpact"),
      passed: options.agentImpactConfirmed === true,
      severity: "warning",
      detail: tt("tools.checkAgentImpactDetail"),
    },
  ];
}

export function checklistHasBlockingErrors(items: PublishChecklistItem[]) {
  return items.some((item) => !item.passed && item.severity === "error");
}

export function checklistHasWarnings(items: PublishChecklistItem[]) {
  return items.some((item) => !item.passed && item.severity === "warning");
}

function extractPathParamNames(path: string) {
  return Array.from(path.matchAll(/\{([^}]+)\}/g))
    .map((match) => match[1] || "")
    .filter(Boolean);
}

function hasNamedFields(params: ToolRequestParam[]) {
  return params.every((param) => Boolean(param.name.trim() && param.type.trim()));
}
