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
// lines, the place with what the board needs to show it.
func (f *fh) switcherResults(c *http.Client, project, q string) []string {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/switcher?project=" + url.QueryEscape(project) + "&q=" + url.QueryEscape(q))
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET switcher: status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Results []struct {
			Kind, ProjectName, Number, Title string
			Opens                            struct{ Place, Feature, Ticket, Ref, URL string }
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
		lines = append(lines, r.ProjectName+" "+r.Kind+" "+r.Number+" "+r.Title+" → "+where)
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
		"sso":        {}, // closed
		"3":          {}, // closed
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
		{"23", gh, []string{}}, // merged: #255
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
