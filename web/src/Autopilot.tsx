import { useState, type ReactNode } from "react";
import type { AutopilotKind, AutopilotStatus } from "./events";

// The Autopilot status badge (#203), as the mockup draws it (ADR-0012,
// ADR-0013). fh works the status out; this only words and colours it.

// What the user reads for each Stop point reason.
const stopName: Record<string, string> = {
  "spec-approval": "Approve spec",
  merge: "Merge",
  exited: "Session exited",
  asking: "Agent asking",
  stuck: "Agent stuck",
  unsure: "Agent unsure",
};

const glyph: Partial<Record<AutopilotKind, string>> = { waiting: "◉", failed: "✖", done: "✓" };

// BadgeStatus is a status as the badge draws it. projectOff marks an off
// Feature in Now whose Project has Autopilot off: it reads "Autopilot off".
export type BadgeStatus = Pick<AutopilotStatus, "kind" | "skill" | "ticket" | "reason"> & {
  projectOff?: boolean;
};

function words(s: BadgeStatus): ReactNode {
  const skill = s.skill && <b className="mono">/{s.skill}</b>;
  switch (s.kind) {
    case "running":
      return (
        <>
          Running {skill}
          {s.ticket && ` · #${s.ticket}`}
        </>
      );
    case "failed":
      return <>Failed to start {skill}</>;
    case "waiting":
      return (
        <>
          Waiting · <b>{stopName[s.reason ?? ""] ?? s.reason}</b>
        </>
      );
    case "off":
      return s.projectOff ? "Autopilot off" : "Off";
    case "idle":
      return "Idle";
    case "done":
      return "Done";
  }
}

function plain(s: BadgeStatus): string {
  switch (s.kind) {
    case "running":
      return `Running /${s.skill ?? ""}${s.ticket ? ` on #${s.ticket}` : ""}`;
    case "failed":
      return `Failed to start /${s.skill ?? ""}`;
    case "waiting":
      return `Waiting at ${stopName[s.reason ?? ""] ?? s.reason}`;
    case "off":
      return s.projectOff ? "Autopilot off" : "Off";
    default:
      return s.kind === "idle" ? "Idle" : "Done";
  }
}

// AutopilotBadge draws one Feature's status. When the status changes while
// it is shown, the new badge springs in (.changed); the first draw doesn't.
export function AutopilotBadge({ status }: { status: BadgeStatus }) {
  const key = `${status.kind}:${status.skill ?? ""}:${status.reason ?? ""}`;
  // The previous key lives in state, not a ref, so the change is seen
  // during render (React's "storing information from previous renders").
  const [prev, setPrev] = useState(key);
  const [changed, setChanged] = useState(false);
  if (prev !== key) {
    setPrev(key);
    setChanged(true);
  }
  const cls = ["ap", status.projectOff ? "in-now" : "", changed ? "changed" : ""]
    .filter(Boolean)
    .join(" ");
  return (
    // Keyed on the status, so the spring-in plays again on every change.
    <span
      key={key}
      className={cls}
      data-ap={status.kind}
      role="img"
      aria-label={`Autopilot status: ${plain(status)}`}
    >
      <i className="ap-mark" aria-hidden="true">
        {glyph[status.kind] ?? ""}
      </i>
      <span>{words(status)}</span>
    </span>
  );
}
