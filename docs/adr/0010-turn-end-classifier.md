---
status: accepted
---

# Rules decide the next step; a model only classifies unclear turn ends

When a Session's turn ends, Autopilot (ADR-0009) has to know whether the Agent is asking the user something, is done, or is stuck. Often fixed rules answer this: an open permission request, a new Spec issue, an open PR. When they don't, fh asks a model to read the Agent's last message and answer one of `asking`, `done` or `stuck`.

## Decision

- Rules first. The model is called only when no rule decides.
- The model sits behind a provider interface in fh's config. The default provider runs `claude -p --model claude-haiku-5-5`, using the Claude Code login the user already has, so no API key is needed.
- If the call fails, or the model answers anything other than one of the three verdicts, fh treats the turn end as a Stop point and asks the user.
- For every call, fh records:
  - the verdict and whether the user later overrode it (this gives the accuracy),
  - latency, plus tokens and cost when the provider reports them,
  - the input and the verdict, as a replay set for testing another provider before switching to it.

## Considered options

- **Rules only.** Rejected: every unclear turn end would stop and ask, and fh would ask too often.
- **A model decides every next step.** Rejected: its choices can't be tested or repeated the way a table can, and Stage already comes from the tracker (ADR-0004).
- **A local Ollama model as the default.** Not chosen for now; it can be added as a provider, and the replay set lets us measure it first.

## Consequences

- `claude` must be on `PATH` for the default provider. Without it, every unclear turn end becomes a Stop point.
- The replay set holds Agent messages from the user's repos. It stays in fh's local state and is never sent anywhere except to the configured provider.
