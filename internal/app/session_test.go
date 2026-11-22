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
// so tests see only the refreshes they cause.
func appOptions(dir string) app.Options {
	return app.Options{Dir: dir, Port: new(int), RefreshInterval: time.Hour}
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
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] != "session" {
			continue
		}
		s, _ := ev["session"].(map[string]any)
		if s["id"] == id && s["state"] == state {
			return s
		}
	}
	t.Fatalf("session %s never reached %s", id, state)
	return nil
}

// waitTurn waits until Session id has worked on a turn and is Idle again.
func waitTurn(t *testing.T, tab *websocket.Conn, id string) map[string]any {
	t.Helper()
	waitState(t, tab, id, "working")
	return waitState(t, tab, id, "idle")
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
	f := startFhWithAgent(t, script{"prompts": map[string]any{
		"hello": []any{map[string]any{"say": "Hi "}, map[string]any{"say": "there"}},
	}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "hello"})

	waitState(t, tab, id, "working")
	s := waitState(t, tab, id, "idle")
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
