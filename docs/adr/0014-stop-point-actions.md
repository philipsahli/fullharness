---
status: accepted, amends ADR-0008 and ADR-0009
---

# What the user does at the Approve spec and Screenshot review Stop points

ADR-0009 lists the Stop points but not what the user does at one. Today fh records a `spec-approval` Stop point and then waits, and Screenshot review isn't built at all. We grilled this on 2026-11-26.

## Decision

- **Approve spec** is an Inbox item with Approve and Reject.
  - Approve adds `ready-for-agent` to the Spec, a write already on ADR-0008's list.
  - Reject asks for a note, and fh posts the note as a comment on the Spec issue. Local markdown has no comments, so there fh adds the note to `spec.md`, quoted under a `## Review notes` heading at the end.
  - fh sends the note as a follow-up turn to the Autopilot Session that wrote the Spec.
  - If there is no such Session, or it has exited, fh starts a new Autopilot Session in the Project's main tree, where the Spec is written (a Feature has no Worktree before its Tickets), with `/to-spec`, the note and the Spec as input.
  - When the revision ends, the Feature is back at Approve spec.
- **Screenshot review** applies to a PR that changes a screen, unless its description says "No visible UI change". fh reads the PR's changed files from GitHub to decide. A PR changes a screen when it changes a file under one of the Project's **UI paths**, a Project setting the user edits in the Projects rail. Until the user sets them, they are `web/src/**`, which is right for fullharness itself (decided 2026-11-27 on #199).
  - The Agent saves a screenshot of each changed screen in its Worktree under `.fh/screenshots/`. fh adds `.fh/` to the repo's `.git/info/exclude`, so it never gets committed, and shows the files in the Inbox item. Nothing is uploaded.
  - Looks good records an Autopilot history event marked as the user's, and posts "Screen reviewed in fh" as a PR comment. The PR then moves on to the merge Stop point.
  - Request changes asks for a note. fh posts it as a PR comment and sends it as a follow-up turn to the PR's Autopilot Session. When that turn ends with a new screenshot, the PR is back at Screenshot review.
- After Approve, Reject, Looks good or Request changes, fh steps that Project at once, as turning the Autopilot switch on already does. It doesn't wait for the next one-minute tick.

This adds two writes to ADR-0008's list: **a comment on a Spec issue** (Reject) and **a comment on a PR** (Looks good, Request changes). Both are started by the user's click.

## Considered options

- **Approve only, no Reject.** Rejected: a wrong Spec would leave you to find and talk to the Session yourself.
- **fh takes the screenshot by running the PR's branch.** Rejected: fh would need to know how to reach every screen of every Project. The Agent that built the screen already knows.
- **Record Looks good only in fh, or as a `screen-reviewed` label.** Rejected: a comment shows who looked on GitHub without adding a label to every repo.

## Consequences

- `tracker.PR` gains the list of changed files, which fh fetches with `gh`.
- Autopilot's implement step has to tell the Agent where screenshots go, because projects other than fullharness don't have this repo's AGENTS.md.
- The "stuck after 2 rework rounds" Stop point stays in #113.
