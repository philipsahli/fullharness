package app_test

import (
	"os"
	"path/filepath"
	"strings"
)

// The CLIs a real Agent or the default turn-end classifier runs: the
// built-in Agent commands (internal/agent Defaults: npx for Claude Code's
// ACP adapter, copilot, opencode) and claude, which the adapter and the
// default classifier (`claude -p`) call. A test that doesn't configure a
// fake would start the real one, which spends the developer's tokens. That
// happened in an e2e test in #216 (#236).
var realAgentCLIs = []string{"npx", "claude", "copilot", "opencode"}

// agentGuard refuses to run, and logs the call (its name, then the folder
// it ran in) to guardLog, so a test can see what would have run.
const agentGuard = `#!/bin/sh
printf '%s\t%s\n' "$(basename "$0")" "$PWD" >>"$(dirname "$0")/calls.log"
echo "fh test: refused to run the real $(basename "$0"): give the test a fake Agent or classifier" >&2
exit 1
`

// guardDir is the folder TestMain puts first on PATH; guardLog is its log.
var guardDir, guardLog string

// installAgentGuard puts a guard first on PATH for every real Agent CLI,
// installed or not, so the tests behave the same on every machine: fh finds
// each CLI, and running it fails. Tests that check a missing CLI set their
// own PATH.
func installAgentGuard() (func(), error) {
	dir, err := os.MkdirTemp("", "fh-agentguard-")
	if err != nil {
		return nil, err
	}
	for _, name := range realAgentCLIs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(agentGuard), 0o755); err != nil {
			return nil, err
		}
	}
	guardDir, guardLog = dir, filepath.Join(dir, "calls.log")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		return nil, err
	}
	return func() { _ = os.RemoveAll(dir) }, nil // a leftover temp dir is harmless
}

// guardCalls are the guarded CLIs that ran in folder dir or below it.
func guardCalls(dir string) []string {
	data, _ := os.ReadFile(guardLog)
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		name, where, ok := strings.Cut(line, "\t")
		if real, err := filepath.EvalSymlinks(where); err == nil {
			where = real
		}
		if ok && (where == dir || strings.HasPrefix(where, dir+string(os.PathSeparator))) {
			out = append(out, name)
		}
	}
	return out
}
