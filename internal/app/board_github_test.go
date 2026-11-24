package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ghIssue is one issue as fh's GraphQL query returns it.
type ghIssue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Body      string `json:"body"`
	Labels    []string
	Parent    int
	SubIssues []int
	BlockedBy map[int]string // number → state
}

func (i ghIssue) node() map[string]any {
	labels := []any{}
	for _, l := range i.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	subs := []any{}
	for _, n := range i.SubIssues {
		subs = append(subs, map[string]any{"number": n})
	}
	blocked := []any{}
	for n, st := range i.BlockedBy {
		blocked = append(blocked, map[string]any{"number": n, "state": st})
	}
	var parent any
	if i.Parent != 0 {
		parent = map[string]any{"number": i.Parent}
	}
	return map[string]any{"number": i.Number, "title": i.Title, "state": i.State, "body": i.Body,
		"labels": map[string]any{"nodes": labels}, "parent": parent,
		"subIssues": map[string]any{"nodes": subs}, "blockedBy": map[string]any{"nodes": blocked}}
}

// fakeGh puts a gh first on PATH that answers fh's GraphQL query with
// issues, `pr list` for every state capped at 500 with prs, and `auth
// status` with authOK. It returns a
// function that counts the GraphQL calls.
func fakeGh(t *testing.T, issues []ghIssue, prs []map[string]any, authOK bool) func() int {
	t.Helper()
	calls, _, _ := fakeGhLogged(t, issues, prs, authOK)
	return calls
}

