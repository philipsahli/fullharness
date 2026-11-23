package app_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var history = []any{map[string]any{"user": "fix the login"}, map[string]any{"agent": "Done, login works."}}

// sessionAfterRestart starts fh with s, runs a Session to its end, stops fh
// and starts it again on the same folder. It returns the restarted fh, a
// client, a tab and the Session's ID.
func sessionAfterRestart(t *testing.T, s script, req map[string]any) (*fh, *http.Client, string, string) {
	t.Helper()
	f := startFhWithAgent(t, s)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	base := map[string]any{"project": repo, "worktree": repo, "agent": "claude"}
	for k, v := range req {
		base[k] = v
	}
	id := f.startSession(c, base)
	waitState(t, tab, id, "idle")
	f.stop()
	restarted := startFhWith(t, f.opts)
	return restarted, restarted.authorisedClient(), id, repo
}

func TestSessionsSurviveARestartAsExitedFhQuit(t *testing.T) {
	f, c, id, repo := sessionAfterRestart(t, script{}, nil)

	sessions := objects(t, f.state(c), "sessions")

	if len(sessions) != 1 || sessions[0]["id"] != id || sessions[0]["state"] != "exited" ||
		sessions[0]["reason"] != "fh quit" || sessions[0]["worktree"] != repo || sessions[0]["agentName"] != "Claude Code" ||
		sessions[0]["canReopen"] != true {
		t.Fatalf("sessions after restart = %v, want Session %s Exited, fh quit, in %s", sessions, id, repo)
	}
	info, err := os.Stat(filepath.Join(f.opts.Dir, "state.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state.json: %v %v, want mode 0600", err, info)
	}
}

func TestReopenLoadsTheSessionWithItsHistory(t *testing.T) {
	f, c, id, repo := sessionAfterRestart(t, script{"history": history, "default": []any{map[string]any{"sayMode": true}}}, map[string]any{"autoApprove": true})
	tab := f.connectTab(c)

	if status, body := f.post(c, "/api/sessions/"+id+"/reopen", nil); status != http.StatusAccepted {
		t.Fatalf("reopen: status %d (%v), want 202", status, body)
	}
	s := waitState(t, tab, id, "idle")

	if s["worktree"] != repo || s["agent"] != "claude" || s["autoApprove"] != true {
		t.Errorf("reopened Session = %v, want the same Worktree, Agent and auto-approve", s)
	}
	items := f.transcript(c, id)
	if len(items) != 2 || items[0]["type"] != "user" || items[0]["text"] != "fix the login" ||
		items[1]["type"] != "agent" || items[1]["text"] != "Done, login works." {
		t.Fatalf("transcript after reopen = %v, want the replayed history", items)
	}
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "mode?"})
	waitTurn(t, tab, id)
	if got := lastAgentText(t, f, c, id); got != "mode=auto" {
		t.Errorf("after reopen the Agent's mode is %q, want auto (auto-approve kept)", got)
	}
}

func TestReopenKeepsTheModeAndModelChosenInTheSession(t *testing.T) {
	f := startFhWithAgent(t, script{"models": true, "default": []any{
		map[string]any{"sayMode": true}, map[string]any{"say": ";"}, map[string]any{"sayConfig": "model"},
	}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/mode", map[string]string{"mode": "plan"})
	f.post(c, "/api/sessions/"+id+"/option", map[string]string{"option": "model", "value": "opus"})
	waitFor(t, tab, id, "plan and opus", func(s map[string]any) bool {
		return s["mode"] == "plan" && option(t, s, "model")["value"] == "opus"
	})
	f.stop()
	f = startFhWith(t, f.opts)
	c = f.authorisedClient()
	tab = f.connectTab(c)

	if status, body := f.post(c, "/api/sessions/"+id+"/reopen", nil); status != http.StatusAccepted {
		t.Fatalf("reopen: status %d (%v), want 202", status, body)
	}
	s := waitState(t, tab, id, "idle")

	if s["mode"] != "plan" || option(t, s, "model")["value"] != "opus" {
		t.Errorf("reopened Session shows mode %v, model %v; want plan and opus", s["mode"], option(t, s, "model")["value"])
	}
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "which?"})
	waitTurn(t, tab, id)
	if got := lastAgentText(t, f, c, id); got != "mode=plan;model=opus" {
		t.Errorf("the reopened Agent runs with %q, want mode=plan;model=opus", got)
	}
}

