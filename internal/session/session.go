// Package session owns Sessions: one Agent conversation in one Worktree.
// It enforces one Session per Worktree, derives each Session's state from
// what the Agent does, keeps the conversation, and reports every change as
// an event for the tabs.
package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/philipsahli/fullharness/internal/acp"
	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/diff"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// Session states (GLOSSARY.md). Waiting here means a permission request is
// open; #7 adds Waiting for a turn that ended unseen.
const (
	Starting = "starting"
	Working  = "working"
	Waiting  = "waiting"
	Idle     = "idle"
	Exited   = "exited"
)

// Answer choices, and what a permission card shows once it's settled.
const (
	ChoiceAllow  = "allow"  // allow_once
	ChoiceDeny   = "deny"   // reject_once
	ChoiceAlways = "always" // allow_always, when the Agent offers it

	StatusPending   = "pending"
	StatusAllowed   = "allowed"
	StatusDenied    = "denied"
	StatusAlways    = "always"
	StatusCancelled = "cancelled" // the turn was cancelled first
	StatusExited    = "exited"    // the Agent exited first
)

// Exited reasons covered in #4; #7 adds the rest.
const (
	ReasonCrashed = "Agent crashed"
	ReasonLogin   = "login needed"
)

// Why a Waiting Session waits (Summary.Waiting).
const (
	WaitingPermission = "permission" // a permission request is open
	WaitingTurn       = "turn"       // a turn ended that nobody has looked at yet
)

// viewerTTL is how long a tab counts as looking at a Session after it last
// said so. A tab repeats it every 15 s while it looks, so a tab that was
// closed or crashed stops counting on its own.
const viewerTTL = 30 * time.Second

// codeAuthRequired is ACP's "authentication required" error code.
const codeAuthRequired = -32000

const (
	ReasonFhQuit = "fh quit"
)

// stderrTail is how many lines of the Agent's stderr an Exited Session
// shows when the Agent crashed.
const stderrTail = 20

// Summary is a Session as the Session list shows it.
type Summary struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Worktree string `json:"worktree"`
	Agent    string `json:"agent"`
	// AgentName is the Agent's display name, e.g. "Claude Code".
	AgentName string `json:"agentName"`
	State     string `json:"state"`
	// AutoApprove is set while the Agent acts without asking first: started
	// with auto-approve, or switched to the Agent's auto mode or option
	// later (syncAutoLocked).
	AutoApprove bool `json:"autoApprove"`
	// Waiting says why a Waiting Session waits: "permission" (a request is
	// open) or "turn" (a turn ended that nobody has looked at yet).
	Waiting string `json:"waiting,omitempty"`
	// Since is when the state (or why it waits) last changed; the Session
	// list orders by it.
	Since time.Time `json:"since"`
	// Login is the command to run in a terminal when the reason is "login
	// needed".
	Login string `json:"login,omitempty"`
	// CanReopen is set when the Session can be reopened once Exited: its
	// Agent offered session/load.
	CanReopen bool `json:"canReopen"`
	// Title is the Worktree's branch until the Agent names the Session.
	Title string `json:"title"`
	// Reason and Stderr explain an Exited Session.
	Reason string   `json:"reason,omitempty"`
	Stderr []string `json:"stderr,omitempty"`
	// Link is the Feature (and Ticket) the Session was started from on the
	// board; nil for a Session started from New Session. An Idea's Session
	// gets a link to its Spec once you confirm it (LinkSpec).
	Link *tracker.Link `json:"link,omitempty"`
	// Idea is the name of the Idea a Grill new idea Session is for (#14).
	Idea string `json:"idea,omitempty"`
	// Commands are the slash commands of the Agent's latest
	// available_commands_update, for the composer's "/" list.
	Commands []Command `json:"commands,omitempty"`
	// Mode is the current session mode, one of Modes; both are empty when
	// the Agent offers no modes.
	Mode  string       `json:"mode,omitempty"`
	Modes []acp.Choice `json:"modes,omitempty"`
	// Options are the Agent's select config options, such as the model.
	Options []acp.Option `json:"options,omitempty"`
	// Usage is the Agent's latest usage_update; nil until it sends one.
	Usage *Usage `json:"usage,omitempty"`
}

// Usage is how much of the context window a Session uses and, when the
// Agent reports it, what the Session has cost so far.
type Usage struct {
	Used int64 `json:"used"` // tokens in context
	Size int64 `json:"size"` // the context window, in tokens
	Cost *Cost `json:"cost,omitempty"`
}

// Cost is a Session's cost so far, in Currency: an ISO 4217 code such as
// "USD", or another unit such as "credits".
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Command is one slash command the Agent offers.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Hint        string `json:"hint,omitempty"` // what to type after the name
}

// Item is one entry of a conversation.
type Item struct {
	Type       string   `json:"type"` // user, agent, tool, permission or notice
	Text       string   `json:"text,omitempty"`
	Tool       *Tool    `json:"tool,omitempty"`
	Permission *Request `json:"permission,omitempty"`
}

// Request is a permission request, as its card and its Inbox entry show it.
type Request struct {
	ID           string      `json:"id"`
	Session      string      `json:"session"`
	SessionTitle string      `json:"sessionTitle"`
	Project      string      `json:"project"`
	Title        string      `json:"title"`
	Kind         string      `json:"kind"`
	Command      string      `json:"command,omitempty"`
	Paths        []string    `json:"paths,omitempty"`
	Content      string      `json:"content,omitempty"` // text content, plain
	Diffs        []diff.File `json:"diffs,omitempty"`   // the edits the tool call would make
	fullDiffs    []diff.File // Diffs uncut, kept only when one is cut; see Manager.FullDiffs
	Always       bool        `json:"always"` // the Agent offers "always allow"
	Status       string      `json:"status"` // StatusPending until settled
	CreatedAt    time.Time   `json:"createdAt"`
}

// Tool is a tool call as its card shows it.
type Tool struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Kind      string      `json:"kind"`
	Status    string      `json:"status"`
	Content   string      `json:"content,omitempty"` // text content, plain
	Diffs     []diff.File `json:"diffs,omitempty"`   // the edits the tool call makes
	fullDiffs []diff.File // Diffs uncut, kept only when one is cut; see Manager.FullDiffs
	RawOutput string      `json:"rawOutput,omitempty"` // raw output, plain
}

// Detail is a Session with its whole conversation.
type Detail struct {
	Summary
	Transcript []Item `json:"transcript"`
}

