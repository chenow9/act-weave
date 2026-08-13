import { expect, test, type Page } from "@playwright/test";

/**
 * Live e2e: create/verify two model configs against a real compatible gateway.
 *
 * Required:
 *   E2E_MODEL_API_BASE  (e.g. http://192.168.20.4:7080/v1)
 *   E2E_MODEL_API_KEY
 *
 * Optional:
 *   E2E_BASE_URL        default http://127.0.0.1:5174
 *   E2E_USER / E2E_PASS default bootstrap admin
 *   E2E_NATIVE_MODEL    default gpt-5.6-terra  (real 5.6)
 *   E2E_ALIAS_MODEL     default gpt-5.2        (third-party alias, not OpenAI)
 *
 * Run: npm run e2e:disclosure:live
 * Do not put the API key in git. This spec is skipped when the key is absent.
 */

const API_BASE = process.env.E2E_MODEL_API_BASE || "";
const API_KEY = process.env.E2E_MODEL_API_KEY || "";
const ADMIN_USER = process.env.E2E_USER || "admin";
const ADMIN_PASS = process.env.E2E_PASS || "actweave-admin-dev-change-me";
const NATIVE_MODEL = process.env.E2E_NATIVE_MODEL || "gpt-5.6-terra";
const ALIAS_MODEL = process.env.E2E_ALIAS_MODEL || "gpt-5.2";
const RUN_ID = `e2e-disc-${Date.now()}`;

const nativeName = `${RUN_ID}-terra`;
const aliasName = `${RUN_ID}-alias`;

async function forceZhLocale(page: Page) {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("actweave.locale", "zh-CN");
    } catch {
      // ignore
    }
  });
}

async function login(page: Page) {
  await page.goto("/login");
  const zh = page.locator('[data-testid="login-lang-zh-CN"]');
  if (await zh.isVisible().catch(() => false)) await zh.click();
  await expect(page.getByRole("heading", { name: "登录", exact: true })).toBeVisible({ timeout: 20_000 });
  await page.locator('input[autocomplete="username"]').fill(ADMIN_USER);
  await page.locator('input[autocomplete="current-password"]').fill(ADMIN_PASS);
  await page.getByRole("button", { name: /^登录$/ }).click();
  await page.waitForURL((url) => !url.pathname.includes("/login"), { timeout: 30_000 });
}

async function openCreate(page: Page) {
  await page.goto("/model-apis");
  await expect(page.getByTestId("model-create")).toBeVisible({ timeout: 20_000 });
  await page.getByTestId("model-create").click();
  await expect(page.getByRole("dialog")).toBeVisible();
}

async function searchConfigs(page: Page, name: string) {
  const search = page.getByRole("searchbox").or(page.getByPlaceholder(/Search model|搜索模型/));
  await search.fill(name);
  await expect(page.getByTestId("model-config-name").filter({ hasText: name })).toBeVisible({ timeout: 20_000 });
}

async function fillAndCreate(page: Page, name: string, modelName: string) {
  await openCreate(page);
  await page.getByTestId("model-field-name").fill(name);
  await page.getByTestId("model-field-api-key").fill(API_KEY);
  await page.getByTestId("model-field-api-base").fill(API_BASE);
  await page.getByTestId("model-field-model-name").fill(modelName);
  await page.locator('[data-action="save-model-config"]').click();
  await searchConfigs(page, name);
}

async function rowMenuAction(page: Page, configName: string, action: "测试" | "编辑" | "删除") {
  const name = page.getByTestId("model-config-name").filter({ hasText: configName });
  await expect(name).toBeVisible({ timeout: 15_000 });
  const row = page.locator("tr").filter({ has: name });
  await row.getByRole("button", { name: /更多操作|More actions/ }).click();
  const label =
    action === "测试" ? /测试|Test/ : action === "编辑" ? /编辑|Edit/ : /删除|Delete/;
  await page.getByRole("menuitem", { name: label }).click();
}

async function verifyOnce(page: Page, configName: string) {
  const done = page.waitForResponse(
    (response) => response.request().method() === "POST" && response.url().includes(":verify"),
    { timeout: 180_000 },
  );
  await rowMenuAction(page, configName, "测试");
  const response = await done;
  const body = (await response.json().catch(() => ({}))) as { status?: string; lastErrorCode?: string };
  const toast = await page
    .locator(".action-toast")
    .innerText()
    .catch(() => "");
  return { httpStatus: response.status(), status: body.status || "", lastErrorCode: body.lastErrorCode || "", toast };
}

async function verifyConfig(page: Page, configName: string) {
  let result = await verifyOnce(page, configName);
  if (result.status !== "VERIFIED" && result.lastErrorCode === "MODEL_CONFIG_VERIFICATION_TIMEOUT") {
    result = await verifyOnce(page, configName);
  }
  return result;
}

