// Package acp runs an Agent process and talks ACP to it over stdio, through
// the SDK's client side. fh offers no client capabilities: no fs and no
// terminal (ADR 0003), so Agents use their own tools.
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"time"

	sdk "github.com/coder/acp-go-sdk"
)

// Handler receives what the Agent sends.
type Handler interface {
	// Update is one session/update notification.
	Update(sdk.SessionNotification)
	// Permission answers a session/request_permission. It may block until
	// the user answers; ctx ends if the Agent goes away first.
	Permission(context.Context, sdk.RequestPermissionRequest) sdk.RequestPermissionResponse
}

// Process is one running Agent with its ACP connection.
type Process struct {
	cmd  *exec.Cmd
	conn *sdk.ClientSideConnection
	done chan struct{}
	err  error // the process's exit error, set before done closes
}

// Start launches command in dir. The Agent's stderr goes to stderr; the
// SDK's own log lines (for example "connection closed") go to log.
func Start(command, env []string, dir string, stderr io.Writer, log *slog.Logger, h Handler) (*Process, error) {
	if len(command) == 0 {
		return nil, errors.New("empty Agent command")
	}
	// With Setpgid, Go reports a missing cmd.Dir as the command not
	// existing ("fork/exec /opt/homebrew/bin/npx: no such file or
	// directory"), which reads like an Agent crash (#62). Name the folder.
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("the Worktree's folder %s: %w", dir, err)
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = stderr
	// Its own process group, so Stop also ends what the command starts
	// (npx starts node, which starts claude).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command[0], err)
	}
	p := &Process{cmd: cmd, done: make(chan struct{})}
	p.conn = sdk.NewClientSideConnection(client{h}, stdin, stdout)
	p.conn.SetLogger(log)
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// ExitErr is the process's exit error; valid after Done closes.
func (p *Process) ExitErr() error { return p.err }

// Done closes when the process has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Stop ends the process group: SIGTERM, then SIGKILL after 3 s.
func (p *Process) Stop() {
	pgid := -p.cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM) // fails only if already gone
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(pgid, syscall.SIGKILL) // as above
		<-p.done
	}
}

// Initialize negotiates ACP v1 with empty client capabilities. It reports
// whether the Agent can reopen a Session (session/load).
func (p *Process) Initialize(ctx context.Context) (canLoad bool, err error) {
	resp, err := p.conn.Initialize(ctx, sdk.InitializeRequest{
		ProtocolVersion:    sdk.ProtocolVersionNumber,
		ClientCapabilities: sdk.ClientCapabilities{},
	})
	return resp.AgentCapabilities.LoadSession, err
}

// Session is a new ACP session: its ID, the session modes the Agent offers
// and the current one, and its select config options.
type Session struct {
	ID      sdk.SessionId
	Mode    string
	Modes   []Choice
	Options []Option
}

// Choice is a session mode, or one value of a config option.
type Choice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Option is a select config option, such as the model.
type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Category is ACP's hint of what it sets, e.g. "model" or "mode"; may
	// be empty.
	Category string   `json:"category,omitempty"`
	Value    string   `json:"value"`
	Values   []Choice `json:"values"`
}

// HasMode reports whether the Agent offers mode.
func (s Session) HasMode(mode string) bool {
	return slices.ContainsFunc(s.Modes, func(c Choice) bool { return c.ID == mode })
}

// HasValue reports whether config option id offers value.
func HasValue(options []Option, id, value string) bool {
	for _, o := range options {
		if o.ID == id {
			return slices.ContainsFunc(o.Values, func(c Choice) bool { return c.ID == value })
		}
	}
	return false
}

// NewSession creates an ACP session in cwd.
func (p *Process) NewSession(ctx context.Context, cwd string) (Session, error) {
	resp, err := p.conn.NewSession(ctx, sdk.NewSessionRequest{Cwd: cwd, McpServers: []sdk.McpServer{}})
	if err != nil {
		return Session{}, err
	}
	return newSessionInfo(resp.SessionId, resp.Modes, resp.ConfigOptions), nil
}

func newSessionInfo(id sdk.SessionId, modes *sdk.SessionModeState, options []sdk.SessionConfigOption) Session {
	s := Session{ID: id, Options: Options(options)}
	if modes != nil {
		s.Mode = string(modes.CurrentModeId)
		for _, m := range modes.AvailableModes {
			s.Modes = append(s.Modes, Choice{ID: string(m.Id), Name: m.Name})
		}
	}
	return s
}

