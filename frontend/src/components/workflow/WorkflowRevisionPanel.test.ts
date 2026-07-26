import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import type { WorkflowReadiness, WorkflowRevision, WorkflowStatus } from "../../types/domain";
import WorkflowRevisionPanel from "./WorkflowRevisionPanel.vue";

const LONG_ACTIVE = "a1b2c3d4-e5f6-7890-abcd-ef0123456789";
const LONG_LATEST = "9z8y7x6w-v1u0-tsrq-ponm-lkjihgfedcba";
const LONG_HISTORY = "hist0001-2345-6789-abcd-ef012345hist";

const emptyDraft: WorkflowRevision["draft"] = {
  schemaVersion: "workflow-graph.v1",
  nodes: [],
  edges: [],
  viewport: { x: 0, y: 0, zoom: 1 },
  ui: {},
};

function revisionFixture(overrides: Partial<WorkflowRevision> = {}): WorkflowRevision {
  return {
    workflowId: "wf-1",
    revisionId: LONG_ACTIVE,
    revisionNo: 2,
    sourceCompilationId: "comp-1",
    status: "Published",
    draft: emptyDraft,
    spec: { workflowId: "wf-1", nodes: [] },
    plan: { workflowId: "wf-1", nodes: [] },
    createdAt: "2026-07-03T02:00:00Z",
    createdBy: "user-1",
    planHash: "sha256:abcdef0123456789deadbeef",
    activatedAt: "2026-07-03T02:05:00Z",
    ...overrides,
  };
}

function readinessFixture(overrides: Partial<WorkflowReadiness> = {}): WorkflowReadiness {
  return {
    stage: "Published",
    canCompile: false,
    canTrial: false,
    canValidate: true,
    canTrialRun: false,
    canPublish: false,
    hasDraft: true,
    compilationCurrent: true,
    compilationValid: true,
    trialCurrent: true,
    trialSuccessful: true,
    published: true,
    activeRevisionId: LONG_ACTIVE,
    latestRevisionId: LONG_LATEST,
    blockers: [],
    updatedAt: "2026-07-03T02:05:00Z",
    ...overrides,
  };
}

function mountPanel(
  options: {
    revisions?: WorkflowRevision[];
    readiness?: WorkflowReadiness;
    busyRevisionId?: string;
    workflowStatus?: WorkflowStatus;
    disableBusy?: boolean;
    emptyText?: string;
  } = {},
) {
  return mount(WorkflowRevisionPanel, {
    props: {
      revisions: options.revisions ?? [
        revisionFixture({ revisionId: LONG_LATEST, revisionNo: 3, createdAt: "2026-07-04T02:00:00Z" }),
        revisionFixture({ revisionId: LONG_ACTIVE, revisionNo: 2 }),
        revisionFixture({ revisionId: LONG_HISTORY, revisionNo: 1, createdAt: "2026-07-01T02:00:00Z" }),
      ],
      readiness: options.readiness ?? readinessFixture(),
      busyRevisionId: options.busyRevisionId,
      workflowStatus: options.workflowStatus,
      disableBusy: options.disableBusy,
      emptyText: options.emptyText,
    },
  });
}

