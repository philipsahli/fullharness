package app_test

import (
	"strconv"
	"testing"
)

// ghPR is one PR as `gh pr list --json` returns it; checks are its
// statusCheckRollup items.
func ghPR(number int, branch, state string, checks ...map[string]any) map[string]any {
	if checks == nil {
		checks = []map[string]any{}
	}
	return map[string]any{"number": number, "headRefName": branch, "body": "", "state": state, "isDraft": false,
		"url": "https://github.com/someone/shop/pull/" + strconv.Itoa(number), "statusCheckRollup": checks}
}

// checkRun is a GitHub Actions check in a statusCheckRollup.
func checkRun(status, conclusion string) map[string]any {
	return map[string]any{"__typename": "CheckRun", "status": status, "conclusion": conclusion}
}

// statusContext is a commit status (an external CI) in a statusCheckRollup.
func statusContext(state string) map[string]any {
	return map[string]any{"__typename": "StatusContext", "state": state}
}

// prSpec is Spec #1 with open, ready Tickets #2 to #9.
func prSpec() []ghIssue {
	issues := []ghIssue{{Number: 1, Title: "Spec", State: "OPEN", Labels: []string{"ready-for-agent"}, SubIssues: []int{2, 3, 4, 5, 6, 7, 8, 9}}}
	for n := 2; n <= 9; n++ {
		issues = append(issues, ghIssue{Number: n, Title: "Ticket " + strconv.Itoa(n), State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 1})
	}
	return issues
}

// prBoard serves prSpec with prs through the fake gh and returns Spec #1.
func prBoard(t *testing.T, prs ...map[string]any) map[string]any {
	t.Helper()
	repo := githubRepo(t)
	fakeGh(t, prSpec(), prs, true)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, repo)
	return featureByID(t, f.board(c, repo), "1")
}

func TestATicketCarriesItsOpenPRWithURLStateAndChecks(t *testing.T) {
	spec := prBoard(t, ghPR(21, "2-form", "OPEN", checkRun("COMPLETED", "SUCCESS"), statusContext("SUCCESS")))

	if got := asJSON(ticketByNumber(t, spec, "2")["prs"]); got != `[{"checks":"passed","number":21,"state":"open","url":"https://github.com/someone/shop/pull/21"}]` {
		t.Errorf("#2 prs = %s, want PR #21 open, checks passed", got)
	}
	if got := asJSON(ticketByNumber(t, spec, "3")["prs"]); got != `[]` {
		t.Errorf("#3 prs = %s, want [] (no PR yet)", got)
	}
}
