import { useState, type ReactNode } from "react";
import { useEventChannel, type Connection } from "./events";
import { Projects } from "./Projects";
import { Conversation, NewSession, SessionList } from "./Sessions";

type View = "sessions" | "factory" | "inbox";

export function App() {
  const [conn, update] = useEventChannel();
  const [view, setView] = useState<View>("sessions");
  // What the Sessions view shows: the New Session dialog, or a Session.
  const [opened, setOpened] = useState<"new" | string | undefined>();
  const snapshot = conn.status === "open" ? conn.snapshot : undefined;
  const inboxCount = snapshot?.inbox.length ?? 0;

  return (
    <div className="app" data-connection={conn.status}>
      <header className="top">
        <div className="brand">
          <b>fh</b>
          <span>fullharness</span>
        </div>
        <div className="tabs" role="tablist" aria-label="Views">
          <Tab view="sessions" current={view} onSelect={setView}>
            Sessions
          </Tab>
          <Tab view="factory" current={view} onSelect={setView}>
            Software factory
          </Tab>
          <Tab view="inbox" current={view} onSelect={setView}>
            Inbox{" "}
            <span className="count" data-zero={inboxCount === 0 || undefined}>
              {inboxCount}
            </span>
          </Tab>
        </div>
      </header>
      <ConnectionBanner conn={conn} />
      <div className="shell" data-view={view}>
        <nav className="rail" aria-label="Sessions and Projects">
          {snapshot && (
            <SessionList
              sessions={snapshot.sessions}
              current={opened}
              onOpen={(id) => {
                setView("sessions");
                setOpened(id);
              }}
              onNew={() => {
                setView("sessions");
                setOpened("new");
              }}
            />
          )}
          {snapshot && <Projects projects={snapshot.projects} />}
        </nav>
        <main className="main" role="tabpanel" aria-labelledby={`tab-${view}`}>
          {view === "sessions" && sessionsView()}
          {view === "factory" && (
            <Empty title="Software factory" text="Each Project's Features appear here by Stage." />
          )}
          {view === "inbox" && <Empty title="Inbox" text="Nothing is waiting for your answer." />}
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
          projects={snapshot.projects}
          sessions={snapshot.sessions}
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
