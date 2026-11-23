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
	Say        string `json:"say,omitempty"`        // an agent message chunk
	SayMode    bool   `json:"sayMode,omitempty"`    // say the current session mode id
	SayEnv     string `json:"sayEnv,omitempty"`     // say "NAME=value" for an environment variable
	SayPID     bool   `json:"sayPid,omitempty"`     // say "pid=N"
	SayConfig  string `json:"sayConfig,omitempty"`  // say "<id>=<value>" for a config option
	Title      string `json:"title,omitempty"`      // a session_info_update with this title
	SwitchMode string `json:"switchMode,omitempty"` // change the mode and send current_mode_update
	Usage      *struct {
		Used     int64   `json:"used"`
		Size     int64   `json:"size"`
		Cost     float64 `json:"cost,omitempty"` // left out of the update when 0
		Currency string  `json:"currency,omitempty"`
	} `json:"usage,omitempty"` // a usage_update
	Commands   []string    `json:"commands,omitempty"`   // an available_commands_update with these names
	Tool       *toolAction `json:"tool,omitempty"`       // a tool_call
	ToolUpdate *toolAction `json:"toolUpdate,omitempty"` // a tool_call_update
	Permission *toolAction `json:"permission,omitempty"` // request permission, then say the chosen option
	WaitCancel bool        `json:"waitCancel,omitempty"` // block until session/cancel, then end with cancelled
	SleepMS    int         `json:"sleepMs,omitempty"`
	Stderr     string      `json:"stderr,omitempty"` // write a line to stderr
	Exit       *int        `json:"exit,omitempty"`   // exit the process with this code
	WriteFile  string      `json:"writeFile,omitempty"`
	ReadFile   string      `json:"readFile,omitempty"` // ask the client for fs/read_text_file of this path, which fh refuses

	PIDFile   string `json:"pidFile,omitempty"`   // write the process ID to this path
	ExitAfter int    `json:"exitAfter,omitempty"` // exit with code 3 this many ms later, without blocking
	AuthError bool   `json:"authError,omitempty"` // fail the request with ACP's "authentication required"
	Fail      string `json:"fail,omitempty"`      // fail the request with this message (an ordinary error)
	Stop      string `json:"stop,omitempty"`      // end the turn with this stop reason instead of end_turn
	// Stream sends Count chunks, PerSecond a second, each "t=<unix nanos>;"
	// so a test can measure how long fh takes to deliver them.
	Stream *struct {
		Count     int `json:"count"`
		PerSecond int `json:"perSecond"`
	} `json:"stream,omitempty"`
}

type toolAction struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Status    string `json:"status,omitempty"`
	Text      string `json:"text,omitempty"`      // text content
	RawOutput string `json:"rawOutput,omitempty"` // raw output, as a JSON string
	Command   string `json:"command,omitempty"`   // permission: rawInput.command
	Path      string `json:"path,omitempty"`      // permission: a location
	Always    bool   `json:"always,omitempty"`    // permission: also offer allow_always
	DiffNew   string `json:"diffNew,omitempty"`   // a diff of Path to this text
	DiffOld   string `json:"diffOld,omitempty"`   // from this text; none means a new file
}

// content is the action's text and diff content, either of which may be
// missing.
func (t *toolAction) content() []acp.ToolCallContent {
	var content []acp.ToolCallContent
	if t.Text != "" {
		content = append(content, acp.ToolContent(acp.TextBlock(t.Text)))
	}
	switch {
	case t.DiffNew != "" && t.DiffOld != "":
		content = append(content, acp.ToolDiffContent(t.Path, t.DiffNew, t.DiffOld))
	case t.DiffNew != "":
		content = append(content, acp.ToolDiffContent(t.Path, t.DiffNew))
	}
	return content
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
	// AllowAll offers Copilot's "allow_all" select config option (on/off).
	AllowAll bool `json:"allowAll"`
	// Models offers a "model" select config option (sonnet, opus), like
	// Claude Code's adapter.
	Models bool `json:"models"`
	// InitAuthError fails initialize with "authentication required".
	InitAuthError bool `json:"initAuthError"`
	// EndAfterCancel ends a cancelled turn with end_turn instead of
	// cancelled, like an Agent that doesn't follow ACP there.
	EndAfterCancel bool `json:"endAfterCancel"`
	// NoLoad leaves out the loadSession capability.
	NoLoad bool `json:"noLoad"`
	// History is replayed on session/load, as a real Agent replays a
	// Session's conversation: {"user": "..."} or {"agent": "..."}.
	History []map[string]string `json:"history"`
}

type agent struct {
	conn   *acp.AgentSideConnection
	script script
	mode   string
	cwd    string
	config map[string]string // config option values
}

