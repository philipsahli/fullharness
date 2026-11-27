import { test, expect } from "@playwright/test";
import { Fh } from "./fh";
import { fakeGhPath } from "./gh";
import { tempRepo, tempRepoWith, withOrigin } from "./repo";

const ticket = (n: string, title: string, status: string) =>
  `# ${n}: ${title}\n\n**Blocked by:** None\n\n**Status:** ${status}\n`;

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
    const search = dialog.getByLabel("Find a Session, Ticket or number");
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
    const search = dialog.getByRole("combobox", { name: "Find a Session, Ticket or number" });
    const options = dialog.getByRole("option");
    // Modal, as the mockup's openPalette: the page behind is inert to a
    // screen reader while the Switcher is open.
    await expect(dialog).toHaveAttribute("aria-modal", "true");
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

// A local board's Feature id is its folder name, which may hold a space.
// Each option id is still one valid ID reference with no whitespace, and
// "dark mode" and "dark-mode" don't collapse to the same id.
test("⌘K option ids hold no whitespace and stay unique when a Feature id has a space", async ({
  page,
}) => {
  const shop = tempRepoWith("shop", {
    ".scratch/dark mode/spec.md": "# Dark mode\n",
    ".scratch/dark mode/issues/01-toggle.md": "# Toggle dark\n\n**Status:** ready-for-agent\n",
    ".scratch/dark-mode/spec.md": "# Dark mode too\n",
    ".scratch/dark-mode/issues/01-toggle.md":
      "# Toggle dark again\n\n**Status:** ready-for-agent\n",
  });
  const fh = await Fh.start({ ghPRs: [] });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(shop);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    await page.getByRole("tab", { name: "Software factory" }).click();
    await expect(page.getByRole("button", { name: /^Dark mode too/ })).toBeVisible();

    await page.keyboard.press("ControlOrMeta+k");
    const dialog = page.getByRole("dialog", { name: "Switcher" });
    const search = dialog.getByRole("combobox", { name: "Find a Session, Ticket or number" });
    const options = dialog.getByRole("option");
    await search.fill("toggle");
    await expect(options).toHaveCount(2);

    const ids = await options.evaluateAll((els) => els.map((el) => el.id));
    expect(new Set(ids).size).toBe(2);
    for (const id of ids) expect(id).toMatch(/^\S+$/);
    await expect(search).toHaveAttribute("aria-activedescendant", ids[0]);
    await page.keyboard.press("ArrowDown");
    await expect(search).toHaveAttribute("aria-activedescendant", ids[1]);
  } finally {
    await fh.dispose();
  }
});

// A number finds every open Spec, Ticket, PR and issue with that number in
// all Projects, the board's Project first, each with a grey type tag
// (#254). A pick opens it where fh shows it.
test("⌘K finds anything by number across Projects and opens it where fh shows it", async ({
  page,
}) => {
  const PATH = fakeGhPath(
    [
      { number: 10, title: "Login", labels: ["ready-for-agent"], subIssues: [11] },
      { number: 11, title: "Login form", labels: ["ready-for-agent"], parent: 10 },
      { number: 2, title: "Drift alert fires twice", labels: ["needs-triage"] },
    ],
    { prs: [{ number: 21, title: "Add the login form", headRefName: "11-form", state: "OPEN" }] },
  );
  const fh = await Fh.start({ env: { PATH } });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    const add = async (dir: string, name: string) => {
      await projects.getByRole("button", { name: "+ Add Project" }).click();
      await projects.getByLabel("Path to a git repository").fill(dir);
      await projects.getByRole("button", { name: "Add", exact: true }).click();
      await expect(projects).toContainText(name);
    };
    await add(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"), "shop");
    await add(
      tempRepoWith("notes", {
        ".scratch/tags/spec.md": "# Tags\n",
        ".scratch/tags/issues/02-colour.md": ticket("02", "Tag colour", "ready-for-agent"),
      }),
      "notes",
    );
    // The board shows shop, so shop's items come first.
    await page.getByRole("tab", { name: "Software factory" }).click();
    await page.getByRole("combobox", { name: "Project" }).selectOption({ label: "shop" });

    const dialog = page.getByRole("dialog", { name: "Switcher" });
    const search = dialog.getByLabel("Find a Session, Ticket or number");
    const options = dialog.getByRole("option");
    const open = async (q: string) => {
      await page.keyboard.press("ControlOrMeta+k");
      await search.fill(q);
    };

    // #2 is an issue in shop and a Ticket in notes: two Project groups.
    await open("#2");
    await expect(options).toHaveCount(2);
    await expect(dialog.locator("li.pgroup")).toHaveText(["shop· this Project", "notes"]);
    await expect(options.nth(0)).toContainText("Issue#2Drift alert fires twice");
    await expect(options.nth(0)).toContainText("→ Needs triage");
    await expect(options.nth(1)).toContainText("Ticket#02Tag colour · Tags");
    // By-number rows take part in the highlight (#248): each has its own id,
    // which aria-activedescendant names as the keys move.
    const ids = await options.evaluateAll((els) => els.map((el) => el.id));
    expect(new Set(ids).size).toBe(2);
    for (const id of ids) expect(id).toMatch(/^\S+$/);
    await expect(search).toHaveAttribute("aria-activedescendant", ids[0]);
    await page.keyboard.press("ArrowDown");
    await expect(search).toHaveAttribute("aria-activedescendant", ids[1]);
    await page.keyboard.press("ArrowUp");
    await expect(search).toHaveAttribute("aria-activedescendant", ids[0]);
    await page.keyboard.press("Enter");
    await expect(
      page
        .getByRole("region", { name: "Needs triage" })
        .getByRole("listitem")
        .filter({ hasText: "Drift alert fires twice" }),
    ).toHaveClass(/sw-flash/);

    // A Spec opens its Feature page.
    await open("10");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText("Spec#10Login");
    await expect(options.first()).toContainText("→ Feature page");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("region", { name: "Feature Login" })).toBeVisible();

    // An open PR opens its Ticket's row.
    await open("21");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText("PR#21Add the login form · #11 Login form");
    await page.keyboard.press("Enter");
    await expect(
      page
        .getByRole("region", { name: "Feature Login" })
        .getByRole("listitem")
        .filter({ hasText: "Login form" }),
    ).toHaveClass(/sw-flash/);

    // A Ticket in another Project opens that Project's board at its row.
    await open("2");
    await options.nth(1).click();
    await expect(
      page
        .getByRole("region", { name: "Feature Tags" })
        .getByRole("listitem")
        .filter({ hasText: "Tag colour" }),
    ).toHaveClass(/sw-flash/);
  } finally {
    await fh.dispose();
  }
});
