package app_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ready labels a GitHub issue so that, with no Tickets yet, it reads as a Spec.
var ready = []string{"ready-for-agent"}

// ideaByName finds the Idea called name on a board.
func ideaByName(t *testing.T, board map[string]any, name string) map[string]any {
	t.Helper()
	for _, i := range objects(t, board, "ideas") {
		if i["name"] == name {
			return i
		}
	}
	t.Fatalf("no idea %q in %v", name, board["ideas"])
	return nil
}

func TestGrillNewIdeaStartsANamedSessionInItsOwnWorktree(t *testing.T) {
	t.Parallel()
	repo := realPath(t, newRepo(t, t.TempDir(), "notes"))
	scratch(t, repo, "login/spec.md", "# Login\n")
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "agent": "claude",
		"idea": "Dark mode, for night owls!", "prompt": "/grill-with-docs "})
	sum := waitEnded(t, tab, id)

	wt := filepath.Join(filepath.Dir(repo), "notes.worktrees", "idea", "dark-mode-for-night-owls")
	if sum["worktree"] != wt || sum["idea"] != "Dark mode, for night owls!" {
		t.Errorf("Session worktree, idea = %v, %v; want %s and the Idea's name", sum["worktree"], sum["idea"], wt)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("Worktree on idea/dark-mode-for-night-owls: %v", err)
	}
	if items := f.transcript(c, id); len(items) == 0 || items[0]["text"] != "/grill-with-docs " {
		t.Errorf("transcript = %v, want the grilling prompt first", items)
	}

	b := f.board(c, repo)
	idea := ideaByName(t, b, "Dark mode, for night owls!")
	if idea["session"] != id {
		t.Errorf("idea = %v, want it tied to Session %s", idea, id)
	}
	if s := featureByID(t, b, "login")["stage"]; s != "spec" {
		t.Errorf("login at %v, want spec: Ideas leave Features alone", s)
	}
}

func TestAnIdeaSessionStartsWithTheAgentsGrillSkill(t *testing.T) {
	t.Parallel()
	f := startFhWithAgents(t, map[string]script{
		"claude":  {"onStart": []any{commands("mattpocock-skills:grill-with-docs")}},
		"copilot": {"onStart": []any{commands("grill-me")}},
	}, nil)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "notes"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	// Starting any Session of an Agent tells fh its slash commands.
	for _, agentID := range []string{"claude", "copilot"} {
		id := f.startSession(c, map[string]any{"project": repo, "branch": "warmup-" + agentID, "agent": agentID})
		waitState(t, tab, id, "idle")
	}

	for name, prompt := range map[string]string{
		"Dark mode": "",
		"Search":    "for the docs site",
		"Export":    "/mattpocock-skills:grill-with-docs about CSV",
	} {
		id := f.startSession(c, map[string]any{"project": repo, "agent": "claude", "idea": name, "prompt": prompt})
		waitEnded(t, tab, id)
		want := strings.TrimSpace("/mattpocock-skills:grill-with-docs " + strings.TrimPrefix(prompt, "/mattpocock-skills:grill-with-docs "))
		if items := f.transcript(c, id); len(items) == 0 || items[0]["text"] != want {
			t.Errorf("Idea %s with prompt %q: transcript %v, want it to start with %q", name, prompt, items, want)
		}
	}

	status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "agent": "copilot", "idea": "Sync"})
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["error"]), "/grill-with-docs") {
		t.Errorf("Idea with an Agent without the skill: status %d (%v), want 400 naming /grill-with-docs", status, body)
	}
}

