import { useEffect, useState, type FormEvent } from "react";
import { command, get } from "./api";
import type { Project, Worktree } from "./events";
import { LinkOrText } from "./LinkOrText";

// Suggestion matches internal/project.Suggestion.
type Suggestion = { path: string; name: string };

// Projects is the rail's Projects section: every Project with its
// Worktrees, plus adding and removing Projects. Changes come back as
// "projects" events, so nothing here keeps its own copy of the list.
export function Projects({ projects }: { projects: Project[] }) {
  return (
    <section aria-label="Projects">
      <h3>
        <span>Projects</span>
        <button
          type="button"
          className="btn small"
          onClick={() => void command("/api/projects/refresh")}
          title="Re-read every Worktree from git"
        >
          Refresh
        </button>
      </h3>
      {projects.length === 0 && <p className="empty">No Projects yet.</p>}
      <ul className="ptree">
        {projects.map((p) => (
          <ProjectItem key={p.path} project={p} />
        ))}
      </ul>
      <AddProject />
    </section>
  );
}

function ProjectItem({ project }: { project: Project }) {
  const [error, setError] = useState("");
  const remove = async () => {
    const r = await command("/api/projects/remove", { path: project.path });
    setError(r.ok ? "" : r.error);
  };
  const setAutopilot = async (on: boolean, agent = project.agent) => {
    const r = await command("/api/projects/autopilot", { path: project.path, on, agent });
    setError(r.ok ? "" : r.error);
  };
  return (
    <li className="proj">
      <div>
        <LinkOrText url={project.url} title={project.path}>
          {project.name}
        </LinkOrText>
        <button
          type="button"
          className="btn small"
          onClick={() => void remove()}
          aria-label={`Remove ${project.name}`}
          title="Remove from fh; the folder on disk stays"
        >
          Remove
        </button>
      </div>
      {!project.missing && (
        <div className="autopilot">
          <label title="fh starts the next step of this Project's Features in Roadmap Now by itself, and stops where it needs you">
            <input
              type="checkbox"
              checked={project.autopilot}
              onChange={(e) => void setAutopilot(e.target.checked)}
            />
            Autopilot
          </label>
          {project.autopilot && (
            <AutopilotAgent value={project.agent} onChange={(a) => void setAutopilot(true, a)} />
          )}
        </div>
      )}
      {project.missing ? (
        <p className="warn">
          Folder not found: <code>{project.path}</code>
        </p>
      ) : (
        <ul className="wts">
          {project.worktrees.map((w) => (
            <WorktreeItem key={w.path} project={project.path} wt={w} />
          ))}
        </ul>
      )}
      {error && <p className="error">{error}</p>}
    </li>
  );
}

// AutopilotAgent picks the Agent Autopilot starts; none picked is Claude
// Code.
function AutopilotAgent({
  value,
  onChange,
}: {
  value?: string;
  onChange: (agent: string) => void;
}) {
  const [agents, setAgents] = useState<{ id: string; name: string }[]>([]);
  useEffect(() => {
    void get<{ id: string; name: string }[]>("/api/agents").then((a) => a && setAgents(a));
  }, []);
  return (
    <select
      aria-label="Autopilot Agent"
      value={value ?? "claude"}
      onChange={(e) => onChange(e.target.value)}
    >
      {agents.map((a) => (
        <option key={a.id} value={a.id}>
          {a.name}
        </option>
      ))}
    </select>
  );
}

function WorktreeItem({ project, wt }: { project: string; wt: Worktree }) {
  const [error, setError] = useState("");
  const counts = [
    wt.uncommitted > 0 && `${wt.uncommitted} uncommitted`,
    wt.base && (wt.ahead > 0 || wt.behind > 0) && `↑${wt.ahead} ↓${wt.behind} vs ${wt.base}`,
  ].filter(Boolean);
  return (
    <li className="wt" title={`${wt.path}\n${fetchNote(wt)}`}>
      <span className="dot" aria-hidden="true" />
      <LinkOrText url={wt.url}>{wt.branch || "(detached)"}</LinkOrText>
      {/* fh's own Worktrees need no tag; the others say why they differ. */}
      {wt.main && <span className="tag">main tree</span>}
      {wt.agent && <span className="tag">Agent worktree</span>}
      {!wt.main && !wt.agent && <RemoveWorktree project={project} wt={wt} onError={setError} />}
      {wt.missing ? (
        <span className="counts warn">Folder not found</span>
      ) : (
        counts.length > 0 && <span className="counts">{counts.join(" · ")}</span>
      )}
      {error && <span className="counts error">{error}</span>}
    </li>
  );
}

