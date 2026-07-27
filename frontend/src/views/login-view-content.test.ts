import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const currentDir = dirname(fileURLToPath(import.meta.url));
const loginView = readFileSync(resolve(currentDir, "LoginView.vue"), "utf8");

describe("login view content", () => {
  it("uses ActWeave platform copy and removes unrelated enterprise auth actions", () => {
    expect(loginView).toContain("ACTWEAVE 织行");
    expect(loginView).toContain("Workflow DAG");
    expect(loginView).toContain("Tool Runtime");
    expect(loginView).toContain("JWT 本地会话");
    expect(loginView).toContain("登录 ActWeave");

    for (const forbidden of ["NEXUS GOVERNANCE", "Keycloak", "企业微信", "飞书", "忘记密码", "统一身份认证"]) {
      expect(loginView).not.toContain(forbidden);
    }
  });

  it("implements the refactor prototype login affordances", () => {
    expect(loginView).toContain("showPassword");
    expect(loginView).toContain("login-field-shell");
    expect(loginView).toContain("login-field-icon");
    expect(loginView).toContain("login-password-toggle");
    expect(loginView).toContain("fa-regular fa-eye");
    expect(loginView).toContain("fa-regular fa-eye-slash");
    expect(loginView).toContain("login-feedback-panel success");
    expect(loginView).toContain("login-feedback-panel error");
    expect(loginView).toContain("安全验证通过");
    expect(loginView).toContain("请输入已配置的账户凭证登录控制台");
    expect(loginView).not.toContain("演示凭证");
    expect(loginView).not.toContain("actweave-demo");
  });

  it("routes temporary-password logins to change-password and shows post-change notice", () => {
    expect(loginView).toContain('name: "change-password"');
    expect(loginView).toContain("mustChangePassword");
    expect(loginView).toContain("passwordChanged");
    expect(loginView).toContain("密码已更新");
  });
});
