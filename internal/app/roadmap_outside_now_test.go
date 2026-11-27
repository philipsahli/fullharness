package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A Roadmap move works the Autopilot statuses out again at once (#217,
// ADR-0013): a Feature moved out of Now mid-turn shows "Session running
// outside Now" straight away, not on Autopilot's next look at the tracker.
// Autopilot looks once an hour here and the turn waits on a permission
// request, so nothing else makes fh work the statuses out again: only the
// move can.
func TestARoadmapMoveOutOfNowMarksTheRunningTurnAtOnce(t *testing.T) {
	t.Parallel()
	s := script{"default": []any{
		map[string]any{"permission": map[string]any{"id": "p1", "title": "go test ./...", "kind": "execute", "command": "go test ./..."}},
		map[string]any{"say": "Which labels?"},
	}}
	dir := t.TempDir()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	classifier := filepath.Join(dir, "fake-classifier")
	writeFile(t, classifier, "#!/bin/sh\ncat >/dev/null\n"+`echo '{"is_error":false,"result":"asking","total_cost_usd":0,"usage":{}}'`+"\n")
	if err := os.Chmod(classifier, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, fmt.Sprintf("[agents.claude]\ncommand = [%q]\nenv = { FH_FAKE_SCRIPT = %q }\n\n"+
		"[classifier]\nname = \"fake\"\ncommand = [%q]\n", fakeAgentBin, raw, classifier))
	opts := appOptions(dir)
	opts.AutopilotInterval = time.Hour
	f := startFhWith(t, opts)
	c := f.authorisedClient()

	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	f.addProject(c, repo)
	tab := f.connectTab(c)
	inbox := f.connectTab(c)

	f.setAutopilot(c, repo, true)
	if s := waitStatus(t, tab, "search", "running"); s["outsideNow"] == true {
		t.Fatalf("running status %v, want it in Now", s)
	}
	req := waitInbox(t, inbox, 1)[0] // the turn now waits for the user
	// The Session's change to Waiting kicks a recompute of its own; let it
	// land before the move, so only the move can bring the next one. A
	// read that times out closes its tab, so a fresh tab follows the move.
	for nextEvent(t, tab, time.Second) != nil {
	}
	tab = f.connectTab(c)
	if s := statusIn(t, snapshotOf(tab), "autopilot", "search"); s == nil || s["kind"] != "running" || s["outsideNow"] == true {
		t.Fatalf("status before the move %v, want running in Now", s)
	}

	if status, body := f.post(c, "/api/tracker/roadmap", map[string]any{
		"project": repo, "ref": ".scratch/search/spec.md", "group": "next",
	}); status != http.StatusNoContent {
		t.Fatalf("move: status %d (%v), want 204", status, body)
	}
	if !waitOutsideNow(t, tab, "search", 5*time.Second) {
		t.Fatal("no running status outside Now within 5 s of the move")
	}

	// The turn ends: outside Now and nothing runs, so the Feature is off.
	if status, body := f.answer(c, req["id"].(string), "allow"); status != http.StatusNoContent {
		t.Fatalf("answer: status %d (%v), want 204", status, body)
	}
	if off := waitStatus(t, tab, "search", "off"); off["reason"] != "not-in-now" || off["outsideNow"] == true {
		t.Errorf("status after the turn %v, want off, not-in-now", off)
	}
}

// waitOutsideNow reads events from tab for up to d, and reports whether an
// "autopilot" event gave Feature id a running status outside Now.
func waitOutsideNow(t *testing.T, tab *websocket.Conn, id string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			return false
		}
		if ev["type"] != "autopilot" {
			continue
		}
		if s := statusIn(t, ev, "statuses", id); s != nil && s["kind"] == "running" && s["outsideNow"] == true {
			return true
		}
	}
	return false
}

// snapshotOf is the snapshot connectTab read for tab.
func snapshotOf(tab *websocket.Conn) map[string]any {
	snap, _ := snapshots.Load(tab)
	m, _ := snap.(map[string]any)
	return m
}
