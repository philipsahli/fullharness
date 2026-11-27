import { useState, type ReactNode } from "react";
import type { AutopilotKind, AutopilotReason, AutopilotStatus } from "./events";

// The Autopilot status badge (#203), as the mockup draws it (ADR-0012,
// ADR-0013). fh works the Autopilot status out; this only words and
// colours it.

// What the user reads for each Stop point, and for why Autopilot is off;
// the flow graph (Flow.tsx) names its Stop points the same way.
export const reasonName: Record<AutopilotReason, string> = {
  "spec-approval": "Approve spec",
  "screenshot-review": "Screenshot review",
  merge: "Merge",
  exited: "Session exited",
  asking: "Agent asking",
  stuck: "Agent stuck",
  unsure: "Agent unsure",
  "autopilot-off": "Autopilot off",
  "not-in-now": "Not in Now",
};

type Words = {
  glyph: string;
  shown: (s: AutopilotStatus) => ReactNode; // what the badge shows
  read: (s: AutopilotStatus) => string; // what a screen reader says
};

const skill = (s: AutopilotStatus) => <b className="mono">/{s.skill}</b>;
const reason = (s: AutopilotStatus) => (s.reason ? reasonName[s.reason] : "");

// One entry per Autopilot status kind.
const words: Record<AutopilotKind, Words> = {
  running: {
    glyph: "",
    shown: (s) => (
      <>
        Running {skill(s)}
        {s.ticket && ` · #${s.ticket}`}
      </>
    ),
    read: (s) => `Running /${s.skill ?? ""}${s.ticket ? ` on #${s.ticket}` : ""}`,
  },
  waiting: {
    glyph: "◉",
    shown: (s) => (
      <>
        Waiting · <b>{reason(s)}</b>
      </>
    ),
    read: (s) => `Waiting at ${reason(s)}`,
  },
  failed: {
    glyph: "✖",
    shown: (s) => <>Failed to start {skill(s)}</>,
    read: (s) => `Failed to start /${s.skill ?? ""}`,
  },
  off: { glyph: "", shown: (s) => reason(s) || "Off", read: (s) => reason(s) || "Off" },
  idle: { glyph: "", shown: () => "Idle", read: () => "Idle" },
  done: { glyph: "✓", shown: () => "Done", read: () => "Done" },
};

// AutopilotBadge draws one Feature's Autopilot status. When it changes
// while shown, the new badge springs in (.changed); the first draw doesn't.
export function AutopilotBadge({ status }: { status: AutopilotStatus }) {
  const key = `${status.kind}:${status.skill ?? ""}:${status.reason ?? ""}`;
  // The previous key lives in state, not a ref, so the change is seen
  // during render (React's "storing information from previous renders").
  const [prev, setPrev] = useState(key);
  const [changed, setChanged] = useState(false);
  if (prev !== key) {
    setPrev(key);
    setChanged(true);
  }
  const w = words[status.kind];
  const cls = ["ap", status.reason === "autopilot-off" ? "in-now" : "", changed ? "changed" : ""]
    .filter(Boolean)
    .join(" ");
  return (
    // Keyed on the Autopilot status, so the spring-in plays again on every change.
    <span
      key={key}
      className={cls}
      data-ap={status.kind}
      role="img"
      aria-label={`Autopilot status: ${w.read(status)}`}
    >
      <i className="ap-mark" aria-hidden="true">
        {w.glyph}
      </i>
      <span>{w.shown(status)}</span>
    </span>
  );
}

// AutopilotLive is the live pulse (#205): the tool call a running turn is
// on, under its badge. fh puts the tool call in the Autopilot status; each
// new one slides in (keyed on it). Nothing shows when the Feature isn't
// running or its Agent is between tool calls.
export function AutopilotLive({ status }: { status: AutopilotStatus }) {
  if (status.kind !== "running" || !status.tool) return null;
  return (
    <span className="ap-live" data-ap="running">
      <span className="ap-eq" aria-hidden="true">
        <span />
        <span />
        <span />
      </span>
      {/* Not a live region: a busy Agent would talk over the screen reader. */}
      <span key={status.tool} className="ap-tool tick">
        {status.tool}
      </span>
    </span>
  );
}

// OutsideNow is the warning beside a running Feature that left Roadmap Now
// (#204, ADR-0013): its turn finishes, and Autopilot starts nothing new
// for it.
export function OutsideNow() {
  return <span className="ap-outside">Session running outside Now</span>;
}

// showsBadge: a Feature outside Now that nothing runs for gets no badge on
// its card; its Roadmap group already says why (the mockup).
export function showsBadge(s: AutopilotStatus | undefined): s is AutopilotStatus {
  return !!s && !(s.kind === "off" && s.reason === "not-in-now");
}

// goToSwitch takes the user to a Project's Autopilot switch in the rail:
// scrolled into view, focused, and flashed. It doesn't turn Autopilot on.
export function goToSwitch(project: string) {
  const box = document.querySelector<HTMLInputElement>(
    `input[data-autopilot-switch="${CSS.escape(project)}"]`,
  );
  if (!box) return;
  box.scrollIntoView({ block: "nearest" });
  box.focus();
  const label = box.closest("label");
  label?.classList.remove("flash");
  void label?.offsetWidth; // restarts the animation when clicked twice
  label?.classList.add("flash");
}
