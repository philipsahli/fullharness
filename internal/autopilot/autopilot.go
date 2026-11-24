// Package autopilot moves Features on by itself (ADR-0009). For Features in
// Roadmap Now, in Projects where the user turned Autopilot on, it starts the
// next step from the next-step rule (tracker/next.go) when a turn of its
// own Session ends done, or when the tracker shows new work. It runs one
// Session per Project at a time and waits at every Stop point.
package autopilot

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// Project is a Project with Autopilot on.
type Project struct {
	Path  string
	Agent string // the Agent Autopilot starts
}

// Start is a new Session for one step.
type Start struct {
	Project string
	Agent   string
	Skill   string
	Ref     string // how the skill's prompt names the Spec or Ticket
	// Branch is the Ticket's branch: the Session runs in its Worktree,
	// which is created if it doesn't exist yet. Empty runs the Session in
	// the Project's main tree (a skill that writes issues).
	Branch string
	Link   tracker.Link
}

// Sessions is what Autopilot reads of fh's Sessions.
type Sessions interface {
	List() []session.Summary
	Get(id string) (session.Detail, error)
}

// Deps is what the Runner needs from the rest of fh.
type Deps struct {
	Projects func() []Project
	// Board returns a Project's Features with Stages derived; refresh
	// skips the tracker cache. ok is false when the tracker can't be read.
	Board    func(project string, refresh bool) (features []tracker.Feature, ok bool)
	Sessions Sessions
	// Start starts a Session and returns its ID.
	Start func(Start) (string, error)
	// FollowUp sends skill on ref to a running Session as its next turn: a
	// Queued message when the Agent is still busy (#93).
	FollowUp func(id, skill, ref string) error
	Classify func(context.Context, classify.Input) classify.Result
	// Load and Save keep the Runner's record in fh's local state, so it
	// survives a restart.
	Load func(*Record) error
	Save func(Record) error
	Log  *slog.Logger
}

// Step names one step: a skill on a Feature, or on one of its Tickets.
type Step struct {
	Project string `json:"project"`
	Feature string `json:"feature"`
	Ticket  string `json:"ticket,omitempty"`
	Skill   string `json:"skill"`
}

// Action is one step Autopilot took.
type Action struct {
	Step
	Time    time.Time `json:"time"`
	Session string    `json:"session"`
	// Verdict is how the Session's last turn ended (empty: it hasn't
	// ended yet); anything but done holds the Project at a Stop point.
	Verdict classify.Verdict `json:"verdict,omitempty"`
	// VerdictAt is when that turn ended, so a slow verdict on an older
	// turn never replaces a newer one.
	VerdictAt time.Time `json:"verdictAt,omitzero"`
}

// Record is every Action, oldest first.
type Record struct {
	Actions []Action `json:"actions"`
}

// Runner is Autopilot.
type Runner struct {
	d Deps

	mu       sync.Mutex // guards rec and steps
	rec      Record
	stepping map[string]*sync.Mutex // Project → held while it steps
}

// New returns a Runner with its record loaded.
func New(d Deps) (*Runner, error) {
	r := &Runner{d: d, stepping: map[string]*sync.Mutex{}}
	if err := d.Load(&r.rec); err != nil {
		return nil, err
	}
	return r, nil
}

// Run looks at every Project with Autopilot on each interval, until ctx
// ends.
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, p := range r.d.Projects() {
				r.Step(p, false)
			}
		}
	}
}

// TurnEnded classifies a turn of an Autopilot Session that ended at at. Done
// moves the Project on at once; anything else is a Stop point, and the
// Project waits until the user's answer ends another turn.
func (r *Runner) TurnEnded(e session.TurnEnd, at time.Time) {
	d, err := r.d.Sessions.Get(e.Session)
	if err != nil || !d.Autopilot {
		return
	}
	in := classify.Input{Session: e.Session, StopReason: e.StopReason,
		PendingPermission: d.Waiting == session.WaitingPermission, Message: lastAgentMessage(d.Transcript)}
	if e.Err != nil {
		in.Err = e.Err.Error()
	}
	verdict := r.d.Classify(context.Background(), in).Verdict

	r.mu.Lock()
	if i := r.lastActionOf(e.Session); i >= 0 && !r.rec.Actions[i].VerdictAt.After(at) {
		r.rec.Actions[i].Verdict, r.rec.Actions[i].VerdictAt = verdict, at
		r.save()
	}
	r.mu.Unlock()
	if verdict != classify.Done {
		return
	}
	for _, p := range r.d.Projects() {
		if p.Path == d.Project {
			r.Step(p, true)
		}
	}
}

// Step takes the next step in p, unless its latest step still holds it, or
// no Feature in Now has one. refresh re-reads the tracker past its cache,
// after a Session changed it.
func (r *Runner) Step(p Project, refresh bool) {
	unlock := r.lockProject(p.Path)
	defer unlock()
	if r.holding(p.Path) {
		return
	}
	features, ok := r.d.Board(p.Path, refresh)
	if !ok {
		return
	}
	sessions := r.d.Sessions.List()
	for _, f := range features {
		if f.Roadmap == tracker.RoadmapNow && r.stepFeature(p, f, sessions) {
			return
		}
	}
}

