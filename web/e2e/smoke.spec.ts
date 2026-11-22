import { test as base, expect } from "@playwright/test";
import { Fh, runFh } from "./fh";
import { tempFolder, tempRepo } from "./repo";

// Browser smoke tests (ADR 0007): the UI logic no Go test can reach.
const test = base.extend<{ fh: Fh }>({
  // eslint-disable-next-line no-empty-pattern -- Playwright fixtures must destructure their first argument
  fh: async ({}, use) => {
    const fh = await Fh.start();
    await use(fh);
    await fh.dispose();
  },
});

test("the token URL opens the empty app shell", async ({ page, fh }) => {
  await page.goto(fh.launchURL);

  await expect(page).toHaveURL(`${fh.origin}/`); // the token left the address bar
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  await expect(page.getByRole("tab", { name: "Sessions" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Software factory" })).toBeVisible();
  await expect(page.getByRole("tab", { name: /Inbox/ })).toContainText("0");
  await expect(page.getByText("No Sessions yet.")).toBeVisible();
});

test("a tab left open across a restart tells the user to run fh", async ({ page, fh }) => {
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");

  await fh.restart();

  // The tab retries every 2 s while fh is down, then gets 401 from the new launch.
  await expect(page.getByRole("alert")).toHaveText("fh restarted. Run fh to open it again.", {
    timeout: 10_000,
  });
});

test("adding a Project shows its main tree, and a non-repo shows why not", async ({ page, fh }) => {
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });

  await projects.getByRole("button", { name: "+ Add Project" }).click();
  const path = projects.getByLabel("Path to a git repository");
  await path.fill(tempFolder());
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects.getByRole("alert")).toContainText("is not a git repository");

  await path.fill(tempRepo("shop"));
  await projects.getByRole("button", { name: "Add", exact: true }).click();

  const shop = projects.getByRole("listitem").filter({ hasText: "shop" }).first();
  await expect(shop).toContainText("main");
  await expect(shop).toContainText("main tree");
});

test("in dev mode a restarted fh reloads the open tab instead of stopping it", async ({ page }) => {
  const fh = await Fh.start({ dev: true });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    await page.evaluate(
      () => ((window as unknown as { beforeRestart: boolean }).beforeRestart = true),
    );

    await fh.restart();

    // A reload clears the marker; the reloaded page connects to the new build.
    await expect
      .poll(() => page.evaluate(() => "beforeRestart" in window), { timeout: 10_000 })
      .toBe(false);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    await expect(page.getByRole("alert")).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});

test("--dev without --dir is refused", async () => {
  const { code, stderr } = await runFh(["--dev", "--no-open"]);

  expect(code).not.toBe(0);
  expect(stderr).toContain("--dev needs --dir");
});
