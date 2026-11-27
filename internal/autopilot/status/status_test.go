package status_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/autopilot/status"
	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// The Autopilot status (GLOSSARY.md, ADR-0013): one per Feature, worked out
// from the Autopilot history, the Issue tracker and the Sessions. This is a
// package of pure logic, so its tests call it directly
// (CODING_STANDARDS.md, "Tests go through the API").

const proj = "/repo"

var (
	t0 = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Minute)
	t2 = t0.Add(2 * time.Minute)
)

func feature(id string, mod ...func(*tracker.Feature)) tracker.Feature {
	f := tracker.Feature{ID: id, Stage: tracker.StageImplementing, Roadmap: tracker.RoadmapNow, Approved: true,
		Tickets: []tracker.Ticket{{Number: "11", Stage: tracker.StageImplementing}}}
	for _, m := range mod {
		m(&f)
	}
	return f
}

func inGroup(g string) func(*tracker.Feature) { return func(f *tracker.Feature) { f.Roadmap = g } }

func atSpec(approved bool) func(*tracker.Feature) {
	return func(f *tracker.Feature) { f.Stage, f.Approved, f.Tickets = tracker.StageSpec, approved, nil }
}

func shipped(f *tracker.Feature) {
	f.Closed, f.Stage = true, tracker.StageShipped
	f.Tickets[0].Closed, f.Tickets[0].Stage = true, tracker.StageShipped
}

func atReview(f *tracker.Feature) { f.Tickets[0].Stage = tracker.StageReview }

func sess(id, state string) session.Summary {
	return session.Summary{ID: id, Project: proj, State: state, Autopilot: true}
}

func skill(feature, ticket, name, id string, at time.Time) autopilot.Event {
	return autopilot.Event{Step: autopilot.Step{Project: proj, Feature: feature, Ticket: ticket, Skill: name},
		Kind: autopilot.KindSkill, Time: at, Session: id}
}

func classified(e autopilot.Event, v classify.Verdict, at time.Time) autopilot.Event {
	e.Kind, e.Verdict, e.Time = autopilot.KindClassified, v, at
	return e
}

func stop(e autopilot.Event, reason string, at time.Time) autopilot.Event {
	e.Kind, e.Reason, e.Time = autopilot.KindStop, reason, at
	return e
}

func failed(feature, ticket, name, msg string, at time.Time) autopilot.Event {
	return autopilot.Event{Step: autopilot.Step{Project: proj, Feature: feature, Ticket: ticket, Skill: name},
		Kind: autopilot.KindFailed, Time: at, Error: msg}
}

// statusOf runs status.Of and returns Feature id's Autopilot status.
func statusOf(t *testing.T, id string, in status.Input) status.Status {
	t.Helper()
	in.Project = proj
	for _, s := range status.Of(in) {
		if s.Feature == id {
			return s
		}
	}
	t.Fatalf("no Autopilot status for Feature %s", id)
	return status.Status{}
}

func want(kind status.Kind, mod ...func(*status.Status)) status.Status {
	s := status.Status{Project: proj, Feature: "login", Kind: kind}
	for _, m := range mod {
		m(&s)
	}
	return s
}

func check(t *testing.T, got, want status.Status) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Autopilot status\n%+v\nwant\n%+v", got, want)
	}
}

func TestOffWhenTheProjectHasAutopilotOff(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: false, Features: []tracker.Feature{feature("login")}})
	check(t, got, want(status.Off, func(s *status.Status) { s.Reason = status.AutopilotOff }))
}

func TestOffWhenTheFeatureIsNotInNow(t *testing.T) {
	for _, on := range []bool{true, false} {
		got := statusOf(t, "login", status.Input{On: on, Features: []tracker.Feature{feature("login", inGroup("next"))}})
		check(t, got, want(status.Off, func(s *status.Status) { s.Reason = status.NotInNow }))
	}
}

func TestIdleInNowWithNothingDoneYet(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true, Features: []tracker.Feature{feature("login")}})
	check(t, got, want(status.Idle))
}

func TestRunningWhileTheSkillsSessionWorks(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Working)},
	})
	check(t, got, want(status.Running, func(s *status.Status) {
		s.Skill, s.Ticket, s.Session = "implement", "11", "s1"
	}))
}

