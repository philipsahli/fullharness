---
status: accepted, amended by ADR-0011
---

# Single Go binary with a browser UI; native .app deferred

fullharness ships as one static Go binary per macOS architecture (arm64, x64), distributed through GitHub Releases and a Homebrew tap. Running it serves the UI on localhost and opens the browser; the UI is static files embedded in the binary, talking to the server over HTTP and WebSocket. We chose Go because the server's core job is supervising many Agent child processes, talking ACP to them over stdio and running a PTY for the terminal fallback, and Go (`os/exec`, `creack/pty`, `embed`) does both in a single binary with the least effort. We chose a browser UI because an unsigned, unnotarized `.dmg` would hit Gatekeeper on every new user's first launch.

## Considered Options

- **Tauri/Electron `.app`**: deferred, not rejected. The browser UI is kept wrapper-agnostic so a later `.app` (Wails, or a Tauri sidecar) is a packaging step, not a rewrite.
- **Rust**: the most direct fit for a future Tauri app, but the most effort for v1.
- **TypeScript compiled with Bun**: PTY support depends on the native `node-pty` module, which makes a single binary unreliable.

## Consequences

- macOS only for v1. Linux needs a build target; Windows needs ConPTY.
- Contributors need a Go toolchain.
