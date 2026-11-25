import { defineConfig, devices } from "@playwright/test";

// ADR 0007: Chromium only. Each test starts its own fh (see e2e/fh.ts), so
// tests need no web server here and can run in parallel.
// ADR 0012: the "visual" project holds the local pixel tests. It runs only
// through make e2e-visual on a Mac; make e2e and CI run "chromium" alone.
export default defineConfig({
  testDir: "e2e",
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: { trace: "retain-on-failure" },
  // Baselines are per contributor and gitignored, so no OS or browser suffix.
  snapshotPathTemplate: "{testDir}/__visual__/{arg}{ext}",
  // At most 100 different pixels, not the Spec's 1 %: an accent colour change
  // touches about 0.3 % of a screen and must fail, and three unchanged runs on
  // one Mac differed in 0 pixels (#169).
  expect: { toHaveScreenshot: { maxDiffPixels: 100, animations: "disabled" } },
  projects: [
    {
      name: "chromium",
      testIgnore: /visual\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "visual",
      testMatch: /visual\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 800 } },
    },
  ],
});
