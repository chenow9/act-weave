import { describe, expect, it } from "vitest";

import type { Agent, ModelApiConfig, SmartGenerateTurn } from "../types/domain";
import {
  alreadyProjectedLastFailure,
  agentHasUsableModel,
  createWorkflowGenerateDockState,
  failureCtas,
  pickPreferredGenerateAgent,
  projectTranscript,
  resolveNodeAiReason,
  visibleReviseIssues,
} from "./workflow-generate-dock";

function agent(id: string, modelConfigId = "model-1"): Agent {
  return {
    id,
    workspaceId: "order",
    name: id,
    roleDescription: "",
    modelConfigId,
    systemPrompt: "",
    isDefault: false,
    status: "ACTIVE",
    toolsCount: 0,
    workflowsCount: 0,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-01T00:00:00Z",
    lockVersion: 1,
  };
}

function turn(overrides: Partial<SmartGenerateTurn>): SmartGenerateTurn {
  return {
    turnId: "turn-1",
    turnIndex: 1,
    userMessage: "供应商准入",
    assistantMessage: "已根据意图更新流程草稿。",
    generationId: "gen-1",
    guardOk: true,
    status: "SUCCEEDED",
    ...overrides,
  };
}

describe("workflow generate dock state", () => {
  it("keeps the prompt when switching tabs and clears it on reset", () => {
    const dock = createWorkflowGenerateDockState();
    dock.syncLeftTabForOpenEditor(true);
    dock.selectGenerateTab();
    dock.prompt.value = "供应商准入，先查资质";

    dock.selectNodesTab(true);
    expect(dock.leftTab.value).toBe("nodes");
    expect(dock.prompt.value).toBe("供应商准入，先查资质");

    dock.resetGenerateDock();
    expect(dock.prompt.value).toBe("");
    expect(dock.optimisticUserMessage.value).toBeNull();
    expect(dock.pendingFailureFeedback.value).toBeNull();
    expect(dock.failureFeedbackBannerHidden.value).toBe(false);
    expect(dock.selectedAgentId.value).toBe("");
  });

  it("does not close the generate sheet while generateLock is true", () => {
    const dock = createWorkflowGenerateDockState();
    dock.selectGenerateTab();
    dock.generateLock.value = true;
    dock.closeGenerateSheet(true);
    expect(dock.generateSheetOpen.value).toBe(true);
    expect(dock.leftTab.value).toBe("generate");
  });
});

describe("pickPreferredGenerateAgent", () => {
  const first = agent("agent-first");
  const draft = agent("agent-draft");
  const session = agent("agent-session");
  const selected = agent("agent-selected");

  it("prefers draft ui.agentId over the first catalog item", () => {
    expect(
      pickPreferredGenerateAgent({
        agents: [first, draft, session],
        draftAgentId: "agent-draft",
        sessionAgentId: "agent-session",
        selectedAgentId: "agent-selected",
      })?.id,
    ).toBe("agent-draft");
  });

  it("falls back to session agent, then selected, then first usable", () => {
    expect(
      pickPreferredGenerateAgent({
        agents: [first, session, selected],
        sessionAgentId: "agent-session",
        selectedAgentId: "agent-selected",
      })?.id,
    ).toBe("agent-session");
    expect(
      pickPreferredGenerateAgent({
        agents: [first, selected],
        selectedAgentId: "agent-selected",
      })?.id,
    ).toBe("agent-selected");
    expect(pickPreferredGenerateAgent({ agents: [first, selected] })?.id).toBe("agent-first");
  });

  it("skips agents without a usable model", () => {
    const unbound = agent("agent-unbound", "");
    const disabledConfig: ModelApiConfig = {
      id: "model-disabled",
      name: "off",
      provider: "openai",
      apiBase: "https://api.example",
      modelName: "gpt",
      credentialConfigured: true,
      options: {},
      status: "DISABLED",
      createdBy: "user-1",
      updatedBy: "user-1",
      createdAt: "2026-07-01T00:00:00Z",
      updatedAt: "2026-07-01T00:00:00Z",
      lockVersion: 1,
    };
    const disabled = agent("agent-disabled", "model-disabled");
    expect(agentHasUsableModel(unbound)).toBe(false);
    expect(agentHasUsableModel(disabled, [disabledConfig])).toBe(false);
    expect(
      pickPreferredGenerateAgent({
        agents: [unbound, disabled, first],
        modelConfigs: [disabledConfig],
        draftAgentId: "agent-unbound",
      })?.id,
    ).toBe("agent-first");
  });
});

