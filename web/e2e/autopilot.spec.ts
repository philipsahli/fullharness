import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { Fh } from "./fh";
import { tempRepoWith } from "./repo";

// The end-to-end acceptance test for Autopilot in the UI (#228, Spec #199):
// move a Feature into Now on the Roadmap, see it wait at Approve spec,
// approve it from the Inbox, and see the next skill start at once. Every
// step goes through the UI; the test sets no state through the API.
//
// The Project is local markdown, not GitHub: a GitHub Spec that isn't
// ready-for-agent and has no Tickets reads as an Untriaged issue, so it never
// reaches Approve spec until #244 (a `spec` label) is built.
//
// Autopilot doesn't run /to-spec for a Spec that already exists, so a Spec
// moved into Now goes straight to waiting at Approve spec; nothing runs first.
//
// Every wait on Autopilot gets 30 s: that only bounds a wait on a busy
// machine (#220), it isn't what the test checks.
const autopilotWait = { timeout: 30_000 };

test("a Feature moved into Now waits at Approve spec, and Approve starts /to-tickets at once", async ({
  page,
}) => {
  const repo = tempRepoWith("shop", {
    ".scratch/search/spec.md": "# Search\n\n**Roadmap:** next\n\n**Status:** needs-triage\n",
  });
  // The /to-tickets turn holds on a gate file until the test has seen it
  // running, then ends asking.
  const gate = path.join(mkdtempSync(path.join(tmpdir(), "fh-gate-")), "gate");
  const fh = await Fh.start({
    ghPRs: [],
    agentScript: { default: [{ waitFile: gate }, { say: "Which labels should the tickets get?" }] },
    verdict: "asking",
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");

    // 1. A Project with Autopilot on.
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    await expect(projects).toContainText("main tree");
    const autopilot = projects.getByRole("checkbox", { name: "Autopilot" });
    await autopilot.click();
    await expect(autopilot).toBeChecked();

    // 2. On the Roadmap, ← moves Search from Next into Now (the groups run
    // Now, Next, Later).
    await page.getByRole("tab", { name: "Roadmap" }).click();
    const roadmap = page.getByRole("region", { name: "Roadmap" });
    const cardsIn = (name: string) =>
      roadmap.getByRole("region", { name, exact: true }).getByRole("listitem");
    await expect(cardsIn("Next")).toHaveText([/Search/]);
    await cardsIn("Next").getByRole("button", { name: "Move Search to Now" }).click();
    const card = cardsIn("Now").filter({ hasText: "Search" });
    await expect(card).toBeVisible();
    await expect(card.getByRole("button", { name: "Move Search to Next" })).toBeEnabled();
    await expect(card.getByRole("alert")).toHaveCount(0);
    expect(readFileSync(path.join(repo, ".scratch/search/spec.md"), "utf8")).toContain(
      "**Roadmap:** now",
    );

    // 3. Its card waits at Approve spec.
    const badge = card.getByRole("img", { name: /^Autopilot status/ });
    await expect(badge).toHaveAccessibleName(
      "Autopilot status: Waiting at Approve spec",
      autopilotWait,
    );

    // 4. The Approve spec item is in the Inbox.
    const inboxTab = page.getByRole("tab", { name: /^Inbox/ });
    await expect(inboxTab).toContainText("1");
    await inboxTab.click();
    const inbox = page.getByRole("region", { name: "Inbox" });
    const item = inbox.getByRole("group", { name: "Approve spec: Search" });
    await expect(item).toBeVisible();

    // 5. Approve writes the label and starts /to-tickets without waiting
    // for Autopilot's next look, a minute later.
    await item.getByRole("button", { name: "Approve" }).click();
    await expect(item).toHaveCount(0);
    expect(readFileSync(path.join(repo, ".scratch/search/spec.md"), "utf8")).toContain(
      "**Status:** ready-for-agent",
    );
    const row = inbox.locator(".jn");
    await expect(row.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      "Autopilot status: Running /to-tickets",
      autopilotWait,
    );

    // The same Feature on the Roadmap runs too, then waits once the turn ends.
    await page.getByRole("tab", { name: "Roadmap" }).click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Running /to-tickets");
    writeFileSync(gate, "");
    await expect(badge).toHaveAccessibleName(
      "Autopilot status: Waiting at Agent asking",
      autopilotWait,
    );
  } finally {
    await fh.dispose();
  }
});
