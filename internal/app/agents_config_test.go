package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// startFhWithAgents starts fh with config.toml [agents.<id>] tables, each
// pointing at the fake agent with its own script and extra env.
func startFhWithAgents(t *testing.T, agents map[string]script, env map[string]map[string]string) *fh {
	t.Helper()
	dir := t.TempDir()
	var cfg strings.Builder
	for id, s := range agents {
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&cfg, "[agents.%s]\ncommand = [%q]\n[agents.%s.env]\nFH_FAKE_SCRIPT = %q\n", id, fakeAgentBin, id, raw)
		for k, v := range env[id] {
			fmt.Fprintf(&cfg, "%s = %q\n", k, v)
		}
	}
	writeConfig(t, dir, cfg.String())
	return startFhWith(t, appOptions(dir))
}

// replyOf starts a Session with agent and prompt in a fresh repo and returns
// the Agent's last reply.
func replyOf(t *testing.T, f *fh, agentID string, autoApprove bool) string {
	t.Helper()
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": agentID, "prompt": "go", "autoApprove": autoApprove})
	waitTurn(t, tab, id)
	return lastAgentText(t, f, c, id)
}

func TestCopilotAndOpenCodeAreBuiltInAndSayWhatToInstall(t *testing.T) {
	bin := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin) // only git: no copilot, no opencode
	f := startFh(t, t.TempDir())

	agents := f.agents(f.authorisedClient())

	for id, want := range map[string][]string{
		"copilot":  {"`copilot` not found", "brew install --cask copilot-cli"},
		"opencode": {"`opencode` not found", "brew install opencode"},
	} {
		missing, listed := agents[id]
		if !listed {
			t.Errorf("Agent %s not listed", id)
			continue
		}
		for _, w := range want {
			if !strings.Contains(missing, w) {
				t.Errorf("%s missing = %q, want it to contain %q", id, missing, w)
			}
		}
	}
}

func TestCopilotAllowAllIsOffUnlessAutoApprove(t *testing.T) {
	copilot := script{"allowAll": true, "default": []any{map[string]any{"sayConfig": "allow_all"}}}

	for auto, want := range map[bool]string{false: "allow_all=off", true: "allow_all=on"} {
		f := startFhWithAgents(t, map[string]script{"copilot": copilot}, nil)
		if got := replyOf(t, f, "copilot", auto); got != want {
			t.Errorf("auto-approve %v: agent saw %q, want %q", auto, got, want)
		}
	}
}

func TestOpenCodeAsksForBashAndEditUnlessAutoApprove(t *testing.T) {
	opencode := script{"default": []any{map[string]any{"sayEnv": "OPENCODE_PERMISSION"}}}

	for auto, want := range map[bool]string{
		false: `OPENCODE_PERMISSION={"bash":"ask","edit":"ask"}`,
		true:  `OPENCODE_PERMISSION={"bash":"allow","edit":"allow"}`,
	} {
		f := startFhWithAgents(t, map[string]script{"opencode": opencode}, nil)
		if got := replyOf(t, f, "opencode", auto); got != want {
			t.Errorf("auto-approve %v: agent saw %q, want %q", auto, got, want)
		}
	}
}

func TestConfigCanAddAnAgentAndPassItsEnvironment(t *testing.T) {
	f := startFhWithAgents(t,
		map[string]script{"myagent": {"default": []any{map[string]any{"sayEnv": "MY_SETTING"}}}},
		map[string]map[string]string{"myagent": {"MY_SETTING": "42"}})

	if missing, listed := f.agents(f.authorisedClient())["myagent"]; !listed || missing != "" {
		t.Fatalf("myagent listed %v, missing %q; want listed and available", listed, missing)
	}
	if got := replyOf(t, f, "myagent", false); got != "MY_SETTING=42" {
		t.Errorf("agent saw %q, want MY_SETTING=42", got)
	}
}

func TestAnAgentFhCantMakeAskSaysSoInTheConversation(t *testing.T) {
	f := startFhWithAgents(t, map[string]script{"myagent": {}}, nil)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "myagent"})
	waitState(t, tab, id, "idle")

	items := f.transcript(c, id)
	if len(items) == 0 || items[0]["type"] != "notice" || !strings.Contains(items[0]["text"].(string), "its own settings") {
		t.Errorf("transcript = %v, want a notice that the Agent follows its own settings", items)
	}
}

func TestAnEnvOnlyOverrideKeepsTheBuiltInCommandAndInstallHint(t *testing.T) {
	bin := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	writeConfig(t, dir, "[agents.copilot.env]\nCOPILOT_SETTING = \"x\"\n")
	f := startFhWith(t, appOptions(dir))

	if missing := f.agents(f.authorisedClient())["copilot"]; !strings.Contains(missing, "brew install --cask copilot-cli") {
		t.Errorf("missing = %q, want the built-in install hint", missing)
	}
}

func TestAnOverrideWithTheSameCLIKeepsItsInstallHint(t *testing.T) {
	bin := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	writeConfig(t, dir, "[agents.opencode]\ncommand = [\"opencode\", \"acp\", \"--print-logs\"]\n")
	f := startFhWith(t, appOptions(dir))

	if missing := f.agents(f.authorisedClient())["opencode"]; !strings.Contains(missing, "brew install opencode") {
		t.Errorf("missing = %q, want the built-in install hint", missing)
	}
}
