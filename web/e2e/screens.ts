import { expect, type Page } from "@playwright/test";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { deflateSync } from "node:zlib";
import { Fh } from "./fh";
import { fakeGhPath, setFakeGhPRs } from "./gh";
import { tempRepo, withOrigin } from "./repo";

// Screenshot review (#226): helpers shared by its e2e and visual tests.

// png encodes a w×h RGB picture, colour(x, y) per pixel, as a PNG file: a
// screenshot the fake Agent "saved", the same bytes on every run.
export function png(w: number, h: number, colour: (x: number, y: number) => number[]): Buffer {
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    raw[y * (w * 3 + 1)] = 0; // no filter
    for (let x = 0; x < w; x++) raw.set(colour(x, y), y * (w * 3 + 1) + 1 + x * 3);
  }
  const chunk = (type: string, data: Buffer) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(body));
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr.set([8, 2, 0, 0, 0], 8); // 8-bit RGB
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function crc32(b: Buffer): number {
  let c = ~0;
  for (const byte of b) {
    c ^= byte;
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return ~c >>> 0;
}

// boardShot is a small picture of a changed screen: a rail, a header and
// three cards, enough to tell the preview from an empty box.
export const boardShot = () =>
  png(320, 200, (x, y) => {
    if (x < 70) return [232, 236, 234]; // rail
    if (y < 26) return [255, 255, 255]; // header
    const card = [86, 152, 218].find((left) => x >= left && x < left + 56);
    if (card !== undefined && y > 44 && y < 150) return y < 54 ? [106, 47, 214] : [255, 255, 255];
    return [243, 245, 244];
  });

export type ReviewSetup = { fh: Fh; PATH: string; repo: string; gate: string };

// startScreenshotReview starts fh for a GitHub Feature, Login, with one
// Ticket, #11, whose PR #5 will change web/src/Board.tsx. parent fixes where
// the repo goes, for pixel tests. Dispose its fh in a finally.
export async function startScreenshotReview(parent?: string): Promise<ReviewSetup> {
  const PATH = fakeGhPath(
    [
      {
        number: 10,
        title: "Login",
        labels: ["roadmap:now", "ready-for-agent"],
        subIssues: [11],
      },
      { number: 11, title: "Form", labels: ["ready-for-agent"], parent: 10 },
    ],
    { prViews: { 5: { files: ["web/src/Board.tsx", "internal/app/app.go"] } } },
  );
  const repo = withOrigin(tempRepo("shop", parent), "git@github.com:someone/shop.git");
  const gate = path.join(tempGateDir(), "gate");
  // The implement turn holds until the PR and the screenshot exist, as when
  // the skill makes them; the review turn then ends done at once.
  const fh = await Fh.start({
    env: { PATH },
    agentScript: { default: [{ waitFile: gate }, { say: "Opened PR #5." }] },
    verdict: "done",
  });
  return { fh, PATH, repo, gate };
}

// reachScreenshotReview adds the Project, turns Autopilot on, and plays the
// implement turn: the Agent saves board.png and opens PR #5. Autopilot's
// review then ends done, so the PR waits at Screenshot review. It returns
// the Inbox item.
export async function reachScreenshotReview(page: Page, { fh, PATH, repo, gate }: ReviewSetup) {
  await page.goto(fh.launchURL);
  await expect(page.locator("[data-connection]")).toHaveAttribute("data-connection", "open");
  const projects = page.getByRole("region", { name: "Projects" });
  await projects.getByRole("button", { name: "+ Add Project" }).click();
  await projects.getByLabel("Path to a git repository").fill(repo);
  await projects.getByRole("button", { name: "Add", exact: true }).click();
  await expect(projects).toContainText("main tree");
  await projects.getByRole("checkbox", { name: "Autopilot" }).click();

  // Autopilot starts /implement #11 in a new Worktree on 11-form.
  const worktree = path.join(path.dirname(repo), `${path.basename(repo)}.worktrees`, "11-form");
  await expect.poll(() => existsSync(worktree), { timeout: 30_000 }).toBe(true);
  const shots = path.join(worktree, ".fh", "screenshots");
  mkdirSync(shots, { recursive: true });
  writeFileSync(path.join(shots, "board.png"), boardShot());
  setFakeGhPRs(PATH, [
    {
      number: 5,
      headRefName: "11-form",
      state: "OPEN",
      url: "https://github.com/someone/shop/pull/5",
    },
  ]);
  writeFileSync(gate, "");

  await page.getByRole("tab", { name: /^Inbox/ }).click();
  const item = page.getByRole("group", { name: "Screenshot review: Login" });
  await expect(item.getByRole("button", { name: "Show board.png full size" })).toBeVisible({
    timeout: 30_000,
  });
  return item;
}

function tempGateDir(): string {
  const dir = path.join(
    tmpdir(),
    `fh-e2e-gate-${process.pid}-${Math.random().toString(36).slice(2)}`,
  );
  mkdirSync(dir, { recursive: true });
  return dir;
}
