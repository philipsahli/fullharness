import { test, expect } from "@playwright/test";
import { Fh } from "./fh";
import { fakeGhPath } from "./gh";
import { tempRepo, withOrigin } from "./repo";

// ⌘K finds the open Tickets of the board's Project (#195): by number or by
// words in the title, after the Sessions, and a pick opens the Ticket's
// Feature on the board with its row marked.
test("⌘K finds a Ticket of the board's Project and opens its Feature at the row", async ({
  page,
}) => {
  const PATH = fakeGhPath([
    { number: 10, title: "Login", labels: ["ready-for-agent"], subIssues: [11, 12, 13] },
    { number: 11, title: "Shop login form", labels: ["ready-for-agent"], parent: 10 },
    { number: 12, title: "Shop session cookie", labels: ["ready-for-agent"], parent: 10 },
    { number: 13, title: "Shop logout", state: "CLOSED", parent: 10 },
  ]);
  const fh = await Fh.start({ env: { PATH }, agentScript: { default: [{ say: "hello" }] } });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects
      .getByLabel("Path to a git repository")
      .fill(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    // A Session in shop, so "shop" matches a Session as well as Tickets.
    await page.getByRole("button", { name: "+ New Session" }).click();
    const form = page.getByRole("form", { name: "New Session" });
    await form.getByRole("radio", { name: /main/ }).check();
    await form.getByRole("button", { name: "Start Session" }).click();
    await expect(page.getByRole("region", { name: /^Session / })).toBeVisible();

    await page.keyboard.press("ControlOrMeta+k");
    const dialog = page.getByRole("dialog", { name: "Switcher" });
    const search = dialog.getByLabel("Find a Session or Ticket");
    const options = dialog.getByRole("option");

    // Words: the Session first, then the open Tickets in board order; the
    // closed #13 is left out.
    await search.fill("shop");
    await expect(options).toHaveCount(3);
    await expect(options.nth(0)).toContainText("Claude Code");
    await expect(options.nth(1)).toContainText("Ticket#11Shop login form");
    await expect(options.nth(2)).toContainText("Ticket#12Shop session cookie");

    // A number: that Ticket alone, with its Feature.
    await search.fill("#12");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText("Shop session cookie · Login");
    await search.fill("13");
    await expect(dialog.getByText("No match.")).toBeVisible();

    await search.fill("#12");
    await expect(options).toHaveCount(1);
    await page.keyboard.press("Enter");

    await expect(dialog).toBeHidden();
    await expect(page.getByRole("tab", { name: "Software factory" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const detail = page.getByRole("region", { name: "Feature Login" });
    const row = detail.getByRole("listitem").filter({ hasText: "Shop session cookie" });
    await expect(row).toHaveClass(/sw-flash/);
    await expect(
      detail.getByRole("listitem").filter({ hasText: "Shop login form" }),
    ).not.toHaveClass(/sw-flash/);
  } finally {
    await fh.dispose();
  }
});
