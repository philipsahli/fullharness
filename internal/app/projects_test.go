package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddProjectListsItWithItsMainTree(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))

	status, body := f.post(c, "/api/projects", map[string]string{"path": repo})

	if status != http.StatusCreated {
		t.Fatalf("add: status %d (%v), want 201", status, body)
	}
	ps := projects(t, f.state(c))
	if len(ps) != 1 {
		t.Fatalf("got %d projects, want 1", len(ps))
	}
	if ps[0]["path"] != repo || ps[0]["name"] != "shop" {
		t.Errorf("project = %v, want path %s and name shop", ps[0], repo)
	}
	wts := worktrees(t, ps[0])
	if len(wts) != 1 {
		t.Fatalf("got %d worktrees, want 1", len(wts))
	}
	if wts[0]["path"] != repo || wts[0]["branch"] != "main" || wts[0]["main"] != true || wts[0]["agent"] != false {
		t.Errorf("worktree = %v, want the main tree on branch main", wts[0])
	}
}

func TestAddProjectRejectsPathsThatAreNotARepo(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	plain := t.TempDir()

	for _, tc := range []struct{ path, wantInError string }{
		{plain, "not a git repository"},
		{filepath.Join(plain, "missing"), "not a folder"},
	} {
		status, body := f.post(c, "/api/projects", map[string]string{"path": tc.path})
		if status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.path, status)
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, tc.wantInError) {
			t.Errorf("%s: error %q, want it to mention %q", tc.path, msg, tc.wantInError)
		}
	}
	if ps := projects(t, f.state(c)); len(ps) != 0 {
		t.Errorf("rejected paths were added: %v", ps)
	}
}

func TestAddProjectRejectsALinkedWorktreeAndNamesItsRepo(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	repo := newRepo(t, parent, "shop")
	linked := filepath.Join(parent, "shop.worktrees", "feature")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", linked)

	status, body := f.post(c, "/api/projects", map[string]string{"path": linked})

	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", status)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "worktree of "+repo) {
		t.Errorf("error %q, want it to say it is a worktree of %s", msg, repo)
	}
}

func TestAddProjectFromASubfolderAddsTheRepo(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	sub := filepath.Join(repo, "docs")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	status, body := f.post(c, "/api/projects", map[string]string{"path": sub})

	if status != http.StatusCreated || body["path"] != repo {
		t.Fatalf("status %d, body %v; want 201 with path %s", status, body, repo)
	}
}

func TestAddingAProjectTwiceIsAConflict(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.post(c, "/api/projects", map[string]string{"path": repo})

	status, _ := f.post(c, "/api/projects", map[string]string{"path": repo})

	if status != http.StatusConflict {
		t.Fatalf("status %d, want 409", status)
	}
	if ps := projects(t, f.state(c)); len(ps) != 1 {
		t.Errorf("got %d projects, want 1", len(ps))
	}
}

func TestProjectsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	first := startFh(t, dir)
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	first.post(first.authorisedClient(), "/api/projects", map[string]string{"path": repo})
	first.stop()

	restarted := startFh(t, dir)

	ps := projects(t, restarted.state(restarted.authorisedClient()))
	if len(ps) != 1 || ps[0]["path"] != repo {
		t.Fatalf("after restart: projects %v, want %s", ps, repo)
	}
}

func TestProjectsFileIsPrivateAndConfigIsNeverWritten(t *testing.T) {
	dir := t.TempDir()
	config := "# my settings\nport = 0 # any free port\n"
	writeConfig(t, dir, config)
	f := startFh(t, dir)
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))

	f.post(f.authorisedClient(), "/api/projects", map[string]string{"path": repo})

	info, err := os.Stat(filepath.Join(dir, "projects.toml"))
	if err != nil {
		t.Fatalf("projects.toml: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("projects.toml mode %o, want 600", perm)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != config {
		t.Errorf("config.toml changed to %q", got)
	}
}