// Errors the API maps to status codes.
var (
	ErrNotFound    = errors.New("no such Session")
	ErrBusy        = errors.New("the Agent is working; wait for the turn to end or cancel it")
	ErrNotRunning  = errors.New("the Session's Agent is not running")
	ErrWorktreeUse = errors.New("this Worktree already has a Session")
	ErrAnswered    = errors.New("this request was already answered")
	ErrNoRequest   = errors.New("no such permission request")
	ErrNoItem      = errors.New("no such tool call or permission request in this Session")
	ErrChoice      = errors.New("the Agent doesn't offer that choice")
	ErrConfirm     = errors.New("the Session's Agent is running; confirm to stop it and close the Session")
	ErrNoReopen    = errors.New("this Agent can't reopen Sessions (no session/load)")
	ErrNotExited   = errors.New("only an Exited Session can be reopened")
	ErrRemoving    = errors.New("this Worktree is being removed")
	ErrLinked      = errors.New("a Session is linked to it; close the Session first")
	ErrFolderGone  = errors.New("the Worktree's folder is gone")
	ErrNotOffered  = errors.New("the Agent doesn't offer that setting")
)

// Saved is what state.json keeps of a Session across fh restarts. The
// conversation isn't kept: the Agent replays it on session/load.
type Saved struct {
	ID          string `json:"id"`
	ACPID       string `json:"acpSessionId"`
	Agent       string `json:"agent"`
	Project     string `json:"project"`
	Worktree    string `json:"worktree"`
	Title       string `json:"title"`
	AutoApprove bool   `json:"autoApprove"`
	CanLoad     bool   `json:"canLoad"` // the Agent offered session/load
	// Link is kept so a Ticket stays Implementing after a restart, and an
	// Idea keeps its Spec.
	Link *tracker.Link `json:"link,omitempty"`
	Idea
	Settings
}

// Settings are the mode and config option values a Session last had. The
// Agent forgets them when it exits, so Reopen sets them again after
// session/load.
type Settings struct {
	Mode string `json:"mode,omitempty"`
	// Options are in the Agent's order: setting one (the model) can change
	// what a later one (the effort) offers.
	Options []OptionValue `json:"options,omitempty"`
}

// OptionValue is one config option's value.
type OptionValue struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// Config wires a Manager into fh.
type Config struct {
	LogDir    string          // holds logs/<session-id>.log
	Broadcast func(event any) // sends an event to every tab
	TurnEnded func()          // runs after each turn, e.g. a Worktree refresh
	// EnteredWaiting runs when a Session starts Waiting, with what it waits
	// for, for notifications (#8). viewed is set when a tab is looking at it.
	EnteredWaiting func(sum Summary, what string, viewed bool)
	Log            *slog.Logger
	// StartTimeout bounds initialize and session/new. The first Claude Code
	// start can take a while: npx downloads the adapter.
	StartTimeout time.Duration
	// Persist saves every Session that can come back after a restart.
	Persist func([]Saved)
	// Commands receives an Agent's slash command names each time one of its
	// Sessions reports them (available_commands_update), for skill
	// resolution (#13).
	Commands func(agentID string, names []string)
	// Changed runs after a Session is added or closed, so Worktrees can say
	// whether a Session is linked to them.
	Changed func()
}

// Manager holds all Sessions.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions []*session
	closed   bool // set by StopAll: fh is quitting, start nothing new
	// removing holds the Worktrees being removed; Start refuses them.
	removing map[string]bool
}

// New returns an empty Manager.
func New(cfg Config) *Manager { return &Manager{cfg: cfg} }

type session struct {
	m   *Manager
	def agent.Definition

	mu          sync.Mutex
	sum         Summary
	transcript  []Item
	tools       map[string]int // tool call ID → transcript index
	pending     map[string]*pending
	settled     []int // transcript indexes of cards settled since the last afterSettle
	inTurn      bool  // a prompt is running
	ready       bool  // the ACP session exists; before that the Session is Starting
	unseen      bool  // a turn ended that nobody has looked at yet
	loginNeeded bool  // the Agent asked for a login; watch reports that, not a crash
	// cancelRequested is set when you cancel a turn, so it ends Idle even if
	// the Agent answers with another stop reason than "cancelled".
	cancelRequested bool
	canLoad         bool                 // the Agent can reopen this Session (session/load)
	agentKnown      bool                 // the Agent is configured; a restored Session's may not be
	closed          bool                 // removed from fh; nothing more is published
	viewers         map[string]time.Time // tab ID → when it last said it's looking
	lastPublished   string               // state and why it waits, as last sent to tabs
	proc            *acp.Process
	acpID           sdk.SessionId
	stopping        bool
	logPath         string
	// settings are the mode and options to set again on Reopen; the live
	// ones are in sum while the Agent has reported them (settingsLocked).
	settings Settings
	idea     Idea // empty for a Session that is no Idea
}

// pending is an open permission request: where its card is, which option
// each choice maps to, and where the answer goes.
type pending struct {
	index   int // into the transcript, which is only ever appended to
	options map[string]sdk.PermissionOptionId
	answer  chan sdk.PermissionOptionId // "" means cancelled
}

// Launch is what a new Session starts with.
type Launch struct {
	Project  string
	Worktree string // must already exist; the caller checked it may be used
	Branch   string // the Worktree's branch, the Session's first title
	Prompt   string // the starting prompt; may be empty
	// AutoApprove starts the Agent in its auto mode.
	AutoApprove bool
	Link        *tracker.Link
	// Idea is set for a Grill new idea Session.
	Idea Idea
}

// Start creates a Session as l says and launches its Agent.
func (m *Manager) Start(def agent.Definition, l Launch) (Summary, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Summary{}, ErrNotRunning
	}
	for _, s := range m.sessions {
		// An Exited Session frees its Worktree, so a crashed or quit Session
		// doesn't block it until someone closes it.
		if sum := s.summary(); sum.Worktree == l.Worktree && sum.State != Exited {
			m.mu.Unlock()
			return Summary{}, ErrWorktreeUse
		}
	}
	// Checked under the lock ReserveRemoval takes, so a Start that the
	// caller validated before a removal can't go ahead after it.
	if m.removing[l.Worktree] {
		m.mu.Unlock()
		return Summary{}, ErrRemoving
	}
	if _, err := os.Stat(l.Worktree); err != nil {
		m.mu.Unlock()
		return Summary{}, ErrFolderGone
	}
	s := m.newSession(def, Summary{ID: newID(), Project: l.Project, Worktree: l.Worktree, Agent: def.ID,
		State: Starting, Title: l.Branch, AutoApprove: l.AutoApprove, Link: l.Link})
	s.idea = l.Idea
	m.sessions = append(m.sessions, s)
	m.mu.Unlock()

	m.cfg.Log.Info("session starting", "session", s.sum.ID, "agent", def.ID, "worktree", l.Worktree)
	// Saved from the start, so even a Session that fails before it has an
	// ACP session (login needed, say) is listed after a restart.
	m.persist()
	s.publish()
	m.changed()
	go s.run(false, l.Prompt)
	return s.summary(), nil
}

// changed runs Config.Changed; the caller holds no lock, because Changed
// may ask the Manager about its Sessions.
func (m *Manager) changed() {
	if m.cfg.Changed != nil {
		m.cfg.Changed()
	}
}

// Linked reports whether a Session, live or Exited, runs in worktree.
func (m *Manager) Linked(worktree string) bool {
	return slices.ContainsFunc(m.all(), func(s *session) bool { return s.summary().Worktree == worktree })
}

