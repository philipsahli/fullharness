package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Autopilot (#110, ADR-0009): for Features in Roadmap Now, in Projects with
// Autopilot on, fh starts the next step itself, one Session per Project at
// a time.

// startAutopilotFh starts fh with the fake Agent running s and a fake
// turn-end classifier that always answers verdict. Autopilot looks at the
// Projects every 50 ms.
func startAutopilotFh(t *testing.T, s script, verdict string) *fh {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	classifier := filepath.Join(dir, "fake-classifier")
	writeFile(t, classifier, "#!/bin/sh\ncat >/dev/null\n"+
		`echo '{"is_error":false,"result":"`+verdict+`","total_cost_usd":0,"usage":{}}'`+"\n")
	if err := os.Chmod(classifier, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, fmt.Sprintf("[agents.claude]\ncommand = [%q]\nenv = { FH_FAKE_SCRIPT = %q }\n\n"+
		"[classifier]\nname = \"fake\"\ncommand = [%q]\n", fakeAgentBin, raw, classifier))
	opts := appOptions(dir)
	opts.AutopilotInterval = 50 * time.Millisecond
	return startFhWith(t, opts)
}

func (f *fh) setAutopilot(c *http.Client, project string, on bool) {
	f.t.Helper()
	if status, body := f.post(c, "/api/projects/autopilot", map[string]any{"path": project, "on": on}); status != http.StatusNoContent {
		f.t.Fatalf("autopilot on %s: status %d (%v)", project, status, body)
	}
}

// autopilotSessions are the Sessions Autopilot started.
func (f *fh) autopilotSessions(c *http.Client) []map[string]any {
	f.t.Helper()
	var out []map[string]any
	for _, s := range objects(f.t, f.state(c), "sessions") {
		if s["autopilot"] == true {
			out = append(out, s)
		}
	}
	return out
}

// waitAutopilotSessions waits until Autopilot has started n Sessions.
func (f *fh) waitAutopilotSessions(c *http.Client, n int) []map[string]any {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.autopilotSessions(c); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("Autopilot never started %d Sessions; it has %v", n, f.autopilotSessions(c))
	return nil
}

// waitTurnEnded waits until Session id's turn ended, polling the state so a
// turn that ended before anyone looked still counts.
func (f *fh) waitTurnEnded(c *http.Client, id string) {
	f.t.Helper()
	f.waitAutopilotState(c, id, "waiting", "idle")
}

// waitAutopilotState waits until Autopilot Session id is in one of states.
func (f *fh) waitAutopilotState(c *http.Client, id string, states ...string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range f.autopilotSessions(c) {
			if st, _ := s["state"].(string); s["id"] == id && slices.Contains(states, st) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("Session %s never reached %v", id, states)
}

// noAutopilotSessionFor checks that Autopilot starts nothing while it looks
// at the Projects a few times over.
func (f *fh) noAutopilotSessionFor(c *http.Client, d time.Duration) {
	f.t.Helper()
	time.Sleep(d)
	if got := f.autopilotSessions(c); len(got) > 0 {
		f.t.Fatalf("Autopilot started %v, want nothing", got)
	}
}

func nowSpec(title string) string {
	return "# " + title + "\n\n**Roadmap:** now\n\n## Problem Statement\n...\n"
}

func TestAutopilotStartsTheNextStartableTicket(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "Opened a PR."}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "login/spec.md", nowSpec("Login"))
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	f.addProject(c, repo)

	f.setAutopilot(c, repo, true)
	s := f.waitAutopilotSessions(c, 1)[0]

	link, _ := s["link"].(map[string]any)
	if link["feature"] != "login" || link["ticket"] != "01" {
		t.Errorf("link %v, want Feature login, Ticket 01", link)
	}
	if s["autoApprove"] == true {
		t.Error("an Autopilot Session auto-approves; permission requests are Stop points")
	}
	if wt, _ := s["worktree"].(string); !strings.HasSuffix(wt, "01-form") {
		t.Errorf("worktree %q, want a new Worktree on the Ticket's branch", wt)
	}
	id, _ := s["id"].(string)
	f.waitTurnEnded(c, id)
	if got := userTexts(f.transcript(c, id)); len(got) != 1 || got[0] != "/implement .scratch/login/issues/01-form.md" {
		t.Errorf("prompt %v, want the implement skill on the Ticket", got)
	}
}

