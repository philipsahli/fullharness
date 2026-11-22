# The conversational interface is ACP-first, with a terminal fallback

fullharness talks to each Agent over the Agent Client Protocol (ACP) and draws its own conversation UI: messages, tool calls and permission requests, the same for every Agent. An Agent that can't speak ACP, or a feature ACP doesn't carry, falls back to the Agent's own TUI in an embedded terminal (PTY + xterm.js). We chose this over a terminal-only interface because ACP reports permission requests and turn ends as explicit events, which makes the Waiting Session state exact instead of a silence heuristic, and because the product is a graphical interface, not a terminal multiplexer.

## Considered Options

- **Terminal only**: shows each Agent exactly as it looks in a terminal and needs no per-Agent integration, but Waiting can only be guessed from output silence, and the UI is just a terminal in a browser.

## Consequences

- Copilot CLI (`--acp`) and OpenCode (`opencode acp`) speak ACP natively; Claude Code needs the ACP adapter `@agentclientprotocol/claude-agent-acp` (formerly Zed's `@zed-industries/claude-code-acp`).
- Claude Code over ACP needs Node at runtime for the adapter; fullharness requires it only when that Agent is used and says so if it is missing.
- fullharness does not offer the ACP `fs` or `terminal` client capabilities in v1: Agents use their own tools, which keeps fullharness out of the code-execution path.
- An Agent definition is two commands: `acp` (start the ACP server) and `terminal` (the fallback TUI).
- Verified by the ACP spike (branch `prototype/acp-agents`, `FINDINGS.md`, 2026-11-22): over ACP v1 with `github.com/coder/acp-go-sdk`, all three Agents complete prompts, run slash-command skills (including skills in `.claude/skills/`), send permission requests and turn ends, and reopen through `session/load` with history replayed, all with empty client capabilities. No terminal fallback is needed for these three Agents in v1.
- fullharness sets the ACP session mode on every Session it starts. Otherwise Claude Code carries over the user's own permission mode (for example `auto`) and never asks, so nothing reaches the Inbox. fh's auto-approve toggle maps to each Agent's mode or option (Claude Code `default`/`auto`, Copilot `allow_all`).
- OpenCode runs commands without asking by default; fullharness must start it with `"permission": {"bash": "ask", "edit": "ask"}`.
- fullharness owns the rendering of everything an Agent shows, so new Agent output types need UI work.