func TestRemoveProjectLeavesTheRepoOnDisk(t *testing.T) {
	dir := t.TempDir()
	f := startFh(t, dir)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.post(c, "/api/projects", map[string]string{"path": repo})

	status, _ := f.post(c, "/api/projects/remove", map[string]string{"path": repo})

	if status != http.StatusNoContent {
		t.Fatalf("status %d, want 204", status)
	}
	if ps := projects(t, f.state(c)); len(ps) != 0 {
		t.Errorf("projects after remove: %v, want none", ps)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Errorf("repo on disk was touched: %v", err)
	}
	f.stop()
	restarted := startFh(t, dir)
	if ps := projects(t, restarted.state(restarted.authorisedClient())); len(ps) != 0 {
		t.Errorf("removed project came back after restart: %v", ps)
	}
}

func TestRemovingAnUnknownProjectIsNotFound(t *testing.T) {
	f := startFh(t, t.TempDir())

	status, _ := f.post(f.authorisedClient(), "/api/projects/remove", map[string]string{"path": "/nowhere"})

	if status != http.StatusNotFound {
		t.Fatalf("status %d, want 404", status)
	}
}

func TestAddProjectRejectsABareRepositoryAndSaysWhy(t *testing.T) {
	f := startFh(t, t.TempDir())
	bare := filepath.Join(t.TempDir(), "shop.git")
	git(t, t.TempDir(), "init", "-q", "--bare", bare)

	status, body := f.post(f.authorisedClient(), "/api/projects", map[string]string{"path": bare})

	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", status)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "bare") {
		t.Errorf("error %q, want it to say the repository is bare", msg)
	}
}

func TestAProjectLinksToItsGitHubRepoOnlyWhenOriginIsOnGitHub(t *testing.T) {
	fakeGh(t, nil, nil, true) // a GitHub origin makes fh read its PRs
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	for name, origin := range map[string]string{
		"ssh":    "git@github.com:someone/ssh-shop.git",
		"https":  "https://github.com/someone/https-shop",
		"gitlab": "git@gitlab.com:someone/shop.git",
		"none":   "",
	} {
		repo := newRepo(t, parent, name)
		if origin != "" {
			git(t, repo, "remote", "add", "origin", origin)
		}
		if status, body := f.post(c, "/api/projects", map[string]string{"path": repo}); status != http.StatusCreated {
			t.Fatalf("add %s: status %d (%v)", name, status, body)
		}
	}
	want := map[string]any{
		"ssh":    "https://github.com/someone/ssh-shop",
		"https":  "https://github.com/someone/https-shop",
		"gitlab": nil,
		"none":   nil,
	}

	ps := projects(t, f.state(c))

	if len(ps) != len(want) {
		t.Fatalf("got %d projects, want %d", len(ps), len(want))
	}
	for _, p := range ps {
		name, _ := p["name"].(string)
		if p["url"] != want[name] {
			t.Errorf("%s: url = %v, want %v", name, p["url"], want[name])
		}
	}
}

func TestAWorktreeBranchLinksToGitHubOnlyWhenOriginHasIt(t *testing.T) {
	fakeGh(t, nil, nil, true) // a GitHub origin makes fh read its PRs
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	for name, origin := range map[string]string{
		"shop":  "git@github.com:someone/shop.git",
		"tools": "git@gitlab.com:someone/tools.git",
	} {
		repo := newRepo(t, parent, name)
		git(t, repo, "remote", "add", "origin", origin)
		// As if main had been pushed and fetched; feature/x never was.
		git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
		git(t, repo, "worktree", "add", "-q", "-b", "feature/x", filepath.Join(parent, name+"-x"))
		if status, body := f.post(c, "/api/projects", map[string]string{"path": repo}); status != http.StatusCreated {
			t.Fatalf("add %s: status %d (%v)", name, status, body)
		}
	}
	want := map[string]map[string]any{
		"shop":  {"main": "https://github.com/someone/shop/tree/main", "feature/x": nil},
		"tools": {"main": nil, "feature/x": nil}, // not GitHub: no links even with the ref
	}

	ps := projects(t, f.state(c))

	for _, p := range ps {
		name, _ := p["name"].(string)
		wts := worktrees(t, p)
		if len(wts) != len(want[name]) {
			t.Fatalf("%s: got %d worktrees, want %d", name, len(wts), len(want[name]))
		}
		for _, w := range wts {
			branch, _ := w["branch"].(string)
			if w["url"] != want[name][branch] {
				t.Errorf("%s %s: url = %v, want %v", name, branch, w["url"], want[name][branch])
			}
		}
	}
}
