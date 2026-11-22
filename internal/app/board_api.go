package app

import (
	"net/http"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/tracker"
	"github.com/philipsahli/fullharness/internal/worktree"
)

// registerBoardRoutes adds GET /api/board?project=<path>. It reads the
// tracker on every call: local markdown is cheap to read, and a tab asks
// only while it shows the board.
func registerBoardRoutes(srv *server.Server, projects *project.Registry) {
	srv.Handle("GET /api/board", func(w http.ResponseWriter, r *http.Request) {
		p, ok := projects.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		server.WriteJSON(w, http.StatusOK, readBoard(p.Path))
	})
}

func readBoard(repo string) tracker.Board {
	kind, name := tracker.Detect(repo)
	b := tracker.Board{Tracker: kind, Features: []tracker.Feature{}}
	switch kind {
	case tracker.KindLocal:
		b.Features = tracker.ReadLocal(repo, worktree.BranchesAhead(repo))
	case tracker.KindGitHub:
		b.Note = "GitHub boards arrive with #12."
	case tracker.KindUnsupported:
		b.Note = name + " is not supported in v1. fh reads GitHub Issues and local markdown."
	case tracker.KindNone:
		b.Note = "No Issue tracker set up: add docs/agents/issue-tracker.md, or run /setup-matt-pocock-skills in a Session."
	}
	return b
}