func TestAutopilotLeavesFeaturesOutsideNowAndProjectsWithItOff(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "done")
	c := f.authorisedClient()

	// Autopilot on, but the Feature is in Next.
	next := realPath(t, newRepo(t, t.TempDir(), "next"))
	scratch(t, next, "login/spec.md", "# Login\n\n**Roadmap:** next\n")
	scratch(t, next, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	f.addProject(c, next)
	f.setAutopilot(c, next, true)

	// The Feature is in Now, but Autopilot is off.
	off := realPath(t, newRepo(t, t.TempDir(), "off"))
	scratch(t, off, "login/spec.md", nowSpec("Login"))
	scratch(t, off, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	f.addProject(c, off)

	f.noAutopilotSessionFor(c, 500*time.Millisecond)
}

func TestAutopilotRunsOneSessionPerProjectAndGoesOnOnceItIsDone(t *testing.T) {
	for _, verdict := range []string{"asking", "done"} {
		t.Run(verdict, func(t *testing.T) {
			f := startAutopilotFh(t, script{"default": []any{map[string]any{"sleepMs": 200}, map[string]any{"say": "Opened a PR."}}}, verdict)
			c := f.authorisedClient()
			repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
			scratch(t, repo, "login/spec.md", nowSpec("Login"))
			scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
			scratch(t, repo, "login/issues/02-cookie.md", ticket("02", "Session cookie", "ready-for-agent", "None"))
			f.addProject(c, repo)
			f.setAutopilot(c, repo, true)

			first := f.waitAutopilotSessions(c, 1)[0]
			f.waitTurnEnded(c, first["id"].(string))

			if verdict == "asking" {
				// The Agent asks the user something: a Stop point. The
				// Project waits for the user, so the second Ticket waits too.
				time.Sleep(500 * time.Millisecond)
				if n := len(f.autopilotSessions(c)); n != 1 {
					t.Fatalf("%d Autopilot Sessions, want only the first", n)
				}
				return
			}
			second := f.waitAutopilotSessions(c, 2)
			for _, s := range second {
				if s["id"] == first["id"] && (s["state"] == "starting" || s["state"] == "working") {
					t.Errorf("the first Session is %v while the second runs", s["state"])
				}
			}
		})
	}
}

func TestTheAutopilotSwitchSurvivesARestart(t *testing.T) {
	f := startAutopilotFh(t, script{}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)

	f.stop()
	f = startFhWith(t, f.opts)
	c = f.authorisedClient()
	projects := objects(t, f.state(c), "projects")
	if len(projects) != 1 || projects[0]["autopilot"] != true {
		t.Fatalf("projects %v, want the one Project with Autopilot still on", projects)
	}

	f.setAutopilot(c, repo, false)
	if p := objects(t, f.state(c), "projects")[0]; p["autopilot"] == true {
		t.Errorf("project %v, want Autopilot off", p)
	}
}

func TestAutopilotOnUnknownProjectIsNotFound(t *testing.T) {
	f := startAutopilotFh(t, script{}, "done")
	if status, _ := f.post(f.authorisedClient(), "/api/projects/autopilot", map[string]any{"path": "/no/such", "on": true}); status != http.StatusNotFound {
		t.Errorf("status %d, want 404", status)
	}
}

func TestAutopilotNeverMergesOrClosesASpec(t *testing.T) {
	// Spec #10 in Now: its only Ticket #11 has an open PR (Review), so the
	// next step is the user's merge. Spec #20 in Now has every Ticket
	// closed, so the next step is the user closing it.
	_, unexpected, _ := fakeGhLogged(t, []ghIssue{
		{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"roadmap:now", "ready-for-agent"}, SubIssues: []int{11}},
		{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
		{Number: 20, Title: "Export", State: "OPEN", Labels: []string{"roadmap:now"}, SubIssues: []int{21}},
		{Number: 21, Title: "CSV", State: "CLOSED", Parent: 20},
	}, []map[string]any{{"number": 5, "headRefName": "11-form", "state": "OPEN"}}, true)
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "done")
	c := f.authorisedClient()
	repo := githubRepo(t)
	f.addProject(c, repo)

	board := f.board(c, repo)
	if login := featureByID(t, board, "10"); login["roadmap"] != "now" || login["approved"] != true {
		t.Errorf("Spec #10: roadmap %v, approved %v; want now and approved", login["roadmap"], login["approved"])
	}

	f.setAutopilot(c, repo, true)
	f.noAutopilotSessionFor(c, 500*time.Millisecond)
	if u := unexpected(); u != "" {
		t.Errorf("Autopilot ran gh %q; it never merges or closes", u)
	}
}

func TestAutopilotTurnsAnApprovedSpecInNowIntoTickets(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "Wrote the tickets."}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	// Not approved yet: the Spec approval is a Stop point.
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n")
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	f.noAutopilotSessionFor(c, 300*time.Millisecond)

	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	s := f.waitAutopilotSessions(c, 1)[0]
	id, _ := s["id"].(string)
	if s["worktree"] != repo {
		t.Errorf("worktree %v, want the main tree: to-tickets writes issues", s["worktree"])
	}
	f.waitTurnEnded(c, id)
	if got := userTexts(f.transcript(c, id)); len(got) != 1 || got[0] != "/to-tickets .scratch/search/spec.md" {
		t.Errorf("prompt %v, want to-tickets on the Spec", got)
	}
}