// ReserveRemoval stops Start from using worktree until release is called,
// for removing it. It fails when a Session is linked to the Worktree or a
// removal is already under way; both checks and the reservation happen
// under one lock, so no Session can slip in between.
func (m *Manager) ReserveRemoval(worktree string) (release func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.removing[worktree] {
		return nil, ErrRemoving
	}
	for _, s := range m.sessions {
		if s.summary().Worktree == worktree {
			return nil, ErrLinked
		}
	}
	if m.removing == nil {
		m.removing = map[string]bool{}
	}
	m.removing[worktree] = true
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.removing, worktree)
	}, nil
}

func (m *Manager) newSession(def agent.Definition, sum Summary) *session {
	sum.AgentName, sum.Since = def.Name, time.Now()
	return &session{
		m:          m,
		def:        def,
		agentKnown: true,
		sum:        sum,
		tools:      map[string]int{},
		pending:    map[string]*pending{},
		viewers:    map[string]time.Time{},
		logPath:    filepath.Join(m.cfg.LogDir, sum.ID+".log"),
	}
}

// Restore lists the Sessions saved before fh last quit, Exited with "fh
// quit": their Agents ended with fh (ADR 0002). agentFor finds an Agent
// definition by ID; an Agent no longer defined can't be reopened.
func (m *Manager) Restore(saved []Saved, agentFor func(id string) (agent.Definition, bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sv := range saved {
		def, ok := agentFor(sv.Agent)
		if !ok {
			def = agent.Definition{ID: sv.Agent, Name: sv.Agent}
		}
		s := m.newSession(def, Summary{ID: sv.ID, Project: sv.Project, Worktree: sv.Worktree, Agent: sv.Agent,
			State: Exited, Reason: ReasonFhQuit, Title: sv.Title, AutoApprove: sv.AutoApprove, Link: sv.Link})
		s.idea, s.settings = sv.Idea, sv.Settings
		// canLoad is kept as saved even while the Agent is missing, so
		// Reopen comes back when the Agent is configured again.
		s.acpID, s.canLoad, s.agentKnown = sdk.SessionId(sv.ACPID), sv.CanLoad, ok
		m.sessions = append(m.sessions, s)
	}
}

// persist saves every Session the Agent could reopen later.
func (m *Manager) persist() {
	if m.cfg.Persist == nil {
		return
	}
	saved := []Saved{}
	for _, s := range m.all() {
		s.mu.Lock()
		if !s.closed {
			saved = append(saved, Saved{ID: s.sum.ID, ACPID: string(s.acpID), Agent: s.sum.Agent, Project: s.sum.Project,
				Worktree: s.sum.Worktree, Title: s.sum.Title, AutoApprove: s.sum.AutoApprove, CanLoad: s.canLoad, Link: s.sum.Link,
				Idea: s.idea, Settings: s.settingsLocked()})
		}
		s.mu.Unlock()
	}
	m.cfg.Persist(saved)
}

// Links returns the links of project's Sessions, live or Exited; a closed
// Session is gone and no longer counts.
func (m *Manager) Links(project string) []tracker.Link {
	var out []tracker.Link
	for _, s := range m.all() {
		if sum := s.summary(); sum.Project == project && sum.Link != nil {
			out = append(out, *sum.Link)
		}
	}
	return out
}

// Reopen restarts an Exited Session's Agent in the same Worktree, Agent and
// auto-approve setting. With an ACP session it reloads it (session/load),
// and the Agent replays the conversation; a Session that never got one
// (e.g. login needed at start) starts a fresh one. Checks and the reset
// happen under the Manager's lock, so two Reopens, or a Reopen and a
// Close or quit, can't both go ahead.
func (m *Manager) Reopen(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrNotRunning
	}
	i := slices.IndexFunc(m.sessions, func(o *session) bool { return o.summary().ID == id })
	if i < 0 {
		return ErrNotFound
	}
	s := m.sessions[i]
	worktree := s.summary().Worktree
	for _, o := range m.sessions {
		if sum := o.summary(); o != s && sum.Worktree == worktree && sum.State != Exited {
			return ErrWorktreeUse
		}
	}
	s.mu.Lock()
	if s.sum.State != Exited || s.closed {
		s.mu.Unlock()
		return ErrNotExited
	}
	if !s.reopenableLocked() {
		s.mu.Unlock()
		return ErrNoReopen
	}
	load := s.acpID != ""
	s.resetRunLocked()
	s.mu.Unlock()
	m.cfg.Log.Info("session reopening", "session", id, "load", load)
	m.cfg.Broadcast(map[string]any{"type": "reset", "session": id}) // tabs drop the old conversation
	s.publish()
	go s.run(load, "")
	return nil
}

// reopenableLocked reports whether Reopen can work: the Agent is
// configured, and it can load the Session or there's none to load yet.
func (s *session) reopenableLocked() bool {
	return s.agentKnown && (s.acpID == "" || s.canLoad)
}

// resetRunLocked clears everything one run of the Agent leaves behind, for
// Reopen. The caller holds s.mu.
func (s *session) resetRunLocked() {
	s.sum.State, s.sum.Since = Starting, time.Now()
	s.sum.Reason, s.sum.Stderr, s.sum.Login, s.sum.Waiting = "", nil, "", ""
	s.proc, s.stopping, s.loginNeeded, s.ready = nil, false, false, false
	s.inTurn, s.unseen, s.cancelRequested = false, false, false
	s.transcript, s.tools = nil, map[string]int{} // the Agent replays the conversation
	// The reloaded Agent reports these again; run sets the mode and options
	// back to what they were.
	s.settings = s.settingsLocked()
	s.sum.Commands, s.sum.Mode, s.sum.Modes, s.sum.Options = nil, "", nil, nil
	s.lastPublished = ""
}

// settingsLocked returns the mode and option values to keep: the Agent's
// current ones once it has reported them, else those from before the last
// Reopen or restart. The caller holds s.mu.
func (s *session) settingsLocked() Settings {
	if s.sum.Mode == "" && len(s.sum.Options) == 0 {
		return s.settings
	}
	out := Settings{Mode: s.sum.Mode}
	for _, o := range s.sum.Options {
		out.Options = append(out.Options, OptionValue{ID: o.ID, Value: o.Value})
	}
	return out
}

// Close removes a Session from fh and frees its Worktree. A live Session's
// Agent is stopped first, which the caller must confirm. The Agent keeps
// its own history; fh deletes the Session's log.
func (m *Manager) Close(id string, confirm bool) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	live := s.sum.State != Exited
	if live && !confirm {
		s.mu.Unlock()
		return ErrConfirm
	}
	s.closed, s.stopping = true, true
	proc := s.proc
	s.mu.Unlock()
	m.mu.Lock()
	m.sessions = slices.DeleteFunc(m.sessions, func(o *session) bool { return o == s })
	m.mu.Unlock()
	if live && proc != nil {
		proc.Stop()
	}
	s.settleAll(StatusExited)
	if err := os.Remove(s.logPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.cfg.Log.Info("delete session log failed", "session", id, "err", err)
	}
	m.cfg.Log.Info("session closed", "session", id)
	m.cfg.Broadcast(map[string]any{"type": "closed", "session": id})
	m.publishInbox()
	m.persist()
	m.changed()
	return nil
}

