import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { command, get } from "./api";
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

// A failed move shows on the card that went back: the group it was meant
// for and gh's own message.
type MoveError = { to: Group; msg: string };
// The card waits this long in its new group before a failed write slides it
// back, so the move is seen before it is undone (#197: it moves first).
const bounceAfter = 600;

const cardKey = (f: RoadmapFeature) => `${f.project} ${f.id}`;
const without = <T,>(m: Record<string, T>, key: string) => {
  const next = { ...m };
  delete next[key];
  return next;
};

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
  // A move shows at once (no confirm step, #197): placed holds a card's new
  // group until the Roadmap is read again after its write.
  const [placed, setPlaced] = useState<Record<string, Group>>({});
  const [moving, setMoving] = useState<Record<string, true>>({});
  const [moveErrors, setMoveErrors] = useState<Record<string, MoveError>>({});
  const board = useRef<HTMLDivElement>(null);
  // flip is the card to slide after the next render, from where it was.
  const flip = useRef<{ key: string; cls: "moved" | "bounced"; before: Map<string, DOMRect> }>(
    undefined,
  );

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

  const snap = () => {
    const at = new Map<string, DOMRect>();
    board.current
      ?.querySelectorAll<HTMLElement>(".rcard[data-rm]")
      .forEach((el) => at.set(el.dataset.rm ?? "", el.getBoundingClientRect()));
    return at;
  };

  // A moved card slides from its old place to its new one (FLIP), then
  // lands with a glow; one that went back shakes once. No motion under
  // prefers-reduced-motion: the classes' animations are off there too.
  useLayoutEffect(() => {
    const f = flip.current;
    if (!f) return;
    flip.current = undefined;
    const el = board.current?.querySelector<HTMLElement>(`.rcard[data-rm="${CSS.escape(f.key)}"]`);
    if (!el) return;
    const was = f.before.get(f.key);
    if (was && el.animate && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      const now = el.getBoundingClientRect();
      el.animate(
        [
          { transform: `translate(${was.left - now.left}px, ${was.top - now.top}px)`, zIndex: 2 },
          { transform: "none", zIndex: 2 },
        ],
        { duration: 520, easing: "cubic-bezier(.2,.8,.2,1)" },
      );
    }
    el.classList.remove("moved", "bounced");
    void el.offsetWidth; // restart the class's animation
    el.classList.add(f.cls);
  });

  const groupOf = (f: RoadmapFeature) => placed[cardKey(f)] ?? f.roadmap;

  // move writes the new group at once. When the write fails, the card goes
  // back to its group and says why, with gh's own message.
  const move = async (f: RoadmapFeature, to: Group) => {
    const key = cardKey(f);
    const from = groupOf(f);
    const started = Date.now();
    flip.current = { key, cls: "moved", before: snap() };
    setMoveErrors((e) => without(e, key));
    setMoving((m) => ({ ...m, [key]: true }));
    setPlaced((p) => ({ ...p, [key]: to }));
    const r = await command("/api/tracker/roadmap", { project: f.project, ref: f.ref, group: to });
    if (r.ok) {
      const d = await get<RoadmapData>("/api/roadmap");
      if (d) {
        setData(d);
        setPlaced((p) => without(p, key));
      }
    } else {
      await new Promise((done) => window.setTimeout(done, bounceAfter - (Date.now() - started)));
      flip.current = { key, cls: "bounced", before: snap() };
      setPlaced((p) => ({ ...p, [key]: from }));
      setMoveErrors((e) => ({ ...e, [key]: { to, msg: r.error } }));
    }
    setMoving((m) => without(m, key));
  };

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
          Every Feature with a Spec, across all Projects. ← and → move a Feature to another Roadmap
          group and save it on the Spec at once: a roadmap:* label on GitHub, the Roadmap: line in
          spec.md on local markdown. Autopilot works only on Features in Now.
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
        <div className="roadmap" ref={board}>
          {groups.map(({ group, name: label, hint }, i) => {
            const items = shown.filter((f) => groupOf(f) === group);
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
                  {items.map((f) => {
                    const key = cardKey(f);
                    const err = moveErrors[key];
                    return (
                      <li
                        key={key}
                        className={err ? "rcard rfail" : "rcard"}
                        data-rm={key}
                        data-ap={badgeFor(f)?.kind}
                      >
                        <div className="rtop">
                          <span className="n">{f.title}</span>
                          <span className={`stage ${f.stage}`}>
                            {stageName[f.stage] ?? f.stage}
                          </span>
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
                        {err && (
                          <div className="rerr" role="alert">
                            <b>Not moved to {groups.find((g) => g.group === err.to)?.name}.</b>
                            <button
                              type="button"
                              className="btn small"
                              aria-label="Dismiss the error"
                              onClick={() => setMoveErrors((e) => without(e, key))}
                            >
                              ✕
                            </button>
                            <span className="mono">{err.msg}</span>
                          </div>
                        )}
                        <div className="rmove">
                          <button
                            type="button"
                            className="btn small"
                            onClick={() => onOpen(f.project, f.id)}
                          >
                            Open
                          </button>
                          <button
                            type="button"
                            className="btn small rarrow"
                            disabled={i === 0 || !!moving[key]}
                            aria-label={`Move ${f.title} to ${i > 0 ? groups[i - 1].name : "the previous group"}`}
                            onClick={() => void move(f, groups[i - 1].group)}
                          >
                            ←
                          </button>
                          <button
                            type="button"
                            className="btn small rarrow"
                            disabled={i === 2 || !!moving[key]}
                            aria-label={`Move ${f.title} to ${i < 2 ? groups[i + 1].name : "the next group"}`}
                            onClick={() => void move(f, groups[i + 1].group)}
                          >
                            →
                          </button>
                        </div>
                        <OffLink status={badgeFor(f)} name={name(f.project)} />
                      </li>
                    );
                  })}
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