func TestAutopilotReviewsATicketInItsOwnSessionOnceItsPRIsOpen(t *testing.T) {
	_, unexpected, setPRs := fakeGhLogged(t, []ghIssue{
		{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"roadmap:now", "ready-for-agent"}, SubIssues: []int{11}},
		{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
	}, nil, true)
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"sleepMs": 300}, map[string]any{"say": "Opened PR #5."}}}, "done")
	c := f.authorisedClient()
	repo := githubRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)

	s := f.waitAutopilotSessions(c, 1)[0]
	id, _ := s["id"].(string)
	// The implement skill opens the Ticket's PR during its turn.
	setPRs([]map[string]any{{"number": 5, "headRefName": s["title"], "state": "OPEN"}})

	deadline := time.Now().Add(10 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		if got = userTexts(f.transcript(c, id)); len(got) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 2 || got[0] != "/implement #11" || got[1] != "/code-review #11" {
		t.Fatalf("turns %v, want implement, then code-review in the same Session", got)
	}
	if n := len(f.autopilotSessions(c)); n != 1 {
		t.Errorf("%d Autopilot Sessions, want the review in the Ticket's own", n)
	}
	if u := unexpected(); u != "" {
		t.Errorf("Autopilot ran gh %q", u)
	}
}

// twoTicketRepo has a Feature in Now with two independent Startable
// Tickets.
func twoTicketRepo(t *testing.T) string {
	t.Helper()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "login/spec.md", nowSpec("Login"))
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	scratch(t, repo, "login/issues/02-cookie.md", ticket("02", "Session cookie", "ready-for-agent", "None"))
	return repo
}

func TestAStopPointStillHoldsTheProjectAfterARestart(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "Which login method?"}}}, "asking")
	c := f.authorisedClient()
	repo := twoTicketRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	first := f.waitAutopilotSessions(c, 1)[0]
	f.waitTurnEnded(c, first["id"].(string))
	time.Sleep(200 * time.Millisecond) // the verdict lands after the turn ends

	f.stop()
	f = startFhWith(t, f.opts)
	c = f.authorisedClient()
	// The Session comes back Exited, but its question is still open.
	time.Sleep(500 * time.Millisecond)
	if n := len(f.autopilotSessions(c)); n != 1 {
		t.Fatalf("%d Autopilot Sessions after the restart, want the first one still waiting", n)
	}
}

func TestAnAgentThatExitsMidTurnIsAStopPoint(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"exit": 3}}}, "done")
	c := f.authorisedClient()
	repo := twoTicketRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	first := f.waitAutopilotSessions(c, 1)[0]
	f.waitAutopilotState(c, first["id"].(string), "exited")

	time.Sleep(500 * time.Millisecond)
	if n := len(f.autopilotSessions(c)); n != 1 {
		t.Fatalf("%d Autopilot Sessions, want none after the crash until the user looks", n)
	}

	// Closing the Session hands the Project back: the next Ticket starts.
	if status, body := f.post(c, "/api/sessions/"+first["id"].(string)+"/close", map[string]any{"confirm": true}); status != http.StatusNoContent {
		t.Fatalf("close: status %d (%v)", status, body)
	}
	f.waitAutopilotSessions(c, 1)
	for _, s := range f.autopilotSessions(c) {
		if link, _ := s["link"].(map[string]any); link["ticket"] != "02" {
			t.Errorf("after the close Autopilot runs %v, want Ticket 02", link)
		}
	}
}

func TestAutopilotReusesATicketsExistingWorktree(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "login/spec.md", nowSpec("Login"))
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	f.addProject(c, repo)
	// fh made a Worktree on the Ticket's branch for a Session that was
	// closed again: the branch has no commits, so the Ticket is Startable.
	byHand := f.startSession(c, map[string]any{"project": repo, "branch": "01-form", "agent": "claude"})
	if status, body := f.post(c, "/api/sessions/"+byHand+"/close", map[string]any{"confirm": true}); status != http.StatusNoContent {
		t.Fatalf("close: status %d (%v)", status, body)
	}
	wt := filepath.Join(filepath.Dir(repo), "shop.worktrees", "01-form")
	f.setAutopilot(c, repo, true)

	s := f.waitAutopilotSessions(c, 1)[0]
	if s["worktree"] != wt {
		t.Errorf("worktree %v, want the existing %s", s["worktree"], wt)
	}
}

func TestAutopilotStartsTheProjectsAgent(t *testing.T) {
	f := startAutopilotFh(t, script{}, "done")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)

	if status, _ := f.post(c, "/api/projects/autopilot", map[string]any{"path": repo, "on": true, "agent": "nope"}); status != http.StatusBadRequest {
		t.Errorf("unknown Agent: status %d, want 400", status)
	}
	if status, body := f.post(c, "/api/projects/autopilot", map[string]any{"path": repo, "on": true, "agent": "copilot"}); status != http.StatusNoContent {
		t.Fatalf("status %d (%v)", status, body)
	}
	if p := objects(t, f.state(c), "projects")[0]; p["agent"] != "copilot" {
		t.Errorf("project %v, want Agent copilot", p)
	}
}
