import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { get } from "./api";
import { byListOrder, projectName, type Project, type SessionSummary } from "./events";

// SwitcherTicket is one open Ticket /api/switcher matched (internal/switcher).
type SwitcherTicket = { number: string; title: string; feature: string; featureTitle: string };

type Entry = {
  key: string;
  label: ReactNode;
  detail: string;
  state?: string;
  pick: () => void;
};

// Switcher is ⌘K (Ctrl+K off macOS), GLOSSARY.md: find a Session by words in
// its title, Project or Agent, in any order; an open Ticket of the board's
// Project by number or by words in its title (#195); or start a New Session.
// fh works out which Tickets match (GET /api/switcher), from the board it
// already has, so typing never calls GitHub per key press.
export function Switcher(props: {
  sessions: SessionSummary[];
  projects: Project[];
  // boardProject is the Project the Software factory shows, whose Tickets
  // the Switcher finds; none lists Sessions only.
  boardProject?: Project;
  onOpen: (id: string) => void;
  onOpenTicket: (project: string, feature: string, ticket: string) => void;
  onNew: () => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  // Option ids come from the Entry key, so the input's aria-activedescendant
  // names the same row while the list around it changes.
  const listId = useId();
  const optionId = (e: Entry) => `${listId}-${e.key}`;

  // The Tickets for the query, kept with the query they answer, so a late
  // answer to an older query is never shown under a newer one.
  const [found, setFound] = useState<{ query: string; tickets: SwitcherTicket[] }>();
  const projectPath = props.boardProject?.path;
  useEffect(() => {
    const q = query.trim();
    if (!projectPath || !q) return;
    let current = true;
    // A short pause, so a fast typist sends one request, not one per key.
    const timer = window.setTimeout(() => {
      void get<{ tickets: SwitcherTicket[] }>(
        `/api/switcher?project=${encodeURIComponent(projectPath)}&q=${encodeURIComponent(q)}`,
      ).then((r) => {
        if (current) setFound({ query: q, tickets: r?.tickets ?? [] });
      });
    }, 80);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [query, projectPath]);
  const entries = useMemo(() => {
    const tickets = projectPath && found?.query === query.trim() ? found.tickets : [];
    const words = query.toLowerCase().split(/\s+/).filter(Boolean);
    const matches = (text: string) => words.every((w) => text.includes(w));
    const sessions: Entry[] = [...props.sessions]
      .sort(byListOrder)
      .filter((s) =>
        matches(
          `${s.title} ${projectName(props.projects, s.project)} ${s.agentName}`.toLowerCase(),
        ),
      )
      .map((s) => ({
        key: s.id,
        label: s.title,
        detail: `${projectName(props.projects, s.project)} · ${s.agentName}`,
        state: s.state,
        pick: () => props.onOpen(s.id),
      }));
    const ticketEntries: Entry[] = tickets.map((t) => ({
      key: `ticket:${t.feature}:${t.number}`,
      label: (
        <>
          <span className="ptag">Ticket</span>
          <span className="pnum">#{t.number}</span>
          {t.title}
          <span className="s"> · {t.featureTitle}</span>
        </>
      ),
      detail: "→ Ticket row",
      pick: () => projectPath && props.onOpenTicket(projectPath, t.feature, t.number),
    }));
    const extra: Entry[] = matches("new session")
      ? [{ key: "new", label: "New Session", detail: "start an Agent", pick: props.onNew }]
      : [];
    return [...sessions, ...ticketEntries, ...extra];
  }, [query, props, found, projectPath]);

  // The highlighted row stays in sight when the keys move it past the
  // list's edge; "nearest" leaves the list still if the row already shows.
  const activeId = entries[active] && optionId(entries[active]);
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
          placeholder="Find a Session, or a Ticket by number or title…"
          aria-label="Find a Session or Ticket"
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
            <li
              key={e.key}
              id={optionId(e)}
              role="option"
              aria-selected={i === active}
              onMouseEnter={() => setActive(i)}
              onClick={() => choose(e)}
            >
              {e.state && <span className={`dot ${e.state}`} aria-hidden="true" />}
              <span className="t">{e.label}</span>
              <span className="s">{e.detail}</span>
            </li>
          ))}
          {entries.length === 0 && <li className="empty">No match.</li>}
        </ul>
      </div>
    </div>
  );
}
