package app

import (
	"net/http"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/autopilot/flow"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// registerAutopilotFlowRoute adds GET /api/autopilot/flow?project=&feature=:
// one Feature's flow graph (#206), worked out in Go like its Autopilot
// status (ADR-0013), so the UI only draws it. The UI reads it again when
// the Feature's Autopilot status changes.
func registerAutopilotFlowRoute(srv *server.Server, reg *project.Registry, b *boards, sessions *session.Manager, pilot *autopilot.Runner, statuses *autopilotStatuses) {
	srv.Handle("GET /api/autopilot/flow", func(w http.ResponseWriter, r *http.Request) {
		p, ok := reg.Get(r.URL.Query().Get("project"))
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		id := r.URL.Query().Get("feature")
		board, _ := b.read(p.Path, false)
		tracker.Derive(board.Features, sessions.Links(p.Path))
		in := flow.Input{Project: p.Path}
		found := false
		for _, f := range board.Features {
			if f.ID == id {
				in.Feature, found = f, true
			}
		}
		if !found {
			server.WriteError(w, http.StatusNotFound, "no such Feature")
			return
		}
		for _, h := range pilot.History(p.Path) {
			in.History = append(in.History, h.Events...)
		}
		for _, s := range statuses.List() {
			if s.Project == p.Path && s.Feature == id {
				in.Status = s
			}
		}
		server.WriteJSON(w, http.StatusOK, flow.Of(in))
	})
}
