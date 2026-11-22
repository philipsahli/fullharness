# Contributing to fullharness

Thanks for taking a look. fh is in an early build: it starts and opens an empty UI, and the v1 features are tracked as issues.

## Building and running

You need Go 1.27.2 or later, and Node 22.12 or later with npm.

| Command | What it does |
| --- | --- |
| `make build` | Builds the UI into `web/dist` and a binary at `bin/fh` that embeds it. |
| `make dev` | Starts the Vite dev server on `127.0.0.1:5173`, proxied to a Go server on port 4816 with its own state folder `.dev/`, and opens the UI. |
| `make test` | Runs the Go tests and the UI typecheck. |

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
- Link the issue the PR closes, for example `Closes #42`.
- Name decisions with the terms in [`GLOSSARY.md`](GLOSSARY.md). Record new decisions as an ADR in [`docs/adr/`](docs/adr/).

## Changing the docs

- Changes to `README.md`, `CONTRIBUTING.md` or user-facing files in `docs/` go through a PR, never straight to `main`.
- Say in the PR description which change triggered the doc update.
- Follow [`docs/documentation-style.md`](docs/documentation-style.md).

## Review

We review changes with `/code-review`, which checks them against [`CODING_STANDARDS.md`](CODING_STANDARDS.md).
