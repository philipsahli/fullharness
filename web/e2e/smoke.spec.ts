import { test as base, expect, type Locator, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { Fh, runFh } from "./fh";
import { fakeGhPath } from "./gh";
import { tempFolder, tempRepo, tempRepoWith, withOrigin, withRemoteBranch } from "./repo";

// Browser smoke tests (ADR 0007): the UI logic no Go test can reach.
const test = base.extend<{ fh: Fh }>({
  // eslint-disable-next-line no-empty-pattern -- Playwright fixtures must destructure their first argument
  fh: async ({}, use) => {
    // A fake gh with no PRs: a Project on GitHub makes fh read its PRs, and
    // the tests must not reach GitHub through the developer's own gh.
    const fh = await Fh.start({ ghPRs: [] });
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
  // Rail actions are buttons, not links (#166).
  await expect(projects.getByRole("button", { name: "Refresh" })).toBeVisible();
  await expect(projects.getByRole("button", { name: "Remove shop" })).toBeVisible();
  // A Worktree's dot names its Sessions' state; with none it says so (#167).
  await expect(shop.getByRole("img", { name: "No Session" })).toBeVisible();
});

test("a Project on GitHub links its name to the repo; one without a GitHub remote is plain text", async ({
  page,
  fh,
}) => {
  await page.goto(fh.launchURL);
  const projects = page.getByRole("region", { name: "Projects" });
  const add = async (repo: string) => {
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
  };

  await add(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
  await add(withOrigin(tempRepo("tools"), "git@gitlab.com:someone/tools.git"));

  const shop = projects.getByRole("link", { name: "shop", exact: true });
  await expect(shop).toHaveAttribute("href", "https://github.com/someone/shop");
  await expect(shop).toHaveAttribute("target", "_blank");
  await expect(shop).toHaveAttribute("rel", "noopener");
  await shop.focus();
  await expect(shop).toBeFocused(); // reachable with the keyboard
  await expect(projects.getByText("tools", { exact: true })).toBeVisible();
  await expect(projects.getByRole("link")).toHaveCount(1);
});

test("a Worktree branch origin has links to it on GitHub; one origin lacks is plain text", async ({
  page,
  fh,
}) => {
  await page.goto(fh.launchURL);
  const projects = page.getByRole("region", { name: "Projects" });
  const add = async (repo: string) => {
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
  };

  await add(
    withRemoteBranch(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"), "main"),
  );
  await add(withOrigin(tempRepo("tools"), "git@github.com:someone/tools.git"));

  const pushed = projects.getByRole("link", { name: "main", exact: true });
  await expect(pushed).toHaveAttribute("href", "https://github.com/someone/shop/tree/main");
  await expect(pushed).toHaveAttribute("target", "_blank");
  await expect(pushed).toHaveAttribute("rel", "noopener");
  await expect(pushed).toHaveCSS("text-overflow", "ellipsis"); // a long branch stays in its row
  // tools' main was never pushed: its name links, its branch doesn't.
  await expect(projects.getByRole("link", { name: "tools", exact: true })).toBeVisible();
  await expect(projects.getByRole("link", { name: "main", exact: true })).toHaveCount(1);
  await expect(projects.getByText("main", { exact: true })).toHaveCount(2);
});

test("the Autopilot switch is per Project and stays on after a reload", async ({ page, fh }) => {
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(tempRepo("shop"));
  await projects.getByRole("button", { name: "Add", exact: true }).click();

  const autopilot = projects.getByRole("checkbox", { name: "Autopilot" });
  await expect(autopilot).not.toBeChecked();
  // The box shows fh's setting, so it turns on once fh has saved it.
  await autopilot.click();
  await expect(autopilot).toBeChecked();

  const pick = projects.getByRole("combobox", { name: "Autopilot Agent" });
  await expect(pick).toHaveValue("claude");
  await pick.selectOption("copilot");
  await expect(pick).toHaveValue("copilot");

  await page.reload();
  const after = page.getByRole("region", { name: "Projects" });
  await expect(after.getByRole("checkbox", { name: "Autopilot" })).toBeChecked();
  await expect(after.getByRole("combobox", { name: "Autopilot Agent" })).toHaveValue("copilot");
});

// Autopilot status (#203, ADR-0013): the Feature card's badge follows fh's
// Autopilot status live, without a reload, and a fresh tab draws it from the
// snapshot. The path is the normal one through Approve spec: a Spec in Now
// waits for approval; once approved, Autopilot runs to-tickets
// (tracker.FeatureStep at StageSpec), whose turn here ends asking.
test("the Autopilot badge follows a Feature through Approve spec without a reload", async ({
  page,
  context,
}) => {
  const spec = (status: string) => `# Search\n\n**Roadmap:** now\n\n**Status:** ${status}\n`;
  const repo = tempRepoWith("shop", { ".scratch/search/spec.md": spec("needs-triage") });
  // The to-tickets turn runs long enough to see it, then ends asking.
  const fh = await Fh.start({
    ghPRs: [],
    agentScript: { default: [{ sleepMs: 3000 }, { say: "Which labels should the tickets get?" }] },
    verdict: "asking",
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    const column = page.locator('.column[aria-label="Spec"]');
    const card = column.getByRole("button", { name: /^Search/ });
    const badge = card.getByRole("img", { name: /^Autopilot status/ });
    // In Now, with Autopilot off for the Project: the card links to the
    // switch, which it focuses without turning it on.
    await expect(badge).toHaveAccessibleName("Autopilot status: Autopilot off");
    const toggle = projects.getByRole("checkbox", { name: "Autopilot" });
    await column.getByRole("button", { name: "Autopilot off for shop · Turn on" }).click();
    await expect(toggle).toBeFocused();
    await expect(toggle).not.toBeChecked();

    await toggle.click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Waiting at Approve spec");
    await expect(card).toHaveAttribute("data-ap", "waiting");
    await expect(column.getByRole("button", { name: /Turn on/ })).toHaveCount(0);

    // Approve the Spec with Mark ready-for-agent. Autopilot picks it up on
    // its next look; stepping at once after Approve is #199 (ADR 0014), so
    // here the switch, which steps the Project when turned on, does it.
    await card.click();
    await page
      .getByRole("region", { name: "Feature Search" })
      .getByRole("button", { name: "Mark ready-for-agent" })
      .click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Idle");
    await toggle.click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Autopilot off");
    await toggle.click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Running /to-tickets");
    await expect(badge).toHaveClass(/changed/);

    await expect(badge).toHaveAccessibleName("Autopilot status: Waiting at Agent asking", {
      timeout: 10_000,
    });

    const fresh = await context.newPage();
    await fresh.goto(fh.origin);
    await fresh.getByRole("tab", { name: "Software factory" }).click();
    await expect(
      fresh.locator('.column[aria-label="Spec"]').getByRole("img", { name: /^Autopilot status/ }),
    ).toHaveAccessibleName("Autopilot status: Waiting at Agent asking");
  } finally {
    await fh.dispose();
  }
});

// The live pulse (#205): a running Feature's card shows the tool call its
// Autopilot turn is on, follows the Agent to the next one without a
// reload, and drops the line when the turn ends.
test("a running Feature's card follows its Agent's tool calls live", async ({ page }) => {
  const repo = tempRepoWith("shop", {
    ".scratch/search/spec.md": "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
  });
  const fh = await Fh.start({
    ghPRs: [],
    agentScript: {
      default: [
        {
          tool: {
            id: "t1",
            title: "Read internal/acp/client.go",
            kind: "read",
            status: "in_progress",
          },
        },
        { sleepMs: 3000 },
        {
          tool: { id: "t2", title: "Edit web/src/Board.tsx", kind: "edit", status: "in_progress" },
        },
        { sleepMs: 3000 },
        { say: "Which labels should the tickets get?" },
      ],
    },
    verdict: "asking",
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    await page.getByRole("tab", { name: "Software factory" }).click();
    const card = page
      .locator('.column[aria-label="Spec"]')
      .getByRole("button", { name: /^Search/ });
    const live = card.locator(".ap-live");

    await projects.getByRole("checkbox", { name: "Autopilot" }).click();
    await expect(live).toHaveText("Read internal/acp/client.go");
    await expect(live).toHaveText("Edit web/src/Board.tsx");

    await expect(card.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      "Autopilot status: Waiting at Agent asking",
      { timeout: 10_000 },
    );
    await expect(live).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});

// Leaving Now (#204, ADR-0013): a Feature moved out of Now mid-turn keeps
// running, marked "Session running outside Now", until its turn ends.
test("a Feature moved out of Now mid-turn shows Session running outside Now until the turn ends", async ({
  page,
}) => {
  const spec = (group: string) =>
    `# Search\n\n**Roadmap:** ${group}\n\n**Status:** ready-for-agent\n`;
  const repo = tempRepoWith("shop", { ".scratch/search/spec.md": spec("now") });
  const fh = await Fh.start({
    ghPRs: [],
    agentScript: { default: [{ sleepMs: 4000 }, { say: "Which labels should the tickets get?" }] },
    verdict: "asking",
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    const wrap = page.locator('.column[aria-label="Spec"] .fcard-wrap', { hasText: "Search" });
    const badge = wrap.getByRole("img", { name: /^Autopilot status/ });
    const mark = wrap.getByText("Session running outside Now");
    const toggle = projects.getByRole("checkbox", { name: "Autopilot" });
    await toggle.click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Running /to-tickets");
    await expect(mark).toHaveCount(0);

    // The user moves Search to Next mid-turn. fh notices a tracker change
    // on its next look; switching Autopilot off and on looks at once.
    writeFileSync(path.join(repo, ".scratch/search/spec.md"), spec("next"));
    await toggle.click();
    await toggle.click();
    await expect(mark).toBeVisible();
    await expect(badge).toHaveAccessibleName("Autopilot status: Running /to-tickets");

    // The turn ends: the Feature is outside Now and nothing runs, so the
    // card is quiet again.
    await expect(mark).toHaveCount(0, { timeout: 10_000 });
    await expect(badge).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
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
    // The header names the Agent in its chip (#167).
    await expect(conversation.getByText("Claude Code", { exact: true })).toBeVisible();
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

test("the composer's aria-activedescendant names the picked slash command while the list shows", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: { onStart: [{ commands: ["grill-me", "review", "tdd"] }] },
  });
  try {
    const conversation = await startSession(page, fh);

    const message = conversation.getByLabel("Message");
    const list = conversation.getByRole("listbox", { name: "Slash commands" });
    const options = list.getByRole("option");
    await expect(message).not.toHaveAttribute("aria-activedescendant");
    await message.fill("/");
    await expect(options).toHaveCount(3);

    // The picked option is the one the textarea points at.
    const pointsAt = async (n: number) => {
      await expect(options.nth(n)).toHaveAttribute("aria-selected", "true");
      const id = await options.nth(n).getAttribute("id");
      expect(id).toBeTruthy();
      await expect(message).toHaveAttribute("aria-activedescendant", id!);
    };
    await pointsAt(0);
    await message.press("ArrowDown");
    await pointsAt(1);
    await message.press("ArrowDown");
    await pointsAt(2);
    await message.press("ArrowDown"); // wraps
    await pointsAt(0);
    await message.press("ArrowUp"); // wraps back
    await pointsAt(2);

    await message.press("Escape");
    await expect(list).toHaveCount(0);
    await expect(message).not.toHaveAttribute("aria-activedescendant");

    await message.fill("/re");
    await pointsAt(0);
    await message.press("Enter"); // picking a command hides the list
    await expect(message).toHaveValue("/review ");
    await expect(list).toHaveCount(0);
    await expect(message).not.toHaveAttribute("aria-activedescendant");
  } finally {
    await fh.dispose();
  }
});

// An Agent may list a command name twice; each option still gets its own id,
// a valid ID reference with no whitespace, so aria-activedescendant names
// one row.
test("slash command options have unique ids without whitespace when a name repeats", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: { onStart: [{ commands: ["review", "review", "tdd"] }] },
  });
  try {
    const conversation = await startSession(page, fh);
    const message = conversation.getByLabel("Message");
    const options = conversation
      .getByRole("listbox", { name: "Slash commands" })
      .getByRole("option");
    await message.fill("/");
    await expect(options).toHaveCount(3);
    const ids = await options.evaluateAll((els) => els.map((el) => el.id));
    expect(new Set(ids).size).toBe(3);
    for (const id of ids) expect(id).toMatch(/^\S+$/);
    await message.press("ArrowDown");
    await expect(message).toHaveAttribute("aria-activedescendant", ids[1]);
  } finally {
    await fh.dispose();
  }
});

test("a permission request answered in the Inbox is answered in its Session too", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      prompts: { quit: [{ exit: 1 }] },
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
    // The Worktree's dot follows its Session (#167).
    const mainDot = projects.locator(".wt").filter({ hasText: "main tree" }).getByRole("img");
    await expect(mainDot).toHaveAccessibleName("Waiting");
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
    await expect(mainDot).toHaveAccessibleName("Idle");

    // An Exited Session frees its Worktree for a new one; the dot shows the
    // most urgent of the two: Waiting beats Exited.
    await conversation.getByLabel("Message").fill("quit");
    await conversation.getByRole("button", { name: "Send" }).click();
    await expect(conversation.locator(".mhead .state")).toHaveText("Exited");
    await expect(mainDot).toHaveAccessibleName("Exited");
    const status = await page.evaluate(async () => {
      const state = await (await fetch("/api/state")).json();
      const s = state.sessions[0];
      const r = await fetch("/api/sessions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          project: s.project,
          worktree: s.worktree,
          agent: s.agent,
          prompt: "clean up",
        }),
      });
      return r.status;
    });
    expect(status).toBe(201);
    await expect(mainDot).toHaveAccessibleName("Waiting");
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
    const dialog = page.getByRole("dialog", { name: "Switcher" });
    await dialog.getByLabel("Find a Session or Ticket").fill("billing claude");
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
    // No Status line: a Ticket without a label (#167).
    ".scratch/login/issues/02-cookie.md": "# 02: Session cookie\n\n**Blocked by:** 01\n",
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
  // Each Ticket shows its Stage; a Startable one adds a Startable marker
  // beside it, and a Ticket without a label shows none (#167).
  const form01 = detail.getByRole("listitem").filter({ hasText: "Login form" });
  await expect(form01).toContainText("ready-for-agent");
  await expect(form01.getByText("Tickets", { exact: true })).toBeVisible();
  await expect(form01.getByText("Startable", { exact: true })).toBeVisible();
  const cookie02 = detail.getByRole("listitem").filter({ hasText: "Session cookie" });
  await expect(cookie02.getByText("Tickets", { exact: true })).toBeVisible();
  await expect(cookie02).not.toContainText("Startable");
  await expect(cookie02).not.toContainText("no label");
  await expect(detail.getByRole("listitem").filter({ hasText: "Session cookie" })).toContainText(
    "blocked by #01",
  );
  // The next-step rule's button shows on the Startable Ticket only.
  await expect(
    detail.getByRole("listitem").filter({ hasText: "Login form" }).getByRole("button"),
  ).toHaveText("Start Session");
  await expect(
    detail.getByRole("listitem").filter({ hasText: "Session cookie" }).getByRole("button"),
  ).toHaveCount(0);
  // The Spec and Ticket numbers open their files in VS Code, in place: no
  // target, unlike a GitHub link (#102). Blockers and titles stay text.
  const spec = detail.getByRole("link", { name: ".scratch/login/spec.md" });
  await expect(spec).toHaveAttribute("href", `vscode://file${repo}/.scratch/login/spec.md`);
  await expect(spec).not.toHaveAttribute("target", /.*/);
  const form = detail
    .getByRole("listitem")
    .filter({ hasText: "Login form" })
    .getByRole("link", { name: "#01", exact: true });
  await expect(form).toHaveAttribute(
    "href",
    `vscode://file${repo}/.scratch/login/issues/01-form.md`,
  );
  await expect(form).not.toHaveAttribute("target", /.*/);
  await expect(form).not.toHaveAttribute("rel", /.*/);
  await expect(detail.getByRole("link")).toHaveCount(3); // Spec, #01, #02
});

test("on a GitHub board the Spec, Ticket and blocker numbers link to their issues", async ({
  page,
}) => {
  const fh = await Fh.start({
    env: {
      PATH: fakeGhPath([
        { number: 10, title: "Login", labels: ["ready-for-agent"], subIssues: [11, 12] },
        { number: 11, title: "Login form", labels: ["ready-for-agent"], parent: 10 },
        {
          number: 12,
          title: "Session cookie",
          labels: ["ready-for-agent"],
          parent: 10,
          blockedBy: { 11: "OPEN" },
        },
      ]),
    },
  });
  try {
    await page.goto(fh.launchURL);
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects
      .getByLabel("Path to a git repository")
      .fill(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    await page
      .locator('.column[aria-label="Tickets"]')
      .getByRole("button", { name: /Login/ })
      .click();

    const issues = "https://github.com/someone/shop/issues/";
    const detail = page.getByRole("region", { name: "Feature Login" });
    await expect(detail.getByRole("link", { name: "#10" })).toHaveAttribute("href", issues + "10");
    const form = detail.getByRole("listitem").filter({ hasText: "Login form" });
    const number = form.getByRole("link", { name: "#11", exact: true });
    await expect(number).toHaveAttribute("href", issues + "11");
    await expect(number).toHaveAttribute("target", "_blank");
    await expect(number).toHaveAttribute("rel", "noopener");
    const cookie = detail.getByRole("listitem").filter({ hasText: "Session cookie" });
    await expect(cookie).toContainText("blocked by #11");
    await expect(cookie.getByRole("link", { name: "#11", exact: true })).toHaveAttribute(
      "href",
      issues + "11",
    );
    // Only the numbers link: the titles stay text.
    await expect(detail.getByRole("link", { name: /Login form|Session cookie/ })).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});

// A Spec in needs-triage with one Ticket in needs-info: both offer Mark
// ready-for-agent (#192).
const triage = [
  { number: 10, title: "Login", labels: ["needs-triage"], subIssues: [11] },
  { number: 11, title: "Login form", labels: ["needs-info"], parent: 10 },
];

async function openGitHubFeature(page: Page, PATH: string): Promise<Fh> {
  const fh = await Fh.start({ env: { PATH } });
  await page.goto(fh.launchURL);
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects
    .getByLabel("Path to a git repository")
    .fill(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");
  await page.getByRole("tab", { name: "Software factory" }).click();
  await page.locator(".column").getByRole("button", { name: /Login/ }).click();
  return fh;
}

test("Mark ready-for-agent on a Ticket relabels it on the board", async ({ page }) => {
  const fh = await openGitHubFeature(
    page,
    fakeGhPath(triage, {
      afterEdit: [triage[0], { ...triage[1], labels: ["ready-for-agent"] }],
    }),
  );
  try {
    const form = page
      .getByRole("region", { name: "Feature Login" })
      .getByRole("listitem")
      .filter({ hasText: "Login form" });
    await expect(form).toContainText("needs-info");

    await form.getByRole("button", { name: "Mark ready-for-agent" }).click();

    await expect(form).toContainText("ready-for-agent");
    await expect(form.getByRole("button", { name: "Mark ready-for-agent" })).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});

test("a failed Mark ready-for-agent shows gh's error beside the button", async ({ page }) => {
  const fh = await openGitHubFeature(
    page,
    fakeGhPath(triage, { editFails: "could not add label: 'ready-for-agent' not found" }),
  );
  try {
    const detail = page.getByRole("region", { name: "Feature Login" });
    // The Spec's own action: Login is not approved yet.
    await detail.getByRole("button", { name: "Mark ready-for-agent" }).first().click();

    await expect(detail.getByRole("alert")).toContainText("'ready-for-agent' not found");
    await expect(detail.getByRole("listitem").filter({ hasText: "Login form" })).toContainText(
      "needs-info",
    );
  } finally {
    await fh.dispose();
  }
});

test("Needs triage lists the Untriaged issues, and Triage opens the Start dialog on one", async ({
  page,
}) => {
  // Seven Untriaged issues and no Spec: more than 5 rows, so the group
  // starts collapsed.
  const fh = await Fh.start({
    env: {
      PATH: fakeGhPath([
        { number: 1, title: "Crash on save", labels: ["needs-triage", "bug"] },
        { number: 2, title: "Idea: dark mode" },
        { number: 3, title: "Slow search", labels: ["needs-info"] },
        { number: 4, title: "Old one-off", state: "CLOSED", labels: ["needs-triage"] },
        { number: 5, title: "Export to CSV", labels: ["ready-for-human"] },
        { number: 6, title: "Typo in README" },
        { number: 7, title: "Flaky login", labels: ["needs-triage"] },
        { number: 8, title: "Idea: themes" },
      ]),
    },
  });
  try {
    await page.goto(fh.launchURL);
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects
      .getByLabel("Path to a git repository")
      .fill(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    const board = page.getByRole("region", { name: "Software factory" });
    await expect(board.locator(".notice")).toHaveText("No Specs yet; 7 issues need triage");
    const group = board.getByRole("region", { name: "Needs triage" });
    const toggle = group.getByRole("button", { name: /Needs triage/ });
    await expect(toggle).toContainText("7");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(group.getByRole("listitem")).toHaveCount(0);
    // The empty-Ideas line under the group names Ideas, so "one" can't be
    // read as one of the issues above.
    await expect(board.locator('[aria-label="Idea"] .empty')).toHaveText(
      "No Ideas yet: start one with Grill new idea.",
    );

    await toggle.click();
    const rows = group.getByRole("listitem");
    await expect(rows).toHaveCount(7);
    // Newest first, by issue number; the closed one-off is not there.
    await expect(rows.first()).toContainText("Idea: themes");
    await expect(rows.last()).toContainText("Crash on save");
    await expect(group).not.toContainText("Old one-off");
    const crash = rows.filter({ hasText: "Crash on save" });
    await expect(crash.getByRole("link", { name: "#1", exact: true })).toHaveAttribute(
      "href",
      "https://github.com/someone/shop/issues/1",
    );
    await expect(crash.getByText("needs-triage", { exact: true })).toBeVisible();
    await expect(crash.getByText("bug", { exact: true })).toBeVisible();

    // A board refresh keeps the group open.
    const refreshed = page.waitForResponse(
      (r) => r.url().includes("/api/board?") && r.url().endsWith("&refresh=1"),
    );
    await board.getByRole("button", { name: "Refresh" }).click();
    await refreshed;
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(rows).toHaveCount(7);

    await rows.filter({ hasText: "Flaky login" }).getByRole("button", { name: "Triage" }).click();
    const form = page.getByRole("form", { name: "Triage #7" });
    await expect(form.getByRole("textbox", { name: /Starting prompt/ })).toHaveValue("/triage #7");
  } finally {
    await fh.dispose();
  }
});

test("a Needs triage group of 5 or fewer starts open, with a note on an orphan Ticket", async ({
  page,
}) => {
  const fh = await Fh.start({
    env: {
      PATH: fakeGhPath([
        { number: 10, title: "Login", labels: ["ready-for-agent"] },
        { number: 11, title: "Crash on save", labels: ["needs-triage"] },
        // A Ticket whose parent, #7, isn't in the issue set (#181).
        { number: 12, title: "Retry uploads", labels: ["ready-for-agent"], body: "Part of #7" },
      ]),
    },
  });
  try {
    await page.goto(fh.launchURL);
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects
      .getByLabel("Path to a git repository")
      .fill(withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    const group = page.getByRole("region", { name: "Needs triage" });
    await expect(group.getByRole("button", { name: /Needs triage/ })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    const rows = group.getByRole("listitem");
    await expect(rows).toHaveText([/Retry uploads/, /Crash on save/]);
    await expect(rows.first().getByText("parent #7 not on the board")).toBeVisible();
    await expect(rows.first().getByText("ready-for-agent", { exact: true })).toBeVisible();
    await expect(rows.last()).not.toContainText("not on the board");
    // A Spec exists, so the note is not the triage count.
    await expect(page.locator(".notice")).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});

test("Triage on a local issue file opens the Start dialog on its path", async ({ page, fh }) => {
  // A .scratch folder without spec.md: its issue files are Untriaged issues.
  const repo = tempRepoWith("shop", {
    ".scratch/x/issues/01-a.md": "# 01: Crash on start\n\n**Status:** needs-triage\n",
    ".scratch/x/issues/02-b.md": "# 02: Slow search\n\n**Status:** needs-info\n",
  });
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");

  await page.getByRole("tab", { name: "Software factory" }).click();
  const board = page.getByRole("region", { name: "Software factory" });
  await expect(board.locator(".notice")).toHaveText("No Specs yet; 2 issues need triage");
  const rows = board.getByRole("region", { name: "Needs triage" }).getByRole("listitem");
  await expect(rows).toHaveText([/Slow search/, /Crash on start/]);
  const crash = rows.filter({ hasText: "Crash on start" });
  await expect(crash).toContainText(".scratch/x/issues/01-a.md");
  await expect(crash.getByText("needs-triage", { exact: true })).toBeVisible();
  await crash.getByRole("button", { name: "Triage" }).click();
  const form = page.getByRole("form", { name: "Triage .scratch/x/issues/01-a.md" });
  await expect(form.getByRole("textbox", { name: /Starting prompt/ })).toHaveValue(
    "/triage .scratch/x/issues/01-a.md",
  );
});

test("a Ticket row shows its PRs with PR state and checks, or No PR yet", async ({ page }) => {
  const pr = (number: number, branch: string, state: string, conclusion: string) => ({
    number,
    headRefName: branch,
    body: "",
    state,
    isDraft: false,
    url: `https://github.com/someone/shop/pull/${number}`,
    statusCheckRollup: [{ __typename: "CheckRun", status: "COMPLETED", conclusion }],
  });
  const fh = await Fh.start({
    ghPRs: [pr(21, "01-form", "OPEN", "SUCCESS"), pr(20, "01-form-v1", "CLOSED", "FAILURE")],
  });
  const ticket = (n: string, title: string) =>
    `# ${n}: ${title}\n\n**Blocked by:** None\n\n**Status:** ready-for-agent\n`;
  const repo = withOrigin(
    tempRepoWith("shop", {
      ".scratch/login/spec.md": "# Login\n",
      ".scratch/login/issues/01-form.md": ticket("01", "Login form"),
      ".scratch/login/issues/02-cookie.md": ticket("02", "Session cookie"),
    }),
    "git@github.com:someone/shop.git",
  );
  try {
    await page.goto(fh.launchURL);
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");

    await page.getByRole("tab", { name: "Software factory" }).click();
    await page.locator(".fcard", { hasText: "Login" }).click();
    const detail = page.getByRole("region", { name: "Feature Login" });
    const form = detail.getByRole("listitem").filter({ hasText: "Login form" });
    await expect(form.getByRole("link", { name: "#21" })).toHaveAttribute(
      "href",
      "https://github.com/someone/shop/pull/21",
    );
    await expect(form).toContainText("#21 ↗ Open ✓");
    await expect(form).toContainText("#20 ↗ Closed ✗");
    await expect(detail.getByRole("listitem").filter({ hasText: "Session cookie" })).toContainText(
      "No PR yet",
    );
  } finally {
    await fh.dispose();
  }
});

test("the Session header links the Worktree's branch and shows its PR with PR state and checks", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: { default: [{ say: "hello" }] },
    ghPRs: [
      {
        number: 21,
        headRefName: "main",
        body: "",
        state: "OPEN",
        isDraft: false,
        url: "https://github.com/someone/shop/pull/21",
        statusCheckRollup: [{ __typename: "CheckRun", status: "COMPLETED", conclusion: "SUCCESS" }],
      },
    ],
  });
  const repo = withRemoteBranch(
    withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"),
    "main",
  );
  try {
    await page.goto(fh.launchURL);
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    await page.getByRole("button", { name: "+ New Session" }).click();
    const form = page.getByRole("form", { name: "New Session" });
    await form.getByRole("radio", { name: /main/ }).check();
    await form.getByRole("button", { name: "Start Session" }).click();

    const head = page.getByRole("region", { name: /^Session / }).locator("header");
    await expect(head.getByRole("link", { name: "main ↗", exact: true })).toHaveAttribute(
      "href",
      "https://github.com/someone/shop/tree/main",
    );
    await expect(head.getByRole("link", { name: "PR #21" })).toHaveAttribute(
      "href",
      "https://github.com/someone/shop/pull/21",
    );
    await expect(head).toContainText("main ↗ · PR #21 ↗ Open · checks ✓");
  } finally {
    await fh.dispose();
  }
});

test("a Spec without Tickets offers Make tickets and a shipped Spec offers Retro", async ({
  page,
  fh,
}) => {
  // The buttons come from the server's next-step rule (internal/tracker/next.go).
  const repo = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n",
    ".scratch/billing/spec.md": "# Billing\n\n**Status:** done\n",
  });
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");
  await page.getByRole("tab", { name: "Software factory" }).click();

  await page.locator('.column[aria-label="Spec"]').getByRole("button", { name: /Login/ }).click();
  await page
    .getByRole("region", { name: "Feature Login" })
    .getByRole("button", { name: "Make tickets" })
    .click();
  await expect(
    page
      .getByRole("form", { name: /Make tickets/ })
      .getByRole("textbox", { name: /Starting prompt/ }),
  ).toHaveValue("/to-tickets .scratch/login/spec.md");
  // The Start dialog lives in the Sessions view; go back to the board.
  await page.getByRole("tab", { name: "Software factory" }).click();

  await page
    .locator('.column[aria-label="Shipped"]')
    .getByRole("button", { name: /Billing/ })
    .click();
  await page
    .getByRole("region", { name: "Feature Billing" })
    .getByRole("button", { name: "Retro" })
    .click();
  await expect(
    page.getByRole("form", { name: /Retro/ }).getByRole("textbox", { name: /Starting prompt/ }),
  ).toHaveValue("/retro .scratch/billing/spec.md");
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
    await expect(row.getByRole("button", { name: /^Remove Worktree .*7-login/ })).toBeVisible();
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
// tree, with prompt when given, returning the Session's conversation region.
async function startSession(page: Page, fh: Fh, prompt?: string): Promise<Locator> {
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
  if (prompt !== undefined) await form.getByLabel("Starting prompt (optional)").fill(prompt);
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

test("a message sent while the Agent works is queued; cancel puts it back in the composer, every time", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: { prompts: { long: [{ waitCancel: true }] } },
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
    await form.getByLabel("Starting prompt (optional)").fill("long");
    await form.getByRole("button", { name: "Start Session" }).click();

    const conversation = page.getByRole("region", { name: /^Session / });
    await expect(conversation.locator(".state")).toHaveText("Working");
    const message = conversation.getByRole("textbox", { name: "Message" });
    await message.fill("and then this");
    await message.press("Enter");

    const queued = conversation.getByRole("status", { name: "Queued message" });
    await expect(queued).toContainText("and then this");
    await expect(message).toHaveValue("");

    await conversation.getByRole("button", { name: "Cancel" }).click();
    await expect(queued).toBeHidden();
    await expect(message).toHaveValue("and then this");
    await expect(conversation.getByRole("button", { name: "Send" })).toBeEnabled();

    // The same text coming back a second time comes back too.
    await message.fill("long");
    await message.press("Enter");
    await expect(conversation.locator(".state")).toHaveText("Working");
    await message.fill("and then this");
    await message.press("Enter");
    await expect(queued).toContainText("and then this");
    await conversation.getByRole("button", { name: "Cancel" }).click();
    await expect(queued).toBeHidden();
    await expect(message).toHaveValue("and then this");
  } finally {
    await fh.dispose();
  }
});
