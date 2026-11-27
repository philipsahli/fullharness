import { test, expect, type Page } from "@playwright/test";
import { Fh } from "./fh";
import { tmpdir } from "node:os";
import path from "node:path";
import { tempRepoWith } from "./repo";

// Local pixel tests (ADR 0012): eight screens in light and dark, compared with
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

    // Autopilot status badges (#203): first with Autopilot off, then one
    // Feature running with its live pulse (#205), one waiting at Spec
    // approval, one outside Now without a badge.
    test("Software factory board with Autopilot badges", async ({ page }) => {
      const fh = await Fh.start({
        ghPRs: [],
        agentScript: {
          default: [
            {
              tool: {
                id: "t1",
                title: "Edit web/src/Board.tsx",
                kind: "edit",
                status: "in_progress",
              },
            },
            { sleepMs: 60_000 },
          ],
        },
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
        // Autopilot off: the Features in Now link to the switch.
        await expect(page.getByRole("button", { name: /Turn on/ })).toHaveCount(2);
        await expect(page).toHaveScreenshot(`board-autopilot-off-${scheme}.png`, {
          mask: dynamic(page),
        });
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
        await expect(page.locator(".ap-live")).toHaveText("Edit web/src/Board.tsx");
        await expect(page).toHaveScreenshot(`board-autopilot-${scheme}.png`, {
          mask: dynamic(page),
        });
      } finally {
        await fh.dispose();
      }
    });

    // The Roadmap (#214): two Projects' Features in Now, Next and Later, with
    // their Autopilot status (#216): shop has Autopilot off, so Login links to
    // the switch; notes has it on, so its unapproved Themes waits at Approve
    // spec. (Not shop: Login has a Ticket, and Autopilot would start an Agent.)
    test("Roadmap", async ({ page }) => {
      const fh = await Fh.start({ ghPRs: [] });
      try {
        const slug = test.info().titlePath.slice(1).join("-").replace(/\W+/g, "-");
        const parent = path.join(tmpdir(), "fh-visual", slug);
        const shop = tempRepoWith(
          "shop",
          {
            ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n",
            ".scratch/login/issues/01-form.md": ticket("01", "Login form", "None"),
            ".scratch/search/spec.md": "# Search\n\n**Roadmap:** next\n",
          },
          path.join(parent, "a"),
        );
        const notes = tempRepoWith(
          "notes",
          {
            ".scratch/tags/spec.md": "# Tags\n",
            ".scratch/themes/spec.md": "# Themes\n\n**Roadmap:** now\n",
          },
          path.join(parent, "b"),
        );
        await open(page, fh, shop);
        const projects = page.getByRole("region", { name: "Projects" });
        await projects.getByRole("button", { name: "+ Add Project" }).click();
        await projects.getByLabel("Path to a git repository").fill(notes);
        await projects.getByRole("button", { name: "Add", exact: true }).click();
        await expect(projects).toContainText("notes");
        await projects.getByRole("checkbox", { name: "Autopilot" }).last().click();
        await page.getByRole("tab", { name: "Roadmap" }).click();
        const now = page.getByRole("region", { name: "Now", exact: true });
        await expect(now.getByRole("listitem")).toHaveCount(2);
        await expect(now.getByRole("img", { name: /^Autopilot status/ })).toHaveText([
          /Autopilot off/,
          /Approve spec/,
        ]);
        await expect(page).toHaveScreenshot(`roadmap-${scheme}.png`, { mask: dynamic(page) });
      } finally {
        await fh.dispose();
      }
    });

    // Approve spec in the Inbox (#229): Search, in Now and unapproved, waits
    // at Approve spec once Autopilot is on. Nothing is Startable, so
    // Autopilot starts no Agent.
    test("Inbox with Approve spec", async ({ page }) => {
      const fh = await Fh.start({ ghPRs: [] });
      try {
        const slug = test.info().titlePath.slice(1).join("-").replace(/\W+/g, "-");
        const repo = tempRepoWith(
          "shop",
          {
            ".scratch/search/spec.md": "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n",
          },
          path.join(tmpdir(), "fh-visual", slug),
        );
        await open(page, fh, repo);
        await page
          .getByRole("region", { name: "Projects" })
          .getByRole("checkbox", { name: "Autopilot" })
          .click();
        await page.getByRole("tab", { name: /^Inbox/ }).click();
        await expect(
          page
            .getByRole("group", { name: "Approve spec: Search" })
            .getByText(".scratch/search/spec.md"),
        ).toBeVisible();
        await expect(page).toHaveScreenshot(`inbox-approve-spec-${scheme}.png`, {
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

    // The flow graph (#206): 01 merged back, 02 failed to start once and
    // now runs (Autopilot is here), 03 blocked by 02.
    test("Feature page with the flow graph", async ({ page }) => {
      const slug = test.info().titlePath.slice(1).join("-").replace(/\W+/g, "-");
      const repo = tempRepoWith(
        "shop",
        {
          ".scratch/login/spec.md": "# Login\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n",
          ".scratch/login/issues/01-form.md":
            "# 01: Login form\n\n**Blocked by:** None\n\n**Status:** done\n",
          ".scratch/login/issues/02-cookie.md": ticket("02", "Session cookie", "None"),
          ".scratch/login/issues/03-logout.md": ticket("03", "Logout", "02"),
        },
        path.join(tmpdir(), "fh-visual", slug),
      );
      const at = (n: string, skill: string) => ({
        project: repo,
        feature: "login",
        ticket: n,
        skill,
        time: "2026-11-27T10:00:00Z",
      });
      const fh = await Fh.start({
        ghPRs: [],
        agentScript: { default: [{ sleepMs: 60_000 }] },
        verdict: "done",
        autopilotHistory: [
          { ...at("", "to-tickets"), kind: "skill", session: "gone-0" },
          { ...at("", "to-tickets"), kind: "classified", session: "gone-0", verdict: "done" },
          { ...at("01", "implement"), kind: "skill", session: "gone-1" },
          { ...at("01", "implement"), kind: "classified", session: "gone-1", verdict: "done" },
          { ...at("01", "code-review"), kind: "stop", reason: "merge" },
          { ...at("02", "implement"), kind: "failed", error: "the Agent didn't start" },
        ],
      });
      try {
        await open(page, fh, repo);
        await page.getByRole("tab", { name: "Software factory" }).click();
        await page
          .locator(".column")
          .getByRole("button", { name: /^Login/ })
          .click();
        await page
          .getByRole("region", { name: "Projects" })
          .getByRole("checkbox", { name: "Autopilot" })
          .click();
        const graph = page.getByRole("group", { name: /^Flow graph of Login/ });
        await expect(
          graph.locator('.fl-node[data-ticket="02"][data-state="running"][data-current]'),
        ).toBeVisible();
        await expect(page.getByRole("region", { name: "Feature Login" })).toHaveScreenshot(
          `feature-flow-${scheme}.png`,
          { mask: dynamic(page) },
        );
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
