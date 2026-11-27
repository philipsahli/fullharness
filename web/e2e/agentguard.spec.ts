import { test, expect } from "@playwright/test";
import { accessSync, constants } from "node:fs";
import path from "node:path";
import { Fh } from "./fh";
import { tempRepo } from "./repo";

// No e2e test may run a real Agent (#236). A test that forgets
// agentScript starts Claude Code's default command, npx, from the
// developer's PATH; the agent guard in Fh.start refuses it. Where Claude
// Code isn't installed (CI), fh shows it as missing and starts nothing, so
// there is nothing to guard.
const installed = (cli: string) =>
  (process.env.PATH ?? "").split(path.delimiter).some((d) => {
    try {
      accessSync(path.join(d, cli), constants.X_OK);
      return true;
    } catch {
      return false;
    }
  });

test("a Session without agentScript reaches the agent guard, not the real Claude Code", async ({
  page,
}) => {
  test.skip(!installed("claude") || !installed("npx"), "Claude Code isn't installed here");
  const fh = await Fh.start();
  try {
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
    await form.getByRole("button", { name: "Start Session" }).click();

    await expect.poll(() => fh.guardCalls(), { timeout: 15_000 }).toEqual(["npx"]);
  } finally {
    await fh.dispose();
  }
});
