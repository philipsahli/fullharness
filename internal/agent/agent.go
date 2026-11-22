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
	// Mode is the session mode in which the Agent asks before acting; fh
	// sets it on every Session. Empty for an Agent without modes.
	Mode string
	// AutoMode is the mode New Session's auto-approve sets instead. fh never
	// sets a mode that skips asking altogether (bypassPermissions).
	AutoMode string
	// claude marks Claude Code, whose environment fh adjusts (see Environ).
	claude bool
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
		Mode:     "default",
		AutoMode: "auto",
		claude:   true,
	}}
}

// Resolve applies config.toml overrides to the built-in definitions. An
// override with a command replaces the built-in command and checks only
// that command; an unknown id adds a new Agent.
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
			d.requires = []requirement{{o.Command[0], ""}}
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

// Environ is the environment the Agent process gets: fh's own environment,
// for Claude Code without ANTHROPIC_API_KEY (so it uses the user's Claude
// login, not an API key's billing) and with CLAUDE_CODE_EXECUTABLE pointing
// at the user's claude, then the definition's Env on top.
func (d Definition) Environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		if d.claude && strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	if d.claude {
		if p, err := exec.LookPath("claude"); err == nil {
			env = append(env, "CLAUDE_CODE_EXECUTABLE="+p)
		}
	}
	for k, v := range d.Env {
		env = append(env, k+"="+v)
	}
	return env
}
