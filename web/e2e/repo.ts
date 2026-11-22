import { execFileSync } from "node:child_process";
import { mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

// Git without the developer's own config (signing, hooks, default branch).
const gitEnv = {
  ...process.env,
  GIT_CONFIG_GLOBAL: "/dev/null",
  GIT_CONFIG_NOSYSTEM: "1",
  GIT_AUTHOR_NAME: "fh e2e",
  GIT_AUTHOR_EMAIL: "fh-e2e@example.com",
  GIT_COMMITTER_NAME: "fh e2e",
  GIT_COMMITTER_EMAIL: "fh-e2e@example.com",
};

// tempRepo creates a git repo on branch main with one commit. The path is
// resolved because macOS temp folders sit behind a symlink and git reports
// the real path.
export function tempRepo(name: string): string {
  const dir = path.join(realpathSync(mkdtempSync(path.join(tmpdir(), "fh-e2e-repo-"))), name);
  execFileSync("git", ["init", "-q", "-b", "main", dir], { env: gitEnv });
  writeFileSync(path.join(dir, "README.md"), "hello\n");
  execFileSync("git", ["add", "README.md"], { cwd: dir, env: gitEnv });
  execFileSync("git", ["commit", "-q", "-m", "init"], { cwd: dir, env: gitEnv });
  return dir;
}

// tempFolder creates an empty folder that is not a git repo.
export function tempFolder(): string {
  return realpathSync(mkdtempSync(path.join(tmpdir(), "fh-e2e-plain-")));
}
