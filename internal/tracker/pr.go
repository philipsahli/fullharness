package tracker

import "slices"

// PR is one pull request as `gh pr list --json` returns it (FetchPRs).
type PR struct {
	Number      int     `json:"number"`
	HeadRefName string  `json:"headRefName"`
	Body        string  `json:"body"`
	State       string  `json:"state"` // OPEN, CLOSED or MERGED
	IsDraft     bool    `json:"isDraft"`
	URL         string  `json:"url"`
	Checks      []Check `json:"statusCheckRollup"`
}

// Check is one item of a PR's check rollup: a CheckRun (GitHub Actions)
// has Status and Conclusion, a StatusContext (commit status) has State.
type Check struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// PR states (GLOSSARY.md, PR state), as fh serves them.
const (
	PRDraft  = "draft"
	PROpen   = "open"
	PRMerged = "merged"
	PRClosed = "closed"
)

// Checks results, as fh serves them; empty means the PR has no checks.
const (
	ChecksPassed  = "passed"
	ChecksFailed  = "failed"
	ChecksRunning = "running"
)

// PRRecord is a Pull request as the UI gets it: no GitHub-specific rules
// are left for the client.
type PRRecord struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`  // a PR state: PRDraft, PROpen, PRMerged or PRClosed
	Checks string `json:"checks"` // ChecksPassed, ChecksFailed, ChecksRunning or empty
}

// Record is p as the UI gets it.
func (p PR) Record() PRRecord {
	return PRRecord{Number: p.Number, URL: p.URL, State: p.state(), Checks: p.checks()}
}

// Unmerged: p is Open or Draft, the only PRs that count as work in progress
// for Stage and Startable.
func (p PR) Unmerged() bool {
	return p.State == "OPEN"
}

func (p PR) state() string {
	switch {
	case p.State == "MERGED":
		return PRMerged
	case p.State == "CLOSED":
		return PRClosed
	case p.IsDraft:
		return PRDraft
	default:
		return PROpen
	}
}

// checks takes the rollup as one result (#127): any failed check wins,
// then any still running, else passed; empty when there are no checks.
func (p PR) checks() string {
	if len(p.Checks) == 0 {
		return ""
	}
	if slices.ContainsFunc(p.Checks, Check.failed) {
		return ChecksFailed
	}
	if slices.ContainsFunc(p.Checks, Check.running) {
		return ChecksRunning
	}
	return ChecksPassed
}

// failed: a CheckRun's conclusion, or a StatusContext's state, says it
// failed. ERROR and STARTUP_FAILURE are GitHub's other words for a failed
// run, so they count too.
func (c Check) failed() bool {
	switch c.Conclusion + c.State {
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "ERROR":
		return true
	}
	return false
}

// running: a CheckRun that hasn't completed (queued, in progress, waiting
// or pending), or a StatusContext that is pending or expected.
func (c Check) running() bool {
	if c.State != "" {
		return c.State == "PENDING" || c.State == "EXPECTED"
	}
	return c.Status != "COMPLETED"
}

// linkPRs sets t.PRs to the records of the prs that linked keeps, newest
// (highest number) first, and t.UnmergedPR when one of them is Open or Draft.
// PRs is never nil, so the UI can tell "No PR yet" from a list.
func (t *Ticket) linkPRs(prs []PR, linked func(PR) bool) {
	t.PRs = []PRRecord{}
	t.UnmergedPR = false
	for _, p := range prs {
		if linked(p) {
			t.PRs = append(t.PRs, p.Record())
			t.UnmergedPR = t.UnmergedPR || p.Unmerged()
		}
	}
	slices.SortFunc(t.PRs, func(a, b PRRecord) int { return b.Number - a.Number })
}

// BranchPR is the Pull request of branch among prs (GLOSSARY.md: it
// belongs to the Session whose Worktree has that branch): the Open or Draft
// one if any, else the most recent; ok is false when branch has none.
func BranchPR(prs []PR, branch string) (r PRRecord, ok bool) {
	var best PR
	for _, p := range prs {
		if p.HeadRefName != branch {
			continue
		}
		unmergedWins := p.Unmerged() && !best.Unmerged()
		newer := p.Unmerged() == best.Unmerged() && p.Number > best.Number
		if !ok || unmergedWins || newer {
			best, ok = p, true
		}
	}
	return best.Record(), ok
}
