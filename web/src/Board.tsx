import { useEffect, useRef, useState } from "react";
import { command, get } from "./api";
import type { Project } from "./events";
import type { StartFrom } from "./Sessions";

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
  branch: string; // Start Session's new Worktree
  ref: string; // how a skill prompt names it, e.g. "#42"
};
type Feature = {
  id: string;
  title: string;
  closed: boolean;
  stage: Stage;
  note?: string;
  tickets: Ticket[];
  breakdown: Partial<Record<Stage, number>>;
  branch: string; // Implement spec's new Worktree
  ref: string;
  idea?: string; // the Idea it was linked from (#14)
};
// Idea matches internal/app's ideaView: an Idea without its Spec yet, and
// the Specs that appeared since it started.
type Idea = {
  session: string;
  name: string;
  suggest: { id: string; title: string; ref: string }[];
};
type BoardData = { tracker: string; note?: string; features: Feature[]; ideas: Idea[] };

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

// nextStep is the button a Feature at stage offers: the skill that moves it
// on (spec #1, story 72). Implement spec (implements) runs in its own new
// Worktree, whose branch is its integration branch, and takes every open
// Ticket; the others write issues, so they start in the main tree.
const makeTickets = { label: "Make tickets", skill: "to-tickets", implements: false };
const implementSpec = { label: "Implement spec", skill: "implement-spec", implements: true };
const nextStep: Partial<Record<Stage, typeof makeTickets>> = {
  spec: makeTickets,
  tickets: implementSpec,
  implementing: implementSpec,
  review: implementSpec,
  shipped: { label: "Retro", skill: "retro", implements: false },
};

function mainTree(p: Project): string {
  return p.worktrees.find((w) => w.main)?.path ?? p.path;
}

// Board is the Software factory: one Project's Features by Stage. It reads
// the board while it's shown, every 60 s and on Refresh; nothing is read
// for boards nobody looks at. Its buttons open the Start dialog (onStart).
export function Board({
  projects,
  onStart,
}: {
  projects: Project[];
  onStart: (from: StartFrom) => void;
}) {
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
        <button
          type="button"
          onClick={() =>
            onStart({
              title: "Grill new idea",
              project: chosen.path,
              skill: "grill-with-docs",
              idea: true,
            })
          }
        >
          Grill new idea
        </button>
        {(board?.tracker === "github" || board?.tracker === "local") && (
          <button
            type="button"
            onClick={() =>
              onStart({
                title: "Write spec",
                project: chosen.path,
                worktree: mainTree(chosen),
                skill: "to-spec",
              })
            }
          >
            Write spec
          </button>
        )}
      </header>
      {board?.note && (
        <p className="notice">
          {board.note}
          {board.tracker === "none" && (
            <>
              {" "}
              <button
                type="button"
                onClick={() =>
                  onStart({
                    title: "Set up tracker",
                    project: chosen.path,
                    worktree: mainTree(chosen),
                    skill: "setup-matt-pocock-skills",
                  })
                }
              >
                Set up tracker
              </button>
            </>
          )}
        </p>
      )}
      <div className="columns">
        {columns.map((c) => (
          <div key={c.stage} className="column" aria-label={c.name}>
            <h3>{c.name}</h3>
            {c.stage === "idea" && (
              <>
                {board?.ideas.length === 0 && (
                  <p className="empty">Start one with Grill new idea.</p>
                )}
                {board?.ideas.map((i) => (
                  <IdeaCard
                    key={i.session}
                    idea={i}
                    onLinked={() => {
                      refreshNext.current = true;
                      setReloads((n) => n + 1);
                    }}
                  />
                ))}
              </>
            )}
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
      {feature && (
        <FeatureDetail
          feature={feature}
          start={(from) => onStart({ project: chosen.path, ...from })}
          mainTree={mainTree(chosen)}
        />
      )}
    </section>
  );
}

// IdeaCard is one Idea in the Idea column. Once a Spec appears that its
// grilling may have written, it offers to link it: one click (story 74).
function IdeaCard({ idea, onLinked }: { idea: Idea; onLinked: () => void }) {
  const [error, setError] = useState("");
  const link = async (spec: string) => {
    const r = await command(`/api/sessions/${idea.session}/link-spec`, { spec });
    if (r.ok) onLinked();
    else setError(r.error);
  };
  return (
    <div className="fcard">
      <span className="t">{idea.name}</span>
      {idea.suggest.length === 0 && <span className="s">grilling: no Spec yet</span>}
      {idea.suggest.map((s) => (
        <button key={s.id} type="button" onClick={() => void link(s.id)}>
          Link {s.ref}: {s.title}
        </button>
      ))}
      {error && (
        <span className="s error" role="alert">
          {error}
        </span>
      )}
    </div>
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

function FeatureDetail({
  feature,
  start,
  mainTree,
}: {
  feature: Feature;
  start: (from: Omit<StartFrom, "project">) => void;
  mainTree: string;
}) {
  // A Spec is never Startable itself; it offers its Stage's next skill.
  const step = nextStep[feature.stage];
  return (
    <section className="fdetail" aria-label={`Feature ${feature.title}`}>
      <h3>{feature.title}</h3>
      <p className="crumb">
        Stage: {stageName[feature.stage]}
        {feature.tickets.length > 0 && " (earliest open Ticket)"}
        {feature.idea && ` · from Idea "${feature.idea}"`}
      </p>
      {step && (
        <p>
          <button
            type="button"
            onClick={() =>
              start({
                title: `${step.label}: ${feature.ref}`,
                skill: step.skill,
                ref: feature.ref,
                ...(step.implements
                  ? {
                      branch: feature.branch,
                      note: `(use this Worktree's branch, ${feature.branch}, as the integration branch)`,
                      link: { feature: feature.id, allTickets: true },
                    }
                  : { worktree: mainTree, link: { feature: feature.id } }),
              })
            }
          >
            {step.label}
          </button>
        </p>
      )}
      {feature.tickets.length === 0 && <p className="empty">No Tickets yet.</p>}
      <ul className="tickets">
        {feature.tickets.map((t) => (
          <li key={t.number} className={t.closed ? "closed" : undefined}>
            <span className="num">{t.number}</span>
            <span className="t">{t.title}</span>
            <span className="tag">{t.label || "no label"}</span>
            <span className="tag">{stageName[t.stage]}</span>
            {t.startable && <span className="tag startable">Startable</span>}
            {t.startable && (
              <button
                type="button"
                onClick={() =>
                  start({
                    title: `Start Session on ${t.ref}`,
                    branch: t.branch,
                    skill: "implement",
                    ref: t.ref,
                    link: { feature: feature.id, ticket: t.number },
                  })
                }
              >
                Start Session
              </button>
            )}
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
