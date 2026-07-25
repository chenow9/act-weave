import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const auditLogsView = readFileSync(resolve(currentDir, "AuditLogsView.vue"), "utf8");
const agentAuditStore = readFileSync(resolve(currentDir, "../stores/agentAudit.ts"), "utf8");
const navigation = readFileSync(resolve(currentDir, "../config/navigation.ts"), "utf8");
const router = readFileSync(resolve(currentDir, "../router/index.ts"), "utf8");

describe("agent full-trace audit logs page", () => {
  it("uses agent-audit store and list/detail timeline structure", () => {
    expect(auditLogsView).toContain("useAgentAuditStore");
    expect(auditLogsView).toContain("Agent 审计中心");
    expect(auditLogsView).toContain("loadTraces");
    expect(auditLogsView).toContain("loadTraceDetail");
    expect(auditLogsView).toContain("timeline");
    expect(auditLogsView).toContain("无推理数据");
    expect(auditLogsView).toContain("数据脱敏");
    expect(auditLogsView).toContain("debugMode");
    expect(auditLogsView).not.toContain("audit-events");
    expect(auditLogsView).not.toContain("createExport");
  });

  it("follows the global topbar workspace and does not render a page-local workspace selector", () => {
    expect(auditLogsView).toContain("activeWorkspaceId");
    expect(auditLogsView).toContain("watch(workspaceId");
    expect(auditLogsView).toContain("agentAudit.goToList()");
    expect(auditLogsView).not.toContain("selectedWorkspaceId");
    expect(auditLogsView).not.toContain("workspaceOptions");
    expect(auditLogsView).not.toContain("workspace-select");
    // Page size uses shared AppSelect; no workspace selector.
    expect(auditLogsView).toContain('aria-label="每页条数"');
    expect(auditLogsView).toContain("AppSelect");
    expect(auditLogsView).not.toContain("<select");
  });

  it("gates the page to platform admins via nav and route meta", () => {
    expect(navigation).toMatch(/id:\s*"logs"[\s\S]*platformAdminOnly:\s*true/);
    expect(router).toContain('name: "logs"');
    expect(router).toContain("requiresPlatformAdmin: true");
  });

  it("store calls agent-audit APIs keyed by trace", () => {
    expect(agentAuditStore).toContain("/agent-audit/traces");
    expect(agentAuditStore).toContain("encodeURIComponent(traceId)");
    expect(agentAuditStore).toContain("debugMode");
    expect(agentAuditStore).toContain("isMasked");
  });

  it("applies presentation-layer masking to user labels and sensitive JSON keys", () => {
    expect(auditLogsView).toContain("displayUserLabel");
    expect(auditLogsView).toContain("isSensitiveKey");
    expect(auditLogsView).toContain("maskSensitiveText");
    expect(auditLogsView).toContain("SENSITIVE_KEY_NEEDLES");
    expect(auditLogsView).toContain("displayUserLabel(item.userLabel)");
    expect(auditLogsView).toContain("detail-cell");
    expect(auditLogsView).toContain("white-space: nowrap");
    expect(auditLogsView).toContain("inline-flex");
    expect(auditLogsView).not.toContain("{{ item.userLabel || \"—\" }}");
  });

  it("scrolls the content area to top when entering or leaving trace detail", () => {
    expect(auditLogsView).toContain("scrollAuditToTop");
    expect(auditLogsView).toContain("pageRef");
    expect(auditLogsView).toContain('closest(".content-area")');
    expect(auditLogsView).toContain("scrollTo({ top: 0");
    expect(auditLogsView).toMatch(/openDetail[\s\S]*scrollAuditToTop/);
    expect(auditLogsView).toMatch(/backToList[\s\S]*scrollAuditToTop/);
  });

  it("paginates the call record list with page size controls", () => {
    expect(auditLogsView).toContain("trace-pagination");
    expect(auditLogsView).toContain("changePage");
    expect(auditLogsView).toContain("changePageSize");
    expect(auditLogsView).toContain("searchList");
    expect(auditLogsView).toContain("pageSizeSelectOptions");
    expect(auditLogsView).toContain("compact");
    expect(auditLogsView).toContain("agentAudit.total");
    expect(auditLogsView).toContain("agentAudit.page");
    expect(agentAuditStore).toContain("pageSize");
    expect(agentAuditStore).toContain("DEFAULT_PAGE_SIZE = 10");
    expect(agentAuditStore).toContain('queryParams.set("page"');
    expect(agentAuditStore).toContain("total");
  });
});
