package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// registerStopPointRoutes adds what the user does at an Autopilot Stop
// point in the Inbox (#199, ADR-0014). Each action writes through the
// tracker write path (#192), records the action in the Autopilot history
// as the user's, and steps that Project at once instead of at the next
// tick.
func registerStopPointRoutes(srv *server.Server, reg *project.Registry, b *boards, sessions *session.Manager, pilot *autopilot.Runner, background func(func(context.Context))) {
	// Approve spec (#229): the Spec becomes ready-for-agent, so Autopilot's
	// next step on it starts.
	srv.Handle("POST /api/autopilot/approve", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project string `json:"project"`
			Feature string `json:"feature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" || req.Feature == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"project": "…", "feature": "…"}`)
			return
		}
		p, ok := reg.Get(req.Project)
		if !ok {
			server.WriteError(w, http.StatusNotFound, "no such Project")
			return
		}
		board, ok := b.read(p.Path, false)
		if !ok {
			server.WriteError(w, http.StatusBadGateway, board.Note)
			return
		}
		tracker.Derive(board.Features, sessions.Links(p.Path))
		var f *tracker.Feature
		for i := range board.Features {
			if board.Features[i].ID == req.Feature {
				f = &board.Features[i]
			}
		}
		next := tracker.FeatureStep(tracker.StageSpec)
		switch {
		case f == nil:
			server.WriteError(w, http.StatusNotFound, "no such Feature")
			return
		case next == nil || f.Closed || f.Stage != tracker.StageSpec || f.Approved:
			server.WriteError(w, http.StatusConflict, "this Spec doesn't wait at Approve spec")
			return
		}
		// The write runs to the end even if the tab goes away (#215): a
		// half-done Approve would leave the user guessing.
		err := tracker.MarkReady(context.WithoutCancel(r.Context()), p.Path, f.Ref)
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
		reg.FetchPRs(p.Path)
		pilot.Answered(autopilot.Step{Project: p.Path, Feature: f.ID, Skill: next.Skill}, autopilot.AnswerApprove)
		w.WriteHeader(http.StatusNoContent)
		for _, ap := range autopilotProjects(reg) {
			if ap.Path == p.Path {
				background(func(context.Context) { pilot.Step(ap, true) })
			}
		}
	})
}
