import { spawn, type ChildProcess } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";

// The binaries `make e2e` builds: fh with the UI embedded, and the fake ACP
// agent (internal/acp/fakeagent) that stands in for Claude Code.
const binary = path.resolve(import.meta.dirname, "../../bin/fh");
const fakeAgent = path.resolve(import.meta.dirname, "../../bin/fakeagent");

// Fh runs the real fh binary on a free port with its own temp state folder,
// the way a user runs it, minus opening the browser.
export class Fh {
  private proc?: ChildProcess;
  private path = process.env.PATH ?? "";
  launchURL = "";

  private constructor(
    readonly port: number,
    readonly dir: string,
    readonly dev: boolean,
  ) {}

  // start launches fh; dev adds --dev (the token survives restarts),
  // agentScript makes the "claude" Agent the fake agent running that script,
  // and ghPRs puts a logged-in fake gh on fh's PATH whose `gh pr list`
  // answers with those PRs (as `gh pr list --json` gives them); any other gh
  // command fails.
  static async start({
    dev = false,
    agentScript = undefined as object | undefined,
    ghPRs = undefined as object[] | undefined,
  } = {}): Promise<Fh> {
    const fh = new Fh(await freePort(), mkdtempSync(path.join(tmpdir(), "fh-e2e-")), dev);
    if (ghPRs) {
      const bin = path.join(fh.dir, "bin");
      mkdirSync(bin);
      writeFileSync(path.join(fh.dir, "prs.json"), JSON.stringify(ghPRs));
      writeFileSync(
        path.join(bin, "gh"),
        `#!/bin/sh\ncase "$*" in\n  *"auth status"*) exit 0 ;;\n  *"pr list"*) cat ${JSON.stringify(path.join(fh.dir, "prs.json"))} ;;\n  *) exit 1 ;;\nesac\n`,
      );
      chmodSync(path.join(bin, "gh"), 0o755);
      fh.path = `${bin}${path.delimiter}${process.env.PATH ?? ""}`;
    }
    if (agentScript) {
      writeFileSync(
        path.join(fh.dir, "config.toml"),
        `[agents.claude]\ncommand = [${JSON.stringify(fakeAgent)}]\nenv = { FH_FAKE_SCRIPT = ${JSON.stringify(JSON.stringify(agentScript))} }\n`,
      );
    }
    await fh.launch();
    return fh;
  }

  get origin(): string {
    return `http://127.0.0.1:${this.port}`;
  }

  // restart stops fh and starts it again on the same port and folder: a new
  // launch with a new token, as when the user quits and reruns `fh`.
  async restart(): Promise<void> {
    await this.stop();
    await this.launch();
  }

  async stop(): Promise<void> {
    const proc = this.proc;
    this.proc = undefined;
    if (!proc || proc.exitCode !== null) return;
    const exited = new Promise((resolve) => proc.once("exit", resolve));
    proc.kill("SIGTERM");
    await exited;
  }

  async dispose(): Promise<void> {
    await this.stop();
    rmSync(this.dir, { recursive: true, force: true });
  }

  // launch starts fh with --no-open, which prints the launch URL instead of
  // opening it.
  private launch(): Promise<void> {
    const args = ["--no-open", "--port", String(this.port), "--dir", this.dir];
    if (this.dev) args.push("--dev");
    const proc = spawn(binary, args, {
      stdio: ["ignore", "pipe", "inherit"],
      env: { ...process.env, PATH: this.path },
    });
    this.proc = proc;
    return new Promise((resolve, reject) => {
      let out = "";
      const timer = setTimeout(() => reject(new Error("fh printed no URL within 10 s")), 10_000);
      proc.stdout!.on("data", (chunk: Buffer) => {
        out += chunk.toString();
        const line = out.split("\n")[0];
        if (out.includes("\n")) {
          clearTimeout(timer);
          this.launchURL = line.trim();
          resolve();
        }
      });
      proc.once("exit", (code) => {
        clearTimeout(timer);
        reject(new Error(`fh exited with code ${code} before printing a URL`));
      });
    });
  }
}

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const { port } = srv.address() as net.AddressInfo;
      srv.close(() => resolve(port));
    });
  });
}

// runFh runs the fh binary with args to completion and returns its exit code
// and stderr, for checking flag errors.
export function runFh(args: string[]): Promise<{ code: number | null; stderr: string }> {
  return new Promise((resolve) => {
    const proc = spawn(binary, args, { stdio: ["ignore", "ignore", "pipe"] });
    let stderr = "";
    proc.stderr!.on("data", (chunk: Buffer) => (stderr += chunk.toString()));
    proc.once("exit", (code) => resolve({ code, stderr }));
  });
}
