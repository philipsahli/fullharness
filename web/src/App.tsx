import {
  useCallback,
  useEffect,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import { shortcutLabel, useEventChannel, type Connection, type Notify } from "./events";
import { Projects } from "./Projects";
import { Conversation, Inbox, NewSession, SessionList, type StartFrom } from "./Sessions";
import { Switcher } from "./Switcher";
import { Board, boardProject, type BoardFocus } from "./Board";
import { Roadmap } from "./Roadmap";
import { awaitingUser } from "./StopPoints";

type View = "sessions" | "factory" | "roadmap" | "inbox";
// The header tabs in screen order: the header renders them from this list,
// and ArrowLeft/ArrowRight step through it.
const tabs: { view: View; label: string }[] = [
  { view: "sessions", label: "Sessions" },
  { view: "factory", label: "Software factory" },
  { view: "roadmap", label: "Roadmap" },
  { view: "inbox", label: "Inbox" },
];
const views = tabs.map((t) => t.view);

export function App() {
  const [view, setView] = useState<View>("sessions");
  // What the Sessions view shows: the New Session dialog, or a Session.
  const [opened, setOpened] = useState<"new" | string | undefined>();
  // A board button's prefill for the New Session dialog (#13).
  const [startFrom, setStartFrom] = useState<StartFrom | undefined>();
  const newSession = (from?: StartFrom) => {
    setStartFrom(from);
    setView("sessions");
    setOpened("new");
  };
  const [notifyPermission, setNotifyPermission] = useState(() =>
    "Notification" in window ? Notification.permission : "denied",
  );
  // A Session started Waiting while nobody looks at it. Every open tab shows
  // it; the tag makes the browser keep one per Session, not one per tab.
  const showNotification = useCallback((n: Notify) => {
    if (!("Notification" in window) || Notification.permission !== "granted") return;
    const note = new Notification(n.title, { body: n.body, tag: n.session });
    note.onclick = () => {
      window.focus();
      setView("sessions");
      setOpened(n.session);
      note.close();
    };
  }, []);
  const [conn, update, reconnect] = useEventChannel(showNotification);

  const [switcher, setSwitcher] = useState(false);
  // A Feature the Roadmap's Open, or a Ticket a Switcher pick, asked the
  // board to show. Picking a tab yourself drops it, so the board opens as
  // you left it. The board reads focus only when it mounts, so each new
  // focus remounts it (focusKey), even when the board is already shown.
  const [focus, setFocus] = useState<BoardFocus | undefined>();
  const [focusKey, setFocusKey] = useState(0);
  const showOnBoard = (f: BoardFocus) => {
    setFocus(f);
    setFocusKey((n) => n + 1);
    setView("factory");
  };
  const pickTab = (v: View) => {
    setFocus(undefined);
    setView(v);
  };
  // ARIA tabs pattern: arrows select the next/previous tab, wrapping at the
  // ends, and focus follows because only the selected tab is focusable.
  const onTabKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    e.preventDefault();
    const step = e.key === "ArrowRight" ? 1 : -1;
    const next = views[(views.indexOf(view) + step + views.length) % views.length];
    pickTab(next);
    document.getElementById(`tab-${next}`)?.focus();
  };

  // ⌘K on macOS, Ctrl+K elsewhere, from anywhere in the page.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSwitcher((open) => !open);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const snapshot = conn.status === "open" ? conn.snapshot : undefined;
  const inboxCount = (snapshot?.inbox.length ?? 0) + awaitingUser(snapshot?.autopilot ?? []).length;

  return (
    <div className="app" data-connection={conn.status}>
      <header className="top">
        <div className="brand">
          <b>fh</b>
          <span>fullharness</span>
        </div>
        {notifyPermission === "default" && (
          <button
            className="notify-on"
            type="button"
            onClick={() =>
              void Notification.requestPermission().then((p) => {
                setNotifyPermission(p);
                if (p === "granted") reconnect(); // tell fh this tab can notify now
              })
            }
            title="Get a notification when a Session needs you and no fh tab shows it"
          >
            Turn on notifications
          </button>
        )}
        <button className="jump" type="button" onClick={() => setSwitcher(true)}>
          Find a Session or Ticket… <kbd>{shortcutLabel}</kbd>
        </button>
        <div className="tabs" role="tablist" aria-label="Views" onKeyDown={onTabKey}>
          {tabs.map((t) => (
            <Tab key={t.view} view={t.view} current={view} onSelect={pickTab}>
              {t.label}
              {t.view === "inbox" && (
                <>
                  {" "}
                  <span className="count" data-zero={inboxCount === 0 || undefined}>
                    {inboxCount}
                  </span>
                </>
              )}
            </Tab>
          ))}
        </div>
      </header>
      <ConnectionBanner conn={conn} />
      {switcher && snapshot && (
        <Switcher
          sessions={snapshot.sessions}
          projects={snapshot.projects}
          boardProject={boardProject(snapshot.projects)}
          onOpen={(id) => {
            setView("sessions");
            setOpened(id);
          }}
          onOpenTicket={(project, feature, ticket) => showOnBoard({ project, feature, ticket })}
          onNew={() => newSession()}
          onClose={() => setSwitcher(false)}
        />
      )}
      <div className="shell" data-view={view}>
        <nav className="rail" aria-label="Sessions and Projects">
          {snapshot && (
            <SessionList
              sessions={snapshot.sessions}
              projects={snapshot.projects}
              current={opened}
              onOpen={(id) => {
                setView("sessions");
                setOpened(id);
              }}
              onNew={() => newSession()}
            />
          )}
          {snapshot && <Projects projects={snapshot.projects} sessions={snapshot.sessions} />}
        </nav>
        <main className="main" role="tabpanel" aria-labelledby={`tab-${view}`}>
          {view === "sessions" && sessionsView()}
          {view === "factory" && snapshot && (
            <Board
              key={focusKey}
              projects={snapshot.projects}
              autopilot={snapshot.autopilot}
              onStart={newSession}
              focus={focus}
              onOpenSession={(id) => {
                setView("sessions");
                setOpened(id);
              }}
            />
          )}
          {view === "roadmap" && (
            <Roadmap
              autopilot={snapshot?.autopilot ?? []}
              onOpen={(project, feature) => showOnBoard({ project, feature })}
            />
          )}
          {view === "inbox" && snapshot && (
            <Inbox
              inbox={snapshot.inbox}
              autopilot={snapshot.autopilot}
              onOpen={(id) => {
                setView("sessions");
                setOpened(id);
              }}
            />
          )}
        </main>
      </div>
    </div>
  );

  // sessionsView is a plain function, not a component, so the New Session
  // form keeps its input when an event re-renders App.
  function sessionsView() {
    if (!snapshot) return null;
    if (opened === "new") {
      return (
        <NewSession
          // A new prefill starts the form over.
          key={startFrom ? JSON.stringify(startFrom) : "new"}
          projects={snapshot.projects}
          sessions={snapshot.sessions}
          from={startFrom}
          onStarted={setOpened}
          onCancel={() => setOpened(undefined)}
        />
      );
    }
    const session = snapshot.sessions.find((s) => s.id === opened);
    if (session) return <Conversation session={session} snapshot={snapshot} update={update} />;
    return (
      <Empty title="Sessions" text="Start one with + New Session, or open one from the list." />
    );
  }
}

function Tab(props: {
  view: View;
  current: View;
  onSelect: (v: View) => void;
  children: ReactNode;
}) {
  return (
    <button
      className="tab"
      role="tab"
      id={`tab-${props.view}`}
      aria-selected={props.view === props.current}
      tabIndex={props.view === props.current ? 0 : -1}
      onClick={() => props.onSelect(props.view)}
    >
      {props.children}
    </button>
  );
}

function Empty(props: { title: string; text: string }) {
  return (
    <div className="mhead-empty">
      <h2>{props.title}</h2>
      <p>{props.text}</p>
    </div>
  );
}

function ConnectionBanner({ conn }: { conn: Connection }) {
  switch (conn.status) {
    case "open":
      return null;
    case "connecting":
      return <div className="banner">Connecting to fh…</div>;
    case "lost":
      return <div className="banner">Lost connection to fh. Retrying…</div>;
    case "restarted":
      return (
        <div className="banner bad" role="alert">
          fh restarted. Run <code>fh</code> to open it again.
        </div>
      );
  }
}
