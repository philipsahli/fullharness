import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { get } from "./api";
import { byListOrder, projectName, type Project, type SessionSummary } from "./events";

// SwitcherResult is one item /api/switcher found (internal/switcher): a
// Spec, Ticket, PR or issue, with its Project and where picking it goes.
export type SwitcherResult = {
  kind: "spec" | "ticket" | "pr" | "issue";
  // project is empty for an item in a repo that isn't a Project
  // (owner/repo#N, #256); projectName is then "owner/repo".
  project: string;
  projectName: string;
  number: string;
  title: string;
  detail?: string;
  // state is set for a closed result, which the Switcher dims (#255).
  state?: "closed" | "merged" | "shipped";
  opens: {
    place: "feature" | "ticket" | "triage" | "github";
    feature?: string;
    ticket?: string;
    ref?: string;
    url?: string;
  };
};

const kindName = { spec: "Spec", ticket: "Ticket", pr: "PR", issue: "Issue" } as const;
const stateName = { closed: "Closed", merged: "Merged", shipped: "Shipped" } as const;
const placeName = {
  feature: "→ Feature page",
  ticket: "→ Ticket row",
  triage: "→ Needs triage",
  github: "GitHub ↗",
} as const;

// A query that is a number searches by number: "42", "#42", owner/repo#42
// or a github.com link, with an optional type word before it ("PR 42",
// #256). The list then comes grouped under Sessions and each Project's
// name. internal/switcher reads the same forms.
const isNumber = (q: string) =>
  /^((spec|ticket|pr|issue)\s+)?(#?\d+|[\w.-]+\/[\w.-]+#\d+|https?:\/\/\S+)$/i.test(q.trim());

// refLabel names a number query in the footer and the empty note (#255):
// "42" for a number, with or without "#" or a type word, and
// "owner/repo#42" for one that names its repo, a pasted link included.
const refLabel = (q: string) => {
  const t = q.trim().replace(/^(spec|ticket|pr|issue)\s+/i, "");
  const link = /github\.com\/([\w.-]+\/[\w.-]+)\/(?:issues|pull)\/(\d+)/i.exec(t);
  return link ? `${link[1]}#${link[2]}` : t.replace(/^#/, "");
};

type Entry = {
  key: string;
  label: ReactNode;
  detail: string;
  state?: string;
  closed?: boolean;
  pick: () => void;
  group?: ReactNode; // the heading above this entry, when it starts a group
};

// Switcher is ⌘K (Ctrl+K off macOS), GLOSSARY.md. Words find a Session by
// its title, Project or Agent, in any order, and an open Ticket of the
// board's Project by its title (#195). A number finds every open Spec,
// Ticket, PR and issue with that number in all Projects, the board's
// Project first (#254). Closed and merged ones follow, dimmed, once you
// stop typing for 300 ms: then fh also asks GitHub for a number no board
// has, once per Project, and keeps the answer for 5 min (#255). New Session
// starts an Agent. fh works out the items (GET /api/switcher) from the
// boards it already has, so typing never calls GitHub per key press.
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

  // The items for the query, kept with the query they answer, so a late
  // answer to an older query is never shown under a newer one. looked is
  // true once the answer includes the GitHub lookup.
  const [found, setFound] = useState<{
    query: string;
    results: SwitcherResult[];
    looked: boolean;
  }>();
  const projectPath = props.boardProject?.path;
  useEffect(() => {
    const q = query.trim();
    if (!projectPath || !q) return;
    let current = true;
    const ask = (looked: boolean) =>
      void get<{ results: SwitcherResult[] }>(
        `/api/switcher?project=${encodeURIComponent(projectPath)}&q=${encodeURIComponent(q)}` +
          (looked ? "&lookup=1" : ""),
      ).then((r) => {
        if (!current) return;
        // The lookup's answer has everything; a slower plain one mustn't
        // replace it.
        setFound((f) =>
          !looked && f?.query === q && f.looked
            ? f
            : { query: q, results: r?.results ?? [], looked },
        );
      });
    // A short pause, so a fast typist sends one request, not one per key;
    // a longer one before a number asks GitHub.
    const timers = [window.setTimeout(() => ask(false), 80)];
    if (isNumber(q)) timers.push(window.setTimeout(() => ask(true), 300));
    return () => {
      current = false;
      timers.forEach((t) => window.clearTimeout(t));
    };
  }, [query, projectPath]);
  const answer = projectPath && found?.query === query.trim() ? found : undefined;
  const entries = useMemo(() => {
    // Closed results wait for the lookup, so they arrive together.
    const results = (answer?.results ?? []).filter((r) => answer?.looked || !r.state);
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
          {(r.detail || r.state) && (
            <span className="s">
              {" · "}
              {[r.detail, r.state && stateName[r.state]].filter(Boolean).join(" · ")}
            </span>
          )}
        </>
      ),
      detail: placeName[r.opens.place],
      closed: !!r.state,
      pick: () => props.onOpenResult(r),
      // A repo that isn't a Project gets no heading: its row says so.
      group:
        grouped && r.project && (i === 0 || results[i - 1].project !== r.project) ? (
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
  }, [query, props, answer, projectPath]);
  const number = isNumber(query) ? refLabel(query) : undefined;
  const looked = !!answer?.looked;
  const closedCount = looked ? (answer?.results.filter((r) => r.state).length ?? 0) : 0;

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
        aria-label="Switcher"
        onClick={(e) => e.stopPropagation()}
      >
        <input
          ref={input}
          value={query}
          placeholder="Find a Session, a Ticket by title, or anything by number…"
          aria-label="Find a Session, Ticket or number"
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
            } else if (e.key === "Escape") {
              props.onClose();
            }
          }}
        />
        <ul role="listbox" aria-label="Matches">
          {entries.map((e, i) => (
            <Fragment key={e.key}>
              {e.group && (
                <li className="pgroup" role="presentation">
                  {e.group}
                </li>
              )}
              <li
                role="option"
                className={e.closed ? "closed" : undefined}
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
          {entries.length === 0 && (
            <li className="empty">
              {number === undefined
                ? "No match."
                : looked
                  ? `Nothing numbered ${number} in your Projects.`
                  : `No open item numbered ${number}.`}
            </li>
          )}
        </ul>
        {number !== undefined && projectPath && (
          <div className="pal-foot" aria-live="polite">
            {looked ? (
              <span>
                Open items from the board · closed and merged ones from GitHub, cached for 5 min
                {closedCount > 0 && ` · ${closedCount} closed`}
              </span>
            ) : (
              <>
                <span className="spin" aria-hidden="true" />
                <span>
                  Looking up closed and merged {/^\d+$/.test(number) ? `#${number}` : number} on
                  GitHub…
                </span>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
