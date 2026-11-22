# Agents start Child Sessions only through fh's MCP tool, and only with the user's approval

An Agent can hand work to another Agent by asking fullharness to start a Child Session. fh runs a local MCP server, configured into every Session it starts with a token per Session, offering two tools: `start_session(agent, branch, prompt)` and `get_session(id)`. `start_session` returns at once with status `pending approval`; the request waits in the user's Inbox until they allow or deny it, and the Parent checks back with `get_session`, which returns the Child's Session state and its Agent's last message. We route every request through the Inbox, and the per-Session auto-approve toggle never covers it, because starting a Session launches a new process that costs money, while auto-approve only hands over the Agent's own tools.

## Limits

- Same Project only, always in a new Worktree, never an existing one.
- One level deep: a Child Session cannot start Sessions.
- At most 3 running Child Sessions per Parent.

## Consequences

- A Child Session's permission requests go to the user's Inbox, never to the Parent Agent, so no Agent approves another Agent's actions.
- Children keep running when their Parent ends; a Parent reopened through `session/load` can still call `get_session`.
- Depends on passing an MCP server to the Agent when a Session starts; whether ACP's `session/new` supports this for each Agent is checked by the ACP prototype.
- Planned for after v1, together with the Roadmap and Readiness.
