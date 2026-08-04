import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import AgentAuditStepNode from "./AgentAuditStepNode.vue";
import type { AgentAuditStep } from "../types/domain";
import { createTestI18n } from "../test-utils/i18n";

const stubs = {
  formatLatency: (ms: number) => `${ms}ms`,
  stepText: () => "",
  displayJson: (v: unknown) => v,
  stepIcon: (type: string) => (type === "agent_delegation" ? "fa-sitemap" : "fa-circle"),
  // Mirrors AuditLogsView.delegationMeta — single source of depth display.
  delegationMeta: (step: AgentAuditStep) => {
    const bits: string[] = [];
    if (step.protocol) bits.push(step.protocol);
    if (step.mode) bits.push(step.mode);
    if (step.depth != null) bits.push(`depth=${step.depth}`);
    if (step.origin) bits.push(step.origin);
    return bits.join(" · ");
  },
};

function mountStep(step: AgentAuditStep, depth = 0) {
  return mount(AgentAuditStepNode, {
    props: {
      step,
      index: 0,
      depth,
      ...stubs,
    },
    global: {
      plugins: [createTestI18n("zh-CN")],
    },
  });
}

describe("AgentAuditStepNode origin-aware path", () => {
  it("EXTERNAL inbound shows externalAgentRef → targetAgentId and depth=0", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: a2a.inbound (service-principal:actor → target)",
      timeOffsetMs: 0,
      protocol: "A2A",
      origin: "EXTERNAL",
      mode: "TASK",
      depth: 0,
      status: "SUCCEEDED",
      callerAgentId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      targetAgentId: "11111111-2222-3333-4444-555555555555",
      externalAgentRef: "service-principal:actor-xyz",
    };
    const w = mountStep(step);
    const path = w.get('[data-testid="delegation-path"]').text();
    expect(path).toContain("service-principal:actor-xyz");
    expect(path).toContain("→");
    expect(path).toContain("11111111");
    // Must NOT render internal caller → target → external chain
    expect(path).not.toMatch(/aaaaaaaa.*→.*11111111.*→.*service-principal/);
    expect(path).not.toContain("aaaaaaaa");
    // depth appears once via delegationMeta only (no second "depth=" append).
    const metaLine = w.get('[data-testid="delegation-meta-line"]').text();
    expect(metaLine).toContain("depth=0");
    expect(metaLine.match(/depth=/g)?.length).toBe(1);
    w.unmount();
  });

  it("does not duplicate depth when delegationMeta already includes it", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: call_b",
      timeOffsetMs: 5,
      protocol: "INTERNAL",
      origin: "INTERNAL",
      mode: "TASK",
      depth: 2,
      callerAgentId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      targetAgentId: "11111111-2222-3333-4444-555555555555",
    };
    const w = mountStep(step);
    const metaLine = w.get('[data-testid="delegation-meta-line"]').text();
    expect(metaLine).toContain("depth=2");
    expect(metaLine.match(/depth=/g)?.length).toBe(1);
    w.unmount();
  });

  it("INTERNAL shows callerAgentId → targetAgentId without external suffix", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: call_b",
      timeOffsetMs: 10,
      protocol: "INTERNAL",
      origin: "INTERNAL",
      mode: "INLINE",
      depth: 1,
      callerAgentId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      targetAgentId: "11111111-2222-3333-4444-555555555555",
    };
    const w = mountStep(step);
    const path = w.get('[data-testid="delegation-path"]').text();
    expect(path).toContain("aaaaaaaa");
    expect(path).toContain("11111111");
    expect(path).not.toContain("service-principal");
    w.unmount();
  });

  it("outbound A2A shows callerAgentId → externalAgentRef", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: remote_helper",
      timeOffsetMs: 20,
      protocol: "A2A",
      origin: "INTERNAL",
      mode: "TASK",
      depth: 1,
      callerAgentId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      targetAgentId: "",
      externalAgentRef: "https://agent.example.com/a2a",
    };
    const w = mountStep(step);
    const path = w.get('[data-testid="delegation-path"]').text();
    expect(path).toContain("aaaaaaaa");
    expect(path).toContain("https://agent.example.com/a2a");
    expect(path).not.toMatch(/→\s*$/);
    w.unmount();
  });

  it("shows 尝试 0 / 重试 0 for pre-dispatch failure (explicit zeros)", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: call_b",
      timeOffsetMs: 0,
      protocol: "INTERNAL",
      origin: "INTERNAL",
      mode: "INLINE",
      depth: 1,
      attemptCount: 0,
      retryCount: 0,
      status: "FAILED",
    };
    const w = mountStep(step);
    const pill = w.get('[data-testid="delegation-attempts"]').text();
    expect(pill).toContain("尝试 0");
    expect(pill).toContain("重试 0");
    w.unmount();
  });

  it("legacy delegation records without attempt fields still show 0/0", () => {
    const step: AgentAuditStep = {
      type: "agent_delegation",
      title: "Agent 调用: legacy",
      timeOffsetMs: 0,
      protocol: "INTERNAL",
      origin: "INTERNAL",
      mode: "INLINE",
      depth: 1,
      // attemptCount / retryCount intentionally omitted (old API payloads)
    };
    const w = mountStep(step);
    const pill = w.get('[data-testid="delegation-attempts"]').text();
    expect(pill).toMatch(/尝试\s*0/);
    expect(pill).toMatch(/重试\s*0/);
    w.unmount();
  });

  it("non-delegation steps never render attempt/retry pill", () => {
    const step: AgentAuditStep = {
      type: "tool",
      title: "工具: search",
      timeOffsetMs: 5,
    };
    const w = mountStep(step);
    expect(w.find('[data-testid="delegation-attempts"]').exists()).toBe(false);
    w.unmount();
  });
});
