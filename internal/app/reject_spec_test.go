package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Reject a Spec with a note (#225, ADR 0014): fh writes the note to the
// Spec, records the action in the Autopilot history as the user's, and has
// the Spec revised with /to-spec: as the next turn of the Session that
// wrote it, or in a new Autopilot Session when there is none. When the
// revision ends, the Spec waits at Approve spec again.

func (f *fh) rejectSpec(c *http.Client, project, feature, note string) (int, map[string]any) {
	f.t.Helper()
	return f.post(c, "/api/autopilot/reject", map[string]any{"project": project, "feature": feature, "note": note})
}

// revisionPrompt is the prompt a revision of the Search Spec gets.
func revisionPrompt(note string) string {
	return "/to-spec .scratch/search/spec.md\n\n" +
		"The user rejected this Spec at Approve spec. Revise it with their note:\n\n" + note
}

// waitBackAtApproveSpec waits until Feature id's history has a new wait
// at Approve spec after its from-th event.
func (f *fh) waitBackAtApproveSpec(c *http.Client, repo, id string, from int) []map[string]any {
	f.t.Helper()
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		events := historyOf(f.t, f.autopilotFeatures(c, repo), id)
		for _, e := range events[min(from, len(events)):] {
			if e["kind"] == "stop" && e["reason"] == "spec-approval" {
				return events
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("Feature %s never came back to Approve spec; history %v", id, historyOf(f.t, f.autopilotFeatures(c, repo), id))
	return nil
}

// lastUserEvent is the index of the last user event in events, or -1.
func lastUserEvent(events []map[string]any) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i]["kind"] == "user" {
			return i
		}
	}
	return -1
}

// waitPrompts waits until Session id's transcript has n prompts.
func (f *fh) waitPrompts(c *http.Client, id string, n int) []string {
	f.t.Helper()
	var got []string
	for deadline := time.Now().Add(waitBudget); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got = userTexts(f.transcript(c, id)); len(got) >= n {
			return got
		}
	}
	f.t.Fatalf("Session %s has prompts %v, want %d", id, got, n)
	return nil
}

func TestRejectingASpecWritesTheNoteAndRevisesItInANewSession(t *testing.T) {
	t.Parallel()
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "Revised the Spec."}}}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", searchSpec)
	f.addProject(c, repo)
	f.waitApproveSpec(c, repo, "search")
	// The wait at Approve spec from before the Reject doesn't count.
	before := len(historyOf(t, f.autopilotFeatures(c, repo), "search"))

	status, body := f.rejectSpec(c, repo, "search", "Split the index work\ninto its own story.")

	if status != http.StatusNoContent {
		t.Fatalf("reject: status %d, body %v", status, body)
	}
	spec, _ := os.ReadFile(filepath.Join(repo, ".scratch/search/spec.md"))
	if !strings.Contains(string(spec), "## Review notes") ||
		!strings.Contains(string(spec), "> Split the index work\n> into its own story.") ||
		!strings.Contains(string(spec), "**Status:** needs-triage") {
		t.Errorf("spec.md after Reject:\n%s\nwant the note under Review notes and the Status unchanged", spec)
	}
	// Autopilot looks only once an hour here: a Session now is Reject's.
	s := f.waitAutopilotSessions(c, 1)[0]
	if link, _ := s["link"].(map[string]any); link["feature"] != "search" || link["ticket"] != nil {
		t.Errorf("revision Session link %v, want the Search Spec", s["link"])
	}
	id, _ := s["id"].(string)
	if got := f.waitPrompts(c, id, 1); got[0] != revisionPrompt("Split the index work\ninto its own story.") {
		t.Errorf("prompt %q, want /to-spec on the Spec with the note", got[0])
	}

	events := f.waitBackAtApproveSpec(c, repo, "search", before)
	user := lastUserEvent(events)
	if user < 0 {
		t.Fatalf("history %v has no event for the user's Reject", events)
	}
	if e := events[user]; e["answer"] != "reject" || e["skill"] != "to-spec" || e["by"] != nil {
		t.Errorf("user event %v, want answer reject before to-spec, with no classifier By", e)
	}
	if !hasEventAfter(events, user, "skill") {
		t.Errorf("history %v: want to-spec started after the user's Reject", events)
	}
}