func main() {
	var s script
	if raw := os.Getenv("FH_FAKE_SCRIPT"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent: bad FH_FAKE_SCRIPT:", err)
			os.Exit(2)
		}
	}
	// Start like a user's own permissive settings, which fh must override:
	// Claude Code's mode "auto" (the spike saw it), Copilot's allow_all on.
	a := &agent{script: s, mode: "auto", config: map[string]string{"allow_all": "on", "model": "sonnet"}}
	a.conn = acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	<-a.conn.Done()
}

func (a *agent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	if a.script.InitAuthError {
		return acp.InitializeResponse{}, acp.NewAuthRequired(nil)
	}
	return acp.InitializeResponse{
		ProtocolVersion:   acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{LoadSession: !a.script.NoLoad},
	}, nil
}

func (a *agent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.cwd = p.Cwd
	id := acp.SessionId("fake-session")
	if _, err := a.run(ctx, id, a.script.OnStart); err != nil {
		return acp.NewSessionResponse{}, err
	}
	modes := []acp.SessionMode{{Id: "default", Name: "Manual"}, {Id: "auto", Name: "Auto"}, {Id: "plan", Name: "Plan"}}
	if a.script.NoDefaultMode {
		modes = modes[1:]
	}
	resp := acp.NewSessionResponse{
		SessionId: id,
		Modes:     &acp.SessionModeState{CurrentModeId: acp.SessionModeId(a.mode), AvailableModes: modes},
	}
	resp.ConfigOptions = a.configOptions()
	return resp, nil
}

// configOptions are the select config options the script asks for, with
// their current values.
func (a *agent) configOptions() []acp.SessionConfigOption {
	sel := func(id, name string, category *acp.SessionConfigOptionCategory, values ...string) acp.SessionConfigOption {
		var opts acp.SessionConfigSelectOptionsUngrouped
		for _, v := range values {
			opts = append(opts, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(v), Name: v})
		}
		return acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
			Type: "select", Id: acp.SessionConfigId(id), Name: name, Category: category,
			CurrentValue: acp.SessionConfigValueId(a.config[id]),
			Options:      acp.SessionConfigSelectOptions{Ungrouped: &opts},
		}}
	}
	var out []acp.SessionConfigOption
	if a.script.AllowAll {
		// Starts "on", like a user whose own Copilot setting allows all:
		// fh has to switch it off explicitly.
		out = append(out, sel("allow_all", "Allow All", nil, "on", "off"))
	}
	if a.script.Models {
		model := acp.SessionConfigOptionCategoryModel
		out = append(out, sel("model", "Model", &model, "sonnet", "opus"))
	}
	return out
}