// A turn keeps running after Autopilot is turned off or the Feature leaves
// Now; it ends by itself (ADR-0013).
func TestRunningEvenWithAutopilotOffAndOutsideNow(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: false,
		Features: []tracker.Feature{feature("login", inGroup("later"))},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Waiting)},
	})
	if got.Kind != status.Running {
		t.Errorf("kind %s, want running", got.Kind)
	}
}

// A Feature moved out of Now keeps running until its turn ends, marked
// "Session running outside Now"; then it is off like any Feature outside
// Now (#204, ADR-0013).
func TestRunningOutsideNowUntilTheTurnEnds(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	in := status.Input{On: true,
		Features: []tracker.Feature{feature("login", inGroup("next"))},
		History:  []autopilot.Event{s},
		Sessions: []session.Summary{sess("s1", session.Working)},
	}
	check(t, statusOf(t, "login", in), want(status.Running, func(s *status.Status) {
		s.Skill, s.Ticket, s.Session, s.OutsideNow = "implement", "11", "s1", true
	}))

	for _, end := range []struct {
		name    string
		history []autopilot.Event
		state   string
	}{
		{"asking", []autopilot.Event{s, classified(s, classify.Asking, t1), stop(s, string(classify.Asking), t1)}, session.Waiting},
		{"stuck", []autopilot.Event{s, classified(s, classify.Stuck, t1), stop(s, string(classify.Stuck), t1)}, session.Idle},
		{"done", []autopilot.Event{s, classified(s, classify.Done, t1)}, session.Idle},
		{"exited", []autopilot.Event{s}, session.Exited},
	} {
		t.Run(end.name, func(t *testing.T) {
			in.History, in.Sessions = end.history, []session.Summary{sess("s1", end.state)}
			check(t, statusOf(t, "login", in), want(status.Off, func(s *status.Status) { s.Reason = status.NotInNow }))
		})
	}
}

func TestARunningTurnInNowIsNotMarkedOutsideNow(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Working)},
	})
	if got.Kind != status.Running || got.OutsideNow {
		t.Errorf("kind %s outsideNow %v, want running in Now", got.Kind, got.OutsideNow)
	}
}

func TestIdleOnceTheTurnEndedDone(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Done, t1)},
		Sessions: []session.Summary{sess("s1", session.Idle)},
	})
	check(t, got, want(status.Idle))
}

func TestWaitingWhenTheTurnEndedAsking(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Asking, t1), stop(s, "asking", t1)},
		Sessions: []session.Summary{sess("s1", session.Waiting)},
	})
	check(t, got, want(status.Waiting, func(s *status.Status) {
		s.Skill, s.Ticket, s.Session, s.Reason = "implement", "11", "s1", status.Asking
	}))
}

// The user answered in the Session, or closed it: the Stop point is gone.
func TestAVerdictStopEndsWhenItsSessionIsClosed(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Stuck, t1), stop(s, "stuck", t1)},
	})
	check(t, got, want(status.Idle))
}

// An unapproved Spec in Now is the Approve spec Stop point, whether or not
// Autopilot has recorded it yet; its next step is to-tickets
// (tracker.FeatureStep at StageSpec).
func TestWaitingAtSpecApprovalFromTheTracker(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true, Features: []tracker.Feature{feature("login", atSpec(false))}})
	check(t, got, want(status.Waiting, func(s *status.Status) {
		s.Skill, s.Reason = "to-tickets", status.SpecApproval
	}))
}

func TestARecordedSpecApprovalEndsOnceTheSpecIsApproved(t *testing.T) {
	st := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "to-tickets"}, Kind: autopilot.KindStop,
		Reason: autopilot.ReasonSpecApproval, Time: t0}
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login", atSpec(true))},
		History:  []autopilot.Event{st},
	})
	check(t, got, want(status.Idle))
}