func TestASpecThatAppearsIsSuggestedAndLinkedInOneClick(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{{Number: 1, Title: "Spec: Login", State: "OPEN", Labels: ready}}, nil, true)
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "agent": "claude", "idea": "Dark mode"})
	waitEnded(t, tab, id)
	if got := asJSON(ideaByName(t, f.board(c, repo), "Dark mode")["suggest"]); got != "[]" {
		t.Errorf("suggest before /to-spec = %s, want none: Spec #1 was there before the Idea", got)
	}

	// The grilling Session's /to-spec files Spec #5; #6 is a closed one.
	fakeGh(t, []ghIssue{
		{Number: 1, Title: "Spec: Login", State: "OPEN", Labels: ready},
		{Number: 5, Title: "Spec: Dark mode", State: "OPEN", Labels: ready},
		{Number: 6, Title: "Spec: Old", State: "CLOSED", SubIssues: []int{7}},
		{Number: 7, Title: "Old ticket", State: "CLOSED", Parent: 6},
	}, nil, true)
	got := asJSON(ideaByName(t, boardRefresh(t, f, c, repo), "Dark mode")["suggest"])
	if want := `[{"id":"5","ref":"#5","title":"Spec: Dark mode"}]`; got != want {
		t.Errorf("suggest = %s, want %s: only the open Spec that appeared since", got, want)
	}

	if status, body := f.post(c, "/api/sessions/"+id+"/link-spec", map[string]any{"spec": "5"}); status != http.StatusNoContent {
		t.Fatalf("link: status %d (%v)", status, body)
	}
	sum := waitFor(t, tab, id, "linked", func(s map[string]any) bool { return s["link"] != nil })
	if got := asJSON(sum["link"]); got != `{"feature":"5"}` {
		t.Errorf("Session link = %s, want Spec 5", got)
	}
	b := f.board(c, repo)
	if n := len(objects(t, b, "ideas")); n != 0 {
		t.Errorf("ideas after linking = %v, want none: the Idea is now Spec #5", b["ideas"])
	}
	// A list, not null, even without Tickets: the board UI counts them.
	if spec := featureByID(t, b, "5"); spec["idea"] != "Dark mode" || spec["stage"] != "spec" || asJSON(spec["tickets"]) != "[]" {
		t.Errorf("Spec #5 = %v, want at Spec with no Tickets, naming its Idea", spec)
	}

	f.stop()
	restarted := startFhWith(t, f.opts)
	rc := restarted.authorisedClient()
	if spec := featureByID(t, restarted.board(rc, repo), "5"); spec["idea"] != "Dark mode" {
		t.Errorf("Spec #5 after restart = %v, want still linked to its Idea (state.json)", spec)
	}
	sessions := objects(t, restarted.state(rc), "sessions")
	if len(sessions) != 1 || sessions[0]["idea"] != "Dark mode" || asJSON(sessions[0]["link"]) != `{"feature":"5"}` {
		t.Errorf("sessions after restart = %v, want the Idea Session linked to Spec 5", sessions)
	}
}

func TestAnIdeaStartedWhileTheTrackerCantBeReadLearnsItsSpecsLater(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{{Number: 1, Title: "Spec: Login", State: "OPEN", Labels: ready}}, nil, false)
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "agent": "claude", "idea": "Dark mode"})
	waitEnded(t, tab, id)

	// Logged in again: Spec #1 was there all along, so it's no suggestion.
	fakeGh(t, []ghIssue{{Number: 1, Title: "Spec: Login", State: "OPEN", Labels: ready}}, nil, true)
	if got := asJSON(ideaByName(t, boardRefresh(t, f, c, repo), "Dark mode")["suggest"]); got != "[]" {
		t.Errorf("suggest on the first board read = %s, want none", got)
	}
	fakeGh(t, []ghIssue{
		{Number: 1, Title: "Spec: Login", State: "OPEN", Labels: ready},
		{Number: 5, Title: "Spec: Dark mode", State: "OPEN", Labels: ready},
	}, nil, true)
	if got := asJSON(ideaByName(t, boardRefresh(t, f, c, repo), "Dark mode")["suggest"]); got != `[{"id":"5","ref":"#5","title":"Spec: Dark mode"}]` {
		t.Errorf("suggest = %s, want Spec #5 alone", got)
	}

	// Closing the Session drops its Idea.
	if status, body := f.post(c, "/api/sessions/"+id+"/close", map[string]any{"confirm": true}); status != http.StatusNoContent {
		t.Fatalf("close: status %d (%v)", status, body)
	}
	if b := f.board(c, repo); len(objects(t, b, "ideas")) != 0 {
		t.Errorf("ideas after Close = %v, want none", b["ideas"])
	}
}

