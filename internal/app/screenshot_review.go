package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// prChanges caches each PR's changed files and description, which decide
// Screenshot review (ADR-0014), for ghCacheFor like the PR list: Autopilot
// looks at a reviewed Ticket on every interval.
type prChanges struct {
	mu sync.Mutex
	m  map[prKey]prChange
}

type prKey struct {
	repo string
	n    int
}

type prChange struct {
	pr tracker.PR
	at time.Time
}

func newPRChanges() *prChanges { return &prChanges{m: map[prKey]prChange{}} }

// read returns PR n's changes; ok is false when gh can't read them.
func (c *prChanges) read(ctx context.Context, repo string, n int) (tracker.PR, bool) {
	k := prKey{repo, n}
	c.mu.Lock()
	e, hit := c.m[k]
	c.mu.Unlock()
	if hit && time.Since(e.at) < ghCacheFor {
		return e.pr, true
	}
	pr, err := tracker.FetchPRChanges(ctx, repo, n)
	if err != nil {
		return tracker.PR{}, false
	}
	c.mu.Lock()
	c.m[k] = prChange{pr: pr, at: time.Now()}
	c.mu.Unlock()
	return pr, true
}

// screenshotDir is where an Agent saves its screenshots, in its Worktree
// (ADR-0014).
const screenshotDir = ".fh/screenshots"

// screenshotNote ends an implement prompt Autopilot sends: other Projects
// don't have fullharness's AGENTS.md, so the prompt says where the
// screenshots for Screenshot review go.
const screenshotNote = "\n\nIf you change a screen, save a PNG screenshot of each changed screen under " +
	screenshotDir + "/ in this Worktree before you open the PR; fh shows them to the user at Screenshot review. " +
	"If the PR changes no screen, say \"" + tracker.NoVisibleUIChange + "\" in its description."

// excludeFh adds .fh/ to repo's .git/info/exclude, which its Worktrees
// share, so screenshots are never committed. It runs before Autopilot
// starts a Session; a failure is logged and the Session starts anyway.
func excludeFh(repo string) error {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return err
	}
	file := filepath.Join(strings.TrimSpace(string(out)), "info", "exclude")
	b, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if slices.Contains(strings.Split(string(b), "\n"), ".fh/") {
		return nil
	}
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		b = append(b, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(b, ".fh/\n"...), 0o644)
}

// openPR is t's Open or Draft PR, the newest; ok is false when it has none.
func openPR(t tracker.Ticket) (tracker.PRRecord, bool) {
	for _, p := range t.PRs { // newest first
		if p.State == tracker.PROpen || p.State == tracker.PRDraft {
			return p, true
		}
	}
	return tracker.PRRecord{}, false
}

// screenReview reports whether Ticket t's PR, reviewed done, stops at
// Screenshot review in Project p; ok is false when fh can't tell yet (no
// open PR, or gh failed), so Autopilot records nothing and looks again.
func screenReview(ctx context.Context, c *prChanges, p project.Project, t tracker.Ticket) (stops, ok bool) {
	rec, ok := openPR(t)
	if !ok {
		return false, false
	}
	pr, ok := c.read(ctx, p.Path, rec.Number)
	if !ok {
		return false, false
	}
	return pr.ChangesScreen(p.ScreenPaths()), true
}