describe("projectTranscript", () => {
  it("shows a non-guard lastFailure when no turn was recorded", () => {
    const rows = projectTranscript(
      {
        turns: [turn({ turnId: "ok-1" })],
        generating: false,
        goal: "加审批",
        lastFailure: { code: "NETWORK_ERROR", message: "流结束", turnId: "" },
      },
      "加审批",
    );
    expect(rows.map((row) => row.kind)).toEqual(["user", "assistant", "user", "failure"]);
    expect(rows.at(-1)).toMatchObject({ id: "last-failure", code: "NETWORK_ERROR" });
  });

  it("does not duplicate a guard failure when lastFailure.turnId does not match the turn row", () => {
    const rows = projectTranscript(
      {
        turns: [
          turn({
            turnId: "local-failed-1",
            userMessage: "坏图",
            assistantMessage: "本轮未通过校验，已保留上一轮合法草稿。",
            guardOk: false,
            status: "GUARD_REJECTED",
            errorCode: "GUARD_REJECTED",
          }),
        ],
        generating: false,
        goal: "坏图",
        lastFailure: { turnId: "detail-turn-9", code: "GUARD_REJECTED", message: "guard" },
      },
      "坏图",
    );
    expect(rows.filter((row) => row.kind === "failure")).toHaveLength(1);
    expect(
      alreadyProjectedLastFailure({
        turns: [
          turn({
            turnId: "local-failed-1",
            userMessage: "坏图",
            guardOk: false,
            errorCode: "GUARD_REJECTED",
            status: "GUARD_REJECTED",
          }),
        ],
        generating: false,
        goal: "坏图",
        lastFailure: { turnId: "detail-turn-9", code: "GUARD_REJECTED", message: "guard" },
      }),
    ).toBe(true);
  });

  it("projects an optimistic user bubble and pending row while generating", () => {
    const rows = projectTranscript({ turns: [], generating: true, goal: "供应商准入" }, "供应商准入");
    expect(rows).toEqual([
      { kind: "user", id: "optimistic", text: "供应商准入" },
      { kind: "pending", id: "inflight" },
    ]);
  });

  it("still projects a non-guard lastFailure when turns is empty", () => {
    const rows = projectTranscript(
      {
        turns: [],
        generating: false,
        goal: "加审批",
        lastFailure: { code: "AGENT_MODEL_REQUIRED", message: "先绑定模型", turnId: "" },
      },
      "加审批",
    );
    expect(rows).toEqual([
      { kind: "user", id: "optimistic", text: "加审批" },
      { kind: "failure", id: "last-failure", code: "AGENT_MODEL_REQUIRED", message: "先绑定模型" },
    ]);
  });
});

describe("generate dock helpers", () => {
  it("reads AI reason from node.ui.reason and never from Approval data.reason", () => {
    expect(
      resolveNodeAiReason({
        id: "approval-1",
        type: "Approval",
        label: "审批",
        position: { x: 0, y: 0 },
        ports: [],
        data: { reason: "审批原因字段" },
        ui: { reason: "AI 加了人工确认" },
      }),
    ).toBe("AI 加了人工确认");
    expect(
      resolveNodeAiReason(
        {
          id: "tool-1",
          type: "Tool",
          label: "查资质",
          position: { x: 0, y: 0 },
          ports: [],
          data: { reason: "not-ai" },
          ui: {},
        },
        [{ nodeId: "tool-1", title: "查资质", reason: "匹配供应商工具" }],
      ),
    ).toBe("匹配供应商工具");
  });

  it("maps AGENT_MODEL_REQUIRED to bind-model instead of toast-only fixConfig", () => {
    expect(failureCtas("AGENT_MODEL_REQUIRED").map((cta) => cta.key)).toEqual(["bind-model", "switch-agent"]);
    expect(failureCtas("AGENT_MODEL_REQUIRED").map((cta) => cta.labelKey)).toEqual([
      "workflow.generateGoBindModel",
      "workflow.generateSwitchAgent",
    ]);
    expect(failureCtas("SMART_DAG_TURN_IN_PROGRESS")).toEqual([]);
  });

  it("maps GUARD_REJECTED to rewrite plus end-session while the session is OPEN", () => {
    expect(failureCtas("GUARD_REJECTED", "OPEN").map((cta) => cta.key)).toEqual(["retry-rewrite", "end-session"]);
    expect(failureCtas("GUARD_REJECTED", "CLOSED").map((cta) => cta.key)).toEqual(["retry-rewrite"]);
    expect(failureCtas("NETWORK_ERROR", "OPEN").map((cta) => cta.key)).toEqual(["retry-rewrite", "end-session"]);
  });

  it("lists at most three revise issues and folds the rest", () => {
    const issues = ["a", "b", "c", "d", "e"].map((message) => ({ message }));
    expect(visibleReviseIssues(issues)).toEqual({
      preview: [{ message: "a" }, { message: "b" }, { message: "c" }],
      extra: [{ message: "d" }, { message: "e" }],
    });
  });
});
