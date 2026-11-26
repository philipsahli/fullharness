package autopilot

import (
	"slices"
	"time"

	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// StatusKind is one of the six Autopilot statuses (GLOSSARY.md).
type StatusKind string

const (
	Off     StatusKind = "off"     // Autopilot is off for the Project, or the Feature is not in Now
	Idle    StatusKind = "idle"    // in Now, nothing to start yet
	Running StatusKind = "running" // an Autopilot turn of the Feature runs
	Waiting StatusKind = "waiting" // at a Stop point, for the user
	Failed  StatusKind = "failed"  // a skill didn't start; Autopilot retries
	Done    StatusKind = "done"    // the Spec is closed and the retro turn ended done
)

// Status is one Feature's Autopilot status. Fields that don't apply to its
// Kind are empty.
type Status struct {
	Project string     `json:"project"`
	Feature string     `json:"feature"`
	Kind    StatusKind `json:"kind"`
	// Skill and Ticket are the step it runs, waits after, or failed to
	// start; Ticket is empty for a step on the whole Feature.
	Skill  string `json:"skill,omitempty"`
	Ticket string `json:"ticket,omitempty"`
	// Reason is the Stop point it waits at: a verdict (asking, stuck,
	// unsure) or one of the Reason constants.
	Reason  string `json:"reason,omitempty"`
	Session string `json:"session,omitempty"`
	Error   string `json:"error,omitempty"` // why the skill didn't start
	// Since is when the history last changed for the Feature; zero when
	// the status comes from the tracker alone.
	Since time.Time `json:"since,omitzero"`
}

// StatusInput is what one Project's statuses are worked out from.
type StatusInput struct {
	Project string
	On      bool // the Project's Autopilot switch
	// Features is the Project's board with Stages derived; nil when the
	// tracker can't be read.
	Features []tracker.Feature
	// History is Autopilot's history, oldest first; events of other
	// Projects are skipped.
	History  []Event
	Sessions []session.Summary
}

// Statuses returns the Autopilot status of each Feature in in.Features, in
// their order, then of each other Feature in the history whose turn still
// runs: a running turn shows even when the tracker can't be read.
func Statuses(in StatusInput) []Status {
	last := map[string]Event{}
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
	seen := map[string]bool{}
	for i := range in.Features {
		f := &in.Features[i]
		seen[f.ID] = true
		e, ok := last[f.ID]
		out = append(out, status(in, f, e, ok, sessions))
	}
	for _, id := range order {
		if seen[id] {
			continue
		}
		if st := status(in, nil, last[id], true, sessions); st.Kind == Running {
			out = append(out, st)
		}
	}
	return out
}

// status is one Feature's status. f is nil when the tracker didn't list
// it; e is its latest event when hasEvent.
func status(in StatusInput, f *tracker.Feature, e Event, hasEvent bool, sessions map[string]session.Summary) Status {
	id := e.Feature
	if f != nil {
		id = f.ID
	}
	st := Status{Project: in.Project, Feature: id}
	of := func(kind StatusKind) Status {
		st.Kind, st.Skill, st.Ticket, st.Session, st.Since = kind, e.Skill, e.Ticket, e.Session, e.Time
		return st
	}
	if hasEvent {
		st.Since = e.Time
	}
	s, alive := sessions[e.Session]
	alive = alive && e.Session != ""

	// A turn that runs, runs on: neither the switch nor leaving Now stops
	// it (ADR-0013).
	if hasEvent && e.Kind == KindSkill && alive && s.State != session.Exited {
		return of(Running)
	}
	if f == nil || !in.On || f.Roadmap != tracker.RoadmapNow {
		st.Kind = Off
		return st
	}
	if f.Closed && hasEvent && e.Kind == KindClassified && e.Verdict == classify.Done && e.Skill == retroSkill() {
		st.Kind = Done
		return st
	}
	if hasEvent {
		switch e.Kind {
		case KindSkill:
			if alive { // Exited before its turn ended: Step records the wait on its next look
				st = of(Waiting)
				st.Reason = ReasonExited
				return st
			}
		case KindStop:
			if stopHolds(f, e, alive) {
				st = of(Waiting)
				st.Reason = e.Reason
				return st
			}
		case KindFailed:
			st = of(Failed)
			st.Error = e.Error
			return st
		}
	}
	// An unapproved Spec waits for the user whether or not Step has
	// recorded it yet.
	if !f.Closed && f.Stage == tracker.StageSpec && !f.Approved {
		if s := tracker.FeatureStep(tracker.StageSpec); s != nil {
			st.Kind, st.Skill, st.Reason = Waiting, s.Skill, ReasonSpecApproval
			return st
		}
	}
	st.Kind = Idle
	return st
}

// stopHolds reports whether the recorded Stop point e still waits: the
// tracker answers for Spec approval and merge, the Session for the rest.
func stopHolds(f *tracker.Feature, e Event, alive bool) bool {
	switch e.Reason {
	case ReasonSpecApproval:
		return !f.Closed && f.Stage == tracker.StageSpec && !f.Approved
	case ReasonMerge:
		return slices.ContainsFunc(f.Tickets, func(t tracker.Ticket) bool {
			return t.Number == e.Ticket && !t.Closed && t.Stage == tracker.StageReview
		})
	default:
		return alive
	}
}

func retroSkill() string {
	if s := tracker.FeatureStep(tracker.StageShipped); s != nil {
		return s.Skill
	}
	return ""
}
