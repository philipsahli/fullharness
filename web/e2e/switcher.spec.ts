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

// The Switcher works from the keyboard alone (#248), as the mockup's
// openPalette does: the input is a combobox that names the highlighted
// option, focus stays in the dialog, Escape closes it from anywhere in it,
// and the highlight scrolls into view in a long list.
test("⌘K is a combobox that names the highlighted option and keeps focus", async ({ page }) => {
  // Twenty open Tickets, more rows than the list shows at once.
  const tickets = Array.from({ length: 20 }, (_, i) => ({
    number: 11 + i,
    title: `Shop task ${i + 1}`,
    labels: ["ready-for-agent"],
    parent: 10,
  }));
  const PATH = fakeGhPath([
    {
      number: 10,
      title: "Login",
      labels: ["ready-for-agent"],
      subIssues: tickets.map((t) => t.number),
    },
    ...tickets,
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
    // The board has read the Tickets before ⌘K looks for them.
    await page.getByRole("tab", { name: "Software factory" }).click();
    await expect(page.getByRole("button", { name: /^Login/ })).toBeVisible();

    await page.keyboard.press("ControlOrMeta+k");
    const dialog = page.getByRole("dialog", { name: "Switcher" });
    const search = dialog.getByRole("combobox", { name: "Find a Session or Ticket" });
    const options = dialog.getByRole("option");
    await search.fill("shop task");
    await expect(options).toHaveCount(20);

    // aria-activedescendant follows the highlight.
    const selected = dialog.locator('[role="option"][aria-selected="true"]');
    const named = async () =>
      expect(search).toHaveAttribute(
        "aria-activedescendant",
        (await selected.getAttribute("id")) ?? "no id",
      );
    await expect(selected).toContainText("Shop task 1");
    await named();
    await page.keyboard.press("ArrowDown");
    await expect(selected).toContainText("Shop task 2");
    await named();

    // The last row starts below the list's edge; walking the highlight down
    // to it scrolls it into view.
    const last = options.filter({ hasText: "Shop task 20" });
    await expect(last).not.toBeInViewport();
    for (let i = 0; i < 18; i++) await page.keyboard.press("ArrowDown");
    await expect(selected).toContainText("Shop task 20");
    await named();
    await expect(last).toBeInViewport();

    // Tab and Shift+Tab leave focus on the input, the dialog's one control
    // (Chrome would otherwise focus the scrolling list, then the page);
    // Escape still closes it.
    await page.keyboard.press("Tab");
    await expect(search).toBeFocused();
    await page.keyboard.press("Shift+Tab");
    await expect(search).toBeFocused();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
  } finally {
    await fh.dispose();
  }
});