func TestWaitingAtMergeUntilTheTicketLeavesReview(t *testing.T) {
	s := skill("login", "11", "code-review", "s1", t0)
	h := []autopilot.Event{s, classified(s, classify.Done, t1), stop(s, autopilot.ReasonMerge, t2)}
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login", atReview)},
		History:  h, Sessions: []session.Summary{sess("s1", session.Idle)},
	})
	check(t, got, want(status.Waiting, func(s *status.Status) {
		s.Skill, s.Ticket, s.Session, s.Reason = "code-review", "11", "s1", status.Merge
	}))

	merged := feature("login")
	merged.Tickets[0].Closed, merged.Tickets[0].Stage = true, tracker.StageShipped
	got = statusOf(t, "login", status.Input{On: true, Features: []tracker.Feature{merged}, History: h})
	if got.Kind != status.Idle {
		t.Errorf("after the merge: kind %s, want idle", got.Kind)
	}
}

// A Session that exits before its turn ends waits for the user at once,
// before Autopilot's next look records the Stop point.
func TestWaitingWhenTheSessionExitsMidTurn(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Exited)},
	})
	check(t, got, want(status.Waiting, func(s *status.Status) {
		s.Skill, s.Ticket, s.Session, s.Reason = "implement", "11", "s1", status.Exited
	}))
}

// Closing the exited Session hands the step back to the user: nothing waits.
func TestIdleOnceTheExitedSessionIsClosed(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, stop(s, autopilot.ReasonExited, t1)},
	})
	check(t, got, want(status.Idle))
}

func TestFailedWhenASkillDidNotStart(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{failed("login", "11", "implement", "no Agent claude", t0)},
	})
	check(t, got, want(status.Failed, func(s *status.Status) {
		s.Skill, s.Ticket, s.Error = "implement", "11", "no Agent claude"
	}))
}

func retroDone() []autopilot.Event {
	s := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "retro"}, Kind: autopilot.KindSkill, Time: t0, Session: "s9"}
	return []autopilot.Event{s, classified(s, classify.Done, t1)}
}

func TestDoneOnceTheSpecIsClosedAndTheRetroEndedDone(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login", shipped)},
		History:  retroDone(),
		Sessions: []session.Summary{sess("s9", session.Idle)},
	})
	check(t, got, want(status.Done))
}

// Done is a fact about the Feature: it holds after the Feature leaves Now
// or Autopilot is turned off.
func TestDoneHoldsOutsideNowAndWithAutopilotOff(t *testing.T) {
	got := statusOf(t, "login", status.Input{On: false,
		Features: []tracker.Feature{feature("login", shipped, inGroup("later"))},
		History:  retroDone(),
	})
	check(t, got, want(status.Done))
}

func TestNotDoneWhileTheRetroWaits(t *testing.T) {
	s := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "retro"}, Kind: autopilot.KindSkill, Time: t0, Session: "s9"}
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login", shipped)},
		History:  []autopilot.Event{s, classified(s, classify.Asking, t1), stop(s, "asking", t1)},
		Sessions: []session.Summary{sess("s9", session.Waiting)},
	})
	if got.Kind != status.Waiting {
		t.Errorf("kind %s, want waiting", got.Kind)
	}
}

// A closed Spec whose last turn wasn't the retro is not done: an earlier
// Ticket's done turn says nothing about the retro.
func TestNotDoneWithoutTheRetro(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login", shipped)},
		History:  []autopilot.Event{s, classified(s, classify.Done, t1)},
	})
	if got.Kind != status.Idle {
		t.Errorf("kind %s, want idle", got.Kind)
	}
}

// Each Feature reads its own history: another Feature's events, or another
// Project's, don't count.
func TestEachFeatureHasItsOwnStatus(t *testing.T) {
	other := skill("search", "", "to-tickets", "s2", t1)
	foreign := skill("login", "11", "implement", "s3", t1)
	foreign.Project = "/elsewhere"
	got := status.Of(status.Input{Project: proj, On: true,
		Features: []tracker.Feature{feature("login"), feature("search")},
		History:  []autopilot.Event{other, foreign},
		Sessions: []session.Summary{sess("s2", session.Working), sess("s3", session.Working)},
	})
	if len(got) != 2 {
		t.Fatalf("got %d Autopilot statuses, want 2: %+v", len(got), got)
	}
	if got[0].Feature != "login" || got[0].Kind != status.Idle {
		t.Errorf("login: %+v, want idle", got[0])
	}
	if got[1].Feature != "search" || got[1].Kind != status.Running {
		t.Errorf("search: %+v, want running", got[1])
	}
}

