package app_test

import (
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// prRepo is a GitHub repo with Worktrees on the given branches, and a new
// fh; it returns fh and the repo with its Worktrees' paths.
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

// waitProjects reads events from tab until a projects event's list matches
// ok, and returns that list. PRs reach the Worktrees in the background, so
// tests wait for them this way.
func waitProjects(t *testing.T, tab *websocket.Conn, what string, ok func([]map[string]any) bool) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] == "projects" && ok(projects(t, ev)) {
			return projects(t, ev)
		}
	}
	t.Fatalf("no projects event with %s", what)
	return nil
}

// prNumber is the number of the PR on the Worktree at path in ps, or nil.
func prNumber(t *testing.T, ps []map[string]any, path string) any {
	t.Helper()
	if len(ps) == 0 {
		return nil
	}
	pr, _ := worktreeAt(t, ps[0], path)["pr"].(map[string]any)
	return pr["number"]
}

// waitPR waits until the Worktree at path carries PR number.
func waitPR(t *testing.T, tab *websocket.Conn, path string, number int) []map[string]any {
	t.Helper()
	return waitProjects(t, tab, "PR #"+strconv.Itoa(number)+" on "+path, func(ps []map[string]any) bool {
		return prNumber(t, ps, path) == float64(number)
	})
}

func TestAWorktreeCarriesItsBranchsUnmergedPROrElseTheMostRecent(t *testing.T) {
	t.Parallel()
	fakeGh(t, nil, []map[string]any{
		ghPR(5, "form", "CLOSED"), ghPR(7, "form", "OPEN", checkRun("COMPLETED", "SUCCESS")), ghPR(9, "form", "CLOSED"),
		ghPR(3, "retry", "MERGED"), ghPR(6, "retry", "CLOSED"),
		ghPR(8, "other", "OPEN"),
	}, true)
	f, repo, paths := prRepo(t, "form", "retry")
	c := f.authorisedClient()
	tab := f.connectTab(c)

	f.addProject(c, repo)

	p := waitPR(t, tab, paths["form"], 7)[0]
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
	t.Parallel()
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
	t.Parallel()
	g := newFakeGh(t, prSpec(), []map[string]any{ghPR(7, "form", "CLOSED")}, true)
	f, repo, paths := prRepo(t, "form")
	c := f.authorisedClient()
	tab := f.connectTab(c)
	refresh := func() {
		t.Helper()
		if status, body := f.post(c, "/api/projects/refresh", nil); status != http.StatusNoContent {
			t.Fatalf("refresh: status %d (%v)", status, body)
		}
	}

	f.addProject(c, repo)
	waitPR(t, tab, paths["form"], 7)
	f.board(c, repo)
	g.setPRs([]map[string]any{ghPR(7, "form", "CLOSED"), ghPR(9, "form", "OPEN")})
	refresh()
	refresh()

	if got := prNumber(t, projects(t, f.state(c)), paths["form"]); got != float64(7) {
		t.Errorf("pr within the cache period = %v, want 7", got)
	}
	if n := g.prListCalls(); n != 1 {
		t.Errorf("pr list calls = %d after adding, a board and two refreshes, want 1", n)
	}

	boardRefresh(t, f, c, repo)

	// No Refresh here: the board refresh itself pushes the new PR to tabs.
	waitPR(t, tab, paths["form"], 9)
	if n := g.prListCalls(); n != 2 {
		t.Errorf("pr list calls = %d after a board refresh, want 2", n)
	}
}

func TestAddingAProjectAndARefreshDoNotWaitForGh(t *testing.T) {
	t.Parallel()
	g := newFakeGh(t, nil, []map[string]any{ghPR(7, "form", "OPEN")}, true)
	g.hold()
	f, repo, paths := prRepo(t, "form")
	// Registered after fh, so it runs before fh stops and waits for gh.
	t.Cleanup(g.release)
	c := f.authorisedClient()
	tab := f.connectTab(c)
	// gh is held until the test releases it, so a call that waited on gh
	// would never return; the timeout turns that into a failure. Any bound
	// tells the two apart, so it is the generous waitBudget: an add runs git
	// and can take seconds on a loaded machine (#220).
	c.Timeout = waitBudget

	// A turn end runs the same Refresh as /api/projects/refresh.
	added := f.addProject(c, repo)
	if status, body := f.post(c, "/api/projects/refresh", nil); status != http.StatusNoContent {
		t.Fatalf("refresh: status %d (%v)", status, body)
	}

	// Both calls returned; now check gh really was running all along.
	deadline := time.Now().Add(waitBudget)
	for g.prListCalls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("gh pr list never started")
		}
		time.Sleep(10 * time.Millisecond) // polling the fake's call log; gh runs in its own process
	}
	if w := worktreeAt(t, added, paths["form"]); w["pr"] != nil || w["noPR"] != nil {
		t.Errorf("form before gh answered: pr, noPR = %v, %v, want both absent", w["pr"], w["noPR"])
	}

	g.release()

	waitPR(t, tab, paths["form"], 7)
	if n := g.prListCalls(); n != 1 {
		t.Errorf("pr list calls = %d after an add and a refresh, want 1", n)
	}
}

func TestQuittingFhCancelsARunningPRFetch(t *testing.T) {
	t.Parallel()
	g := newFakeGh(t, nil, []map[string]any{ghPR(7, "form", "OPEN")}, true)
	g.hold()
	// Releases gh only after the test, so fh's Close below can't wait for it.
	t.Cleanup(g.release)
	f, repo, _ := prRepo(t, "form")
	c := f.authorisedClient()
	f.addProject(c, repo)

	deadline := time.Now().Add(waitBudget)
	for g.prListCalls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("gh pr list never started")
		}
		time.Sleep(10 * time.Millisecond) // polling the fake's call log; gh runs in its own process
	}

	start := time.Now()
	if err := f.inst.Close(); err != nil {
		t.Fatalf("stop fh: %v", err)
	}
	// gh is held past the test and its own timeout is 30 s, so a Close that
	// waited for gh would take 30 s. 15 s leaves room for a loaded machine
	// (#220) and still tells the two apart.
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("Close took %v with gh held, want it to cancel the fetch", d)
	}
}
