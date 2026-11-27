package app_test

import (
	"slices"
	"testing"
	"time"
)

// A Feature moved out of Now while its Autopilot turn runs (#204,
// ADR-0013): the turn finishes, the Project stays held until it ends, and
// then, whatever the turn's verdict, the next Feature in Now starts. The
// left-behind Session stays open.
func TestAFeatureThatLeftNowNoLongerHoldsTheProjectOnceItsTurnEnds(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"asking", "stuck", "unsure", "done", "exited"} {
		t.Run(end, func(t *testing.T) {
			// The turn lasts long enough to move the Feature and look a few
			// times while it runs.
			last, verdict := map[string]any{"say": "Opened a PR."}, end
			if end == "exited" {
				// The Agent exits mid-turn, so no turn end is classified.
				last, verdict = map[string]any{"exit": 3}, "done"
			}
			f := startAutopilotFh(t, script{"default": []any{map[string]any{"sleepMs": 1500}, last}}, verdict)
			c := f.authorisedClient()
			repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
			scratch(t, repo, "login/spec.md", nowSpec("Login"))
			scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
			scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** later\n")
			scratch(t, repo, "search/issues/01-box.md", ticket("01", "Search box", "ready-for-agent", "None"))
			f.addProject(c, repo)
			f.setAutopilot(c, repo, true)
			id := f.waitAutopilotSessions(c, 1)[0]["id"].(string)

			// The user moves Login to Next and Search to Now mid-turn.
			scratch(t, repo, "login/spec.md", "# Login\n\n**Roadmap:** next\n")
			scratch(t, repo, "search/spec.md", nowSpec("Search"))

			// While Login's turn runs, the Project stays held: no second
			// Autopilot Session, though Search is in Now.
			time.Sleep(400 * time.Millisecond)
			running := f.autopilotSessions(c)
			if len(running) != 1 {
				t.Fatalf("%d Autopilot Sessions while Login's turn runs, want 1", len(running))
			}
			if st := running[0]["state"]; st != "starting" && st != "working" {
				t.Fatalf("Login's turn already ended (%v), so the check above proves nothing", running[0]["state"])
			}

			// Once it ends, Search starts. Under make stress a 1.5 s turn
			// took 6 s, and Search's start then adds a Worktree and an
			// Agent, so the wait is long; a hold that is never released
			// still fails here. On a timeout the history says whether
			// Login's turn end was classified.
			deadline := time.Now().Add(30 * time.Second)
			for len(f.autopilotSessions(c)) < 2 {
				if time.Now().After(deadline) {
					t.Fatalf("Autopilot never started Search; Sessions %v\nhistory %v",
						f.autopilotSessions(c), f.autopilotFeatures(c, repo))
				}
				time.Sleep(20 * time.Millisecond)
			}
			for _, s := range f.autopilotSessions(c) {
				link, _ := s["link"].(map[string]any)
				want := "search"
				if s["id"] == id {
					want = "login"
				}
				if link["feature"] != want {
					t.Errorf("Session %v linked to %v, want %s", s["id"], link, want)
				}
			}
			if !slices.ContainsFunc(f.autopilotSessions(c), func(s map[string]any) bool { return s["id"] == id }) {
				t.Error("the left-behind Session was closed; it stays open for the user")
			}
		})
	}
}
