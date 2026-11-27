package app_test

import (
	"testing"
)

// The spec label (#244): on GitHub an open issue labelled spec is a Spec,
// approved or not, even before it has Tickets. Without the label, an
// unapproved issue with no Tickets reads as an Untriaged issue (#196), so a
// Spec Autopilot wrote could never wait at Approve spec.

func TestAnUnapprovedIssueLabelledSpecIsAFeatureNotUntriaged(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 20, Title: "A plain bug", State: "OPEN", Labels: []string{"needs-triage"}},
		{Number: 30, Title: "Spec: Search", State: "OPEN", Labels: []string{"spec", "needs-triage", "roadmap:next"}},
		{Number: 31, Title: "Spec: Old", State: "CLOSED", Labels: []string{"spec"}},
	}, nil, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	b := f.board(c, repo)

	if ids := joined(t, b, "features", "id"); ids != "30" {
		t.Errorf("features = %v, want only 30 (a closed spec issue with no Tickets stays off, as before)", ids)
	}
	search := featureByID(t, b, "30")
	if search["approved"] == true || search["roadmap"] != "next" {
		t.Errorf("Spec #30: approved %v, roadmap %v; want unapproved, in Next", search["approved"], search["roadmap"])
	}
	if refs := joined(t, b, "untriaged", "ref"); refs != "#20" {
		t.Errorf("untriaged = %v, want only #20: a spec issue is never Untriaged", refs)
	}
}

func TestAGitHubSpecInNowWaitsAtApproveSpec(t *testing.T) {
	t.Parallel()
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 30, Title: "Spec: Search", State: "OPEN", Labels: []string{"spec", "roadmap:now"}},
	}, nil, true)
	f := startSlowAutopilotFh(t, script{"default": []any{map[string]any{"say": "ok"}}}, "asking")
	c := f.authorisedClient()
	f.addProject(c, repo)

	f.waitApproveSpec(c, repo, "30")
}
