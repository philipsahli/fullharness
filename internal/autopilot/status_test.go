package autopilot_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// The Autopilot status (GLOSSARY.md, ADR-0013): one per Feature, worked out
// from the Autopilot history, the Issue tracker and the Sessions. These
// tests go through Statuses only.

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

// statusOf runs Statuses and returns Feature id's status.
func statusOf(t *testing.T, id string, in autopilot.StatusInput) autopilot.Status {
	t.Helper()
	in.Project = proj
	for _, s := range autopilot.Statuses(in) {
		if s.Feature == id {
			return s
		}
	}
	t.Fatalf("no status for Feature %s", id)
	return autopilot.Status{}
}

func want(kind autopilot.StatusKind, mod ...func(*autopilot.Status)) autopilot.Status {
	s := autopilot.Status{Project: proj, Feature: "login", Kind: kind}
	for _, m := range mod {
		m(&s)
	}
	return s
}

func check(t *testing.T, got, want autopilot.Status) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status\n%+v\nwant\n%+v", got, want)
	}
}

func TestOffWhenTheProjectHasAutopilotOff(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: false, Features: []tracker.Feature{feature("login")}})
	check(t, got, want(autopilot.Off))
}

func TestOffWhenTheFeatureIsNotInNow(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: true, Features: []tracker.Feature{feature("login", inGroup("next"))}})
	check(t, got, want(autopilot.Off))
}

func TestIdleInNowWithNothingDoneYet(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: true, Features: []tracker.Feature{feature("login")}})
	check(t, got, want(autopilot.Idle))
}

func TestRunningWhileTheSkillsSessionWorks(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Working)},
	})
	check(t, got, want(autopilot.Running, func(s *autopilot.Status) {
		s.Skill, s.Ticket, s.Session, s.Since = "implement", "11", "s1", t0
	}))
}

// A turn keeps running after Autopilot is turned off or the Feature leaves
// Now; it ends by itself (ADR-0013).
func TestRunningEvenWithAutopilotOff(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: false,
		Features: []tracker.Feature{feature("login", inGroup("later"))},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Waiting)},
	})
	if got.Kind != autopilot.Running {
		t.Errorf("kind %s, want running", got.Kind)
	}
}

func TestIdleOnceTheTurnEndedDone(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Done, t1)},
		Sessions: []session.Summary{sess("s1", session.Idle)},
	})
	check(t, got, want(autopilot.Idle, func(s *autopilot.Status) { s.Since = t1 }))
}

func TestWaitingWhenTheTurnEndedAsking(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Asking, t1), stop(s, "asking", t1)},
		Sessions: []session.Summary{sess("s1", session.Waiting)},
	})
	check(t, got, want(autopilot.Waiting, func(s *autopilot.Status) {
		s.Skill, s.Ticket, s.Session, s.Reason, s.Since = "implement", "11", "s1", "asking", t1
	}))
}

// The user answered in the Session, or closed it: the Stop point is gone.
func TestAVerdictStopEndsWhenItsSessionIsClosed(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, classified(s, classify.Stuck, t1), stop(s, "stuck", t1)},
	})
	check(t, got, want(autopilot.Idle, func(s *autopilot.Status) { s.Since = t1 }))
}

func TestWaitingAtSpecApprovalFromTheTracker(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: true, Features: []tracker.Feature{feature("login", atSpec(false))}})
	check(t, got, want(autopilot.Waiting, func(s *autopilot.Status) {
		s.Skill, s.Reason = "to-tickets", autopilot.ReasonSpecApproval
	}))
}

// The Spec approval wait starts the moment the turn that wrote the Spec
// ends, before Autopilot's next look records the Stop point.
func TestWaitingAtSpecApprovalRightAfterARunningTurnEnds(t *testing.T) {
	s := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "to-tickets"}, Kind: autopilot.KindSkill, Time: t0, Session: "s1"}
	in := autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login", atSpec(false))},
		History:  []autopilot.Event{s},
		Sessions: []session.Summary{sess("s1", session.Working)},
	}
	if got := statusOf(t, "login", in); got.Kind != autopilot.Running {
		t.Fatalf("during the turn: kind %s, want running", got.Kind)
	}
	in.History = append(in.History, classified(s, classify.Done, t1))
	in.Sessions = []session.Summary{sess("s1", session.Idle)}
	check(t, statusOf(t, "login", in), want(autopilot.Waiting, func(s *autopilot.Status) {
		s.Skill, s.Reason, s.Since = "to-tickets", autopilot.ReasonSpecApproval, t1
	}))
}

func TestARecordedSpecApprovalEndsOnceTheSpecIsApproved(t *testing.T) {
	st := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "to-tickets"}, Kind: autopilot.KindStop,
		Reason: autopilot.ReasonSpecApproval, Time: t0}
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login", atSpec(true))},
		History:  []autopilot.Event{st},
	})
	check(t, got, want(autopilot.Idle, func(s *autopilot.Status) { s.Since = t0 }))
}

