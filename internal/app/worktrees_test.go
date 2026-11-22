package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// addProject adds repo and returns its entry from the snapshot.
func (f *fh) addProject(c *http.Client, repo string) map[string]any {
	f.t.Helper()
	if status, body := f.post(c, "/api/projects", map[string]string{"path": repo}); status != http.StatusCreated {
		f.t.Fatalf("add %s: status %d (%v)", repo, status, body)
	}
	for _, p := range projects(f.t, f.state(c)) {
		if p["path"] == repo {
			return p
		}
	}
	f.t.Fatalf("%s not in snapshot", repo)
	return nil
}

func worktreeAt(t *testing.T, project map[string]any, path string) map[string]any {
	t.Helper()
	for _, w := range worktrees(t, project) {
		if w["path"] == path {
			return w
		}
	}
	t.Fatalf("no worktree at %s in %v", path, project["worktrees"])
	return nil
}

func TestEveryWorktreeIsListedAndOthersAreAgentWorktrees(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	repo := newRepo(t, parent, "shop")
	a := filepath.Join(parent, "shop.worktrees", "a")
	b := filepath.Join(parent, "elsewhere", "b")
	git(t, repo, "worktree", "add", "-q", "-b", "a", a)
	git(t, repo, "worktree", "add", "-q", "-b", "b", b)

	p := f.addProject(c, repo)

	if n := len(worktrees(t, p)); n != 3 {
		t.Fatalf("got %d worktrees, want 3", n)
	}
	if w := worktreeAt(t, p, repo); w["main"] != true || w["agent"] != false {
		t.Errorf("main tree = %v", w)
	}
	for path, branch := range map[string]string{a: "a", b: "b"} {
		w := worktreeAt(t, p, path)
		if w["main"] != false || w["agent"] != true || w["branch"] != branch {
			t.Errorf("%s = %v, want an Agent worktree on %s", path, w, branch)
		}
	}
}

func TestWorktreeShowsUncommittedFiles(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	writeFile(t, filepath.Join(repo, "README.md"), "changed\n") // modified
	writeFile(t, filepath.Join(repo, "new.txt"), "x\n")         // untracked

	w := worktreeAt(t, f.addProject(c, repo), repo)

	if w["uncommitted"] != float64(2) {
		t.Errorf("uncommitted = %v, want 2", w["uncommitted"])
	}
}

func TestAheadBehindComparesWithTheRemoteDefaultBranch(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	origin := newRepo(t, parent, "origin")
	clone := filepath.Join(parent, "shop")
	git(t, parent, "clone", "-q", origin, clone)
	commitFile(t, origin, "upstream.txt", "1\n") // remote moves on…
	git(t, clone, "fetch", "-q")                 // …and the clone sees it
	commitFile(t, clone, "local1.txt", "1\n")
	commitFile(t, clone, "local2.txt", "2\n")

	w := worktreeAt(t, f.addProject(c, clone), clone)

	if w["base"] != "origin/main" || w["ahead"] != float64(2) || w["behind"] != float64(1) {
		t.Errorf("base %v ahead %v behind %v, want origin/main 2 1", w["base"], w["ahead"], w["behind"])
	}
	fetched, _ := w["fetchedAt"].(string)
	if ts, err := time.Parse(time.RFC3339, fetched); err != nil || time.Since(ts) > time.Minute {
		t.Errorf("fetchedAt = %v, want the last fetch's time", w["fetchedAt"])
	}
}

func TestWithoutARemoteAheadBehindComparesWithLocalMain(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	repo := newRepo(t, parent, "shop")
	feature := filepath.Join(parent, "feature")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", feature)
	commitFile(t, feature, "f.txt", "1\n")

	p := f.addProject(c, repo)

	w := worktreeAt(t, p, feature)
	if w["base"] != "main" || w["ahead"] != float64(1) || w["behind"] != float64(0) {
		t.Errorf("base %v ahead %v behind %v, want main 1 0", w["base"], w["ahead"], w["behind"])
	}
	if w["fetchedAt"] != nil {
		t.Errorf("fetchedAt = %v, want null (never fetched)", w["fetchedAt"])
	}
}

func TestProjectWithMissingFolderIsMarkedAndRecovers(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	repo := newRepo(t, parent, "shop")
	f.addProject(c, repo)
	away := filepath.Join(parent, "moved")

	if err := os.Rename(repo, away); err != nil {
		t.Fatal(err)
	}
	f.post(c, "/api/projects/refresh", nil)

	p := projects(t, f.state(c))[0]
	if p["missing"] != true || len(worktrees(t, p)) != 0 {
		t.Errorf("after move: missing %v, worktrees %v; want true and none", p["missing"], p["worktrees"])
	}

	if err := os.Rename(away, repo); err != nil {
		t.Fatal(err)
	}
	f.post(c, "/api/projects/refresh", nil)

	p = projects(t, f.state(c))[0]
	if p["missing"] != false || len(worktrees(t, p)) != 1 {
		t.Errorf("after move back: missing %v, worktrees %v; want false and the main tree", p["missing"], p["worktrees"])
	}
}

func TestAMissingProjectCanBeRemoved(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	if status, _ := f.post(c, "/api/projects/remove", map[string]string{"path": repo}); status != http.StatusNoContent {
		t.Fatalf("status %d, want 204", status)
	}
}
