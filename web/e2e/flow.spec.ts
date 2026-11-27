import { test, expect, type Page } from "@playwright/test";
import { Fh } from "./fh";
import { tempRepoWith } from "./repo";

// The flow graph on the Feature page (#206, ADR-0013): the Stages on a main
// line, a branch per Ticket, a dot per skill Autopilot ran. fh works it out
// (internal/autopilot/flow, table-tested there); these check the UI draws
// it, moves it live, and opens a dot's Session.

const ticket = (n: string, title: string, status: string, blockedBy = "None") =>
  `# ${n}: ${title}\n\n**Blocked by:** ${blockedBy}\n\n**Status:** ${status}\n`;

async function openFeature(page: Page, fh: Fh, repo: string, name: string) {
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");
  await page.getByRole("tab", { name: "Software factory" }).click();
  await page
    .locator(".column")
    .getByRole("button", { name: new RegExp(`^${name}`) })
    .click();
  return page.getByRole("region", { name: `Feature ${name}` }).getByRole("group", {
    name: /^Flow graph of/,
  });
}

const node = (graph: ReturnType<Page["locator"]>, ticketNo: string, name: string, state: string) =>
  graph.locator(`.fl-node[data-ticket="${ticketNo}"][data-name="${name}"][data-state="${state}"]`);

test("the flow graph follows Autopilot live and a dot opens its Session", async ({ page }) => {
  const repo = tempRepoWith("shop", {
    ".scratch/search/spec.md": "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
    ".scratch/search/issues/01-index.md": ticket("01", "Index", "ready-for-agent"),
    ".scratch/search/issues/02-query.md": ticket("02", "Query", "ready-for-agent", "01"),
  });
  // implement on 01 runs long enough to see it, then its turn ends asking.
  const fh = await Fh.start({
    ghPRs: [],
    agentScript: { default: [{ sleepMs: 3000 }, { say: "Which fields go in the index?" }] },
    verdict: "asking",
  });
  try {
    const graph = await openFeature(page, fh, repo, "Search");
    await expect(graph).toHaveAccessibleName("Flow graph of Search: 2 Tickets");
    // A branch per Ticket; 02 waits for 01.
    await expect(graph.locator("text.tn")).toHaveText(["#01", "#02"]);
    await expect(graph.getByText("blocked by #01")).toBeVisible();

    await page
      .getByRole("region", { name: "Projects" })
      .getByRole("checkbox", { name: "Autopilot" })
      .click();
    const running = node(graph, "01", "implement", "running");
    await expect(running).toHaveAttribute("data-current", "true");
    await expect(graph).toHaveAccessibleName("Flow graph of Search: 2 Tickets, Autopilot running");

    // The turn ends asking: Autopilot now waits there, ◉, without a reload.
    const asking = node(graph, "01", "asking", "waiting");
    await expect(asking).toHaveAttribute("data-current", "true", { timeout: 10_000 });
    await expect(asking).toContainText("◉");
    await expect(node(graph, "01", "implement", "passed")).toBeVisible();

    // The implement dot ran in a Session: clicking it opens that Session.
    await graph.getByRole("button", { name: /^#01 \/implement: passed/ }).click();
    const conversation = page.getByRole("region", { name: /^Session / });
    await expect(conversation).toContainText("Which fields go in the index?");
  } finally {
    await fh.dispose();
  }
});

test("the flow graph marks a failed start ✖, a waiting Stop point ◉ and a merged branch", async ({
  page,
}) => {
  const repo = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
    ".scratch/login/issues/01-form.md": ticket("01", "Login form", "done"),
    ".scratch/login/issues/02-cookie.md": ticket("02", "Session cookie", "ready-for-agent"),
    ".scratch/login/issues/03-logout.md": ticket("03", "Logout", "ready-for-agent", "02"),
  });
  const at = (ticketNo: string, skill: string) => ({
    project: repo,
    feature: "login",
    ticket: ticketNo,
    skill,
    time: "2026-11-27T10:00:00Z",
  });
  const fh = await Fh.start({
    ghPRs: [],
    autopilotHistory: [
      { ...at("01", "implement"), kind: "skill", session: "gone-1" },
      { ...at("01", "implement"), kind: "classified", session: "gone-1", verdict: "done" },
      { ...at("01", "code-review"), kind: "stop", reason: "merge" },
      { ...at("02", "implement"), kind: "failed", error: "the Agent didn't start" },
      { ...at("02", "implement"), kind: "skill", session: "gone-2" },
      { ...at("02", "implement"), kind: "classified", session: "gone-2", verdict: "asking" },
      { ...at("02", "implement"), kind: "stop", session: "gone-2", reason: "asking" },
    ],
  });
  try {
    const graph = await openFeature(page, fh, repo, "Login");
    await expect(graph.locator("text.tn")).toHaveText(["#01", "#02", "#03"]);

    const fail = node(graph, "02", "implement", "failed");
    await expect(fail).toHaveAccessibleName("#02 /implement: failed (the Agent didn't start)");
    await expect(fail.locator("path.fl-fail")).toBeVisible();
    // Autopilot is off for the Project, so nothing is current, but the
    // turn that ended asking still waits for the user.
    await expect(node(graph, "02", "asking", "waiting")).not.toHaveAttribute("data-current");
    await expect(node(graph, "02", "asking", "waiting").locator(".fl-stop-dot")).toBeVisible();
    // 01 is closed: its merge Stop point passed and its branch merged back.
    await expect(node(graph, "01", "merge", "passed")).toBeVisible();
    await expect(graph.getByText("blocked by #02")).toBeVisible();
  } finally {
    await fh.dispose();
  }
});

test("the flow graph scrolls sideways at phone width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  const repo = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
    ".scratch/login/issues/01-form.md": ticket("01", "Login form", "ready-for-agent"),
  });
  const fh = await Fh.start({ ghPRs: [] });
  try {
    const graph = await openFeature(page, fh, repo, "Login");
    await expect(graph).toBeVisible();
    const wrap = page.locator(".flow-wrap");
    const { scroll, client } = await wrap.evaluate((el) => ({
      scroll: el.scrollWidth,
      client: el.clientWidth,
    }));
    expect(scroll).toBeGreaterThan(client); // the graph keeps its size; its box scrolls
    expect(client).toBeLessThanOrEqual(390);
  } finally {
    await fh.dispose();
  }
});
