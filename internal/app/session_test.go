package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/philipsahli/fullharness/internal/app"
)

// script is the fake agent's FH_FAKE_SCRIPT (see internal/acp/fakeagent).
type script map[string]any

// startFhWithAgent starts fh whose "claude" Agent is the fake agent running
// s, the way a user overrides an Agent in config.toml.
func startFhWithAgent(t *testing.T, s script) *fh {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, fmt.Sprintf("[agents.claude]\ncommand = [%q]\nenv = { FH_FAKE_SCRIPT = %q }\n", fakeAgentBin, raw))
	return startFhWith(t, appOptions(dir))
}

// appOptions starts fh on a free port with periodic refresh effectively off,
// so tests see only the refreshes they cause. The fake Agent starts in
// milliseconds, but a loaded machine can take seconds to run it (#220), so
// its start gets 30 s: well inside waitBudget, so a start that never ends
// still fails the test that waits on it.
func appOptions(dir string) app.Options {
	return app.Options{Dir: dir, Port: new(int), RefreshInterval: time.Hour, AgentStartTimeout: 30 * time.Second}
}

// startSession posts a New Session and returns the new Session's ID.
func (f *fh) startSession(c *http.Client, req map[string]any) string {
	f.t.Helper()
	status, body := f.post(c, "/api/sessions", req)
	if status != http.StatusCreated {
		f.t.Fatalf("start session: status %d (%v)", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		f.t.Fatalf("start session: no id in %v", body)
	}
	return id
}

// waitState reads events from tab until Session id reaches state, and
// returns that Session summary.
func waitState(t *testing.T, tab *websocket.Conn, id, state string) map[string]any {
	t.Helper()
	return waitFor(t, tab, id, state, func(s map[string]any) bool { return s["state"] == state })
}

// waitEnded waits until Session id's turn has ended: Waiting for you to look.
func waitEnded(t *testing.T, tab *websocket.Conn, id string) map[string]any {
	t.Helper()
	return waitFor(t, tab, id, "turn ended", func(s map[string]any) bool {
		return s["state"] == "waiting" && s["waiting"] == "turn"
	})
}

// waitFor reads events from tab until a summary of Session id matches ok.
func waitFor(t *testing.T, tab *websocket.Conn, id, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] != "session" {
			continue
		}
		s, _ := ev["session"].(map[string]any)
		if s["id"] == id && ok(s) {
			return s
		}
	}
	t.Fatalf("session %s never reached %s%s", id, what, lateTab(tab, func(snap map[string]any) bool {
		for _, s := range objects(t, snap, "sessions") {
			if s["id"] == id && ok(s) {
				return true
			}
		}
		return false
	}))
	return nil
}

// waitTurn waits until Session id has worked on a turn and the turn ended.
func waitTurn(t *testing.T, tab *websocket.Conn, id string) map[string]any {
	t.Helper()
	waitState(t, tab, id, "working")
	return waitEnded(t, tab, id)
}

// sessionDetail returns GET /api/sessions/{id}.
func (f *fh) sessionDetail(c *http.Client, id string) map[string]any {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/sessions/" + id)
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET session %s: status %d: %s", id, resp.StatusCode, body)
	}
	return decodeObject(f.t, body)
}

// transcript returns a Session's conversation items.
func (f *fh) transcript(c *http.Client, id string) []map[string]any {
	f.t.Helper()
	return objects(f.t, f.sessionDetail(c, id), "transcript")
}

func TestStartSessionRunsTheStartingPromptAndShowsTheReply(t *testing.T) {
	t.Parallel()
	f := startFhWithAgent(t, script{"prompts": map[string]any{
		"hello": []any{map[string]any{"say": "Hi "}, map[string]any{"say": "there"}},
	}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "hello"})

	waitState(t, tab, id, "working")
	s := waitEnded(t, tab, id)
	if s["project"] != repo || s["worktree"] != repo || s["agent"] != "claude" || s["title"] != "main" {
		t.Errorf("summary = %v, want project/worktree %s, agent claude, title main", s, repo)
	}
	items := f.transcript(c, id)
	if len(items) != 2 ||
		items[0]["type"] != "user" || items[0]["text"] != "hello" ||
		items[1]["type"] != "agent" || items[1]["text"] != "Hi there" {
		t.Fatalf("transcript = %v, want user hello then agent \"Hi there\"", items)
	}
}
