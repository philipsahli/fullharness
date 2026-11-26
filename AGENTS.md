## Agent skills

### Issue tracker

GitHub Issues on `philipsahli/fullharness`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Pull requests

Run `make check` before opening a PR; it runs exactly what CI runs. A PR is done when `gh pr checks <number>` is green. GitHub requires only the ubuntu job before a merge (#37), so report a PR as done only after that command shows the macOS job passed too.

A PR that changes Go code under `internal/` also runs `make stress` before it is pushed, and its description says what it ran and whether anything failed. It runs the tests that depend on the change many times under `-race`, which catches tests that fail on timing (#153).

## UI changes

A PR that changes what a screen shows or how it behaves updates the mockup, `docs/design/fh-mockup.html`, in the same branch. The PR description names the changed mockup screen and how to reach it (which Session or Feature to click). Show the user a screenshot of that screen in chat; it stays out of the repo. The PR is done when the Pull requests rule above is met, including the macOS job, and the user has reviewed the screen. A PR that Autopilot opens and that changes a screen stops at the **Screenshot review** Stop point until the user has reviewed it. A PR that touches `web/src` without a visible change says "No visible UI change" in its description.

## Documentation

Changes to `README.md`, `CONTRIBUTING.md` or user-facing files in `docs/` go through a pull request, never straight to `main`. The PR description says which code change triggered the doc update. Style rules are in `docs/documentation-style.md` and are checked at review time (see `CODING_STANDARDS.md`).
