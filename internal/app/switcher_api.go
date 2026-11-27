package app

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/gitremote"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/switcher"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// lookupTTL is how long a GitHub lookup's answer is kept: long enough that
// typing the same number again asks nothing, short enough that an issue
// closed meanwhile shows closed soon (Spec #201: "a few minutes").
const lookupTTL = 5 * time.Minute

// registerSwitcherRoutes adds GET /api/switcher?project=<path>&q=<query>:
// what the Switcher lists for the query besides Sessions, with project the
// one the board shows (Spec #201). A number finds open, closed and merged
// items in every Project; words find that Project's open Tickets
// (internal/switcher).
//
// Every board and PR list comes from the same cache as /api/board, read
// side by side, so typing calls gh at most once per Project per cache
// period, never once per key press. A board that can't be read adds no
// results; the others still do.
//
// With lookup=1, which the Switcher sends once typing stops, a GitHub
// Project whose board has nothing with that number asks GitHub for it
// (#255). Answers are kept for lookupTTL; a failed lookup adds nothing.
func registerSwitcherRoutes(srv *server.Server, projects *project.Registry, b *boards) {
	cache := switcher.NewCache(lookupTTL)
	srv.Handle("GET /api/switcher", func(w http.ResponseWriter, r *http.Request) {
		current, ok := projects.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		lookup := r.URL.Query().Get("lookup") == "1"
		list := projects.List()
		sources := make([]switcher.Source, len(list))
		var wg sync.WaitGroup
		for i, p := range list {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s := switcher.Source{Project: p.Path, Name: p.Name}
				g, onGitHub := gitremote.ParseGitHub(p.URL)
				if onGitHub {
					s.Repo = g.NameWithOwner()
				}
				board, read := b.read(p.Path, false)
				if !read {
					sources[i] = s
					return
				}
				if onGitHub && board.Tracker == tracker.KindGitHub {
					tracker.LinkIssues(&board, g)
				}
				s.Board = board
				// The board read above filled the same cache, so this is no
				// second gh call.
				if board.Tracker == tracker.KindGitHub || tracker.HasGitHubRemote(p.Path) {
					s.PRs, _ = b.prs(r.Context(), p.Path)
				}
				// A local-markdown board already holds its closed files.
				if lookup && board.Tracker == tracker.KindGitHub {
					s.Lookup = cache.Wrap(p.Path, githubLookup(r.Context(), p.Path))
				}
				sources[i] = s
			}()
		}
		wg.Wait()
		server.WriteJSON(w, http.StatusOK, struct {
			Results []switcher.Result `json:"results"`
		}{switcher.Resolve(r.URL.Query().Get("q"), current.Path, sources)})
	})
}

// githubLookup asks repo's GitHub repository for one number.
func githubLookup(ctx context.Context, repo string) switcher.Lookup {
	return func(n string) (switcher.Found, bool, error) {
		num, err := strconv.Atoi(n)
		if err != nil {
			return switcher.Found{}, false, err
		}
		v, ok, err := tracker.FetchNumbered(ctx, repo, num)
		if err != nil || !ok {
			return switcher.Found{}, ok, err
		}
		f := switcher.Found{PR: v.PullRequest != nil, Title: v.Title, URL: v.URL}
		switch {
		case v.State != "closed": // open: no State
		case v.PullRequest != nil && v.PullRequest.MergedAt != nil:
			f.State = switcher.StateMerged
		default:
			f.State = switcher.StateClosed
		}
		return f, true, nil
	}
}
