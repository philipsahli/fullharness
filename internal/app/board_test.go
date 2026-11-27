package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scratch writes a local markdown tracker file under repo/.scratch.
func scratch(t *testing.T, repo, path, content string) {
	t.Helper()
	full := filepath.Join(repo, ".scratch", path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, full, content)
}

func ticket(number, title, status, blockedBy string) string {
	return "# " + number + ": " + title + "\n\n**What to build:** something\n\n**Blocked by:** " + blockedBy +
		"\n\n**Status:** " + status + "\n\n- [ ] it works\n"
}

func (f *fh) board(c *http.Client, project string) map[string]any {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/board?project=" + url.QueryEscape(project))
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET board: status %d: %s", resp.StatusCode, body)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func featureByID(t *testing.T, board map[string]any, id string) map[string]any {
	t.Helper()
	for _, f := range objects(t, board, "features") {
		if f["id"] == id {
			return f
		}
	}
	t.Fatalf("no feature %q in %v", id, board["features"])
	return nil
}

func ticketByNumber(t *testing.T, feature map[string]any, n string) map[string]any {
	t.Helper()
	for _, tk := range objects(t, feature, "tickets") {
		if tk["number"] == n {
			return tk
		}
	}
	t.Fatalf("no ticket %s in %v", n, feature["tickets"])
	return nil
}

// localBoardRepo builds a Project whose .scratch covers every Stage.
func localBoardRepo(t *testing.T) string {
	t.Helper()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	// Spec only, no Tickets yet.
	scratch(t, repo, "search/spec.md", "# Search\n\n## Problem Statement\n...\n")
	// Tickets: 01 Startable, 02 blocked by open 01, 03 blocked by an unknown 09.
	scratch(t, repo, "login/spec.md", "# Login\n\n## Problem Statement\n...\n")
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None (can start immediately)"))
	scratch(t, repo, "login/issues/02-session.md", ticket("02", "Session cookie", "ready-for-agent", "01"))
	scratch(t, repo, "login/issues/03-sso.md", ticket("03", "SSO", "needs-triage", "09"))
	// Implementing: ticket 01 has a branch 1-… with a commit ahead of main.
	scratch(t, repo, "export/spec.md", "# Export\n")
	scratch(t, repo, "export/issues/01-csv.md", ticket("01", "CSV export", "ready-for-agent", "None"))
	scratch(t, repo, "export/issues/02-pdf.md", ticket("02", "PDF export", "done", "None"))
	// Review: all Tickets done, Spec still open.
	scratch(t, repo, "audit/spec.md", "# Audit log\n")
	scratch(t, repo, "audit/issues/01-log.md", ticket("01", "Write log", "done", "None"))
	// Shipped: Spec done.
	scratch(t, repo, "billing/spec.md", "# Billing\n\n**Status:** done\n")
	scratch(t, repo, "billing/issues/01-invoice.md", ticket("01", "Invoice", "done", "None"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "specs")
	git(t, repo, "checkout", "-q", "-b", "1-csv-export")
	commitFile(t, repo, "csv.go", "package main\n")
	git(t, repo, "checkout", "-q", "main")
	return repo
}

func TestALocalMarkdownBoardPutsEachFeatureAtItsStage(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := localBoardRepo(t)
	f.addProject(c, repo)

	b := f.board(c, repo)

	if b["tracker"] != "local" {
		t.Fatalf("tracker = %v, want local", b["tracker"])
	}
	for id, stage := range map[string]string{
		"search": "spec", "login": "tickets", "export": "implementing", "audit": "review", "billing": "shipped",
	} {
		if got := featureByID(t, b, id)["stage"]; got != stage {
			t.Errorf("feature %s at %v, want %s", id, got, stage)
		}
	}
	if title := featureByID(t, b, "audit")["title"]; title != "Audit log" {
		t.Errorf("title = %v, want the spec's heading", title)
	}
	if note := featureByID(t, b, "audit")["note"]; !strings.Contains(note.(string), "close the Spec") {
		t.Errorf("audit note = %v, want a hint to close the Spec", note)
	}
}

func TestLocalTicketsShowLabelStageBlockersAndStartable(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := localBoardRepo(t)
	f.addProject(c, repo)

	login := featureByID(t, f.board(c, repo), "login")

	t01, t02, t03 := ticketByNumber(t, login, "01"), ticketByNumber(t, login, "02"), ticketByNumber(t, login, "03")
	if t01["title"] != "Login form" || t01["label"] != "ready-for-agent" || t01["stage"] != "tickets" || t01["startable"] != true {
		t.Errorf("01 = %v, want Startable ready-for-agent at Tickets", t01)
	}
	if t02["startable"] != false || !strings.Contains(asJSON(t02["blockers"]), `"number":"01"`) {
		t.Errorf("02 = %v, want not Startable, blocked by 01", t02)
	}
	if t03["startable"] != false || !strings.Contains(asJSON(t03["blockers"]), `"known":false`) {
		t.Errorf("03 = %v, want an unknown blocker 09 and not Startable", t03)
	}
	breakdown := login["breakdown"].(map[string]any)
	if breakdown["tickets"] != float64(3) {
		t.Errorf("breakdown = %v, want 3 at Tickets", breakdown)
	}
}

func TestABranchAheadMakesItsTicketImplementing(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := localBoardRepo(t)
	f.addProject(c, repo)

	export := featureByID(t, f.board(c, repo), "export")

	if s := ticketByNumber(t, export, "01")["stage"]; s != "implementing" {
		t.Errorf("01 stage = %v, want implementing (branch 1-csv-export is ahead)", s)
	}
	if s := ticketByNumber(t, export, "02")["stage"]; s != "shipped" {
		t.Errorf("02 stage = %v, want shipped (done)", s)
	}
}

func TestTrackerDetection(t *testing.T) {
	fakeGh(t, nil, nil, false) // GitHub cases must never reach the real gh
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	parent := realPath(t, t.TempDir())
	cases := map[string]struct {
		setup func(repo string)
		want  string
	}{
		"declared local": {func(repo string) {
			writeDoc(t, repo, "# Issue tracker: Local Markdown\n")
		}, "local"},
		"declared GitHub": {func(repo string) {
			writeDoc(t, repo, "# Issue tracker: GitHub\n")
		}, "github"},
		"declared GitLab": {func(repo string) {
			writeDoc(t, repo, "# Issue tracker: GitLab\n")
		}, "unsupported"},
		"inferred from .scratch": {func(repo string) {
			scratch(t, repo, "x/spec.md", "# X\n")
		}, "local"},
		"inferred from a GitHub remote": {func(repo string) {
			git(t, repo, "remote", "add", "origin", "git@github.com:someone/shop.git")
		}, "github"},
		"nothing": {func(string) {}, "none"},
	}
	for name, tc := range cases {
		repo := newRepo(t, parent, strings.ReplaceAll(name, " ", "-"))
		tc.setup(repo)
		f.addProject(c, repo)
		b := f.board(c, repo)
		if b["tracker"] != tc.want {
			t.Errorf("%s: tracker = %v, want %s", name, b["tracker"], tc.want)
		}
		if tc.want == "unsupported" && !strings.Contains(b["note"].(string), "not supported in v1") {
			t.Errorf("%s: note = %v, want 'not supported in v1'", name, b["note"])
		}
	}
}

func writeDoc(t *testing.T, repo, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "docs", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "docs", "agents", "issue-tracker.md"), content)
}