func TestReopenIsRefusedWhenTheWorktreeHasANewSession(t *testing.T) {
	f, c, id, repo := sessionAfterRestart(t, script{}, nil)
	f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude"})

	if status, _ := f.post(c, "/api/sessions/"+id+"/reopen", nil); status != http.StatusConflict {
		t.Fatalf("reopen: status %d, want 409", status)
	}
}

func TestReopenIsRefusedForAnAgentThatCantLoadSessions(t *testing.T) {
	f, c, id, _ := sessionAfterRestart(t, script{"noLoad": true}, nil)

	status, body := f.post(c, "/api/sessions/"+id+"/reopen", nil)

	if status != http.StatusConflict || !strings.Contains(body["error"].(string), "can't reopen") {
		t.Fatalf("reopen: status %d (%v), want 409 saying the Agent can't reopen", status, body)
	}
}

func TestCloseRemovesTheSessionFreesItsWorktreeAndDeletesItsLog(t *testing.T) {
	f, c, id, repo := sessionAfterRestart(t, script{}, nil)

	if status, _ := f.post(c, "/api/sessions/"+id+"/close", nil); status != http.StatusNoContent {
		t.Fatalf("close: status %d, want 204", status)
	}

	if sessions := objects(t, f.state(c), "sessions"); len(sessions) != 0 {
		t.Errorf("sessions after close = %v, want none", sessions)
	}
	if _, err := os.Stat(filepath.Join(f.opts.Dir, "logs", id+".log")); !os.IsNotExist(err) {
		t.Errorf("Session log still there: %v", err)
	}
	f.stop()
	again := startFhWith(t, f.opts)
	if sessions := objects(t, again.state(again.authorisedClient()), "sessions"); len(sessions) != 0 {
		t.Errorf("closed Session came back after restart: %v", sessions)
	}
	again.startSession(again.authorisedClient(), map[string]any{"project": repo, "worktree": repo, "agent": "claude"})
}

func TestClosingALiveSessionNeedsConfirmationAndStopsItsAgent(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"waitCancel": true}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})
	waitState(t, tab, id, "working")

	if status, body := f.post(c, "/api/sessions/"+id+"/close", nil); status != http.StatusConflict || !strings.Contains(body["error"].(string), "confirm") {
		t.Fatalf("close without confirm: status %d (%v), want 409 asking to confirm", status, body)
	}
	if status, _ := f.post(c, "/api/sessions/"+id+"/close", map[string]any{"confirm": true}); status != http.StatusNoContent {
		t.Fatalf("close with confirm: status %d, want 204", status)
	}

	for {
		ev := nextEvent(t, tab, 5_000_000_000)
		if ev == nil {
			t.Fatal("no closed event")
		}
		if ev["type"] == "closed" && ev["session"] == id {
			break
		}
	}
	if sessions := objects(t, f.state(c), "sessions"); len(sessions) != 0 {
		t.Errorf("sessions = %v, want none", sessions)
	}
}

func TestALoginNeededSessionCanBeReopened(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"authError": true}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "go"})
	if s := waitState(t, tab, id, "exited"); s["reason"] != "login needed" {
		t.Fatalf("reason = %v, want login needed", s["reason"])
	}

	if status, _ := f.post(c, "/api/sessions/"+id+"/reopen", nil); status != http.StatusAccepted {
		t.Fatalf("reopen after login: status %d, want 202", status)
	}
	waitState(t, tab, id, "idle")
}

