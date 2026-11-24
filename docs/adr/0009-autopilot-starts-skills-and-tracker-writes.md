---
status: accepted, amends ADR-0008
---

# Autopilot starts skills and tracker writes itself, and stops only at Stop points

Every Software factory step in fh is started by the user, so a Feature stands still whenever nobody clicks. Autopilot (#98) lets fh move a Feature on by itself: when a Session's turn ends, or a tracker refresh shows new Startable work, fh picks the next skill from the Feature's Stage and starts a Session for it.

## Decision

- Autopilot works only on Features in the Roadmap group Now, in Projects where the user turned it on. Moving a Feature into Now is how the user steers it.
- The next skill comes from a fixed table in Go, keyed by Stage, which the Board and ⌘K also read. Stage is still derived from the Issue tracker (ADR-0004).
- At most one Autopilot Session runs per Project at a time.
- Autopilot may start the ADR-0008 label writes (`ready-for-agent`, `needs-info`, `needs-triage`) itself. This amends ADR-0008's "every write is started by the user".
- Autopilot waits at these Stop points:
  - a grilling question (the Session is Waiting),
  - approving a Spec (an Inbox item; Approve adds `ready-for-agent`),
  - merging a PR (fh never merges without the user's confirmation),
  - permission requests (Autopilot Sessions never auto-approve),
  - a Feature that failed review after 2 rework rounds.
- fh records every Autopilot action per Feature in its local state, so the Loop view can draw it after a restart.

## History

fh keeps an Autopilot history per Feature (#112): each skill Autopilot started, how each turn of its Sessions ended, each Stop point, and each skill that failed to start. A Session that exits before its turn ends is a Stop point with reason `exited`.

Planned with #113, not recorded yet: label writes, rework rounds, and an Approve the user clicks, marked as the user's, so the Loop view (#114) can draw the end of a wait.

- A Stop point or a failed start that Autopilot meets again on its next look is recorded once, until something else happens to that Feature or Ticket. A failed start keeps its first error; fh.log has every retry.
- Nothing is trimmed: trimming at a closed Spec or to the last N events would lose the drawing of a finished Feature. Revisit when `state.json` passes 1 MB or `/api/autopilot` is slow to load.

## Considered options

- **Buttons only** (today). Rejected: the user asked for fh to work without being pushed for every step.
- **Let Autopilot merge once all checks pass.** Rejected: merge changes `main`, and ADR-0008 keeps it a confirmation.
- **Every Feature, not only Now.** Rejected: it would start work the user hasn't chosen, and spend money on it.

## Consequences

- Closing a Spec and merging stay user confirmations, as in ADR-0008.
- Autopilot needs the Queued message (#93) to send follow-up turns without racing the Agent.
- An unclear turn end needs a classifier; see ADR-0010.
