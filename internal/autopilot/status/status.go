// Package status works out each Feature's Autopilot status (GLOSSARY.md,
// ADR-0013) from the Autopilot history, the Issue tracker and the
// Sessions. It is pure logic with no I/O: internal/app reads the inputs and
// sends the result to the UI, which only draws it.
package status

import (
	"slices"
	"time"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// Kind is one of the six Autopilot statuses.
type Kind string

const (
	Off     Kind = "off"     // Autopilot is off for the Project, or the Feature is not in Now
	Idle    Kind = "idle"    // in Now, nothing to start yet
	Running Kind = "running" // an Autopilot turn of the Feature runs
	Waiting Kind = "waiting" // at a Stop point, for the user
	Failed  Kind = "failed"  // a skill didn't start; Autopilot retries
	Done    Kind = "done"    // the Spec is closed and the retro turn ended done
)

// Reason says why an Autopilot status is waiting (the Stop point) or off.
type Reason string

const (
	SpecApproval Reason = autopilot.ReasonSpecApproval // Approve spec
	Merge        Reason = autopilot.ReasonMerge
	Exited       Reason = autopilot.ReasonExited // the Session exited before its turn ended
	Asking       Reason = Reason(classify.Asking)
	Stuck        Reason = Reason(classify.Stuck)
	Unsure       Reason = Reason(classify.Unsure)
	AutopilotOff Reason = "autopilot-off" // off: the Project has Autopilot off
	NotInNow     Reason = "not-in-now"    // off: the Feature is not in Roadmap Now
)

// Status is one Feature's Autopilot status. Fields that don't apply to its
// Kind are empty.
type Status struct {
	Project string `json:"project"`
	Feature string `json:"feature"`
	Kind    Kind   `json:"kind"`
	// Skill and Ticket are the step it runs, waits after, or failed to
	// start; Ticket is empty for a step on the whole Feature.
	Skill   string `json:"skill,omitempty"`
	Ticket  string `json:"ticket,omitempty"`
	Reason  Reason `json:"reason,omitempty"`
	Session string `json:"session,omitempty"`
	Error   string `json:"error,omitempty"` // why the skill didn't start
	// Since is when this Autopilot status began; Stamp sets it.
	Since time.Time `json:"since,omitzero"`
}

// Input is what one Project's Autopilot statuses are worked out from.
type Input struct {
	Project string
	On      bool // the Project's Autopilot switch
	// Features is the Project's board with Stages derived; nil when the
	// tracker can't be read.
	Features []tracker.Feature
	// History is Autopilot's history, oldest first; events of other
	// Projects are skipped.
	History  []autopilot.Event
	Sessions []session.Summary
}

// Of returns the Autopilot status of each Feature in in.Features, in their
// order, then of each other Feature in the history whose turn still runs:
// a running turn shows even when the tracker can't be read. Since is left
// zero for Stamp.
func Of(in Input) []Status {
	last := map[string]autopilot.Event{}
	var order []string
	for _, e := range in.History {
		if e.Project != in.Project {
			continue
		}
		if _, ok := last[e.Feature]; !ok {
			order = append(order, e.Feature)
		}
		last[e.Feature] = e
	}
	sessions := map[string]session.Summary{}
	for _, s := range in.Sessions {
		sessions[s.ID] = s
	}

	out := []Status{}
	listed := map[string]bool{}
	for i := range in.Features {
		f := &in.Features[i]
		listed[f.ID] = true
		e, ok := last[f.ID]
		out = append(out, of(in, f.ID, f, e, ok, sessions))
	}
	for _, id := range order {
		if listed[id] {
			continue
		}
		if st := of(in, id, nil, last[id], true, sessions); st.Kind == Running {
			out = append(out, st)
		}
	}
	return out
}

// of is Feature id's Autopilot status. f is nil when the tracker didn't
// list it; e is its latest event when hasEvent.
func of(in Input, id string, f *tracker.Feature, e autopilot.Event, hasEvent bool, sessions map[string]session.Summary) Status {
	st := Status{Project: in.Project, Feature: id}
	// fromEvent is st with kind, for the step e names.
	fromEvent := func(kind Kind, reason Reason) Status {
		st.Kind, st.Reason, st.Skill, st.Ticket, st.Session = kind, reason, e.Skill, e.Ticket, e.Session
		return st
	}
	s, open := sessions[e.Session]
	open = hasEvent && open && e.Session != ""

	// A turn that runs, runs on: neither the switch nor leaving Now stops
	// it (ADR-0013).
	if open && e.Kind == autopilot.KindSkill && s.State != session.Exited {
		return fromEvent(Running, "")
	}
	if f == nil {
		st.Kind, st.Reason = Off, NotInNow
		return st
	}
	// Done is a fact about the Feature, so it holds outside Now too.
	if f.Closed && hasEvent && e.Kind == autopilot.KindClassified && e.Verdict == classify.Done && e.Skill == retroSkill() {
		st.Kind = Done
		return st
	}
	if f.Roadmap != tracker.RoadmapNow {
		st.Kind, st.Reason = Off, NotInNow
		return st
	}
	if !in.On {
		st.Kind, st.Reason = Off, AutopilotOff
		return st
	}
	if hasEvent {
		switch e.Kind {
		case autopilot.KindSkill:
			if open { // exited before its turn ended: Step records the wait on its next look
				return fromEvent(Waiting, Exited)
			}
		case autopilot.KindStop:
			if stopHolds(f, e, open) {
				return fromEvent(Waiting, Reason(e.Reason))
			}
		case autopilot.KindFailed:
			st = fromEvent(Failed, "")
			st.Error = e.Error
			return st
		}
	}
	// An unapproved Spec waits for the user whether or not Step has
	// recorded it yet.
	if !f.Closed && f.Stage == tracker.StageSpec && !f.Approved {
		if next := tracker.FeatureStep(tracker.StageSpec); next != nil {
			st.Kind, st.Skill, st.Reason = Waiting, next.Skill, SpecApproval
			return st
		}
	}
	st.Kind = Idle
	return st
}

// stopHolds reports whether the recorded Stop point e still waits: the
// tracker answers for Approve spec and merge, the Session for the rest.
func stopHolds(f *tracker.Feature, e autopilot.Event, open bool) bool {
	switch Reason(e.Reason) {
	case SpecApproval:
		return !f.Closed && f.Stage == tracker.StageSpec && !f.Approved
	case Merge:
		return slices.ContainsFunc(f.Tickets, func(t tracker.Ticket) bool {
			return t.Number == e.Ticket && !t.Closed && t.Stage == tracker.StageReview
		})
	default:
		return open
	}
}

func retroSkill() string {
	if s := tracker.FeatureStep(tracker.StageShipped); s != nil {
		return s.Skill
	}
	return ""
}

// Stamp returns next with each Autopilot status's Since: kept from prev
// while the Feature's Autopilot status is the same, now when it changed or
// is new. It doesn't change next.
func Stamp(prev, next []Status, now time.Time) []Status {
	type key struct{ project, feature string }
	before := map[key]Status{}
	for _, s := range prev {
		before[key{s.Project, s.Feature}] = s
	}
	out := make([]Status, len(next))
	for i, s := range next {
		s.Since = now
		if p, ok := before[key{s.Project, s.Feature}]; ok {
			same := p
			same.Since = now
			if same == s {
				s.Since = p.Since
			}
		}
		out[i] = s
	}
	return out
}
