package app_test

import (
	"testing"
)

// The next-step rule (#108, Spec #107): what the Board offers for a
// Feature or a Ticket at each Stage, and what Autopilot runs next. It comes
// with each Feature and Ticket in GET /api/board.

// nextSkill is the skill of a Feature's or Ticket's next step; "" for none.
func nextSkill(item map[string]any) string {
	next, _ := item["next"].(map[string]any)
	s, _ := next["skill"].(string)
	return s
}

func TestEachFeaturesNextStepFollowsItsStage(t *testing.T) {
	repo := localBoardRepo(t)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	b := f.board(c, repo)

	for _, tc := range []struct {
		id, stage, skill, label string
		implements              bool
	}{
		{"search", "spec", "to-tickets", "Make tickets", false},
		{"login", "tickets", "implement-spec", "Implement spec", true},
		{"export", "implementing", "implement-spec", "Implement spec", true},
		{"audit", "review", "implement-spec", "Implement spec", true},
		{"billing", "shipped", "retro", "Retro", false},
	} {
		feature := featureByID(t, b, tc.id)
		if feature["stage"] != tc.stage {
			t.Fatalf("Feature %s at %v, want %s", tc.id, feature["stage"], tc.stage)
		}
		next, _ := feature["next"].(map[string]any)
		implements, _ := next["implements"].(bool)
		if nextSkill(feature) != tc.skill || next["label"] != tc.label || implements != tc.implements {
			t.Errorf("Feature at %s: next = %v, want %s (%q, implements %v)", tc.stage, next, tc.skill, tc.label, tc.implements)
		}
	}
}

func TestEachTicketsNextStepFollowsItsStage(t *testing.T) {
	repo := localBoardRepo(t)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	b := f.board(c, repo)

	for _, tc := range []struct {
		feature, ticket, stage, skill string
	}{
		{"login", "01", "tickets", "implement"},
		// Blocked, so the Board doesn't offer it, but the rule still says
		// what comes next once it's Startable.
		{"login", "02", "tickets", "implement"},
		{"export", "01", "implementing", "implement"},
		{"export", "02", "shipped", ""},
	} {
		tk := ticketByNumber(t, featureByID(t, b, tc.feature), tc.ticket)
		if tk["stage"] != tc.stage {
			t.Fatalf("Ticket %s/%s at %v, want %s", tc.feature, tc.ticket, tk["stage"], tc.stage)
		}
		if got := nextSkill(tk); got != tc.skill {
			t.Errorf("Ticket at %s: next skill %q, want %q", tc.stage, got, tc.skill)
		}
	}
}

func TestATicketAtReviewGetsCodeReviewNext(t *testing.T) {
	repo := githubRepo(t)
	fakeGh(t, []ghIssue{
		{Number: 20, Title: "Search", State: "OPEN", SubIssues: []int{21}},
		{Number: 21, Title: "Index", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 20},
	}, []map[string]any{{"number": 100, "headRefName": "21-index", "body": "Adds the index", "state": "OPEN"}}, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)

	tk := ticketByNumber(t, featureByID(t, f.board(c, repo), "20"), "21")
	if tk["stage"] != "review" || nextSkill(tk) != "code-review" || tk["startable"] != false {
		t.Errorf("Ticket #21 = %v, want at review, not Startable, next code-review", tk)
	}
}
