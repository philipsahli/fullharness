import { useEffect, useMemo, useRef, useState } from "react";
import { byListOrder, type Project, type SessionSummary } from "./events";
import { projectName } from "./Sessions";

type Entry = { key: string; label: string; detail: string; state?: string; pick: () => void };

// Switcher is ⌘K (Ctrl+K off macOS): find a Session by words in its title,
// Project or Agent, in any order, or start a New Session.
export function Switcher(props: {
  sessions: SessionSummary[];
  projects: Project[];
  onOpen: (id: string) => void;
  onNew: () => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);

  const entries = useMemo(() => {
    const words = query.toLowerCase().split(/\s+/).filter(Boolean);
    const matches = (text: string) => words.every((w) => text.includes(w));
    const sessions: Entry[] = [...props.sessions]
      .sort(byListOrder)
      .filter((s) =>
        matches(`${s.title} ${projectName(props.projects, s.project)} ${s.agent}`.toLowerCase()),
      )
      .map((s) => ({
        key: s.id,
        label: s.title,
        detail: `${projectName(props.projects, s.project)} · ${s.agent}`,
        state: s.state,
        pick: () => props.onOpen(s.id),
      }));
    const extra: Entry[] = matches("new session")
      ? [{ key: "new", label: "New Session", detail: "start an Agent", pick: props.onNew }]
      : [];
    return [...sessions, ...extra];
  }, [query, props]);

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
        aria-label="Jump to a Session"
        onClick={(e) => e.stopPropagation()}
      >
        <input
          ref={input}
          value={query}
          placeholder="Jump to a Session…"
          aria-label="Search Sessions"
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
            <li
              key={e.key}
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
