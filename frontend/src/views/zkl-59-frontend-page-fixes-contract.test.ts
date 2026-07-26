import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * ZKL-59 AC-01～AC-12 automation index (checklist item #6).
 * Point to concrete source/test evidence; do not re-implement behavior here.
 */
const dir = dirname(fileURLToPath(import.meta.url));
const root = resolve(dir, "..");

function read(rel: string) {
  return readFileSync(resolve(root, rel), "utf8");
}

describe("ZKL-59 AC automation matrix", () => {
  it("AC-01～03 Workflow revision layout evidence exists", () => {
    const panel = read("components/workflow/WorkflowRevisionPanel.vue");
    const css = read("styles/app.css");
    expect(panel).toContain("workflow-revision-meta-card");
    expect(panel).toContain("displayRevisionId");
    expect(panel).toContain("activate");
    expect(panel).toContain("rollback");
    expect(panel).toContain("compare");
    expect(panel).toContain("disable");
    expect(css).toContain("overflow-y: auto");
    expect(css).toContain(".workflow-detail-modal-body");
    expect(css).toContain("flex-wrap: wrap");
  });

  it("AC-04～05 Chat archive busy and non-danger styles evidence exists", () => {
    const chat = read("views/ChatExecutionView.vue");
    expect(chat).toContain("archivingSession");
    expect(chat).toContain("chat-inline-action");
    expect(chat).toContain("aria-busy");
    expect(chat).not.toMatch(/chat-inline-action[\s\S]{0,120}#b91c1c/);
    expect(chat).toContain("appearance: none");
  });

  it("AC-06 Provider menu short labels evidence exists", () => {
    const actions = read("components/ManagementRowActions.vue");
    const providers = read("views/ProvidersView.vue");
    expect(actions).toContain("menuVisibleLabel");
    expect(actions).toContain("actionShortLabel");
    expect(providers).toContain('shortLabel: "查看能力资产"');
    expect(providers).toContain('shortLabel: "删除"');
  });

  it("AC-07～09 Provider identity multi-select and fail-closed evidence exists", () => {
    const helper = read("views/provider-outbound-identity.ts");
    const providers = read("views/ProvidersView.vue");
    expect(helper).toContain("throw new Error");
    expect(helper).not.toMatch(/supportedModes\.push\("REQUEST_PASSTHROUGH"\)\s*;\s*\}/);
    expect(providers).toContain("已支持");
    expect(providers).toContain("至少选择一种");
    expect(providers).toContain("查看技术约束");
    expect(providers).toContain("provider-identity-mode-error");
  });

  it("AC-10～11 OpenAPI detail shell and states evidence exists", () => {
    const openapi = read("views/OpenAPIImportsView.vue");
    expect(openapi).toContain("openapi-detail-modal-head");
    expect(openapi).toContain("detailLoading");
    expect(openapi).toContain("detailError");
    expect(openapi).toContain("detailRequestSeq");
    expect(openapi).toContain("loadOpenAPIImportDetail");
    expect(openapi).toContain("padding: 20px");
    expect(openapi).toContain("overflow-x: hidden");
  });

  it("AC-12 freeze boundary: no backend/API lifecycle auto-writes in touched surfaces", () => {
    const panel = read("components/workflow/WorkflowRevisionPanel.vue");
    const openapi = read("views/OpenAPIImportsView.vue");
    expect(panel).not.toContain("apiClient");
    expect(panel).not.toContain("compile");
    // Open detail must not call generateToolDrafts
    const openFn = openapi.match(/async function openImportDetail[\s\S]*?\nasync function fetchImportDetail/)?.[0] || "";
    expect(openFn).not.toContain("generateToolDrafts");
    const fetchFn = openapi.match(/async function fetchImportDetail[\s\S]*?\nasync function retryImportDetail/)?.[0] || "";
    expect(fetchFn).toContain("loadOpenAPIImportDetail");
    expect(fetchFn).not.toContain("generateToolDrafts");
  });
});
