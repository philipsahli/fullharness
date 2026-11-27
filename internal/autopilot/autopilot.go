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
	// Note is the user's review note, sent with the skill's prompt: a
	// Spec's revision after Reject (ADR-0014). Empty for every other step.
	Note string
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
	// Queued message when the Agent is still busy (#93). note is as in
	// Start.Note.
	FollowUp func(id, skill, ref, note string) error
	// ScreenReview reports whether Ticket t's PR, which Autopilot reviewed
	// done, stops at Screenshot review (ADR-0014); ok is false when it
	// can't tell yet. Nil means no PR ever does.
	ScreenReview func(project string, t tracker.Ticket) (stops, ok bool)
	Classify     func(context.Context, classify.Input) classify.Result
	// Load and Save keep the Runner's record in fh's local state, so it
	// survives a restart.
	Load func(*Record) error
	Save func(Record) error
	// Changed, if set, runs after the history changed, with the Runner's
	// lock held: it must not block or call back into the Runner.
	Changed func()
	Log     *slog.Logger
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

// Record is every Action, oldest first, and the history the Loop view
// (#114) will draw (#112).
type Record struct {
	Actions []Action `json:"actions"`
	History []Event  `json:"history,omitempty"`
	// Revisions are Specs the user rejected, waiting for their revision
	// turn, oldest first: it starts once the Project isn't held, so a
	// Project still never runs two Autopilot turns at once.
	Revisions []Revision `json:"revisions,omitempty"`
}

// Revision is a rejected Spec's revision: its Step runs SkillRevise on
// the Spec named by Ref, with the user's Note.
type Revision struct {
	Step
	Ref  string `json:"ref"`
	Note string `json:"note"`
}

// SkillRevise is the skill that revises a rejected Spec.
const SkillRevise = "to-spec"

// EventKind says what Autopilot did.
type EventKind string

const (
	KindSkill      EventKind = "skill"      // a skill started, in a new Session or as a follow-up turn
	KindClassified EventKind = "classified" // the turn-end classifier decided how a turn ended
	KindStop       EventKind = "stop"       // Autopilot reached a Stop point and waits for the user
	KindFailed     EventKind = "failed"     // a skill didn't start: the Session or follow-up turn failed; Autopilot retries
	KindUser       EventKind = "user"       // the user acted at a Stop point; Answer says how (ADR-0014)
)

// What the user did at a Stop point (KindUser).
const (
	AnswerApprove   = "approve"    // Approve spec: the Spec is ready-for-agent
	AnswerLooksGood = "looks-good" // Screenshot review: the screens are right
	AnswerReject    = "reject"     // Approve spec: the Spec goes back for a revision with the user's note
)

// Stop point reasons besides a turn-end verdict (asking, stuck, unsure).
const (
	ReasonSpecApproval = "spec-approval"     // an unapproved Spec in Now
	ReasonScreenshot   = "screenshot-review" // a reviewed PR that changes a screen waits for the user's look
	ReasonMerge        = "merge"             // a reviewed Ticket's PR waits for the user's merge
	ReasonExited       = "exited"            // the Session exited before its turn ended
)

// Event is one entry in a Feature's history: its Step is the skill it
// concerns. Fields that don't apply to its Kind are empty.
type Event struct {
	Step
	Kind EventKind `json:"kind"`
	// Time is when it happened; for KindClassified, when the turn ended.
	// The classifier answers later, so order by position, not by Time.
	Time    time.Time `json:"time"`
	Session string    `json:"session,omitempty"`
	// Verdict and By are a classified turn end: the verdict and the rule
	// or Provider that gave it.
	Verdict classify.Verdict `json:"verdict,omitempty"`
	By      string           `json:"by,omitempty"`
	// Reason is why Autopilot stopped: a verdict, or one of the Reason
	// constants.
	Reason string `json:"reason,omitempty"`
	// Error is why a skill didn't start (KindFailed): the first try's
	// error; fh.log has every retry.
	Error string `json:"error,omitempty"`
	// Answer is what the user did at a Stop point (KindUser): one of the
	// Answer constants. By stays the classifier's: it never names the user.
	Answer string `json:"answer,omitempty"`
}

