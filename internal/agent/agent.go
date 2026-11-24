// Package agent defines the Agents fh can start: a built-in definition per
// supported Agent, overridable in config.toml, and the check that the CLIs a
// definition needs are installed.
package agent

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// ClaudeAdapter is the ACP adapter version the ACP spike validated. Bump it
// only after the release smoke test (#16) passes on the new version.
const ClaudeAdapter = "@agentclientprotocol/claude-agent-acp@0.109.1"

// Definition is how to start one Agent over ACP.
type Definition struct {
	ID      string
	Name    string
	Command []string
	// Env is added to the Agent's environment, after fh's own changes.
	Env map[string]string
	// requires lists the CLIs the command needs, each with how to install it.
	requires []requirement
	// Asking is how fh makes the Agent ask before it acts, and what
	// auto-approve changes. Without it the user's own Agent settings decide,
	// which can bypass the Inbox (ADR 0003).
	Asking Asking
	// Login is the terminal command that logs the user in, shown when the
	// Agent reports it needs one.
	Login string
	// claude marks Claude Code, whose environment fh adjusts (see Environ).
	claude bool
}

// Asking holds the per-Agent switches between "ask first" and auto-approve.
// Each is set on every Session, both ways, so the user's own setting (for
// example Claude Code's auto mode, or Copilot's allow-all) never carries
// over. fh never chooses a value that skips asking altogether, such as
// Claude Code's bypassPermissions. Only built-in Agents have one; for an
// Agent added in config.toml, fh can't tell how it asks.
type Asking struct {
	// Mode is a session/set_mode ID (Claude Code).
	Mode Pair[string]
	// Option names a session/set_config_option select and Value its value
	// (Copilot CLI's allow_all).
	Option string
	Value  Pair[string]
	// Env is added to the process environment (OpenCode's
	// OPENCODE_PERMISSION). It is undocumented; the release smoke test
	// planned in #16 is to catch it changing.
	Env Pair[map[string]string]
}

// Pair is a setting's value when asking first, and with auto-approve.
type Pair[T any] struct{ Ask, Auto T }

// Pick returns the value for autoApprove.
func (p Pair[T]) Pick(autoApprove bool) T {
	if autoApprove {
		return p.Auto
	}
	return p.Ask
}

// Known reports whether fh knows how to make the Agent ask.
func (a Asking) Known() bool {
	return a.Mode.Ask != "" || a.Option != "" || len(a.Env.Ask) > 0
}

type requirement struct{ cli, install string }

// Override is an [agents.<id>] table from config.toml.
type Override struct {
	Command []string          `toml:"command"`
	Env     map[string]string `toml:"env"`
}

// Defaults are the built-in definitions.
func Defaults() []Definition {
	return []Definition{{
		ID:      "claude",
		Name:    "Claude Code",
		Command: []string{"npx", "-y", ClaudeAdapter},
		requires: []requirement{
			{"claude", "install Claude Code: npm install -g @anthropic-ai/claude-code"},
			{"node", "install Node: brew install node"},
			{"npx", "install Node: brew install node"},
		},
		Asking: Asking{Mode: Pair[string]{Ask: "default", Auto: "auto"}},
		Login:  "claude auth login",
		claude: true,
	}, {
		ID:       "copilot",
		Name:     "Copilot CLI",
		Command:  []string{"copilot", "--acp", "--stdio"},
		requires: []requirement{{"copilot", "install Copilot CLI: brew install --cask copilot-cli"}},
		Asking:   Asking{Option: "allow_all", Value: Pair[string]{Ask: "off", Auto: "on"}},
		Login:    "copilot login",
	}, {
		ID:       "opencode",
		Name:     "OpenCode",
		Command:  []string{"opencode", "acp"},
		requires: []requirement{{"opencode", "install OpenCode: brew install opencode"}},
		Login:    "opencode auth login",
		Asking: Asking{Env: Pair[map[string]string]{
			Ask:  map[string]string{"OPENCODE_PERMISSION": `{"bash":"ask","edit":"ask"}`},
			Auto: map[string]string{"OPENCODE_PERMISSION": `{"bash":"allow","edit":"allow"}`},
		}},
	}}
}

// Resolve applies config.toml overrides to the built-in definitions. An
// override with a command replaces the built-in command and checks only
// that command (with the built-in install hint if it's the same CLI); an
// unknown id adds a new Agent. An override keeps the built-in's Asking.
func Resolve(overrides map[string]Override) []Definition {
	defs := Defaults()
	for id, o := range overrides {
		i := slices.IndexFunc(defs, func(d Definition) bool { return d.ID == id })
		if i < 0 {
			defs = append(defs, Definition{ID: id, Name: id})
			i = len(defs) - 1
		}
		d := &defs[i]
		if len(o.Command) > 0 {
			d.Command = o.Command
			req := requirement{cli: o.Command[0]}
			for _, r := range d.requires {
				if r.cli == req.cli {
					req.install = r.install
				}
			}
			d.requires = []requirement{req}
		}
		if len(o.Env) > 0 {
			d.Env = o.Env
		}
	}
	slices.SortStableFunc(defs, func(a, b Definition) int { return strings.Compare(a.ID, b.ID) })
	return defs
}

// Missing returns why the Agent can't start, with the install command, or ""
// when every CLI it needs is on PATH.
func (d Definition) Missing() string {
	var problems []string
	for _, r := range d.requires {
		if _, err := exec.LookPath(r.cli); err != nil {
			msg := fmt.Sprintf("`%s` not found", r.cli)
			if r.install != "" {
				msg += "; " + r.install
			}
			if !slices.Contains(problems, msg) {
				problems = append(problems, msg)
			}
		}
	}
	return strings.Join(problems, ". ")
}

// WithoutAPIKey returns env without ANTHROPIC_API_KEY, so a claude process
// uses the user's Claude login rather than billing an API key. It is never
// nil: a nil Env would make exec pass on fh's whole environment, key
// included.
func WithoutAPIKey(env []string) []string {
	out := []string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			out = append(out, kv)
		}
	}
	return out
}

// Environ is the environment the Agent process gets: fh's own environment,
// for Claude Code without ANTHROPIC_API_KEY (so it uses the user's Claude
// login, not an API key's billing) and with CLAUDE_CODE_EXECUTABLE pointing
// at the user's claude; then the asking variables for autoApprove; then the
// definition's Env, so the user's config wins.
func (d Definition) Environ(autoApprove bool) []string {
	env := os.Environ()
	if d.claude {
		env = WithoutAPIKey(env)
	}
	if d.claude {
		if p, err := exec.LookPath("claude"); err == nil {
			env = append(env, "CLAUDE_CODE_EXECUTABLE="+p)
		}
	}
	for k, v := range d.Asking.Env.Pick(autoApprove) {
		env = append(env, k+"="+v)
	}
	for k, v := range d.Env {
		env = append(env, k+"="+v)
	}
	return env
}
