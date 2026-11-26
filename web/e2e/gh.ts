import { chmodSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

// GhIssue is one issue as fh's GraphQL query reads it (internal/tracker).
export type GhIssue = {
  number: number;
  title: string;
  state?: "OPEN" | "CLOSED";
  body?: string;
  labels?: string[];
  parent?: number;
  subIssues?: number[];
  blockedBy?: Record<number, "OPEN" | "CLOSED">;
  // blockedByRepo puts a blocker in another repository ("owner/repo");
  // the others are in the fake's repo.
  blockedByRepo?: Record<number, string>;
};

// GhPR is one PR, in any state, as `gh pr list --json number,headRefName,body` gives it.
export type GhPR = { number: number; headRefName: string; body?: string };

function issuesPage(issues: GhIssue[], repo: string) {
  const nodes = issues.map((i) => ({
    number: i.number,
    title: i.title,
    state: i.state ?? "OPEN",
    body: i.body ?? "",
    labels: { nodes: (i.labels ?? []).map((name) => ({ name })) },
    parent: i.parent ? { number: i.parent } : null,
    subIssues: { nodes: (i.subIssues ?? []).map((number) => ({ number })) },
    blockedBy: {
      nodes: Object.entries(i.blockedBy ?? {}).map(([n, state]) => ({
        number: Number(n),
        state,
        repository: { nameWithOwner: i.blockedByRepo?.[Number(n)] ?? repo },
      })),
    },
  }));
  return [
    {
      data: {
        repository: { issues: { nodes, pageInfo: { hasNextPage: false, endCursor: null } } },
      },
    },
  ];
}

// fakeGhPath writes a gh that is logged in, answers fh's issues query with
// issues and `pr list` with prs, and returns a PATH with it first. Pass it
// to Fh.start({ env: { PATH } }), as the Go tests' fakeGh does. repo is the
// "owner/repo" the issues are in; match it to the test repo's origin.
// An `issue edit` succeeds and makes the issues read afterEdit from then
// on, as GitHub would show them; with editFails it fails with that
// message on stderr instead.
export function fakeGhPath(
  issues: GhIssue[],
  {
    repo = "someone/shop",
    prs = [] as GhPR[],
    afterEdit = undefined as GhIssue[] | undefined,
    editFails = undefined as string | undefined,
  } = {},
): string {
  const dir = mkdtempSync(path.join(tmpdir(), "fh-e2e-gh-"));
  writeFileSync(path.join(dir, "issues.json"), JSON.stringify(issuesPage(issues, repo)));
  if (afterEdit)
    writeFileSync(path.join(dir, "after.json"), JSON.stringify(issuesPage(afterEdit, repo)));
  if (editFails !== undefined) writeFileSync(path.join(dir, "edit-fails"), editFails + "\n");
  writeFileSync(
    path.join(dir, "prs.json"),
    JSON.stringify(prs.map((p) => ({ ...p, body: p.body ?? "" }))),
  );
  const gh = path.join(dir, "gh");
  writeFileSync(
    gh,
    [
      "#!/bin/sh",
      'case "$*" in',
      '  *"auth status"*) exit 0 ;;',
      `  *graphql*) cat "${dir}/issues.json" ;;`,
      `  *"pr list"*) cat "${dir}/prs.json" ;;`,
      `  "issue edit "*) if [ -f "${dir}/edit-fails" ]; then cat "${dir}/edit-fails" >&2; exit 1; fi`,
      `    if [ -f "${dir}/after.json" ]; then mv "${dir}/after.json" "${dir}/issues.json"; fi ;;`,
      '  *) echo "fake gh: unexpected $*" >&2; exit 2 ;;',
      "esac",
      "",
    ].join("\n"),
  );
  chmodSync(gh, 0o755);
  return `${dir}${path.delimiter}${process.env.PATH ?? ""}`;
}
