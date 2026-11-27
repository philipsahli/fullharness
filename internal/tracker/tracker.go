// Package tracker reads a Project's Issue tracker into Features, Specs and
// Tickets and derives each one's Stage, in one place for every tracker
// (ADR 0004). It also runs the fixed list of writes fh makes itself
// (ADR 0008, write.go); every other write happens in a skill in a Session.
package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Kinds of Issue tracker.
const (
	KindLocal       = "local"
	KindGitHub      = "github"
	KindUnsupported = "unsupported"
	KindNone        = "none"
)

// Stages (GLOSSARY.md), in Software factory order.
const (
	StageIdea         = "idea"
	StageSpec         = "spec"
	StageTickets      = "tickets"
	StageImplementing = "implementing"
	StageReview       = "review"
	StageShipped      = "shipped"
)

var stageOrder = []string{StageIdea, StageSpec, StageTickets, StageImplementing, StageReview, StageShipped}

// ReadyLabel marks a Ticket an Agent can pick up (docs/agents/triage-labels.md),
// and an approved Spec.
const ReadyLabel = "ready-for-agent"

// RoadmapNow is the Roadmap group Autopilot works on (ADR-0009).
const RoadmapNow = "now"

// Board is one Project's Software factory board.
type Board struct {
	Tracker  string    `json:"tracker"`        // a Kind
	Note     string    `json:"note,omitempty"` // why the board is empty, e.g. an unsupported tracker
	Features []Feature `json:"features"`
	// Untriaged are the open issues the Software factory can't place
	// (GLOSSARY.md), newest first; never nil.
	Untriaged []Untriaged `json:"untriaged"`
}

// Untriaged is one Untriaged issue, a row of the board's Needs triage group.
type Untriaged struct {
	// Number is the issue's number on GitHub; empty for a local markdown
	// file, which has only a Ref.
	Number string `json:"number,omitempty"`
	// Ref is how a skill's prompt names the issue: "#42", or a file path.
	Ref    string   `json:"ref"`
	Title  string   `json:"title"`
	Labels []string `json:"labels"` // never nil
	URL    string   `json:"url,omitempty"`
	// Note says why the issue is here when that isn't plain, such as a
	// Ticket whose parent isn't on the board.
	Note string `json:"note,omitempty"`
}

// Feature is one Spec and its Tickets.
type Feature struct {
	ID        string         `json:"id"` // local: the .scratch folder; GitHub: the Spec's number
	Title     string         `json:"title"`
	Closed    bool           `json:"closed"` // the Spec is closed
	Stage     string         `json:"stage"`
	Note      string         `json:"note,omitempty"`
	Tickets   []Ticket       `json:"tickets"`   // never nil: the UI reads its length
	Breakdown map[string]int `json:"breakdown"` // Stage → number of Tickets
	// Branch is the branch a new Worktree for the whole Spec gets (Implement
	// spec), and Ref how a skill's prompt names the Spec (#13).
	Branch string `json:"branch"`
	Ref    string `json:"ref"`
	// Idea names the Idea linked to this Spec, if any (#14).
	Idea string `json:"idea,omitempty"`
	// URL opens the Spec in its tracker; empty shows plain text (#86).
	URL string `json:"url,omitempty"`
	// Roadmap is the Spec's Roadmap group (ADR 0005): now, next or later,
	// or empty. Autopilot works only on Features in Now (ADR-0009).
	Roadmap string `json:"roadmap,omitempty"`
	// Approved: the Spec is labelled ready-for-agent, so its Tickets can
	// be written (the Spec approval Stop point, ADR-0009).
	Approved bool `json:"approved,omitempty"`
	// Next is the skill that moves it on from its Stage (next.go).
	Next *Step `json:"next,omitempty"`
}

