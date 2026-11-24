---
status: accepted, amends ADR-0001
---

# fh ships as a Wails .app in a DMG; Homebrew installs updates; channels come from the version

fullharness ships as a universal macOS `.app` inside a DMG. The `.app` is built with Wails v3, so the Go server and a webview window run in one process, and a menu-bar icon keeps fh running after the window closes. The `.app` contains no updater: it shows an Update notice, and its update button runs `brew upgrade --cask`. Decided in issue #152.

This changes one part of [ADR 0001](./0001-go-binary-with-browser-ui.md): the UI is no longer only a browser tab, and fh now has an app bundle. The rest of ADR 0001 (one Go binary, UI embedded in it, macOS only) still holds.

## Decision

- **Window and menu bar**: closing the window keeps fh and its Sessions running in the menu bar. "Quit fh" warns while Sessions are running.
- **No updater code**: Homebrew installs updates. The cask installs the `.app` and does not set `auto_updates`.
- **Update notice**: fh checks GitHub Releases at launch and every 24h. It shows "Update to vX…" in the menu bar and a banner in the UI that the user can dismiss. "Skip this version" is saved in state.
- **Update button**: runs `brew upgrade --cask fh` (or `fh@beta`). Without Homebrew it links to the DMG.
- **Updating mid-Session**: the update button lists the running Sessions and Autopilot. The user confirms the update, or picks "Update when idle".
- **Release channels**: stable uses normal tags (`v0.3.0`); beta uses prerelease tags (`v0.3.0-beta.2`). Each channel has its own cask: `fh` and `fh@beta`. To switch channel, the user installs the other cask.
- **Channel of a running app**: read from its own version. A prerelease suffix means beta. There is no setting.
- **Downgrade safety**: `state.json` gets a `schema_version`. An older fh refuses to start on newer state and says why, instead of silently dropping fields it does not know.
- **DMG build**: a new `make dmg` target will pack the universal (arm64 + amd64) `.app` with `hdiutil`, and `release.yml` will upload the DMG to the GitHub Release.
- **CLI**: the `fh` binary ships inside the `.app`. `fh` will open or focus the app window, and a planned `fh --browser` flag will keep today's browser-tab behaviour.
- **tar.gz binaries**: `fh_darwin_*.tar.gz` keep being published next to the DMG for users who only want the CLI.

## Considered options

- **Launcher `.app` that opens the browser**: least work, but no window of its own and no menu-bar icon.
- **Tauri with fh as a sidecar**: has an official updater, but adds a Rust toolchain and a second process.
- **Swift shell with Sparkle**: has mature updates and channels, but adds Xcode and Swift to the build and runs fh as a child process.
- **In-app updater (the app downloads and replaces itself)**: works without Homebrew, but means more code to write and test, and it would fight Homebrew over who owns the installed version.
- **A version picker for any release**: rejected. Moving to an older release can lose state (see Downgrade safety), and the beta channel covers what users need.

## Consequences

- Wails needs CGO on macOS (the build has `CGO_ENABLED=0` today), so release builds stay on macOS runners.
- **Homebrew users keep the `fh` command.** The cask declares a `binary` stanza pointing at `fh.app/Contents/MacOS/fh`, so Homebrew puts `fh` on PATH as it does today. Users who `brew upgrade` from today's bare binary keep the `fh` command.
- **DMG users get the command through a menu item.** "Install command-line tool" symlinks the binary inside the `.app` into `~/.local/bin`.
- **Gatekeeper**: the `.app` is ad-hoc signed, not notarized, because only a few trusted users install it.
  - The cask's postflight removes the quarantine flag from `fh.app`. Today's hook in `.goreleaser.yaml` removes it from `{{ .StagedPath }}/fh`, the bare binary; it moves to the `.app`.
  - DMG users open the app once through System Settings → Privacy & Security → Open Anyway. Right-click → Open no longer bypasses Gatekeeper on macOS 15.
- Users without Homebrew get the Update notice and a link to the DMG, but no one-click update.
- An update restarts fh and stops running Sessions, which is why the update button lists them first.
- Moving from beta to stable can mean running an older version. The `schema_version` check makes that fail loudly instead of losing state.

## Still open

- Paying for an Apple Developer ID and notarizing the `.app` before any public audience.
- Whether GoReleaser's DMG and app-bundle features are Pro-only. That assumption is why `make dmg` uses `hdiutil`.
- How the CLI-only tar.gz build and the Wails build share one codebase (a build tag or a second binary). To be decided in the Spec.