// LoadSession replays the scripted history, then answers like session/new.
// It only knows the ID NewSession hands out, like a real Agent only knows
// its own Sessions.
func (a *agent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if p.SessionId != "fake-session" {
		return acp.LoadSessionResponse{}, acp.NewInvalidParams(map[string]any{"error": "unknown session " + string(p.SessionId)})
	}
	a.cwd = p.Cwd
	for _, h := range a.script.History {
		u := acp.UpdateAgentMessageText(h["agent"])
		if text, ok := h["user"]; ok {
			u = acp.UpdateUserMessageText(text)
		}
		if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: p.SessionId, Update: u}); err != nil {
			return acp.LoadSessionResponse{}, err
		}
	}
	resp, err := a.NewSession(ctx, acp.NewSessionRequest{Cwd: p.Cwd})
	return acp.LoadSessionResponse{Modes: resp.Modes, ConfigOptions: resp.ConfigOptions}, err
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
	if cancelled && !a.script.EndAfterCancel {
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	reason := acp.StopReasonEndTurn
	for _, st := range steps {
		if st.Stop != "" {
			reason = acp.StopReason(st.Stop)
		}
	}
	return acp.PromptResponse{StopReason: reason}, nil
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
		case s.SayConfig != "":
			err = update(acp.UpdateAgentMessageText(s.SayConfig + "=" + a.config[s.SayConfig]))
		case s.SayPID:
			err = update(acp.UpdateAgentMessageText(fmt.Sprintf("pid=%d", os.Getpid())))
		case s.Title != "":
			err = update(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{
				SessionUpdate: "session_info_update", Title: acp.PatchSet(s.Title),
			}})
		case s.SwitchMode != "":
			a.mode = s.SwitchMode
			err = update(acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{
				SessionUpdate: "current_mode_update", CurrentModeId: acp.SessionModeId(s.SwitchMode),
			}})
		case s.Usage != nil:
			u := &acp.SessionUsageUpdate{SessionUpdate: "usage_update", Used: s.Usage.Used, Size: s.Usage.Size}
			if s.Usage.Cost != 0 {
				u.Cost = &acp.Cost{Amount: s.Usage.Cost, Currency: s.Usage.Currency}
			}
			err = update(acp.SessionUpdate{UsageUpdate: u})
		case s.Commands != nil:
			var cmds []acp.AvailableCommand
			for _, name := range s.Commands {
				cmds = append(cmds, acp.AvailableCommand{Name: name, Description: "fake " + name})
			}
			err = update(acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
				SessionUpdate: "available_commands_update", AvailableCommands: cmds,
			}})
		case s.Tool != nil:
			t := s.Tool
			opts := []acp.ToolCallStartOpt{acp.WithStartKind(acp.ToolKind(t.Kind)), acp.WithStartStatus(acp.ToolCallStatus(t.Status))}
			if c := t.content(); len(c) > 0 {
				opts = append(opts, acp.WithStartContent(c))
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
			if c := t.content(); len(c) > 0 {
				opts = append(opts, acp.WithUpdateContent(c))
			}
			if t.RawOutput != "" {
				opts = append(opts, acp.WithUpdateRawOutput(t.RawOutput))
			}
			err = update(acp.UpdateToolCall(acp.ToolCallId(t.ID), opts...))
		case s.Permission != nil:
			t := s.Permission
			tc := acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(t.ID), Title: acp.PatchSet(t.Title), Kind: acp.PatchSet(acp.ToolKind(t.Kind))}
			if t.Command != "" {
				raw, _ := json.Marshal(map[string]string{"command": t.Command}) // a map of strings always encodes
				tc.RawInput = acp.PatchSet(json.RawMessage(raw))
			}
			if t.Path != "" {
				tc.Locations = acp.PatchSet([]acp.ToolCallLocation{{Path: t.Path}})
			}
			if content := t.content(); len(content) > 0 {
				tc.Content = acp.PatchSet(content)
			}
			resp, perr := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
				SessionId: sid,
				ToolCall:  tc,
				Options:   a.permissionOptions(t.Always),
			})
			chosen := "cancelled"
			switch {
			case perr != nil && ctx.Err() != nil:
				// session/cancel arrived before the answer: the request ends
				// cancelled, as ACP asks.
			case perr != nil:
				return false, perr
			case resp.Outcome.Selected != nil:
				chosen = string(resp.Outcome.Selected.OptionId)
			}
			// Report what it got even if the turn is being cancelled right
			// now: the test reads this, and a cancelled ctx would drop it.
			err = a.conn.SessionUpdate(context.WithoutCancel(ctx), acp.SessionNotification{
				SessionId: sid, Update: acp.UpdateAgentMessageText("permission=" + chosen),
			})
			if chosen == "cancelled" {
				// A client answers "cancelled" when it cancels the turn, and
				// session/cancel follows; end the turn as cancelled, as ACP
				// asks, rather than racing on.
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
				return true, nil
			}
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
		case s.AuthError:
			return false, acp.NewAuthRequired(nil)
		case s.Fail != "":
			return false, acp.NewInternalError(map[string]any{"error": s.Fail})
		case s.Stream != nil:
			tick := time.NewTicker(time.Second / time.Duration(s.Stream.PerSecond))
			for i := 0; i < s.Stream.Count && err == nil; i++ {
				select {
				case <-tick.C:
				case <-ctx.Done():
					tick.Stop()
					return true, nil
				}
				err = update(acp.UpdateAgentMessageText(fmt.Sprintf("t=%d;", time.Now().UnixNano())))
			}
			tick.Stop()
		case s.ExitAfter > 0:
			go func(ms int) {
				time.Sleep(time.Duration(ms) * time.Millisecond)
				os.Exit(3)
			}(s.ExitAfter)
		case s.PIDFile != "":
			err = os.WriteFile(s.PIDFile, []byte(fmt.Sprint(os.Getpid())), 0o644)
		case s.ReadFile != "":
			// fh offers no fs capability, so the request fails; carry on like
			// an Agent that falls back to its own tools.
			_, _ = a.conn.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: sid, Path: a.cwd + "/" + s.ReadFile})
		case s.WriteFile != "":
			err = os.WriteFile(a.cwd+"/"+s.WriteFile, []byte("written by fakeagent\n"), 0o644)
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func (a *agent) permissionOptions(always bool) []acp.PermissionOption {
	opts := []acp.PermissionOption{
		{OptionId: "allow-once", Name: "Yes", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "reject", Name: "No", Kind: acp.PermissionOptionKindRejectOnce},
	}
	if always {
		opts = append(opts, acp.PermissionOption{OptionId: "allow-always", Name: "Always", Kind: acp.PermissionOptionKindAllowAlways})
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
func (a *agent) SetSessionConfigOption(_ context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	if v := p.ValueId; v != nil {
		a.config[string(v.ConfigId)] = string(v.Value)
	}
	return acp.SetSessionConfigOptionResponse{ConfigOptions: a.configOptions()}, nil
}
func (a *agent) UnstableAuthStatus(context.Context, acp.UnstableAuthStatusRequest) (acp.UnstableAuthStatusResponse, error) {
	return acp.UnstableAuthStatusResponse{}, nil
}
