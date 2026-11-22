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
	// AutoApprove is set when the Session started in the Agent's auto mode.
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
	ID           string    `json:"id"`
	Session      string    `json:"session"`
	SessionTitle string    `json:"sessionTitle"`
	Project      string    `json:"project"`
	Title        string    `json:"title"`
	Kind         string    `json:"kind"`
	Command      string    `json:"command,omitempty"`
	Paths        []string  `json:"paths,omitempty"`
	Content      string    `json:"content,omitempty"` // text and diffs, plain
	Always       bool      `json:"always"`            // the Agent offers "always allow"
	Status       string    `json:"status"`            // StatusPending until settled
	CreatedAt    time.Time `json:"createdAt"`
}

// Tool is a tool call as its card shows it.
type Tool struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Content   string `json:"content,omitempty"`   // text content, plain
	RawOutput string `json:"rawOutput,omitempty"` // raw output, plain
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
	ErrChoice      = errors.New("the Agent doesn't offer that choice")
	ErrConfirm     = errors.New("the Session's Agent is running; confirm to stop it and close the Session")
	ErrNoReopen    = errors.New("this Agent can't reopen Sessions (no session/load)")
	ErrNotExited   = errors.New("only an Exited Session can be reopened")
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
}

// Manager holds all Sessions.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions []*session
	closed   bool // set by StopAll: fh is quitting, start nothing new
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
	closed          bool                 // removed from fh; nothing more is published
	viewers         map[string]time.Time // tab ID → when it last said it's looking
	lastPublished   string               // state and why it waits, as last sent to tabs
	proc            *acp.Process
	acpID           sdk.SessionId
	stopping        bool
	logPath         string
}

// pending is an open permission request: where its card is, which option
// each choice maps to, and where the answer goes.
type pending struct {
	index   int // into the transcript, which is only ever appended to
	options map[string]sdk.PermissionOptionId
	answer  chan sdk.PermissionOptionId // "" means cancelled
}

// Start creates a Session for worktree in project and launches its Agent.
// The Worktree must already exist; the caller checked it may be used.
// branch is the Worktree's branch, the Session's first title. autoApprove
// starts the Agent in its auto mode.
func (m *Manager) Start(def agent.Definition, project, worktree, branch, prompt string, autoApprove bool) (Summary, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Summary{}, ErrNotRunning
	}
	for _, s := range m.sessions {
		// An Exited Session frees its Worktree. Until Close exists (#9) that
		// is the only way a crashed Session's Worktree becomes usable again.
		if sum := s.summary(); sum.Worktree == worktree && sum.State != Exited {
			m.mu.Unlock()
			return Summary{}, ErrWorktreeUse
		}
	}
	s := m.newSession(def, Summary{ID: newID(), Project: project, Worktree: worktree, Agent: def.ID,
		State: Starting, Title: branch, AutoApprove: autoApprove})
	m.sessions = append(m.sessions, s)
	m.mu.Unlock()

	m.cfg.Log.Info("session starting", "session", s.sum.ID, "agent", def.ID, "worktree", worktree)
	s.publish()
	go s.run(false, prompt)
	return s.summary(), nil
}

func (m *Manager) newSession(def agent.Definition, sum Summary) *session {
	sum.AgentName, sum.Since = def.Name, time.Now()
	return &session{
		m:       m,
		def:     def,
		sum:     sum,
		tools:   map[string]int{},
		pending: map[string]*pending{},
		viewers: map[string]time.Time{},
		logPath: filepath.Join(m.cfg.LogDir, sum.ID+".log"),
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
			State: Exited, Reason: ReasonFhQuit, Title: sv.Title, AutoApprove: sv.AutoApprove})
		s.acpID, s.canLoad = sdk.SessionId(sv.ACPID), sv.CanLoad && ok
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
		if s.acpID != "" && !s.closed {
			saved = append(saved, Saved{ID: s.sum.ID, ACPID: string(s.acpID), Agent: s.sum.Agent, Project: s.sum.Project,
				Worktree: s.sum.Worktree, Title: s.sum.Title, AutoApprove: s.sum.AutoApprove, CanLoad: s.canLoad})
		}
		s.mu.Unlock()
	}
	m.cfg.Persist(saved)
}

// Reopen restarts an Exited Session's Agent and reloads its conversation
// with session/load, in the same Worktree, Agent and auto-approve setting.
func (m *Manager) Reopen(id string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return ErrNotRunning
	}
	s.mu.Lock()
	switch {
	case s.sum.State != Exited:
		s.mu.Unlock()
		return ErrNotExited
	case !s.canLoad || s.acpID == "":
		s.mu.Unlock()
		return ErrNoReopen
	}
	worktree := s.sum.Worktree
	s.mu.Unlock()
	for _, o := range m.all() {
		if sum := o.summary(); o != s && sum.Worktree == worktree && sum.State != Exited {
			return ErrWorktreeUse
		}
	}
	s.mu.Lock()
	s.sum.State, s.sum.Since = Starting, time.Now()
	s.sum.Reason, s.sum.Stderr, s.sum.Login, s.sum.Waiting = "", nil, "", ""
	s.proc, s.stopping, s.loginNeeded, s.ready, s.inTurn, s.unseen, s.cancelRequested = nil, false, false, false, false, false, false
	s.transcript, s.tools = nil, map[string]int{} // the Agent replays the conversation
	s.mu.Unlock()
	m.cfg.Log.Info("session reopening", "session", id)
	s.publish()
	go s.run(true, "")
	return nil
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
		return // no log folder yet: nothing to sweep
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
	return Detail{Summary: s.sum, Transcript: append([]Item{}, s.transcript...)}, nil
}

