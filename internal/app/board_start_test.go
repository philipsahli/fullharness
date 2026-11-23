package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// skillInfo is one Agent as GET /api/agents?skill=<name> answers.
type skillInfo struct {
	ID              string `json:"id"`
	Skill           string `json:"skill"`   // the slash command to run, e.g. "/mattpocock-skills:implement"
	NoSkill         string `json:"noSkill"` // why the Agent can't run the skill
	CommandsUnknown bool   `json:"commandsUnknown"`
}

func (f *fh) skill(c *http.Client, name string) map[string]skillInfo {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/agents?skill=" + url.QueryEscape(name))
	if err != nil {
		f.t.Fatal(err)
	}
	var list []skillInfo
	if err := json.Unmarshal([]byte(readBody(f.t, resp)), &list); err != nil {
		f.t.Fatal(err)
	}
	out := map[string]skillInfo{}
	for _, a := range list {
		out[a.ID] = a
	}
	return out
}

// commands is a fake-agent step that reports the Agent's slash commands.
func commands(names ...string) map[string]any { return map[string]any{"commands": names} }

func TestSkillsResolvePerAgentFromItsSlashCommands(t *testing.T) {
	f := startFhWithAgents(t, map[string]script{
		// A plugin skill, plus a longer name that must not match "implement".
		"claude":   {"onStart": []any{commands("mattpocock-skills:implement-spec", "mattpocock-skills:implement")}},
		"copilot":  {"onStart": []any{commands("grill-me", "implement-spec")}},
		"opencode": {"onStart": []any{commands("implement")}},
	}, nil)
	c := f.authorisedClient()

	for id, a := range f.skill(c, "implement") {
		if !a.CommandsUnknown || a.Skill != "" || a.NoSkill != "" {
			t.Errorf("%s before any Session = %+v, want its commands unknown", id, a)
		}
	}

	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	for _, id := range []string{"claude", "copilot"} {
		branch := "try-" + id
		sid := f.startSession(c, map[string]any{"project": repo, "branch": branch, "agent": id})
		waitState(t, tab, sid, "idle")
	}

	got := f.skill(c, "implement")
	if a := got["claude"]; a.Skill != "/mattpocock-skills:implement" || a.CommandsUnknown {
		t.Errorf("claude = %+v, want /mattpocock-skills:implement by its :implement suffix", a)
	}
	if a := got["copilot"]; a.Skill != "" || a.NoSkill != "`/implement` isn't installed for Copilot CLI" {
		t.Errorf("copilot = %+v, want it disabled: no implement among its commands", a)
	}
	if a := got["opencode"]; !a.CommandsUnknown {
		t.Errorf("opencode = %+v, want its commands still unknown: none of its Sessions ran", a)
	}

	f.stop()
	restarted := startFhWith(t, f.opts)
	if a := restarted.skill(restarted.authorisedClient(), "implement")["claude"]; a.Skill != "/mattpocock-skills:implement" {
		t.Errorf("after a restart claude = %+v, want its commands kept in state.json", a)
	}
}

// loginSpecRepo is a GitHub Project with Spec #1 and a Startable Ticket #2.
func loginSpecRepo(t *testing.T) string {
	t.Helper()
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 1, Title: "Spec: Login", State: "OPEN", SubIssues: []int{2}},
		{Number: 2, Title: "Login form (with OAuth!)", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1},
	}, nil, true)
	return repo
}

