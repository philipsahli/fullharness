import { expect, test } from "@playwright/test";
import { Fh } from "./fh";
import { tempRepoWith } from "./repo";

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
