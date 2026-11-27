package flow_test

import (
	"reflect"
	"testing"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/autopilot/flow"
	"github.com/philipsahli/fullharness/internal/autopilot/status"
	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// The flow graph (#206, ADR-0013): one Feature's Stages, its Tickets'
// branches and a dot per skill Autopilot ran. This is a package of pure
// logic, so its tests call it directly (CODING_STANDARDS.md, "Tests go
// through the API").

const proj = "/repo"

func step(ticket, skill string) autopilot.Step {
	return autopilot.Step{Project: proj, Feature: "f", Ticket: ticket, Skill: skill}
}

func ran(ticket, skill, session string) autopilot.Event {
	return autopilot.Event{Step: step(ticket, skill), Kind: autopilot.KindSkill, Session: session}
}

func ended(ticket, skill, session string, v classify.Verdict) autopilot.Event {
	return autopilot.Event{Step: step(ticket, skill), Kind: autopilot.KindClassified, Session: session, Verdict: v}
}

func stopped(ticket, skill, session, reason string) autopilot.Event {
	return autopilot.Event{Step: step(ticket, skill), Kind: autopilot.KindStop, Session: session, Reason: reason}
}

func failed(ticket, skill, err string) autopilot.Event {
	return autopilot.Event{Step: step(ticket, skill), Kind: autopilot.KindFailed, Error: err}
}

func ticket(n string, mod ...func(*tracker.Ticket)) tracker.Ticket {
	t := tracker.Ticket{Number: n, Title: "Ticket " + n, Stage: tracker.StageImplementing}
	for _, m := range mod {
		m(&t)
	}
	return t
}

func closed(t *tracker.Ticket) { t.Closed, t.Stage = true, tracker.StageShipped }

func blockedBy(n string) func(*tracker.Ticket) {
	return func(t *tracker.Ticket) {
		t.Stage = tracker.StageTickets
		t.Blockers = append(t.Blockers, tracker.Blocker{Number: n, Known: true, Open: true})
	}
}

func atReview(t *tracker.Ticket) { t.Stage = tracker.StageReview }

func feature(stage string, approved bool, tickets ...tracker.Ticket) tracker.Feature {
	if tickets == nil {
		tickets = []tracker.Ticket{}
	}
	return tracker.Feature{ID: "f", Stage: stage, Approved: approved, Tickets: tickets, Roadmap: tracker.RoadmapNow}
}

// main is the main line's states, by node name, current marked with "*".
func main(g flow.Graph) map[string]string {
	out := map[string]string{}
	for _, n := range g.Main {
		s := string(n.State)
		if n.Current {
			s += "*"
		}
		out[n.Name] = s
	}
	return out
}

// lane is a lane's nodes as "name:state", current marked with "*".
func lane(l flow.Lane) []string {
	out := []string{}
	for _, n := range l.Nodes {
		s := n.Name + ":" + string(n.State)
		if n.Current {
			s += "*"
		}
		out = append(out, s)
	}
	return out
}

func TestTheMainLineIsTheStagesInOrder(t *testing.T) {
	g := flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageSpec, false), History: nil, Status: status.Status{}})

	var names, kinds []string
	for _, n := range g.Main {
		names = append(names, n.Name)
		kinds = append(kinds, string(n.Kind))
	}
	if want := []string{"idea", "spec", "spec-approval", "tickets", "implementing", "review", "shipped"}; !reflect.DeepEqual(names, want) {
		t.Errorf("main line = %v, want %v", names, want)
	}
	if want := []string{"stage", "stage", "stop", "stage", "stage", "stage", "stage"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if g.Lanes == nil {
		t.Error("lanes is nil, want an empty list for the UI")
	}
}

func TestAnUnapprovedSpecWaitsAtApproveSpec(t *testing.T) {
	st := status.Status{Kind: status.Waiting, Reason: status.SpecApproval, Skill: "to-tickets"}

	got := main(flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageSpec, false), History: nil, Status: st}))

	want := map[string]string{"idea": "passed", "spec": "passed", "spec-approval": "waiting*", "tickets": "todo",
		"implementing": "todo", "review": "todo", "shipped": "todo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("main = %v\nwant %v", got, want)
	}
}

