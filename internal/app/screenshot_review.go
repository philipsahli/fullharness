package app

import (
	"context"
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