// Without the tracker (gh failed) a Feature with a running turn still
// shows it.
func TestARunningTurnShowsWithoutTheTracker(t *testing.T) {
	got := status.Of(status.Input{Project: proj, On: true,
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Working)},
	})
	if len(got) != 1 || got[0].Kind != status.Running {
		t.Errorf("got %+v, want login running", got)
	}
}

// Since is when the Autopilot status last changed, not when the history
// last did: Stamp keeps it while nothing changes and sets it on a change.

func TestStampSetsTheTimeOfAWaitThatComesFromTheTracker(t *testing.T) {
	next := []status.Status{want(status.Waiting, func(s *status.Status) { s.Skill, s.Reason = "to-tickets", status.SpecApproval })}
	got := status.Stamp(nil, next, t2)
	if !got[0].Since.Equal(t2) {
		t.Errorf("since %v, want %v", got[0].Since, t2)
	}
}

func TestStampKeepsTheTimeWhileTheStatusStaysTheSame(t *testing.T) {
	waiting := want(status.Waiting, func(s *status.Status) { s.Skill, s.Ticket, s.Reason = "code-review", "11", status.Merge })
	prev := status.Stamp(nil, []status.Status{waiting}, t1)
	got := status.Stamp(prev, []status.Status{waiting}, t2)
	if !got[0].Since.Equal(t1) {
		t.Errorf("since %v, want it kept at %v", got[0].Since, t1)
	}
}

func TestStampSetsANewTimeWhenAMergeWaitTurnsIdle(t *testing.T) {
	waiting := want(status.Waiting, func(s *status.Status) { s.Skill, s.Ticket, s.Reason = "code-review", "11", status.Merge })
	prev := status.Stamp(nil, []status.Status{waiting}, t1)
	got := status.Stamp(prev, []status.Status{want(status.Idle)}, t2)
	if got[0].Kind != status.Idle || !got[0].Since.Equal(t2) {
		t.Errorf("got %+v, want idle since %v", got[0], t2)
	}
}

func TestStampDoesNotChangeItsInput(t *testing.T) {
	next := []status.Status{want(status.Idle)}
	_ = status.Stamp(nil, next, t2) // only next matters here
	if !next[0].Since.IsZero() {
		t.Errorf("Stamp changed its input: %+v", next[0])
	}
}

// The live pulse (#205): a running Autopilot status carries the tool call
// its Session is on, and the next tool call replaces it.
func TestRunningCarriesTheSessionsCurrentToolCall(t *testing.T) {
	in := status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
	}
	for _, tool := range []string{"Read internal/acp/client.go", "Edit web/src/Board.tsx"} {
		s := sess("s1", session.Working)
		s.Tool = tool
		in.Sessions = []session.Summary{s}
		check(t, statusOf(t, "login", in), want(status.Running, func(s *status.Status) {
			s.Skill, s.Ticket, s.Session, s.Tool = "implement", "11", "s1", tool
		}))
	}
}

// Only a running Autopilot status shows a tool call: at a Stop point the
// turn has ended, whatever the Session still remembers.
func TestAWaitingStatusCarriesNoToolCall(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	waiting := sess("s1", session.Waiting)
	waiting.Tool = "Read internal/acp/client.go"
	got := statusOf(t, "login", status.Input{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Asking, t1), stop(s, "asking", t1)},
		Sessions: []session.Summary{waiting},
	})
	if got.Kind != status.Waiting || got.Tool != "" {
		t.Errorf("got %+v, want waiting with no tool call", got)
	}
}

// A new tool call is not a new Autopilot status: Since stays while the
// turn moves on to its next tool call.
func TestStampKeepsTheTimeWhenOnlyTheToolCallChanges(t *testing.T) {
	running := want(status.Running, func(s *status.Status) {
		s.Skill, s.Ticket, s.Session, s.Tool = "implement", "11", "s1", "Read internal/acp/client.go"
	})
	prev := status.Stamp(nil, []status.Status{running}, t1)
	running.Tool = "Edit web/src/Board.tsx"
	got := status.Stamp(prev, []status.Status{running}, t2)
	if !got[0].Since.Equal(t1) || got[0].Tool != "Edit web/src/Board.tsx" {
		t.Errorf("got %+v, want the new tool call since %v", got[0], t1)
	}
}
