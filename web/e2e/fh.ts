import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
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
  launchURL = "";

  private constructor(
    readonly port: number,
    readonly dir: string,
    readonly dev: boolean,
    readonly env: Record<string, string>,
  ) {}

  // start launches fh; dev adds --dev (the token survives restarts),
  // agentScript makes the "claude" Agent the fake agent running that script,
  // and env is added to fh's environment, e.g. a PATH with a fake gh first.
  static async start({
    dev = false,
    agentScript = undefined as object | undefined,
    env = {} as Record<string, string>,
  } = {}): Promise<Fh> {
    const fh = new Fh(await freePort(), mkdtempSync(path.join(tmpdir(), "fh-e2e-")), dev, env);
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
      env: { ...process.env, ...this.env },
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