func TestTheBoardNamesEachTicketsBranchAndReference(t *testing.T) {
	gh := loginSpecRepo(t)
	local := realPath(t, newRepo(t, t.TempDir(), "notes"))
	scratch(t, local, "login/spec.md", "# Login\n")
	scratch(t, local, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, gh)
	f.addProject(c, local)

	spec := featureByID(t, f.board(c, gh), "1")
	if spec["branch"] != "1-spec-login" || spec["ref"] != "#1" {
		t.Errorf("GitHub Spec branch, ref = %v, %v; want 1-spec-login, #1", spec["branch"], spec["ref"])
	}
	if tk := ticketByNumber(t, spec, "2"); tk["branch"] != "2-login-form-with-oauth" || tk["ref"] != "#2" {
		t.Errorf("GitHub Ticket branch, ref = %v, %v; want 2-login-form-with-oauth, #2", tk["branch"], tk["ref"])
	}

	login := featureByID(t, f.board(c, local), "login")
	if login["branch"] != "login" || login["ref"] != ".scratch/login/spec.md" {
		t.Errorf("local Spec branch, ref = %v, %v; want login, .scratch/login/spec.md", login["branch"], login["ref"])
	}
	// The branch is the file's name, which the board's own rule matches.
	if tk := ticketByNumber(t, login, "01"); tk["branch"] != "01-form" || tk["ref"] != ".scratch/login/issues/01-form.md" {
		t.Errorf("local Ticket branch, ref = %v, %v; want 01-form, .scratch/login/issues/01-form.md", tk["branch"], tk["ref"])
	}
}

func TestAlongBranchNameIsCutAtAWord(t *testing.T) {
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 7, Title: "Show every Worktree's uncommitted files and commits ahead of main", State: "OPEN", Labels: []string{"ready-for-agent"}},
	}, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	got, _ := featureByID(t, f.board(c, repo), "7")["branch"].(string)

	if got != "7-show-every-worktree-s-uncommitted-files" {
		t.Errorf("branch = %q, want the slug cut to 40 characters at a word", got)
	}
}

func TestASessionStartedFromATicketIsLinkedAndMakesItImplementing(t *testing.T) {
	repo := loginSpecRepo(t)
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "branch": "2-login-form", "agent": "claude",
		"link": map[string]any{"feature": "1", "ticket": "2"}})
	sum := waitState(t, tab, id, "idle")

	if got := asJSON(sum["link"]); got != `{"feature":"1","ticket":"2"}` {
		t.Errorf("Session link = %s, want Feature 1, Ticket 2", got)
	}
	tk := ticketByNumber(t, featureByID(t, boardRefresh(t, f, c, repo), "1"), "2")
	if tk["stage"] != "implementing" || tk["startable"] != false {
		t.Errorf("Ticket #2 = %v, want Implementing and not Startable: a Session is linked, though nothing is committed", tk)
	}

	// Still linked once the Session is Exited, after a restart.
	f.stop()
	restarted := startFhWith(t, f.opts)
	rc := restarted.authorisedClient()
	sessions := objects(t, restarted.state(rc), "sessions")
	if len(sessions) != 1 || asJSON(sessions[0]["link"]) != `{"feature":"1","ticket":"2"}` {
		t.Errorf("sessions after restart = %v, want the Session still linked", sessions)
	}
	if s := ticketByNumber(t, featureByID(t, boardRefresh(t, restarted, rc, repo), "1"), "2")["stage"]; s != "implementing" {
		t.Errorf("Ticket #2 after restart = %v, want Implementing: an Exited Session still counts", s)
	}
}

func TestASessionLinkedOnlyToAFeatureLeavesItsTicketsAlone(t *testing.T) {
	repo := loginSpecRepo(t)
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "branch": "1-spec-login", "agent": "claude",
		"link": map[string]any{"feature": "1"}})
	waitState(t, tab, id, "idle")

	if tk := ticketByNumber(t, featureByID(t, boardRefresh(t, f, c, repo), "1"), "2"); tk["startable"] != true {
		t.Errorf("Ticket #2 = %v, want still Startable: Implement spec links the Feature, not the Ticket", tk)
	}
}

func TestALinkWithoutAFeatureIsRefused(t *testing.T) {
	repo := loginSpecRepo(t)
	f := startFhWithAgent(t, script{})
	c := f.authorisedClient()
	f.addProject(c, repo)

	status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "branch": "2-x", "agent": "claude",
		"link": map[string]any{"ticket": "2"}})

	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "Feature") {
		t.Errorf("status %d (%v), want 400 asking for the Feature", status, body)
	}
}
