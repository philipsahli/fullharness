package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

// switcherResults asks /api/switcher what the Switcher lists for q while
// the board shows project, as "<Project> <kind> <number> <title> → <place>"
// lines, the place with what the board needs to show it. A closed result
// has its state in brackets after the title.
func (f *fh) switcherResults(c *http.Client, project, q string) []string {
	f.t.Helper()
	return f.switcherGet(c, project, q, "")
}

// switcherLookup is switcherResults with lookup=1: what the Switcher asks
// once you stop typing a number, GitHub lookups included (#255).
func (f *fh) switcherLookup(c *http.Client, project, q string) []string {
	f.t.Helper()
	return f.switcherGet(c, project, q, "&lookup=1")
}

func (f *fh) switcherGet(c *http.Client, project, q, extra string) []string {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/switcher?project=" + url.QueryEscape(project) + "&q=" + url.QueryEscape(q) + extra)
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET switcher: status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Results []struct {
			Kind, ProjectName, Number, Title, State string
			Opens                                   struct{ Place, Feature, Ticket, Ref, URL string }
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		f.t.Fatal(err)
	}
	lines := []string{}
	for _, r := range got.Results {
		where := r.Opens.Place
		for _, s := range []string{r.Opens.Feature, r.Opens.Ticket, r.Opens.Ref, r.Opens.URL} {
			if s != "" {
				where += " " + s
			}
		}
		title := r.Title
		if r.State != "" {
			title += " [" + r.State + "]"
		}
		lines = append(lines, r.ProjectName+" "+r.Kind+" "+r.Number+" "+title+" → "+where)
	}
	return lines
}

func TestTheSwitcherFindsOpenTicketsOfAProjectByNumberOrWords(t *testing.T) {
	t.Parallel()
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "login/spec.md", "# Login\n")
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	scratch(t, repo, "login/issues/02-session.md", ticket("02", "Session cookie", "needs-info", "01"))
	scratch(t, repo, "login/issues/03-sso.md", ticket("03", "SSO form", "done", "None"))
	f.addProject(c, repo)

	for q, want := range map[string][]string{
		"2":          {"shop ticket 02 Session cookie → ticket login 02"},
		"#01":        {"shop ticket 01 Login form → ticket login 01"},
		"form LOGIN": {"shop ticket 01 Login form → ticket login 01"},
		"sso":        {},                                                     // closed: words find open Tickets only
		"3":          {"shop ticket 03 SSO form [closed] → ticket login 03"}, // a local file, no lookup (#255)
		"":           {},
	} {
		if got := f.switcherResults(c, repo, q); !reflect.DeepEqual(got, want) {
			t.Errorf("q %q: results = %v, want %v", q, got, want)
		}
	}
}

func TestTheSwitcherFindsEachKindOfItemByNumberAcrossProjects(t *testing.T) {
	t.Parallel()
	gh := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 10, Title: "Spec: Billing", State: "OPEN", Labels: []string{"ready-for-agent"}, SubIssues: []int{11}},
		{Number: 11, Title: "Invoice page", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
		{Number: 2, Title: "Drift alert fires twice", State: "OPEN", Labels: []string{"needs-triage"}},
	}, []map[string]any{
		titled(ghPR(21, "11-invoice", "OPEN"), "Invoice page"),
		titled(ghPR(22, "bump-deps", "OPEN"), "Bump deps"),
		titled(ghPR(23, "old-fix", "MERGED"), "Old fix"),
	}, true)
	local := realPath(t, newRepo(t, t.TempDir(), "notes"))
	scratch(t, local, "tags/spec.md", "# Tags\n")
	scratch(t, local, "tags/issues/02-colour.md", ticket("02", "Tag colour", "ready-for-agent", "None"))
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, gh)
	f.addProject(c, local)

	pr := func(n string) string { return "https://github.com/someone/shop/pull/" + n }
	for _, tc := range []struct {
		q, current string
		want       []string
	}{
		{"10", gh, []string{"shop spec 10 Spec: Billing → feature 10"}},
		{"#11", gh, []string{"shop ticket 11 Invoice page → ticket 10 11"}},
		{"21", gh, []string{"shop pr 21 Invoice page → ticket 10 11"}},
		{"22", gh, []string{"shop pr 22 Bump deps → github " + pr("22")}},
		{"23", gh, []string{"shop pr 23 Old fix [merged] → github " + pr("23")}}, // #255
		// A number in two Projects: the board's Project first.
		{"2", gh, []string{"shop issue 2 Drift alert fires twice → triage #2", "notes ticket 02 Tag colour → ticket tags 02"}},
		{"2", local, []string{"notes ticket 02 Tag colour → ticket tags 02", "shop issue 2 Drift alert fires twice → triage #2"}},
		{"99", gh, []string{}},
		// The Project's repo comes from its origin (#256).
		{"PR 2", gh, []string{}},
		{"someone/shop#2", local, []string{"shop issue 2 Drift alert fires twice → triage #2"}},
		{pr("21") + "/files", local, []string{"shop pr 21 Invoice page → ticket 10 11"}},
		{"other/repo#5", gh, []string{"other/repo issue 5 other/repo → github https://github.com/other/repo/issues/5"}},
	} {
		if got := f.switcherResults(c, tc.current, tc.q); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("q %q from %s:\n got %v\nwant %v", tc.q, tc.current, got, tc.want)
		}
	}
}

