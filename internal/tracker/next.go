package tracker

// Step is the skill that moves a Feature or Ticket on from its Stage: the
// button the Board offers, and what Autopilot (#107) runs next.
type Step struct {
	Skill string `json:"skill"`
	Label string `json:"label"` // the Board button's text
	// Implements: the Session runs in a new Worktree on the Feature's
	// branch and takes every open Ticket (Implement spec). Otherwise it
	// writes issues, so it runs in the main tree.
	Implements bool `json:"implements,omitempty"`
}

var (
	implementSpec = &Step{Skill: "implement-spec", Label: "Implement spec", Implements: true}
	implement     = &Step{Skill: "implement", Label: "Start Session"}
)

// featureSteps and ticketSteps are the next-step rule: a fixed table per
// Stage (#108, Spec #107). The Feature table is the Board's Feature button;
// the Ticket table is per Ticket, so Autopilot can run one Ticket at a
// time. A Stage missing from a table has no next step. Ideas aren't
// Features yet: they start with grillSkill (internal/app).
var (
	featureSteps = map[string]*Step{
		StageSpec:         {Skill: "to-tickets", Label: "Make tickets"},
		StageTickets:      implementSpec,
		StageImplementing: implementSpec,
		StageReview:       implementSpec,
		StageShipped:      {Skill: "retro", Label: "Retro"},
	}
	ticketSteps = map[string]*Step{
		StageTickets:      implement,
		StageImplementing: implement,
		// A Ticket at Review isn't Startable, so the Board never offers
		// this; Autopilot (#110) runs it on the Ticket's PR.
		StageReview: {Skill: "code-review", Label: "Review"},
	}
)

// FeatureStep is the next step of a Feature at stage, or nil.
func FeatureStep(stage string) *Step { return featureSteps[stage] }

// TicketStep is the next step of a Ticket at stage, or nil.
func TicketStep(stage string) *Step { return ticketSteps[stage] }
