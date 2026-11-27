import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { command, get } from "./api";
import { AutopilotBadge } from "./Autopilot";
import type { AutopilotStatus } from "./events";

// Screenshot review in the Inbox (#226, ADR-0014), as the mockup draws it:
// a reviewed PR that changes a screen waits here until the user has looked
// at the Agent's screenshots. fh serves them from the Ticket's Worktree;
// nothing is uploaded. Looks good moves the PR on to merge; Request
// changes (#227) sends a note back to the Ticket's Session.

// awaitingScreens is every Feature whose Ticket's PR waits at Screenshot
// review.
export function awaitingScreens(statuses: AutopilotStatus[]): AutopilotStatus[] {
  return statuses.filter((s) => s.kind === "waiting" && s.reason === "screenshot-review");
}

// Shots matches GET /api/autopilot/screenshots.
type Shots = {
  screenshots: string[];
  // After Request changes: the screenshots saved since then.
  new?: string[];
  pr?: { number: number; url: string };
  files?: { path: string; ui: boolean }[];
};

const query = (s: AutopilotStatus, extra: Record<string, string> = {}) =>
  new URLSearchParams({ project: s.project, feature: s.feature, ticket: s.ticket ?? "", ...extra });

export function ScreenshotReview(props: {
  status: AutopilotStatus;
  title: string;
  project: string;
  onLooksGood: (pr?: number) => void;
  onRequestedChanges: (pr?: number) => void;
}) {
  const s = props.status;
  const [shots, setShots] = useState<Shots>();
  const [open, setOpen] = useState<number>(); // the screenshot shown full size
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // note is the open Request changes note box's text; undefined while it
  // is closed.
  const [note, setNote] = useState<string>();
  const key = query(s).toString();
  useEffect(() => {
    void get<Shots>(`/api/autopilot/screenshots?${key}`).then((d) => {
      if (d) setShots(d);
    });
  }, [key]);

  const src = (name: string) => `/api/autopilot/screenshot?${query(s, { name }).toString()}`;
  const names = shots?.screenshots ?? [];
  const looksGood = async () => {
    setBusy(true);
    const r = await command("/api/autopilot/looks-good", {
      project: s.project,
      feature: s.feature,
      ticket: s.ticket,
    });
    setBusy(false);
    setError(r.ok ? "" : r.error);
    // The item goes away by itself once fh's Autopilot status moves on.
    if (r.ok) props.onLooksGood(shots?.pr?.number);
  };
  const requestChanges = async () => {
    setBusy(true);
    const r = await command("/api/autopilot/request-changes", {
      project: s.project,
      feature: s.feature,
      ticket: s.ticket,
      note: note ?? "",
    });
    setBusy(false);
    setError(r.ok ? "" : r.error);
    if (r.ok) {
      setNote(undefined);
      props.onRequestedChanges(shots?.pr?.number);
    }
  };
  const pr = shots?.pr;
  const fresh = new Set(shots?.new ?? []);
  return (
    <div
      className="ibox stop"
      data-ap="waiting"
      role="group"
      aria-label={`Screenshot review: ${props.title}`}
    >
      <div>
        <div className="meta">
          <AutopilotBadge status={s} />
          <span>
            {props.project} · {props.title} · #{s.ticket}
          </span>
        </div>
        <div className="kind">
          <strong>Screenshot review</strong>
          {pr && (
            <a className="mono" href={pr.url} target="_blank" rel="noreferrer">
              PR #{pr.number}
            </a>
          )}
          <span className="crumb">code review done</span>
        </div>
        <div className="why">
          This PR changes files under {props.project}'s UI paths, so it stops here before the merge
          Stop point.{" "}
          {names.length > 0
            ? `Look at the ${names.length === 1 ? "screen" : `${names.length} screens`} the Agent saved.`
            : "The Agent saved no screenshot in its Worktree under .fh/screenshots/."}
          {shots?.new &&
            (shots.new.length > 0
              ? ` ${shots.new.length === 1 ? "One is" : `${shots.new.length} are`} new since your Request changes.`
              : " The Agent saved no new screenshot since your Request changes.")}
        </div>
        {shots?.files && shots.files.length > 0 && (
          <div className="files">
            {shots.files.map((f) => (
              <span
                key={f.path}
                className={f.ui ? "mono ui" : "mono"}
                title={f.ui ? "Under a UI path" : "Not a UI path"}
              >
                {f.path}
              </span>
            ))}
          </div>
        )}
        {names.length > 0 && (
          <div className="shots">
            {names.map((n, i) => (
              <button
                key={n}
                type="button"
                className="shot"
                aria-label={`Show ${n} full size`}
                onClick={() => setOpen(i)}
              >
                <img src={src(n)} alt="" />
                <span>
                  {n}
                  {fresh.has(n) && <b className="new">new</b>}
                </span>
              </button>
            ))}
          </div>
        )}
        {note !== undefined && (
          <div className="note-box">
            <label htmlFor={`changes-${s.feature}-${s.ticket ?? ""}`}>
              {pr
                ? `What's wrong? fh posts this on PR #${pr.number}.`
                : "What's wrong? fh posts this on the PR."}
            </label>
            <textarea
              id={`changes-${s.feature}-${s.ticket ?? ""}`}
              value={note}
              placeholder="What to change on the screen"
              onChange={(e) => setNote(e.target.value)}
              autoFocus
            />
            <span className="goes">
              Then fh sends it to #{s.ticket}'s Autopilot Session as its next turn, or starts a new
              one with <b className="mono">/implement</b> if that Session is gone. When the turn
              ends, a new Screenshot review item comes back here.
            </span>
            <div className="row">
              <button type="button" className="btn" onClick={() => setNote(undefined)}>
                Cancel
              </button>
              <button
                type="button"
                className="btn primary"
                disabled={busy || note.trim() === ""}
                onClick={() => void requestChanges()}
              >
                Post and send
              </button>
            </div>
          </div>
        )}
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
      </div>
      <div className="acts">
        <button
          type="button"
          className="btn primary"
          disabled={busy}
          onClick={() => void looksGood()}
        >
          Looks good
        </button>
        <button
          type="button"
          className="btn"
          disabled={busy}
          aria-expanded={note !== undefined}
          onClick={() => setNote((n) => (n === undefined ? "" : undefined))}
        >
          Request changes
        </button>
      </div>
      {open !== undefined && names[open] && (
        <Lightbox
          names={names}
          index={open}
          src={src}
          onMove={setOpen}
          onClose={() => setOpen(undefined)}
        />
      )}
    </div>
  );
}

