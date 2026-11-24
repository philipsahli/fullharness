package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
)

// Instruction tells the model what to answer. The Agent's message goes on
// stdin, so its length is never an argument-length problem.
const Instruction = `You read the last message a coding agent wrote at the end of its turn. Answer with exactly one word:
asking - it asks the user a question, or waits for their decision or approval;
done - it finished what it was asked and needs nothing from the user;
stuck - it failed, hit an error it can't get past, or says it can't continue.`

// DefaultName is what the replay set records DefaultCommand as.
const DefaultName = "claude -p claude-haiku-5-5"

// DefaultCommand is the default Provider (ADR-0010): Claude Code in print
// mode with Haiku, with no tools and no saved session.
var DefaultCommand = []string{
	"claude", "-p",
	"--model", "claude-haiku-5-5",
	"--output-format", "json",
	"--tools", "",
	"--no-session-persistence",
}

// Command is a Provider that runs a command with the message on stdin and
// --system-prompt Instruction appended, and reads `claude -p --output-format
// json` output from stdout. Appending the instruction keeps it the same for
// every configured command, so their replays compare.
type Command struct {
	name string
	args []string
}

// NewClaude returns the default Provider: DefaultCommand on the user's
// Claude login.
func NewClaude() *Command {
	return &Command{name: DefaultName, args: DefaultCommand}
}

// NewCommand returns a Provider that runs args; name is what the replay set
// records it as.
func NewCommand(name string, args []string) *Command {
	return &Command{name: name, args: args}
}

func (c *Command) Name() string { return c.name }

// claudeResult is the part of Claude Code's JSON result fh reads.
type claudeResult struct {
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens              int `json:"input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
}

func (c *Command) Classify(ctx context.Context, message string) (Answer, error) {
	if len(c.args) == 0 {
		return Answer{}, errors.New("no classifier command")
	}
	args := append(slices.Clone(c.args[1:]), "--system-prompt", Instruction)
	cmd := exec.CommandContext(ctx, c.args[0], args...)
	// Run outside every repo, so no project's CLAUDE.md or settings shape
	// the answer.
	cmd.Dir = os.TempDir()
	// Any claude, the default or a configured one, runs on the user's
	// login, as fh's Claude Code Agents do.
	if filepath.Base(c.args[0]) == "claude" {
		cmd.Env = agent.WithoutAPIKey(os.Environ())
	}
	// A child that keeps stdout open (an MCP server claude started) would
	// hold Run after the context ends; stop waiting for it.
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var r claudeResult
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		if runErr != nil {
			return Answer{}, fmt.Errorf("%s: %w: %s", c.name, runErr, strings.TrimSpace(stderr.String()))
		}
		return Answer{}, fmt.Errorf("%s: read its output: %w", c.name, err)
	}
	if r.IsError || runErr != nil {
		why := r.Result
		if !r.IsError || why == "" {
			why = fmt.Sprintf("%v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return Answer{}, fmt.Errorf("%s: %s", c.name, why)
	}
	u := r.Usage
	return Answer{
		Text: r.Result,
		// Cached input still counts as input the model read.
		InputTokens:  u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		OutputTokens: u.OutputTokens,
		CostUSD:      r.TotalCostUSD,
	}, nil
}
