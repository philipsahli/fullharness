# Coding standards

`/code-review` reads this file when it checks a change against the repo's standards. Agents writing the change don't need to read it first; the review catches violations.

## Documentation

Changes to `README.md`, `CONTRIBUTING.md` or user-facing files in `docs/` follow [`docs/documentation-style.md`](docs/documentation-style.md).

Internal docs are exempt from its README structure and audience rules, but not from its anti-slop and command-verification rules:

- `docs/adr/` (decision records)
- `docs/agents/` (skill configuration)
- `docs/design/` (mockups)
- `GLOSSARY.md`

## Code

Mechanical rules live in the linters, which `make lint` runs (see [ADR 0007](docs/adr/0007-quality-gates.md)):

- Go: [`.golangci.yml`](.golangci.yml) (golangci-lint's standard linters and gofmt), plus govulncheck
- UI: [`web/eslint.config.js`](web/eslint.config.js) and Prettier ([`web/.prettierrc.json`](web/.prettierrc.json))

This file holds only the rules a linter can't check:

- **An ignored error says why.** When code drops an error on purpose, write `_ =` and a short comment giving the reason, for example `_ = resp.Body.Close() // only the status matters`. A linter suppression (`//nolint`) needs the same kind of comment.
- **Tests go through the API.** Tests drive fh over HTTP and WebSocket, as the UI does, and never call unexported code (spec #1, Testing Decisions). The exception is a package of pure logic with no I/O, such as `internal/diff`: it can have table tests of its exported functions, in a `_test` package, when edge cases are too many to reach over HTTP (spec #81).

Add a rule here when `/retro` or a review finds a judgement call worth enforcing. Turn mechanical rules into linter config instead.
