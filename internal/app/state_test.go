package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// viewing tells fh whether tab is looking at Session id (its conversation
// is open in a visible tab), as the UI does every 15 s while it is.
func (f *fh) viewing(c *http.Client, id, tab string, on bool) int {
	f.t.Helper()
	status, _ := f.post(c, "/api/sessions/"+id+"/viewing", map[string]any{"tab": tab, "viewing": on})
	return status
}

func TestAFinishedTurnWaitsUntilYouLook(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	waitEnded(t, tab, id)
	if status := f.viewing(c, id, "tab-1", true); status != http.StatusNoContent {
		t.Fatalf("viewing: status %d, want 204", status)
	}

	waitState(t, tab, id, "idle")
}

func TestLookingDoesNotClearAWaitingPermissionRequest(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, _ := askAndWait(t, f)

	f.viewing(c, id, "tab-1", true)

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

func TestATurnThatEndsWhileYouLookGoesStraightToIdle(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.viewing(c, id, "tab-1", true)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})
	waitState(t, tab, id, "working")

	s := waitFor(t, tab, id, "the end of the turn", func(s map[string]any) bool { return s["state"] != "working" })

	if s["state"] != "idle" {
		t.Errorf("state after the turn = %v (%v), want idle with no Waiting in between", s["state"], s["waiting"])
	}
}

func TestAViewerThatLeftNoLongerCounts(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.viewing(c, id, "tab-1", true)
	f.viewing(c, id, "tab-1", false)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	waitEnded(t, tab, id)
}

func TestSendingFromAFinishedTurnStartsTheNextOne(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "one"})
	waitEnded(t, tab, id)

	if status, _ := f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "two"}); status != http.StatusAccepted {
		t.Fatalf("prompt from Waiting(turn): status %d, want 202", status)
	}
	waitTurn(t, tab, id)
}

func TestACancelEndsIdleEvenIfTheAgentSaysEndTurn(t *testing.T) {
	f := startFhWithAgent(t, script{"endAfterCancel": true, "default": []any{map[string]any{"waitCancel": true}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})
	waitState(t, tab, id, "working")

	f.post(c, "/api/sessions/"+id+"/cancel", nil)

	if s := waitFor(t, tab, id, "the end of the turn", func(s map[string]any) bool { return s["state"] != "working" }); s["state"] != "idle" {
		t.Errorf("state = %v, want idle", s["state"])
	}
}

func TestLoginNeededAtInitialize(t *testing.T) {
	f := startFhWithAgent(t, script{"initAuthError": true})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude"})

	if s := waitState(t, tab, id, "exited"); s["reason"] != "login needed" {
		t.Errorf("reason = %v, want login needed", s["reason"])
	}
}

func TestAReconnectedTabGetsCurrentStatesInItsSnapshot(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})
	waitEnded(t, tab, id)

	conn, status, err := f.dialWS(c, f.origin())
	if err != nil {
		t.Fatalf("reconnect: status %d: %v", status, err)
	}
	snap := readEvent(t, conn)

	sessions := objects(t, snap, "sessions")
	if len(sessions) != 1 || sessions[0]["state"] != "waiting" || sessions[0]["waiting"] != "turn" || sessions[0]["agentName"] != "Claude Code" {
		t.Errorf("snapshot sessions = %v, want the Session waiting for its turn, with its Agent's name", sessions)
	}
}
