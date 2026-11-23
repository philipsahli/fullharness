import { test as base, expect, type Locator, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { Fh, runFh } from "./fh";
import { tempFolder, tempRepo, tempRepoWith } from "./repo";

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

test("typing / lists the Agent's slash commands; the model and usage show in the header", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      models: true,
      onStart: [{ commands: ["grill-me", "mattpocock-skills:tdd"] }],
      prompts: { hello: [{ usage: { used: 53000, size: 200000, cost: 0.42, currency: "USD" } }] },
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
    await expect(conversation.getByText("context 53k / 200k (27%) · $0.42")).toBeVisible();

    const message = conversation.getByLabel("Message");
    await message.fill("/");
    const list = conversation.getByRole("listbox", { name: "Slash commands" });
    await expect(list.getByRole("option")).toHaveCount(2);
    await message.fill("/td"); // a plugin's skill matches by its own name
    await expect(list.getByRole("option")).toHaveText([
      "/mattpocock-skills:tdd" + "fake mattpocock-skills:tdd",
    ]);
    await message.press("Enter");
    await expect(message).toHaveValue("/mattpocock-skills:tdd ");
    await expect(list).toHaveCount(0);

    await conversation.getByLabel("Model").selectOption("opus");
    await expect(conversation.getByLabel("Model")).toHaveValue("opus");
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

test("a Session that starts Waiting while you look elsewhere notifies, and the click opens it", async ({
  page,
}) => {
  // Record notifications instead of showing them; clicking one runs onclick.
  await page.addInitScript(() => {
    const shown: { title: string; tag?: string; click: () => void }[] = [];
    (window as unknown as { shown: typeof shown }).shown = shown;
    class FakeNotification {
      static permission = "granted";
      static requestPermission = async () => "granted";
      onclick: (() => void) | null = null;
      constructor(title: string, opts?: { tag?: string }) {
        shown.push({ title, tag: opts?.tag, click: () => this.onclick?.() });
      }
      close() {}
    }
    (window as unknown as { Notification: unknown }).Notification = FakeNotification;
  });
  const fh = await Fh.start({
    agentScript: { prompts: { slow: [{ sleepMs: 1500 }, { say: "done" }] } },
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
    await form.getByLabel("Starting prompt (optional)").fill("slow");
    await form.getByRole("button", { name: "Start Session" }).click();
    await expect(page.getByRole("region", { name: /^Session / })).toBeVisible();
    await page.getByRole("tab", { name: /Inbox/ }).click(); // look elsewhere

    await expect
      .poll(() => page.evaluate(() => (window as unknown as { shown: unknown[] }).shown.length), {
        timeout: 10_000,
      })
      .toBe(1);
    await page.evaluate(() =>
      (window as unknown as { shown: { click: () => void }[] }).shown[0].click(),
    );

    await expect(page.getByRole("region", { name: /^Session / })).toBeVisible();
  } finally {
    await fh.dispose();
  }
});

test("after a restart a Session can be reopened with its history, then closed", async ({
  page,
}) => {
  const fh = await Fh.start({
    dev: true, // the tab reloads across the restart instead of stopping
    agentScript: { history: [{ user: "fix the login" }, { agent: "Done, login works." }] },
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
    await form.getByRole("button", { name: "Start Session" }).click();
    const conversation = page.getByRole("region", { name: /^Session / });
    await expect(conversation.locator(".mhead .state")).toHaveText("Idle");

    await fh.restart();

    const row = page
      .getByRole("region", { name: "Sessions" })
      .getByRole("button", { name: /main/ });
    await expect(row).toContainText("fh quit", { timeout: 10_000 });
    await row.click();
    await conversation.getByRole("button", { name: "Reopen" }).click();
    await expect(conversation.getByText("Done, login works.")).toBeVisible();
    await expect(conversation.locator(".mhead .state")).toHaveText("Idle");

    page.once("dialog", (d) => void d.accept()); // confirm stopping the live Agent
    await conversation.getByRole("button", { name: "Close" }).click();
    await expect(page.getByText("No Sessions yet.")).toBeVisible();
  } finally {
    await fh.dispose();
  }
});

test("the Software factory shows a local markdown Feature at its Stage, with its Tickets", async ({
  page,
  fh,
}) => {
  const ticket = (n: string, title: string, blockedBy: string) =>
    `# ${n}: ${title}\n\n**Blocked by:** ${blockedBy}\n\n**Status:** ready-for-agent\n`;
  const repo = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n",
    ".scratch/login/issues/01-form.md": ticket("01", "Login form", "None"),
    ".scratch/login/issues/02-cookie.md": ticket("02", "Session cookie", "01"),
  });
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");

  await page.getByRole("tab", { name: "Software factory" }).click();
  const tickets = page.locator('.column[aria-label="Tickets"]');
  await expect(tickets.getByRole("button", { name: /Login/ })).toContainText("2 tickets");
  await tickets.getByRole("button", { name: /Login/ }).click();

  const detail = page.getByRole("region", { name: "Feature Login" });
  await expect(detail.getByRole("listitem").filter({ hasText: "Login form" })).toContainText(
    "Startable",
  );
  await expect(detail.getByRole("listitem").filter({ hasText: "Session cookie" })).toContainText(
    "blocked by 01",
  );
});

test("a Worktree fh created is not labelled an Agent worktree", async ({ page }) => {
  // The fake agent stands in for Claude Code, which CI doesn't have.
  const fh = await Fh.start({ agentScript: {} });
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
    await form.getByLabel("New branch name").fill("7-login");
    await form.getByRole("button", { name: "Start Session" }).click();

    const row = projects.locator(".wt").filter({ hasText: "7-login" });
    await expect(row).toBeVisible();
    await expect(row).not.toContainText("Agent worktree");
  } finally {
    await fh.dispose();
  }
});

test("Start Session on a Startable ticket runs the implement skill, linked to the Ticket", async ({
  page,
}) => {
  const ref = ".scratch/login/issues/01-form.md";
  const fh = await Fh.start({
    agentScript: {
      onStart: [{ commands: ["mattpocock-skills:implement"] }],
      prompts: { [`/implement ${ref}`]: [{ say: "implementing 01" }] },
    },
  });
  try {
    const repo = tempRepoWith("shop", {
      ".scratch/login/spec.md": "# Login\n",
      ".scratch/login/issues/01-form.md":
        "# 01: Login form\n\n**Blocked by:** None\n\n**Status:** ready-for-agent\n",
    });
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    await page.getByRole("button", { name: /Login/ }).click();
    const detail = page.getByRole("region", { name: "Feature Login" });
    await detail.getByRole("button", { name: "Start Session" }).click();

    // No Session has reported Claude Code's commands yet: the plain skill
    // name, with a warning.
    const form = page.getByRole("form", { name: `Start Session on ${ref}` });
    await expect(form.getByLabel("New branch name")).toHaveValue("01-form");
    await expect(form.getByRole("textbox", { name: /Starting prompt/ })).toHaveValue(
      `/implement ${ref}`,
    );
    await expect(form).toContainText("fh doesn't know Claude Code's slash commands yet");
    await form.getByRole("button", { name: "Start Session" }).click();

    const conversation = page.getByRole("region", { name: "Session 01-form" });
    await expect(conversation).toContainText("implementing 01");
    await expect(conversation).toContainText("started from Ticket 01 of Spec login");

    // Now fh knows Claude Code's commands: it has implement, not implement-spec.
    await page.getByRole("tab", { name: "Software factory" }).click();
    const board = page.getByRole("region", { name: "Software factory" });
    await board.getByRole("button", { name: "Refresh" }).click();
    await expect(board.locator('.column[aria-label="Implementing"]')).toContainText("Login");
    await board.getByRole("button", { name: /Login/ }).click();
    await detail.getByRole("button", { name: "Implement spec" }).click();
    const spec = page.getByRole("form", { name: /Implement spec/ });
    await expect(spec).toContainText("`/implement-spec` isn't installed for Claude Code");
    // Its new Worktree's branch is the integration branch the skill works on.
    await expect(spec.getByLabel("New branch name")).toHaveValue("login");
    await expect(spec.getByRole("textbox", { name: /Starting prompt/ })).toHaveValue(
      "/implement-spec .scratch/login/spec.md (use this Worktree's branch, login, as the integration branch)",
    );
  } finally {
    await fh.dispose();
  }
});

test("Grill new idea starts a named Session, and its Spec links in one click", async ({ page }) => {
  const fh = await Fh.start({
    agentScript: {},
  });
  try {
    const repo = tempRepoWith("shop", { ".scratch/login/spec.md": "# Login\n" });
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    await page.getByRole("button", { name: "Grill new idea" }).click();
    const form = page.getByRole("form", { name: "Grill new idea" });
    await form.getByLabel("Idea name").fill("Dark mode");
    await expect(form.getByRole("textbox", { name: /Starting prompt/ })).toHaveValue(
      "/grill-with-docs ",
    );
    await form.getByRole("button", { name: "Start Session" }).click();

    // The Session runs in a new Worktree on idea/dark-mode, its first title.
    const conversation = page.getByRole("region", { name: "Session idea/dark-mode" });
    await expect(conversation).toContainText("/grill-with-docs");
    await expect(conversation).toContainText('Idea "Dark mode"');

    // The grilling's /to-spec writes a Spec; the Idea offers to link it.
    mkdirSync(path.join(repo, ".scratch/dark-mode"), { recursive: true });
    writeFileSync(path.join(repo, ".scratch/dark-mode/spec.md"), "# Dark mode\n");
    await page.getByRole("tab", { name: "Software factory" }).click();
    const board = page.getByRole("region", { name: "Software factory" });
    const ideas = board.locator('.column[aria-label="Idea"]');
    await expect(ideas).toContainText("Dark mode");
    await ideas.getByRole("button", { name: /Link .scratch\/dark-mode\/spec.md/ }).click();

    await expect(ideas).not.toContainText("Dark mode");
    await board
      .locator('.column[aria-label="Spec"]')
      .getByRole("button", { name: /Dark mode/ })
      .click();
    await expect(page.getByRole("region", { name: "Feature Dark mode" })).toContainText(
      'from Idea "Dark mode"',
    );
  } finally {
    await fh.dispose();
  }
});

// startSession adds a repo as a Project and starts a Session in its main
// tree with prompt, returning the Session's conversation region.
async function startSession(page: Page, fh: Fh, prompt: string): Promise<Locator> {
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
  await form.getByLabel("Starting prompt (optional)").fill(prompt);
  await form.getByRole("button", { name: "Start Session" }).click();
  return page.getByRole("region", { name: /^Session / });
}

test("a streamed Agent reply renders as Markdown while it arrives; the user's text stays as typed", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      default: [
        { say: "## Plan\n\n| step | done |\n|---" },
        { say: "|---|\n| build | yes |\n\n- [x] tests\n- run `make`\n\n" },
        { sleepMs: 1500 },
        { say: "```go\nfunc main() {" }, // the fence never closes
      ],
    },
  });
  try {
    const typed = "/plan **not bold**\n- not a list";
    const conversation = await startSession(page, fh, typed);
    const reply = conversation.locator(".msg.agent");

    // Before the last chunk: the table is already a table.
    await expect(reply.getByRole("heading", { name: "Plan" })).toBeVisible();
    await expect(reply.getByRole("cell", { name: "build" })).toBeVisible();
    await expect(reply.getByRole("checkbox")).toBeChecked();
    await expect(reply.locator("code", { hasText: "make" })).toBeVisible();
    await expect(reply.locator("pre")).toHaveCount(0);

    await expect(reply.locator("pre code")).toHaveText("func main() {");
    await expect(reply).not.toContainText("```");

    const mine = conversation.locator(".msg.user");
    expect(await mine.evaluate((el) => (el as HTMLElement).innerText)).toBe(typed);
    await expect(mine.locator("*")).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});

