import { Fragment, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { get } from "./api";
import { byListOrder, projectName, type Project, type SessionSummary } from "./events";
import { optionIds } from "./optionIds";

// SwitcherResult is one item /api/switcher found (internal/switcher): a
// Spec, Ticket, PR or issue, with its Project and where picking it goes.
export type SwitcherResult = {
  kind: "spec" | "ticket" | "pr" | "issue";
  project: string;
  projectName: string;
  number: string;
  title: string;
  detail?: string;
  opens: {
    place: "feature" | "ticket" | "triage" | "github";
    feature?: string;
    ticket?: string;
    ref?: string;
    url?: string;
  };
};

const kindName = { spec: "Spec", ticket: "Ticket", pr: "PR", issue: "Issue" } as const;
const placeName = {
  feature: "→ Feature page",
  ticket: "→ Ticket row",
  triage: "→ Needs triage",
  github: "GitHub ↗",
} as const;

// A query that is a number, with or without "#", searches every Project;
// the list then comes grouped under Sessions and each Project's name.
const isNumber = (q: string) => /^#?\d+$/.test(q.trim());

type Entry = {
  key: string;
  label: ReactNode;
  detail: string;
  state?: string;
  pick: () => void;
  group?: ReactNode; // the heading above this entry, when it starts a group
};

// Switcher is ⌘K (Ctrl+K off macOS), GLOSSARY.md. Words find a Session by
// its title, Project or Agent, in any order, and an open Ticket of the
// board's Project by its title (#195). A number finds every open Spec,
// Ticket, PR and issue with that number in all Projects, the board's
// Project first (#254). New Session starts an Agent. fh works out the
// items (GET /api/switcher) from the boards it already has, so typing
// never calls GitHub per key press.
export function Switcher(props: {
  sessions: SessionSummary[];
  projects: Project[];
  // boardProject is the Project the Software factory shows: its Tickets
  // match by words, and its items come first for a number.
  boardProject?: Project;
  onOpen: (id: string) => void;
  onOpenResult: (r: SwitcherResult) => void;
  onNew: () => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  // Option ids come from the Entry key (optionIds), so the input's
  // aria-activedescendant names the same row while the list around it
  // changes.
  const listId = useId();

  // The items for the query, kept with the query they answer, so a late
  // answer to an older query is never shown under a newer one.
  const [found, setFound] = useState<{ query: string; results: SwitcherResult[] }>();
  const projectPath = props.boardProject?.path;
  useEffect(() => {
    const q = query.trim();
    if (!projectPath || !q) return;
    let current = true;
    // A short pause, so a fast typist sends one request, not one per key.
    const timer = window.setTimeout(() => {
      void get<{ results: SwitcherResult[] }>(
        `/api/switcher?project=${encodeURIComponent(projectPath)}&q=${encodeURIComponent(q)}`,
      ).then((r) => {
        if (current) setFound({ query: q, results: r?.results ?? [] });
      });
    }, 80);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [query, projectPath]);
  const entries = useMemo(() => {
    const results = projectPath && found?.query === query.trim() ? found.results : [];
    const words = query.toLowerCase().split(/\s+/).filter(Boolean);
    const matches = (text: string) => words.every((w) => text.includes(w));
    const grouped = isNumber(query);
    const sessions: Entry[] = [...props.sessions]
      .sort(byListOrder)
      .filter((s) =>
        matches(
          `${s.title} ${projectName(props.projects, s.project)} ${s.agentName}`.toLowerCase(),
        ),
      )
      .map((s, i) => ({
        key: s.id,
        label: s.title,
        detail: `${projectName(props.projects, s.project)} · ${s.agentName}`,
        state: s.state,
        pick: () => props.onOpen(s.id),
        group: grouped && i === 0 ? "Sessions" : undefined,
      }));
    const items: Entry[] = results.map((r, i) => ({
      key: `${r.project}:${r.kind}:${r.opens.feature ?? ""}:${r.number}`,
      label: (
        <>
          <span className="ptag">{kindName[r.kind]}</span>
          <span className="pnum">#{r.number}</span>
          {r.title}
          {r.detail && <span className="s"> · {r.detail}</span>}
        </>
      ),
      detail: placeName[r.opens.place],
      pick: () => props.onOpenResult(r),
      group:
        grouped && (i === 0 || results[i - 1].project !== r.project) ? (
          <>
            {r.projectName}
            {r.project === projectPath && <small>· this Project</small>}
          </>
        ) : undefined,
    }));
    const extra: Entry[] = matches("new session")
      ? [{ key: "new", label: "New Session", detail: "start an Agent", pick: props.onNew }]
      : [];
    return [...sessions, ...items, ...extra];
  }, [query, props, found, projectPath]);

  // The highlighted row stays in sight when the keys move it past the
  // list's edge; "nearest" leaves the list still if the row already shows.
  const ids = optionIds(
    listId,
    entries.map((e) => e.key),
  );
  const activeId = ids[active];
  useEffect(() => {
    if (activeId) document.getElementById(activeId)?.scrollIntoView({ block: "nearest" });
  }, [activeId]);

  const choose = (e: Entry | undefined) => {
    if (!e) return;
    e.pick();
    props.onClose();
  };

  return (
    <div className="overlay" onClick={props.onClose}>
      <div
        className="switcher"
        role="dialog"
        aria-modal="true"
        aria-label="Switcher"
        onClick={(e) => e.stopPropagation()}
        // Keys on the dialog, not the input, so they work wherever focus is
        // inside it. The input is the only thing to focus here: Tab keeps
        // focus on it, so focus never reaches the page behind until the
        // Switcher closes.
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            props.onClose();
          } else if (e.key === "Tab") {
            e.preventDefault();
            input.current?.focus();
          }
        }}
        // A click on a row picks it with the mouse; keep focus on the input
        // so the keys above still reach the dialog.
        onMouseDown={(e) => {
          if (e.target !== input.current) e.preventDefault();
        }}
      >
        <input
          ref={input}
          value={query}
          placeholder="Find a Session, a Ticket by title, or anything by number…"
          aria-label="Find a Session, Ticket or number"
          role="combobox"
          aria-controls={listId}
          aria-expanded="true"
          aria-autocomplete="list"
          aria-activedescendant={activeId}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setActive((a) => Math.min(a + 1, entries.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setActive((a) => Math.max(a - 1, 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              choose(entries[active]);
            }
          }}
        />
        <ul id={listId} role="listbox" aria-label="Matches">
          {entries.map((e, i) => (
            <Fragment key={e.key}>
              {e.group && (
                <li className="pgroup" role="presentation">
                  {e.group}
                </li>
              )}
              <li
                id={ids[i]}
                role="option"
                aria-selected={i === active}
                onMouseEnter={() => setActive(i)}
                onClick={() => choose(e)}
              >
                {e.state && <span className={`dot ${e.state}`} aria-hidden="true" />}
                <span className="t">{e.label}</span>
                <span className="s">{e.detail}</span>
              </li>
            </Fragment>
          ))}
          {entries.length === 0 && <li className="empty">No match.</li>}
        </ul>
      </div>
    </div>
  );
}
