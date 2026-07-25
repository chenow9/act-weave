import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  use: {
    baseURL: process.env.E2E_BASE_URL || "http://127.0.0.1:4173",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "npm run dev -- --port 4173",
    port: 4173,
    reuseExistingServer: true,
    stdout: "ignore",
    stderr: "pipe",
    timeout: 120_000,
  },
});
