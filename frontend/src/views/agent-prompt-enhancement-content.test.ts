import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const agentsView = readFileSync(resolve(currentDir, "AgentsView.vue"), "utf8");

describe("agents prompt enhancement view", () => {
  it("uses the backend prompt enhancement action in preview mode instead of local prompt templating", () => {
    expect(agentsView).toContain("await agents.enhanceAgentPrompt(draftAgent.value, draftAgent.value.systemPrompt, { preview: true })");
    expect(agentsView).toContain("async function applyWeavePreview()");
    expect(agentsView).toContain("preview: false");
    expect(agentsView).toContain("lockVersion: draftAgent.value.lockVersion");
    expect(agentsView).toContain("agent-weave-preview-dialog");
    expect(agentsView).not.toContain("function enhancedPrompt(");
  });

  it("shows backend or timeout errors when enhancement fails", () => {
    expect(agentsView).toContain("function agentActionErrorMessage(error: unknown)");
    expect(agentsView).toContain('return `整理失败：${responseError}`;');
    expect(agentsView).toContain('timeoutCode === "ECONNABORTED"');
  });

  it("shows in-progress feedback while waiting for the model", () => {
    expect(agentsView).toContain("const enhancingAgentId = ref(\"\")");
    expect(agentsView).toContain("正在生成 System Prompt 整理预览，通常需要 1 到 2 分钟");
    expect(agentsView).toContain("canEnhanceDraftPrompt");
    expect(agentsView).toContain(":disabled=\"!canEnhanceDraftPrompt\"");
  });

  it("shows only the current prompt revision identity from the Agent read DTO", () => {
    expect(agentsView).toContain("const promptDetailAgent = ref<Agent | null>(null)");
    expect(agentsView).toContain("function openPromptDetail(agent: Agent)");
    expect(agentsView).toContain("currentPromptRevisionId");
    expect(agentsView).toContain("Read DTO 只返回当前 Revision ID，不返回 Prompt 明文");
    expect(agentsView).toContain("查看 Revision");
    expect(agentsView).toContain("agent-prompt-detail-dialog");
  });
});
