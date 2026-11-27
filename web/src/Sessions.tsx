import { useEffect, useRef, useState, type FormEvent } from "react";
import { command, get } from "./api";
import { Highlighted, languageOfPath, unfence } from "./Code";
import { ChangeCounts, Diffs } from "./Diff";
import { LinkOrText } from "./LinkOrText";
import { AgentMarkdown } from "./Markdown";
import { PRPart } from "./PR";
import {
  byListOrder,
  loadedConversation,
  loadingConversation,
  projectName,
  stopLoadingConversation,
  tabID,
} from "./events";
import { AgentChip, stateNames } from "./Chips";
import { StopPoints, awaitingApproval } from "./StopPoints";
import type {
  AutopilotStatus,
  Command,
  Item,
  Link,
  PermissionRequest,
  Project,
  SessionState,
  SessionSummary,
  Snapshot,
  Tool,
  Update,
  Usage,
  Worktree,
} from "./events";

// SessionList is the rail's Sessions section.
export function SessionList(props: {
  sessions: SessionSummary[];
  projects: Project[];
  current?: string;
  onOpen: (id: string) => void;
  onNew: () => void;
}) {
  const sorted = [...props.sessions].sort(byListOrder);
  return (
    <section aria-label="Sessions">
      <h3>Sessions</h3>
      <button className="addp" type="button" onClick={props.onNew}>
        + New Session
      </button>
      {props.sessions.length === 0 && <p className="empty">No Sessions yet.</p>}
      <ul className="slist">
        {sorted.map((s) => (
          <li key={s.id}>
            <button
              type="button"
              className={`srow ${s.state}`}
              aria-current={s.id === props.current}
              onClick={() => props.onOpen(s.id)}
            >
              <span className={`dot ${s.state}`} aria-hidden="true" />
              <span className="t">{s.title}</span>
              <StateBadge state={s.state} />
              <span className="s">
                {projectName(props.projects, s.project)} ·{" "}
                <AgentChip id={s.agent} name={s.agentName} />
                {s.autoApprove && " · auto-approve"}
                {s.state === "exited" && s.reason && ` · ${s.reason}`}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

// linkText names a Session's Feature and Ticket the way its tracker does:
// "Ticket #42 of Spec #1" on GitHub, whose Specs are numbered, or "Ticket
// 01 of Spec login" for a local .scratch folder.
function linkText(l: Link): string {
  const hash = /^\d+$/.test(l.feature) ? "#" : "";
  const spec = `Spec ${hash}${l.feature}`;
  return l.ticket ? `Ticket ${hash}${l.ticket} of ${spec}` : spec;
}

function StateBadge({ state }: { state: SessionState }) {
  return <span className={`state ${state}`}>{stateNames[state]}</span>;
}

type AgentInfo = {
  id: string;
  name: string;
  missing?: string;
  // With ?skill= (see StartFrom):
  skill?: string; // the slash command, e.g. "/mattpocock-skills:implement"
  noSkill?: string; // why the Agent can't run the skill
  commandsUnknown?: boolean; // none of its Sessions reported its commands yet
};

// StartFrom is a board button's Session: which skill it runs, on what, and
// where. The New Session dialog opens prefilled with it (#13).
export type StartFrom = {
  title: string; // the dialog's heading, e.g. "Start Session on #42"
  project: string;
  branch?: string; // a new Worktree on this branch…
  worktree?: string; // …or this existing one (the main tree)
  skill: string; // the skill's name, resolved per Agent, e.g. "implement"
  ref?: string; // what the skill works on, e.g. "#42"
  note?: string; // added after ref, e.g. which branch to integrate on
  link?: Link;
  // idea: Grill new idea. The dialog asks for the Idea's name instead of a
  // Worktree; fh gives it a new Worktree on idea/<name> (#14).
  idea?: boolean;
};

// Agents whose unknown commands were already pointed out in this tab: the
// warning shows once per Agent (spec #1, skill resolution).
const warnedUnknown = new Set<string>();

function startPrompt(from: StartFrom, a: AgentInfo | undefined): string {
  const cmd = a?.skill ?? `/${from.skill}`;
  const text = [cmd, from.ref, from.note].filter(Boolean).join(" ");
  return from.ref ? text : `${text} `;
}

// NewSession is the New Session dialog: Project, a new or free Worktree,
// Agent and an optional starting prompt. Choices that can't be used are
// shown disabled with the reason, so the rules are visible, not surprising.
// With from, it is a board button's Start dialog: prefilled, and Agents
// that don't have the skill are disabled.
export function NewSession(props: {
  projects: Project[];
  sessions: SessionSummary[];
  from?: StartFrom;
  onStarted: (id: string) => void;
  onCancel: () => void;
}) {
  const { from } = props;
  const usable = props.projects.filter((p) => !p.missing);
  const [project, setProject] = useState(from?.project ?? usable[0]?.path ?? "");
  // The prefilled Worktree (the main tree) may already have a live Session;
  // then the dialog offers a new Worktree instead, and says why.
  const [prefillBusy] = useState(
    () =>
      !!from?.worktree &&
      props.sessions.some((s) => s.worktree === from.worktree && s.state !== "exited"),
  );
  const [worktree, setWorktree] = useState(prefillBusy ? "" : (from?.worktree ?? "")); // "" means a new Worktree
  const [branch, setBranch] = useState(from?.branch ?? "");
  const [ideaName, setIdeaName] = useState("");
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [agent, setAgent] = useState("");
  const [prompt, setPrompt] = useState("");
  const [autoApprove, setAutoApprove] = useState(false);
  const [error, setError] = useState("");
  // Agents already warned about before this dialog opened.
  const [alreadyWarned] = useState(() => new Set(warnedUnknown));

  useEffect(() => {
    const url = from ? `/api/agents?skill=${encodeURIComponent(from.skill)}` : "/api/agents";
    // get never rejects: a failure resolves to undefined, shown as no Agents.
    void get<AgentInfo[]>(url).then((list) => {
      const all = list ?? [];
      const first = all.find((a) => !a.missing && !a.noSkill);
      setAgents(all);
      setAgent(first?.id ?? "");
      if (from) {
        setPrompt(startPrompt(from, first));
        // Only Agents that show the warning: a missing CLI hides it.
        all.filter((a) => a.commandsUnknown && !a.missing).forEach((a) => warnedUnknown.add(a.id));
      }
    });
  }, [from]);

  const chooseAgent = (id: string) => {
    // A board prompt follows the Agent, since each names the skill its own
    // way, unless you've changed it.
    if (from) {
      const before = startPrompt(
        from,
        agents.find((a) => a.id === agent),
      );
      if (prompt === before)
        setPrompt(
          startPrompt(
            from,
            agents.find((a) => a.id === id),
          ),
        );
    }
    setAgent(id);
  };

  const p = usable.find((x) => x.path === project);
  const reason = (w: Worktree): string => {
    if (w.agent) return "Agent worktree: fh never starts Sessions in it";
    if (w.missing) return "folder not found";
    if (props.sessions.some((s) => s.worktree === w.path)) return "already has a Session";
    return "";
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const req: Record<string, unknown> = { project, agent, prompt, autoApprove, link: from?.link };
    if (from?.idea) req.idea = ideaName.trim();
    else if (worktree) req.worktree = worktree;
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
    <form
      className="panel newsession"
      onSubmit={(e) => void submit(e)}
      aria-label={from?.title ?? "New Session"}
    >
      <h2>{from?.title ?? "New Session"}</h2>
      <label>
        Project
        <select
          value={project}
          // A board button's Session belongs to its Project's Feature.
          disabled={!!from}
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
      {from?.idea ? (
        <label>
          Idea name
          <input
            value={ideaName}
            onChange={(e) => setIdeaName(e.target.value)}
            placeholder="Dark mode"
          />
          <span className="why">It gets its own Worktree on a new idea/… branch.</span>
        </label>
      ) : (
        <fieldset className="radios stack">
          <legend>Worktree</legend>
          {prefillBusy && (
            <p className="why">
              The main tree already has a Session, so this one needs a new Worktree: name its
              branch.
            </p>
          )}
          <label>
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
              <label key={w.path} className={why ? "dis" : undefined}>
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
      )}
      <fieldset className="radios">
        <legend>Agent</legend>
        {agents.map((a) => {
          const why = a.missing ?? a.noSkill ?? "";
          return (
            <label key={a.id} className={why ? "dis" : undefined}>
              <input
                type="radio"
                disabled={why !== ""}
                checked={agent === a.id}
                onChange={() => chooseAgent(a.id)}
              />
              {a.name}
              {why && <span className="why">{why}</span>}
              {!why && a.commandsUnknown && !alreadyWarned.has(a.id) && (
                <span className="why">
                  fh doesn't know {a.name}'s slash commands yet, so it can't tell whether{" "}
                  <code>/{from?.skill}</code> is installed. Start any Session of {a.name} once so fh
                  learns them
                </span>
              )}
            </label>
          );
        })}
      </fieldset>
      <label className="choice">
        <input
          type="checkbox"
          checked={autoApprove}
          onChange={(e) => setAutoApprove(e.target.checked)}
        />
        Auto-approve: the Agent decides by itself what is safe to do, and asks less
      </label>
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
        <button
          className="btn primary"
          type="submit"
          disabled={!agent || (from?.idea ? !ideaName.trim() : !worktree && !branch.trim())}
        >
          Start Session
        </button>
        <button className="btn" type="button" onClick={props.onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// Conversation shows one Session: its messages, tool calls and notices,
// and the composer. While the Session is Starting, Working or Waiting on a
// permission request, a message becomes the Queued message (#93), sent once
// the turn ends. Cancel sits next to Queue once the Agent is ready.
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

  // Fetch the whole conversation once per open, and again after a
  // reconnect (a new epoch); events keep it current. A fetch that fails is
  // tried again; one that a newer fetch replaced is ignored.
  const epoch = props.snapshot.epoch;
  useEffect(() => {
    if (items) return;
    const id = session.id;
    let live = true;
    let retry: number | undefined;
    const load = () => {
      void get<{ transcript: Item[]; seq: number }>(`/api/sessions/${id}`).then((d) => {
        if (!live) return;
        if (d) update((snap) => loadedConversation(snap, id, d.transcript, d.seq));
        else retry = window.setTimeout(load, 1000);
      });
    };
    update((snap) => loadingConversation(snap, id));
    load();
    return () => {
      live = false;
      window.clearTimeout(retry);
      update((snap) => stopLoadingConversation(snap, id));
    };
  }, [session.id, items, epoch, update]);

  // You're looking at this Session while its conversation is open in a
  // visible tab. Tell fh now, every 15 s (fh forgets a tab after 30 s, so a
  // closed tab stops counting), and when that stops; a turn that ends while
  // you look then never waits for you.
  useEffect(() => {
    const say = (viewing: boolean) =>
      // A lost update only means a turn waits until the next one.
      void command(`/api/sessions/${session.id}/viewing`, { tab: tabID, viewing });
    const update = () => say(document.visibilityState === "visible");
    update();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") say(true);
    }, 15_000);
    document.addEventListener("visibilitychange", update);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
      say(false);
    };
  }, [session.id]);

  // A statement body on purpose: scrollIntoView returns a Promise in current
  // Chromium, and an effect must return nothing or a cleanup function.
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [items?.length]);

  const reopen = async () => {
    const r = await command(`/api/sessions/${session.id}/reopen`);
    setError(r.ok ? "" : r.error);
  };
  const close = async () => {
    const live = session.state !== "exited";
    // A live Session's Agent is stopped by Close (#1, story 57): ask first.
    if (live && !window.confirm(`Stop ${session.agentName} and close "${session.title}"?`)) return;
    const r = await command(`/api/sessions/${session.id}/close`, { confirm: live });
    setError(r.ok ? "" : r.error);
  };

  const send = async (e: FormEvent) => {
    e.preventDefault();
    const r = await command(`/api/sessions/${session.id}/prompt`, { text });
    if (r.ok) setText("");
    setError(r.ok ? "" : r.error);
  };

  // Edit takes the Queued message (and any unsent text) back from fh into
  // the composer, after what is already typed.
  const takeBack = async () => {
    const r = await command(`/api/sessions/${session.id}/queued`, { action: "edit" });
    if (!r.ok) return setError(r.error);
    const back = (r.body as { text?: string } | undefined)?.text;
    if (back) setText((t) => joinMessages(t, back));
  };
  const actOnQueued = async (action: "remove" | "send-now") => {
    const r = await command(`/api/sessions/${session.id}/queued`, { action });
    setError(r.ok ? "" : r.error);
  };
  // A Queued message the turn couldn't send (cancel, error, exit) comes back
  // into the composer, so it's never lost; then fh forgets it. backFromFh
  // is what this tab took, until fh has forgotten it: the same text can
  // come back again later.
  const [backFromFh, setBackFromFh] = useState("");
  if (session.unsent && session.unsent !== backFromFh) {
    setBackFromFh(session.unsent);
    const back = session.unsent;
    setText((t) => joinMessages(t, back));
  } else if (!session.unsent && backFromFh) {
    setBackFromFh("");
  }
  useEffect(() => {
    // A lost clear only means the text comes back again on the next load.
    if (session.unsent)
      void command(`/api/sessions/${session.id}/queued`, { action: "clear-unsent" });
  }, [session.id, session.unsent]);

  // A message sent now becomes the Queued message.
  const queues =
    session.state === "starting" ||
    session.state === "working" ||
    (session.state === "waiting" && session.waiting !== "turn");
  const canSend = session.state !== "exited" && text.trim() !== "";

  // The "/" list: shown while the composer holds only "/" and a partial
  // command name.
  const [pick, setPick] = useState(0);
  const [slashOff, setSlashOff] = useState(false); // Escape hides it until the text changes
  const matches = slashMatches(session.commands ?? [], text);
  const showSlash = !slashOff && matches.length > 0;
  const choose = (c: Command) => {
    setText(`/${c.name} `);
    setPick(0);
  };
  const change = (t: string) => {
    setText(t);
    setPick(0);
    setSlashOff(false);
  };
  const setting = async (path: string, body: Record<string, string>) => {
    const r = await command(`/api/sessions/${session.id}/${path}`, body);
    setError(r.ok ? "" : r.error);
  };

  return (
    <section className="conversation" aria-label={`Session ${session.title}`}>
      <header className="mhead">
        <h2>{session.title}</h2>
        <AgentChip id={session.agent} name={session.agentName} />
        <StateBadge state={session.state} />
        {session.autoApprove && <span className="tag">auto-approve</span>}
        <button type="button" className="btn small close" onClick={() => void close()}>
          Close
        </button>
        <span className="crumb" title={session.worktree}>
          {session.worktree}
          {session.idea && ` · Idea "${session.idea}"`}
          {session.link &&
            (session.idea
              ? ` → ${linkText(session.link)}`
              : ` · started from ${linkText(session.link)}`)}
        </span>
        <BranchLine
          worktree={props.snapshot.projects
            .flatMap((p) => p.worktrees)
            .find((w) => w.path === session.worktree)}
        />
        <SessionSettings session={session} onSet={(p, b) => void setting(p, b)} />
      </header>
      <div className="items">
        {(items ?? []).map((it, i) => (
          <ItemView key={i} item={it} session={session.id} />
        ))}
        {session.state === "exited" && (
          <div className="notice bad" role="status">
            Exited: {session.reason}
            {session.login && (
              <p>
                Log in from a terminal, then start a new Session: <code>{session.login}</code>
              </p>
            )}
            {session.stderr && session.stderr.length > 0 && <pre>{session.stderr.join("\n")}</pre>}
            {session.folderGone && <p>Can't reopen: the Worktree's folder is gone.</p>}
            {session.canReopen && (
              <p>
                <button className="btn primary" type="button" onClick={() => void reopen()}>
                  Reopen
                </button>{" "}
                restarts the Agent and loads this conversation again.
              </p>
            )}
          </div>
        )}
        {session.queued && (
          <div className="queued" role="status" aria-label="Queued message">
            <span className="why">Queued, sends when the turn ends</span>
            <p>{session.queued}</p>
            <div className="row">
              <button className="btn small" type="button" onClick={() => void takeBack()}>
                Edit
              </button>
              <button
                className="btn small"
                type="button"
                onClick={() => void actOnQueued("remove")}
              >
                Remove
              </button>
              {session.state !== "starting" && (
                <button
                  className="btn small"
                  type="button"
                  onClick={() => void actOnQueued("send-now")}
                >
                  Send now
                </button>
              )}
            </div>
          </div>
        )}
        <div ref={end} />
      </div>
      <form className="composer" onSubmit={(e) => void send(e)}>
        {showSlash && (
          <ul className="slash" role="listbox" aria-label="Slash commands">
            {matches.map((c, i) => (
              <li
                key={c.name}
                role="option"
                aria-selected={i === pick}
                // mousedown, not click: the textarea keeps focus.
                onMouseDown={(e) => {
                  e.preventDefault();
                  choose(c);
                }}
              >
                <code>/{c.name}</code>
                {c.hint && <span className="hint">{c.hint}</span>}
                {c.description && <span className="desc">{c.description}</span>}
              </li>
            ))}
          </ul>
        )}
        <textarea
          value={text}
          onChange={(e) => change(e.target.value)}
          placeholder={
            queues
              ? `${stateNames[session.state]}: a message waits until the turn ends`
              : session.state === "exited"
                ? stateNames[session.state]
                : `Message the Agent${session.commands?.length ? ", or / for its commands" : ""}`
          }
          aria-label="Message"
          rows={2}
          onKeyDown={(e) => {
            if (showSlash) {
              const n = matches.length;
              if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                e.preventDefault();
                setPick((pick + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
                return;
              }
              if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
                e.preventDefault();
                choose(matches[Math.min(pick, n - 1)]);
                return;
              }
              if (e.key === "Escape") {
                setSlashOff(true);
                return;
              }
            }
            if (e.key === "Enter" && !e.shiftKey && canSend) {
              e.preventDefault();
              void send(e);
            }
          }}
        />
        <button className="btn primary" type="submit" disabled={!canSend}>
          {queues ? "Queue" : "Send"}
        </button>
        {queues && session.state !== "starting" && (
          <button
            className="btn"
            type="button"
            onClick={() => void command(`/api/sessions/${session.id}/cancel`)}
          >
            Cancel
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

// BranchLine is the Session header's branch and PR (#129): the branch
// links to GitHub once origin has it, then the branch's Pull request, or
// No PR yet. When fh can't know the PR, the PR part is left out.
function BranchLine({ worktree: w }: { worktree?: Worktree }) {
  if (!w?.branch) return null;
  return (
    <span className="crumb branchline">
      <LinkOrText url={w.url} title={w.url && `Branch ${w.branch} on GitHub`}>
        {w.branch}
        {w.url && " ↗"}
      </LinkOrText>
      {w.pr && (
        <>
          {" · "}
          <PRPart pr={w.pr} labelled />
        </>
      )}
      {w.noPR && " · No PR yet"}
    </span>
  );
}

// joinMessages puts more after what the composer holds, a blank line apart,
// as fh joins a Queued message.
function joinMessages(held: string, more: string): string {
  return held.trim() ? `${held}\n\n${more}` : more;
}

// slashMatches lists the commands for a composer holding "/" and a partial
// name. A plugin's "<plugin>:<skill>" also matches by its skill part, so
// "/tdd" finds "mattpocock-skills:tdd".
function slashMatches(commands: Command[], text: string): Command[] {
  const m = /^\/(\S*)$/.exec(text);
  if (!m) return [];
  const q = m[1].toLowerCase();
  return commands.filter((c) => {
    const name = c.name.toLowerCase();
    return name.startsWith(q) || name.split(":").pop()!.startsWith(q);
  });
}

// SessionSettings shows the Session's mode and config options (the model,
// say) where the Agent offers them, and its usage.
function SessionSettings(props: {
  session: SessionSummary;
  onSet: (path: string, body: Record<string, string>) => void;
}) {
  const { session: s, onSet } = props;
  // The Agent can only change them while it runs and its session exists.
  const off = s.state === "starting" || s.state === "exited";
  return (
    <div className="settings">
      {s.modes && s.modes.length > 0 && (
        <label>
          Mode
          <select
            value={s.mode ?? ""}
            disabled={off}
            onChange={(e) => onSet("mode", { mode: e.target.value })}
          >
            {s.modes.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {s.options?.map((o) => (
        <label key={o.id}>
          {o.name}
          <select
            value={o.value}
            disabled={off}
            onChange={(e) => onSet("option", { option: o.id, value: e.target.value })}
          >
            {o.values.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name}
              </option>
            ))}
          </select>
        </label>
      ))}
      {s.usage && <UsageView usage={s.usage} />}
    </div>
  );
}

function UsageView({ usage }: { usage: Usage }) {
  const pct = usage.size > 0 ? Math.round((usage.used / usage.size) * 100) : 0;
  return (
    <span className="usage" title={`${usage.used} of ${usage.size} tokens in context`}>
      context {tokens(usage.used)} / {tokens(usage.size)} ({pct}%)
      {usage.cost && ` · ${money(usage.cost.amount, usage.cost.currency)}`}
    </span>
  );
}

// tokens writes a token count short: 950, 53k, 1.2M.
function tokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}k`;
  return String(n);
}

// money writes an ISO 4217 amount as currency; ACP also allows other units,
// such as "credits", which are written after the number.
function money(amount: number, unit: string): string {
  if (/^[A-Z]{3}$/.test(unit)) {
    try {
      return new Intl.NumberFormat(undefined, { style: "currency", currency: unit }).format(amount);
    } catch {
      // An unknown code: fall through to the plain form.
    }
  }
  return `${amount} ${unit}`;
}

function ItemView({ item, session }: { item: Item; session: string }) {
  switch (item.type) {
    case "user":
      return <div className="msg user">{item.text}</div>;
    case "agent":
      return (
        <div className="msg agent">
          <AgentMarkdown text={item.text} />
        </div>
      );
    case "notice":
      return (
        <div className="notice" role="note">
          {item.text}
        </div>
      );
    case "tool":
      return <ToolCard tool={item.tool} session={session} />;
    case "permission":
      return <PermissionCard request={item.permission} />;
  }
}

const settledLabels: Record<PermissionRequest["status"], string> = {
  pending: "",
  allowed: "Allowed",
  denied: "Denied",
  always: "Always allowed",
  cancelled: "Cancelled", // with the turn, or withdrawn by the Agent
  exited: "Agent exited before you answered",
};

// PermissionCard is one permission request, in its Session and in the
// Inbox. Answering in either place answers the same request.
export function PermissionCard({
  request,
  showSession,
}: {
  request: PermissionRequest;
  showSession?: () => void;
}) {
  const [error, setError] = useState("");
  const answer = async (choice: "allow" | "deny" | "always") => {
    const r = await command(`/api/inbox/${request.id}/answer`, { choice });
    setError(r.ok ? "" : r.error);
  };
  const pending = request.status === "pending";
  return (
    <div
      className={`permcard ${request.status}`}
      role="group"
      aria-label={`Permission: ${request.title}`}
    >
      <div className="permhead">
        <span className="kind">{request.kind || "tool"}</span>
        <strong>{request.title}</strong>
        {showSession && (
          <button type="button" className="link" onClick={showSession}>
            {request.sessionTitle} · {basename(request.project)}
          </button>
        )}
      </div>
      {request.command && <pre>{request.command}</pre>}
      {request.paths && request.paths.length > 0 && (
        <p className="paths">{request.paths.join(", ")}</p>
      )}
      {request.content && <pre>{request.content}</pre>}
      {request.diffs && request.diffs.length > 0 && (
        <Diffs files={request.diffs} session={request.session} item={request.id} />
      )}
      {pending ? (
        <div className="row">
          <button type="button" className="btn primary" onClick={() => void answer("allow")}>
            Allow
          </button>
          <button className="btn" type="button" onClick={() => void answer("deny")}>
            Deny
          </button>
          {request.always && (
            <button type="button" className="btn" onClick={() => void answer("always")}>
              Always allow
            </button>
          )}
        </div>
      ) : (
        <p className="settled">{settledLabels[request.status]}</p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

// Inbox lists every open permission request across Sessions, oldest first.
export function Inbox(props: {
  inbox: PermissionRequest[];
  autopilot: AutopilotStatus[];
  onOpen: (session: string) => void;
}) {
  // Autopilot's Stop points come first: each holds a whole Feature.
  const stops = awaitingApproval(props.autopilot).length;
  return (
    <section className="panel inbox" aria-label="Inbox">
      <h2>Inbox</h2>
      <StopPoints statuses={props.autopilot} />
      {props.inbox.length === 0 && stops === 0 && (
        <p className="empty">Nothing is waiting for your answer.</p>
      )}
      {props.inbox.map((r) => (
        <PermissionCard key={r.id} request={r} showSession={() => props.onOpen(r.session)} />
      ))}
    </section>
  );
}

function basename(path: string): string {
  return path.split("/").filter(Boolean).pop() ?? path;
}

// ToolCard is collapsed by default, so following the Agent isn't noisy. A
// failed call opens itself, to show why; an edit's summary shows +N −M.
function ToolCard({ tool, session }: { tool: Tool; session: string }) {
  // A read's text is the file, highlighted by its extension; a failed read's
  // text is an error message.
  const lang =
    tool.kind === "read" && tool.status !== "failed" ? languageOfPath(tool.paths?.[0]) : undefined;
  const readText = lang && tool.content ? unfence(tool.content) : undefined;
  const output = [readText === undefined && tool.content, tool.rawOutput]
    .filter(Boolean)
    .join("\n");
  const diffs = tool.diffs ?? [];
  return (
    <details className="toolcard" open={tool.status === "failed" || undefined}>
      <summary>
        <span className="kind">{tool.kind || "tool"}</span>
        <span className="title">{tool.title}</span>
        {diffs.length > 0 && <ChangeCounts files={diffs} />}
        <span className={`status ${tool.status}`}>{tool.status.replace("_", " ")}</span>
      </summary>
      {lang && readText !== undefined && (
        <pre>
          <code className={`language-${lang}`}>
            <Highlighted language={lang} text={readText} />
          </code>
        </pre>
      )}
      {output && <pre>{output}</pre>}
      {diffs.length > 0 && <Diffs files={diffs} session={session} item={tool.id} />}
      {readText === undefined && !output && diffs.length === 0 && (
        <p className="empty">No output yet.</p>
      )}
    </details>
  );
}
