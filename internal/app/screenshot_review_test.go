package app_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// reviewed runs Autopilot on a GitHub Feature with one Ticket until
// Autopilot's code review of the Ticket's PR #5 ended done. The PR changes
// files and has body as its description; uiPaths, if not nil, are the
// Project's UI paths. It returns the Ticket's Autopilot Session.
func reviewed(t *testing.T, files []string, body string, uiPaths []string) (*fh, *http.Client, string, ghFake, map[string]any) {
	t.Helper()
	g := newFakeGh(t, []ghIssue{
		{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"roadmap:now", "ready-for-agent"}, SubIssues: []int{11}},
		{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
	}, nil, true)
	g.setPRView(5, files, body)
	// The implement turn holds until the PR is set, as when the skill opens it.
	gate, openGate := turnGate(t)
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"waitFile": gate}, map[string]any{"say": "Opened PR #5."}}}, "done")
	c := f.authorisedClient()
	repo := githubRepo(t)
	f.addProject(c, repo)
	if uiPaths != nil {
		f.setUIPaths(c, repo, uiPaths)
	}
	f.setAutopilot(c, repo, true)
	s := f.waitAutopilotSessions(c, 1)[0]
	g.setPRs([]map[string]any{{"number": 5, "headRefName": s["title"], "state": "OPEN", "url": "https://github.com/someone/shop/pull/5"}})
	openGate()
	return f, c, repo, g, s
}

func (f *fh) setUIPaths(c *http.Client, project string, paths []string) {
	f.t.Helper()
	if status, body := f.post(c, "/api/projects/ui-paths", map[string]any{"path": project, "uiPaths": paths}); status != http.StatusNoContent {
		f.t.Fatalf("ui paths: status %d (%v)", status, body)
	}
}

// lastStop waits until Feature 10's history ends with a Stop point after
// the code review ended done, and returns its reason.
func (f *fh) lastStop(c *http.Client, project string) string {
	f.t.Helper()
	// skill implement, classified done, skill code-review, classified done, stop
	f.waitEvents(c, project, "10", 5)
	events := eventsOf(f.t, f.autopilotFeatures(c, project), "10", "kind", "reason")
	last := events[len(events)-1]
	if last["kind"] != "stop" {
		f.t.Fatalf("events %v, want them to end with a Stop point", events)
	}
	return last["reason"].(string)
}

func TestAPRThatChangesAScreenStopsAtScreenshotReview(t *testing.T) {
	t.Parallel()
	f, c, repo, g, _ := reviewed(t, []string{"web/src/Board.tsx", "internal/app/app.go"}, "Adds the badge.", nil)
	if got := f.lastStop(c, repo); got != "screenshot-review" {
		t.Errorf("Stop point %q, want screenshot-review before the merge", got)
	}
	time.Sleep(300 * time.Millisecond) // more looks add no second Stop point
	events := eventsOf(t, f.autopilotFeatures(c, repo), "10", "kind", "reason")
	if n := strings.Count(asJSON(events), `"stop"`); n != 1 {
		t.Errorf("events %v, want one Stop point", events)
	}
	if u := g.unexpected(); u != "" {
		t.Errorf("unexpected gh commands:\n%s", u)
	}
}

func TestAPRThatChangesNoScreenGoesOnToMerge(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		files   []string
		body    string
		uiPaths []string
	}{
		"no UI path":             {files: []string{"internal/app/app.go"}},
		"No visible UI change":   {files: []string{"web/src/events.ts"}, body: "Renames a type.\n\nNo visible UI change."},
		"the Project's own path": {files: []string{"web/src/Board.tsx"}, uiPaths: []string{"app/views/**"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, c, repo, _, _ := reviewed(t, tc.files, tc.body, tc.uiPaths)
			if got := f.lastStop(c, repo); got != "merge" {
				t.Errorf("Stop point %q, want merge", got)
			}
		})
	}
}

func TestAProjectsOwnUIPathsStopItsPRs(t *testing.T) {
	t.Parallel()
	f, c, repo, _, _ := reviewed(t, []string{"app/views/login.html.erb"}, "", []string{"app/views/**"})
	if got := f.lastStop(c, repo); got != "screenshot-review" {
		t.Errorf("Stop point %q, want screenshot-review", got)
	}
	var paths any
	for _, p := range objects(t, f.state(c), "projects") {
		if p["path"] == repo {
			paths = p["uiPaths"]
		}
	}
	if !reflect.DeepEqual(paths, []any{"app/views/**"}) {
		t.Errorf("uiPaths %v, want the Project's own", paths)
	}
}

