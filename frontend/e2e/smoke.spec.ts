import { expect, test, type Page, type Route } from "@playwright/test";

/**
 * ZKL-64 item 9 — deterministic console smoke against vite preview.
 * All /api/v1 traffic is intercepted. Unmocked paths return 501 (fail-visible).
 * No E2E_USER/E2E_PASS, no shared environments, no real credentials.
 */

type SmokeUserKey = "admin" | "viewer" | "must.change" | "user";

const SMOKE_COOKIE = "smoke_auth=1";

function smokeUser(overrides: Record<string, unknown> = {}) {
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
    ...overrides,
  };
}

function smokeWorkspace(id: string, role: string, displayName?: string) {
  return {
    id,
    slug: id,
    displayName: displayName || `Workspace ${id}`,
    mode: "PRODUCTION",
    status: "ACTIVE",
    ownerUserId: "user-1",
    settings: {},
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lockVersion: 1,
    currentUserRole: role,
  };
}

function emptyOverviewMetrics() {
  return {
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
      workspaces: 2,
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
  };
}

function authToken(user: ReturnType<typeof smokeUser>, mustChangePassword = false) {
  return {
    accessToken: "smoke-token",
    accessTokenExpires: "2099-01-01T00:00:00Z",
    sessionId: "sess-1",
    mustChangePassword,
    user,
  };
}

function userForKey(key: SmokeUserKey) {
  switch (key) {
    case "viewer":
      return smokeUser({
        username: "viewer",
        displayName: "Smoke Viewer",
        platformRole: "USER",
      });
    case "must.change":
      return smokeUser({ username: "must.change", displayName: "Must Change" });
    case "user":
      return smokeUser({
        username: "user",
        displayName: "Smoke User",
        platformRole: "USER",
      });
    default:
      return smokeUser({ username: "admin", platformRole: "PLATFORM_ADMIN" });
  }
}

/** Track list GET counts so mutation → fresh reload is observable. */
type RequestLog = { method: string; path: string; at: number };

