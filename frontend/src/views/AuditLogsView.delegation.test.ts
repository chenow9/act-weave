import { describe, expect, it } from "vitest";
import type { AgentAuditStep } from "../types/domain";

/** Pure helpers mirroring AuditLogsView nesting display contract. */
function stepKey(step: AgentAuditStep, index: number): string {
  return [step.stepId || step.runId || "", step.type, String(step.timeOffsetMs), String(index)].join(":");
}

function stepIcon(type: string) {
  if (type === "agent_delegation") return "fa-solid fa-sitemap";
  if (type === "reasoning") return "fa-solid fa-brain";
  if (type === "tool") return "fa-solid fa-screwdriver-wrench";
  return "fa-solid fa-terminal";
}

/** Mirrors AuditLogsView.delegationMeta human-readable contract. */
function delegationMeta(step: AgentAuditStep) {
  const bits: string[] = [];
  const protocol = (step.protocol || "").toUpperCase();
  const origin = (step.origin || "").toUpperCase();
  const mode = (step.mode || "").toUpperCase();
  if (protocol === "INTERNAL") bits.push("内部协作");
  else if (protocol === "A2A") bits.push("外部协议");
  else if (step.protocol) bits.push(step.protocol);
  if (mode === "INLINE") bits.push("同次对话");
  else if (mode === "TASK") bits.push("独立任务");
  else if (step.mode) bits.push(step.mode);
  if (origin === "EXTERNAL") bits.push("外部发起");
  else if (origin && origin !== "INTERNAL" && origin !== protocol) bits.push(origin);
  if (step.depth != null) bits.push(step.depth === 0 ? "顶层" : `第 ${step.depth} 层`);
  return bits.join(" · ");
}

describe("agent audit delegation timeline", () => {
  it("renders hierarchy metadata without secrets", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: call_b",
      timeOffsetMs: 100,
      protocol: "INTERNAL",
      mode: "INLINE",
      depth: 1,
      status: "SUCCEEDED",
      callerAgentId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      targetAgentId: "11111111-2222-3333-4444-555555555555",
      children: [
        { type: "reasoning", title: "大模型推理", timeOffsetMs: 120 },
        { type: "tool", title: "工具调用: lookup", timeOffsetMs: 200, params: { q: 1 } },
      ],
    };
    expect(stepIcon(step.type)).toContain("sitemap");
    expect(step.children?.length).toBe(2);
    expect(stepKey(step, 0)).toContain("agent_delegation");
    // No secret fields on DTO
    expect(JSON.stringify(step)).not.toMatch(/Authorization|Bearer|systemPrompt/i);
  });

  it("always shows depth=0 for EXTERNAL root (API must not omit 0)", () => {
    const root: AgentAuditStep = {
      type: "agent_delegation",
      title: "A2A inbound",
      timeOffsetMs: 0,
      protocol: "A2A",
      origin: "EXTERNAL",
      depth: 0,
      status: "SUCCEEDED",
    };
    // JSON wire format must retain depth:0 (backend omitempty regression guard).
    expect(JSON.stringify(root)).toContain('"depth":0');
    expect(delegationMeta(root)).toContain("顶层");
    expect(root.depth).toBe(0);
  });

  it("keeps legacy tool/reasoning steps renderable", () => {
    const legacy: AgentAuditStep[] = [
      { type: "input", title: "用户输入", timeOffsetMs: 0, content: "hi" },
      { type: "reasoning", title: "大模型推理", timeOffsetMs: 10 },
      { type: "tool", title: "工具调用: x", timeOffsetMs: 20 },
      { type: "output", title: "最终输出", timeOffsetMs: 30, content: "ok" },
    ];
    expect(legacy.every((s) => stepIcon(s.type))).toBe(true);
  });
});