func TestWaitingAtMergeUntilTheTicketLeavesReview(t *testing.T) {
	s := skill("login", "11", "code-review", "s1", t0)
	h := []autopilot.Event{s, classified(s, classify.Done, t1), stop(s, autopilot.ReasonMerge, t2)}
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login", atReview)},
		History:  h, Sessions: []session.Summary{sess("s1", session.Idle)},
	})
	check(t, got, want(autopilot.Waiting, func(s *autopilot.Status) {
		s.Skill, s.Ticket, s.Session, s.Reason, s.Since = "code-review", "11", "s1", autopilot.ReasonMerge, t2
	}))

	merged := feature("login")
	merged.Tickets[0].Closed, merged.Tickets[0].Stage = true, tracker.StageShipped
	got = statusOf(t, "login", autopilot.StatusInput{On: true, Features: []tracker.Feature{merged}, History: h})
	if got.Kind != autopilot.Idle {
		t.Errorf("after the merge: kind %s, want idle", got.Kind)
	}
}

// A Session that exits before its turn ends waits for the user at once,
// before Autopilot's next look records the Stop point.
func TestWaitingWhenTheSessionExitsMidTurn(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Exited)},
	})
	check(t, got, want(autopilot.Waiting, func(s *autopilot.Status) {
		s.Skill, s.Ticket, s.Session, s.Reason, s.Since = "implement", "11", "s1", autopilot.ReasonExited, t0
	}))
}

// Closing the exited Session hands the step back to the user: nothing waits.
func TestIdleOnceTheExitedSessionIsClosed(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{s, stop(s, autopilot.ReasonExited, t1)},
	})
	check(t, got, want(autopilot.Idle, func(s *autopilot.Status) { s.Since = t1 }))
}

func TestFailedWhenASkillDidNotStart(t *testing.T) {
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login")},
		History:  []autopilot.Event{failed("login", "11", "implement", "no Agent claude", t0)},
	})
	check(t, got, want(autopilot.Failed, func(s *autopilot.Status) {
		s.Skill, s.Ticket, s.Error, s.Since = "implement", "11", "no Agent claude", t0
	}))
}

func TestDoneOnceTheSpecIsClosedAndTheRetroEndedDone(t *testing.T) {
	s := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "retro"}, Kind: autopilot.KindSkill, Time: t0, Session: "s9"}
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login", shipped)},
		History:  []autopilot.Event{s, classified(s, classify.Done, t1)},
		Sessions: []session.Summary{sess("s9", session.Idle)},
	})
	check(t, got, want(autopilot.Done, func(s *autopilot.Status) { s.Since = t1 }))
}

func TestNotDoneWhileTheRetroWaits(t *testing.T) {
	s := autopilot.Event{Step: autopilot.Step{Project: proj, Feature: "login", Skill: "retro"}, Kind: autopilot.KindSkill, Time: t0, Session: "s9"}
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login", shipped)},
		History:  []autopilot.Event{s, classified(s, classify.Asking, t1), stop(s, "asking", t1)},
		Sessions: []session.Summary{sess("s9", session.Waiting)},
	})
	if got.Kind != autopilot.Waiting {
		t.Errorf("kind %s, want waiting", got.Kind)
	}
}

// A closed Spec whose last turn wasn't the retro is not done: an earlier
// Ticket's done turn says nothing about the retro.
func TestNotDoneWithoutTheRetro(t *testing.T) {
	s := skill("login", "11", "implement", "s1", t0)
	got := statusOf(t, "login", autopilot.StatusInput{On: true,
		Features: []tracker.Feature{feature("login", shipped)},
		History:  []autopilot.Event{s, classified(s, classify.Done, t1)},
	})
	if got.Kind != autopilot.Idle {
		t.Errorf("kind %s, want idle", got.Kind)
	}
}

// Each Feature reads its own history: another Feature's events, or another
// Project's, don't count.
func TestEachFeatureHasItsOwnStatus(t *testing.T) {
	other := skill("search", "", "to-tickets", "s2", t1)
	foreign := skill("login", "11", "implement", "s3", t1)
	foreign.Project = "/elsewhere"
	in := autopilot.StatusInput{Project: proj, On: true,
		Features: []tracker.Feature{feature("login"), feature("search")},
		History:  []autopilot.Event{other, foreign},
		Sessions: []session.Summary{sess("s2", session.Working), sess("s3", session.Working)},
	}
	got := autopilot.Statuses(in)
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want 2: %+v", len(got), got)
	}
	if got[0].Feature != "login" || got[0].Kind != autopilot.Idle {
		t.Errorf("login: %+v, want idle", got[0])
	}
	if got[1].Feature != "search" || got[1].Kind != autopilot.Running {
		t.Errorf("search: %+v, want running", got[1])
	}
}

// Without the tracker (gh failed, or Autopilot off and the board not read)
// a Feature with a running turn still shows it.
func TestARunningTurnShowsWithoutTheTracker(t *testing.T) {
	got := autopilot.Statuses(autopilot.StatusInput{Project: proj, On: false,
		History:  []autopilot.Event{skill("login", "11", "implement", "s1", t0)},
		Sessions: []session.Summary{sess("s1", session.Working)},
	})
	if len(got) != 1 || got[0].Kind != autopilot.Running {
		t.Errorf("got %+v, want login running", got)
	}
}
