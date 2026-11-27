package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Approve spec in the Inbox (#229, ADR 0014): Approve marks the Spec
// ready-for-agent through the tracker write path, records the action in
// the Autopilot history as the user's, and steps the Project at once.

// startSlowAutopilotFh is startAutopilotFh with Autopilot's own look an
// hour apart, so a step a test sees came from the user's action, not
// from the tick.
func startSlowAutopilotFh(t *testing.T, s script, verdict string) *fh {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	classifier := filepath.Join(dir, "fake-classifier")
	writeFile(t, classifier, "#!/bin/sh\ncat >/dev/null\n"+
		`echo '{"is_error":false,"result":"`+verdict+`","total_cost_usd":0,"usage":{}}'`+"\n")
	if err := os.Chmod(classifier, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, fmt.Sprintf("[agents.claude]\ncommand = [%q]\nenv = { FH_FAKE_SCRIPT = %q }\n\n"+
		"[classifier]\nname = \"fake\"\ncommand = [%q]\n", fakeAgentBin, raw, classifier))
	opts := appOptions(dir)
	opts.AutopilotInterval = time.Hour
	return startFhWith(t, opts)
}

func (f *fh) approveSpec(c *http.Client, project, feature string) (int, map[string]any) {
	f.t.Helper()
	return f.post(c, "/api/autopilot/approve", map[string]any{"project": project, "feature": feature})
}

// searchSpec is a local Spec in Now that waits at Approve spec. On GitHub
// an unapproved Spec with no Tickets reads as an Untriaged issue (#196),
// so Approve spec is reached on local markdown.
const searchSpec = "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n"

// waitApproveSpec turns Autopilot on for repo and waits until Feature id
// waits at Approve spec.
func (f *fh) waitApproveSpec(c *http.Client, repo, id string) {
	f.t.Helper()
	tab, _, err := f.dialWS(c, f.origin())
	if err != nil {
		f.t.Fatal(err)
	}
	readEvent(f.t, tab) // the snapshot
	f.setAutopilot(c, repo, true)
	if st := waitStatus(f.t, tab, id, "waiting"); st["reason"] != "spec-approval" {
		f.t.Fatalf("Feature %s waits at %v, want Approve spec", id, st["reason"])
	}
}

func TestApproveSpecMarksTheSpecReadyAndStartsTheNextSkillAtOnce(t *testing.T) {
	t.Parallel()
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "Wrote the tickets."}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", searchSpec)
	f.addProject(c, repo)
	f.waitApproveSpec(c, repo, "search")

	status, body := f.approveSpec(c, repo, "search")

	if status != http.StatusNoContent {
		t.Fatalf("approve: status %d, body %v", status, body)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".scratch/search/spec.md")); !strings.Contains(string(b), "**Status:** ready-for-agent") {
		t.Errorf("spec.md after Approve:\n%s\nwant its Status line ready-for-agent", b)
	}
	// Autopilot looks only once an hour here: a Session now is Approve's step.
	s := f.waitAutopilotSessions(c, 1)[0]
	if link, _ := s["link"].(map[string]any); link["feature"] != "search" {
		t.Errorf("Autopilot Session %v, want one for Search", s)
	}
	id, _ := s["id"].(string)
	var got []string
	for deadline := time.Now().Add(waitBudget); len(got) == 0 && time.Now().Before(deadline); {
		if got = userTexts(f.transcript(c, id)); len(got) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if len(got) != 1 || got[0] != "/to-tickets .scratch/search/spec.md" {
		t.Errorf("prompt %v, want /to-tickets on the Spec", got)
	}

	events := historyOf(t, f.autopilotFeatures(c, repo), "search")
	user := -1
	for i, e := range events {
		if e["kind"] == "user" {
			user = i
			if e["answer"] != "approve" || e["skill"] != "to-tickets" || e["by"] != nil {
				t.Errorf("user event %v, want answer approve before to-tickets, with no classifier By", e)
			}
		}
	}
	if user < 0 {
		t.Fatalf("history %v has no event for the user's Approve", events)
	}
	if !hasEventAfter(events, user, "skill") {
		t.Errorf("history %v: want to-tickets started after the user's Approve", events)
	}
}

func TestApproveSpecOnASpecThatDoesNotWaitIsRefused(t *testing.T) {
	t.Parallel()
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	f.addProject(c, repo)

	status, body := f.approveSpec(c, repo, "search")

	if status != http.StatusConflict {
		t.Errorf("approve an approved Spec: status %d (%v), want 409", status, body)
	}
	if status, _ := f.approveSpec(c, repo, "nope"); status != http.StatusNotFound {
		t.Errorf("approve an unknown Feature: status %d, want 404", status)
	}
	for _, e := range historyOf(t, f.autopilotFeatures(c, repo), "search") {
		if e["kind"] == "user" {
			t.Errorf("history has %v, want no user event for a refused Approve", e)
		}
	}
}

func TestAFailedApproveShowsTheErrorAndRecordsNothing(t *testing.T) {
	t.Parallel()
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", searchSpec)
	f.addProject(c, repo)
	f.waitApproveSpec(c, repo, "search")
	// The write is atomic: a folder fh can't write to fails it.
	dir := filepath.Join(repo, ".scratch", "search")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // so the temp dir can be removed

	status, body := f.approveSpec(c, repo, "search")

	if status != http.StatusBadGateway || body["error"] == nil || body["error"] == "" {
		t.Errorf("approve: status %d, body %v; want 502 with the write's error", status, body)
	}
	for _, e := range historyOf(t, f.autopilotFeatures(c, repo), "search") {
		if e["kind"] == "user" {
			t.Errorf("history has %v, want no user event for a write that failed", e)
		}
	}
	f.noAutopilotSessionFor(c, 300*time.Millisecond)
}

// historyOf is Feature id's events in GET /api/autopilot's Features.
func historyOf(t *testing.T, features []map[string]any, id string) []map[string]any {
	t.Helper()
	for _, h := range features {
		if h["feature"] == id {
			return objects(t, h, "events")
		}
	}
	return nil
}

func hasEventAfter(events []map[string]any, i int, kind string) bool {
	for _, e := range events[i+1:] {
		if e["kind"] == kind {
			return true
		}
	}
	return false
}
