---
status: accepted, amended by ADR-0005
---

# fullharness orchestrates the skills and never re-implements them; Stage is read from the Issue tracker

The Software factory GUI starts Sessions that run the existing skills (`/grill-with-docs`, `/to-spec`, `/to-tickets`, `/implement`, ...) and shows each Feature's Stage, but it never re-implements a skill as forms or wizards, and it keeps no database of Stages. Stage is derived from the Project's Issue tracker, as declared in `docs/agents/issue-tracker.md` (GitHub or local markdown in v1), plus live Sessions and PRs. We chose this so fullharness benefits automatically when the skills change, and so the Issue tracker stays the single record of truth that both the skills and fullharness read.

## Consequences

- The only local state fullharness keeps for the factory is Idea-stage Features (named Sessions without a spec yet) and their link to the spec once `/to-spec` creates it.
- Tracker detection reads the `# Issue tracker: ...` heading, which is prose written for agents; inference (`.scratch/`, a GitHub remote) is the fallback.
