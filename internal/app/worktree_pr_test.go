package app_test

import (
	"net/http"
	"path/filepath"
	"testing"
)

// prRepo is a GitHub repo with Worktrees on the given branches, added to a
// new fh; it returns fh's client and the repo with its Worktrees' paths.
func prRepo(t *testing.T, branches ...string) (f *fh, repo string, paths map[string]string) {
	t.Helper()
	repo = githubRepo(t)
	paths = map[string]string{"main": repo}
	for _, b := range branches {
		p := filepath.Join(filepath.Dir(repo), b)
		git(t, repo, "worktree", "add", "-q", "-b", b, p)
		paths[b] = p
	}
	f = startFh(t, t.TempDir())
	return f, repo, paths
}

func TestAWorktreeCarriesItsBranchsOpenPROrElseTheMostRecent(t *testing.T) {
	fakeGh(t, nil, []map[string]any{
		ghPR(5, "form", "CLOSED"), ghPR(7, "form", "OPEN", checkRun("COMPLETED", "SUCCESS")), ghPR(9, "form", "CLOSED"),
		ghPR(3, "retry", "MERGED"), ghPR(6, "retry", "CLOSED"),
		ghPR(8, "other", "OPEN"),
	}, true)
	f, repo, paths := prRepo(t, "form", "retry")
	c := f.authorisedClient()

	p := f.addProject(c, repo)

	for branch, want := range map[string]string{
		"form":  `{"checks":"passed","number":7,"state":"open","url":"https://github.com/someone/shop/pull/7"}`,
		"retry": `{"checks":"","number":6,"state":"closed","url":"https://github.com/someone/shop/pull/6"}`,
	} {
		w := worktreeAt(t, p, paths[branch])
		if got := asJSON(w["pr"]); got != want {
			t.Errorf("%s: pr = %s, want %s", branch, got, want)
		}
		if w["noPR"] != nil {
			t.Errorf("%s: noPR = %v, want absent", branch, w["noPR"])
		}
	}
	main := worktreeAt(t, p, paths["main"])
	if main["pr"] != nil || main["noPR"] != true {
		t.Errorf("main: pr, noPR = %v, %v, want none, true", main["pr"], main["noPR"])
	}
}

func TestAWorktreeCarriesNoPRInformationWhenGhIsLoggedOut(t *testing.T) {
	fakeGh(t, nil, []map[string]any{ghPR(7, "form", "OPEN")}, false)
	f, repo, paths := prRepo(t, "form")
	c := f.authorisedClient()

	p := f.addProject(c, repo)

	for branch, path := range paths {
		w := worktreeAt(t, p, path)
		if w["pr"] != nil || w["noPR"] != nil {
			t.Errorf("%s: pr, noPR = %v, %v, want both absent", branch, w["pr"], w["noPR"])
		}
	}
}

func TestWorktreesAndTheBoardShareOnePRListUntilABoardRefresh(t *testing.T) {
	g := newFakeGh(t, prSpec(), []map[string]any{ghPR(7, "form", "CLOSED")}, true)
	f, repo, paths := prRepo(t, "form")
	c := f.authorisedClient()
	prNumber := func() any {
		t.Helper()
		if status, body := f.post(c, "/api/projects/refresh", nil); status != http.StatusNoContent {
			t.Fatalf("refresh: status %d (%v)", status, body)
		}
		for _, p := range projects(t, f.state(c)) {
			if pr, ok := worktreeAt(t, p, paths["form"])["pr"].(map[string]any); ok {
				return pr["number"]
			}
		}
		return nil
	}

	f.addProject(c, repo)
	f.board(c, repo)
	first := prNumber()
	g.setPRs([]map[string]any{ghPR(7, "form", "CLOSED"), ghPR(9, "form", "OPEN")})
	cached := prNumber()

	if first != float64(7) || cached != float64(7) {
		t.Errorf("pr within the cache period = %v, then %v, want 7 both times", first, cached)
	}
	if n := g.prListCalls(); n != 1 {
		t.Errorf("pr list calls = %d after adding, a board and two refreshes, want 1", n)
	}

	boardRefresh(t, f, c, repo)

	if got := prNumber(); got != float64(9) {
		t.Errorf("pr after a board refresh = %v, want 9", got)
	}
	if n := g.prListCalls(); n != 2 {
		t.Errorf("pr list calls = %d after a board refresh, want 2", n)
	}
}
