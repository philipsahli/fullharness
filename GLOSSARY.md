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

**Inbox**:
The single list of every pending permission request across all Sessions, where the user answers them.
_Avoid_: queue, notifications, approvals

**Session state**:
Where a Session is in its life: **Working** (producing output), **Waiting** (the Agent wants input from the user), **Idle** (quiet, not asking for anything), or **Exited**.
_Avoid_: status, busy, blocked

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

**Stage**:
Where a Feature is in the Software factory: Idea, Spec, Tickets, Implementing, Review or Shipped. A Feature is at the earliest Stage of its open Tickets, and Shipped once its Spec is closed.
_Avoid_: status, phase, column

**Ticket**:
An issue in the Software factory that describes one buildable piece of work, typically produced by `/to-tickets`.
_Avoid_: task, story, card

**Startable ticket**:
A Ticket of a Spec, labelled `ready-for-agent`, with no open blockers; the only kind of Ticket a Session can be started from.
_Avoid_: ready ticket ("ready" means the `ready-for-agent` label alone), unblocked, available