func TestAutopilotKeepsScreenshotsOutOfGitAndTellsTheAgentWhereTheyGo(t *testing.T) {
	t.Parallel()
	f, c, repo, _, s := reviewed(t, []string{"internal/x.go"}, "", nil)
	f.lastStop(c, repo)
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+string(exclude), "\n.fh/\n") {
		t.Errorf(".git/info/exclude is %q, want a .fh/ line", exclude)
	}
	prompts := userTexts(f.transcript(c, s["id"].(string)))
	if len(prompts) == 0 || firstLine(prompts[0]) != "/implement #11" || !strings.Contains(prompts[0], ".fh/screenshots/") {
		t.Errorf("prompts %q, want the implement skill, saying where screenshots go", prompts)
	}
}

// firstLine is s up to its first newline: the skill and its argument of a
// prompt Autopilot sent.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func TestScreenshotsAreServedOnlyFromTheTicketsWorktree(t *testing.T) {
	t.Parallel()
	f, c, repo, _, s := reviewed(t, []string{"web/src/Board.tsx"}, "", nil)
	f.lastStop(c, repo)
	shots := filepath.Join(s["worktree"].(string), ".fh", "screenshots")
	writeFile(t, filepath.Join(shots, "board.png"), "\x89PNG fake")
	writeFile(t, filepath.Join(shots, "notes.txt"), "not a screenshot")
	secret := filepath.Join(t.TempDir(), "secret.png")
	writeFile(t, secret, "secret")
	if err := os.Symlink(secret, filepath.Join(shots, "link.png")); err != nil {
		t.Fatal(err)
	}

	q := url.Values{"project": {repo}, "feature": {"10"}, "ticket": {"11"}}
	resp := get(t, c, f.origin()+"/api/autopilot/screenshots?"+q.Encode())
	list := decodeObject(t, readBody(t, resp))
	if got := list["screenshots"]; !reflect.DeepEqual(got, []any{"board.png"}) {
		t.Errorf("screenshots %v, want only board.png", got)
	}
	if pr, _ := list["pr"].(map[string]any); pr["number"] != float64(5) {
		t.Errorf("pr %v, want PR #5", list["pr"])
	}

	shot := func(name string) (int, string) {
		q := url.Values{"project": {repo}, "feature": {"10"}, "ticket": {"11"}, "name": {name}}
		resp, err := c.Get(f.origin() + "/api/autopilot/screenshot?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if status, body := shot("board.png"); status != http.StatusOK || body != "\x89PNG fake" {
		t.Errorf("board.png: status %d, body %q", status, body)
	}
	for _, name := range []string{"../../../../etc/passwd", "link.png", "notes.txt", "missing.png"} {
		if status, body := shot(name); status < 400 || strings.Contains(body, "secret") {
			t.Errorf("%s: status %d, body %q, want it refused", name, status, body)
		}
	}
}

func TestLooksGoodCommentsOnThePRAndMovesItOnToMerge(t *testing.T) {
	t.Parallel()
	f, c, repo, g, _ := reviewed(t, []string{"web/src/Board.tsx"}, "", nil)
	f.lastStop(c, repo)

	status, body := f.post(c, "/api/autopilot/looks-good", map[string]any{"project": repo, "feature": "10", "ticket": "11"})
	if status != http.StatusNoContent {
		t.Fatalf("looks good: status %d (%v)", status, body)
	}
	if got := g.prComments(); !strings.Contains(got, "pr comment 5") || !strings.Contains(got, "Screen reviewed in fh") {
		t.Errorf("PR comments %q, want \"Screen reviewed in fh\" on PR #5", got)
	}
	want := []map[string]any{
		{"kind": "stop", "reason": "screenshot-review"},
		{"kind": "user", "answer": "looks-good"},
		{"kind": "stop", "reason": "merge"},
	}
	events := eventsOf(t, f.waitEvents(c, repo, "10", 7), "10", "kind", "reason", "answer")
	if got := events[len(events)-3:]; !reflect.DeepEqual(got, want) {
		t.Errorf("events end\n%v\nwant\n%v", got, want)
	}

	// A second Looks good finds nothing waiting.
	if status, _ := f.post(c, "/api/autopilot/looks-good", map[string]any{"project": repo, "feature": "10", "ticket": "11"}); status != http.StatusConflict {
		t.Errorf("second looks good: status %d, want 409", status)
	}
}
