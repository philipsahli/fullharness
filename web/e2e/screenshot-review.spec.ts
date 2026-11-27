import { expect, test } from "@playwright/test";
import { utimesSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fakeGhComments } from "./gh";
import { boardShot, reachScreenshotReview, startScreenshotReview } from "./screens";

// Screenshot review in the Inbox (#226, ADR 0014): a PR that changes a
// screen waits after Autopilot's code review until the user has looked at
// the Agent's screenshot; Looks good says so on the PR and moves it on to
// the merge Stop point.
test("Screenshot review shows the Agent's screenshot, and Looks good moves the PR on", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const setup = await startScreenshotReview();
  try {
    const item = await reachScreenshotReview(page, setup);
    const inboxTab = page.getByRole("tab", { name: /^Inbox/ });
    await expect(inboxTab).toContainText("1");
    await expect(item.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      /Waiting at Screenshot review/,
    );
    await expect(item.getByRole("link", { name: "PR #5" })).toBeVisible();
    // Only the file under a UI path is marked.
    await expect(item.locator(".files .ui")).toHaveText("web/src/Board.tsx");

    // The screenshot opens full size; Escape closes it.
    await item.getByRole("button", { name: "Show board.png full size" }).click();
    const box = page.getByRole("dialog", { name: "Screenshot board.png" });
    await expect(box.getByRole("img", { name: "Screenshot board.png" })).toBeVisible();
    await expect(box).toContainText("1 of 1");
    await page.keyboard.press("Escape");
    await expect(box).toHaveCount(0);

    await item.getByRole("button", { name: "Looks good" }).click();

    await expect(item).toHaveCount(0);
    await expect(inboxTab).toContainText("0");
    const row = page.getByRole("region", { name: "Inbox" }).locator(".jn");
    await expect(row).toContainText("Looks good: PR #5");
    await expect(row.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      /Waiting at Merge/,
    );
    expect(fakeGhComments(setup.PATH)).toContain("pr comment 5 --body Screen reviewed in fh");
  } finally {
    await setup.fh.dispose();
  }
});

// Request changes (#227, ADR 0014): the note goes on the PR and to the
// Ticket's Session as its next turn; when that turn ends, the PR is back at
// Screenshot review, with the screenshot saved since then marked new.
test("Request changes sends the note back, and the PR returns with a new screenshot", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const setup = await startScreenshotReview();
  try {
    const item = await reachScreenshotReview(page, setup);
    await item.getByRole("button", { name: "Request changes" }).click();
    const send = item.getByRole("button", { name: "Post and send" });
    await expect(send).toBeDisabled(); // an empty note can't be sent
    await item.getByLabel(/^What's wrong\?/).fill("The badge covers the card's title.");

    // The Agent's next screenshot: its time is after the request, as when
    // the fix turn takes it.
    const after = path.join(worktreeOf(setup.repo), ".fh", "screenshots", "after.png");
    writeFileSync(after, boardShot());
    const later = new Date(Date.now() + 60_000);
    utimesSync(after, later, later);

    await send.click();

    const row = page.getByRole("region", { name: "Inbox" }).locator(".jn");
    await expect(row).toContainText("Changes requested on PR #5");
    expect(fakeGhComments(setup.PATH)).toContain("The badge covers the card's title.");

    // The fix turn ended done: a new item, with after.png marked new.
    await expect(item.getByRole("button", { name: "Show after.png full size" })).toBeVisible({
      timeout: 30_000,
    });
    await expect(item.getByRole("button", { name: "Show after.png full size" })).toContainText(
      "new",
    );
    await expect(item.getByRole("button", { name: "Show board.png full size" })).not.toContainText(
      "new",
    );
    await expect(item).toContainText("One is new since your Request changes.");
  } finally {
    await setup.fh.dispose();
  }
});

// worktreeOf is the 11-form Worktree that reachScreenshotReview's
// implement turn runs in.
const worktreeOf = (repo: string) =>
  path.join(path.dirname(repo), `${path.basename(repo)}.worktrees`, "11-form");

test("A Project's UI paths are edited in the rail", async ({ page }) => {
  const setup = await startScreenshotReview();
  try {
    await page.goto(setup.fh.launchURL);
    const projects = page.getByRole("region", { name: "Projects" });
    await projects.getByRole("button", { name: "+ Add Project" }).click();
    await projects.getByLabel("Path to a git repository").fill(setup.repo);
    await projects.getByRole("button", { name: "Add", exact: true }).click();
    const line = projects.getByRole("button", { name: /^UI paths of shop/ });
    await expect(line).toHaveAccessibleName("UI paths of shop: web/src/**");

    await line.click();
    const dialog = page.getByRole("dialog", { name: "UI paths for shop" });
    await dialog.getByLabel("Paths").fill("app/views/**\napp/assets/**\n");
    await dialog.getByRole("button", { name: "Save" }).click();

    await expect(dialog).toHaveCount(0);
    await expect(line).toHaveAccessibleName("UI paths of shop: app/views/**, app/assets/**");
  } finally {
    await setup.fh.dispose();
  }
});
