package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Moving a Feature on the Roadmap (#215, ADR 0005): fh writes the new
// Roadmap group to the Spec at once, and the next read shows it.

func (f *fh) moveRoadmap(c *http.Client, project, ref, group string) (int, map[string]any) {
	f.t.Helper()
	return f.post(c, "/api/tracker/roadmap", map[string]any{"project": project, "ref": ref, "group": group})
}

var roadmapIssues = []ghIssue{
	{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"ready-for-agent", "roadmap:next"}},
	{Number: 20, Title: "Search", State: "OPEN", Labels: []string{"ready-for-agent"}},
}

// regrouped is roadmapIssues after #n moved to group, as GitHub would
// answer once the edit ran.
func regrouped(n int, group string) []ghIssue {
	out := []ghIssue{}
	for _, i := range roadmapIssues {
		if i.Number == n {
			labels := []string{}
			for _, l := range i.Labels {
				if !strings.HasPrefix(l, "roadmap:") {
					labels = append(labels, l)
				}
			}
			i.Labels = append(labels, "roadmap:"+group)
		}
		out = append(out, i)
	}
	return out
}

func TestMovingAGitHubFeatureSwapsItsRoadmapLabelAndTheNextReadShowsIt(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, roadmapIssues, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	if got := f.roadmap(c).groups()["shop/10"]; got != "next" {
		t.Fatalf("#10 in %q before, want next", got)
	}
	g.afterEdit(regrouped(10, "now"))

	status, body := f.moveRoadmap(c, repo, "#10", "now")

	if status != http.StatusNoContent {
		t.Fatalf("move: status %d, body %v", status, body)
	}
	if e := strings.TrimSpace(g.edits()); e != "issue edit 10 --add-label roadmap:now --remove-label roadmap:next" {
		t.Errorf("gh edits = %q, want the add of the new group and the remove of the old", e)
	}
	if l := g.labelsCreated(); l != "" {
		t.Errorf("labels created = %q, want none: roadmap:now exists", l)
	}
	// No refresh: the write itself must have expired the cached read.
	if got := f.roadmap(c).groups()["shop/10"]; got != "now" {
		t.Errorf("#10 in %q after, want now", got)
	}
}

func TestMovingAFeatureWithNoGroupOnlyAddsTheLabel(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, roadmapIssues, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	f.roadmap(c) // the move reads #20's group from the board fh has read
	g.afterEdit(regrouped(20, "next"))

	if status, body := f.moveRoadmap(c, repo, "#20", "next"); status != http.StatusNoContent {
		t.Fatalf("move: status %d, body %v", status, body)
	}

	if e := strings.TrimSpace(g.edits()); e != "issue edit 20 --add-label roadmap:next" {
		t.Errorf("gh edits = %q, want only the add: #20 had no roadmap label", e)
	}
	if got := f.roadmap(c).groups()["shop/20"]; got != "next" {
		t.Errorf("#20 in %q after, want next", got)
	}
}

func TestTheFirstMoveToAGroupCreatesItsLabel(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, roadmapIssues, nil, true)
	g.missingLabels("roadmap:now")
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	g.afterEdit(regrouped(10, "now"))

	status, body := f.moveRoadmap(c, repo, "#10", "now")

	if status != http.StatusNoContent {
		t.Fatalf("move: status %d, body %v", status, body)
	}
	if l := g.labelsCreated(); !strings.HasPrefix(strings.TrimSpace(l), "label create roadmap:now ") {
		t.Errorf("labels created = %q, want roadmap:now", l)
	}
	edits := strings.Split(strings.TrimSpace(g.edits()), "\n")
	if last := edits[len(edits)-1]; last != "issue edit 10 --add-label roadmap:now --remove-label roadmap:next" {
		t.Errorf("last gh edit = %q, want the move run again once the label exists", last)
	}
	if got := f.roadmap(c).groups()["shop/10"]; got != "now" {
		t.Errorf("#10 in %q after, want now", got)
	}
}

func TestAFailedMoveAnswersWithGhsOwnErrorAndLeavesTheGroup(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	g := newFakeGh(t, roadmapIssues, nil, true)
	g.failEdits("HTTP 403: Resource not accessible by personal access token (addLabelsToLabelable)")
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	status, body := f.moveRoadmap(c, repo, "#10", "now")

	if status < 400 {
		t.Fatalf("status %d, want an error", status)
	}
	if e, _ := body["error"].(string); !strings.Contains(e, "Resource not accessible") {
		t.Errorf("error = %q, want gh's stderr in it", e)
	}
	if l := g.labelsCreated(); l != "" {
		t.Errorf("labels created = %q, want none for an error that isn't a missing label", l)
	}
	if got := f.roadmap(c).groups()["shop/10"]; got != "next" {
		t.Errorf("#10 in %q after a failed move, want next", got)
	}
}

func TestAMoveRefusesAnUnknownGroupOrARefThatIsNotASpec(t *testing.T) {
	t.Parallel()
	repo := localBoardRepo(t)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	for _, tc := range []struct{ ref, group string }{
		{".scratch/login/spec.md", "soon"},
		{".scratch/login/spec.md", ""},
		{".scratch/login/issues/01-form.md", "now"}, // a Ticket has no Roadmap group
		{"../spec.md", "now"},
		{"#10", "now"},
	} {
		if status, _ := f.moveRoadmap(c, repo, tc.ref, tc.group); status != http.StatusBadRequest {
			t.Errorf("ref %q group %q: status %d, want 400", tc.ref, tc.group, status)
		}
	}
}

func TestMovingALocalFeatureRewritesOnlyItsRoadmapLine(t *testing.T) {
	t.Parallel()
	repo := realPath(t, newRepo(t, t.TempDir(), "notes"))
	files := map[string]struct{ before, after string }{
		// Bold, with text on both sides and a CRLF line.
		"onboarding": {
			"# Onboarding\r\n\r\n**Status:** ready-for-agent\r\n**Roadmap:** Later\r\n\r\n## Problem\r\n...\r\n",
			"# Onboarding\r\n\r\n**Status:** ready-for-agent\r\n**Roadmap:** now\r\n\r\n## Problem\r\n...\r\n",
		},
		// Plain.
		"themes": {
			"# Themes\n\nRoadmap: next\n\n## Problem\n- Roadmap: not this one\n",
			"# Themes\n\nRoadmap: now\n\n## Problem\n- Roadmap: not this one\n",
		},
		// No line yet: a bold one goes after the heading.
		"tags": {
			"# Tags\n\n## Problem\n...\n",
			"# Tags\n\n**Roadmap:** now\n\n## Problem\n...\n",
		},
	}
	for name, c := range files {
		scratch(t, repo, name+"/spec.md", c.before)
	}
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	for name := range files {
		if status, body := f.moveRoadmap(c, repo, ".scratch/"+name+"/spec.md", "now"); status != http.StatusNoContent {
			t.Fatalf("move %s: status %d, body %v", name, status, body)
		}
	}

	for name, want := range files {
		b, err := os.ReadFile(filepath.Join(repo, ".scratch", name, "spec.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want.after {
			t.Errorf("%s/spec.md = %q\nwant %q", name, b, want.after)
		}
	}
	groups := f.roadmap(c).groups()
	for name := range files {
		if groups["notes/"+name] != "now" {
			t.Errorf("%s in %q after, want now", name, groups["notes/"+name])
		}
	}
}
