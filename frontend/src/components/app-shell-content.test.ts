import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const appShell = readFileSync(resolve(currentDir, "AppShell.vue"), "utf8");
const navigation = readFileSync(resolve(currentDir, "../config/navigation.ts"), "utf8");

describe("app shell content", () => {
  it("uses the supplied prototype ActWeave mark instead of the temporary initials mark", () => {
    expect(appShell).toContain("app-brand-mark");
    expect(appShell).toContain("fa-solid fa-circle-nodes");
    expect(appShell).not.toContain('class="brand-mark">AW');
  });

  it("makes topbar icon controls route to functional screens", () => {
    expect(appShell).toContain("@click=\"goRuntimeStatus\"");
    expect(appShell).toContain("@click=\"goNotifications\"");
    expect(appShell).toContain('router.push({ name: "workflow" })');
    expect(appShell).toContain('router.push({ name: "logs" })');
  });

  it("does not hard-code seeded data counts in the navigation", () => {
    expect(appShell).not.toContain("TRACE_ACTIVE");
    expect(appShell).not.toContain("RUNTIME: EINO_ENGINE");
    expect(appShell).not.toContain("runtime-footer");
    expect(appShell).not.toContain("live-dot");
    expect(appShell).not.toContain("TRACE_READY");
    expect(navigation).toContain('label: "编排"');
    expect(navigation).toContain('label: "智能编排"');
    expect(navigation).toContain('route: "/smart-dag"');
    expect(navigation).toContain('badge: "AI"');
    expect(navigation).not.toContain('badge: "4"');
    expect(navigation).not.toContain('badge: "8"');
    expect(navigation).not.toContain('badge: "42"');
    expect(navigation).not.toContain('badge: "3"');
    expect(navigation).not.toContain('badge: "M1"');
    expect(navigation).not.toContain('badge: "M2"');
    expect(navigation).not.toContain('badge: "M5"');
  });

  it("keeps the prototype workspace switch free of fabricated metrics", () => {
    expect(appShell).not.toContain("workspaceTopbarThroughput");
    expect(appShell).not.toContain("workspaceTopbarHealth");
    expect(appShell).toContain("workspaceDisplayName");
    expect(appShell).not.toContain("未初始化");
    expect(appShell).not.toContain("1,284 req/min");
    expect(appShell).toContain("workspaces.items.length && activeModule !== 'overview'");
    expect(appShell).toContain("workspaceId !== workspaces.activeWorkspaceId");
  });

  it("lets the chat console occupy the full shell workspace", () => {
    expect(appShell).toContain("content-area--workspace");
    expect(appShell).toContain("activeModule === 'chat'");
  });

  it("handles unauthorized workspace bootstrap failures without leaving shell errors unhandled", () => {
    expect(appShell).toContain("catch (error)");
    expect(appShell).toContain("getHttpStatus(error) === 401");
    expect(appShell).toContain("auth.logout()");
    expect(appShell).toContain('router.push({ name: "login" })');
  });

  it("uses prototype Font Awesome navigation and profile controls instead of Element circular icon defaults", () => {
    expect(appShell).toContain('class="fluid-trigger"');
    expect(appShell).not.toContain('class="command-jump"');
    expect(appShell).not.toContain("跳转到");
    expect(appShell).toContain("fa-solid fa-wave-square");
    expect(appShell).toContain("fa-regular fa-bell");
    expect(appShell).toContain("运行时状态");
    expect(appShell).toContain("通知与审计");
    expect(appShell).toContain('aria-label="退出登录"');
    expect(appShell).not.toContain(":icon=\"DataLine\" circle");
    expect(navigation).toContain('icon: "fa-solid fa-layer-group"');
    expect(navigation).toContain('icon: "fa-solid fa-user-gear"');
    expect(navigation).not.toContain("@element-plus/icons-vue");
  });

  it("uses the fluid navigation and workspace classes required by the supplied prototype", () => {
    expect(appShell).toContain("fluid-island");
    expect(appShell).toContain("live-orb");
    expect(appShell).toContain("island-grid");
    expect(appShell).toContain("island-groups");
    expect(appShell).toContain("user-avatar");
    expect(appShell).toContain("logout-button");
    expect(appShell).not.toContain("showWorkspaceContext");
    expect(appShell).not.toContain("topbar-context");
    expect(appShell).toContain("workspace-switcher");
    expect(appShell).toContain("切换当前业务空间");
    expect(appShell).toContain("搜索业务空间");
    expect(appShell).toContain("管理业务空间");
    expect(appShell).toContain("workspaces.selectWorkspace(workspaceId)");
    expect(appShell).toContain(":key=\"workspaces.activeWorkspaceId || 'no-workspace'\"");
    expect(appShell).not.toContain("Workspace Service</span>");
    expect(appShell).not.toContain("<el-select");
    expect(appShell).not.toContain("<el-option");
  });
});