// Prompt sends text as the next turn. Sending is only allowed while Idle,
// so a message never lands in a turn the user didn't write it for.
func (m *Manager) Prompt(id, text string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	if err := s.beginTurn(Idle); err != nil {
		return err
	}
	go s.prompt(text)
	return nil
}

// beginTurn moves the Session from state from to Working, checked and set
// under one lock, so two sends at once can't both start a turn.
func (s *session) beginTurn(from string) error {
	s.mu.Lock()
	state := s.sum.State
	if from == Idle && state == Waiting && s.sum.Waiting == WaitingTurn {
		state = Idle // writing the next message means you've looked
	}
	switch state {
	case from:
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
	s.setAsking(ctx, proc, acpSess)
	s.mu.Lock()
	s.acpID, s.ready = sid, true
	if prompt == "" {
		s.deriveLocked() // Idle; with a prompt, beginTurn goes straight to Working
	}
	s.mu.Unlock()
	s.m.cfg.Log.Info("session ready", "session", s.sum.ID)
	s.m.persist()
	if prompt == "" {
		s.publish()
		return
	}
	// Straight from Starting to Working: an Idle in between would let a
	// second message in before the starting prompt.
	if s.beginTurn(Starting) == nil {
		s.prompt(prompt)
	}
}

// setAsking puts the Agent in "ask before acting", or auto-approve, by
// mode and config option as its definition says (the environment part was
// set at launch). If that fails, the conversation says so instead of the
// user finding out from an unasked edit.
func (s *session) setAsking(ctx context.Context, proc *acp.Process, acpSess acp.Session) {
	a, auto := s.def.Asking, s.summary().AutoApprove
	if !a.Known() {
		s.addItem(Item{Type: "notice", Text: "fh doesn't know how to make this Agent ask before it acts, so it follows its own settings; auto-approve has no effect."})
		return
	}
	var problems []error
	if mode := a.Mode.Pick(auto); mode != "" {
		if !slices.Contains(acpSess.Modes, mode) {
			problems = append(problems, fmt.Errorf("mode %q not offered", mode))
		} else if err := proc.SetMode(ctx, acpSess.ID, mode); err != nil {
			problems = append(problems, err)
		}
	}
	if a.Option != "" {
		value := a.Value.Pick(auto)
		if !slices.Contains(acpSess.Options[a.Option], value) {
			problems = append(problems, fmt.Errorf("option %s=%s not offered", a.Option, value))
		} else if err := proc.SetOption(ctx, acpSess.ID, a.Option, value); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) == 0 {
		return
	}
	s.m.cfg.Log.Info("agent asking setup failed", "session", s.sum.ID, "err", errors.Join(problems...))
	text := "fh couldn't switch the Agent to asking before it acts, so it may edit files and run commands without asking."
	if auto {
		text = "fh couldn't switch the Agent to auto-approve, so it keeps its own setting."
	}
	s.addItem(Item{Type: "notice", Text: text})
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
			t.Content = contentText(tc.Content)
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
				t.Content = contentText(v)
			}
			if v, ok := tu.RawOutput.Value(); ok {
				t.RawOutput = rawText(v)
			}
		})
	case u.SessionInfoUpdate != nil:
		if title, ok := u.SessionInfoUpdate.Title.Value(); ok && title != "" {
			s.retitle(title)
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
		req.Content = contentText(cs)
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
	sum.CanReopen = s.canLoad && s.acpID != ""
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

// contentText shows a tool call's content as plain text: text as is, a diff
// as its path and every old line with "-", every new line with "+". It is
// not a minimal diff; nicer diffs may come with #45.
func contentText(cs []sdk.ToolCallContent) string {
	var parts []string
	for _, c := range cs {
		switch {
		case c.Content != nil && c.Content.Content.Text != nil:
			parts = append(parts, c.Content.Content.Text.Text)
		case c.Diff != nil:
			var b strings.Builder
			b.WriteString(c.Diff.Path + "\n")
			if c.Diff.OldText != nil {
				prefixLines(&b, "-", *c.Diff.OldText)
			}
			prefixLines(&b, "+", c.Diff.NewText)
			parts = append(parts, strings.TrimRight(b.String(), "\n"))
		}
	}
	return strings.Join(parts, "\n")
}

func prefixLines(b *strings.Builder, prefix, text string) {
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		b.WriteString(prefix + line + "\n")
	}
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
