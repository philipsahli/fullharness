---
status: accepted, amends ADR-0009
---

# fullharness works out each Feature's Autopilot status and pushes it to the UI

Autopilot runs, but the UI only shows its on/off switch per Project. You can't see what it is doing with a Feature or where it waits for you, so nobody has tested it end to end in the app. We grilled the Autopilot UI on 2026-11-26 and decided how the UI learns what Autopilot is doing.

## Decision

- Each Feature has exactly one **Autopilot status** (GLOSSARY.md): off, idle, running a skill, waiting at a Stop point, failed to start a skill, or done. Done means the Spec is closed and Autopilot's retro turn ended done.
- fh works it out in Go, in one function over the Autopilot history, the Issue tracker and the Sessions, with Go tests. The UI only draws the result.
- fh sends a new `autopilot` WebSocket event whenever a Feature's Autopilot status changes. The snapshot carries every Autopilot status, so a fresh tab needs no extra request.
- Moving a Feature out of Now does not stop a running turn. The turn finishes, and Autopilot starts nothing new for that Feature, because `Step` only walks Features in Now. Until the turn ends, its Autopilot status stays running, and the Roadmap card says "Session running outside Now".
- When that turn ends, the Project is no longer held by it, whatever the turn's verdict. Today a turn that ends anywhere but done holds the whole Project (`holding` in `internal/autopilot/autopilot.go`), so a Feature left at a Stop point outside Now would stop every Feature in Now. The left-behind Session stays open until the user closes it or moves the Feature back. While the turn still runs, the Project stays held, so a Project never has two Autopilot turns at once.
- Moving a Feature into Now never turns Autopilot on. If its Project has Autopilot off, the card shows "Autopilot off" with a link to the switch.
- Each Autopilot status has one colour, designed in the mockup with a soft tint and a glow, and used the same way on the badge, the flow graph, the Roadmap card and the Inbox item (ADR-0012).
- The UI shows a change with motion, and a running Feature with a live pulse and its current tool call. The new screens get `make e2e-visual` baselines, with animations paused.

## Considered options

- **Work the status out in the UI from `/api/autopilot` events.** Rejected: the rules would sit in TypeScript, which has no unit tests here, and every screen would repeat them.
- **Poll `/api/autopilot` every 60 seconds, like the board.** Rejected: a change would show up to a minute late.
- **Stop the Session when its Feature leaves Now.** Rejected: it cuts off work in the middle of a turn.

## Consequences

- The Feature page, the Feature card, the Roadmap and the Inbox read one Autopilot status, so they can't disagree.
- The flow graph on the Feature page is the same SVG the Loop view (#114) will show full-screen.
