package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	if w := worktreeAt(t, projects(t, f.state(c))[0], wt); w["merge"] != "merged" || w["removal"] != "" {
		t.Fatalf("merge %q removal %q, want merged and no refusal", w["merge"], w["removal"])
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
	if got := git(t, repo, "branch", "--list", "done"); got == "" {
		t.Error("branch done is gone, want it kept")
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
	// A user's diff settings must not change the patch fh compares: an
	// external diff tool that prints nothing, and colour always on.
	git(t, repo, "config", "diff.external", "true")
	git(t, repo, "config", "color.ui", "always")

	f.post(c, "/api/projects/refresh", nil)
	w := worktreeAt(t, projects(t, f.state(c))[0], wt)

	if w["merge"] != "merged" || w["ahead"] != float64(2) {
		t.Fatalf("merge %q ahead %v, want merged and still 2 ahead", w["merge"], w["ahead"])
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
	// The snapshot says so too, without waiting for a refresh tick.
	if w := worktreeAt(t, projects(t, f.state(c))[0], wt); w["removal"] != "a Session is linked to it; close the Session first" {
		t.Errorf("removal = %q, want the Session reason", w["removal"])
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

func TestAWorktreeWhoseFolderIsGoneIsNotRemoved(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := mergedWorktree(t, f, c, repo)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	status, msg := f.removeWorktree(c, repo, wt, true)

	if status != http.StatusConflict || msg != "the Worktree's folder is gone" {
		t.Errorf("status %d %q, want 409 saying the folder is gone", status, msg)
	}
}

func TestARebaseMergedBranchCountsAsMerged(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "rebased")
	commitFile(t, wt, "a.txt", "1\n")
	commitFile(t, wt, "b.txt", "2\n")
	commitFile(t, repo, "other.txt", "main moved on\n")
	// GitHub's "Rebase and merge": main gets copies of both commits, with
	// new IDs; the local branch keeps the old ones.
	git(t, repo, "cherry-pick", "main..rebased")

	f.post(c, "/api/projects/refresh", nil)
	w := worktreeAt(t, projects(t, f.state(c))[0], wt)

	if w["merge"] != "merged" || w["ahead"] != float64(2) {
		t.Fatalf("merge %q ahead %v, want merged and still 2 ahead", w["merge"], w["ahead"])
	}
}

func TestAWorktreeWithNoCommitsOfItsOwnIsRemovableButNotCalledMerged(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "fresh")

	w := worktreeAt(t, projects(t, f.state(c))[0], wt)

	if w["merge"] != "no-own-commits" || w["removal"] != "" {
		t.Fatalf("merge %q removal %q, want no-own-commits and no refusal", w["merge"], w["removal"])
	}
	if status, msg := f.removeWorktree(c, repo, wt, true); status != http.StatusNoContent {
		t.Errorf("status %d %q, want 204", status, msg)
	}
}

func TestTheConfirmationListsTheIgnoredFilesRemovalDeletes(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "done")
	commitFile(t, wt, ".gitignore", ".env\nnode_modules/\n")
	git(t, repo, "merge", "-q", "--ff-only", "done")
	writeFile(t, filepath.Join(wt, ".env"), "SECRET=1\n")
	if err := os.MkdirAll(filepath.Join(wt, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt, "node_modules", "left-pad", "index.js"), "x\n")

	status, body := f.post(c, "/api/worktrees/remove", map[string]any{"project": repo, "path": wt})

	if status != http.StatusConflict || body["confirm"] != true {
		t.Fatalf("status %d body %v, want 409 asking to confirm", status, body)
	}
	var ignored []string
	for _, v := range body["ignored"].([]any) {
		ignored = append(ignored, v.(string))
	}
	if !slices.Equal(ignored, []string{".env", "node_modules"}) {
		t.Errorf("ignored = %v, want [.env node_modules]", ignored)
	}
}

func TestRemovingInAnUnknownProjectIsNotFound(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()

	if status, _ := f.removeWorktree(c, "/no/such/project", "/no/such/project.worktrees/x", true); status != http.StatusNotFound {
		t.Errorf("status %d, want 404", status)
	}
}

func TestAWorktreeMergedIntoOriginIsRemoved(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	seed := newRepo(t, parent, "seed")
	origin := filepath.Join(parent, "origin.git")
	git(t, parent, "clone", "-q", "--bare", seed, origin)
	repo := filepath.Join(parent, "shop")
	git(t, parent, "clone", "-q", origin, repo)
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "done")
	commitFile(t, wt, "done.txt", "1\n")
	if status, msg := f.removeWorktree(c, repo, wt, true); msg != "its branch isn't merged into origin/main" {
		t.Fatalf("before the merge: status %d %q, want it not merged into origin/main", status, msg)
	}

	git(t, wt, "push", "-q", "origin", "done:main") // merged on the remote…
	git(t, repo, "fetch", "-q")                     // …and fetched

	if status, msg := f.removeWorktree(c, repo, wt, true); status != http.StatusNoContent {
		t.Errorf("status %d %q, want 204", status, msg)
	}
}

func TestWithoutAReflogAWorktreeWithNothingOfItsOwnIsCalledInBase(t *testing.T) {
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	// Without reflogs fh can't tell a fresh branch from a fast-forward merge.
	git(t, repo, "config", "core.logAllRefUpdates", "false")
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "fresh")

	w := worktreeAt(t, projects(t, f.state(c))[0], wt)

	if w["merge"] != "in-base" || w["removal"] != "" {
		t.Fatalf("merge %q removal %q, want in-base and no refusal", w["merge"], w["removal"])
	}
}

// rawPost is post for a goroutine: it reports errors instead of failing.
func rawPost(f *fh, c *http.Client, path string, body any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", f.origin()+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.origin())
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close() // only the status matters
	return resp.StatusCode, nil
}

func TestNoSessionStartsInAWorktreeWhileItIsBeingRemoved(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, fmt.Sprintf("[agents.claude]\ncommand = [%q]\nenv = { FH_FAKE_SCRIPT = \"{}\" }\n", fakeAgentBin))
	opts := appOptions(dir)
	// The hook holds the removal open, reserved, until the test lets go.
	reached, release := make(chan struct{}, 1), make(chan struct{})
	opts.BeforeWorktreeRemove = func() {
		reached <- struct{}{}
		<-release
	}
	f := startFhWith(t, opts)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	wt := fhWorktree(t, f, c, repo, "done")
	removed := make(chan int, 1)
	go func() {
		status, err := rawPost(f, c, "/api/worktrees/remove", map[string]any{"project": repo, "path": wt, "confirm": true})
		if err != nil {
			status = 0
		}
		removed <- status
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the removal never reached git")
	}

	status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "worktree": wt, "agent": "claude"})
	close(release)

	if status != http.StatusConflict || body["error"] != "this Worktree is being removed" {
		t.Errorf("start during removal: status %d %v, want 409 saying it is being removed", status, body)
	}
	if got := <-removed; got != http.StatusNoContent {
		t.Errorf("removal: status %d, want 204", got)
	}
}