// Ticket is one buildable piece of a Feature.
type Ticket struct {
	Number    string    `json:"number"` // local: "01"; GitHub: "42"
	Title     string    `json:"title"`
	Label     string    `json:"label"` // the triage role, e.g. ready-for-agent
	Closed    bool      `json:"closed"`
	Blockers  []Blocker `json:"blockers"`
	Stage     string    `json:"stage"`
	Startable bool      `json:"startable"`
	// Branch is the branch Start Session gives the Ticket's new Worktree,
	// one the adapter's own branch rule matches; Ref is how the implement
	// skill's prompt names the Ticket, e.g. "#42" (#13).
	Branch string `json:"branch"`
	Ref    string `json:"ref"`
	URL    string `json:"url,omitempty"` // opens the Ticket in its tracker (#86)
	// Next is the skill that moves it on from its Stage (next.go). The
	// Board offers it only on a Startable Ticket.
	Next *Step `json:"next,omitempty"`
	// PRs are the Pull requests that belong to it, in every PR state,
	// newest first; never nil. Only Open and Draft ones set UnmergedPR.
	PRs []PRRecord `json:"prs"`
	// Inputs to the Stage, filled by the adapter, and by Derive from links:
	BranchAhead bool `json:"-"` // a branch <N>-… has commits ahead of the base
	UnmergedPR  bool `json:"-"` // a linked PR is Open or Draft
	Linked      bool `json:"-"` // a Session (live or Exited) was started from it
}

// Blocker is a Ticket that has to close first.
type Blocker struct {
	Number string `json:"number"`
	// Known: the Ticket exists. An adapter that sees blockers outside the
	// Feature (GitHub) sets Known and Open itself; unknown counts as open.
	Known bool   `json:"known"`
	Open  bool   `json:"open"`
	URL   string `json:"url,omitempty"` // opens the blocker in its tracker (#86)
	// Repo is the GitHub repository ("owner/repo") gh's dependencies put
	// the blocker in; empty means the Ticket's own repository.
	Repo string `json:"-"`
}

// Detect finds the Project's Issue tracker: the `# Issue tracker: …`
// heading of docs/agents/issue-tracker.md, which the skills read too
// (ADR 0004); without it, a .scratch folder means local markdown and a
// GitHub remote means GitHub. For an unsupported tracker it also returns
// its name.
func Detect(repo string) (kind, name string) {
	if b, err := os.ReadFile(filepath.Join(repo, "docs", "agents", "issue-tracker.md")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "# Issue tracker:"); ok {
				name = strings.TrimSpace(rest)
				switch strings.ToLower(name) {
				case "local markdown":
					return KindLocal, name
				case "github":
					return KindGitHub, name
				default:
					return KindUnsupported, name
				}
			}
		}
	}
	if info, err := os.Stat(filepath.Join(repo, ".scratch")); err == nil && info.IsDir() {
		return KindLocal, "Local Markdown"
	}
	if HasGitHubRemote(repo) {
		return KindGitHub, "GitHub"
	}
	return KindNone, ""
}