func TestStartupRemovesLogsOfSessionsThatAreGone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "logs", "gone1234.log")
	writeFile(t, orphan, "old stderr\n")

	startFhWith(t, appOptions(dir))

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan log still there: %v", err)
	}
}

func TestTwoReopensAtOnceStartOneAgent(t *testing.T) {
	f, c, id, _ := sessionAfterRestart(t, script{}, nil)

	statuses := make(chan int, 2)
	for range 2 {
		go func() {
			status, _ := f.post(c, "/api/sessions/"+id+"/reopen", nil)
			statuses <- status
		}()
	}
	got := []int{<-statuses, <-statuses}

	slices.Sort(got) // 202 sorts before 409
	if got[0] != http.StatusAccepted || got[1] != http.StatusConflict {
		t.Fatalf("statuses %v, want one 202 and one 409", got)
	}
}

func TestALoginNeededAtStartCanBeReopenedAsANewSession(t *testing.T) {
	f := startFhWithAgent(t, script{"initAuthError": true})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude"})
	if s := waitState(t, tab, id, "exited"); s["canReopen"] != true {
		t.Fatalf("login needed at start: canReopen = %v, want true", s["canReopen"])
	}
	// The user logs in; the next start succeeds.
	f.stop()
	dir := f.opts.Dir
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	writeConfig(t, dir, strings.Replace(string(cfg), `\"initAuthError\":true`, `\"initAuthError\":false`, 1))
	f = startFhWith(t, f.opts)
	c = f.authorisedClient()
	tab = f.connectTab(c)

	if status, body := f.post(c, "/api/sessions/"+id+"/reopen", nil); status != http.StatusAccepted {
		t.Fatalf("reopen: status %d (%v), want 202", status, body)
	}
	waitState(t, tab, id, "idle")
}

func TestReopenInTheSameRunTellsTabsToDropTheOldConversation(t *testing.T) {
	f := startFhWithAgent(t, script{"history": history, "default": []any{map[string]any{"exit": 2}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "crash"})
	waitState(t, tab, id, "exited")

	f.post(c, "/api/sessions/"+id+"/reopen", nil)

	for {
		ev := nextEvent(t, tab, 5_000_000_000)
		if ev == nil {
			t.Fatal("no reset event before the replay")
		}
		if ev["type"] == "item" && ev["session"] == id {
			t.Fatal("replayed item arrived before any reset event")
		}
		if ev["type"] == "reset" && ev["session"] == id {
			break
		}
	}
}

func TestAnAgentRemovedFromConfigKeepsItsReopenAfterItComesBack(t *testing.T) {
	f := startFhWithAgents(t, map[string]script{"myagent": {}}, nil)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "myagent"})
	waitState(t, tab, id, "idle")
	f.stop()
	dir := f.opts.Dir
	cfg, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, "")
	gone := startFhWith(t, f.opts)
	if s := objects(t, gone.state(gone.authorisedClient()), "sessions")[0]; s["canReopen"] != false {
		t.Errorf("Agent not configured: canReopen = %v, want false", s["canReopen"])
	}
	gone.stop()
	writeConfig(t, dir, string(cfg))

	back := startFhWith(t, f.opts)

	if s := objects(t, back.state(back.authorisedClient()), "sessions")[0]; s["id"] != id || s["canReopen"] != true {
		t.Errorf("Agent configured again: session %v, want canReopen true", s)
	}
}

func TestASessionsOwnPageSaysItCanBeReopened(t *testing.T) {
	f, c, id, _ := sessionAfterRestart(t, script{}, nil)

	resp, err := c.Get(f.origin() + "/api/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
		t.Fatal(err)
	}

	if d["canReopen"] != true {
		t.Errorf("GET /api/sessions/%s canReopen = %v, want true, as the list says", id, d["canReopen"])
	}
}
