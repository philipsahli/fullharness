import { useEffect, useState } from "react";
import { command, get } from "./api";
import { AutopilotBadge, AutopilotLive } from "./Autopilot";
import type { AutopilotStatus } from "./events";
import { ScreenshotReview, awaitingScreens } from "./ScreenshotReview";

// Autopilot's Stop points in the Inbox (#199, ADR-0014), as the mockup
// draws them. fh works out which Feature waits where (the Autopilot
// status); this draws an item for each and sends the user's action.

// The Feature names a Stop point item shows, from GET /api/roadmap: every
// open Feature with a Spec, so every Spec that can wait at Approve spec.
type RoadmapData = {
  projects: { path: string; name: string }[];
  features: { project: string; id: string; title: string; ref: string; url?: string }[];
};

// awaitingApproval is every Feature that waits at Approve spec.
export function awaitingApproval(statuses: AutopilotStatus[]): AutopilotStatus[] {
  return statuses.filter((s) => s.kind === "waiting" && s.reason === "spec-approval");
}

// awaitingUser is every Stop point item the Inbox shows.
export function awaitingUser(statuses: AutopilotStatus[]): AutopilotStatus[] {
  return [...awaitingApproval(statuses), ...awaitingScreens(statuses)];
}

// What the user did just now, under the items: the row keeps the Feature's
// live Autopilot status, so the user sees it move on.
type Done = { key: string; project: string; feature: string; text: string; what: string };

const keyOf = (s: { project: string; feature: string }) => `${s.project}\n${s.feature}`;

export function StopPoints({ statuses }: { statuses: AutopilotStatus[] }) {
  const waiting = awaitingApproval(statuses);
  const screens = awaitingScreens(statuses);
  const [names, setNames] = useState<RoadmapData>();
  const [done, setDone] = useState<Done[]>([]);
  const want = [...waiting, ...screens].map(keyOf).join("|");
  // Read the names again whenever another Feature starts to wait.
  useEffect(() => {
    if (!want) return;
    void get<RoadmapData>("/api/roadmap").then((d) => {
      if (d) setNames(d);
    });
  }, [want]);

  const feature = (s: { project: string; feature: string }) =>
    names?.features.find((f) => f.project === s.project && f.id === s.feature);
  const projectName = (path: string) =>
    names?.projects.find((p) => p.path === path)?.name ?? path.split("/").pop() ?? path;
  const statusOf = (d: Done) => statuses.find((s) => keyOf(s) === d.key);

  if (waiting.length === 0 && screens.length === 0 && done.length === 0) return null;
  return (
    <>
      {waiting.map((s) => {
        const f = feature(s);
        return (
          <ApproveSpec
            key={keyOf(s)}
            status={s}
            title={f?.title ?? s.feature}
            specRef={f?.ref ?? s.feature}
            url={f?.url}
            project={projectName(s.project)}
            onApproved={() =>
              setDone((d) => [
                {
                  key: keyOf(s),
                  project: s.project,
                  feature: s.feature,
                  text: `Approved ${f?.title ?? s.feature}`,
                  what: `ready-for-agent set · Autopilot stepped ${projectName(s.project)} now`,
                },
                ...d.filter((x) => x.key !== keyOf(s)),
              ])
            }
          />
        );
      })}
      {screens.map((s) => {
        const title = feature(s)?.title ?? s.feature;
        return (
          <ScreenshotReview
            key={`${keyOf(s)}\n${s.ticket ?? ""}`}
            status={s}
            title={title}
            project={projectName(s.project)}
            onLooksGood={(pr) =>
              setDone((d) => [
                {
                  key: keyOf(s),
                  project: s.project,
                  feature: s.feature,
                  text: `Looks good: ${pr ? `PR #${pr}` : `#${s.ticket ?? ""}`}`,
                  what: "Screen reviewed in fh, posted on the PR · now at merge",
                },
                ...d.filter((x) => x.key !== keyOf(s)),
              ])
            }
          />
        );
      })}
      {done.length > 0 && (
        <div className="justnow">
          <h3>Just now</h3>
          {done.map((d) => {
            const st = statusOf(d);
            return (
              <div key={d.key} className="jn">
                <span className="ok" aria-hidden="true">
                  ✓
                </span>
                <strong>{d.text}</strong>
                <span className="what">{d.what}</span>
                {st && <AutopilotBadge status={st} />}
                {st && <AutopilotLive status={st} />}
              </div>
            );
          })}
        </div>
      )}
    </>
  );
}

// ApproveSpec is one "Approve spec" item. Reject comes with #225.
function ApproveSpec(props: {
  status: AutopilotStatus;
  title: string;
  specRef: string;
  url?: string;
  project: string;
  onApproved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const approve = async () => {
    setBusy(true);
    const r = await command("/api/autopilot/approve", {
      project: props.status.project,
      feature: props.status.feature,
    });
    setBusy(false);
    setError(r.ok ? "" : r.error);
    // The item goes away by itself once fh's Autopilot status moves on.
    if (r.ok) props.onApproved();
  };
  return (
    <div
      className="ibox stop"
      data-ap="waiting"
      role="group"
      aria-label={`Approve spec: ${props.title}`}
    >
      <div>
        <div className="meta">
          <AutopilotBadge status={props.status} />
          <span>
            {props.project} · {props.title}
          </span>
        </div>
        <div className="kind">
          <strong>Approve spec</strong>
          {props.url ? (
            <a className="mono" href={props.url} target="_blank" rel="noreferrer">
              {props.specRef}
            </a>
          ) : (
            <span className="mono">{props.specRef}</span>
          )}
        </div>
        <div className="why">
          Autopilot stops here before it makes Tickets. Approve labels the Spec{" "}
          <span className="mono">ready-for-agent</span> and starts{" "}
          <b className="mono">/{props.status.skill ?? "to-tickets"}</b> at once.
        </div>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
      </div>
      <div className="acts">
        <button
          type="button"
          className="btn primary"
          disabled={busy}
          onClick={() => void approve()}
        >
          Approve
        </button>
      </div>
    </div>
  );
}