// SweepLogs deletes Session logs in dir that belong to no Session fh
// knows, e.g. from Sessions that were never saved before a crash.
func (m *Manager) SweepLogs() {
	known := map[string]bool{}
	for _, s := range m.all() {
		known[filepath.Base(s.logPath)] = true
	}
	entries, err := os.ReadDir(m.cfg.LogDir)
	if err != nil {
		m.cfg.Log.Debug("session log sweep skipped", "err", err) // a missing folder has nothing to sweep
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") && !known[e.Name()] {
			if err := os.Remove(filepath.Join(m.cfg.LogDir, e.Name())); err != nil {
				m.cfg.Log.Info("delete stale session log failed", "file", e.Name(), "err", err)
			}
		}
	}
}

// List returns every Session's summary, never nil, in the global list's
// order: Waiting (longest waiting first), Working and Starting, Idle,
// Exited (most recent change first within each).
func (m *Manager) List() []Summary {
	out := []Summary{}
	for _, s := range m.all() {
		out = append(out, s.summary())
	}
	slices.SortStableFunc(out, compareForList)
	return out
}

var listGroup = map[string]int{Waiting: 0, Working: 1, Starting: 1, Idle: 2, Exited: 3}

func compareForList(a, b Summary) int {
	if ga, gb := listGroup[a.State], listGroup[b.State]; ga != gb {
		return ga - gb
	}
	if a.State == Waiting {
		return a.Since.Compare(b.Since) // the one waiting longest first
	}
	return b.Since.Compare(a.Since) // most recent change first
}

// Viewing records whether tab is looking at Session id.
func (m *Manager) Viewing(id, tab string, on bool) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	s.viewing(tab, on)
	return nil
}

