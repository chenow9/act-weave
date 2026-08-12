import { expect, test, type Page, type Route } from "@playwright/test";

/**
 * Chrome/Playwright e2e: Agent Studio A2UI additive toggle (enableA2UI).
 * API is fully mocked — deterministic, no live backend/LLM required.
 */

const SMOKE_COOKIE = "smoke_auth=1";

function smokeUser() {
  return {
    id: "user-1",
    username: "admin",
    displayName: "Smoke Admin",
    status: "ACTIVE",
    platformRole: "PLATFORM_ADMIN",
    locale: "zh-CN",
    timezone: "Asia/Shanghai",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lockVersion: 1,
  };
}

function smokeWorkspace() {
  return {
    id: "ws-1",
    slug: "ws-1",
    displayName: "Workspace ws-1",
    mode: "PRODUCTION",
    status: "ACTIVE",
    ownerUserId: "user-1",
    settings: {},
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lockVersion: 1,
    currentUserRole: "EDITOR",
  };
}

function smokeModelConfig() {
  return {
    id: "model-1",
    workspaceId: "ws-1",
    name: "Smoke Model",
    provider: "openai",
    modelId: "gpt-test",
    baseUrl: "https://example.test/v1",
    status: "ACTIVE",
    verificationStatus: "VERIFIED",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lockVersion: 1,
  };
}

function smokeAgent(overrides: Record<string, unknown> = {}) {
  return {
    id: "agent-1",
    workspaceId: "ws-1",
    name: "A2UI Smoke Agent",
    roleDescription: "e2e agent",
    modelConfigId: "model-1",
    status: "ACTIVE",
    isDefault: true,
    currentPromptRevisionId: "rev-1",
    // token_window so Studio shows advanced AAP toggles (A2UI lives there)
    contextPolicy: {
      schemaVersion: "session-context-policy.v1",
      mode: "token_window",
    },
    toolsCount: 0,
    workflowsCount: 0,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lockVersion: 3,
    ...overrides,
  };
}

async function forceZhLocale(page: Page) {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("actweave.locale", "zh-CN");
    } catch {
      // ignore
    }
  });
}

async function mockA2UIConsoleApi(page: Page) {
  let agent = smokeAgent();
  const patches: unknown[] = [];

  await page.route("**/api/v1/**", async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname.replace(/^\/api\/v1/, "") || "/";
    const method = req.method();

    const json = (status: number, body: unknown, extraHeaders: Record<string, string> = {}) =>
      route.fulfill({
        status,
        contentType: "application/json",
        headers: extraHeaders,
        body: JSON.stringify(body),
      });

    const err = (status: number, code: string, message: string) =>
      json(status, { error: { code, message, requestId: "req-a2ui-e2e" } });

    if (method === "POST" && path === "/auth/login") {
      return json(
        200,
        {
          accessToken: "a2ui-e2e-token",
          accessTokenExpires: "2099-01-01T00:00:00Z",
          sessionId: "sess-a2ui",
          mustChangePassword: false,
          user: smokeUser(),
        },
        { "Set-Cookie": `${SMOKE_COOKIE}; Path=/` },
      );
    }
    if (method === "POST" && path === "/auth/refresh") {
      const cookie = req.headers()["cookie"] || "";
      if (!cookie.includes("smoke_auth=1")) return err(401, "UNAUTHORIZED", "no session");
      return json(200, {
        accessToken: "a2ui-e2e-token",
        accessTokenExpires: "2099-01-01T00:00:00Z",
        sessionId: "sess-a2ui",
        mustChangePassword: false,
        user: smokeUser(),
      });
    }
    if (method === "GET" && path === "/users/me") {
      return json(200, smokeUser());
    }
    if (method === "GET" && path === "/workspaces") {
      const items = [smokeWorkspace()];
      return json(200, {
        items,
        pagination: { page: 1, pageSize: 50, total: 1 },
        summary: { total: 1, active: 1, production: 1, boundAgents: 1 },
      });
    }
    if (method === "GET" && path === "/workspaces/ws-1") {
      return json(200, smokeWorkspace());
    }
    if (method === "GET" && path === "/overview/metrics") {
      return json(200, {
        range: { from: "2026-07-14", to: "2026-07-27" },
        toolSuccessRate: 0,
        agentSuccessRate: 0,
        workflowSuccessRate: 0,
        sessionCount: 0,
        toolCallCount: 0,
        agentRunCount: 0,
        failureCount: 0,
        resourceScale: 0,
        dailyTraffic: [],
        trendSnapshots: [],
        successFailure: [],
        riskConfig: {
          workspaces: 1,
          agents: 1,
          tools: 0,
          workflows: 0,
          connectionsVerified: 0,
          connectionsTotal: 0,
          modelsVerified: 1,
          modelsTotal: 1,
        },
        dailyDetails: [],
        topTools: [],
        failingTools: [],
        topWorkspaces: [],
      });
    }
    if (method === "GET" && path === "/workspaces/ws-1/agents") {
      return json(200, { items: [agent] });
    }
    if (method === "GET" && path === "/workspaces/ws-1/model-configs") {
      return json(200, { items: [smokeModelConfig()] });
    }
    if (method === "GET" && path === "/workspaces/ws-1/tools") {
      return json(200, { items: [] });
    }
    if (method === "GET" && path === "/workspaces/ws-1/agents/agent-1/prompt-revisions/current") {
      return json(200, {
        revisionId: "rev-1",
        revisionNo: 1,
        systemPrompt: "You are a helpful agent.",
        source: "MANUAL",
        createdAt: "2026-01-01T00:00:00Z",
      });
    }
    if (method === "GET" && path === "/workspaces/ws-1/agents/agent-1/capabilities") {
      return json(200, { items: [] });
    }
    if (method === "GET" && path === "/workspaces/ws-1/a2a/capabilities") {
      return json(200, { allowAuthNone: false, authModes: ["AGENT_ACCESS"], softDisable: true });
    }
    if (method === "GET" && path === "/workspaces/ws-1/agents/agent-1/delegation-bindings") {
      return json(200, { items: [] });
    }
    if (method === "GET" && path === "/workspaces/ws-1/a2a/exposures") {
      return json(200, { items: [] });
    }
    if (method === "GET" && path === "/workspaces/ws-1/agents/agent-1/a2a-remotes") {
      return json(200, { items: [] });
    }
    if (method === "PATCH" && path === "/workspaces/ws-1/agents/agent-1") {
      const body = req.postDataJSON() as Record<string, unknown>;
      patches.push(body);
      const nextPolicy = (body.contextPolicy as Record<string, unknown>) || agent.contextPolicy;
      agent = {
        ...agent,
        name: typeof body.name === "string" ? body.name : agent.name,
        roleDescription: typeof body.roleDescription === "string" ? body.roleDescription : agent.roleDescription,
        modelConfigId: typeof body.modelConfigId === "string" ? body.modelConfigId : agent.modelConfigId,
        status: typeof body.status === "string" ? body.status : agent.status,
        contextPolicy: nextPolicy,
        lockVersion: Number(agent.lockVersion) + 1,
        updatedAt: "2026-08-11T12:00:00Z",
      };
      return json(200, agent);
    }

    return err(501, "A2UI_E2E_UNMOCKED", `unmocked ${method} ${path}`);
  });

  return {
    patches,
    lastPatch: () => patches[patches.length - 1] as Record<string, unknown> | undefined,
  };
}

