## Agent skills

### Issue tracker

GitHub Issues on `philipsahli/fullharness`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Pull requests

Run `make check` before opening a PR; it runs exactly what CI runs. A PR is done when `gh pr checks <number>` is green. Until the repo is public (#16), GitHub can't require checks, so report a PR as done only after that command passes.

## Documentation

Changes to `README.md`, `CONTRIBUTING.md` or user-facing files in `docs/` go through a pull request, never straight to `main`. The PR description says which code change triggered the doc update. Style rules are in `docs/documentation-style.md` and are checked at review time (see `CODING_STANDARDS.md`).
