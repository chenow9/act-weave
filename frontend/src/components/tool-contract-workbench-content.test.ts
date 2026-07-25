import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const workbenchView = readFileSync(resolve(currentDir, "ToolContractWorkbench.vue"), "utf8");
const jsonEditorView = readFileSync(resolve(currentDir, "ToolSchemaJsonEditor.vue"), "utf8");
const treeView = readFileSync(resolve(currentDir, "ToolSchemaTreeView.vue"), "utf8");
const appStyles = readFileSync(resolve(currentDir, "..", "styles", "app.css"), "utf8");

describe("tool contract workbench", () => {
  it("uses parameter details as the only contract workbench view", () => {
    expect(workbenchView).toContain("参数详情");
    expect(workbenchView).toContain("ToolSchemaDualPane");
    expect(workbenchView).not.toContain("JSON 视图");
    expect(workbenchView).not.toContain("结构视图");
    expect(workbenchView).not.toContain("双栏对照");
    expect(workbenchView).not.toContain("modeOptions");
    expect(workbenchView).not.toContain("defaultMode");
  });

  it("reuses the linked parameter detail surface inside a dedicated workbench modal", () => {
    expect(workbenchView).toContain("ToolSchemaDualPane");
    expect(workbenchView).toContain("modal-backdrop");
    expect(workbenchView).toContain("tool-contract-workbench-modal");
    expect(workbenchView).toContain("embedded");
    expect(workbenchView).toContain("@click.self=\"!props.embedded && updateVisible(false)\"");
    expect(appStyles).toContain("tool-contract-workbench-embedded");
    expect(workbenchView).not.toContain("el-drawer");
    expect(workbenchView).not.toContain("@update:model-value=\"updateVisible\"");
    expect(workbenchView).toContain("tool-contract-workbench");
  });

  it("keeps the workbench header compact instead of repeating a long description block", () => {
    expect(workbenchView).toContain("参数详情");
    expect(workbenchView).not.toContain("<p>{{ description }}</p>");
  });

  it("uses shared modal focus behavior", () => {
    expect(workbenchView).toContain("useModalFocus");
    expect(workbenchView).toContain("data-modal-initial-focus");
  });

  it("removes obsolete mode switching controls", () => {
    expect(workbenchView).not.toContain("tool-contract-workbench-mode-switch");
  });

  it("uses a light theme for payload_schema.jsonc and the structured field explanation pane", () => {
    const editorShellBlock = appStyles.match(/\.tool-contract-editor-shell-ide,\s*\.tool-schema-view-spec\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const codeEditorBlock = appStyles.match(/\.tool-contract-code-editor\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const treeSpecBlock = appStyles.match(/\.tool-schema-tree-spec\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(jsonEditorView).toContain('backgroundColor: "#ffffff"');
    expect(jsonEditorView).toContain("{ dark: false }");
    expect(treeView).toContain('data-vxe-ui-theme="light"');
    expect(treeView).not.toContain("data-vxe-ui-theme=\"variantMode === 'spec' ? 'dark' : 'light'\"");
    expect(editorShellBlock).toContain("background: #fff;");
    expect(codeEditorBlock).toContain("background: #ffffff;");
    expect(treeSpecBlock).toContain("background: #fff;");
    expect(editorShellBlock).not.toContain("#12182b");
    expect(codeEditorBlock).not.toContain("#1e1e1e");
    expect(treeSpecBlock).not.toContain("#0f1424");
  });
});
