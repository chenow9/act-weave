import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const modelApiConfigsView = readFileSync(resolve(currentDir, "ModelAPIConfigsView.vue"), "utf8");
const managementList = readFileSync(resolve(currentDir, "../components/ManagementList.vue"), "utf8");
const managementRowActions = readFileSync(resolve(currentDir, "../components/ManagementRowActions.vue"), "utf8");
const dataTable = readFileSync(resolve(currentDir, "../components/DataTable.vue"), "utf8");

describe("model api configs view", () => {
  it("submits new model configs through the store create action", () => {
    expect(modelApiConfigsView).toContain("async function saveDraftModelConfig()");
    expect(modelApiConfigsView).toContain("await modelConfigs.createModelConfig({");
    expect(modelApiConfigsView).toContain("...draftModelConfig.value");
    expect(modelApiConfigsView).toContain('@click="saveDraftModelConfig"');
  });

  it("submits edited model configs through the store update action and keeps provider fixed", () => {
    expect(modelApiConfigsView).toContain("async function saveEditedModelConfig()");
    expect(modelApiConfigsView).toContain("await modelConfigs.updateModelConfig(editingModelDraft.value.id, {");
    expect(modelApiConfigsView).toContain("...editingModelDraft.value");
    expect(modelApiConfigsView).toContain("OPENAI_COMPATIBLE_PROVIDER");
    expect(modelApiConfigsView).not.toContain('v-model="draftModelConfig.provider"');
    expect(modelApiConfigsView).not.toContain('v-model="editingModelDraft.provider"');
  });

  it("matches the actweave_model_api_configs reference table, empty states, and scoped modal controls", () => {
    const scopedStyle = modelApiConfigsView.match(/<style scoped>[\s\S]*<\/style>/)?.[0] || "";

    expect(modelApiConfigsView).toContain("ManagementList");
    expect(modelApiConfigsView).toContain("modelConfigColumns");
    expect(modelApiConfigsView).toContain('storage-key="actweave:model-api-configs:columns"');
    expect(modelApiConfigsView).toContain(':sticky-left-keys="[\'config\']"');
    expect(modelApiConfigsView).toContain(':sticky-right-keys="[\'actions\']"');
    expect(modelApiConfigsView).toContain("modelStatusFilter");
    expect(modelApiConfigsView).toContain("model-config-page");
    expect(modelApiConfigsView).toContain("ManagementPageHeader");
    expect(modelApiConfigsView).toContain("model-config-header");
    expect(modelApiConfigsView).toContain("model-config-card");
    expect(modelApiConfigsView).toContain("model-config-management-list");
    expect(modelApiConfigsView).toContain("@update:search=\"setModelSearch\"");
    expect(modelApiConfigsView).toContain("@page-change=\"changeModelConfigPage\"");
    expect(modelApiConfigsView).toContain("<template #filters>");
    expect(modelApiConfigsView).toContain("<template #card=");
    expect(modelApiConfigsView).toContain("model-config-name-cell");
    expect(modelApiConfigsView).not.toContain("model-empty-state-icon compact");
    expect(modelApiConfigsView).toContain("model-provider-pill");
    expect(modelApiConfigsView).toContain("model-latency-value");
    expect(modelApiConfigsView).toContain("ManagementRowActions");
    expect(modelApiConfigsView).not.toContain("model-row-actions");
    expect(modelApiConfigsView).toContain("modelModalVisible");
    expect(modelApiConfigsView).toContain("model-modal-backdrop");
    expect(modelApiConfigsView).toContain("model-modal-card");
    expect(modelApiConfigsView).toContain("model-modal-head");
    expect(modelApiConfigsView).toContain("model-modal-form");
    expect(modelApiConfigsView).toContain("model-modal-field");
    expect(modelApiConfigsView).toContain("model-modal-actions");
    expect(modelApiConfigsView).toContain("<style scoped>");
    expect(modelApiConfigsView).not.toContain("<el-drawer");
    expect(modelApiConfigsView).toContain('import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue"');
    expect(modelApiConfigsView).toContain("<ManagementSegmentedFilter");
    expect(modelApiConfigsView).not.toContain("search-box");
    expect(modelApiConfigsView).not.toContain("model-api-table-head");
    expect(modelApiConfigsView).not.toContain("model-api-list");
    expect(modelApiConfigsView).not.toContain("model-api-row");
    expect(modelApiConfigsView).toContain('label: "配置名称"');
    expect(modelApiConfigsView).toContain('label: "Provider"');
    expect(modelApiConfigsView).toContain('label: "凭据"');
    expect(modelApiConfigsView).not.toContain('label: "API Key"');
    expect(modelApiConfigsView).toContain('label: "API 请求地址"');
    expect(modelApiConfigsView).toContain('label: "模型名称"');
    expect(modelApiConfigsView).toContain('label: "延迟"');
    expect(modelApiConfigsView).toContain('label: "操作"');
    expect(modelApiConfigsView).toContain('headerAlign: "center"');
    expect(scopedStyle).toContain(".model-config-page");
    expect(scopedStyle).toContain(".model-config-card.management-list-card");
    expect(scopedStyle).toContain("background: transparent");
    expect(modelApiConfigsView).toContain('class="primary-button"');
    expect(scopedStyle).not.toContain(".model-config-management-list :deep(.data-table .is-sticky-boundary-right::after)");
    expect(scopedStyle.match(/\.model-status-filter\s*\{[\s\S]*?\}/)?.[0] || "").not.toContain("margin-left: auto;");
    expect(managementList).toContain("management-list-toolbar-actions");
    expect(managementList).toContain("justify-content: space-between;");
    expect(managementList).not.toContain("margin-right: 52px;");
    expect(managementList).not.toContain("top: -66px;");
    expect(managementList).not.toContain(".management-list-pagination button {");
    expect(managementList).toContain("management-list-pagination-button");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
    expect(dataTable).toContain("box-shadow: -4px 0 10px -6px rgba(15, 23, 42, 0.16);");
    expect(managementRowActions).toContain("width: 44px;");
    expect(managementRowActions).toContain("height: 44px;");
    expect(scopedStyle).toContain(".model-modal-head");
    expect(scopedStyle).toContain("background: #fff;");
    expect(scopedStyle).toContain("border-bottom: 1px solid #eef2f7;");
    expect(scopedStyle).toContain("background: #d1f0d0;");
    expect(scopedStyle).toContain("color: #15803d;");
    expect(scopedStyle).toContain("color: #0f172a;");
    expect(scopedStyle).toContain(".model-config-management-list :deep(.registry-empty-state)");
    expect(scopedStyle).toContain("min-height: 220px;");
    expect(scopedStyle).toContain("max-width: 380px;");
    expect(scopedStyle).toContain(".registry-empty-state .primary-button");
    expect(scopedStyle).toContain(".registry-empty-state .ghost-button");
    expect(modelApiConfigsView).toContain('class="primary-button"');
    expect(scopedStyle).toContain(".model-provider-pill");
    expect(scopedStyle).toContain(".model-modal-field");
  });

  it("delegates status filter interaction and desktop visuals to the shared segmented filter", () => {
    expect(modelApiConfigsView).not.toContain("function handleModelStatusFilterKeydown");
    expect(modelApiConfigsView).not.toMatch(/\.model-status-filter button\s*\{/);
  });

  it("clips model card contents while the column settings menu is teleported", () => {
    expect(modelApiConfigsView.match(/\.model-config-card\.management-list-card\s*\{[\s\S]*?\}/)?.[0]).toContain("background: transparent");
  });
});
