// Commands are HTTP POSTs (spec #1). Their results arrive as events on the
// event channel, so callers only need to know whether the command was taken.
// The browser adds the cookie and the Origin header fh checks.

// A refusal keeps its body too: some carry more than the error, such as the
// ignored files a Worktree removal asks you to confirm.
export type CommandResult =
  { ok: true; body: unknown } | { ok: false; error: string; body?: unknown };

export async function command(path: string, body?: unknown): Promise<CommandResult> {
  try {
    const resp = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await resp.text();
    const data = text ? JSON.parse(text) : undefined;
    if (!resp.ok)
      return { ok: false, error: data?.error ?? `fh answered ${resp.status}`, body: data };
    return { ok: true, body: data };
  } catch {
    return { ok: false, error: "fh is not reachable" };
  }
}

export async function get<T>(path: string): Promise<T | undefined> {
  try {
    const resp = await fetch(path, { credentials: "same-origin", cache: "no-store" });
    return resp.ok ? ((await resp.json()) as T) : undefined;
  } catch {
    return undefined;
  }
}
