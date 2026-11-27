package app_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The fake gh is found per repo, not per process, so tests that use it can
// run in parallel (#209). TestMain puts one dispatcher named gh first on
// PATH. fh runs gh inside the Project's repo or one of its Worktrees, and
// the dispatcher runs the fake that the repo's git config names (fhtest.gh,
// set by linkFakeGh). A repo with no fake gets an error, so a test never
// reaches the real gh by accident.
const ghDispatcher = `#!/bin/sh
fake=$(git config --get fhtest.gh 2>/dev/null)
if [ -n "$fake" ] && [ -x "$fake" ]; then
	exec "$fake" "$@"
fi
echo "fake gh: this repo has no fake gh (see newFakeGh and linkFakeGh)" >&2
exit 1
`

func installGhDispatcher() (func(), error) {
	dir, err := os.MkdirTemp("", "fh-gh-")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(ghDispatcher), 0o755); err != nil {
		return nil, err
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		return nil, err
	}
	return func() { _ = os.RemoveAll(dir) }, nil // a leftover temp dir is harmless
}

// fakeGhDirs holds each test's fake gh folder, made on first use, so
// newFakeGh and linkFakeGh can run in either order.
var fakeGhDirs sync.Map // test name → folder

func fakeGhDir(t *testing.T) string {
	t.Helper()
	if d, ok := fakeGhDirs.Load(t.Name()); ok {
		return d.(string)
	}
	fakeGhDirs.Store(t.Name(), t.TempDir())
	t.Cleanup(func() { fakeGhDirs.Delete(t.Name()) })
	d, _ := fakeGhDirs.Load(t.Name())
	return d.(string)
}

// linkFakeGh makes gh, run in repo or any of its Worktrees, run this test's
// fake gh (newFakeGh).
func linkFakeGh(t *testing.T, repo string) {
	t.Helper()
	git(t, repo, "config", "fhtest.gh", filepath.Join(fakeGhDir(t), "bin", "gh"))
}
