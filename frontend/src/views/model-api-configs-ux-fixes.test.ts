import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const modelApiConfigsView = readFileSync(resolve(currentDir, "ModelAPIConfigsView.vue"), "utf8");
const dataTableComponent = readFileSync(resolve(currentDir, "../components/DataTable.vue"), "utf8");
const managementListComponent = readFileSync(resolve(currentDir, "../components/ManagementList.vue"), "utf8");
const managementRowActionsComponent = readFileSync(resolve(currentDir, "../components/ManagementRowActions.vue"), "utf8");
const appStyles = readFileSync(resolve(currentDir, "../styles/app.css"), "utf8");

describe("model api configs UX audit fixes", () => {
  it("keeps the model config modal keyboard-operable and scroll-safe", () => {
    expect(modelApiConfigsView).toContain("function handleModelModalKeydown");
    expect(modelApiConfigsView).toContain("function focusInitialModelModalElement");
    expect(modelApiConfigsView).toContain("window.addEventListener(\"keydown\", handleModelModalKeydown)");
    expect(modelApiConfigsView).toContain("<Transition name=\"modal-fade\">");
    expect(modelApiConfigsView).toContain("ref=\"modelModalRef\"");
    expect(modelApiConfigsView).toContain("data-modal-initial-focus");
    expect(modelApiConfigsView).toContain("aria-label=\"关闭模型配置弹窗\"");
    expect(modelApiConfigsView).toContain("min-height: 0;");
    expect(modelApiConfigsView).toContain("overflow-y: auto;");
  });

  it("prevents duplicate model API requests with explicit busy states", () => {
    expect(modelApiConfigsView).toContain("const verifyingModelId = ref<string | null>(null)");
    expect(modelApiConfigsView).toContain("const savingModelConfig = ref(false)");
    expect(modelApiConfigsView).toContain("if (verifyingModelId.value) return;");
    expect(modelApiConfigsView).toContain("if (savingModelConfig.value) return;");
    expect(modelApiConfigsView).toContain(":disabled=\"Boolean(verifyingModelId)\"");
    expect(modelApiConfigsView).toContain(":disabled=\"savingModelConfig\"");
    expect(modelApiConfigsView).toContain("fa-solid fa-spinner fa-spin");
  });

  it("makes table overflow, row selection, long text, and action controls accessible", () => {
    expect(dataTableComponent).toContain("table-layout: fixed;");
    expect(dataTableComponent).toContain("position: sticky;");
    expect(dataTableComponent).toContain("style.right");
    expect(dataTableComponent).toContain("function selectRowFromKeyboard");
    expect(dataTableComponent).toContain("event.target !== event.currentTarget");
    expect(dataTableComponent).toContain("@keydown.enter=\"selectRowFromKeyboard($event, row)\"");
    expect(dataTableComponent).toContain("@keydown.space=\"selectRowFromKeyboard($event, row)\"");
    expect(modelApiConfigsView).toContain(":sticky-left-keys=\"['config']\"");
    expect(modelApiConfigsView).toContain(":sticky-right-keys=\"['actions']\"");
    expect(modelApiConfigsView).toContain("storage-key=\"actweave:model-api-configs:columns\"");
    expect(modelApiConfigsView).toContain('label: "测试模型 API 连接"');
    expect(modelApiConfigsView).toContain('label: "编辑模型配置"');
    expect(modelApiConfigsView).toContain('label: "删除模型配置"');
    expect(managementRowActionsComponent).toContain(':aria-label="actionItem.label"');
    expect(modelApiConfigsView).toContain("aria-label=\"复制 API 请求地址\"");
  });

  it("adds accessible filters, toast semantics, and documented target sizes", () => {
    expect(modelApiConfigsView).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(modelApiConfigsView).toContain("<ManagementSegmentedFilter");
    expect(managementListComponent).toContain('clearSearchAriaLabel: "清除搜索"');
    expect(modelApiConfigsView).toContain("role=\"status\"");
    expect(modelApiConfigsView).toContain("aria-live=\"polite\"");
    expect(modelApiConfigsView).toContain("aria-label=\"关闭提示\"");
    expect(modelApiConfigsView).toContain("min-height: 44px;");
    expect(modelApiConfigsView).toContain("width: 44px;");
    expect(modelApiConfigsView).toContain("color: #475569;");
  });

  it("meets the P2 responsive, contrast, and shared focus contracts", () => {
    expect(dataTableComponent).not.toContain("right: auto !important;");
    expect(dataTableComponent).toContain("position: sticky;");
    expect(dataTableComponent).toContain("min-height: 44px;");
    expect(modelApiConfigsView).not.toContain("function handleModelStatusFilterKeydown");
    expect(modelApiConfigsView).not.toMatch(/\.model-status-filter button\s*\{/);
    expect(modelApiConfigsView).toContain("color: #047857;");
    expect(modelApiConfigsView).toContain("color: #64748b;");
    expect(modelApiConfigsView).toContain("min-height: 220px;");
    expect(modelApiConfigsView).toContain(":disabled=\"Boolean(verifyingModelId)\"");
    expect(modelApiConfigsView).toContain("required");
    expect(modelApiConfigsView).toContain("aria-invalid");
    expect(appStyles).toContain("a[href]:focus-visible");
  });
});
