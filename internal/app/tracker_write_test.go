package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tracker write path (#192, ADR 0008): fh runs a write itself, then
// the next board read shows it. Mark ready-for-agent is its first write.

func (f *fh) markReady(c *http.Client, project, ref string) (int, map[string]any) {
	f.t.Helper()
	return f.post(c, "/api/tracker/mark-ready", map[string]any{"project": project, "ref": ref})
}

var triageIssues = []ghIssue{
	{Number: 10, Title: "Spec: Login", State: "OPEN", Labels: []string{"needs-triage", "roadmap:now"}, SubIssues: []int{11}},
	{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"needs-info"}, Parent: 10},
}

// relabelled is triageIssues after #n got ready-for-agent in place of its
// triage labels, as GitHub would answer once the edit ran.
func relabelled(n int) []ghIssue {
	out := []ghIssue{}
	for _, i := range triageIssues {
		if i.Number == n {
			labels := []string{"ready-for-agent"}
			for _, l := range i.Labels {
				if l != "needs-triage" && l != "needs-info" {
					labels = append(labels, l)
				}
			}
			i.Labels = labels
		}
		out = append(out, i)
	}
	return out
}

func TestMarkReadyOnATicketRelabelsItAndTheNextBoardShowsIt(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, triageIssues, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	if l := ticketByNumber(t, featureByID(t, f.board(c, repo), "10"), "11")["label"]; l != "needs-info" {
		t.Fatalf("#11 label = %v before, want needs-info", l)
	}
	g.afterEdit(relabelled(11))

	status, body := f.markReady(c, repo, "#11")

	if status != http.StatusNoContent {
		t.Fatalf("mark ready: status %d, body %v", status, body)
	}
	if e := g.edits(); strings.TrimSpace(e) != "issue edit 11 --add-label ready-for-agent --remove-label needs-triage --remove-label needs-info" {
		t.Errorf("gh edits = %q, want the add and both removes", e)
	}
	// No refresh=1: the write itself must have expired the cached read.
	if l := ticketByNumber(t, featureByID(t, f.board(c, repo), "10"), "11")["label"]; l != "ready-for-agent" {
		t.Errorf("#11 label = %v after, want ready-for-agent", l)
	}
}

func TestMarkReadyOnASpecApprovesIt(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, triageIssues, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	if a := featureByID(t, f.board(c, repo), "10")["approved"]; a == true {
		t.Fatal("Spec #10 approved before the write")
	}
	g.afterEdit(relabelled(10))

	if status, body := f.markReady(c, repo, "#10"); status != http.StatusNoContent {
		t.Fatalf("mark ready: status %d, body %v", status, body)
	}

	if a := featureByID(t, f.board(c, repo), "10")["approved"]; a != true {
		t.Errorf("Spec #10 approved = %v after, want true", a)
	}
}

func TestAFailedWriteAnswersWithGhsOwnError(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, triageIssues, nil, true)
	g.failEdits("could not add label: 'ready-for-agent' not found")
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	status, body := f.markReady(c, repo, "#11")

	if status == http.StatusNoContent || status < 400 {
		t.Fatalf("status %d, want an error", status)
	}
	if e, _ := body["error"].(string); !strings.Contains(e, "'ready-for-agent' not found") {
		t.Errorf("error = %q, want gh's stderr in it", e)
	}
}

func TestMarkReadyRefusesARefThatIsNotASpecOrTicket(t *testing.T) {
	t.Parallel()
	repo := localBoardRepo(t)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "**Status:** draft\n")
	// A feature folder that links out of the repo is not followed.
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "spec.md"), "# Elsewhere\n")
	if err := os.Symlink(outside, filepath.Join(repo, ".scratch", "linked")); err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{"../outside.md", "README.md", ".scratch/../README.md", "#12", "", ".scratch/linked/spec.md"} {
		if status, _ := f.markReady(c, repo, ref); status != http.StatusBadRequest {
			t.Errorf("ref %q: status %d, want 400", ref, status)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(b) != "**Status:** draft\n" {
		t.Errorf("README.md changed: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "spec.md")); string(b) != "# Elsewhere\n" {
		t.Errorf("linked spec.md changed: %q", b)
	}
}

func TestMarkReadyInLocalMarkdownRewritesOnlyTheStatusLine(t *testing.T) {
	t.Parallel()
	repo := localBoardRepo(t)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	bold := ".scratch/login/issues/03-sso.md" // **Status:** needs-triage
	plain := ".scratch/login/issues/04-mfa.md"
	scratch(t, repo, "login/issues/04-mfa.md", "# 04: MFA\n\nStatus: needs-info\n\n- [ ] codes work\n")
	before := map[string]string{}
	for _, p := range []string{bold, plain} {
		b, err := os.ReadFile(filepath.Join(repo, p))
		if err != nil {
			t.Fatal(err)
		}
		before[p] = string(b)
	}

	for _, ref := range []string{bold, plain} {
		if status, body := f.markReady(c, repo, ref); status != http.StatusNoContent {
			t.Fatalf("mark ready %s: status %d, body %v", ref, status, body)
		}
	}

	want := map[string]string{
		bold:  strings.Replace(before[bold], "**Status:** needs-triage", "**Status:** ready-for-agent", 1),
		plain: strings.Replace(before[plain], "Status: needs-info", "Status: ready-for-agent", 1),
	}
	for p, w := range want {
		if b, _ := os.ReadFile(filepath.Join(repo, p)); string(b) != w {
			t.Errorf("%s =\n%q\nwant\n%q", p, b, w)
		}
	}
	login := featureByID(t, f.board(c, repo), "login")
	for _, n := range []string{"03", "04"} {
		if l := ticketByNumber(t, login, n)["label"]; l != "ready-for-agent" {
			t.Errorf("ticket %s label = %v, want ready-for-agent", n, l)
		}
	}
}

func TestMarkReadyOnALocalSpecWithoutAStatusLineAddsOne(t *testing.T) {
	t.Parallel()
	repo := localBoardRepo(t)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	if status, body := f.markReady(c, repo, ".scratch/search/spec.md"); status != http.StatusNoContent {
		t.Fatalf("mark ready: status %d, body %v", status, body)
	}

	b, _ := os.ReadFile(filepath.Join(repo, ".scratch/search/spec.md"))
	if want := "# Search\n\n**Status:** ready-for-agent\n\n## Problem Statement\n...\n"; string(b) != want {
		t.Errorf("spec.md =\n%q\nwant\n%q", b, want)
	}
	if a := featureByID(t, f.board(c, repo), "search")["approved"]; a != true {
		t.Errorf("search approved = %v, want true", a)
	}
}