// Lightbox shows one screenshot full size; ← and → (buttons or keys) go
// through the rest, Escape closes it.
function Lightbox(props: {
  names: string[];
  index: number;
  src: (name: string) => string;
  onMove: (i: number) => void;
  onClose: () => void;
}) {
  const { names, index, onMove, onClose } = props;
  const last = names.length - 1;
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowLeft" && index > 0) onMove(index - 1);
      if (e.key === "ArrowRight" && index < last) onMove(index + 1);
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [index, last, onMove, onClose]);
  const name = names[index];
  // A portal to the page: inside the Inbox item, the item's own layout and
  // motion would hold the dialog.
  return createPortal(
    <div className="overlay" onClick={onClose}>
      <div
        className="lightbox"
        role="dialog"
        aria-label={`Screenshot ${name}`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="lbhead">
          <strong>{name}</strong>
          <span className="mono">
            {index + 1} of {names.length}
          </span>
          <div className="acts">
            <button
              type="button"
              className="btn small"
              aria-label="Previous screenshot"
              disabled={index === 0}
              onClick={() => onMove(index - 1)}
            >
              ←
            </button>
            <button
              type="button"
              className="btn small"
              aria-label="Next screenshot"
              disabled={index === last}
              onClick={() => onMove(index + 1)}
            >
              →
            </button>
            <button type="button" className="btn small" onClick={onClose} autoFocus>
              Close
            </button>
          </div>
        </div>
        <div className="lbimg">
          <img src={props.src(name)} alt={`Screenshot ${name}`} />
        </div>
      </div>
    </div>,
    document.body,
  );
}
