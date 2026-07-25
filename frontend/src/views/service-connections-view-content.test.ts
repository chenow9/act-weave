import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const serviceConnectionsView = readFileSync(resolve(currentDir, "ServiceConnectionsView.vue"), "utf8");
const dataTable = readFileSync(resolve(currentDir, "../components/DataTable.vue"), "utf8");

describe("service connections view", () => {
  it("marks a connection as usable only when the v1 status is VERIFIED", () => {
    expect(serviceConnectionsView).toContain("function syncConnectionVerifiedState");
    expect(serviceConnectionsView).toContain('status === "VERIFIED"');
    expect(serviceConnectionsView).not.toContain('status === "Available"');
    expect(serviceConnectionsView).toContain('verification.status === "SUCCEEDED" ? "VERIFIED" : "ERROR"');
  });

  it("uses ManagementList for the service connection registry while preserving detail and form shells", () => {
    expect(serviceConnectionsView).toContain("connectionStatusFilter");
    expect(serviceConnectionsView).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(serviceConnectionsView).toContain("<ManagementSegmentedFilter");
    expect(serviceConnectionsView).toContain('{ label: "全部", value: "ALL" }');
    expect(serviceConnectionsView).toContain('{ label: "已验证", value: "VERIFIED" }');
    expect(serviceConnectionsView).toContain('{ label: "未验证", value: "UNVERIFIED" }');
    expect(serviceConnectionsView).toContain('{ label: "错误", value: "ERROR" }');
    expect(serviceConnectionsView).toContain('ariaLabel="服务连接状态筛选"');
    expect(serviceConnectionsView).not.toContain("connection-abnormal-toggle");
    expect(serviceConnectionsView).not.toContain("showAbnormalOnly");
    expect(serviceConnectionsView).toContain("connectionCurrentView");
    expect(serviceConnectionsView).toContain("service-connections-page");
    expect(serviceConnectionsView).toContain("connection-reference-banner");
    expect(serviceConnectionsView).toContain("connection-reference-table-card");
    expect(serviceConnectionsView).toContain('import ManagementList, { type ManagementListColumn }');
    expect(serviceConnectionsView).toContain("connectionColumns");
    expect(serviceConnectionsView).toContain("<ManagementList");
    expect(serviceConnectionsView).toContain('storage-key="actweave:service-connections:columns"');
    expect(serviceConnectionsView).toContain(':sticky-left-keys="[\'name\']"');
    expect(serviceConnectionsView).toContain(':sticky-right-keys="[\'actions\']"');
    expect(serviceConnectionsView).toContain(':selectable="false"');
    expect(serviceConnectionsView).toContain(':has-loaded="connectionsHasLoaded"');
    expect(serviceConnectionsView).toContain(':error="connectionLoadError"');
    expect(serviceConnectionsView).toContain(':rows="integration.serviceConnectionPageItems"');
    expect(serviceConnectionsView).toContain(':pagination="integration.serviceConnectionPagination"');
    expect(serviceConnectionsView).toContain('@update:search="updateConnectionSearch"');
    expect(serviceConnectionsView).toContain('@page-change="changeConnectionPage"');
    expect(serviceConnectionsView).toContain('@reset="resetConnectionFilters"');
    expect(serviceConnectionsView).toContain("function loadConnectionPage");
    expect(serviceConnectionsView).not.toContain("visibleConnections");
    expect(serviceConnectionsView).not.toContain("connection-result-count");
    expect(serviceConnectionsView).toContain('<template #filters>');
    expect(serviceConnectionsView).toContain('<template #card="{ row: connection }">');
    expect(serviceConnectionsView).toContain('<template #empty>');
    expect(serviceConnectionsView).toContain('<template #error="{ error }">');
    expect(serviceConnectionsView).toContain("router.push('/providers')");
    expect(serviceConnectionsView).not.toContain("providerFormVisible");
    expect(serviceConnectionsView).toContain("credentialSecretId");
    expect(serviceConnectionsView).toContain("connection-status-pill");
    expect(serviceConnectionsView).toContain('label: "协议"');
    expect(serviceConnectionsView).toContain('label: "环境"');
    expect(serviceConnectionsView).toContain('label: "认证方式"');
    expect(serviceConnectionsView).toContain("ManagementRowActions");
    expect(serviceConnectionsView).toContain("connectionMenuActions");
    expect(serviceConnectionsView).toContain("handleConnectionRowAction");
    expect(serviceConnectionsView).toContain('{ key: "actions", label: "操作", width: 68');
    expect(serviceConnectionsView).toContain('label: "查看详情"');
    expect(serviceConnectionsView).toContain("fa-solid fa-eye");
    expect(serviceConnectionsView).not.toContain("connection-icon-action");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
    expect(serviceConnectionsView).toContain("connection-name-cell");
    expect(serviceConnectionsView).toContain("min-width: 0");
    expect(serviceConnectionsView).toContain("text-overflow: ellipsis");
    expect(serviceConnectionsView).toContain("connection-empty-state");
    expect(serviceConnectionsView).toContain("connection-detail-hero");
    expect(serviceConnectionsView).toContain("connection-verdict-banner");
    expect(serviceConnectionsView).toContain("connection-detail-grid");
    expect(serviceConnectionsView).toContain("connection-form-modal");
    expect(serviceConnectionsView).toContain("connection-form-workspace");
    expect(serviceConnectionsView).toContain("connection-reference-select");
    expect(serviceConnectionsView).toContain("connection-form-single-column");
    expect(serviceConnectionsView).toContain("connection-verification-section");
    expect(serviceConnectionsView).toContain("connection-advanced-section");
    expect(serviceConnectionsView).toContain('aria-controls="connection-verification-fields"');
    expect(serviceConnectionsView).toContain('aria-controls="connection-advanced-fields"');
    expect(serviceConnectionsView).not.toContain("connection-form-summary");
    expect(serviceConnectionsView).not.toContain("connection-pm-note");
    expect(serviceConnectionsView).not.toContain("connection-intro-steps");
    expect(serviceConnectionsView).not.toContain('verificationPath || "/health"');
    expect(serviceConnectionsView).toContain("<style scoped>");
    expect(serviceConnectionsView).not.toContain("<el-drawer");
    expect(serviceConnectionsView).not.toContain("connection-card-grid");
    expect(serviceConnectionsView).not.toContain("service-connection-card");
    expect(serviceConnectionsView).not.toContain('<table class="service-connection-table">');
  });

  it("defines page-owned mobile connection cards with an accessible action menu", () => {
    expect(serviceConnectionsView).toContain("connection-mobile-card");
    expect(serviceConnectionsView).toContain("mobileConnectionActionMenuId");
    expect(serviceConnectionsView).toContain("连接操作");
    expect(serviceConnectionsView).toContain('role="menu"');
    expect(serviceConnectionsView).toContain('role="menuitem"');
  });

  it("styles passed and failed form diagnostic rows independently", () => {
    expect(serviceConnectionsView).toContain(".connection-form-verification-result.pending");
    expect(serviceConnectionsView).toContain(".connection-form-checks > span.passed");
    expect(serviceConnectionsView).toContain(".connection-form-checks > span.failed");
  });

  it("renders address column like the name cell: icon, host title, and 验证 subtitle", () => {
    expect(serviceConnectionsView).toContain("connectionAddressPrimary");
    expect(serviceConnectionsView).toContain("verificationMethodLabel");
    expect(serviceConnectionsView).toContain("connection-address-cell");
    expect(serviceConnectionsView).toContain("connection-address-body");
    expect(serviceConnectionsView).toContain("connection-address-host");
    expect(serviceConnectionsView).toContain("connection-address-verify");
    expect(serviceConnectionsView).toContain("fa-solid fa-link");
    expect(serviceConnectionsView).toContain("验证 ·");
    expect(serviceConnectionsView).not.toContain("connection-method-pill");
    expect(serviceConnectionsView).not.toContain(
      'aria-label="`完整服务地址：${connectionAddress(connection)}`">{{ connectionAddress(connection) }}</code>',
    );
  });

  it("uses one compact visual hierarchy for create and edit connection forms", () => {
    expect(serviceConnectionsView).toContain('class="connection-form-title-lockup"');
    expect(serviceConnectionsView).toContain('class="connection-form-icon"');
    expect(serviceConnectionsView).toContain('class="connection-form-section basic"');
    expect(serviceConnectionsView).toContain('class="connection-section-icon"');
    expect(serviceConnectionsView).toContain('class="connection-disclosure-copy"');
    expect(serviceConnectionsView).toContain('class="connection-disclosure-icon verification"');
    expect(serviceConnectionsView).toContain('class="connection-disclosure-icon advanced"');
    expect(serviceConnectionsView).toContain("fa-solid fa-link");
    expect(serviceConnectionsView).toContain("fa-solid fa-vial-circle-check");
    expect(serviceConnectionsView).toContain("fa-solid fa-sliders");
  });

  it("styles the shared form as a compact ACTWEAVE management modal", () => {
    expect(serviceConnectionsView).toMatch(
      /\.connection-form-icon\s*\{[^}]*background:\s*var\(--aw-cyan-soft\);[^}]*color:\s*var\(--aw-cyan\);/s,
    );
    expect(serviceConnectionsView).toMatch(
      /\.connection-form-body\s*\{[^}]*background:\s*#f8fafc;/s,
    );
    expect(serviceConnectionsView).toMatch(
      /\.connection-form-section\.basic\s*\{[^}]*border-radius:\s*12px;[^}]*background:\s*#fff;/s,
    );
    expect(serviceConnectionsView).toMatch(
      /\.connection-disclosure-body\s*\{[^}]*border-top:\s*1px solid #eef2f7;/s,
    );
    expect(serviceConnectionsView).toMatch(
      /\.connection-disclosure-icon\.advanced\s*\{[^}]*background:\s*var\(--aw-bg\);[^}]*color:\s*var\(--aw-muted\);/s,
    );
    expect(serviceConnectionsView).toMatch(
      /\.connection-field input,\s*\.connection-reference-select\s*\{[^}]*background:\s*#f8fafc;/s,
    );
    expect(serviceConnectionsView).toMatch(
      /\.connection-form-actions\s*\{[^}]*border-top:\s*1px solid #eef2f7;[^}]*background:\s*#f8fafc;/s,
    );
  });

  it("matches the model API modal's single thin focus ring", () => {
    expect(serviceConnectionsView).toMatch(
      /\.connection-field input:focus,\s*\.connection-reference-select:focus\s*\{[^}]*outline:\s*none;[^}]*border-color:\s*rgba\(16, 185, 129, 0\.6\);[^}]*box-shadow:\s*0 0 0 2px rgba\(16, 185, 129, 0\.15\);/s,
    );
    expect(serviceConnectionsView).not.toMatch(
      /\.connection-detail-back:focus-visible,\s*\.connection-field input:focus-visible,/s,
    );
  });
});