async function mockApiV1(page: Page, options?: { workspaceRoleForAdmin?: string }) {
  const log: RequestLog[] = [];
  let sessionUser: ReturnType<typeof smokeUser> | null = null;
  let mustChange = false;
  const workspaceRole = options?.workspaceRoleForAdmin || "EDITOR";
  const created: Array<ReturnType<typeof smokeWorkspace>> = [];

  const catalog = () => {
    const base =
      sessionUser?.username === "viewer"
        ? [smokeWorkspace("ws-1", "VIEWER"), smokeWorkspace("ws-2", "VIEWER")]
        : [smokeWorkspace("ws-1", workspaceRole), smokeWorkspace("ws-2", "VIEWER"), ...created];
    return base;
  };

  await page.route("**/api/v1/**", async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname.replace(/^\/api\/v1/, "") || "/";
    const method = req.method();
    log.push({ method, path, at: Date.now() });

    const json = (status: number, body: unknown, extraHeaders: Record<string, string> = {}) =>
      route.fulfill({
        status,
        contentType: "application/json",
        headers: extraHeaders,
        body: JSON.stringify(body),
      });

    const err = (status: number, code: string, message: string) =>
      json(status, { error: { code, message, requestId: "req-smoke" } });

    // --- auth ---
    if (method === "POST" && path === "/auth/login") {
      const raw = req.postDataJSON() as { username?: string } | null;
      const username = raw?.username || "";
      if (username === "must.change") {
        sessionUser = userForKey("must.change");
        mustChange = true;
        return json(200, authToken(sessionUser, true), {
          "Set-Cookie": `${SMOKE_COOKIE}; Path=/`,
        });
      }
      if (username === "viewer") {
        sessionUser = userForKey("viewer");
        mustChange = false;
        return json(200, authToken(sessionUser, false), {
          "Set-Cookie": `${SMOKE_COOKIE}; Path=/`,
        });
      }
      if (username === "user") {
        sessionUser = userForKey("user");
        mustChange = false;
        return json(200, authToken(sessionUser, false), {
          "Set-Cookie": `${SMOKE_COOKIE}; Path=/`,
        });
      }
      // default admin
      sessionUser = userForKey("admin");
      mustChange = false;
      return json(200, authToken(sessionUser, false), {
        "Set-Cookie": `${SMOKE_COOKIE}; Path=/`,
      });
    }

    if (method === "POST" && path === "/auth/refresh") {
      // Only restore when login previously set smoke cookie (hard-nav rehydrate).
      const cookie = req.headers()["cookie"] || "";
      if (!cookie.includes("smoke_auth=1") || !sessionUser) {
        // After full page reload, in-memory sessionUser is gone — allow cookie alone.
        if (cookie.includes("smoke_auth=1")) {
          sessionUser = userForKey("admin");
          mustChange = false;
          return json(200, authToken(sessionUser, false));
        }
        return err(401, "UNAUTHORIZED", "no session");
      }
      return json(200, authToken(sessionUser, mustChange));
    }

    if (method === "POST" && path === "/auth/logout") {
      sessionUser = null;
      mustChange = false;
      return route.fulfill({
        status: 204,
        headers: { "Set-Cookie": "smoke_auth=; Path=/; Max-Age=0" },
        body: "",
      });
    }

    if (method === "POST" && path === "/users/me:change-password") {
      // Success: sessions revoked; client clears and re-logins.
      sessionUser = null;
      mustChange = false;
      return route.fulfill({
        status: 204,
        headers: { "Set-Cookie": "smoke_auth=; Path=/; Max-Age=0" },
        body: "",
      });
    }

    if (method === "GET" && path === "/users/me") {
      if (!sessionUser && !(req.headers()["cookie"] || "").includes("smoke_auth=1")) {
        return err(401, "UNAUTHORIZED", "no session");
      }
      const user = sessionUser || userForKey("admin");
      return json(200, user);
    }

    // --- workspaces ---
    if (method === "GET" && path === "/workspaces") {
      const items = catalog();
      return json(200, {
        items,
        pagination: { page: 1, pageSize: 50, total: items.length },
        summary: {
          total: items.length,
          active: items.length,
          production: items.length,
          boundAgents: 0,
        },
      });
    }

    if (method === "POST" && path === "/workspaces") {
      const raw = req.postDataJSON() as {
        slug?: string;
        displayName?: string;
        mode?: string;
      } | null;
      const id = `ws-created-${created.length + 1}`;
      const ws = smokeWorkspace(id, "OWNER", raw?.displayName || id);
      ws.slug = raw?.slug || id;
      ws.mode = raw?.mode || "PRODUCTION";
      created.push(ws);
      return json(201, ws);
    }

    if (method === "GET" && /^\/workspaces\/[^/]+$/.test(path)) {
      const id = path.split("/")[2];
      const found = catalog().find((w) => w.id === id);
      if (!found) return err(404, "NOT_FOUND", "workspace not found");
      return json(200, found);
    }

    if (method === "GET" && /^\/workspaces\/[^/]+\/agents$/.test(path)) {
      return json(200, { items: [] });
    }

    if (method === "GET" && /^\/workspaces\/[^/]+\/tools$/.test(path)) {
      return json(200, { items: [] });
    }

    if (method === "GET" && path === "/overview/metrics") {
      return json(200, emptyOverviewMetrics());
    }

    // Catch-all: fail loudly so missing mocks are visible in CI.
    return err(501, "SMOKE_UNMOCKED", `unmocked ${method} ${path}`);
  });

  return {
    log,
    countGets: (pathPrefix: string) => log.filter((e) => e.method === "GET" && e.path.startsWith(pathPrefix)).length,
    countPosts: (pathExact: string) => log.filter((e) => e.method === "POST" && e.path === pathExact).length,
  };
}

/** Product DEFAULT_LOCALE is en; smoke assertions use zh-CN console copy. */
async function forceZhLocale(page: Page) {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("actweave.locale", "zh-CN");
    } catch {
      // ignore
    }
  });
}

async function expectLoginPage(page: Page) {
  // Prefer explicit language control when the switcher is visible (overrides navigator).
  const zh = page.locator('[data-testid="login-lang-zh-CN"]');
  if (await zh.isVisible().catch(() => false)) {
    await zh.click();
  }
  // LoginView: brand "ACTWEAVE 织行" + heading "登录" (not the legacy "登录 ActWeave").
  await expect(page.getByRole("heading", { name: "登录", exact: true })).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByText("ACTWEAVE 织行").first()).toBeVisible();
}

async function loginAs(page: Page, username: string, password = "Password-123456") {
  await page.goto("/login");
  await expectLoginPage(page);
  await page.locator('input[autocomplete="username"]').fill(username);
  await page.locator('input[autocomplete="current-password"]').fill(password);
  await page.getByRole("button", { name: /^登录$/ }).click();
}

