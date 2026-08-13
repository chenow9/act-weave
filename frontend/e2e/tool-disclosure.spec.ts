import { expect, test, type Page, type Route } from "@playwright/test";

/**
 * Deterministic Console e2e for multi-provider tool disclosure UI.
 * All /api/v1 traffic is mocked. Live gateway coverage is
 * e2e/tool-disclosure-live.spec.ts (E2E_MODEL_API_KEY).
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

type ModelRow = {
  id: string;
  name: string;
  modelName: string;
  status: "UNVERIFIED" | "VERIFIED" | "ERROR";
  lockVersion: number;
  toolDisclosureUI?: "hidden" | "binary" | "unavailable" | "unverified";
  toolDisclosurePolicy?: Record<string, unknown>;
  agenticCapabilities?: Record<string, unknown>;
};

function modelRow(overrides: Partial<ModelRow> = {}): Record<string, unknown> {
  return {
    id: "model-1",
    workspaceId: "ws-1",
    name: "Unverified Model",
    provider: "openai-compatible",
    apiBase: "https://example.test/v1",
    modelName: "unverified-alias",
    status: "UNVERIFIED",
    credentialConfigured: true,
    options: {},
    runtimeCapabilities: {},
    agenticCapabilities: {},
    toolDisclosurePolicy: {},
    toolDisclosureUI: "unverified",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
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

async function mockDisclosureApi(page: Page, models: Record<string, unknown>[]) {
  const catalog = [...models];

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
      json(status, { error: { code, message, requestId: "req-disclosure-e2e" } });

    if (method === "POST" && path === "/auth/login") {
      return json(
        200,
        {
          accessToken: "disclosure-e2e-token",
          accessTokenExpires: "2099-01-01T00:00:00Z",
          sessionId: "sess-disclosure",
          mustChangePassword: false,
          user: smokeUser(),
        },
        { "Set-Cookie": `${SMOKE_COOKIE}; Path=/` },
      );
    }
    if (method === "POST" && path === "/auth/refresh") {
      return json(200, {
        accessToken: "disclosure-e2e-token",
        accessTokenExpires: "2099-01-01T00:00:00Z",
        sessionId: "sess-disclosure",
        mustChangePassword: false,
        user: smokeUser(),
      });
    }
    if (method === "GET" && path === "/users/me") return json(200, smokeUser());
    if (method === "GET" && path === "/workspaces") {
      return json(200, {
        items: [smokeWorkspace()],
        pagination: { page: 1, pageSize: 50, total: 1 },
        summary: { total: 1, active: 1, production: 1, boundAgents: 0 },
      });
    }
    if (method === "GET" && path === "/workspaces/ws-1") return json(200, smokeWorkspace());
    if (method === "GET" && path === "/workspaces/ws-1/model-configs") {
      return json(200, { items: catalog });
    }
    if (method === "POST" && /\/model-configs\/[^/]+:verify$/.test(path)) {
      const id = path.split("/").pop()?.replace(":verify", "") || "";
      const idx = catalog.findIndex((item) => item.id === id);
      if (idx < 0) return err(404, "NOT_FOUND", "missing");
      catalog[idx] = {
        ...catalog[idx],
        status: "VERIFIED",
        lastLatencyMs: 42,
        lockVersion: Number(catalog[idx].lockVersion || 1) + 1,
      };
      return json(200, catalog[idx]);
    }
    if (method === "POST" && /\/model-configs\/[^/]+:set-disclosure$/.test(path)) {
      const id = path.split("/").pop()?.replace(":set-disclosure", "") || "";
      const idx = catalog.findIndex((item) => item.id === id);
      if (idx < 0) return err(404, "NOT_FOUND", "missing");
      const body = req.postDataJSON() as { toolDisclosurePolicy?: { mode?: string } };
      catalog[idx] = {
        ...catalog[idx],
        toolDisclosurePolicy: body.toolDisclosurePolicy || {},
        toolDisclosureUI: "binary",
        lockVersion: Number(catalog[idx].lockVersion || 1) + 1,
      };
      return json(200, catalog[idx]);
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
        workspaceCount: 1,
        dailyTraffic: [],
        trendSnapshots: [],
        successFailure: [],
        riskConfig: {
          workspaces: 1,
          agents: 0,
          tools: 0,
          workflows: 0,
          connectionsVerified: 0,
          connectionsTotal: 0,
          modelsVerified: 0,
          modelsTotal: 0,
        },
        dailyDetails: [],
        topTools: [],
        failingTools: [],
        topWorkspaces: [],
      });
    }
    return err(501, "SMOKE_UNMOCKED", `unmocked ${method} ${path}`);
  });
}

async function login(page: Page) {
  await page.goto("/login");
  const user = page.locator('input[autocomplete="username"]');
  if (await user.isVisible({ timeout: 5_000 }).catch(() => false)) {
    const zh = page.locator('[data-testid="login-lang-zh-CN"]');
    if (await zh.isVisible().catch(() => false)) await zh.click();
    await user.fill("admin");
    await page.locator('input[autocomplete="current-password"]').fill("Password-123456");
    await page.getByRole("button", { name: /^登录$/ }).click();
  }
  await expect(page).toHaveURL(/overview|workspaces|agents|model-apis/, { timeout: 15_000 });
}

async function openRowMenu(page: Page, configName: string) {
  const name = page.getByTestId("model-config-name").filter({ hasText: configName });
  await expect(name).toBeVisible();
  const row = page.locator("tr").filter({ has: name });
  await row.getByRole("button", { name: "更多操作" }).click();
}

test.describe("tool disclosure console (mocked API)", () => {
  test.beforeEach(async ({ page }) => {
    await forceZhLocale(page);
  });

  test("list badges never use modelName and map UI tokens", async ({ page }) => {
    await mockDisclosureApi(page, [
      modelRow({
        id: "native-1",
        name: "Named Like GPT-5.6",
        modelName: "gpt-5.6-terra",
        status: "VERIFIED",
        toolDisclosureUI: "hidden",
        agenticCapabilities: { schemaVersion: "agentic-model.v1", toolSearchModes: ["client"] },
      }),
      modelRow({
        id: "fc-1",
        name: "Named Like GPT-5.2",
        modelName: "gpt-5.2",
        status: "VERIFIED",
        toolDisclosureUI: "binary",
        agenticCapabilities: { schemaVersion: "agentic-model.v2", toolCalling: "function_calling" },
      }),
      modelRow({
        id: "none-1",
        name: "Text Only",
        modelName: "gpt-5.4",
        status: "VERIFIED",
        toolDisclosureUI: "unavailable",
        agenticCapabilities: { schemaVersion: "agentic-model.v2", toolCalling: "none" },
      }),
      modelRow({ id: "raw-1", name: "Not Verified", modelName: "gpt-5.6-luna", status: "UNVERIFIED" }),
    ]);
    await login(page);
    await page.goto("/model-apis");
    await expect(page.getByRole("heading", { name: "模型 API 配置" })).toBeVisible({ timeout: 15_000 });

    await expect(page.locator('[data-testid="model-capability-badge"][data-model-name="gpt-5.6-terra"]')).toHaveText(
      "原生按需",
    );
    await expect(page.locator('[data-testid="model-capability-badge"][data-model-name="gpt-5.2"]')).toHaveText(
      "函数调用",
    );
    await expect(page.locator('[data-testid="model-capability-badge"][data-model-name="gpt-5.4"]')).toHaveText("无工具");
    await expect(page.locator('[data-testid="model-capability-badge"][data-model-name="gpt-5.6-luna"]')).toHaveText(
      "未验证",
    );
  });

  test("native edit shows hidden disclosure; FC shows radio; none has no radio", async ({ page }) => {
    await mockDisclosureApi(page, [
      modelRow({
        id: "native-1",
        name: "Native Config",
        modelName: "gpt-5.6-terra",
        status: "VERIFIED",
        toolDisclosureUI: "hidden",
      }),
      modelRow({
        id: "fc-1",
        name: "FC Config",
        modelName: "gpt-5.2",
        status: "VERIFIED",
        toolDisclosureUI: "binary",
        toolDisclosurePolicy: { schemaVersion: "tool-disclosure.v1", mode: "platform_on_demand" },
      }),
      modelRow({
        id: "none-1",
        name: "None Config",
        modelName: "text-only",
        status: "VERIFIED",
        toolDisclosureUI: "unavailable",
      }),
    ]);
    await login(page);
    await page.goto("/model-apis");

    await openRowMenu(page, "Native Config");
    await page.getByRole("menuitem", { name: "编辑" }).or(page.getByRole("button", { name: "编辑" })).click();
    await expect(page.getByTestId("model-disclosure")).toBeVisible();
    await expect(page.getByTestId("model-disclosure")).toContainText("原生工具检索");
    await expect(page.locator('[data-action="set-disclosure"]')).toHaveCount(0);
    await page.getByRole("button", { name: "取消" }).click();

    await openRowMenu(page, "FC Config");
    await page.getByRole("menuitem", { name: "编辑" }).or(page.getByRole("button", { name: "编辑" })).click();
    await expect(page.getByTestId("model-disclosure")).toBeVisible();
    await expect(page.locator('input[type="radio"][value="platform_on_demand"]')).toBeVisible();
    await expect(page.locator('input[type="radio"][value="carry_all"]')).toBeVisible();
    await page.locator('input[type="radio"][value="carry_all"]').check();
    await page.locator('[data-action="set-disclosure"]').click();
    await page.getByRole("button", { name: "取消" }).click();

    await openRowMenu(page, "None Config");
    await page.getByRole("menuitem", { name: "编辑" }).or(page.getByRole("button", { name: "编辑" })).click();
    await expect(page.getByTestId("model-disclosure")).toContainText("无法调用工具");
    await expect(page.locator('[data-action="set-disclosure"]')).toHaveCount(0);
  });
});
