package app_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newSessionIn adds a fresh repo as a Project, connects a tab and starts a
// Session in its main tree. Without a starting prompt it waits until the
// Session is Idle, ready for one. It returns the client, a function reading
// the tab's next event, the Session ID and the repo.
func newSessionIn(t *testing.T, f *fh, prompt string) (c *http.Client, tab func() map[string]any, id, repo string) {
	t.Helper()
	c = f.authorisedClient()
	repo = realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	conn := f.connectTab(c)
	req := map[string]any{"project": repo, "worktree": repo, "agent": "claude"}
	if prompt != "" {
		req["prompt"] = prompt
	}
	id = f.startSession(c, req)
	if prompt == "" {
		waitState(t, conn, id, "idle")
	}
	return c, func() map[string]any { return nextEvent(t, conn, 10*time.Second) }, id, repo
}

func TestAgentRepliesStreamToTabsAsChunks(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "one "}, map[string]any{"say": "two"}}})
	_, next, id, _ := newSessionIn(t, f, "go")

	var streamed []string
	for ev := next(); ev != nil; ev = next() {
		switch ev["type"] {
		case "item":
			if it := ev["item"].(map[string]any); it["type"] == "agent" {
				streamed = append(streamed, it["text"].(string))
			}
		case "chunk":
			streamed = append(streamed, ev["text"].(string))
		case "session":
			if s := ev["session"].(map[string]any); s["id"] == id && s["state"] == "idle" && len(streamed) > 0 {
				if got := strings.Join(streamed, ""); got != "one two" {
					t.Errorf("streamed %q, want %q", got, "one two")
				}
				return
			}
		}
	}
	t.Fatal("turn never ended")
}

func TestToolCallsShowStatusAndOutput(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{
		map[string]any{"tool": map[string]any{"id": "t1", "title": "Terminal", "kind": "execute", "status": "pending"}},
		map[string]any{"toolUpdate": map[string]any{"id": "t1", "title": "ls", "text": "a.txt"}},
		map[string]any{"toolUpdate": map[string]any{"id": "t1", "status": "completed", "rawOutput": "a.txt\n"}},
	}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "list"})
	waitTurn(t, tab, id)

	var tool map[string]any
	for _, it := range f.transcript(c, id) {
		if it["type"] == "tool" {
			tool = it["tool"].(map[string]any)
		}
	}
	if tool == nil || tool["title"] != "ls" || tool["kind"] != "execute" || tool["status"] != "completed" ||
		tool["content"] != "a.txt" || tool["rawOutput"] != "a.txt\n" {
		t.Errorf("tool card = %v", tool)
	}
}

func TestSendingWhileWorkingIsRefusedAndCancelEndsTheTurn(t *testing.T) {
	f := startFhWithAgent(t, script{"prompts": map[string]any{"long": []any{map[string]any{"waitCancel": true}}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "long"})
	waitState(t, tab, id, "working")

	if status, _ := f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"}); status != http.StatusConflict {
		t.Errorf("prompt while working: status %d, want 409", status)
	}
	if status, _ := f.post(c, "/api/sessions/"+id+"/cancel", nil); status != http.StatusAccepted {
		t.Fatalf("cancel: status %d, want 202", status)
	}
	waitState(t, tab, id, "idle")

	items := f.transcript(c, id)
	if last := items[len(items)-1]; last["type"] != "notice" || last["text"] != "Turn cancelled." {
		t.Errorf("last item = %v, want the cancelled notice", last)
	}
}

func TestOnlyOneSessionPerWorktree(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	req := map[string]any{"project": repo, "worktree": repo, "agent": "claude"}
	f.startSession(c, req)

	if status, _ := f.post(c, "/api/sessions", req); status != http.StatusConflict {
		t.Fatalf("second Session in the same Worktree: status %d, want 409", status)
	}
}

func TestNoSessionInAnAgentWorktree(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	repo := newRepo(t, parent, "shop")
	agentWT := filepath.Join(parent, "agent-made")
	git(t, repo, "worktree", "add", "-q", "-b", "agent-made", agentWT)
	f.addProject(c, repo)

	status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "worktree": agentWT, "agent": "claude"})

	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "Agent worktree") {
		t.Fatalf("status %d (%v), want 400 naming the Agent worktree", status, body)
	}
}

