// Package flow works out one Feature's flow graph (#206, ADR-0013): its
// Stages as a main line, a lane per Ticket that branches off after Tickets
// and joins back once merged, and a dot per skill Autopilot ran on it,
// coloured by how it went. It is pure logic with no I/O: internal/app
// reads the inputs, and the UI only draws the result, on the Feature page
// now and full-screen in the Loop view (#114) later.
package flow

import (
	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/autopilot/status"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// Kind is what a node stands for.
type Kind string

const (
	KindStage Kind = "stage" // a Stage on the main line
	KindStop  Kind = "stop"  // a Stop point: drawn as a diamond, ◉ while it waits
	KindSkill Kind = "skill" // one skill Autopilot ran on a Ticket
)

// State is how a node went.
type State string

const (
	Todo    State = "todo"    // not reached yet
	Passed  State = "passed"  // reached and moved past, or the turn ended
	Running State = "running" // its Autopilot turn runs now
	Waiting State = "waiting" // a Stop point that waits for the user
	Failed  State = "failed"  // the skill didn't start
)

// Node is one point of the graph. Fields that don't apply are empty.
type Node struct {
	Kind Kind `json:"kind"`
	// Name is the Stage (idea … shipped), the Stop point's reason
	// (spec-approval, merge, asking, …) or the skill.
	Name  string `json:"name"`
	State State  `json:"state"`
	// Skill is the skill that ran or failed here; on a Stop point, the
	// skill it waits after or before.
	Skill   string `json:"skill,omitempty"`
	Reason  string `json:"reason,omitempty"`  // why a Stage node waits: a turn-end verdict
	Session string `json:"session,omitempty"` // the Session it ran in: clicking it opens that
	Error   string `json:"error,omitempty"`   // why the skill didn't start
	// Current marks where Autopilot is now: the node the Feature's
	// Autopilot status names. At most one node is current.
	Current bool `json:"current,omitempty"`
}

// Lane is one Ticket's branch, from Tickets to Review.
type Lane struct {
	Ticket string `json:"ticket"`
	Title  string `json:"title"`
	Nodes  []Node `json:"nodes"` // never nil; oldest first
	// Joined: the Ticket is closed, so its branch merged back.
	Joined bool `json:"joined,omitempty"`
	// BlockedBy are the open Tickets that must close first.
	BlockedBy []string `json:"blockedBy,omitempty"`
}

// Graph is one Feature's flow graph.
type Graph struct {
	// Main is the main line: idea, spec, the Approve spec Stop point,
	// tickets, implementing, review, shipped. The lanes branch off after
	// tickets and join back before review.
	Main  []Node `json:"main"`
	Lanes []Lane `json:"lanes"` // never nil; in the Feature's Ticket order
}

// stageSkill is the skill Autopilot runs on the whole Feature to reach a
// main-line Stage (tracker.FeatureStep): to-tickets writes the Tickets,
// implement-spec implements them, retro follows Shipped.
func stageSkill(stage string) string {
	from := map[string]string{
		tracker.StageTickets:      tracker.StageSpec,
		tracker.StageImplementing: tracker.StageTickets,
		tracker.StageShipped:      tracker.StageShipped,
	}[stage]
	if s := tracker.FeatureStep(from); s != nil && from != "" {
		return s.Skill
	}
	return ""
}

// Input is what one Feature's flow graph is worked out from.
type Input struct {
	Project string
	// Feature is the Feature with its Stages derived (tracker.Derive).
	Feature tracker.Feature
	// History is Autopilot's history, oldest first; events of other
	// Projects and Features are skipped.
	History []autopilot.Event
	Status  status.Status // the Feature's Autopilot status
}

// Of works out one Feature's flow graph.
func Of(in Input) Graph {
	f, st := in.Feature, in.Status
	var mine []autopilot.Event
	for _, e := range in.History {
		if e.Project == in.Project && e.Feature == f.ID {
			mine = append(mine, e)
		}
	}
	// The Feature's latest event: a turn-end Stop point waits only while
	// nothing came after it.
	last := len(mine) - 1

	g := Graph{Main: mainLine(f, mine, last, st), Lanes: []Lane{}}
	for _, t := range f.Tickets {
		g.Lanes = append(g.Lanes, laneOf(t, mine, last, st))
	}
	markCurrent(&g, st)
	return g
}

func mainLine(f tracker.Feature, events []autopilot.Event, last int, st status.Status) []Node {
	idx := stageIndex(f.Stage)
	if f.Closed {
		idx = stageIndex(tracker.StageShipped) + 1
	}
	reached := func(stage string) bool { return idx > stageIndex(stage) }

	approve := Node{Kind: KindStop, Name: string(status.SpecApproval), State: Todo}
	switch {
	case f.Approved || f.Closed || len(f.Tickets) > 0 || reached(tracker.StageSpec):
		approve.State = Passed
	case f.Stage == tracker.StageSpec:
		// An unapproved Spec is the Stop point, whether or not Autopilot
		// recorded it or is on (ADR-0009).
		approve.State = Waiting
	}

	stage := func(name string, progressed bool) Node {
		n := Node{Kind: KindStage, Name: name, State: Todo}
		if progressed {
			n.State = Passed
		}
		if skill := stageSkill(name); skill != "" {
			runOn(&n, events, "", skill, last, st)
		}
		return n
	}
	return []Node{
		{Kind: KindStage, Name: tracker.StageIdea, State: Passed},
		{Kind: KindStage, Name: tracker.StageSpec, State: Passed},
		approve,
		stage(tracker.StageTickets, len(f.Tickets) > 0 || f.Closed),
		stage(tracker.StageImplementing, reached(tracker.StageImplementing)),
		stage(tracker.StageReview, reached(tracker.StageReview)),
		stage(tracker.StageShipped, f.Closed),
	}
}

// runOn sets n from the latest event of skill on the whole Feature (ticket
// ""), if Autopilot ran it: running, waiting after a turn that didn't end
// done, failed, or passed.
func runOn(n *Node, events []autopilot.Event, ticket, skill string, last int, st status.Status) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Ticket != ticket || e.Skill != skill || e.Reason == string(status.SpecApproval) {
			continue
		}
		n.Skill = skill
		switch e.Kind {
		case autopilot.KindSkill:
			n.Session = e.Session
			n.State = Passed
			if running(e, st) {
				n.State = Running
			}
		case autopilot.KindClassified:
			n.Session, n.State = e.Session, Passed
		case autopilot.KindStop:
			n.Session = e.Session
			if i == last {
				n.State, n.Reason = Waiting, e.Reason
			} else {
				n.State = Passed
			}
		case autopilot.KindFailed:
			n.State, n.Error = Failed, e.Error
		}
		return
	}
}

