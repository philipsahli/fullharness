import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";

// The binary `make e2e` builds, with the UI embedded.
const binary = path.resolve(import.meta.dirname, "../../bin/fh");

// Fh runs the real fh binary on a free port with its own temp state folder,
// the way a user runs it, minus opening the browser.
export class Fh {
  private proc?: ChildProcess;
  launchURL = "";

  private constructor(
    readonly port: number,
    readonly dir: string,
  ) {}

  static async start(): Promise<Fh> {
    const fh = new Fh(await freePort(), mkdtempSync(path.join(tmpdir(), "fh-e2e-")));
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
    const proc = spawn(binary, ["--no-open", "--port", String(this.port), "--dir", this.dir], {
      stdio: ["ignore", "pipe", "inherit"],
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
