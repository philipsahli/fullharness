import { defineConfig, devices } from "@playwright/test";

// ADR 0007: Chromium only. Each test starts its own fh (see e2e/fh.ts), so
// tests need no web server here and can run in parallel.
export default defineConfig({
  testDir: "e2e",
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: { trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