// fakeGhLogged is fakeGh that also returns the gh commands it didn't
// expect, such as a merge or an issue edit, one per line, and a function
// that changes the open PRs.
func fakeGhLogged(t *testing.T, issues []ghIssue, prs []map[string]any, authOK bool) (calls func() int, unexpected func() string, setPRs func([]map[string]any)) {
	t.Helper()
	dir := t.TempDir()
	nodes := []any{}
	for _, i := range issues {
		nodes = append(nodes, i.node())
	}
	page := []any{map[string]any{"data": map[string]any{"repository": map[string]any{"issues": map[string]any{
		"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}}}}}
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, name), string(b))
	}
	write("issues.json", page)
	if prs == nil {
		prs = []map[string]any{}
	}
	write("prs.json", prs)
	auth := "0"
	if !authOK {
		auth = "1"
	}
	callsFile := filepath.Join(dir, "graphql-calls")
	unexpectedFile := filepath.Join(dir, "unexpected")
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *\"auth status\"*) [ " + auth + " = 0 ] || echo 'You are not logged into any GitHub hosts' >&2; exit " + auth + " ;;\n" +
		"  *graphql*) echo x >> \"" + callsFile + "\"; cat \"" + dir + "/issues.json\" ;;\n" +
		"  *\"pr list --state all --limit 500 \"*) cat \"" + dir + "/prs.json\" ;;\n" +
		"  *) echo \"$*\" >> \"" + unexpectedFile + "\"; echo \"fake gh: unexpected $*\" >&2; exit 2 ;;\n" +
		"esac\n"
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bin, "gh"), script)
	if err := os.Chmod(filepath.Join(bin, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	calls = func() int {
		b, _ := os.ReadFile(callsFile) // no file yet means no calls
		return strings.Count(string(b), "x")
	}
	unexpected = func() string {
		b, _ := os.ReadFile(unexpectedFile) // no file yet means none
		return string(b)
	}
	setPRs = func(prs []map[string]any) { write("prs.json", prs) }
	return calls, unexpected, setPRs
}

func githubRepo(t *testing.T) string {
	t.Helper()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	git(t, repo, "remote", "add", "origin", "git@github.com:someone/shop.git")
	return repo
}

func boardRefresh(t *testing.T, f *fh, c *http.Client, project string) map[string]any {
	t.Helper()
	resp, err := c.Get(f.origin() + "/api/board?refresh=1&project=" + url.QueryEscape(project))
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(readBody(t, resp)), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAGitHubBoardPutsEachFeatureAtItsStage(t *testing.T) {
	repo := githubRepo(t)
	git(t, repo, "checkout", "-q", "-b", "31-export")
	commitFile(t, repo, "export.go", "package main\n")
	git(t, repo, "checkout", "-q", "main")
	fakeGh(t, []ghIssue{
		{Number: 10, Title: "Spec: Login", State: "OPEN", Labels: []string{"ready-for-agent"}, SubIssues: []int{11, 12, 13}},
		{Number: 11, Title: "Form", State: "CLOSED", Labels: []string{"ready-for-agent"}, Parent: 10},
		{Number: 12, Title: "Cookie", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10, BlockedBy: map[int]string{11: "CLOSED"}},
		{Number: 13, Title: "SSO", State: "OPEN", Labels: []string{"needs-triage"}, Parent: 10, Body: "Blocked by: #12"},
		{Number: 20, Title: "Search", State: "OPEN", SubIssues: []int{21}},
		{Number: 21, Title: "Index", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 20},
		{Number: 22, Title: "Ranking", State: "OPEN", Labels: []string{"ready-for-agent"}, Body: "## Parent\n\n#20\n\nRank results."},
		{Number: 30, Title: "Export", State: "OPEN", SubIssues: []int{31}},
		{Number: 31, Title: "CSV", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 30},
		{Number: 40, Title: "Billing", State: "CLOSED", SubIssues: []int{41}},
		{Number: 41, Title: "Invoice", State: "OPEN", Parent: 40},
		{Number: 50, Title: "Audit log", State: "OPEN", Labels: []string{"ready-for-agent"}},
		{Number: 60, Title: "Explore caching", State: "OPEN", Labels: []string{"wayfinder:map"}, SubIssues: []int{61}},
		{Number: 61, Title: "Measure", State: "OPEN", Labels: []string{"wayfinder:research"}, Parent: 60},
		{Number: 70, Title: "A plain bug", State: "OPEN", Labels: []string{"needs-triage"}},
	}, []map[string]any{
		{"number": 100, "headRefName": "21-index", "body": "Adds the index", "state": "OPEN"},
		{"number": 101, "headRefName": "ranking", "body": "Ranks results.\n\ncloses #22", "state": "OPEN"},
	}, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	b := f.board(c, repo)

	if b["tracker"] != "github" {
		t.Fatalf("tracker = %v, want github (note %v)", b["tracker"], b["note"])
	}
	for id, stage := range map[string]string{"10": "tickets", "20": "review", "30": "implementing", "40": "shipped", "50": "spec"} {
		if got := featureByID(t, b, id)["stage"]; got != stage {
			t.Errorf("feature #%s at %v, want %s", id, got, stage)
		}
	}
	if n := len(objects(t, b, "features")); n != 5 {
		t.Errorf("got %d features, want 5 (no Wayfinder map, no plain issue)", n)
	}
	login := featureByID(t, b, "10")
	if tk := ticketByNumber(t, login, "12"); tk["startable"] != true {
		t.Errorf("#12 = %v, want Startable (its blocker #11 is closed)", tk)
	}
	if tk := ticketByNumber(t, login, "13"); tk["startable"] != false || !strings.Contains(asJSON(tk["blockers"]), `"number":"12"`) {
		t.Errorf("#13 = %v, want blocked by #12 from its body line", tk)
	}
	search := featureByID(t, b, "20")
	if s := ticketByNumber(t, search, "21")["stage"]; s != "review" {
		t.Errorf("#21 = %v, want review (PR from branch 21-index)", s)
	}
	if s := ticketByNumber(t, search, "22")["stage"]; s != "review" {
		t.Errorf("#22 = %v, want review (PR body closes #22; ticket by ## Parent)", s)
	}
}

func TestGitHubReadsAreCachedUntilRefresh(t *testing.T) {
	repo := githubRepo(t)
	calls := fakeGh(t, []ghIssue{{Number: 1, Title: "Spec", State: "OPEN", Labels: []string{"ready-for-agent"}}}, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	f.board(c, repo)
	f.board(c, repo) // a second tab, within the cache time
	if n := calls(); n != 1 {
		t.Fatalf("gh GraphQL calls = %d, want 1 for two reads in a row", n)
	}
	boardRefresh(t, f, c, repo)
	if n := calls(); n != 2 {
		t.Fatalf("gh GraphQL calls after Refresh = %d, want 2", n)
	}
}

func TestAMissingOrLoggedOutGhSaysWhatToDo(t *testing.T) {
	repo := githubRepo(t)
	fakeGh(t, nil, nil, false)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	if note := f.board(c, repo)["note"]; !strings.Contains(note.(string), "gh auth login") {
		t.Errorf("logged out: note = %v, want gh auth login", note)
	}

	bin := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if note := boardRefresh(t, f, c, repo)["note"]; !strings.Contains(note.(string), "brew install gh") {
		t.Errorf("no gh: note = %v, want brew install gh", note)
	}
}

func TestALocalProjectWithAGitHubRemoteGetsItsPRs(t *testing.T) {
	repo := githubRepo(t)
	scratch(t, repo, "login/spec.md", "# Login\n")
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	fakeGh(t, nil, []map[string]any{{"number": 7, "headRefName": "1-form", "body": "", "state": "OPEN", "url": "https://github.com/someone/shop/pull/7"}}, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	login := featureByID(t, f.board(c, repo), "login")

	if s := ticketByNumber(t, login, "01")["stage"]; s != "review" {
		t.Errorf("01 = %v, want review (open PR from branch 1-form)", s)
	}
	if got := asJSON(ticketByNumber(t, login, "01")["prs"]); got != `[{"checks":"","number":7,"state":"open","url":"https://github.com/someone/shop/pull/7"}]` {
		t.Errorf("01 prs = %s, want PR #7, open, no checks", got)
	}
}

func TestGitHubBlockersParentsAndPRLinksInEveryWrittenForm(t *testing.T) {
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 1, Title: "Spec", State: "OPEN", Labels: []string{"ready-for-agent"}},
		{Number: 2, Title: "Native open blocker", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1, BlockedBy: map[int]string{3: "OPEN"}},
		{Number: 3, Title: "Part of", State: "OPEN", Labels: []string{"ready-for-agent"}, Body: "Part of #1"},
		{Number: 4, Title: "Section blockers", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1,
			Body: "## Blocked by\n\n- #3 (needs #9 first)\n- #5\n\n## Notes\n\n#7 is unrelated"},
		{Number: 5, Title: "Done", State: "CLOSED", Labels: []string{"ready-for-agent"}, Parent: 1},
		{Number: 6, Title: "Linked by body", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1},
	}, []map[string]any{{"number": 9, "headRefName": "3-part", "body": "Closes: #6", "state": "OPEN"}}, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	spec := featureByID(t, f.board(c, repo), "1")

	if tk := ticketByNumber(t, spec, "2"); tk["startable"] != false {
		t.Errorf("#2 = %v, want blocked by open #3 (native)", tk)
	}
	if s := ticketByNumber(t, spec, "3")["stage"]; s != "review" {
		t.Errorf("#3 (Part of #1) = %v, want review via branch 3-part", s)
	}
	if got := asJSON(ticketByNumber(t, spec, "4")["blockers"]); got != `[{"known":true,"number":"3","open":true},{"known":true,"number":"5","open":false}]` {
		t.Errorf("#4 blockers = %s, want #3 (open) and #5 (closed) from the section", got)
	}
	if s := ticketByNumber(t, spec, "6")["stage"]; s != "review" {
		t.Errorf("#6 = %v, want review: the PR on branch 3-part also says Closes: #6", s)
	}
}

func TestClosedIssuesWithoutTicketsAreLeftOff(t *testing.T) {
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 1, Title: "Old chore", State: "CLOSED", Labels: []string{"ready-for-agent"}},
		{Number: 2, Title: "Shipped spec", State: "CLOSED", SubIssues: []int{3}},
		{Number: 3, Title: "Its ticket", State: "CLOSED", Labels: []string{"ready-for-agent"}, Parent: 2},
		{Number: 4, Title: "Open spec", State: "OPEN", Labels: []string{"ready-for-agent"}},
	}, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	b := f.board(c, repo)

	ids := []string{}
	for _, ft := range objects(t, b, "features") {
		ids = append(ids, ft["id"].(string))
	}
	if strings.Join(ids, ",") != "2,4" {
		t.Errorf("features = %v, want 2 (closed with Tickets) and 4 (open), not 1", ids)
	}
}

func TestAnEmptyBoardSaysHowToStart(t *testing.T) {
	fakeGh(t, nil, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()

	gh := githubRepo(t)
	f.addProject(c, gh)
	if note, _ := f.board(c, gh)["note"].(string); !strings.Contains(note, "No Specs in GitHub yet") || !strings.Contains(note, "/to-spec") {
		t.Errorf("GitHub, no issues: note = %q, want a hint to run /to-spec", note)
	}

	local := realPath(t, newRepo(t, t.TempDir(), "notes"))
	if err := os.MkdirAll(filepath.Join(local, ".scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.addProject(c, local)
	if note, _ := f.board(c, local)["note"].(string); !strings.Contains(note, "No Specs in .scratch yet") {
		t.Errorf("empty .scratch: note = %q, want a hint to run /to-spec", note)
	}
}

func TestATicketAlreadyInProgressIsNotStartable(t *testing.T) {
	repo := githubRepo(t)
	git(t, repo, "checkout", "-q", "-b", "3-work")
	commitFile(t, repo, "w.go", "package main\n")
	git(t, repo, "checkout", "-q", "main")
	fakeGh(t, []ghIssue{
		{Number: 1, Title: "Spec", State: "OPEN", SubIssues: []int{2, 3, 4}},
		{Number: 2, Title: "In review", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1},
		{Number: 3, Title: "Implementing", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1},
		{Number: 4, Title: "Not started", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1},
	}, []map[string]any{{"number": 9, "headRefName": "2-review", "body": "", "state": "OPEN"}}, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	spec := featureByID(t, f.board(c, repo), "1")

	for n, want := range map[string]bool{"2": false, "3": false, "4": true} {
		if got := ticketByNumber(t, spec, n)["startable"]; got != want {
			t.Errorf("#%s startable = %v, want %v", n, got, want)
		}
	}
}
