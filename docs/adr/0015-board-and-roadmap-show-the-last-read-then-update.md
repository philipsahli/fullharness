---
status: accepted
---

# The Software factory and Roadmap tabs show the last read, then update

When you leave the Software factory tab or the Roadmap tab and come back, the tab is empty until a new read returns. That read can wait on several `gh` calls to GitHub and the `git` calls for every Project. We grilled this on 2026-11-27 (Spec #271) and decided to show the last read data at once and update it in the background. A tab can be up to one poll (60 s) behind, but you no longer wait when you come back.

## Decision

- **In the browser:** `App` keeps the last board for each Project and the last Roadmap for this sitting. A tab shows that data when it opens, then reads again in the background. A page reload starts empty.
- **First visit:** until the first read returns, the tab shows the "Board loading" state from the mockup, not empty columns.
- **While a read runs:** the Refresh button pulses. Nothing else changes until the new data arrives.
- **When a read fails:** the old data stays on screen with "Last read N min ago". It is not cleared.
- **On the server:** when a Project's `gh` read has expired but is less than 5 minutes old, fh sends it at once and reads GitHub again in the background. If it is older (for example after the laptop slept), the request waits for a new read. The `git` calls behind the board get a short cache too.
- **Fresh reads:** the Refresh button (`refresh=1`) and fh's own tracker writes still skip the cache and wait for a new read.
- **Polling:** both tabs keep polling every 60 s. Board and Roadmap data are not pushed over the WebSocket.

## Considered options

- **Keep both tabs mounted but hidden.** Rejected: hidden tabs keep polling, and the Roadmap's moves and filters would stay from the last visit.
- **Save the last read in `localStorage`.** Rejected for now: after a restart it could show Tickets that are days old, and it needs a storage format. We can add it if cold starts feel slow too.
- **Push board and Roadmap data over the WebSocket, as ADR-0013 does for Autopilot status.** Rejected for now: fh only learns about tracker changes by polling `gh`, so pushing would move the polling to the server without making reads faster.
- **Fix it only on the server.** Rejected: the browser would still throw the data away on every tab switch.

## Consequences

- Right after a merge made outside fh, a tab can show the old state until the next poll. Press Refresh to read now.
