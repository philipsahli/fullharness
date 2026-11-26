package main_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The smoke command is tested as a process, the way make smoke runs it, with
// each Agent's command pointed at internal/acp/fakeagent through -config
// (the same [agents.<id>] overrides config.toml takes).

var smokeBin, fakeAgentBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fh-smoke-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	smokeBin = filepath.Join(dir, "smoke")
	fakeAgentBin = filepath.Join(dir, "fakeagent")
	for bin, pkg := range map[string]string{
		smokeBin:     "github.com/philipsahli/fullharness/scripts/smoke",
		fakeAgentBin: "github.com/philipsahli/fullharness/internal/acp/fakeagent",
	} {
		if out, err := exec.Command("go", "build", "-o", bin, pkg).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", pkg, err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir) // a leftover temp dir is harmless
	os.Exit(code)
}

type script map[string]any

// wellBehaved acts like a real Agent that passes every check: it lists the
// scratch repo's skill, answers each prompt with the markers the checks look
// for, asks before running a command, and replays its history on reopen.
func wellBehaved() script {
	return script{
		"onStart": []any{map[string]any{"commands": []string{"fh-smoke-echo"}}},
		"prompts": map[string]any{
			"/fh-smoke-echo": []any{map[string]any{"say": "FH-SMOKE-ECHO-4412"}},
			// check 9's long turn, which runs until cancelled
			"Write the numbers from 1 to 2000 in English words, one per line. Do not use any tools.": []any{map[string]any{"say": "one"}, map[string]any{"waitCancel": true}},
		},
		"default":  []any{map[string]any{"say": "PONG-1"}, map[string]any{"permission": map[string]any{"id": "t1", "title": "touch fh-perm.txt", "kind": "execute", "command": "touch fh-perm.txt"}}},
		"history":  []map[string]string{{"user": "Reply with PONG-1"}, {"agent": "PONG-1"}},
		"allowAll": true, // Copilot's allow_all option, which the smoke test switches off
	}
}

// runSmoke runs the smoke command with each Agent in scripts replaced by the
// fake agent running that script. It returns the output and exit code.
func runSmoke(t *testing.T, scripts map[string]script, args ...string) (string, int) {
	t.Helper()
	var cfg strings.Builder
	for id, s := range scripts {
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&cfg, "[agents.%s]\ncommand = [%q]\nenv = { FH_FAKE_SCRIPT = %q }\n", id, fakeAgentBin, raw)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(cfg.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(smokeBin, append([]string{"-config", path}, args...)...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	case err != nil:
		t.Fatalf("run smoke: %v\n%s", err, out)
	}
	return string(out), 0
}

func allAgents(s script) map[string]script {
	return map[string]script{"claude": s, "copilot": s, "opencode": s}
}

func TestAllChecksPassForWellBehavedAgents(t *testing.T) {
	out, code := runSmoke(t, allAgents(wellBehaved()))
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "FAIL") {
		t.Fatalf("output has a FAIL:\n%s", out)
	}
	for _, check := range []string{"drive over ACP", "slash skills", "permission and turn end", "reopen", "no fs/terminal", "claude adapter", ".claude/skills/", "OPENCODE_PERMISSION", "cancel and resend"} {
		if !strings.Contains(out, check) {
			t.Errorf("output doesn't name check %q:\n%s", check, out)
		}
	}
}

func TestFailsWhenAnAgentAsksForFileAccess(t *testing.T) {
	reader := wellBehaved()
	reader["default"] = append([]any{map[string]any{"readFile": "main.go"}}, reader["default"].([]any)...)
	out, code := runSmoke(t, map[string]script{"copilot": reader}, "-agent", "copilot")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "5 FAIL no fs/terminal") || !strings.Contains(out, "fs/read_text_file") {
		t.Errorf("output doesn't fail check 5 naming fs/read_text_file:\n%s", out)
	}
}

func TestFailsWhenAnAgentCannotReopen(t *testing.T) {
	forgetful := wellBehaved()
	forgetful["noLoad"] = true
	out, code := runSmoke(t, map[string]script{"claude": forgetful}, "-agent", "claude")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "4 FAIL reopen") {
		t.Errorf("output doesn't fail check 4:\n%s", out)
	}
}

func TestFailsWhenOpenCodeDoesNotAsk(t *testing.T) {
	quiet := wellBehaved()
	quiet["default"] = []any{map[string]any{"say": "PONG-1"}} // runs the command without asking
	out, code := runSmoke(t, map[string]script{"opencode": quiet}, "-agent", "opencode")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, `8 FAIL OPENCODE_PERMISSION: started with OPENCODE_PERMISSION={"bash":"ask","edit":"ask"}; permission_requested=false`) {
		t.Errorf("output doesn't fail check 8 with the variable fh set:\n%s", out)
	}
}

func TestFailsWhenACancelledTurnDoesNotSayCancelled(t *testing.T) {
	// ACP says a cancelled turn MUST stop with "cancelled"; this Agent says
	// end_turn, which fh would show as a finished turn.
	stubborn := wellBehaved()
	stubborn["endAfterCancel"] = true
	out, code := runSmoke(t, map[string]script{"copilot": stubborn}, "-agent", "copilot")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, `9 FAIL cancel and resend: cancelled stop="end_turn"`) {
		t.Errorf("output doesn't fail check 9 with stop end_turn:\n%s", out)
	}
}
