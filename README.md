# fullharness (fh)

fullharness is a local app for running several coding agents in parallel across your repos. It has two halves:

- **Sessions**: a chat view for each running agent (Claude Code, Copilot CLI, OpenCode). A list sorted by state shows which Sessions are waiting for you, and one Inbox collects every pending permission request.
- **Software factory**: a board per repo that shows where each Feature is in the flow from idea to shipped (Idea → Spec → Tickets → Implementing → Review → Shipped). Buttons start a Session that runs the next skill, for example `/to-spec` or `/implement #42`.

> **Status:** design phase. There is no code yet. The decisions so far are in [`GLOSSARY.md`](GLOSSARY.md) and [`docs/adr/`](docs/adr/). A clickable mockup with example data is in [`docs/design/fh-mockup.html`](docs/design/fh-mockup.html).

Planned after v1: a **Roadmap** (Features from all repos ordered into Now / Next / Later), **Readiness** (fixed checks plus an optional Agent Assessment that tell you whether a Spec and its Tickets are ready for `/implement-spec`), and **Child Sessions** (an Agent asks fh to start another Agent in a new Worktree; you approve each request in the Inbox). See [ADR 0005](docs/adr/0005-fh-writes-roadmap-group-to-tracker.md) and [ADR 0006](docs/adr/0006-agents-start-child-sessions-via-fh-mcp.md).

## How it works (planned v1)

- One Go binary for macOS (Apple Silicon and Intel), installed from GitHub Releases or a Homebrew tap. Running `fh` serves the UI on `127.0.0.1` and opens it in your browser. A random token per launch protects the server. See [ADR 0001](docs/adr/0001-go-binary-with-browser-ui.md).
- fh starts every agent itself. It does not detect agents you started in another terminal. See [ADR 0002](docs/adr/0002-host-sessions-do-not-observe.md).
- fh talks to agents over the [Agent Client Protocol](https://agentclientprotocol.com) (ACP), so permission requests and finished turns arrive as events. An agent that can't speak ACP runs in an embedded terminal instead. See [ADR 0003](docs/adr/0003-acp-first-conversational-interface.md).
- You start a Session in one of three ways: **Start Session** on a ticket that is ready and unblocked, the next-skill button on a Feature (for example "Write spec"), or **New Session**, where you pick any Project, Worktree and Agent and optionally type a starting prompt.
- Each Session runs in its own git worktree. fh can create the worktree when you start a Session, and offers to remove it once its branch is merged.
- fh reads each repo's specs and tickets from the issue tracker named in `docs/agents/issue-tracker.md`: GitHub Issues or local markdown under `.scratch/`. fh never re-implements a skill; it starts Sessions that run them. See [ADR 0004](docs/adr/0004-orchestrate-skills-read-stage-from-tracker.md).

## Requirements (planned)

- macOS
- `git`, and `gh` (logged in) for repos that track work in GitHub Issues
- The agent CLIs you want to use: Claude Code, Copilot CLI and/or OpenCode
- Node, only if you use Claude Code (its ACP adapter is an npm package)

## Working on this repo

Development follows [Matt Pocock's engineering skills](https://github.com/mattpocock/skills). [`AGENTS.md`](AGENTS.md) and [`docs/agents/`](docs/agents/) configure them: issues live in GitHub Issues on this repo, with the five default triage labels.

Next step: a throwaway prototype (`prototype/acp-agents`) that checks whether the three agents can be driven over ACP from Go. After that, `/to-spec` and `/to-tickets`.

## License

MIT