// Options converts the Agent's config options. Only selects: ACP's boolean
// toggles have no use in fh yet. A grouped select's values are listed
// group after group.
func Options(options []sdk.SessionConfigOption) []Option {
	out := []Option{}
	for _, o := range options {
		sel := o.Select
		if sel == nil {
			continue
		}
		opt := Option{ID: string(sel.Id), Name: sel.Name, Value: string(sel.CurrentValue), Values: []Choice{}}
		if sel.Category != nil {
			opt.Category = string(*sel.Category)
		}
		var values []sdk.SessionConfigSelectOption
		switch {
		case sel.Options.Ungrouped != nil:
			values = *sel.Options.Ungrouped
		case sel.Options.Grouped != nil:
			for _, g := range *sel.Options.Grouped {
				values = append(values, g.Options...)
			}
		}
		for _, v := range values {
			opt.Values = append(opt.Values, Choice{ID: string(v.Value), Name: v.Name})
		}
		out = append(out, opt)
	}
	return out
}

// LoadSession reopens the ACP session sid in cwd. The Agent replays its
// conversation as session/update notifications before it answers.
func (p *Process) LoadSession(ctx context.Context, sid sdk.SessionId, cwd string) (Session, error) {
	resp, err := p.conn.LoadSession(ctx, sdk.LoadSessionRequest{SessionId: sid, Cwd: cwd, McpServers: []sdk.McpServer{}})
	if err != nil {
		return Session{}, err
	}
	return newSessionInfo(sid, resp.Modes, resp.ConfigOptions), nil
}

// SetOption sets a select config option. It returns every option as the
// Agent reports them afterwards: one change may change others, such as the
// effort levels a new model allows.
func (p *Process) SetOption(ctx context.Context, sid sdk.SessionId, id, value string) ([]Option, error) {
	resp, err := p.conn.SetSessionConfigOption(ctx, sdk.SetSessionConfigOptionRequest{ValueId: &sdk.SetSessionConfigOptionValueId{
		SessionId: sid, ConfigId: sdk.SessionConfigId(id), Value: sdk.SessionConfigValueId(value),
	}})
	if err != nil {
		return nil, err
	}
	return Options(resp.ConfigOptions), nil
}

// SetMode sets the session's mode.
func (p *Process) SetMode(ctx context.Context, sid sdk.SessionId, mode string) error {
	_, err := p.conn.SetSessionMode(ctx, sdk.SetSessionModeRequest{SessionId: sid, ModeId: sdk.SessionModeId(mode)})
	return err
}

// Prompt sends text and blocks until the turn ends.
func (p *Process) Prompt(ctx context.Context, sid sdk.SessionId, text string) (sdk.StopReason, error) {
	resp, err := p.conn.Prompt(ctx, sdk.PromptRequest{SessionId: sid, Prompt: []sdk.ContentBlock{sdk.TextBlock(text)}})
	return resp.StopReason, err
}

// Cancel asks the Agent to end the current turn; Prompt then returns
// "cancelled".
func (p *Process) Cancel(ctx context.Context, sid sdk.SessionId) error {
	return p.conn.Cancel(ctx, sdk.CancelNotification{SessionId: sid})
}

// client is fh's side of ACP. Everything but updates and permission
// requests is refused: fh offers no fs, terminal or elicitation.
type client struct{ h Handler }

var errUnsupported = errors.New("not supported by fh")

func (c client) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	c.h.Update(n)
	return nil
}

func (c client) RequestPermission(ctx context.Context, r sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	return c.h.Permission(ctx, r), nil
}

// Refused is optionally implemented by a Handler that wants to hear about
// requests fh refuses (fs, terminal, elicitation). The release smoke test
// uses it to catch an Agent that starts relying on them.
type Refused interface{ Refused(method string) }

func (c client) refuse(method string) error {
	if r, ok := c.h.(Refused); ok {
		r.Refused(method)
	}
	return errUnsupported
}

func (c client) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, c.refuse("fs/read_text_file")
}
func (c client) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, c.refuse("fs/write_text_file")
}
func (c client) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, c.refuse("terminal/create")
}
func (c client) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, c.refuse("terminal/kill")
}
func (c client) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, c.refuse("terminal/output")
}
func (c client) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, c.refuse("terminal/release")
}
func (c client) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, c.refuse("terminal/wait_for_exit")
}
func (c client) CreateElicitation(context.Context, sdk.CreateElicitationRequest) (sdk.CreateElicitationResponse, error) {
	return sdk.CreateElicitationResponse{}, c.refuse("elicitation/create")
}
func (c client) CompleteElicitation(context.Context, sdk.CompleteElicitationNotification) error {
	return c.refuse("elicitation/complete")
}
