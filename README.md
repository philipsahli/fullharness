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
scan_folder = "~/work"    # where Add Project looks for git repositories to suggest
```

`scan_folder` suggestions go two levels deep, so both `~/work/<name>` and `~/work/<org>/<name>` show up. fh never adds a Project on its own.

### Projects

Add the git repositories you work in as Projects with **+ Add Project** in the left rail. Type a path, or pick a suggestion from `scan_folder`.

- A path inside a git repository adds the whole repository.
- fh refuses a linked worktree.
- Add the linked worktree's main repository instead; its Project lists all its Worktrees.
- **Remove** takes a Project out of fh. The folder on disk stays as it is.

fh keeps your Projects in `~/.config/fullharness/projects.toml`. Only fh writes that file.

Each Project lists every git Worktree, with:

- its branch;
- a **main tree** tag for the Project's own folder, or an **Agent worktree** tag. fh shows an Agent worktree but never starts a Session in it or removes it. That's every Worktree fh didn't create itself;
- its uncommitted files;
- its commits ahead of and behind `origin/<default branch>`, or the local `main` (else `master`) when there's no remote.

fh never runs `git fetch`, so ahead/behind is as fresh as your last fetch. A Worktree's tooltip shows when that was. The list refreshes every 10 seconds while a tab is open, or right away with **Refresh**.

### Sessions

Start one with **+ New Session** in the left rail. Pick:

- a Project;
- a Worktree: a new one on a new branch, or a free existing one;
- the Agent (Claude Code for now);
- an optional starting prompt;
- **Auto-approve**, off by default.

A Worktree that already has a Session, or is an Agent worktree, is greyed out with the reason.

A new Worktree goes to `<project>.worktrees/<branch>/`, next to the Project's folder. Its branch starts from `origin/<default branch>`, or the local default branch when there's no remote.

The conversation shows your messages, the Agent's reply as it streams in, and each tool call as a card you can expand. While the Agent works, **Cancel** replaces **Send**.

When the Agent wants to edit a file or run a command, it asks first:

- The request shows as a card in its Session, with **Allow**, **Deny** and, when the Agent offers it, **Always allow**.
- The **Inbox** lists every open request across Sessions, oldest first.
- The Inbox count stays visible in the tab bar.
- Answering in the Inbox or in the Session is the same answer; the request leaves both.
- A Session with an open request is **Waiting**.
- **Cancel** ends the turn and answers its open requests as cancelled.

fh starts Claude Code in its asking mode (`default`), even if your own Claude Code setting is `auto`. With **Auto-approve** on, fh starts it in `auto` instead: Claude Code decides by itself what's safe and asks less. Anything it still asks goes to the Inbox. fh never uses the mode that skips asking altogether.

Each Session shows its state:

| State | Meaning |
| --- | --- |
| Starting | The Agent is launching. |
| Working | The Agent is on a turn. |
| Waiting | The Agent wants you: a permission request is open, or a turn ended and you haven't looked yet. |
| Idle | Nothing to do: you've seen the last turn, or cancelled it. |
| Exited | The Agent is gone: Agent crashed, login needed, or fh quit. |

A turn counts as seen once its conversation is open in a visible tab. If a Session exits with **login needed**, it shows the command to run in your terminal (`claude auth login`, `copilot login` or `opencode auth login`).

The Sessions list covers all Projects and puts what needs you first:

1. Waiting, longest waiting first;
2. Working and Starting;
3. Idle;
4. Exited, faded.

When a Session starts Waiting and you're not looking at it, fh notifies you:

- Through an fh tab that may show notifications, as a browser notification. Click **Turn on notifications** in the header once to allow it.
- Clicking a browser notification opens its Session.
- Otherwise (no fh tab open, or none allowed to notify), as a macOS notification.
- At most one per Session per minute.
- None for the Session you're looking at.

Press **⌘K** (Ctrl+K off macOS) to jump to a Session by words from its title, Project or Agent, or to start a New Session.

Quitting fh stops every Agent. When fh starts again, your Sessions are listed as Exited ("fh quit"):

- **Reopen** restarts the Agent in the same Worktree, with the same auto-approve setting.
- The Agent then replays the conversation (ACP `session/load`).
- Reopen isn't offered for an Agent that can't reload Sessions, or one no longer configured. Use the Agent's own resume command instead.
- Reopen is refused while another Session uses that Worktree.
- **Close** removes a Session from fh and frees its Worktree. For a Session whose Agent still runs, fh asks first and stops the Agent.
- The Agent keeps its own history; fh deletes the Session's log.

A Session whose Agent exited also frees its Worktree for a new Session.

### Software factory

The **Software factory** tab shows one Project's Features, each under its Stage: Idea, Spec, Tickets, Implementing, Review, Shipped. Pick the Project at the top. Open a Feature to see its Tickets with their label, Stage and blockers; **Startable** marks the ones an Agent can pick up: labelled `ready-for-agent`, no open blockers, and not started yet (no branch with commits, no open PR).

fh reads the Issue tracker named in the Project's `docs/agents/issue-tracker.md`. Without that file, a `.scratch/` folder means local markdown, and a GitHub remote means GitHub. Local markdown follows the files [Matt Pocock's skills](https://github.com/mattpocock/skills) write:

- `.scratch/<feature>/spec.md` is a Feature's Spec; `.scratch/<feature>/issues/<NN>-<slug>.md` are its Tickets.
- A Ticket's `**Status:**` line is its label (`ready-for-agent`, `needs-triage`, …).
- `**Status:** done` (or `closed`) closes a Ticket.
- No skill writes that line yet: add it yourself when a Ticket is done.
- A `**Status:** done` line in `spec.md` closes the Spec and ships the Feature.
- `**Blocked by:** 01, 02` names blocking Tickets in the same folder: the number starting each comma-separated item counts, so `01 (the form), 03` means 01 and 03.

A Feature's Stage is the earliest Stage of its open Tickets. A Ticket is Implementing once a branch `<N>-<slug>…` (e.g. `1-form` for `01-form.md`) has commits ahead of the Project's base: `origin/<default branch>`, or the local default branch without a remote. A Feature whose Tickets are all closed but whose Spec is open waits in Review until you close the Spec.

For GitHub, fh reads issues, labels, sub-issues, blocking links and open PRs through your own `gh` login (`brew install gh`, then `gh auth login`):

- A Spec is an issue with sub-issues, or one that other issues name with `Part of #N` or a `## Parent` section. An issue without Tickets counts as a Spec only while open and labelled `ready-for-agent`.
- A closed issue without Tickets is left off the board.
- Issues labelled `wayfinder:*` are left out.
- Blockers are GitHub's issue dependencies. Without them, fh reads a `Blocked by: #N` line or a `## Blocked by` section's `- #N` list in the body.
- An open PR puts its Ticket in Review.
- A PR belongs to a Ticket by its branch `<N>-…`, or by `Closes`, `Fixes` or `Resolves #N` in its body.
- fh reuses a Project's GitHub results for 50 seconds, so several tabs share one `gh` call.
- **Refresh** reads GitHub again at once.

