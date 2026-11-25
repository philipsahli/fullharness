---
status: proposed
---

# The UI's look follows the design mockup

The design mockup, `docs/design/fh-mockup.html`, is where fh's look is decided. The app's stylesheet, `web/src/styles.css`, copies the mockup's tokens and component classes under the same names. We decided this while planning the UI polish (#157–#160 came out of the same review), because many app buttons and controls had kept the browser's default look and drifted from the mockup.

## Decision

- **The mockup is the style source.** `web/src/styles.css` copies the mockup's CSS variables (light and dark) and its component classes, such as `.btn`, `.btn.primary`, `.btn.small`, `.stage`, `.lbl` and `.agent`, with the same names and values. A change to the look goes into the mockup first, then into `styles.css`.
- **Fonts are part of the binary.** fh serves Instrument Sans (400, 500, 600), Bricolage Grotesque (600, 700) and JetBrains Mono (400, 500) itself, as latin-subset woff2 files embedded with the UI. All three use the SIL Open Font License.
- **At most one primary button per area**, such as the board header, the message composer, a permission card or a dialog. Every other action is a plain `.btn` or `.btn.small`. Links are only for navigation, for example to GitHub, a branch or an issue.
- **Pixel tests are local.** Playwright screenshot tests run on a developer's Mac with `make e2e-visual`. `make e2e-visual-update` takes the baselines, which are gitignored. CI and `make check` don't run them. Run them before opening a UI pull request, and review any difference together with the before/after screenshots.

Dark mode follows the OS setting (`prefers-color-scheme`); fh has no theme switch.

## Considered options

- **A shared tokens file** that both the mockup and the app import. It would stop the two from drifting, but the mockup could no longer be opened as one file. Copying keeps the mockup standalone; review compares the two.
- **Google Fonts**, as the mockup used. fh is a local tool: each launch would call Google, and offline the UI would fall back to system fonts.
- **Pixel tests in CI with one baseline per OS.** Font rendering differs between macOS and Linux, so CI would need two sets of baselines, and the Linux set can't be made on a Mac without Docker or a CI job.

## Consequences

- A reviewer of a UI pull request compares the screens with the mockup, and a pull request that changes the look also changes the mockup.
- Pixel tests only catch changes on the machine that took the baselines, so screenshots in the pull request stay the shared review step.
