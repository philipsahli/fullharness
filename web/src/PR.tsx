import { LinkOrText } from "./LinkOrText";

// PR is a Pull request with its PR state (internal/tracker/pr.go, PRRecord);
// the server has already turned GitHub's check rollup into one result.
export type PR = {
  number: number;
  url: string;
  state: "draft" | "open" | "merged" | "closed";
  checks: "passed" | "failed" | "running" | "";
};

// The glossary words for PR states and the marks for checks results.
const prStateName: Record<PR["state"], string> = {
  draft: "Draft",
  open: "Open",
  merged: "Merged",
  closed: "Closed",
};
const checksMark: Record<PR["checks"], string> = {
  passed: "✓",
  failed: "✗",
  running: "…",
  "": "",
};

function ChecksMark({ checks }: { checks: PR["checks"] }) {
  return (
    <span className={`checks ${checks}`} title={`checks ${checks}`}>
      {checksMark[checks]}
    </span>
  );
}

// PRPart is one Pull request on a Ticket row as `#21 ↗ Open ✓`: the number
// links to GitHub.
export function PRPart({ pr }: { pr: PR }) {
  return (
    <span className={`pr ${pr.state}`}>
      <LinkOrText url={pr.url} title={`Pull request #${pr.number} on GitHub`}>
        #{pr.number} ↗
      </LinkOrText>{" "}
      {prStateName[pr.state]}
      {pr.checks && (
        <>
          {" "}
          <ChecksMark checks={pr.checks} />
        </>
      )}
    </span>
  );
}

// SessionPRPart is a Worktree's Pull request in the Session header as
// `PR #21 ↗ Open · checks ✓`; a PR without checks leaves the checks out.
export function SessionPRPart({ pr }: { pr: PR }) {
  return (
    <span className={`pr ${pr.state}`}>
      <LinkOrText url={pr.url} title={`Pull request #${pr.number} on GitHub`}>
        PR #{pr.number} ↗
      </LinkOrText>{" "}
      {prStateName[pr.state]}
      {pr.checks && (
        <>
          {" · checks "}
          <ChecksMark checks={pr.checks} />
        </>
      )}
    </span>
  );
}
