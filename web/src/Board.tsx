import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { command, get } from "./api";
import type { AutopilotStatus, Project } from "./events";
import { AutopilotBadge, AutopilotLive, goToSwitch, OutsideNow, showsBadge } from "./Autopilot";
import { FeatureFlow } from "./Flow";
import { PRPart, type PR } from "./PR";
import { LinkOrText } from "./LinkOrText";
import type { StartFrom } from "./Sessions";

// These types match internal/tracker. A url opens the item in its tracker;
// the server leaves it out where it can't build one (#86).
type Stage = "idea" | "spec" | "tickets" | "implementing" | "review" | "shipped";
type Blocker = { number: string; known: boolean; open: boolean; url?: string };
// Step is one row of the server's next-step rule (internal/tracker/next.go).
type Step = { skill: string; label: string; implements?: boolean };
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
  url?: string;
  next?: Step; // offered only while startable
  prs: PR[]; // newest first; empty means No PR yet
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
  url?: string;
  next?: Step;
  approved?: boolean; // the Spec is labelled ready-for-agent
  roadmap?: string; // its Roadmap group: now, next or later (ADR 0005)
};

// A Ticket in one of these triage roles can be marked ready-for-agent.
const markable = ["needs-info", "needs-triage"];
// Idea matches internal/app's ideaView: an Idea without its Spec yet, and
// the Specs that appeared since it started.
type Idea = {
  session: string;
  name: string;
  suggest: { id: string; title: string; ref: string }[];
};
// Untriaged is an open issue the Software factory can't place
// (GLOSSARY.md), a row of the Needs triage group.
type Untriaged = {
  number?: string; // GitHub only
  ref: string; // "#42", or a local markdown file's path
  title: string;
  labels: string[];
  url?: string;
  note?: string; // e.g. a Ticket whose parent isn't on the board
};
type BoardData = {
  tracker: string;
  note?: string;
  features: Feature[];
  ideas: Idea[];
  untriaged: Untriaged[]; // newest first
};

// A Needs triage group with more rows than this starts collapsed.
const triageOpenUpTo = 5;

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

// boardProject is the Project the Software factory shows: the one last
// picked in its Project picker, or else the first Project. The Switcher
// finds that Project's Tickets (#195).
export function boardProject(projects: Project[]): Project | undefined {
  let stored = "";
  try {
    stored = localStorage.getItem(lastProjectKey) ?? "";
  } catch {
    // storage blocked: the first Project
  }
  return projects.find((p) => p.path === stored) ?? projects[0];
}

function mainTree(p: Project): string {
  return p.worktrees.find((w) => w.main)?.path ?? p.path;
}

// BoardFocus is a Feature the board opens on, as the Roadmap's Open asks;
// ticket also marks that Ticket's row, as a Switcher pick asks (#195).
export type BoardFocus = { project: string; feature: string; ticket?: string };