test("an Agent reply can't run script or load images, and only http(s) links are links", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      default: [
        {
          say:
            "<script>window.pwned = 1</script>\n\n" +
            '<img src="x" onerror="window.pwned = 2">\n\n' +
            "[bad](javascript:window.pwned=3) [local](file:///etc/passwd) [docs](https://example.com/a)\n\n" +
            "![chart](https://example.com/c.png) ![pic](javascript:window.pwned=4)",
        },
      ],
    },
  });
  try {
    const conversation = await startSession(page, fh, "hi");
    const reply = conversation.locator(".msg.agent");
    await expect(reply.getByText("docs")).toBeVisible();

    await reply.getByText("bad").click();
    await expect(reply).toContainText("<script>window.pwned = 1</script>"); // shown, not run
    await expect(reply.locator("script, img")).toHaveCount(0);
    expect(await page.evaluate(() => "pwned" in window)).toBe(false);
    // An image would make the page fetch its URL unasked: it's a link instead.
    const chart = reply.getByRole("link", { name: "chart" });
    await expect(chart).toHaveAttribute("href", "https://example.com/c.png");
    await expect(reply.getByText("pic")).toBeVisible();
    await expect(reply.getByRole("link")).toHaveCount(2);
    await expect(reply.getByText("local")).toBeVisible();
    const docs = reply.getByRole("link", { name: "docs" });
    await expect(docs).toHaveAttribute("href", "https://example.com/a");
    await expect(docs).toHaveAttribute("target", "_blank");
    await expect(docs).toHaveAttribute("rel", "noopener");
  } finally {
    await fh.dispose();
  }
});

