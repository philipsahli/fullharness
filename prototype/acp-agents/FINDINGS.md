# ACP spike findings (2026-11-22)

**Question:** can fullharness drive Claude Code, Copilot CLI and OpenCode over ACP v1 from Go, well enough for ADR 0003 (ACP-first Sessions) and ADR 0006 (Child Sessions via fh's MCP tool)?

**Answer:** yes. All 8 checks pass for all three agents (run 2), provided fh does three things explained below:
1. set the permission mode when it starts a Session
2. ship OpenCode a config that makes it ask before running commands
3. serve its MCP server over HTTP, not stdio

## Setup

- Go 1.27.2, `github.com/coder/acp-go-sdk` v0.20.0, ACP protocol v1.
- **Agent versions:**
  - Claude Code 2.1.330 through `@agentclientprotocol/claude-agent-acp` 0.109.1, run with `CLAUDE_CODE_EXECUTABLE`
  - Copilot CLI 1.0.109, run as `copilot --acp --stdio`
  - OpenCode 1.19.6, run as `opencode acp`
- Every agent ran in a fresh scratch git repo with `ANTHROPIC_API_KEY` removed from its environment.
- fh sent **empty `clientCapabilities`**: no `fs`, no `terminal`.
- **Accounts used:**
  - **Claude Code:** your Claude Code login. The adapter reported model `claude-opus-5-5`, your default.
  - **Copilot:** your Copilot login, model `claude-sonnet-5.5`.
  - **OpenCode:** its GitHub Copilot login, model `github-copilot/claude-haiku-5.5`, set through `opencode.json`.
- Permission answers: `allow_once` only for `touch fh-perm.txt` and `fh_ping`; `reject_once` for anything else. Nothing else was asked.

## Results

Line numbers point at the `check N` note in each JSONL log.

| Check | Claude Code | Copilot CLI | OpenCode |
|---|---|---|---|
| 1 Drive over ACP (`end_turn`, reply `PONG-1`) | PASS ([L23](logs/claude.jsonl)) | PASS ([L20](logs/copilot.jsonl)) | PASS ([L11](logs/opencode.jsonl)) |
| 2 Slash skills (`/fh-spike-echo` listed and ran) | PASS (L32) | PASS (L32) | PASS (L17) |
| 3 Waiting (`session/request_permission`, then `end_turn`) | PASS (L49), with mode `default` | PASS (L44) | PASS (L30), with ask config |
| 4 Reopen (`loadSession: true`; `session/load` replayed history after a kill) | PASS (L106) | PASS (L89) | PASS (L60) |
| 5 Works without fs/terminal capabilities | PASS (L107) | PASS (L90) | PASS (L61) |
| 6 Claude adapter | PASS (L24) | – | – |
| 7 `.claude/skills/` skill loads | PASS (L33) | PASS (L33) | PASS (L18) |
| 8 MCP server passed in `session/new` | PASS (L70), over HTTP | PASS (L60), over HTTP | PASS (L38), over HTTP |

No agent called `fs/*` or `terminal/*`; the logs contain no `UNEXPECTED` notes.

## Run 1 failures and what they mean for v1

Run 1 (`logs-run1/`) used the agents' own defaults and a stdio MCP server. Three checks failed:

1. **Claude Code, check 3** (`logs-run1/claude.jsonl` L44): no permission request was sent. The adapter carries over the user's own Claude Code permission mode, which was `auto` (`logs-run1/claude.stderr.log`: `permissionMode=auto`), so it approved `touch` itself. The fix is `session/set_mode` to `default` right after `session/new` and `session/load`. The adapter offers the modes `default`, `acceptEdits`, `plan`, `auto` and `bypassPermissions`.
   **→ v1:** fh sets the ACP session mode on every Session, and fh's auto-approve toggle maps to this mode instead of the user's global setting.
2. **OpenCode, check 3** (`logs-run1/opencode.jsonl` L27): OpenCode runs shell commands without asking by default. The fix is `"permission": {"bash": "ask", "edit": "ask"}` in its config.
   **→ v1:** fh must supply this config when it starts OpenCode, or Sessions skip the Inbox. How fh delivers it without writing into the user's repo is still open; a config file fh owns or OpenCode's environment config are two options.
3. **Copilot, check 8** (`logs-run1/copilot.jsonl` L65): Copilot's own log says `Rejecting non-http/sse MCP server "fh-ping" from client`. Copilot accepts only HTTP and SSE MCP servers from an ACP client. All three agents advertise `mcpCapabilities.http: true`, and with an HTTP server all three passed.
   **→ v1.1 (ADR 0006):** fh serves its MCP tools over HTTP on its existing localhost server, protected by the per-Session token. No stdio MCP server is needed.

## Other observations

- **coder/acp-go-sdk** fit well. The client interface, `SessionUpdate` union types, `LoadSession`, `SetSessionMode` and `mcpServers` all worked without falling back to raw JSON-RPC. Verdict: usable as the v1 dependency.
- **Sessions change modes and config over ACP.** Claude Code and Copilot expose session modes, and all three expose `configOptions` (model, effort, mode). fh can show and change them per Session.
- **Copilot names its modes as URLs** (`…/session-modes#agent`, `#plan`, `#autopilot`) and has an `allow_all` config option. fh's auto-approve toggle needs a per-Agent mapping.
- **v2 would only be needed** for Claude's `session/resume`. `session/load` on v1 already replays history for all three agents, so v1 is enough.
- **The agents inherit the user's environment:** all user-level skills appear in `available_commands_update`, and Claude Code uses the user's default model. That is the intended behaviour, since fh starts the user's own Agents, but it means fh can't assume a clean setup.

## Reproduce

```sh
cd prototype/acp-agents
go build -o fh-spike . && ./fh-spike -agent all      # run 2 setup (HTTP MCP, ask mode)
./fh-spike -agent copilot -mcp stdio                  # reproduces the Copilot stdio rejection
```

The logs contain your local paths and the names of your installed skills; the repo is private.
