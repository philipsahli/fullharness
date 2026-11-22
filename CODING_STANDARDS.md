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

No code standards yet. Add rules here when `/retro` or a review finds a judgement call worth enforcing; turn mechanical rules into automated checks instead.
