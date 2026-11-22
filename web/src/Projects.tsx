import { useState, type FormEvent } from "react";
import { command, get } from "./api";
import type { Project, Worktree } from "./events";

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
            <WorktreeItem key={w.path} wt={w} />
          ))}
        </ul>
      )}
      {error && <p className="error">{error}</p>}
    </li>
  );
}

function WorktreeItem({ wt }: { wt: Worktree }) {
  const counts = [
    wt.uncommitted > 0 && `${wt.uncommitted} uncommitted`,
    wt.base && (wt.ahead > 0 || wt.behind > 0) && `↑${wt.ahead} ↓${wt.behind} vs ${wt.base}`,
  ].filter(Boolean);
  return (
    <li className="wt" title={`${wt.path}\n${fetchNote(wt)}`}>
      <span className="dot" aria-hidden="true" />
      <span>{wt.branch || "(detached)"}</span>
      <span className="tag">{wt.main ? "main tree" : "Agent worktree"}</span>
      {wt.missing ? (
        <span className="counts warn">Folder not found</span>
      ) : (
        counts.length > 0 && <span className="counts">{counts.join(" · ")}</span>
      )}
    </li>
  );
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
