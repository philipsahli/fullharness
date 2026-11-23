import { test, expect } from "@playwright/test";
import { Fh } from "./fh";
import { tempRepo } from "./repo";

// #69: the page fetches a Session's conversation once when it opens, and
// live events keep it current. Events that arrive while that fetch is still
// on its way must not be lost.
test("a reply that arrives while the page still loads the conversation is shown", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      prompts: {
        hello: [
          { sleepMs: 300 },
          { say: "Hi there" },
          { tool: { id: "t1", title: "ls", kind: "execute", status: "pending" } },
          { toolUpdate: { id: "t1", status: "completed", rawOutput: "README.md" } },
        ],
      },
    },
  });
  try {
    // Hold the conversation fetch back until the Agent has answered: fh has
    // already built the response, so the reply is only in the live events.
    await page.route(/\/api\/sessions\/[a-z0-9]+$/, async (route) => {
      const resp = await route.fetch();
      await new Promise((r) => setTimeout(r, 1000));
      await route.fulfill({ response: resp });
    });
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
    await expect(conversation.locator(".state")).toHaveText("Idle");
    await expect(conversation.getByText("Hi there")).toBeVisible({ timeout: 2000 });
    await expect(conversation.getByText("ls", { exact: true })).toBeVisible({ timeout: 2000 });
  } finally {
    await fh.dispose();
  }
});

// fh sends conversation events from several goroutines, so a tab can get
// them out of order; a chunk the fetched conversation already holds can also
// come in after it. Each event applies once, in fh's order.
test("conversation events apply once each, in fh's order, however they arrive", async ({
  page,
}) => {
  const fh = await Fh.start({
    agentScript: {
      prompts: {
        hello: [{ sleepMs: 1000 }, { say: "A" }, { say: "B" }, { say: "C" }],
      },
    },
  });
  try {
    // Hold chunk "B" back until "C" has gone through, then send "B" twice.
    await page.routeWebSocket(/\/ws/, (ws) => {
      const server = ws.connectToServer();
      let heldB: string | undefined;
      server.onMessage((msg) => {
        const text = typeof msg === "string" ? msg : msg.toString();
        const ev = JSON.parse(text);
        if (ev.type === "chunk" && ev.text === "B") {
          heldB = text;
          return;
        }
        ws.send(text);
        if (ev.type === "chunk" && ev.text === "C" && heldB) {
          ws.send(heldB);
          ws.send(heldB);
        }
      });
    });
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
    await expect(conversation.locator(".state")).toHaveText("Idle");
    await expect(conversation.locator(".msg.agent")).toHaveText("ABC");
  } finally {
    await fh.dispose();
  }
});
