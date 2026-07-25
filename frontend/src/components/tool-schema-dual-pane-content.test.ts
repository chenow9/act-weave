import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const dualPane = readFileSync(resolve(currentDir, "ToolSchemaDualPane.vue"), "utf8");
const jsonEditor = readFileSync(resolve(currentDir, "ToolSchemaJsonEditor.vue"), "utf8");
const treeView = readFileSync(resolve(currentDir, "ToolSchemaTreeView.vue"), "utf8");
const treeEditor = readFileSync(resolve(currentDir, "ToolSchemaTreeEditor.vue"), "utf8");
const appStyles = readFileSync(resolve(currentDir, "..", "styles", "app.css"), "utf8");

function lastStyleBlock(selector: string) {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const blocks = [...appStyles.matchAll(new RegExp(`${escapedSelector}\\s*\\{[\\s\\S]*?\\n\\}`, "g"))];
  return blocks.at(-1)?.[0] || "";
}

describe("tool schema dual pane", () => {
  it("keeps JSON and readonly structured mapping surfaces side by side", () => {
    expect(dualPane).toContain("tool-schema-linked-json");
    expect(dualPane).toContain("tool-schema-linked-table");
    expect(dualPane).toContain("linkedRows");
    expect(dualPane).toContain("linkedJsonLines");
    expect(dualPane).toContain("selectedFieldId");
    expect(dualPane).toContain("hoveredFieldId");
    expect(dualPane).toContain("data-schema-field-id");
    expect(dualPane).toContain("data-schema-line-key");
    expect(dualPane).toContain("结构化字段说明");
    expect(dualPane).toContain("只读映射预览");
    expect(dualPane).not.toContain("ToolSchemaJsonEditor");
    expect(dualPane).not.toContain("parseContractJson");
    expect(dualPane).not.toContain("SCHEMA_SPECIFICATION");
  });

  it("renders editor validation affordances", () => {
    expect(jsonEditor).toContain("payload_schema.jsonc");
    expect(jsonEditor).toContain("tool-contract-editor-caption-action");
    expect(jsonEditor).toContain("实时解析同步中");
    expect(jsonEditor).toContain("formatDocument");
    expect(jsonEditor).toContain("parseError");
    expect(jsonEditor).not.toContain("EditorState");
  });

  it("renders the right side as a schema specification browser instead of an editor stack", () => {
    expect(dualPane).toContain("<table class=\"tool-schema-linked-table\">");
    expect(dualPane).toContain("字段名");
    expect(dualPane).toContain("位置");
    expect(dualPane).toContain("数据类型");
    expect(dualPane).toContain("必填状态");
    expect(dualPane).toContain("字段说明");
    expect(dualPane).toContain("fieldNameLabel(row)");
    expect(dualPane).toContain("locationLabel(row)");
    expect(dualPane).toContain("typeLabel(row.type)");
    expect(treeView).not.toContain("位置 (IN)");
    expect(treeView).not.toContain("默认字段描述");
    expect(dualPane).toContain("buildLinkedRows");
    expect(dualPane).toContain("buildLinkedJsonLines");
    expect(dualPane).not.toContain("split(\".\")");
  });

  it("uses supported VXE table tree lines and native title overflow in schema tables", () => {
    for (const component of [treeView, treeEditor]) {
      expect(component).toContain("showLine: true");
      expect(component).toContain('show-overflow="title"');
      expect(component).not.toContain("line: true");
      expect(component).not.toContain('show-overflow="tooltip"');
    }
  });

  it("keeps request parameters and structured compare panes readable at table density", () => {
    const dualPaneBlock = appStyles.match(/\.tool-contract-dual-pane\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const linkedCodeLineBlock = lastStyleBlock(".tool-schema-linked-code-line");
    const linkedTableCellBlock = lastStyleBlock(".tool-schema-linked-table td");
    const linkedTableHeaderBlock = lastStyleBlock(".tool-schema-linked-table th");
    const linkedFieldNameBlock = lastStyleBlock(".tool-schema-linked-field-name");
    const specDescriptionBlock = appStyles.match(/\.tool-schema-view-spec \.tool-schema-view-description\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const schemaInputBlock = appStyles.match(/\.tool-schema-cell-input\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const nameBlock = appStyles.match(/\.tool-schema-view-name\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const rootNameBlock = appStyles.match(/\.tool-schema-view-name\.is-root\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const descriptionBlock = appStyles.match(/\.tool-schema-view-description\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const typeBlock = appStyles.match(/\.tool-schema-view-type\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const optionalBlock = appStyles.match(/\.tool-schema-view-required\.optional\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(dualPaneBlock).toContain("grid-template-columns: minmax(480px, 1fr) minmax(480px, 1fr);");
    expect(dualPaneBlock).toContain("gap: 22px;");
    expect(linkedCodeLineBlock).toContain("border-left: 3px solid var(--schema-anchor-color);");
    expect(linkedTableCellBlock).toContain("padding: 12px 14px;");
    expect(linkedTableHeaderBlock).toContain("height: 42px;");
    expect(linkedFieldNameBlock).toContain("min-height: 36px;");
    expect(specDescriptionBlock).toContain("line-height: 1.6;");
    expect(schemaInputBlock).toContain("height: 40px;");

    // Default schema tree (tool detail 入参/出参) must use light-surface readable colors.
    expect(nameBlock).toContain("color: #334155;");
    expect(rootNameBlock).toContain("color: #0f766e;");
    expect(descriptionBlock).toContain("color: #334155;");
    expect(typeBlock).toContain("color: #92400e;");
    expect(optionalBlock).toContain("color: #047857;");
    expect(nameBlock).not.toContain("color: #d6e4ef;");
    expect(descriptionBlock).not.toContain("color: #d8e7f3;");
  });

  it("documents persistent visual mapping and click selection in compare mode", () => {
    expect(dualPane).toContain("anchorPalette");
    expect(dualPane).toContain("anchorStyle");
    expect(dualPane).toContain("--schema-anchor-color");
    expect(dualPane).toContain("is-linked");
    expect(dualPane).toContain("is-selected");
    expect(dualPane).toContain("@mouseenter=\"hoverField");
    expect(dualPane).toContain("@click=\"selectField");
    expect(dualPane).toContain("scrollIntoView");
    expect(appStyles).toContain(".tool-schema-linked-table tr.is-selected td");
    expect(appStyles).toContain(".tool-schema-linked-code-line.is-selected");
  });
});
