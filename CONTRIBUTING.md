# Contributing to fullharness

Thanks for taking a look. fh is in the design phase, so most contributions right now are issues, ADR discussions and doc fixes.

## Building and running

There is no code yet, so there is nothing to build or run. The [README](README.md#v1-scope-and-build-order) lists the planned build order.

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
- Link the issue the PR closes, for example `Closes #42`.
- Name decisions with the terms in [`GLOSSARY.md`](GLOSSARY.md). Record new decisions as an ADR in [`docs/adr/`](docs/adr/).

## Changing the docs

- Changes to `README.md`, `CONTRIBUTING.md` or user-facing files in `docs/` go through a PR, never straight to `main`.
- Say in the PR description which change triggered the doc update.
- Follow [`docs/documentation-style.md`](docs/documentation-style.md).

## Review

We review changes with `/code-review`, which checks them against [`CODING_STANDARDS.md`](CODING_STANDARDS.md).