// Board is the Software factory: one Project's Features by Stage. It reads
// the board while it's shown, every 60 s and on Refresh; nothing is read
// for boards nobody looks at. Its buttons open the Start dialog (onStart).
// focus, read when the board is first shown, picks its Project and Feature.
export function Board({
  projects,
  autopilot,
  onStart,
  focus,
  onOpenSession,
}: {
  projects: Project[];
  autopilot: AutopilotStatus[];
  onStart: (from: StartFrom) => void;
  focus?: BoardFocus;
  onOpenSession: (id: string) => void;
}) {
  const [project, setProject] = useState(() => {
    if (focus) return focus.project;
    try {
      return localStorage.getItem(lastProjectKey) ?? "";
    } catch {
      return ""; // storage blocked: start on the first Project
    }
  });
  const chosen = projects.find((p) => p.path === project) ?? projects[0];
  const [board, setBoard] = useState<BoardData | undefined>();
  const [open, setOpen] = useState<string | undefined>(focus?.feature);
  // The Needs triage group's toggle; undefined until clicked, so its size
  // decides. Kept across board refreshes, not across reloads or Projects.
  const [triageOpen, setTriageOpen] = useState<boolean | undefined>();

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
            setTriageOpen(undefined);
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
          className="btn small"
          onClick={() => {
            refreshNext.current = true;
            setReloads((n) => n + 1);
          }}
        >
          Refresh
        </button>
        <button
          className="btn primary"
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
            className="btn"
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
                className="btn primary"
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
                {board && board.untriaged.length > 0 && (
                  <NeedsTriage
                    issues={board.untriaged}
                    open={triageOpen ?? board.untriaged.length <= triageOpenUpTo}
                    onToggle={(o) => setTriageOpen(o)}
                    onTriage={(u) =>
                      onStart({
                        title: `Triage ${u.ref}`,
                        project: chosen.path,
                        worktree: mainTree(chosen),
                        skill: "triage",
                        ref: u.ref,
                      })
                    }
                  />
                )}
                {board?.ideas.length === 0 && (
                  <p className="empty">No Ideas yet: start one with Grill new idea.</p>
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
              .map((f) => {
                const found = autopilot.find(
                  (x) => x.project === chosen.path && x.feature === f.id,
                );
                const ap = showsBadge(found) ? found : undefined;
                return (
                  <div key={f.id} className="fcard-wrap">
                    <button
                      type="button"
                      className="fcard"
                      data-ap={ap?.kind}
                      aria-pressed={f.id === open}
                      onClick={() => setOpen(f.id === open ? undefined : f.id)}
                    >
                      <span className="t">{f.title}</span>
                      {ap && <AutopilotBadge status={ap} />}
                      {ap && <AutopilotLive status={ap} />}
                      {ap?.outsideNow && <OutsideNow />}
                      <span className="s">{breakdown(f)}</span>
                      {f.note && <span className="s note">{f.note}</span>}
                    </button>
                    {/* Outside the card's button: a button can't hold one. */}
                    {ap?.reason === "autopilot-off" && (
                      <button
                        type="button"
                        className="ap-offlink"
                        onClick={() => goToSwitch(chosen.path)}
                      >
                        Autopilot off for {chosen.name} · <u>Turn on</u>
                      </button>
                    )}
                  </div>
                );
              })}
          </div>
        ))}
      </div>
      {feature && (
        <FeatureDetail
          feature={feature}
          project={chosen.path}
          start={(from) => onStart({ project: chosen.path, ...from })}
          mainTree={mainTree(chosen)}
          // fh expired its cached read with the write: a plain read shows it.
          onWritten={() => setReloads((n) => n + 1)}
          focusTicket={focus?.feature === feature.id ? focus.ticket : undefined}
          flow={
            <FeatureFlow
              project={chosen.path}
              feature={feature.id}
              title={feature.title}
              status={autopilot.find((x) => x.project === chosen.path && x.feature === feature.id)}
              boardKey={JSON.stringify([
                feature.stage,
                feature.closed,
                feature.approved,
                feature.tickets,
              ])}
              onOpenSession={onOpenSession}
            />
          }
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
        <button key={s.id} className="btn small" type="button" onClick={() => void link(s.id)}>
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

// NeedsTriage lists the Project's Untriaged issues at the top of the Idea
// column. Its count shows while it is collapsed. Triage is the user's to
// start: the button opens the Start dialog, and Autopilot never runs it.
function NeedsTriage({
  issues,
  open,
  onToggle,
  onTriage,
}: {
  issues: Untriaged[];
  open: boolean;
  onToggle: (open: boolean) => void;
  onTriage: (u: Untriaged) => void;
}) {
  return (
    <section className="triage" aria-label="Needs triage">
      <button
        type="button"
        className="triage-head"
        aria-expanded={open}
        onClick={() => onToggle(!open)}
      >
        <span className="caret" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        Needs triage <span className="count">{issues.length}</span>
      </button>
      {open && (
        <ul>
          {issues.map((u) => (
            <li key={u.ref}>
              <span className="num">
                <LinkOrText url={u.url}>{u.ref}</LinkOrText>
              </span>
              <span className="t">{u.title}</span>
              {u.labels.map((l) => (
                <span key={l} className="lbl">
                  {l}
                </span>
              ))}
              {u.note && <span className="s note">{u.note}</span>}
              <button className="btn small" type="button" onClick={() => onTriage(u)}>
                Triage
              </button>
            </li>
          ))}
        </ul>
      )}
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

// MarkReady is Mark ready-for-agent on a Spec or Ticket: fh writes the
// label itself (ADR 0008, #192). A failure shows gh's message beside it.
function MarkReady({
  project,
  itemRef,
  onWritten,
}: {
  project: string;
  itemRef: string;
  onWritten: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const mark = async () => {
    setBusy(true);
    setError("");
    const r = await command("/api/tracker/mark-ready", { project, ref: itemRef });
    setBusy(false);
    if (r.ok) onWritten();
    else setError(r.error);
  };
  return (
    <>
      <button className="btn small" type="button" disabled={busy} onClick={() => void mark()}>
        Mark ready-for-agent
      </button>
      {error && (
        <span className="error" role="alert">
          {error}
        </span>
      )}
    </>
  );
}

function FeatureDetail({
  feature,
  project,
  start,
  mainTree,
  onWritten,
  focusTicket,
  flow,
}: {
  feature: Feature;
  project: string;
  start: (from: Omit<StartFrom, "project">) => void;
  mainTree: string;
  onWritten: () => void;
  focusTicket?: string; // the Ticket a Switcher pick landed on: its row glows
  flow: ReactNode; // the Feature's flow graph (Flow.tsx)
}) {
  // Bring the picked Ticket's row into view once, when it first shows;
  // the board's later reads leave the scroll alone.
  const tickets = useRef<HTMLUListElement>(null);
  const hasFocusRow = feature.tickets.some((t) => t.number === focusTicket);
  useEffect(() => {
    if (hasFocusRow) {
      tickets.current?.querySelector(".sw-flash")?.scrollIntoView({ block: "nearest" });
    }
  }, [hasFocusRow]);
  // A Spec is never Startable itself; it offers its Stage's next skill.
  const step = feature.next;
  return (
    <section className="fdetail" aria-label={`Feature ${feature.title}`}>
      <h3>{feature.title}</h3>
      <p className="crumb">
        Spec <LinkOrText url={feature.url}>{feature.ref}</LinkOrText> ·{" "}
        <span className={`stage ${feature.stage}`}>{stageName[feature.stage]}</span>
        {!feature.closed && feature.tickets.length > 0 && " (earliest open Ticket)"}
        {feature.idea && ` · from Idea "${feature.idea}"`}
      </p>
      {!feature.closed && !feature.approved && (
        <p>
          <MarkReady
            key={feature.id}
            project={project}
            itemRef={feature.ref}
            onWritten={onWritten}
          />
        </p>
      )}
      {step && (
        <p>
          <button
            className="btn primary"
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
      {flow}
      {feature.tickets.length === 0 && <p className="empty">No Tickets yet.</p>}
      <ul className="ticketlist" ref={tickets}>
        {feature.tickets.map((t) => {
          // The Board offers a Ticket's next step only while it's Startable.
          const next = t.startable ? t.next : undefined;
          const cls = [t.closed && "closed", t.number === focusTicket && "sw-flash"].filter(
            Boolean,
          );
          return (
            <li key={t.number} className={cls.join(" ") || undefined}>
              <span className="num">
                <LinkOrText url={t.url}>#{t.number}</LinkOrText>
              </span>
              <span className="t">{t.title}</span>
              {t.label && <span className="lbl">{t.label}</span>}
              <span className={`stage ${t.stage}`}>{stageName[t.stage]}</span>
              {/* Startable is not a Stage: its own marker, beside the Stage. */}
              {t.startable && <span className="stage startable">Startable</span>}
              {next && (
                <button
                  className="btn small primary"
                  type="button"
                  onClick={() =>
                    start({
                      title: `${next.label} on ${t.ref}`,
                      branch: t.branch,
                      skill: next.skill,
                      ref: t.ref,
                      link: { feature: feature.id, ticket: t.number },
                    })
                  }
                >
                  {next.label}
                </button>
              )}
              {!t.closed && markable.includes(t.label) && (
                <MarkReady project={project} itemRef={t.ref} onWritten={onWritten} />
              )}
              <span className="prs">
                {t.prs.length === 0
                  ? "No PR yet"
                  : t.prs.map((p) => <PRPart key={p.number} pr={p} />)}
              </span>
              {t.blockers.some((b) => b.open) && (
                <span className="s">
                  blocked by {/* One element per blocker, so each number is its own link. */}
                  {t.blockers
                    .filter((b) => b.open)
                    .map((b, i) => (
                      <Fragment key={b.number}>
                        {i > 0 && ", "}
                        <LinkOrText url={b.url}>#{b.number}</LinkOrText>
                        {!b.known && " (unknown)"}
                      </Fragment>
                    ))}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
