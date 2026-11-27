package app

import (
	"net/http"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/switcher"
)

// registerSwitcherRoutes adds GET /api/switcher?project=<path>&q=<query>:
// the open Tickets of that Project the Switcher lists for the query (#195).
// It reads the same cached board as /api/board, so typing calls gh at most
// once per cache period, never once per key press. A board that can't be
// read lists no Tickets, and the Switcher shows Sessions only.
func registerSwitcherRoutes(srv *server.Server, projects *project.Registry, b *boards) {
	srv.Handle("GET /api/switcher", func(w http.ResponseWriter, r *http.Request) {
		p, ok := projects.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		tickets := []switcher.Ticket{}
		if board, read := b.read(p.Path, false); read {
			tickets = switcher.Tickets(r.URL.Query().Get("q"), board)
		}
		server.WriteJSON(w, http.StatusOK, struct {
			Tickets []switcher.Ticket `json:"tickets"`
		}{tickets})
	})
}
