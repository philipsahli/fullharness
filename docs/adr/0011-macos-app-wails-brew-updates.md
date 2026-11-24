---
status: accepted, supersedes ADR-0001
---

# fh ships as a Wails .app in a DMG; Homebrew installs updates; channels come from the version

fullharness ships as a universal macOS `.app` inside a DMG. The `.app` is built with Wails v3, so the Go server and a webview window run in one process, and a menu-bar icon keeps fh running after the window closes. The `.app` contains no updater. It checks GitHub Releases, shows an Update notice, and the update button runs `brew upgrade --cask`. The Release channel (stable or beta) comes from the app's own version, and each channel has its own cask: `fh` and `fh@beta`. This replaces [ADR 0001](./0001-go-binary-with-browser-ui.md)'s browser-only UI, which deferred a native `.app` until now. Decided in issue #152.

The DMG stays unsigned for now, because only a few trusted users install it. They get past Gatekeeper the same way as with today's binary: through the Homebrew cask, which removes the quarantine flag, or with right-click → Open.

## Considered Options

- **Launcher `.app` that opens the browser**: least work, but no window of its own and no menu-bar icon.
- **Tauri with fh as a sidecar**: has an official updater, but adds a Rust toolchain and a second process.
- **Swift shell with Sparkle**: has mature updates and channels, but adds Xcode and Swift to the build and runs fh as a child process.
- **In-app updater (the app downloads and replaces itself)**: works without Homebrew, but means more code to write and test, and it would fight Homebrew over who owns the installed version.
- **A version picker for any release**: rejected. Moving to an older release can lose state (see Consequences), and the beta channel covers what users need.

## Consequences

- Wails needs CGO on macOS (the build has `CGO_ENABLED=0` today), so release builds stay on macOS runners. `make dmg` packs the universal `.app` with `hdiutil`.
- The Homebrew cask installs the `.app` and does not set `auto_updates`, so `brew upgrade` stays in charge of the installed version.
- Users without Homebrew get the notice and a link to the DMG, but no one-click update.
- An update restarts fh and stops running Sessions. The update button lists them, including Autopilot, and offers "Update when idle".
- Moving from beta to stable can mean running an older version. `state.json` gets a `schema_version`, and an older fh refuses to start on newer state instead of silently dropping fields it does not know.
- The `fh` binary ships inside the `.app`, and a menu item symlinks it into `~/.local/bin`. `fh` opens or focuses the window; `fh --browser` keeps the old browser-tab behaviour. The bare `fh_darwin_*.tar.gz` binaries are still published for users who only want the CLI.
- Still open: signing and notarization before any public audience.