// Get returns a Session with its conversation.
func (m *Manager) Get(id string) (Detail, error) {
	s, err := m.find(id)
	if err != nil {
		return Detail{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// summaryLocked, not s.sum: it fills the derived fields (canReopen).
	return Detail{Summary: s.summaryLocked(), Transcript: append([]Item{}, s.transcript...)}, nil
}

// FullDiffs returns the uncut diffs of a tool card or permission request in
// a Session, by its ID, for Show full diff. The transcript only carries
// diffs cut at diff.MaxChanged lines, so a large edit doesn't go to every
// tab on every update.
func (m *Manager) FullDiffs(id, item string) ([]diff.File, error) {
	s, err := m.find(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pick := func(cut, full []diff.File) []diff.File {
		if full != nil {
			return full
		}
		return append([]diff.File{}, cut...) // never nil: the API sends []
	}
	if i, ok := s.tools[item]; ok {
		t := s.transcript[i].Tool
		return pick(t.Diffs, t.fullDiffs), nil
	}
	for _, it := range s.transcript {
		if p := it.Permission; p != nil && p.ID == item {
			return pick(p.Diffs, p.fullDiffs), nil
		}
	}
	return nil, ErrNoItem
}

// Prompt sends text as the next turn. Sending is only allowed while Idle,
// so a message never lands in a turn the user didn't write it for.
func (m *Manager) Prompt(id, text string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	if err := s.beginTurn(); err != nil {
		return err
	}
	go s.prompt(text)
	return nil
}

// beginTurn moves the Session from Idle to Working, checked and set under
// one lock, so two sends at once can't both start a turn. The starting
// prompt doesn't come through here: run starts its turn as it sets ready.
func (s *session) beginTurn() error {
	s.mu.Lock()
	state := s.sum.State
	if state == Waiting && s.sum.Waiting == WaitingTurn {
		state = Idle // writing the next message means you've looked
	}
	switch state {
	case Idle:
		s.inTurn, s.unseen, s.cancelRequested = true, false, false
		s.deriveLocked()
		s.mu.Unlock()
		s.publish()
		return nil
	case Exited:
		s.mu.Unlock()
		return ErrNotRunning
	default:
		s.mu.Unlock()
		return ErrBusy
	}
}

// Cancel asks the Agent to end the current turn.
func (m *Manager) Cancel(id string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	proc, sid, state := s.proc, s.acpID, s.sum.State
	s.mu.Unlock()
	if (state != Working && state != Waiting) || proc == nil {
		return nil // nothing to cancel; the turn already ended
	}
	s.mu.Lock()
	s.cancelRequested = true
	s.mu.Unlock()
	// ACP: a client that cancels a turn answers its open permission
	// requests with "cancelled".
	s.settleAll(StatusCancelled)
	return proc.Cancel(context.Background(), sid)
}

// Inbox returns every open permission request, oldest first, never nil.
func (m *Manager) Inbox() []Request {
	out := []Request{}
	for _, s := range m.all() {
		s.mu.Lock()
		for _, p := range s.pending {
			out = append(out, *s.transcript[p.index].Permission)
		}
		s.mu.Unlock()
	}
	slices.SortFunc(out, func(a, b Request) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Answer settles a permission request with choice (ChoiceAllow, ChoiceDeny
// or ChoiceAlways). The Inbox and the Session card are the same request, so
// answering in either place settles both; only the first answer counts.
func (m *Manager) Answer(requestID, choice string) error {
	for _, s := range m.all() {
		if err := s.answer(requestID, choice); !errors.Is(err, ErrNoRequest) {
			return err
		}
	}
	return ErrNoRequest
}

// all returns the Sessions, safe to range over without m.mu.
func (m *Manager) all() []*session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sessions)
}

func (m *Manager) publishInbox() {
	m.cfg.Broadcast(map[string]any{"type": "inbox", "inbox": m.Inbox()})
}

// StopAll ends every Agent process, as when fh quits (ADR 0002).
func (m *Manager) StopAll() {
	m.mu.Lock()
	m.closed = true
	all := slices.Clone(m.sessions)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		s.mu.Lock()
		s.stopping = true
		proc := s.proc
		s.mu.Unlock()
		if proc != nil {
			wg.Go(proc.Stop)
		}
	}
	wg.Wait()
}

func (m *Manager) find(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.sum.ID == id {
			return s, nil
		}
	}
	return nil, ErrNotFound
}

// run starts the Agent, creates the ACP session and sends the starting
// prompt.
func (s *session) run(load bool, prompt string) {
	logFile, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.exit(ReasonCrashed, []string{"open the Session log: " + err.Error()})
		return
	}
	defer func() { _ = logFile.Close() }() // the process holds its own copy of the descriptor

	proc, err := acp.Start(s.def.Command, s.def.Environ(s.summary().AutoApprove), s.summary().Worktree, logFile,
		s.m.cfg.Log.With("session", s.sum.ID), s)
	if err != nil {
		s.exit(ReasonCrashed, []string{err.Error()})
		return
	}
	s.mu.Lock()
	s.proc = proc
	stopping := s.stopping
	s.mu.Unlock()
	go s.watch(proc)
	if stopping {
		// fh began quitting while this Agent was being launched; StopAll
		// saw no process to stop, so stop it here (ADR 0002).
		proc.Stop()
		return
	}

	// A failure here would otherwise leave the Session Starting for good:
	// note why in the Session log, whose tail the Exited Session shows, and
	// stop the Agent so watch reports it.
	fail := func(step string, err error) {
		_, _ = fmt.Fprintf(logFile, "fh: %s failed: %v\n", step, err) // best effort; the log is the only place to say it
		s.m.cfg.Log.Info("agent start failed", "session", s.sum.ID, "step", step, "err", err)
		if authRequired(err) {
			s.markLoginNeeded()
		}
		proc.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.m.cfg.StartTimeout)
	defer cancel()
	canLoad, err := proc.Initialize(ctx)
	if err != nil {
		fail("initialize", err)
		return
	}
	var acpSess acp.Session
	if load {
		s.mu.Lock()
		sid := s.acpID
		s.mu.Unlock()
		acpSess, err = proc.LoadSession(ctx, sid, s.summary().Worktree)
		if err != nil {
			fail("session/load", err)
			return
		}
	} else {
		acpSess, err = proc.NewSession(ctx, s.summary().Worktree)
		if err != nil {
			fail("session/new", err)
			return
		}
		s.mu.Lock()
		s.canLoad = canLoad
		s.mu.Unlock()
	}
	sid := acpSess.ID
	acpSess = s.setAsking(ctx, proc, acpSess)
	if load {
		acpSess = s.restoreSettings(ctx, proc, acpSess)
	}
	s.mu.Lock()
	s.acpID, s.ready = sid, true
	s.sum.Mode, s.sum.Modes, s.sum.Options = acpSess.Mode, acpSess.Modes, acpSess.Options
	// With a prompt, straight from Starting to Working under the same lock
	// that sets ready: anything that derives the state in between (a tab
	// saying it's looking) would otherwise make it Idle, and the starting
	// prompt would be dropped, or a second message let in before it.
	if prompt != "" {
		s.inTurn = true
	}
	s.deriveLocked()
	exited := s.sum.State == Exited
	s.mu.Unlock()
	s.m.cfg.Log.Info("session ready", "session", s.sum.ID)
	s.m.persist()
	s.publish()
	if prompt != "" && !exited {
		s.prompt(prompt)
	}
}

// setAsking puts the Agent in "ask before acting", or auto-approve, by
// mode and config option as its definition says (the environment part was
// set at launch). If that fails, the conversation says so instead of the
// user finding out from an unasked edit. It returns acpSess with the mode
// and options as they are afterwards.
func (s *session) setAsking(ctx context.Context, proc *acp.Process, acpSess acp.Session) acp.Session {
	a, auto := s.def.Asking, s.summary().AutoApprove
	if !a.Known() {
		s.addItem(Item{Type: "notice", Text: "fh doesn't know how to make this Agent ask before it acts, so it follows its own settings; auto-approve has no effect."})
		return acpSess
	}
	want := Settings{Mode: a.Mode.Pick(auto)}
	if a.Option != "" {
		want.Options = []OptionValue{{ID: a.Option, Value: a.Value.Pick(auto)}}
	}
	acpSess, err := apply(ctx, proc, acpSess, want)
	if err == nil {
		return acpSess
	}
	s.m.cfg.Log.Info("agent asking setup failed", "session", s.sum.ID, "err", err)
	text := "fh couldn't switch the Agent to asking before it acts, so it may edit files and run commands without asking."
	if auto {
		text = "fh couldn't switch the Agent to auto-approve, so it keeps its own setting."
	}
	s.addItem(Item{Type: "notice", Text: text})
	return acpSess
}

// restoreSettings sets the mode and options a reopened Session had before.
// It runs after setAsking, so a mode or option chosen in the Session wins
// over the asking default; both agree on auto-approve (syncAutoLocked).
func (s *session) restoreSettings(ctx context.Context, proc *acp.Process, acpSess acp.Session) acp.Session {
	s.mu.Lock()
	want := s.settings
	s.mu.Unlock()
	acpSess, err := apply(ctx, proc, acpSess, want)
	if err != nil {
		s.m.cfg.Log.Info("restore mode and options failed", "session", s.sum.ID, "err", err)
		s.addItem(Item{Type: "notice", Text: "fh couldn't set the mode or options this Session had before: " + err.Error()})
	}
	return acpSess
}

// apply sets want's mode and options on the Agent, skipping what it doesn't
// offer, and returns acpSess as it is afterwards with every problem joined.
func apply(ctx context.Context, proc *acp.Process, acpSess acp.Session, want Settings) (acp.Session, error) {
	var problems []error
	if mode := want.Mode; mode != "" {
		if !acpSess.HasMode(mode) {
			problems = append(problems, fmt.Errorf("mode %q not offered", mode))
		} else if err := proc.SetMode(ctx, acpSess.ID, mode); err != nil {
			problems = append(problems, err)
		} else {
			acpSess.Mode = mode
		}
	}
	for _, o := range want.Options {
		if !acp.HasValue(acpSess.Options, o.ID, o.Value) {
			problems = append(problems, fmt.Errorf("option %s=%s not offered", o.ID, o.Value))
		} else if opts, err := proc.SetOption(ctx, acpSess.ID, o.ID, o.Value); err != nil {
			problems = append(problems, err)
		} else {
			acpSess.Options = afterSet(acpSess.Options, opts, o.ID, o.Value)
		}
	}
	return acpSess, errors.Join(problems...)
}

// afterSet is the option list after option id was set to value: the list
// the Agent answered with, or, if it sent none, the old list with that one
// value changed.
func afterSet(old, answered []acp.Option, id, value string) []acp.Option {
	if len(answered) > 0 {
		return answered
	}
	out := slices.Clone(old)
	for i := range out {
		if out[i].ID == id {
			out[i].Value = value
		}
	}
	return out
}

// SetMode switches Session id to one of the modes its Agent offers.
func (m *Manager) SetMode(id, mode string) error {
	return m.set(id,
		func(sum Summary) bool { return acp.Session{Modes: sum.Modes}.HasMode(mode) },
		func(proc *acp.Process, sid sdk.SessionId) (func(*Summary), error) {
			err := proc.SetMode(context.Background(), sid, mode)
			return func(sum *Summary) { sum.Mode = mode }, err
		})
}

// SetOption sets one of Session id's config options, such as its model.
func (m *Manager) SetOption(id, option, value string) error {
	return m.set(id,
		func(sum Summary) bool { return acp.HasValue(sum.Options, option, value) },
		func(proc *acp.Process, sid sdk.SessionId) (func(*Summary), error) {
			opts, err := proc.SetOption(context.Background(), sid, option, value)
			return func(sum *Summary) { sum.Options = afterSet(sum.Options, opts, option, value) }, err
		})
}

// set changes a live Session's setting if offered says the Agent offers it:
// send tells the Agent and returns the change to the summary, which only
// applies when send succeeded.
func (m *Manager) set(id string, offered func(Summary) bool,
	send func(*acp.Process, sdk.SessionId) (func(*Summary), error)) error {
	s, proc, sid, err := m.live(id)
	if err != nil {
		return err
	}
	if !offered(s.summary()) {
		return ErrNotOffered
	}
	change, err := send(proc, sid)
	if err != nil {
		return err
	}
	s.changeSettings(change)
	return nil
}

// change applies fn to the summary and tells the tabs.
func (s *session) change(fn func(*Summary)) {
	s.mu.Lock()
	fn(&s.sum)
	s.mu.Unlock()
	s.publish()
}

// changeSettings is change for the mode or options, which are saved so
// Reopen can set them again, and which decide auto-approve.
func (s *session) changeSettings(fn func(*Summary)) {
	s.change(func(sum *Summary) {
		fn(sum)
		s.syncAutoLocked()
	})
	s.m.persist()
}

// syncAutoLocked makes AutoApprove say whether the Agent asks first, after
// the mode or the asking option changed in the Session (spec #1, story
// 40): the auto value turns it on, any other value off. Before the Session
// is ready, setAsking hasn't run yet and AutoApprove is what it will
// apply, so it stays. The caller holds s.mu.
func (s *session) syncAutoLocked() {
	a := s.def.Asking
	if !s.ready {
		return
	}
	if a.Mode.Auto != "" && s.sum.Mode != "" {
		s.sum.AutoApprove = s.sum.Mode == a.Mode.Auto
	}
	if a.Option == "" {
		return
	}
	for _, o := range s.sum.Options {
		if o.ID == a.Option {
			s.sum.AutoApprove = o.Value == a.Value.Auto
		}
	}
}

// live finds Session id with its running Agent and ACP session.
func (m *Manager) live(id string) (*session, *acp.Process, sdk.SessionId, error) {
	s, err := m.find(id)
	if err != nil {
		return nil, nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready || s.sum.State == Exited || s.proc == nil {
		return nil, nil, "", ErrNotRunning
	}
	return s, s.proc, s.acpID, nil
}

// watch turns an unexpected process exit into Exited "Agent crashed".
func (s *session) watch(proc *acp.Process) {
	<-proc.Done()
	s.mu.Lock()
	stopping, login := s.stopping, s.loginNeeded
	s.mu.Unlock()
	switch {
	case stopping:
		s.exit(ReasonFhQuit, nil)
		return
	case login:
		s.exit(ReasonLogin, nil)
		return
	}
	s.m.cfg.Log.Info("agent exited", "session", s.sum.ID, "err", proc.ExitErr())
	s.exit(ReasonCrashed, lastLines(s.logPath, stderrTail))
}

func (s *session) prompt(text string) {
	s.mu.Lock()
	proc, sid := s.proc, s.acpID
	s.mu.Unlock()
	s.addItem(Item{Type: "user", Text: text})
	reason, err := proc.Prompt(context.Background(), sid, text)
	if err != nil {
		s.m.cfg.Log.Info("prompt failed", "session", s.sum.ID, "err", err)
		select {
		case <-proc.Done():
			return // watch reports the exit
		default:
		}
		if authRequired(err) {
			s.markLoginNeeded()
			proc.Stop() // watch reports "login needed"
			return
		}
		// The Agent is alive but refused the turn; show it and let the
		// user try again.
		s.addItem(Item{Type: "notice", Text: "The Agent answered with an error: " + err.Error()})
		s.endTurn(false)
		return
	}
	cancelledTurn := reason == sdk.StopReasonCancelled
	if cancelledTurn {
		s.addItem(Item{Type: "notice", Text: "Turn cancelled."})
	}
	// A turn you cancelled needs no second look; any other ending (end_turn,
	// max_tokens, refusal) waits for you.
	s.endTurn(cancelledTurn)
	if s.m.cfg.TurnEnded != nil {
		s.m.cfg.TurnEnded()
	}
}

// Update applies one session/update from the Agent (acp.Handler).
func (s *session) Update(n sdk.SessionNotification) {
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			s.appendText("agent", t.Text)
		}
	case u.UserMessageChunk != nil:
		// Sent when the Agent replays a reopened Session's conversation.
		if t := u.UserMessageChunk.Content.Text; t != nil {
			s.appendText("user", t.Text)
		}
	case u.ToolCall != nil:
		tc := u.ToolCall
		s.upsertTool(string(tc.ToolCallId), func(t *Tool) {
			t.Title, t.Kind, t.Status = tc.Title, string(tc.Kind), string(tc.Status)
			if t.Status == "" {
				t.Status = "pending"
			}
			t.Content, t.Diffs, t.fullDiffs = toolContent(tc.Content)
			t.RawOutput = rawText(tc.RawOutput)
		})
	case u.ToolCallUpdate != nil:
		tu := u.ToolCallUpdate
		s.upsertTool(string(tu.ToolCallId), func(t *Tool) {
			if v, ok := tu.Title.Value(); ok {
				t.Title = v
			}
			if v, ok := tu.Kind.Value(); ok {
				t.Kind = string(v)
			}
			if v, ok := tu.Status.Value(); ok {
				t.Status = string(v)
			}
			if v, ok := tu.Content.Value(); ok {
				t.Content, t.Diffs, t.fullDiffs = toolContent(v)
			}
			if v, ok := tu.RawOutput.Value(); ok {
				t.RawOutput = rawText(v)
			}
		})
	case u.SessionInfoUpdate != nil:
		if title, ok := u.SessionInfoUpdate.Title.Value(); ok && title != "" {
			s.retitle(title)
		}
	case u.CurrentModeUpdate != nil:
		s.changeSettings(func(sum *Summary) { sum.Mode = string(u.CurrentModeUpdate.CurrentModeId) })
	case u.UsageUpdate != nil:
		uu := u.UsageUpdate
		usage := &Usage{Used: uu.Used, Size: uu.Size}
		if uu.Cost != nil {
			usage.Cost = &Cost{Amount: uu.Cost.Amount, Currency: uu.Cost.Currency}
		}
		s.change(func(sum *Summary) { sum.Usage = usage })
	case u.ConfigOptionUpdate != nil:
		s.changeSettings(func(sum *Summary) { sum.Options = acp.Options(u.ConfigOptionUpdate.ConfigOptions) })
	case u.AvailableCommandsUpdate != nil:
		names := []string{}
		cmds := []Command{}
		for _, c := range u.AvailableCommandsUpdate.AvailableCommands {
			names = append(names, c.Name)
			cmd := Command{Name: c.Name, Description: c.Description}
			if c.Input != nil && c.Input.Unstructured != nil {
				cmd.Hint = c.Input.Unstructured.Hint
			}
			cmds = append(cmds, cmd)
		}
		s.change(func(sum *Summary) { sum.Commands = cmds })
		if s.m.cfg.Commands != nil {
			s.m.cfg.Commands(s.def.ID, names)
		}
	}
}

