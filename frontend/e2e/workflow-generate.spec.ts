import { expect, test, type Page, type Route } from "@playwright/test";

import {
  WORKFLOW_E2E,
  installStableUi,
  installWorkflowApiMocks,
  loginAndOpenWorkflow,
  seedGeneratedWorkflow,
} from "./fixtures/workflow";

test.use({
  baseURL: WORKFLOW_E2E.baseURL,
  colorScheme: "light",
  locale: "zh-CN",
  timezoneId: "Asia/Singapore",
  viewport: { width: 1600, height: 1100 },
});

const GENERATE_SESSION_ID = "sess-e2e-001";
const GENERATED_WORKFLOW_ID = "wf-e2e-generated";
const GENERATE_GOAL = "订单取消：查状态、判断能否取消、通知下游";

test.describe("workflow generate dock e2e", () => {
  test("generates a Start+Tool+End graph from a sentence without a live LLM", async ({ page }) => {
    await page.clock.setFixedTime(WORKFLOW_E2E.fixedTime);
    const fixture = await installWorkflowApiMocks(page);
    await installGenerateSessionMocks(page, fixture);
    await loginAndOpenWorkflow(page);
    await installStableUi(page);

    await page.getByRole("button", { name: "用一句话生成" }).first().click();
    const dock = page.locator(".workflow-generate-dock");
    await expect(dock).toBeVisible();
    await expect(page.locator(".workflow-graph-empty")).toBeVisible();
    await expect(page.locator(".workflow-graph-empty")).toContainText("描述流程后，草稿会出现在这里");

    await expect(page.locator('[data-action="generate-agent-chip"]')).toContainText("Ops Executor");
    const prompt = page.locator("textarea.workflow-generate-prompt");
    await expect(prompt).toBeVisible();
    await prompt.fill(GENERATE_GOAL);
    const submit = page.locator('[data-action="submit-generate"]');
    await expect(submit).toBeEnabled();
    await submit.click();

    await expect(page.locator(".workflow-graph-empty")).toHaveCount(0, { timeout: 15_000 });
    await expect(page.locator('[data-node-id="start"]')).toBeVisible();
    await expect(page.locator('[data-node-id="tool-1"]')).toBeVisible();
    await expect(page.locator('[data-node-id="end"]')).toBeVisible();
    await expect(dock).toContainText("已生成第 1 版草稿");
  });

  test("redirects /smart-dag onto /workflow?generate=1 and opens the generate dock", async ({ page }) => {
    await page.clock.setFixedTime(WORKFLOW_E2E.fixedTime);
    const fixture = await installWorkflowApiMocks(page);
    await installGenerateSessionMocks(page, fixture);
    await loginAndOpenWorkflow(page);
    await installStableUi(page);

    await page.locator(".fluid-trigger").click();
    await expect(page.locator('.fluid-content a[href="/smart-dag"]')).toHaveCount(0);
    await expect(page.locator(".fluid-content")).not.toContainText("智能编排");

    // Stay in the authenticated SPA. A full document load of /smart-dag remounts
    // the app before Pinia auth hydrates and lands on /login.
    await page.evaluate(() => {
      window.history.pushState({}, "", "/smart-dag");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });

    await expect(page).toHaveURL(/\/workflow\?generate=1$/);
    await expect(page.locator(".workflow-generate-dock")).toBeVisible();
    await expect(page.locator("textarea.workflow-generate-prompt")).toBeVisible();
    await expect(page.locator(".workflow-graph-empty")).toBeVisible();
    await expect(page.locator('[data-action="submit-generate"]')).toBeVisible();
  });
});

async function installGenerateSessionMocks(page: Page, fixture: Awaited<ReturnType<typeof installWorkflowApiMocks>>) {
  // Predicate match — glob **/workflow-generate-sessions** misses /turns.
  await page.route(
    (url) => url.pathname.includes("/workflow-generate-sessions"),
    async (route) => {
      await fulfillGenerateSessionRoute(route, fixture);
    },
  );
}