// FeatureHistory is one Feature's Events, oldest first.
type FeatureHistory struct {
	Project string  `json:"project"`
	Feature string  `json:"feature"`
	Events  []Event `json:"events"`
}

// History returns the history of every Feature in project ("" for every
// Project), in the order each Feature first appeared.
func (r *Runner) History(project string) []FeatureHistory {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []FeatureHistory{}
	index := map[[2]string]int{}
	for _, e := range r.rec.History {
		if project != "" && e.Project != project {
			continue
		}
		k := [2]string{e.Project, e.Feature}
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, FeatureHistory{Project: e.Project, Feature: e.Feature})
		}
		out[i].Events = append(out[i].Events, e)
	}
	return out
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
// Project waits until the user's answer ends another turn. Once ctx ends
// (fh is closing) it records nothing: the verdict would be about fh
// quitting, not about the turn.
func (r *Runner) TurnEnded(ctx context.Context, e session.TurnEnd, at time.Time) {
	d, err := r.d.Sessions.Get(e.Session)
	if err != nil || !d.Autopilot {
		return
	}
	in := classify.Input{Session: e.Session, StopReason: e.StopReason,
		PendingPermission: d.Waiting == session.WaitingPermission, Message: lastAgentMessage(d.Transcript)}
	if e.Err != nil {
		in.Err = e.Err.Error()
	}
	res := r.d.Classify(ctx, in)
	verdict := res.Verdict
	if ctx.Err() != nil {
		return
	}

	r.mu.Lock()
	if i := r.lastActionOf(e.Session); i >= 0 && !r.rec.Actions[i].VerdictAt.After(at) {
		a := &r.rec.Actions[i]
		a.Verdict, a.VerdictAt = verdict, at
		// A stale verdict is dropped here and from the history alike, so
		// the history shows only the turn ends Autopilot acted on.
		ev := Event{Step: a.Step, Time: at.UTC(), Session: e.Session}
		classified := ev
		classified.Kind, classified.Verdict, classified.By = KindClassified, verdict, res.By
		r.rec.History = append(r.rec.History, classified)
		if res.StopPoint() {
			stop := ev
			stop.Kind, stop.Reason = KindStop, string(verdict)
			r.rec.History = append(r.rec.History, stop)
		}
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
	// The board is read at most once, and only when needed: a Project held
	// by a running turn doesn't read it at all.
	var features []tracker.Feature
	ok, read := false, false
	board := func() ([]tracker.Feature, bool) {
		if !read {
			features, ok = r.d.Board(p.Path, refresh)
			read = true
		}
		return features, ok
	}
	if r.holding(p.Path, board) {
		return
	}
	if r.revise(p) {
		return
	}
	if _, ok := board(); !ok {
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
		s := tracker.FeatureStep(tracker.StageSpec)
		if s != nil && f.Approved {
			return r.start(p, Step{Project: p.Path, Feature: f.ID, Skill: s.Skill}, f.Ref, "")
		}
		if s != nil {
			r.recordUnlessRepeated(Event{Step: Step{Project: p.Path, Feature: f.ID, Skill: s.Skill}, Kind: KindStop, Reason: ReasonSpecApproval})
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
			// Autopilot's review ended done: the PR waits for the user's
			// Screenshot review if it changes a screen, then for the merge.
			// A review that ended any other way is its own Stop point.
			if r.finished(step) {
				if reason := r.reviewedStop(p, step, t); reason != "" {
					r.recordUnlessRepeated(Event{Step: step, Kind: KindStop, Reason: reason})
				}
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
		r.recordUnlessRepeated(Event{Step: step, Kind: KindFailed, Error: err.Error()})
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
		if err := r.d.FollowUp(s.ID, step.Skill, t.Ref, ""); err != nil {
			r.d.Log.Info("autopilot follow-up failed", "session", s.ID, "skill", step.Skill, "err", err)
			r.recordUnlessRepeated(Event{Step: step, Kind: KindFailed, Session: s.ID, Error: err.Error()})
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
//
// A Feature that left Roadmap Now holds the Project only while its turn
// runs, so a Project never has two Autopilot turns at once. Once that turn
// ended, with any verdict, or its Session exited, the wait is no longer
// Autopilot's: the next Feature in Now goes on, and the left-behind
// Session stays open for the user (ADR-0013).
// board reads the Project's board; when it can't be read, the Project
// stays held.
func (r *Runner) holding(project string, board func() ([]tracker.Feature, bool)) bool {
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
	d, err := r.d.Sessions.Get(last.Session)
	if err != nil {
		return false // gone: the user closed it and took the step over
	}
	if last.Verdict == "" && d.State == session.Exited {
		// No turn end will come for this Session, so the history shows the
		// wait here.
		r.recordUnlessRepeated(Event{Step: last.Step, Kind: KindStop, Session: last.Session, Reason: ReasonExited})
	}
	if last.Verdict == "" && d.State != session.Exited {
		return true // the turn still runs
	}
	features, ok := board()
	return !ok || inNow(features, last.Feature)
}

// inNow reports whether Feature id is in Roadmap Now on the board.
func inNow(features []tracker.Feature, id string) bool {
	return slices.ContainsFunc(features, func(f tracker.Feature) bool {
		return f.ID == id && f.Roadmap == tracker.RoadmapNow
	})
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

// recordUnlessRepeated records e, a Stop point or a failed start Autopilot
// met while looking at the Project, unless an event of the same kind,
// skill and reason is already the latest of its Feature or Ticket:
// Autopilot looks again on every interval, and one wait or one failing
// step is one event, even when a retry fails with another error.
func (r *Runner) recordUnlessRepeated(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.rec.History) - 1; i >= 0; i-- {
		if h := r.rec.History[i]; h.Project == e.Project && h.Feature == e.Feature && h.Ticket == e.Ticket {
			if h.Kind == e.Kind && h.Skill == e.Skill && h.Reason == e.Reason {
				return
			}
			break
		}
	}
	e.Time = time.Now().UTC()
	r.rec.History = append(r.rec.History, e)
	r.save()
}

// reviewedStop is the Stop point of a Ticket whose review, step, ended
// done: Screenshot review until the user's Looks good when its PR changes
// a screen, else merge; empty while fh can't tell. A Ticket already at
// either Stop point stays there, so a wait reads gh once, not every look.
func (r *Runner) reviewedStop(p Project, step Step, t tracker.Ticket) string {
	if e, ok := r.Waiting(step.Project, step.Feature, step.Ticket); ok && e.Skill == step.Skill &&
		(e.Reason == ReasonMerge || e.Reason == ReasonScreenshot) {
		return e.Reason
	}
	if r.d.ScreenReview == nil || r.answered(step, AnswerLooksGood) {
		return ReasonMerge
	}
	stops, ok := r.d.ScreenReview(p.Path, t)
	switch {
	case !ok:
		return ""
	case stops:
		return ReasonScreenshot
	default:
		return ReasonMerge
	}
}

// answered reports whether the user gave answer at a Stop point of step
// since Autopilot last ran it.
func (r *Runner) answered(step Step, answer string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.rec.History) - 1; i >= 0; i-- {
		switch e := r.rec.History[i]; {
		case e.Step != step:
		case e.Kind == KindSkill:
			return false
		case e.Kind == KindUser && e.Answer == answer:
			return true
		}
	}
	return false
}

// Waiting returns the Stop point a Feature's Ticket ("" for the Feature
// itself) waits at: its latest event, if that is a Stop point.
func (r *Runner) Waiting(project, feature, ticket string) (Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.rec.History) - 1; i >= 0; i-- {
		if e := r.rec.History[i]; e.Project == project && e.Feature == feature && e.Ticket == ticket {
			return e, e.Kind == KindStop
		}
	}
	return Event{}, false
}

// finished reports whether Autopilot's latest run of step ended done.
func (r *Runner) finished(step Step) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.rec.Actions) - 1; i >= 0; i-- {
		if a := r.rec.Actions[i]; a.Step == step {
			return a.Verdict == classify.Done
		}
	}
	return false
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
	now := time.Now().UTC()
	r.rec.Actions = append(r.rec.Actions, Action{Step: step, Time: now, Session: id})
	r.rec.History = append(r.rec.History, Event{Step: step, Kind: KindSkill, Time: now, Session: id})
	r.save()
}

// save writes the record; the caller holds r.mu. A failed write loses the
// record of this step, which a restart would repeat; the log says so.
func (r *Runner) save() {
	if err := r.d.Save(r.rec); err != nil {
		r.d.Log.Info("save autopilot record failed", "err", err)
	}
	if r.d.Changed != nil {
		r.d.Changed()
	}
}

// Answered records what the user did at a Stop point: step is the step
// the Stop point waited before, answer one of the Answer constants. The
// caller has already written what the answer changes (ADR-0014) and steps
// the Project next.
func (r *Runner) Answered(step Step, answer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.History = append(r.rec.History, Event{Step: step, Kind: KindUser, Time: time.Now().UTC(), Answer: answer})
	r.save()
}

// Rejected records the user's Reject at Approve spec, and the revision it
// asks for: SkillRevise on the Spec ref of Feature feature, with note. The
// caller has already written the note to the Spec (ADR-0014) and steps the
// Project next, which starts the revision.
func (r *Runner) Rejected(project, feature, ref, note string) {
	step := Step{Project: project, Feature: feature, Skill: SkillRevise}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.History = append(r.rec.History, Event{Step: step, Kind: KindUser, Time: time.Now().UTC(), Answer: AnswerReject})
	r.rec.Revisions = append(r.rec.Revisions, Revision{Step: step, Ref: ref, Note: note})
	r.save()
}

// revise starts p's oldest waiting revision, and reports whether it did:
// as the next turn of the Autopilot Session that wrote the Spec, or in a
// new Session when that one is gone or exited. A revision that fails to
// start stays, and the next look tries again.
func (r *Runner) revise(p Project) bool {
	r.mu.Lock()
	i := slices.IndexFunc(r.rec.Revisions, func(v Revision) bool { return v.Project == p.Path })
	if i < 0 {
		r.mu.Unlock()
		return false
	}
	rev := r.rec.Revisions[i]
	r.mu.Unlock()

	id := r.specWriter(rev.Step)
	var err error
	if id != "" {
		err = r.d.FollowUp(id, rev.Skill, rev.Ref, rev.Note)
	} else {
		id, err = r.d.Start(Start{Project: p.Path, Agent: p.Agent, Skill: rev.Skill, Ref: rev.Ref, Note: rev.Note,
			Link: tracker.Link{Feature: rev.Feature}})
	}
	if err != nil {
		r.d.Log.Info("autopilot revision failed", "project", p.Path, "feature", rev.Feature, "err", err)
		r.recordUnlessRepeated(Event{Step: rev.Step, Kind: KindFailed, Session: id, Error: err.Error()})
		return false
	}
	r.mu.Lock()
	if j := slices.Index(r.rec.Revisions, rev); j >= 0 {
		r.rec.Revisions = slices.Delete(r.rec.Revisions, j, j+1)
	}
	r.mu.Unlock()
	r.record(rev.Step, id) // saves the record, with the revision gone
	return true
}

// specWriter is the Autopilot Session that wrote step's Spec, if it hasn't
// exited: the Session whose latest step was a revision of that Spec.
func (r *Runner) specWriter(step Step) string {
	for _, s := range r.d.Sessions.List() {
		if !s.Autopilot || s.Project != step.Project || s.State == session.Exited ||
			s.Link == nil || s.Link.Feature != step.Feature || s.Link.Ticket != "" {
			continue
		}
		r.mu.Lock()
		i := r.lastActionOf(s.ID)
		wrote := i >= 0 && r.rec.Actions[i].Step == step
		r.mu.Unlock()
		if wrote {
			return s.ID
		}
	}
	return ""
}

func lastAgentMessage(items []session.Item) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == "agent" {
			return items[i].Text
		}
	}
	return ""
}
