import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
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

// tempRepo creates a git repo on branch main with one commit, in a new temp
// folder, or in parent when given (emptied first). The path is resolved
// because macOS temp folders sit behind a symlink and git reports the real
// path.
export function tempRepo(name: string, parent?: string): string {
  if (parent) {
    rmSync(parent, { recursive: true, force: true });
    mkdirSync(parent, { recursive: true });
  }
  const base = parent ?? mkdtempSync(path.join(tmpdir(), "fh-e2e-repo-"));
  const dir = path.join(realpathSync(base), name);
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

// tempRepoWith creates a repo like tempRepo, with extra files committed,
// e.g. a .scratch tracker.
export function tempRepoWith(name: string, files: Record<string, string>, parent?: string): string {
  const dir = tempRepo(name, parent);
  for (const [rel, content] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dir, rel)), { recursive: true });
    writeFileSync(path.join(dir, rel), content);
  }
  execFileSync("git", ["add", "."], { cwd: dir, env: gitEnv });
  execFileSync("git", ["commit", "-q", "-m", "tracker"], { cwd: dir, env: gitEnv });
  return dir;
}

// withOrigin adds an origin remote to a repo; nothing is fetched or pushed.
export function withOrigin(dir: string, url: string): string {
  execFileSync("git", ["remote", "add", "origin", url], { cwd: dir, env: gitEnv });
  return dir;
}

// withRemoteBranch makes origin/<branch> exist locally, as if the branch had
// been pushed and fetched, without a network.
export function withRemoteBranch(dir: string, branch: string): string {
  execFileSync("git", ["update-ref", `refs/remotes/origin/${branch}`, "HEAD"], {
    cwd: dir,
    env: gitEnv,
  });
  return dir;
}
