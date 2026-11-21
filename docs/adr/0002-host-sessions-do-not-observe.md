# fullharness hosts Sessions; it does not observe Agents started elsewhere

fullharness starts every Session itself, owning its PTY, so it always knows which Sessions exist, what state they are in, and can switch to any of them. Agents the user starts in their own terminal are invisible to fullharness. We chose this over observing because each Agent stores sessions in its own undocumented format, so detection would mean fragile per-Agent parsing, and "switching" to a terminal fullharness doesn't own can't be done reliably.

## Considered Options

- **Observe**: discover running Agents via processes, worktrees and each Agent's session files. Rejected for the reasons above.
- **Host, plus adopt read-only**: deferred; possible later on top of hosting.

## Consequences

- Agent processes end when fullharness quits. fullharness stores each Session's ACP session ID and offers "Reopen" after a restart where the Agent supports `session/load`; otherwise the Agent's own resume command recovers the conversation.
- Hosting applies to Agents, not Worktrees: fullharness lists every git worktree of a Project, including Agent worktrees it did not create, because git reports them reliably.