// retitle renames the Session, including its open requests, so the Inbox
// shows the current title.
func (s *session) retitle(title string) {
	s.mu.Lock()
	s.sum.Title = title
	var changed []int
	for _, p := range s.pending {
		req := *s.transcript[p.index].Permission
		req.SessionTitle = title
		s.transcript[p.index].Permission = &req
		changed = append(changed, p.index)
	}
	items := make([]Item, len(changed))
	for i, idx := range changed {
		items[i] = s.transcript[idx]
	}
	s.mu.Unlock()
	for i, idx := range changed {
		s.emit("item", idx, "item", items[i])
	}
	s.publish()
	if len(changed) > 0 {
		s.m.publishInbox()
	}
	s.m.persist()
}

// Permission shows a permission request as a card and in the Inbox, and
// waits for the user's answer (acp.Handler). The Session is Waiting while
// any request is open.
func (s *session) Permission(ctx context.Context, r sdk.RequestPermissionRequest) sdk.RequestPermissionResponse {
	req := Request{ID: newID(), Status: StatusPending, CreatedAt: time.Now()}
	tc := r.ToolCall
	req.Title, _ = tc.Title.Value()
	if k, ok := tc.Kind.Value(); ok {
		req.Kind = string(k)
	}
	if raw, ok := tc.RawInput.Value(); ok {
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &in) == nil {
			req.Command = in.Command
		}
	}
	if locs, ok := tc.Locations.Value(); ok {
		for _, l := range locs {
			req.Paths = append(req.Paths, l.Path)
		}
	}
	if cs, ok := tc.Content.Value(); ok {
		req.Content, req.Diffs, req.fullDiffs = toolContent(cs)
	}
	p := &pending{options: map[string]sdk.PermissionOptionId{}, answer: make(chan sdk.PermissionOptionId, 1)}
	for _, o := range r.Options {
		// The first option of each kind; never reject_always, which would
		// change the Agent's rules for the rest of the Session.
		choice := map[sdk.PermissionOptionKind]string{
			sdk.PermissionOptionKindAllowOnce:   ChoiceAllow,
			sdk.PermissionOptionKindRejectOnce:  ChoiceDeny,
			sdk.PermissionOptionKindAllowAlways: ChoiceAlways,
		}[o.Kind]
		if _, seen := p.options[choice]; choice != "" && !seen {
			p.options[choice] = o.OptionId
		}
	}
	_, req.Always = p.options[ChoiceAlways]

	s.mu.Lock()
	if s.sum.State == Exited {
		s.mu.Unlock()
		return cancelled()
	}
	req.Session, req.SessionTitle, req.Project = s.sum.ID, s.sum.Title, s.sum.Project
	s.transcript = append(s.transcript, Item{Type: "permission", Permission: &req})
	p.index = len(s.transcript) - 1
	s.pending[req.ID] = p
	s.deriveLocked()
	s.mu.Unlock()
	s.emit("item", p.index, "item", Item{Type: "permission", Permission: &req})
	s.publish()
	s.m.publishInbox()

	select {
	case opt := <-p.answer:
		if opt == "" {
			return cancelled()
		}
		return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{
			Selected: &sdk.RequestPermissionOutcomeSelected{OptionId: opt},
		}}
	case <-ctx.Done():
		// The SDK ends this context when the Agent withdraws this one
		// request ($/cancel_request) or goes away; settle only this one.
		status := StatusCancelled
		if s.agentGone() {
			status = StatusExited
		}
		s.settleOne(req.ID, status)
		return cancelled()
	}
}

