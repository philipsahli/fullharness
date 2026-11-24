import { chmodSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

// GhIssue is one issue as fh's GraphQL query reads it (internal/tracker).
export type GhIssue = {
  number: number;
  title: string;
  state?: "OPEN" | "CLOSED";
  labels?: string[];
  parent?: number;
  subIssues?: number[];
  blockedBy?: Record<number, "OPEN" | "CLOSED">;
};

// fakeGhPath writes a gh that is logged in, answers fh's issues query with
// issues and `pr list` with no PRs, and returns a PATH with it first. Pass
// it to Fh.start({ env: { PATH } }), as the Go tests' fakeGh does.
export function fakeGhPath(issues: GhIssue[]): string {
  const dir = mkdtempSync(path.join(tmpdir(), "fh-e2e-gh-"));
  const nodes = issues.map((i) => ({
    number: i.number,
    title: i.title,
    state: i.state ?? "OPEN",
    body: "",
    labels: { nodes: (i.labels ?? []).map((name) => ({ name })) },
    parent: i.parent ? { number: i.parent } : null,
    subIssues: { nodes: (i.subIssues ?? []).map((number) => ({ number })) },
    blockedBy: {
      nodes: Object.entries(i.blockedBy ?? {}).map(([n, state]) => ({ number: Number(n), state })),
    },
  }));
  const page = [
    {
      data: {
        repository: { issues: { nodes, pageInfo: { hasNextPage: false, endCursor: null } } },
      },
    },
  ];
  writeFileSync(path.join(dir, "issues.json"), JSON.stringify(page));
  const gh = path.join(dir, "gh");
  writeFileSync(
    gh,
    [
      "#!/bin/sh",
      'case "$*" in',
      '  *"auth status"*) exit 0 ;;',
      `  *graphql*) cat "${dir}/issues.json" ;;`,
      '  *"pr list"*) echo "[]" ;;',
      '  *) echo "fake gh: unexpected $*" >&2; exit 2 ;;',
      "esac",
      "",
    ].join("\n"),
  );
  chmodSync(gh, 0o755);
  return `${dir}${path.delimiter}${process.env.PATH ?? ""}`;
}