test.describe("console smoke (mocked API)", () => {
  test.beforeEach(async ({ page }) => {
    await forceZhLocale(page);
  });

  test("login page renders core affordances", async ({ page }) => {
    await mockApiV1(page);
    await page.goto("/login");
    await expectLoginPage(page);
    await expect(page.locator('input[autocomplete="username"]')).toBeVisible();
    await expect(page.locator('input[autocomplete="current-password"]')).toBeVisible();
    await expect(page.getByRole("button", { name: /^登录$/ })).toBeVisible();
  });

  test("normal login reaches app shell and can switch workspace", async ({ page }) => {
    await mockApiV1(page);
    await loginAs(page, "admin");
    await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
    await expect(page.getByRole("button", { name: /打开用户菜单/ })).toBeVisible({ timeout: 15_000 });

    // Switcher is hidden on overview; open a workspace-scoped page first.
    await page.goto("/agents");
    await expect(page.getByTestId("workspace-switcher")).toBeVisible({ timeout: 15_000 });
    await page.getByTestId("workspace-switcher").click();
    await expect(page.getByRole("dialog", { name: /选择业务空间/ })).toBeVisible({ timeout: 10_000 });
    await page.locator('[data-workspace-id="ws-2"]').click();
    await expect(page.getByTestId("workspace-switcher")).toContainText("Workspace ws-2", {
      timeout: 10_000,
    });
  });

  test("must-change-password users are forced onto change-password and can complete", async ({ page }) => {
    await mockApiV1(page);
    await loginAs(page, "must.change");
    await expect(page).toHaveURL(/change-password/, { timeout: 15_000 });
    await expect(page.getByRole("heading", { name: "修改密码", exact: true })).toBeVisible();

    await page.locator('input[autocomplete="current-password"]').fill("Temp-Password-1");
    const newPassInputs = page.locator('input[autocomplete="new-password"]');
    await newPassInputs.nth(0).fill("New-Password-123456");
    await newPassInputs.nth(1).fill("New-Password-123456");
    // auth.submitChangePassword (zh-CN): "更新密码"
    await page.getByRole("button", { name: /更新密码/ }).click();
    await expect(page).toHaveURL(/login/, { timeout: 15_000 });
    await expect(page.getByText(/密码已更新/)).toBeVisible({ timeout: 10_000 });
  });

  test("VIEWER hides agent create affordance", async ({ page }) => {
    await mockApiV1(page);
    await loginAs(page, "viewer");
    await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
    await page.goto("/agents");
    await expect(page.getByRole("heading", { name: /Agent/i }).first()).toBeVisible({ timeout: 15_000 });
    await expect(page.locator("button.agent-create-button")).toHaveCount(0);
  });

  test("EDITOR sees agent create affordance", async ({ page }) => {
    await mockApiV1(page, { workspaceRoleForAdmin: "EDITOR" });
    await loginAs(page, "admin");
    await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
    await page.goto("/agents");
    // Hard reload rehydrates via cookie-backed refresh; create button is RBAC-gated.
    await expect(page.locator("button.agent-create-button")).toBeVisible({ timeout: 15_000 });
  });

  test("non-platform-admin is rejected from /users onto overview", async ({ page }) => {
    await mockApiV1(page);
    await loginAs(page, "user");
    await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
    await page.goto("/users");
    await expect(page).toHaveURL(/overview/, { timeout: 15_000 });
    await expect(page.getByRole("heading", { name: /空间总览/ })).toBeVisible({ timeout: 15_000 });
  });

  test("unknown deep link shows NotFound after session restore", async ({ page }) => {
    await mockApiV1(page);
    await loginAs(page, "admin");
    await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
    // Hard navigation: cookie-backed refresh restores session → AppShell NotFound.
    await page.goto("/this-path-does-not-exist-zkl64");
    await expect(page.getByText("404")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText("页面不存在")).toBeVisible();
    await expect(page.getByRole("button", { name: /返回空间总览/ })).toBeVisible();
  });

  test("workspaces list → create → fresh GET reload", async ({ page }) => {
    const api = await mockApiV1(page);
    await loginAs(page, "admin");
    await expect(page).toHaveURL(/overview|workspaces|agents/, { timeout: 15_000 });
    await page.goto("/workspaces");
    await expect(page.getByRole("heading", { name: /业务空间/ })).toBeVisible({ timeout: 15_000 });

    const getsBeforeCreate = api.countGets("/workspaces");
    await page.getByRole("button", { name: "新建业务空间" }).click();
    const dialog = page.getByRole("dialog", { name: "新建业务空间" });
    await expect(dialog).toBeVisible({ timeout: 10_000 });

    await dialog.getByPlaceholder("例如: customer-service").fill("smoke-ws");
    await dialog.getByPlaceholder("例如: 客户服务业务空间").fill("Smoke Created Workspace");
    await expect(dialog.getByRole("button", { name: "创建业务空间" })).toBeEnabled({ timeout: 5_000 });
    await dialog.getByRole("button", { name: "创建业务空间" }).click();

    // POST create must happen, then at least one more GET /workspaces (fresh list reload).
    await expect.poll(() => api.countPosts("/workspaces"), { timeout: 15_000 }).toBeGreaterThanOrEqual(1);
    await expect.poll(() => api.countGets("/workspaces"), { timeout: 15_000 }).toBeGreaterThan(getsBeforeCreate);

    await expect(page.getByRole("button", { name: "查看 Smoke Created Workspace 详情", exact: true })).toBeVisible({
      timeout: 15_000,
    });
  });
});