// Derive sets every Ticket's and Feature's Stage, Startable and breakdown,
// after the adapter read the tracker; links are the Project's Sessions
// started from the board. It is the one place these rules live (#11), for
// every tracker:
//   - Ticket: closed → Shipped; an open linked PR → Review; a branch ahead
//     or a linked Session → Implementing; else Tickets.
//   - Startable: at the Tickets Stage (open, not started), labelled
//     ready-for-agent, no open (or unknown) blocker. "Part of a Spec" is the adapter's job: it passes only
//     Tickets that belong to a Spec.
//   - Feature: Spec closed → Shipped (with a Note if a Ticket is still
//     open); no Tickets → Spec; open Tickets → the
//     earliest Stage among them; all Tickets closed, Spec open → Review.
//   - Next: each one's Stage's next step (next.go).
func Derive(features []Feature, links []Link) {
	for i := range features {
		f := &features[i]
		for j := range f.Tickets {
			t := &f.Tickets[j]
			t.Linked = slices.ContainsFunc(links, func(l Link) bool {
				return l.Feature == f.ID && (l.AllTickets || l.Ticket == t.Number)
			})
		}
		// Blockers match Tickets by numeric value: "5" and "05" are the same
		// Ticket, whatever width a folder uses.
		byValue := map[int]Ticket{}
		for _, t := range f.Tickets {
			if n, err := strconv.Atoi(t.Number); err == nil {
				byValue[n] = t
			}
		}
		f.Breakdown = map[string]int{}
		earliest := -1
		for j := range f.Tickets {
			t := &f.Tickets[j]
			switch {
			case t.Closed:
				t.Stage = StageShipped
			case t.UnmergedPR:
				t.Stage = StageReview
			case t.BranchAhead || t.Linked:
				t.Stage = StageImplementing
			default:
				t.Stage = StageTickets
			}
			blocked := false
			for k := range t.Blockers {
				b := &t.Blockers[k]
				n, err := strconv.Atoi(b.Number)
				if other, inFeature := byValue[n]; err == nil && inFeature {
					b.Number = other.Number // show it as the Ticket names itself
					b.Known, b.Open = true, !other.Closed
				} else if !b.Known {
					// Not in this Feature, and the adapter doesn't know it
					// either (GitHub blockers may sit in other Features).
					b.Open = true
				}
				blocked = blocked || b.Open
			}
			// Only a Ticket nobody has started: a branch ahead or an open PR
			// means it is already in progress.
			t.Startable = t.Stage == StageTickets && t.Label == ReadyLabel && !blocked
			t.Next = TicketStep(t.Stage)
			f.Breakdown[t.Stage]++
			if !t.Closed {
				if pos := slices.Index(stageOrder, t.Stage); earliest < 0 || pos < earliest {
					earliest = pos
				}
			}
		}
		switch {
		case f.Closed:
			f.Stage = StageShipped
			// GLOSSARY Stage: Shipped still, but a mistake to show, e.g. a
			// Spec closed by hand before its Tickets were done (#140).
			var still []string
			for _, t := range f.Tickets {
				if !t.Closed {
					still = append(still, "#"+t.Number)
				}
			}
			switch len(still) {
			case 0:
			case 1:
				f.Note = "Spec closed with Ticket " + still[0] + " open"
			default:
				f.Note = "Spec closed with Tickets " + strings.Join(still, ", ") + " open"
			}
		case len(f.Tickets) == 0:
			f.Stage = StageSpec
		case earliest >= 0:
			f.Stage = stageOrder[earliest]
		default:
			f.Stage, f.Note = StageReview, "All Tickets are closed: close the Spec to ship it."
		}
		f.Next = FeatureStep(f.Stage)
	}
}

// Link is a Session's tie to the Feature (and Ticket) it was started from
// on the board. Ticket is empty for a Session about the whole Feature.
// AllTickets marks Implement spec, whose Session works on every open
// Ticket; a Feature link without it (Make tickets, Retro) marks none.
type Link struct {
	Feature    string `json:"feature"`
	Ticket     string `json:"ticket,omitempty"`
	AllTickets bool   `json:"allTickets,omitempty"`
}

// maxSlug bounds the slug part of a branch fh names after a title.
const maxSlug = 40

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// branchName is "<n>-<slug of title>".
func branchName(n, title string) string {
	if s := slug(title); s != "" {
		return n + "-" + s
	}
	return n
}

// IdeaBranch is the branch Grill new idea gives an Idea's Worktree,
// "idea/<slug of name>", so two Ideas grilled at once each edit their own
// glossary (spec #1, story 73). ok is false for a name with no letters or
// digits.
func IdeaBranch(name string) (branch string, ok bool) {
	s := slug(name)
	return "idea/" + s, s != ""
}

// slug is text lowercased, every run of other characters one "-", cut at a
// word to at most maxSlug characters.
func slug(text string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if len(s) > maxSlug {
		s = s[:maxSlug+1]
		if cut := strings.LastIndex(s, "-"); cut > 0 {
			s = s[:cut]
		} else {
			s = s[:maxSlug]
		}
	}
	return s
}

// numberOf parses a Ticket number from a branch name like "12-login" or
// "012-login"; ok is false for any other name.
func numberOf(branch string) (int, bool) {
	head, _, found := strings.Cut(branch, "-")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(head)
	return n, err == nil && n > 0
}
