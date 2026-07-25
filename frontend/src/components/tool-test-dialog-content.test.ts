import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const toolTestDialog = readFileSync(resolve(currentDir, "ToolTestDialog.vue"), "utf8");

describe("tool test dialog content", () => {
  it("uses the refactored modal and local form controls for tool testing", () => {
    expect(toolTestDialog).toContain("modal-backdrop");
    expect(toolTestDialog).toContain("tool-test-modal-card");
    expect(toolTestDialog).toContain("tool-test-input");
    expect(toolTestDialog).toContain("tool-test-checkbox");
    expect(toolTestDialog).toContain("primary-button");
    expect(toolTestDialog).toContain("ghost-button");
    expect(toolTestDialog).not.toContain("<el-dialog");
    expect(toolTestDialog).not.toContain("<el-button");
    expect(toolTestDialog).not.toContain("<el-input");
    expect(toolTestDialog).not.toContain("<el-input-number");
  });

  it("uses shared modal focus behavior and maps credential failures to recovery copy", () => {
    expect(toolTestDialog).toContain("useModalFocus");
    expect(toolTestDialog).toContain("data-modal-initial-focus");
    expect(toolTestDialog).toContain("formatToolTestError");
    expect(toolTestDialog).toContain("服务连接凭证无效或已过期");
  });
});
