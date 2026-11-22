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
	ReasonFhQuit  = "fh quit"
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
	State    string `json:"state"`
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
)

// Config wires a Manager into fh.
type Config struct {
	LogDir    string          // holds logs/<session-id>.log
	Broadcast func(event any) // sends an event to every tab
	TurnEnded func()          // runs after each turn, e.g. a Worktree refresh
	Log       *slog.Logger
	// StartTimeout bounds initialize and session/new. The first Claude Code
	// start can take a while: npx downloads the adapter.
	StartTimeout time.Duration
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
	proc        *acp.Process
	acpID       sdk.SessionId
	stopping    bool
	logPath     string
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
	s := &session{
		m:       m,
		def:     def,
		sum:     Summary{ID: newID(), Project: project, Worktree: worktree, Agent: def.ID, State: Starting, Title: branch, AutoApprove: autoApprove, Since: time.Now()},
		tools:   map[string]int{},
		pending: map[string]*pending{},
	}
	s.logPath = filepath.Join(m.cfg.LogDir, s.sum.ID+".log")
	m.sessions = append(m.sessions, s)
	m.mu.Unlock()

	m.cfg.Log.Info("session starting", "session", s.sum.ID, "agent", def.ID, "worktree", worktree)
	s.publish()
	go s.run(prompt)
	return s.summary(), nil
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

// Seen records that you looked at Session id.
func (m *Manager) Seen(id string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	s.seen()
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
	if from == Idle && state == Waiting && s.sum.Waiting == "turn" {
		state = Idle // writing the next message means you've looked
	}
	switch state {
	case from:
		s.inTurn, s.unseen = true, false
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
func (s *session) run(prompt string) {
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
			s.mu.Lock()
			s.loginNeeded = true
			s.mu.Unlock()
		}
		proc.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.m.cfg.StartTimeout)
	defer cancel()
	if err := proc.Initialize(ctx); err != nil {
		fail("initialize", err)
		return
	}
	acpSess, err := proc.NewSession(ctx, s.summary().Worktree)
	if err != nil {
		fail("session/new", err)
		return
	}
	sid := acpSess.ID
	s.setAsking(ctx, proc, acpSess)
	s.mu.Lock()
	s.acpID = sid
	if prompt == "" {
		s.ready = true
		s.deriveLocked()
	}
	s.mu.Unlock()
	s.m.cfg.Log.Info("session ready", "session", s.sum.ID)
	if prompt == "" {
		s.publish()
		return
	}
	s.mu.Lock()
	s.ready = true // Starting moves straight to Working in beginTurn
	s.mu.Unlock()
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
			s.mu.Lock()
			s.loginNeeded = true
			s.mu.Unlock()
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
			s.appendAgentText(t.Text)
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

func (s *session) appendAgentText(text string) {
	s.mu.Lock()
	n := len(s.transcript)
	if n > 0 && s.transcript[n-1].Type == "agent" {
		s.transcript[n-1].Text += text
		s.mu.Unlock()
		s.emit("chunk", n-1, "text", text)
		return
	}
	s.mu.Unlock()
	s.addItem(Item{Type: "agent", Text: text})
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
	s.unseen = !cancelled
	s.deriveLocked()
	s.mu.Unlock()
	s.publish()
}

// seen records that you looked at the Session (its conversation is open in
// a visible tab): a finished turn no longer waits.
func (s *session) seen() {
	s.mu.Lock()
	if !s.unseen {
		s.mu.Unlock()
		return
	}
	s.unseen = false
	s.deriveLocked()
	s.mu.Unlock()
	s.publish()
}

// deriveLocked sets the Session state from the facts, in priority order
// (GLOSSARY.md): Exited stays; not ready is Starting; an open permission
// request is Waiting; a running turn is Working; a finished turn nobody has
// seen is Waiting; else Idle. The caller holds s.mu.
func (s *session) deriveLocked() {
	if s.sum.State == Exited {
		return
	}
	state, waiting := Idle, ""
	switch {
	case !s.ready && !s.inTurn:
		state = Starting
	case len(s.pending) > 0:
		state, waiting = Waiting, "permission"
	case s.inTurn:
		state = Working
	case s.unseen:
		state, waiting = Waiting, "turn"
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
	return s.sum
}

// emit sends a conversation event: kind "item" (a new or replaced item at
// index) or "chunk" (text appended to the agent message at index).
func (s *session) emit(kind string, index int, key string, value any) {
	s.m.cfg.Broadcast(map[string]any{"type": kind, "session": s.sum.ID, "index": index, key: value})
}

func (s *session) publish() {
	s.m.cfg.Broadcast(map[string]any{"type": "session", "session": s.summary()})
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
	return errors.As(err, &re) && re.Code == -32000
}