func (r *Runner) lockProject(path string) (unlock func()) {
	r.mu.Lock()
	m, ok := r.stepping[path]
	if !ok {
		m = &sync.Mutex{}
		r.stepping[path] = m
	}
	r.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// stepFeature takes f's next step, if Autopilot has one; it reports
// whether it did. Merging a PR and closing a Spec are the user's
// (ADR-0008), so a Feature at Review waits for them.
func (r *Runner) stepFeature(p Project, f tracker.Feature, sessions []session.Summary) bool {
	if f.Closed {
		// Retro only on a Feature Autopilot worked on: turning Autopilot on
		// mustn't start a retro of every Feature shipped before.
		if s := tracker.FeatureStep(tracker.StageShipped); s != nil && r.worked(p.Path, f.ID) {
			return r.start(p, Step{Project: p.Path, Feature: f.ID, Skill: s.Skill}, f.Ref, "")
		}
		return false
	}
	if f.Stage == tracker.StageSpec {
		// An unapproved Spec is the Spec approval Stop point.
		if s := tracker.FeatureStep(tracker.StageSpec); s != nil && f.Approved {
			return r.start(p, Step{Project: p.Path, Feature: f.ID, Skill: s.Skill}, f.Ref, "")
		}
		return false
	}
	for _, t := range f.Tickets {
		if t.Next == nil {
			continue
		}
		step := Step{Project: p.Path, Feature: f.ID, Ticket: t.Number, Skill: t.Next.Skill}
		switch {
		case t.Startable:
			if r.start(p, step, t.Ref, t.Branch) {
				return true
			}
		case t.Stage == tracker.StageReview:
			if r.review(p, step, t, sessions) {
				return true
			}
		}
	}
	return false
}

// start starts step in a new Session unless Autopilot took it already.
func (r *Runner) start(p Project, step Step, ref, branch string) bool {
	if r.took(step) {
		return false
	}
	id, err := r.d.Start(Start{Project: p.Path, Agent: p.Agent, Skill: step.Skill, Ref: ref, Branch: branch,
		Link: tracker.Link{Feature: step.Feature, Ticket: step.Ticket}})
	if err != nil {
		// The next look tries again.
		r.d.Log.Info("autopilot start failed", "project", p.Path, "feature", step.Feature, "ticket", step.Ticket, "skill", step.Skill, "err", err)
		return false
	}
	r.record(step, id)
	return true
}

// review runs the review where the change was made: the next message in
// the Ticket's own Autopilot Session, or a new Session in its Worktree once
// that one has exited. A Ticket someone started by hand is theirs.
func (r *Runner) review(p Project, step Step, t tracker.Ticket, sessions []session.Summary) bool {
	if r.took(step) {
		return false
	}
	i := slices.IndexFunc(sessions, func(s session.Summary) bool {
		return s.Autopilot && s.Project == p.Path && s.Link != nil && s.Link.Feature == step.Feature && s.Link.Ticket == step.Ticket
	})
	if i < 0 {
		return false
	}
	if s := sessions[i]; s.State != session.Exited {
		if err := r.d.FollowUp(s.ID, step.Skill, t.Ref); err != nil {
			r.d.Log.Info("autopilot follow-up failed", "session", s.ID, "skill", step.Skill, "err", err)
			return false
		}
		r.record(step, s.ID)
		return true
	}
	return r.start(p, step, t.Ref, t.Branch)
}

// holding reports whether the Project's latest step still holds it: its
// last turn didn't end done. That covers a Session still working, one
// waiting on a permission, a question to the user, and a Session that
// exited before its turn ended (a crash, or fh quit): each waits for the
// user, across restarts. Closing that Session hands the Project back.
func (r *Runner) holding(project string) bool {
	r.mu.Lock()
	var last *Action
	for i := len(r.rec.Actions) - 1; i >= 0; i-- {
		if a := r.rec.Actions[i]; a.Project == project {
			last = &a
			break
		}
	}
	r.mu.Unlock()
	if last == nil || last.Verdict == classify.Done {
		return false
	}
	_, err := r.d.Sessions.Get(last.Session)
	return err == nil // gone: the user closed it and took the step over
}

// lastActionOf is the index of id's latest Action, or -1; the caller holds
// r.mu.
func (r *Runner) lastActionOf(id string) int {
	for i := len(r.rec.Actions) - 1; i >= 0; i-- {
		if r.rec.Actions[i].Session == id {
			return i
		}
	}
	return -1
}

func (r *Runner) took(step Step) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.rec.Actions, func(a Action) bool { return a.Step == step })
}

func (r *Runner) worked(project, feature string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.rec.Actions, func(a Action) bool { return a.Project == project && a.Feature == feature })
}

func (r *Runner) record(step Step, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.Actions = append(r.rec.Actions, Action{Step: step, Time: time.Now().UTC(), Session: id})
	r.save()
}

// save writes the record; the caller holds r.mu. A failed write loses the
// record of this step, which a restart would repeat; the log says so.
func (r *Runner) save() {
	if err := r.d.Save(r.rec); err != nil {
		r.d.Log.Info("save autopilot record failed", "err", err)
	}
}

func lastAgentMessage(items []session.Item) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == "agent" {
			return items[i].Text
		}
	}
	return ""
}