func TestASpecLinkedToOneIdeaIsNotSuggestedToAnother(t *testing.T) {
	t.Parallel()
	repo := realPath(t, newRepo(t, t.TempDir(), "notes"))
	scratch(t, repo, "login/spec.md", "# Login\n")
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)
	dark := f.startSession(c, map[string]any{"project": repo, "agent": "claude", "idea": "Dark mode"})
	waitEnded(t, tab, dark)
	search := f.startSession(c, map[string]any{"project": repo, "agent": "claude", "idea": "Search"})
	waitEnded(t, tab, search)

	scratch(t, repo, "dark-mode/spec.md", "# Dark mode\n")
	b := f.board(c, repo)
	want := `[{"id":"dark-mode","ref":".scratch/dark-mode/spec.md","title":"Dark mode"}]`
	for _, name := range []string{"Dark mode", "Search"} {
		if got := asJSON(ideaByName(t, b, name)["suggest"]); got != want {
			t.Errorf("%s suggests %s, want the new dark-mode Spec", name, got)
		}
	}

	if status, body := f.post(c, "/api/sessions/"+dark+"/link-spec", map[string]any{"spec": "dark-mode"}); status != http.StatusNoContent {
		t.Fatalf("link: status %d (%v)", status, body)
	}
	b = f.board(c, repo)
	if got := asJSON(ideaByName(t, b, "Search")["suggest"]); got != "[]" {
		t.Errorf("Search suggests %s after Dark mode took the Spec, want none", got)
	}
	if got := asJSON(featureByID(t, b, "dark-mode")["tickets"]); got != "[]" {
		t.Errorf("dark-mode tickets = %s, want [] for a Spec without issues/", got)
	}
}

func TestIdeaRequestsFhRefuses(t *testing.T) {
	t.Parallel()
	repo := realPath(t, newRepo(t, t.TempDir(), "notes"))
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)

	for name, req := range map[string]map[string]any{
		"no letters": {"idea": "!!!"},
		"a branch":   {"idea": "Dark mode", "branch": "dark"},
		"a Worktree": {"idea": "Dark mode", "worktree": repo},
		"a link":     {"idea": "Dark mode", "link": map[string]any{"feature": "1"}},
	} {
		req["project"], req["agent"] = repo, "claude"
		if status, body := f.post(c, "/api/sessions", req); status != http.StatusBadRequest {
			t.Errorf("Idea with %s: status %d (%v), want 400", name, status, body)
		}
	}

	plain := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude"})
	waitState(t, tab, plain, "idle")
	if status, body := f.post(c, "/api/sessions/"+plain+"/link-spec", map[string]any{"spec": "x"}); status != http.StatusConflict {
		t.Errorf("link a Session that's no Idea: status %d (%v), want 409", status, body)
	}
	idea := f.startSession(c, map[string]any{"project": repo, "agent": "claude", "idea": "Dark mode"})
	waitEnded(t, tab, idea)
	if status, body := f.post(c, "/api/sessions/"+idea+"/link-spec", map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("link without a Spec: status %d (%v), want 400", status, body)
	}
	if status, body := f.post(c, "/api/sessions/"+idea+"/link-spec", map[string]any{"spec": "x"}); status != http.StatusNoContent {
		t.Fatalf("link: status %d (%v)", status, body)
	}
	if status, body := f.post(c, "/api/sessions/"+idea+"/link-spec", map[string]any{"spec": "y"}); status != http.StatusConflict {
		t.Errorf("link twice: status %d (%v), want 409", status, body)
	}
}
