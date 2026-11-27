package tracker

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/philipsahli/fullharness/internal/gitremote"
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
			Number     int    `json:"number"`
			State      string `json:"state"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
		} `json:"nodes"`
	} `json:"blockedBy"`
}

// triageRoles are the labels a Ticket's role comes from
// (docs/agents/triage-labels.md).
var triageRoles = []string{"needs-triage", "needs-info", ReadyLabel, "ready-for-human", "wontfix"}

var (
	partOf     = regexp.MustCompile(`(?i)\bpart of #(\d+)`)
	parentHead = regexp.MustCompile(`(?im)^##\s*parent\s*\n+\s*#(\d+)`)
	// blockerLine is "Blocked by: #3, #5" (bold or not); blockerSection is
	// a "## Blocked by" heading with "- #3" items below, as /to-tickets
	// writes it, up to the next heading.
	blockerLine    = regexp.MustCompile(`(?im)^\**blocked by:?\**:?[ \t]*(\S.*)$`)
	blockerSection = regexp.MustCompile(`(?ims)^#+\s*blocked by\s*$(.*?)(?:^#|\z)`)
	leadingRef     = regexp.MustCompile(`^[-*\s]*#(\d+)`)
	// closingRef matches the keywords GitHub itself reads to close an
	// issue, with or without a colon.
	closingRef = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+#(\d+)`)
)

// LinkIssues points every Spec, Ticket, Blocker and Untriaged issue of b at
// its issue in repo, the Project's GitHub repository. Specs, Tickets and
// Untriaged issues are issues of repo. A blocker links only when it is known and in repo: gh's dependencies can
// name an issue in another repository (Blocker.Repo), where the same
// number is a different issue, and an unknown "#N" body line may name no
// issue at all.
func LinkIssues(b *Board, repo gitremote.GitHub) {
	for i := range b.Untriaged {
		if n := b.Untriaged[i].Number; n != "" {
			b.Untriaged[i].URL = repo.IssueURL(n)
		}
	}
	for i := range b.Features {
		f := &b.Features[i]
		f.URL = repo.IssueURL(f.ID)
		for j := range f.Tickets {
			t := &f.Tickets[j]
			t.URL = repo.IssueURL(t.Number)
			for k := range t.Blockers {
				b := &t.Blockers[k]
				if b.Known && (b.Repo == "" || strings.EqualFold(b.Repo, repo.NameWithOwner())) {
					b.URL = repo.IssueURL(b.Number)
				}
			}
		}
	}
}

// ReadGitHub builds a GitHub board's Features from a repository's issues and PRs
// (#12). A Spec is an issue with sub-issues, or that others name with
// "Part of #N" or a "## Parent" section; an issue with no Tickets is a Spec
// only when open and labelled ready-for-agent. Wayfinder issues are left out.
// aheadBranches are the Project's branches with commits ahead of its base.
// Stages are left to Derive.
//
// Every other open issue with no parent and no Tickets is an Untriaged
// issue, whatever its labels, "Idea: …" titles included (#92). So is an
// open Ticket whose parent isn't in the set, because the parent is a
// wayfinder issue or "Part of #N" names another issue; its Note names the
// parent, and ready-for-agent doesn't place it (#181). They come back
// newest first. Closed ones stay left out, as above.
func ReadGitHub(issues []Issue, prs []PR, aheadBranches []string) Board {
	byNum := map[int]Issue{}
	for _, i := range issues {
		if !wayfinder(i) {
			byNum[i.Number] = i
		}
	}
	children := map[int][]int{}
	hasParent := map[int]bool{}
	// onBoard marks a Ticket whose parent is on the board. The loop skips
	// wayfinder issues, so it never reads a wayfinder parent's subIssues;
	// GitHub returns each sub-issue link from both ends, so the Ticket's
	// own parent field (parentOf) still names that parent.
	onBoard := map[int]bool{}
	for _, i := range byNum {
		for _, s := range i.SubIssues.Nodes {
			if _, ok := byNum[s.Number]; ok && !slices.Contains(children[i.Number], s.Number) {
				children[i.Number] = append(children[i.Number], s.Number)
				hasParent[s.Number] = true
				onBoard[s.Number] = true
			}
		}
		if p := parentOf(i); p != 0 {
			if _, ok := byNum[p]; ok {
				if !slices.Contains(children[p], i.Number) {
					children[p] = append(children[p], i.Number)
				}
				onBoard[i.Number] = true
			}
			hasParent[i.Number] = true
		}
	}
	features := []Feature{}
	untriaged := []Untriaged{}
	// Ascending by number, so Features come out in order; untriaged is
	// reversed at the end.
	for _, num := range slices.Sorted(maps.Keys(byNum)) {
		i := byNum[num]
		kids := children[i.Number]
		// A Ticket whose parent is a wayfinder issue or outside the issue
		// set is listed whatever its labels, so it isn't lost.
		orphan := hasParent[i.Number] && !onBoard[i.Number]
		if len(kids) == 0 && i.State != "CLOSED" && (orphan || !hasParent[i.Number] && !slices.Contains(labels(i), ReadyLabel)) {
			n := strconv.Itoa(i.Number)
			u := Untriaged{Number: n, Ref: "#" + n, Title: i.Title, Labels: append([]string{}, labels(i)...)}
			if orphan {
				u.Note = "parent #" + strconv.Itoa(parentOf(i)) + " not on the board"
			}
			untriaged = append(untriaged, u)
			continue
		}
		if len(kids) == 0 && (hasParent[i.Number] || !slices.Contains(labels(i), ReadyLabel) || i.State == "CLOSED") {
			// A Ticket, an issue outside the factory, or a closed one-off
			// issue: without Tickets a closed issue was never a Feature
			// worth showing, and they would pile up in Shipped (#12).
			continue
		}
		f := Feature{ID: strconv.Itoa(i.Number), Title: i.Title, Closed: i.State == "CLOSED",
			Branch: branchName(strconv.Itoa(i.Number), i.Title), Ref: "#" + strconv.Itoa(i.Number), Tickets: []Ticket{}}
		for _, l := range labels(i) {
			if group, ok := strings.CutPrefix(l, "roadmap:"); ok {
				f.Roadmap = group
			}
			f.Approved = f.Approved || l == ReadyLabel
		}
		slices.Sort(kids)
		for _, k := range kids {
			f.Tickets = append(f.Tickets, githubTicket(byNum[k], byNum, prs, aheadBranches))
		}
		features = append(features, f)
	}
	slices.Reverse(untriaged) // newest first
	return Board{Tracker: KindGitHub, Features: features, Untriaged: untriaged}
}

