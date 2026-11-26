import { test, expect, type Page } from "@playwright/test";
import { Fh } from "./fh";
import { tmpdir } from "node:os";
import path from "node:path";
import { tempRepoWith } from "./repo";

// Local pixel tests (ADR 0012): four screens in light and dark, compared with
// the contributor's own last baseline. They run only in the "visual" project
// (make e2e-visual), never in make check or CI, because font rendering differs
// between OSes. Baselines live in e2e/__visual__/, which is gitignored.

const ticket = (n: string, title: string, blockedBy: string) =>
  `# ${n}: ${title}\n\n**Blocked by:** ${blockedBy}\n\n**Status:** ready-for-agent\n`;

// A Project with a local markdown Feature: one Startable Ticket, one blocked.
// The repo sits at a fixed path per test: the Session header shows the path,
// and a random temp name changes its width, which can wrap the header.
function shopRepo(): string {
  const slug = test.info().titlePath.slice(1).join("-").replace(/\W+/g, "-");
  return tempRepoWith(
    "shop",
    {
      ".scratch/login/spec.md": "# Login\n",
      ".scratch/login/issues/01-form.md": ticket("01", "Login form", "None"),
      ".scratch/login/issues/02-cookie.md": ticket("02", "Session cookie", "01"),
    },
    path.join(tmpdir(), "fh-visual", slug),
  );
}

async function open(page: Page, fh: Fh, repo: string) {
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");
}

// Text that changes between machines or runs: times and temp paths.
const dynamic = (page: Page) => [page.locator("time"), page.getByText(/fh-visual/)];

for (const scheme of ["light", "dark"] as const) {
  test.describe(scheme, () => {
    test.use({ colorScheme: scheme });

    test("Software factory board", async ({ page }) => {
      const fh = await Fh.start({ ghPRs: [] });
      try {
        await open(page, fh, shopRepo());
        await page.getByRole("tab", { name: "Software factory" }).click();
        await expect(page.locator('.column[aria-label="Tickets"]')).toContainText("2 tickets");
        await expect(page).toHaveScreenshot(`board-${scheme}.png`, { mask: dynamic(page) });
      } finally {
        await fh.dispose();
      }
    });

    // Autopilot status badges (#203): one Feature running, one waiting at
    // Spec approval, one outside Now without a badge.
    test("Software factory board with Autopilot badges", async ({ page }) => {
      const fh = await Fh.start({
        ghPRs: [],
        agentScript: { default: [{ sleepMs: 60_000 }] },
        verdict: "done",
      });
      try {
        const slug = test.info().titlePath.slice(1).join("-").replace(/\W+/g, "-");
        const repo = tempRepoWith(
          "shop",
          {
            ".scratch/login/spec.md":
              "# Login\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
            ".scratch/login/issues/01-form.md": ticket("01", "Login form", "None"),
            ".scratch/search/spec.md": "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n",
            ".scratch/billing/spec.md": "# Billing\n\n**Roadmap:** next\n",
          },
          path.join(tmpdir(), "fh-visual", slug),
        );
        await open(page, fh, repo);
        await page.getByRole("tab", { name: "Software factory" }).click();
        await page
          .getByRole("region", { name: "Projects" })
          .getByRole("checkbox", { name: "Autopilot" })
          .click();
        const badges = page.getByRole("img", { name: /^Autopilot status/ });
        await expect(badges).toHaveCount(2);
        await expect(badges.first()).toHaveAccessibleName(
          "Autopilot status: Waiting at Approve spec",
        );
        await expect(badges.last()).toHaveAccessibleName(
          "Autopilot status: Running /implement on #01",
        );
        await expect(page).toHaveScreenshot(`board-autopilot-${scheme}.png`, {
          mask: dynamic(page),
        });
      } finally {
        await fh.dispose();
      }
    });

    test("Feature detail", async ({ page }) => {
      const fh = await Fh.start({ ghPRs: [] });
      try {
        await open(page, fh, shopRepo());
        await page.getByRole("tab", { name: "Software factory" }).click();
        await page
          .locator('.column[aria-label="Tickets"]')
          .getByRole("button", { name: /Login/ })
          .click();
        await expect(page.getByRole("region", { name: "Feature Login" })).toContainText(
          "Start Session",
        );
        await expect(page).toHaveScreenshot(`feature-${scheme}.png`, { mask: dynamic(page) });
      } finally {
        await fh.dispose();
      }
    });

    test("Session with a permission card", async ({ page }) => {
      const fh = await Fh.start({
        ghPRs: [],
        agentScript: {
          default: [
            { say: "I'll clean up the build folder." },
            {
              permission: {
                id: "t1",
                title: "rm -rf build",
                kind: "execute",
                command: "rm -rf build",
              },
            },
          ],
        },
      });
      try {
        await open(page, fh, shopRepo());
        await page.getByRole("button", { name: "+ New Session" }).click();
        const form = page.getByRole("form", { name: "New Session" });
        await form.getByRole("radio", { name: /main/ }).check();
        await form.getByLabel("Starting prompt (optional)").fill("clean up");
        await form.getByRole("button", { name: "Start Session" }).click();
        const conversation = page.getByRole("region", { name: /^Session / });
        await expect(
          conversation.getByRole("group", { name: "Permission: rm -rf build" }),
        ).toBeVisible();
        await expect(conversation.locator(".mhead .state")).toHaveText("Waiting");
        await expect(page).toHaveScreenshot(`session-${scheme}.png`, { mask: dynamic(page) });
      } finally {
        await fh.dispose();
      }
    });

    test("Projects rail with the Add Project form", async ({ page }) => {
      const fh = await Fh.start({ ghPRs: [] });
      try {
        await open(page, fh, shopRepo());
        const projects = page.getByRole("region", { name: "Projects" });
        await projects.getByRole("button", { name: "+ Add Project" }).click();
        await expect(projects.getByRole("form", { name: "Add Project" })).toBeVisible();
        await expect(page).toHaveScreenshot(`projects-${scheme}.png`, { mask: dynamic(page) });
      } finally {
        await fh.dispose();
      }
    });
  });
}