// numbered is n lines "line 1" to "line n".
function numbered(n: number): string {
  return Array.from({ length: n }, (_, i) => `line ${i + 1}\n`).join("");
}

test("an edit tool card shows +N −M collapsed and its diff opened; a failed call opens itself; a cut diff shows in full on request", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      default: [
        {
          tool: {
            id: "t1",
            title: "Edit notes.md",
            kind: "edit",
            status: "completed",
            path: "notes.md",
            diffOld: "a\nb\nc\n",
            diffNew: "a\nB\nc\nd\n",
          },
        },
        {
          tool: { id: "t2", title: "go vet", kind: "execute", status: "failed", text: "vet: boom" },
        },
        {
          tool: {
            id: "t3",
            title: "Write big.txt",
            kind: "edit",
            status: "in_progress",
            path: "big.txt",
            diffNew: numbered(400),
          },
        },
        { say: "done" },
        { sleepMs: 3000 },
        // Re-sends the card with the same diffs: the full diff must stay.
        { toolUpdate: { id: "t3", status: "completed" } },
      ],
    },
  });
  try {
    const conversation = await startSession(page, fh, "edit");
    await expect(conversation.locator(".msg.agent")).toHaveText("done");

    const edit = conversation.locator("details.toolcard", { hasText: "Edit notes.md" });
    await expect(edit.locator("summary")).toContainText("+2 −1");
    await expect(edit).not.toHaveAttribute("open");
    await edit.locator("summary").click();
    const diff = edit.getByRole("group", { name: "Diff of notes.md" });
    await expect(diff.locator(".hunk")).toHaveText("@@ -1,3 +1,4 @@");
    await expect(diff.locator(".del")).toHaveText(["-b"]);
    await expect(diff.locator(".add")).toHaveText(["+B", "+d"]);
    await expect(diff.locator(".ctx")).toHaveText([" a", " c"]);

    const failed = conversation.locator("details.toolcard", { hasText: "go vet" });
    await expect(failed).toHaveAttribute("open");
    await expect(failed).toContainText("vet: boom");

    const big = conversation.locator("details.toolcard", { hasText: "Write big.txt" });
    await expect(big.locator("summary")).toContainText("+400 −0");
    await big.locator("summary").click();
    const bigDiff = big.getByRole("group", { name: "Diff of big.txt" });
    await expect(bigDiff.locator(".add")).toHaveCount(300);
    await expect(bigDiff).toContainText("Showing 300 of 400 changed lines");
    await big.getByRole("button", { name: "Show full diff" }).click();
    await expect(bigDiff.locator(".add")).toHaveCount(400);
    await expect(bigDiff.locator(".add").last()).toHaveText("+line 400");
    await expect(big.getByRole("button", { name: "Show full diff" })).toHaveCount(0);
    await expect(big.locator(".status")).toHaveText("completed");
    await expect(bigDiff.locator(".add")).toHaveCount(400);
  } finally {
    await fh.dispose();
  }
});

