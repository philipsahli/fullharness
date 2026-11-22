import { test as base, expect } from "@playwright/test";
import { Fh } from "./fh";

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
