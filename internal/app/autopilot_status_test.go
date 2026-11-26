package app_test

import (
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Autopilot status (ADR-0013): the snapshot carries every Feature's status,
// and an "autopilot" event brings the change the moment one changes.

// statusIn returns Feature id's status from an "autopilot" event or a
// snapshot, or nil.
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
// Feature id the status kind, and returns that status.
func waitStatus(t *testing.T, tab *websocket.Conn, id, kind string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ev := readEvent(t, tab)
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

func TestAutopilotStatusGoesFromRunningToWaitingAtSpecApproval(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"sleepMs": 600}, map[string]any{"say": "Wrote the tickets."}}}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	f.addProject(c, repo)
	tab, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	readEvent(t, tab) // the snapshot

	f.setAutopilot(c, repo, true)
	running := waitStatus(t, tab, "search", "running")
	if running["skill"] != "to-tickets" || running["session"] == "" || running["project"] != repo {
		t.Errorf("running status %v, want to-tickets in a Session of %s", running, repo)
	}

	// The Spec loses its approval while the turn runs, so when the turn
	// ends done the Feature waits at Spec approval again.
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n")
	waiting := waitStatus(t, tab, "search", "waiting")
	if waiting["reason"] != "spec-approval" {
		t.Errorf("waiting status %v, want reason spec-approval", waiting)
	}

	// A new tab gets the status from its snapshot alone.
	fresh, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	if s := statusIn(t, readEvent(t, fresh), "autopilot", "search"); s == nil || s["kind"] != "waiting" {
		t.Errorf("snapshot status %v, want waiting", s)
	}
}

func TestAProjectWithAutopilotOffSendsNoStatuses(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	f.addProject(c, repo)
	time.Sleep(200 * time.Millisecond) // the statuses are worked out on each Project change

	tab, _, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := readEvent(t, tab)["autopilot"].([]any); !ok || len(list) != 0 {
		t.Errorf("snapshot autopilot %v, want an empty list: every Feature is off", list)
	}
}
