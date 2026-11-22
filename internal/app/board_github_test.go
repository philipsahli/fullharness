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
// issues, `pr list` with prs, and `auth status` with authOK. It returns a
// function that counts the GraphQL calls.
func fakeGh(t *testing.T, issues []ghIssue, prs []map[string]any, authOK bool) func() int {
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
	calls := filepath.Join(dir, "graphql-calls")
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *\"auth status\"*) [ " + auth + " = 0 ] || echo 'You are not logged into any GitHub hosts' >&2; exit " + auth + " ;;\n" +
		"  *graphql*) echo x >> \"" + calls + "\"; cat \"" + dir + "/issues.json\" ;;\n" +
		"  *\"pr list\"*) cat \"" + dir + "/prs.json\" ;;\n" +
		"  *) echo \"fake gh: unexpected $*\" >&2; exit 2 ;;\n" +
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
	return func() int {
		b, _ := os.ReadFile(calls) // no file yet means no calls
		return strings.Count(string(b), "x")
	}
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
		{"number": 100, "headRefName": "21-index", "body": "Adds the index"},
		{"number": 101, "headRefName": "ranking", "body": "Ranks results.\n\ncloses #22"},
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
	fakeGh(t, nil, []map[string]any{{"number": 7, "headRefName": "1-form", "body": ""}}, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	login := featureByID(t, f.board(c, repo), "login")

	if s := ticketByNumber(t, login, "01")["stage"]; s != "review" {
		t.Errorf("01 = %v, want review (open PR from branch 1-form)", s)
	}
}
