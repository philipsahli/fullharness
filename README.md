# fullharness (fh)

fullharness (fh) is a local Mac app for developers who run several coding agents in parallel across their repos: one browser tab shows every agent's Session, its permission requests, and where each Feature stands.

[![CI](https://github.com/philipsahli/fullharness/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/philipsahli/fullharness/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/philipsahli/fullharness)](https://github.com/philipsahli/fullharness/releases/latest)
[![License: MIT](https://img.shields.io/github/license/philipsahli/fullharness)](LICENSE)

## Why this exists

When you run several coding agents at once, each one sits in its own terminal, and you have to check them one by one to see which is waiting for you. fh puts every Session in one list sorted by state and collects pending permission requests in one Inbox. Its board shows, per repo, where each Feature is between idea and shipped.

## What fh does

fh has two halves:

- **Sessions**
  - A chat view for each running agent (Claude Code, Copilot CLI, OpenCode).
  - A list sorted by state shows which Sessions are waiting for you.
  - One Inbox collects every pending permission request.
- **Software factory**
  - A board per repo shows where each Feature is in the flow from idea to shipped (Idea → Spec → Tickets → Implementing → Review → Shipped).
  - Buttons start a Session that runs the next skill, for example `/to-spec` or `/implement #42`.

> **Status:** v1 is in progress (see [v1 scope and build order](#v1-scope-and-build-order)). The decisions so far are in [`GLOSSARY.md`](GLOSSARY.md) and [`docs/adr/`](docs/adr/). A clickable mockup with example data is in [`docs/design/fh-mockup.html`](docs/design/fh-mockup.html).

Planned after v1:

- **Roadmap**: Features from all repos ordered into Now / Next / Later. See [ADR 0005](docs/adr/0005-fh-writes-roadmap-group-to-tracker.md).
- **Readiness**: fixed checks plus an optional Agent Assessment that tell you whether a Spec and its Tickets are ready for `/implement-spec`.
- **Child Sessions**: an Agent asks fh to start another Agent in a new Worktree, and you approve each request in the Inbox. See [ADR 0006](docs/adr/0006-agents-start-child-sessions-via-fh-mcp.md).

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

## Requirements

- macOS
- `git`, and `gh` (logged in) for repos that track work in GitHub Issues
- The agent CLIs you want to use: Claude Code, Copilot CLI and/or OpenCode
- Node, only if you use Claude Code (its ACP adapter is an npm package)

## Quickstart

Install fh with Homebrew and start it:

```bash
brew install philipsahli/tap/fh
fh
# level=INFO msg="fh started" port=4815 ...   (and your browser opens the UI)
```

`fh` starts the server on `127.0.0.1:4815` and opens the UI in your browser: add a Project, start a Session with an Agent, and open the Software factory for its board. Stop fh with Ctrl+C.

Running `fh` again while it runs opens another tab on the running instance instead of starting a second server.

### Without Homebrew

From the [latest release](https://github.com/philipsahli/fullharness/releases/latest), download `checksums.txt` and the archive for your Mac:

- `fh_darwin_arm64.tar.gz` for Apple Silicon
- `fh_darwin_amd64.tar.gz` for Intel

Then, in the folder you downloaded them to (shown for Apple Silicon):

```bash
shasum -a 256 -c checksums.txt --ignore-missing   # fh_darwin_arm64.tar.gz: OK
tar -xzf fh_darwin_arm64.tar.gz fh
xattr -d com.apple.quarantine fh                  # once; see below
mkdir -p ~/.local/bin && mv fh ~/.local/bin/
fh --version                                      # fh 0.1.0
```

The fh binaries are not signed by an Apple developer ID, so macOS blocks a downloaded `fh` until you remove its quarantine flag with `xattr`. If `xattr` prints `No such xattr`, the file was never flagged (for example, you downloaded it with `curl`), and you can go on. Homebrew removes the flag for you.

### From source

You need Go 1.27.2 or later, and Node 22.12 or later with npm.

```bash
git clone https://github.com/philipsahli/fullharness.git
cd fullharness
make install
fh
```

`make install` builds fh and copies it to `~/.local/bin` (set `PREFIX` for another folder).

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

A Worktree fh created has a **Remove** button. It runs `git worktree remove` and keeps the branch. The button works only when all of these hold:

- its branch is merged into the default branch, or it has no commits of its own;
- it has no uncommitted or untracked files;
- no Session is linked to it, live or Exited. Close the Session first.

fh counts a branch as merged when the default branch has its work in any of these forms:

- the same commits, for example after a fast-forward or a merge commit;
- copies of each commit, as GitHub's **Rebase and merge** leaves them;
- one commit with all of the branch's changes, as GitHub's **Squash and merge** leaves it.

When the button doesn't work, it's greyed out and its tooltip says why. Click it to see the reason as text.

Before removing, fh asks you to confirm. The question says whether the branch is merged or has no commits of its own. When fh can't tell those two apart, because the branch has no reflog, it says that everything on the branch is already in the default branch. It also lists the ignored files at the top of the Worktree, such as `.env` or `node_modules`, because `git worktree remove` deletes them with the folder.

fh never removes a Worktree on its own, and never removes the main tree or an Agent worktree.

### Sessions

Start one with **+ New Session** in the left rail. Pick:

- a Project;
- a Worktree: a new one on a new branch, or a free existing one;
- the Agent (Claude Code for now);
- an optional starting prompt;
- **Auto-approve**, off by default.

A Worktree that already has a Session, or is an Agent worktree, is greyed out with the reason.

A new Worktree goes to `<project>.worktrees/<branch>/`, next to the Project's folder. Its branch starts from `origin/<default branch>`, or the local default branch when there's no remote.

The conversation shows your messages, the Agent's reply as it streams in, and each tool call as a card you can expand.

While the Agent is starting, working, or waiting on a permission request, you can still write. The button reads **Queue**:

- Your message waits as the Queued message and goes out as the next turn when this one ends.
- Sending again adds to the Queued message.
- **Edit** takes it back into the composer, **Remove** drops it, and **Send now** cancels the turn and sends it at once.
- **Cancel** sits next to **Queue** once the Agent is ready.
- If you cancel, the turn fails, or the Agent exits, the Queued message comes back into the composer, so it is never lost.

When the Agent wants to edit a file or run a command, it asks first:

- The request shows as a card in its Session, with **Allow**, **Deny** and, when the Agent offers it, **Always allow**.
- The **Inbox** lists every open request across Sessions, oldest first.
- The Inbox count stays visible in the tab bar.
- Answering in the Inbox or in the Session is the same answer; the request leaves both.
- A Session with an open request is **Waiting**.
- **Cancel** ends the turn, answers its open requests as cancelled, and puts a Queued message back in the composer.
- If you cancel before your message has gone out to the Agent, the turn ends at once and the Agent never sees that message. The conversation still shows it, followed by "Turn cancelled."

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

A Feature's Stage is the earliest Stage of its open Tickets. A Ticket is Implementing once a branch `<N>-<slug>…` (e.g. `1-form` for `01-form.md`) has commits ahead of the Project's base (`origin/<default branch>`, or the local default branch without a remote), or once you started a Session from it, even if that Session has Exited. A Feature whose Tickets are all closed but whose Spec is open waits in Review until you close the Spec.

For GitHub, fh reads issues, labels, sub-issues, blocking links and open PRs through your own `gh` login (`brew install gh`, then `gh auth login`):

- A Spec is an issue with sub-issues, or one that other issues name with `Part of #N` or a `## Parent` section. An issue without Tickets counts as a Spec only while open and labelled `ready-for-agent`.
- A closed issue without Tickets is left off the board.
- Issues labelled `wayfinder:*` are left out.
- Blockers are GitHub's issue dependencies. Without them, fh reads a `Blocked by: #N` line or a `## Blocked by` section's `- #N` list in the body.
- Spec, Ticket and blocker numbers link to their GitHub issues. A blocker in another repo, or one fh can't read, shows as plain text.
- An open PR puts its Ticket in Review.
- A PR belongs to a Ticket by its branch `<N>-…`, or by `Closes`, `Fixes` or `Resolves #N` in its body.
- fh reuses a Project's GitHub results for 50 seconds, so several tabs share one `gh` call.
- **Refresh** reads GitHub again at once.

A local markdown Project with a GitHub remote gets its open PRs the same way, matched by branch `<N>-<slug>…`.

A board with no Specs says so, with a hint to run `/to-spec` in a Session.

Other trackers (GitLab, Jira, …) show a "not supported in v1" note.

#### Starting Sessions from the board

Each button opens New Session, prefilled with a Worktree, an Agent and a starting prompt that runs one of [Matt Pocock's skills](https://github.com/mattpocock/skills). Change anything before you click **Start Session**.

| Button | Where | Skill | Worktree |
| --- | --- | --- | --- |
| **Start Session** | a Startable Ticket | `implement` | new, on the Ticket's branch, e.g. `42-login-form` |
| **Make tickets** | a Feature at Spec | `to-tickets` | the main tree |
| **Implement spec** | a Feature at Tickets, Implementing or Review | `implement-spec` | new, on the Spec's branch, which the prompt names as the integration branch |
| **Retro** | a Shipped Feature | `retro` | the main tree |
| **Grill new idea** | the board header | `grill-with-docs` | new, on `idea/<name>`; the dialog asks for the Idea's name |
| **Write spec** | the board header | `to-spec` | the main tree |
| **Set up tracker** | a Project without a tracker | `setup-matt-pocock-skills` | the main tree |

A Session started from a Ticket or Feature shows that link in its header. A Session started from a Ticket makes that Ticket Implementing; an Implement spec Session does the same for every open Ticket of its Feature that isn't in Review. Both hold once the Session has Exited. Closing the Session removes the link, so a start you abandon leaves the Ticket Startable again.

An Idea is a Feature at the Idea Stage. Until it has a Spec, the Idea is the named Session that **Grill new idea** started, and it waits in the Idea column:

- The Session's starting prompt always begins with the Agent's `grill-with-docs` command. An Agent that doesn't have the skill can't start an Idea.
- Each Idea gets its own Worktree, so two Ideas grilled at once don't overwrite each other's glossary edits.
- Once a Spec appears that wasn't on the board when the Idea started, and no other Idea has it, the Idea's card offers **Link #N**. One click links them.
- The linked Spec names its Idea, and fh keeps the link in `state.json` across restarts.
- Closing the Idea's Session removes the Idea.

Each Agent names skills its own way, so fh looks for the skill among the slash commands the Agent last reported: by its exact name, or as `<plugin>:<name>` (Claude Code lists plugin skills as `mattpocock-skills:implement`).

- An Agent without the skill is greyed out in New Session, with a note such as "`/implement` isn't installed for Copilot CLI".
- fh learns an Agent's slash commands from its first Session and keeps them across restarts. Until then the Agent can still be picked, with the plain skill name (`/implement`), and the dialog warns once: start any Session of that Agent so fh learns its commands.

Copilot CLI and OpenCode see Matt's skills only once they're installed where each Agent looks for skills:

| Agent | Skills folder |
| --- | --- |
| Copilot CLI | `~/.copilot/skills` |
| OpenCode | `~/.claude/skills` (it reads Claude Code's folder) |

For Copilot CLI:

```bash
git clone https://github.com/mattpocock/skills.git ~/src/mattpocock-skills
mkdir -p ~/.copilot/skills
ln -s ~/src/mattpocock-skills/skills/engineering/* ~/.copilot/skills/
ls ~/.copilot/skills   # code-review  implement  implement-spec  …  to-spec  to-tickets
# then start a new Session of that Agent, so fh learns its commands
```

For OpenCode, link the same folders into `~/.claude/skills`. Claude Code then lists each skill twice, as `implement` and as `mattpocock-skills:implement`.

#### Autopilot

Tick **Autopilot** under a Project in the left rail, and fh moves that Project's Features in Roadmap **Now** on by itself ([ADR 0009](./docs/adr/0009-autopilot-starts-skills-and-tracker-writes.md)). The switch stays on across restarts.

- A Feature is in Now when its Spec has the label `roadmap:now` (GitHub), or a `**Roadmap:** now` line in `spec.md` (local markdown).
- An approved Spec (`ready-for-agent`) without Tickets gets `/to-tickets` in the main tree.
- A Startable Ticket gets `/implement` in a new Worktree, as **Start Session** does.
- A Ticket whose PR is open gets `/code-review` as the next message in its own Autopilot Session, or in a new Session in its Worktree once that one has exited.
- A Feature Autopilot worked on gets `/retro` once its Spec is closed.

fh runs one Autopilot Session per Project at a time. It looks for new work every minute, and again when a turn of its own Session ends. It waits for you at these Stop points:

- The Agent asks you something: answer in the Session.
- The [turn-end classifier](#turn-end-classifier) can't tell how the turn ended: answer in the Session.
- The Agent stopped before its turn ended: reopen the Session, or close it.
- A permission request: Autopilot Sessions never auto-approve.
- A Spec that isn't approved yet: add `ready-for-agent`.
- A PR to merge: fh never merges.
- A Spec to close: fh never closes one.

A Stop point stays one across restarts. Closing the waiting Session hands the Project back to Autopilot.

Autopilot starts Claude Code; pick another Agent next to the Autopilot box. fh keeps what it started in `state.json`, so a restart doesn't start a step twice.

fh also keeps a history per Feature in `state.json`: each skill Autopilot started, how each of its turns ended, each Stop point it reached, and each skill that failed to start. To read it, open `/api/autopilot?project=<path>` in the browser where fh runs (shortened here):

```json
{ "features": [ { "feature": "login", "events": [
  { "kind": "skill", "ticket": "01", "skill": "implement", "session": "uacsy6t46u" },
  { "kind": "classified", "ticket": "01", "skill": "implement", "verdict": "asking", "by": "rule:permission" },
  { "kind": "stop", "ticket": "01", "skill": "implement", "reason": "asking" } ] } ] }
```

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

### Turn-end classifier

When a turn of an Autopilot Session ends, fh decides how it ended: when no fixed rule says, fh asks a model whether the Agent is asking you something, is done, or is stuck ([ADR 0010](./docs/adr/0010-turn-end-classifier.md)). Only Autopilot Sessions are classified.

- By default fh runs `claude -p --model claude-haiku-5-5` on your Claude login.
- Any `claude` command runs without `ANTHROPIC_API_KEY`, as fh's Claude Code Agents do.
- `claude` must be on your `PATH` for that.
- If the call fails, fh stops and asks you.

To use another model, set a command in `config.toml`. fh sends it the Agent's message on stdin, adds `--system-prompt` with the question, and reads output in the shape `claude -p --output-format json` prints:

```toml
[classifier]
name = "claude sonnet"                         # how the measurements record it
command = ["claude", "-p", "--model", "claude-sonnet-5-5", "--output-format", "json", "--tools", ""]
```

fh records each call in `classifier.jsonl` next to `state.json`: the message, the verdict, the latency and the cost. The messages come from your repos and go only to the configured command. To see the totals, open `/api/classifier` in the browser where fh runs (shortened here):

```json
{ "provider": "claude -p claude-haiku-5-5", "calls": 12, "overrides": 1, "meanLatencyMs": 2100, "costUsd": 0.0014 }
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

### Releasing

Pushing a version tag runs [`.github/workflows/release.yml`](.github/workflows/release.yml). GoReleaser ([`.goreleaser.yaml`](.goreleaser.yaml)) then:

- builds `fh` for Apple Silicon and Intel;
- attaches both archives and `checksums.txt` to a GitHub Release;
- updates the `fh` cask in [`philipsahli/homebrew-tap`](https://github.com/philipsahli/homebrew-tap).

The binaries are unsigned in v1. Go's linker ad-hoc signs the Apple Silicon build, which is enough for it to run, and the cask removes the quarantine flag on install.

Before each release, on your Mac:

1. Check that CI is green on `main`. The STATUS column must show `✓`:

   ```bash
   gh run list --workflow ci.yml --branch main --limit 1
   # STATUS  TITLE           WORKFLOW  BRANCH  EVENT  ID           ELAPSED  AGE
   # ✓       Merge PRs f...  CI        main    push   45144662355  2m29s    about 3 hours ago
   ```

2. Log in to Claude Code, Copilot CLI and OpenCode, and set a model in your OpenCode config (see [Agents](#agents)).
3. Run `make smoke` from `main`. It must end with `smoke: PASS`.
4. Tag and push:

   ```bash
   git tag v0.1.0 && git push origin v0.1.0
   # * [new tag]         v0.1.0 -> v0.1.0
   ```

5. Watch the release build until it completes with `success`:

   ```bash
   gh run watch $(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')
   # ...
   # ✓ Run Release (45144662355) completed with 'success'
   ```

6. Check the install. The version must be the tag without its `v`:

   ```bash
   brew update && brew install philipsahli/tap/fh && fh --version
   # ...
   # 🍺  fh was successfully installed!
   # fh 0.1.0
   ```

`make smoke` runs the ACP spike's checks against each real Agent, in a fresh scratch git repo, using fh's built-in Agent definitions:

| Check | Passes when |
| --- | --- |
| 1 drive over ACP | A prompt gets the expected reply and ends with `end_turn`. |
| 2 slash skills | The scratch repo's skill is listed as a slash command and runs. |
| 3 permission and turn end | Asked to run a command, the Agent sends a permission request, then ends its turn. |
| 4 reopen | After the Agent restarts, `session/load` brings the conversation back. |
| 5 no fs/terminal | Checks 1–4 pass with no client capabilities, and the Agent asks for no `fs` or `terminal` calls. |
| 6 claude adapter | Check 1 passes through the pinned Claude Code ACP adapter. |
| 7 `.claude/skills/` loading | The skill in the scratch repo's `.claude/skills/` runs. |
| 8 `OPENCODE_PERMISSION` | OpenCode sends a permission request with only that variable telling it to ask. |

It spends real tokens and needs your logins, so CI never runs it ([ADR 0007](docs/adr/0007-quality-gates.md)). Run one Agent with `go run ./scripts/smoke -agent opencode`. If a check fails after an Agent update, don't tag: file an issue with the output, and keep the Agent's stderr log, whose folder the last lines print.

One-time setup, before the first release:

- Create the public repo `philipsahli/homebrew-tap`.
- Add a fine-grained token with Contents read and write on that repo only, as the Actions secret `HOMEBREW_TAP_GITHUB_TOKEN` on this repo.
- Require both CI jobs, ubuntu and macOS, in `main`'s branch protection (#37).

## Community & Support

Questions, bug reports and ideas go to [GitHub Issues](https://github.com/philipsahli/fullharness/issues). There is no chat or Discussions forum yet.

## License

MIT. See [`LICENSE`](LICENSE).
