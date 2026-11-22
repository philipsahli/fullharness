// Package tracker reads a Project's Issue tracker into Features, Specs and
// Tickets and derives each one's Stage, in one place for every tracker
// (ADR 0004). fh only reads trackers; skills in Sessions write them.
package tracker

import (
	"os"
	"path/filepath"
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

// ReadyLabel marks a Ticket an Agent can pick up (docs/agents/triage-labels.md).
const ReadyLabel = "ready-for-agent"

// Board is one Project's Software factory board.
type Board struct {
	Tracker  string    `json:"tracker"`        // a Kind
	Note     string    `json:"note,omitempty"` // why the board is empty, e.g. an unsupported tracker
	Features []Feature `json:"features"`
}

// Feature is one Spec and its Tickets.
type Feature struct {
	ID        string         `json:"id"` // local: the .scratch folder; GitHub: the Spec's number
	Title     string         `json:"title"`
	Closed    bool           `json:"closed"` // the Spec is closed
	Stage     string         `json:"stage"`
	Note      string         `json:"note,omitempty"`
	Tickets   []Ticket       `json:"tickets"`
	Breakdown map[string]int `json:"breakdown"` // Stage → number of Tickets
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
	// Inputs to the Stage, filled by the adapter:
	BranchAhead bool `json:"-"` // a branch <N>-… has commits ahead of the base
	OpenPR      bool `json:"-"` // a linked PR is open
}

// Blocker is a Ticket that has to close first.
type Blocker struct {
	Number string `json:"number"`
	// Known: the Ticket exists. An adapter that sees blockers outside the
	// Feature (GitHub) sets Known and Open itself; unknown counts as open.
	Known bool `json:"known"`
	Open  bool `json:"open"`
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

// Derive sets every Ticket's and Feature's Stage, Startable and breakdown.
// It is the one place these rules live (#11), for every tracker:
//   - Ticket: closed → Shipped; an open linked PR → Review; a branch ahead
//     → Implementing; else Tickets.
//   - Startable: open, labelled ready-for-agent, no open (or unknown)
//     blocker. "Part of a Spec" is the adapter's job: it passes only
//     Tickets that belong to a Spec.
//   - Feature: Spec closed → Shipped; no Tickets → Spec; open Tickets → the
//     earliest Stage among them; all Tickets closed, Spec open → Review.
func Derive(features []Feature) {
	for i := range features {
		f := &features[i]
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
			case t.OpenPR:
				t.Stage = StageReview
			case t.BranchAhead:
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
			t.Startable = !t.Closed && t.Label == ReadyLabel && !blocked
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
		case len(f.Tickets) == 0:
			f.Stage = StageSpec
		case earliest >= 0:
			f.Stage = stageOrder[earliest]
		default:
			f.Stage, f.Note = StageReview, "All Tickets are closed: close the Spec to ship it."
		}
	}
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
