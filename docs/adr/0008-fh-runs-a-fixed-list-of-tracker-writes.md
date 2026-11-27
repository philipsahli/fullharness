---
status: accepted, amends ADR-0004 and ADR-0005, amended by ADR-0009 and ADR-0014
---

# fullharness runs a fixed list of Issue tracker writes itself

ADR-0005 allowed one direct write, the Roadmap group. Everything else still meant starting an Agent Session, which is why a Feature stalled once its PR was open: merging, closing the Spec and fixing a triage label all happened in GitHub or a terminal. We want the Software factory loop to run from end to end inside fh, so fh now runs these writes itself, through `gh`:

| Write | Where in fh |
|---|---|
| Merge a PR once every check on it has passed (`gh pr merge` with squash if the repo allows it, else merge, else rebase; no `--delete-branch`, #187) | Ticket row on the Feature page |
| Close a Spec once all its Tickets are closed | Feature page, Review Stage |
| Reopen a Spec that was closed while a Ticket is still open | Feature page warning |
| Add or remove `ready-for-agent`, `needs-info`, `needs-triage` | Ticket row |
| Set the Roadmap group (`roadmap:now|next|later`, from ADR-0005) | Roadmap |
| Create an Idea issue (label `idea`) | Board, Roadmap, File as Idea on a Retro |
| Add the `idea` label to an existing issue | Once per Project, for issues titled `Idea: …` |
| Close an Idea issue with "Continued in Spec #N" when its Spec is linked | Linking an Idea to its Spec |
| Comment "Screen reviewed in fh" on a PR (`gh pr comment`, from ADR-0014) | Looks good on a Screenshot review Inbox item |
| Comment on a PR with the user's note (`gh pr comment`, from ADR-0014) | Request changes on a Screenshot review Inbox item |
| Comment on a Spec issue with the user's note (from ADR-0014) | Reject on an Approve spec item in the Inbox |

Every write is started by the user, and the ones that change code or close work (merge, close or reopen a Spec) ask for confirmation first. Stage is still derived from the tracker, never stored: a write changes the tracker, and the board shows the new Stage on its next read.

Merge needs every check, not only the ones GitHub requires: today only the ubuntu job is required (#37), and AGENTS.md counts a PR as done only when the macOS job has passed too. fh never merges on a pending or failing check.

## Considered options

- **Every write through a skill in a Session** (ADR-0004 as written). Rejected: an Agent turn for a label or a merge is slow, and ADR-0005 already made the same call for the Roadmap.
- **Read-only fh with links out to GitHub.** Rejected: the Software factory is meant to be first-class in fh, and every link out breaks the loop.

## Consequences

- An Idea is an issue in the Issue tracker, recognised by the `idea` label, not only a named Session in fh's state. It survives closing its Session and restarting fh. ADR-0004's "only local state is Idea-stage Features" no longer holds once the Ideas Spec is built.
- A Retro's findings stay in its output until the user files one with File as Idea; the Retro never creates issues by itself.
- Writing specs, tickets and code stays inside skills (ADR-0004). This list is for state changes a person would otherwise click in GitHub.
- For a local-markdown tracker, fh offers only the writes the format defines (the `Roadmap:` line, the `Status:` line of a Spec or Ticket for the label writes, closing a Spec, a new Idea file in `ideas/`, and the note of a Reject, quoted under a `## Review notes` heading at the end of `spec.md`, since local markdown has no comments). A Spec without a `Status:` or `Roadmap:` line gets one, in bold, after its heading. Merge needs a GitHub remote.
- Adding a write to this list needs a new ADR.
