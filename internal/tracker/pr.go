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

// Open: p is Open or Draft, the only PRs that count as work in progress
// for Stage and Startable.
func (p PR) Open() bool {
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

func (p PR) checks() string {
	if len(p.Checks) == 0 {
		return ""
	}
	return ChecksPassed
}

// records are prs's records, newest (highest number) first; never nil, so
// the UI can tell "No PR yet" from a list.
func records(prs []PR) []PRRecord {
	out := []PRRecord{}
	for _, p := range prs {
		out = append(out, p.Record())
	}
	slices.SortFunc(out, func(a, b PRRecord) int { return b.Number - a.Number })
	return out
}
