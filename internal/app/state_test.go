package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func (f *fh) seen(c *http.Client, id string) int {
	f.t.Helper()
	status, _ := f.post(c, "/api/sessions/"+id+"/seen", nil)
	return status
}

func TestAFinishedTurnWaitsUntilYouLook(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	waitEnded(t, tab, id)
	if status := f.seen(c, id); status != http.StatusNoContent {
		t.Fatalf("seen: status %d, want 204", status)
	}

	waitState(t, tab, id, "idle")
}

func TestLookingDoesNotClearAWaitingPermissionRequest(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, _ := askAndWait(t, f)

	f.seen(c, id)

	if s := f.sessionDetail(c, id); s["state"] != "waiting" || s["waiting"] != "permission" {
		t.Errorf("state %v waiting %v, want waiting for permission", s["state"], s["waiting"])
	}
}

func TestACancelledTurnIsIdleNotWaiting(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"waitCancel": true}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})
	waitState(t, tab, id, "working")

	f.post(c, "/api/sessions/"+id+"/cancel", nil)

	if s := waitFor(t, tab, id, "the end of the turn", func(s map[string]any) bool { return s["state"] != "working" }); s["state"] != "idle" {
		t.Errorf("state after cancel = %v, want idle", s["state"])
	}
}

func TestTurnsEndingByLimitOrErrorAlsoWait(t *testing.T) {
	for name, step := range map[string]map[string]any{
		"max_tokens": {"stop": "max_tokens"},
		"refusal":    {"stop": "refusal"},
		"error":      {"fail": "model overloaded"},
	} {
		t.Run(name, func(t *testing.T) {
			f := startFhWithAgent(t, script{"default": []any{step}})
			c, _, id, _ := newSessionIn(t, f, "")
			tab := f.connectTab(c)
			f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

			waitEnded(t, tab, id)
		})
	}
}

func TestAnAgentThatNeedsALoginExitsWithTheLoginCommand(t *testing.T) {
	for name, s := range map[string]script{
		"at start":  {"onStart": []any{map[string]any{"authError": true}}},
		"in a turn": {"default": []any{map[string]any{"authError": true}}},
	} {
		t.Run(name, func(t *testing.T) {
			f := startFhWithAgent(t, s)
			c := f.authorisedClient()
			repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
			f.addProject(c, repo)
			tab := f.connectTab(c)
			id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "go"})

			sum := waitState(t, tab, id, "exited")

			if sum["reason"] != "login needed" || sum["login"] != "claude auth login" {
				t.Errorf("reason %v login %v, want login needed with claude auth login", sum["reason"], sum["login"])
			}
		})
	}
}

func TestTheSessionListPutsWhatNeedsYouFirst(t *testing.T) {
	f := startFhWithAgent(t, script{
		"prompts": map[string]any{
			"wait":  []any{map[string]any{"say": "done"}},
			"work":  []any{map[string]any{"waitCancel": true}},
			"crash": []any{map[string]any{"exit": 2}},
		},
	})
	c := f.authorisedClient()
	tab := f.connectTab(c)
	start := func(name, prompt string) string {
		repo := realPath(t, newRepo(t, t.TempDir(), name))
		f.addProject(c, repo)
		req := map[string]any{"project": repo, "worktree": repo, "agent": "claude"}
		if prompt != "" {
			req["prompt"] = prompt
		}
		return f.startSession(c, req)
	}
	exited := start("crash", "crash")
	waitState(t, tab, exited, "exited")
	idle := start("idle", "")
	waitState(t, tab, idle, "idle")
	working := start("work", "work")
	waitState(t, tab, working, "working")
	waitedLong := start("wait1", "wait")
	waitEnded(t, tab, waitedLong)
	time.Sleep(10 * time.Millisecond) // distinct "since" times
	waitedShort := start("wait2", "wait")
	waitEnded(t, tab, waitedShort)

	var order []string
	for _, s := range objects(t, f.state(c), "sessions") {
		order = append(order, s["id"].(string))
	}

	want := []string{waitedLong, waitedShort, working, idle, exited}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order %v, want waiting (longest first), working, idle, exited: %v", order, want)
	}
}
