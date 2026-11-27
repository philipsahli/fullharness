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
	t.Parallel()
	spec := prBoard(t, ghPR(21, "2-form", "OPEN", checkRun("COMPLETED", "SUCCESS"), statusContext("SUCCESS")))

	if got := asJSON(ticketByNumber(t, spec, "2")["prs"]); got != `[{"checks":"passed","number":21,"state":"open","url":"https://github.com/someone/shop/pull/21"}]` {
		t.Errorf("#2 prs = %s, want PR #21 open, checks passed", got)
	}
	if got := asJSON(ticketByNumber(t, spec, "3")["prs"]); got != `[]` {
		t.Errorf("#3 prs = %s, want [] (no PR yet)", got)
	}
}

func TestATicketListsEveryPRNewestFirstInEachPRState(t *testing.T) {
	t.Parallel()
	draft := ghPR(30, "4-draft", "OPEN")
	draft["isDraft"] = true
	spec := prBoard(t,
		ghPR(22, "3-retry", "CLOSED"), ghPR(25, "3-retry-again", "OPEN"),
		draft, ghPR(31, "5-done", "MERGED"))

	for n, want := range map[string]string{
		"3": `[{"checks":"","number":25,"state":"open","url":"https://github.com/someone/shop/pull/25"},{"checks":"","number":22,"state":"closed","url":"https://github.com/someone/shop/pull/22"}]`,
		"4": `[{"checks":"","number":30,"state":"draft","url":"https://github.com/someone/shop/pull/30"}]`,
		"5": `[{"checks":"","number":31,"state":"merged","url":"https://github.com/someone/shop/pull/31"}]`,
	} {
		if got := asJSON(ticketByNumber(t, spec, n)["prs"]); got != want {
			t.Errorf("#%s prs = %s, want %s", n, got, want)
		}
	}
}

func TestEachCheckRollupGivesOneChecksResult(t *testing.T) {
	t.Parallel()
	spec := prBoard(t,
		ghPR(20, "2-a", "OPEN", checkRun("COMPLETED", "SUCCESS"), checkRun("COMPLETED", "SKIPPED"), statusContext("SUCCESS")),
		ghPR(21, "3-a", "OPEN", checkRun("COMPLETED", "SUCCESS"), checkRun("COMPLETED", "FAILURE"), checkRun("IN_PROGRESS", "")),
		ghPR(22, "4-a", "OPEN", checkRun("COMPLETED", "CANCELLED")),
		ghPR(23, "5-a", "OPEN", checkRun("COMPLETED", "TIMED_OUT")),
		ghPR(24, "6-a", "OPEN", checkRun("COMPLETED", "ACTION_REQUIRED")),
		ghPR(25, "7-a", "OPEN", statusContext("ERROR")),
		ghPR(26, "8-a", "OPEN", checkRun("COMPLETED", "SUCCESS"), checkRun("QUEUED", "")),
		ghPR(27, "9-a", "OPEN", checkRun("COMPLETED", "SUCCESS"), statusContext("PENDING")),
	)

	for n, want := range map[string]string{
		"2": "passed", "3": "failed", "4": "failed", "5": "failed",
		"6": "failed", "7": "failed", "8": "running", "9": "running",
	} {
		prs, _ := ticketByNumber(t, spec, n)["prs"].([]any)
		if len(prs) != 1 {
			t.Fatalf("#%s prs = %v, want one", n, prs)
		}
		if got := prs[0].(map[string]any)["checks"]; got != want {
			t.Errorf("#%s checks = %v, want %s", n, got, want)
		}
	}
}

func TestAMergedOrClosedPRLeavesStageAndStartableAlone(t *testing.T) {
	t.Parallel()
	spec := prBoard(t, ghPR(40, "2-old", "MERGED"), ghPR(41, "3-gone", "CLOSED"), ghPR(42, "4-live", "OPEN"))

	for n, want := range map[string]struct {
		stage     string
		startable bool
	}{"2": {"tickets", true}, "3": {"tickets", true}, "4": {"review", false}} {
		tk := ticketByNumber(t, spec, n)
		if tk["stage"] != want.stage || tk["startable"] != want.startable {
			t.Errorf("#%s stage, startable = %v, %v, want %s, %v", n, tk["stage"], tk["startable"], want.stage, want.startable)
		}
	}
}
