import { describe, expect, it } from "vitest";

import { messages } from "./messages";
import { createTestI18n } from "../test-utils/i18n";

function leafPaths(value: unknown, prefix = ""): string[] {
  if (value !== null && typeof value === "object" && !Array.isArray(value)) {
    return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
      leafPaths(child, prefix ? `${prefix}.${key}` : key),
    );
  }
  return prefix ? [prefix] : [];
}

/** Keys that must resolve through real vue-i18n t() for batch-2 chrome surfaces. */
const CHROME_KEYS = [
  "workspaces.tabOverview",
  "workspaces.tabMembers",
  "workspaces.create",
  "workspaces.deleteTitle",
  "workspaces.nameEn",
  "tools.colName",
  "tools.colStatus",
  "tools.emptyTitle",
  "tools.noMatchTitle",
  "tools.batchTest",
  "tools.summaryTotal",
  "agents.colWorkspace",
  "agents.colStatus",
  "agents.emptyTitle",
  "agents.backToList",
  "agents.createTitle",
  "agents.params",
  "workflow.colName",
  "workflow.emptyTitle",
  "workflow.noMatchTitle",
  "workflow.saveCanvas",
  "workflow.publish",
  "workflow.canvasEdit",
  "workflow.checkIssues",
  "workflow.readinessCompile",
  "workflow.readinessTrial",
  "workflow.readinessPublish",
  "workflow.nodeStart",
  "workflow.nodeTool",
  "workflow.nodeCondition",
  "tools.govPublished",
  "tools.govConnAttention",
  "agents.collabExternal",
  "agents.initialSystemPrompt",
  "agents.aiEnhance",
  "agents.firstParagraphPreview",
] as const;

describe("domain chrome i18n (batch 2)", () => {
  it("keeps en/zh-CN leaf parity for migrated domains", () => {
    for (const domain of [
      "workspaces",
      "tools",
      "agents",
      "workflow",
      "providers",
      "connections",
      "modelApis",
      "openapi",
      "agentAccess",
      "users",
      "logs",
      "chat",
      "common",
    ] as const) {
      const zhKeys = leafPaths(messages["zh-CN"][domain]).sort();
      const enKeys = leafPaths(messages.en[domain]).sort();
      expect(enKeys, domain).toEqual(zhKeys);
    }
  });

  it("resolves list/studio/editor chrome keys via real t() for both locales", () => {
    for (const locale of ["en", "zh-CN"] as const) {
      const i18n = createTestI18n(locale);
      const t = i18n.global.t.bind(i18n.global);
      for (const key of CHROME_KEYS) {
        const value = String(t(key));
        expect(value, `${locale} ${key}`).not.toBe(key);
        expect(value.length, `${locale} ${key}`).toBeGreaterThan(0);
        if (locale === "en") {
          // English chrome must not be pure Chinese prose
          expect(/[\u4e00-\u9fff]/.test(value), `${key}=${value}`).toBe(false);
        }
      }
    }
  });

  it("ships English product chrome for tools/agents/workflow empty titles", () => {
    const i18n = createTestI18n("en");
    const t = i18n.global.t.bind(i18n.global);
    expect(String(t("tools.emptyTitle"))).toMatch(/tool/i);
    expect(String(t("agents.emptyTitle"))).toMatch(/agent/i);
    expect(String(t("workflow.emptyTitle"))).toMatch(/workflow/i);
    expect(String(t("workflow.saveCanvas"))).toMatch(/save|canvas/i);
  });
});

describe("skeptic-gap chrome keys (en)", () => {
  it("resolves tool governance list pills in English via shipped getToolLifecycleStatus", async () => {
    const { setI18nLocale } = await import("./index");
    const { getToolLifecycleStatus, getToolRunStatus } = await import("../utils/tool-governance");
    setI18nLocale("en");
    const tool = {
      id: "t1",
      workspaceId: "w1",
      providerId: "p1",
      connectionId: "c1",
      defaultConnectionId: "c1",
      name: "t",
      slug: "t",
      protocol: "HTTP",
      actionConfig: { method: "GET", path: "/x" },
      actionConfigSchemaVersion: "http.action.v1",
      description: "",
      status: "Published",
      capabilityStatus: "ACTIVE",
      versions: [],
      requestParams: [],
      responseFields: [],
      errorMappings: [],
      timeoutMs: 1000,
      retryPolicy: { maxAttempts: 1, backoff: "fixed" },
      rateLimitPolicy: "",
      lockVersion: 1,
      createdAt: "",
      updatedAt: "",
    } as any;
    expect(getToolLifecycleStatus(tool).label).toBe("Published");
    expect(getToolLifecycleStatus(tool).label).not.toMatch(/[\u4e00-\u9fff]/);
    const conn = {
      id: "c1",
      workspaceId: "w1",
      providerId: "p1",
      name: "c",
      status: "Needs attention",
      authType: "NONE",
      baseUrl: "http://x",
      lockVersion: 1,
      createdAt: "",
      updatedAt: "",
    } as any;
    expect(getToolRunStatus(tool, conn).label).toBe("Connection needs attention");
  });

  it("resolves workflow readiness and node palette labels in English", () => {
    const i18n = createTestI18n("en");
    const t = i18n.global.t.bind(i18n.global);
    for (const key of [
      "workflow.readinessCompile",
      "workflow.readinessTrial",
      "workflow.readinessPublish",
      "workflow.nodeStart",
      "workflow.nodeTool",
      "workflow.nodeCondition",
      "workflow.nodeApproval",
    ]) {
      const v = String(t(key));
      expect(v).not.toBe(key);
      expect(v).not.toMatch(/[\u4e00-\u9fff]/);
    }
    expect(String(t("workflow.readinessCompile"))).toBe("Compile");
    expect(String(t("workflow.nodeCondition"))).toBe("Condition");
  });
});
