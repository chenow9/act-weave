import { defineConfig } from "@playwright/test";

const isCI = Boolean(process.env.CI);

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  retries: isCI ? 1 : 0,
  // Chromium = Playwright's Chrome engine (used for console smoke + A2UI e2e).
  projects: [
    {
      name: "chromium",
      use: {
        browserName: "chromium",
      },
    },
  ],
  use: {
    baseURL: process.env.E2E_BASE_URL || "http://127.0.0.1:4173",
    // Desktop width keeps workflow workbench 3-column layout (media collapses at 1180px).
    viewport: { width: 1440, height: 960 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: process.env.E2E_VIDEO === "1" ? "retain-on-failure" : "off",
  },
  webServer: {
    // ZKL-64 item 9: production preview (CI already ran build; rebuild only if dist missing).
    command: isCI
      ? "npx vite preview --host 127.0.0.1 --port 4173"
      : "npm run build && npx vite preview --host 127.0.0.1 --port 4173",
    port: 4173,
    reuseExistingServer: false,
    stdout: "ignore",
    stderr: "pipe",
    timeout: 300_000,
  },
});
