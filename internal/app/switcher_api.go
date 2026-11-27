package app

import (
	"net/http"
	"sync"

	"github.com/philipsahli/fullharness/internal/gitremote"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/switcher"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// registerSwitcherRoutes adds GET /api/switcher?project=<path>&q=<query>:
// what the Switcher lists for the query besides Sessions, with project the
// one the board shows (Spec #201). A number finds open items in every
// Project; words find that Project's open Tickets (internal/switcher).
//
// Every board and PR list comes from the same cache as /api/board, read
// side by side, so typing calls gh at most once per Project per cache
// period, never once per key press. A board that can't be read adds no
// results; the others still do.
func registerSwitcherRoutes(srv *server.Server, projects *project.Registry, b *boards) {
	srv.Handle("GET /api/switcher", func(w http.ResponseWriter, r *http.Request) {
		current, ok := projects.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
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
				sources[i] = s
			}()
		}
		wg.Wait()
		server.WriteJSON(w, http.StatusOK, struct {
			Results []switcher.Result `json:"results"`
		}{switcher.Resolve(r.URL.Query().Get("q"), current.Path, sources)})
	})
}
