import { test, expect, type Page } from "@playwright/test";
import { Fh } from "./fh";
import { tempRepoWith } from "./repo";

// The Roadmap tab (#214): every Project's open Features in Now, Next and
// Later, read only. Moving a card is #215.

async function addProject(page: Page, repo: string) {
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText(repo.split("/").pop()!);
}

test("the Roadmap shows every Project's Features in their groups, and a chip filters them", async ({
  page,
}) => {
  const shop = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n",
    ".scratch/search/spec.md": "# Search\n\nRoadmap: next\n",
    ".scratch/billing/spec.md": "# Billing\n\n**Status:** done\n**Roadmap:** now\n",
  });
  const notes = tempRepoWith("notes", {
    ".scratch/tags/spec.md": "# Tags\n",
    ".scratch/themes/spec.md": "# Themes\n\n**Roadmap:** now\n",
  });
  const fh = await Fh.start({ ghPRs: [] });
  try {
    await page.goto(fh.launchURL);
    await addProject(page, shop);
    await addProject(page, notes);
    await page.getByRole("tab", { name: "Roadmap" }).click();

    const roadmap = page.getByRole("region", { name: "Roadmap" });
    const group = (name: string) => roadmap.getByRole("region", { name, exact: true });
    const cards = (name: string) => group(name).getByRole("listitem");
    await expect(cards("Now")).toHaveText([/Login/, /Themes/]);
    await expect(cards("Next")).toHaveText([/Search/]);
    // No group: Later, and the card says so. Billing is shipped: not here.
    await expect(cards("Later")).toHaveText([/Tags.*no Roadmap group/]);
    await expect(roadmap).not.toContainText("Billing");
    // Read only for now: the arrows are there, but don't move anything.
    await expect(
      group("Now")
        .getByRole("button", { name: /^Move Login/ })
        .first(),
    ).toBeDisabled();

    await roadmap.getByRole("button", { name: /^notes/ }).click();
    await expect(cards("Now")).toHaveText([/Themes/]);
    await expect(cards("Next")).toHaveText(["No Features in Next"]);
    await expect(roadmap.getByRole("button", { name: /^notes/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    await roadmap.getByRole("button", { name: "All Projects" }).click();
    await expect(cards("Now")).toHaveCount(2);

    // Open takes you to the Feature on the Software factory board.
    await cards("Now").filter({ hasText: "Themes" }).getByRole("button", { name: "Open" }).click();
    await expect(page.getByRole("tab", { name: "Software factory" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.getByRole("region", { name: "Feature Themes" })).toBeVisible();
  } finally {
    await fh.dispose();
  }
});
