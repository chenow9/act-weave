import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const diffViewer = readFileSync(resolve(currentDir, "AgentPromptDiffViewer.vue"), "utf8");

describe("agent prompt diff viewer content", () => {
  it("removes browser-default underlines from inserted and deleted inline diff text", () => {
    expect(diffViewer).toContain(":deep(ins)");
    expect(diffViewer).toContain(":deep(del)");
    expect(diffViewer).toContain("text-decoration: none");
  });
});
