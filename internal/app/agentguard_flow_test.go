package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// No test may run a real Agent (#236). fh here has no [agents.*] table and
// the default classifier, as a test that forgets its fakes would. Each
// Agent's Session, started by Autopilot for a Ticket in Now or by the user,
// must reach the test guard instead of the real CLI. Without the guard,
// these Sessions would start the developer's own Claude Code, Copilot CLI
// or OpenCode.
func TestASessionWithoutAFakeAgentNeverRunsTheRealOne(t *testing.T) {
	// The CLI each built-in Agent runs first.
	runs := map[string]string{"claude": "npx", "copilot": "copilot", "opencode": "opencode"}
	for id, cli := range runs {
		t.Run(id+" by Autopilot", func(t *testing.T) {
			t.Parallel()
			opts := appOptions(t.TempDir())
			opts.AutopilotInterval = 50 * time.Millisecond
			f := startFhWith(t, opts)
			c := f.authorisedClient()
			repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
			scratch(t, repo, "login/spec.md", nowSpec("Login"))
			scratch(t, repo, "login/issues/01-form.md", ticket("01", "Login form", "ready-for-agent", "None"))
			f.addProject(c, repo)

			if status, body := f.post(c, "/api/projects/autopilot", map[string]any{"path": repo, "on": true, "agent": id}); status != http.StatusNoContent {
				t.Fatalf("autopilot on: status %d (%v)", status, body)
			}

			// Autopilot starts the Ticket in a new Worktree beside the repo.
			waitGuardCall(t, filepath.Dir(repo), cli)
		})
		t.Run(id+" by the user", func(t *testing.T) {
			t.Parallel()
			f := startFh(t, t.TempDir())
			c := f.authorisedClient()
			repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
			f.addProject(c, repo)

			f.post(c, "/api/sessions", map[string]any{"project": repo, "worktree": repo, "agent": id, "prompt": "go"})

			waitGuardCall(t, repo, cli)
		})
	}
}

// waitGuardCall waits until the guard logged cli running in dir or below,
// and fails if any other guarded CLI ran there.
func waitGuardCall(t *testing.T, dir, cli string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // only bounds the wait
	for time.Now().Before(deadline) {
		calls := guardCalls(dir)
		if len(calls) > 0 {
			for _, c := range calls {
				if c != cli {
					t.Errorf("ran %s, want only %s", c, cli)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(guardLog)
	t.Fatalf("no Session ran %s in %s; the guard logged:\n%s", cli, dir, log)
}