func TestNewWorktreeIsCreatedNextToTheProjectAndIsNotAnAgentWorktree(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	origin := newRepo(t, parent, "origin")
	repo := filepath.Join(parent, "shop")
	git(t, parent, "clone", "-q", origin, repo)
	commitFile(t, repo, "local-only.txt", "not pushed\n") // the new branch must not include this
	f.addProject(c, repo)

	status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "branch": "feature/login", "agent": "claude"})
	if status != http.StatusCreated {
		t.Fatalf("status %d (%v), want 201", status, body)
	}

	want := filepath.Join(parent, "shop.worktrees", "feature", "login")
	if body["worktree"] != want || body["title"] != "feature/login" {
		t.Errorf("session = %v, want worktree %s titled feature/login", body, want)
	}
	if _, err := os.Stat(filepath.Join(want, "local-only.txt")); err == nil {
		t.Error("new Worktree started from the main tree's HEAD, want origin/main")
	}
	w := worktreeAt(t, projects(t, f.state(c))[0], want)
	if w["agent"] != false || w["branch"] != "feature/login" {
		t.Errorf("new Worktree = %v, want branch feature/login, not an Agent worktree", w)
	}
	f.stop()
	restarted := startFhWith(t, f.opts)
	w = worktreeAt(t, projects(t, restarted.state(restarted.authorisedClient()))[0], want)
	if w["agent"] != false {
		t.Errorf("after restart the Worktree fh created is an Agent worktree")
	}
}

func TestNewWorktreeRejectsBadAndExistingBranches(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	git(t, repo, "branch", "taken")
	f.addProject(c, repo)

	for branch, want := range map[string]string{"bad..name": "not a valid branch name", "taken": "already exists"} {
		status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "branch": branch, "agent": "claude"})
		if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), want) {
			t.Errorf("branch %q: status %d (%v), want 400 with %q", branch, status, body, want)
		}
	}
}

func TestPermissionRequestsAreDeniedWithANoticeUntilTheInboxExists(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"permission": map[string]any{"id": "t1", "title": "rm -rf build"}}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "clean"})
	waitTurn(t, tab, id)

	var notice, reply string
	for _, it := range f.transcript(c, id) {
		switch it["type"] {
		case "notice":
			notice = it["text"].(string)
		case "agent":
			reply = it["text"].(string)
		}
	}
	if !strings.Contains(notice, "rm -rf build") || !strings.Contains(notice, "denied") {
		t.Errorf("notice = %q, want it to name the request and say it was denied", notice)
	}
	if reply != "permission=reject" {
		t.Errorf("agent saw %q, want the reject option chosen", reply)
	}
}

func TestACrashedAgentShowsItsLastStderrLines(t *testing.T) {
	var steps []any
	for i := 1; i <= 25; i++ {
		steps = append(steps, map[string]any{"stderr": "line " + string(rune('A'+i-1))})
	}
	steps = append(steps, map[string]any{"exit": 3})
	f := startFhWithAgent(t, script{"default": steps})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "boom"})

	s := waitState(t, tab, id, "exited")

	lines, _ := s["stderr"].([]any)
	if s["reason"] != "Agent crashed" || len(lines) != 20 || lines[19] != "line Y" {
		t.Errorf("reason %v, %d stderr lines ending %v; want Agent crashed, 20, line Y", s["reason"], len(lines), lines)
	}
	info, err := os.Stat(filepath.Join(f.opts.Dir, "logs", id+".log"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("session log: %v, mode %v; want 0600", err, info)
	}
}

func TestStartingFhClaudeSetsDefaultModeAndItsEnvironment(t *testing.T) {
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	writeFile(t, claude, "#!/bin/sh\n")
	if err := os.Chmod(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-not-reach-claude")
	f := startFhWithAgent(t, script{"default": []any{
		map[string]any{"sayMode": true}, map[string]any{"say": " "},
		map[string]any{"sayEnv": "ANTHROPIC_API_KEY"}, map[string]any{"say": " "},
		map[string]any{"sayEnv": "CLAUDE_CODE_EXECUTABLE"},
	}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "env"})
	waitTurn(t, tab, id)

	items := f.transcript(c, id)
	want := "mode=default ANTHROPIC_API_KEY= CLAUDE_CODE_EXECUTABLE=" + claude
	if got := items[len(items)-1]["text"]; got != want {
		t.Errorf("agent saw %q, want %q", got, want)
	}
}

func TestTheAgentsTitleReplacesTheBranchName(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"title": "Fix the login page"}}})
	_, next, id, _ := newSessionIn(t, f, "title please")

	for ev := next(); ev != nil; ev = next() {
		if s, ok := ev["session"].(map[string]any); ok && s["id"] == id && s["title"] == "Fix the login page" {
			return
		}
	}
	t.Fatal("title never changed")
}

func TestATurnEndRefreshesTheWorktrees(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"writeFile": "made-by-agent.txt"}}})
	_, next, _, repo := newSessionIn(t, f, "write")

	for ev := next(); ev != nil; ev = next() {
		if ev["type"] == "projects" && worktreeAt(t, projects(t, ev)[0], repo)["uncommitted"] == float64(1) {
			return // well before the 1 h refresh interval
		}
	}
	t.Fatal("no projects event showing the Agent's new file")
}

func TestQuittingFhStopsItsAgents(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"sayPid": true}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "pid"})
	waitTurn(t, tab, id)
	items := f.transcript(c, id)
	pid := strings.TrimPrefix(items[len(items)-1]["text"].(string), "pid=")

	f.stop()

	if out, err := exec.Command("kill", "-0", pid).CombinedOutput(); err == nil {
		t.Errorf("Agent process %s still runs after fh quit (%s)", pid, out)
	}
}
