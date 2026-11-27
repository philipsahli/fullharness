import { test, expect, type Page } from "@playwright/test";
import { Fh } from "./fh";
import { fakeGhPath, type GhIssue } from "./gh";
import { tempRepo, tempRepoWith, withOrigin } from "./repo";

// The Roadmap tab (#214): every Project's open Features in Now, Next and
// Later. ← and → move a card and write its Roadmap group at once (#215).

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
    // Now is the first group: there is nowhere further left to go.
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

// Autopilot status on Roadmap cards (#216, ADR 0013): the same badge as the
// Feature card, from the snapshot and the autopilot event, so no request of
// its own.
test("a Roadmap card in Now whose Project has Autopilot off links to the switch", async ({
  page,
}) => {
  const shop = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
    ".scratch/search/spec.md": "# Search\n\n**Roadmap:** next\n",
  });
  const fh = await Fh.start({ ghPRs: [] });
  try {
    await page.goto(fh.launchURL);
    await addProject(page, shop);
    await page.getByRole("tab", { name: "Roadmap" }).click();

    const roadmap = page.getByRole("region", { name: "Roadmap" });
    const now = roadmap.getByRole("region", { name: "Now", exact: true });
    const login = now.getByRole("listitem").filter({ hasText: "Login" });
    await expect(login.getByRole("img", { name: /^Autopilot status/ })).toHaveAccessibleName(
      "Autopilot status: Autopilot off",
    );
    // A Feature outside Now that nothing runs for gets no badge: its group
    // already says why.
    await expect(
      roadmap
        .getByRole("region", { name: "Next", exact: true })
        .getByRole("img", { name: /^Autopilot status/ }),
    ).toHaveCount(0);

    const toggle = page
      .getByRole("region", { name: "Projects" })
      .getByRole("checkbox", { name: "Autopilot" });
    await login.getByRole("button", { name: "Autopilot off for shop · Turn on" }).click();
    await expect(toggle).toBeFocused();
    await expect(toggle).not.toBeChecked();
  } finally {
    await fh.dispose();
  }
});

test("a Roadmap card's Autopilot badge changes without a reload", async ({ page }) => {
  const shop = tempRepoWith("shop", {
    ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
  });
  // Turning Autopilot on starts to-tickets for the approved Spec; the fake
  // turn runs long enough to see, then ends asking.
  const fh = await Fh.start({
    ghPRs: [],
    agentScript: { default: [{ sleepMs: 3000 }, { say: "Which labels should the tickets get?" }] },
    verdict: "asking",
  });
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    await addProject(page, shop);
    await page.getByRole("tab", { name: "Roadmap" }).click();

    const login = page
      .getByRole("region", { name: "Roadmap" })
      .getByRole("region", { name: "Now", exact: true })
      .getByRole("listitem")
      .filter({ hasText: "Login" });
    const badge = login.getByRole("img", { name: /^Autopilot status/ });
    await expect(badge).toHaveAccessibleName("Autopilot status: Autopilot off");

    await page
      .getByRole("region", { name: "Projects" })
      .getByRole("checkbox", { name: "Autopilot" })
      .click();
    await expect(badge).toHaveAccessibleName("Autopilot status: Running /to-tickets");
    await expect(login).toHaveAttribute("data-ap", "running");
    await expect(login.getByRole("button", { name: /Turn on/ })).toHaveCount(0);
    await expect(badge).toHaveAccessibleName("Autopilot status: Waiting at Agent asking", {
      timeout: 10_000,
    });
    await expect(login).toHaveAttribute("data-ap", "waiting");
  } finally {
    await fh.dispose();
  }
});

const login: GhIssue = { number: 10, title: "Login", labels: ["ready-for-agent", "roadmap:next"] };

async function openGitHubRoadmap(page: Page, PATH: string): Promise<Fh> {
  const fh = await Fh.start({ env: { PATH } });
  await page.goto(fh.launchURL);
  await addProject(page, withOrigin(tempRepo("shop"), "git@github.com:someone/shop.git"));
  await page.getByRole("tab", { name: "Roadmap" }).click();
  return fh;
}

const cardsIn = (page: Page, name: string) =>
  page
    .getByRole("region", { name: "Roadmap" })
    .getByRole("region", { name, exact: true })
    .getByRole("listitem");

test("→ moves a card to the next group and writes it, so it stays after a reload", async ({
  page,
}) => {
  const fh = await openGitHubRoadmap(
    page,
    fakeGhPath([login], {
      afterEdit: [{ ...login, labels: ["ready-for-agent", "roadmap:later"] }],
    }),
  );
  try {
    await expect(cardsIn(page, "Next")).toHaveText([/Login/]);

    await cardsIn(page, "Next").getByRole("button", { name: "Move Login to Later" }).click();

    const card = cardsIn(page, "Later").filter({ hasText: "Login" });
    await expect(card).toBeVisible();
    await expect(cardsIn(page, "Next")).toHaveText(["No Features in Next"]);
    // The write has ended once the arrows work again; it didn't fail.
    await expect(card.getByRole("button", { name: "Move Login to Next" })).toBeEnabled();
    await expect(card.getByRole("alert")).toHaveCount(0);

    await page.reload();
    await page.getByRole("tab", { name: "Roadmap" }).click();
    await expect(cardsIn(page, "Later")).toHaveText([/Login/]);
  } finally {
    await fh.dispose();
  }
});

test("a move gh refuses goes back to its group with gh's error", async ({ page }) => {
  const fh = await openGitHubRoadmap(
    page,
    fakeGhPath([login], {
      editFails:
        "HTTP 403: Resource not accessible by personal access token (addLabelsToLabelable)",
    }),
  );
  try {
    // ← from Next is towards Now: the groups run Now, Next, Later.
    await cardsIn(page, "Next").getByRole("button", { name: "Move Login to Now" }).click();

    const card = cardsIn(page, "Next").filter({ hasText: "Login" });
    await expect(card.getByRole("alert")).toContainText("Not moved to Now.");
    await expect(card.getByRole("alert")).toContainText("Resource not accessible");
    await expect(cardsIn(page, "Now")).toHaveText(["No Features in Now"]);

    await card.getByRole("button", { name: "Dismiss the error" }).click();
    await expect(card.getByRole("alert")).toHaveCount(0);
  } finally {
    await fh.dispose();
  }
});
