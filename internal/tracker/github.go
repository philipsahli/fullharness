package tracker

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Issue is one GitHub issue as fh's GraphQL query returns it.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // OPEN or CLOSED
	Body   string `json:"body"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Parent *struct {
		Number int `json:"number"`
	} `json:"parent"`
	SubIssues struct {
		Nodes []struct {
			Number int `json:"number"`
		} `json:"nodes"`
	} `json:"subIssues"`
	BlockedBy struct {
		Nodes []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"blockedBy"`
}

// PR is one open pull request.
type PR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	Body        string `json:"body"`
}

// triageRoles are the labels a Ticket's role comes from
// (docs/agents/triage-labels.md).
var triageRoles = []string{"needs-triage", "needs-info", ReadyLabel, "ready-for-human", "wontfix"}

var (
	partOf       = regexp.MustCompile(`(?i)\bpart of #(\d+)`)
	parentHead   = regexp.MustCompile(`(?im)^##\s*parent\s*\n+\s*#(\d+)`)
	bodyBlockers = regexp.MustCompile(`(?im)^\**blocked by:?\**:?\s*(.*)$`)
	issueRef     = regexp.MustCompile(`#(\d+)`)
	// closingRef matches the keywords GitHub itself reads to close an issue.
	closingRef = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)
)

// ReadGitHub builds Features from a repository's issues and open PRs
// (#12). A Spec is an issue with sub-issues, or that others name with
// "Part of #N" or a "## Parent" section; an issue with no Tickets is a Spec
// only when labelled ready-for-agent. Wayfinder issues are left out.
// aheadBranches are the Project's branches with commits ahead of its base.
func ReadGitHub(issues []Issue, prs []PR, aheadBranches []string) []Feature {
	byNum := map[int]Issue{}
	for _, i := range issues {
		if !wayfinder(i) {
			byNum[i.Number] = i
		}
	}
	children := map[int][]int{}
	hasParent := map[int]bool{}
	for _, i := range byNum {
		for _, s := range i.SubIssues.Nodes {
			if _, ok := byNum[s.Number]; ok && !slices.Contains(children[i.Number], s.Number) {
				children[i.Number] = append(children[i.Number], s.Number)
				hasParent[s.Number] = true
			}
		}
		if p := parentOf(i); p != 0 {
			if _, ok := byNum[p]; ok && !slices.Contains(children[p], i.Number) {
				children[p] = append(children[p], i.Number)
			}
			hasParent[i.Number] = true
		}
	}
	features := []Feature{}
	for _, i := range byNum {
		kids := children[i.Number]
		if len(kids) == 0 && (hasParent[i.Number] || !slices.Contains(labels(i), ReadyLabel)) {
			continue // a Ticket, or an issue that isn't part of the factory
		}
		f := Feature{ID: strconv.Itoa(i.Number), Title: i.Title, Closed: i.State == "CLOSED"}
		for _, k := range kids {
			f.Tickets = append(f.Tickets, githubTicket(byNum[k], byNum, prs, aheadBranches))
		}
		slices.SortFunc(f.Tickets, func(a, b Ticket) int { return atoi(a.Number) - atoi(b.Number) })
		features = append(features, f)
	}
	slices.SortFunc(features, func(a, b Feature) int { return atoi(a.ID) - atoi(b.ID) })
	Derive(features)
	return features
}

func githubTicket(i Issue, byNum map[int]Issue, prs []PR, aheadBranches []string) Ticket {
	t := Ticket{Number: strconv.Itoa(i.Number), Title: i.Title, Closed: i.State == "CLOSED", Blockers: []Blocker{}}
	for _, l := range labels(i) {
		if slices.Contains(triageRoles, l) {
			t.Label = l
			break
		}
	}
	// Native dependencies first; a "Blocked by: #N" line only without them.
	for _, b := range i.BlockedBy.Nodes {
		t.Blockers = append(t.Blockers, Blocker{Number: strconv.Itoa(b.Number), Known: true, Open: b.State != "CLOSED"})
	}
	if len(t.Blockers) == 0 {
		for _, m := range bodyBlockers.FindAllStringSubmatch(i.Body, -1) {
			for _, ref := range issueRef.FindAllStringSubmatch(m[1], -1) {
				n := atoi(ref[1])
				other, known := byNum[n]
				t.Blockers = append(t.Blockers, Blocker{Number: ref[1], Known: known, Open: !known || other.State != "CLOSED"})
			}
		}
	}
	t.BranchAhead = slices.ContainsFunc(aheadBranches, func(b string) bool { n, ok := numberOf(b); return ok && n == i.Number })
	t.OpenPR = slices.ContainsFunc(prs, func(p PR) bool { return prLinks(p, i.Number) })
	return t
}

// prLinks: a PR belongs to Ticket n by its source branch <n>-…, or else by
// a closing keyword for #n in its body.
func prLinks(p PR, n int) bool {
	if b, ok := numberOf(p.HeadRefName); ok {
		return b == n
	}
	for _, m := range closingRef.FindAllStringSubmatch(p.Body, -1) {
		if atoi(m[1]) == n {
			return true
		}
	}
	return false
}

func parentOf(i Issue) int {
	if i.Parent != nil {
		return i.Parent.Number
	}
	for _, re := range []*regexp.Regexp{partOf, parentHead} {
		if m := re.FindStringSubmatch(i.Body); m != nil {
			return atoi(m[1])
		}
	}
	return 0
}

func labels(i Issue) []string {
	var out []string
	for _, l := range i.Labels.Nodes {
		out = append(out, l.Name)
	}
	return out
}

func wayfinder(i Issue) bool {
	return slices.ContainsFunc(labels(i), func(l string) bool { return strings.HasPrefix(l, "wayfinder:") })
}

// atoi parses a number the regexes or GitHub produced; anything else is 0.
func atoi(s string) int {
	n, _ := strconv.Atoi(s) // inputs are digits only; 0 sorts first otherwise
	return n
}