// RemoveWorktree is offered only on fh's own Worktrees, and works only once
// nothing can be lost (#1, story 20); wt.removal says why not, from the
// server. fh asks first and never removes one on its own (story 21).
function RemoveWorktree({
  project,
  wt,
  onError,
}: {
  project: string;
  wt: Worktree;
  onError: (e: string) => void;
}) {
  const name = wt.branch || wt.path;
  const state = mergeNote(wt);
  const remove = async () => {
    // The button stays focusable when refused, so the reason is reachable
    // by keyboard and screen reader, not only as a hover tooltip.
    if (wt.removal) {
      onError(`Can't remove: ${wt.removal}`);
      return;
    }
    // Asking without confirm returns what removal would delete that git
    // doesn't track, for the question below.
    const ask = await command("/api/worktrees/remove", { project, path: wt.path });
    const body = (ask.ok ? undefined : ask.body) as
      { confirm?: boolean; ignored?: string[] } | undefined;
    if (!ask.ok && !body?.confirm) {
      onError(ask.error);
      return;
    }
    const ignored = body?.ignored?.length
      ? `\n\nThese ignored files will be deleted: ${body.ignored.join(", ")}`
      : "";
    if (
      !window.confirm(
        `Remove the Worktree ${wt.path}? ${state} Its branch ${name} stays.${ignored}`,
      )
    )
      return;
    const r = await command("/api/worktrees/remove", { project, path: wt.path, confirm: true });
    onError(r.ok ? "" : r.error);
  };
  return (
    <button
      type="button"
      className="btn small"
      aria-disabled={wt.removal !== ""}
      onClick={() => void remove()}
      aria-label={`Remove Worktree ${name}`}
      title={
        wt.removal
          ? `Can't remove: ${wt.removal}`
          : `${state} Run git worktree remove; the branch stays`
      }
    >
      Remove
    </button>
  );
}

// mergeNote says, for the confirmation and tooltip, why nothing on the
// Worktree's branch is lost.
function mergeNote(wt: Worktree): string {
  switch (wt.merge) {
    case "no-own-commits":
      return "It has no commits of its own.";
    case "in-base":
      return `Everything on it is already in ${wt.base}.`;
    default:
      return `It is merged into ${wt.base}.`;
  }
}

function fetchNote(wt: Worktree): string {
  if (!wt.fetchedAt) return "never fetched";
  return `as of last fetch, ${new Date(wt.fetchedAt).toLocaleString()}`;
}

function AddProject() {
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState("");
  const [error, setError] = useState("");
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);

  const show = async () => {
    setOpen(true);
    setSuggestions((await get<Suggestion[]>("/api/projects/suggestions")) ?? []);
  };

  const add = async (p: string) => {
    const r = await command("/api/projects", { path: p });
    if (!r.ok) {
      setError(r.error);
      return;
    }
    setPath("");
    setError("");
    setOpen(false);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (path.trim()) void add(path.trim());
  };

  if (!open) {
    return (
      <button className="addp" type="button" onClick={() => void show()}>
        + Add Project
      </button>
    );
  }
  return (
    <form className="addform" onSubmit={submit} aria-label="Add Project">
      <label>
        Path to a git repository
        <input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="/Users/you/work/shop"
          autoFocus
        />
      </label>
      <div className="row">
        <button className="btn small primary" type="submit">
          Add
        </button>
        <button className="btn small" type="button" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {suggestions.length > 0 && (
        <>
          <p className="hint">From your scan folder:</p>
          <ul className="suggest">
            {suggestions.map((s) => (
              <li key={s.path}>
                <button
                  className="btn small"
                  type="button"
                  onClick={() => void add(s.path)}
                  title={s.path}
                >
                  {s.name}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </form>
  );
}
