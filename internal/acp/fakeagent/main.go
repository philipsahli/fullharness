// Command fakeagent is an ACP Agent for fh's API tests. It speaks ACP over
// stdio through the SDK's agent side, like Claude Code's adapter does, and
// does whatever the test's script says. Tests run it in place of a real
// Agent by pointing an Agent definition's command at it (config.toml).
//
// The script is JSON in FH_FAKE_SCRIPT:
//
//	{"onStart": [actions], "prompts": {"<prompt text>": [actions]}, "default": [actions]}
//
// onStart runs on session/new. A prompt runs the actions listed for its
// text, else "default", then ends the turn with end_turn.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
)

// action is one step. Exactly one field is set.
type action struct {
	Say        string      `json:"say,omitempty"`        // an agent message chunk
	SayMode    bool        `json:"sayMode,omitempty"`    // say the current session mode id
	SayEnv     string      `json:"sayEnv,omitempty"`     // say "NAME=value" for an environment variable
	SayPID     bool        `json:"sayPid,omitempty"`     // say "pid=N"
	Title      string      `json:"title,omitempty"`      // a session_info_update with this title
	Tool       *toolAction `json:"tool,omitempty"`       // a tool_call
	ToolUpdate *toolAction `json:"toolUpdate,omitempty"` // a tool_call_update
	Permission *toolAction `json:"permission,omitempty"` // request permission, then say the chosen option
	WaitCancel bool        `json:"waitCancel,omitempty"` // block until session/cancel, then end with cancelled
	SleepMS    int         `json:"sleepMs,omitempty"`
	Stderr     string      `json:"stderr,omitempty"` // write a line to stderr
	Exit       *int        `json:"exit,omitempty"`   // exit the process with this code
	WriteFile  string      `json:"writeFile,omitempty"`
	PIDFile    string      `json:"pidFile,omitempty"` // write the process ID to this path
}

type toolAction struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Status    string `json:"status,omitempty"`
	Text      string `json:"text,omitempty"`      // text content
	RawOutput string `json:"rawOutput,omitempty"` // raw output, as a JSON string
}

type script struct {
	OnStart []action            `json:"onStart"`
	Prompts map[string][]action `json:"prompts"`
	Default []action            `json:"default"`
	// NoDefaultMode leaves "default" out of the offered modes, like an Agent
	// that can't be told to ask before acting.
	NoDefaultMode bool `json:"noDefaultMode"`
	// RejectAlwaysFirst lists "reject always" before "reject once" in
	// permission requests.
	RejectAlwaysFirst bool `json:"rejectAlwaysFirst"`
}

type agent struct {
	conn   *acp.AgentSideConnection
	script script
	mode   string
	cwd    string
}

func main() {
	var s script
	if raw := os.Getenv("FH_FAKE_SCRIPT"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent: bad FH_FAKE_SCRIPT:", err)
			os.Exit(2)
		}
	}
	a := &agent{script: s, mode: "auto"} // Claude Code starts in the user's mode; the spike saw auto
	a.conn = acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	<-a.conn.Done()
}

func (a *agent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}

func (a *agent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.cwd = p.Cwd
	id := acp.SessionId("fake-session")
	if _, err := a.run(ctx, id, a.script.OnStart); err != nil {
		return acp.NewSessionResponse{}, err
	}
	modes := []acp.SessionMode{{Id: "default", Name: "Manual"}, {Id: "auto", Name: "Auto"}}
	if a.script.NoDefaultMode {
		modes = modes[1:]
	}
	return acp.NewSessionResponse{
		SessionId: id,
		Modes:     &acp.SessionModeState{CurrentModeId: acp.SessionModeId(a.mode), AvailableModes: modes},
	}, nil
}

func (a *agent) SetSessionMode(ctx context.Context, p acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	a.mode = string(p.ModeId)
	return acp.SetSessionModeResponse{}, nil
}

