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

test("a New Session in a Project's main tree streams the Agent's reply and its tool call", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      prompts: {
        hello: [
          { say: "Hi " },
          { say: "there" },
          { tool: { id: "t1", title: "ls", kind: "execute", status: "pending" } },
          { toolUpdate: { id: "t1", status: "completed", rawOutput: "README.md" } },
        ],
      },
    },
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(tempRepo("shop"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("button", { name: "+ New Session" }).click();
    const form = page.getByRole("form", { name: "New Session" });
    await form.getByRole("radio", { name: /main/ }).check();
    await form.getByLabel("Starting prompt (optional)").fill("hello");
    await form.getByRole("button", { name: "Start Session" }).click();

    const conversation = page.getByRole("region", { name: /^Session / });
    await expect(conversation.getByText("Hi there")).toBeVisible();
    await expect(conversation.locator(".state")).toHaveText("Idle");
    await conversation.getByText("ls", { exact: true }).click(); // expand the tool card
    await expect(conversation.getByText("README.md")).toBeVisible();
    await expect(conversation.getByText("completed")).toBeVisible();
    await expect(conversation.getByRole("button", { name: "Send" })).toBeDisabled(); // empty composer
  } finally {
    await fh.dispose();
  }
});

test("a permission request answered in the Inbox is answered in its Session too", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      default: [
        {
          permission: { id: "t1", title: "rm -rf build", kind: "execute", command: "rm -rf build" },
        },
      ],
    },
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(tempRepo("shop"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    await page.getByRole("button", { name: "+ New Session" }).click();
    const form = page.getByRole("form", { name: "New Session" });
    await form.getByRole("radio", { name: /main/ }).check();
    await form.getByLabel("Starting prompt (optional)").fill("clean up");
    await form.getByRole("button", { name: "Start Session" }).click();

    const conversation = page.getByRole("region", { name: /^Session / });
    const card = conversation.getByRole("group", { name: "Permission: rm -rf build" });
    await expect(card).toContainText("rm -rf build");
    await expect(conversation.locator(".mhead .state")).toHaveText("Waiting");
    const inboxTab = page.getByRole("tab", { name: /Inbox/ });
    await expect(inboxTab).toContainText("1");

    await inboxTab.click();
    await page
      .getByRole("region", { name: "Inbox" })
      .getByRole("button", { name: "Allow", exact: true })
      .click();
    await expect(inboxTab).toContainText("0");

    await page.getByRole("tab", { name: "Sessions" }).click();
    await page.getByRole("button", { name: /main/ }).first().click();
    await expect(card).toContainText("Allowed");
    await expect(conversation.getByText("permission=allow-once")).toBeVisible();
  } finally {
    await fh.dispose();
  }
});

test("⌘K finds a Session by Project and opens it", async ({ page }) => {
  const fh = await Fh.start({ agentScript: { default: [{ say: "hello" }] } });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(tempRepo("billing"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    await page.getByRole("button", { name: "+ New Session" }).click();
    const form = page.getByRole("form", { name: "New Session" });
    await form.getByRole("radio", { name: /main/ }).check();
    await form.getByRole("button", { name: "Start Session" }).click();
    await expect(page.getByRole("region", { name: /^Session / })).toBeVisible();
    await page.getByRole("tab", { name: /Inbox/ }).click(); // leave the Session

    await page.keyboard.press("ControlOrMeta+k");
    const dialog = page.getByRole("dialog", { name: "Jump to a Session" });
    await dialog.getByLabel("Search Sessions").fill("billing claude");
    await expect(dialog.getByRole("option")).toHaveCount(1);
    await page.keyboard.press("Enter");

    await expect(dialog).toBeHidden();
    await expect(page.getByRole("region", { name: /^Session / })).toBeVisible();
  } finally {
    await fh.dispose();
  }
});