func cancelled() sdk.RequestPermissionResponse {
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{Cancelled: &sdk.RequestPermissionOutcomeCancelled{}}}
}

func (s *session) answer(requestID, choice string) error {
	s.mu.Lock()
	p, ok := s.pending[requestID]
	if !ok {
		answered := slices.ContainsFunc(s.transcript, func(it Item) bool {
			return it.Permission != nil && it.Permission.ID == requestID
		})
		s.mu.Unlock()
		if answered {
			return ErrAnswered
		}
		return ErrNoRequest
	}
	opt, offered := p.options[choice]
	if !offered {
		s.mu.Unlock()
		return ErrChoice
	}
	status := map[string]string{ChoiceAllow: StatusAllowed, ChoiceDeny: StatusDenied, ChoiceAlways: StatusAlways}[choice]
	s.settleLocked(requestID, status, opt)
	s.mu.Unlock()
	s.afterSettle()
	return nil
}

// agentGone reports whether the Agent process has exited. A request's
// context ends as the connection drops, a moment before the process is
// reaped, so it waits up to a second for the exit; an Agent that withdrew
// one request keeps running and is told apart that way.
func (s *session) agentGone() bool {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return false
	}
	select {
	case <-proc.Done():
		return true
	case <-time.After(time.Second):
		return false
	}
}

// settleOne settles request id with status, if it's still open.
func (s *session) settleOne(id, status string) {
	s.mu.Lock()
	if _, open := s.pending[id]; !open {
		s.mu.Unlock()
		return
	}
	s.settleLocked(id, status, "")
	s.mu.Unlock()
	s.afterSettle()
}

// settleAll settles every open request with status (cancelled or exited);
// the Agent gets the cancelled outcome.
func (s *session) settleAll(status string) {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	for id := range s.pending {
		s.settleLocked(id, status, "")
	}
	s.mu.Unlock()
	s.afterSettle()
}

// settleLocked closes one request; the caller holds s.mu and calls
// afterSettle once it has released it.
func (s *session) settleLocked(id, status string, opt sdk.PermissionOptionId) {
	p := s.pending[id]
	delete(s.pending, id)
	req := *s.transcript[p.index].Permission
	req.Status = status
	s.transcript[p.index].Permission = &req
	s.settled = append(s.settled, p.index)
	s.deriveLocked()
	p.answer <- opt // buffered and written once: never blocks
}

// afterSettle tells the tabs about settled cards, the state and the Inbox.
func (s *session) afterSettle() {
	s.mu.Lock()
	settled := s.settled
	s.settled = nil
	items := make([]Item, len(settled))
	for i, idx := range settled {
		items[i] = s.transcript[idx]
	}
	s.mu.Unlock()
	for i, idx := range settled {
		s.emit("item", idx, "item", items[i])
	}
	s.publish()
	s.m.publishInbox()
}

// appendText adds text to the last item if it's a message of the same kind,
// else starts one.
func (s *session) appendText(kind, text string) {
	s.mu.Lock()
	n := len(s.transcript)
	if n > 0 && s.transcript[n-1].Type == kind {
		s.transcript[n-1].Text += text
		s.mu.Unlock()
		s.emit("chunk", n-1, "text", text)
		return
	}
	s.mu.Unlock()
	s.addItem(Item{Type: kind, Text: text})
}

