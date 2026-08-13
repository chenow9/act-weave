import { defineConfig } from "@playwright/test";

/**
 * Live Console + backend e2e. Does not start vite preview (no /api proxy).
 * Point E2E_BASE_URL at `npm run dev` (default :5174) with backend on :8082.
 */
export default defineConfig({
  testDir: "./e2e",
  timeout: 300_000,
  retries: 0,
  workers: 1,
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
  use: {
    baseURL: process.env.E2E_BASE_URL || "http://127.0.0.1:5174",
    viewport: { width: 1440, height: 960 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: process.env.E2E_VIDEO === "1" ? "retain-on-failure" : "off",
  },
});
