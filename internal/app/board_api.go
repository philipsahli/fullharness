package app

import (
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/gitremote"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
	"github.com/philipsahli/fullharness/internal/worktree"
)

// ghCacheFor is how long a Project's gh results are reused. The board view
// reads every 60 s; several tabs on one board share one gh call within
// this window, and Refresh skips it.
const ghCacheFor = 50 * time.Second

// boardView is a board with its Ideas, which only fh knows (ADR 0004).
type boardView struct {
	tracker.Board
	Ideas []ideaView `json:"ideas"`
}

// ideaView is an Idea not yet linked to a Spec, for the Idea column.
type ideaView struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	// Suggest lists the Specs that appeared since the Idea started and no
	// other Idea has, any of which its `/to-spec` may have written.
	Suggest []specRef `json:"suggest"`
}

type specRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Ref   string `json:"ref"`
}

// registerBoardRoutes adds GET /api/board?project=<path>[&refresh=1].
// Local markdown is read fresh on every call; gh results are cached.
// sessions gives the Session links, which make Tickets Implementing, and
// the Ideas.
func registerBoardRoutes(srv *server.Server, projects *project.Registry, b *boards, sessions *session.Manager) {
	srv.Handle("GET /api/board", func(w http.ResponseWriter, r *http.Request) {
		p, ok := projects.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		board, read := b.read(p.Path, r.URL.Query().Get("refresh") == "1")
		tracker.Derive(board.Features, sessions.Links(p.Path))
		// p.URL is set only for a github.com origin, so a GitHub tracker
		// on another host gets no links (#101).
		if g, ok := gitremote.ParseGitHub(p.URL); ok && board.Tracker == tracker.KindGitHub {
			tracker.LinkIssues(board.Features, g)
		}
		server.WriteJSON(w, http.StatusOK, withIdeas(p.Path, board, read, sessions))
	})
}

// withIdeas adds p's Ideas to its board, names each linked Spec's Idea, and
// suggests the Specs an unlinked Idea may have produced. read says the
// tracker was read; an Idea started while it couldn't be learns its known
// Specs from the first board that is.
func withIdeas(project string, board tracker.Board, read bool, sessions *session.Manager) boardView {
	v := boardView{Board: board, Ideas: []ideaView{}}
	ideas := sessions.Ideas(project)
	taken := map[string]bool{}
	for _, i := range ideas {
		if i.Spec != "" {
			taken[i.Spec] = true
			if j := slices.IndexFunc(v.Features, func(f tracker.Feature) bool { return f.ID == i.Spec }); j >= 0 {
				v.Features[j].Idea = i.Name
			}
		}
	}
	for _, i := range ideas {
		if i.Spec != "" {
			continue
		}
		iv := ideaView{Session: i.Session, Name: i.Name, Suggest: []specRef{}}
		switch {
		case !read:
		case !i.SpecsRead:
			_ = sessions.SetKnownSpecs(i.Session, specIDs(board)) // fails only for a Session closed meanwhile, whose Idea is gone
		default:
			for _, f := range v.Features {
				if !f.Closed && !taken[f.ID] && !slices.Contains(i.KnownSpecs, f.ID) {
					iv.Suggest = append(iv.Suggest, specRef{ID: f.ID, Title: f.Title, Ref: f.Ref})
				}
			}
		}
		v.Ideas = append(v.Ideas, iv)
	}
	return v
}

func specIDs(board tracker.Board) []string {
	ids := []string{}
	for _, f := range board.Features {
		ids = append(ids, f.ID)
	}
	return ids
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

// read reads repo's board; ok is false when its tracker couldn't be read:
// none, unsupported, or gh failed.
func (b *boards) read(repo string, refresh bool) (board tracker.Board, ok bool) {
	kind, name := tracker.Detect(repo)
	board = tracker.Board{Tracker: kind, Features: []tracker.Feature{}}
	switch kind {
	case tracker.KindLocal:
		ok = true
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
		if len(board.Features) == 0 {
			board.Note = "No Specs in .scratch yet: run /to-spec in a Session to write one."
		}
	case tracker.KindGitHub:
		gr := b.gh(repo, true, refresh)
		if gr.err != nil {
			board.Note = tracker.Note(gr.err)
			break
		}
		ok = true
		board.Features = tracker.ReadGitHub(gr.issues, gr.prs, worktree.BranchesAhead(repo))
		if len(board.Features) == 0 {
			board.Note = "No Specs in GitHub yet: run /to-spec in a Session to write one."
		}
	case tracker.KindUnsupported:
		board.Note = name + " is not supported in v1. fh reads GitHub Issues and local markdown."
	case tracker.KindNone:
		board.Note = "No Issue tracker set up: add docs/agents/issue-tracker.md, or run /setup-matt-pocock-skills in a Session."
	}
	return board, ok
}

// specs lists repo's Specs as its board has them now, for a new Idea's
// record; ok is false when the tracker can't be read.
func (b *boards) specs(repo string) (ids []string, ok bool) {
	board, ok := b.read(repo, false)
	return specIDs(board), ok
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
