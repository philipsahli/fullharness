import { useEffect, useState } from "react";
import { get } from "./api";
import { AutopilotBadge, goToSwitch, showsBadge } from "./Autopilot";
import type { AutopilotStatus } from "./events";
import { LinkOrText } from "./LinkOrText";

// These types match internal/app's roadmapView (GET /api/roadmap).
type RoadmapProject = { path: string; name: string; tracker: string; note?: string };
type Group = "now" | "next" | "later";
type RoadmapFeature = {
  project: string; // the Project's path
  id: string;
  title: string;
  ref: string;
  url?: string;
  stage: string;
  roadmap: Group;
  noGroup?: boolean; // the Spec names no group, so it is in Later
};
type RoadmapData = { projects: RoadmapProject[]; features: RoadmapFeature[] };

const groups: { group: Group; name: string; hint?: string }[] = [
  { group: "now", name: "Now", hint: "Autopilot works here" },
  { group: "next", name: "Next" },
  { group: "later", name: "Later" },
];
const stageName: Record<string, string> = {
  idea: "Idea",
  spec: "Spec",
  tickets: "Tickets",
  implementing: "Implementing",
  review: "Review",
  shipped: "Shipped",
};
const trackerName: Record<string, string> = { github: "GitHub", local: "Local markdown" };

const refreshEvery = 60_000;

// Roadmap is every Project's open Features in their Roadmap groups (ADR
// 0005). It reads them in one request while it's shown, and every 60 s.
// Open shows a Feature on the Software factory board (onOpen). Each card's
// Autopilot status comes from the snapshot and the autopilot event
// (autopilot), as on the Feature card (#216, ADR 0013).
export function Roadmap({
  autopilot,
  onOpen,
}: {
  autopilot: AutopilotStatus[];
  onOpen: (project: string, feature: string) => void;
}) {
  const [data, setData] = useState<RoadmapData | undefined>();
  const [failed, setFailed] = useState(false);
  // The chip filter: a Project's path, or undefined for All Projects.
  const [only, setOnly] = useState<string | undefined>();

  useEffect(() => {
    const read = () =>
      void get<RoadmapData>("/api/roadmap").then((d) => {
        setFailed(!d);
        if (d) setData(d);
      });
    read();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") read();
    }, refreshEvery);
    return () => window.clearInterval(timer);
  }, []);

  const name = (path: string) => data?.projects.find((p) => p.path === path)?.name ?? path;
  const shown = (data?.features ?? []).filter((f) => !only || f.project === only);
  const notes = (data?.projects ?? []).filter((p) => p.note && (!only || p.path === only));
  // badgeFor is a card's Autopilot status, if its card shows one: a Feature
  // outside Now that nothing runs for shows none (showsBadge).
  const badgeFor = (f: RoadmapFeature) => {
    const found = autopilot.find((x) => x.project === f.project && x.feature === f.id);
    return showsBadge(found) ? found : undefined;
  };

  return (
    <section className="roadmap-view" aria-label="Roadmap">
      <header className="board-head">
        <h2>Roadmap</h2>
        <p className="crumb">
          Every Feature with a Spec, across all Projects, in its Roadmap group. Autopilot works only
          on Features in Now.
        </p>
      </header>
      {failed && (
        <p className="notice bad">Couldn't read the Roadmap. fh tries again in a minute.</p>
      )}
      {data && data.projects.length === 0 && (
        <p className="notice">
          No Project has an Issue tracker yet. Add one with + Add Project, or set up its tracker
          from the Software factory.
        </p>
      )}
      {data && data.projects.length > 0 && (
        <div className="fhead" role="group" aria-label="Filter by Project">
          <button
            type="button"
            className="pchip"
            aria-pressed={!only}
            onClick={() => setOnly(undefined)}
          >
            All Projects
          </button>
          {data.projects.map((p) => (
            <button
              key={p.path}
              type="button"
              className="pchip"
              aria-pressed={only === p.path}
              onClick={() => setOnly(p.path)}
            >
              {p.name}
              <small>{trackerName[p.tracker] ?? p.tracker}</small>
            </button>
          ))}
        </div>
      )}
      {notes.map((p) => (
        <p key={p.path} className="notice">
          {p.name}: {p.note}
        </p>
      ))}
      {data && (
        <div className="roadmap">
          {groups.map(({ group, name: label, hint }, i) => {
            const items = shown.filter((f) => f.roadmap === group);
            return (
              <section key={group} className="rcol" aria-label={label}>
                <h3>
                  <span>
                    {label}
                    {hint && <small className="rhint"> · {hint}</small>}
                  </span>
                  <span className="bd">{items.length}</span>
                </h3>
                <ol className="rlist">
                  {items.length === 0 && <li className="rempty">No Features in {label}</li>}
                  {items.map((f) => (
                    <li key={`${f.project} ${f.id}`} className="rcard" data-ap={badgeFor(f)?.kind}>
                      <div className="rtop">
                        <span className="n">{f.title}</span>
                        <span className={`stage ${f.stage}`}>{stageName[f.stage] ?? f.stage}</span>
                      </div>
                      <span className="ref">
                        {name(f.project)} · <LinkOrText url={f.url}>{f.ref}</LinkOrText>
                        {f.noGroup && (
                          <>
                            {" · "}
                            <span className="rnog" title="The Spec names no Roadmap group">
                              no Roadmap group
                            </span>
                          </>
                        )}
                      </span>
                      <RoadmapBadge status={badgeFor(f)} />
                      <div className="rmove">
                        <button
                          type="button"
                          className="btn small"
                          onClick={() => onOpen(f.project, f.id)}
                        >
                          Open
                        </button>
                        {/* Moving a card isn't built yet; the arrows show where it goes. */}
                        <button
                          type="button"
                          className="btn small rarrow"
                          disabled
                          aria-label={`Move ${f.title} to ${i > 0 ? groups[i - 1].name : "the previous group"}`}
                        >
                          ←
                        </button>
                        <button
                          type="button"
                          className="btn small rarrow"
                          disabled
                          aria-label={`Move ${f.title} to ${i < 2 ? groups[i + 1].name : "the next group"}`}
                        >
                          →
                        </button>
                      </div>
                      <OffLink status={badgeFor(f)} name={name(f.project)} />
                    </li>
                  ))}
                </ol>
              </section>
            );
          })}
        </div>
      )}
    </section>
  );
}

// RoadmapBadge is a card's Autopilot status badge, as on the Feature card
// (#216).
function RoadmapBadge({ status }: { status: AutopilotStatus | undefined }) {
  if (!status) return null;
  return (
    <div className="rap">
      <AutopilotBadge status={status} />
    </div>
  );
}

// OffLink ends a card in Now whose Project has Autopilot off: it takes the
// user to the switch, which it focuses without turning it on.
function OffLink({ status, name }: { status: AutopilotStatus | undefined; name: string }) {
  if (status?.reason !== "autopilot-off") return null;
  return (
    <button type="button" className="ap-offlink" onClick={() => goToSwitch(status.project)}>
      Autopilot off for {name} · <u>Turn on</u>
    </button>
  );
}
