package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

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
		p, f, ok := specAtApproval(w, reg, b, sessions, req.Project, req.Feature)
		if !ok {
			return
		}
		next := tracker.FeatureStep(tracker.StageSpec)
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
		stepAtOnce(reg, pilot, background, p.Path)
	})

	// Reject (#225): the user's note goes to the Spec, and the Spec goes
	// back to Autopilot for a revision with that note; when the revision
	// ends, the Spec waits at Approve spec again.
	srv.Handle("POST /api/autopilot/reject", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project string `json:"project"`
			Feature string `json:"feature"`
			Note    string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" || req.Feature == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"project": "…", "feature": "…", "note": "…"}`)
			return
		}
		note := strings.TrimSpace(req.Note)
		if note == "" {
			server.WriteError(w, http.StatusBadRequest, tracker.ErrEmptyNote.Error())
			return
		}
		p, f, ok := specAtApproval(w, reg, b, sessions, req.Project, req.Feature)
		if !ok {
			return
		}
		// As with Approve, the write runs to the end if the tab goes away.
		err := tracker.CommentSpec(context.WithoutCancel(r.Context()), p.Path, f.Ref, note)
		switch {
		case errors.Is(err, tracker.ErrBadRef) || errors.Is(err, tracker.ErrNoWrites):
			server.WriteError(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			server.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		b.expire(p.Path)
		pilot.Rejected(p.Path, f.ID, f.Ref, note)
		w.WriteHeader(http.StatusNoContent)
		stepAtOnce(reg, pilot, background, p.Path)
	})
}

// specAtApproval is Feature feature of Project project when its Spec waits
// at Approve spec. Otherwise it writes the error and ok is false.
func specAtApproval(w http.ResponseWriter, reg *project.Registry, b *boards, sessions *session.Manager, projectPath, feature string) (p project.Project, f tracker.Feature, ok bool) {
	p, ok = reg.Get(projectPath)
	if !ok {
		server.WriteError(w, http.StatusNotFound, "no such Project")
		return p, f, false
	}
	board, ok := b.read(p.Path, false)
	if !ok {
		server.WriteError(w, http.StatusBadGateway, board.Note)
		return p, f, false
	}
	tracker.Derive(board.Features, sessions.Links(p.Path))
	i := slices.IndexFunc(board.Features, func(f tracker.Feature) bool { return f.ID == feature })
	switch {
	case i < 0:
		server.WriteError(w, http.StatusNotFound, "no such Feature")
		return p, f, false
	case tracker.FeatureStep(tracker.StageSpec) == nil || board.Features[i].Closed ||
		board.Features[i].Stage != tracker.StageSpec || board.Features[i].Approved:
		server.WriteError(w, http.StatusConflict, "this Spec doesn't wait at Approve spec")
		return p, f, false
	}
	return p, board.Features[i], true
}

// stepAtOnce steps the Project in the background, if Autopilot is on for
// it, instead of waiting for the next tick (ADR-0014).
func stepAtOnce(reg *project.Registry, pilot *autopilot.Runner, background func(func(context.Context)), path string) {
	for _, ap := range autopilotProjects(reg) {
		if ap.Path == path {
			background(func(context.Context) { pilot.Step(ap, true) })
		}
	}
}