async function loginAsAdmin(page: Page) {
  await page.goto("/login");
  const zh = page.locator('[data-testid="login-lang-zh-CN"]');
  if (await zh.isVisible().catch(() => false)) {
    await zh.click();
  }
  await expect(page.getByRole("heading", { name: "登录", exact: true })).toBeVisible({ timeout: 15_000 });
  await page.locator('input[autocomplete="username"]').fill("admin");
  await page.locator('input[autocomplete="current-password"]').fill("Password-123456");
  await page.getByRole("button", { name: /^登录$/ }).click();
  await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
}

test.describe("A2UI enable toggle (mocked API, Chromium)", () => {
  test.beforeEach(async ({ page }) => {
    await forceZhLocale(page);
  });

  test("default off; enabling A2UI patches contextPolicy.aap.enableA2UI=true", async ({ page }) => {
    const api = await mockA2UIConsoleApi(page);
    await loginAsAdmin(page);

    await page.goto("/agents");
    await expect(page.getByText("A2UI Smoke Agent")).toBeVisible({ timeout: 15_000 });

    // Row a11y name also contains "更多操作" — use exact label on the actions trigger only.
    await page.getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("menuitem", { name: "编辑", exact: true }).click();

    const studio = page.getByRole("dialog", { name: "编辑 Agent" });
    await expect(studio).toBeVisible({ timeout: 15_000 });

    // Expand advanced context policy section (zh-CN: 高级选项)
    await studio.getByRole("button", { name: "高级选项" }).click();

    const a2uiSwitch = studio.getByRole("switch", { name: "启用 A2UI（附加）", exact: true });
    await expect(a2uiSwitch).toBeVisible({ timeout: 10_000 });
    await expect(a2uiSwitch).toHaveAttribute("aria-checked", "false");

    await a2uiSwitch.click();
    await expect(a2uiSwitch).toHaveAttribute("aria-checked", "true");

    await studio.getByRole("button", { name: "保存 Agent", exact: true }).click();

    await expect.poll(() => api.patches.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(1);

    const patch = api.lastPatch();
    expect(patch).toBeTruthy();
    const policy = patch?.contextPolicy as {
      schemaVersion?: string;
      aap?: { enableA2UI?: boolean; includeCompactionSummary?: boolean };
    };
    expect(policy?.schemaVersion).toBe("session-context-policy.v2");
    expect(policy?.aap?.enableA2UI).toBe(true);
    // Flag bag always includes sibling flag
    expect(policy?.aap?.includeCompactionSummary).toBe(false);
  });

  test("A2UI remains off when not toggled — patch aap absent or enableA2UI false", async ({ page }) => {
    const api = await mockA2UIConsoleApi(page);
    await loginAsAdmin(page);
    await page.goto("/agents");
    await expect(page.getByText("A2UI Smoke Agent")).toBeVisible({ timeout: 15_000 });

    await page.getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("menuitem", { name: "编辑", exact: true }).click();
    const studio = page.getByRole("dialog", { name: "编辑 Agent" });
    await expect(studio).toBeVisible({ timeout: 15_000 });

    // Dirty form without enabling A2UI (rename agent)
    const nameInput = studio.locator('input[type="text"]').first();
    await nameInput.fill("A2UI Smoke Agent Renamed");

    const save = studio.getByRole("button", { name: "保存 Agent", exact: true });
    await expect(save).toBeEnabled({ timeout: 10_000 });
    await save.click();

    await expect.poll(() => api.patches.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(1);
    const policy = (api.lastPatch()?.contextPolicy || {}) as {
      aap?: { enableA2UI?: boolean };
    };
    // Either no aap (empty/default policy) or explicit false — never true
    if (policy.aap) {
      expect(policy.aap.enableA2UI).not.toBe(true);
    }
  });
});