func TestAnUnapprovedSpecWaitsEvenWithAutopilotOff(t *testing.T) {
	st := status.Status{Kind: status.Off, Reason: status.AutopilotOff}

	got := main(flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageSpec, false), History: nil, Status: st}))["spec-approval"]

	if got != "waiting" {
		t.Errorf("spec-approval = %s, want waiting, not current", got)
	}
}

func TestToTicketsRunsOnTheTicketsNode(t *testing.T) {
	h := []autopilot.Event{stopped("", "to-tickets", "", "spec-approval"), ran("", "to-tickets", "s1")}
	st := status.Status{Kind: status.Running, Skill: "to-tickets", Session: "s1"}

	g := flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageSpec, true), History: h, Status: st})

	got := main(g)
	if got["spec-approval"] != "passed" || got["tickets"] != "running*" {
		t.Errorf("main = %v, want spec-approval passed and tickets running*", got)
	}
	if n := g.Main[3]; n.Session != "s1" || n.Skill != "to-tickets" {
		t.Errorf("tickets node = %+v, want session s1 and skill to-tickets", n)
	}
}

func TestAFailedFeatureStepShowsOnItsNode(t *testing.T) {
	h := []autopilot.Event{failed("", "to-tickets", "no Agent claude")}
	st := status.Status{Kind: status.Failed, Skill: "to-tickets"}

	g := flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageSpec, true), History: h, Status: st})

	if n := g.Main[3]; n.State != flow.Failed || !n.Current || n.Error != "no Agent claude" {
		t.Errorf("tickets node = %+v, want failed, current, with the error", n)
	}
}

func TestEachTicketIsALane(t *testing.T) {
	f := feature(tracker.StageImplementing, true, ticket("10"), ticket("11", blockedBy("10")))

	g := flow.Of(flow.Input{Project: proj, Feature: f, History: nil, Status: status.Status{}})

	if len(g.Lanes) != 2 {
		t.Fatalf("lanes = %d, want 2", len(g.Lanes))
	}
	if l := g.Lanes[0]; l.Ticket != "10" || l.Title != "Ticket 10" || len(l.BlockedBy) != 0 {
		t.Errorf("lane 0 = %+v", l)
	}
	if l := g.Lanes[1]; !reflect.DeepEqual(l.BlockedBy, []string{"10"}) || len(l.Nodes) != 0 {
		t.Errorf("lane 1 = %+v, want blocked by 10 and no dots", l)
	}
	if got := main(g); got["tickets"] != "passed" || got["implementing"] != "todo" {
		t.Errorf("main = %v, want tickets passed, implementing todo", got)
	}
}

func TestALaneHasADotPerSkillColouredByOutcome(t *testing.T) {
	h := []autopilot.Event{
		ran("10", "implement", "s1"), ended("10", "implement", "s1", classify.Done),
		failed("10", "code-review", "follow-up failed"),
		ran("10", "code-review", "s1"), ended("10", "code-review", "s1", classify.Done),
		stopped("10", "code-review", "", "merge"),
	}
	st := status.Status{Kind: status.Waiting, Reason: status.Merge, Ticket: "10", Skill: "code-review"}
	f := feature(tracker.StageReview, true, ticket("10", atReview))

	l := flow.Of(flow.Input{Project: proj, Feature: f, History: h, Status: st}).Lanes[0]

	want := []string{"implement:passed", "code-review:failed", "code-review:passed", "merge:waiting*"}
	if got := lane(l); !reflect.DeepEqual(got, want) {
		t.Errorf("lane = %v\nwant %v", got, want)
	}
	if l.Nodes[0].Session != "s1" || l.Nodes[1].Error != "follow-up failed" {
		t.Errorf("nodes = %+v, want the Session on the skill dot and the error on the failed one", l.Nodes)
	}
	if l.Nodes[3].Kind != flow.KindStop || l.Nodes[0].Kind != flow.KindSkill {
		t.Errorf("kinds = %s, %s; want skill and stop", l.Nodes[0].Kind, l.Nodes[3].Kind)
	}
}

