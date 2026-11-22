package app

import (
	"net/http"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/tracker"
	"github.com/philipsahli/fullharness/internal/worktree"
)

// ghCacheFor is how long a Project's gh results are reused. The board view
// reads every 60 s; several tabs on one board share one gh call within
// this window, and Refresh skips it.
const ghCacheFor = 50 * time.Second

// registerBoardRoutes adds GET /api/board?project=<path>[&refresh=1].
// Local markdown is read fresh on every call; gh results are cached.
func registerBoardRoutes(srv *server.Server, projects *project.Registry) {
	b := &boards{cache: map[string]*ghEntry{}}
	srv.Handle("GET /api/board", func(w http.ResponseWriter, r *http.Request) {
		p, ok := projects.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		server.WriteJSON(w, http.StatusOK, b.read(p.Path, r.URL.Query().Get("refresh") == "1"))
	})
}

type boards struct {
	mu    sync.Mutex
	cache map[string]*ghEntry // repo → its gh reads
}

// ghEntry is one repository's cached gh read. Its own lock is held during
// the read, so tabs on the same board wait for one call, while other
// Projects' boards don't wait at all.
type ghEntry struct {
	mu sync.Mutex
	ghResult
}

// ghResult is one gh read of a repository.
type ghResult struct {
	at         time.Time
	withIssues bool // issues were read (GitHub boards); local boards need PRs only
	issues     []tracker.Issue
	prs        []tracker.PR
	err        error
}

func (b *boards) read(repo string, refresh bool) tracker.Board {
	kind, name := tracker.Detect(repo)
	board := tracker.Board{Tracker: kind, Features: []tracker.Feature{}}
	switch kind {
	case tracker.KindLocal:
		var prBranches []string
		if tracker.HasGitHubRemote(repo) {
			// Without gh the board skips Review; the Tickets still show.
			if gr := b.gh(repo, false, refresh); gr.err == nil {
				for _, p := range gr.prs {
					prBranches = append(prBranches, p.HeadRefName)
				}
			}
		}
		board.Features = tracker.ReadLocal(repo, worktree.BranchesAhead(repo), prBranches)
	case tracker.KindGitHub:
		gr := b.gh(repo, true, refresh)
		if gr.err != nil {
			board.Note = tracker.Note(gr.err)
			break
		}
		board.Features = tracker.ReadGitHub(gr.issues, gr.prs, worktree.BranchesAhead(repo))
	case tracker.KindUnsupported:
		board.Note = name + " is not supported in v1. fh reads GitHub Issues and local markdown."
	case tracker.KindNone:
		board.Note = "No Issue tracker set up: add docs/agents/issue-tracker.md, or run /setup-matt-pocock-skills in a Session."
	}
	return board
}

// gh returns the repo's gh read, from the cache when it's fresh and has
// what's asked for.
func (b *boards) gh(repo string, withIssues, refresh bool) ghResult {
	b.mu.Lock()
	e, ok := b.cache[repo]
	if !ok {
		e = &ghEntry{}
		b.cache[repo] = e
	}
	b.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	fresh := !e.at.IsZero() && time.Since(e.at) < ghCacheFor
	if fresh && !refresh && (e.withIssues || !withIssues || e.err != nil) {
		return e.ghResult
	}
	r := ghResult{at: time.Now(), withIssues: withIssues}
	if r.err = tracker.CheckGh(repo); r.err == nil && withIssues {
		r.issues, r.err = tracker.FetchIssues(repo)
	}
	if r.err == nil {
		r.prs, r.err = tracker.FetchPRs(repo)
	}
	e.ghResult = r
	return r
}