// joined lists field of each object in board[list], comma-separated, for a
// test that checks which items a list holds and in what order.
func joined(t *testing.T, board map[string]any, list, field string) string {
	t.Helper()
	var out []string
	for _, o := range objects(t, board, list) {
		out = append(out, o[field].(string))
	}
	return strings.Join(out, ",")
}

func asJSON(v any) string {
	b, _ := json.Marshal(v) // test helper: values come from decoded JSON, which always re-encodes
	return string(b)
}

func TestBlockedByReadsOnlyLeadingNumbersAndMatchesByValue(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "auth/spec.md", "# Auth\n")
	scratch(t, repo, "auth/issues/01-base.md", ticket("01", "Base", "done", "None"))
	scratch(t, repo, "auth/issues/02-oauth.md", ticket("02", "OAuth", "ready-for-agent", "01 (OAuth 2 flow), since 2026-11-22"))
	scratch(t, repo, "auth/issues/100-late.md", ticket("100", "Late", "ready-for-agent", "5"))
	scratch(t, repo, "auth/issues/05-mid.md", ticket("05", "Mid", "needs-triage", "None"))
	f.addProject(c, repo)

	auth := featureByID(t, f.board(c, repo), "auth")

	oauth := ticketByNumber(t, auth, "02")
	if got := asJSON(oauth["blockers"]); got != `[{"known":true,"number":"01","open":false}]` {
		t.Errorf("02 blockers = %s, want only 01, closed", got)
	}
	if oauth["startable"] != true {
		t.Errorf("02 = %v, want Startable: its only blocker is done", oauth)
	}
	if got := asJSON(ticketByNumber(t, auth, "100")["blockers"]); got != `[{"known":true,"number":"05","open":true}]` {
		t.Errorf("100 blockers = %s, want 05 (known, open)", got)
	}
}