async function deleteConfig(page: Page, configName: string) {
  await rowMenuAction(page, configName, "删除");
  const confirm = page.getByRole("button", { name: /删除模型配置|Delete model config/ });
  if (await confirm.isVisible().catch(() => false)) await confirm.click();
}

test.describe("tool disclosure live gateway", () => {
  test.skip(!API_KEY || !API_BASE, "Set E2E_MODEL_API_KEY and E2E_MODEL_API_BASE to run live disclosure e2e.");
  test.setTimeout(300_000);

  test.beforeEach(async ({ page }) => {
    await forceZhLocale(page);
  });

  test.afterEach(async ({ page }) => {
    await page.goto("/model-apis").catch(() => undefined);
    for (const name of [nativeName, aliasName]) {
      const search = page.getByRole("searchbox").or(page.getByPlaceholder(/Search model|搜索模型/));
      if (await search.isVisible().catch(() => false)) await search.fill(name);
      const loc = page.getByTestId("model-config-name").filter({ hasText: name });
      if (await loc.isVisible().catch(() => false)) {
        await deleteConfig(page, name).catch(() => undefined);
      }
    }
  });

  test("probe-first: real 5.6 vs third-party 5.2 alias get different disclosure UI", async ({ page }) => {
    await login(page);

    await fillAndCreate(page, nativeName, NATIVE_MODEL);
    const nativeRow = page.locator("tr").filter({
      has: page.getByTestId("model-config-name").filter({ hasText: nativeName }),
    });
    await expect(nativeRow.getByTestId("model-capability-badge")).toHaveText(/未验证|Unverified/);
    const nativeVerify = await verifyConfig(page, nativeName);
    expect(nativeVerify.status, `native ${nativeVerify.lastErrorCode} ${nativeVerify.toast}`).toBe("VERIFIED");
    const nativeBadge = nativeRow.getByTestId("model-capability-badge");
    await expect(nativeBadge).not.toHaveText(/未验证|Unverified/, { timeout: 10_000 });
    const nativeKind = (await nativeBadge.innerText()).trim();

    await rowMenuAction(page, nativeName, "编辑");
    const nativeDisclosure = page.getByTestId("model-disclosure");
    const nativeIsNative = /原生按需|Native/.test(nativeKind);
    if (nativeIsNative) {
      await expect(nativeDisclosure).toBeVisible();
      await expect(nativeDisclosure).toContainText(/原生工具检索|native tool search/i);
      await expect(page.locator('[data-action="set-disclosure"]')).toHaveCount(0);
    }
    await page.getByRole("button", { name: /取消|Cancel/ }).click();

    await fillAndCreate(page, aliasName, ALIAS_MODEL);
    const aliasRow = page.locator("tr").filter({
      has: page.getByTestId("model-config-name").filter({ hasText: aliasName }),
    });
    const aliasVerify = await verifyConfig(page, aliasName);
    expect(aliasVerify.status, `alias ${aliasVerify.lastErrorCode} ${aliasVerify.toast}`).toBe("VERIFIED");
    const aliasBadge = aliasRow.getByTestId("model-capability-badge");
    await expect(aliasBadge).not.toHaveText(/未验证|Unverified/, { timeout: 10_000 });
    const aliasKind = (await aliasBadge.innerText()).trim();

    await rowMenuAction(page, aliasName, "编辑");
    const aliasDisclosure = page.getByTestId("model-disclosure");
    const aliasIsFC = /函数调用|Function/.test(aliasKind);
    const aliasIsNone = /无工具|No tools/.test(aliasKind);
    if (aliasIsFC) {
      await expect(aliasDisclosure).toBeVisible();
      await expect(page.locator('input[type="radio"][value="platform_on_demand"]')).toBeVisible();
      await expect(page.locator('input[type="radio"][value="carry_all"]')).toBeVisible();
    } else if (aliasIsNone) {
      await expect(aliasDisclosure).toContainText(/无法调用工具|cannot call tools/i);
      await expect(page.locator('[data-action="set-disclosure"]')).toHaveCount(0);
    }
    await page.getByRole("button", { name: /取消|Cancel/ }).click();

    expect(nativeVerify.status, `native ${nativeVerify.lastErrorCode} ${nativeVerify.toast}`).toBe("VERIFIED");
    expect(aliasVerify.status, `alias ${aliasVerify.lastErrorCode} ${aliasVerify.toast}`).toBe("VERIFIED");
    test.info().annotations.push(
      { type: "native-model", description: `${NATIVE_MODEL} → ${nativeKind}` },
      { type: "alias-model", description: `${ALIAS_MODEL} → ${aliasKind}` },
    );
  });
});
