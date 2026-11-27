package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Autopilot status (ADR-0013): the snapshot carries every Feature's
// Autopilot status, and an "autopilot" event brings the change the moment
// one changes.

// statusIn returns Feature id's Autopilot status from an "autopilot" event
// or a snapshot, or nil.
func statusIn(t *testing.T, ev map[string]any, key, id string) map[string]any {
	t.Helper()
	list, _ := ev[key].([]any)
	for _, raw := range list {
		if s, _ := raw.(map[string]any); s["feature"] == id {
			return s
		}
	}
	return nil
}

// waitStatus reads events from tab until an "autopilot" event gives
// Feature id the Autopilot status kind, and returns that status.
func waitStatus(t *testing.T, tab *websocket.Conn, id, kind string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] != "autopilot" {
			continue
		}
		if s := statusIn(t, ev, "statuses", id); s != nil && s["kind"] == kind {
			return s
		}
	}
	t.Fatalf("Feature %s never became %s", id, kind)
	return nil
}

// The normal path through Approve spec: a Spec in Now waits for approval;
// once approved, Autopilot runs to-tickets (tracker.FeatureStep at
// StageSpec); that turn ends asking, a Stop point of its own.
func TestAutopilotStatusFollowsTheFeatureThroughApproveSpec(t *testing.T) {
	// The to-tickets turn holds until the test has seen it running, so a
	// slow machine can't end it first (#220).
	gate, openGate := turnGate(t)
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"waitFile": gate}, map[string]any{"say": "Which labels?"}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n")
	f.addProject(c, repo)
	tab, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	readEvent(t, tab) // the snapshot

	f.setAutopilot(c, repo, true)
	approve := waitStatus(t, tab, "search", "waiting")
	if approve["reason"] != "spec-approval" || approve["skill"] != "to-tickets" || approve["since"] == nil {
		t.Errorf("waiting status %v, want Approve spec before to-tickets, with the time it began", approve)
	}

	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	running := waitStatus(t, tab, "search", "running")
	if running["skill"] != "to-tickets" || running["session"] == "" || running["project"] != repo {
		t.Errorf("running status %v, want to-tickets in a Session of %s", running, repo)
	}
	openGate()

	asking := waitStatus(t, tab, "search", "waiting")
	if asking["reason"] != "asking" {
		t.Errorf("waiting status %v, want reason asking", asking)
	}

	// A new tab gets the Autopilot status from its snapshot alone.
	fresh, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	if s := statusIn(t, readEvent(t, fresh), "autopilot", "search"); s == nil || s["reason"] != "asking" {
		t.Errorf("snapshot Autopilot status %v, want waiting, asking", s)
	}
}

// waitTool reads events from tab until an "autopilot" event gives Feature
// id a running Autopilot status on tool call tool.
func waitTool(t *testing.T, tab *websocket.Conn, id, tool string) {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] != "autopilot" {
			continue
		}
		if s := statusIn(t, ev, "statuses", id); s != nil && s["kind"] == "running" && s["tool"] == tool {
			return
		}
	}
	t.Fatalf("Feature %s never showed tool call %q", id, tool)
}

// The live pulse (#205): a running Autopilot status carries the tool call
// its Session is on, the next tool call replaces it, and it is gone once
// the turn ends. After each tool call the Agent asks a permission and
// waits, so the test, not a sleep, says when the turn moves on: fh may
// merge status updates that come close together (#153).
func TestARunningAutopilotStatusFollowsTheToolCalls(t *testing.T) {
	t.Parallel()
	ask := func(id string) map[string]any {
		return map[string]any{"permission": map[string]any{"id": id, "title": "go test ./...", "kind": "execute", "command": "go test ./..."}}
	}
	f := startAutopilotFh(t, script{"default": []any{
		map[string]any{"tool": map[string]any{"id": "t1", "title": "Read internal/acp/client.go", "kind": "read", "status": "in_progress"}},
		ask("p1"),
		map[string]any{"tool": map[string]any{"id": "t2", "title": "Edit web/src/Board.tsx", "kind": "edit", "status": "in_progress"}},
		ask("p2"),
		map[string]any{"say": "Which labels?"},
	}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	f.addProject(c, repo)
	tab := f.connectTab(c)   // follows the Autopilot status
	inbox := f.connectTab(c) // follows the permission requests

	f.setAutopilot(c, repo, true)
	for i, tool := range []string{"Read internal/acp/client.go", "Edit web/src/Board.tsx"} {
		waitTool(t, tab, "search", tool)
		req := waitInbox(t, inbox, 1)[0]
		if status, body := f.answer(c, req["id"].(string), "allow"); status != http.StatusNoContent {
			t.Fatalf("answer %d: status %d (%v), want 204", i+1, status, body)
		}
		waitInbox(t, inbox, 0)
	}

	if after := waitStatus(t, tab, "search", "waiting"); after["tool"] != nil {
		t.Errorf("status after the turn %v, want no tool call", after)
	}
}

// fh works the off status out too, so the UI never makes one up: a Feature
// in Now with Autopilot off for its Project says so, one outside Now says
// that instead.
func TestAProjectWithAutopilotOffGetsOffStatusesWithTheirReason(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	scratch(t, repo, "billing/spec.md", "# Billing\n\n**Roadmap:** next\n")
	tab, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	readEvent(t, tab) // the snapshot, from before the Project

	f.addProject(c, repo)
	off := waitStatus(t, tab, "search", "off")
	if off["reason"] != "autopilot-off" {
		t.Errorf("search %v, want off because the Project has Autopilot off", off)
	}

	fresh, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	if s := statusIn(t, readEvent(t, fresh), "autopilot", "billing"); s == nil || s["kind"] != "off" || s["reason"] != "not-in-now" {
		t.Errorf("billing %v, want off, not in Now", s)
	}
}