func (s *session) upsertTool(id string, apply func(*Tool)) {
	s.mu.Lock()
	i, ok := s.tools[id]
	if !ok {
		s.transcript = append(s.transcript, Item{Type: "tool", Tool: &Tool{ID: id}})
		i = len(s.transcript) - 1
		s.tools[id] = i
	}
	t := *s.transcript[i].Tool
	apply(&t)
	s.transcript[i].Tool = &t
	item := s.transcript[i]
	s.mu.Unlock()
	s.emit("item", i, "item", item)
}

func (s *session) addItem(it Item) {
	s.mu.Lock()
	s.transcript = append(s.transcript, it)
	i := len(s.transcript) - 1
	s.mu.Unlock()
	s.emit("item", i, "item", it)
}

// endTurn marks the turn over. Unless you cancelled it, the Session waits
// for you to look.
func (s *session) endTurn(cancelled bool) {
	s.mu.Lock()
	s.inTurn = false
	// Seen already if you cancelled it, or if a tab is looking right now.
	s.unseen = !cancelled && !s.cancelRequested && !s.viewedLocked()
	s.deriveLocked()
	s.mu.Unlock()
	s.publish()
}

// viewing records whether tab is looking at the Session (its conversation
// is open in a visible tab). Looking clears a finished turn's wait, and a
// turn that ends while someone looks never waits.
func (s *session) viewing(tab string, on bool) {
	s.mu.Lock()
	if !on {
		delete(s.viewers, tab)
		s.mu.Unlock()
		return
	}
	s.viewers[tab] = time.Now()
	changed := s.unseen
	s.unseen = false
	s.deriveLocked()
	s.mu.Unlock()
	if changed {
		s.publish()
	}
}

// viewedLocked reports whether a tab looked within viewerTTL; the caller
// holds s.mu.
func (s *session) viewedLocked() bool {
	for tab, at := range s.viewers {
		if time.Since(at) < viewerTTL {
			return true
		}
		delete(s.viewers, tab)
	}
	return false
}

func (s *session) markLoginNeeded() {
	s.mu.Lock()
	s.loginNeeded = true
	s.mu.Unlock()
}

// deriveLocked sets the Session state from the facts. The states are
// GLOSSARY.md's; the order they're checked in is fh's choice, so that what
// needs you wins: Exited stays; not ready is Starting; an open permission
// request is Waiting (even mid-turn); a running turn is Working; a finished
// turn nobody has seen is Waiting; else Idle. The caller holds s.mu.
func (s *session) deriveLocked() {
	if s.sum.State == Exited {
		return
	}
	state, waiting := Idle, ""
	switch {
	case !s.ready && !s.inTurn:
		state = Starting
	case len(s.pending) > 0:
		state, waiting = Waiting, WaitingPermission
	case s.inTurn:
		state = Working
	case s.unseen:
		state, waiting = Waiting, WaitingTurn
	}
	if state != s.sum.State || waiting != s.sum.Waiting {
		s.sum.State, s.sum.Waiting, s.sum.Since = state, waiting, time.Now()
	}
}

func (s *session) exit(reason string, stderr []string) {
	s.mu.Lock()
	s.sum.State, s.sum.Waiting, s.sum.Since = Exited, "", time.Now()
	s.sum.Reason, s.sum.Stderr = reason, stderr
	if reason == ReasonLogin {
		s.sum.Login = s.def.Login
	}
	s.mu.Unlock()
	// After Exited is set, so settling doesn't flash Working first.
	s.settleAll(StatusExited)
	s.m.cfg.Log.Info("session exited", "session", s.sum.ID, "reason", reason)
	s.publish()
}

func (s *session) summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryLocked()
}

func (s *session) summaryLocked() Summary {
	sum := s.sum
	sum.CanReopen, sum.Idea = s.reopenableLocked(), s.idea.Name
	return sum
}

// emit sends a conversation event: kind "item" (a new or replaced item at
// index) or "chunk" (text appended to the agent message at index).
func (s *session) emit(kind string, index int, key string, value any) {
	s.m.cfg.Broadcast(map[string]any{"type": kind, "session": s.sum.ID, "index": index, key: value})
}

func (s *session) publish() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	sum := s.summaryLocked()
	key := sum.State + "/" + sum.Waiting
	entered := sum.State == Waiting && key != s.lastPublished
	s.lastPublished = key
	what, viewed := "", false
	if entered {
		what, viewed = s.waitingForLocked(), s.viewedLocked()
	}
	s.mu.Unlock()
	s.m.cfg.Broadcast(map[string]any{"type": "session", "session": sum})
	if entered && s.m.cfg.EnteredWaiting != nil {
		s.m.cfg.EnteredWaiting(sum, what, viewed)
	}
}

// waitingForLocked says in a few words what a Waiting Session waits for,
// for a notification; the caller holds s.mu.
func (s *session) waitingForLocked() string {
	if s.sum.Waiting == WaitingTurn {
		return s.sum.AgentName + " finished its turn"
	}
	// Name the oldest open request: the one the Inbox lists first.
	var oldest *Request
	for _, p := range s.pending {
		if r := s.transcript[p.index].Permission; oldest == nil || r.CreatedAt.Before(oldest.CreatedAt) {
			oldest = r
		}
	}
	if oldest != nil {
		return s.sum.AgentName + " asks to run " + oldest.Title
	}
	return s.sum.AgentName + " is waiting"
}

// toolContent splits a tool call's content into its text, joined, and its
// diffs as line diffs. No old text means a new file. When any diff is cut,
// full holds them all uncut; otherwise it is nil, so only large edits are
// kept twice.
func toolContent(cs []sdk.ToolCallContent) (text string, files, full []diff.File) {
	var parts []string
	type change struct{ path, old, new string }
	var changes []change
	cut := false
	for _, c := range cs {
		switch {
		case c.Content != nil && c.Content.Content.Text != nil:
			parts = append(parts, c.Content.Content.Text.Text)
		case c.Diff != nil:
			old := ""
			if c.Diff.OldText != nil {
				old = *c.Diff.OldText
			}
			f := diff.Lines(c.Diff.Path, old, c.Diff.NewText)
			files = append(files, f)
			changes = append(changes, change{c.Diff.Path, old, c.Diff.NewText})
			cut = cut || f.Truncated
		}
	}
	if cut {
		for _, c := range changes {
			full = append(full, diff.Full(c.path, c.old, c.new))
		}
	}
	return strings.Join(parts, "\n"), files, full
}

// rawText shows raw output as plain text: a JSON string unquoted, anything
// else as its JSON.
func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// lastLines returns the last n lines of the file at path.
func lastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil // no log, nothing to show
	}
	defer func() { _ = f.Close() }() // read-only
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

func newID() string { return strings.ToLower(rand.Text()[:10]) }

// authRequired reports whether err is ACP's "authentication required"
// (-32000): the Agent needs a login in a terminal before it can work.
func authRequired(err error) bool {
	var re *sdk.RequestError
	return errors.As(err, &re) && re.Code == codeAuthRequired
}
