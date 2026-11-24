# fullharness

fullharness is a graphical interface with two halves: a conversational interface to several coding agents running in parallel across a developer's repos, and a graphical interface to the Software factory, the skill flow from idea to shipped code.

## Workspace

**Agent**:
A coding-agent CLI that fullharness can launch, such as Claude Code, Copilot CLI or OpenCode.
_Avoid_: tool, assistant, AI, harness

**Project**:
One git repository the user has added to fullharness.
_Avoid_: repo (in UI and docs), workspace

**Worktree**:
A git worktree of a Project; the working directory a Session runs in. An **Agent worktree** is one an Agent created itself (for example `/implement-spec`'s subagents); fullharness shows it but never starts a Session in it or removes it.
_Avoid_: checkout, branch (a Worktree has a branch, but is not one)

## Sessions

**Session**:
One running conversation of one Agent, inside exactly one Worktree, started by fullharness.
_Avoid_: chat, run, terminal, instance

**Child Session**:
A Session that another Session's Agent asked fullharness to start, and the user approved. The Session that asked is its **Parent Session**. A Child Session runs in its own new Worktree of the same Project and belongs to its Parent's Feature unless the user changes it.
_Avoid_: subagent (that runs inside one Session), spawned session, sub-session

**Loose Session**:
A Session that belongs to no Feature, because it was started outside the Software factory. The user can attach it to a Feature or turn it into a new Idea; until then it stays Loose.
_Avoid_: orphan, ad-hoc session, scratch session

**Inbox**:
The single list of everything waiting for the user's answer across all Sessions: permission requests and requests to start a Child Session.
_Avoid_: queue (that is a Queued message), notifications, approvals

**Session state**:
Where a Session is in its life: **Starting** (the Agent is launching, not ready yet), **Working** (producing output), **Waiting** (the Agent wants input from the user: a permission request is open, or it finished a turn the user hasn't looked at), **Idle** (quiet, not asking for anything), or **Exited** (the Agent process is gone). An Exited Session always carries its reason: fh quit, Agent crashed, or login needed. (Closing a Session removes it, so "closed" is never shown as a reason.)
_Avoid_: status, busy, blocked, error (an error is an Exited reason)

**Queued message**:
A message the user sent while the Agent was Starting, Working or Waiting. fullharness sends it as the Session's next turn once the current turn ends, never into the running turn. A Session has at most one; sending again adds to it.
_Avoid_: pending message, draft; and never call the Inbox a queue

**Notification**:
A desktop alert that a Session started Waiting while nobody was looking at it: shown by an fh tab, or natively by macOS when no tab can. It points to the Session; answering happens there or in the Inbox.
_Avoid_: alert, ping; and never "notifications" for the Inbox

**Close (a Session)**:
Remove a Session from fullharness and free its Worktree; a live Session's Agent is stopped first. The Agent keeps its own history.
_Avoid_: delete, kill, end

## Software factory

**Software factory**:
The flow of skills (grilling, prototype, spec, tickets, implement, review, retro) that takes work from idea to shipped, recorded in a Project's Issue tracker.
_Avoid_: pipeline, backlog, workflow

**Spec**:
The issue (or `spec.md`) describing a whole Feature, produced by `/to-spec`; its Tickets hang off it. A Spec is never a Ticket.
_Avoid_: PRD, epic, plan

**Issue tracker**:
Where a Project's Software factory records specs and Tickets: GitHub Issues or local markdown files, as declared by the Project.
_Avoid_: backlog, board

**Feature**:
One piece of work moving through the Software factory, from Idea to Shipped; matches a spec and its Tickets in the Issue tracker.
_Avoid_: effort (that is wayfinder's term), initiative, work item, epic

**Idea**:
The first Stage of a Feature, and a Feature at that Stage. An Idea is an issue in the Issue tracker, whether or not anyone has grilled it yet. It outlives any Session that grills it. Linking the Spec its grilling wrote moves the Feature on to Spec.
_Avoid_: draft, proposal; and never call a grilling Session the Idea

**Stage**:
Where a Feature is in the Software factory: Idea, Spec, Tickets, Implementing, Review or Shipped. A Feature is at the earliest Stage of its open Tickets, and Shipped once its Spec is closed. With every Ticket closed but the Spec still open, it is at Review. A closed Spec means Shipped even if a Ticket is still open; that is a mistake to show, not a different Stage.
_Avoid_: status, phase, column

**Ticket**:
An issue in the Software factory that describes one buildable piece of work, typically produced by `/to-tickets`.
_Avoid_: task, story, card

**Startable ticket**:
A Ticket of a Spec, labelled `ready-for-agent`, with no open blockers, that nobody has started (no branch with commits, no Session started from it, no open PR); the only kind of Ticket a Session can be started from.
_Avoid_: ready ticket ("ready" means the `ready-for-agent` label alone), unblocked, available

**Autopilot**:
fullharness starting the next skill for a Feature by itself, for Features in the Roadmap group Now of a Project where the user turned it on. It acts when a Session's turn ends or when the Issue tracker shows new Startable work, and waits at Stop points.
_Avoid_: pipeline, automation, bot, agent (an Agent is a CLI)

**Stop point**:
A place where Autopilot waits for the user instead of going on: an answer to a grilling question, approving a Spec, merging a PR, a permission request, or a Feature that failed review twice. Each one is a Waiting Session or an Inbox item.
_Avoid_: gate, checkpoint, pause

**Autopilot history**:
What Autopilot did for one Feature, in order: each skill it started, how each turn ended, each Stop point it reached, each skill that failed to start. It is kept across restarts.
_Avoid_: log (fh.log is fh's own log), audit trail, timeline

## Planning

**Roadmap**:
The user's ordering of Features across all Projects into the Roadmap groups Now, Next and Later, without dates.
_Avoid_: timeline, backlog, plan

**Roadmap group**:
One of Now, Next or Later. For a Feature with a Spec it is recorded on the Spec in the Issue tracker; an Idea's group exists only in fullharness.
_Avoid_: bucket, column, priority, lane

**Readiness**:
Whether a Spec and its Tickets can be handed to `/implement-spec` and left to run: either Ready or Not ready, decided by fixed checks.
_Avoid_: score, quality, health, "plan" (say Spec and its Tickets)

**Assessment**:
An Agent's written verdict on a Spec's Readiness, posted to the Issue tracker by the `plan-readiness` skill. It is Stale once the Spec or one of its Tickets changes after it was posted.
_Avoid_: review (that is `/code-review`), audit, evaluation