A local markdown Project with a GitHub remote gets its open PRs the same way, matched by branch `<N>-<slug>…`.

A board with no Specs says so, with a hint to run `/to-spec` in a Session.

Other trackers (GitLab, Jira, …) show a "not supported in v1" note.

### Agents

fh knows three Agents. Each needs its CLI installed and logged in:

| Agent | Needs | Started as |
| --- | --- | --- |
| Claude Code | `claude`, Node (`node`, `npx`) | its ACP adapter, `@agentclientprotocol/claude-agent-acp`, at a version fh has tested |
| Copilot CLI | `copilot` (`brew install --cask copilot-cli`) | `copilot --acp --stdio` |
| OpenCode | `opencode` (`brew install opencode`) | `opencode acp` |

If a CLI is missing, New Session greys that Agent out and shows the install command.

fh makes each built-in Agent ask before it acts, whatever your own Agent settings say. With **Auto-approve**, it uses the Agent's own way of asking less:

| Agent | Asks before acting | Auto-approve |
| --- | --- | --- |
| Claude Code | mode `default` | mode `auto` |
| Copilot CLI | `allow_all` off | `allow_all` on |
| OpenCode | `OPENCODE_PERMISSION={"bash":"ask","edit":"ask"}` | `{"bash":"allow","edit":"allow"}` |

Two exceptions:

- An Agent you add in `config.toml` follows its own settings. fh says so in the conversation, and auto-approve has no effect on it.
- `env` in `config.toml` comes last, so setting `OPENCODE_PERMISSION` there replaces fh's value, for asking and for auto-approve alike.

fh removes `ANTHROPIC_API_KEY` from Claude Code's environment, so Claude Code uses your Claude login rather than billing an API key.

OpenCode over ACP doesn't use the model `opencode run` picks. Set one in your OpenCode config (`~/.config/opencode/opencode.jsonc`), or a prompt can hang with no reply:

```json
{ "model": "github-copilot/claude-haiku-5.5" }
```

To change how an Agent starts, or to add one, use `config.toml`:

```toml
[agents.claude]
command = ["npx", "-y", "@agentclientprotocol/claude-agent-acp@0.109.1"]
env = { ANTHROPIC_API_KEY = "your-api-key" }   # only if you do want API billing

[agents.myagent]                               # any ACP Agent
command = ["my-agent", "--acp"]
```

### Logs

fh logs to the terminal and to `~/.config/fullharness/fh.log`, which it starts over at 10 MB. `fh.log` never contains the launch token or the text of your messages or the Agent's replies.

Each Session's Agent writes its own error output to `~/.config/fullharness/logs/<session-id>.log`, unfiltered. A Session whose Agent crashed shows the last 20 lines of it.

Only you can read these files.

Flags override the config file:

| Flag | Effect |
| --- | --- |
| `--port N` | Listen on port `N` instead. |
| `--no-open` | Print the URL instead of opening the browser. |
| `--dir PATH` | Use another config and state folder. |
| `--verbose` | Log at debug level. |

Each launch creates a new token, so a tab left open across a restart stops working and tells you to run `fh` again.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for how to file issues and open pull requests.

Development follows [Matt Pocock's engineering skills](https://github.com/mattpocock/skills). [`AGENTS.md`](AGENTS.md) and [`docs/agents/`](docs/agents/) configure them: issues live in GitHub Issues on this repo, with the five default triage labels.

The ACP spike passed: all three agents work over ACP from Go (see [ADR 0003](docs/adr/0003-acp-first-conversational-interface.md)). Next step: `/to-spec` and `/to-tickets` for v1.

## Community & Support

Questions, bug reports and ideas go to [GitHub Issues](https://github.com/philipsahli/fullharness/issues). There is no chat or Discussions forum yet.

## License

MIT. See [`LICENSE`](LICENSE).
