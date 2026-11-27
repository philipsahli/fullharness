import { test, expect } from "@playwright/test";
import { Fh } from "./fh";

// The header view tabs follow the ARIA tabs pattern (#247): ArrowRight and
// ArrowLeft select the next and previous tab and move focus to it, wrapping at
// both ends, and only the selected tab is in the Tab order.
test("arrow keys move between the view tabs and only the selected tab is in the Tab order", async ({
  page,
}) => {
  const fh = await Fh.start();
  try {
    await page.goto(fh.launchURL);
    await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
    const tablist = page.getByRole("tablist", { name: "Views" });
    const tab = (name: string) => tablist.getByRole("tab", { name, exact: false });
    const names = ["Sessions", "Software factory", "Roadmap", "Inbox"];

    const expectSelected = async (selected: string) => {
      await expect(tab(selected)).toBeFocused();
      for (const name of names) {
        const on = name === selected;
        await expect(tab(name)).toHaveAttribute("aria-selected", String(on));
        await expect(tab(name)).toHaveAttribute("tabindex", on ? "0" : "-1");
      }
    };

    await tab("Sessions").focus();
    await page.keyboard.press("ArrowRight");
    await expectSelected("Software factory");
    await page.keyboard.press("ArrowRight");
    await expectSelected("Roadmap");
    await page.keyboard.press("ArrowRight");
    await expectSelected("Inbox");
    // Wraps from the last tab to the first, and back.
    await page.keyboard.press("ArrowRight");
    await expectSelected("Sessions");
    await page.keyboard.press("ArrowLeft");
    await expectSelected("Inbox");
    await page.keyboard.press("ArrowLeft");
    await expectSelected("Roadmap");
  } finally {
    await fh.dispose();
  }
});
