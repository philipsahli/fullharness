import { useEffect, useRef, useState } from "react";
import { get } from "./api";
import type { Project } from "./events";

// These types match internal/tracker.
type Stage = "idea" | "spec" | "tickets" | "implementing" | "review" | "shipped";
type Blocker = { number: string; known: boolean; open: boolean };
type Ticket = {
  number: string;
  title: string;
  label: string;
  closed: boolean;
  blockers: Blocker[];
  stage: Stage;
  startable: boolean;
};
type Feature = {
  id: string;
  title: string;
  closed: boolean;
  stage: Stage;
  note?: string;
  tickets: Ticket[];
  breakdown: Partial<Record<Stage, number>>;
};
type BoardData = { tracker: string; note?: string; features: Feature[] };

const columns: { stage: Stage; name: string }[] = [
  { stage: "idea", name: "Idea" },
  { stage: "spec", name: "Spec" },
  { stage: "tickets", name: "Tickets" },
  { stage: "implementing", name: "Implementing" },
  { stage: "review", name: "Review" },
  { stage: "shipped", name: "Shipped" },
];
const stageName = Object.fromEntries(columns.map((c) => [c.stage, c.name])) as Record<
  Stage,
  string
>;

const refreshEvery = 60_000;
const lastProjectKey = "fh.board.project";

// Board is the Software factory: one Project's Features by Stage. It reads
// the board while it's shown, every 60 s and on Refresh; nothing is read
// for boards nobody looks at.
export function Board({ projects }: { projects: Project[] }) {
  const [project, setProject] = useState(() => {
    try {
      return localStorage.getItem(lastProjectKey) ?? "";
    } catch {
      return ""; // storage blocked: start on the first Project
    }
  });
  const chosen = projects.find((p) => p.path === project) ?? projects[0];
  const [board, setBoard] = useState<BoardData | undefined>();
  const [open, setOpen] = useState<string | undefined>();

  // Refresh bumps reloads to read again, past fh's GitHub cache; the ref
  // marks that next read alone, so later Project switches use the cache.
  const [reloads, setReloads] = useState(0);
  const refreshNext = useRef(false);

  // Keyed on the path, not the Project object: every projects event
  // brings new objects, and the board shouldn't be read again for those.
  const chosenPath = chosen?.path;
  useEffect(() => {
    if (!chosenPath) return;
    const url = `/api/board?project=${encodeURIComponent(chosenPath)}`;
    // get never rejects: a failed read resolves to undefined, shown as no board.
    const read = (refresh = false) =>
      void get<BoardData>(refresh ? `${url}&refresh=1` : url).then(setBoard);
    read(refreshNext.current);
    refreshNext.current = false;
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") read();
    }, refreshEvery);
    return () => window.clearInterval(timer);
  }, [chosenPath, reloads]);

  if (!chosen) {
    return (
      <div className="panel">
        <h2>Software factory</h2>
        <p>Add a Project first, with + Add Project in the left rail.</p>
      </div>
    );
  }
  const feature = board?.features.find((f) => f.id === open);
  return (
    <section className="board" aria-label="Software factory">
      <header className="board-head">
        <h2>Software factory</h2>
        <select
          aria-label="Project"
          value={chosen.path}
          onChange={(e) => {
            setProject(e.target.value);
            setOpen(undefined);
            try {
              localStorage.setItem(lastProjectKey, e.target.value);
            } catch {
              // storage blocked: the choice just isn't remembered
            }
          }}
        >
          {projects.map((p) => (
            <option key={p.path} value={p.path}>
              {p.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          className="link"
          onClick={() => {
            refreshNext.current = true;
            setReloads((n) => n + 1);
          }}
        >
          Refresh
        </button>
      </header>
      {board?.note && <p className="notice">{board.note}</p>}
      <div className="columns">
        {columns.map((c) => (
          <div key={c.stage} className="column" aria-label={c.name}>
            <h3>{c.name}</h3>
            {c.stage === "idea" && <p className="empty">Ideas arrive with Grill new idea (#14).</p>}
            {board?.features
              .filter((f) => f.stage === c.stage)
              .map((f) => (
                <button
                  key={f.id}
                  type="button"
                  className="fcard"
                  aria-pressed={f.id === open}
                  onClick={() => setOpen(f.id === open ? undefined : f.id)}
                >
                  <span className="t">{f.title}</span>
                  <span className="s">{breakdown(f)}</span>
                  {f.note && <span className="s note">{f.note}</span>}
                </button>
              ))}
          </div>
        ))}
      </div>
      {feature && <FeatureDetail feature={feature} />}
    </section>
  );
}

function breakdown(f: Feature): string {
  if (f.tickets.length === 0) return "no Tickets yet";
  return [...columns]
    .reverse()
    .filter((c) => f.breakdown[c.stage])
    .map((c) => `${f.breakdown[c.stage]} ${c.name.toLowerCase()}`)
    .join(" · ");
}

function FeatureDetail({ feature }: { feature: Feature }) {
  return (
    <section className="fdetail" aria-label={`Feature ${feature.title}`}>
      <h3>{feature.title}</h3>
      <p className="crumb">
        Stage: {stageName[feature.stage]}
        {feature.tickets.length > 0 && " (earliest open Ticket)"}
      </p>
      {feature.tickets.length === 0 && <p className="empty">No Tickets yet.</p>}
      <ul className="tickets">
        {feature.tickets.map((t) => (
          <li key={t.number} className={t.closed ? "closed" : undefined}>
            <span className="num">{t.number}</span>
            <span className="t">{t.title}</span>
            <span className="tag">{t.label || "no label"}</span>
            <span className="tag">{stageName[t.stage]}</span>
            {t.startable && <span className="tag startable">Startable</span>}
            {t.blockers.some((b) => b.open) && (
              <span className="s">
                blocked by{" "}
                {t.blockers
                  .filter((b) => b.open)
                  .map((b) => (b.known ? b.number : `${b.number} (unknown)`))
                  .join(", ")}
              </span>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
