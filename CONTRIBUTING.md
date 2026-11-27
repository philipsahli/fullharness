# Contributing to fullharness

Thanks for taking a look. fh is in an early build: it starts and opens an empty UI, and the v1 features are tracked as issues.

## Building and running

You need Go 1.27.2 or later, and Node 22.12 or later with npm.

| Command | What it does |
| --- | --- |
| `make build` | Builds the UI into `web/dist` and a binary at `bin/fh` that embeds it. |
| `make install` | Runs `make build` and copies `bin/fh` to `~/.local/bin` (set `PREFIX` for another folder), so `fh` runs from any terminal. |
| `make dev` | Starts the Vite dev server on `127.0.0.1:5173`, proxied to a Go server on port 4816 with its own state folder `.dev/`, and opens the UI. The UI reloads on web changes; fh rebuilds and restarts on Go changes, and the open tab reloads by itself. A failed build is printed, and the last good build keeps running. |
| `make lint` | Runs golangci-lint, a gofmt check and govulncheck on the Go code, and `tsc`, ESLint and a Prettier check on the UI. |
| `make test` | Runs the Go tests with the race detector. |
| `make e2e` | Builds `bin/fh` and runs the Playwright smoke tests in Chromium. The first run downloads Chromium (about 150 MB). |
| `make check` | Runs `lint`, `test` and `e2e`, the same as CI. Run it before you open a PR. |
| `make stress` | Runs the Go tests that depend on what you changed against `origin/main`, 5 times each at `-cpu 1,2` with the race detector, to catch tests that fail on timing. A change outside `internal/` runs every package; `STRESS_PKGS=./...` does too. CI runs it nightly on `main` 20 times each (`STRESS_COUNT=20`) and opens a `needs-triage` issue when a test fails. |
| `make e2e-visual` | Takes screenshots of nine screens in light and dark (the Software factory board with Autopilot badges in two states, the Roadmap, the Inbox with Approve spec, the Inbox with Screenshot review, and the Feature page with its flow graph) and compares them with your last baselines. Mac only; CI never runs it. |
| `make e2e-visual-update` | Takes new baselines in `web/e2e/__visual__/`, which git ignores. |
| `make smoke` | Runs the release smoke test against the real Claude Code, Copilot CLI and OpenCode. It needs all three installed and logged in, and spends real tokens, so CI never runs it. See [Releasing](README.md#releasing). |

The lint tool versions are pinned in `tools/go.mod` (Go) and `web/package.json` (UI). `cd web && npm run format` fixes Prettier findings.

The Go tests in `internal/app` start fh in-process against a temp folder and drive it over HTTP and WebSocket, the same way the UI does. New tests go through that API, not against unexported code.

## Filing an issue

Open an issue on [GitHub Issues](https://github.com/philipsahli/fullharness/issues). Describe what you expected and what happened, or the problem an idea solves.

We triage new issues with these labels:

| Label | Meaning |
| --- | --- |
| `needs-triage` | A maintainer still needs to evaluate it. |
| `needs-info` | We're waiting on the reporter for more information. |
| `ready-for-agent` | Fully specified; a coding agent can pick it up. |
| `ready-for-human` | Needs a person to implement it. |
| `wontfix` | We won't act on it. |

## Opening a pull request

- Branch from `main` and open a PR against it.
- Run `make check` before you open it. If it changes Go code under `internal/`, run `make stress` too and say in the PR what it ran and whether anything failed.
- Merge only when the PR's checks are green (`gh pr checks <number>`). GitHub blocks the merge only on the ubuntu job; the macOS job isn't required yet (#37).
- If the PR's branch is checked out in an fh Worktree, merge with `gh pr merge <number> --squash`, then run `git push origin --delete <branch>`. `--delete-branch` also deletes the Worktree, along with its ignored files, and its Sessions can no longer reopen. Remove the Worktree in fh instead.
- Link the issue the PR closes, for example `Closes #42`.
- If the PR changes a stylesheet or a screen, run the pixel tests ([ADR 0012](docs/adr/0012-ui-look-follows-the-mockup.md)). Take baselines on `main` with `make e2e-visual-update`, then run `make e2e-visual` on your branch. A failed screen leaves an image of the difference under `web/test-results/`. If every difference is one you meant, run `make e2e-visual-update` on your branch to accept it.
- Name decisions with the terms in [`GLOSSARY.md`](GLOSSARY.md). Record new decisions as an ADR in [`docs/adr/`](docs/adr/).

## Changing the docs

- Changes to `README.md`, `CONTRIBUTING.md` or user-facing files in `docs/` go through a PR, never straight to `main`.
- Say in the PR description which change triggered the doc update.
- Follow [`docs/documentation-style.md`](docs/documentation-style.md).

## Review

We review changes with `/code-review`, which checks them against [`CODING_STANDARDS.md`](CODING_STANDARDS.md).