func (a *agent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	var text strings.Builder
	for _, b := range p.Prompt {
		if b.Text != nil {
			text.WriteString(b.Text.Text)
		}
	}
	steps, ok := a.script.Prompts[text.String()]
	if !ok {
		steps = a.script.Default
	}
	cancelled, err := a.run(ctx, p.SessionId, steps)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	if cancelled {
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// run performs steps; it reports whether the turn was cancelled.
func (a *agent) run(ctx context.Context, sid acp.SessionId, steps []action) (bool, error) {
	update := func(u acp.SessionUpdate) error {
		return a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: u})
	}
	for _, s := range steps {
		var err error
		switch {
		case s.Say != "":
			err = update(acp.UpdateAgentMessageText(s.Say))
		case s.SayMode:
			err = update(acp.UpdateAgentMessageText("mode=" + a.mode))
		case s.SayEnv != "":
			err = update(acp.UpdateAgentMessageText(s.SayEnv + "=" + os.Getenv(s.SayEnv)))
		case s.SayPID:
			err = update(acp.UpdateAgentMessageText(fmt.Sprintf("pid=%d", os.Getpid())))
		case s.Title != "":
			err = update(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{
				SessionUpdate: "session_info_update", Title: acp.PatchSet(s.Title),
			}})
		case s.Tool != nil:
			t := s.Tool
			opts := []acp.ToolCallStartOpt{acp.WithStartKind(acp.ToolKind(t.Kind)), acp.WithStartStatus(acp.ToolCallStatus(t.Status))}
			if t.Text != "" {
				opts = append(opts, acp.WithStartContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(t.Text))}))
			}
			if t.RawOutput != "" {
				opts = append(opts, acp.WithStartRawOutput(t.RawOutput))
			}
			err = update(acp.StartToolCall(acp.ToolCallId(t.ID), t.Title, opts...))
		case s.ToolUpdate != nil:
			t := s.ToolUpdate
			var opts []acp.ToolCallUpdateOpt
			if t.Title != "" {
				opts = append(opts, acp.WithUpdateTitle(t.Title))
			}
			if t.Status != "" {
				opts = append(opts, acp.WithUpdateStatus(acp.ToolCallStatus(t.Status)))
			}
			if t.Text != "" {
				opts = append(opts, acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(t.Text))}))
			}
			if t.RawOutput != "" {
				opts = append(opts, acp.WithUpdateRawOutput(t.RawOutput))
			}
			err = update(acp.UpdateToolCall(acp.ToolCallId(t.ID), opts...))
		case s.Permission != nil:
			t := s.Permission
			resp, perr := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
				SessionId: sid,
				ToolCall:  acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(t.ID), Title: acp.PatchSet(t.Title)},
				Options:   a.permissionOptions(),
			})
			if perr != nil {
				return false, perr
			}
			chosen := "cancelled"
			if resp.Outcome.Selected != nil {
				chosen = string(resp.Outcome.Selected.OptionId)
			}
			err = update(acp.UpdateAgentMessageText("permission=" + chosen))
		case s.WaitCancel:
			<-ctx.Done()
			return true, nil
		case s.SleepMS > 0:
			select {
			case <-time.After(time.Duration(s.SleepMS) * time.Millisecond):
			case <-ctx.Done():
				return true, nil
			}
		case s.Stderr != "":
			fmt.Fprintln(os.Stderr, s.Stderr)
		case s.Exit != nil:
			os.Exit(*s.Exit)
		case s.PIDFile != "":
			err = os.WriteFile(s.PIDFile, []byte(fmt.Sprint(os.Getpid())), 0o644)
		case s.WriteFile != "":
			err = os.WriteFile(a.cwd+"/"+s.WriteFile, []byte("written by fakeagent\n"), 0o644)
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func (a *agent) permissionOptions() []acp.PermissionOption {
	opts := []acp.PermissionOption{
		{OptionId: "allow-once", Name: "Yes", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "reject", Name: "No", Kind: acp.PermissionOptionKindRejectOnce},
	}
	if a.script.RejectAlwaysFirst {
		always := acp.PermissionOption{OptionId: "reject-always", Name: "Never", Kind: acp.PermissionOptionKindRejectAlways}
		opts = append([]acp.PermissionOption{always}, opts...)
	}
	return opts
}

// The rest of the Agent interface is unused by fh in #4.

func (a *agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (a *agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (a *agent) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (a *agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (a *agent) DeleteSession(context.Context, acp.DeleteSessionRequest) (acp.DeleteSessionResponse, error) {
	return acp.DeleteSessionResponse{}, nil
}
func (a *agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}
func (a *agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}
func (a *agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
func (a *agent) UnstableAuthStatus(context.Context, acp.UnstableAuthStatusRequest) (acp.UnstableAuthStatusResponse, error) {
	return acp.UnstableAuthStatusResponse{}, nil
}
