---
status: accepted, amends spec #1 (Testing Decisions)
---

# Quality gates: one `make check` that CI runs, plus one browser smoke test

Every change passes the same gate locally and in CI. `make check` runs three targets:
- `make lint`: golangci-lint (default linters, pinned version), a gofmt check, govulncheck, `tsc --noEmit`, ESLint (typescript-eslint and react-hooks rules) and a Prettier check;
- `make test`: `go test -race ./...`;
- `make e2e`: a Playwright smoke test in Chromium against the built binary.

GitHub Actions only calls these `make` targets, so the commands live in one place. We chose this because Agents write most of fh's code: a gate they can run themselves before opening a PR catches more than review does.

## Decisions

- **Runners.** PRs run on `ubuntu-latest`. Pushes to `main` also run on `macos-latest`, which catches macOS-only problems (the `open` command, file modes, later `osascript`). On a private repo, macOS minutes count 10× against the free quota. Since the repo went public (#38), PRs run on macOS too.
- **Race detector always.** fh's core job is concurrent: many Agent processes, WebSockets, launch races. `-race` is on locally too, not only in CI.
- **One exception: the load test (#7).** It checks spec #1's target of 10 Sessions without lag: 10 Sessions × 20 chunks a second, with each chunk reaching a tab within 100 ms at p95. The race detector slows everything by 2–10×, which would make that number meaningless, so the test is built only without `-race` (`//go:build !race`). `make test` runs it once more after the `-race` pass. It prints p95 and p99 on every run.
- **Time budget.** The whole PR workflow finishes in under 3 minutes. A slow test is a bug to fix, for example a shutdown that waits for a close handshake.
- **Browser smoke test.** This amends spec #1, which left the React UI to hand checks in v1. The tab's restart and reconnect logic lives only in the UI, so `web/e2e/` holds Playwright tests for it:
  - the shell loads through the token URL;
  - the restart message appears after fh restarts.

  Chromium only, on every PR. A later UI ticket may add one smoke case for its main path; the UI stays mostly checked by hand.
- **No coverage threshold.** Spec #1's "Covered through the seam" list is what review checks. A percentage would mostly measure code behind the seam, which the tests deliberately leave alone.
- **Dependencies.** Dependabot opens weekly PRs for gomod, npm and github-actions, with minor and patch updates grouped per ecosystem. govulncheck runs in `make lint`. There is no npm audit gate, because it mostly flags dev-only dependencies.
- **Real-Agent smoke test stays manual.** It needs logged-in Claude Code, Copilot CLI and OpenCode, and real tokens that cost money. It runs as `make smoke` on the maintainer's Mac before tagging, never in Actions (#16).
- **No git hooks.** Hooks would run in every Agent Worktree and slow down each commit. AGENTS.md tells Agents to run `make check` before opening a PR, and CI catches what they skip.

## Merge gate

The repo is private on GitHub's free plan, which has no branch protection, so checks can't be required. Until the repo goes public with the release pipeline (#16), the rule is a convention, written in CONTRIBUTING and AGENTS.md: merge a PR only when `gh pr checks` is green. Agents check it before they report a PR as done. Once the repo is public, branch protection makes the CI checks required.

## Considered Options

- **macOS for every run**: closest to the target OS, at about 10× the minutes on a private repo. Rejected for PRs and kept for `main`.
- **No browser tests in v1** (spec #1 as written): rejected, because the restart logic would have no test at all.
- **A full Playwright suite**: rejected for v1. It would cost the most to keep working while the UI changes every ticket.
- **Pre-commit hooks (lefthook)**: rejected for the reasons above.
- **A coverage threshold**: rejected for the reason above.

## Consequences

- Contributors need Node 22.12 or later for ESLint, Prettier and Playwright. Playwright downloads Chromium the first time `make e2e` runs.
- `CODING_STANDARDS.md` lists the lint rules that need judgement. Mechanical rules go into the linters, not into prose.
- CI reads the Go version from `go.mod` and the Node version from `web/.nvmrc`.
