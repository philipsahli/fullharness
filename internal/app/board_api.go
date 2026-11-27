package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
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
		refresh := r.URL.Query().Get("refresh") == "1"
		if refresh {
			// A board with no GitHub tracker reads no PRs below, yet its
			// Worktrees' PRs should be re-read too.
			b.expire(p.Path)
		}
		board, read := b.read(p.Path, refresh)
		if refresh {
			// The Session header shows the new PRs through a projects
			// event, without waiting for the next Refresh.
			projects.FetchPRs(p.Path)
		}
		tracker.Derive(board.Features, sessions.Links(p.Path))
		// p.URL is set only for a github.com origin, so a GitHub tracker
		// on another host gets no links (#101).
		if g, ok := gitremote.ParseGitHub(p.URL); ok && board.Tracker == tracker.KindGitHub {
			tracker.LinkIssues(&board, g)
		}
		server.WriteJSON(w, http.StatusOK, withIdeas(p.Path, board, read, sessions))
	})
}

// registerTrackerWrites adds one POST endpoint per write fh runs itself
// (ADR 0008), each taking {"project", "ref", …}, with ref a Spec's or
// Ticket's Ref from the board. After a write the repo's cached gh read is
// expired and its PRs fetched again, so the board read the UI makes next
// shows the change. A failed write answers with gh's own message.
func registerTrackerWrites(srv *server.Server, projects *project.Registry, b *boards) {
	srv.Handle("POST /api/tracker/mark-ready", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project string `json:"project"`
			Ref     string `json:"ref"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"project": "…", "ref": "…"}`)
			return
		}
		p, ok := projects.Get(req.Project)
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		err := tracker.MarkReady(r.Context(), p.Path, req.Ref)
		switch {
		case errors.Is(err, tracker.ErrBadRef) || errors.Is(err, tracker.ErrNoWrites):
			server.WriteError(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			// gh (or the file write) failed: the message is its own.
			server.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		b.expire(p.Path)
		projects.FetchPRs(p.Path)
		w.WriteHeader(http.StatusNoContent)
	})

	// Move a Feature to another Roadmap group (#215, ADR 0005): {"project",
	// "ref", "group"}, with ref the Spec's Ref. The group it leaves is the
	// one the board last read, so on GitHub only that label is removed.
	srv.Handle("POST /api/tracker/roadmap", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project string `json:"project"`
			Ref     string `json:"ref"`
			Group   string `json:"group"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"project": "…", "ref": "…", "group": "now|next|later"}`)
			return
		}
		p, ok := projects.Get(req.Project)
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		board, _ := b.read(p.Path, false)
		from, found := "", false
		for _, f := range board.Features {
			if f.Ref == req.Ref {
				from, found = f.Roadmap, true
				break
			}
		}
		err := tracker.ErrBadRef // only a Spec on the board has a Roadmap group
		if found {
			// A move the user made finishes even if the tab goes away
			// meanwhile (a reload, a closed tab); gh still has its timeout.
			err = tracker.SetRoadmap(context.WithoutCancel(r.Context()), p.Path, req.Ref, from, req.Group)
		}
		switch {
		case errors.Is(err, tracker.ErrBadRef) || errors.Is(err, tracker.ErrBadGroup) || errors.Is(err, tracker.ErrNoWrites):
			server.WriteError(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			server.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		b.expire(p.Path)
		w.WriteHeader(http.StatusNoContent)
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
	board = tracker.Board{Tracker: kind, Features: []tracker.Feature{}, Untriaged: []tracker.Untriaged{}}
	switch kind {
	case tracker.KindLocal:
		ok = true
		var prs []tracker.PR
		if tracker.HasGitHubRemote(repo) {
			// Without gh the board skips Review; the Tickets still show.
			if gr := b.gh(context.Background(), repo, false, refresh); gr.err == nil {
				prs = gr.prs
			}
		}
		board = tracker.ReadLocal(repo, worktree.BranchesAhead(repo), prs)
		if len(board.Features) == 0 {
			board.Note = "No Specs in .scratch yet: run /to-spec in a Session to write one."
		}
	case tracker.KindGitHub:
		gr := b.gh(context.Background(), repo, true, refresh)
		if gr.err != nil {
			board.Note = tracker.Note(gr.err)
			break
		}
		ok = true
		board = tracker.ReadGitHub(gr.issues, gr.prs, worktree.BranchesAhead(repo))
		if len(board.Features) == 0 {
			board.Note = "No Specs in GitHub yet: run /to-spec in a Session to write one."
		}
	case tracker.KindUnsupported:
		board.Note = name + " is not supported in v1. fh reads GitHub Issues and local markdown."
	case tracker.KindNone:
		board.Note = "No Issue tracker set up: add docs/agents/issue-tracker.md, or run /setup-matt-pocock-skills in a Session."
	}
	if n := len(board.Untriaged); n > 0 && len(board.Features) == 0 {
		// Open issues wait for triage, so writing a Spec isn't the next step.
		if n == 1 {
			board.Note = "No Specs yet; 1 issue needs triage"
		} else {
			board.Note = "No Specs yet; " + strconv.Itoa(n) + " issues need triage"
		}
	}
	return board, ok
}

// specs lists repo's Specs as its board has them now, for a new Idea's
// record; ok is false when the tracker can't be read.
func (b *boards) specs(repo string) (ids []string, ok bool) {
	board, ok := b.read(repo, false)
	return specIDs(board), ok
}

// prs is repo's PRs for its Worktrees, from the same cache as its board, so
// the two share one `gh pr list` per cache period; ok is false when gh
// failed. Ending ctx stops a running gh call.
func (b *boards) prs(ctx context.Context, repo string) (prs []tracker.PR, ok bool) {
	gr := b.gh(ctx, repo, false, false)
	return gr.prs, gr.err == nil
}

// entry is repo's cache entry, made on first use.
func (b *boards) entry(repo string) *ghEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.cache[repo]
	if !ok {
		e = &ghEntry{}
		b.cache[repo] = e
	}
	return e
}

// expire makes the next gh read of repo call gh.
func (b *boards) expire(repo string) {
	e := b.entry(repo)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.at = time.Time{}
}

// gh returns the repo's gh read, from the cache when it's fresh and has
// what's asked for.
func (b *boards) gh(ctx context.Context, repo string, withIssues, refresh bool) ghResult {
	e := b.entry(repo)
	e.mu.Lock()
	defer e.mu.Unlock()
	fresh := !e.at.IsZero() && time.Since(e.at) < ghCacheFor
	if fresh && !refresh && (e.withIssues || !withIssues || e.err != nil) {
		return e.ghResult
	}
	if fresh && !refresh {
		// The Worktree refresh read the PRs; the board adds the issues
		// without a second `gh pr list`. A failed issue read isn't kept,
		// so the Worktrees keep their PRs.
		r := e.ghResult
		r.withIssues = true
		if r.issues, r.err = tracker.FetchIssues(ctx, repo); r.err == nil {
			e.ghResult = r
		}
		return r
	}
	r := ghResult{at: time.Now(), withIssues: withIssues}
	if r.err = tracker.CheckGh(ctx, repo); r.err == nil && withIssues {
		r.issues, r.err = tracker.FetchIssues(ctx, repo)
	}
	if r.err == nil {
		r.prs, r.err = tracker.FetchPRs(ctx, repo)
	}
	e.ghResult = r
	return r
}