async function fulfillGenerateSessionRoute(route: Route, fixture: Awaited<ReturnType<typeof installWorkflowApiMocks>>) {
  const request = route.request();
  const url = new URL(request.url());
  const method = request.method();
  const pathname = url.pathname;

  if (method === "POST" && /\/workflow-generate-sessions\/[^/]+\/turns$/.test(pathname)) {
    const payload = generatedTurnPayload();
    seedGeneratedWorkflow(fixture, {
      workflowId: GENERATED_WORKFLOW_ID,
      name: payload.workflow.name,
      graph: payload.draft.graph,
    });
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: { "cache-control": "no-cache" },
      body: [
        "event: started",
        `data: ${JSON.stringify({ status: "RUNNING", sessionId: GENERATE_SESSION_ID })}`,
        "",
        "event: completed",
        `data: ${JSON.stringify(payload)}`,
        "",
        "",
      ].join("\n"),
    });
    return;
  }

  if (method === "POST" && /\/workflow-generate-sessions\/[^/]+:close$/.test(pathname)) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ sessionId: GENERATE_SESSION_ID, status: "CLOSED" }),
    });
    return;
  }

  if (method === "POST" && /\/workflow-generate-sessions$/.test(pathname)) {
    const body = (request.postDataJSON() as { agentId?: string; workflowId?: string } | null) || {};
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        sessionId: GENERATE_SESSION_ID,
        agentId: body.agentId || "agent-ops",
        modelConfigId: "model-ops",
        status: "OPEN",
        workflowId: body.workflowId || "",
        lockVersion: 1,
      }),
    });
    return;
  }

  if (method === "GET" && /\/workflow-generate-sessions\/[^/]+$/.test(pathname)) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        session: {
          sessionId: GENERATE_SESSION_ID,
          agentId: "agent-ops",
          modelConfigId: "model-ops",
          status: "OPEN",
          workflowId: GENERATED_WORKFLOW_ID,
        },
        turns: [],
      }),
    });
    return;
  }

  await route.fallback();
}

function generatedTurnPayload() {
  const graph = {
    schemaVersion: "workflow.graph.v1",
    nodes: [
      {
        id: "start",
        type: "Start",
        label: "Start",
        position: { x: 48, y: 260 },
        ports: [{ key: "output", label: "Output", direction: "output" }],
        data: {},
        ui: { generated: true, reason: "统一入口" },
      },
      {
        id: "tool-1",
        type: "Tool",
        label: "取消订单",
        position: { x: 288, y: 260 },
        ports: [
          { key: "input", label: "Input", direction: "input" },
          { key: "output", label: "Output", direction: "output" },
        ],
        data: { toolId: "tool.cancel-order" },
        ui: { generated: true, reason: "调用已发布的取消订单工具" },
      },
      {
        id: "end",
        type: "End",
        label: "End",
        position: { x: 528, y: 260 },
        ports: [{ key: "input", label: "Input", direction: "input" }],
        data: {},
        ui: { generated: true },
      },
    ],
    edges: [
      {
        id: "edge-start-tool-1",
        sourceNodeId: "start",
        sourcePort: "output",
        targetNodeId: "tool-1",
        targetPort: "input",
        data: {},
        ui: {},
      },
      {
        id: "edge-tool-1-end",
        sourceNodeId: "tool-1",
        sourcePort: "output",
        targetNodeId: "end",
        targetPort: "input",
        data: {},
        ui: {},
      },
    ],
    viewport: { x: 0, y: 0, zoom: 1 },
    ui: { generatedBy: "smart-dag.v2", agentId: "agent-ops", sessionId: GENERATE_SESSION_ID },
  };

  return {
    sessionId: GENERATE_SESSION_ID,
    turnId: "turn-e2e-1",
    generationId: "gen-e2e-1",
    workflow: {
      id: GENERATED_WORKFLOW_ID,
      currentDraftId: `draft-${GENERATED_WORKFLOW_ID}`,
      name: `AI · ${GENERATE_GOAL}`,
      slug: "ai-workflow-e2e",
      description: "",
      status: "ACTIVE",
      createdBy: "user-chen-ops",
      updatedBy: "user-chen-ops",
      createdAt: "2026-07-18T01:15:00.000Z",
      updatedAt: "2026-07-18T01:15:00.000Z",
      lockVersion: 1,
      nodeCount: 3,
      edgeCount: 2,
    },
    draft: {
      id: `draft-${GENERATED_WORKFLOW_ID}`,
      draftVersion: 1,
      schemaVersion: "workflow.graph.v1",
      graph,
      graphHash: "sha256:graph-generated-1",
      updatedBy: "user-chen-ops",
      updatedAt: "2026-07-18T01:15:00.000Z",
      lockVersion: 1,
    },
    assistantMessage: "已根据意图更新流程草稿（draftVersion=1）。",
    reasoningSteps: [
      { id: "context", label: "收集上下文", status: "COMPLETED", detail: "ok" },
      { id: "model", label: "生成图", status: "COMPLETED", detail: "ok" },
      { id: "guard", label: "校验图", status: "COMPLETED", detail: "ok" },
      { id: "draft", label: "写入草稿", status: "COMPLETED", detail: "ok" },
    ],
    missingCapabilities: [],
    nodeExplanations: [{ nodeId: "tool-1", title: "取消订单", reason: "调用已发布的取消订单工具" }],
    availableToolIds: ["tool.cancel-order"],
    selectedToolIds: ["tool.cancel-order"],
    confidence: 90,
    guardReport: { ok: true, violations: [] },
    draftVersion: 1,
    generatedBy: "smart-dag.v2",
    agentId: "agent-ops",
    modelConfigId: "model-ops",
  };
}
