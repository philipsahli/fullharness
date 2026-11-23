package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fhWorktree has fh create a Worktree on branch (through a Session it then
// closes), so it is one of fh's own and not an Agent worktree.
func fhWorktree(t *testing.T, f *fh, c *http.Client, repo, branch string) string {
	t.Helper()
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "branch": branch, "agent": "claude"})
	waitState(t, tab, id, "idle")
	if status, body := f.post(c, "/api/sessions/"+id+"/close", map[string]any{"confirm": true}); status != http.StatusNoContent {
		t.Fatalf("close session: status %d (%v)", status, body)
	}
	return filepath.Join(filepath.Dir(repo), filepath.Base(repo)+".worktrees", branch)
}

// removeWorktree posts a removal and returns the status and error message.
func (f *fh) removeWorktree(c *http.Client, repo, path string, confirm bool) (int, string) {
	f.t.Helper()
	status, body := f.post(c, "/api/worktrees/remove", map[string]any{"project": repo, "path": path, "confirm": confirm})
	msg, _ := body["error"].(string)
	return status, msg
}

// mergedWorktree is an fh Worktree whose one commit main has merged.
func mergedWorktree(t *testing.T, f *fh, c *http.Client, repo string) string {
	t.Helper()
	wt := fhWorktree(t, f, c, repo, "done")
	commitFile(t, wt, "done.txt", "1\n")
	git(t, repo, "merge", "-q", "--ff-only", "done")
	return wt
}

func TestAMergedCleanWorktreeIsRemovedAfterConfirmation(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := mergedWorktree(t, f, c, repo)
	f.post(c, "/api/projects/refresh", nil)
	if w := worktreeAt(t, projects(t, f.state(c))[0], wt); w["merged"] != true {
		t.Fatalf("merged = %v, want true", w["merged"])
	}

	if status, msg := f.removeWorktree(c, repo, wt, false); status != http.StatusConflict || !strings.Contains(msg, "confirm") {
		t.Fatalf("unconfirmed: status %d %q, want 409 asking to confirm", status, msg)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("unconfirmed removal touched the Worktree: %v", err)
	}

	if status, msg := f.removeWorktree(c, repo, wt, true); status != http.StatusNoContent {
		t.Fatalf("confirmed: status %d %q, want 204", status, msg)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("folder still there: %v", err)
	}
	if n := len(worktrees(t, projects(t, f.state(c))[0])); n != 1 {
		t.Errorf("got %d worktrees after removal, want only the main tree", n)
	}
}

func TestAWorktreeWithAnUnmergedBranchIsNotRemoved(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "wip")
	commitFile(t, wt, "wip.txt", "1\n")

	status, msg := f.removeWorktree(c, repo, wt, true)

	if status != http.StatusConflict || msg != "its branch isn't merged into main" {
		t.Errorf("status %d %q, want 409 saying the branch isn't merged", status, msg)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("Worktree touched: %v", err)
	}
}

func TestASquashMergedBranchCountsAsMerged(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "squashed")
	commitFile(t, wt, "a.txt", "1\n")
	commitFile(t, wt, "b.txt", "2\n")
	commitFile(t, repo, "other.txt", "main moved on\n")
	git(t, repo, "merge", "-q", "--squash", "squashed")
	git(t, repo, "commit", "-q", "-m", "squashed (#1)")

	f.post(c, "/api/projects/refresh", nil)
	w := worktreeAt(t, projects(t, f.state(c))[0], wt)

	if w["merged"] != true || w["ahead"] != float64(2) {
		t.Fatalf("merged %v ahead %v, want true and still 2 ahead", w["merged"], w["ahead"])
	}
	if status, msg := f.removeWorktree(c, repo, wt, true); status != http.StatusNoContent {
		t.Errorf("status %d %q, want 204", status, msg)
	}
}

func TestAWorktreeWithUncommittedChangesIsNotRemoved(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := mergedWorktree(t, f, c, repo)
	writeFile(t, filepath.Join(wt, "notes.txt"), "keep me\n") // untracked

	status, msg := f.removeWorktree(c, repo, wt, true)

	if status != http.StatusConflict || msg != "it has 1 uncommitted file" {
		t.Errorf("status %d %q, want 409 naming the uncommitted file", status, msg)
	}
	if _, err := os.Stat(filepath.Join(wt, "notes.txt")); err != nil {
		t.Errorf("uncommitted file lost: %v", err)
	}
}

func TestAWorktreeWithALiveSessionIsNotRemoved(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := mergedWorktree(t, f, c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": wt, "agent": "claude"})
	waitState(t, tab, id, "idle")

	status, msg := f.removeWorktree(c, repo, wt, true)

	if status != http.StatusConflict || msg != "a Session is linked to it; close the Session first" {
		t.Errorf("status %d %q, want 409 naming the Session", status, msg)
	}
}

func TestAWorktreeWithAnExitedSessionIsNotRemoved(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"exit": 2}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := mergedWorktree(t, f, c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": wt, "agent": "claude"})
	waitState(t, tab, id, "idle")
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "crash"})
	waitState(t, tab, id, "exited")

	status, msg := f.removeWorktree(c, repo, wt, true)

	if status != http.StatusConflict || msg != "a Session is linked to it; close the Session first" {
		t.Errorf("status %d %q, want 409: an Exited Session still counts", status, msg)
	}
}

func TestAnAgentWorktreeIsNeverRemoved(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	repo := newRepo(t, parent, "shop")
	wt := filepath.Join(parent, "agent")
	git(t, repo, "worktree", "add", "-q", "-b", "agent", wt) // merged, clean, no Session
	f.addProject(c, repo)

	status, msg := f.removeWorktree(c, repo, wt, true)

	if status != http.StatusBadRequest || msg != "fh never removes an Agent worktree" {
		t.Errorf("status %d %q, want 400 refusing an Agent worktree", status, msg)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("Agent worktree touched: %v", err)
	}
}

func TestTheMainTreeIsNeverRemoved(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)

	if status, _ := f.removeWorktree(c, repo, repo, true); status != http.StatusBadRequest {
		t.Errorf("status %d, want 400", status)
	}
}