func TestAStopPointPassesOnceAutopilotMovesOn(t *testing.T) {
	h := []autopilot.Event{
		ran("10", "implement", "s1"), ended("10", "implement", "s1", classify.Asking),
		stopped("10", "implement", "s1", "asking"),
		ran("10", "implement", "s2"),
	}
	st := status.Status{Kind: status.Running, Skill: "implement", Ticket: "10", Session: "s2"}

	got := lane(flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageImplementing, true, ticket("10")), History: h, Status: st}).Lanes[0])

	want := []string{"implement:passed", "asking:passed", "implement:running*"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lane = %v\nwant %v", got, want)
	}
}

func TestAStopPointStillWaitsWhenAutopilotIsOff(t *testing.T) {
	h := []autopilot.Event{
		ran("10", "implement", "s1"), ended("10", "implement", "s1", classify.Asking),
		stopped("10", "implement", "s1", "asking"),
	}
	st := status.Status{Kind: status.Off, Reason: status.AutopilotOff}

	got := lane(flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageImplementing, true, ticket("10")), History: h, Status: st}).Lanes[0])

	if want := []string{"implement:passed", "asking:waiting"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lane = %v, want %v", got, want)
	}
}

func TestAMergedTicketJoinsBack(t *testing.T) {
	h := []autopilot.Event{
		ran("10", "implement", "s1"), ended("10", "implement", "s1", classify.Done),
		stopped("10", "code-review", "", "merge"),
	}
	f := feature(tracker.StageImplementing, true, ticket("10", closed), ticket("11"))

	l := flow.Of(flow.Input{Project: proj, Feature: f, History: h, Status: status.Status{}}).Lanes[0]

	if !l.Joined {
		t.Error("a closed Ticket's lane doesn't join back")
	}
	if got, want := lane(l), []string{"implement:passed", "merge:passed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lane = %v, want %v", got, want)
	}
}

func TestATurnThatEndedWithoutAVerdictIsNotRunning(t *testing.T) {
	h := []autopilot.Event{ran("10", "implement", "s1"), stopped("10", "implement", "s1", "exited")}
	st := status.Status{Kind: status.Waiting, Reason: status.Exited, Ticket: "10", Skill: "implement", Session: "s1"}

	got := lane(flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageImplementing, true, ticket("10")), History: h, Status: st}).Lanes[0])

	if want := []string{"implement:passed", "exited:waiting*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lane = %v, want %v", got, want)
	}
}

func TestAShippedFeaturePassesEveryStage(t *testing.T) {
	f := feature(tracker.StageShipped, true, ticket("10", closed))
	f.Closed = true
	h := []autopilot.Event{ran("", "retro", "s9"), ended("", "retro", "s9", classify.Done)}

	got := main(flow.Of(flow.Input{Project: proj, Feature: f, History: h, Status: status.Status{Kind: status.Done}}))

	for name, state := range got {
		if state != "passed" {
			t.Errorf("%s = %s, want passed", name, state)
		}
	}
}

func TestOnlyThisFeaturesHistoryCounts(t *testing.T) {
	other := ran("10", "implement", "x")
	other.Feature = "g"
	elsewhere := ran("10", "implement", "y")
	elsewhere.Project = "/other"

	g := flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageImplementing, true, ticket("10")), History: []autopilot.Event{other, elsewhere}, Status: status.Status{}})

	if n := len(g.Lanes[0].Nodes); n != 0 {
		t.Errorf("lane has %d dots from other Features or Projects, want 0", n)
	}
}

func TestOneNodeAtMostIsCurrent(t *testing.T) {
	// Two failed tries of the same step: only the latest is where Autopilot is.
	h := []autopilot.Event{failed("10", "implement", "a"), failed("10", "implement", "b")}
	st := status.Status{Kind: status.Failed, Skill: "implement", Ticket: "10"}

	got := lane(flow.Of(flow.Input{Project: proj, Feature: feature(tracker.StageImplementing, true, ticket("10")), History: h, Status: st}).Lanes[0])

	if want := []string{"implement:failed", "implement:failed*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lane = %v, want %v", got, want)
	}
}
