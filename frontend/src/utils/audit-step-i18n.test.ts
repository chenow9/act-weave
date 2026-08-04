import { describe, expect, it } from "vitest";

import {
  extractAuditDelegationParts,
  extractAuditToolName,
  isMissingReasoningContent,
  localizedAuditStepTitle,
} from "./audit-step-i18n";

const en: Record<string, string> = {
  "logs.stepInput": "User input",
  "logs.stepOutput": "Final output",
  "logs.stepReasoning": "Model reasoning",
  "logs.stepTool": "Tool call: {name}",
  "logs.stepToolDefault": "Tool call",
  "logs.stepDelegation": "Agent call: {name}",
  "logs.stepDelegationDefault": "Agent call",
  "logs.stepCompaction": "Context compaction",
  "logs.stepCompactionCompleted": "Context compaction completed",
  "logs.stepCompactionFailed": "Context compaction failed",
  "logs.stepCompactionFallback": "Context compaction failed; fell back to token_window",
  "logs.stepUnknown": "Step: {type}",
};

const zh: Record<string, string> = {
  "logs.stepInput": "用户输入",
  "logs.stepOutput": "最终输出",
  "logs.stepReasoning": "大模型推理",
  "logs.stepTool": "工具调用: {name}",
  "logs.stepToolDefault": "工具调用",
  "logs.stepDelegation": "Agent 调用: {name}",
  "logs.stepDelegationDefault": "Agent 调用",
  "logs.stepCompaction": "上下文 Compact",
  "logs.stepCompactionCompleted": "上下文 Compact 完成",
  "logs.stepCompactionFailed": "上下文 Compact 失败",
  "logs.stepCompactionFallback": "上下文 Compact 失败；已退化为 token_window",
  "logs.stepUnknown": "步骤: {type}",
};

function makeT(table: Record<string, string>) {
  return (key: string, params?: Record<string, unknown>) => {
    let out = table[key] ?? key;
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        out = out.replace(`{${k}}`, String(v));
      }
    }
    return out;
  };
}

describe("localizedAuditStepTitle", () => {
  it("maps fixed Chinese backend titles to English by type", () => {
    const t = makeT(en);
    expect(localizedAuditStepTitle({ type: "input", title: "用户输入" }, t)).toBe("User input");
    expect(localizedAuditStepTitle({ type: "output", title: "最终输出" }, t)).toBe("Final output");
    expect(localizedAuditStepTitle({ type: "reasoning", title: "大模型推理" }, t)).toBe("Model reasoning");
    expect(localizedAuditStepTitle({ type: "tool", title: "工具调用: check_inventory" }, t)).toBe(
      "Tool call: check_inventory",
    );
    expect(
      localizedAuditStepTitle(
        {
          type: "agent_delegation",
          title: "Agent 调用: inventory_agent (019fca7b → 5290c409)",
        },
        t,
      ),
    ).toBe("Agent call: inventory_agent (019fca7b → 5290c409)");
  });

  it("keeps Chinese labels when locale is zh-CN", () => {
    const t = makeT(zh);
    expect(localizedAuditStepTitle({ type: "input", title: "用户输入" }, t)).toBe("用户输入");
    expect(localizedAuditStepTitle({ type: "tool", title: "工具调用: create_order" }, t)).toBe(
      "工具调用: create_order",
    );
  });

  it("prefers callableName from params for tools", () => {
    const t = makeT(en);
    expect(
      localizedAuditStepTitle(
        {
          type: "tool",
          title: "工具调用: old",
          params: { callableName: "create_order" },
        },
        t,
      ),
    ).toBe("Tool call: create_order");
  });

  it("preserves status suffix on delegation titles", () => {
    const t = makeT(en);
    expect(
      localizedAuditStepTitle(
        {
          type: "agent_delegation",
          title: "Agent 调用: call_b (aaaa → bbbb) [FAILED]",
        },
        t,
      ),
    ).toBe("Agent call: call_b (aaaa → bbbb) [FAILED]");
  });

  it("localizes context compaction titles", () => {
    const t = makeT(en);
    expect(localizedAuditStepTitle({ type: "context_compaction", title: "上下文 Compact 完成" }, t)).toBe(
      "Context compaction completed",
    );
    expect(
      localizedAuditStepTitle(
        {
          type: "context_compaction",
          title: "上下文 Compact 失败；已退化为 token_window",
        },
        t,
      ),
    ).toBe("Context compaction failed; fell back to token_window");
  });
});

describe("extract helpers", () => {
  it("extractAuditToolName", () => {
    expect(extractAuditToolName({ title: "工具调用: foo" })).toBe("foo");
    expect(extractAuditToolName({ title: "Tool call: bar" })).toBe("bar");
    expect(extractAuditToolName({ title: "x", params: { toolName: "from_params" } })).toBe("from_params");
  });

  it("extractAuditDelegationParts", () => {
    expect(extractAuditDelegationParts("Agent 调用: inv (a → b)")).toEqual({
      name: "inv",
      path: "(a → b)",
      statusSuffix: "",
    });
    expect(extractAuditDelegationParts("Agent call: inv [FAILED]")).toEqual({
      name: "inv",
      path: "",
      statusSuffix: " [FAILED]",
    });
  });
});

describe("isMissingReasoningContent", () => {
  it("detects backend Chinese placeholder and missing states", () => {
    expect(
      isMissingReasoningContent({
        type: "reasoning",
        content: "无推理数据",
        contentState: "missing",
      }),
    ).toBe(true);
    expect(
      isMissingReasoningContent({
        type: "reasoning",
        content: "real chain-of-thought",
        contentState: "plain",
      }),
    ).toBe(false);
    expect(isMissingReasoningContent({ type: "tool", content: "无推理数据" })).toBe(false);
  });
});
