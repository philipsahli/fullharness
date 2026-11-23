import { useState, type FormEvent } from "react";
import { command, get } from "./api";
import type { Project, SessionSummary, Worktree } from "./events";

// Suggestion matches internal/project.Suggestion.
type Suggestion = { path: string; name: string };

// Projects is the rail's Projects section: every Project with its
// Worktrees, plus adding and removing Projects. Changes come back as
// "projects" events, so nothing here keeps its own copy of the list.
export function Projects({
  projects,
  sessions,
}: {
  projects: Project[];
  sessions: SessionSummary[];
}) {
  return (
    <section aria-label="Projects">
      <h3>
        <span>Projects</span>
        <button
          type="button"
          className="link"
          onClick={() => void command("/api/projects/refresh")}
          title="Re-read every Worktree from git"
        >
          Refresh
        </button>
      </h3>
      {projects.length === 0 && <p className="empty">No Projects yet.</p>}
      <ul className="ptree">
        {projects.map((p) => (
          <ProjectItem key={p.path} project={p} sessions={sessions} />
        ))}
      </ul>
      <AddProject />
    </section>
  );
}

function ProjectItem({ project, sessions }: { project: Project; sessions: SessionSummary[] }) {
  const [error, setError] = useState("");
  const remove = async () => {
    const r = await command("/api/projects/remove", { path: project.path });
    setError(r.ok ? "" : r.error);
  };
  return (
    <li className="proj">
      <div>
        <span title={project.path}>{project.name}</span>
        <button
          type="button"
          className="link"
          onClick={() => void remove()}
          aria-label={`Remove ${project.name}`}
          title="Remove from fh; the folder on disk stays"
        >
          Remove
        </button>
      </div>
      {project.missing ? (
        <p className="warn">
          Folder not found: <code>{project.path}</code>
        </p>
      ) : (
        <ul className="wts">
          {project.worktrees.map((w) => (
            <WorktreeItem key={w.path} project={project.path} wt={w} sessions={sessions} />
          ))}
        </ul>
      )}
      {error && <p className="error">{error}</p>}
    </li>
  );
}

function WorktreeItem({
  project,
  wt,
  sessions,
}: {
  project: string;
  wt: Worktree;
  sessions: SessionSummary[];
}) {
  const [error, setError] = useState("");
  const counts = [
    wt.uncommitted > 0 && `${wt.uncommitted} uncommitted`,
    wt.base && (wt.ahead > 0 || wt.behind > 0) && `↑${wt.ahead} ↓${wt.behind} vs ${wt.base}`,
  ].filter(Boolean);
  return (
    <li className="wt" title={`${wt.path}\n${fetchNote(wt)}`}>
      <span className="dot" aria-hidden="true" />
      <span>{wt.branch || "(detached)"}</span>
      {/* fh's own Worktrees need no tag; the others say why they differ. */}
      {wt.main && <span className="tag">main tree</span>}
      {wt.agent && <span className="tag">Agent worktree</span>}
      {!wt.main && !wt.agent && (
        <RemoveWorktree project={project} wt={wt} sessions={sessions} onError={setError} />
      )}
      {wt.missing ? (
        <span className="counts warn">Folder not found</span>
      ) : (
        counts.length > 0 && <span className="counts">{counts.join(" · ")}</span>
      )}
      {error && <span className="counts error">{error}</span>}
    </li>
  );
}

// RemoveWorktree is offered only on fh's own Worktrees, and only once
// nothing can be lost (#1, story 20). fh asks first and never removes one on
// its own (story 21).
function RemoveWorktree({
  project,
  wt,
  sessions,
  onError,
}: {
  project: string;
  wt: Worktree;
  sessions: SessionSummary[];
  onError: (e: string) => void;
}) {
  const reason = removeRefusal(wt, sessions);
  const name = wt.branch || wt.path;
  const remove = async () => {
    // The button stays focusable when refused, so the reason is reachable
    // by keyboard and screen reader, not only as a hover tooltip.
    if (reason) {
      onError(`Can't remove: ${reason}`);
      return;
    }
    if (!window.confirm(`Remove the Worktree ${wt.path}? Its branch ${name} stays.`)) return;
    const r = await command("/api/worktrees/remove", { project, path: wt.path, confirm: true });
    onError(r.ok ? "" : r.error);
  };
  return (
    <button
      type="button"
      className="link"
      aria-disabled={reason !== ""}
      onClick={() => void remove()}
      aria-label={`Remove Worktree ${name}`}
      title={reason ? `Can't remove: ${reason}` : "Run git worktree remove; the branch stays"}
    >
      Remove
    </button>
  );
}

// removeRefusal mirrors internal/app/worktrees_api.go: why fh won't remove a
// Worktree it created, or "" when it may. The server checks again.
function removeRefusal(wt: Worktree, sessions: SessionSummary[]): string {
  if (wt.missing) return "the Worktree's folder is gone";
  if (wt.uncommitted > 0)
    return `it has ${wt.uncommitted} uncommitted ${wt.uncommitted === 1 ? "file" : "files"}`;
  if (!wt.base) return "no main branch to check the merge against";
  if (!wt.merged) return `its branch isn't merged into ${wt.base}`;
  if (sessions.some((s) => s.worktree === wt.path))
    return "a Session is linked to it; close the Session first";
  return "";
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
        <button type="submit">Add</button>
        <button type="button" onClick={() => setOpen(false)}>
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
                <button type="button" onClick={() => void add(s.path)} title={s.path}>
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