func laneOf(t tracker.Ticket, events []autopilot.Event, last int, st status.Status) Lane {
	l := Lane{Ticket: t.Number, Title: t.Title, Nodes: []Node{}, Joined: t.Closed}
	for _, b := range t.Blockers {
		if b.Open || !b.Known {
			l.BlockedBy = append(l.BlockedBy, b.Number)
		}
	}
	for i, e := range events {
		if e.Ticket != t.Number {
			continue
		}
		switch e.Kind {
		case autopilot.KindSkill:
			n := Node{Kind: KindSkill, Name: e.Skill, Skill: e.Skill, Session: e.Session, State: Passed}
			if running(e, st) {
				n.State = Running
			}
			l.Nodes = append(l.Nodes, n)
		case autopilot.KindClassified:
			// The turn ended: its dot is no longer running. A verdict other
			// than done comes with its own Stop point event.
			for j := len(l.Nodes) - 1; j >= 0; j-- {
				if l.Nodes[j].Kind == KindSkill && l.Nodes[j].Session == e.Session {
					l.Nodes[j].State = Passed
					break
				}
			}
		case autopilot.KindStop:
			n := Node{Kind: KindStop, Name: e.Reason, Skill: e.Skill, Session: e.Session, State: Passed}
			if stopWaits(e, t, i == last) {
				n.State = Waiting
			}
			// A turn that ended without a verdict (exited) stopped running too.
			for j := len(l.Nodes) - 1; j >= 0; j-- {
				if l.Nodes[j].Kind == KindSkill && l.Nodes[j].Session == e.Session && e.Session != "" {
					l.Nodes[j].State = Passed
					break
				}
			}
			l.Nodes = append(l.Nodes, n)
		case autopilot.KindFailed:
			l.Nodes = append(l.Nodes, Node{Kind: KindSkill, Name: e.Skill, Skill: e.Skill, Session: e.Session, State: Failed, Error: e.Error})
		}
	}
	return l
}

// stopWaits reports whether Stop point e on Ticket t still waits. Merge
// waits while the Ticket is open at Review, whatever Autopilot did on
// other Tickets meanwhile; a turn-end Stop point waits until anything
// happens after it, since Autopilot holds the Project there (ADR-0009).
func stopWaits(e autopilot.Event, t tracker.Ticket, latest bool) bool {
	if e.Reason == string(status.Merge) {
		return !t.Closed && t.Stage == tracker.StageReview
	}
	return latest
}

func running(e autopilot.Event, st status.Status) bool {
	return st.Kind == status.Running && e.Session != "" && e.Session == st.Session
}

// markCurrent marks the node the Autopilot status names: the latest one
// that matches, so a step tried twice is current only once.
func markCurrent(g *Graph, st status.Status) {
	var at *Node
	match := func(n *Node, ticket string) {
		ok := false
		switch st.Kind {
		case status.Running:
			ok = n.State == Running && n.Session == st.Session
		case status.Waiting:
			reason := n.Reason
			if n.Kind == KindStop {
				reason = n.Name
			}
			ok = n.State == Waiting && reason == string(st.Reason) && ticket == st.Ticket
		case status.Failed:
			ok = n.State == Failed && n.Skill == st.Skill && ticket == st.Ticket
		}
		if ok {
			at = n
		}
	}
	for i := range g.Main {
		match(&g.Main[i], "")
	}
	for li := range g.Lanes {
		for i := range g.Lanes[li].Nodes {
			match(&g.Lanes[li].Nodes[i], g.Lanes[li].Ticket)
		}
	}
	if at != nil {
		at.Current = true
	}
}

func stageIndex(stage string) int {
	for i, s := range []string{tracker.StageIdea, tracker.StageSpec, tracker.StageTickets, tracker.StageImplementing, tracker.StageReview, tracker.StageShipped} {
		if s == stage {
			return i
		}
	}
	return 0
}
