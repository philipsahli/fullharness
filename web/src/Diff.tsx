import { Fragment, useState } from "react";
import { get } from "./api";
import type { DiffFile } from "./events";

// changeCounts sums the added and removed lines of every file, also the ones
// past a cut, for a card's "+N −M".
function changeCounts(files: DiffFile[]): { added: number; removed: number } {
  return files.reduce(
    (sum, f) => ({ added: sum.added + f.added, removed: sum.removed + f.removed }),
    { added: 0, removed: 0 },
  );
}

export function ChangeCounts({ files }: { files: DiffFile[] }) {
  const { added, removed } = changeCounts(files);
  return (
    <span className="changes">
      <span className="added">+{added}</span> <span className="removed">−{removed}</span>
    </span>
  );
}

// Diffs shows the diffs of a tool card or permission request (item, in
// session) as line diffs. The server cuts a diff after diff.MaxChanged
// changed lines; Show full diff fetches them all.
export function Diffs({
  files,
  session,
  item,
}: {
  files: DiffFile[];
  session: string;
  item: string;
}) {
  // The full diffs belong to the diffs they were fetched for, compared by
  // content: every Inbox broadcast and card update sends new arrays of the
  // same diffs, which must not cut them again. Only a tool call update with
  // different diffs does.
  const key = JSON.stringify(files);
  const [full, setFull] = useState<{ of: string; files: DiffFile[] }>();
  const [error, setError] = useState("");
  const shown = full?.of === key ? full.files : files;
  const showFull = async () => {
    const got = await get<DiffFile[]>(
      `/api/sessions/${encodeURIComponent(session)}/diffs/${encodeURIComponent(item)}`,
    );
    if (got) {
      setFull({ of: key, files: got });
      setError("");
    } else {
      setError("Couldn't load the full diff.");
    }
  };
  return (
    <>
      {shown.map((f, i) => (
        <FileDiff key={i} file={f} onShowFull={() => void showFull()} />
      ))}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </>
  );
}

function FileDiff({ file, onShowFull }: { file: DiffFile; onShowFull: () => void }) {
  const shownChanged = file.hunks.reduce(
    (n, h) => n + h.lines.filter((l) => l.op !== " ").length,
    0,
  );
  return (
    <div className="diff" role="group" aria-label={`Diff of ${file.path}`}>
      <div className="diffhead">
        <span className="path">{file.path}</span>
        <ChangeCounts files={[file]} />
      </div>
      {file.hunks.map((h, i) => (
        <Fragment key={i}>
          <div className="hunk">
            @@ -{h.oldStart},{h.oldLines} +{h.newStart},{h.newLines} @@
          </div>
          {h.lines.map((l, j) => (
            <Fragment key={j}>
              <div className={l.op === "+" ? "add" : l.op === "-" ? "del" : "ctx"}>
                {l.op}
                {l.text}
              </div>
              {l.noNewline && <div className="nonl">\ No newline at end of file</div>}
            </Fragment>
          ))}
        </Fragment>
      ))}
      {file.truncated && (
        <div className="cut">
          Showing {shownChanged} of {file.added + file.removed} changed lines.{" "}
          <button type="button" className="link" onClick={onShowFull}>
            Show full diff
          </button>
        </div>
      )}
    </div>
  );
}