func githubTicket(i Issue, byNum map[int]Issue, prs []PR, aheadBranches []string) Ticket {
	n := strconv.Itoa(i.Number)
	t := Ticket{Number: n, Title: i.Title, Closed: i.State == "CLOSED", Blockers: []Blocker{},
		Branch: branchName(n, i.Title), Ref: "#" + n}
	for _, l := range labels(i) {
		if slices.Contains(triageRoles, l) {
			t.Label = l
			break
		}
	}
	// Native dependencies first; a "Blocked by: #N" line only without them.
	for _, b := range i.BlockedBy.Nodes {
		t.Blockers = append(t.Blockers, Blocker{Number: strconv.Itoa(b.Number), Known: true, Open: b.State != "CLOSED",
			Repo: b.Repository.NameWithOwner})
	}
	if len(t.Blockers) == 0 {
		for _, n := range bodyBlockerRefs(i.Body) {
			other, known := byNum[n]
			t.Blockers = append(t.Blockers, Blocker{Number: strconv.Itoa(n), Known: known, Open: !known || other.State != "CLOSED"})
		}
	}
	t.BranchAhead = slices.ContainsFunc(aheadBranches, func(b string) bool { n, ok := numberOf(b); return ok && n == i.Number })
	t.linkPRs(prs, func(p PR) bool { return p.linksTo(i.Number) })
	return t
}

// bodyBlockerRefs reads blockers from an issue body: the #N starting each
// item of a "Blocked by:" line or of a "## Blocked by" section's list.
// Text in parentheses is a note, not a blocker ("#3 (needs #9)" is #3).
func bodyBlockerRefs(body string) []int {
	var items []string
	for _, m := range blockerLine.FindAllStringSubmatch(body, -1) {
		items = append(items, strings.Split(m[1], ",")...)
	}
	for _, m := range blockerSection.FindAllStringSubmatch(body, -1) {
		items = append(items, strings.Split(m[1], "\n")...)
	}
	var out []int
	for _, item := range items {
		item = parenthesised.ReplaceAllString(item, "")
		if m := leadingRef.FindStringSubmatch(strings.TrimSpace(item)); m != nil && !slices.Contains(out, atoi(m[1])) {
			out = append(out, atoi(m[1]))
		}
	}
	return out
}

// linksTo: a PR belongs to Ticket n by its source branch <n>-…, or by a
// closing keyword for #n in its body. Both count: one PR can carry a
// Ticket's branch and close another.
func (p PR) linksTo(n int) bool {
	if b, ok := numberOf(p.HeadRefName); ok && b == n {
		return true
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
