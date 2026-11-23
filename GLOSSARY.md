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

**Inbox**:
The single list of everything waiting for the user's answer across all Sessions: permission requests and requests to start a Child Session.
_Avoid_: queue, notifications, approvals

**Session state**:
Where a Session is in its life: **Starting** (the Agent is launching, not ready yet), **Working** (producing output), **Waiting** (the Agent wants input from the user: a permission request is open, or it finished a turn the user hasn't looked at), **Idle** (quiet, not asking for anything), or **Exited** (the Agent process is gone). An Exited Session always carries its reason: fh quit, Agent crashed, or login needed. (Closing a Session removes it, so "closed" is never shown as a reason.)
_Avoid_: status, busy, blocked, error (an error is an Exited reason)

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
The first Stage of a Feature, and a Feature at that Stage. Until it has a Spec, an Idea is the named Session that Grill new idea started; fh, not the Issue tracker, keeps it. Linking the Spec its grilling wrote moves the Feature on to Spec.
_Avoid_: draft, proposal; and never call the Session itself the Idea once it has a Spec

**Stage**:
Where a Feature is in the Software factory: Idea, Spec, Tickets, Implementing, Review or Shipped. A Feature is at the earliest Stage of its open Tickets, and Shipped once its Spec is closed.
_Avoid_: status, phase, column

**Ticket**:
An issue in the Software factory that describes one buildable piece of work, typically produced by `/to-tickets`.
_Avoid_: task, story, card

**Startable ticket**:
A Ticket of a Spec, labelled `ready-for-agent`, with no open blockers, that nobody has started (no branch with commits, no Session started from it, no open PR); the only kind of Ticket a Session can be started from.
_Avoid_: ready ticket ("ready" means the `ready-for-agent` label alone), unblocked, available

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
