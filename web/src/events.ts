import { useEffect, useState } from "react";

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

// Snapshot is the full state the server sends when the event channel opens.
// Later tickets give Sessions and Inbox items a shape.
export type Snapshot = {
  type: "snapshot";
  launch: string; // random ID of the fh launch; changes on restart
  sessions: unknown[];
  inbox: unknown[];
  projects: Project[];
};

export type Connection =
  | { status: "connecting" }
  | { status: "open"; snapshot: Snapshot }
  | { status: "lost" } // fh is not reachable; retrying
  | { status: "restarted" }; // a new fh launch no longer accepts this tab's cookie

const retryMs = 2000;

// useEventChannel keeps one WebSocket open to fh and reconnects when it drops.
// A restart shows up one of two ways: fh rejects this tab's cookie, or (when
// the restarted fh's own new tab already replaced the cookie for 127.0.0.1)
// the snapshot carries a different launch ID. Either way the tab stops and
// says so instead of quietly continuing on a launch it was never opened for.
export function useEventChannel(): Connection {
  const [conn, setConn] = useState<Connection>({ status: "connecting" });

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
      ws = new WebSocket(`${proto}://${location.host}/ws`);
      ws.onmessage = (e) => {
        const ev = JSON.parse(e.data);
        if (ev.type === "projects") {
          const projects = ev.projects as Project[];
          setConn((c) =>
            c.status === "open" ? { ...c, snapshot: { ...c.snapshot, projects } } : c,
          );
          return;
        }
        if (ev.type !== "snapshot") return;
        const snap = ev as Snapshot;
        launch ??= snap.launch;
        if (snap.launch !== launch) {
          restarted();
          return;
        }
        setConn({ status: "open", snapshot: snap });
      };
      ws.onclose = () => {
        if (!stopped) void checkAfterClose();
      };
    };

    const checkAfterClose = async () => {
      try {
        const resp = await fetch("/api/state", { credentials: "same-origin", cache: "no-store" });
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
      setConn({ status: "lost" });
      timer = window.setTimeout(checkAfterClose, retryMs);
    };

    connect();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      ws?.close();
    };
  }, []);

  return conn;
}