func TestASecondRejectIsTheNextTurnOfTheSessionThatRevisedTheSpec(t *testing.T) {
	t.Parallel()
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "Revised the Spec."}}}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", searchSpec)
	f.addProject(c, repo)
	f.waitApproveSpec(c, repo, "search")
	// The wait at Approve spec from before the Reject doesn't count.
	before := len(historyOf(t, f.autopilotFeatures(c, repo), "search"))
	if status, body := f.rejectSpec(c, repo, "search", "Name the index."); status != http.StatusNoContent {
		t.Fatalf("first reject: status %d, body %v", status, body)
	}
	id, _ := f.waitAutopilotSessions(c, 1)[0]["id"].(string)
	events := f.waitBackAtApproveSpec(c, repo, "search", before)

	if status, body := f.rejectSpec(c, repo, "search", "Drop the ranking story."); status != http.StatusNoContent {
		t.Fatalf("second reject: status %d, body %v", status, body)
	}

	got := f.waitPrompts(c, id, 2)
	if got[1] != revisionPrompt("Drop the ranking story.") {
		t.Errorf("second prompt %q, want the second note on the same Session", got[1])
	}
	f.waitBackAtApproveSpec(c, repo, "search", len(events))
	if n := len(f.autopilotSessions(c)); n != 1 {
		t.Errorf("Autopilot Sessions = %d, want the one that wrote the Spec", n)
	}
	spec, _ := os.ReadFile(filepath.Join(repo, ".scratch/search/spec.md"))
	if strings.Count(string(spec), "## Review notes") != 1 || !strings.Contains(string(spec), "> Name the index.") ||
		!strings.Contains(string(spec), "> Drop the ranking story.") {
		t.Errorf("spec.md:\n%s\nwant both notes under one Review notes heading", spec)
	}
}

func TestARejectAfterTheWritingSessionExitedStartsANewOne(t *testing.T) {
	t.Parallel()
	exitLater := []any{map[string]any{"say": "Revised the Spec."}, map[string]any{"exitAfter": 300}}
	f := startSlowAutopilotFh(t, script{
		"prompts": map[string]any{revisionPrompt("Name the index."): exitLater},
		"default": []any{map[string]any{"say": "Revised again."}},
	}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", searchSpec)
	f.addProject(c, repo)
	f.waitApproveSpec(c, repo, "search")
	// The wait at Approve spec from before the Reject doesn't count.
	before := len(historyOf(t, f.autopilotFeatures(c, repo), "search"))
	if status, body := f.rejectSpec(c, repo, "search", "Name the index."); status != http.StatusNoContent {
		t.Fatalf("first reject: status %d, body %v", status, body)
	}
	first, _ := f.waitAutopilotSessions(c, 1)[0]["id"].(string)
	events := f.waitBackAtApproveSpec(c, repo, "search", before)
	f.waitAutopilotState(c, first, "exited")

	if status, body := f.rejectSpec(c, repo, "search", "Drop the ranking story."); status != http.StatusNoContent {
		t.Fatalf("second reject: status %d, body %v", status, body)
	}

	var second string
	for _, s := range f.waitAutopilotSessions(c, 2) {
		if s["id"] != first {
			second, _ = s["id"].(string)
		}
	}
	if got := f.waitPrompts(c, second, 1); got[0] != revisionPrompt("Drop the ranking story.") {
		t.Errorf("new Session's prompt %q, want /to-spec with the second note", got[0])
	}
	f.waitBackAtApproveSpec(c, repo, "search", len(events))
}

func TestARejectWithoutANoteOrOnASpecThatDoesNotWaitIsRefused(t *testing.T) {
	t.Parallel()
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", searchSpec)
	scratch(t, repo, "tags/spec.md", "# Tags\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	f.addProject(c, repo)

	if status, _ := f.rejectSpec(c, repo, "search", "  \n"); status != http.StatusBadRequest {
		t.Errorf("reject with an empty note: status %d, want 400", status)
	}
	if status, _ := f.rejectSpec(c, repo, "tags", "No."); status != http.StatusConflict {
		t.Errorf("reject an approved Spec: status %d, want 409", status)
	}
	if status, _ := f.rejectSpec(c, repo, "nope", "No."); status != http.StatusNotFound {
		t.Errorf("reject an unknown Feature: status %d, want 404", status)
	}
	if spec, _ := os.ReadFile(filepath.Join(repo, ".scratch/search/spec.md")); string(spec) != searchSpec {
		t.Errorf("spec.md changed by a refused Reject:\n%s", spec)
	}
	for _, id := range []string{"search", "tags"} {
		for _, e := range historyOf(t, f.autopilotFeatures(c, repo), id) {
			if e["kind"] == "user" {
				t.Errorf("history has %v, want no user event for a refused Reject", e)
			}
		}
	}
}
