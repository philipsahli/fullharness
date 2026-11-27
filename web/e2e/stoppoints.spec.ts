import { expect, test } from "@playwright/test";
import { Fh } from "./fh";
import { fakeGhPath } from "./gh";
import { tempRepo, tempRepoWith, withOrigin } from "./repo";

// Approve spec in the Inbox (#229, ADR 0014): a Feature in Now that waits
// at Approve spec gets an Inbox item; Approve marks the Spec
// ready-for-agent and steps the Project at once, so /to-tickets starts
// without waiting for Autopilot's next look, a minute later.
test("Approve spec in the Inbox starts /to-tickets at once", async ({ page }) => {
  const repo = tempRepoWith("shop", {
    ".scratch/search/spec.md": "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n",
  });
  // The to-tickets turn runs long enough to see it running.
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

    const inboxTab = page.getByRole("tab", { name: /^Inbox/ });
    await expect(inboxTab).toContainText("0");
    await projects.getByRole("checkbox", { name: "Autopilot" }).click();
    await expect(inboxTab).toContainText("1");

    await inboxTab.click();
    const inbox = page.getByRole("region", { name: "Inbox" });
    const item = inbox.getByRole("group", { name: "Approve spec: Search" });
    await expect(item).toContainText(".scratch/search/spec.md");
    await expect(item.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      "Autopilot status: Waiting at Approve spec",
    );
    await expect(item.getByRole("button", { name: "Reject" })).toHaveCount(0); // #225

    await item.getByRole("button", { name: "Approve" }).click();

    await expect(item).toHaveCount(0);
    await expect(inboxTab).toContainText("0");
    const row = inbox.locator(".jn");
    await expect(row).toContainText("Approved Search");
    await expect(row).toContainText("ready-for-agent set · Autopilot stepped shop now");
    await expect(row.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      "Autopilot status: Running /to-tickets",
    );
  } finally {
    await fh.dispose();
  }
});

// A GitHub Spec reaches Approve spec only with the spec label (#244):
// without it, an unapproved Spec with no Tickets reads as an Untriaged
// issue (#196).
test("a GitHub issue labelled spec waits at Approve spec in the Inbox", async ({ page }) => {
  const repo = withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git");
  const fh = await Fh.start({
    env: {
      PATH: fakeGhPath([
        { number: 30, title: "Search", labels: ["spec", "roadmap:now"] },
        { number: 31, title: "A plain bug", labels: ["needs-triage"] },
      ]),
    },
    agentScript: { default: [{ say: "ok" }] },
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

    await projects.getByRole("checkbox", { name: "Autopilot" }).click();
    const inboxTab = page.getByRole("tab", { name: /^Inbox/ });
    // Autopilot's step reads the board through gh first; on a busy machine
    // that takes longer than the default 5 s. 30 s only bounds the wait.
    await expect(inboxTab).toContainText("1", { timeout: 30_000 });
    await inboxTab.click();
    const item = page
      .getByRole("region", { name: "Inbox" })
      .getByRole("group", { name: "Approve spec: Search" });
    await expect(item).toContainText("#30");
    await expect(item.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      "Autopilot status: Waiting at Approve spec",
    );
  } finally {
    await fh.dispose();
  }
});
