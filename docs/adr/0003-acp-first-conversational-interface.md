# The conversational interface is ACP-first, with a terminal fallback

fullharness talks to each Agent over the Agent Client Protocol (ACP) and draws its own conversation UI: messages, tool calls and permission requests, the same for every Agent. An Agent that can't speak ACP, or a feature ACP doesn't carry, falls back to the Agent's own TUI in an embedded terminal (PTY + xterm.js). We chose this over a terminal-only interface because ACP reports permission requests and turn ends as explicit events, which makes the Waiting Session state exact instead of a silence heuristic, and because the product is a graphical interface, not a terminal multiplexer.

## Considered Options

- **Terminal only**: shows each Agent exactly as it looks in a terminal and needs no per-Agent integration, but Waiting can only be guessed from output silence, and the UI is just a terminal in a browser.

## Consequences

- Copilot CLI (`--acp`) and OpenCode (`opencode acp`) speak ACP natively; Claude Code needs Zed's ACP adapter. Whether slash-command skills pass through ACP for each Agent is checked by the `prototype/acp-agents` prototype before the spec.
- Claude Code over ACP needs Node at runtime for the adapter; fullharness requires it only when that Agent is used and says so if it is missing.
- fullharness does not offer the ACP `fs` or `terminal` client capabilities in v1: Agents use their own tools, which keeps fullharness out of the code-execution path.
- An Agent definition is two commands: `acp` (start the ACP server) and `terminal` (the fallback TUI).
- fullharness owns the rendering of everything an Agent shows, so new Agent output types need UI work.
