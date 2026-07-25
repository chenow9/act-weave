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
    Draft: { label: "草稿", tone: "warning", description: "尚未完成测试和发布" },
    Review: { label: "待配置", tone: "warning", description: "配置已保存，等待补齐测试或发布条件" },
    Tested: { label: "待发布", tone: "info", description: "最近一次测试通过，尚未发布给 Agent" },
    Published: { label: "已发布", tone: "success", description: "可被 Agent 或 Workflow 调用" },
    Disabled: { label: "已停用", tone: "neutral", description: "当前不会开放给 Agent 调用" },
  };
  return map[tool.status] || { label: tool.status, tone: "neutral", description: "未知生命周期状态" };
}

export function getToolTestStatus(tool: Tool): GovernanceStatusMeta {
  if (hasPassingToolTest(tool)) {
    return { label: "测试通过", tone: "success", description: "最近一次测试通过" };
  }
  if (!tool.lastTestResult) {
    return { label: "未测试", tone: "neutral", description: "暂无测试记录" };
  }
  return { label: "测试失败", tone: "danger", description: "最近一次测试失败，需要修复后重试" };
}

export function hasPassingToolTest(tool: Tool): boolean {
  if (tool.lastTestResult) return tool.lastTestResult.status === "Tested";
  return tool.status === "Tested" || tool.status === "Published";
}

const CONNECTION_DANGER_STATUSES = new Set(["Needs attention", "UNVERIFIED", "ERROR", "DISABLED"]);

export function getToolRunStatus(tool: Tool, connection?: ServiceConnection): GovernanceStatusMeta {
  if (tool.status === "Disabled") {
    return { label: "已停用", tone: "neutral", description: "Tool 已停用，运行状态不再更新" };
  }
  if (!connection) {
    return { label: "连接缺失", tone: "danger", description: "找不到绑定的服务连接" };
  }
  if (CONNECTION_DANGER_STATUSES.has(connection.status)) {
    const reason = connectionStatusReason(connection.status);
    return {
      label: "连接需处理",
      tone: "danger",
      description: reason,
    };
  }
  if (connection.status === "Expiring soon") {
    return { label: "凭证将过期", tone: "warning", description: "服务连接凭证即将过期" };
  }
  return { label: "暂无观测", tone: "neutral", description: "后端尚未提供运行健康、调用量和失败率" };
}

/** True when the Tool's bound connection blocks or degrades safe invocation. */
export function toolHasConnectionAttention(tool: Tool, connection?: ServiceConnection): boolean {
  if (tool.status === "Disabled") return false;
  const run = getToolRunStatus(tool, connection);
  return run.tone === "danger" || run.tone === "warning";
}

export interface ToolUnifiedStatusMeta extends GovernanceStatusMeta {
  /** Lifecycle label alone (e.g. 已发布). */
  lifecycleLabel: string;
  /** Connection/run label when it overrides or composes the pill. */
  runLabel?: string;
  /** True when status is driven by connection health rather than pure lifecycle. */
  connectionAttention: boolean;
}

/**
 * Single table-status pill: keep lifecycle visible, but surface connection
 * problems without dropping the published/draft signal.
 */
export function getToolUnifiedStatus(tool: Tool, connection?: ServiceConnection): ToolUnifiedStatusMeta {
  const lifecycle = getToolLifecycleStatus(tool);
  const test = getToolTestStatus(tool);
  const run = getToolRunStatus(tool, connection);

  if (run.tone === "danger" || run.tone === "warning") {
    const publishedHint =
      tool.status === "Published" ? "已发布但当前不可安全调用，请先处理服务连接" : lifecycle.description;
    return {
      label: `${lifecycle.label} · ${run.label}`,
      tone: run.tone,
      description: `${run.description}。${publishedHint}`,
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
      return "服务连接尚未验证通过";
    case "ERROR":
      return "服务连接验证失败或运行异常";
    case "DISABLED":
      return "服务连接已停用";
    case "Needs attention":
      return "服务连接需要处理（认证、迁移或配置问题）";
    default:
      return "服务连接不可用或认证需要处理";
  }
}

export function formatToolUpdatedAt(tool: Tool) {
  if (!tool.updatedAt) return "暂无数据";
  const date = new Date(tool.updatedAt);
  if (Number.isNaN(date.getTime())) return "暂无数据";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
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
      label: "基础信息完整",
      passed: Boolean(tool.name.trim() && tool.description.trim() && (tool.updatedBy || tool.createdBy || "").trim()),
      severity: "error",
      detail: "Tool 名称、说明和维护人必须完整。",
    },
    {
      id: "connection-available",
      label: "服务连接可用",
      passed: Boolean(connection && ["Available", "VERIFIED"].includes(connection.status)),
      severity: "error",
      detail: connection ? `当前连接状态：${connection.status}` : "未找到绑定的服务连接。",
    },
    {
      id: "method-endpoint-configured",
      label: "Method 与 Endpoint 已配置",
      passed: Boolean(method && path.startsWith("/")),
      severity: "error",
      detail: "Endpoint Path 需要以 / 开头。",
    },
    {
      id: "path-params-match",
      label: "Path 参数与 URL 匹配",
      passed: missingPathParams.length === 0,
      severity: "error",
      detail: missingPathParams.length ? `缺少 Path 参数：${missingPathParams.join("、")}` : "Path 参数已匹配。",
    },
    {
      id: "request-contract-valid",
      label: "入参契约合法",
      passed: hasNamedFields(tool.requestParams),
      severity: "error",
      detail: "至少需要一个命名入参或明确无入参。",
    },
    {
      id: "response-contract-valid",
      label: "出参契约合法",
      passed: tool.responseFields.every((field) => Boolean(field.name.trim() && field.type.trim())),
      severity: "error",
      detail: "所有出参字段都需要字段名和类型。",
    },
    {
      id: "latest-test-passed",
      label: "最近一次测试通过",
      passed: testPassed,
      severity: "error",
      detail: testPassed ? "最近一次测试通过。" : "发布前必须执行并通过测试。",
    },
    {
      id: "timeout-policy-configured",
      label: "已配置超时策略",
      passed: tool.runtimePolicy.timeoutMs > 0,
      severity: "error",
      detail: "超时时间必须大于 0。",
    },
    {
      id: "retry-policy-configured",
      label: "已配置重试策略",
      passed: tool.runtimePolicy.retryCount >= 0 && Boolean(tool.runtimePolicy.backoffPolicy),
      severity: "error",
      detail: "重试次数和退避策略必须明确。",
    },
    {
      id: "agent-impact-confirmed",
      label: "已确认 Agent 引用影响面",
      passed: options.agentImpactConfirmed === true,
      severity: "warning",
      detail: "后端暂未提供 Agent / Workflow 引用明细，发布前需要人工确认影响面。",
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
  return Array.from(path.matchAll(/\{([^}]+)\}/g)).map((match) => match[1] || "").filter(Boolean);
}

function hasNamedFields(params: ToolRequestParam[]) {
  return params.every((param) => Boolean(param.name.trim() && param.type.trim()));
}
