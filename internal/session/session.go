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

	sdk "github.com/coder/acp-go-sdk"

	"github.com/philipsahli/fullharness/internal/acp"
	"github.com/philipsahli/fullharness/internal/agent"
)

// Session states (GLOSSARY.md). Waiting arrives with #7.
const (
	Starting = "starting"
	Working  = "working"
	Idle     = "idle"
	Exited   = "exited"
)

// Exited reasons covered in #4; #7 adds the rest.
const (
	ReasonCrashed = "Agent crashed"
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
	// Title is the Worktree's branch until the Agent names the Session.
	Title string `json:"title"`
	// Reason and Stderr explain an Exited Session.
	Reason string   `json:"reason,omitempty"`
	Stderr []string `json:"stderr,omitempty"`
}

// Item is one entry of a conversation.
type Item struct {
	Type string `json:"type"` // user, agent, tool or notice
	Text string `json:"text,omitempty"`
	Tool *Tool  `json:"tool,omitempty"`
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
)

// Config wires a Manager into fh.
type Config struct {
	LogDir    string          // holds logs/<session-id>.log
	Broadcast func(event any) // sends an event to every tab
	TurnEnded func()          // runs after each turn, e.g. a Worktree refresh
	Log       *slog.Logger
}

// Manager holds all Sessions.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions []*session
}

// New returns an empty Manager.
func New(cfg Config) *Manager { return &Manager{cfg: cfg} }

type session struct {
	m   *Manager
	def agent.Definition

	mu         sync.Mutex
	sum        Summary
	transcript []Item
	tools      map[string]int // tool call ID → transcript index
	proc       *acp.Process
	acpID      sdk.SessionId
	stopping   bool
	logPath    string
}

// Start creates a Session for worktree in project and launches its Agent.
// The Worktree must already exist; the caller checked it may be used.
// branch is the Worktree's branch, the Session's first title.
func (m *Manager) Start(def agent.Definition, project, worktree, branch, prompt string) (Summary, error) {
	m.mu.Lock()
	for _, s := range m.sessions {
		if s.summary().Worktree == worktree {
			m.mu.Unlock()
			return Summary{}, ErrWorktreeUse
		}
	}
	s := &session{
		m:     m,
		def:   def,
		sum:   Summary{ID: newID(), Project: project, Worktree: worktree, Agent: def.ID, State: Starting, Title: branch},
		tools: map[string]int{},
	}
	s.logPath = filepath.Join(m.cfg.LogDir, s.sum.ID+".log")
	m.sessions = append(m.sessions, s)
	m.mu.Unlock()

	m.cfg.Log.Info("session starting", "session", s.sum.ID, "agent", def.ID, "worktree", worktree)
	s.publish()
	go s.run(prompt)
	return s.summary(), nil
}

// UsedWorktree reports whether a Session uses the Worktree at path.
func (m *Manager) UsedWorktree(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.ContainsFunc(m.sessions, func(s *session) bool { return s.summary().Worktree == path })
}

// List returns every Session's summary, never nil.
func (m *Manager) List() []Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Summary{}
	for _, s := range m.sessions {
		out = append(out, s.summary())
	}
	return out
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
	s.mu.Lock()
	state := s.sum.State
	s.mu.Unlock()
	switch state {
	case Idle:
	case Exited:
		return ErrNotRunning
	default:
		return ErrBusy
	}
	go s.prompt(text)
	return nil
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
	if state != Working || proc == nil {
		return nil // nothing to cancel; the turn already ended
	}
	return proc.Cancel(context.Background(), sid)
}