func TestTheSwitcherListsNothingWithoutATracker(t *testing.T) {
	t.Parallel()
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "bare"))
	f.addProject(c, repo)

	if got := f.switcherResults(c, repo, "1"); len(got) != 0 {
		t.Errorf("results = %v, want none for a Project without a tracker", got)
	}
	resp, err := c.Get(f.origin() + "/api/switcher?project=" + url.QueryEscape(t.TempDir()) + "&q=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = readBody(t, resp) // only the status matters
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown Project: status %d, want 404", resp.StatusCode)
	}
}

// titled is a fake gh PR with its title, as `pr list --json title` gives it.
func titled(pr map[string]any, title string) map[string]any {
	pr["title"] = title
	return pr
}

// Closed and merged items are listed dimmed (#255). Those on the board come
// with it; any other number is looked up on GitHub with lookup=1, once per
// GitHub Project, and the answer is kept for a few minutes.
func TestTheSwitcherListsClosedItemsAndLooksUpTheRestOnGitHub(t *testing.T) {
	t.Parallel()
	gh := githubRepo(t)
	g := newFakeGh(t, []ghIssue{
		{Number: 10, Title: "Spec: Billing", State: "CLOSED", Labels: []string{"spec"}, SubIssues: []int{11}},
		{Number: 11, Title: "Invoice page", State: "CLOSED", Labels: []string{"ready-for-agent"}, Parent: 10},
		{Number: 2, Title: "Drift alert fires twice", State: "OPEN", Labels: []string{"needs-triage"}},
	}, nil, true)
	g.setIssue(77, map[string]any{"number": 77, "title": "Board read fails behind a proxy", "state": "closed",
		"html_url": "https://github.com/someone/shop/issues/77"})
	g.setIssue(5, map[string]any{"number": 5, "title": "First commit", "state": "closed",
		"html_url":     "https://github.com/someone/shop/pull/5",
		"pull_request": map[string]any{"merged_at": "2026-01-02T03:04:05Z"}})
	g.setIssue(6, map[string]any{"number": 6, "title": "Try a new parser", "state": "closed",
		"html_url":     "https://github.com/someone/shop/pull/6",
		"pull_request": map[string]any{"merged_at": nil}})
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, gh)

	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"10", []string{"shop spec 10 Spec: Billing [shipped] → feature 10"}},
		{"11", []string{"shop ticket 11 Invoice page [closed] → ticket 10 11"}},
		{"77", []string{"shop issue 77 Board read fails behind a proxy [closed] → github https://github.com/someone/shop/issues/77"}},
		{"#5", []string{"shop pr 5 First commit [merged] → github https://github.com/someone/shop/pull/5"}},
		{"6", []string{"shop pr 6 Try a new parser [closed] → github https://github.com/someone/shop/pull/6"}},
		{"404", []string{}},
	} {
		if got := f.switcherLookup(c, gh, tc.q); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("q %q:\n got %v\nwant %v", tc.q, got, tc.want)
		}
	}
	// 10 and 11 are on the board: only 77, 5, 6 and 404 were looked up.
	if n := g.issueCalls(); n != 4 {
		t.Errorf("lookups = %d, want 4", n)
	}
	// Asking again, or typing without the pause, calls GitHub no more.
	f.switcherLookup(c, gh, "77")
	f.switcherLookup(c, gh, "404")
	if got := f.switcherResults(c, gh, "77"); len(got) != 0 {
		t.Errorf("without lookup=1: %v, want nothing looked up", got)
	}
	if n := g.issueCalls(); n != 4 {
		t.Errorf("lookups after asking again = %d, want still 4", n)
	}
}

func TestAFailedGitHubLookupKeepsTheOpenResults(t *testing.T) {
	t.Parallel()
	gh := githubRepo(t)
	g := newFakeGh(t, []ghIssue{
		{Number: 2, Title: "Drift alert fires twice", State: "OPEN", Labels: []string{"needs-triage"}},
	}, nil, true)
	g.failIssues()
	local := realPath(t, newRepo(t, t.TempDir(), "notes"))
	scratch(t, local, "tags/spec.md", "# Tags\n")
	scratch(t, local, "tags/issues/03-colour.md", ticket("03", "Tag colour", "ready-for-agent", "None"))
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, gh)
	f.addProject(c, local)

	got := f.switcherLookup(c, gh, "3")
	if want := []string{"notes ticket 03 Tag colour → ticket tags 03"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// A failure is no answer to keep: the next query asks again.
	f.switcherLookup(c, gh, "3")
	if n := g.issueCalls(); n != 2 {
		t.Errorf("lookups = %d, want 2", n)
	}
}
