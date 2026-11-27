package app_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// requestChanges posts Request changes on Ticket 11's PR with note.
func (f *fh) requestChanges(c *http.Client, project, note string) (int, map[string]any) {
	f.t.Helper()
	return f.post(c, "/api/autopilot/request-changes", map[string]any{"project": project, "feature": "10", "ticket": "11", "note": note})
}

// shotsOf reads Ticket 11's Screenshot review: its screenshots and which
// of them are new since the user's latest Request changes.
func (f *fh) shotsOf(c *http.Client, project string) map[string]any {
	f.t.Helper()
	q := url.Values{"project": {project}, "feature": {"10"}, "ticket": {"11"}}
	resp, err := c.Get(f.origin() + "/api/autopilot/screenshots?" + q.Encode())
	if err != nil {
		f.t.Fatal(err)
	}
	return decodeObject(f.t, readBody(f.t, resp))
}

// tail is the last n of events.
func tail(events []map[string]any, n int) []map[string]any {
	if len(events) < n {
		return events
	}
	return events[len(events)-n:]
}

func TestRequestChangesCommentsOnThePRAndSendsTheNoteToItsSession(t *testing.T) {
	t.Parallel()
	f, c, repo, g, s := reviewed(t, []string{"web/src/Board.tsx"}, "", nil)
	f.lastStop(c, repo)
	shots := filepath.Join(s["worktree"].(string), ".fh", "screenshots")
	if err := os.MkdirAll(shots, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(shots, "before.png"), "\x89PNG before")

	if status, _ := f.requestChanges(c, repo, "  "); status != http.StatusBadRequest {
		t.Errorf("an empty note: status %d, want 400", status)
	}
	const note = "The badge covers the card's title."
	if status, body := f.requestChanges(c, repo, note); status != http.StatusNoContent {
		t.Fatalf("request changes: status %d (%v)", status, body)
	}
	if got := g.prComments(); !strings.Contains(got, "pr comment 5") || !strings.Contains(got, note) {
		t.Errorf("PR comments %q, want the note on PR #5", got)
	}

	// The note goes to the Ticket's own Session as its next turn: the
	// implement skill, the note, and where the new screenshots go.
	id := s["id"].(string)
	deadline := time.Now().Add(waitBudget)
	var fix string
	for fix == "" && time.Now().Before(deadline) {
		for _, p := range userTexts(f.transcript(c, id)) {
			if strings.Contains(p, note) {
				fix = p
			}
		}
		if fix == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if firstLine(fix) != "/implement #11" || !strings.Contains(fix, ".fh/screenshots/") {
		t.Fatalf("follow-up turn %q, want /implement #11 with the note and where screenshots go", fix)
	}
	if n := len(f.autopilotSessions(c)); n != 1 {
		t.Errorf("%d Autopilot Sessions, want the Ticket's one Session", n)
	}

	// The fix turn ends done, so the PR is back at Screenshot review.
	// implement, classified, code-review, classified, stop, user, implement, classified, stop
	events := eventsOf(t, f.waitEvents(c, repo, "10", 9), "10", "kind", "reason", "answer", "skill")
	want := []map[string]any{
		{"kind": "stop", "reason": "screenshot-review", "skill": "code-review"},
		{"kind": "user", "answer": "request-changes", "skill": "code-review"},
		{"kind": "skill", "skill": "implement"},
		{"kind": "classified", "skill": "implement"},
		{"kind": "stop", "reason": "screenshot-review", "skill": "code-review"},
	}
	if got := tail(events, 5); !reflect.DeepEqual(got, want) {
		t.Errorf("events end\n%v\nwant\n%v", got, want)
	}

	// A screenshot saved after the request is new; the old one isn't.
	time.Sleep(20 * time.Millisecond) // a later modification time on every file system
	writeFile(t, filepath.Join(shots, "after.png"), "\x89PNG after")
	list := f.shotsOf(c, repo)
	if got := list["new"]; !reflect.DeepEqual(got, []any{"after.png"}) {
		t.Errorf("new screenshots %v, want only after.png", got)
	}
}

func TestRequestChangesStartsANewSessionWhenTheTicketsSessionIsGone(t *testing.T) {
	t.Parallel()
	f, c, repo, _, s := reviewed(t, []string{"web/src/Board.tsx"}, "", nil)
	f.lastStop(c, repo)
	if status, body := f.post(c, "/api/sessions/"+s["id"].(string)+"/close", map[string]any{"confirm": true}); status != http.StatusNoContent {
		t.Fatalf("close: status %d (%v)", status, body)
	}

	const note = "Make the badge smaller."
	if status, body := f.requestChanges(c, repo, note); status != http.StatusNoContent {
		t.Fatalf("request changes: status %d (%v)", status, body)
	}
	fresh := f.waitAutopilotSessions(c, 1)[0]
	if fresh["id"] == s["id"] {
		t.Fatalf("Session %v, want a new one", fresh["id"])
	}
	if fresh["worktree"] != s["worktree"] {
		t.Errorf("new Session runs in %v, want the Ticket's Worktree %v", fresh["worktree"], s["worktree"])
	}
	deadline := time.Now().Add(waitBudget)
	var prompts []string
	for len(prompts) == 0 && time.Now().Before(deadline) {
		prompts = userTexts(f.transcript(c, fresh["id"].(string)))
		if len(prompts) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if len(prompts) == 0 || firstLine(prompts[0]) != "/implement #11" || !strings.Contains(prompts[0], note) {
		t.Errorf("prompts %q, want /implement #11 with the note", prompts)
	}
}

func TestRequestChangesNeedsAPRAtScreenshotReview(t *testing.T) {
	t.Parallel()
	f, c, repo, _, _ := reviewed(t, []string{"internal/app/app.go"}, "", nil)
	if got := f.lastStop(c, repo); got != "merge" {
		t.Fatalf("Stop point %q, want merge", got)
	}
	if status, _ := f.requestChanges(c, repo, "Change it."); status != http.StatusConflict {
		t.Errorf("status %d, want 409: the PR waits at merge, not Screenshot review", status)
	}
}
