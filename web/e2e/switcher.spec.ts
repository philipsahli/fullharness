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
    // The closed #13 comes after the pause, dimmed (#255).
    await search.fill("13");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toHaveClass(/closed/);
    await expect(options.first()).toContainText("Shop logout · Login · Closed");

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

// Closed and merged items come dimmed, once typing stops (#255). A shipped
// Spec opens its Feature page, a closed Ticket its row; what fh doesn't
// show (a closed issue off the board, a merged PR) opens on GitHub.
test("⌘K lists closed and merged items dimmed and opens each where fh shows it", async ({
  page,
  context,
}) => {
  const PATH = fakeGhPath(
    [
      { number: 10, title: "Login", state: "CLOSED", labels: ["spec"], subIssues: [11] },
      { number: 11, title: "Login form", state: "CLOSED", labels: ["ready-for-agent"], parent: 10 },
      { number: 20, title: "Search", labels: ["ready-for-agent"], subIssues: [21] },
      { number: 21, title: "Search box", labels: ["ready-for-agent"], parent: 20 },
    ],
    {
      prs: [{ number: 12, title: "Add the login form", headRefName: "11-form", state: "MERGED" }],
      numbered: {
        77: {
          title: "Board read fails behind a proxy",
          state: "closed",
          url: "https://github.com/someone/shop/issues/77",
        },
      },
    },
  );
  // A pick that opens GitHub opens a tab; it never leaves this machine.
  await context.route("https://github.com/**", (r) => r.fulfill({ body: "GitHub" }));
  const fh = await Fh.start({ env: { PATH } });
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
    await page.getByRole("tab", { name: "Software factory" }).click();

    const dialog = page.getByRole("dialog", { name: "Switcher" });
    const search = dialog.getByLabel("Find a Session, Ticket or number");
    const options = dialog.getByRole("option");
    const open = async (q: string) => {
      await page.keyboard.press("ControlOrMeta+k");
      await search.fill(q);
    };

    // A closed issue no board has: GitHub's answer, dimmed.
    await open("#77");
    await expect(dialog.getByText("Looking up closed and merged #77 on GitHub…")).toBeVisible();
    await expect(options).toHaveCount(1);
    await expect(options.first()).toHaveClass(/closed/);
    await expect(options.first()).toContainText("Issue#77Board read fails behind a proxy · Closed");
    await expect(options.first()).toContainText("GitHub ↗");
    await expect(dialog.locator(".pal-foot")).toContainText("cached for 5 min · 1 closed");
    const tab = page.waitForEvent("popup");
    await page.keyboard.press("Enter");
    expect((await tab).url()).toBe("https://github.com/someone/shop/issues/77");
    await (await tab).close();

    // owner/repo#N with a type word works on a looked-up item too (#256).
    await open("Issue someone/shop#77");
    await expect(
      dialog.getByText("Looking up closed and merged someone/shop#77 on GitHub…"),
    ).toBeVisible();
    await expect(options).toHaveCount(1);
    await expect(options.first()).toHaveClass(/closed/);
    await expect(options.first()).toContainText("Issue#77Board read fails behind a proxy");
    await search.fill("PR someone/shop#77");
    await expect(
      dialog.getByText("Nothing numbered someone/shop#77 in your Projects."),
    ).toBeVisible();
    await page.keyboard.press("Escape");

    // A number nobody has.
    await open("404");
    await expect(dialog.getByText("Nothing numbered 404 in your Projects.")).toBeVisible();
    await page.keyboard.press("Escape");

    // A shipped Spec opens its Feature page.
    await open("10");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toHaveClass(/closed/);
    await expect(options.first()).toContainText("Spec#10Login · Shipped");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("region", { name: "Feature Login" })).toBeVisible();

    // A closed Ticket opens its Feature at its row.
    await open("11");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText("Ticket#11Login form · Login · Closed");
    await page.keyboard.press("Enter");
    await expect(
      page
        .getByRole("region", { name: "Feature Login" })
        .getByRole("listitem")
        .filter({ hasText: "Login form" }),
    ).toHaveClass(/sw-flash/);

    // A merged PR of a closed Ticket, dimmed, with its Ticket.
    await open("12");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText(
      "PR#12Add the login form · #11 Login form · Merged",
    );
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
    await expect(page.locator(".sw-flash")).toHaveCount(0);

    // A pasted PR link goes to that one PR, and opens its Ticket's row
    // (#256).
    await open("https://github.com/someone/shop/pull/21/files");
    await expect(options).toHaveCount(1);
    await expect(dialog.locator("li.pgroup")).toHaveText(["shop· this Project"]);
    await expect(options.first()).toContainText("PR#21Add the login form · #11 Login form");
    await page.keyboard.press("Enter");
    await expect(
      page
        .getByRole("region", { name: "Feature Login" })
        .getByRole("listitem")
        .filter({ hasText: "Login form" }),
    ).toHaveClass(/sw-flash/);

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

    // A type word keeps that type; a repo that isn't a Project opens on
    // GitHub (#256).
    await open("Ticket 2");
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText("Ticket#02Tag colour");
    await search.fill("other/repo#5");
    await expect(options).toHaveCount(1);
    await expect(dialog.locator("li.pgroup")).toHaveCount(0);
    await expect(options.first()).toContainText("Issue#5other/repo · not one of your Projects");
    await expect(options.first()).toContainText("GitHub ↗");
    await page.keyboard.press("Escape");

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
