# fullharness (fh)

fullharness (fh) is a local app for developers who run several coding agents in parallel across their repos. It has two halves:

- **Sessions**
  - A chat view for each running agent (Claude Code, Copilot CLI, OpenCode).
  - A list sorted by state shows which Sessions are waiting for you.
  - One Inbox collects every pending permission request.
- **Software factory**
  - A board per repo shows where each Feature is in the flow from idea to shipped (Idea → Spec → Tickets → Implementing → Review → Shipped).
  - Buttons start a Session that runs the next skill, for example `/to-spec` or `/implement #42`.

> **Status:** early build. `fh` starts and opens an empty UI; Sessions and the Software factory come next (see [v1 scope and build order](#v1-scope-and-build-order)). The decisions so far are in [`GLOSSARY.md`](GLOSSARY.md) and [`docs/adr/`](docs/adr/). A clickable mockup with example data is in [`docs/design/fh-mockup.html`](docs/design/fh-mockup.html).

Planned after v1:

- **Roadmap**: Features from all repos ordered into Now / Next / Later. See [ADR 0005](docs/adr/0005-fh-writes-roadmap-group-to-tracker.md).
- **Readiness**: fixed checks plus an optional Agent Assessment that tell you whether a Spec and its Tickets are ready for `/implement-spec`.
- **Child Sessions**: an Agent asks fh to start another Agent in a new Worktree, and you approve each request in the Inbox. See [ADR 0006](docs/adr/0006-agents-start-child-sessions-via-fh-mcp.md).

## Why this exists

When you run several coding agents at once, each one sits in its own terminal, and you have to check them one by one to see which is waiting for you. fh puts every Session in one list sorted by state and collects pending permission requests in one Inbox. Its board shows, per repo, where each Feature is between idea and shipped.

## How it works (planned v1)

- **Binary** ([ADR 0001](docs/adr/0001-go-binary-with-browser-ui.md)):
  - One Go binary for macOS (Apple Silicon and Intel), installed from GitHub Releases or a Homebrew tap.
  - Running `fh` serves the UI on `127.0.0.1` and opens it in your browser.
  - A random token per launch protects the server.
- **Agent startup** ([ADR 0002](docs/adr/0002-host-sessions-do-not-observe.md)): fh starts every agent itself. It does not detect agents you started in another terminal.
- **Agent protocol** ([ADR 0003](docs/adr/0003-acp-first-conversational-interface.md)):
  - fh talks to agents over the [Agent Client Protocol](https://agentclientprotocol.com) (ACP), so permission requests and finished turns arrive as events.
  - A terminal fallback for agents that can't speak ACP is deferred until one is needed.
- **Starting a Session**: you start one in one of three ways: **Start Session** on a ticket that is ready and unblocked, the next-skill button on a Feature (for example "Write spec"), or **New Session**, where you pick any Project, Worktree and Agent and optionally type a starting prompt.
- **Worktrees**:
  - Each Session runs in its own git worktree.
  - fh can create the worktree when you start a Session, and offers to remove it once its branch is merged.
- **Tracker** ([ADR 0004](docs/adr/0004-orchestrate-skills-read-stage-from-tracker.md)):
  - fh reads each repo's specs and tickets from the issue tracker named in `docs/agents/issue-tracker.md`: GitHub Issues or local markdown under `.scratch/`.
  - fh never re-implements a skill. It starts Sessions that run them.

## v1 scope and build order

v1 contains:
- Projects you add explicitly
- Worktrees: list all, create, offer removal
- ACP Sessions for Claude Code, Copilot CLI and OpenCode
- Session states, the global Session list and ⌘K
- the Inbox, and notifications (browser, or native when no tab is open)
- reopening a Session through `session/load`
- the Software factory board for GitHub and local markdown
- "Start Session", the next-skill buttons, "Set up tracker" and New Session
- the localhost token
- a release through GitHub Releases and a Homebrew tap

Build order:
1. One Claude Code Session end-to-end in the browser
2. Permission cards and the Inbox
3. Copilot CLI and OpenCode
4. Session states, the Session list, ⌘K and notifications
5. Reopen
6. The Software factory board
7. The release build

Stack:
- **Server:** a Go server with packages `internal/{acp,session,worktree,tracker,server}`
- **UI:** a React + TypeScript + Vite app in `web/`, included in the binary
- **Storage:** config in `~/.config/fullharness/config.toml`, runtime state in one JSON file
- **Transport:** events over a WebSocket, commands over HTTP POST

Testing:
- **Automated tests** drive fh's own API against a fake ACP agent written in Go.
- **A real-agent smoke test** (the spike's checks) runs before each release.

Target: 10 Sessions at once without lag. v1 is done when it has been the only way to run agents for a week, across two real repos.

## Requirements (planned)

- macOS
- `git`, and `gh` (logged in) for repos that track work in GitHub Issues
- The agent CLIs you want to use: Claude Code, Copilot CLI and/or OpenCode
- Node, only if you use Claude Code (its ACP adapter is an npm package)

## Quickstart

There is no release yet, so you build fh from source. You need Go 1.27.2 or later, and Node 22.12 or later with npm.

```bash
git clone https://github.com/philipsahli/fullharness.git
cd fullharness
make build
./bin/fh
```

`fh` starts the server on `127.0.0.1:4815` and opens the UI in your browser. The UI shows empty Sessions, Software factory and Inbox areas for now. Stop fh with Ctrl+C.

Running `fh` again while it runs opens another tab on the running instance instead of starting a second server.

## Configuration / Usage

fh reads `~/.config/fullharness/config.toml`. fh never writes this file, so your comments stay.

```toml
port = 4815               # the port fh listens on
scan_folder = "~/work"    # where Add Project looks for repos to suggest
```

`scan_folder` suggestions go two levels deep, so both `~/work/<repo>` and `~/work/<org>/<repo>` show up. fh never adds a repo on its own.

### Projects

Add the git repos you work in with **+ Add Project** in the left rail. You can type a path, or pick a suggestion from `scan_folder`.

- A path inside a repo adds the whole repo.
- A linked worktree is refused; add its main repo instead, which lists all its worktrees.
- **Remove** takes a Project out of fh. The repo on disk stays as it is.

fh keeps your Projects in `~/.config/fullharness/projects.toml`. Only fh writes that file.

Each Project lists every git worktree:
- its branch, and whether it is the **main tree** or an **Agent worktree** (one fh didn't create);
- uncommitted files;
- commits ahead of and behind `origin/<default branch>`, or the local `main` if there's no remote.

fh never runs `git fetch`, so ahead/behind is as fresh as your last fetch. The worktree's tooltip shows when that was. The list refreshes every 10 seconds while a tab is open, or right away with **Refresh**.

Flags override the config file:

| Flag | Effect |
| --- | --- |
| `--port N` | Listen on port `N` instead. |
| `--no-open` | Print the URL instead of opening the browser. |
| `--dir PATH` | Use another config and state folder. |

Each launch creates a new token, so a tab left open across a restart stops working and tells you to run `fh` again.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for how to file issues and open pull requests.

Development follows [Matt Pocock's engineering skills](https://github.com/mattpocock/skills). [`AGENTS.md`](AGENTS.md) and [`docs/agents/`](docs/agents/) configure them: issues live in GitHub Issues on this repo, with the five default triage labels.

The ACP spike passed: all three agents work over ACP from Go (see [ADR 0003](docs/adr/0003-acp-first-conversational-interface.md)). Next step: `/to-spec` and `/to-tickets` for v1.

## Community & Support

Questions, bug reports and ideas go to [GitHub Issues](https://github.com/philipsahli/fullharness/issues). There is no chat or Discussions forum yet.

## License

MIT. See [`LICENSE`](LICENSE).
