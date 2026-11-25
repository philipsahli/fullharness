import type { SessionState } from "./events";

// The chips and dots from docs/design/fh-mockup.html (ADR 0012) that more
// than one screen shows.

export const stateNames: Record<SessionState, string> = {
  starting: "Starting",
  working: "Working",
  waiting: "Waiting",
  idle: "Idle",
  exited: "Exited",
};

// The mockup's Agent colours, keyed by Agent id. There is no setting: an
// Agent fh doesn't ship a colour for gets the idle grey, as in the mockup.
const agentColours: Record<string, string> = {
  claude: "#c96442",
  copilot: "#6e56cf",
  opencode: "#0e8a7a",
};

export function AgentChip({ id, name }: { id: string; name: string }) {
  return (
    <span className="agent">
      <i style={{ background: agentColours[id] ?? "var(--idle)" }} aria-hidden="true" />
      {name}
    </span>
  );
}

// Most urgent first: a Worktree's dot shows the first of these that any of
// its Sessions is in, so a Waiting Session is never hidden behind an Idle one.
const urgency: SessionState[] = ["waiting", "working", "starting", "idle", "exited"];

export function mostUrgent(states: SessionState[]): SessionState | undefined {
  return urgency.find((s) => states.includes(s));
}

// WorktreeDot is the state of the Worktree's Sessions, named for screen
// readers; a Worktree without a Session shows a faint dot.
export function WorktreeDot({ state }: { state?: SessionState }) {
  return (
    <span
      className={`dot ${state ?? "none"}`}
      role="img"
      aria-label={state ? stateNames[state] : "No Session"}
    />
  );
}
