import { useEffect, useRef, useState, type FormEvent } from "react";
import { command, get } from "./api";
import type {
  Item,
  Project,
  SessionState,
  SessionSummary,
  Snapshot,
  Tool,
  Update,
  Worktree,
} from "./events";

// SessionList is the rail's Sessions section.
export function SessionList(props: {
  sessions: SessionSummary[];
  current?: string;
  onOpen: (id: string) => void;
  onNew: () => void;
}) {
  return (
    <section aria-label="Sessions">
      <h3>Sessions</h3>
      <button className="addp" type="button" onClick={props.onNew}>
        + New Session
      </button>
      {props.sessions.length === 0 && <p className="empty">No Sessions yet.</p>}
      <ul className="slist">
        {props.sessions.map((s) => (
          <li key={s.id}>
            <button
              type="button"
              className="srow"
              aria-current={s.id === props.current}
              onClick={() => props.onOpen(s.id)}
            >
              <span className={`dot ${s.state}`} aria-hidden="true" />
              <span className="t">{s.title}</span>
              <StateBadge state={s.state} />
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

const stateNames: Record<SessionState, string> = {
  starting: "Starting",
  working: "Working",
  idle: "Idle",
  exited: "Exited",
};

function StateBadge({ state }: { state: SessionState }) {
  return <span className={`state ${state}`}>{stateNames[state]}</span>;
}

type AgentInfo = { id: string; name: string; missing?: string };

// NewSession is the New Session dialog: Project, a new or free Worktree,
// Agent and an optional starting prompt. Choices that can't be used are
// shown disabled with the reason, so the rules are visible, not surprising.
export function NewSession(props: {
  projects: Project[];
  sessions: SessionSummary[];
  onStarted: (id: string) => void;
  onCancel: () => void;
}) {
  const usable = props.projects.filter((p) => !p.missing);
  const [project, setProject] = useState(usable[0]?.path ?? "");
  const [worktree, setWorktree] = useState(""); // "" means a new Worktree
  const [branch, setBranch] = useState("");
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [agent, setAgent] = useState("");
  const [prompt, setPrompt] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    // get never rejects: a failure resolves to undefined, shown as no Agents.
    void get<AgentInfo[]>("/api/agents").then((list) => {
      setAgents(list ?? []);
      setAgent((list ?? []).find((a) => !a.missing)?.id ?? "");
    });
  }, []);

  const p = usable.find((x) => x.path === project);
  const reason = (w: Worktree): string => {
    if (w.agent) return "Agent worktree: fh never starts Sessions in it";
    if (w.missing) return "folder not found";
    if (props.sessions.some((s) => s.worktree === w.path)) return "already has a Session";
    return "";
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const req: Record<string, string> = { project, agent, prompt };
    if (worktree) req.worktree = worktree;
    else req.branch = branch.trim();
    const r = await command("/api/sessions", req);
    if (!r.ok) {
      setError(r.error);
      return;
    }
    props.onStarted((r.body as SessionSummary).id);
  };

  if (usable.length === 0) {
    return (
      <div className="panel">
        <h2>New Session</h2>
        <p>Add a Project first, with + Add Project in the left rail.</p>
      </div>
    );
  }
  return (
    <form className="panel newsession" onSubmit={(e) => void submit(e)} aria-label="New Session">
      <h2>New Session</h2>
      <label>
        Project
        <select
          value={project}
          onChange={(e) => {
            setProject(e.target.value);
            setWorktree("");
          }}
        >
          {usable.map((x) => (
            <option key={x.path} value={x.path}>
              {x.name}
            </option>
          ))}
        </select>
      </label>
      <fieldset>
        <legend>Worktree</legend>
        <label className="choice">
          <input type="radio" checked={worktree === ""} onChange={() => setWorktree("")} />
          New Worktree on branch
          <input
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            onFocus={() => setWorktree("")}
            placeholder="feature/login"
            aria-label="New branch name"
          />
        </label>
        {p?.worktrees.map((w) => {
          const why = reason(w);
          return (
            <label key={w.path} className="choice" data-disabled={why !== "" || undefined}>
              <input
                type="radio"
                disabled={why !== ""}
                checked={worktree === w.path}
                onChange={() => setWorktree(w.path)}
              />
              <code>{w.branch || "(detached)"}</code>
              {w.main && <span className="tag">main tree</span>}
              {why && <span className="why">{why}</span>}
            </label>
          );
        })}
      </fieldset>
      <fieldset>
        <legend>Agent</legend>
        {agents.map((a) => (
          <label key={a.id} className="choice" data-disabled={a.missing ? true : undefined}>
            <input
              type="radio"
              disabled={!!a.missing}
              checked={agent === a.id}
              onChange={() => setAgent(a.id)}
            />
            {a.name}
            {a.missing && <span className="why">{a.missing}</span>}
          </label>
        ))}
      </fieldset>
      <label>
        Starting prompt (optional)
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={3} />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="row">
        <button type="submit" disabled={!agent || (!worktree && !branch.trim())}>
          Start Session
        </button>
        <button type="button" onClick={props.onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// Conversation shows one Session: its messages, tool calls and notices,
// and the composer. Sending is only possible while the Session is Idle;
// while the Agent works, Cancel takes its place.
export function Conversation(props: {
  session: SessionSummary;
  snapshot: Snapshot;
  update: Update;
}) {
  const { session, update } = props;
  const items = props.snapshot.transcripts[session.id];
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const end = useRef<HTMLDivElement>(null);

  // Fetch the whole conversation once per open; events keep it current.
  useEffect(() => {
    if (items) return;
    void get<{ transcript: Item[] }>(`/api/sessions/${session.id}`).then((d) => {
      if (d)
        update((snap) => ({
          ...snap,
          transcripts: { ...snap.transcripts, [session.id]: d.transcript },
        }));
    });
  }, [session.id, items, update]);

  // A statement body on purpose: scrollIntoView returns a Promise in current
  // Chromium, and an effect must return nothing or a cleanup function.
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [items?.length]);

  const send = async (e: FormEvent) => {
    e.preventDefault();
    const r = await command(`/api/sessions/${session.id}/prompt`, { text });
    if (r.ok) setText("");
    setError(r.ok ? "" : r.error);
  };

  return (
    <section className="conversation" aria-label={`Session ${session.title}`}>
      <header className="mhead">
        <h2>{session.title}</h2>
        <StateBadge state={session.state} />
        <span className="crumb" title={session.worktree}>
          {session.agent} · {session.worktree}
        </span>
      </header>
      <div className="items">
        {(items ?? []).map((it, i) => (
          <ItemView key={i} item={it} />
        ))}
        {session.state === "exited" && (
          <div className="notice bad" role="status">
            Exited: {session.reason}
            {session.stderr && session.stderr.length > 0 && <pre>{session.stderr.join("\n")}</pre>}
          </div>
        )}
        <div ref={end} />
      </div>
      <form className="composer" onSubmit={(e) => void send(e)}>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={session.state === "idle" ? "Message the Agent" : stateNames[session.state]}
          aria-label="Message"
          rows={2}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && session.state === "idle" && text.trim()) {
              e.preventDefault();
              void send(e);
            }
          }}
        />
        {session.state === "working" ? (
          <button type="button" onClick={() => void command(`/api/sessions/${session.id}/cancel`)}>
            Cancel
          </button>
        ) : (
          <button type="submit" disabled={session.state !== "idle" || !text.trim()}>
            Send
          </button>
        )}
      </form>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

function ItemView({ item }: { item: Item }) {
  switch (item.type) {
    case "user":
      return <div className="msg user">{item.text}</div>;
    case "agent":
      return <div className="msg agent">{item.text}</div>;
    case "notice":
      return (
        <div className="notice" role="note">
          {item.text}
        </div>
      );
    case "tool":
      return <ToolCard tool={item.tool} />;
  }
}

// ToolCard is collapsed by default, so following the Agent isn't noisy.
function ToolCard({ tool }: { tool: Tool }) {
  const output = [tool.content, tool.rawOutput].filter(Boolean).join("\n");
  return (
    <details className="toolcard">
      <summary>
        <span className="kind">{tool.kind || "tool"}</span>
        <span className="title">{tool.title}</span>
        <span className={`status ${tool.status}`}>{tool.status.replace("_", " ")}</span>
      </summary>
      {output ? <pre>{output}</pre> : <p className="empty">No output yet.</p>}
    </details>
  );
}