test("a permission request to edit a file shows its diff in the Session and in the Inbox", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      default: [
        {
          permission: {
            id: "t1",
            title: "Edit main.go",
            kind: "edit",
            path: "main.go",
            diffOld: "package main\nfunc old() {}\n",
            diffNew: "package main\nfunc new() {}\n",
          },
        },
      ],
    },
  });
  try {
    const conversation = await startSession(page, fh, "edit");
    const expectDiff = async (card: Locator) => {
      await expect(card).toContainText("+1 −1");
      const diff = card.getByRole("group", { name: "Diff of main.go" });
      await expect(diff.locator(".del")).toHaveText(["-func old() {}"]);
      await expect(diff.locator(".add")).toHaveText(["+func new() {}"]);
    };

    await expectDiff(conversation.getByRole("group", { name: "Permission: Edit main.go" }));
    await page.getByRole("tab", { name: /Inbox/ }).click();
    await expectDiff(
      page
        .getByRole("region", { name: "Inbox" })
        .getByRole("group", { name: "Permission: Edit main.go" }),
    );
  } finally {
    await fh.dispose();
  }
});

test("fenced code and a read file are highlighted by language; an unknown language is plain", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      default: [
        {
          say:
            '```go\nfunc main() { fmt.Println("hi") }\n```\n\n' +
            "```ts\nconst n: number = 1;\n```\n\n" +
            "```cobol\nDISPLAY 'HI'.\n```",
        },
        {
          tool: {
            id: "r1",
            title: "Read main.go",
            kind: "read",
            status: "completed",
            path: "/w/main.go",
            // As Claude Code's adapter sends a read: the Agent's own line
            // numbers, in a fence.
            text: "```\n1\tpackage main\n2\t\n3\t// entry\n4\tfunc main() {}\n```",
          },
        },
      ],
    },
  });
  try {
    const conversation = await startSession(page, fh, "show me");
    const reply = conversation.locator(".msg.agent");
    const keyword = (code: string) => reply.locator(`pre code.language-${code} .hljs-keyword`);

    await expect(keyword("go").first()).toHaveText("func");
    await expect(reply.locator("pre code.language-go .hljs-string")).toHaveText('"hi"');
    await expect(keyword("ts").first()).toHaveText("const");
    const plain = reply.locator("pre code.language-cobol");
    await expect(plain).toHaveText("DISPLAY 'HI'.");
    await expect(plain.locator("*")).toHaveCount(0);

    // A keyword is coloured apart from the code around it.
    const colour = (l: typeof plain) => l.evaluate((el) => getComputedStyle(el).color);
    expect(await colour(keyword("go").first())).not.toBe(await colour(plain));

    const card = conversation.locator(".toolcard", { hasText: "Read main.go" });
    await card.locator("summary").click();
    await expect(card.locator("pre code .hljs-keyword").first()).toHaveText("package");
    await expect(card.locator("pre code .hljs-comment")).toHaveText("// entry");
    await expect(card.locator("pre code")).toHaveText(
      /^1\tpackage main\n.*\n4\tfunc main\(\) \{\}$/s,
    );
  } finally {
    await fh.dispose();
  }
});
