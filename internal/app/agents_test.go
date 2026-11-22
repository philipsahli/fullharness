package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func (f *fh) agents(c *http.Client) map[string]string {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/agents")
	if err != nil {
		f.t.Fatal(err)
	}
	var list []struct{ ID, Missing string }
	if err := json.Unmarshal([]byte(readBody(f.t, resp)), &list); err != nil {
		f.t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range list {
		out[a.ID] = a.Missing
	}
	return out
}

func TestClaudeCodeIsMissingWithoutItsCLIsAndSaysHowToInstall(t *testing.T) {
	// PATH holds only git, so neither claude nor node is found.
	bin := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()

	missing := f.agents(c)["claude"]

	for _, want := range []string{"`claude` not found", "npm install -g @anthropic-ai/claude-code", "`node` not found", "brew install node"} {
		if !strings.Contains(missing, want) {
			t.Errorf("missing = %q, want it to contain %q", missing, want)
		}
	}
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	status, body := f.post(c, "/api/sessions", map[string]any{"project": repo, "worktree": repo, "agent": "claude"})
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "`claude` not found") {
		t.Errorf("start: status %d (%v), want 400 saying claude is missing", status, body)
	}
}

func TestAnOverriddenAgentCommandThatIsMissingIsReported(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, fmt.Sprintf("[agents.claude]\ncommand = [%q]\n", filepath.Join(dir, "no-such-agent")))
	f := startFhWith(t, appOptions(dir))

	if missing := f.agents(f.authorisedClient())["claude"]; !strings.Contains(missing, "no-such-agent") {
		t.Errorf("missing = %q, want it to name the command", missing)
	}
}

func TestFhLogIsPrivateAndHoldsNoTokenOrMessageText(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "secret reply text"}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "secret prompt text"})
	waitTurn(t, tab, id)
	f.stop()

	path := filepath.Join(f.opts.Dir, "fh.log")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("fh.log: %v %v, want mode 0600", err, info)
	}
	log, _ := os.ReadFile(path)
	token := f.launchURL().Query().Get("token")
	for _, secret := range []string{token, "secret prompt text", "secret reply text"} {
		if strings.Contains(string(log), secret) {
			t.Errorf("fh.log contains %q", secret)
		}
	}
	if !strings.Contains(string(log), "session ready") || !strings.Contains(string(log), id) {
		t.Errorf("fh.log has no line for Session %s:\n%s", id, log)
	}
}