func TestAClosedSpecShipsEvenWithOpenTicketsAndPlainStatusLinesWork(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "old/spec.md", "# Old\n\nStatus: done\n")
	scratch(t, repo, "old/issues/01-left.md", "# 01: Left over\n\nStatus: ready-for-agent\n")
	f.addProject(c, repo)

	old := featureByID(t, f.board(c, repo), "old")

	if old["stage"] != "shipped" {
		t.Errorf("stage = %v, want shipped (Spec closed)", old["stage"])
	}
	if l := ticketByNumber(t, old, "01")["label"]; l != "ready-for-agent" {
		t.Errorf("label from a plain Status: line = %v, want ready-for-agent", l)
	}
}

func TestIssueFilesInAFolderWithoutASpecAreUntriaged(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "login/spec.md", "# Login\n")
	scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
	scratch(t, repo, "x/issues/01-a.md", ticket("01", "Crash on start", "needs-triage", "None"))
	f.addProject(c, repo)

	b := f.board(c, repo)

	want := `[{"labels":["needs-triage"],"ref":".scratch/x/issues/01-a.md","title":"Crash on start"}]`
	if got := asJSON(b["untriaged"]); got != want {
		t.Errorf("untriaged = %s\nwant %s", got, want)
	}
}

func TestAClosedIssueFileWithoutASpecIsNotUntriaged(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "x/issues/01-a.md", ticket("01", "Crash on start", "needs-triage", "None"))
	scratch(t, repo, "x/issues/02-b.md", ticket("02", "Fixed already", "done", "None"))
	scratch(t, repo, "x/issues/03-c.md", ticket("03", "Won't do", "closed", "None"))
	f.addProject(c, repo)

	if got := joined(t, f.board(c, repo), "untriaged", "ref"); got != ".scratch/x/issues/01-a.md" {
		t.Errorf("untriaged refs = %s, want only the open 01-a.md", got)
	}
}

func TestLocalUntriagedIssuesAreNewestFirstByFileName(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "x/issues/01-a.md", ticket("01", "First", "needs-triage", "None"))
	scratch(t, repo, "x/issues/03-c.md", ticket("03", "Third", "needs-info", "None"))
	scratch(t, repo, "y/issues/02-b.md", ticket("02", "Second", "", "None"))
	f.addProject(c, repo)

	b := f.board(c, repo)

	want := ".scratch/x/issues/03-c.md,.scratch/y/issues/02-b.md,.scratch/x/issues/01-a.md"
	if got := joined(t, b, "untriaged", "ref"); got != want {
		t.Errorf("untriaged refs = %s\nwant %s", got, want)
	}
	if note := b["note"]; note != "No Specs yet; 3 issues need triage" {
		t.Errorf("note = %q, want %q", note, "No Specs yet; 3 issues need triage")
	}
}

func TestLocalSpecAndTicketNumbersLinkToTheirFilesInVSCode(t *testing.T) {
	// The links name files on this machine, so the remote doesn't matter
	// (#102): no remote, a GitLab remote and a GitHub remote all get them.
	for name, origin := range map[string]string{
		"no remote": "",
		"GitLab":    "git@gitlab.com:someone/shop.git",
		"GitHub":    "git@github.com:someone/shop.git",
	} {
		t.Run(name, func(t *testing.T) {
			fakeGh(t, nil, nil, true) // a GitHub remote makes the local board read PRs
			f := startFh(t, t.TempDir())
			c := f.authorisedClient()
			repo := localBoardRepo(t)
			if origin != "" {
				git(t, repo, "remote", "add", "origin", origin)
			}
			f.addProject(c, repo)

			b := f.board(c, repo)

			if b["tracker"] != "local" {
				t.Fatalf("tracker = %v, want local", b["tracker"])
			}
			if !filepath.IsAbs(repo) {
				t.Fatalf("repo %s is not absolute: the URLs below would not be either", repo)
			}
			login := featureByID(t, b, "login")
			want := "vscode://file" + repo + "/.scratch/login/spec.md"
			if login["url"] != want {
				t.Errorf("Spec url = %v, want %s", login["url"], want)
			}
			want = "vscode://file" + repo + "/.scratch/login/issues/02-session.md"
			if got := ticketByNumber(t, login, "02")["url"]; got != want {
				t.Errorf("Ticket 02 url = %v, want %s", got, want)
			}
		})
	}
}
