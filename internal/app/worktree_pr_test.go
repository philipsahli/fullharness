package app_test

import (
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