// StopAll ends every Agent process, as when fh quits (ADR 0002).
func (m *Manager) StopAll() {
	m.mu.Lock()
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

	proc, err := acp.Start(s.def.Command, s.def.Environ(), s.summary().Worktree, logFile, s)
	if err != nil {
		s.exit(ReasonCrashed, []string{err.Error()})
		return
	}
	s.mu.Lock()
	s.proc = proc
	s.mu.Unlock()
	go s.watch(proc)

	ctx := context.Background()
	if err := proc.Initialize(ctx); err != nil {
		s.m.cfg.Log.Info("agent initialize failed", "session", s.sum.ID, "err", err)
		return // watch reports the crash
	}
	sid, modes, err := proc.NewSession(ctx, s.summary().Worktree)
	if err != nil {
		s.m.cfg.Log.Info("agent session/new failed", "session", s.sum.ID, "err", err)
		proc.Stop()
		return
	}
	// Claude Code otherwise keeps the user's own permission mode (the spike
	// saw "auto") and never asks, so nothing would reach the Inbox (ADR 0003).
	if slices.Contains(modes, "default") {
		if err := proc.SetMode(ctx, sid, "default"); err != nil {
			s.m.cfg.Log.Info("agent set_mode failed", "session", s.sum.ID, "err", err)
		}
	}
	s.mu.Lock()
	s.acpID = sid
	s.mu.Unlock()
	s.setState(Idle)
	s.m.cfg.Log.Info("session ready", "session", s.sum.ID)
	if prompt != "" {
		s.prompt(prompt)
	}
}

// watch turns an unexpected process exit into Exited "Agent crashed".
func (s *session) watch(proc *acp.Process) {
	<-proc.Done()
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		s.exit(ReasonFhQuit, nil)
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
	s.setState(Working)
	reason, err := proc.Prompt(context.Background(), sid, text)
	if err != nil {
		s.m.cfg.Log.Info("prompt failed", "session", s.sum.ID, "err", err)
		return // a dead process is reported by watch
	}
	if reason == sdk.StopReasonCancelled {
		s.addItem(Item{Type: "notice", Text: "Turn cancelled."})
	}
	s.setState(Idle)
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
			s.mu.Lock()
			s.sum.Title = title
			s.mu.Unlock()
			s.publish()
		}
	}
}

// Permission answers a permission request (acp.Handler). Until permission
// cards arrive (#5), fh rejects every request and says so in the
// conversation, so no turn hangs waiting for an answer nobody can give.
func (s *session) Permission(r sdk.RequestPermissionRequest) sdk.RequestPermissionResponse {
	title, _ := r.ToolCall.Title.Value()
	s.addItem(Item{Type: "notice", Text: fmt.Sprintf("The Agent asked to run %q. fh can't answer permission requests yet (#5), so this one was denied.", title)})
	for _, o := range r.Options {
		if o.Kind == sdk.PermissionOptionKindRejectOnce || o.Kind == sdk.PermissionOptionKindRejectAlways {
			return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{
				Selected: &sdk.RequestPermissionOutcomeSelected{OptionId: o.OptionId},
			}}
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{Cancelled: &sdk.RequestPermissionOutcomeCancelled{}}}
}

func (s *session) appendAgentText(text string) {
	s.mu.Lock()
	n := len(s.transcript)
	if n > 0 && s.transcript[n-1].Type == "agent" {
		s.transcript[n-1].Text += text
		id := s.sum.ID
		s.mu.Unlock()
		s.m.cfg.Broadcast(map[string]any{"type": "chunk", "session": id, "index": n - 1, "text": text})
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
	item, sid := s.transcript[i], s.sum.ID
	s.mu.Unlock()
	s.m.cfg.Broadcast(map[string]any{"type": "item", "session": sid, "index": i, "item": item})
}

func (s *session) addItem(it Item) {
	s.mu.Lock()
	s.transcript = append(s.transcript, it)
	i, sid := len(s.transcript)-1, s.sum.ID
	s.mu.Unlock()
	s.m.cfg.Broadcast(map[string]any{"type": "item", "session": sid, "index": i, "item": it})
}

func (s *session) setState(state string) {
	s.mu.Lock()
	if s.sum.State == Exited {
		s.mu.Unlock()
		return
	}
	s.sum.State = state
	s.mu.Unlock()
	s.publish()
}

func (s *session) exit(reason string, stderr []string) {
	s.mu.Lock()
	s.sum.State, s.sum.Reason, s.sum.Stderr = Exited, reason, stderr
	s.mu.Unlock()
	s.m.cfg.Log.Info("session exited", "session", s.sum.ID, "reason", reason)
	s.publish()
}

func (s *session) summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sum
}

func (s *session) publish() {
	s.m.cfg.Broadcast(map[string]any{"type": "session", "session": s.summary()})
}

func contentText(cs []sdk.ToolCallContent) string {
	var parts []string
	for _, c := range cs {
		if c.Content != nil && c.Content.Content.Text != nil {
			parts = append(parts, c.Content.Content.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
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