describe("WorkflowRevisionPanel FE-01 layout and semantics", () => {
  it("splits head into title, independent Active/Latest meta cards, and non-shrinking disable action", () => {
    const wrapper = mountPanel();

    expect(wrapper.find(".workflow-revision-head-title").text()).toBe("发布版本");
    expect(wrapper.find(".workflow-revision-disable-button").text()).toBe("停用新执行");

    const activeMeta = wrapper.get('[data-testid="workflow-revision-active-meta"]');
    const latestMeta = wrapper.get('[data-testid="workflow-revision-latest-meta"]');
    expect(activeMeta.find(".workflow-revision-meta-label").text()).toBe("Active");
    expect(latestMeta.find(".workflow-revision-meta-label").text()).toBe("Latest");
    expect(activeMeta.find(".workflow-revision-meta-id").attributes("title")).toBe(LONG_ACTIVE);
    expect(latestMeta.find(".workflow-revision-meta-id").attributes("title")).toBe(LONG_LATEST);
    expect(activeMeta.text()).toContain("a1b2c3d4…6789");
    expect(latestMeta.text()).toContain("9z8y7x6w…dcba");
    expect(activeMeta.text()).not.toContain(LONG_ACTIVE);
    expect(latestMeta.text()).not.toContain(LONG_LATEST);
  });

  it("keeps Active and Latest as independent fields even when they share the same revision id", () => {
    const shared = LONG_ACTIVE;
    const wrapper = mountPanel({
      revisions: [revisionFixture({ revisionId: shared })],
      readiness: readinessFixture({ activeRevisionId: shared, latestRevisionId: shared }),
    });

    const activeMeta = wrapper.get('[data-testid="workflow-revision-active-meta"]');
    const latestMeta = wrapper.get('[data-testid="workflow-revision-latest-meta"]');
    expect(activeMeta.find(".workflow-revision-meta-id").attributes("title")).toBe(shared);
    expect(latestMeta.find(".workflow-revision-meta-id").attributes("title")).toBe(shared);
    expect(wrapper.findAll(".workflow-revision-item")).toHaveLength(1);
    expect(wrapper.get(".workflow-revision-status").text()).toBe("Active");
  });

  it("renders empty state with stable copy and unset Active/Latest placeholders", () => {
    const wrapper = mountPanel({
      revisions: [],
      readiness: readinessFixture({ activeRevisionId: "", latestRevisionId: "" }),
      emptyText: "自定义空状态",
    });

    expect(wrapper.find(".workflow-revision-empty").text()).toBe("自定义空状态");
    expect(wrapper.get('[data-testid="workflow-revision-active-meta"]').text()).toContain("未设置");
    expect(wrapper.get('[data-testid="workflow-revision-latest-meta"]').text()).toContain("暂无");
    expect(wrapper.findAll(".workflow-revision-item")).toHaveLength(0);
  });

  it("partitions each revision row into info, status pill, and wrap-capable actions", () => {
    const wrapper = mountPanel();
    const items = wrapper.findAll(".workflow-revision-item");
    expect(items).toHaveLength(3);

    const history = items.find((item) => item.attributes("data-revision-id") === LONG_HISTORY)!;
    expect(history.find(".workflow-revision-id").attributes("title")).toBe(LONG_HISTORY);
    expect(history.find(".workflow-revision-id").text()).toBe("hist0001…hist");
    expect(history.find(".workflow-revision-status").text()).toBe("History");
    expect(history.find(".workflow-revision-info").exists()).toBe(true);
    expect(history.find(".workflow-revision-actions").exists()).toBe(true);

    const latest = items.find((item) => item.attributes("data-revision-id") === LONG_LATEST)!;
    expect(latest.find(".workflow-revision-status").text()).toBe("Latest");
    expect(latest.classes()).not.toContain("active");

    const active = items.find((item) => item.attributes("data-revision-id") === LONG_ACTIVE)!;
    expect(active.find(".workflow-revision-status").text()).toBe("Active");
    expect(active.classes()).toContain("active");
  });

  it("preserves activate / rollback / compare / disable emit names and parameters", async () => {
    const wrapper = mountPanel();

    await wrapper.get(".workflow-revision-disable-button").trigger("click");
    expect(wrapper.emitted("disable")).toEqual([[]]);

    const history = wrapper
      .findAll(".workflow-revision-item")
      .find((item) => item.attributes("data-revision-id") === LONG_HISTORY)!;
    const buttons = history.findAll("button");
    expect(buttons.map((button) => button.text())).toEqual(["激活", "回滚", "对比"]);

    await buttons[0].trigger("click");
    await buttons[1].trigger("click");
    await buttons[2].trigger("click");

    expect(wrapper.emitted("activate")).toEqual([[LONG_HISTORY]]);
    expect(wrapper.emitted("rollback")).toEqual([[LONG_HISTORY]]);
    expect(wrapper.emitted("compare")).toEqual([[LONG_ACTIVE, LONG_HISTORY]]);
  });

  it("disables Active-row activate/rollback and compare-to-self; marks busy row without changing labels", () => {
    const wrapper = mountPanel({ busyRevisionId: LONG_LATEST });

    const active = wrapper
      .findAll(".workflow-revision-item")
      .find((item) => item.attributes("data-revision-id") === LONG_ACTIVE)!;
    const activeButtons = active.findAll("button");
    expect(activeButtons[0].attributes("disabled")).toBeDefined();
    expect(activeButtons[1].attributes("disabled")).toBeDefined();
    expect(activeButtons[2].attributes("disabled")).toBeDefined();
    expect(activeButtons.map((button) => button.text())).toEqual(["激活", "回滚", "对比"]);

    const latest = wrapper
      .findAll(".workflow-revision-item")
      .find((item) => item.attributes("data-revision-id") === LONG_LATEST)!;
    expect(latest.findAll("button")[0].attributes("disabled")).toBeDefined();
    expect(latest.findAll("button")[0].attributes("aria-busy")).toBe("true");
    expect(latest.findAll("button")[0].text()).toBe("激活");
  });

  it("shows 已停用 and disables the disable control when workflow status is Disabled", () => {
    const wrapper = mountPanel({ workflowStatus: "Disabled" });
    const button = wrapper.get(".workflow-revision-disable-button");
    expect(button.text()).toBe("已停用");
    expect(button.attributes("disabled")).toBeDefined();
  });

  it("disables the disable control while disableBusy is true without changing label width semantics", () => {
    const wrapper = mountPanel({ disableBusy: true });
    const button = wrapper.get(".workflow-revision-disable-button");
    expect(button.text()).toBe("停用新执行");
    expect(button.attributes("disabled")).toBeDefined();
  });

  it("keeps short revision ids untruncated and still exposes full title", () => {
    const shortId = "rev-short";
    const wrapper = mountPanel({
      revisions: [revisionFixture({ revisionId: shortId })],
      readiness: readinessFixture({ activeRevisionId: shortId, latestRevisionId: shortId }),
    });

    expect(wrapper.get(".workflow-revision-id").text()).toBe(shortId);
    expect(wrapper.get(".workflow-revision-id").attributes("title")).toBe(shortId);
  });
});
