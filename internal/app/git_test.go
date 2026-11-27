package app_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain isolates every git call, the test's and fh's own, from the
// developer's git config (signing, hooks, default branch).
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "fh test",
		"GIT_AUTHOR_EMAIL":    "fh-test@example.com",
		"GIT_COMMITTER_NAME":  "fh test",
		"GIT_COMMITTER_EMAIL": "fh-test@example.com",
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	bin, cleanup, err := buildFakeAgent()
	if err != nil {
		panic(err)
	}
	fakeAgentBin = bin
	ghCleanup, err := installGhDispatcher()
	if err != nil {
		panic(err)
	}
	guardCleanup, err := installAgentGuard()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	ghCleanup()
	guardCleanup()
	os.Exit(code)
}

// fakeAgentBin is internal/acp/fakeagent, built once for all tests.
var fakeAgentBin string

func buildFakeAgent() (string, func(), error) {
	dir, err := os.MkdirTemp("", "fh-fakeagent-")
	if err != nil {
		return "", nil, err
	}
	bin := filepath.Join(dir, "fakeagent")
	out, err := exec.Command("go", "build", "-o", bin, "github.com/philipsahli/fullharness/internal/acp/fakeagent").CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("build fake agent: %v\n%s", err, out)
	}
	return bin, func() { _ = os.RemoveAll(dir) }, nil // a leftover temp dir is harmless
}

// git runs git in dir and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo creates a git repo named name under parent, on branch main with
// one commit, and returns its path.
func newRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "README.md", "hello\n")
	return dir
}

// commitFile writes a file and commits it.
func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name), content)
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", "add "+name)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// realPath resolves symlinks, because macOS temp folders live under /var,
// a symlink to /private/var, and git reports the resolved path.
func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
