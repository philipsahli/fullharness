import { useCallback, useEffect, useRef, useState } from "react";

// Worktree and Project match the JSON from internal/worktree and
// internal/project.
export type Worktree = {
  path: string;
  branch: string; // empty when HEAD is detached
  main: boolean; // the Project's own tree
  agent: boolean; // not created by fh: fh never starts a Session in it
  missing: boolean; // git lists it, but its folder is gone
  uncommitted: number;
  ahead: number;
  behind: number;
  base: string; // what ahead/behind compare with, e.g. "origin/main"
  fetchedAt: string | null; // last git fetch; null if never
};

export type Project = {
  path: string;
  name: string;
  missing: boolean; // folder not found
  worktrees: Worktree[];
};

// Link matches internal/tracker.Link: the Feature (and Ticket) a Session
// was started from on the board.
export type Link = { feature: string; ticket?: string };

// SessionSummary, Tool and Item match internal/session.
export type SessionState = "starting" | "working" | "waiting" | "idle" | "exited";

export type SessionSummary = {
  id: string;
  project: string;
  worktree: string;
  agent: string; // Agent ID, e.g. "claude"
  agentName: string; // e.g. "Claude Code"
  state: SessionState;
  title: string;
  autoApprove: boolean; // started in the Agent's auto mode
  waiting?: "permission" | "turn"; // why a Waiting Session waits
  since: string; // when the state last changed; orders the list
  login?: string; // the command to run when the reason is "login needed"
  canReopen: boolean; // its Agent can reload it (session/load) once Exited
  reason?: string; // why it Exited
  stderr?: string[]; // the Agent's last stderr lines, when it crashed
  link?: Link; // started from the board (#13)
};

export type Tool = {
  id: string;
  title: string;
  kind: string;
  status: string; // pending, in_progress, completed, failed
  content?: string;
  rawOutput?: string;
};

// PermissionRequest matches internal/session.Request: one permission
// request, as its card and its Inbox entry show it.
export type PermissionRequest = {
  id: string;
  session: string;
  sessionTitle: string;
  project: string;
  title: string;
  kind: string;
  command?: string;
  paths?: string[];
  content?: string;
  always: boolean; // the Agent offers "always allow"
  status: "pending" | "allowed" | "denied" | "always" | "cancelled" | "exited";
  createdAt: string;
};

export type Item =
  | { type: "user" | "agent" | "notice"; text: string }
  | { type: "tool"; tool: Tool }
  | { type: "permission"; permission: PermissionRequest };

// Snapshot is the full state the server sends when the event channel opens.
// transcripts holds only the conversations this tab opened; events keep
// them current.
export type Snapshot = {
  type: "snapshot";
  launch: string; // random ID of the fh launch; changes on restart
  dev: boolean; // fh --dev: the token survives restarts, so a tab reloads
  sessions: SessionSummary[];
  inbox: PermissionRequest[]; // open requests, oldest first
  projects: Project[];
  transcripts: Record<string, Item[]>;
};

export type Connection =
  | { status: "connecting" }
  | { status: "open"; snapshot: Snapshot }
  | { status: "lost" } // fh is not reachable; retrying
  | { status: "restarted" }; // a new fh launch no longer accepts this tab's cookie

// Update changes the open snapshot, e.g. to store a fetched conversation.
export type Update = (f: (snap: Snapshot) => Snapshot) => void;

const retryMs = 2000;

// useEventChannel keeps one WebSocket open to fh and reconnects when it drops.
// A restart shows up one of two ways: fh rejects this tab's cookie, or (when
// the restarted fh's own new tab already replaced the cookie for 127.0.0.1)
// the snapshot carries a different launch ID. Either way the tab stops and
// says so instead of quietly continuing on a launch it was never opened for.
// Notify is a "notify" event: a Session started Waiting and nobody is
// looking at it (#8).
export type Notify = { session: string; title: string; body: string };

// Reconnect closes the event channel so it reconnects, e.g. after the tab
// got notification permission and fh should know.
export type Reconnect = () => void;

