package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

// switcherTickets asks /api/switcher which of project's open Tickets the
// Switcher lists for q, as "number title (feature)" lines.
func (f *fh) switcherTickets(c *http.Client, project, q string) []string {
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
		Tickets []struct{ Number, Title, Feature, FeatureTitle string }
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		f.t.Fatal(err)
	}
	lines := []string{}
	for _, t := range got.Tickets {
		lines = append(lines, t.Number+" "+t.Title+" ("+t.Feature+": "+t.FeatureTitle+")")
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
		"2":          {"02 Session cookie (login: Login)"},
		"#01":        {"01 Login form (login: Login)"},
		"form LOGIN": {"01 Login form (login: Login)"},
		"sso":        {}, // closed
		"3":          {}, // closed
		"":           {},
	} {
		if got := f.switcherTickets(c, repo, q); !reflect.DeepEqual(got, want) {
			t.Errorf("q %q: tickets = %v, want %v", q, got, want)
		}
	}
}

func TestTheSwitcherListsNoTicketsWithoutATracker(t *testing.T) {
	t.Parallel()
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "bare"))
	f.addProject(c, repo)

	if got := f.switcherTickets(c, repo, "1"); len(got) != 0 {
		t.Errorf("tickets = %v, want none for a Project without a tracker", got)
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