export function useEventChannel(onNotify?: (n: Notify) => void): [Connection, Update, Reconnect] {
  const [conn, setConn] = useState<Connection>({ status: "connecting" });
  const socket = useRef<WebSocket | undefined>(undefined);
  const reconnect = useCallback(() => socket.current?.close(), []);
  const notify = useRef(onNotify);
  useEffect(() => {
    notify.current = onNotify;
  }, [onNotify]);
  const update: Update = useCallback(
    (f) => setConn((c) => (c.status === "open" ? { ...c, snapshot: f(c.snapshot) } : c)),
    [],
  );

  useEffect(() => {
    let ws: WebSocket | undefined;
    let timer: number | undefined;
    let stopped = false;
    let launch: string | undefined;

    const restarted = () => {
      stopped = true;
      ws?.close();
      setConn({ status: "restarted" });
    };

    const connect = () => {
      const proto = location.protocol === "https:" ? "wss" : "ws";
      // notify=1 tells fh this tab can show notifications; without such a
      // tab fh notifies natively instead (#8).
      const canNotify = "Notification" in window && Notification.permission === "granted";
      ws = new WebSocket(`${proto}://${location.host}/ws${canNotify ? "?notify=1" : ""}`);
      socket.current = ws;
      ws.onmessage = (e) => {
        const ev = JSON.parse(e.data);
        if (ev.type === "notify") {
          notify.current?.(ev as Notify);
          return;
        }
        if (ev.type !== "snapshot") {
          update((snap) => apply(snap, ev));
          return;
        }
        const snap = ev as Snapshot;
        launch ??= snap.launch;
        if (snap.launch !== launch) {
          // In dev mode the restarted fh accepts this tab's cookie, so it
          // can pick up the new build itself.
          if (snap.dev) location.reload();
          else restarted();
          return;
        }
        // A reconnect drops the cached conversations: events may have been
        // missed, and an open conversation fetches itself again.
        setConn({ status: "open", snapshot: { ...snap, transcripts: {} } });
      };
      ws.onclose = () => {
        if (!stopped) void checkAfterClose();
      };
    };

    const checkAfterClose = async () => {
      try {
        const resp = await fetch("/api/state", { credentials: "same-origin", cache: "no-store" });
        // The tab may have stopped (unmounted, or found fh restarted) while
        // the fetch was in flight; reconnecting now would leave a socket open
        // that nothing closes.
        if (stopped) return;
        if (resp.status === 401) {
          restarted();
          return;
        }
        if (resp.ok) {
          connect();
          return;
        }
      } catch {
        // fh is not running (yet); keep trying.
      }
      if (stopped) return;
      setConn({ status: "lost" });
      timer = window.setTimeout(checkAfterClose, retryMs);
    };

    connect();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      ws?.close();
    };
  }, [update]);

  return [conn, update, reconnect];
}

// apply folds one event into the snapshot.
function apply(snap: Snapshot, ev: Record<string, unknown>): Snapshot {
  switch (ev.type) {
    case "projects":
      return { ...snap, projects: ev.projects as Project[] };
    case "inbox":
      return { ...snap, inbox: ev.inbox as PermissionRequest[] };
    case "reset": {
      // A reopened Session: the Agent replays its conversation from the start.
      const id = ev.session as string;
      if (!snap.transcripts[id]) return snap;
      return { ...snap, transcripts: { ...snap.transcripts, [id]: [] } };
    }
    case "closed": {
      const id = ev.session as string;
      const transcripts = { ...snap.transcripts };
      delete transcripts[id];
      return { ...snap, sessions: snap.sessions.filter((s) => s.id !== id), transcripts };
    }
    case "session": {
      const s = ev.session as SessionSummary;
      const known = snap.sessions.some((x) => x.id === s.id);
      const sessions = known
        ? snap.sessions.map((x) => (x.id === s.id ? s : x))
        : [...snap.sessions, s];
      return { ...snap, sessions };
    }
    case "item":
    case "chunk": {
      const id = ev.session as string;
      const items = snap.transcripts[id];
      if (!items) return snap; // not open in this tab; fetched in full when opened
      const index = ev.index as number;
      const next = [...items];
      if (ev.type === "item") {
        next[index] = ev.item as Item;
      } else {
        const it = next[index];
        if (it && (it.type === "agent" || it.type === "user"))
          next[index] = { ...it, text: it.text + (ev.text as string) };
      }
      return { ...snap, transcripts: { ...snap.transcripts, [id]: next } };
    }
  }
  return snap;
}

const listGroup: Record<SessionState, number> = {
  waiting: 0,
  working: 1,
  starting: 1,
  idle: 2,
  exited: 3,
};

// byListOrder sorts Sessions like the server's list (internal/session):
// Waiting (longest waiting first), Working and Starting, Idle, Exited (most
// recent change first within each).
export function byListOrder(a: SessionSummary, b: SessionSummary): number {
  const g = listGroup[a.state] - listGroup[b.state];
  if (g !== 0) return g;
  const t = Date.parse(a.since) - Date.parse(b.since);
  return a.state === "waiting" ? t : -t;
}

// projectName is the Project's name for path, or the folder name.
export function projectName(projects: Project[], path: string): string {
  return (
    projects.find((p) => p.path === path)?.name ?? path.split("/").filter(Boolean).pop() ?? path
  );
}

// tabID names this tab when it tells fh which Session it's looking at.
export const tabID = Math.random().toString(36).slice(2);

// shortcutLabel is how the switcher's shortcut is written on this computer.
export const shortcutLabel = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘K" : "Ctrl+K";
